package main

import (
	"fmt"
	"strings"
)

// namespace.go — the Go-inspired hierarchical-namespace resolver.
//
// The `import:` statement (see unified.go) mounts another project under a child
// namespace (`import: [{cachyos: '@github.com/overthinkos/cachyos:vTAG'}]`).
// Entries in that project are then referenced QUALIFIED — `base: cachyos.cachyos`,
// `builder: {pixi: ov.arch-builder}` — rather than flat-merged into the importing
// project's global per-kind maps.
//
// Resolution is namespace-relative (Go package-member semantics): a bare ref
// inside namespace `cachyos` resolves within cachyos first; a qualified ref
// `ov.arch` inside cachyos descends into cachyos's own `ov` namespace. The
// resolver below walks Config.Namespaces (projected from UnifiedFile.Namespaces).
//
// Inheritance across a namespace boundary:
//   - distro:/build: are VALUES (tags, formats) → inherited across namespaces.
//   - builder: is a map of REFS relative to the base's namespace → NOT inherited
//     across a boundary (the consumer declares its own builder map). This avoids
//     leaking a base-namespace-relative ref (`ov.arch-builder`) into a consumer
//     where that namespace doesn't exist.

// splitNamespaceRef splits a qualified ref on its FIRST `.` into (namespace,
// remainder). A bare ref (no dot, or a leading/trailing dot) returns ok=false.
// The remainder may itself be qualified (`a.b.c` → "a", "b.c").
func splitNamespaceRef(ref string) (ns, rest string, ok bool) {
	i := strings.IndexByte(ref, '.')
	if i <= 0 || i >= len(ref)-1 {
		return "", "", false
	}
	return ref[:i], ref[i+1:], true
}

// resolveImageRef resolves a (possibly qualified) image name to its ImageConfig
// and the Config (namespace context) it lives in. Bare names resolve in c;
// `ns.name` descends into c.Namespaces[ns] recursively.
func (c *Config) resolveImageRef(ref string) (ImageConfig, *Config, bool) {
	if ns, rest, ok := splitNamespaceRef(ref); ok {
		sub, ok := c.Namespaces[ns]
		if !ok {
			return ImageConfig{}, nil, false
		}
		return sub.resolveImageRef(rest)
	}
	img, ok := c.Image[ref]
	if !ok {
		return ImageConfig{}, nil, false
	}
	return img, c, true
}

// resolveLocalRef resolves a (possibly qualified) kind:local template ref.
func (c *Config) resolveLocalRef(ref string) (*LocalSpec, bool) {
	if ns, rest, ok := splitNamespaceRef(ref); ok {
		sub, ok := c.Namespaces[ns]
		if !ok {
			return nil, false
		}
		return sub.resolveLocalRef(rest)
	}
	l, ok := c.Local[ref]
	return l, ok
}

// resolveNamespacedBases pulls every namespace-qualified base referenced by the
// already-resolved local image set into `out` (keyed by the fully-qualified
// name), resolving each within its own namespace context. Iterates to a fixpoint
// because a pulled-in image may itself reference a (deeper) namespaced base.
func (c *Config) resolveNamespacedBases(out map[string]*ResolvedImage, calverTag, dir string, opts ResolveOpts) error {
	for {
		var todo []string
		add := func(ref string) {
			if _, ok := out[ref]; ok {
				return
			}
			if _, _, qualified := splitNamespaceRef(ref); qualified {
				todo = append(todo, ref)
			}
		}
		for _, ri := range out {
			if !ri.IsExternalBase {
				add(ri.Base)
			}
			// Qualified builder refs (e.g. a submodule image's
			// `builder: {pixi: ov.arch-builder}`) are pulled in so the generator
			// can resolve the builder stage's FROM — but ONLY for images that
			// actually have layers to build. A layerless base (e.g. cachyos.cachyos)
			// needs no builder, and its builder map is namespace-relative to ITS
			// own namespace (so the ref would not resolve from the root context).
			// A local/layered image's builder refs ARE relative to the config
			// being resolved, so they resolve correctly.
			if len(ri.Layer) > 0 {
				for _, b := range ri.Builder.AllBuilder() {
					add(b)
				}
			}
		}
		if len(todo) == 0 {
			return nil
		}
		for _, qref := range todo {
			if _, ok := out[qref]; ok {
				continue
			}
			if err := c.pullNamespacedImage(c, qref, "", calverTag, dir, opts, out); err != nil {
				return err
			}
		}
	}
}

// pullNamespacedImage resolves `ref` (possibly qualified, relative to base
// Config `from`) and stores it in `out` under its fully-qualified key
// (keyPrefix + descended namespaces + leaf). Re-keys the entry's own internal
// base so the build graph references the fully-qualified ancestor, and recurses
// to pull that ancestor too.
func (c *Config) pullNamespacedImage(from *Config, ref, keyPrefix, calverTag, dir string, opts ResolveOpts, out map[string]*ResolvedImage) error {
	cur := from
	curPrefix := keyPrefix
	for {
		ns, rest, qualified := splitNamespaceRef(ref)
		if !qualified {
			break
		}
		child, ok := cur.Namespaces[ns]
		if !ok {
			return fmt.Errorf("import namespace %q not found (resolving %q)", ns, keyPrefix+ref)
		}
		cur = child
		curPrefix += ns + "."
		ref = rest
	}
	fullKey := curPrefix + ref
	if _, ok := out[fullKey]; ok {
		return nil
	}
	if _, ok := cur.Image[ref]; !ok {
		return fmt.Errorf("imported image %q not found in namespace", fullKey)
	}
	ri, err := cur.ResolveImage(ref, calverTag, dir, opts)
	if err != nil {
		return fmt.Errorf("resolving imported image %q: %w", fullKey, err)
	}
	if ri.IsExternalBase {
		out[fullKey] = ri
		return nil
	}
	// Internal base within `cur` — re-key it fully-qualified, store, recurse.
	origBase := ri.Base
	ri.Base = curPrefix + origBase
	out[fullKey] = ri
	return c.pullNamespacedImage(cur, origBase, curPrefix, calverTag, dir, opts, out)
}
