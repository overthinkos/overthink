package main

import (
	"strings"
	"testing"
)

// deploy_executor_nested_test.go — tests for NestedExecutor
// composition (jump generation, Venue() accumulation).

func TestNestedExecutor_VenueChains(t *testing.T) {
	local := LocalDeployExecutor{}
	if local.Venue() != VenueLocal {
		t.Errorf("LocalDeployExecutor.Venue = %q, want %q", local.Venue(), VenueLocal)
	}

	n1 := &NestedExecutor{
		Parent: local,
		Jump:   NestedJump{Kind: JumpPodmanExec, Target: "mybox"},
	}
	if !strings.Contains(n1.Venue(), "podman-exec:mybox") {
		t.Errorf("NestedExecutor Venue missing jump description: %q", n1.Venue())
	}
	if !strings.Contains(n1.Venue(), VenueLocal) {
		t.Errorf("NestedExecutor Venue missing parent venue: %q", n1.Venue())
	}

	// Stacked: container-in-host-in-ssh.
	n2 := &NestedExecutor{
		Parent: n1,
		Jump:   NestedJump{Kind: JumpSSH, Target: "user@guest:2222"},
	}
	v := n2.Venue()
	if !strings.Contains(v, "ssh:user@guest:2222") {
		t.Errorf("stacked Venue missing SSH jump: %q", v)
	}
	if !strings.Contains(v, "podman-exec:mybox") {
		t.Errorf("stacked Venue missing parent jump: %q", v)
	}
}

func TestWrapWithJump_Podman(t *testing.T) {
	j := NestedJump{Kind: JumpPodmanExec, Target: "mybox"}
	out, err := wrapWithJump(j, "echo hi", false)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	if !strings.Contains(out, "podman exec -i") {
		t.Errorf("wrapped missing `podman exec -i`: %s", out)
	}
	if !strings.Contains(out, "'mybox'") {
		t.Errorf("wrapped missing quoted target: %s", out)
	}
	if !strings.Contains(out, "echo hi") {
		t.Errorf("wrapped missing script body: %s", out)
	}
}

func TestWrapWithJump_PodmanRoot(t *testing.T) {
	j := NestedJump{Kind: JumpPodmanExec, Target: "mybox"}
	out, err := wrapWithJump(j, "id", true)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	if !strings.Contains(out, "sudo bash") {
		t.Errorf("root mode missing sudo bash: %s", out)
	}
}

func TestWrapWithJump_SSH(t *testing.T) {
	j := NestedJump{
		Kind:       JumpSSH,
		Target:     "arch@guest.invalid:2224",
		SSHKeyPath: "/tmp/key",
	}
	out, err := wrapWithJump(j, "uname -a", false)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	if !strings.Contains(out, "ssh") {
		t.Errorf("missing ssh invocation: %s", out)
	}
	if !strings.Contains(out, "arch@guest.invalid") {
		t.Errorf("missing target: %s", out)
	}
	if !strings.Contains(out, "-i '/tmp/key'") {
		t.Errorf("missing key flag: %s", out)
	}
	if !strings.Contains(out, "-p 2224") {
		t.Errorf("missing port flag: %s", out)
	}
}

func TestWrapWithJump_VirshConsoleRejected(t *testing.T) {
	j := NestedJump{Kind: JumpVirshConsole, Target: "mydomain"}
	_, err := wrapWithJump(j, "anything", false)
	if err == nil {
		t.Fatal("expected error for JumpVirshConsole (not supported for scripted exec)")
	}
}

func TestParseSSHTarget(t *testing.T) {
	cases := []struct {
		in   string
		user string
		host string
		port int
	}{
		{"arch@127.0.0.1:2224", "arch", "127.0.0.1", 2224},
		{"guest.invalid", "", "guest.invalid", 0},
		{"root@host", "root", "host", 0},
		{"host:22", "", "host", 22},
	}
	for _, tc := range cases {
		u, h, p := parseSSHTarget(tc.in)
		if u != tc.user || h != tc.host || p != tc.port {
			t.Errorf("parseSSHTarget(%q) = (%q,%q,%d), want (%q,%q,%d)",
				tc.in, u, h, p, tc.user, tc.host, tc.port)
		}
	}
}

func TestCopyIntoJumpCommand_PodmanChownChmod(t *testing.T) {
	j := NestedJump{Kind: JumpPodmanExec, Target: "box"}
	cmd, err := copyIntoJumpCommand(j, "/tmp/stage", "/usr/local/bin/foo", 0o755, true)
	if err != nil {
		t.Fatalf("copyIntoJumpCommand: %v", err)
	}
	if !strings.Contains(cmd, "podman cp") {
		t.Errorf("missing podman cp: %s", cmd)
	}
	if !strings.Contains(cmd, "chown root:root") {
		t.Errorf("root ownership requested but chown missing: %s", cmd)
	}
	// fmtOctal renders modes as 0NNN (4-digit) octal — 0755 → "00755".
	if !strings.Contains(cmd, "chmod 00755") {
		t.Errorf("mode 0755 requested but chmod missing: %s", cmd)
	}
}

