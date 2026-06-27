package main

// plugin_command_prescan.go — the EARLY (pre-kong.Parse) external-command-word prescan.
//
// An external (out-of-tree) COMMAND plugin contributes a `charly <word>` CLI subcommand.
// Kong must know that word to PARSE `charly <word> …`, but the provider is only connected
// by loadProjectPlugins AFTER the project dir is resolved from a Kong flag — chicken-and-egg
// (the same shape plugin_prescan.go solves for deploy substrates). The fix: before kong.Parse,
// cheaply read the project's declared command words (the byte-gated plugin prescan), so
// collectExternalCommandPlugins can put a grammar holder in place for each. The provider is
// NOT connected here — the connect is LAZY, paid only when the user actually runs the command
// (dispatchExternalCommand). Best-effort and ADDITIVE: a project with no command plugins (or
// no readable charly.yml) registers nothing, so the grammar is byte-for-byte unchanged and
// every existing charly invocation is unaffected.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// prescanProjectCommandWords learns the external COMMAND words the pre-parse project directory
// declares, so collectExternalCommandPlugins can build a Kong grammar holder for each BEFORE
// kong.Parse. Reuses the byte-gated, best-effort plugin prescan (prescanDeclaredPluginWords →
// prescanPluginManifest, which registers ClassCommand words). Best-effort: a missing/unparsable
// charly.yml or a project with no command plugins registers nothing — the grammar is unchanged.
func prescanProjectCommandWords() {
	dir := projectDirPreParse()
	if dir == "" {
		return
	}
	data, err := os.ReadFile(filepath.Join(dir, UnifiedFileName))
	if err != nil {
		return
	}
	prescanDeclaredPluginWords(data, dir)
}

// projectDirPreParse resolves the project directory BEFORE kong.Parse, mirroring main's later
// -C/--dir/CHARLY_PROJECT_DIR resolution. Kong populates CHARLY_PROJECT_DIR from the --dir flag,
// so the env is the reliable proxy; a minimal os.Args scan covers the bare flag form. --repo is
// intentionally NOT resolved here (it would fetch a remote repo on every charly invocation); a
// remote-repo project's command plugins are simply not pre-registered. Returns "" only when
// os.Getwd() also fails.
func projectDirPreParse() string {
	if d := os.Getenv("CHARLY_PROJECT_DIR"); d != "" {
		return d
	}
	if d := scanDirFlag(os.Args); d != "" {
		return d
	}
	if d, err := os.Getwd(); err == nil {
		return d
	}
	return ""
}

// scanDirFlag finds a -C / --dir project-dir flag in raw argv (before kong parses), in both
// "-C <dir>" / "--dir <dir>" and "-C=<dir>" / "--dir=<dir>" forms. Returns "" if absent.
func scanDirFlag(args []string) string {
	for i := 1; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-C" || a == "--dir":
			if i+1 < len(args) {
				return args[i+1]
			}
		case strings.HasPrefix(a, "-C="):
			return strings.TrimPrefix(a, "-C=")
		case strings.HasPrefix(a, "--dir="):
			return strings.TrimPrefix(a, "--dir=")
		}
	}
	return ""
}

// connectCommandPlugin builds + connects the out-of-process plugin that provides command:word
// and returns its Provider — the LAZY connect, paid only when the user actually invokes the
// command. It mirrors loadDeployPlugins' scan (LoadConfig → ScanAllCandyWithConfigOpts →
// loadProjectPlugins) but SCOPES the load to this one command word (the word IS the reference,
// so collectReferencedPluginWords is bypassed), so only the invoked command's plugin is built.
// The project dir is the post-chdir cwd (main resolved -C/--dir/--repo before dispatch). Returns
// (nil,false) on any load/connect failure — surfaced loudly by dispatchExternalCommand.
func connectCommandPlugin(word string) (Provider, bool) {
	if p, ok := providerRegistry.resolve(ClassCommand, word); ok {
		return p, true // already connected (the eager path)
	}
	// Baked path: a DEPLOYED container has no candy source to scan, so a baked command plugin
	// connects DIRECTLY from its baked binary (discoverBakedPluginWords mapped word → binary).
	if bin, ok := bakedCommandBinaries[word]; ok {
		if loadBakedPluginBinary(context.Background(), bin) {
			if p, ok := providerRegistry.resolve(ClassCommand, word); ok {
				return p, true
			}
		}
	}
	dir, err := os.Getwd()
	if err != nil {
		return nil, false
	}
	cfg, cerr := LoadConfig(dir)
	if cerr != nil {
		return nil, false
	}
	candyMap, scanErr := ScanAllCandyWithConfigOpts(dir, cfg, ResolveOpts{})
	if scanErr != nil || candyMap == nil {
		return nil, false
	}
	if perr := loadProjectPlugins(context.Background(), candyMap, map[string]struct{}{word: {}}); perr != nil {
		fmt.Fprintf(os.Stderr, "warning: command plugin load: %v\n", perr)
	}
	return providerRegistry.resolve(ClassCommand, word)
}
