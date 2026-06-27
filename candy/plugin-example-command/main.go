// Command plugin-example-command is the reference OUT-OF-TREE charly COMMAND-class
// plugin: a standalone binary (its own Go module) contributing the `examplecommand`
// command. It is the COMMAND-class companion of the verb-class candy/plugin-example-external.
//
// CLI dispatch contract (charly/provider_command_external.go dispatchExternalCommand):
// on `charly examplecommand <args…>`, charly RESOLVES this plugin's binary and
// syscall.Exec's it with the pass-through tokens after the command word, in CLI mode
// (the go-plugin handshake cookie is stripped, so sdk.Main runs cliMain rather than
// serving gRPC). The plugin therefore owns real terminal stdio/TTY — the whole point of
// the command-passthrough seam — and does ONE observable, deterministic thing: it prints
// the joined args to STDOUT, so a test can assert both that the command ran and which
// args it received, AND that a command plugin reaches a real stdout.
//
// A command is NOT a gRPC-registry capability (charly fork/execs the binary; it never
// connects over gRPC for a command), so this plugin advertises NO Describe capability —
// its serve half (sdk.Serve, never reached for a command-only plugin) exists only to
// satisfy the dual-mode sdk.Main signature. The candy's plugin.providers declaration still
// lists command:examplecommand (that drives the CLI-grammar prescan + the baked manifest).
package main

import (
	"context"
	"embed"
	"fmt"
	"strings"

	pb "github.com/overthinkos/overthink/charly/plugin/proto"
	"github.com/overthinkos/overthink/charly/plugin/sdk"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// main is dual-mode (sdk.Main). For a command-only plugin, charly only ever fork/execs the
// binary (CLI mode) — it never launches it in go-plugin serve mode — so cliMain is the live
// path; the serve half is inert (no gRPC capability).
func main() { sdk.Main(&provider{}, &meta{}, cliMain) }

// cliMain is the CLI-mode entry point: charly fork/exec'd this plugin with the pass-through
// tokens after `charly examplecommand`. It prints them (joined) to real stdout and exits 0.
func cliMain(args []string) int {
	fmt.Println(strings.Join(args, " "))
	return 0
}

type provider struct{ pb.UnimplementedProviderServer }

// Invoke is unreachable for this command-only plugin: charly dispatches command:examplecommand
// by fork/exec (CLI mode), never gRPC. It returns a clear error so a stray gRPC Invoke is loud,
// never a silent surprise.
func (provider) Invoke(context.Context, *pb.InvokeRequest) (*pb.InvokeReply, error) {
	return nil, fmt.Errorf("plugin-example-command: command:examplecommand is dispatched via the CLI (charly fork/execs this binary), not gRPC Invoke")
}

type meta struct {
	pb.UnimplementedPluginMetaServer
}

// Describe advertises NO gRPC capability — command:examplecommand is CLI-dispatched, not
// resolved through the gRPC provider registry. It ships only the self-contained doc schema to
// satisfy the host's non-empty-schema load gate and the params codegen loop. The SDK compiles
// the embedded schema STANDALONE here, failing loudly before serving if it is broken.
func (meta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) {
	return sdk.BuildCapabilities("2026.175.0900",
		[]sdk.ProvidedCapability{},
		schemaFS, "schema")
}
