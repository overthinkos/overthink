package kit

// migrate_support.go — the shared contract between charly core and the
// compiled-in candy/plugin-migrate: the migration runtime context (built by
// core's NewMigrateContext + host-prelift, consumed by the candy's chain), the
// host-prelifted loader inputs, the OpRun reply, the canonical plural→singular
// key map (shared by core's load-time RejectLegacyPluralKeys gate AND the candy's
// field-singular migrator — R3), and the project path constants the migrators
// reference. Core aliases MigrateContext via `type MigrateContext = kit.MigrateContext`.

import "io"

// Project path constants shared by core and the migrate candy. Core aliases each
// via `const X = kit.X`.
const (
	UnifiedFileName = "charly.yml" // the ONE box/candy manifest filename
	DefaultBoxDir   = "box"        // discovered box/<name>/ directory
	DefaultCandyDir = "candy"      // discovered candy/<name>/ directory
)

// LedgerSchemaVersion is the install-ledger record format version, DECOUPLED from
// the project schema HEAD so a non-ledger cutover never invalidates a migrated
// ledger. Shared by core's ledger read path (ReadDeployRecord/ReadCandyRecord,
// which hard-reject a record lacking this stamp) AND the candy's ledger-candy-keys
// migrator (which stamps it). Core aliases via `const ledgerSchemaVersion = kit.LedgerSchemaVersion`.
const LedgerSchemaVersion = "2026.161.1649"

// MigrateContext carries the paths, flags, and HOST-PRELIFTED loader inputs every
// migration step needs. Core's NewMigrateContext builds one (resolving the per-host
// paths) and the in-core RunMigrations/RunProjectMigrations shims fill the prelifted
// fields (running the package-main-coupled lookups the candy cannot) before marshalling
// it as the OpRun input; the candy's Invoke decodes it, sets Out, and runs the chain.
// Out is never serialized (json:"-"); the in-proc compiled-in placement makes the
// candy's os.Stderr the host's, so progress lines surface for `charly migrate`.
type MigrateContext struct {
	Dir            string `json:"dir"`              // project directory
	HostDeployPath string `json:"host_deploy_path"` // per-host deploy overlay (legacy ~/.config/ov/deploy.yml initially; chain retargets)
	HostConfigPath string `json:"host_config_path"` // ~/.config/charly/config.yml
	SecretsFile    string `json:"secrets_file"`     // <Dir>/.secrets
	QuadletDir     string `json:"quadlet_dir"`      // ~/.config/containers/systemd
	LedgerRoot     string `json:"ledger_root"`      // install-ledger root ("" in project-only mode)
	DryRun         bool   `json:"dry_run"`
	ProjectOnly    bool   `json:"project_only"` // skip TouchesHost steps (remote-cache auto-migration)
	Quiet          bool   `json:"quiet"`        // suppress progress output (the project-only path)

	// Host-prelifted loader-coupled inputs — core runs the package-main loader
	// lookups the candy cannot reach and threads the results here (host-prelift):
	ImageNames           []string              `json:"image_names"`             // LoadUnified(dir).Box keys — require-image rule 3
	HostDeployConfigPath string                `json:"host_deploy_config_path"` // DeployConfigPath() — require-image per-host file
	LocalTemplates       []string              `json:"local_templates"`         // LoadUnified(dir).Local keys — target-local disambiguation
	BuildYmlMatchesEmbed bool                  `json:"build_yml_matches_embed"` // localBuildMatchesEmbeddedVocab(<dir>/build.yml) — single-filename
	BundleVolumes        []MigrateBundleVolume `json:"bundle_volumes"`          // LoadBundleConfig() summary — quadlets stale detection

	// NOTE: the plan-unify act-verb set (VerbCatalog DoAct keys) is NOT prelifted
	// here — it is a compile-time constant, injected once at startup via the candy's
	// SetActVerbs (see candy/plugin-migrate/credential_inject.go), not per-run.

	Out io.Writer `json:"-"` // progress reporting; the candy defaults to os.Stderr (io.Discard when Quiet)
}

// MigrateBundleVolume is the per-deploy summary the quadlet stale-detector needs,
// host-prelifted from LoadBundleConfig (the candy cannot run the bundle loader).
type MigrateBundleVolume struct {
	Name         string `json:"name"`          // deploy name (→ ov-<name>.container quadlet)
	Target       string `json:"target"`        // deploy target ("" / pod / container are quadlet-bearing)
	HasEncrypted bool   `json:"has_encrypted"` // declares at least one encrypted volume
}

// MigrateReply is the OpRun result: Changed reports whether anything was migrated,
// Files lists the changed step identifiers, Error carries a chain failure ("" == ok).
type MigrateReply struct {
	Changed bool     `json:"changed"`
	Files   []string `json:"files"`
	Error   string   `json:"error"`
}

