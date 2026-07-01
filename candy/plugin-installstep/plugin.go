// Package installstep is the importable, DUAL-PLACEMENT charly class:step plugin that serves
// the BUILD-context OpEmit leg for the seven PURE builtin InstallStep kinds — file, shell-hook,
// shell-snippet, service-packaged, service-custom, repo-change, and apk-install.
//
// It is the C1.1 externalization of those kinds' build-emit: each render is pure string
// formatting from the compiler-produced spec.InstallStepView (the SAME serializable view the
// deploy walk consumes), so the plugin needs no host build engine — it returns the Containerfile
// fragment directly from OpEmit. The DEPLOY leg for these kinds STAYS in charly/plugin/kit.WalkPlans
// (walkFile / walkShellHook / …), which renders them from the same view over the executor reverse
// channel; this plugin serves ONLY OpEmit (the pod-overlay build-emit the host's OCITarget splices).
//
// Placement is free: charly COMPILES this candy IN (listed in charly.yml compiled_plugins:,
// registered in-process via registerCompiledPlugin), and the SAME provider serves OUT-OF-PROCESS
// over go-plugin gRPC via the cmd/serve shim — zero authoring change either way.
//
// The OpEmit payload is a spec.InstallStepView; there is NO authored plugin_input (these steps are
// compiler-emitted from declarative candy fields, never authored as a `plugin:` step), so no
// capability declares an InputDef and the shipped CUE schema is vestigial (present only to satisfy
// the plugin load gate).
package installstep

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/overthinkos/overthink/charly/plugin/kit"
	pb "github.com/overthinkos/overthink/charly/plugin/proto"
	"github.com/overthinkos/overthink/charly/plugin/sdk"
	"github.com/overthinkos/overthink/charly/spec"
)

//go:embed schema/*.cue
var schemaFS embed.FS

const calver = "2026.182.1000"

// opEmit mirrors charly's OpEmit selector ("emit"). This plugin serves ONLY the build-context
// emit leg — every other op is a no-op acknowledgment (the deploy leg is charly/plugin/kit.WalkPlans,
// never this plugin).
const opEmit = "emit"

// The seven step words this plugin serves. The word is the lowercase-hyphenated reserved name;
// the host maps each InstallStep kind to its word in pluginEmitStepWords (charly/provider_step.go).
const (
	wordFile            = "file"
	wordShellHook       = "shell-hook"
	wordShellSnippet    = "shell-snippet"
	wordServicePackaged = "service-packaged"
	wordServiceCustom   = "service-custom"
	wordRepoChange      = "repo-change"
	wordApkInstall      = "apk-install"
)

// NewProvider returns the step provider for in-proc registration or out-of-proc serving.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta returns the capability/schema describer.
func NewMeta() pb.PluginMetaServer { return &meta{} }

type provider struct{ pb.UnimplementedProviderServer }

// Invoke serves the BUILD-context OpEmit leg: decode the compiler-produced spec.InstallStepView
// (op.Params), render the word's Containerfile fragment, and return it as a spec.EmitReply. Any op
// other than OpEmit is a no-op ack (the deploy leg lives in charly/plugin/kit.WalkPlans, not here).
func (provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetOp() != opEmit {
		return &pb.InvokeReply{ResultJson: []byte("{}")}, nil
	}
	var view spec.InstallStepView
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &view); err != nil {
			return nil, fmt.Errorf("plugin-installstep: decode InstallStepView for %q: %w", req.GetReserved(), err)
		}
	}
	frag, err := renderFragment(req.GetReserved(), view)
	if err != nil {
		return nil, err
	}
	j, err := json.Marshal(spec.EmitReply{Fragment: frag})
	if err != nil {
		return nil, err
	}
	return &pb.InvokeReply{ResultJson: j}, nil
}

// renderFragment dispatches by step word to the pure per-kind Containerfile renderer. Each render
// reproduces the former OCITarget.emit<Kind> body verbatim, reading the SAME fields off the view.
func renderFragment(word string, v spec.InstallStepView) (string, error) {
	switch word {
	case wordFile:
		return renderFile(v), nil
	case wordShellHook:
		return renderShellHook(v), nil
	case wordShellSnippet:
		return renderShellSnippet(v), nil
	case wordServicePackaged:
		return renderServicePackaged(v), nil
	case wordServiceCustom:
		return renderServiceCustom(v), nil
	case wordRepoChange:
		return renderRepoChange(v), nil
	case wordApkInstall:
		// apk-install declares Emits=false, so the host never invokes OpEmit for it (no device at
		// image-build time; the android deploy preresolver reads the step at deploy). Kept for
		// completeness — returns an empty fragment.
		return "", nil
	default:
		return "", fmt.Errorf("plugin-installstep: unknown step word %q", word)
	}
}

