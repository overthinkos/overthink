package main

import (
	"fmt"

	"github.com/overthinkos/overthink/charly/spec"
	"gopkg.in/yaml.v3"
)

// builtinProviderInstances supplies every compiled-in Provider INSTANCE — verbs,
// kinds, deploy targets, steps, builders. The Go types can only be instantiated in
// Go (data cannot construct a Go value), but WHICH of them register is driven by the
// `providers:` manifest in the embedded charly.yml (parsed at init below), NOT by
// this list's membership: the manifest is the registry authority, this list supplies
// the instances it names, and init() gates the two into bijection. Adding a built-in
// is a charly.yml manifest entry PLUS its instance here; the gate fails loudly if
// either side is missing. (Replaces the five per-class init() registration loops.)
//
// EXCEPTION — the externalizable dedicated-provider pattern: a provider carrying NO served
// plugin schema may live in its OWN dedicated plugin_<class>_<name>.go file and self-register
// via registerDedicatedBuiltin, INTENTIONALLY absent from BOTH this slice and the `providers:`
// manifest, yet dispatching identically through providerRegistry. Two sub-cases share the
// mechanism: (1) a schema-LESS IR provider (a deploy-target / step / builder — derived from
// cross-refs or candy-internal, never user-authored, so no authored input to validate); and
// (2) a deploy-shape OR factory KIND provider (group + pod/vm/k8s/local/android — plugin_group.go /
// plugin_substrate.go — plus candy, the box⊻layer factory arm — plugin_candy.go), which IS
// user-authored but is validated by the CLOSED CORE #Pod/#Vm/#K8s/#Local/#Android/#Deploy/#Candy/#Box
// (#NodeDoc) gate rather than a served plugin schema, and which decodes into the typed core maps
// (the deploy-shapes recurse over the genericNode member tree into uf.Bundle/uf.Pod/…; candy routes
// box⊻layer into uf.Box/uf.Candy) — so it cannot use the schema-carrying RegisterBuiltinPluginUnit /
// runPluginKind path the tier-1 kinds use. The per-class bijection gates below still prove every such
// provider is registered (deploy/step have a gate, kinds have checkKindProviderBijection; builders have
// none). See plugin_deploy_local.go, plugin_step_reboot.go, plugin_builder_cargo.go,
// plugin_group.go, plugin_substrate.go, plugin_candy.go.
var builtinProviderInstances = []Provider{
	// verbs (ClassVerb) — file/package/command/service/http/interface/addr/unix_group/user/
	// kernel-param/mount are NOT here: each is a dedicated plugin UNIT (plugin_verb_file.go /
	// plugin_verb_package.go / plugin_verb_command.go / plugin_verb_service.go / plugin_http.go /
	// plugin_interface.go / plugin_addr.go / plugin_unix_group.go / plugin_user.go /
	// plugin_kernel_param.go / plugin_mount.go) that self-registers via RegisterBuiltinPluginUnit,
	// absent from both this slice and the providers: manifest (the goss-verb→plugin + the
	// state-provision-verb→plugin extractions, like process/port/dns; `file` is the LAST
	// state-provision/goss-tier verb extracted; `command` is the install-task-act member and
	// `package`/`service` are the TWO typed-step members of that set).
	// cdp/vnc/wl/dbus/mcp/record/spice/libvirt are NOT here — each is a live-container
	// verb extracted into its OWN dedicated file (plugin_verb_<verb>.go), self-registering
	// via registerDedicatedBuiltin (the schema-less dedicated-provider path, since their
	// modifiers stay on the closed base #Op — no plugin_input, no served schema), absent
	// from both this slice and the providers: manifest. `appium` is the FIRST dep-shedder
	// EXTRACTED: it is an external-charly-verb (candy/plugin-appium, source github.com/…)
	// served out-of-process — NOT a compiled-in instance, absent from this slice AND the
	// providers: manifest; its grpcProvider registers at loadProjectPlugins time. The
	// dep-shedders adb/kube stay here (manifest-listed) until their later extraction.
	kubeVerb{}, adbVerb{},
	summarizeVerb{}, killVerb{}, pluginVerb{},
	// kinds (ClassKind) — NONE remain here: Phase 2 is COMPLETE, every kind is now a
	// dedicated provider file. The tier-1 kinds (agent/module/sidecar/package-group/distro/
	// builder/init/resource/target) became schema-carrying RegisterBuiltinPluginUnit plugins
	// routed through runPluginKind; the 7 dedicated-builtin KindProviders — the 6 deploy-shape
	// kinds (group + pod/vm/k8s/local/android, plugin_group.go / plugin_substrate.go) plus
	// candy (the box⊻layer factory arm, plugin_candy.go) — self-register via
	// registerDedicatedBuiltin, staying in-proc KindProviders (typed DecodeNode → the core
	// buildBundleNodeInto / buildStandaloneResource recursion helpers for the deploy-shapes,
	// candyIsImage + buildCandy for candy) because they decode into the typed core maps. All
	// of those are absent from BOTH this slice and the providers: manifest;
	// checkKindProviderBijection still proves each spec.KindWords entry has a registered
	// KindProvider.
	// deploy targets (ClassDeployTarget) — ALL self-register from their dedicated
	// plugin_deploy_<name>.go files (the externalizable dedicated-provider pattern):
	// local, pod, vm, k8s, android.
	// steps (ClassStep) — ALL self-register from their dedicated plugin_step_<name>.go
	// files: SystemPackages, Builder, Op, File, ServicePackaged, ServiceCustom, ShellHook,
	// ShellSnippet, RepoChange, ApkInstall, LocalPkgInstall, Reboot.
	// builders (ClassBuilder) — ALL self-register from their dedicated
	// plugin_builder_<name>.go files: aur, pixi, cargo, npm.
}

