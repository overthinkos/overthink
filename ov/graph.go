package main

import (
	"fmt"
	"strings"
)

// CycleError represents a circular dependency error
type CycleError struct {
	Cycle []string
}

func (e *CycleError) Error() string {
	return fmt.Sprintf("circular dependency: %s", strings.Join(e.Cycle, " -> "))
}

// ExpandLayers expands layer composition references (layers: field in layer.yml).
// For each layer that has IncludedLayers, recursively inserts them into the result.
// Layers without content (no install files, no env/ports/etc.) are omitted.
// Returns a flat, deduplicated layer list.
func ExpandLayers(requested []string, layers map[string]*Layer) ([]string, error) {
	var result []string
	seen := make(map[string]bool)
	expanding := make(map[string]bool)

	var expand func(name string) error
	expand = func(name string) error {
		if seen[name] {
			return nil
		}
		if expanding[name] {
			return fmt.Errorf("circular layer composition: %s", name)
		}

		layer, ok := layers[name]
		if !ok {
			// Unknown layer — pass through for ResolveLayerOrder to report
			seen[name] = true
			result = append(result, name)
			return nil
		}

		if len(layer.IncludedLayers) > 0 {
			expanding[name] = true
			for _, included := range layer.IncludedLayers {
				if err := expand(included); err != nil {
					return err
				}
			}
			expanding[name] = false
			seen[name] = true
			// Composing layers only appear in result if they also have content
			if layer.HasContent() {
				result = append(result, name)
			}
		} else {
			// Regular layer — always include
			seen[name] = true
			result = append(result, name)
		}
		return nil
	}

	for _, name := range requested {
		if err := expand(name); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// ResolveLayerOrder resolves layer dependencies and returns them in topological order.
// It takes the explicitly requested layers and the full layer map, then:
// 1. Expands layer composition (layers: field)
// 2. Transitively resolves all dependencies
// 3. Topologically sorts the result
// 4. Returns layers in install order (dependencies before dependents)
//
// parentLayers contains layers already installed by parent images (via base chain).
// These are excluded from the returned order.
func ResolveLayerOrder(requested []string, layers map[string]*Layer, parentLayers map[string]bool) ([]string, error) {
	// Expand layer composition first
	expanded, err := ExpandLayers(requested, layers)
	if err != nil {
		return nil, err
	}

	// Build the set of all layers we need (transitive closure)
	needed := make(map[string]bool)
	visiting := make(map[string]bool) // Track current path for cycle detection

	var addTransitive func(name string, path []string) error
	addTransitive = func(name string, path []string) error {
		if needed[name] {
			return nil
		}
		if parentLayers[name] {
			// Already provided by parent, skip
			return nil
		}

		// Check for cycle
		if visiting[name] {
			cycle := append(path, name)
			return &CycleError{Cycle: cycle}
		}

		layer, ok := layers[name]
		if !ok {
			return fmt.Errorf("unknown layer %q", name)
		}

		visiting[name] = true
		newPath := append(path, name)

		// Add included layers (composition)
		for _, included := range layer.IncludedLayers {
			if err := addTransitive(included, newPath); err != nil {
				return err
			}
		}

		// Add dependencies
		for _, dep := range layer.Depends {
			if err := addTransitive(dep, newPath); err != nil {
				return err
			}
		}

		visiting[name] = false
		// Composing layers without content don't need to be built
		if len(layer.IncludedLayers) == 0 || layer.HasContent() {
			needed[name] = true
		}
		return nil
	}

	for _, name := range expanded {
		if err := addTransitive(name, nil); err != nil {
			return nil, err
		}
	}

	// Build adjacency list for topological sort
	// Edge from A to B means A depends on B (B must come before A)
	graph := make(map[string][]string)

	// resolveDepEdges returns the effective dependencies for a dep reference,
	// expanding through composing layers that aren't in the needed set.
	var resolveDepEdges func(dep string) []string
	resolveDepEdges = func(dep string) []string {
		if needed[dep] {
			return []string{dep}
		}
		// dep is a composing layer not in needed — inherit its included layers
		layer, ok := layers[dep]
		if !ok {
			return nil
		}
		var edges []string
		for _, included := range layer.IncludedLayers {
			edges = append(edges, resolveDepEdges(included)...)
		}
		return edges
	}

	for name := range needed {
		layer := layers[name]
		var deps []string
		for _, dep := range layer.Depends {
			deps = append(deps, resolveDepEdges(dep)...)
		}
		// Included layers that have content are also dependencies (must install before)
		for _, included := range layer.IncludedLayers {
			deps = append(deps, resolveDepEdges(included)...)
		}
		graph[name] = deps
	}

	// Topological sort using Kahn's algorithm
	return topoSort(graph)
}

// ImageNeedsBuilder returns true if any of the image's own resolved layers
// (excluding parent-provided) have pixi.toml, package.json, or Cargo.toml.
// When layers is nil, falls back to unconditional builder dependency.
func ImageNeedsBuilder(img *ResolvedImage, images map[string]*ResolvedImage, layers map[string]*Layer) bool {
	if layers == nil {
		return true // conservative fallback
	}

	// Get parent-provided layers
	var parentLayers map[string]bool
	if !img.IsExternalBase {
		var err error
		parentLayers, err = LayersProvidedByImage(img.Base, images, layers)
		if err != nil {
			return true // conservative
		}
	}

	// Resolve this image's own layers (excluding parent)
	resolved, err := ResolveLayerOrder(img.Layers, layers, parentLayers)
	if err != nil {
		return true // conservative
	}

	for _, layerName := range resolved {
		layer, ok := layers[layerName]
		if !ok {
			continue
		}
		// Check file-based builder triggers
		if layer.PixiManifest() != "" || layer.HasPackageJson || layer.HasCargoToml {
			return true
		}
		// Check config-based builder triggers (any format with a matching builder)
		if layer.HasFormatPackages() {
			return true
		}
	}
	return false
}

// ResolveImageOrder resolves image dependencies and returns them in build order.
// Images that reference other images via `base` create dependencies.
// Each image's Builder field determines its builder dependency.
// Pass layers to enable conditional builder dependency; nil for unconditional.
func ResolveImageOrder(images map[string]*ResolvedImage, layers map[string]*Layer) ([]string, error) {
	// Build adjacency list
	// Edge from A to B means A depends on B (B must be built before A)
	graph := make(map[string][]string)
	for name, img := range images {
		var deps []string
		if !img.IsExternalBase {
			// base is another image in image.yml
			deps = append(deps, img.Base)
		}
		// Collect all builder images this image may depend on
		if ImageNeedsBuilder(img, images, layers) {
			for _, builder := range img.Builder.AllBuilders() {
				if builder != name {
					if _, ok := images[builder]; ok {
						deps = append(deps, builder)
					}
				}
			}
		}
		graph[name] = deps
	}

	return topoSort(graph)
}

// topoSort performs topological sort using Kahn's algorithm.
// Returns nodes in dependency order (dependencies before dependents).
func topoSort(graph map[string][]string) ([]string, error) {
	// Calculate in-degrees
	inDegree := make(map[string]int)
	for node := range graph {
		if _, ok := inDegree[node]; !ok {
			inDegree[node] = 0
		}
		for _, dep := range graph[node] {
			inDegree[dep] = inDegree[dep] // ensure dep exists in map
		}
	}

	// For each edge A -> B (A depends on B), increment in-degree of A
	// (because B must come before A)
	reverseGraph := make(map[string][]string)
	for node, deps := range graph {
		for _, dep := range deps {
			reverseGraph[dep] = append(reverseGraph[dep], node)
		}
	}

	// Recalculate in-degrees based on reverse graph
	for node := range graph {
		inDegree[node] = len(graph[node])
	}

	// Find all nodes with no dependencies (in-degree 0)
	var queue []string
	for node, degree := range inDegree {
		if degree == 0 {
			queue = append(queue, node)
		}
	}

	// Sort queue for deterministic output
	sortStrings(queue)

	var result []string
	for len(queue) > 0 {
		// Take the first node (lexicographically smallest for determinism)
		node := queue[0]
		queue = queue[1:]
		result = append(result, node)

		// For each node that depends on this one, decrement in-degree
		dependents := reverseGraph[node]
		sortStrings(dependents) // for determinism
		for _, dependent := range dependents {
			inDegree[dependent]--
			if inDegree[dependent] == 0 {
				queue = append(queue, dependent)
				sortStrings(queue) // maintain sorted order
			}
		}
	}

	// Check for cycles
	if len(result) != len(graph) {
		// Find a cycle for error reporting
		cycle := findCycle(graph, inDegree)
		return nil, &CycleError{Cycle: cycle}
	}

	return result, nil
}

// topoLevels performs topological sort and groups nodes by level.
// Nodes at the same level have no dependencies on each other and can be processed concurrently.
// Returns levels in dependency order (level 0 has no dependencies).
func topoLevels(graph map[string][]string) ([][]string, error) {
	// Calculate in-degrees
	inDegree := make(map[string]int)
	reverseGraph := make(map[string][]string)
	for node := range graph {
		inDegree[node] = len(graph[node])
		for _, dep := range graph[node] {
			reverseGraph[dep] = append(reverseGraph[dep], node)
		}
	}

	// Find all nodes with no dependencies (in-degree 0)
	var queue []string
	for node, degree := range inDegree {
		if degree == 0 {
			queue = append(queue, node)
		}
	}
	sortStrings(queue)

	var levels [][]string
	for len(queue) > 0 {
		// All nodes in queue form the current level
		level := make([]string, len(queue))
		copy(level, queue)
		levels = append(levels, level)

		var nextQueue []string
		for _, node := range queue {
			dependents := reverseGraph[node]
			sortStrings(dependents)
			for _, dependent := range dependents {
				inDegree[dependent]--
				if inDegree[dependent] == 0 {
					nextQueue = append(nextQueue, dependent)
				}
			}
		}
		sortStrings(nextQueue)
		queue = nextQueue
	}

	// Check for cycles
	total := 0
	for _, level := range levels {
		total += len(level)
	}
	if total != len(graph) {
		cycle := findCycle(graph, inDegree)
		return nil, &CycleError{Cycle: cycle}
	}

	return levels, nil
}

// ResolveImageLevels resolves image dependencies and returns them grouped by build level.
// Images at the same level can be built concurrently.
func ResolveImageLevels(images map[string]*ResolvedImage, layers map[string]*Layer) ([][]string, error) {
	graph := make(map[string][]string)
	for name, img := range images {
		var deps []string
		if !img.IsExternalBase {
			deps = append(deps, img.Base)
		}
		if ImageNeedsBuilder(img, images, layers) {
			for _, builder := range img.Builder.AllBuilders() {
				if builder != name {
					if _, ok := images[builder]; ok {
						deps = append(deps, builder)
					}
				}
			}
		}
		graph[name] = deps
	}

	return topoLevels(graph)
}

// findCycle finds a cycle in the graph for error reporting
func findCycle(graph map[string][]string, inDegree map[string]int) []string {
	// Start from any node still in the graph (non-zero in-degree)
	var start string
	for node, degree := range inDegree {
		if degree > 0 {
			start = node
			break
		}
	}

	// DFS to find cycle
	visited := make(map[string]bool)
	path := make(map[string]bool)
	var cyclePath []string

	var dfs func(node string) bool
	dfs = func(node string) bool {
		visited[node] = true
		path[node] = true
		cyclePath = append(cyclePath, node)

		for _, dep := range graph[node] {
			if !visited[dep] {
				if dfs(dep) {
					return true
				}
			} else if path[dep] {
				// Found cycle
				cyclePath = append(cyclePath, dep)
				return true
			}
		}

		path[node] = false
		cyclePath = cyclePath[:len(cyclePath)-1]
		return false
	}

	if start != "" {
		dfs(start)
	}

	return cyclePath
}

// LayersProvidedByImage returns the set of layers installed by an image
// (including those inherited from parent images via base chain)
func LayersProvidedByImage(imageName string, images map[string]*ResolvedImage, layers map[string]*Layer) (map[string]bool, error) {
	provided := make(map[string]bool)
	visited := make(map[string]bool)

	var collect func(name string) error
	collect = func(name string) error {
		if visited[name] {
			return fmt.Errorf("image cycle detected at %q", name)
		}
		visited[name] = true

		img, ok := images[name]
		if !ok {
			return fmt.Errorf("image %q not found", name)
		}

		// If base is internal, collect from parent first
		if !img.IsExternalBase {
			if err := collect(img.Base); err != nil {
				return err
			}
		}

		// Add this image's layers (expand composition)
		expanded, _ := ExpandLayers(img.Layers, layers)
		for _, layerName := range expanded {
			provided[layerName] = true
		}
		// Also mark composing layer names as provided
		for _, layerName := range img.Layers {
			provided[layerName] = true
		}

		return nil
	}

	if err := collect(imageName); err != nil {
		return nil, err
	}

	return provided, nil
}
