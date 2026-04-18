package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Auto-exported variable names reserved for the generator.
// `vars:` entries may not shadow these. Every task-field `${VAR}` reference
// resolves against (auto-exports ∪ layer.Vars).
var taskAutoExports = map[string]bool{
	"USER":       true,
	"UID":        true,
	"GID":        true,
	"HOME":       true,
	"ARCH":       true, // BuildKit-style: amd64/arm64 — emitted as ENV ARCH=${TARGETARCH}
	"BUILD_ARCH": true, // uname-style: x86_64/aarch64 — only valid inside shell (cmd/download)
}

// taskVarRefPattern matches ${NAME} references in task fields.
var taskVarRefPattern = regexp.MustCompile(`\$\{([A-Z_][A-Z0-9_]*)\}`)

// taskKnownNames returns the set of ${NAME} references that resolve cleanly
// in task fields for this layer: auto-exports ∪ layer.Vars keys.
func taskKnownNames(vars map[string]string) map[string]bool {
	known := make(map[string]bool, len(taskAutoExports)+len(vars))
	for k := range taskAutoExports {
		known[k] = true
	}
	for k := range vars {
		known[k] = true
	}
	return known
}

// taskUnresolvedRefs returns the names of ${VAR} references in s that are
// not in known. Used by the validator to flag typos at config time.
func taskUnresolvedRefs(s string, known map[string]bool) []string {
	matches := taskVarRefPattern.FindAllStringSubmatch(s, -1)
	var out []string
	seen := make(map[string]bool)
	for _, m := range matches {
		name := m[1]
		if !known[name] && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// shellSingleQuote quotes s for safe embedding in bash-c '...' by escaping
// embedded single quotes via the standard '\'' trick.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shellAnsiQuote wraps s in bash ANSI-C quoting ($'...'). Real newlines and
// backslashes are emitted as \n and \\ escapes, keeping the whole quoted
// string on a single physical line. This is required inside Dockerfile
// RUN lines, whose parser terminates the instruction at the first unquoted
// newline even when the argument is inside shell single quotes.
func shellAnsiQuote(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 4)
	b.WriteString("$'")
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '\'':
			b.WriteString(`\'`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteRune('\'')
	return b.String()
}

// resolveUserSpec converts a task's `user:` field to (userDirective, chownPair).
//   - userDirective: the value to emit in `USER <value>` (numeric or name).
//   - chownPair: the numeric "<uid>:<gid>" for COPY --chown (empty if not needed).
//
// For root (empty or "root" or "0"): directive="0", chown="" (root:root is the
// default so COPY --chown= is omitted).
// For ${USER}: directive=<img.User>, chown=<img.UID>:<img.GID>.
// For numeric "uid:gid": directive as-is, chown as-is.
// For literal names: directive=<name>, chown="" (generator doesn't know the
// uid/gid, and COPY --chown supports literal names too so we fall back to
// --chown=<name>:<name> in that case).
func resolveUserSpec(userField string, img *ResolvedImage) (directive, chown string) {
	u := strings.TrimSpace(userField)

	// Special-case ${USER} to emit numeric UID directive (matches the
	// existing USER <UID> form at generate.go:1113 in the legacy path).
	// This also avoids a /etc/passwd dependency at the instant USER is
	// switched — base images create the user, but numeric always works.
	if u == "${USER}" {
		return strconv.Itoa(img.UID), fmt.Sprintf("%d:%d", img.UID, img.GID)
	}

	u = taskSubstAutoExports(u, img) // resolve ${USER}, ${UID}, ${GID}, ${HOME}

	if u == "" || u == "root" || u == "0" {
		return "0", ""
	}
	// Numeric uid:gid
	if strings.Contains(u, ":") {
		return u, u
	}
	// All-numeric uid
	if _, err := strconv.Atoi(u); err == nil {
		return u, u + ":" + u
	}
	// Literal name — emit as-is; COPY --chown supports <name>:<name>
	return u, u + ":" + u
}

// taskSubstAutoExports performs string substitution of the image-level
// auto-exports (USER, UID, GID, HOME) in s. ARCH and BUILD_ARCH are NOT
// substituted here — ARCH is delivered via ENV at layer-section top so
// Docker's substitution handles it; BUILD_ARCH is shell-only (cmd/download).
func taskSubstAutoExports(s string, img *ResolvedImage) string {
	if s == "" || img == nil {
		return s
	}
	repl := map[string]string{
		"USER": img.User,
		"UID":  strconv.Itoa(img.UID),
		"GID":  strconv.Itoa(img.GID),
		"HOME": img.Home,
	}
	return taskVarRefPattern.ReplaceAllStringFunc(s, func(match string) string {
		name := match[2 : len(match)-1]
		if v, ok := repl[name]; ok {
			return v
		}
		return match
	})
}

// taskSubstPath resolves a destination/path field: runs auto-export
// substitution and expands leading ~/ to ${HOME}'s resolved value.
func taskSubstPath(p string, img *ResolvedImage) string {
	if p == "" {
		return p
	}
	if strings.HasPrefix(p, "~/") && img != nil && img.Home != "" {
		p = img.Home + p[1:]
	}
	return taskSubstAutoExports(p, img)
}

// stageInlineContent writes write-task content to
// <buildDir>/_inline/<layer>/<sha256> on disk and returns the
// build-context-relative path suitable for COPY (e.g.
// ".build/<image>/_inline/<layer>/<sha256>"). Writes are idempotent —
// repeated calls with identical content are no-ops via content-addressed
// filenames.
//
// buildDir is the absolute path to .build/<imageName>/.
// contextRelPrefix is the build-context-relative prefix (e.g. ".build/<imageName>").
func stageInlineContent(buildDir, contextRelPrefix, layerName, content string) (string, error) {
	sum := sha256.Sum256([]byte(content))
	hexSum := hex.EncodeToString(sum[:])
	relToBuildDir := filepath.ToSlash(filepath.Join("_inline", layerName, hexSum))
	abs := filepath.Join(buildDir, relToBuildDir)
	contextRel := filepath.ToSlash(filepath.Join(contextRelPrefix, relToBuildDir))

	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", fmt.Errorf("staging inline content dir: %w", err)
	}
	// Idempotent: skip write if file already exists with identical content
	if existing, err := os.ReadFile(abs); err == nil && string(existing) == content {
		return contextRel, nil
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("staging inline content: %w", err)
	}
	return contextRel, nil
}

// emitVarsEnv writes ENV directives for layer.Vars and the build-arg-sourced
// ARCH auto-export. Sorts vars by key for deterministic output.
// Emitted once per layer, before the layer's tasks block.
func emitVarsEnv(b *strings.Builder, vars map[string]string) {
	// ARCH comes from BuildKit's TARGETARCH automatic arg.
	// ARG TARGETARCH makes it visible in this stage; ENV propagates it
	// to subsequent directives and to RUN shells.
	b.WriteString("ARG TARGETARCH\n")
	b.WriteString("ENV ARCH=${TARGETARCH}\n")

	if len(vars) == 0 {
		return
	}
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := vars[k]
		// Quote the value if it contains spaces or shell-sensitive chars.
		// Simple strategy: always double-quote with basic escaping.
		escaped := strings.ReplaceAll(v, `\`, `\\`)
		escaped = strings.ReplaceAll(escaped, `"`, `\"`)
		b.WriteString(fmt.Sprintf("ENV %s=\"%s\"\n", k, escaped))
	}
}

// --- Per-verb emitters ---
//
// Each emitter receives an already-verified task (Kind() has been called
// and returned OK, user has been resolved via resolveUserSpec). Coalescing
// functions receive a slice of same-verb, same-user tasks.

// emitMkdirBatch emits a single RUN mkdir -p for a batch of adjacent
// mkdir tasks that share the same user. When modes differ within the batch,
// splits per-mode chmod at the tail of the single RUN.
func emitMkdirBatch(b *strings.Builder, tasks []Task, img *ResolvedImage) {
	if len(tasks) == 0 {
		return
	}
	// Collect paths grouped by mode (default mode: empty = skip chmod).
	byMode := make(map[string][]string)
	var modeOrder []string
	var allPaths []string
	for _, t := range tasks {
		pth := taskSubstPath(t.Mkdir, img)
		allPaths = append(allPaths, pth)
		mode := t.Mode
		if _, ok := byMode[mode]; !ok {
			modeOrder = append(modeOrder, mode)
		}
		byMode[mode] = append(byMode[mode], pth)
	}
	parts := []string{"mkdir -p " + strings.Join(allPaths, " ")}
	for _, m := range modeOrder {
		if m == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("chmod %s %s", m, strings.Join(byMode[m], " ")))
	}
	b.WriteString("RUN " + strings.Join(parts, " && ") + "\n")
}

