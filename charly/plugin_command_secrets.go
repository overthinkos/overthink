package main

// secretsCommand is the `charly secrets` command group as a dedicated COMMAND-class
// provider (the externalizable pattern landed for udev — see plugin_command_udev.go).
// It self-registers via registerDedicatedBuiltin and reaches the CLI root through
// collectCommandPlugins() → kong.Plugins; KongCommand() returns SecretsCmdGroup verbatim
// (including the nested gpg subgroup + every Run handler), so `charly secrets …` parses
// and dispatches exactly as when it was a hardcoded CLI field.
type secretsCommand struct{ builtinCommandBase }

func (secretsCommand) Reserved() string { return "secrets" }
func (secretsCommand) KongCommand() any {
	return &struct {
		Secrets SecretsCmdGroup `cmd:"" help:"Manage credentials (Secret Service / config) and GPG-encrypted .secrets files"`
	}{}
}

var _ = registerDedicatedBuiltin(secretsCommand{})
