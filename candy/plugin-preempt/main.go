// Command plugin-preempt is the OUT-OF-TREE charly COMMAND-class plugin serving the
// externalized `charly preempt …` CLI — the exclusive-resource preemption lease
// inspector/recoverer ported OUT of charly's core (the deleted charly/preempt_cmd.go +
// charly/plugin_command_preempt.go), so the operator-facing `charly preempt` command no
// longer compiles into the core binary.
//
// The SECOND WELDED-command externalization in the core-externalization program (after
// candy/plugin-tmux). `charly preempt` was WELDED to the 1225-LOC GPU resource ARBITER
// (charly/preempt.go, ResourceArbiter) which MUST STAY CORE — it is shared by `charly vm
// create`, `charly vm gpu`, and the check-bed runner, so the arbiter cannot move (R3). The
// SOLUTION (the tmux precedent — a plugin shells back through SANCTIONED charly CLI verbs,
// never ad-hoc anything, R4): this plugin re-expresses each `charly preempt` leaf as a
// shell-back through two NEW HIDDEN core commands that expose the in-core arbiter —
// `charly __preempt-status` (newResourceArbiter().Status() + the lease table) and
// `charly __preempt-restore [claimant]` (reconcileStranded / ReleaseClaimant). Those hidden
// verbs are the SAME `charly __cli-model` / `charly __plugin-providers` internal-command
// pattern; the arbiter logic stays in core, invoked ONLY there. No core symbol crosses the
// process boundary; no ad-hoc podman/virsh.
//
// CLI dispatch contract (charly/provider_command_external.go dispatchExternalCommand): on
// `charly preempt <args…>`, charly RESOLVES this plugin's binary (host-built from source, or
// baked into /usr/lib/charly/plugins via pkg/arch) and syscall.Exec's it with the
// pass-through tokens after the `preempt` word, in CLI mode (the go-plugin handshake cookie is
// stripped, so sdk.Main runs cliMain instead of serving gRPC) with CHARLY_BIN stamped to
// charly's own executable — so every shell-back re-enters the SAME charly binary that
// dispatched the plugin. The plugin owns real terminal stdio, so the status table + restore
// messages reach the operator's terminal natively.
//
// A command is NOT a gRPC-registry capability (charly fork/execs the binary; it never
// connects over gRPC for a command), so this plugin advertises NO Describe capability — its
// serve half (sdk.Serve, never reached for a command-only plugin) exists only to satisfy the
// dual-mode sdk.Main signature. The candy's plugin.providers declaration still lists
// command:preempt (that drives the CLI-grammar prescan + the baked `.providers` manifest).
package main

import (
	"embed"

	"github.com/overthinkos/overthink/charly/plugin/sdk"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// main is dual-mode (sdk.Main). For a command-only plugin, charly only ever fork/execs the
// binary (CLI mode) — it never launches it in go-plugin serve mode — so cliMain is the live
// path; the serve half is inert (no gRPC capability).
func main() { sdk.Main(&provider{}, &meta{}, cliMain) }