// emitCopy emits a COPY --from=<layer-stage> directive for an existing file
// in the layer directory. No RUN required — BuildKit handles the file
// transfer directly from the layer's scratch stage.
func emitCopy(b *strings.Builder, t Task, layerStage string, img *ResolvedImage) {
	src := t.Copy // relative to layer dir; do not substitute (filesystem path at generate time)
	dest := taskSubstPath(t.To, img)
	mode := t.Mode
	if mode == "" {
		mode = "0755"
	}

	_, chown := resolveUserSpec(t.User, img)
	flags := []string{fmt.Sprintf("--from=%s", layerStage), fmt.Sprintf("--chmod=%s", mode)}
	if chown != "" {
		flags = append(flags, fmt.Sprintf("--chown=%s", chown))
	}
	b.WriteString(fmt.Sprintf("COPY %s %s %s\n", strings.Join(flags, " "), src, dest))
}

// emitWrite emits a COPY from the staged inline-content directory to the
// destination. Caller has already called stageInlineContent to produce the
// relative path. layerStage here is the final build-stage name and srcPath
// is the _inline/<layer>/<hash> path inside the build context (NOT from a
// layer stage — inline content lives in the image's .build directory).
func emitWrite(b *strings.Builder, t Task, srcPath string, img *ResolvedImage) {
	dest := taskSubstPath(t.Write, img)
	mode := t.Mode
	if mode == "" {
		mode = "0644"
	}

	_, chown := resolveUserSpec(t.User, img)
	flags := []string{fmt.Sprintf("--chmod=%s", mode)}
	if chown != "" {
		flags = append(flags, fmt.Sprintf("--chown=%s", chown))
	}
	// srcPath is relative to the build context root (the image's .build/<image>/ dir).
	b.WriteString(fmt.Sprintf("COPY %s %s %s\n", strings.Join(flags, " "), srcPath, dest))
}

