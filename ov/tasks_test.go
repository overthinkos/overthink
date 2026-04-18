package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testResolvedImage returns a ResolvedImage suitable for feeding the
// task emitters. Uses fedora (rpm) by default with UID/GID 1000.
func testResolvedImage() *ResolvedImage {
	return &ResolvedImage{
		Name:         "test-img",
		User:         "user",
		UID:          1000,
		GID:          1000,
		Home:         "/home/user",
		Pkg:          "rpm",
		BuildFormats: []string{"rpm"},
		Tags:         []string{"all", "rpm"},
		DistroDef:    testDistroDef("fedora"),
	}
}

// --- Task.Kind() — exactly-one-verb enforcement ---

func TestTaskKind_Valid(t *testing.T) {
	cases := []struct {
		task Task
		want string
	}{
		{Task{Cmd: "echo hi"}, "cmd"},
		{Task{Mkdir: "/etc/foo"}, "mkdir"},
		{Task{Copy: "foo", To: "/bar"}, "copy"},
		{Task{Write: "/x", Content: "body"}, "write"},
		{Task{Link: "/a", Target: "/b"}, "link"},
		{Task{Download: "http://x"}, "download"},
		{Task{Setcap: "/bin/x"}, "setcap"},
		{Task{Build: "all"}, "build"},
	}
	for _, c := range cases {
		got, err := c.task.Kind()
		if err != nil {
			t.Errorf("Kind(%+v) error: %v", c.task, err)
		}
		if got != c.want {
			t.Errorf("Kind(%+v) = %q, want %q", c.task, got, c.want)
		}
	}
}

func TestTaskKind_ZeroVerbs(t *testing.T) {
	_, err := (&Task{User: "root"}).Kind()
	if err == nil {
		t.Fatal("expected error for zero-verb task")
	}
	if !strings.Contains(err.Error(), "no action") {
		t.Errorf("error should mention missing action, got: %v", err)
	}
}

func TestTaskKind_MultipleVerbs(t *testing.T) {
	_, err := (&Task{Cmd: "x", Mkdir: "/y"}).Kind()
	if err == nil {
		t.Fatal("expected error for multi-verb task")
	}
	if !strings.Contains(err.Error(), "conflicting") {
		t.Errorf("error should mention conflict, got: %v", err)
	}
}

// --- Variable substitution ---

