package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Retention fallbacks — used ONLY when defaults.keep_images / keep_eval_runs are
// absent from config. Zero means "disabled" so third-party configs that never
// declare the keys get NO surprise pruning. The repo's overthink.yml opts in
// (keep_images: 5, keep_eval_runs: 10). See /ov-core:clean.
const (
	keepImagesFallback   = 0
	keepEvalRunsFallback = 0
)

// listContainerImageRefs returns the set of image IDs and image refs currently
// referenced by ANY container (running or stopped, incl. quadlet-managed
// deploys). Package-level var for testability (same pattern as ListLocalImages).
var listContainerImageRefs = defaultContainerImageRefs

func defaultContainerImageRefs(engine string) (ids map[string]bool, refs map[string]bool, err error) {
	ids = map[string]bool{}
	refs = map[string]bool{}
	// Parse JSON, not a Go-template `--format`: podman's `{{.ImageID}}` template
	// panics (slice bounds [:12] length 0) when any container has an empty image
	// ID. The raw JSON field handles that gracefully.
	out, e := exec.Command(EngineBinary(engine), "ps", "-a", "--format", "json").Output()
	if e != nil {
		return ids, refs, fmt.Errorf("listing containers via %s: %w", EngineBinary(engine), e)
	}
	var rows []map[string]any
	if e := json.Unmarshal(out, &rows); e != nil {
		return ids, refs, fmt.Errorf("parsing %s ps output: %w", EngineBinary(engine), e)
	}
	for _, r := range rows {
		if v, ok := r["ImageID"].(string); ok {
			if id := normImageID(v); id != "" {
				ids[id] = true
			}
		}
		if v, ok := r["Image"].(string); ok && v != "" {
			refs[v] = true
		}
	}
	return ids, refs, nil
}

// normImageID strips the "sha256:" prefix so short (12-char) and full (64-char)
// IDs compare by prefix.
func normImageID(s string) string { return strings.TrimPrefix(strings.TrimSpace(s), "sha256:") }

// imageInUse reports whether the candidate image is referenced by any container,
// by ID (prefix-tolerant: 12-char vs 64-char) or by any of its tags.
func imageInUse(im LocalImageInfo, ids, refs map[string]bool) bool {
	cid := normImageID(im.ID)
	for id := range ids {
		if cid != "" && id != "" && (strings.HasPrefix(cid, id) || strings.HasPrefix(id, cid)) {
			return true
		}
	}
	for _, n := range im.Names {
		if refs[n] {
			return true
		}
	}
	return false
}

// imageLabelCalVer parses the image's org.overthinkos.version label (the
// content-derived EffectiveVersion) — the PRIMARY retention ordering key.
func imageLabelCalVer(im LocalImageInfo) (CalVer, bool) {
	return ParseCalVer(im.Labels[LabelVersion])
}

// imageTagCalVer returns the newest CalVer among an image's `:YYYY.DDD.HHMM`
// tags (the per-build timestamp) — the TIEBREAKER. Because the version label is
// content-stable, many builds of an unchanged image share one label-CalVer, so
// the tag is what distinguishes (and retains) the newest BUILD.
func imageTagCalVer(im LocalImageInfo) (CalVer, bool) {
	var best CalVer
	found := false
	for _, n := range im.Names {
		if cv, ok := ParseCalVer(extractCalVerTag(n)); ok {
			if !found || best.Less(cv) {
				best, found = cv, true
			}
		}
	}
	return best, found
}

// imageDatable reports whether an image carries EITHER a parseable label-CalVer
// OR a parseable tag-CalVer — i.e. retention can order it. An undatable image
// is never removed.
func imageDatable(im LocalImageInfo) bool {
	_, okL := imageLabelCalVer(im)
	_, okT := imageTagCalVer(im)
	return okL || okT
}

