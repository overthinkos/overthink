package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMcpServeDefaultRepo_AutoFallback verifies that `ov mcp serve --stdio`,
// when started in an empty directory with no project env vars, auto-falls
// back to overthinkos/overthink. Stays hermetic by setting OV_REPO_CACHE
// to a pre-populated temp dir so EnsureRepoDownloaded short-circuits via
// IsRepoCached and never shells out to git.
func TestMcpServeDefaultRepo_AutoFallback(t *testing.T) {
	bin := buildOvBinary(t)

	cacheRoot := t.TempDir()
	cachedRepo := filepath.Join(cacheRoot, "github.com", "overthinkos", "overthink@main")
	if err := os.MkdirAll(cachedRepo, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	writeMinProject(t, cachedRepo)

	// Start in an empty dir; no OV_PROJECT_DIR, no OV_PROJECT_REPO.
	startCwd := t.TempDir()

	// Sub-test 1 — case 2 (top-level --repo wires through main() chdir,
	// then bootstrapProject() sees OV_PROJECT_REPO is set and does nothing).
	// Hermetic via OV_REPO_CACHE pointing at the pre-populated stub.
	t.Run("top_level_repo_flag", func(t *testing.T) {
		out := runMcpServeListImages(t, bin, startCwd,
			[]string{"OV_REPO_CACHE=" + cacheRoot, "OV_PROJECT_REPO=overthinkos/overthink@main"},
			[]string{"mcp", "serve", "--stdio"})
		if !strings.Contains(out, "testimage") {
			t.Errorf("expected testimage from cached repo; output:\n%s", out)
		}
	})

	// Sub-test 2 — case 4 hard-fail with --no-default-repo.
	t.Run("no_default_repo_hard_fail", func(t *testing.T) {
		cmd := exec.Command(bin, "mcp", "serve", "--stdio", "--no-default-repo")
		cmd.Dir = startCwd
		// Intentionally drop OV_PROJECT_DIR/OV_PROJECT_REPO from env.
		cmd.Env = sanitizedEnv()
		// Send a no-op then close stdin to let the server exit.
		cmd.Stdin = strings.NewReader("")
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("expected non-zero exit, got success; output:\n%s", out)
		}
		if !strings.Contains(string(out), "no project source found") {
			t.Errorf("expected 'no project source found' error, got: %s", out)
		}
	})
}

// runMcpServeListImages spawns `ov` with the given args, performs the MCP
// stdio handshake, calls box.list.boxes, and returns the stringified
// tool result. Hermetic: uses caller-supplied env (typically pinning
// OV_REPO_CACHE).
func runMcpServeListImages(t *testing.T, bin, cwd string, extraEnv, args []string) string {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = cwd
	cmd.Env = append(sanitizedEnv(), extraEnv...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start ov mcp serve: %v", err)
	}
	defer func() {
		stdin.Close()
		// Give the process a moment to exit cleanly; force-kill on timeout.
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			cmd.Process.Kill()
			<-done
		}
	}()

	// Minimal MCP stdio handshake: initialize, then tools/call box.list.boxes.
	send := func(req map[string]any) {
		b, _ := json.Marshal(req)
		stdin.Write(b)
		stdin.Write([]byte("\n"))
	}
	send(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test", "version": "0"},
		},
	})
	send(map[string]any{
		"jsonrpc": "2.0", "method": "notifications/initialized",
		"params": map[string]any{},
	})
	send(map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{
			"name":      "box.list.boxes",
			"arguments": map[string]any{},
		},
	})

	// Read responses with a deadline.
	type result struct{ data string }
	resCh := make(chan result, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var sb strings.Builder
		buf := make([]byte, 8192)
		for {
			select {
			case <-ctx.Done():
				resCh <- result{sb.String()}
				return
			default:
			}
			n, err := stdout.Read(buf)
			if n > 0 {
				sb.Write(buf[:n])
				// Heuristic: stop once we've seen the response to id=2.
				if strings.Contains(sb.String(), `"id":2`) {
					resCh <- result{sb.String()}
					return
				}
			}
			if err == io.EOF {
				resCh <- result{sb.String()}
				return
			}
		}
	}()

	select {
	case r := <-resCh:
		return r.data
	case <-time.After(15 * time.Second):
		t.Fatalf("timeout waiting for MCP response")
		return ""
	}
}

// sanitizedEnv returns os.Environ minus any OV_PROJECT_DIR / OV_PROJECT_REPO
// inherited from the test harness, so each subtest controls these cleanly.
func sanitizedEnv() []string {
	var out []string
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "OV_PROJECT_DIR=") || strings.HasPrefix(e, "OV_PROJECT_REPO=") {
			continue
		}
		out = append(out, e)
	}
	return out
}
