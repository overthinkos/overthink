package main

// migrate_quadlets.go — `ov migrate quadlets`.
//
// Walks ~/.config/containers/systemd/ov-*.container, identifies units
// whose deploy declares encrypted volumes but whose quadlet on disk
// lacks the `ExecStartPre=ov config mount <image>` auto-mount hook, and
// regenerates them via the existing `ov config <image>` codepath.
//
// Why: the auto-mount hook landed 2026-04-16. Quadlets generated before
// that date silently boot containers against empty plain/ FUSE mount-
// points whenever gocryptfs is unmounted (host reboot, manual
// fusermount3 -u, scope-unit crash). The container then writes plaintext
// data into plain/ on top of the populated cipher tree — silent data
// loss with no error surfaced anywhere. This is the actual root cause of
// the 2026-04-18 immich incident.
//
// Idempotent: a quadlet that already has the hook is left untouched.
// Re-running on a fully-migrated tree is a no-op.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// MigrateQuadletsCmd is `ov migrate quadlets`.
type MigrateQuadletsCmd struct {
	DryRun bool `long:"dry-run" help:"List stale quadlets without regenerating"`
}

func (c *MigrateQuadletsCmd) Run() error {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return fmt.Errorf("locating user config dir: %w", err)
	}
	quadletDir := filepath.Join(configDir, "containers", "systemd")

	stale, err := DetectStaleEncryptedQuadlets(quadletDir)
	if err != nil {
		return err
	}
	if len(stale) == 0 {
		fmt.Println("ov migrate quadlets: nothing to migrate (all encrypted-volume quadlets carry ExecStartPre=ov config mount …)")
		return nil
	}

	for _, name := range stale {
		if c.DryRun {
			fmt.Printf("[dry-run] would regenerate ov-%s.container (missing ExecStartPre=ov config mount %s)\n", name, name)
			continue
		}
		fmt.Printf("regenerating ov-%s.container via 'ov config %s'\n", name, name)
		// Re-invoke ourselves so the regenerated quadlet ships every
		// concomitant change (secrets idempotence, encrypted-volume
		// init, daemon-reload, env_provides injection). Self-exec via
		// os.Args[0] follows the ov-binary self-exec pattern documented
		// in /ov-dev:go "Self-exec coordination".
		cmd := exec.Command(os.Args[0], "config", name)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("regenerating ov-%s.container: %w", name, err)
		}
	}
	return nil
}

// DetectStaleEncryptedQuadlets returns the sorted list of deploy names
// whose ~/.config/containers/systemd/ov-<name>.container exists, declares
// encrypted volumes via deploy.yml, but does NOT carry an
// `ExecStartPre=… config mount <name>` directive.
//
// Exported for tests. Reads deploy.yml via LoadDeployConfig — that
// implicitly runs the legacy-schema guard (see C2), so calling this
// against a pre-2026-04 deploy.yml fails loud rather than silently
// returning zero hits.
func DetectStaleEncryptedQuadlets(quadletDir string) ([]string, error) {
	dc, err := LoadDeployConfig()
	if err != nil {
		return nil, err
	}
	if dc == nil {
		return nil, nil
	}
	var stale []string
	for name, node := range dc.Deploy {
		// Only container-class deploys have quadlets.
		switch node.Target {
		case "", "pod", "container":
			// applicable
		default:
			continue
		}
		hasEncrypted := false
		for _, v := range node.Volumes {
			if v.Type == "encrypted" {
				hasEncrypted = true
				break
			}
		}
		if !hasEncrypted {
			continue
		}
		quadletPath := filepath.Join(quadletDir, "ov-"+name+".container")
		data, err := os.ReadFile(quadletPath)
		if err != nil {
			// No quadlet on disk — nothing to regenerate. The user
			// hasn't run `ov config <name>` yet for this deploy.
			continue
		}
		if !quadletHasMountHook(string(data), name) {
			stale = append(stale, name)
		}
	}
	sort.Strings(stale)
	return stale, nil
}

// quadletHasMountHook reports whether the quadlet body carries an
// `ExecStartPre=…ov config mount <name>` line (the auto-mount hook
// added 2026-04-16). Tolerant to ov-binary path variations
// (`/usr/bin/ov`, `~/.local/bin/ov`, bare `ov`).
func quadletHasMountHook(body, name string) bool {
	for _, line := range strings.Split(body, "\n") {
		trim := strings.TrimSpace(line)
		if !strings.HasPrefix(trim, "ExecStartPre=") {
			continue
		}
		rest := strings.TrimPrefix(trim, "ExecStartPre=")
		// Match patterns:
		//   ExecStartPre=/path/ov config mount <name>
		//   ExecStartPre=ov config mount <name>
		//   ExecStartPre=/path/ov config mount <name> --foo
		// The decisive substring is "config mount <name>" anchored at
		// a token boundary at the end (so "mount immich-ml" doesn't
		// match a request for "mount immich").
		needle := "config mount " + name
		if !strings.Contains(rest, needle) {
			continue
		}
		idx := strings.Index(rest, needle)
		end := idx + len(needle)
		if end == len(rest) || rest[end] == ' ' || rest[end] == '\t' {
			return true
		}
	}
	return false
}
