package main

// plugin_prescan.go — the EXTERNAL-deploy-substrate parse pre-scan.
//
// An external (out-of-tree) deploy provider's reserved word (e.g. `exampledeploy`)
// is only registered in providerRegistry AFTER loadProjectPlugins builds + connects
// the plugin — which happens only AFTER LoadConfig parses the project charly.yml.
// But a `disposable: true` check bed (or any deploy) may use that word as its
// SUBSTRATE discriminator (`check-foo: { exampledeploy: {…} }`), which the node-form
// parser must recognize at LOAD time, before the provider connects. Chicken-and-egg.
//
// The fix (the lightweight declaration pre-scan): before the root entity nodes are
// normalized (loadUnifiedInto depth-0, ahead of mergeUnifiedDocs), cheaply read each
// discovered candy's `plugin:` DECLARATION and register the DEPLOY words it declares
// as recognized substrate discriminators. This needs only the WORD + its class — no
// provider instance, no build, no connect (the real provider still connects later at
// loadProjectPlugins and dispatches the actual Add). The parse hooks
// (classifyDisc / isResourceDisc / normalizeNodeInto / validateCheckBeds) consult
// recognizedDeploySubstrate, which is satisfied by EITHER a connected provider OR a
// pre-scanned declaration.
//
// The scan is purely ADDITIVE and best-effort: it only widens the set of recognized
// substrate words and never changes any existing classification, so a project with
// no external deploy substrate is unaffected (the byte-gate skips every non-plugin
// candy before any YAML parse).

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

// declaredDeploySubstrates holds the external DEPLOY-provider words a project's
// candy `plugin:` declarations name, learned by the pre-scan before the provider
// connects. Process-wide + additive: recognizing a superset of substrate words is
// harmless (an unconnected word that reaches dispatch fails loudly at ResolveDeploy),
// and the common no-external-substrate project never adds an entry.
var (
	declaredDeployMu        sync.RWMutex
	declaredDeploySubstrate = map[string]bool{}
	// declaredExternalVerb holds the external (out-of-tree) VERB words a project's candy
	// plugin declarations name — learned POST-SCAN by registerExternalVerbsFromCandies
	// (called at Validate), NOT by the parse-time prescan: a @github-composed plugin candy
	// is fetched DURING the scan, after the prescan, so only the scanned candy map sees its
	// `verb:<word>` declaration. Lets a build-context `run:` plugin verb step validate as
	// build-emit-capable WITHOUT building+connecting the plugin (standalone `charly box
	// validate`). Additive, best-effort, no-false-negatives: an over-broad recognition is
	// harmless — a verb that turns out non-build-emit-capable fails loudly at build via
	// emitPluginFragment's empty-fragment guard. Shares declaredDeployMu (the one lock).
	declaredExternalVerb = map[string]bool{}
)

// recognizedDeploySubstrate reports whether word names a deploy substrate the
// loader may treat as an entity discriminator: EITHER a connected deploy provider
// (built-in or already-loaded external) OR a pre-scanned declaration. The single
// predicate the four parse hooks share (R3).
func recognizedDeploySubstrate(word string) bool {
	if _, ok := providerRegistry.ResolveDeploy(word); ok {
		return true
	}
	declaredDeployMu.RLock()
	defer declaredDeployMu.RUnlock()
	return declaredDeploySubstrate[word]
}

// isExternalDeploySubstrate reports whether target names an EXTERNAL (out-of-process)
// deploy substrate — a recognized deploy word that is NOT one of the core in-process
// substrate kinds (pod/vm/k8s/local/android/group, the resourceKindSet; a group's
// Target is "" and never matches). Such a deploy applies in place on the host venue
// via the E3b reverse channel, so the bed runner treats it like a kind:local deploy
// (no image build, no config/start, bundle-del teardown).
func isExternalDeploySubstrate(target string) bool {
	if target == "" || resourceKindSet[target] {
		return false
	}
	return recognizedDeploySubstrate(target)
}

// registerDeclaredDeploySubstrate records one declared external deploy word.
func registerDeclaredDeploySubstrate(word string) {
	if word == "" {
		return
	}
	declaredDeployMu.Lock()
	declaredDeploySubstrate[word] = true
	declaredDeployMu.Unlock()
}

// isDeclaredExternalVerb reports whether word was registered as an external verb a
// plugin candy declares (registerExternalVerbsFromCandies, post-scan) — used by
// opActsInBuildDeploy ONLY when the verb does NOT resolve in the registry (standalone
// `charly box validate`, where external plugins are not connected). A CONNECTED verb
// (builtin always, or an external connected via the build-time connect seam) is
// classified by its provider type, not this map.
func isDeclaredExternalVerb(word string) bool {
	declaredDeployMu.RLock()
	defer declaredDeployMu.RUnlock()
	return declaredExternalVerb[word]
}

