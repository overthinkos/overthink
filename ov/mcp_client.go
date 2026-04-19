package main

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcp_client.go is the SDK-wrapper layer used by ov/mcp.go. It isolates
// three concerns that the CLI leaves otherwise don't care about:
//
//  1. MCP-server discovery — resolving an image name to a single
//     MCPProvidesEntry (name, URL, transport) via OCI labels + pod-aware
//     localhost rewrite.
//  2. Host reachability — OCI-label URLs use container-network hostnames
//     (`http://ov-jupyter:8888/mcp`) that don't resolve from the host, so
//     we rewrite to `127.0.0.1:<published-host-port>` using the same
//     port-mapping data that powers ${HOST_PORT:N} in testvars.go.
//  3. Transport dispatch — the SDK's transports are struct-literal
//     constructors, so mapping "http"|""|"sse" → a Transport implementation
//     is a small switch.

// resolveMCPEntry picks the target MCPProvidesEntry for a given image.
//
// Inputs:
//   - meta:     image metadata from ExtractMetadata (provides meta.MCPProvides)
//   - image:    logical image name (for pod-aware self-match)
//   - ctrName:  running container name (for template substitution)
//   - wantName: optional discriminator when the image provides multiple servers
//
// Returns a single MCPProvidesEntry with template fields resolved (no
// {{.ContainerName}} left) and with same-image URLs rewritten to localhost
// (pod-aware path). Matches the pattern used by injectMCPProvides at
// ov config time.
func resolveMCPEntry(meta *ImageMetadata, image, ctrName, wantName string) (MCPProvidesEntry, error) {
	if meta == nil || len(meta.MCPProvides) == 0 {
		return MCPProvidesEntry{}, fmt.Errorf("image %q declares no mcp_provides", image)
	}

	entries := make([]MCPProvidesEntry, 0, len(meta.MCPProvides))
	for _, p := range meta.MCPProvides {
		entries = append(entries, MCPProvidesEntry{
			Name:      p.Name,
			URL:       resolveContainerNameTemplate(p.URL, ctrName),
			Transport: p.Transport,
			Source:    image,
		})
	}

	resolved := podAwareMCPProvides(entries, image, ctrName)
	return pickMCPEntry(resolved, wantName)
}

// pickMCPEntry disambiguates by name. Empty wantName auto-picks when there
// is exactly one entry; errors when there are multiple with a clear listing.
func pickMCPEntry(entries []MCPProvidesEntry, wantName string) (MCPProvidesEntry, error) {
	switch {
	case len(entries) == 0:
		return MCPProvidesEntry{}, fmt.Errorf("no mcp_provides entries to pick from")
	case wantName != "":
		for _, e := range entries {
			if e.Name == wantName {
				return e, nil
			}
		}
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name
		}
		return MCPProvidesEntry{}, fmt.Errorf("no mcp_provides entry named %q (available: %s)", wantName, strings.Join(names, ", "))
	case len(entries) == 1:
		return entries[0], nil
	default:
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name
		}
		return MCPProvidesEntry{}, fmt.Errorf("image provides multiple mcp servers; use --name (available: %s)", strings.Join(names, ", "))
	}
}

// resolveContainerNameTemplate substitutes the {{.ContainerName}} template
// token with the running container name. No full Go-template engine needed —
// the only placeholder ov emits into mcp_provides URLs is {{.ContainerName}}.
func resolveContainerNameTemplate(raw, ctrName string) string {
	if ctrName == "" {
		return raw
	}
	return strings.ReplaceAll(raw, "{{.ContainerName}}", ctrName)
}

