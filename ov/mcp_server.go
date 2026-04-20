package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"github.com/alecthomas/kong"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcp_server.go turns the entire `ov` CLI into an MCP server. Every leaf Kong
// command becomes one MCP tool whose input schema is derived mechanically from
// the command's flags and positional args. Tool-call handling re-invokes the
// Kong CLI inside this same process (no fork/exec) so the server stays in lock-
// step with the shell CLI.
//
// The symmetric client lives in mcp.go / mcp_client.go and is built on the same
// github.com/modelcontextprotocol/go-sdk. Layers that ship the `ov` binary
// declare `mcp_provides: ov` to advertise this endpoint to consumers
// (see layers/ov/layer.yml).

// Tool names that should not be exposed — the serve command itself (would be a
// shell into itself), and the interactive help flag.
var mcpSkipToolPaths = map[string]bool{
	"mcp.serve": true,
}

// mcpDestructivePaths is the set of leaf command paths whose tools should be
// annotated with DestructiveHint=true, and suppressed when the server is
// started with --read-only. Keep this list deliberately conservative: a false
// negative exposes a dangerous tool to an unsuspecting LLM.
//
// Entries must match the exact leaf path emitted by leafPath(). Verified by
// running `ov mcp serve --stdio` + tools/list and comparing names.
var mcpDestructivePaths = map[string]bool{
	// Lifecycle
	"remove":          true,
	"stop":            true,
	"start":           true,
	"update":          true,
	"cmd":             true,
	"shell":           true,
	"service.restart": true,
	"service.start":   true,
	"service.stop":    true,
	// Image config / encrypted storage / quadlets
	"config.setup":   true,
	"config.mount":   true,
	"config.unmount": true,
	"config.passwd":  true,
	"config.remove":  true,
	// Secrets — top-level
	"secrets.set":    true,
	"secrets.delete": true,
	"secrets.import": true,
	"secrets.init":   true,
	// Secrets — GPG subtree (only the mutating leaves; read-only ones stay exposed)
	"secrets.gpg.setup":          true,
	"secrets.gpg.set":            true,
	"secrets.gpg.unset":          true,
	"secrets.gpg.edit":           true,
	"secrets.gpg.encrypt":        true,
	"secrets.gpg.add-recipient":  true,
	"secrets.gpg.import-key":     true,
	// Deployment mutations
	"deploy.import": true,
	"deploy.reset":  true,
	// Image build/push/scaffold
	"image.build":     true,
	"image.merge":     true,
	"image.new.layer": true,
	// VM lifecycle
	"vm.create":  true,
	"vm.destroy": true,
	"vm.start":   true,
	"vm.stop":    true,
	"vm.build":   true,
	// udev / alias installation writes to the host
	"udev.install":    true,
	"udev.remove":     true,
	"alias.install":   true,
	"alias.uninstall": true,
	"alias.add":       true,
	"alias.remove":    true,
	// record / tmux state
	"record.start": true,
	"record.stop":  true,
	"record.cmd":   true,
	"tmux.kill":    true,
	"tmux.run":     true,
	"tmux.send":    true,
	"tmux.cmd":     true,
	// Settings mutations
	"settings.set":             true,
	"settings.reset":           true,
	"settings.migrate-secrets": true,
}

// ---------------------------------------------------------------------------
// Kong command tree
// ---------------------------------------------------------------------------

// McpCmdGroup is the top-level `ov mcp` command. Currently one subcommand.
type McpCmdGroup struct {
	Serve McpServeCmd `cmd:"" help:"Run an MCP server exposing every ov CLI command as a tool"`
}

// McpServeCmd: `ov mcp serve`
type McpServeCmd struct {
	Listen   string `long:"listen" default:":18765" help:"TCP listen address for Streamable HTTP transport"`
	Path     string `long:"path" default:"/mcp" help:"HTTP path prefix for the MCP endpoint"`
	Stdio    bool   `long:"stdio" help:"Use stdio transport instead of HTTP (for editor/LLM integration)"`
	ReadOnly bool   `long:"read-only" help:"Skip registration of tools that mutate state"`
}