// emitLinkBatch emits a single RUN with chained ln -sf for a batch of
// adjacent link tasks sharing the same user.
func emitLinkBatch(b *strings.Builder, tasks []Task, img *ResolvedImage) {
	if len(tasks) == 0 {
		return
	}
	parts := make([]string, 0, len(tasks))
	for _, t := range tasks {
		link := taskSubstPath(t.Link, img)
		target := taskSubstPath(t.Target, img)
		parts = append(parts, fmt.Sprintf("ln -sf %s %s", target, link))
	}
	b.WriteString("RUN " + strings.Join(parts, " && ") + "\n")
}

// emitSetcapBatch emits a single RUN setcap … for a batch of adjacent
// setcap tasks. strip (empty caps) and set (non-empty caps) are chained
// via &&.
func emitSetcapBatch(b *strings.Builder, tasks []Task, img *ResolvedImage) {
	if len(tasks) == 0 {
		return
	}
	parts := make([]string, 0, len(tasks))
	for _, t := range tasks {
		pth := taskSubstPath(t.Setcap, img)
		if strings.TrimSpace(t.Caps) == "" {
			parts = append(parts, fmt.Sprintf("setcap -r %s 2>/dev/null || true", pth))
		} else {
			parts = append(parts, fmt.Sprintf("setcap %s %s", t.Caps, pth))
		}
	}
	b.WriteString("RUN " + strings.Join(parts, " && ") + "\n")
}