// registerDedicatedBuiltin self-registers a built-in Provider that lives in its OWN
// dedicated file (the externalizable dedicated-provider pattern): either a schema-less
// deploy-target / step / builder (no authored input), or a deploy-shape KIND validated by
// the closed CORE schema rather than a served plugin schema (plugin_group.go /
// plugin_substrate.go) — neither carries a `providers:`-manifest entry nor a
// builtinProviderInstances slice membership. Each such file calls this from a
// package-var initializer, which Go runs before ANY init() — so the per-class
// bijection gates in init() below observe the registration WITHOUT depending on
// cross-file init ordering (the alphabetical race the gates were structured to avoid).
// Returns the provider so the `var _ = registerDedicatedBuiltin(...)` call site reads
// cleanly. RegisterBuiltinProvider panics on a duplicate (class, word), so a provider
// left in BOTH the manifest/slice and a dedicated file is caught loudly at startup.
func registerDedicatedBuiltin(p Provider) Provider {
	RegisterBuiltinProvider(p)
	return p
}

// providerManifest is the parsed `providers:` directive — provider class → the
// reserved words it contributes (matched against each instance's Class()+Reserved()).
type providerManifest map[string][]string

// builtinInstanceMap keys every compiled-in instance by provKey(Class, Reserved).
func builtinInstanceMap() map[string]Provider {
	m := make(map[string]Provider, len(builtinProviderInstances))
	for _, p := range builtinProviderInstances {
		m[provKey(p.Class(), p.Reserved())] = p
	}
	return m
}

// manifestInstanceProblems reports every break in the bijection between the manifest
// and the compiled-in instances: a manifest entry with no instance, or an instance no
// manifest entry names. Empty result ⇒ they agree exactly. Pure (no registration), so
// it is unit-testable with doctored inputs.
func manifestInstanceProblems(manifest providerManifest, byKey map[string]Provider) []string {
	var problems []string
	named := make(map[string]bool, len(byKey))
	for class, words := range manifest {
		for _, w := range words {
			key := provKey(ProviderClass(class), w)
			if _, ok := byKey[key]; !ok {
				problems = append(problems, key+" (manifest entry has no compiled-in instance)")
				continue
			}
			named[key] = true
		}
	}
	for key := range byKey {
		if !named[key] {
			problems = append(problems, key+" (compiled-in instance absent from the providers: manifest)")
		}
	}
	return problems
}

// unmarshalEmbeddedDefaults decodes the embedded charly.yml (the //go:embed default
// config) into dst via a minimal yaml decode — the shared reader for top-level directives
// (providers, context_ignore_baseline) that must be read WITHOUT the full node-form loader
// (the bootstrap circularity: the loader needs the kind providers the manifest registers).
// embeddedCharlyDefaults is populated before init(). Panics on an unparseable embed (a
// build-time invariant, never a runtime input).
func unmarshalEmbeddedDefaults(dst any) {
	if err := yaml.Unmarshal(embeddedCharlyDefaults, dst); err != nil {
		panic(fmt.Errorf("embedded charly.yml unparseable: %w", err))
	}
}

// parseEmbeddedProviderManifest extracts ONLY the `providers:` directive from the embedded
// charly.yml. embeddedCharlyDefaults is a //go:embed var, populated before init().
func parseEmbeddedProviderManifest() providerManifest {
	var doc struct {
		Providers providerManifest `yaml:"providers"`
	}
	unmarshalEmbeddedDefaults(&doc)
	if len(doc.Providers) == 0 {
		panic("registry bootstrap: embedded charly.yml has no providers: manifest")
	}
	return doc.Providers
}

// init is the ONE built-in provider registration site. The `providers:` manifest in
// the embedded charly.yml is the authoritative membership: it gates the manifest ⇄
// compiled-in instances into exact bijection, registers exactly the providers the
// manifest names, then runs every per-class bijection gate after ALL registration so
// each observes the full registry.
func init() {
	byKey := builtinInstanceMap()
	manifest := parseEmbeddedProviderManifest()
	if problems := manifestInstanceProblems(manifest, byKey); len(problems) > 0 {
		panic(fmt.Errorf("registry bootstrap: providers: manifest ⇄ compiled-in instances bijection broken: %v", problems))
	}
	for class, words := range manifest {
		for _, w := range words {
			RegisterBuiltinProvider(byKey[provKey(ProviderClass(class), w)])
		}
	}
	for _, gate := range []func() error{
		func() error { return checkVerbProviderBijection(spec.OpVerbs) },
		func() error { return checkMethodAllowlists(spec.LiveVerbMethods) },
		func() error { return checkKindProviderBijection(spec.KindWords) },
		checkDeployProviderBijection,
		checkStepProviderBijection,
	} {
		if err := gate(); err != nil {
			panic(err)
		}
	}
}
