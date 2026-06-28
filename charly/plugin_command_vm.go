package main

// vmCommand is the `charly vm` command tree extracted into its OWN file as a
// dedicated COMMAND-class provider — the same externalizable dedicated-provider
// pattern (see plugin_command_alias.go for the full rationale). It self-registers via
// registerDedicatedBuiltin, is absent from builtinProviderInstances + the
// `providers:` manifest, and reaches the CLI root through collectCommandPlugins()
// → kong.Plugins. KongCommand() returns VmCmd verbatim (its subcommands' Run
// handlers still call the unchanged VM machinery — libvirt/qemu backends, cloud
// init, the VM deploy target), so `charly vm …` parses and dispatches exactly as
// when it was a hardcoded CLI field. Only the registration LOCATION moved off the
// CLI struct.
type vmCommand struct{ builtinCommandBase }

func (vmCommand) Reserved() string { return "vm" }
func (vmCommand) KongCommand() any {
	return &struct {
		Vm VmCmd `cmd:"" help:"Manage virtual machines from bootc images"`
	}{}
}

var _ = registerDedicatedBuiltin(vmCommand{})