// pruneImagesByRetention removes all but the newest keepN builds per
// `org.overthinkos.image` group, ordered by the `org.overthinkos.version`
// CalVer label. Images in use by a container are skipped, and `rmi` is invoked
// WITHOUT `-f` so the engine refuses any still-referenced image as a backstop.
// keepN <= 0 disables (no-op). Returns the refs removed (or that would be, when
// dryRun).
func pruneImagesByRetention(engine string, keepN int, dryRun bool) ([]string, error) {
	if keepN <= 0 {
		return nil, nil
	}
	imgs, err := ListLocalImages(engine)
	if err != nil {
		return nil, err
	}
	inUseIDs, inUseRefs, err := listContainerImageRefs(engine)
	if err != nil {
		return nil, err
	}

	// Group ov-labelled images by short name; ignore images without the label
	// (never touch non-ov images).
	groups := map[string][]LocalImageInfo{}
	for _, im := range imgs {
		short := im.Labels["org.overthinkos.image"]
		if short == "" {
			continue
		}
		groups[short] = append(groups[short], im)
	}

	var removed []string
	for _, group := range groups {
		// Newest first by the org.overthinkos.version LABEL (the content-derived
		// EffectiveVersion) as the PRIMARY key, then the `:YYYY.DDD.HHMM` build
		// TAG as the TIEBREAKER. The label is content-stable, so unchanged-image
		// builds share one label-CalVer and the tag is what keeps the newest
		// BUILDS. Images datable by neither sort last and are skipped below.
		sort.SliceStable(group, func(i, j int) bool {
			li, okLi := imageLabelCalVer(group[i])
			lj, okLj := imageLabelCalVer(group[j])
			if okLi && okLj && li != lj {
				return lj.Less(li) // newer label first
			}
			if okLi != okLj {
				return okLi // labelled sorts before unlabelled
			}
			// Equal (or both-absent) label-CalVer → tiebreak on the build tag.
			ti, okTi := imageTagCalVer(group[i])
			tj, okTj := imageTagCalVer(group[j])
			if okTi && okTj {
				return tj.Less(ti) // newer build first
			}
			return okTi && !okTj // dateable sorts before undateable
		})
		for idx, im := range group {
			if idx < keepN {
				continue // keep the newest keepN
			}
			if !imageDatable(im) {
				continue // never remove an image we can't date (no label/tag CalVer)
			}
			if imageInUse(im, inUseIDs, inUseRefs) {
				continue // referenced by a container/deploy
			}
			for _, ref := range im.Names {
				if dryRun {
					removed = append(removed, ref)
					continue
				}
				// rmi without -f refuses images still in use by a container —
				// including "external"/build containers that `podman ps -a`
				// doesn't list, which our imageInUse pre-check can't see. That
				// refusal is the safety backstop: such images are correctly
				// retained. Silent skip — in-use retention is expected, not an
				// error worth per-image noise on every build.
				if err := exec.Command(EngineBinary(engine), "rmi", ref).Run(); err != nil {
					continue
				}
				removed = append(removed, ref)
			}
		}
	}
	return removed, nil
}

// pruneEvalRuns trims each bed/score subdir of evalDir to the newest keepN run
// artifacts: CalVer-named run dirs (bed runs), `runs/<id>` dirs (score
// iterations), and `result-<calver>.yml` files. NOTES.md and any other file are
// always preserved. keepN <= 0 disables. Returns the paths removed.
func pruneEvalRuns(evalDir string, keepN int, dryRun bool) ([]string, error) {
	if keepN <= 0 {
		return nil, nil
	}
	entries, err := os.ReadDir(evalDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var removed []string
	for _, e := range entries {
		if !e.IsDir() {
			continue // top-level files (ISSUE-*.md, etc.) are not run output
		}
		rm, err := pruneOneEvalDir(filepath.Join(evalDir, e.Name()), keepN, dryRun)
		if err != nil {
			return removed, err
		}
		removed = append(removed, rm...)
	}
	return removed, nil
}

func pruneOneEvalDir(bedDir string, keepN int, dryRun bool) ([]string, error) {
	children, err := os.ReadDir(bedDir)
	if err != nil {
		return nil, err
	}
	var calverDirs, resultFiles []string
	hasRuns := false
	for _, c := range children {
		name := c.Name()
		if name == "NOTES.md" {
			continue // durable memory — never prune
		}
		if c.IsDir() {
			if _, ok := ParseCalVer(name); ok {
				calverDirs = append(calverDirs, name)
			} else if name == "runs" {
				hasRuns = true
			}
		} else if strings.HasPrefix(name, "result-") && strings.HasSuffix(name, ".yml") {
			resultFiles = append(resultFiles, name)
		}
	}

	var removed []string
	// CalVer-named run dirs: keep newest keepN by CalVer.
	removed = append(removed, removeOldestByCalVer(bedDir, calverDirs, keepN, "result-", ".yml", dryRun)...)
	// result-<calver>.yml: keep newest keepN by embedded CalVer.
	removed = append(removed, removeOldestByCalVer(bedDir, resultFiles, keepN, "result-", ".yml", dryRun)...)
	// runs/<id>: keep newest keepN by mtime (runIDs aren't CalVer).
	if hasRuns {
		removed = append(removed, removeOldestByMtime(filepath.Join(bedDir, "runs"), keepN, dryRun)...)
	}
	return removed, nil
}

// removeOldestByCalVer keeps the newest keepN entries (sorted by the CalVer
// embedded in the name, after trimming the given prefix/suffix) and removes the
// rest. Entries without a parseable CalVer are left untouched.
func removeOldestByCalVer(parent string, names []string, keepN int, prefix, suffix string, dryRun bool) []string {
	type dated struct {
		name string
		cv   CalVer
	}
	var items []dated
	for _, n := range names {
		core := strings.TrimSuffix(strings.TrimPrefix(n, prefix), suffix)
		if cv, ok := ParseCalVer(core); ok {
			items = append(items, dated{n, cv})
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[j].cv.Less(items[i].cv) }) // newest first
	var removed []string
	for idx, it := range items {
		if idx < keepN {
			continue
		}
		p := filepath.Join(parent, it.name)
		if dryRun {
			removed = append(removed, p)
			continue
		}
		if err := os.RemoveAll(p); err == nil {
			removed = append(removed, p)
		}
	}
	return removed
}

// removeOldestByMtime keeps the newest keepN immediate subdirs of dir (by
// modification time) and removes the rest.
func removeOldestByMtime(dir string, keepN int, dryRun bool) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	type timed struct {
		name string
		mod  int64
	}
	var items []timed
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		items = append(items, timed{e.Name(), info.ModTime().UnixNano()})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].mod > items[j].mod }) // newest first
	var removed []string
	for idx, it := range items {
		if idx < keepN {
			continue
		}
		p := filepath.Join(dir, it.name)
		if dryRun {
			removed = append(removed, p)
			continue
		}
		if err := os.RemoveAll(p); err == nil {
			removed = append(removed, p)
		}
	}
	return removed
}

