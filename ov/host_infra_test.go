package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Tests for the four Task-9 host-infra files.

// ---------------- hostdistro.go ----------------

func TestDetectHostDistroFedora43(t *testing.T) {
	// Verify parsing against a realistic os-release body — we synthesize
	// a temp file and exercise the line-parsing primitive.
	tests := []struct {
		line string
		k, v string
		ok   bool
	}{
		{`ID=fedora`, "ID", "fedora", true},
		{`VERSION_ID=43`, "VERSION_ID", "43", true},
		{`ID_LIKE="debian ubuntu"`, "ID_LIKE", "debian ubuntu", true},
		{`NAME='Fedora Linux'`, "NAME", "Fedora Linux", true},
		{`# comment`, "", "", false},
		{``, "", "", false},
	}
	for _, tc := range tests {
		k, v, ok := splitOsReleaseLine(tc.line)
		if k != tc.k || v != tc.v || ok != tc.ok {
			t.Errorf("splitOsReleaseLine(%q) = (%q, %q, %v); want (%q, %q, %v)",
				tc.line, k, v, ok, tc.k, tc.v, tc.ok)
		}
	}
}

func TestHostDistroTagsAndFormatHint(t *testing.T) {
	tests := []struct {
		hd       *HostDistro
		wantTag  string
		wantFmt  string
	}{
		{
			hd:      &HostDistro{ID: "fedora", VersionID: "43"},
			wantTag: "fedora:43",
			wantFmt: "rpm",
		},
		{
			hd:      &HostDistro{ID: "ubuntu", VersionID: "24.04", IDLike: []string{"debian"}},
			wantTag: "ubuntu:24.04",
			wantFmt: "deb",
		},
		{
			hd:      &HostDistro{ID: "arch"},
			wantTag: "arch",
			wantFmt: "pac",
		},
	}
	for _, tc := range tests {
		tc.hd.populateTags()
		if got := tc.hd.PrimaryTag(); got != tc.wantTag {
			t.Errorf("PrimaryTag() = %q, want %q", got, tc.wantTag)
		}
		if got := tc.hd.FormatHint(); got != tc.wantFmt {
			t.Errorf("FormatHint() = %q, want %q", got, tc.wantFmt)
		}
	}
}

