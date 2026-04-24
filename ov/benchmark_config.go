package main

// benchmark_config.go — runner configuration + token substitution for
// `ov benchmark`. Extends the unified YAML loader with a `benchmark:`
// top-level key that carries runner entries and the prompt template.
//
// Exported surface:
//   - BenchmarkConfig, BenchmarkRunner, CredentialMount, SubstContext
//   - LoadBenchmarkConfig(dir) (*BenchmarkConfig, error)
//   - ResolveRunner(cfg, name) (*BenchmarkRunner, error)
//   - Substitute, SubstituteArgv, SubstituteEnv
//   - PrintRunners(w, cfg)
//   - ErrNoRunners, ErrRunnerNotFound sentinels
//
// The UnifiedFile struct in unified.go grows one optional field —
// Benchmark *BenchmarkConfig — wired in benchmark_cmd.go's consumer
// path, NOT here. This file stays decoupled from the unified loader's
// internals so benchmark_config_test.go can exercise it standalone.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// BenchmarkConfig is the `benchmark:` section of overthink.yml.
//
// A project with no benchmark runs configured simply omits the key;
// LoadBenchmarkConfig returns (nil, nil) in that case and the caller
// decides whether absence is fatal (BenchmarkRunCmd: yes).
type BenchmarkConfig struct {
	Runners []BenchmarkRunner `yaml:"runners,omitempty"`
	Prompt  string            `yaml:"prompt,omitempty"`
}

// BenchmarkRunner is one entry under benchmark.runners. The Command
// slice is the argv that `ov benchmark run` executes INSIDE the target
// deployment via the dispatcher (pod/host/vm).
type BenchmarkRunner struct {
	Name        string            `yaml:"name"`
	Command     []string          `yaml:"command"`
	PromptVia   string            `yaml:"prompt_via,omitempty"`
	Env         map[string]string `yaml:"env,omitempty"`
	Timeout     string            `yaml:"timeout,omitempty"`
	WorkingDir  string            `yaml:"working_dir,omitempty"`
	Credentials []CredentialMount `yaml:"credentials,omitempty"`
	Cleanup     bool              `yaml:"cleanup_credentials,omitempty"`
}

// CredentialMount names one host path whose contents are synced into
// the deployment BEFORE the first AI invocation. Typical entries:
// `~/.claude/` (Claude Code OAuth state). Copied ONCE at benchmark
// start, never per iteration.
type CredentialMount struct {
	Src      string `yaml:"src"`
	Dst      string `yaml:"dst"`
	Mode     string `yaml:"mode,omitempty"`     // "copy" (default) | "bind"
	Optional bool   `yaml:"optional,omitempty"` // missing src: warn, don't fail
}

// SubstContext holds every token Substitute can expand into. Callers
// populate the fields they know about; unknown tokens fall through to
// os.Getenv, and unresolved tokens expand to empty.
type SubstContext struct {
	RunID             string
	WorkspacePath     string
	TargetImage       string
	TargetDeployment  string
	Iteration         int
	MaxIterations     int
	PlateauIterations int
	PlateauCounter    int
	BestScore         int
	MCPEndpoint       string
	Tags              string
	Prompt            string            // rendered prompt text (for ${PROMPT})
	PromptFile        string            // only for PromptVia == "file"
	Deadline          string            // RFC3339 string, or "" when no deadline
	Timeout           string            // per-runner resolved timeout string
	ExtraEnv          map[string]string // overrides for any ${X} token
}

// DefaultRunnerTimeout is the Go-level default applied by ResolveRunner
// when a runner entry's `timeout:` field is empty. Per the approved
// plan (§2.2), 30 minutes bounds a single AI invocation — the overall
// benchmark is plateau-driven and has no wall-clock cap.
const DefaultRunnerTimeout = "30m"

// ---------------------------------------------------------------------------
// Sentinel errors
// ---------------------------------------------------------------------------

var (
	// ErrNoRunners fires when benchmark.runners is missing or empty.
	ErrNoRunners = errors.New("benchmark: no runners configured in overthink.yml (add a 'benchmark.runners' list)")

	// ErrRunnerNotFound fires when the requested runner name is absent.
	ErrRunnerNotFound = errors.New("benchmark: runner not found")
)

