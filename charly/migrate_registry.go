package main

// migrate_registry.go — the single, ordered migration chain behind `charly migrate`.
//
// Every schema cutover the project has ever shipped is one MigrationStep here,
// stamped with the CalVer of the date it landed and ordered chronologically —
// the order in which the cutovers must replay against an arbitrarily-old config.
// `charly migrate` runs the whole chain to HEAD; each step's existing idempotency
// guard makes running the chain whole safe (already-current files are no-ops).
//
// This replaces the former ~16 hand-invoked `charly migrate <name>` sub-verbs. The
// version representation moved from an integer (`version: 4`) to a CalVer
// (`version: 2026.141.1600`): LatestSchemaVersion is the HEAD step's CalVer, the
// curated constant every versioned file is stamped to and the load-time gate
// compares against.
//
// Adding a future cutover = append ONE MigrationStep with a CalVer strictly
// greater than the current HEAD, just before nothing — the calver-schema stamp
// step always stays last so it picks up the new HEAD automatically.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// mustCalVer parses a compile-time-constant CalVer literal, panicking on a
// malformed value. Used only for the registry's hardcoded step versions, so a
// bad literal fails fast at startup rather than silently mis-ordering the chain.
func mustCalVer(s string) CalVer {
	v, ok := ParseCalVer(s)
	if !ok {
		panic("migrate_registry: invalid CalVer literal " + s)
	}
	return v
}

// MigrateContext carries the paths and flags every migration step needs. The
// unified `charly migrate` runner builds one (NewMigrateContext) and passes it to
// each step's Apply.
type MigrateContext struct {
	Dir            string // project directory (overthink.yml + per-kind files + layers/)
	HostDeployPath string // ~/.config/ov/deploy.yml
	HostConfigPath string // ~/.config/ov/config.yml
	SecretsFile    string // <Dir>/.secrets
	QuadletDir     string // ~/.config/containers/systemd
	LedgerRoot     string // ~/.config/opencharly/installed (install-ledger root; empty in project-only mode)
	DryRun         bool
	Out            io.Writer // progress reporting; defaults to os.Stderr when nil
}

// MigrationStep is one ordered transform in the chain. Version is the schema
// CalVer this step lands files at; the registry is sorted ascending by Version.
// Name is for progress / dry-run reporting only — it is no longer a CLI verb.
// TouchesHost marks steps that mutate per-host state (~/.config/ov, quadlets,
// .secrets); the project-only runner (remote-cache auto-migration) skips them.
// Apply runs the transform and reports whether it changed anything, reusing the
// existing Migrate* functions as its body.
type MigrationStep struct {
	Version     CalVer
	Name        string
	TouchesHost bool
	Apply       func(ctx *MigrateContext) (changed bool, err error)
}

// latestSchemaVersion is the curated HEAD CalVer — the schema-generation
// constant every versioned file is stamped to and the value the load-time gate
// requires. It is defined as a standalone var (not derived from the registry's
// last element) to avoid an initialization cycle: the calver-schema step's
// closure references it, and the registry's last entry uses it as its Version,
// so the two are guaranteed equal (asserted by TestRegistryHeadMatchesLatest).
// Bump it — and append the matching MigrationStep — for each future cutover.
var latestSchemaVersion = mustCalVer("2026.169.0004")