func TestParseGlibcVersion(t *testing.T) {
	tests := map[string]string{
		"ldd (GNU libc) 2.39\n":                            "2.39",
		"ldd (Ubuntu GLIBC 2.35-0ubuntu3.8) 2.35\n":        "2.35",
		"ldd (GNU libc) 2.38.0\n":                          "2.38",
		"something unexpected\n":                           "",
		"":                                                 "",
	}
	for in, want := range tests {
		if got := parseGlibcVersion(in); got != want {
			t.Errorf("parseGlibcVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCompareGlibc(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"2.39", "2.35", 1},
		{"2.35", "2.39", -1},
		{"2.35", "2.35", 0},
		{"2.39.0", "2.39.1", 0},
		{"2.40", "2.9", 1},
		{"", "2.39", 0}, // unknown compares equal
		{"2.39", "", 0},
	}
	for _, tc := range tests {
		if got := CompareGlibc(tc.a, tc.b); got != tc.want {
			t.Errorf("CompareGlibc(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

// ---------------- install_ledger.go ----------------

func withTempLedger(t *testing.T) *LedgerPaths {
	t.Helper()
	root := t.TempDir()
	return &LedgerPaths{
		Root:     root,
		Deploys:  filepath.Join(root, "deploys"),
		Layers:   filepath.Join(root, "layers"),
		LockFile: filepath.Join(root, ".lock"),
	}
}

func TestLedgerRoundTrip(t *testing.T) {
	paths := withTempLedger(t)
	rec := &DeployRecord{
		DeployID:   "abc123",
		Image:      "fedora-coder",
		Target:     "host",
		Layers:     []string{"ripgrep", "uv"},
		DeployedAt: "2026-04-21T00:00:00Z",
	}
	if err := WriteDeployRecord(paths, rec); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadDeployRecord(paths, "abc123")
	if err != nil || got == nil {
		t.Fatalf("read: %v / %+v", err, got)
	}
	if got.Image != "fedora-coder" || len(got.Layers) != 2 {
		t.Errorf("round-trip broken: %+v", got)
	}
}

func TestLedgerRefcount(t *testing.T) {
	paths := withTempLedger(t)
	// Deploy A and B both include ripgrep.
	if err := AddLayerDeployment(paths, "ripgrep", "deploy-A", nil); err != nil {
		t.Fatal(err)
	}
	if err := AddLayerDeployment(paths, "ripgrep", "deploy-B", nil); err != nil {
		t.Fatal(err)
	}
	rec, _ := ReadLayerRecord(paths, "ripgrep")
	if len(rec.DeployedBy) != 2 {
		t.Errorf("DeployedBy = %v, want 2 entries", rec.DeployedBy)
	}

	// Remove A — ripgrep stays.
	_, shouldRemove, err := RemoveLayerDeployment(paths, "ripgrep", "deploy-A")
	if err != nil {
		t.Fatal(err)
	}
	if shouldRemove {
		t.Errorf("shouldRemove=true after removing one of two deployers")
	}
	rec, _ = ReadLayerRecord(paths, "ripgrep")
	if len(rec.DeployedBy) != 1 || rec.DeployedBy[0] != "deploy-B" {
		t.Errorf("after decrement: %v", rec.DeployedBy)
	}

	// Remove B — ripgrep should fully teardown.
	_, shouldRemove, err = RemoveLayerDeployment(paths, "ripgrep", "deploy-B")
	if err != nil {
		t.Fatal(err)
	}
	if !shouldRemove {
		t.Errorf("shouldRemove=false when DeployedBy drains to empty")
	}
}

func TestLedgerFlock(t *testing.T) {
	paths := withTempLedger(t)
	lock, err := AcquireLedgerLock(paths)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	// Can't easily test contention without a second process — at least
	// verify release succeeds and the lock file exists.
	if _, err := os.Stat(paths.LockFile); err != nil {
		t.Errorf("lock file not created: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Errorf("release: %v", err)
	}
}

// ---------------- builder_run.go ----------------

func TestBuildBuilderRunArgs(t *testing.T) {
	opts := BuilderRunOpts{
		BuilderImage: "fedora-builder:latest",
		LayerDir:     "/home/user/layers/pre-commit",
		HostHome:     "/home/user",
		BindMounts: map[string]string{
			"/home/user/.pixi": "/home/user/.pixi",
		},
		Env: map[string]string{
			"PIXI_CACHE_DIR": "/home/user/.cache/ov/pixi",
		},
	}
	args := buildBuilderRunArgs(opts)
	want := []string{
		"run", "--rm",
		"--user", // we don't check the exact uid because it varies
	}
	if len(args) < len(want) {
		t.Fatalf("args too short: %v", args)
	}
	for i, w := range want {
		if args[i] != w {
			t.Errorf("args[%d] = %q, want %q (full: %v)", i, args[i], w, args)
		}
	}
	// Verify critical pieces are present.
	fullCmd := strings.Join(args, " ")
	mustContain := []string{
		"fedora-builder:latest",
		"-v /home/user/.pixi:/home/user/.pixi:rw",
		"-v /home/user/layers/pre-commit:/work:ro",
		"-e HOME=/home/user",
		"-e PIXI_CACHE_DIR=/home/user/.cache/ov/pixi",
		"-w /work",
		"bash -s",
	}
	for _, m := range mustContain {
		if !strings.Contains(fullCmd, m) {
			t.Errorf("missing %q in args: %s", m, fullCmd)
		}
	}
}

func TestBuilderRunDryRun(t *testing.T) {
	// DryRun should return nil, nil without actually exec'ing.
	out, err := BuilderRun(context.Background(), BuilderRunOpts{
		BuilderImage: "fedora-builder",
		DryRun:       true,
		ScriptBody:   "echo hi",
	})
	if err != nil {
		t.Errorf("dry-run should not error: %v", err)
	}
	if out != nil {
		t.Errorf("dry-run should return nil output; got %q", out)
	}
}

// ---------------- shell_profile.go ----------------

func TestRenderEnvdBody(t *testing.T) {
	body := renderEnvdBody("pre-commit",
		map[string]string{
			"PIXI_CACHE_DIR": "/home/u/.cache/pixi",
			"FOO":            "bar baz",
		},
		[]string{"/home/u/.pixi/bin", "/home/u/.local/bin"},
	)
	// Deterministic ordering (sorted keys).
	lines := strings.Split(body, "\n")
	// Find the FOO and PIXI_CACHE_DIR lines to check ordering.
	var fooIdx, pixiIdx int = -1, -1
	for i, l := range lines {
		if strings.HasPrefix(l, "export FOO=") {
			fooIdx = i
		}
		if strings.HasPrefix(l, "export PIXI_CACHE_DIR=") {
			pixiIdx = i
		}
	}
	if fooIdx == -1 || pixiIdx == -1 {
		t.Fatalf("missing env lines; body:\n%s", body)
	}
	if fooIdx > pixiIdx {
		t.Errorf("sort order broken: FOO at %d, PIXI_CACHE_DIR at %d", fooIdx, pixiIdx)
	}
	if !strings.Contains(body, "export PATH=") {
		t.Errorf("missing PATH line; body:\n%s", body)
	}
	if !strings.Contains(body, "/home/u/.pixi/bin") {
		t.Errorf("missing pixi bin in PATH; body:\n%s", body)
	}
}

func TestEnsureAndRemoveManagedBlock(t *testing.T) {
	home := t.TempDir()
	shell := ShellBash

	path, err := EnsureManagedBlock(shell, home)
	if err != nil {
		t.Fatalf("EnsureManagedBlock: %v", err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "# overthink:begin") {
		t.Errorf("managed block not written; got:\n%s", data)
	}

	// Re-running should be idempotent.
	if _, err := EnsureManagedBlock(shell, home); err != nil {
		t.Fatalf("second Ensure: %v", err)
	}
	data, _ = os.ReadFile(path)
	count := strings.Count(string(data), "# overthink:begin")
	if count != 1 {
		t.Errorf("managed block appeared %d times, want 1; got:\n%s", count, data)
	}

	if err := RemoveManagedBlock(shell, home); err != nil {
		t.Fatalf("RemoveManagedBlock: %v", err)
	}
	data, _ = os.ReadFile(path)
	if strings.Contains(string(data), "# overthink:begin") {
		t.Errorf("managed block still present after remove; got:\n%s", data)
	}
}

func TestShellInitFilePath(t *testing.T) {
	home := "/home/atrawog"
	tests := map[ShellKind]string{
		ShellBash: "/home/atrawog/.profile",
		ShellZsh:  "/home/atrawog/.zshenv",
		ShellFish: "/home/atrawog/.config/fish/conf.d/overthink.fish",
	}
	for kind, want := range tests {
		if got := ShellInitFilePath(kind, home); got != want {
			t.Errorf("%v → %q, want %q", kind, got, want)
		}
	}
}

func TestWriteAndRemoveEnvdFile(t *testing.T) {
	home := t.TempDir()
	path, err := WriteEnvdFile(home, "pre-commit", map[string]string{"K": "v"}, []string{"/bin"})
	if err != nil {
		t.Fatalf("WriteEnvdFile: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("file not created: %v", err)
	}
	if err := RemoveEnvdFile(home, "pre-commit"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file still exists after remove: %v", err)
	}
	// Remove again — should not error.
	if err := RemoveEnvdFile(home, "pre-commit"); err != nil {
		t.Errorf("double-remove errored: %v", err)
	}
}

func TestShQuoteEnv(t *testing.T) {
	tests := map[string]string{
		"simple":              "simple",
		"":                    "''",
		"with spaces":         "'with spaces'",
		"has'quote":           `'has'\''quote'`,
		"safe-chars_1.2":      "safe-chars_1.2",
		"$VAR":                `'$VAR'`,
	}
	for in, want := range tests {
		if got := shQuoteEnv(in); got != want {
			t.Errorf("shQuoteEnv(%q) = %q, want %q", in, got, want)
		}
	}
}