// emitDownload emits one RUN per download task: curl to the appropriate
// extractor. Uses a shared /tmp/downloads cache mount.
func emitDownload(b *strings.Builder, t Task, img *ResolvedImage) error {
	url := t.Download // no generate-time substitution — left for shell/ENV to handle
	dest := taskSubstPath(t.To, img)
	extract := strings.TrimSpace(t.Extract)

	// Exports are terminated with `;` so subsequent shell-level parameter
	// expansions (e.g. ${BUILD_ARCH} inside the URL) see the variable.
	// The bare `VAR=val cmd` prefix form sets VAR only in cmd's environment
	// *after* bash has already expanded any ${VAR} in cmd's arguments, which
	// leaves the URL with an empty arch string.
	envPrefix := "export BUILD_ARCH=$(uname -m);"
	envForSh := "BUILD_ARCH=$(uname -m)"
	if len(t.Env) > 0 {
		keys := make([]string, 0, len(t.Env))
		for k := range t.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			envPrefix += fmt.Sprintf(" export %s=%s;", k, shellSingleQuote(t.Env[k]))
			envForSh += fmt.Sprintf(" %s=%s", k, shellSingleQuote(t.Env[k]))
		}
	}

	var cmd string
	switch extract {
	case "tar.gz", "":
		// Default to tar.gz if extract unspecified and url ends in .tar.gz/.tgz
		// otherwise fall through to "none" behaviour.
		if extract == "" && !strings.HasSuffix(url, ".tar.gz") && !strings.HasSuffix(url, ".tgz") {
			extract = "none"
		} else {
			extract = "tar.gz"
		}
		if extract == "tar.gz" {
			inc := strings.Join(t.Include, " ")
			cmd = fmt.Sprintf(`%s curl -fsSL %q | tar -xzf - -C %s %s`, envPrefix, url, dest, inc)
		}
	case "tar.xz":
		inc := strings.Join(t.Include, " ")
		cmd = fmt.Sprintf(`%s curl -fsSL %q | tar -xJf - -C %s %s`, envPrefix, url, dest, inc)
	case "tar.zst":
		inc := strings.Join(t.Include, " ")
		cmd = fmt.Sprintf(`%s curl -fsSL %q | tar --zstd -xf - -C %s %s`, envPrefix, url, dest, inc)
	case "zip":
		cmd = fmt.Sprintf(`%s curl -fsSL %q -o /tmp/dl.zip && unzip -o /tmp/dl.zip -d %s && rm /tmp/dl.zip`, envPrefix, url, dest)
	case "sh":
		// For piped install scripts, curl doesn't need the env vars — only
		// the shell running the script does. Put env vars before `sh`.
		cmd = fmt.Sprintf(`curl -fsSL %q | %s sh`, url, envForSh)
	case "none":
		if dest == "" {
			return fmt.Errorf("download %q: extract=none requires `to:` destination", url)
		}
		cmd = fmt.Sprintf(`%s curl -fsSL %q -o %s`, envPrefix, url, dest)
	default:
		return fmt.Errorf("download %q: unknown extract %q", url, extract)
	}

	if extract != "none" && extract != "sh" && t.Mode != "" {
		// Apply mode to extracted files in dest
		cmd += fmt.Sprintf(" && chmod -R %s %s", t.Mode, dest)
	}
	if t.Mode != "" && extract == "none" {
		cmd += fmt.Sprintf(" && chmod %s %s", t.Mode, dest)
	}

	b.WriteString("RUN --mount=type=cache,dst=/tmp/downloads bash -c " + shellSingleQuote(cmd) + "\n")
	return nil
}

