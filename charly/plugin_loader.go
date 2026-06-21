package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"cuelang.org/go/cue"

	"github.com/overthinkos/overthink/charly/internal/schemaconcat"
)

// validatePluginCandy validates a candy's `plugin:` block. The CUE schema already
// checks the capability + source FORMAT (#Plugin / #PluginCapability); this adds
// the Go runtime checks the schema cannot express:
//   - each capability is well-formed (<class>:<word> with a known class);
//   - for source:builtin, the provider is ACTUALLY registered (its init()
//     compiled it into charly) — a builtin plugin candy naming a provider charly
//     does not ship is a hard error, not a silent no-op.
//
// An out-of-tree source (github.com/…) is NOT connected here — that is the
// loader's job at deploy/check time; validate only confirms the declaration is
// well-formed (a load-TIMING property of out-of-process plugins, not a
// schema-handling distinction — see validateAuthoredPluginInput).
func validatePluginCandy(name string, p *CandyPluginDecl) []string {
	if p == nil {
		return nil
	}
	var issues []string
	source := p.Source
	if source == "" {
		source = "builtin"
	}
	if len(p.Providers) == 0 {
		issues = append(issues, fmt.Sprintf("candy %q: plugin block declares no providers", name))
	}
	for _, capStr := range p.Providers {
		class, word, ok := splitCapability(string(capStr))
		if !ok {
			issues = append(issues, fmt.Sprintf("candy %q: plugin capability %q is malformed (want <class>:<word>)", name, capStr))
			continue
		}
		if source == "builtin" {
			if _, ok := providerRegistry.resolve(class, word); !ok {
				issues = append(issues, fmt.Sprintf(
					"candy %q: plugin declares builtin %s:%s but no such provider is compiled into charly", name, class, word))
			}
		}
	}
	return issues
}

// compileBasePlusServed compiles charly's BASE schema concatenated with served
// plugin schema source (base ++ served) — the unified value a plugin's authored
// plugin_input validates against. servedCUE is the package-less, SELF-CONTAINED
// .cue body a plugin shipped over the Describe channel (for a builtin, its
// embedded schema; for an external, the gRPC schema_cue) — NEVER read from a candy
// schema/ dir. Same concatenation contract as the runtime sharedCueSchema (R3).
func compileBasePlusServed(servedCUE string) (cue.Value, error) {
	baseBody, _, err := schemaconcat.ConcatSchema(schemaFS, "schema", nil)
	if err != nil {
		return cue.Value{}, fmt.Errorf("base schema: %w", err)
	}
	v := cueSchemaCtx.CompileString(baseBody + "\n" + servedCUE)
	return v, v.Err()
}

// pluginSchemaSet is the process-wide compiled plugin schema: base ++ Σ(every
// loaded unit's self-contained schema). Each plugin (builtin at process start,
// external at connect) adds its served schema through registerPluginUnitSchema,
// which recompiles the unified value. validateAuthoredPluginInput reads from it.
type pluginSchemaSet struct {
	mu        sync.Mutex
	sources   []string
	inputDefs map[string]string // provKey → def
	unified   cue.Value
}

var pluginSchemas = &pluginSchemaSet{inputDefs: map[string]string{}}

