package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcp.go contains the `ov test mcp …` Kong subcommand tree. Each leaf
// resolves the image's mcp_provides declaration via OCI labels, opens a
// session using the Anthropic Go SDK (StreamableClientTransport for
// `transport: http` / empty; SSEClientTransport for `transport: sse`), and
// exercises one MCP operation. Output is tab-separated plaintext so the
// declarative test framework's MatcherList can `contains: <tool-name>`
// without decoding JSON; each leaf also supports --json for programmatic
// use.
//
// The dispatcher pattern mirrors ov/dbus.go (parent struct + N leaf structs
// with Run() methods + positional args + `-i` flag). Live-container wiring
// (runMcp + method allowlist) lives in testrun_ov_verbs.go.

// McpCmd groups the seven `ov test mcp …` leaves.
type McpCmd struct {
	Ping          McpPingCmd          `cmd:"" help:"Ping an MCP server (liveness check)"`
	Servers       McpServersCmd       `cmd:"" help:"Enumerate MCP servers declared by an image (no dial)"`
	ListTools     McpListToolsCmd     `cmd:"list-tools" help:"List tools offered by an MCP server"`
	ListResources McpListResourcesCmd `cmd:"list-resources" help:"List resources offered by an MCP server"`
	ListPrompts   McpListPromptsCmd   `cmd:"list-prompts" help:"List prompts offered by an MCP server"`
	Call          McpCallCmd          `cmd:"" help:"Call a tool on an MCP server"`
	Read          McpReadCmd          `cmd:"" help:"Read a resource from an MCP server"`
}

// ---------------------------------------------------------------------------
// Shared leaf flags
// ---------------------------------------------------------------------------

type mcpCommonFlags struct {
	Name     string        `long:"name" help:"Which mcp_provides entry to target when the image declares multiple"`
	Instance string        `short:"i" long:"instance" help:"Instance name"`
	JSON     bool          `long:"json" help:"Emit SDK result as pretty-printed JSON instead of tab-separated text"`
	Timeout  time.Duration `long:"timeout" default:"30s" help:"Per-operation timeout"`
}

// ---------------------------------------------------------------------------
// Leaf commands
// ---------------------------------------------------------------------------

// McpPingCmd: `ov test mcp ping <image>`
type McpPingCmd struct {
	Image string `arg:"" help:"Image name"`
	mcpCommonFlags
}

func (c *McpPingCmd) Run() error {
	ctx, cancel := context.WithTimeout(context.Background(), c.Timeout)
	defer cancel()

	sess, _, closeFn, err := mcpOpenSession(ctx, c.Image, c.Instance, c.Name)
	if err != nil {
		return err
	}
	defer closeFn()

	if err := sess.Ping(ctx, nil); err != nil {
		return fmt.Errorf("mcp ping: %w", err)
	}
	fmt.Println("ok")
	return nil
}

