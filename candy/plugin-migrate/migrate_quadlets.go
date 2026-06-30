package migrate

// migrate_quadlets.go — `charly migrate`.
//
// Walks ~/.config/containers/systemd/ov-*.container, identifies units
// whose deploy declares encrypted volumes but whose quadlet on disk
// lacks the `ExecStartPre=charly config mount <image>` auto-mount hook, and
// regenerates them via the existing `charly config <image>` codepath.
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

	"github.com/overthinkos/overthink/charly/plugin/kit"
)

// MigrateQuadlets is the public form (an empty bundle summary — used standalone /
// pre-prelift). The per-host deploy summary (LoadBundleConfig, package-main) is
// HOST-PRELIFTED by charly core; the registry passes ctx.BundleVolumes to
// migrateQuadlets.
func MigrateQuadlets(quadletDir string, dryRun bool) ([]string, error) {
	return migrateQuadlets(quadletDir, nil, dryRun)
}

// migrateQuadlets regenerates every stale encrypted-volume quadlet under quadletDir
// (those missing the ExecStartPre=charly config mount <name> hook) by re-invoking
// `charly config <name>`. Returns the list of regenerated deploy names (or, under
// dryRun, the names that would be regenerated). Non-interactive (the self-exec'd
// `charly config` resolves secrets from the active credential store with no
// prompt). bundleVolumes is the host-prelifted per-deploy encrypted-volume summary.
func migrateQuadlets(quadletDir string, bundleVolumes []kit.MigrateBundleVolume, dryRun bool) ([]string, error) {
	stale := DetectStaleEncryptedQuadlets(quadletDir, bundleVolumes)
	var done []string
	for _, name := range stale {
		if dryRun {
			done = append(done, name)
			continue
		}
		cmd := exec.Command(os.Args[0], "config", name)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return done, fmt.Errorf("regenerating ov-%s.container: %w", name, err)
		}
		done = append(done, name)
	}
	return done, nil
}

// DetectStaleEncryptedQuadlets returns the sorted list of deploy names whose
// ~/.config/containers/systemd/ov-<name>.container exists, declares encrypted
// volumes, but does NOT carry an `ExecStartPre=… config mount <name>` directive.
//
// Exported for tests. The per-deploy encrypted-volume summary is HOST-PRELIFTED by
// charly core from LoadBundleConfig (package-main, unreachable from this candy):
// core runs the bundle loader — which implicitly runs the legacy-schema guard — and
// hands the {name, target, has-encrypted} summary here.
func DetectStaleEncryptedQuadlets(quadletDir string, summary []kit.MigrateBundleVolume) []string {
	var stale []string
	for _, b := range summary {
		// Only container-class deploys have quadlets.
		switch b.Target {
		case "", "pod", "container":
			// applicable
		default:
			continue
		}
		if !b.HasEncrypted {
			continue
		}
		quadletPath := filepath.Join(quadletDir, "ov-"+b.Name+".container")
		data, err := os.ReadFile(quadletPath)
		if err != nil {
			// No quadlet on disk — nothing to regenerate. The user
			// hasn't run `charly config <name>` yet for this deploy.
			continue
		}
		if !quadletHasMountHook(string(data), b.Name) {
			stale = append(stale, b.Name)
		}
	}
	sort.Strings(stale)
	return stale
}

// quadletHasMountHook reports whether the quadlet body carries an
// `ExecStartPre=…charly config mount <name>` line (the auto-mount hook
// added 2026-04-16). Tolerant to ov-binary path variations
// (`/usr/bin/ov`, `~/.local/bin/ov`, bare `ov`).
func quadletHasMountHook(body, name string) bool {
	for line := range strings.SplitSeq(body, "\n") {
		trim := strings.TrimSpace(line)
		if !strings.HasPrefix(trim, "ExecStartPre=") {
			continue
		}
		rest := strings.TrimPrefix(trim, "ExecStartPre=")
		// Match patterns:
		//   ExecStartPre=/path/charly config mount <name>
		//   ExecStartPre=charly config mount <name>
		//   ExecStartPre=/path/charly config mount <name> --foo
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