// PluralToSingularYAMLKeys is the canonical plural → singular mapping applied by
// the candy's field-singular migrator AND rejected by core's load-time
// RejectLegacyPluralKeys gate (parseCandyYAML / LoadUnified / LoadBundleConfig).
// ONE source of truth shared across the module boundary (R3). Every entry is a
// top-level YAML mapping key; nested keys with the same spelling are rewritten too
// because the migrator algorithm is purely lexical.
//
// Three categories: list-plurals (sequence values), map/namespace plurals
// (mapping values), and compound plurals.
var PluralToSingularYAMLKeys = map[string]string{
	// §A.2.a — list-plurals
	"includes": "include",
	"layers":   "layer",
	"ports":    "port",
	"volumes":  "volume",
	"secrets":  "secret",
	"aliases":  "alias",
	// builds: → produce: is a SEMANTIC rename, not a pluralization
	// removal. The naive singular `build:` would collide with the
	// existing `build:` yaml tag in BoxConfig (BuildFormats). The
	// downstream consumer assigns img.Produce to BuilderCapabilities, so
	// `produce:` is the semantic fit.
	"builds":    "produce",
	"requires":  "require",
	"tasks":     "task",
	"artifacts": "artifact",
	"packages":  "package",
	"sidecars":  "sidecar",
	// 2026-06 singular-label cutover: the candy parser now hard-rejects
	// these as unknown keys (the OCI label contract + the CandyYAML fields
	// went singular). hooks: / capabilities: are candy-level fields; tags:
	// is the eval-scenario field. requires_capabilities (below) already
	// singularizes longest-first, so `capabilities` is safe to add here.
	"hooks":        "hook",
	"capabilities": "capability",
	"tags":         "tag",

	// §A.2.b — map/namespace plurals
	"images":      "image",
	"distros":     "distro",
	"builders":    "builder",
	"inits":       "init",
	"deployments": "deploy",
	"deploys":     "deploy",
	"clusters":    "cluster",
	// "groups": "group" — CARVE-OUT: the eval Check struct already has a
	// verb-level `group:` scalar (the group-membership check), so renaming a
	// Check's `groups:` list to `group:` would collide. cloud-init's `groups:`
	// is also kept plural for the same global-key reason. (Same class of
	// semantic carve-out as addr/addrs below.)
	"targets": "target",
	"modules": "module",

	// §A.2.c — compound plurals
	"env_requires":          "env_require",
	"env_accepts":           "env_accept",
	"secret_requires":       "secret_require",
	"secret_accepts":        "secret_accept",
	"mcp_provides":          "mcp_provide",
	"mcp_requires":          "mcp_require",
	"mcp_accepts":           "mcp_accept",
	"requires_capabilities": "requires_capability",
	"add_layers":            "add_layer",
	"exit_codes":            "exit_code",
	"system_services":       "system_service",
	"cap_adds":              "cap_add",
	"with_services":         "with_service",

	// §A.2.d — domain plurals (overthink-native authoring keys)
	"events":    "event",
	"replicas":  "replica",
	"ssh_args":  "ssh_arg",
	"mounts":    "mount",
	"snapshots": "snapshot",
	"repos":     "repo",
	"subgroups": "subgroup",
	// "addrs": "addr" — REVERTED: collides with existing addr: scalar field in evalspec.go (semantic carve-out)
	"phases":             "phase",
	"steps":              "step",
	"metrics":            "metric",
	"notes":              "note",
	"examples":           "example",
	"replaces":           "replace",
	"recipes":            "recipe",
	"scenarios":          "scenario",
	"versions":           "version",
	"formats":            "format",
	"start_retries":      "start_retry",
	"start_secs":         "start_sec",
	"wait_seconds":       "wait_second",
	"solved_ids":         "solved_id",
	"over_ids":           "over_id",
	"newly_solved_ids":   "newly_solved_id",
	"oci_labels":         "oci_label",
	"extra_repos":        "extra_repo",
	"pod_defaults":       "pod_default",
	"env_defaults":       "env_default",
	"path_contributions": "path_contribution",

	// §A.2.e — field-singular completion batch (2026-05). Only overthink-NATIVE
	// authoring keys are singularized here. EXTERNAL-SCHEMA keys are deliberately
	// ABSENT and kept PLURAL so the authoring key matches the external output:
	// overthink renders cloud-init / Kubernetes / libvirt config with the
	// external schema's own plural keys (`users:`, `labels:`, `resources:`,
	// `<devices>`, `<topology sockets= cores= threads=>`, …), so authoring those
	// fields in the SAME plural spelling keeps a 1:1 mapping. The kept-plural set
	// (defined by the kept-plural keys in the generated spec/ types, k8s_config.go,
	// and the charly/vmshared/ libvirt + cloud-init types) includes: users, groups, mirrors,
	// write_files, ethernets, hostnames, labels, annotations, tolerations,
	// pull_secrets, resources, limits, requests, devices, channels, cores,
	// sockets, threads, dies, disks, cpus, filesystems, interfaces, inputs,
	// hostdevs, memnodes, snippets, graphics, hugepages, iothreads, timers,
	// frequencies, retries, oem_strings, port_forwards, spinlocks, features,
	// heads, shares, patches, kernel_hashes. Other carve-outs: groups/addrs
	// (verb collisions, above); config.yml-only keys in runtime_config.go (not
	// field-singular-walked).
	"platforms":           "platform",
	"instances":           "instance",
	"options":             "option",
	"opts":                "opt",
	"headers":             "header",
	"args":                "arg",
	"workarounds":         "workaround",
	"vars":                "var",
	"install_commands":    "install_command",
	"management_commands": "management_command",
	"detect_files":        "detect_file",
	"layer_files":         "layer_file",
	"layer_fields":        "layer_field",
	"section_fields":      "section_field",
	"base_packages":       "base_package",
	"include_packages":    "include_package",
	"copy_artifacts":      "copy_artifact",
	"exclude_distros":     "exclude_distro",
	"env_provides":        "env_provide",
}