// McpServersCmd: `ov test mcp servers <image>` — discovery-only.
type McpServersCmd struct {
	Image    string `arg:"" help:"Image name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
	JSON     bool   `long:"json" help:"Emit as JSON array"`
}

func (c *McpServersCmd) Run() error {
	engine, containerName, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	imageRef, err := containerImageRef(engine, containerName)
	if err != nil {
		return err
	}
	meta, err := ExtractMetadata(engine, imageRef)
	if err != nil {
		return err
	}
	if meta == nil || len(meta.MCPProvides) == 0 {
		return fmt.Errorf("image %q declares no mcp_provides", c.Image)
	}
	entries := make([]MCPProvidesEntry, 0, len(meta.MCPProvides))
	for _, p := range meta.MCPProvides {
		entries = append(entries, MCPProvidesEntry{
			Name:      p.Name,
			URL:       resolveContainerNameTemplate(p.URL, containerName),
			Transport: p.Transport,
			Source:    c.Image,
		})
	}
	entries = podAwareMCPProvides(entries, c.Image, containerName)

	if c.JSON {
		out, err := json.MarshalIndent(entries, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(out))
		return nil
	}
	for _, e := range entries {
		transport := e.Transport
		if transport == "" {
			transport = "http"
		}
		fmt.Printf("%s\t%s\t%s\n", e.Name, e.URL, transport)
	}
	return nil
}

// McpListToolsCmd: `ov test mcp list-tools <image>`
type McpListToolsCmd struct {
	Image string `arg:"" help:"Image name"`
	mcpCommonFlags
}

func (c *McpListToolsCmd) Run() error {
	ctx, cancel := context.WithTimeout(context.Background(), c.Timeout)
	defer cancel()

	sess, _, closeFn, err := mcpOpenSession(ctx, c.Image, c.Instance, c.Name)
	if err != nil {
		return err
	}
	defer closeFn()

	var all []*mcp.Tool
	var cursor string
	for {
		res, err := sess.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return fmt.Errorf("mcp list-tools: %w", err)
		}
		all = append(all, res.Tools...)
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}

	if c.JSON {
		return emitJSON(all)
	}
	for _, t := range all {
		fmt.Println(formatTool(t))
	}
	return nil
}

// McpListResourcesCmd: `ov test mcp list-resources <image>`
type McpListResourcesCmd struct {
	Image string `arg:"" help:"Image name"`
	mcpCommonFlags
}

func (c *McpListResourcesCmd) Run() error {
	ctx, cancel := context.WithTimeout(context.Background(), c.Timeout)
	defer cancel()

	sess, _, closeFn, err := mcpOpenSession(ctx, c.Image, c.Instance, c.Name)
	if err != nil {
		return err
	}
	defer closeFn()

	var all []*mcp.Resource
	var cursor string
	for {
		res, err := sess.ListResources(ctx, &mcp.ListResourcesParams{Cursor: cursor})
		if err != nil {
			return fmt.Errorf("mcp list-resources: %w", err)
		}
		all = append(all, res.Resources...)
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}

	if c.JSON {
		return emitJSON(all)
	}
	for _, r := range all {
		fmt.Println(formatResource(r))
	}
	return nil
}

// McpListPromptsCmd: `ov test mcp list-prompts <image>`
type McpListPromptsCmd struct {
	Image string `arg:"" help:"Image name"`
	mcpCommonFlags
}

func (c *McpListPromptsCmd) Run() error {
	ctx, cancel := context.WithTimeout(context.Background(), c.Timeout)
	defer cancel()

	sess, _, closeFn, err := mcpOpenSession(ctx, c.Image, c.Instance, c.Name)
	if err != nil {
		return err
	}
	defer closeFn()

	var all []*mcp.Prompt
	var cursor string
	for {
		res, err := sess.ListPrompts(ctx, &mcp.ListPromptsParams{Cursor: cursor})
		if err != nil {
			return fmt.Errorf("mcp list-prompts: %w", err)
		}
		all = append(all, res.Prompts...)
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}

	if c.JSON {
		return emitJSON(all)
	}
	for _, p := range all {
		fmt.Println(formatPrompt(p))
	}
	return nil
}

// McpCallCmd: `ov test mcp call <image> <tool> [args-json]`
type McpCallCmd struct {
	Image string `arg:"" help:"Image name"`
	Tool  string `arg:"" help:"Tool name (matches a name from list-tools)"`
	Input string `arg:"" optional:"" help:"Tool arguments as a JSON object, e.g. '{\"path\":\"notebook.ipynb\"}'"`
	mcpCommonFlags
}

func (c *McpCallCmd) Run() error {
	ctx, cancel := context.WithTimeout(context.Background(), c.Timeout)
	defer cancel()

	var args any
	if strings.TrimSpace(c.Input) != "" {
		if err := json.Unmarshal([]byte(c.Input), &args); err != nil {
			return fmt.Errorf("parsing tool arguments as JSON: %w", err)
		}
	}

	sess, _, closeFn, err := mcpOpenSession(ctx, c.Image, c.Instance, c.Name)
	if err != nil {
		return err
	}
	defer closeFn()

	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: c.Tool, Arguments: args})
	if err != nil {
		return fmt.Errorf("mcp call %s: %w", c.Tool, err)
	}

	if c.JSON {
		return emitJSON(res)
	}
	text := extractToolText(res)
	if text != "" {
		fmt.Println(text)
	}
	if res.IsError {
		// Side-effect failures surface as a non-zero exit so declarative
		// tests can assert `exit_status: 1` on intentional-error paths.
		fmt.Fprintln(os.Stderr, "mcp: tool reported isError=true")
		return fmt.Errorf("tool %s reported an error", c.Tool)
	}
	return nil
}

// McpReadCmd: `ov test mcp read <image> <uri>`
type McpReadCmd struct {
	Image string `arg:"" help:"Image name"`
	URI   string `arg:"" help:"Resource URI (match a URI from list-resources)"`
	mcpCommonFlags
}

func (c *McpReadCmd) Run() error {
	ctx, cancel := context.WithTimeout(context.Background(), c.Timeout)
	defer cancel()

	sess, _, closeFn, err := mcpOpenSession(ctx, c.Image, c.Instance, c.Name)
	if err != nil {
		return err
	}
	defer closeFn()

	res, err := sess.ReadResource(ctx, &mcp.ReadResourceParams{URI: c.URI})
	if err != nil {
		return fmt.Errorf("mcp read %s: %w", c.URI, err)
	}

	if c.JSON {
		return emitJSON(res)
	}
	for _, cnt := range res.Contents {
		if cnt == nil {
			continue
		}
		if cnt.Text != "" {
			fmt.Println(cnt.Text)
			continue
		}
		if len(cnt.Blob) > 0 {
			fmt.Fprintf(os.Stderr, "[binary resource %s: %d bytes, mime %s — use --json for base64]\n", cnt.URI, len(cnt.Blob), cnt.MIMEType)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// mcpOpenSession is the shared establishment path used by every leaf except
// `servers` (which is pure metadata and doesn't dial). The returned closeFn
// is safe to `defer` immediately; it tolerates a nil session on error paths.
var mcpOpenSession = defaultMcpOpenSession

func defaultMcpOpenSession(ctx context.Context, image, instance, wantName string) (*mcp.ClientSession, MCPProvidesEntry, func(), error) {
	engine, containerName, err := resolveContainer(image, instance)
	if err != nil {
		return nil, MCPProvidesEntry{}, func() {}, err
	}
	imageRef, err := containerImageRef(engine, containerName)
	if err != nil {
		return nil, MCPProvidesEntry{}, func() {}, err
	}
	meta, err := ExtractMetadata(engine, imageRef)
	if err != nil {
		return nil, MCPProvidesEntry{}, func() {}, err
	}

	entry, err := resolveMCPEntry(meta, image, containerName, wantName)
	if err != nil {
		return nil, MCPProvidesEntry{}, func() {}, err
	}

	inspection, err := InspectContainer(engine, containerName)
	if err != nil {
		return nil, entry, func() {}, fmt.Errorf("inspecting container %s: %w", containerName, err)
	}

	rewritten, err := rewriteMCPURLForHost(entry.URL, containerName, inspection)
	if err != nil {
		return nil, entry, func() {}, fmt.Errorf("mcp %s: %w", entry.Name, err)
	}
	entry.URL = rewritten

	transport, err := buildMCPTransport(entry)
	if err != nil {
		return nil, entry, func() {}, err
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "ov", Version: ComputeCalVer()}, nil)
	sess, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, entry, func() {}, fmt.Errorf("mcp connect %s (%s): %w", entry.Name, entry.URL, err)
	}
	return sess, entry, func() { _ = sess.Close() }, nil
}

// emitJSON is the shared --json formatter. Uses indented output so human
// scrolling / jq piping both work.
func emitJSON(v any) error {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(out))
	return nil
}
