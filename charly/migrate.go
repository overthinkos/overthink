package main

// migrate.go — the in-core SHIM for the migration chain (C13a). The whole
// ordered migration chain (the registry + every transformer) moved into the
// COMPILED-IN candy/plugin-migrate; this shim resolves verb:migrate and Invokes
// its OpRun. Because several migration steps need package-main loader machinery a
// candy cannot import (LoadUnified, LoadBundleConfig, mergeUnifiedDocs/
// embeddedDefaults, VerbCatalog, DeployConfigPath), the shim HOST-PRELIFTS those
// lookups and passes the results in the MigrateContext (kit.MigrateContext) it
// marshals as the OpRun input — the same "resolve host-coupled inputs host-side,
// hand the plugin a self-contained payload" pattern as k8s_generate.go.
//
// host→plugin dispatch mirrors egress.go / k8s_generate.go (plain resolve+Invoke).
// Compiled-in placement keeps verb:migrate resolvable during `charly migrate` AND
// the remote-cache auto-migration (refs.go) with no connect step.
//
// NewMigrateContext + the in-core RunMigrations/RunProjectMigrations signatures are
// UNCHANGED from the pre-C13a core API, so migrate_cmd.go, refs.go, and the core
// integration tests are unaffected.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"

	migratecandy "github.com/overthinkos/overthink/candy/plugin-migrate"
	"github.com/overthinkos/overthink/charly/plugin/kit"
)

// init injects the two package-main values the compiled-in migrate candy needs but
// cannot import: the VerbCatalog act-verb set (plan-unify step classification) and
// the host credential store (charly-cutover4 keyring re-key). Both are injected
// once at startup (the act-verb set is a compile-time constant; the credential
// store is lazily resolved per call). plugin-migrate is always compiled-in (it is
// in charly/charly.yml compiled_plugins), so this import is sound.
func init() {
	migratecandy.SetActVerbs(actVerbList())
	migratecandy.SetCredentialStoreProvider(func() migratecandy.CredentialStore { return DefaultCredentialStore() })
}

// MigrateContext is the migration runtime context shared with candy/plugin-migrate.
// The type lives in kit so both core (this shim + NewMigrateContext) and the candy
// chain reference the ONE definition; this alias keeps the in-core name unchanged.
type MigrateContext = kit.MigrateContext

// NewMigrateContext builds a context for project dir, resolving the per-host file
// paths from the user config dir. (Unchanged from the pre-C13a core API — used by
// migrate_cmd.go + the core integration tests.)
func NewMigrateContext(dir string, dryRun bool) (*MigrateContext, error) {
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	hostConfig, err := RuntimeConfigPath()
	if err != nil {
		return nil, err
	}
	ledgerRoot := ""
	if lp, lerr := DefaultLedgerPaths(); lerr == nil {
		ledgerRoot = lp.Root
	}
	return &MigrateContext{
		Dir:            dir,
		HostDeployPath: filepath.Join(cfgDir, "ov", "deploy.yml"),
		HostConfigPath: hostConfig,
		SecretsFile:    filepath.Join(dir, ".secrets"),
		QuadletDir:     filepath.Join(cfgDir, "containers", "systemd"),
		LedgerRoot:     ledgerRoot,
		DryRun:         dryRun,
		Out:            os.Stderr,
	}, nil
}

// RunMigrations runs the full chain (project + per-host + quadlets + secrets) to
// HEAD via the candy. Returns whether anything changed. Each step is idempotent,
// so a config already at HEAD produces zero changes. (Unchanged core API.)
func RunMigrations(ctx *MigrateContext) (bool, error) {
	return runMigrateViaPlugin(ctx, false)
}

// RunProjectMigrations runs only the steps that stay inside the project dir — used
// by the remote-cache auto-migration (refs.go) so fetching a remote repo never
// mutates the user's per-host state. (Unchanged core API.)
func RunProjectMigrations(ctx *MigrateContext) (bool, error) {
	return runMigrateViaPlugin(ctx, true)
}

// runMigrateViaPlugin host-prelifts the loader-coupled inputs, then resolves
// verb:migrate and Invokes OpRun with the marshalled MigrateContext.
func runMigrateViaPlugin(ctx *MigrateContext, projectOnly bool) (bool, error) {
	if ctx == nil {
		return false, errors.New("migrate: nil context")
	}
	ctx.ProjectOnly = projectOnly
	// The project-only runner (refs.go) is silent; the full `charly migrate` is
	// verbose. The candy reconstructs Out from Quiet (Out is never serialized).
	ctx.Quiet = projectOnly
	prelift(ctx, projectOnly)

	prov, ok := providerRegistry.resolve(ClassVerb, "migrate")
	if !ok {
		return false, fmt.Errorf("migrate plugin (verb:migrate) not registered — charly built without candy/plugin-migrate")
	}
	params, err := marshalJSON(ctx)
	if err != nil {
		return false, fmt.Errorf("migrate marshal context: %w", err)
	}
	res, err := prov.Invoke(context.Background(), &Operation{Reserved: "migrate", Op: OpRun, Params: params})
	if err != nil {
		return false, fmt.Errorf("migrate invoke: %w", err)
	}
	var reply kit.MigrateReply
	if res != nil && len(res.JSON) > 0 {
		if err := json.Unmarshal(res.JSON, &reply); err != nil {
			return false, fmt.Errorf("migrate decode reply: %w", err)
		}
	}
	if reply.Error != "" {
		return reply.Changed, errors.New(reply.Error)
	}
	return reply.Changed, nil
}

