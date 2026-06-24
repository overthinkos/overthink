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
			for _, word := range deployWordsInManifest(filepath.Join(dir, manifest)) {
				registerDeclaredDeploySubstrate(word)
			}
		}
	}
}

// deployWordsInManifest returns the external DEPLOY words a candy manifest's
// `plugin: providers:` declares. The byte-gate skips every non-plugin manifest
// before paying for a YAML parse (the vast majority of candies). Best-effort:
// a read/parse error yields no words.
func deployWordsInManifest(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(data, []byte("plugin:")) {
		return nil
	}
	var root any
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil
	}
	var providers []string
	collectPluginProviders(root, &providers)
	var words []string
	for _, p := range providers {
		if class, word, ok := splitCapability(p); ok && class == ClassDeployTarget {
			words = append(words, word)
		}
	}
	return words
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