// ---------------------------------------------------------------------------
// Loader
// ---------------------------------------------------------------------------

// LoadBenchmarkConfig reads <dir>/overthink.yml and returns the
// benchmark: section if present. An absent section returns (nil, nil)
// so the caller can distinguish "no config" from "malformed config".
//
// The loader is standalone — it does NOT invoke LoadUnified. This keeps
// benchmark config parsing cheap for the common `ov benchmark list-runners`
// path, and makes unit testing trivial.
func LoadBenchmarkConfig(dir string) (*BenchmarkConfig, error) {
	path := filepath.Join(dir, "overthink.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	// Parse the multi-doc YAML stream; benchmark: can live in any root-
	// shape doc, matching how the unified loader handles other keys.
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	for {
		var probe map[string]yaml.Node
		if err := dec.Decode(&probe); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		if node, ok := probe["benchmark"]; ok {
			var cfg BenchmarkConfig
			if err := node.Decode(&cfg); err != nil {
				return nil, fmt.Errorf("parse benchmark: section in %s: %w", path, err)
			}
			return &cfg, nil
		}
	}
	return nil, nil
}

// ---------------------------------------------------------------------------
// Runner resolution + defaults
// ---------------------------------------------------------------------------

// ResolveRunner returns the named runner, or the sole runner if name == ""
// and exactly one is configured. Applies Go-level defaults:
//   - Timeout: "30m" when empty
//   - PromptVia: "argv" when empty
//   - Cleanup: already false by zero value (no action)
//
// Errors: ErrNoRunners when the config carries no runners;
// ErrRunnerNotFound when name is non-empty and doesn't match; ambiguity
// error when name is "" and multiple runners exist.
func ResolveRunner(cfg *BenchmarkConfig, name string) (*BenchmarkRunner, error) {
	if cfg == nil || len(cfg.Runners) == 0 {
		return nil, ErrNoRunners
	}

	pickRunner := func(r BenchmarkRunner) *BenchmarkRunner {
		// Return a copy so callers can mutate without poisoning the config.
		out := r
		if out.Timeout == "" {
			out.Timeout = DefaultRunnerTimeout
		}
		if out.PromptVia == "" {
			out.PromptVia = "argv"
		}
		return &out
	}

	if name == "" {
		if len(cfg.Runners) > 1 {
			names := runnerNames(cfg.Runners)
			return nil, fmt.Errorf("benchmark: multiple runners configured (%s); pass --runner NAME to pick one",
				strings.Join(names, ", "))
		}
		return pickRunner(cfg.Runners[0]), nil
	}

	for _, r := range cfg.Runners {
		if r.Name == name {
			return pickRunner(r), nil
		}
	}
	return nil, fmt.Errorf("%w: %q (available: %s)",
		ErrRunnerNotFound, name, strings.Join(runnerNames(cfg.Runners), ", "))
}

// runnerNames extracts runner names in config order for error reporting.
func runnerNames(runners []BenchmarkRunner) []string {
	out := make([]string, len(runners))
	for i, r := range runners {
		out[i] = r.Name
	}
	return out
}

// ParseRunnerTimeout parses the runner's Timeout field with the Go-level
// default applied for empty strings. Exposed so benchmark_loop can apply
// the same default consistently.
func ParseRunnerTimeout(s string) (time.Duration, error) {
	if s == "" {
		s = DefaultRunnerTimeout
	}
	return time.ParseDuration(s)
}

// ---------------------------------------------------------------------------
// Token substitution
// ---------------------------------------------------------------------------

// tokenRe matches ${IDENT} where IDENT is the standard shell identifier
// form (leading uppercase letter or underscore, followed by uppercase,
// digit, or underscore). Matches the plan's §2.2 regex exactly.
var tokenRe = regexp.MustCompile(`\$\{([A-Z_][A-Z0-9_]*)\}`)

// Substitute replaces every ${TOKEN} in `in` using ctx. Unknown tokens
// that match OS env vars are expanded from os.Getenv; unknown-and-unset
// tokens resolve to the empty string.
//
// The substitution is single-pass — a token whose expansion contains
// another ${X} pattern is NOT re-expanded. This is intentional: it
// prevents runaway expansion when env vars coincidentally use the same
// syntax.
func Substitute(in string, ctx *SubstContext) string {
	if ctx == nil {
		ctx = &SubstContext{}
	}
	return tokenRe.ReplaceAllStringFunc(in, func(match string) string {
		// match is "${NAME}"; strip the boundaries.
		name := match[2 : len(match)-1]
		return lookupToken(name, ctx)
	})
}

// SubstituteArgv applies Substitute to every element of argv, returning
// a new slice. argv is not mutated.
func SubstituteArgv(argv []string, ctx *SubstContext) []string {
	out := make([]string, len(argv))
	for i, a := range argv {
		out[i] = Substitute(a, ctx)
	}
	return out
}

// SubstituteEnv applies Substitute to every value in env, returning a
// new map. env is not mutated. Keys are not substituted.
func SubstituteEnv(env map[string]string, ctx *SubstContext) map[string]string {
	if env == nil {
		return nil
	}
	out := make(map[string]string, len(env))
	for k, v := range env {
		out[k] = Substitute(v, ctx)
	}
	return out
}

// lookupToken resolves one token name against ctx, ctx.ExtraEnv, and
// finally os.Getenv. Unresolved tokens return "".
func lookupToken(name string, ctx *SubstContext) string {
	// Well-known token table first — fixed set, deterministic.
	switch name {
	case "PROMPT":
		return ctx.Prompt
	case "PROMPT_FILE":
		return ctx.PromptFile
	case "WORKSPACE":
		return ctx.WorkspacePath
	case "TARGET_IMAGE":
		return ctx.TargetImage
	case "TARGET_DEPLOYMENT":
		return ctx.TargetDeployment
	case "RUN_ID":
		return ctx.RunID
	case "ITERATION":
		return intToken(ctx.Iteration)
	case "MAX_ITERATIONS":
		return intToken(ctx.MaxIterations)
	case "PLATEAU_ITERATIONS":
		return intToken(ctx.PlateauIterations)
	case "PLATEAU_COUNTER":
		return intToken(ctx.PlateauCounter)
	case "BEST_SCORE":
		return intToken(ctx.BestScore)
	case "MCP_ENDPOINT":
		return ctx.MCPEndpoint
	case "TAGS":
		return ctx.Tags
	case "DEADLINE":
		return ctx.Deadline
	case "TIMEOUT":
		return ctx.Timeout
	}
	// ExtraEnv overrides osEnv when both are set; this lets callers
	// inject per-run env without polluting the real os.Environ().
	if v, ok := ctx.ExtraEnv[name]; ok {
		return v
	}
	return os.Getenv(name)
}

// intToken stringifies an int for substitution. Zero becomes "0" —
// the reader can distinguish zero-by-default from zero-explicit by
// context (e.g. ITERATION starts at 1 in the loop).
func intToken(n int) string {
	return fmt.Sprintf("%d", n)
}

// ---------------------------------------------------------------------------
// Runner table printer
// ---------------------------------------------------------------------------

// PrintRunners writes a human-readable table of configured runners to w.
// Used by `ov benchmark list-runners`.
func PrintRunners(w io.Writer, cfg *BenchmarkConfig) {
	if cfg == nil || len(cfg.Runners) == 0 {
		fmt.Fprintln(w, "No runners configured. Add a benchmark.runners list to overthink.yml.")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tCOMMAND\tTIMEOUT\tPROMPT_VIA\tCREDENTIALS\tCLEANUP")
	for _, r := range cfg.Runners {
		timeout := r.Timeout
		if timeout == "" {
			timeout = DefaultRunnerTimeout + " (default)"
		}
		promptVia := r.PromptVia
		if promptVia == "" {
			promptVia = "argv (default)"
		}
		cmd := strings.Join(r.Command, " ")
		if len(cmd) > 60 {
			cmd = cmd[:57] + "..."
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%v\n",
			r.Name, cmd, timeout, promptVia, len(r.Credentials), r.Cleanup)
	}
	_ = tw.Flush()
}

// SortedRunnerNames returns runner names in alphabetical order. Useful
// for deterministic error messages and stable test expectations.
func SortedRunnerNames(cfg *BenchmarkConfig) []string {
	if cfg == nil {
		return nil
	}
	out := runnerNames(cfg.Runners)
	sort.Strings(out)
	return out
}
