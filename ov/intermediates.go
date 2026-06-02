package main

import (
	"fmt"
	"os"
	"strings"
)

// pixiBoundLayers identifies layers that have install files (user.yml/root.yml)
// but depend on a pixi environment from their including parent meta-layer.
// These layers must NOT be extracted into auto-intermediates because the
// intermediate won't have the pixi environment they need.
//
// A layer is pixi-bound if:
// 1. It has install files (user.yml or root.yml)
// 2. It does NOT have its own pixi manifest (pixi.toml/pyproject.toml/environment.yml)
// 3. It is included via layers: by another layer that DOES have a pixi manifest
func pixiBoundLayers(layers map[string]*Layer) map[string]bool {
	bound := make(map[string]bool)
	for _, layer := range layers {
		if layer.PixiManifest() == "" {
			continue
		}
		// This layer owns a pixi env. Check its IncludedLayers.
		for _, includedRef := range layer.IncludedLayer {
			included := includedRef.Bare()
			child, ok := layers[included]
			if !ok {
				continue
			}
			// If the included layer has install files but no pixi manifest,
			// it depends on this parent's pixi env and must not be extracted.
			if child.HasInstallFiles() && child.PixiManifest() == "" {
				bound[included] = true
			}
		}
	}
	return bound
}

// trieNode represents a node in the layer prefix trie.
type trieNode struct {
	layer    string               // layer at this position ("" for root)
	children map[string]*trieNode // layer name → child node
	images   []string             // user-defined images terminating here
}

func newTrieNode(layer string) *trieNode {
	return &trieNode{
		layer:    layer,
		children: make(map[string]*trieNode),
	}
}

// GlobalLayerOrder computes a global topological order of all layers across
// all enabled images, using popularity (number of images needing each layer)
// as the primary tie-breaker and lexicographic as secondary.
func GlobalLayerOrder(images map[string]*ResolvedImage, layers map[string]*Layer) ([]string, error) {
	// Count popularity: how many images need each layer (including transitive deps)
	popularity := make(map[string]int)
	for _, img := range images {
		resolved, err := ResolveLayerOrder(img.Layer, layers, nil)
		if err != nil {
			return nil, fmt.Errorf("resolving layers for image %q: %w", img.Name, err)
		}
		// Also include layers from the base chain
		allLayers := collectAllImageLayers(img.Name, images, layers)
		// Merge resolved with allLayers
		seen := make(map[string]bool)
		for _, l := range allLayers {
			seen[l] = true
		}
		for _, l := range resolved {
			if !seen[l] {
				allLayers = append(allLayers, l)
				seen[l] = true
			}
		}
		for _, l := range allLayers {
			popularity[l]++
		}
	}

	// Build dependency graph from layer depends and included layers
	// Only include layers that appear in at least one image
	graph := make(map[string][]string)
	for name := range popularity {
		layer, ok := layers[name]
		if !ok {
			continue
		}
		var deps []string
		for _, depRef := range layer.Require {
			dep := depRef.Bare()
			if _, inUse := popularity[dep]; inUse {
				deps = append(deps, dep)
			}
		}
		for _, includedRef := range layer.IncludedLayer {
			included := includedRef.Bare()
			if _, inUse := popularity[included]; inUse {
				deps = append(deps, included)
			}
		}
		graph[name] = deps
	}

	// Authored layer-list order is an ordering CONSTRAINT, not just a seed set.
	// When an image (or metalayer) writes `layer: [A, B]`, the author means A's
	// install steps run before B's — even when B declares no `require: A`. The
	// canonical case is the builder images' `[rpmfusion, …, build-toolchain]`:
	// build-toolchain installs ffmpeg-devel / x264-devel / libva-devel, which
	// live in the RPM Fusion repos that the rpmfusion layer enables, yet
	// build-toolchain CANNOT `require: rpmfusion` (it is also used on Arch, where
	// those libs come from the distro repos). Without honoring authored order the
	// popularity tie-break can place build-toolchain ahead of rpmfusion in a
	// project whose image set makes build-toolchain the more popular layer,
	// emitting its dnf install before the repos exist and breaking the build.
	//
	// We add each list-adjacent graph-node pair as a dependency edge (the later
	// entry depends on the earlier), skipping any edge that is redundant or that
	// would create a cycle — so genuinely conflicting authored orders fall back
	// to the popularity tie-break exactly as before, while consistent orders
	// (the overwhelming majority) are now respected.
	isNode := func(name string) bool {
		if _, ok := popularity[name]; !ok {
			return false
		}
		_, ok := layers[name]
		return ok
	}
	addListOrderEdge := func(prev, cur string) {
		if prev == cur || !isNode(prev) || !isNode(cur) {
			return
		}
		for _, d := range graph[cur] {
			if d == prev {
				return // already constrained
			}
		}
		// Adding "cur depends on prev" creates a cycle iff prev already
		// (transitively) depends on cur.
		if graphReaches(graph, prev, cur) {
			return
		}
		graph[cur] = append(graph[cur], prev)
	}
	addListEdges := func(list []string) {
		for i := 1; i < len(list); i++ {
			addListOrderEdge(list[i-1], list[i])
		}
	}
	// Every authored layer list contributes ordering edges: image-level lists
	// AND metalayer `layers:` (IncludedLayer) lists. Non-node entries (pure-
	// composition metalayers with no RUN steps) are skipped by addListOrderEdge,
	// so only content layers are constrained.
	for _, img := range images {
		addListEdges(img.Layer)
	}
	for name := range popularity {
		if l, ok := layers[name]; ok {
			addListEdges(bareRefs(l.IncludedLayer))
		}
	}

	// Kahn's algorithm with popularity-based tie-breaking
	return topoSortByPopularity(graph, popularity)
}