// emitCmd emits a single RUN for a cmd task, with the layer-stage /ctx bind
// mount plus cache mounts appropriate to the user (distro format caches for
// root, npm cache for non-root). Shell is bash -c 'set -e; <command>'.
// BUILD_ARCH is injected as a shell var so tasks using ${BUILD_ARCH} work.
func emitCmd(b *strings.Builder, t Task, layerStage string, img *ResolvedImage, userIsRoot bool) {
	body := "BUILD_ARCH=$(uname -m)\nset -e\n" + t.Cmd
	quoted := shellAnsiQuote(body)

	var mounts []string
	mounts = append(mounts, fmt.Sprintf("--mount=type=bind,from=%s,source=/,target=/ctx", layerStage))

	if userIsRoot && img != nil && img.DistroDef != nil {
		if formatDef, ok := img.DistroDef.Formats[img.Pkg]; ok {
			for _, m := range formatDef.CacheMounts {
				sharing := m.Sharing
				if sharing == "" {
					sharing = "locked"
				}
				mounts = append(mounts, fmt.Sprintf("--mount=type=cache,dst=%s,sharing=%s", m.Dst, sharing))
			}
		}
	} else {
		// Non-root: npm cache (matches old writeUserYml)
		mounts = append(mounts, fmt.Sprintf("--mount=type=cache,dst=/tmp/npm-cache,uid=%d,gid=%d", img.UID, img.GID))
	}

	b.WriteString("RUN ")
	for _, m := range mounts {
		b.WriteString(m + " ")
	}
	b.WriteString("bash -c " + quoted + "\n")
}

// --- Orchestrator ---

// parentDirsForDest returns the dirname of dest, or "" if dest has no parent
// worth creating (e.g., destination is at root). Used for auto-mkdir.
func parentDirForDest(dest string) string {
	clean := path.Clean(dest)
	dir := path.Dir(clean)
	if dir == "." || dir == "/" {
		return ""
	}
	return dir
}

// taskCoalescesWith returns true if next can be batched with current under
// the adjacent-coalescing rule: same verb, same user, both verbs support
// batching. mkdir additionally requires same mode to share one chmod.
func taskCoalescesWith(current, next Task, currentVerb string) bool {
	nextVerb, err := next.Kind()
	if err != nil || nextVerb != currentVerb {
		return false
	}
	if current.User != next.User {
		return false
	}
	switch currentVerb {
	case "mkdir", "link", "setcap":
		return true
	}
	return false
}

