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
