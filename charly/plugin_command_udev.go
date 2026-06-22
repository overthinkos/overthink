package main

// udevCommand is the `charly udev` leaf CLI command extracted into its OWN file as
// the externalizable dedicated-provider pattern applied to the 6th provider class
// (ClassCommand). A CLI command is not user-authored schema — it carries no
// plugin_input and no CUE def — so, like the schema-less IR providers
// (plugin_deploy_local.go, plugin_step_reboot.go, plugin_builder_cargo.go), it
// self-registers via registerDedicatedBuiltin and is INTENTIONALLY absent from both
// the builtinProviderInstances slice and the `providers:` manifest. There is no
// command bijection gate (mirroring builders), so the registry resolve IS the wiring
// proof. collectCommandPlugins() discovers it through the registry and embeds its Kong
// grammar onto the CLI root via kong.Plugins, so `charly udev …` parses and dispatches
// through UdevCmd's compiled-in subcommands + Run handlers exactly as when it was a
// hardcoded field on the CLI struct (behavior-preserving). It satisfies CommandProvider,
// so collectExternalCommandPlugins() skips it (the out-of-process path is for plugins
// that have no static KongCommand()).
type udevCommand struct{ builtinCommandBase }

func (udevCommand) Reserved() string { return "udev" }
func (udevCommand) KongCommand() any {
	return &struct {
		Udev UdevCmd `cmd:"" help:"Manage udev rules for GPU device access in containers"`
	}{}
}

// Self-register at package-var init (before any init()), so the registry observes it
// without a cross-init race — identical to the dedicated IR providers.
var _ = registerDedicatedBuiltin(udevCommand{})