func (c *McpServeCmd) Run() error {
	server, err := buildMcpServer(c.ReadOnly)
	if err != nil {
		return err
	}

	ctx := context.Background()

	if c.Stdio {
		return server.Run(ctx, &mcp.StdioTransport{})
	}

	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return server
	}, nil)
	mux := http.NewServeMux()
	mux.Handle(c.Path, handler)
	fmt.Fprintf(os.Stderr, "ov mcp: serving %d tools on http://%s%s\n",
		mcpRegisteredToolCount(server), c.Listen, c.Path)
	return http.ListenAndServe(c.Listen, mux)
}

// mcpRegisteredToolCount is only used for the startup banner. We keep a shadow
// counter because the SDK doesn't expose a tool count on *mcp.Server.
var mcpRegisteredToolCount = func(*mcp.Server) int { return mcpLastRegisteredCount }

var mcpLastRegisteredCount int

// ---------------------------------------------------------------------------
// Server construction
// ---------------------------------------------------------------------------

// buildMcpServer reflects the CLI struct, walks every leaf command, and
// registers one MCP tool per leaf.
func buildMcpServer(readOnly bool) (*mcp.Server, error) {
	// Build a Kong parser purely for its model tree. We do NOT Parse() here —
	// each tool call gets its own fresh parser + CLI struct so in-memory state
	// from one call doesn't leak into the next.
	var modelCLI CLI
	k, err := kong.New(&modelCLI, kong.Name("ov"), kong.UsageOnError())
	if err != nil {
		return nil, fmt.Errorf("building kong model: %w", err)
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "ov",
		Version: ComputeCalVer(),
	}, nil)

	count := 0
	for _, leaf := range k.Model.Leaves(true) {
		path := leafPath(leaf)
		if mcpSkipToolPaths[path] {
			continue
		}
		destructive := mcpDestructivePaths[path]
		if readOnly && destructive {
			continue
		}
		tool := kongLeafToTool(leaf, path, destructive)
		server.AddTool(tool, makeToolHandler(path, leaf))
		count++
	}
	mcpLastRegisteredCount = count
	return server, nil
}

// ---------------------------------------------------------------------------
// Kong → MCP tool schema
// ---------------------------------------------------------------------------

// leafPath returns a dotted command path like "image.build" from Kong's
// space-separated Path() value.
func leafPath(n *kong.Node) string {
	return strings.ReplaceAll(n.Path(), " ", ".")
}

// kongLeafToTool converts a Kong leaf command into an *mcp.Tool with a JSON
// Schema object built from its flags and positional args.
func kongLeafToTool(leaf *kong.Node, path string, destructive bool) *mcp.Tool {
	props := map[string]any{}
	var required []string

	// Positional args become required (or optional, per their tag) properties.
	for _, pos := range leaf.Positional {
		name := posPropName(pos)
		props[name] = valueToSchema(pos)
		if pos.Required {
			required = append(required, name)
		}
	}

	// Flags from all ancestor branches (includes --kdbx from root). Kong's
	// AllFlags returns grouped slices; flatten.
	for _, group := range leaf.AllFlags(true) {
		for _, f := range group {
			if f.Hidden || isHelpFlag(f) {
				continue
			}
			name := flagPropName(f)
			if _, exists := props[name]; exists {
				continue
			}
			props[name] = flagToSchema(f)
			if f.Required {
				required = append(required, name)
			}
		}
	}

	schema := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	// additionalProperties: false keeps the LLM honest — unknown keys surface
	// as schema violations rather than silently being passed as unknown flags.
	schema["additionalProperties"] = false

	desc := strings.TrimSpace(leaf.Help)
	if desc == "" {
		desc = fmt.Sprintf("ov %s", strings.ReplaceAll(path, ".", " "))
	}
	if destructive {
		desc += " [destructive: mutates container, volume, or host state]"
	}

	tool := &mcp.Tool{
		Name:        path,
		Description: desc,
		InputSchema: schema,
	}
	if destructive {
		yes := true
		tool.Annotations = &mcp.ToolAnnotations{DestructiveHint: &yes}
	} else {
		// Read-only by default for non-destructive commands.
		tool.Annotations = &mcp.ToolAnnotations{ReadOnlyHint: true}
	}
	return tool
}

