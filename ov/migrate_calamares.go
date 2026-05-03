package main

// migrate_calamares.go — `ov migrate calamares`.
//
// One-shot migration that brings layer.yml authoring into 1:1 alignment with
// Calamares' netinstall.yaml package-group vocabulary where the concepts
// overlap, and folds ov-specific fields next to them at the top level (no
// `ov:` namespace wrapper).
//
// Per-file rewrites:
//   - rename `depends:` → `requires:`
//   - delete `directory:` (0 layers used it; removed from schema)
//   - delete `info:` (0 layers used it; description: carries the metadata)
//   - collapse `rpm:` / `deb:` / `pac:` / `aur:` format sections AND
//     per-distro tag sections (`debian:13:`, `ubuntu:24.04:`, `debian,ubuntu:`)
//     into one Calamares-shaped surface:
//       * top-level `packages: [<flat list>]` carries the intersection across
//         every distro the layer targets (or all of them if a single distro);
//       * `distros: {<distro>: {packages: [...], copr/repos/exclude/options/
//         modules: ...}}` carries per-distro overrides plus format-specific
//         extras. AUR sub-block under `distros.archlinux.aur:`. Versioned
//         distro keys flatten to `debian-13` / `ubuntu-24.04`. Comma-form
//         (`debian,ubuntu:`) expands to two entries.
//
// Idempotent. Running twice produces byte-identical output (a re-run finds
// no `depends:` / `rpm:` / `deb:` / `pac:` / `aur:` / `<distro>:<ver>:` /
// `directory:` / `info:` keys to migrate).

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// MigrateCalamaresCmd is `ov migrate calamares`.
type MigrateCalamaresCmd struct {
	DryRun bool `long:"dry-run" help:"Print files that would be modified, don't touch the filesystem"`
}

func (c *MigrateCalamaresCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	changed, err := MigrateCalamares(dir, c.DryRun)
	if err != nil {
		return err
	}
	prefix := "modified "
	if c.DryRun {
		prefix = "[dry-run] would modify "
	}
	if len(changed) == 0 {
		fmt.Println("ov migrate calamares: nothing to migrate (already at Calamares-aligned schema)")
		return nil
	}
	for _, p := range changed {
		fmt.Println(prefix + p)
	}
	return nil
}

// distroForFormat maps a legacy format-section name to the distro names that
// install via that format. AUR is a special case — it lives under the
// archlinux distro as a sub-block, not as a parallel distro.
var distroForFormat = map[string][]string{
	"rpm": {"fedora"},
	"deb": {"debian", "ubuntu"},
	"pac": {"archlinux"},
	"aur": {"archlinux"},
}

// knownFormats is the set of legacy format-section keys collapsed by the
// migrator. Anything else at the top level matching the dotted/colon form
// (e.g. `debian:13`) is treated as a per-distro tag section.
var knownFormats = map[string]bool{
	"rpm": true,
	"deb": true,
	"pac": true,
	"aur": true,
}

// MigrateCalamares walks every *.yml / *.yaml under dir and applies the
// Calamares-alignment rewrites. Returns the list of modified file paths.
func MigrateCalamares(dir string, dryRun bool) ([]string, error) {
	var changed []string
	walkErr := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			base := filepath.Base(path)
			if base == ".git" || base == "node_modules" || base == ".build" ||
				base == ".cache" || base == ".eval" || base == "vendor" || base == "bin" {
				return filepath.SkipDir
			}
			// Don't descend into .claude — that's user-local state.
			if base == ".claude" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".yml") && !strings.HasSuffix(path, ".yaml") {
			return nil
		}
		modified, err := migrateCalamaresFile(path, dryRun)
		if err != nil {
			return fmt.Errorf("migrating %s: %w", path, err)
		}
		if modified {
			changed = append(changed, path)
		}
		return nil
	})
	if walkErr != nil {
		return changed, walkErr
	}
	sort.Strings(changed)
	return changed, nil
}

// migrateCalamaresFile parses one YAML file, walks its layer mappings,
// applies the rewrites, and writes back if anything changed. Comment-
// preserving via the yaml.v3 Node API.
func migrateCalamaresFile(path string, dryRun bool) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		// Not parseable YAML; skip silently.
		return false, nil
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return false, nil
	}

	changed := false
	for _, root := range doc.Content {
		if root.Kind != yaml.MappingNode {
			continue
		}
		if migrateLayerMappings(root) {
			changed = true
		}
	}

	if !changed {
		return false, nil
	}
	if dryRun {
		return true, nil
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return true, err
	}
	if err := enc.Close(); err != nil {
		return true, err
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return true, err
	}
	return true, nil
}