// renderFile emits a file placement as COPY --chmod/--chown from the file's scratch-stage source.
func renderFile(v spec.InstallStepView) string {
	chmod := fmt.Sprintf("%04o", v.Mode&0o777)
	chown := ""
	if v.Owner != "" && v.Owner != "root" && v.Owner != "0" {
		chown = fmt.Sprintf(" --chown=%s", v.Owner)
	}
	return fmt.Sprintf("COPY --chmod=%s%s %s %s\n", chmod, chown, v.Source, v.Dest)
}

// renderShellHook emits `env:` and `path_append:` as ENV directives. Env keys are emitted in sorted
// order (deterministic — matching the box-build emitVarsEnv path).
func renderShellHook(v spec.InstallStepView) string {
	var b strings.Builder
	keys := make([]string, 0, len(v.EnvVars))
	for k := range v.EnvVars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "ENV %s=%q\n", k, v.EnvVars[k])
	}
	if len(v.PathAdd) > 0 {
		// Prepend the additions (earlier-listed entries end up leftmost on the final PATH).
		parts := make([]string, 0, len(v.PathAdd)+1)
		parts = append(parts, v.PathAdd...)
		parts = append(parts, "$PATH")
		fmt.Fprintf(&b, "ENV PATH=%s\n", strings.Join(parts, ":"))
	}
	return b.String()
}

// renderShellSnippet renders a candy's per-shell init snippet into the container's system-wide
// drop-in directory via a heredoc with a snippet-hash-derived end-marker (anti-collision).
func renderShellSnippet(v spec.InstallStepView) string {
	if v.Snippet == "" {
		return ""
	}
	h := sha256.Sum256([]byte(v.Snippet))
	marker := fmt.Sprintf("CHARLY_SHELL_%s_%x", strings.ToUpper(v.Shell), h[:4])
	return fmt.Sprintf(
		"RUN mkdir -p %s && cat > %s <<'%s'\n%s\n%s\n",
		kit.ShellQuote(filepath.Dir(v.Destination)),
		kit.ShellQuote(v.Destination),
		marker,
		v.Snippet,
		marker,
	)
}

// renderServicePackaged renders an "enable packaged systemd unit" step: the optional drop-in as a
// heredoc file write, plus an enable marker comment (the packaged unit was installed by its package).
func renderServicePackaged(v spec.InstallStepView) string {
	var b strings.Builder
	if v.OverridesText != "" && v.OverridesPath != "" {
		fmt.Fprintf(&b, "RUN mkdir -p $(dirname %s) && cat > %s <<'CHARLY_DROPIN'\n%s\nCHARLY_DROPIN\n",
			v.OverridesPath, v.OverridesPath, v.OverridesText)
	}
	if v.Enable {
		scope := "system"
		if v.TargetScope == spec.ScopeUser {
			scope = "user"
		}
		fmt.Fprintf(&b, "# Service: enable packaged unit %s (scope=%s, layer=%s)\n",
			v.Unit, scope, v.CandyName)
	}
	return b.String()
}

// renderServiceCustom emits the custom-service marker; the rendered unit content travels via the
// init-fragment pipeline, not this build-emit.
func renderServiceCustom(v spec.InstallStepView) string {
	if v.UnitText == "" {
		return ""
	}
	return fmt.Sprintf("# Service: custom %s (layer=%s)\n# -- unit content follows in the init fragment pipeline --\n",
		v.Name, v.CandyName)
}

// renderRepoChange emits a structured repo file write.
func renderRepoChange(v spec.InstallStepView) string {
	return fmt.Sprintf("RUN mkdir -p $(dirname %s) && cat > %s <<'CHARLY_REPO'\n%s\nCHARLY_REPO\n",
		v.File, v.File, v.Content)
}

type meta struct {
	pb.UnimplementedPluginMetaServer
}

// Describe advertises the seven class:step capabilities, each with its declared StepContract.
// Only Emits is load-bearing here: the host's pod-overlay OCITarget consults it to decide whether
// to Invoke OpEmit (true) or skip (false, apk-install). Scope/Venue/Gate are nominal — these
// kinds' deploy leg is charly/plugin/kit.WalkPlans, which reads the per-instance view.Scope/Venue
// computed on the concrete step, so the static contract's Scope/Venue/Gate are never consulted.
func (meta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) {
	emit := func(word string, emits bool) sdk.ProvidedCapability {
		return sdk.ProvidedCapability{
			Class:        "step",
			Word:         word,
			StepContract: &sdk.StepContract{Scope: "system", Venue: 0, Gate: "", Emits: emits},
		}
	}
	return sdk.BuildCapabilities(calver,
		[]sdk.ProvidedCapability{
			emit(wordFile, true),
			emit(wordShellHook, true),
			emit(wordShellSnippet, true),
			emit(wordServicePackaged, true),
			emit(wordServiceCustom, true),
			emit(wordRepoChange, true),
			emit(wordApkInstall, false),
		},
		schemaFS, "schema")
}
