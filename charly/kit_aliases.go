package main

import "github.com/overthinkos/overthink/charly/plugin/kit"

// kit_aliases.go — package-main bindings onto generic helpers that live ONCE in the importable
// host-engine kit (github.com/overthinkos/overthink/charly/plugin/kit), shared with the out-of-tree
// plugin candies that also import kit. These thin aliases keep core's call sites unchanged after
// FU-11 consolidated the former core↔plugin pure-helper duplication (shellSingleQuote was already
// byte-identical to kit.ShellQuote; trimPreview/wrapContainerCommand moved into kit).
var (
	shellSingleQuote     = kit.ShellQuote
	trimPreview          = kit.TrimPreview
	wrapContainerCommand = kit.WrapContainerCommand
)
