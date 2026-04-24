package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// ResolveDispatcher routing
// ---------------------------------------------------------------------------

func TestResolveDispatcher_Routing(t *testing.T) {
	cases := []struct {
		target string
		want   string // expected Kind()
	}{
		{"host", "host"},
		{"pod", "pod"},
		{"container", "pod"}, // legacy alias
		{"", "pod"},          // empty defaults to pod
		{"vm", "vm"},
	}
	for _, c := range cases {
		d, err := ResolveDispatcher(&DeploymentNode{Target: c.target}, "test")
		if err != nil {
			t.Errorf("Target=%q: unexpected error %v", c.target, err)
			continue
		}
		if d.Kind() != c.want {
			t.Errorf("Target=%q: Kind() = %q; want %q", c.target, d.Kind(), c.want)
		}
	}
}

func TestResolveDispatcher_K8sRejected(t *testing.T) {
	for _, target := range []string{"k8s", "kubernetes"} {
		_, err := ResolveDispatcher(&DeploymentNode{Target: target}, "test")
		if !errors.Is(err, ErrK8sUnsupported) {
			t.Errorf("Target=%q: want ErrK8sUnsupported, got %v", target, err)
		}
	}
}

func TestResolveDispatcher_UnknownTarget(t *testing.T) {
	_, err := ResolveDispatcher(&DeploymentNode{Target: "mars"}, "test")
	if !errors.Is(err, ErrUnknownTarget) {
		t.Errorf("want ErrUnknownTarget, got %v", err)
	}
}

func TestResolveDispatcher_NilNode(t *testing.T) {
	_, err := ResolveDispatcher(nil, "test")
	if err == nil {
		t.Error("nil node should error")
	}
}

// ---------------------------------------------------------------------------
// WorkspacePath
// ---------------------------------------------------------------------------

func TestWorkspacePath_Per(t *testing.T) {
	layout := NewRunLayout("/proj", "abc123")

	hd := &hostDispatcher{name: "test"}
	if got := hd.WorkspacePath(layout); got != "/proj/.benchmark/abc123/worktree" {
		t.Errorf("host: %q", got)
	}

	pd := &podDispatcher{name: "test", containerName: "ov-test"}
	if got := pd.WorkspacePath(layout); got != "/workspace/.benchmark/abc123/worktree" {
		t.Errorf("pod: %q", got)
	}

	vd := &vmDispatcher{name: "test"}
	if got := vd.WorkspacePath(layout); got != "/workspace/.benchmark/abc123/worktree" {
		t.Errorf("vm: %q", got)
	}
}

// ---------------------------------------------------------------------------
// hostDispatcher.Build
// ---------------------------------------------------------------------------

