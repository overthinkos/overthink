package main

import (
	"fmt"
	"strings"
)

// trieNode represents a node in the layer prefix trie.
type trieNode struct {
	layer    string                // layer at this position ("" for root)
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
		resolved, err := ResolveLayerOrder(img.Layers, layers, nil)
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

	// Build dependency graph from layer depends
	// Only include layers that appear in at least one image
	graph := make(map[string][]string)
	for name := range popularity {
		layer, ok := layers[name]
		if !ok {
			continue
		}
		var deps []string
		for _, dep := range layer.Depends {
			if _, inUse := popularity[dep]; inUse {
				deps = append(deps, dep)
			}
		}
		graph[name] = deps
	}

	// Kahn's algorithm with popularity-based tie-breaking
	return topoSortByPopularity(graph, popularity)
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
		resolved, err := ResolveLayerOrder(img.Layers, layers, nil)
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

// ComputeIntermediates analyzes all images, groups them by direct parent (Base),
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

	builderName := cfg.Defaults.Builder

	// Group images by their direct parent (Base field)
	siblingGroups := make(map[string][]string)
	for name, img := range images {
		if name == builderName {
			continue
		}
		siblingGroups[img.Base] = append(siblingGroups[img.Base], name)
	}

	// Process internal-base groups in topological order (parents before children)
	// so auto-intermediates from parent groups are visible when processing child groups
	imageOrder, err := ResolveImageOrder(images, layers)
	if err != nil {
		return nil, fmt.Errorf("resolving image order: %w", err)
	}

	processed := make(map[string]bool)
	for _, parentName := range imageOrder {
		children := siblingGroups[parentName]
		if len(children) < 2 {
			continue
		}
		processed[parentName] = true
		if err := processSiblingGroup(parentName, children, result, images, layers, cfg, tag, globalOrder); err != nil {
			return nil, err
		}
	}

	// Process external-base groups (parent is an external OCI ref, not in imageOrder)
	for parentBase, children := range siblingGroups {
		if processed[parentBase] || len(children) < 2 {
			continue
		}
		if err := processSiblingGroup(parentBase, children, result, images, layers, cfg, tag, globalOrder); err != nil {
			return nil, err
		}
	}

	return result, nil
}

