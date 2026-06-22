package main

// aliasCommand is the `charly alias` command group extracted into its OWN file as a
// dedicated COMMAND-class provider — the same externalizable pattern landed for udev
// (see plugin_command_udev.go for the full rationale). It self-registers via
// registerDedicatedBuiltin, is absent from builtinProviderInstances + the `providers:`
// manifest, and reaches the CLI root through collectCommandPlugins() → kong.Plugins.
// KongCommand() returns AliasCmd verbatim (subcommands + Run handlers unchanged), so
// `charly alias …` parses and dispatches exactly as when it was a hardcoded CLI field.
type aliasCommand struct{ builtinCommandBase }

func (aliasCommand) Reserved() string { return "alias" }
func (aliasCommand) KongCommand() any {
	return &struct {
		Alias AliasCmd `cmd:"" help:"Manage command aliases for container images"`
	}{}
}

var _ = registerDedicatedBuiltin(aliasCommand{})
