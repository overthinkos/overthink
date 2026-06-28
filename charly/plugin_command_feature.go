package main

// featureCommand is the `charly feature` command tree extracted into its OWN file
// as a dedicated COMMAND-class provider — the same externalizable dedicated-provider
// pattern (see plugin_command_alias.go for the full rationale). It self-registers
// via registerDedicatedBuiltin, is absent from builtinProviderInstances + the
// `providers:` manifest, and reaches the CLI root through collectCommandPlugins()
// → kong.Plugins. KongCommand() returns FeatureCmd verbatim (its list/pending/
// validate Run handlers still call the unchanged plan-description machinery), so
// `charly feature …` parses and dispatches exactly as when it was a hardcoded CLI
// field. Only the registration LOCATION moved off the CLI struct. (The Feature
// RUN verbs stay where they are — children of CheckCmd / ImageCmd — so
// `charly check feature run` / `charly box feature run` are unaffected.)
type featureCommand struct{ builtinCommandBase }

func (featureCommand) Reserved() string { return "feature" }
func (featureCommand) KongCommand() any {
	return &struct {
		Feature FeatureCmd `cmd:"" help:"plan-shaped description authoring: list/pending/validate"`
	}{}
}

var _ = registerDedicatedBuiltin(featureCommand{})