// migrationSteps is the ordered registry. Chronological by git landing date
// (see `git log --diff-filter=A` on each migrate_*.go), which is the order the
// cutovers were authored in and therefore the only correct replay order.
func migrationSteps() []MigrationStep {
	return []MigrationStep{
		{mustCalVer("2026.112.0522"), "unified", false, func(c *MigrateContext) (bool, error) {
			w, err := MigrateUnified(MigrateUnifiedOpts{Dir: c.Dir, RewriteCandies: true, DryRun: c.DryRun})
			return len(w) > 0, err
		}},
		{mustCalVer("2026.114.1558"), "schema-v4", false, func(c *MigrateContext) (bool, error) {
			w, err := MigrateSchemaV4Files(c.Dir, c.DryRun)
			return len(w) > 0, err
		}},
		{mustCalVer("2026.114.2207"), "description", false, func(c *MigrateContext) (bool, error) {
			w, err := MigrateDescription(MigrateDescriptionOpts{Dir: c.Dir, DryRun: c.DryRun})
			return len(w) > 0, err
		}},
		{mustCalVer("2026.123.0114"), "target-local", false, func(c *MigrateContext) (bool, error) {
			w, err := MigrateTargetLocal(c.Dir, c.DryRun)
			return len(w) > 0, err
		}},
		{mustCalVer("2026.124.1942"), "shell-schema", false, func(c *MigrateContext) (bool, error) {
			w, err := MigrateShellSchema(c.Dir, c.DryRun)
			return len(w) > 0, err
		}},
		{mustCalVer("2026.125.0702"), "ov-cachyos", true, func(c *MigrateContext) (bool, error) {
			w, err := MigrateCharlyCachyos(c.Dir, c.DryRun)
			return len(w) > 0, err
		}},
		{mustCalVer("2026.125.1107"), "local-images", false, func(c *MigrateContext) (bool, error) {
			w, err := MigrateLocalImage(c.Dir, c.DryRun)
			return len(w) > 0, err
		}},
		{kindFilesSchemaVersion, "kind-files", false, func(c *MigrateContext) (bool, error) {
			if _, err := os.Stat(filepath.Join(c.Dir, "overthink.yml")); err != nil {
				return false, nil // pre-unified tree; `unified` runs first and creates it
			}
			r, err := MigrateKindFiles(c.Dir, c.DryRun)
			return !r.NoChanges, err
		}},
		{mustCalVer("2026.128.0255"), "local-deploy", true, func(c *MigrateContext) (bool, error) {
			changed, _, err := MigrateLocalDeploy(c.HostDeployPath, c.DryRun)
			return changed, err
		}},
		{mustCalVer("2026.128.0306"), "quadlets", true, func(c *MigrateContext) (bool, error) {
			w, err := MigrateQuadlets(c.QuadletDir, c.DryRun)
			return len(w) > 0, err
		}},
		{mustCalVer("2026.130.1530"), "field-singular", false, func(c *MigrateContext) (bool, error) {
			r, err := MigrateFieldSingular(MigrateFieldSingularOpts{Dir: c.Dir, DryRun: c.DryRun})
			return len(r.Rewritten) > 0, err
		}},
		{mustCalVer("2026.131.0857"), "marimo-rename", true, func(c *MigrateContext) (bool, error) {
			w, err := MigrateMarimoRename(c.Dir, c.DryRun)
			return len(w) > 0, err
		}},
		{mustCalVer("2026.132.1009"), "require-image", true, func(c *MigrateContext) (bool, error) {
			results, warnings, err := MigrateRequireImage(c.Dir, c.DryRun, true)
			for _, w := range warnings {
				fmt.Fprintf(migrateOut(c), "require-image: %s\n", w)
			}
			return len(results) > 0, err
		}},
		{mustCalVer("2026.132.2311"), "tailscale-secrets", true, func(c *MigrateContext) (bool, error) {
			w, err := MigrateTailscaleSecretsAuto(c.SecretsFile, c.DryRun)
			return len(w) > 0, err
		}},
		{mustCalVer("2026.141.1326"), "drop-kdbx", true, func(c *MigrateContext) (bool, error) {
			w, err := MigrateDropKdbx(c.HostConfigPath, c.DryRun)
			return len(w) > 0, err
		}},
		{mustCalVer("2026.141.1559"), "arch-rename", false, func(c *MigrateContext) (bool, error) {
			w, err := MigrateArchRename(c.Dir, c.HostDeployPath, c.DryRun)
			return len(w) > 0, err
		}},
		// 2026-05 import-namespace cutover: the `include:` composition key was
		// deleted in favor of the single `import:` statement (flat + namespaced
		// `alias: ref` items). This step renames include: → import: in every
		// project YAML; repo-specific reshaping (base.yml merge, cachyos
		// namespace, deploy→eval beds) is hand-authored. See CHANGELOG/.
		{mustCalVer("2026.143.0843"), "import-namespace", false, func(c *MigrateContext) (bool, error) {
			w, err := MigrateImportNamespace(c.Dir, c.DryRun)
			return len(w) > 0, err
		}},
		// 2026-05 per-kind versioning cutover: the per-entity `version:` field
		// became load-bearing (image content-stable label + cross-repo layer
		// resolution). Backfills every layer.yml + bare-base image entry with the
		// HEAD CalVer. TouchesHost is false so remote-cache auto-migration also
		// backfills fetched remote layers (the runtime then hard-errors on an
		// unversioned fetched layer rather than carrying a fallback). See CHANGELOG/.
		{mustCalVer("2026.144.1442"), "entity-version", false, func(c *MigrateContext) (bool, error) {
			r, err := MigrateEntityVersion(c.Dir, latestSchemaVersion.String(), c.DryRun)
			return len(r) > 0, err
		}},
		// 2026-06 singular-label cutover: the OCI label contract + the layer
		// authoring keys went singular (hooks→hook, capabilities→capability,
		// services→service, ports→port, …). The layer KEY renames are handled
		// by the field-singular table (extended this cutover); this step
		// rewrites the remaining label-STRING references a config can carry —
		// build.yml init `label_key: org.overthinkos.service.<init>`, plus any
		// forked `oci_label:` / eval label inspection. Baked image labels are
		// re-emitted singular on the next `charly box build` (hard-cutover
		// rebuild), not by config migration. See CHANGELOG/.
		{mustCalVer("2026.155.1800"), "singular-label", false, func(c *MigrateContext) (bool, error) {
			r, err := MigrateSingularLabel(c.Dir, c.DryRun)
			return len(r) > 0, err
		}},
		// 2026-06 candy/box rebrand: the schema kinds `layer:`→`candy:` and
		// `image:`→`box:` were renamed across the whole authoring surface — keys
		// at every depth, the per-kind filenames (image.yml→box.yml,
		// layer.yml→candy.yml), and the layers/→candy/ directory. This step
		// renames the keys, the files, and the directory, and rewrites
		// import:/discover: path references. See CHANGELOG/.
		{mustCalVer("2026.156.0556"), "candy-box-rename", false, func(c *MigrateContext) (bool, error) {
			w, err := MigrateBoxCandyRename(c.Dir, c.HostDeployPath, c.DryRun)
			return len(w) > 0, err
		}},
		// 2026-06 zero-idiosyncrasy generic discover: the kind-keyed
		// `discover: {candy: [...]}` block becomes a FLAT generic scan-spec list
		// `discover: [{path, recursive, manifest}]`. Files are generic
		// kind-containers routed by shape; discovery is fully configured in
		// overthink.yml with no per-kind filename baked into the loader. See
		// CHANGELOG/.
		{mustCalVer("2026.156.1040"), "discover-flatten", false, func(c *MigrateContext) (bool, error) {
			w, err := MigrateDiscoverFlatten(c.Dir, c.DryRun)
			return len(w) > 0, err
		}},
		// 2026-06 cross-deployment `peer:` field: kind:eval beds + kind:deploy
		// gained a `peer:` map of sibling companion deployments (a Chrome DRIVER
		// pod CDP-probing a SEPARATE web SUBJECT, etc.). Purely ADDITIVE — a config
		// without `peer:` is unchanged — so this step transforms nothing; it
		// raises HEAD so an older `ov` REJECTS a `peer:`-using config (with a
		// `Run: charly migrate` hint) instead of silently dropping the unknown key and
		// never bringing the peer up. The calver-schema stamp re-stamps every
		// file to the new HEAD. See CHANGELOG/.
		{mustCalVer("2026.156.1530"), "peer-field", false, func(c *MigrateContext) (bool, error) {
			return false, nil
		}},
		// 2026-06 localpkg per-format map: the layer `localpkg:` field went from a
		// single scalar (Arch-only) to a per-format map {pac:…, rpm:…, deb:…} so
		// ONE charly layer carries a native-package SOURCE per distro format. This step
		// rewrites a legacy scalar `localpkg: <dir>` to `localpkg: {pac: <dir>}`
		// (the legacy value always targeted the Arch PKGBUILD). The loader
		// hard-rejects the scalar form (LocalPkgMap.UnmarshalYAML) with an
		// `charly migrate` hint. See CHANGELOG/.
		{mustCalVer("2026.157.0310"), "localpkg-map", false, func(c *MigrateContext) (bool, error) {
			w, err := MigrateLocalpkgMap(c.Dir, c.DryRun)
			return len(w) > 0, err
		}},
		// 2026-06 ov→charly / overthink→opencharly rebrand: the CLI binary `ov`
		// became `charly` and the project name `overthink` became `opencharly`
		// (the `overthinkos` GitHub org + ghcr registry + repo names are KEPT).
		// This step renames the project root config `overthink.yml`→`charly.yml`,
		// rewrites `@github…/candy/ov[-mcp]` layer-ref paths, `org.overthinkos.*`
		// label strings → `ai.opencharly.*`, the import-namespace alias `ov`→
		// `charly` (key + qualified `ov.<member>` refs), and host-gated relocates
		// the per-host state dirs (~/.config/ov→charly, ~/.config/overthink→
		// opencharly, ~/.cache/ov→charly, ~/.local/share/ov→charly) with OV_*→CH_*
		// env-key rewrites, mutating ctx so calver-schema stamps the new paths.
		// TouchesHost false → remote-cache auto-migration applies the project-side
		// rewrites to fetched repos. See CHANGELOG/.
		{mustCalVer("2026.159.0002"), "charly-rebrand", false, func(c *MigrateContext) (bool, error) {
			w, err := MigrateCharlyRebrand(c)
			return len(w) > 0, err
		}},
		// 2026-06 Cutover 4: finish the rebrand — CH_→CHARLY_ env, credential
		// service prefix ov/→charly/ (incl. OS-keyring re-key), charly-first
		// image names arch-charly→charly-arch / fedora-charly→charly-fedora, and
		// the fish shell-init overthink.fish→opencharly.fish + markers.
		// TouchesHost FALSE — the project-YAML rewrites (Phase A) must also run
		// under the remote-cache auto-migration; the host transforms (Phase B:
		// keyring re-key + shell-init relocation) are gated internally on
		// ctx.HostDeployPath (empty under the project-only runner), mirroring
		// charly-rebrand.
		{mustCalVer("2026.159.1911"), "charly-cutover4", false, func(c *MigrateContext) (bool, error) {
			w, err := MigrateCharlyCutover4(c)
			return len(w) > 0, err
		}},
		// 2026-06 single-filename cutover: charly.yml is the ONE filename for box +
		// candy definitions, and the only file a project needs. Boxes split out of
		// box.yml/base.yml (and an inline box: map) into box/<name>/charly.yml; candy
		// manifests rename candy.yml->charly.yml; vm/pod/k8s/eval/local/android fold
		// into charly.yml's root; the build.yml import is dropped (vocabulary embedded
		// in the binary). TouchesHost false — runs under remote-cache auto-migration so
		// a fetched remote's candy manifests rename too.
		{mustCalVer("2026.160.1300"), "single-filename", false, func(c *MigrateContext) (bool, error) {
			w, err := MigrateSingleFilename(c.Dir, c.HostDeployPath, c.DryRun)
			return len(w) > 0, err
		}},
		// 2026-06 recipe-section-values cutover: finish the candy/box rebrand's
		// DATA VALUES. The 2026.156 candy-box-rename renamed the kind
		// discriminators but missed the eval-harness recipe `from[i].kind:`
		// selector and `from[i].scope:` section-filter list, which still used
		// "layer"/"image". This step rewrites those VALUES (layer→candy,
		// image→box), scoped to `from:` sequence items so a builder `kind: layer`
		// and a check-level `scope: build|deploy` are never touched. The eval label
		// WIRE keys were already candy/box; the new code hard-rejects a recipe
		// `kind: layer` ("invalid kind ... (one of: candy, box, pod, vm)"), so this
		// migration is mandatory. TouchesHost false. See CHANGELOG/.
		{mustCalVer("2026.161.1300"), "recipe-section-values", false, func(c *MigrateContext) (bool, error) {
			w, err := MigrateRecipeSectionValues(c.Dir, c.DryRun)
			return len(w) > 0, err
		}},
		// 2026-06 init-candy-keys: the candy/box rebrand's INIT-SYSTEM vocabulary.
		// `layer_field:`→`candy_field:`, `layer_file:`→`candy_file:`,
		// `depends_layer:`→`depends_candy:` inside `init:` system defs (build.yml /
		// charly.yml). The Go struct (init_config.go) now reads candy_*; a config on
		// the old keys silently loses them. Scoped to the init: subtree; TouchesHost
		// false so remote-cache auto-migration applies it to fetched init overrides.
		{mustCalVer("2026.161.1501"), "init-candy-keys", false, func(c *MigrateContext) (bool, error) {
			w, err := MigrateInitCandyKeys(c.Dir, c.DryRun)
			return len(w) > 0, err
		}},
		// 2026-06 host-charly-yml: rename the per-host deploy overlay
		// ~/.config/charly/deploy.yml → charly.yml so it loads through the SAME
		// unified loader as every project charly.yml (Cutover E). Retargets
		// ctx.HostDeployPath so the trailing calver-schema stamp lands on the
		// renamed file. TouchesHost — never runs under remote-cache auto-migration.
		{mustCalVer("2026.161.1554"), "host-charly-yml", true, MigrateHostCharlyYml},
		// 2026-06 ledger-candy-keys: rename the install-ledger json keys
		// layer→candy / add_layer→add_candy and stamp each record with the
		// ledger schema_version (Cutover F). The ledger is per-host deploy STATE
		// the unified loader never sees, so it gets its OWN version gate
		// (ReadDeployRecord/ReadCandyRecord hard-reject a record without
		// schema_version). TouchesHost; walks ctx.LedgerRoot/{deploys,layers}.
		{mustCalVer("2026.161.1649"), "ledger-candy-keys", true, MigrateLedgerCandyKeys},
		// 2026-06 candy-port-inheritance cutover: boxes no longer declare ports.
		// The box-level `port:` field is RETIRED (published ports are inherited
		// from the candy chain — CollectBoxPorts) and host mappings are
		// auto-allocated on 127.0.0.1 at deploy, so the `port: [auto]` sentinel is
		// retired too (absence of pins IS auto). This step removes box.port +
		// defaults.port and the auto sentinel from deploy/eval/pod/k8s entries;
		// explicit deploy port PINS are preserved. The loader hard-rejects a
		// residual box `port:` (rejectLegacyBoxPort) with a `charly migrate` hint.
		// TouchesHost false — runs under remote-cache auto-migration. See CHANGELOG/.
		{mustCalVer("2026.161.2302"), "drop-box-port", false, func(c *MigrateContext) (bool, error) {
			w, err := MigrateDropBoxPort(c.Dir, c.DryRun)
			return len(w) > 0, err
		}},
		// 2026-06 agent-kind-rename: the reusable agent-CLI catalog kind
		// `kind: ai` → `kind: agent`. The catalog map key `ai:` + the kind:score
		// eligible-agent selector `ai:` → `agent:`, and the standalone-doc
		// discriminator value `kind: ai` → `agent`. The Go loader now reads
		// AgentConfig/`agent:`; a config carrying the old `ai:` key silently
		// loses the catalog/selector. The independent kind:score
		// `validate_ai_artifacts` flag is a separate concept and is NOT renamed.
		// TouchesHost false → remote-cache auto-migration applies the project-file
		// rewrites; the per-host agent overlay (the AI-CLI catalog that never ships
		// with the repo) is processed when ctx.HostDeployPath is set. See CHANGELOG/.
		{mustCalVer("2026.163.0927"), "agent-kind-rename", false, func(c *MigrateContext) (bool, error) {
			w, err := MigrateAgentKindRename(c.Dir, c.HostDeployPath, c.DryRun)
			return len(w) > 0, err
		}},
		// 2026-06 Op-vocabulary unification: task: + eval: + agent: collapse into
		// one generic Op vocabulary; eval: check lists fold into scenario:;
		// description.scenario: hoists to a top-level scenario:; task cmd:→command:
		// + run-as user:→run_as:; check scope:→context:. The ROOT harness eval:
		// block (a kind:eval bed map) is untouched (only a SequenceNode eval: is a
		// check list). TouchesHost false → remote-cache auto-migration applies it
		// to fetched candy manifests. See migrate_op_unify.go + CHANGELOG/.
		{mustCalVer("2026.164.0001"), "op-unify", false, func(c *MigrateContext) (bool, error) {
			w, err := MigrateOpUnify(c.Dir, c.DryRun)
			return len(w) > 0, err
		}},
		// eval→check rename: the evaluation harness's schema vocabulary renames so
		// the YAML verb matches the renamed CLI verb (charly eval → charly check):
		// root eval:→check: bed registry, eval_level:→check_level:, keep_eval_runs:→
		// keep_check_runs:, kind: eval→kind: check. Author entity NAMES are not
		// touched. TouchesHost false → remote-cache auto-migration applies it to
		// fetched candy manifests. See migrate_eval_check.go + CHANGELOG/.
		{mustCalVer("2026.164.0003"), "eval-check", false, func(c *MigrateContext) (bool, error) {
			w, err := MigrateEvalCheck(c.Dir, c.DryRun)
			return len(w) > 0, err
		}},
		// plan-unify: the entire test/eval/benchmark surface collapses into ONE
		// flat plan: vocabulary — task:+scenario:+description.scenario fold into
		// plan: (run:/check:/agent-run:/agent-check:/include:); the Gherkin
		// keywords + the Op.Do axis retire (the keyword IS the do-mode); the
		// description: struct collapses to a string; kind:recipe/kind:score fold
		// into a deploy iterate: block + the entity's own plan:. TouchesHost
		// false → remote-cache auto-migration applies it to fetched candy
		// manifests. See migrate_plan_unify.go + CHANGELOG/.
		{mustCalVer("2026.164.0005"), "plan-unify", false, func(c *MigrateContext) (bool, error) {
			w, err := MigratePlanUnify(c.Dir, c.DryRun)
			return len(w) > 0, err
		}},
		// 2026-06 sidecar-root: the sidecar-template library became a first-class
		// root key. `sidecar:` is now a recognized UnifiedFile field (it was
		// parsed only by a bespoke embedded loader before), so the binary's own
		// embedded charly.yml — and any project — can carry a root `sidecar:`
		// section that flows through the SAME unified loader as every charly.yml.
		// Purely ADDITIVE — a config without `sidecar:` is unchanged — so this
		// step transforms nothing; it raises HEAD so an older `charly` REJECTS a
		// root-`sidecar:`-using config (with a `Run: charly migrate` hint) instead
		// of silently dropping the key. The calver-schema stamp re-stamps every
		// file to the new HEAD. See CHANGELOG/.
		{mustCalVer("2026.165.1047"), "sidecar-root", false, func(c *MigrateContext) (bool, error) {
			return false, nil
		}},
		// unified-node — the ONE forward migration to the name-first node-form model
		// (`<name>: {<kind>: <value>, <sub-entity-children>}`). Rewrites every legacy
		// kind-keyed entity (a `candy:`/`box:`/`vm:`/… single entity or a root-shape
		// `<kind>: {name → entity}` map, and `deploy:`/`check:` → `bundle` nodes with
		// member children). Comment-preserving + idempotent (a node-form doc has no
		// legacy kind-map key → no-op). TouchesHost false so remote-cache
		// auto-migration converts fetched repos too. ALSO converts the per-host
		// deploy overlay (ctx.HostDeployPath, set by host-charly-yml to the runtime
		// ~/.config/charly/charly.yml) via migrateHostOverlayDoc — its legacy
		// `deploy:` map would otherwise stay un-converted (HEAD-stamped but
		// loader-rejected); the host portion self-gates on a non-empty
		// HostDeployPath so the project-only runner still skips host state. The
		// calver-schema stamp (below) then raises every file to HEAD so an older
		// `charly` REJECTS node-form.
		{mustCalVer("2026.169.0001"), "unified-node", false, func(c *MigrateContext) (bool, error) {
			w, err := MigrateUnifiedNode(c.Dir, c.DryRun)
			if err != nil {
				return len(w) > 0, err
			}
			hostChanged, herr := migrateHostOverlayDoc(c, migrateUnifiedNodeDoc)
			return len(w) > 0 || hostChanged, herr
		}},
		// install-strategy-key — completes the 2026-06 ov→charly rebrand's
		// coverage of the per-host VM deploy STATE. The rebrand renamed every
		// authored brand surface but missed the INTERNAL vm_state key
		// `ov_install_strategy:` (the current name is `charly_install_strategy`,
		// VmDeployState.CharlyInstallStrategy); a recovered/old overlay keeps the
		// legacy key and the loader silently drops it (the install strategy is lost
		// on the next VM destroy→create). This step renames the key in the per-host
		// overlay AND any project YAML. It is a COMPLETION of an already-shipped
		// rename (the charly_install_strategy format landed at schema < 2026.169),
		// NOT a new format change, so it does NOT raise HEAD — it slots in as an
		// intra-HEAD step AFTER unified-node converts the overlay to node-form and
		// BEFORE the calver-schema stamp. `charly migrate` runs the whole chain
		// regardless of a config's stamp, so it reaches a stale overlay whether
		// stamped below HEAD or mis-stamped AT HEAD by a prior buggy run.
		// TouchesHost false (project rewrite runs under remote-cache auto-migration;
		// the host overlay portion self-gates on ctx.HostDeployPath). See CHANGELOG/.
		{mustCalVer("2026.169.0002"), "install-strategy-key", false, MigrateInstallStrategyKey},
		// step-venue — the 2026-06 venue-from-position cutover. Retires the
		// step-level venue OVERRIDES (`pod:` per-step container, `on:`
		// cross-member driver) in favor of TREE POSITION: each distinct `pod:`
		// venue becomes an `agent_provisioned: true` resource-node chain (bare →
		// sibling member, dotted → nested children) with the step reparented
		// under its leaf; each `on: D` step moves under member `D`; and
		// `${PEER_HOST:m}`/`${PEER_ENDPOINT:m:p}` rewrite to the unified
		// `${HOST:…}`. Runs AFTER unified-node so it operates on node-form.
		// Comment-preserving + idempotent. TouchesHost false (remote-cache
		// auto-migration converts fetched repos). ALSO runs the node-form transform
		// over the per-host overlay (ctx.HostDeployPath) via migrateHostOverlayDoc
		// for R3 symmetry with unified-node — the overlay carries no venue steps so
		// this is a no-op there, but the shared call keeps the host overlay on the
		// SAME node-form pipeline as the project files (and stays correct should a
		// future overlay ever carry one); the host portion self-gates on a
		// non-empty HostDeployPath so the project-only runner still skips host
		// state. The dotted VM phase's disc is a best-effort `pod` scaffold — the
		// genuine vm/pod disc is hand-authored in the cutover (CHANGELOG/). See
		// migrate_step_venue.go.
		{mustCalVer("2026.169.0003"), "step-venue", false, func(c *MigrateContext) (bool, error) {
			w, err := MigrateStepVenue(c.Dir, c.DryRun)
			if err != nil {
				return len(w) > 0, err
			}
			hostChanged, herr := migrateHostOverlayDoc(c, stepVenueDoc)
			return len(w) > 0 || hostChanged, herr
		}},
		// HEAD — the schema stamp. Must stay LAST so LatestSchemaVersion picks it up
		// and every versioned file lands on this CalVer. This is the integer→CalVer
		// transition step (version: 4 → version: <HEAD>) and the universal stamper.
		// TouchesHost is false so it ALSO runs in project-only mode (remote-cache
		// auto-migration); its host-file stamping is gated on ctx.HostDeployPath,
		// which the project-only runner leaves empty.
		{mustCalVer("2026.169.0004"), "calver-schema", false, func(c *MigrateContext) (bool, error) {
			w, err := MigrateCalverSchema(c.Dir, c.HostDeployPath, latestSchemaVersion, c.DryRun)
			return len(w) > 0, err
		}},
	}
}

