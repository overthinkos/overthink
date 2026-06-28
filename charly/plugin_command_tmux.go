package main

// tmuxCommand is the `charly tmux` command group as a dedicated COMMAND-class provider
// (the externalizable dedicated-provider pattern — see plugin_command_alias.go). It
// self-registers via registerDedicatedBuiltin and reaches the CLI root through
// collectCommandPlugins() → kong.Plugins; KongCommand() returns TmuxCmd verbatim, so
// `charly tmux …` parses and dispatches exactly as when it was a hardcoded CLI field.
type tmuxCommand struct{ builtinCommandBase }

func (tmuxCommand) Reserved() string { return "tmux" }
func (tmuxCommand) KongCommand() any {
	return &struct {
		Tmux TmuxCmd `cmd:"" help:"Manage tmux sessions inside running containers"`
	}{}
}

var _ = registerDedicatedBuiltin(tmuxCommand{})