// migrateLayerMappings finds every layer body inside the root and applies
// the calamares rewrites to it. Layer bodies are found at:
//   - root.layer.<body>           — kind-keyed standalone layer.yml
//   - root.layers.<name>.<body>   — inline-layers map in overthink.yml
//   - root.<key like depends/rpm/etc> — flat root IS a layer body (legacy
//     form, parser rejects it but we still rewrite if present)
func migrateLayerMappings(root *yaml.Node) bool {
	changed := false
	// Path 1: kind-keyed `layer: <body>`
	if val := mappingChild(root, "layer"); val != nil && val.Kind == yaml.MappingNode {
		if migrateLayerBody(val) {
			changed = true
		}
	}
	// Path 2: inline `layers: { <name>: <body>, ... }`
	if val := mappingChild(root, "layers"); val != nil && val.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(val.Content); i += 2 {
			body := val.Content[i+1]
			if body.Kind == yaml.MappingNode {
				if migrateLayerBody(body) {
					changed = true
				}
			}
		}
	}
	// Path 3: flat root that has a layer-shaped key. Detected by presence
	// of `depends:`, top-level `rpm:`/`deb:`/`pac:`/`aur:`, or `tasks:`
	// directly on the root mapping (and no `layer:` / `layers:` wrapper).
	if mappingChild(root, "layer") == nil && mappingChild(root, "layers") == nil {
		if hasLayerSurface(root) {
			if migrateLayerBody(root) {
				changed = true
			}
		}
	}
	return changed
}

// hasLayerSurface returns true if the mapping looks like a flat layer body
// (carries any of: depends, rpm, deb, pac, aur, tasks, service, requires).
func hasLayerSurface(node *yaml.Node) bool {
	for i := 0; i+1 < len(node.Content); i += 2 {
		switch node.Content[i].Value {
		case "depends", "rpm", "deb", "pac", "aur", "tasks", "service", "requires":
			return true
		}
	}
	return false
}

// migrateLayerBody applies all per-layer rewrites to a single layer mapping.
// Returns true if the body was modified.
func migrateLayerBody(node *yaml.Node) bool {
	if node.Kind != yaml.MappingNode {
		return false
	}
	changed := false

	// Pass 1: rename depends: → requires:.
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == "depends" {
			node.Content[i].Value = "requires"
			changed = true
		}
	}

	// Pass 2: delete dead keys (directory, info).
	for i := 0; i+1 < len(node.Content); {
		k := node.Content[i].Value
		if k == "directory" || k == "info" {
			node.Content = append(node.Content[:i], node.Content[i+2:]...)
			changed = true
			continue
		}
		i += 2
	}

	// Pass 3: collapse format sections + tag sections into packages: + distros:.
	if collapsePackagesIntoDistros(node) {
		changed = true
	}

	return changed
}

