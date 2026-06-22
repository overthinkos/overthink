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
var builtinProviderInstances = []Provider{
	// verbs (ClassVerb)
	fileVerb{}, portVerb{}, commandVerb{}, httpVerb{}, packageVerb{}, serviceVerb{},
	processVerb{}, dnsVerb{}, userVerb{}, unixGroupVerb{}, interfaceVerb{}, kernelParamVerb{},
	mountVerb{}, addrVerb{}, matchingVerb{}, cdpVerb{}, wlVerb{}, dbusVerb{}, vncVerb{},
	mcpVerb{}, recordVerb{}, spiceVerb{}, libvirtVerb{}, kubeVerb{}, adbVerb{}, appiumVerb{},
	summarizeVerb{}, killVerb{}, pluginVerb{},
	// kinds (ClassKind)
	candyKind{}, sidecarKind{}, distroKind{}, builderKind{}, initKind{}, resourceKind{},
	agentKind{}, groupKind{}, packageGroupKind{}, targetKind{}, moduleKind{},
	standaloneKind{word: "pod", def: "#Pod"},
	standaloneKind{word: "vm", def: "#Vm"},
	standaloneKind{word: "k8s", def: "#K8s"},
	standaloneKind{word: "local", def: "#Local"},
	standaloneKind{word: "android", def: "#Android"},
	// deploy targets (ClassDeployTarget)
	localTarget{}, vmTarget{}, podTarget{}, k8sTarget{}, androidTarget{},
	// steps (ClassStep)
	systemPackagesStepProvider{}, builderStepProvider{}, opStepProvider{}, fileStepProvider{},
	servicePackagedStepProvider{}, serviceCustomStepProvider{}, shellHookStepProvider{},
	shellSnippetStepProvider{}, repoChangeStepProvider{}, apkInstallStepProvider{},
	localPkgInstallStepProvider{}, rebootStepProvider{},
	// builders (ClassBuilder)
	aurBuilder{}, pixiBuilder{}, cargoBuilder{}, npmBuilder{},
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