// registerPluginUnitSchema is THE plugin schema load gate — byte-identical for a
// builtin (in-proc) and an external (out-of-proc) unit (the zero-distinction
// requirement). It rejects an empty schema, a schema that will not splice onto the
// base (base ++ plugin), and a declared input def the schema does not define. On
// success it commits the unit's schema into the process-wide set and recompiles
// base ++ Σ. (directive: a proper schema is evaluated every time a plugin loads.)
func registerPluginUnitSchema(name string, s PluginSchema) error {
	if strings.TrimSpace(s.CueSource) == "" {
		return fmt.Errorf("plugin %q served an EMPTY CUE schema (every plugin MUST ship its own schema)", name)
	}
	pluginSchemas.mu.Lock()
	defer pluginSchemas.mu.Unlock()
	merged := append(append([]string(nil), pluginSchemas.sources...), s.CueSource)
	v, err := compileBasePlusServed(strings.Join(merged, "\n"))
	if err != nil {
		return fmt.Errorf("plugin %q: schema does not splice onto the base (base ++ plugin): %w", name, err)
	}
	for key, def := range s.InputDefs {
		if d := v.LookupPath(cue.ParsePath(def)); d.Err() != nil {
			return fmt.Errorf("plugin %q: provides %s but its schema defines no %s: %w", name, key, def, d.Err())
		}
	}
	pluginSchemas.sources = merged
	for key, def := range s.InputDefs {
		pluginSchemas.inputDefs[key] = def
	}
	pluginSchemas.unified = v
	return nil
}

// validateAuthoredPluginInput is THE only plugin_input validator — schema-source
// agnostic (the def comes from the process-wide set the load gate fills, so a
// builtin and an external are validated identically). A missing def, an
// uncompilable input, or a failed constraint (e.g. the externalprobe marker's
// `& !=""`) is a hard error, never a silent runtime surprise.
//
//nolint:unparam // class is the provider-key dimension (InputDefs are keyed by provKey(class,word)); the verb runtime seam (runPluginVerb) is the only caller today — kind/deploy/step/builder plugin_inputs validate through this SAME function when their seams wire.
func validateAuthoredPluginInput(class ProviderClass, word string, inputJSON []byte) error {
	pluginSchemas.mu.Lock()
	def, ok := pluginSchemas.inputDefs[provKey(class, word)]
	unified := pluginSchemas.unified
	pluginSchemas.mu.Unlock()
	if !ok {
		return fmt.Errorf("plugin %s:%s: no input def registered (schema not loaded)", class, word)
	}
	d := unified.LookupPath(cue.ParsePath(def))
	if d.Err() != nil {
		return fmt.Errorf("plugin %s:%s: schema missing %s: %w", class, word, def, d.Err())
	}
	in := cueSchemaCtx.CompileBytes(inputJSON)
	if in.Err() != nil {
		return fmt.Errorf("plugin %s:%s: input: %w", class, word, in.Err())
	}
	if err := d.Unify(in).Validate(cue.Concrete(true)); err != nil {
		return fmt.Errorf("plugin %s:%s: plugin_input fails %s: %w", class, word, def, err)
	}
	return nil
}

// builtinGateOnce + loadBuiltinPluginUnits gate EVERY in-tree builtin plugin unit's
// schema at process start (directive: a schema is evaluated every time a plugin is
// loaded — a builtin is "loaded" at process start). It obtains each unit through
// InProcTransport — the builtin's Describe channel — so the schema reaches the
// SAME gate (registerPluginUnitSchema) an external's does. Builtin PROVIDERS are
// already registered at init() (RegisterBuiltinPluginUnit); this gates only their
// schemas. Idempotent (sync.Once); a broken builtin schema fails loudly here.
var (
	builtinGateOnce sync.Once
	builtinGateErr  error
)

func loadBuiltinPluginUnits() error {
	builtinGateOnce.Do(func() {
		for i := range builtinPluginUnits {
			unit, _, err := (&InProcTransport{Unit: &builtinPluginUnits[i]}).Connect(context.Background())
			if err != nil {
				builtinGateErr = err
				return
			}
			if err := registerPluginUnitSchema(builtinUnitName(unit), unit.Schema); err != nil {
				builtinGateErr = err
				return
			}
		}
	})
	return builtinGateErr
}

// builtinUnitName derives a stable error-message name for a builtin unit from its
// first provider capability (a unit has no separate name field — its candy does).
func builtinUnitName(u *PluginUnit) string {
	if len(u.Providers) > 0 {
		return provKey(u.Providers[0].Class(), u.Providers[0].Reserved())
	}
	return "<builtin>"
}