// rewriteMCPURLForHost rewrites a container-network URL to a host-routable
// one using the published port-mapping from a container inspection. If the
// URL's host is not the container name, it is returned unchanged — the user
// may have set an explicit external URL.
func rewriteMCPURLForHost(rawURL, ctrName string, inspect *ContainerInspection) (string, error) {
	if rawURL == "" {
		return "", fmt.Errorf("mcp URL is empty")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parsing mcp URL %q: %w", rawURL, err)
	}
	// Host alias forms we accept: the bare container name, or an already-
	// rewritten "localhost" (pod-aware). Leave anything else alone.
	host := u.Hostname()
	if host != ctrName && host != "localhost" && host != "127.0.0.1" {
		return rawURL, nil
	}
	if u.Port() == "" {
		return "", fmt.Errorf("mcp URL %q has no port (cannot map to a host port)", rawURL)
	}
	if inspect == nil {
		return "", fmt.Errorf("no container inspection; cannot resolve host port for %q", rawURL)
	}
	hostPort, ok := lookupHostPort(inspect, u.Port())
	if !ok {
		return "", fmt.Errorf("container port %s/tcp is not published to a host port; declare `ports: [%s:%s]` in the image or run the test from inside the pod", u.Port(), u.Port(), u.Port())
	}
	u.Host = "127.0.0.1:" + hostPort
	return u.String(), nil
}

// lookupHostPort reads the first host-side port binding for a given container
// port. Mirrors the logic in mergeRuntimeVars (testvars.go:200-213).
func lookupHostPort(inspect *ContainerInspection, containerPort string) (string, bool) {
	if inspect == nil {
		return "", false
	}
	for key, binds := range inspect.NetworkSettings.Ports {
		portStr := key
		if i := strings.IndexByte(key, '/'); i >= 0 {
			portStr = key[:i]
		}
		if portStr != containerPort {
			continue
		}
		if len(binds) == 0 {
			continue
		}
		if hp := binds[0].HostPort; hp != "" {
			return hp, true
		}
	}
	return "", false
}

// buildMCPTransport constructs the SDK transport for a resolved entry. The
// transport field is advisory ("http" / "sse" / ""); empty and "http" both
// map to Streamable HTTP (the overthink default and what both the jupyter
// and chrome-devtools MCP servers expose).
func buildMCPTransport(entry MCPProvidesEntry) (mcp.Transport, error) {
	switch strings.ToLower(entry.Transport) {
	case "", "http", "streamable", "streamable-http":
		return &mcp.StreamableClientTransport{Endpoint: entry.URL}, nil
	case "sse":
		return &mcp.SSEClientTransport{Endpoint: entry.URL}, nil
	default:
		return nil, fmt.Errorf("unsupported mcp transport %q (expected http or sse)", entry.Transport)
	}
}

// formatTool renders a *mcp.Tool as a single tab-separated line so author-
// facing matchers can `contains: insert_cell` without having to decode JSON.
// Multi-line descriptions collapse to the first non-empty line.
func formatTool(t *mcp.Tool) string {
	if t == nil {
		return ""
	}
	return t.Name + "\t" + firstLine(t.Description)
}

// formatResource renders a *mcp.Resource as <uri>\t<name>\t<mime>.
func formatResource(r *mcp.Resource) string {
	if r == nil {
		return ""
	}
	return r.URI + "\t" + r.Name + "\t" + r.MIMEType
}

// formatPrompt renders a *mcp.Prompt as <name>\t<description>.
func formatPrompt(p *mcp.Prompt) string {
	if p == nil {
		return ""
	}
	return p.Name + "\t" + firstLine(p.Description)
}

// firstLine returns the first non-empty line of s with trailing whitespace
// stripped. Used to keep the one-line-per-record output shape even when
// server-reported descriptions span multiple lines.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// extractToolText collects the textual portion of a CallToolResult, one line
// per TextContent block. Non-text content (images, resource links) is
// summarised with a bracketed placeholder so the author sees it happened
// without trying to print binary data.
func extractToolText(res *mcp.CallToolResult) string {
	if res == nil {
		return ""
	}
	var b strings.Builder
	for i, c := range res.Content {
		if i > 0 {
			b.WriteByte('\n')
		}
		switch v := c.(type) {
		case *mcp.TextContent:
			b.WriteString(v.Text)
		case *mcp.ImageContent:
			fmt.Fprintf(&b, "[image content: %s, %d bytes]", v.MIMEType, len(v.Data))
		case *mcp.AudioContent:
			fmt.Fprintf(&b, "[audio content: %s, %d bytes]", v.MIMEType, len(v.Data))
		default:
			fmt.Fprintf(&b, "[non-text content: %T]", c)
		}
	}
	return b.String()
}