func TestHostDispatcher_Build(t *testing.T) {
	layout := NewRunLayout("/proj", "id")
	hd := &hostDispatcher{node: &DeploymentNode{}, name: "test"}

	cmd, err := hd.Build(context.Background(), layout,
		[]string{"echo", "hello"},
		map[string]string{"FOO": "bar"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Dir != "/proj/.benchmark/id/worktree" {
		t.Errorf("cwd: %q", cmd.Dir)
	}
	// Env should include FOO=bar.
	foundFoo := false
	for _, e := range cmd.Env {
		if e == "FOO=bar" {
			foundFoo = true
			break
		}
	}
	if !foundFoo {
		t.Error("FOO=bar missing from Env")
	}
}

func TestHostDispatcher_Build_EmptyArgvErrors(t *testing.T) {
	hd := &hostDispatcher{node: &DeploymentNode{}, name: "test"}
	_, err := hd.Build(context.Background(), NewRunLayout("/p", "i"), nil, nil)
	if err == nil {
		t.Error("empty argv should error")
	}
}

// ---------------------------------------------------------------------------
// podDispatcher.Build
// ---------------------------------------------------------------------------

func TestPodDispatcher_Build_ComposesExecArgs(t *testing.T) {
	layout := NewRunLayout("/proj", "xyz")
	pd := &podDispatcher{
		node:          &DeploymentNode{Engine: "podman"},
		name:          "sway-pod",
		containerName: "ov-sway-pod",
	}

	cmd, err := pd.Build(context.Background(), layout,
		[]string{"claude", "-p", "${PROMPT}"},
		map[string]string{"OV_BENCHMARK_RUN_ID": "xyz"},
	)
	if err != nil {
		t.Fatal(err)
	}

	argv := append([]string{cmd.Path}, cmd.Args...)
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "exec") {
		t.Errorf("missing exec: %v", argv)
	}
	if !strings.Contains(joined, "ov-sway-pod") {
		t.Errorf("missing container name: %v", argv)
	}
	if !strings.Contains(joined, "-w") {
		t.Errorf("missing -w (workspace): %v", argv)
	}
	if !strings.Contains(joined, "/workspace/.benchmark/xyz/worktree") {
		t.Errorf("missing workspace path: %v", argv)
	}
	if !strings.Contains(joined, "OV_BENCHMARK_RUN_ID=xyz") {
		t.Errorf("missing env -e: %v", argv)
	}
}

func TestPodDispatcher_PickEngineDefault(t *testing.T) {
	pd := &podDispatcher{node: &DeploymentNode{}, name: "x", containerName: "ov-x"}
	cmd, _ := pd.Build(context.Background(), NewRunLayout("/p", "i"), []string{"echo"}, nil)
	if !strings.Contains(cmd.Args[0]+" "+strings.Join(cmd.Args[1:], " "), "podman") {
		// cmd.Args[0] is the binary; podman exec ...
		if !strings.Contains(cmd.Args[0], "podman") {
			t.Errorf("default engine should be podman, got %v", cmd.Args)
		}
	}
}

func TestPodDispatcher_PickEngineOverride(t *testing.T) {
	pd := &podDispatcher{node: &DeploymentNode{Engine: "docker"}, name: "x", containerName: "ov-x"}
	cmd, _ := pd.Build(context.Background(), NewRunLayout("/p", "i"), []string{"echo"}, nil)
	if !strings.Contains(cmd.Args[0], "docker") {
		t.Errorf("Engine=docker should propagate, got %v", cmd.Args[0])
	}
}

// ---------------------------------------------------------------------------
// vmDispatcher.Build
// ---------------------------------------------------------------------------

func TestVmDispatcher_Build_UsesOvVmSsh(t *testing.T) {
	vd := &vmDispatcher{node: &DeploymentNode{}, name: "arch-vm"}
	cmd, err := vd.Build(context.Background(), NewRunLayout("/p", "id"),
		[]string{"claude", "-p", "hi"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	argv := strings.Join(cmd.Args, " ")
	if !strings.Contains(argv, "vm") || !strings.Contains(argv, "ssh") || !strings.Contains(argv, "arch-vm") {
		t.Errorf("vm dispatch should use ov vm ssh: %v", cmd.Args)
	}
	// The remote command is wrapped in `sh -c`; cd + export + exec pattern.
	if !strings.Contains(argv, "cd ") || !strings.Contains(argv, "exec") {
		t.Errorf("vm remote command missing cd/exec pattern: %v", cmd.Args)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func TestBenchShellQuote(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"simple", "'simple'"},
		{"has space", "'has space'"},
		{"it's", `'it'"'"'s'`},
	}
	for _, c := range cases {
		got := benchShellQuote(c.in)
		if got != c.want {
			t.Errorf("benchShellQuote(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

func TestExpandHostPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	cases := []struct {
		in, want string
	}{
		{"/absolute", "/absolute"},
		{"relative/path", "relative/path"},
		{"~", home},
		{"~/foo", filepath.Join(home, "foo")},
	}
	for _, c := range cases {
		got, err := expandHostPath(c.in)
		if err != nil {
			t.Errorf("expand(%q) err: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("expand(%q) = %q; want %q", c.in, got, c.want)
		}
	}
	if _, err := expandHostPath(""); err == nil {
		t.Error("empty path should error")
	}
}

func TestMergeOsEnv(t *testing.T) {
	t.Setenv("EXISTING_VAR", "original")
	env := mergeOsEnv(map[string]string{"EXISTING_VAR": "overridden", "NEW_VAR": "fresh"})
	foundOverride := false
	foundFresh := false
	for _, e := range env {
		if e == "EXISTING_VAR=overridden" {
			foundOverride = true
		}
		if e == "NEW_VAR=fresh" {
			foundFresh = true
		}
		if e == "EXISTING_VAR=original" {
			t.Error("original should have been replaced")
		}
	}
	if !foundOverride || !foundFresh {
		t.Errorf("merge failed: override=%v, fresh=%v", foundOverride, foundFresh)
	}
}

func TestMergeOsEnv_EmptyOverrides(t *testing.T) {
	env := mergeOsEnv(nil)
	// Must return os.Environ() untouched when overrides are empty.
	if len(env) != len(os.Environ()) {
		t.Errorf("empty overrides: got %d vars, want %d", len(env), len(os.Environ()))
	}
}

// ---------------------------------------------------------------------------
// SyncCredentials end-to-end — the load-bearing privacy invariant test
// ---------------------------------------------------------------------------

// TestSyncCredentials_EndToEnd proves two things at once:
//  1. SyncCredentials actually copies the file into the "deploy" (a
//     tempdir acting as the deploy's $HOME).
//  2. The credentials NEVER leak into the git worktree — `git status`
//     on the worktree stays clean after sync.
//
// We use hostDispatcher because it exercises the real
// syncCredentialsLocal code path without requiring a live container.
func TestSyncCredentials_EndToEnd(t *testing.T) {
	// Set up: a git repo + a benchmark worktree.
	projectDir := initGitRepo(t)
	l := NewRunLayout(projectDir, "cred-test")
	ctx := context.Background()
	if err := CreateWorktree(ctx, l); err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}
	defer func() { _ = RemoveWorktree(ctx, l) }()

	// Set up: a fake host $HOME with a credential file, and a fake
	// deploy $HOME.
	hostHome := t.TempDir()
	deployHome := t.TempDir()
	hostCredDir := filepath.Join(hostHome, ".claude")
	if err := os.MkdirAll(hostCredDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := "sk-ant-test-token-42"
	if err := os.WriteFile(filepath.Join(hostCredDir, "token"), []byte(sentinel), 0o600); err != nil {
		t.Fatal(err)
	}

	// Invoke SyncCredentials on the host dispatcher.
	hd := &hostDispatcher{node: &DeploymentNode{}, name: "cred-test"}
	mounts := []CredentialMount{
		{Src: hostCredDir, Dst: filepath.Join(deployHome, ".claude")},
	}
	if err := hd.SyncCredentials(ctx, mounts); err != nil {
		t.Fatalf("SyncCredentials: %v", err)
	}

	// Assertion (i): the sentinel landed at deployHome/.claude/token
	// with matching contents.
	got, err := os.ReadFile(filepath.Join(deployHome, ".claude", "token"))
	if err != nil {
		t.Fatalf("read synced token: %v", err)
	}
	if string(got) != sentinel {
		t.Errorf("sentinel mismatch: got %q, want %q", string(got), sentinel)
	}

	// Assertion (ii): no file named "token" anywhere under the worktree.
	var leakFound bool
	_ = filepath.Walk(l.WorktreeDir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() && info.Name() == "token" {
			leakFound = true
		}
		return nil
	})
	if leakFound {
		t.Error("PRIVACY INVARIANT VIOLATED: credential 'token' found inside worktree")
	}

	// Assertion (iii): git status on the worktree is clean.
	statusCmd := exec.CommandContext(ctx, "git", "-C", l.WorktreeDir, "status", "--porcelain")
	out, err := statusCmd.Output()
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("PRIVACY INVARIANT VIOLATED: git status not clean after sync:\n%s", string(out))
	}
}

func TestSyncCredentials_OptionalSrcMissing(t *testing.T) {
	hostHome := t.TempDir()
	deployHome := t.TempDir()
	hd := &hostDispatcher{node: &DeploymentNode{}, name: "opt"}

	// Required src missing: should error.
	err := hd.SyncCredentials(context.Background(), []CredentialMount{
		{Src: filepath.Join(hostHome, "missing"), Dst: filepath.Join(deployHome, "x")},
	})
	if err == nil {
		t.Error("required missing src should error")
	}

	// Optional src missing: should succeed silently.
	err = hd.SyncCredentials(context.Background(), []CredentialMount{
		{Src: filepath.Join(hostHome, "missing"), Dst: filepath.Join(deployHome, "x"), Optional: true},
	})
	if err != nil {
		t.Errorf("optional missing src should NOT error: %v", err)
	}
}

func TestSyncCredentials_SingleFile(t *testing.T) {
	hostHome := t.TempDir()
	deployHome := t.TempDir()
	srcFile := filepath.Join(hostHome, "config.json")
	if err := os.WriteFile(srcFile, []byte(`{"key": "val"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	hd := &hostDispatcher{node: &DeploymentNode{}, name: "single"}
	err := hd.SyncCredentials(context.Background(), []CredentialMount{
		{Src: srcFile, Dst: filepath.Join(deployHome, "config.json")},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(deployHome, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"key": "val"}` {
		t.Errorf("single-file copy failed: %q", string(got))
	}
}

// ---------------------------------------------------------------------------
// CleanupCredentials
// ---------------------------------------------------------------------------

func TestCleanupCredentials_RemovesDst(t *testing.T) {
	deployHome := t.TempDir()
	target := filepath.Join(deployHome, ".claude")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "token"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	hd := &hostDispatcher{node: &DeploymentNode{}, name: "cleanup"}
	err := hd.CleanupCredentials(context.Background(), []CredentialMount{
		{Src: "/nonexistent", Dst: target},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("target should be removed: err=%v", err)
	}
}
