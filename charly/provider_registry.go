package main

import (
	"fmt"
	"io"
	"sort"
	"sync"
)

// providerRegistry is the ONE process-wide registry of Providers — the
// generalization of the three hand-maintained reserved-word maps
// (reservedKindHandlers + VerbCatalog + liveVerbDispatch in reserved_registry.go).
// Built-ins register from init() (RegisterBuiltinProvider); plugins register
// lazily after the loader connects them (RegisterPluginProviders). Every reserved
// word resolves through here regardless of transport.
//
// C0 scope: the registry + the plugin path are PERMANENT architecture. The
// built-in dispatch switches (checkrun.go runOne, node_normalize.go, …) are NOT
// migrated in C0 — they are converted to register their handlers here one cutover
// at a time (C1–C5). So in C0 the registry holds the plugin-contributed providers
// (its first consumer is the example plugin), and the built-in maps still drive
// built-in dispatch; the call sites consult the registry as a fall-through for any
// reserved word the built-in switch does not handle.
var providerRegistry = newRegistry()

// Registry maps (class, reserved-word) → Provider. Keyed by both because a word
// may exist in two classes (e.g. "k8s" is both a kind and a verb).
type Registry struct {
	mu      sync.RWMutex
	byKey   map[string]Provider
	origins map[string]string // key → "builtin" | "github.com/org/repo@tag" | "local:<bin>"
	closers []io.Closer       // plugin connections, closed by Close()
}

func newRegistry() *Registry {
	return &Registry{byKey: map[string]Provider{}, origins: map[string]string{}}
}

func provKey(c ProviderClass, word string) string { return string(c) + ":" + word }

// register indexes one provider. It is the single mutation path (R3): it rejects
// an unknown class and a duplicate (class, word) — fail-fast, like
// registerCueKind's duplicate panic.
func (r *Registry) register(p Provider, origin string) error {
	class, word := p.Class(), p.Reserved()
	if !providerClasses[class] {
		return fmt.Errorf("provider %q: unknown class %q", word, class)
	}
	if word == "" {
		return fmt.Errorf("provider (class %q): empty reserved word", class)
	}
	k := provKey(class, word)
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.byKey[k]; dup {
		return fmt.Errorf("provider %s already registered (origin %s) — refusing duplicate from %s",
			k, r.origins[k], origin)
	}
	r.byKey[k] = p
	r.origins[k] = origin
	return nil
}

// RegisterBuiltinProvider is called from init() for an in-process built-in. It
// panics on conflict (a startup invariant, like the bijection gate) — a built-in
// duplicate is a programming error caught at process start.
func RegisterBuiltinProvider(p Provider) {
	if err := providerRegistry.register(p, "builtin"); err != nil {
		panic("RegisterBuiltinProvider: " + err.Error())
	}
}

// RegisterPluginProviders indexes the (already-connected) out-of-process
// providers of one plugin, tracking the connection for Close(). Unlike a built-in
// it returns an error rather than panicking — a misbehaving third-party plugin
// must not crash charly; the loader surfaces the error and skips the plugin.
func (r *Registry) RegisterPluginProviders(ps []Provider, origin string, conn io.Closer) error {
	for _, p := range ps {
		if err := r.register(p, origin); err != nil {
			return err
		}
	}
	if conn != nil {
		r.mu.Lock()
		r.closers = append(r.closers, conn)
		r.mu.Unlock()
	}
	return nil
}

// resolve returns the provider for (class, word), if any.
func (r *Registry) resolve(class ProviderClass, word string) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.byKey[provKey(class, word)]
	return p, ok
}

// Typed resolvers — what the call sites use. They never branch on transport.
func (r *Registry) ResolveVerb(word string) (Provider, bool) { return r.resolve(ClassVerb, word) }
func (r *Registry) ResolveKind(word string) (Provider, bool) { return r.resolve(ClassKind, word) }
func (r *Registry) ResolveDeploy(word string) (Provider, bool) {
	return r.resolve(ClassDeployTarget, word)
}
func (r *Registry) ResolveStep(word string) (Provider, bool)    { return r.resolve(ClassStep, word) }
func (r *Registry) ResolveBuilder(word string) (Provider, bool) { return r.resolve(ClassBuilder, word) }

// allProviders returns every registered provider (sorted by key) — used by
// `charly __plugin serve` to expose the in-proc set over gRPC.
func (r *Registry) allProviders() []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	keys := make([]string, 0, len(r.byKey))
	for k := range r.byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]Provider, 0, len(keys))
	for _, k := range keys {
		out = append(out, r.byKey[k])
	}
	return out
}

// Close shuts down every plugin connection (each go-plugin client; the in-venue
// server auto-exits on socket close — see plugin_transport.go).
func (r *Registry) Close() error {
	r.mu.Lock()
	closers := r.closers
	r.closers = nil
	r.mu.Unlock()
	var firstErr error
	for _, c := range closers {
		if err := c.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