// processSiblingGroup builds a prefix trie from the relative layer sequences
// of children sharing the same parent, and creates intermediates at branch points.
func processSiblingGroup(parentName string, children []string, result, origImages map[string]*ResolvedImage, layers map[string]*Layer, cfg *Config, tag string, globalOrder []string) error {
	sortStrings(children)

	// Get layers provided by parent
	parentProvided := make(map[string]bool)
	if _, ok := result[parentName]; ok {
		provided, err := LayersProvidedByImage(parentName, result, layers)
		if err == nil {
			parentProvided = provided
		}
	}

	// Build trie from relative layer sequences
	root := newTrieNode("")
	for _, childName := range children {
		seq := relativeLayerSequence(childName, parentProvided, result, layers, globalOrder)
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

	return walkTrieScoped(root, parentName, result, origImages, layers, cfg, tag, globalOrder)
}

// relativeLayerSequence returns an image's layers minus what the parent provides,
// ordered according to the global layer order.
func relativeLayerSequence(imageName string, parentProvided map[string]bool, images map[string]*ResolvedImage, layers map[string]*Layer, globalOrder []string) []string {
	allLayers := collectAllImageLayers(imageName, images, layers)
	layerSet := make(map[string]bool, len(allLayers))
	for _, l := range allLayers {
		layerSet[l] = true
	}

	var seq []string
	for _, l := range globalOrder {
		if layerSet[l] && !parentProvided[l] {
			seq = append(seq, l)
		}
	}
	return seq
}

// walkTrieScoped walks the trie creating intermediates at branch points.
// User-defined images at branch points are reused as intermediates without rebasing.
func walkTrieScoped(node *trieNode, parentName string, result map[string]*ResolvedImage, origImages map[string]*ResolvedImage, layers map[string]*Layer, cfg *Config, tag string, globalOrder []string) error {
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
				if err := walkTrieScoped(current, intermediateName, result, origImages, layers, cfg, tag, globalOrder); err != nil {
					return err
				}
			} else {
				// 0 or 2+ user images: create auto-intermediate
				intermediateName := pickAutoName(pathLayers, parentName, result, origImages)
				createIntermediate(intermediateName, parentName, pathLayers, result, origImages, cfg, tag, layers, globalOrder)
				// Rebase all terminal images to this intermediate
				for _, imgName := range current.images {
					updateImageBase(imgName, intermediateName, result)
				}
				if err := walkTrieScoped(current, intermediateName, result, origImages, layers, cfg, tag, globalOrder); err != nil {
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

// pickAutoName chooses a name for an auto-intermediate using {parent}-{lastLayer}.
// For OCI refs (e.g. "quay.io/fedora/fedora:43"), extracts the short image name.
// Appends -2, -3 etc. to avoid conflicts with existing or already-created images.
func pickAutoName(pathLayers []string, parentName string, result, origImages map[string]*ResolvedImage) string {
	lastLayer := pathLayers[len(pathLayers)-1]

	// Extract short parent name from OCI refs: "quay.io/fedora/fedora:43" → "fedora"
	shortParent := parentName
	if i := strings.LastIndex(shortParent, ":"); i >= 0 {
		shortParent = shortParent[:i]
	}
	if i := strings.LastIndex(shortParent, "/"); i >= 0 {
		shortParent = shortParent[i+1:]
	}

	baseName := shortParent + "-" + lastLayer
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
func createIntermediate(name, parentName string, pathLayers []string, result map[string]*ResolvedImage, origImages map[string]*ResolvedImage, cfg *Config, tag string, layers map[string]*Layer, globalOrder []string) {
	ownLayers := computeOwnLayers(parentName, pathLayers, result, layers, globalOrder)

	isExternalBase := false
	if _, ok := result[parentName]; !ok {
		isExternalBase = true
	}

	platforms := resolvePlatforms(cfg)
	if parent, ok := result[parentName]; ok && len(parent.Platforms) > 0 {
		platforms = intersectPlatforms(parent.Platforms, platforms)
	}

	img := &ResolvedImage{
		Name:           name,
		Base:           parentName,
		IsExternalBase: isExternalBase,
		Layers:         ownLayers,
		Tag:            tag,
		Registry:       cfg.Defaults.Registry,
		Pkg:            cfg.Defaults.Pkg,
		Platforms:      platforms,
		User:           cfg.Defaults.User,
		UID:            resolveIntPtr(cfg.Defaults.UID, nil, 1000),
		GID:            resolveIntPtr(cfg.Defaults.GID, nil, 1000),
		Merge:          cfg.Defaults.Merge,
		Builder:        cfg.Defaults.Builder,
		Auto:           true,
	}
	if img.Pkg == "" {
		img.Pkg = "rpm"
	}
	if img.User == "" {
		img.User = "user"
	}
	img.Home = fmt.Sprintf("/home/%s", img.User)
	if img.Registry != "" {
		img.FullTag = fmt.Sprintf("%s/%s:%s", img.Registry, name, tag)
	} else {
		img.FullTag = fmt.Sprintf("%s:%s", name, tag)
	}

	result[name] = img
}

// computeOwnLayers determines which layers an intermediate needs to install
// (pathLayers minus what the parent already provides).
func computeOwnLayers(parentName string, pathLayers []string, result map[string]*ResolvedImage, layers map[string]*Layer, globalOrder []string) []string {
	parentProvided := make(map[string]bool)
	if _, ok := result[parentName]; ok {
		provided, err := LayersProvidedByImage(parentName, result, layers)
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

	// Return in global order
	var ordered []string
	for _, l := range globalOrder {
		if needed[l] && !parentProvided[l] {
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
	for _, dep := range layer.Depends {
		if excluded[dep] || needed[dep] {
			continue
		}
		needed[dep] = true
		addTransitiveDeps(dep, layers, needed, excluded)
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