// graphReaches reports whether `to` is reachable from `from` by following
// dependency edges (graph[x] lists the layers x depends on). Used to keep
// authored-list-order edge insertion cycle-safe in GlobalLayerOrder.
func graphReaches(graph map[string][]string, from, to string) bool {
	if from == to {
		return true
	}
	visited := make(map[string]bool)
	var dfs func(n string) bool
	dfs = func(n string) bool {
		if n == to {
			return true
		}
		if visited[n] {
			return false
		}
		visited[n] = true
		for _, d := range graph[n] {
			if dfs(d) {
				return true
			}
		}
		return false
	}
	return dfs(from)
}

// topoSortByPopularity performs topological sort with popularity tie-breaking.
// Higher popularity layers come first among zero-in-degree candidates.
func topoSortByPopularity(graph map[string][]string, popularity map[string]int) ([]string, error) {
	inDegree := make(map[string]int)
	reverseGraph := make(map[string][]string)

	for node := range graph {
		inDegree[node] = len(graph[node])
		for _, dep := range graph[node] {
			reverseGraph[dep] = append(reverseGraph[dep], node)
		}
	}

	// Find all nodes with no dependencies
	var queue []string
	for node, degree := range inDegree {
		if degree == 0 {
			queue = append(queue, node)
		}
	}
	sortByPopularity(queue, popularity)

	var result []string
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		result = append(result, node)

		dependents := reverseGraph[node]
		for _, dep := range dependents {
			inDegree[dep]--
			if inDegree[dep] == 0 {
				queue = append(queue, dep)
			}
		}
		sortByPopularity(queue, popularity)
	}

	if len(result) != len(graph) {
		return nil, fmt.Errorf("cycle detected in layer dependency graph")
	}
	return result, nil
}