// collapsePackagesIntoDistros walks the layer mapping and collapses every
// `rpm:`/`deb:`/`pac:`/`aur:` block plus every `<distro>[:<ver>][,<other>]:`
// tag block into a top-level `packages:` (intersection if multi-distro) +
// `distros:` map (per-distro packages + format-specific extras).
//
// Returns true when at least one legacy block was consumed.
func collapsePackagesIntoDistros(node *yaml.Node) bool {
	// Collect indices of legacy keys to remove + their parsed contents.
	type collected struct {
		distros []string                 // distros that get this entry's packages (e.g. rpm → [fedora])
		isAUR   bool                     // when true, packages go under distros.archlinux.aur, not distros.archlinux
		packages []string
		extras   map[string]*yaml.Node // copr, repos, exclude, options, modules
	}

	type indexed struct {
		index   int
		content collected
	}
	var entries []indexed

	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i].Value
		val := node.Content[i+1]

		var distros []string
		isAUR := false

		if knownFormats[key] {
			distros = distroForFormat[key]
			if key == "aur" {
				isAUR = true
			}
		} else if d, isTag := classifyDistroTag(key); isTag {
			distros = d
		} else {
			continue
		}

		c := collected{distros: distros, isAUR: isAUR, extras: map[string]*yaml.Node{}}
		if val.Kind == yaml.MappingNode {
			for j := 0; j+1 < len(val.Content); j += 2 {
				k := val.Content[j].Value
				v := val.Content[j+1]
				switch k {
				case "packages":
					c.packages = stringsFromSequence(v)
				case "copr", "repos", "exclude", "options", "modules":
					c.extras[k] = v
				}
			}
		}
		entries = append(entries, indexed{index: i, content: c})
	}

	if len(entries) == 0 {
		return false
	}

	// Build per-distro accumulator.
	type accumPkgs struct {
		Packages []string
		AUR      []string
		Extras   map[string]*yaml.Node
	}
	accum := map[string]*accumPkgs{}
	getDistro := func(name string) *accumPkgs {
		if accum[name] == nil {
			accum[name] = &accumPkgs{Extras: map[string]*yaml.Node{}}
		}
		return accum[name]
	}

	for _, e := range entries {
		for _, d := range e.content.distros {
			a := getDistro(d)
			if e.content.isAUR {
				a.AUR = append(a.AUR, e.content.packages...)
			} else {
				a.Packages = append(a.Packages, e.content.packages...)
			}
			for k, v := range e.content.extras {
				if _, exists := a.Extras[k]; !exists {
					a.Extras[k] = v
				}
			}
		}
	}

	// Sorted distro names for deterministic output.
	distroNames := make([]string, 0, len(accum))
	for d := range accum {
		distroNames = append(distroNames, d)
	}
	sort.Strings(distroNames)

	// Compute intersection ONLY when every referenced distro has non-empty
	// packages AND there are at least two distros to intersect across. The
	// 2026-05 ov-full bug: original `rpm: [G,P], pac: [], deb: []` meant
	// "install on fedora, intentionally NOTHING on arch/debian". Naively
	// elevating fedora's packages to the top level would feed them to
	// pacman/apt as well via the bridge — breaking the build with
	// "target not found". Rule:
	//   - single distro contributes → emit under distros.<name>, NO top-level
	//   - multi-distro, ANY empty → emit per-distro, NO top-level (preserve
	//     intent: empty distros stay absent so the bridge skips them)
	//   - multi-distro, all non-empty → intersection at top level + per-distro diffs
	type pkgWithDescription struct{ Name, Desc string }
	var intersection []string
	{
		allHaveNonEmpty := len(distroNames) > 0
		for _, d := range distroNames {
			if len(accum[d].Packages) == 0 {
				allHaveNonEmpty = false
				break
			}
		}
		if allHaveNonEmpty && len(distroNames) >= 2 {
			intersection = uniqueStrings(accum[distroNames[0]].Packages)
			for _, d := range distroNames[1:] {
				other := stringSet(accum[d].Packages)
				filtered := intersection[:0]
				for _, p := range intersection {
					if other[p] {
						filtered = append(filtered, p)
					}
				}
				intersection = filtered
			}
		}
	}
	intSet := stringSet(intersection)

	// Build the top-level `packages:` node (only if non-empty intersection).
	var packagesNode *yaml.Node
	if len(intersection) > 0 {
		packagesNode = newSequenceNodeStrings(intersection)
	}

	// Build the `distros:` mapping with per-distro differences + extras.
	distrosNode := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	hasDistros := false
	for _, d := range distroNames {
		a := accum[d]
		// Compute set difference vs. intersection.
		diff := []string{}
		for _, p := range uniqueStrings(a.Packages) {
			if !intSet[p] {
				diff = append(diff, p)
			}
		}
		body := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		if len(diff) > 0 {
			body.Content = append(body.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "packages"},
				newSequenceNodeStrings(diff),
			)
		}
		// Format-specific extras (copr, repos, exclude, options, modules).
		extraKeys := make([]string, 0, len(a.Extras))
		for k := range a.Extras {
			extraKeys = append(extraKeys, k)
		}
		sort.Strings(extraKeys)
		for _, k := range extraKeys {
			body.Content = append(body.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k},
				a.Extras[k],
			)
		}
		// AUR sub-block (only on archlinux).
		if d == "archlinux" && len(a.AUR) > 0 {
			aurBody := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			aurBody.Content = append(aurBody.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "packages"},
				newSequenceNodeStrings(uniqueStrings(a.AUR)),
			)
			body.Content = append(body.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "aur"},
				aurBody,
			)
		}
		// Skip empty distro entries.
		if len(body.Content) == 0 {
			continue
		}
		distrosNode.Content = append(distrosNode.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: d},
			body,
		)
		hasDistros = true
	}

	// Now rewrite the layer body:
	//   - delete every legacy entry index (in reverse to keep indices valid)
	//   - insert `packages:` and `distros:` nodes at the position of the first
	//     deleted legacy entry (or at end if none-before).
	insertAt := -1
	indices := make([]int, 0, len(entries))
	for _, e := range entries {
		indices = append(indices, e.index)
	}
	sort.Ints(indices)
	if len(indices) > 0 {
		insertAt = indices[0]
	}
	// Delete in reverse.
	for i := len(indices) - 1; i >= 0; i-- {
		idx := indices[i]
		if idx+1 < len(node.Content) {
			node.Content = append(node.Content[:idx], node.Content[idx+2:]...)
		}
	}

	// Build inserts.
	var inserts []*yaml.Node
	if packagesNode != nil {
		inserts = append(inserts,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "packages"},
			packagesNode,
		)
	}
	if hasDistros {
		inserts = append(inserts,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "distros"},
			distrosNode,
		)
	}
	if len(inserts) == 0 {
		return true
	}

	if insertAt < 0 || insertAt > len(node.Content) {
		insertAt = len(node.Content)
	}
	tail := append([]*yaml.Node{}, node.Content[insertAt:]...)
	node.Content = append(node.Content[:insertAt], inserts...)
	node.Content = append(node.Content, tail...)
	return true
}