// LatestSchemaVersion is the HEAD step's CalVer — the curated schema-generation
// constant every versioned file is stamped to, and the value the load-time gate
// requires. Bumped per future cutover by appending a step (calver-schema stays last).
func LatestSchemaVersion() CalVer {
	return latestSchemaVersion
}

func migrateOut(ctx *MigrateContext) io.Writer {
	if ctx.Out != nil {
		return ctx.Out
	}
	return os.Stderr
}

// NewMigrateContext builds a context for project dir, resolving the per-host
// file paths from the user config dir.
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
// HEAD. Returns whether anything changed. Each step is idempotent, so a config
// already at HEAD produces zero changes.
func RunMigrations(ctx *MigrateContext) (bool, error) {
	return runMigrations(ctx, false)
}

// RunProjectMigrations runs only the steps that stay inside the project dir —
// used by the remote-cache auto-migration (refs.go) so fetching a remote repo
// never mutates the user's per-host state.
func RunProjectMigrations(ctx *MigrateContext) (bool, error) {
	return runMigrations(ctx, true)
}

func runMigrations(ctx *MigrateContext, projectOnly bool) (bool, error) {
	out := migrateOut(ctx)
	anyChanged := false
	for _, step := range migrationSteps() {
		if projectOnly && step.TouchesHost {
			continue
		}
		changed, err := step.Apply(ctx)
		if err != nil {
			return anyChanged, fmt.Errorf("migrate step %s (schema %s): %w", step.Name, step.Version, err)
		}
		if changed {
			anyChanged = true
			verb := "applied"
			if ctx.DryRun {
				verb = "would apply"
			}
			fmt.Fprintf(out, "%s %s (schema %s)\n", verb, step.Name, step.Version)
		}
	}
	if !anyChanged {
		fmt.Fprintf(out, "nothing to migrate (already at schema %s)\n", LatestSchemaVersion())
	}
	return anyChanged, nil
}
