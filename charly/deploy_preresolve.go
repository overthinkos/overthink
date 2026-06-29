package main

import (
	"encoding/json"
	"fmt"
)

// deploy_preresolve.go — the GENERAL per-substrate deploy preresolver hook (F1).
//
// An external (out-of-process) deploy substrate's plugin runs the deployment on a
// venue it cannot hold across the process boundary. For most substrates the generic
// externalDeployTarget hands the plugin the deploy's InstallPlan VIEWS + a venue
// descriptor and the plugin drives the venue via the E3b reverse channel. But some
// substrates need HOST-RESOLVED inputs the InstallPlanView provenance view cannot
// carry (the rich Steps are not on the wire) — e.g. deploy:android needs the live adb
// endpoint (engine inspect on the running pod) + the apk install specs (committed-APK
// paths resolved against the candy source tree) + the google-play creds (host
// credential store). That resolution is substrate-specific AND requires host context,
// so it CANNOT live in the plugin and MUST NOT android-special-case the generic target.
//
// The hook is the seam: each external substrate registers ONE preresolver keyed by its
// word; externalDeployTarget looks it up and, when present, ships its opaque payload in
// DeployVenue.Substrate. The generic target never branches on the substrate — only the
// preresolver body is substrate-specific. GENERAL for all 5: any substrate that needs
// host-resolved venue data registers a preresolver here.

// deployPreresolver resolves the substrate-specific preresolved venue payload for one
// external deploy. It receives the deploy's identity (name/path), the project dir, the
// dispatch-merged node (may be nil on the Update path — the preresolver re-resolves
// from the tree), and the compiled InstallPlans (where the apk: ApkInstallStep entries
// live). It returns the opaque JSON the matching plugin decodes (or nil to ship none).
type deployPreresolver func(name, dir string, node *BundleNode, plans []*InstallPlan) (json.RawMessage, error)

// deployPreresolvers maps an external deploy SUBSTRATE word → its host-side preresolver.
// Populated at package-var init time (before any init(), like registerDedicatedBuiltin),
// so the lookup is race-free.
var deployPreresolvers = map[string]deployPreresolver{}

// registerDeployPreresolver records one substrate's preresolver. Panics on a duplicate
// word (a startup invariant, like the registry's duplicate-provider panic).
func registerDeployPreresolver(word string, fn deployPreresolver) {
	if word == "" || fn == nil {
		panic("registerDeployPreresolver: empty word or nil preresolver")
	}
	if _, dup := deployPreresolvers[word]; dup {
		panic(fmt.Sprintf("registerDeployPreresolver: duplicate preresolver for %q", word))
	}
	deployPreresolvers[word] = fn
}

// deployPreresolverFor returns the registered preresolver for an external substrate
// word, if any. externalDeployTarget calls it; a substrate with no preresolver (the
// marker-only example) ships an empty DeployVenue.Substrate.
func deployPreresolverFor(word string) (deployPreresolver, bool) {
	fn, ok := deployPreresolvers[word]
	return fn, ok
}