// registerDeclaredExternalVerb records one declared external verb word.
func registerDeclaredExternalVerb(word string) {
	if word == "" {
		return
	}
	declaredDeployMu.Lock()
	declaredExternalVerb[word] = true
	declaredDeployMu.Unlock()
}

// registerExternalVerbsFromCandies registers the external (out-of-tree) VERB words every
// scanned plugin candy declares, so a build-context `run:` plugin verb step validates as
// build-emit-capable in standalone `charly box validate` (where the provider is not
// connected). It runs over the SCANNED candy map — which includes @github-composed plugin
// candies fetched DURING the scan — so it recognizes a verb whether the plugin candy is
// locally vendored OR pulled via @github (the gap the parse-time prescan, which sees only
// locally-discovered dirs, cannot close). Builtins are skipped: they register their verbs
// at init(), so ResolveVerb already classifies them (this map is the not-connected path).
func registerExternalVerbsFromCandies(candies map[string]*Candy) {
	for _, candy := range candies {
		if candy == nil || candy.Plugin == nil {
			continue
		}
		if src := candy.Plugin.Source; src == "" || src == "builtin" {
			continue
		}
		for _, capability := range candy.Plugin.Providers {
			if class, word, ok := splitCapability(string(capability)); ok && class == ClassVerb {
				registerDeclaredExternalVerb(word)
			}
		}
	}
}

// prescanDeclaredPluginWords reads the project root's `discover:` directive from
// rootData (leniently — ignoring everything else), walks each discovered manifest
// dir, and registers every external DEPLOY word a candy's `plugin: providers:`
// declares. baseDir anchors relative discover paths. Best-effort: any I/O or parse
// error on a single file is skipped, never fatal — a malformed candy must not break
// `charly status` / `charly box validate` repo-wide.
func prescanDeclaredPluginWords(rootData []byte, baseDir string) {
	var doc struct {
		Discover DiscoverConfig `yaml:"discover"`
	}
	if err := yaml.Unmarshal(rootData, &doc); err != nil || len(doc.Discover) == 0 {
		return
	}
	for _, spec := range doc.Discover {
		if spec.Path == "" {
			continue
		}
		manifest := spec.Manifest
		if manifest == "" {
			manifest = UnifiedFileName
		}
		root := spec.Path
		if !filepath.IsAbs(root) {
			root = filepath.Join(baseDir, root)
		}
		dirs, err := findEntityDirs(root, manifest, spec.Recursive)
		if err != nil {
			continue
		}
		for _, dir := range dirs {
			prescanPluginManifest(filepath.Join(dir, manifest))
		}
	}
}

// prescanPluginManifest reads one candy manifest's `plugin: providers:` declarations
// and registers each external DEPLOY substrate word — the PARSE-time loader recognition
// that lets a root-charly.yml bed using an external deploy substrate as its entity
// discriminator LOAD before the provider connects. External VERB words are NOT registered
// here: they are needed only at Validate (opActsInBuildDeploy), and a @github-composed
// plugin candy is fetched only DURING the scan (after this parse-time prescan), so verbs
// register post-scan from the candy map (registerExternalVerbsFromCandies). The byte-gate
// skips every non-plugin manifest before paying for a YAML parse. Best-effort.
func prescanPluginManifest(path string) {
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(data, []byte("plugin:")) {
		return
	}
	var root any
	if err := yaml.Unmarshal(data, &root); err != nil {
		return
	}
	var providers []string
	collectPluginProviders(root, &providers)
	for _, p := range providers {
		if class, word, ok := splitCapability(p); ok && class == ClassDeployTarget {
			registerDeclaredDeploySubstrate(word)
		}
	}
}

// collectPluginProviders recursively gathers every `plugin: { providers: [...] }`
// capability string in a decoded node-form manifest (the plugin block sits on a
// `<name>-decl:` CHILD node, so a generic descent finds it regardless of nesting).
func collectPluginProviders(v any, out *[]string) {
	switch t := v.(type) {
	case map[string]any:
		if pv, ok := t["plugin"].(map[string]any); ok {
			if provs, ok := pv["providers"].([]any); ok {
				for _, p := range provs {
					if s, ok := p.(string); ok {
						*out = append(*out, s)
					}
				}
			}
		}
		for _, child := range t {
			collectPluginProviders(child, out)
		}
	case []any:
		for _, e := range t {
			collectPluginProviders(e, out)
		}
	}
}