// knownDistroNames is the canonical set of distro identifiers ov recognizes
// as the bare-form leading segment of a tag key. Used by classifyDistroTag
// to whitelist legitimate per-distro tag sections instead of blacklisting
// non-layer YAML keys (Taskfile / overthink.yml top-levels / etc.).
var knownDistroNames = map[string]bool{
	"fedora":     true,
	"archlinux":  true,
	"arch":       true,
	"debian":     true,
	"ubuntu":     true,
	"alpine":     true,
	"rocky":      true,
	"almalinux":  true,
	"opensuse":   true,
	"suse":       true,
	"centos":     true,
	"rhel":       true,
	"oraclelinux": true,
	"amazonlinux": true,
	"gentoo":     true,
	"nixos":      true,
}

// classifyDistroTag returns the distro name(s) implied by a top-level key
// like `debian:13` / `ubuntu:24.04` / `debian,ubuntu` / `archlinux`. The
// `<distro>:<ver>` form flattens to a single `<distro>-<ver>` key. The
// comma form expands to one entry per distro. Returns isTag=false when
// the key isn't a distro tag (e.g. plain layer fields, Taskfile keys, etc.).
//
// Detection rule (whitelist): a key is a distro tag if and only if every
// comma-separated leading segment (before the optional `:<version>`) is in
// the knownDistroNames set. This rejects arbitrary YAML keys like `tasks`,
// `vars`, `dotenv`, `includes`, `silent` from being mistaken for distro
// tags.
func classifyDistroTag(key string) ([]string, bool) {
	if knownFormats[key] {
		return nil, false
	}
	parts := []string{key}
	if strings.Contains(key, ",") {
		parts = strings.Split(key, ",")
	}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		// Strip optional ":<version>" before whitelist check.
		bare := p
		if i := strings.Index(p, ":"); i >= 0 {
			bare = p[:i]
		}
		if !knownDistroNames[bare] {
			return nil, false
		}
		out = append(out, normalizeDistroTagKey(p))
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

// normalizeDistroTagKey flattens `debian:13` → `debian-13`, leaves bare
// distro names unchanged.
func normalizeDistroTagKey(key string) string {
	if i := strings.Index(key, ":"); i >= 0 {
		return key[:i] + "-" + key[i+1:]
	}
	return key
}

// stringsFromSequence pulls out string values from a YAML sequence node.
// Scalar entries become their literal value; map entries with `name:` use
// the name. Anything else is skipped.
func stringsFromSequence(node *yaml.Node) []string {
	if node == nil || node.Kind != yaml.SequenceNode {
		return nil
	}
	out := make([]string, 0, len(node.Content))
	for _, item := range node.Content {
		switch item.Kind {
		case yaml.ScalarNode:
			out = append(out, item.Value)
		case yaml.MappingNode:
			for j := 0; j+1 < len(item.Content); j += 2 {
				if item.Content[j].Value == "name" {
					out = append(out, item.Content[j+1].Value)
					break
				}
			}
		}
	}
	return out
}

// uniqueStrings preserves order while removing duplicates.
func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// stringSet returns a set view of a slice.
func stringSet(in []string) map[string]bool {
	m := make(map[string]bool, len(in))
	for _, s := range in {
		m[s] = true
	}
	return m
}

// newSequenceNodeStrings builds a flow-or-block YAML sequence node from a
// string slice. Always uses block style for readability.
func newSequenceNodeStrings(values []string) *yaml.Node {
	n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: 0}
	for _, v := range values {
		n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v})
	}
	return n
}
