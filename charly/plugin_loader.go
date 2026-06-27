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
	// An OUT-OF-PROCESS plugin binary builds STANDALONE in the candy's own module
	// (its go.mod + `replace …/charly => ../../charly`), NEVER in the repo
	// workspace: set GOWORK=off so a repo-root go.work — which lists only the
	// COMPILED-IN plugin candies (the `compiled_plugins:` selection) — cannot
	// reject a non-member candy dir ("current directory is contained in a module
	// that is not one of the workspace modules listed in go.work"). The dual
	// placement (compiled-in vs out-of-process) is exactly why the out-of-process
	// build must ignore the workspace.
	//
	// The serve shim lives conventionally at ./cmd/serve (the importable provider
	// package sits at the candy root for the in-proc placement; the shim wraps it
	// for the out-of-process one). Fall back to the candy root for a candy that has
	// not yet adopted the shim layout.
	target := "."
	if st, statErr := os.Stat(filepath.Join(srcDir, "cmd", "serve")); statErr == nil && st.IsDir() {
		target = "./cmd/serve"
	}
	cmd := exec.CommandContext(ctx, "go", "build", "-o", bin, target)
	cmd.Dir = srcDir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("plugin %q: go build in %s: %w\n%s", name, srcDir, err, out)
	}
	return bin, nil
}

// bakedPluginDir is the FHS system path a candy's `bake_plugin:` step copies a
// pre-built provider binary to at image-build time, so a DEPLOYED container (which has
// neither the candy source nor a go toolchain) can run an external plugin its
// in-container charly needs at runtime — e.g. the charly-mcp service's `charly mcp
// serve`. `CHARLY_PLUGIN_DIR` overrides it (tests, non-FHS layouts).
const bakedPluginDir = "/usr/lib/charly/plugins"

// bakedPluginFileName is the filename a baked plugin binary takes under bakedPluginDir.
// It keys by the plugin candy's LEAF name (the last path segment) — STABLE across how the
// candy is referenced: bare `plugin-mcp` in a local composition vs the qualified
// `github.com/org/repo/candy/plugin-mcp` scanned-set key under an @github composition. The
// build-side bake and the in-container loader resolve the candy under different keys
// (the build may see the @github ref while the in-container project sees it bare), so they
// agree ONLY on the leaf. Shared by emitBakedPlugins (bake) + bakedPluginBinary (load), R3.
func bakedPluginFileName(name string) string {
	return safePluginBinName(filepath.Base(name))
}

// bakedPluginDirs returns the directories baked plugin binaries (+ their .providers word
// manifests) live in: $CHARLY_PLUGIN_DIR (override) then the FHS bakedPluginDir.
func bakedPluginDirs() []string {
	dirs := []string{}
	if d := os.Getenv("CHARLY_PLUGIN_DIR"); d != "" {
		dirs = append(dirs, d)
	}
	return append(dirs, bakedPluginDir)
}

// bakedPluginBinary returns a pre-built provider binary for `name` if one was baked into
// the image (bake_plugin:), else "".
func bakedPluginBinary(name string) string {
	for _, d := range bakedPluginDirs() {
		p := filepath.Join(d, bakedPluginFileName(name))
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// bakedCommandBinaries maps a baked plugin's COMMAND word → its binary path, populated by
// discoverBakedPluginWords from the `.providers` manifests baked beside each binary. It lets
// dispatchExternalCommand (resolveCommandPluginBinary) fork/exec a baked command plugin in a
// deployed container that has NO candy source to scan (the charly-mcp service's `charly mcp
// serve` is the motivating case).
var bakedCommandBinaries = map[string]string{}

// discoverBakedPluginWords reads the `.providers` word manifests baked beside each plugin
// binary (bake_plugin:) and registers their declared external COMMAND words into the kong
// grammar (registerDeclaredExternalCommand) — CHEAPLY, WITHOUT connecting any plugin. It also
// records word → baked-binary in bakedCommandBinaries, so dispatchExternalCommand can
// syscall.Exec the baked binary directly (a deployed container has no project to scan). So the
// in-container charly recognizes `charly <word>` for a baked command. A NO-OP when no plugins
// are baked (the dev-host / from-source case): the dirs are absent or hold no `.providers`
// files, and every existing charly invocation is byte-for-byte unchanged.
func discoverBakedPluginWords() {
	for _, dir := range bakedPluginDirs() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".providers") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			binPath := filepath.Join(dir, strings.TrimSuffix(e.Name(), ".providers"))
			for _, line := range strings.Split(string(data), "\n") {
				class, word, ok := splitCapability(strings.TrimSpace(line))
				if !ok || class != ClassCommand {
					continue
				}
				registerDeclaredExternalCommand(word)
				bakedCommandBinaries[word] = binPath
			}
		}
	}
}

