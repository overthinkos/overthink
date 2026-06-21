package main

import "fmt"

// validatePluginCandy validates a candy's `plugin:` block. The CUE schema already
// checks the capability + source FORMAT (#Plugin / #PluginCapability); this adds
// the Go runtime checks the schema cannot express:
//   - each capability is well-formed (<class>:<word> with a known class);
//   - for source:builtin, the provider is ACTUALLY registered (its init()
//     compiled it into charly) — a builtin plugin candy naming a provider charly
//     does not ship is a hard error, not a silent no-op.
//
// An out-of-tree source (github.com/…) is NOT connected here — that is the
// loader's job at deploy/check time (the out-of-proc follow-up); validate only
// confirms the declaration is well-formed.
func validatePluginCandy(name string, p *CandyPluginDecl) []string {
	if p == nil {
		return nil
	}
	var issues []string
	source := p.Source
	if source == "" {
		source = "builtin"
	}
	if len(p.Providers) == 0 {
		issues = append(issues, fmt.Sprintf("candy %q: plugin block declares no providers", name))
	}
	for _, capStr := range p.Providers {
		class, word, ok := splitCapability(string(capStr))
		if !ok {
			issues = append(issues, fmt.Sprintf("candy %q: plugin capability %q is malformed (want <class>:<word>)", name, capStr))
			continue
		}
		if source == "builtin" {
			if _, ok := providerRegistry.resolve(class, word); !ok {
				issues = append(issues, fmt.Sprintf(
					"candy %q: plugin declares builtin %s:%s but no such provider is compiled into charly", name, class, word))
			}
		}
	}
	return issues
}