func isHelpFlag(f *kong.Flag) bool {
	return f.Name == "help" || f.Name == "help-all"
}

func posPropName(p *kong.Positional) string {
	return sanitizePropName(p.Name)
}

func flagPropName(f *kong.Flag) string {
	return sanitizePropName(f.Name)
}

// sanitizePropName lowercases and replaces "-" with "_" so JSON Schema
// properties are idiomatic LLM-friendly snake_case. Reverse mapping is handled
// in argvFromJSON via the Kong model.
func sanitizePropName(s string) string {
	return strings.ReplaceAll(strings.ToLower(s), "-", "_")
}

// valueToSchema produces a JSON Schema fragment for a Kong Value (positional
// or flag, same type).
func valueToSchema(v *kong.Value) map[string]any {
	out := map[string]any{}
	if v.Help != "" {
		out["description"] = v.Help
	}

	if v.Enum != "" {
		enums := v.EnumSlice()
		if len(enums) > 0 {
			anyEnum := make([]any, len(enums))
			for i, e := range enums {
				anyEnum[i] = e
			}
			out["enum"] = anyEnum
		}
	}

	// Slice → array of strings (items inferred from target type, but Kong
	// accumulates via repeated flags; we encode as array of strings/numbers
	// based on element kind).
	if v.IsSlice() {
		out["type"] = "array"
		out["items"] = map[string]any{"type": jsonTypeForKind(v.Target.Type().Elem().Kind())}
		return out
	}

	if v.IsMap() {
		out["type"] = "object"
		out["additionalProperties"] = map[string]any{"type": "string"}
		return out
	}

	out["type"] = jsonTypeForKind(v.Target.Kind())

	if v.HasDefault && v.Default != "" {
		// Coerce the default string into the schema's type so JSON Schema
		// validators accept it. For bools/ints/floats, parse; for strings,
		// keep as-is.
		switch out["type"] {
		case "boolean":
			if b, err := strconv.ParseBool(v.Default); err == nil {
				out["default"] = b
			}
		case "integer":
			if i, err := strconv.ParseInt(v.Default, 10, 64); err == nil {
				out["default"] = i
			}
		case "number":
			if f, err := strconv.ParseFloat(v.Default, 64); err == nil {
				out["default"] = f
			}
		default:
			out["default"] = v.Default
		}
	}
	return out
}

func flagToSchema(f *kong.Flag) map[string]any {
	return valueToSchema(f.Value)
}

// jsonTypeForKind maps a reflect.Kind to a JSON Schema primitive type.
// time.Duration, net addresses, etc. all round-trip as strings.
func jsonTypeForKind(k reflect.Kind) string {
	switch k {
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Slice, reflect.Array:
		return "array"
	case reflect.Map, reflect.Struct:
		return "object"
	default:
		return "string"
	}
}

// ---------------------------------------------------------------------------
// Tool handler: JSON input → argv → kong.Parse → run
// ---------------------------------------------------------------------------

// runMu serializes tool invocations because each handler captures os.Stdout
// / os.Stderr for its duration. Running two in parallel would interleave
// their output. `ov` commands do not benefit much from parallelism — most
// are I/O-bound shell-outs to podman/docker anyway.
var runMu sync.Mutex

// makeToolHandler closes over the leaf path and flag model, returning an MCP
// ToolHandler that reconstructs an argv slice and invokes the CLI.
func makeToolHandler(path string, leaf *kong.Node) mcp.ToolHandler {
	// Build a reverse map: sanitized JSON prop name → Kong flag/arg model,
	// precomputed once to keep the hot path cheap and deterministic.
	posByProp := map[string]*kong.Positional{}
	posOrder := make([]string, 0, len(leaf.Positional))
	for _, p := range leaf.Positional {
		name := posPropName(p)
		posByProp[name] = p
		posOrder = append(posOrder, name)
	}

	flagByProp := map[string]*kong.Flag{}
	for _, group := range leaf.AllFlags(true) {
		for _, f := range group {
			if f.Hidden || isHelpFlag(f) {
				continue
			}
			flagByProp[flagPropName(f)] = f
		}
	}

	cmdTokens := strings.Split(path, ".")

	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		runMu.Lock()
		defer runMu.Unlock()

		var input map[string]any
		if len(req.Params.Arguments) > 0 {
			if err := json.Unmarshal(req.Params.Arguments, &input); err != nil {
				return toolError(fmt.Errorf("parsing arguments: %w", err))
			}
		}
		if input == nil {
			input = map[string]any{}
		}

		argv, err := argvFromJSON(cmdTokens, posOrder, posByProp, flagByProp, input)
		if err != nil {
			return toolError(err)
		}

		stdout, stderr, runErr := captureAndRun(argv)

		text := assembleToolText(stdout, stderr, runErr)
		res := &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}
		if runErr != nil {
			res.IsError = true
		}
		return res, nil
	}
}