// emitTasks renders layer.tasks to b in strict YAML order, with
// adjacent-coalescing for mkdir/link/setcap batches, parent-dir
// auto-insertion for copy/write, USER switches on user change, and
// implicit build-task auto-append if the layer has builders and no
// explicit build: in tasks:.
//
// Returns the final USER after processing (so writeLayerSteps knows
// whether to emit USER root for the layer boundary reset).
// Returns an error if emission fails (only for download/write I/O).
func (g *Generator) emitTasks(b *strings.Builder, layer *Layer, img *ResolvedImage, buildDir, contextRelPrefix, initialUser string) (string, error) {
	if len(layer.tasks) == 0 && !g.layerHasImplicitBuild(layer, img) {
		return initialUser, nil
	}

	// Clone tasks and append implicit build if needed.
	tasks := make([]Task, 0, len(layer.tasks)+1)
	tasks = append(tasks, layer.tasks...)
	hasExplicitBuild := false
	for _, t := range layer.tasks {
		if t.Build != "" {
			hasExplicitBuild = true
			break
		}
	}
	if !hasExplicitBuild && g.layerHasImplicitBuild(layer, img) {
		tasks = append(tasks, Task{Build: "all", User: "${USER}"})
	}

	// Track known mkdirs to suppress parent-dir auto-insertion for
	// paths the author already declared. mkdir -p implicitly creates all
	// ancestors, so register those too (declaring /etc/foo/bar makes
	// /etc and /etc/foo both "declared" for auto-insert purposes).
	declaredDirs := make(map[string]bool)
	for _, t := range tasks {
		if t.Mkdir != "" {
			for p := taskSubstPath(t.Mkdir, img); p != "" && p != "/" && p != "."; p = path.Dir(p) {
				declaredDirs[p] = true
			}
		}
	}

	runningUser := initialUser
	i := 0
	for i < len(tasks) {
		t := tasks[i]
		verb, err := t.Kind()
		if err != nil {
			// Validation should have caught this; emit a comment and skip.
			b.WriteString(fmt.Sprintf("# skipping task %d: %v\n", i, err))
			i++
			continue
		}

		// Resolve USER for this task. Build tasks default to ${USER}.
		userField := t.User
		if verb == "build" && userField == "" {
			userField = "${USER}"
		}
		directive, _ := resolveUserSpec(userField, img)
		if directive != runningUser {
			b.WriteString(fmt.Sprintf("USER %s\n", directive))
			runningUser = directive
		}

		// Comment (optional)
		if t.Comment != "" {
			b.WriteString("# " + t.Comment + "\n")
		}

		// Verb dispatch
		switch verb {
		case "mkdir":
			batch := []Task{t}
			for i+1 < len(tasks) && taskCoalescesWith(t, tasks[i+1], verb) {
				batch = append(batch, tasks[i+1])
				i++
			}
			emitMkdirBatch(b, batch, img)

		case "copy":
			// Auto-insert parent mkdir if not declared by the author.
			parent := parentDirForDest(taskSubstPath(t.To, img))
			if parent != "" && !declaredDirs[parent] && parent != img.Home && parent != "/" {
				emitMkdirBatch(b, []Task{{Mkdir: parent, User: t.User}}, img)
				declaredDirs[parent] = true
			}
			emitCopy(b, t, layer.Name, img)

		case "write":
			parent := parentDirForDest(taskSubstPath(t.Write, img))
			if parent != "" && !declaredDirs[parent] && parent != img.Home && parent != "/" {
				emitMkdirBatch(b, []Task{{Mkdir: parent, User: t.User}}, img)
				declaredDirs[parent] = true
			}
			srcPath, err := stageInlineContent(buildDir, contextRelPrefix, layer.Name, t.Content)
			if err != nil {
				return runningUser, err
			}
			emitWrite(b, t, srcPath, img)

		case "link":
			batch := []Task{t}
			for i+1 < len(tasks) && taskCoalescesWith(t, tasks[i+1], verb) {
				batch = append(batch, tasks[i+1])
				i++
			}
			emitLinkBatch(b, batch, img)

		case "setcap":
			batch := []Task{t}
			for i+1 < len(tasks) && taskCoalescesWith(t, tasks[i+1], verb) {
				batch = append(batch, tasks[i+1])
				i++
			}
			emitSetcapBatch(b, batch, img)

		case "download":
			if err := emitDownload(b, t, img); err != nil {
				return runningUser, err
			}

		case "cmd":
			emitCmd(b, t, layer.Name, img, runningUser == "0" || runningUser == "root")

		case "build":
			// Builders are emitted by the existing builder block in
			// writeLayerSteps; this is a marker the orchestrator honours by
			// yielding back to the caller's builder logic. Because our
			// emitTasks returns after the whole loop, we don't physically
			// emit anything here — writeLayerSteps handles build placement
			// by calling emitBuild at the position of the explicit task (or
			// at end for implicit). Phase 0 keeps existing builder behaviour
			// unchanged, so build tasks are a no-op in this emitter.
			b.WriteString("# build: " + t.Build + " (handled by builder stage)\n")

		default:
			b.WriteString(fmt.Sprintf("# unknown verb %q — skipping\n", verb))
		}
		i++
	}
	return runningUser, nil
}

// layerHasImplicitBuild returns true if the layer has a detection file
// (pixi.toml, package.json, Cargo.toml, aur: config) that would trigger a
// builder auto-append. Phase 0: placeholder that returns false — builders
// continue to run via the existing writeLayerSteps builder block. Phase 2
// migrations will switch on this once explicit build: tasks appear.
func (g *Generator) layerHasImplicitBuild(layer *Layer, img *ResolvedImage) bool {
	return false
}
