package main

// preemptCommand is the `charly preempt` command group as a dedicated COMMAND-class
// provider (the externalizable pattern landed for udev — see plugin_command_udev.go).
// It self-registers via registerDedicatedBuiltin and reaches the CLI root through
// collectCommandPlugins() → kong.Plugins; KongCommand() returns PreemptCmd verbatim
// (status + restore subcommands + Run handlers), so `charly preempt …` parses and
// dispatches exactly as when it was a hardcoded CLI field.
type preemptCommand struct{ builtinCommandBase }

func (preemptCommand) Reserved() string { return "preempt" }
func (preemptCommand) KongCommand() any {
	return &struct {
		Preempt PreemptCmd `cmd:"" help:"Inspect and recover exclusive-resource preemption leases (preemptible holders stopped to free a resource for a claimant)"`
	}{}
}

var _ = registerDedicatedBuiltin(preemptCommand{})
