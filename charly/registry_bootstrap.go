package main

import "github.com/overthinkos/overthink/charly/spec"

// builtinProviderInstances is the SINGLE list of every compiled-in Provider —
// verbs, kinds, deploy targets, steps, builders. It replaces the five per-class
// init() registration for-loops (verb/kind/deploy/step/builder_builtins.go) with
// ONE registration site + ONE gate pass, in one sequential init() (below). Each
// instance's (Class, Reserved) is its registry key; RegisterBuiltinProvider keys on
// those. This is the consolidation precursor to the charly.yml-declared provider
// manifest (the next cutover data-drives THIS list's membership from charly.yml).
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

// init is the ONE built-in provider registration site. It registers every Provider
// from builtinProviderInstances, THEN runs every per-class bijection gate — all in
// one sequential init(). Each gate runs after ALL registration, so it observes the
// full registry: the same after-registration guarantee the five per-class init()s
// each gave for their own class, now unified in one place. This single registration
// site is the structural foundation for data-driving provider membership from a
// charly.yml provider manifest (the next cutover).
func init() {
	for _, p := range builtinProviderInstances {
		RegisterBuiltinProvider(p)
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