// resolvePluginBinary returns a plugin's provider binary: a BAKED binary (pre-built,
// baked into the image for a source/toolchain-less deployed container) if present, else
// built from the candy source on the host. The baked path is the enabler for running an
// external plugin INSIDE a deployed container.
func resolvePluginBinary(ctx context.Context, srcDir, name string) (string, error) {
	if baked := bakedPluginBinary(name); baked != "" {
		return baked, nil
	}
	if srcDir == "" {
		return "", fmt.Errorf("no baked binary (%s) and no source dir to build from", filepath.Join(bakedPluginDir, safePluginBinName(name)))
	}
	return buildPluginBinary(ctx, srcDir, name)
}

// loadPluginUnit loads ONE out-of-tree plugin: resolve its provider binary (baked-in or
// host-built), connect over LocalTransport, run the SAME schema gate a builtin runs, then
// register its providers. The schema travels over the Describe channel (gRPC
// schema_cue) — the host never reads the candy's schema/ dir.
func loadPluginUnit(ctx context.Context, name string, p *CandyPluginDecl, srcDir string) error {
	bin, err := resolvePluginBinary(ctx, srcDir, name)
	if err != nil {
		return fmt.Errorf("plugin %q (source %s): %w", name, p.Source, err)
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

// collectReferencedPluginWords returns the COMPLETE set of plugin words the work at
// hand can reference, so loadProjectPlugins host-builds + connects ONLY the plugins
// it actually needs (perf-scoping). It unions every reference SITE:
//   - every candy's `external_builder:` selection (the BUILDER leg);
//   - every Op.Plugin across every candy PLAN step (the verb/step legs — all steps,
//     not just run:, so a build-emit run verb AND a deploy/runtime check verb count);
//   - every Op.Plugin across every box PLAN step (a box may author a plugin check
//     verb directly, baked into ai.opencharly.description and run at check live);
//   - the caller-supplied `extra` words (a deploy's substrate kind + the inline
//     Op.Plugin words in its FLATTENED bed plan — see deployNodePluginContext).
//
// Word-keyed + class-AGNOSTIC by design: a plugin candy loads iff ANY of its provided
// words is in this set (pluginProvidesReferencedWord), regardless of class. Over-load
// (a matched-but-unused word) is harmless — the idempotency guard + a connect for an
// undispatched word — while under-load (a MISSED reference) breaks the verb/builder/
// substrate at dispatch, so collection errs toward INCLUDE: every enumerated site is
// unioned, and when in doubt a word is added rather than filtered.
func collectReferencedPluginWords(candies map[string]*Candy, boxes map[string]BoxConfig, extra []string) map[string]struct{} {
	refs := make(map[string]struct{})
	add := func(w string) {
		if w != "" {
			refs[w] = struct{}{}
		}
	}
	for _, w := range extra {
		add(w)
	}
	// addStep references a step's explicit plugin: word AND its closed-#Op verb discriminator. A
	// closed-#Op EXTERNAL check verb (libvirt/spice/kube/adb/appium) authored in a candy/box PLAN
	// is NOT a plugin: word, so without op.Kind() the perf-scoping never connects the candy serving
	// it — e.g. an android bed's `adb:`/`appium:` assertions live in the android-emulator candy
	// plan, and their plugins must load at BOTH the device deploy and check-live. This MIRRORS the
	// op.Kind() surfacing deployNodePluginContext already does for the deploy NODE's plan (R3).
	// Over-load safe: a builtin verb's candy is already registered; a non-plugin verb has no candy.
	addStep := func(op *Op) {
		add(op.Plugin)
		if v, err := op.Kind(); err == nil {
			add(v)
		}
	}
	for _, candy := range candies {
		if candy == nil {
			continue
		}
		add(candy.ExternalBuilder)
		for i := range candy.plan {
			addStep(&candy.plan[i].Op)
		}
	}
	for _, box := range boxes {
		for i := range box.Plan {
			addStep(&box.Plan[i].Op)
		}
	}
	return refs
}

// pluginProvidesReferencedWord reports whether ANY of a plugin candy's declared
// providers' words is in the referenced set — the perf-scoping predicate. Class is
// IGNORED (a word match in any class loads the unit): collection is the complete,
// over-load-safe side, so matching on the word alone can never UNDER-load on a class
// mismatch. A malformed capability string is skipped (validate flags it elsewhere).
func pluginProvidesReferencedWord(p *CandyPluginDecl, refs map[string]struct{}) bool {
	for _, capability := range p.Providers {
		if _, word, ok := splitCapability(string(capability)); ok {
			if _, hit := refs[word]; hit {
				return true
			}
		}
	}
	return false
}

// loadProjectPlugins gates every builtin plugin unit's schema (process-start pass)
// and connects the out-of-tree plugin candies the work at hand REFERENCES (the words
// in refs) before checks/deploys/builds dispatch to their providers. It takes the
// scanned set (ScanAllCandyWithConfig) — which, unlike LoadUnified's project-local
// Candy map, includes @github-fetched candies and carries each candy's own .SourceDir
// + .Plugin (so a box that vendors all its candies via @github, like box/<distro>,
// still loads its plugins). refs (from collectReferencedPluginWords) SCOPES the load:
// a plugin candy NONE of whose providers is referenced is SKIPPED — a host `go build`
// + connect avoided for a word nothing dispatches (a box/<distro> set vendors many
// plugin candies — adb/appium/kube/spice/example-* — most unused by any one build or
// deploy). Errors are returned (not swallowed) so a bed asserting a plugin verb fails
// loudly if its REFERENCED plugin won't load.
func loadProjectPlugins(ctx context.Context, candies map[string]*Candy, refs map[string]struct{}) error {
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
		// PERF-SCOPING: skip an out-of-tree plugin candy NONE of whose providers is
		// referenced by the work at hand — no wasted host build/connect for a word
		// nothing will dispatch. refs is collected COMPLETE (collectReferencedPluginWords
		// + deployNodePluginContext), so a skip here can never drop a referenced plugin
		// (over-load safe; under-load is a bug — see the HARD CONSTRAINT in those docs).
		if !pluginProvidesReferencedWord(candy.Plugin, refs) {
			continue
		}
		// Idempotent re-load: loadProjectPlugins runs on EVERY connect path (build,
		// deploy, check), and a single process that builds AND deploys connects twice
		// (e.g. `charly bundle add` → loadDeployPlugins, then the pod-overlay
		// NewGenerator's build-time connect seam). Skip a plugin already connected FROM
		// THE SAME SOURCE in this process — short-circuiting the whole build+connect+
		// schema-append+register before any of it runs a second time. A SAME word
		// already registered from a DIFFERENT origin is a genuine bijection collision
		// and errors here (preserving register's intent) before the wasteful re-build.
		connected, err := pluginAlreadyConnected(name, candy.Plugin)
		if err != nil {
			return err
		}
		if connected {
			continue
		}
		if err := loadPluginUnit(ctx, name, candy.Plugin, candy.SourceDir); err != nil {
			return err
		}
	}
	return nil
}

// pluginAlreadyConnected reports whether an out-of-tree plugin candy's declared
// providers are ALREADY registered in this process from candy.Plugin.Source — making a
// re-load a no-op. It checks EVERY declared capability: any one already registered from
// the SAME source means the unit is connected (loadPluginUnit registers a unit's
// providers together), so it returns true (skip); any one registered from a DIFFERENT
// origin is a real word→two-providers collision and returns an error. Returns
// (false, nil) when none of the plugin's providers are registered yet.
func pluginAlreadyConnected(name string, p *CandyPluginDecl) (bool, error) {
	connected := false
	for _, capability := range p.Providers {
		class, word, ok := splitCapability(string(capability))
		if !ok {
			continue
		}
		origin, found := providerRegistry.registeredOrigin(class, word)
		if !found {
			continue
		}
		// COEXIST SWITCH: a word already registered as a COMPILED-IN plugin (origin
		// "builtin", registered at init() by registerCompiledPlugin from the
		// charly.yml `compiled_plugins:` selection) means this candy is compiled INTO
		// the running charly — the out-of-process host build + connect is redundant,
		// so SKIP it rather than reporting a collision. This is THE placement-coexist
		// path: a plugin NOT in compiled_plugins loads out-of-process here; one that IS
		// compiled in is served in-proc and skipped. Placement is a per-charly-build
		// choice, invisible above the registry.
		if origin == originBuiltin {
			connected = true
			continue
		}
		if origin != p.Source {
			return false, fmt.Errorf("plugin %q provider %s:%s collides with one already registered from %q", name, class, word, origin)
		}
		connected = true
	}
	return connected, nil
}