// safePluginBinName flattens a candy key (which may be an @github ref with
// slashes/colons) to a single filesystem-safe filename for the built binary.
func safePluginBinName(name string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-':
			return r
		default:
			return '_'
		}
	}, name)
}

// pluginBuildCacheDir is where built out-of-tree plugin binaries land.
func pluginBuildCacheDir() string {
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "charly", "plugins")
}

// buildPluginBinary go-builds an out-of-tree plugin's provider binary on the HOST
// (never in a venue — the host owns the toolchain; the built binary is delivered
// into a venue by the in-venue transport). srcDir is the plugin candy's resolved
// dir, which is its own Go module (go.mod + a main serving via plugin/sdk).
func buildPluginBinary(ctx context.Context, srcDir, name string) (string, error) {
	cacheDir := pluginBuildCacheDir()
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", fmt.Errorf("plugin %q: build cache: %w", name, err)
	}
	// The candy key may be an @github ref ("github.com/org/repo/candy/<name>") with
	// slashes; flatten it to ONE safe filename so `go build -o` lands a regular file
	// in cacheDir (a slash would imply non-existent nested dirs).
	bin := filepath.Join(cacheDir, safePluginBinName(name))
	cmd := exec.CommandContext(ctx, "go", "build", "-o", bin, ".")
	cmd.Dir = srcDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("plugin %q: go build in %s: %w\n%s", name, srcDir, err, out)
	}
	return bin, nil
}

// loadPluginUnit loads ONE out-of-tree plugin: build its provider binary on the
// host, connect over LocalTransport, run the SAME schema gate a builtin runs, then
// register its providers. The schema travels over the Describe channel (gRPC
// schema_cue) — the host never reads the candy's schema/ dir.
func loadPluginUnit(ctx context.Context, name string, p *CandyPluginDecl, srcDir string) error {
	if srcDir == "" {
		return fmt.Errorf("plugin %q (source %s): no resolved source dir to build from", name, p.Source)
	}
	bin, err := buildPluginBinary(ctx, srcDir, name)
	if err != nil {
		return err
	}
	unit, closer, err := (&LocalTransport{BinPath: bin}).Connect(ctx)
	if err != nil {
		return fmt.Errorf("plugin %q: connect: %w", name, err)
	}
	if err := registerPluginUnitSchema(name, unit.Schema); err != nil {
		_ = closer.Close()
		return err
	}
	if err := providerRegistry.RegisterPluginProviders(unit.Providers, p.Source, closer); err != nil {
		_ = closer.Close()
		return fmt.Errorf("plugin %q: register: %w", name, err)
	}
	return nil
}

// loadProjectPlugins gates every builtin plugin unit's schema (process-start pass)
// and connects every out-of-tree plugin candy in the RESOLVED candy set before
// checks/deploys reference its providers. It takes the scanned set
// (ScanAllCandyWithConfig) — which, unlike LoadUnified's project-local Candy map,
// includes @github-fetched candies and carries each candy's own .SourceDir +
// .Plugin (so a box that vendors all its candies via @github, like box/<distro>,
// still loads its plugins). Errors are returned (not swallowed) so a bed asserting
// a plugin verb fails loudly if its plugin won't load.
func loadProjectPlugins(ctx context.Context, candies map[string]*Candy) error {
	if err := loadBuiltinPluginUnits(); err != nil {
		return fmt.Errorf("builtin plugin schema gate: %w", err)
	}
	for name, candy := range candies {
		if candy == nil || candy.Plugin == nil {
			continue
		}
		// Builtins are gated above (their schemas) and registered at init() (their
		// providers); only out-of-tree sources need build + connect + register.
		if src := candy.Plugin.Source; src == "" || src == "builtin" {
			continue
		}
		if err := loadPluginUnit(ctx, name, candy.Plugin, candy.SourceDir); err != nil {
			return err
		}
	}
	return nil
}