func TestNestedContainerName(t *testing.T) {
	if got := NestedContainerName("stack.web.db"); got != "stack_web_db" {
		t.Errorf("got %q, want stack_web_db", got)
	}
	if got := NestedContainerName("stack"); got != "stack" {
		t.Errorf("got %q, want stack", got)
	}
}

func TestSSHExecutor_Venue(t *testing.T) {
	e := &SSHExecutor{User: "arch", Host: "127.0.0.1", Port: 2224}
	want := "ssh://arch@127.0.0.1:2224"
	if e.Venue() != want {
		t.Errorf("Venue = %q, want %q", e.Venue(), want)
	}
}

// ---------------------------------------------------------------------------
// 2026-04-27 cutover: heredoc-delim uniqueness + env-var propagation
// ---------------------------------------------------------------------------

// TestNestedExecutor_ThreeLevelNesting_DelimitersUnique verifies the
// heredoc delim collision fix: at 3 levels of nesting (outer → mid →
// inner), each wrap layer must use a DIFFERENT delim or the outer
// bash terminates its heredoc on the first occurrence and the
// trailing closing delims become bare commands → exit 127.
func TestNestedExecutor_ThreeLevelNesting_DelimitersUnique(t *testing.T) {
	innerJump := NestedJump{Kind: JumpPodmanExec, Target: "deepest"}
	midJump := NestedJump{Kind: JumpPodmanExec, Target: "middle"}
	outerJump := NestedJump{Kind: JumpPodmanExec, Target: "outermost"}

	innerScript := `echo hello`
	midScript, err := wrapWithJump(innerJump, innerScript, false)
	if err != nil {
		t.Fatalf("inner wrap: %v", err)
	}
	outerScript, err := wrapWithJump(midJump, midScript, false)
	if err != nil {
		t.Fatalf("mid wrap: %v", err)
	}
	final, err := wrapWithJump(outerJump, outerScript, false)
	if err != nil {
		t.Fatalf("outer wrap: %v", err)
	}

	// Collect delim closing tokens at line start (open delims appear
	// in `<<'<delim>'` form on the heredoc-opening line and don't
	// start with the delim text). Three nesting levels → three
	// distinct close-delims at line start, each appearing exactly
	// once (the matching close for its open). Also verify each
	// distinct close-delim's open delim appears exactly once
	// elsewhere in the script (in `<<'<delim>'` form).
	closeDelims := map[string]int{}
	for _, line := range strings.Split(final, "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "OV_NESTED_SCRIPT_EOF") && !strings.Contains(l, " ") {
			closeDelims[l]++
		}
	}
	if len(closeDelims) < 3 {
		t.Errorf("expected at least 3 distinct close-delims for 3-level nesting; got %d distinct: %v\n--- script ---\n%s",
			len(closeDelims), closeDelims, final)
	}
	for d, n := range closeDelims {
		if n != 1 {
			t.Errorf("close-delim %q appears %d times, want 1", d, n)
		}
		// And its matching open should appear exactly once.
		openMarker := "<<'" + d + "'"
		if got := strings.Count(final, openMarker); got != 1 {
			t.Errorf("open marker %q appears %d times, want 1", openMarker, got)
		}
	}
}

// TestNestedExecutor_EnvVarsPropagated_XdgRuntimeDir verifies the
// load-bearing libvirt-session-socket fix: when XDG_RUNTIME_DIR is
// set in the parent environ, wrapWithJump emits a `--env
// XDG_RUNTIME_DIR=...` flag in the podman exec invocation so the
// libvirt session-socket lookup succeeds inside the nested container.
func TestNestedExecutor_EnvVarsPropagated_XdgRuntimeDir(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/home/user/.local/share/ov-runtime")
	t.Setenv("DISPLAY", "")
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "")

	wrapped, err := wrapWithJump(NestedJump{Kind: JumpPodmanExec, Target: "vm"}, `ov test libvirt info vm`, false)
	if err != nil {
		t.Fatalf("wrapWithJump: %v", err)
	}
	if !strings.Contains(wrapped, "--env") {
		t.Errorf("expected `--env` flag in podman exec invocation; got:\n%s", wrapped)
	}
	if !strings.Contains(wrapped, "XDG_RUNTIME_DIR=/home/user/.local/share/ov-runtime") {
		t.Errorf("expected XDG_RUNTIME_DIR propagation; got:\n%s", wrapped)
	}
}

// TestNestedExecutor_EnvVarsPropagated_DisplayWayland verifies that
// the other display-related env vars in the allowlist are also
// propagated.
func TestNestedExecutor_EnvVarsPropagated_DisplayWayland(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("DISPLAY", ":1")
	t.Setenv("WAYLAND_DISPLAY", "wayland-2")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/run/user/1000/bus")

	wrapped, err := wrapWithJump(NestedJump{Kind: JumpPodmanExec, Target: "ov-fixture-desktop"}, `ov test wl status fixture-desktop`, false)
	if err != nil {
		t.Fatalf("wrapWithJump: %v", err)
	}
	for _, expect := range []string{"DISPLAY=:1", "WAYLAND_DISPLAY=wayland-2"} {
		if !strings.Contains(wrapped, expect) {
			t.Errorf("expected propagation of %q; got:\n%s", expect, wrapped)
		}
	}
}