// prelift fills the host-prelifted MigrateContext fields by running the
// package-main loader lookups the candy chain cannot reach. Every lookup is
// best-effort (an error → empty), matching the pre-C13a in-chain behavior (the
// loader-coupled steps treated a load failure as "no project context"): during a
// migration the project is below-HEAD until the final stamp, so LoadUnified
// rejects it and the steps already ran with empty inputs.
func prelift(ctx *MigrateContext, projectOnly bool) {
	// LoadUnified-derived project inputs (best-effort).
	if uf, ok, err := LoadUnified(ctx.Dir); ok && err == nil && uf != nil {
		ctx.LocalTemplates = sortedMapKeys(uf.Local)
		if !projectOnly {
			ctx.ImageNames = sortedMapKeys(uf.Box)
		}
	}

	// single-filename: does <dir>/build.yml match the embedded vocabulary?
	if data, err := os.ReadFile(filepath.Join(ctx.Dir, "build.yml")); err == nil {
		ctx.BuildYmlMatchesEmbed = localBuildMatchesEmbeddedVocab(data)
	}

	// Host-state prelifts — skipped under the project-only runner so the
	// remote-cache auto-migration never reads the user's per-host state.
	if projectOnly {
		return
	}
	if p, err := DeployConfigPath(); err == nil {
		ctx.HostDeployConfigPath = p
	}
	ctx.BundleVolumes = bundleVolumeSummary()
}

// actVerbList returns the sorted set of verbs whose VerbCatalog DefaultDo is
// DoAct (mkdir/copy/write/link/download/setcap/build) — the plan-unify
// step-classification input the candy cannot derive (VerbCatalog is package-main).
func actVerbList() []string {
	var out []string
	for v, spec := range VerbCatalog {
		if spec.DefaultDo == DoAct {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

// bundleVolumeSummary lifts the per-host deploy config to the quadlet detector's
// minimal summary (name, target, has-encrypted-volume). Best-effort: a load
// failure / nil config yields an empty summary (the quadlet step then finds
// nothing stale, exactly as DetectStaleEncryptedQuadlets did on a load error).
func bundleVolumeSummary() []kit.MigrateBundleVolume {
	dc, err := LoadBundleConfig()
	if err != nil || dc == nil {
		return nil
	}
	var out []kit.MigrateBundleVolume
	for name, node := range dc.Bundle {
		hasEncrypted := false
		for _, v := range node.Volume {
			if v.Type == "encrypted" {
				hasEncrypted = true
				break
			}
		}
		out = append(out, kit.MigrateBundleVolume{Name: name, Target: node.Target, HasEncrypted: hasEncrypted})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// sortedMapKeys returns the map keys as a sorted slice.
func sortedMapKeys[V any](m map[string]V) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// localBuildMatchesEmbeddedVocab reports whether a project's local build.yml
// contributes only build vocabulary the binary already embeds. It is the
// host-prelift helper for the single-filename migrator (candy/plugin-migrate
// cannot run mergeUnifiedDocs / embeddedDefaults). It compares the PARSED
// distro/builder/init/resource maps (not raw bytes) against the embedded default,
// so the redundant-default drop survives the build.yml+sidecar.yml -> charly.yml
// consolidation and is robust to comment / key-order / whitespace differences. A
// customized build.yml (any vocab the embed lacks) returns false.
func localBuildMatchesEmbeddedVocab(data []byte) bool {
	// Parse the local build.yml through the SAME document-routing core the embedded
	// default flows through (mergeUnifiedDocs → classifyDoc → normalizeNodeInto),
	// so both sides get identical normalization + defaults materialization (a raw
	// yaml.Unmarshal would skip the node-form normalizer + default fill, making a
	// content-identical vocab compare unequal). A legacy-shape build.yml fails
	// classifyDoc (mergeUnifiedDocs errors) → returns false → the build.yml is left
	// imported (the safe fallback), exactly as a customized vocab is.
	var local UnifiedFile
	if _, err := mergeUnifiedDocs(&local, data, "local build.yml", ""); err != nil {
		return false
	}
	def, err := embeddedDefaults()
	if err != nil {
		return false
	}
	// distro/builder/init/resource are plugin kinds now (uf.PluginKinds); the accessors
	// decode them back into the typed name-keyed maps this default-vocab comparison needs.
	return reflect.DeepEqual(local.Distros(), def.Distros()) &&
		reflect.DeepEqual(local.Builders(), def.Builders()) &&
		reflect.DeepEqual(local.Inits(), def.Inits()) &&
		reflect.DeepEqual(local.Resources(), def.Resources())
}