// argvFromJSON reconstructs a CLI args slice from MCP-supplied JSON. Order:
//
//  1. Command tokens (e.g. "image", "build").
//  2. --flag=value pairs (sorted for determinism; booleans emit --flag or
//     --no-flag with no value).
//  3. Positional values, in the order declared by Kong.
//
// Slices are emitted as repeated --flag=value pairs (Kong's accumulate mode).
func argvFromJSON(cmdTokens, posOrder []string, posByProp map[string]*kong.Positional, flagByProp map[string]*kong.Flag, input map[string]any) ([]string, error) {
	argv := append([]string{}, cmdTokens...)

	// Flags first — this lets positionals after them be unambiguous.
	for _, name := range sortedJSONKeys(input) {
		if _, isPos := posByProp[name]; isPos {
			continue
		}
		f, ok := flagByProp[name]
		if !ok {
			return nil, fmt.Errorf("unknown argument %q for tool", name)
		}
		val := input[name]
		tokens, err := flagToArgv(f, val)
		if err != nil {
			return nil, fmt.Errorf("flag --%s: %w", f.Name, err)
		}
		argv = append(argv, tokens...)
	}

	// Positionals last, in Kong's declared order.
	for _, name := range posOrder {
		v, ok := input[name]
		if !ok {
			// Kong will complain if the positional was required.
			continue
		}
		s, err := scalarToString(v)
		if err != nil {
			return nil, fmt.Errorf("positional %s: %w", name, err)
		}
		// Cumulative positionals (e.g. image build <images...>) accept
		// multiple values. If the JSON value is an array, expand it.
		if arr, ok := v.([]any); ok {
			for _, item := range arr {
				s, err := scalarToString(item)
				if err != nil {
					return nil, fmt.Errorf("positional %s: %w", name, err)
				}
				argv = append(argv, s)
			}
			continue
		}
		argv = append(argv, s)
	}

	_ = posByProp
	return argv, nil
}

// flagToArgv renders a single flag + value as CLI tokens.
func flagToArgv(f *kong.Flag, v any) ([]string, error) {
	if f.IsBool() {
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("expected boolean, got %T", v)
		}
		if b {
			return []string{"--" + f.Name}, nil
		}
		// Kong supports negatable flags with --no- prefix; fall back to
		// omitting the flag for non-negatable booleans.
		if f.Negated {
			return []string{"--no-" + f.Name}, nil
		}
		return nil, nil
	}

	// Slices → repeat --flag=value
	if arr, ok := v.([]any); ok {
		out := make([]string, 0, len(arr))
		for _, item := range arr {
			s, err := scalarToString(item)
			if err != nil {
				return nil, err
			}
			out = append(out, "--"+f.Name+"="+s)
		}
		return out, nil
	}

	s, err := scalarToString(v)
	if err != nil {
		return nil, err
	}
	return []string{"--" + f.Name + "=" + s}, nil
}

// scalarToString coerces a JSON scalar into the string representation Kong
// expects on the command line.
func scalarToString(v any) (string, error) {
	switch x := v.(type) {
	case string:
		return x, nil
	case bool:
		return strconv.FormatBool(x), nil
	case float64:
		// JSON numbers all decode as float64. Preserve integer-looking values
		// without a trailing ".0".
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10), nil
		}
		return strconv.FormatFloat(x, 'g', -1, 64), nil
	case json.Number:
		return string(x), nil
	case nil:
		return "", nil
	default:
		return "", fmt.Errorf("unsupported scalar type %T", v)
	}
}

func sortedJSONKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Stable, deterministic order — lexical is fine, nothing in Kong cares
	// about flag order.
	sortStrings(keys)
	return keys
}

// ---------------------------------------------------------------------------
// Stdout/stderr capture + kong.Parse invocation
// ---------------------------------------------------------------------------

// captureAndRun invokes a fresh Kong parser on a fresh CLI struct with the
// given argv, capturing stdout and stderr. Returns captured streams and any
// error from kong.Parse or the command's Run() method.
//
// Capture is done at the os.Stdout/os.Stderr package-variable level (not at
// the fd level via syscall.Dup2). Fd-level capture was tried first but
// conflicted with the MCP stdio transport: the SDK's StdioTransport captures
// os.Stdout by pointer at Connect time, and that pointer's underlying fd 1
// becomes our capture pipe once dup2'd — so JSON-RPC responses get silently
// eaten into the tool's output buffer instead of reaching the client.
//
// Consequence: any ov command that writes via Go's builtin println() (which
// goes to fd 2 bypassing os.Stderr), or that spawns a subprocess with the
// parent's inherited fds, will leak output past this capture. The `ov`
// codebase uses fmt.Println/Printf exclusively for user output; `println`
// usage has been audited out in the commands invoked via the MCP surface.
func captureAndRun(argv []string) (stdout, stderr string, err error) {
	origStdout := os.Stdout
	origStderr := os.Stderr

	outR, outW, pipeErr := os.Pipe()
	if pipeErr != nil {
		return "", "", fmt.Errorf("creating stdout pipe: %w", pipeErr)
	}
	errR, errW, pipeErr := os.Pipe()
	if pipeErr != nil {
		outR.Close()
		outW.Close()
		return "", "", fmt.Errorf("creating stderr pipe: %w", pipeErr)
	}
	os.Stdout = outW
	os.Stderr = errW

	// Drain readers concurrently so writes past the pipe buffer don't block.
	var outBuf, errBuf bytes.Buffer
	var drainWG sync.WaitGroup
	drainWG.Add(2)
	go func() { defer drainWG.Done(); _, _ = io.Copy(&outBuf, outR) }()
	go func() { defer drainWG.Done(); _, _ = io.Copy(&errBuf, errR) }()

	defer func() {
		os.Stdout = origStdout
		os.Stderr = origStderr
	}()

	func() {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("panic in command: %v", r)
			}
		}()

		var cli CLI
		k, kerr := kong.New(&cli,
			kong.Name("ov"),
			kong.UsageOnError(),
			// Prevent os.Exit on parse errors so the MCP handler can surface
			// them as tool errors.
			kong.Exit(func(int) {}),
			// Redirect Kong's own output (help, errors) into our captured
			// stderr stream.
			kong.Writers(outW, errW),
		)
		if kerr != nil {
			err = fmt.Errorf("building kong: %w", kerr)
			return
		}
		kctx, perr := k.Parse(argv)
		if perr != nil {
			err = fmt.Errorf("parse %v: %w", argv, perr)
			return
		}
		err = kctx.Run()
	}()

	_ = outW.Close()
	_ = errW.Close()
	drainWG.Wait()
	_ = outR.Close()
	_ = errR.Close()

	return outBuf.String(), errBuf.String(), err
}

// assembleToolText composes the TextContent payload: stdout first, stderr
// appended as a labelled block (so errors are visible even when commands
// also wrote to stdout), then an error summary.
func assembleToolText(stdout, stderr string, runErr error) string {
	var b strings.Builder
	b.WriteString(stdout)
	if stderr != "" {
		if b.Len() > 0 && !strings.HasSuffix(stdout, "\n") {
			b.WriteByte('\n')
		}
		b.WriteString("--- stderr ---\n")
		b.WriteString(stderr)
	}
	if runErr != nil {
		if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "--- error ---\n%v\n", runErr)
	}
	return b.String()
}

func toolError(err error) (*mcp.CallToolResult, error) {
	res := &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
	}
	return res, nil
}