// cleanMakepkgArtifacts removes the one-time makepkg build leftovers under
// pkg/arch (src/, pkg/, *.pkg.tar.zst, *.log). These are pure transient waste:
// the package is already installed via pacman. Returns the paths removed.
func cleanMakepkgArtifacts(projectDir string, dryRun bool) []string {
	base := filepath.Join(projectDir, "pkg", "arch")
	var targets []string
	for _, sub := range []string{"src", "pkg"} {
		p := filepath.Join(base, sub)
		if _, err := os.Stat(p); err == nil {
			targets = append(targets, p)
		}
	}
	for _, pat := range []string{"*.pkg.tar.zst", "*.log"} {
		matches, _ := filepath.Glob(filepath.Join(base, pat))
		targets = append(targets, matches...)
	}
	var removed []string
	for _, p := range targets {
		if dryRun {
			removed = append(removed, p)
			continue
		}
		if err := os.RemoveAll(p); err == nil {
			removed = append(removed, p)
		}
	}
	return removed
}

// CleanCmd applies the configured retention now (the on-demand counterpart to
// the auto-prune that runs after `ov image build` / `ov eval run`), and also
// sweeps the one-time makepkg backlog.
type CleanCmd struct {
	DryRun bool `long:"dry-run" help:"Print everything that would be removed; touch nothing"`
	Images bool `long:"images" help:"Only image-tag retention"`
	Eval   bool `long:"eval" help:"Only eval-run retention"`
	Keep   int  `long:"keep" help:"Override the retention count for this run (0 = use defaults:)"`
}

func (c *CleanCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	keepImages := resolveIntPtr(nil, nil, keepImagesFallback)
	keepEval := resolveIntPtr(nil, nil, keepEvalRunsFallback)
	if cfg, err := LoadConfig(dir); err == nil {
		keepImages = resolveIntPtr(cfg.Defaults.KeepImages, nil, keepImagesFallback)
		keepEval = resolveIntPtr(cfg.Defaults.KeepEvalRuns, nil, keepEvalRunsFallback)
	}
	if c.Keep > 0 {
		keepImages, keepEval = c.Keep, c.Keep
	}

	// Default (neither flag) = all categories.
	doImages := c.Images || (!c.Images && !c.Eval)
	doEval := c.Eval || (!c.Images && !c.Eval)
	doMakepkg := !c.Images && !c.Eval

	tag := "removed"
	if c.DryRun {
		tag = "would remove"
	}

	if doImages {
		rt, err := ResolveRuntime()
		if err != nil {
			return err
		}
		refs, err := pruneImagesByRetention(EngineBinary(rt.BuildEngine), keepImages, c.DryRun)
		if err != nil {
			return fmt.Errorf("pruning images: %w", err)
		}
		fmt.Printf("images: %s %d tag(s) (keep_images=%d)\n", tag, len(refs), keepImages)
		for _, r := range refs {
			fmt.Printf("  %s\n", r)
		}
	}
	if doEval {
		paths, err := pruneEvalRuns(filepath.Join(dir, ".eval"), keepEval, c.DryRun)
		if err != nil {
			return fmt.Errorf("pruning eval runs: %w", err)
		}
		fmt.Printf("eval: %s %d run artifact(s) (keep_eval_runs=%d, NOTES.md preserved)\n", tag, len(paths), keepEval)
		for _, p := range paths {
			fmt.Printf("  %s\n", p)
		}
	}
	if doMakepkg {
		paths := cleanMakepkgArtifacts(dir, c.DryRun)
		fmt.Printf("makepkg: %s %d leftover(s) under pkg/arch\n", tag, len(paths))
		for _, p := range paths {
			fmt.Printf("  %s\n", p)
		}
	}
	return nil
}