func TestTaskSubstAutoExports(t *testing.T) {
	img := testResolvedImage()
	cases := []struct {
		in, want string
	}{
		{"${USER}", "user"},
		{"${UID}", "1000"},
		{"${GID}", "1000"},
		{"${HOME}", "/home/user"},
		{"hello ${USER}!", "hello user!"},
		{"${UNKNOWN}", "${UNKNOWN}"}, // left alone
		{"${USER}/${HOME}", "user//home/user"},
	}
	for _, c := range cases {
		got := taskSubstAutoExports(c.in, img)
		if got != c.want {
			t.Errorf("taskSubstAutoExports(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTaskSubstPath_TildeExpansion(t *testing.T) {
	img := testResolvedImage()
	got := taskSubstPath("~/.local/bin", img)
	if got != "/home/user/.local/bin" {
		t.Errorf("tilde expansion: got %q", got)
	}
}

func TestTaskUnresolvedRefs(t *testing.T) {
	known := taskKnownNames(map[string]string{"MY_VAR": "x"})
	refs := taskUnresolvedRefs("${MY_VAR}/${USER}/${MISSING}/${NOPE}", known)
	if len(refs) != 2 {
		t.Fatalf("expected 2 unresolved, got %d: %v", len(refs), refs)
	}
	// Order preserved, duplicates deduped
	if refs[0] != "MISSING" || refs[1] != "NOPE" {
		t.Errorf("unresolved = %v, want [MISSING NOPE]", refs)
	}
}

// --- User resolution ---

func TestResolveUserSpec(t *testing.T) {
	img := testResolvedImage()
	cases := []struct {
		in, wantDirective, wantChown string
	}{
		{"", "0", ""},
		{"root", "0", ""},
		{"0", "0", ""},
		{"${USER}", "1000", "1000:1000"},
		{"1000:1000", "1000:1000", "1000:1000"},
		{"500", "500", "500:500"},
		{"postgres", "postgres", "postgres:postgres"},
	}
	for _, c := range cases {
		gotDir, gotCh := resolveUserSpec(c.in, img)
		if gotDir != c.wantDirective || gotCh != c.wantChown {
			t.Errorf("resolveUserSpec(%q) = (%q, %q), want (%q, %q)",
				c.in, gotDir, gotCh, c.wantDirective, c.wantChown)
		}
	}
}

// --- Inline content staging ---

func TestStageInlineContent_Idempotent(t *testing.T) {
	dir := t.TempDir()
	buildDir := filepath.Join(dir, ".build", "img")
	ctx := ".build/img"

	rel1, err := stageInlineContent(buildDir, ctx, "lyr", "hello\n")
	if err != nil {
		t.Fatalf("stage 1: %v", err)
	}
	rel2, err := stageInlineContent(buildDir, ctx, "lyr", "hello\n")
	if err != nil {
		t.Fatalf("stage 2: %v", err)
	}
	if rel1 != rel2 {
		t.Errorf("non-idempotent path: %q vs %q", rel1, rel2)
	}
	if !strings.HasPrefix(rel1, ".build/img/_inline/lyr/") {
		t.Errorf("bad rel path: %q", rel1)
	}
	// Different content → different hash
	rel3, err := stageInlineContent(buildDir, ctx, "lyr", "different\n")
	if err != nil {
		t.Fatalf("stage 3: %v", err)
	}
	if rel3 == rel1 {
		t.Error("different content should produce different hash path")
	}
	// File actually exists
	abs := filepath.Join(dir, rel1)
	data, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("reading staged file: %v", err)
	}
	if string(data) != "hello\n" {
		t.Errorf("staged content mismatch: %q", data)
	}
}

// --- Emitters ---

func TestEmitMkdirBatch_Coalesces(t *testing.T) {
	var b strings.Builder
	tasks := []Task{
		{Mkdir: "/a", User: "root"},
		{Mkdir: "/b", User: "root"},
		{Mkdir: "/c", User: "root"},
	}
	emitMkdirBatch(&b, tasks, testResolvedImage())
	out := b.String()
	if !strings.Contains(out, "RUN mkdir -p /a /b /c") {
		t.Errorf("expected coalesced mkdir, got:\n%s", out)
	}
	// Only one RUN line
	if strings.Count(out, "RUN") != 1 {
		t.Errorf("expected 1 RUN, got %d\n%s", strings.Count(out, "RUN"), out)
	}
}

func TestEmitMkdirBatch_PerModeChmod(t *testing.T) {
	var b strings.Builder
	tasks := []Task{
		{Mkdir: "/a", Mode: "0700"},
		{Mkdir: "/b"}, // default — no chmod
		{Mkdir: "/c", Mode: "0700"},
	}
	emitMkdirBatch(&b, tasks, testResolvedImage())
	out := b.String()
	if !strings.Contains(out, "mkdir -p /a /b /c") {
		t.Errorf("mkdir missing paths:\n%s", out)
	}
	if !strings.Contains(out, "chmod 0700 /a /c") {
		t.Errorf("chmod should group by mode:\n%s", out)
	}
}

func TestEmitCopy_WithChown(t *testing.T) {
	var b strings.Builder
	emitCopy(&b,
		Task{Copy: "wrapper", To: "/home/user/.local/bin/wrapper", Mode: "0755", User: "${USER}"},
		"my-layer", testResolvedImage(),
	)
	out := b.String()
	if !strings.Contains(out, "--from=my-layer") {
		t.Errorf("missing layer stage reference:\n%s", out)
	}
	if !strings.Contains(out, "--chmod=0755") {
		t.Errorf("missing chmod:\n%s", out)
	}
	if !strings.Contains(out, "--chown=1000:1000") {
		t.Errorf("missing chown for ${USER} (should resolve to numeric UID:GID):\n%s", out)
	}
	if !strings.Contains(out, "wrapper /home/user/.local/bin/wrapper") {
		t.Errorf("missing src/dest:\n%s", out)
	}
}

func TestEmitCopy_RootNoChown(t *testing.T) {
	var b strings.Builder
	emitCopy(&b,
		Task{Copy: "traefik.yml", To: "/etc/traefik/traefik.yml", Mode: "0644", User: "root"},
		"traefik", testResolvedImage(),
	)
	out := b.String()
	if strings.Contains(out, "--chown") {
		t.Errorf("root should not emit --chown:\n%s", out)
	}
}

func TestEmitWrite_UsesStagedPath(t *testing.T) {
	var b strings.Builder
	emitWrite(&b,
		Task{Write: "/etc/foo.conf", Content: "body", Mode: "0644", User: "root"},
		".build/img/_inline/lyr/abc123",
		testResolvedImage(),
	)
	out := b.String()
	if !strings.Contains(out, "COPY --chmod=0644 .build/img/_inline/lyr/abc123 /etc/foo.conf") {
		t.Errorf("write should COPY from staged path:\n%s", out)
	}
	// root: no chown
	if strings.Contains(out, "--chown") {
		t.Errorf("root write should not emit --chown:\n%s", out)
	}
}

func TestEmitLinkBatch(t *testing.T) {
	var b strings.Builder
	tasks := []Task{
		{Link: "/usr/local/bin/node", Target: "/usr/bin/node-24"},
		{Link: "/usr/local/bin/npm", Target: "/usr/bin/npm-24"},
	}
	emitLinkBatch(&b, tasks, testResolvedImage())
	out := b.String()
	if !strings.Contains(out, "ln -sf /usr/bin/node-24 /usr/local/bin/node") {
		t.Errorf("missing first link:\n%s", out)
	}
	if !strings.Contains(out, "ln -sf /usr/bin/npm-24 /usr/local/bin/npm") {
		t.Errorf("missing second link:\n%s", out)
	}
	if strings.Count(out, "RUN") != 1 {
		t.Errorf("links should coalesce to one RUN:\n%s", out)
	}
}

func TestEmitSetcapBatch_StripAndSet(t *testing.T) {
	var b strings.Builder
	tasks := []Task{
		{Setcap: "/usr/bin/sway"}, // strip
		{Setcap: "/usr/bin/newuidmap", Caps: "cap_setuid=ep"},
	}
	emitSetcapBatch(&b, tasks, testResolvedImage())
	out := b.String()
	if !strings.Contains(out, "setcap -r /usr/bin/sway") {
		t.Errorf("strip should use -r:\n%s", out)
	}
	if !strings.Contains(out, "setcap cap_setuid=ep /usr/bin/newuidmap") {
		t.Errorf("set should include caps:\n%s", out)
	}
	if strings.Count(out, "RUN") != 1 {
		t.Errorf("setcap should coalesce to one RUN:\n%s", out)
	}
}

func TestEmitDownload_TarGz(t *testing.T) {
	var b strings.Builder
	err := emitDownload(&b,
		Task{
			Download: "https://example.com/app.tar.gz",
			Extract:  "tar.gz",
			To:       "/usr/local/bin",
			Include:  []string{"app"},
		},
		testResolvedImage(),
	)
	if err != nil {
		t.Fatalf("emitDownload: %v", err)
	}
	out := b.String()
	if !strings.Contains(out, "tar -xzf - -C /usr/local/bin app") {
		t.Errorf("missing tar -xzf with include filter:\n%s", out)
	}
	if !strings.Contains(out, "BUILD_ARCH=$(uname -m)") {
		t.Errorf("should set BUILD_ARCH from uname:\n%s", out)
	}
	if !strings.Contains(out, "/tmp/downloads") {
		t.Errorf("should use downloads cache mount:\n%s", out)
	}
}

func TestEmitDownload_Sh(t *testing.T) {
	var b strings.Builder
	err := emitDownload(&b,
		Task{Download: "https://sh.install", Extract: "sh", Env: map[string]string{"UV_INSTALL_DIR": "/usr/local/bin"}},
		testResolvedImage(),
	)
	if err != nil {
		t.Fatalf("emitDownload: %v", err)
	}
	out := b.String()
	// Env vars are shell-quoted inside a shell-quoted outer — double-escape.
	// Confirm the env var name appears before "sh" and the value is present.
	if !strings.Contains(out, "UV_INSTALL_DIR=") {
		t.Errorf("should include env var assignment:\n%s", out)
	}
	if !strings.Contains(out, "/usr/local/bin") {
		t.Errorf("should include env value:\n%s", out)
	}
	if !strings.Contains(out, "| ") || !strings.Contains(out, " sh") {
		t.Errorf("should pipe into shell:\n%s", out)
	}
	// For install scripts, env vars belong before `sh` (so the script sees them),
	// not before `curl` (which doesn't need them).
	idxPipe := strings.Index(out, "| ")
	idxEnv := strings.LastIndex(out, "UV_INSTALL_DIR=")
	if idxEnv < idxPipe {
		t.Errorf("env vars should appear AFTER pipe (before sh):\n%s", out)
	}
}

func TestEmitDownload_UnknownExtract(t *testing.T) {
	var b strings.Builder
	err := emitDownload(&b, Task{Download: "http://x", Extract: "rar"}, testResolvedImage())
	if err == nil {
		t.Fatal("expected error for unknown extract")
	}
}

func TestEmitCmd_RootCacheMounts(t *testing.T) {
	var b strings.Builder
	emitCmd(&b,
		Task{Cmd: "echo hello", User: "root"},
		"my-layer", testResolvedImage(), true,
	)
	out := b.String()
	if !strings.Contains(out, "--mount=type=bind,from=my-layer") {
		t.Errorf("should bind-mount layer stage at /ctx:\n%s", out)
	}
	if !strings.Contains(out, "libdnf5") {
		t.Errorf("root cmd should include distro format cache:\n%s", out)
	}
	if !strings.Contains(out, "set -e") {
		t.Errorf("should include set -e:\n%s", out)
	}
	if !strings.Contains(out, "BUILD_ARCH=$(uname -m)") {
		t.Errorf("should set BUILD_ARCH inside shell:\n%s", out)
	}
}

func TestEmitCmd_UserNpmCache(t *testing.T) {
	var b strings.Builder
	emitCmd(&b,
		Task{Cmd: "xdg-settings default-browser foo", User: "${USER}"},
		"my-layer", testResolvedImage(), false,
	)
	out := b.String()
	if strings.Contains(out, "libdnf5") {
		t.Errorf("non-root cmd should NOT include distro cache:\n%s", out)
	}
	if !strings.Contains(out, "/tmp/npm-cache") {
		t.Errorf("non-root cmd should include npm cache:\n%s", out)
	}
	if !strings.Contains(out, "uid=1000,gid=1000") {
		t.Errorf("npm cache should be UID/GID owned:\n%s", out)
	}
}

// --- emitTasks orchestrator ---

func TestEmitTasks_UserCoalescing(t *testing.T) {
	dir := t.TempDir()
	g := &Generator{BuildDir: dir}
	layer := &Layer{Name: "lyr", tasks: []Task{
		{Mkdir: "/a", User: "root"},
		{Mkdir: "/b", User: "root"},
		{Mkdir: "/c", User: "root"}, // all root → single USER 0 header, one RUN
	}}
	var b strings.Builder
	_, err := g.emitTasks(&b, layer, testResolvedImage(), dir, ".build/test-img", "0")
	if err != nil {
		t.Fatalf("emitTasks: %v", err)
	}
	out := b.String()
	// No USER directive should be emitted (running user was "0" already)
	if strings.Contains(out, "USER") {
		t.Errorf("no USER directive expected when starting user matches task user:\n%s", out)
	}
	if strings.Count(out, "RUN") != 1 {
		t.Errorf("three mkdirs should coalesce to one RUN:\n%s", out)
	}
}

func TestEmitTasks_UserSwitches(t *testing.T) {
	dir := t.TempDir()
	g := &Generator{BuildDir: dir}
	layer := &Layer{Name: "lyr", tasks: []Task{
		{Mkdir: "/a", User: "root"},
		{Mkdir: "/b", User: "${USER}"},
		{Mkdir: "/c", User: "${USER}"}, // coalesces with previous
	}}
	var b strings.Builder
	_, err := g.emitTasks(&b, layer, testResolvedImage(), dir, ".build/test-img", "0")
	if err != nil {
		t.Fatalf("emitTasks: %v", err)
	}
	out := b.String()
	// One USER switch (root → user); no second switch within the user group
	if strings.Count(out, "USER ") != 1 {
		t.Errorf("expected 1 USER switch, got %d:\n%s", strings.Count(out, "USER "), out)
	}
	if !strings.Contains(out, "USER 1000") {
		t.Errorf("should switch to USER 1000 (numeric form from ${USER}):\n%s", out)
	}
	// Two RUN mkdir (one for root, one for user — NOT coalesced across users)
	if strings.Count(out, "mkdir") != 2 {
		t.Errorf("expected 2 mkdir (across users):\n%s", out)
	}
}

func TestEmitTasks_OrderPreserved(t *testing.T) {
	dir := t.TempDir()
	g := &Generator{BuildDir: dir}
	// mkdir → copy → mkdir sequence: the second mkdir must NOT merge with the first
	layer := &Layer{Name: "lyr", tasks: []Task{
		{Mkdir: "/a", User: "root"},
		{Copy: "f", To: "/a/f", User: "root"},
		{Mkdir: "/b", User: "root"},
	}}
	var b strings.Builder
	_, err := g.emitTasks(&b, layer, testResolvedImage(), dir, ".build/test-img", "0")
	if err != nil {
		t.Fatalf("emitTasks: %v", err)
	}
	out := b.String()
	// Check ordering: mkdir /a must come before COPY, COPY before mkdir /b
	idx1 := strings.Index(out, "mkdir -p /a")
	idxCopy := strings.Index(out, "COPY")
	idx2 := strings.Index(out, "mkdir -p /b")
	if idx1 < 0 || idxCopy < 0 || idx2 < 0 {
		t.Fatalf("missing directive: mkdir1=%d copy=%d mkdir2=%d\n%s", idx1, idxCopy, idx2, out)
	}
	if !(idx1 < idxCopy && idxCopy < idx2) {
		t.Errorf("order violated: mkdir1=%d copy=%d mkdir2=%d\n%s", idx1, idxCopy, idx2, out)
	}
}

func TestEmitTasks_ParentDirAutoInsert(t *testing.T) {
	dir := t.TempDir()
	g := &Generator{BuildDir: dir}
	layer := &Layer{Name: "lyr", tasks: []Task{
		// Copy to /etc/traefik/traefik.yml without declaring /etc/traefik first
		{Copy: "traefik.yml", To: "/etc/traefik/traefik.yml", User: "root"},
	}}
	var b strings.Builder
	_, err := g.emitTasks(&b, layer, testResolvedImage(), dir, ".build/test-img", "0")
	if err != nil {
		t.Fatalf("emitTasks: %v", err)
	}
	out := b.String()
	// auto-inserted mkdir -p /etc/traefik before COPY
	idxMkdir := strings.Index(out, "mkdir -p /etc/traefik")
	idxCopy := strings.Index(out, "COPY")
	if idxMkdir < 0 {
		t.Errorf("expected auto-inserted parent mkdir:\n%s", out)
	}
	if idxCopy < idxMkdir {
		t.Errorf("parent mkdir must precede COPY:\n%s", out)
	}
}

func TestEmitTasks_ParentDirSuppressedWhenDeclared(t *testing.T) {
	dir := t.TempDir()
	g := &Generator{BuildDir: dir}
	// Author explicitly declared /etc/foo via mkdir — no auto-insert
	layer := &Layer{Name: "lyr", tasks: []Task{
		{Mkdir: "/etc/foo", User: "root"},
		{Copy: "bar", To: "/etc/foo/bar", User: "root"},
	}}
	var b strings.Builder
	_, err := g.emitTasks(&b, layer, testResolvedImage(), dir, ".build/test-img", "0")
	if err != nil {
		t.Fatalf("emitTasks: %v", err)
	}
	out := b.String()
	// Only ONE mkdir RUN (the author's) — no auto-insert duplicate
	if strings.Count(out, "mkdir -p /etc/foo") != 1 {
		t.Errorf("should not auto-insert parent dir already declared by author:\n%s", out)
	}
}

func TestEmitTasks_WriteStagesContent(t *testing.T) {
	dir := t.TempDir()
	g := &Generator{BuildDir: dir}
	layer := &Layer{Name: "lyr", tasks: []Task{
		{Write: "/etc/foo.conf", Content: "hello world\n", User: "root"},
	}}
	var b strings.Builder
	buildDir := filepath.Join(dir, "test-img")
	_, err := g.emitTasks(&b, layer, testResolvedImage(), buildDir, ".build/test-img", "0")
	if err != nil {
		t.Fatalf("emitTasks: %v", err)
	}
	out := b.String()
	if !strings.Contains(out, "COPY --chmod=0644 .build/test-img/_inline/lyr/") {
		t.Errorf("expected COPY from staged inline path:\n%s", out)
	}
	// Content file exists on disk
	entries, _ := os.ReadDir(filepath.Join(buildDir, "_inline", "lyr"))
	if len(entries) != 1 {
		t.Errorf("expected one staged file, got %d", len(entries))
	}
}

// --- emitVarsEnv ---

func TestEmitVarsEnv_AlwaysEmitsArch(t *testing.T) {
	var b strings.Builder
	emitVarsEnv(&b, nil)
	out := b.String()
	if !strings.Contains(out, "ARG TARGETARCH") {
		t.Errorf("expected ARG TARGETARCH:\n%s", out)
	}
	if !strings.Contains(out, "ENV ARCH=${TARGETARCH}") {
		t.Errorf("expected ENV ARCH=${TARGETARCH}:\n%s", out)
	}
}

func TestEmitVarsEnv_SortedKeys(t *testing.T) {
	var b strings.Builder
	emitVarsEnv(&b, map[string]string{"ZETA": "z", "ALPHA": "a", "MIDDLE": "m"})
	out := b.String()
	idxA := strings.Index(out, "ENV ALPHA")
	idxM := strings.Index(out, "ENV MIDDLE")
	idxZ := strings.Index(out, "ENV ZETA")
	if !(idxA < idxM && idxM < idxZ) {
		t.Errorf("vars should be emitted in sorted order:\n%s", out)
	}
}

// --- Validator ---

func TestValidateLayerTasks_CopyRequiresTo(t *testing.T) {
	layers := map[string]*Layer{
		"mylyr": {
			Name:     "mylyr",
			HasTasks: true,
			tasks:    []Task{{Copy: "foo" /* no To */}},
		},
	}
	errs := &ValidationError{}
	validateLayerTasks(layers, errs)
	if !errs.HasErrors() {
		t.Fatal("expected missing-to error")
	}
	if !strings.Contains(errs.Error(), "requires to:") {
		t.Errorf("error should mention missing to: %v", errs.Error())
	}
}

func TestValidateLayerTasks_UnresolvedVar(t *testing.T) {
	layers := map[string]*Layer{
		"mylyr": {
			Name:     "mylyr",
			HasTasks: true,
			tasks:    []Task{{Mkdir: "${UNDEFINED}/foo"}},
		},
	}
	errs := &ValidationError{}
	validateLayerTasks(layers, errs)
	if !errs.HasErrors() {
		t.Fatal("expected unresolved var error")
	}
	if !strings.Contains(errs.Error(), "UNDEFINED") {
		t.Errorf("error should name the unresolved var: %v", errs.Error())
	}
}

func TestValidateLayerTasks_ReservedVarKey(t *testing.T) {
	layers := map[string]*Layer{
		"mylyr": {
			Name:     "mylyr",
			HasTasks: true,
			vars:     map[string]string{"USER": "ignored"}, // collides with auto-export
		},
	}
	errs := &ValidationError{}
	validateLayerTasks(layers, errs)
	if !errs.HasErrors() {
		t.Fatal("expected reserved-key error")
	}
	if !strings.Contains(errs.Error(), "reserved auto-export") {
		t.Errorf("error should mention reserved auto-export: %v", errs.Error())
	}
}

func TestValidateLayerTasks_BadMode(t *testing.T) {
	layers := map[string]*Layer{
		"mylyr": {
			Name:     "mylyr",
			HasTasks: true,
			tasks:    []Task{{Mkdir: "/a", Mode: "9999"}},
		},
	}
	errs := &ValidationError{}
	validateLayerTasks(layers, errs)
	if !errs.HasErrors() {
		t.Fatal("expected bad-mode error")
	}
}

func TestValidateLayerTasks_BuildOnlyAll(t *testing.T) {
	layers := map[string]*Layer{
		"mylyr": {
			Name:     "mylyr",
			HasTasks: true,
			tasks:    []Task{{Build: "pixi"}}, // reserved for future
		},
	}
	errs := &ValidationError{}
	validateLayerTasks(layers, errs)
	if !errs.HasErrors() {
		t.Fatal("expected build-only-all error")
	}
}

func TestValidateLayerTasks_HappyPath(t *testing.T) {
	layers := map[string]*Layer{
		"mylyr": {
			Name:     "mylyr",
			HasTasks: true,
			vars:     map[string]string{"VERSION": "1.0"},
			tasks: []Task{
				{Mkdir: "/etc/foo", User: "root"},
				{Copy: "bar", To: "/etc/foo/bar", Mode: "0644", User: "root"},
				{Write: "/etc/baz.conf", Content: "hello", User: "root"},
				{Download: "https://x.com/v${VERSION}/app.tar.gz", Extract: "tar.gz", To: "/usr/local/bin", User: "root"},
				{Link: "/usr/local/bin/app-current", Target: "/usr/local/bin/app", User: "root"},
				{Setcap: "/usr/bin/foo", Caps: "cap_setuid=ep"},
				{Cmd: "echo hello ${VERSION}", User: "${USER}"},
			},
		},
	}
	errs := &ValidationError{}
	validateLayerTasks(layers, errs)
	if errs.HasErrors() {
		t.Fatalf("expected no errors on happy path, got:\n%s", errs.Error())
	}
}

// --- Parity: ensure HasInstallFiles picks up HasTasks ---

func TestLayer_HasInstallFiles_IncludesTasks(t *testing.T) {
	l := &Layer{HasTasks: true}
	if !l.HasInstallFiles() {
		t.Error("HasInstallFiles() should be true when HasTasks is true")
	}
}