// sortByPopularity sorts by descending popularity, then lexicographic ascending.
func sortByPopularity(s []string, popularity map[string]int) {
	for i := 0; i < len(s)-1; i++ {
		for j := i + 1; j < len(s); j++ {
			pi, pj := popularity[s[i]], popularity[s[j]]
			if pi < pj || (pi == pj && s[i] > s[j]) {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}

// collectAllImageLayers returns the complete set of layers for an image,
// including all layers inherited through the base chain.
func collectAllImageLayers(imageName string, images map[string]*ResolvedImage, layers map[string]*Layer) []string {
	seen := make(map[string]bool)
	var result []string

	var walk func(name string)
	walk = func(name string) {
		img, ok := images[name]
		if !ok {
			return
		}
		if !img.IsExternalBase {
			walk(img.Base)
		}
		resolved, err := ResolveLayerOrder(img.Layer, layers, nil)
		if err != nil {
			return
		}
		for _, l := range resolved {
			if !seen[l] {
				seen[l] = true
				result = append(result, l)
			}
		}
	}
	walk(imageName)
	return result
}

// AbsoluteLayerSequence returns an image's complete layer set (own + entire
// base chain) as a subsequence of the global order.
func AbsoluteLayerSequence(imageName string, images map[string]*ResolvedImage, layers map[string]*Layer, globalOrder []string) []string {
	allLayers := collectAllImageLayers(imageName, images, layers)
	layerSet := make(map[string]bool, len(allLayers))
	for _, l := range allLayers {
		layerSet[l] = true
	}

	// Filter global order to only include this image's layers
	var seq []string
	for _, l := range globalOrder {
		if layerSet[l] {
			seq = append(seq, l)
		}
	}
	return seq
}

// siblingKey identifies a sibling-grouping equivalence class for intermediate
// computation. Grouping by (Base, UID) — not just Base — ensures that images
// with different user contexts (e.g. uid=1000 default vs. uid=0 root) don't
// share an auto-intermediate. Sharing would bake one group's HOME-relative
// paths (path_append `~/.foo/bin`, env vars using `~` or `$HOME`) into the
// intermediate's `ENV PATH` directives, leaving the other group with dead
// PATH entries that can't be overridden by its own ENV emission.
type siblingKey struct {
	base string
	uid  int
}

// ComputeIntermediates analyzes all images, groups them by (Base, UID),
// builds prefix tries of relative layer sequences within each sibling group,
// creates intermediates at branching points, and returns updated images map.
// User-defined images always take priority over auto-intermediates.
func ComputeIntermediates(images map[string]*ResolvedImage, layers map[string]*Layer, cfg *Config, tag string) (map[string]*ResolvedImage, error) {
	globalOrder, err := GlobalLayerOrder(images, layers)
	if err != nil {
		return nil, fmt.Errorf("computing global layer order: %w", err)
	}

	// Copy all existing images
	result := make(map[string]*ResolvedImage)
	for name, img := range images {
		cp := *img
		result[name] = &cp
	}

	// Compute pixi-bound layers: these must not be extracted into intermediates
	pixiBound := pixiBoundLayers(layers)

	// Collect all builder image names to exclude from intermediate generation.
	builderNames := make(map[string]bool)
	for _, builder := range cfg.Defaults.Builder {
		if builder != "" {
			builderNames[builder] = true
		}
	}
	// Also exclude builders referenced by ANY image's builder map (not just
	// defaults) — e.g. a submodule consumer's `builder: {pixi: ov.arch-builder}`.
	// Without this, a pulled namespaced builder (ov.arch-builder) would be grouped
	// with its consumers and factored into an intermediate it must itself build,
	// producing a `builder -> intermediate -> builder` dependency cycle.
	for _, img := range images {
		for _, builder := range img.Builder {
			if builder != "" {
				builderNames[builder] = true
			}
		}
	}

	// Default UID — used to decide whether an intermediate needs a UID
	// suffix in its name to avoid collision with the default-UID sibling.
	defaultUID := resolveIntPtr(cfg.Defaults.UID, nil, 1000)

	// Group images by (Base, UID). See siblingKey docstring for rationale.
	siblingGroups := make(map[siblingKey][]string)
	for name, img := range images {
		if builderNames[name] {
			continue
		}
		// Pulled namespace-qualified images (e.g. ov.arch, ov.arch-builder,
		// cachyos.cachyos) are external/fixed dependencies, not local siblings —
		// never factor them into local intermediates. (Local consumers that root
		// ON them have unqualified names and ARE grouped by their qualified base.)
		if strings.Contains(name, ".") {
			continue
		}
		k := siblingKey{img.Base, img.UID}
		siblingGroups[k] = append(siblingGroups[k], name)
	}

	// Process internal-base groups in topological order (parents before children)
	// so auto-intermediates from parent groups are visible when processing child groups
	imageOrder, err := ResolveImageOrder(images, layers)
	if err != nil {
		return nil, fmt.Errorf("resolving image order: %w", err)
	}

	processed := make(map[siblingKey]bool)
	for _, parentName := range imageOrder {
		// Each parent may host multiple sibling groups (one per UID).
		// Iterate every sibling key whose base matches parentName.
		for k, children := range siblingGroups {
			if k.base != parentName || len(children) < 2 {
				continue
			}
			processed[k] = true
			if err := processSiblingGroup(k.base, k.uid, defaultUID, children, result, images, layers, cfg, tag, globalOrder, pixiBound); err != nil {
				return nil, err
			}
		}
	}

	// Process external-base groups (parent is an external OCI ref, not in imageOrder)
	for k, children := range siblingGroups {
		if processed[k] || len(children) < 2 {
			continue
		}
		if err := processSiblingGroup(k.base, k.uid, defaultUID, children, result, images, layers, cfg, tag, globalOrder, pixiBound); err != nil {
			return nil, err
		}
	}

	return result, nil
}

// processSiblingGroup builds a prefix trie from the relative layer sequences
// of children sharing the same parent + uid, and creates intermediates at
// branch points. The uid is the shared UID of this sibling group; it flows
// through walkTrieScoped into createIntermediate so the emitted ENV PATH
// references the correct HOME for this group's user context.
func processSiblingGroup(parentName string, uid, defaultUID int, children []string, result, origImages map[string]*ResolvedImage, layers map[string]*Layer, cfg *Config, tag string, globalOrder []string, pixiBound map[string]bool) error {
	sortStrings(children)

	// Get layers provided by parent
	parentProvided := make(map[string]bool)
	if _, ok := result[parentName]; ok {
		provided, err := LayerProvidedByImage(parentName, result, layers)
		if err == nil {
			parentProvided = provided
		}
	}

	// Build trie from relative layer sequences
	root := newTrieNode("")
	for _, childName := range children {
		seq := relativeLayerSequence(childName, parentProvided, result, layers, globalOrder, pixiBound)
		node := root
		for _, layer := range seq {
			child, ok := node.children[layer]
			if !ok {
				child = newTrieNode(layer)
				node.children[layer] = child
			}
			node = child
		}
		node.images = append(node.images, childName)
	}

	return walkTrieScoped(root, parentName, uid, defaultUID, result, origImages, layers, cfg, tag, globalOrder, pixiBound)
}

// relativeLayerSequence returns an image's layers minus what the parent provides,
// ordered according to the global layer order.
func relativeLayerSequence(imageName string, parentProvided map[string]bool, images map[string]*ResolvedImage, layers map[string]*Layer, globalOrder []string, pixiBound map[string]bool) []string {
	allLayers := collectAllImageLayers(imageName, images, layers)
	layerSet := make(map[string]bool, len(allLayers))
	for _, l := range allLayers {
		layerSet[l] = true
	}

	var seq []string
	for _, l := range globalOrder {
		if layerSet[l] && !parentProvided[l] && !pixiBound[l] {
			seq = append(seq, l)
		}
	}
	return seq
}

// walkTrieScoped walks the trie creating intermediates at branch points.
// User-defined images at branch points are reused as intermediates without rebasing.
// uid + defaultUID propagate from the sibling group so auto-intermediates
// inherit the right user context and get UID-suffixed names when needed.
func walkTrieScoped(node *trieNode, parentName string, uid, defaultUID int, result map[string]*ResolvedImage, origImages map[string]*ResolvedImage, layers map[string]*Layer, cfg *Config, tag string, globalOrder []string, pixiBound map[string]bool) error {
	for _, childLayerName := range sortedKeys(node.children) {
		child := node.children[childLayerName]

		// Collect linear chain: walk as long as exactly one child and no terminal images
		var pathLayers []string
		current := child
		pathLayers = append(pathLayers, childLayerName)

		for len(current.children) == 1 && len(current.images) == 0 {
			for layerName, next := range current.children {
				pathLayers = append(pathLayers, layerName)
				current = next
			}
		}

		// current is at a branch point, leaf, or has terminal images
		isBranch := len(current.children) >= 2 || (len(current.children) >= 1 && len(current.images) > 0)
		isLeaf := len(current.children) == 0

		if isBranch {
			// Count user-defined images at this branch point
			var userImages []string
			for _, img := range current.images {
				if _, isOrig := origImages[img]; isOrig {
					userImages = append(userImages, img)
				}
			}

			if len(userImages) == 1 && len(current.images) == 1 {
				// Single user image at branch: use it as intermediate, preserve its Base
				intermediateName := userImages[0]
				if err := walkTrieScoped(current, intermediateName, uid, defaultUID, result, origImages, layers, cfg, tag, globalOrder, pixiBound); err != nil {
					return err
				}
			} else {
				// 0 or 2+ user images: create auto-intermediate
				intermediateName := pickAutoName(pathLayers, parentName, uid, defaultUID, result, origImages)
				// Every terminal image in this subtree will base (directly or
				// transitively) on this intermediate, so it must carry the UNION
				// of their build formats / distro tags — a layer hoisted here whose
				// package section is keyed on a format only the consumers declare
				// would otherwise be silently dropped. See createIntermediate.
				consumerImages := collectSubtreeImages(current)
				createIntermediate(intermediateName, parentName, uid, pathLayers, consumerImages, result, origImages, cfg, tag, layers, globalOrder, pixiBound)
				// Rebase all terminal images to this intermediate
				for _, imgName := range current.images {
					updateImageBase(imgName, intermediateName, result)
				}
				if err := walkTrieScoped(current, intermediateName, uid, defaultUID, result, origImages, layers, cfg, tag, globalOrder, pixiBound); err != nil {
					return err
				}
			}
		} else if isLeaf {
			// Terminal images at leaf — rebase to current parent
			for _, imgName := range current.images {
				updateImageBase(imgName, parentName, result)
			}
		}
	}
	return nil
}

// collectSubtreeImages returns every terminal user image in the subtree rooted
// at node — the images terminating at node plus all images in descendant nodes.
// These are exactly the images that will base, directly or transitively, on an
// auto-intermediate created at this node, so they define the union of build
// formats / distro tags the intermediate must carry (see createIntermediate).
func collectSubtreeImages(node *trieNode) []string {
	out := append([]string(nil), node.images...)
	for _, child := range node.children {
		out = append(out, collectSubtreeImages(child)...)
	}
	return out
}

// pickAutoName chooses a name for an auto-intermediate using {parent}-{lastLayer}.
// For OCI refs (e.g. "quay.io/fedora/fedora:43"), extracts the short image name.
// When uid != defaultUID, appends "-uid<N>" so uid=0 and uid=1000 intermediates
// at the same trie position get distinct OCI tags (otherwise they'd collide
// and one group's HOME-baked ENV would poison the other).
// Appends -2, -3 etc. to avoid conflicts with existing or already-created images.
func pickAutoName(pathLayers []string, parentName string, uid, defaultUID int, result, origImages map[string]*ResolvedImage) string {
	lastLayer := pathLayers[len(pathLayers)-1]
	// Remote layer keys are fully-qualified paths
	// ("github.com/overthinkos/overthink/layers/pixi"); reduce to the short
	// layer name so the intermediate gets a valid, slash-free OCI image name
	// ("arch-pixi", not "arch-github.com/.../layers/pixi" — the latter is a
	// malformed ref that crashes buildah's content-summary on COPY/FROM). Local
	// layer keys are already short, so this is a no-op for them.
	if i := strings.LastIndex(lastLayer, "/"); i >= 0 {
		lastLayer = lastLayer[i+1:]
	}

	// Extract short parent name from OCI refs: "quay.io/fedora/fedora:43" → "fedora"
	shortParent := parentName
	if i := strings.LastIndex(shortParent, ":"); i >= 0 {
		shortParent = shortParent[:i]
	}
	if i := strings.LastIndex(shortParent, "/"); i >= 0 {
		shortParent = shortParent[i+1:]
	}

	baseName := shortParent + "-" + lastLayer
	if uid != defaultUID {
		baseName = fmt.Sprintf("%s-uid%d", baseName, uid)
	}
	name := baseName
	suffix := 2
	for {
		if _, exists := origImages[name]; !exists {
			if _, exists := result[name]; !exists {
				return name
			}
		}
		name = fmt.Sprintf("%s-%d", baseName, suffix)
		suffix++
	}
}

// createIntermediate creates an auto-generated intermediate image in the result map.
// uid is the sibling group's UID — it determines the intermediate's User/GID/Home
// so HOME-relative env/path_append expansion matches the children that will
// inherit from this intermediate.
func createIntermediate(name, parentName string, uid int, pathLayers []string, consumerImages []string, result map[string]*ResolvedImage, origImages map[string]*ResolvedImage, cfg *Config, tag string, layers map[string]*Layer, globalOrder []string, pixiBound map[string]bool) {
	ownLayers := computeOwnLayers(parentName, pathLayers, result, layers, globalOrder, pixiBound)

	isExternalBase := false
	if _, ok := result[parentName]; !ok {
		isExternalBase = true
	}

	platforms := resolvePlatforms(cfg)
	if parent, ok := result[parentName]; ok && len(parent.Platforms) > 0 {
		platforms = intersectPlatforms(parent.Platforms, platforms)
	}

	// Distro + BuildFormats MUST come from the parent first. Only non-auto
	// parents have their own values; external roots fall back to defaults.
	// This was previously inverted — defaults won even when the parent was
	// an explicit non-default base (e.g. arch with build: [pac]), so
	// every arch-rooted auto-intermediate got mis-tagged as build: [rpm]
	// and all `pac:`-only layer sections silently dropped out of the
	// generated Containerfile. Fixed by resolving parent first.
	var inheritedDistro []string
	var inheritedBuilds []string
	if parent, ok := result[parentName]; ok {
		inheritedDistro = parent.Distro
		inheritedBuilds = parent.BuildFormats
	}
	if len(inheritedDistro) == 0 {
		inheritedDistro = cfg.Defaults.Distro
	}
	if len(inheritedBuilds) == 0 {
		inheritedBuilds = []string(cfg.Defaults.Build)
	}

	// An auto-intermediate hosts layers hoisted out of its consuming images.
	// When a hoisted layer's package section is keyed on a build format (or
	// distro tag) the PARENT chain doesn't declare but a CONSUMER does — e.g.
	// the cachyos base is build:[pac] while selkies-labwc/openclaw-desktop are
	// build:[pac,aur] and the hoisted chrome layer needs aur for google-chrome —
	// parent-only inheritance silently drops that section (the AUR gate in
	// generate.go keys on BuildFormats). Union the parent's formats/distro with
	// every consuming descendant's, keeping the parent's primary format FIRST
	// (it drives img.Pkg + cache mounts below). No-op when consumers share the
	// parent's formats (the common case). Mirrors the parent-first inheritance
	// fix above for the orthogonal "format declared on children" case.
	buildSeen := make(map[string]bool, len(inheritedBuilds))
	for _, f := range inheritedBuilds {
		buildSeen[f] = true
	}
	distroSeen := make(map[string]bool, len(inheritedDistro))
	for _, d := range inheritedDistro {
		distroSeen[d] = true
	}
	for _, cname := range consumerImages {
		c, ok := result[cname]
		if !ok {
			c, ok = origImages[cname]
		}
		if !ok {
			continue
		}
		for _, f := range c.BuildFormats {
			if !buildSeen[f] {
				buildSeen[f] = true
				inheritedBuilds = append(inheritedBuilds, f)
			}
		}
		for _, d := range c.Distro {
			if !distroSeen[d] {
				distroSeen[d] = true
				inheritedDistro = append(inheritedDistro, d)
			}
		}
	}

	// Derive User/GID/Home from the sibling group's UID. uid=0 is root with
	// /root as HOME; any other UID reuses cfg.Defaults.User (typically "user")
	// and /home/<user>. This keeps HOME-relative ENV expansion consistent
	// with the child images that inherit this intermediate.
	var user string
	var gid int
	if uid == 0 {
		user = "root"
		gid = 0
	} else {
		user = cfg.Defaults.User
		if user == "" {
			user = "user"
		}
		gid = resolveIntPtr(cfg.Defaults.GID, nil, 1000)
	}

	// Builder map: defaults as the base, then the PARENT, then the CONSUMERS win.
	// The hoisted layers belong to the consumers, so the consumers' builder map is
	// authoritative for them. In the flat case the consumers inherit the parent's
	// builder (so they agree — consumer-wins is a no-op vs parent-wins). In the
	// import-namespace case the parent is a cross-namespace base (e.g.
	// cachyos.cachyos) whose builder refs are relative to ITS namespace
	// (`ov.arch-builder`) and do NOT resolve in this context; the consumers carry
	// the correct context-local builder (`arch-builder`), so consumer-wins is what
	// lets the hoisted AUR layer (chrome's google-chrome) find its builder instead
	// of failing with "needs builder aur but no builders.aur configured".
	builderMap := make(BuilderMap)
	for k, v := range cfg.Defaults.Builder {
		builderMap[k] = v
	}
	// Distro-keyed default — the SAME mechanism ResolveImage /
	// resolveEffectiveBuilder use: a cachyos/Arch intermediate seeds
	// arch-builder from the root-namespace distro image, so it never falls back
	// to the Fedora default even before the consumer-wins merge below (which
	// remains authoritative for the hoisted layers).
	for k, v := range cfg.distroBuilderMap(inheritedDistro) {
		builderMap[k] = v
	}
	if parent, ok := result[parentName]; ok {
		for k, v := range parent.Builder {
			builderMap[k] = v
		}
	}
	for _, cname := range consumerImages {
		c, ok := result[cname]
		if !ok {
			c, ok = origImages[cname]
		}
		if !ok {
			continue
		}
		for k, v := range c.Builder {
			builderMap[k] = v
		}
	}

	img := &ResolvedImage{
		Name:           name,
		Base:           parentName,
		IsExternalBase: isExternalBase,
		Layer:          ownLayers,
		Tag:            tag,
		Registry:       cfg.Defaults.Registry,
		Distro:         inheritedDistro,
		BuildFormats:   inheritedBuilds,
		Platforms:      platforms,
		User:           user,
		UID:            uid,
		GID:            gid,
		Merge:          cfg.Defaults.Merge,
		Builder:        builderMap,
		Auto:           true,
	}
	if len(img.BuildFormats) == 0 {
		fmt.Fprintf(os.Stderr, "Warning: auto-intermediate %s has no build formats (set build: in defaults)\n", name)
		return
	}
	img.Pkg = img.BuildFormats[0]
	// Inherit format configs from parent image (auto-intermediates share the same configs)
	if parent, ok := result[parentName]; ok {
		img.DistroConfig = parent.DistroConfig
		img.DistroDef = parent.DistroDef
		img.BuilderConfig = parent.BuilderConfig
	}
	// Build unified Tags: ["all"] + Distro + BuildFormats
	img.Tags = append([]string{"all"}, img.Distro...)
	img.Tags = append(img.Tags, img.BuildFormats...)
	if img.User == "root" {
		img.Home = "/root"
	} else {
		img.Home = fmt.Sprintf("/home/%s", img.User)
	}
	if img.Registry != "" {
		img.FullTag = fmt.Sprintf("%s/%s:%s", img.Registry, name, tag)
	} else {
		img.FullTag = fmt.Sprintf("%s:%s", name, tag)
	}

	result[name] = img
}

// computeOwnLayers determines which layers an intermediate needs to install
// (pathLayers minus what the parent already provides).
func computeOwnLayers(parentName string, pathLayers []string, result map[string]*ResolvedImage, layers map[string]*Layer, globalOrder []string, pixiBound map[string]bool) []string {
	parentProvided := make(map[string]bool)
	if _, ok := result[parentName]; ok {
		provided, err := LayerProvidedByImage(parentName, result, layers)
		if err == nil {
			parentProvided = provided
		}
	}

	var own []string
	for _, l := range pathLayers {
		if !parentProvided[l] {
			own = append(own, l)
		}
	}

	// Also include transitive dependencies of these layers that aren't parent-provided
	needed := make(map[string]bool)
	for _, l := range own {
		needed[l] = true
		addTransitiveDeps(l, layers, needed, parentProvided)
	}

	// Return in global order, excluding pixi-bound layers
	var ordered []string
	for _, l := range globalOrder {
		if needed[l] && !parentProvided[l] && !pixiBound[l] {
			ordered = append(ordered, l)
		}
	}
	if len(ordered) == 0 {
		return own // fallback
	}
	return ordered
}

// addTransitiveDeps adds all transitive dependencies of a layer to the needed set.
func addTransitiveDeps(layerName string, layers map[string]*Layer, needed map[string]bool, excluded map[string]bool) {
	layer, ok := layers[layerName]
	if !ok {
		return
	}
	for _, depRef := range layer.Require {
		dep := depRef.Bare()
		if excluded[dep] || needed[dep] {
			continue
		}
		needed[dep] = true
		addTransitiveDeps(dep, layers, needed, excluded)
	}
	for _, includedRef := range layer.IncludedLayer {
		included := includedRef.Bare()
		if excluded[included] || needed[included] {
			continue
		}
		needed[included] = true
		addTransitiveDeps(included, layers, needed, excluded)
	}
}

// updateImageBase updates an image's Base to point to the given parent.
func updateImageBase(imgName, parentName string, result map[string]*ResolvedImage) {
	img, ok := result[imgName]
	if !ok {
		return
	}
	img.Base = parentName
	if _, isInternal := result[parentName]; isInternal {
		img.IsExternalBase = false
	} else {
		img.IsExternalBase = true
	}
}

// resolvePlatforms returns platforms from config defaults.
func resolvePlatforms(cfg *Config) []string {
	if len(cfg.Defaults.Platforms) > 0 {
		return cfg.Defaults.Platforms
	}
	return []string{"linux/amd64", "linux/arm64"}
}

// intersectPlatforms returns platforms present in both slices.
// If the intersection is empty, returns parent (the more restrictive set).
func intersectPlatforms(parent, defaults []string) []string {
	set := make(map[string]bool, len(parent))
	for _, p := range parent {
		set[p] = true
	}
	var result []string
	for _, p := range defaults {
		if set[p] {
			result = append(result, p)
		}
	}
	if len(result) == 0 {
		return parent
	}
	return result
}

// sortedKeys returns sorted keys from a map.
func sortedKeys(m map[string]*trieNode) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sortStrings(keys)
	return keys
}
