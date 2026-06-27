package main

import "github.com/overthinkos/overthink/charly/plugin/kit"

// kit_aliases.go — package-main bindings onto generic helpers that live ONCE in the importable
// host-engine kit (github.com/overthinkos/overthink/charly/plugin/kit), shared with the out-of-tree
// plugin candies that also import kit. These thin aliases keep core's call sites unchanged after
// FU-11 consolidated the former core↔plugin pure-helper duplication (shellSingleQuote was already
// byte-identical to kit.ShellQuote; trimPreview/wrapContainerCommand moved into kit).
var (
	shellSingleQuote = kit.ShellQuote
	// shellQuote is the brevity alias used across the build / deploy / notify / tmux /
	// secrets call sites (formerly defined in wl.go, FU-14 folded onto kit.ShellQuote);
	// it moved here when the `wl` verb externalized and wl.go was deleted.
	shellQuote           = kit.ShellQuote
	trimPreview          = kit.TrimPreview
	wrapContainerCommand = kit.WrapContainerCommand
)
