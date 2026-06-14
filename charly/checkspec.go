package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Op is the unified operation vocabulary — the single struct that replaces the
// former Task (install/build verbs) and Check (probe/assert verbs). One Op has
// exactly one verb plus three orthogonal axes:
//
//   - Do:      act | assert | instruct — does the op perform a side-effect,
//     assert state, or hand a free-form instruction to the agent grader.
//     Per-verb default lives in VerbCatalog; an explicit `do:` overrides.
//   - Context: build | deploy | runtime | agent — where the op is legal and which
//     engine runs it (generalization of the former Check.Scope). Empty →
//     the verb's default contexts from VerbCatalog.
//
// VerbCatalog is the single source of truth for per-verb legality, default Do,
// reversibility, and the InstallPlan step kind an act-mode op lowers to.
type Op struct {
	// Verb discriminators — exactly one non-empty (enforced by Kind()).
	File        string `yaml:"file,omitempty"          json:"file,omitempty"`
	Package     string `yaml:"package,omitempty"       json:"package,omitempty"`
	Service     string `yaml:"service,omitempty"       json:"service,omitempty"`
	Port        int    `yaml:"port,omitempty"          json:"port,omitempty"`
	Process     string `yaml:"process,omitempty"       json:"process,omitempty"`
	Command     string `yaml:"command,omitempty"       json:"command,omitempty"`
	HTTP        string `yaml:"http,omitempty"          json:"http,omitempty"`
	DNS         string `yaml:"dns,omitempty"           json:"dns,omitempty"`
	User        string `yaml:"user,omitempty"          json:"user,omitempty"`
	Group       string `yaml:"group,omitempty"         json:"group,omitempty"`
	Interface   string `yaml:"interface,omitempty"     json:"interface,omitempty"`
	KernelParam string `yaml:"kernel-param,omitempty"  json:"kernel-param,omitempty"`
	Mount       string `yaml:"mount,omitempty"         json:"mount,omitempty"`
	Addr        string `yaml:"addr,omitempty"          json:"addr,omitempty"`
	Matching    any    `yaml:"matching,omitempty"      json:"matching,omitempty"`

	// Install/build verb discriminators (formerly the Task struct). These are
	// imperative by nature: their VerbCatalog DefaultDo is "act". `command`
	// (above) collapses the former Task `cmd:` and Check `command:` into one
	// verb — context decides RUN-directive (build) vs RunCapture/RunSystem.
	Mkdir    string `yaml:"mkdir,omitempty"    json:"mkdir,omitempty"`    // directory to create
	Copy     string `yaml:"copy,omitempty"     json:"copy,omitempty"`     // candy-dir file to stage (src)
	Write    string `yaml:"write,omitempty"    json:"write,omitempty"`    // destination path for inline Content
	Link     string `yaml:"link,omitempty"     json:"link,omitempty"`     // symlink path (where the link lives)
	Download string `yaml:"download,omitempty" json:"download,omitempty"` // URL to fetch
	Setcap   string `yaml:"setcap,omitempty"   json:"setcap,omitempty"`   // file path for capability op
	Build    string `yaml:"build,omitempty"    json:"build,omitempty"`    // builder selector ("all")

	// Test-mode live-container verbs — each is a method-name discriminator
	// validated against the CLI's subcommand surface. Dispatched by runCdp/
	// runWl/runDbus/runVnc/runMcp/runRecord/runSpice/runLibvirt in
	// testrun_ov_verbs.go via subprocess delegation to `charly check <verb> <method>`.
	// See /charly:test for authoring, /charly:cdp, /charly:wl, /charly:dbus, /charly:vnc, /charly:mcp,
	// /charly:record, /charly:spice, /charly:libvirt for per-verb method semantics.
	Cdp     string `yaml:"cdp,omitempty"     json:"cdp,omitempty"`
	Wl      string `yaml:"wl,omitempty"      json:"wl,omitempty"`
	Dbus    string `yaml:"dbus,omitempty"    json:"dbus,omitempty"`
	Vnc     string `yaml:"vnc,omitempty"     json:"vnc,omitempty"`
	Mcp     string `yaml:"mcp,omitempty"     json:"mcp,omitempty"`
	Record  string `yaml:"record,omitempty"  json:"record,omitempty"`
	Spice   string `yaml:"spice,omitempty"   json:"spice,omitempty"`
	Libvirt string `yaml:"libvirt,omitempty" json:"libvirt,omitempty"`
	K8s     string `yaml:"k8s,omitempty"     json:"k8s,omitempty"`
	Adb     string `yaml:"adb,omitempty"     json:"adb,omitempty"`
	Appium  string `yaml:"appium,omitempty"  json:"appium,omitempty"`
	// Kill is a verb discriminator. The value is a PID string (typically
	// produced via ${CAPTURED:<name>} from a prior `command:` step that
	// ran with `background: true` and captured its "backgrounded pid=N"
	// message). The kill: verb resolves this PID and sends a signal —
	// SIGTERM by default, SIGKILL when Signal is "KILL". Companion to
	// Background; the two together let steps spawn a writer, kill it
	// mid-stream, and assert post-state consistency.
	Kill   string `yaml:"kill,omitempty"   json:"kill,omitempty"`
	Signal string `yaml:"signal,omitempty" json:"signal,omitempty"` // "TERM" (default) or "KILL"

	// Shared resource-identity modifiers — reusable across verbs that need
	// them (k8s needs all four; future verbs can opt in). Kept as plain
	// top-level fields so YAML authoring stays `name:` / `namespace:` rather
	// than verb-prefixed.
	Name      string `yaml:"name,omitempty"      json:"name,omitempty"`
	Namespace string `yaml:"namespace,omitempty" json:"namespace,omitempty"`
	Label     string `yaml:"label,omitempty"     json:"label,omitempty"`
	Cluster   string `yaml:"cluster,omitempty"   json:"cluster,omitempty"`
	Manifest  string `yaml:"manifest,omitempty"  json:"manifest,omitempty"` // path to a YAML file for k8s apply/delete — intentionally distinct from the file: verb

	// k8s-specific modifiers — prefixed so they don't collide with the
	// existing `Kind()` method on Check (Go forbids a field and method with
	// the same name on the same type) and so the YAML surface is
	// self-describing (k8s_kind: Deployment).
	K8sKind     string `yaml:"k8s_kind,omitempty"     json:"k8s_kind,omitempty"`
	K8sContext  string `yaml:"k8s_context,omitempty"  json:"k8s_context,omitempty"`
	Kubeconfig  string `yaml:"kubeconfig,omitempty"   json:"kubeconfig,omitempty"`
	K8sCount    int    `yaml:"k8s_count,omitempty"    json:"k8s_count,omitempty"`
	K8sResource string `yaml:"k8s_resource,omitempty" json:"k8s_resource,omitempty"`
	K8sGroup    string `yaml:"k8s_group,omitempty"    json:"k8s_group,omitempty"`
	K8sVersion  string `yaml:"k8s_version,omitempty"  json:"k8s_version,omitempty"`
	// JSON modifies the `k8s: raw` verb's list-mode output: when true,
	// the underlying `charly check k8s raw` invocation passes --json so the
	// full Kubernetes List object (with .kind, .apiVersion, .items[])
	// is emitted instead of the default `<namespace>/<name>` per
	// line. Check authors expecting `stdout: { contains: "kind" }`
	// against the JSON document need this flag — the default
	// names-only list is back-compat-preserved.
	JSON bool `yaml:"json,omitempty" json:"json,omitempty"`

	// Shared modifiers
	ID          string `yaml:"id,omitempty"           json:"id,omitempty"`
	Description string `yaml:"description,omitempty"  json:"description,omitempty"`
	Skip        bool   `yaml:"skip,omitempty"         json:"skip,omitempty"`
	Timeout     string `yaml:"timeout,omitempty"      json:"timeout,omitempty"`
	InContainer *bool  `yaml:"in_container,omitempty" json:"in_container,omitempty"`

	// Unified operation axes.
	//   intentDo: act | assert | instruct — STAMPED by the enclosing Step's
	//            keyword (run→act, check→assert, agent-*→instruct) at run/
	//            collect time, never authored. The former authored `do:` axis
	//            is RETIRED — the step keyword IS the do-mode.
	//   Context: subset of {build, deploy, runtime}. Empty → VerbCatalog default.
	intentDo DoMode   `yaml:"-"                 json:"-"`
	Context  []string `yaml:"context,omitempty" json:"context,omitempty"`

	// Pod targets the container a check/agent-check step probes (a per-step
	// field). Empty → the run-level default target. DependsOn
	// lists step ids this step depends on (topological ordering + cascade
	// skip). Both are per-step fields carried on the inline Op.
	Pod       string   `yaml:"pod,omitempty"        json:"pod,omitempty"`
	DependsOn []string `yaml:"depends_on,omitempty" json:"depends_on,omitempty"`

	// Install/build modifiers (formerly Task). Validity depends on verb.
	//   RunAs:           user context for an act-mode op (root / ${USER} / name /
	//                    uid:gid) — the former Task `user:`, renamed so it does not
	//                    collide with the `user` VERB discriminator. (Mode, Target,
	//                    Caps already exist above and merge by verb-scoped meaning.)
	RunAs           string            `yaml:"run_as,omitempty"          json:"run_as,omitempty"`
	To              string            `yaml:"to,omitempty"              json:"to,omitempty"`                // copy/download destination
	Content         string            `yaml:"content,omitempty"         json:"content,omitempty"`           // write: inline body
	Extract         string            `yaml:"extract,omitempty"         json:"extract,omitempty"`           // download: archive format
	ExtractInclude  []string          `yaml:"extract_include,omitempty" json:"extract_include,omitempty"`   // download: extract filter (renamed from include: — that key is now the plan-composition Step.Include)
	StripComponents int               `yaml:"strip_components,omitempty" json:"strip_components,omitempty"` // download: tar --strip-components
	Uninstall       []string          `yaml:"uninstall,omitempty"       json:"uninstall,omitempty"`         // explicit reverse-target file list
	Comment         string            `yaml:"comment,omitempty"         json:"comment,omitempty"`           // Containerfile comment
	Cache           []string          `yaml:"cache,omitempty"           json:"cache,omitempty"`             // BuildKit cache-mount paths
	Env             map[string]string `yaml:"env,omitempty"             json:"env,omitempty"`               // download: install-script env

	// Step modifiers — usable both in plan Steps and in classical `tests:` entries.
	//
	//   Capture:       stash this check's produced output under <name>; downstream refs use ${CAPTURED:name}.
	//                  Scoped per plan run; reset between runs.
	//                  Capture is recorded ONLY on the final PASS (so Eventually retries don't pollute).
	//   Eventually:    duration string; retry the check (verb + matchers) until pass or timeout.
	//                  Composes with Timeout (per-attempt cap) — Eventually is the outer retry cap.
	//   RetryInterval: spacing between retries; defaults to "1s"; must be ≤ Eventually.
	//   On:            target-entity override for multi-target plan runs. Omit → use the run's default target.
	//                  Each On dispatch resolves a target-specific VarResolver (HOST_PORT / CONTAINER_IP / …).
	//   Tag: free-form label set for --tag filtering (per-step; no group inheritance).
	Capture string `yaml:"capture,omitempty"        json:"capture,omitempty"`
	// CaptureExtract: optional Go RE2 regex applied to the raw capture
	// value before it lands in ScenarioContext.Captures. The first
	// regex submatch group becomes the captured value (whole match if
	// no group is present). Use when a verb's natural output carries
	// the value you want plus surrounding noise — e.g. the
	// "backgrounded pid=NNNN" Message from a `background: true`
	// command, where `capture_extract: "pid=([0-9]+)"` extracts just
	// the PID. If the regex fails to match, the check FAILS rather
	// than storing the unextracted value (silent fall-through would
	// be worse than a loud failure here).
	CaptureExtract string   `yaml:"capture_extract,omitempty" json:"capture_extract,omitempty"`
	Eventually     string   `yaml:"eventually,omitempty"     json:"eventually,omitempty"`
	RetryInterval  string   `yaml:"retry_interval,omitempty" json:"retry_interval,omitempty"`
	On             string   `yaml:"on,omitempty"             json:"on,omitempty"`
	Tag            []string `yaml:"tag,omitempty"           json:"tag,omitempty"`

	// Concurrency primitives (2026-05) — usable in plan Steps to express
	// stress, race, and high-volume concurrency tests declaratively.
	//
	//   Parallel:   non-empty group id; consecutive steps with the same value
	//               run concurrently as goroutines, awaited via WaitGroup
	//               before the runner advances past the last step in the group.
	//               Empty = sequential (current behavior). Validator: only
	//               valid in plan Steps.
	//   Count:      expand the step into N iterations. Each iteration receives
	//               its own CheckResult with id="<orig>-<i>" and an INDEX
	//               variable available via ${INDEX} (or ${IndexVar} if set).
	//               Combines with Parallel: all N expansions of a parallel
	//               step run concurrently.
	//   IndexVar:   override the default "INDEX" variable name when Count > 0.
	//   Background: spawn a `command:` verb without waiting for it to exit.
	//               PID is tracked in ScenarioContext.Backgrounds and reaped
	//               at run teardown via SIGTERM. Only valid on command:.
	Parallel   string `yaml:"parallel,omitempty"   json:"parallel,omitempty"`
	Count      int    `yaml:"count,omitempty"      json:"count,omitempty"`
	IndexVar   string `yaml:"index_var,omitempty"  json:"index_var,omitempty"`
	Background bool   `yaml:"background,omitempty" json:"background,omitempty"`

	// Aggregation primitive (2026-05) — `summarize:` is a verb that walks
	// prior steps' CheckResult.Elapsed values and computes distribution
	// metrics. Discriminator value is the metric kind ("latency"); siblings
	// OverIDs / Metrics / EmitID / per-metric matchers configure it.
	Summarize string   `yaml:"summarize,omitempty" json:"summarize,omitempty"`
	OverIDs   []string `yaml:"over_id,omitempty"  json:"over_ids,omitempty"`
	Metrics   []string `yaml:"metric,omitempty"   json:"metrics,omitempty"`
	EmitID    string   `yaml:"emit_id,omitempty"   json:"emit_id,omitempty"`
	// Per-metric numeric matchers — when set, the verb fails if the metric
	// exceeds the threshold. Reuses the existing Matcher type so lt/le/gt/ge
	// numeric ops work as on any other verb.
	P50Match  Matcher `yaml:"p50,omitempty"  json:"p50"`
	P95Match  Matcher `yaml:"p95,omitempty"  json:"p95"`
	P99Match  Matcher `yaml:"p99,omitempty"  json:"p99"`
	MaxMatch  Matcher `yaml:"max,omitempty"  json:"max"`
	MeanMatch Matcher `yaml:"mean,omitempty" json:"mean"`

	// Origin is populated at collection time (candy:<name>, box:<name>,
	// deploy-default, deploy-local). Not authored in YAML, but travels in
	// the OCI label JSON.
	Origin string `yaml:"-" json:"origin,omitempty"`

	// file-specific
	Exists   *bool        `yaml:"exists,omitempty"   json:"exists,omitempty"`
	Mode     string       `yaml:"mode,omitempty"     json:"mode,omitempty"`
	Owner    string       `yaml:"owner,omitempty"    json:"owner,omitempty"`
	GroupOf  string       `yaml:"group_of,omitempty" json:"group_of,omitempty"` // file's group; named to avoid clashing with verb-level Group
	Filetype string       `yaml:"filetype,omitempty" json:"filetype,omitempty"` // file, directory, symlink
	Contains ContainsList `yaml:"contains,omitempty" json:"contains,omitempty"`
	Sha256   string       `yaml:"sha256,omitempty"   json:"sha256,omitempty"`

	// package-specific
	Installed *bool    `yaml:"installed,omitempty" json:"installed,omitempty"`
	Versions  []string `yaml:"version,omitempty"  json:"versions,omitempty"`
	// PackageMap overrides the Package name per distro. Keys match the image's
	// distro tags (e.g. "arch", "fedora", "ubuntu", "debian"). If the
	// running image's distro tag is present in the map, that value replaces
	// Package for the probe; otherwise Package is used as-is. Required for
	// cross-distro tests where the same software ships under different
	// package names (e.g. openssh-server on Fedora vs openssh on Arch).
	PackageMap map[string]string `yaml:"package_map,omitempty" json:"package_map,omitempty"`

	// ExcludeDistros lists distro tags on which this check must NOT run.
	// The test runner skips (not fails) the check when any of the image's
	// distro tags matches an entry here. Use this when a probe is only
	// meaningful on some distros — e.g. a `file: /usr/bin/fastfetch`
	// probe when the package only ships on some distros' repos. Matched
	// against the full image distro list (e.g. ["ubuntu:24.04", "ubuntu",
	// "debian"]) so `ubuntu:24.04` and `ubuntu` both match.
	ExcludeDistros []string `yaml:"exclude_distro,omitempty" json:"exclude_distros,omitempty"`

	// service-specific
	Enabled *bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Running *bool `yaml:"running,omitempty" json:"running,omitempty"` // also reused by process

	// port-specific
	Listening *bool  `yaml:"listening,omitempty" json:"listening,omitempty"`
	IP        string `yaml:"ip,omitempty"        json:"ip,omitempty"`

	// command-specific
	ExitStatus *int        `yaml:"exit_status,omitempty" json:"exit_status,omitempty"`
	Stdout     MatcherList `yaml:"stdout,omitempty"      json:"stdout,omitempty"`
	Stderr     MatcherList `yaml:"stderr,omitempty"      json:"stderr,omitempty"`

	// http-specific
	Status        int         `yaml:"status,omitempty"              json:"status,omitempty"`
	Body          MatcherList `yaml:"body,omitempty"                json:"body,omitempty"`
	Headers       MatcherList `yaml:"header,omitempty"             json:"headers,omitempty"`
	AllowInsecure bool        `yaml:"allow_insecure,omitempty"      json:"allow_insecure,omitempty"`
	NoFollowRedir bool        `yaml:"no_follow_redirects,omitempty" json:"no_follow_redirects,omitempty"`
	CAFile        string      `yaml:"ca_file,omitempty"             json:"ca_file,omitempty"`
	Method        string      `yaml:"method,omitempty"              json:"method,omitempty"`
	RequestBody   string      `yaml:"request_body,omitempty"        json:"request_body,omitempty"`

	// dns-specific
	Resolvable *bool    `yaml:"resolvable,omitempty" json:"resolvable,omitempty"`
	Addrs      []string `yaml:"addrs,omitempty"      json:"addrs,omitempty"` // also reused by interface
	Server     string   `yaml:"server,omitempty"     json:"server,omitempty"`

	// user/group-specific
	UID    *int     `yaml:"uid,omitempty"    json:"uid,omitempty"`
	GID    *int     `yaml:"gid,omitempty"    json:"gid,omitempty"`
	Home   string   `yaml:"home,omitempty"   json:"home,omitempty"`
	Shell  string   `yaml:"shell,omitempty"  json:"shell,omitempty"`
	Groups []string `yaml:"groups,omitempty" json:"groups,omitempty"`

	// interface-specific
	MTU *int `yaml:"mtu,omitempty" json:"mtu,omitempty"`

	// kernel-param specific (and reused wherever a single expected value
	// + optional matcher decoration is the natural shape). MatcherList
	// scalar shorthand means `value: "1"` decodes to [{equals 1}].
	Value MatcherList `yaml:"value,omitempty" json:"value,omitempty"`

	// mount-specific
	MountSource string      `yaml:"mount_source,omitempty" json:"mount_source,omitempty"` // backing device/path
	Filesystem  string      `yaml:"filesystem,omitempty"   json:"filesystem,omitempty"`
	Opts        MatcherList `yaml:"opt,omitempty"         json:"opts,omitempty"`

	// addr-specific
	Reachable *bool `yaml:"reachable,omitempty" json:"reachable,omitempty"`

	// command-specific routing (false = run from host, true = run via podman exec)
	FromHost bool `yaml:"from_host,omitempty" json:"from_host,omitempty"`

	// cdp/wl/dbus/vnc-specific modifiers. Applicable sets vary per method —
	// validate_tests.go enforces required-modifier rules per {verb, method}.
	Tab                   string   `yaml:"tab,omitempty"                json:"tab,omitempty"`                            // cdp: tab id
	Expression            string   `yaml:"expression,omitempty"         json:"expression,omitempty"`                     // cdp: check expression
	URL                   string   `yaml:"url,omitempty"                json:"url,omitempty"`                            // cdp: open url
	Selector              string   `yaml:"selector,omitempty"           json:"selector,omitempty"`                       // cdp: click/type/wait/coords/axtree
	Dest                  string   `yaml:"dest,omitempty"               json:"dest,omitempty"`                           // dbus: service name
	Path                  string   `yaml:"path,omitempty"               json:"path,omitempty"`                           // dbus: object path
	Args                  []string `yaml:"arg,omitempty"               json:"args,omitempty"`                            // dbus: method args (type:value)
	Artifact              string   `yaml:"artifact,omitempty"                json:"artifact,omitempty"`                  // cdp/wl/vnc: output file path for screenshot / raw capture
	ArtifactMinBytes      int      `yaml:"artifact_min_bytes,omitempty"      json:"artifact_min_bytes,omitempty"`        // post-run size assertion on artifact
	ArtifactMinDimensions string   `yaml:"artifact_min_dimensions,omitempty" json:"artifact_min_dimensions,omitempty"`   // post-run "WxH" min dimensions assertion (PNG/JPEG header decode)
	ArtifactNotUniform    bool     `yaml:"artifact_not_uniform,omitempty"    json:"artifact_not_uniform,omitempty"`      // post-run "image is not uniformly one color" assertion (full decode + pixel sampling)
	ArtifactMinCastEvents int      `yaml:"artifact_min_cast_events,omitempty" json:"artifact_min_cast_events,omitempty"` // post-run asciinema .cast event-line count assertion
	X                     int      `yaml:"x,omitempty"                  json:"x,omitempty"`                              // wl/vnc: click/mouse x coord; for drag: start x (X1)
	Y                     int      `yaml:"y,omitempty"                  json:"y,omitempty"`                              // wl/vnc: click/mouse y coord; for drag: start y (Y1)
	X2                    int      `yaml:"x2,omitempty"                 json:"x2,omitempty"`                             // wl: drag end x (mirrors WlDragCmd.X2)
	Y2                    int      `yaml:"y2,omitempty"                 json:"y2,omitempty"`                             // wl: drag end y (mirrors WlDragCmd.Y2)
	Button                string   `yaml:"button,omitempty"             json:"button,omitempty"`                         // wl/vnc: left/middle/right
	Text                  string   `yaml:"text,omitempty"               json:"text,omitempty"`                           // wl/vnc: type text / overlay text
	KeyName               string   `yaml:"key,omitempty"                json:"key,omitempty"`                            // wl/vnc: key name (Return/Escape/...)
	Combo                 string   `yaml:"combo,omitempty"              json:"combo,omitempty"`                          // wl: key-combo (ctrl+c)
	Direction             string   `yaml:"direction,omitempty"          json:"direction,omitempty"`                      // wl: scroll up/down/left/right
	Amount                int      `yaml:"amount,omitempty"             json:"amount,omitempty"`                         // wl: scroll amount
	Target                string   `yaml:"target,omitempty"             json:"target,omitempty"`                         // wl: focus/close/geometry/xprop target
	Action                string   `yaml:"action,omitempty"             json:"action,omitempty"`                         // wl: atspi action (tree/find/click)
	Query                 string   `yaml:"query,omitempty"              json:"query,omitempty"`                          // cdp: axtree filter / wl: atspi find query

	// mcp-specific modifiers. See /charly:test "Method allowlist — mcp" for which
	// methods require which fields; enforcement in validate_tests.go.
	McpName string `yaml:"mcp_name,omitempty" json:"mcp_name,omitempty"` // mcp: server name when image exposes multiple mcp_provides
	Tool    string `yaml:"tool,omitempty"     json:"tool,omitempty"`     // mcp: tool name for the `call` method
	URI     string `yaml:"uri,omitempty"      json:"uri,omitempty"`      // mcp: resource URI for the `read` method
	Input   string `yaml:"input,omitempty"    json:"input,omitempty"`    // mcp: JSON argument blob for the `call` method (e.g. '{"path":"x.ipynb"}')

	// adb / appium-specific modifiers. See /charly-check:adb and /charly-check:appium.
	Apk      string `yaml:"apk,omitempty"      json:"apk,omitempty"`      // adb: install / appium: install-app — APK path on host filesystem
	Property string `yaml:"property,omitempty" json:"property,omitempty"` // adb: getprop — system property key (e.g. sys.boot_completed)
	Caps     string `yaml:"caps,omitempty"     json:"caps,omitempty"`     // appium: session-create — W3C alwaysMatch capabilities JSON
	Strategy string `yaml:"strategy,omitempty" json:"strategy,omitempty"` // appium: find/click/send-keys — locator strategy (xpath|id|accessibility-id|class-name|android-uiautomator)
	Session  string `yaml:"session,omitempty"  json:"session,omitempty"`  // appium: explicit session id override (default: read from ~/.cache/charly/appium/sessions/<image>[_<instance>].json)
	AppId    string `yaml:"app_id,omitempty"   json:"app_id,omitempty"`   // appium app-*: package id (io.appium.android.apis); ALSO adb install-app: the app package id to fetch+install via apkeep
	// adb install-app (apkeep) modifiers. The verb downloads <app_id> via apkeep
	// IN the pod and installs it onto the emulator (single .apk / split set / .xapk).
	Source     string `yaml:"source,omitempty"      json:"source,omitempty"`      // adb install-app: apkeep source (apk-pure|google-play|f-droid|huawei-app-gallery; default apk-pure)
	Arch       string `yaml:"arch,omitempty"        json:"arch,omitempty"`        // adb install-app: apkeep -o arch= native ABI (e.g. x86_64) — apk-pure only
	AppVersion string `yaml:"app_version,omitempty" json:"app_version,omitempty"` // adb install-app: optional specific app version (default: latest)
	Activity   string `yaml:"activity,omitempty" json:"activity,omitempty"`       // appium app-start-activity: pkg/.activity (intent form, e.g. io.appium.android.apis/.view.TextFields)
	Attribute  string `yaml:"attribute,omitempty" json:"attribute,omitempty"`     // appium get-attribute: attribute name (checked/enabled/selected/text/class/...)
	Percent    string `yaml:"percent,omitempty"  json:"percent,omitempty"`        // appium gesture-swipe/scroll/fling/pinch: magnitude fraction (e.g. "0.75")
	Keycode    int    `yaml:"keycode,omitempty"  json:"keycode,omitempty"`        // appium key-press: Android keycode (4=BACK, 66=ENTER, ...)
	Params     string `yaml:"params,omitempty"   json:"params,omitempty"`         // appium gesture/device escape: JSON object merged into the mobile: args / W3C body (speed, duration, endX/endY, clipboard text, orientation, context name)

	// record-specific modifiers — record: verb wraps `charly check record <method>`.
	// The Artifact + ArtifactMinBytes modifiers are reused: for `record: stop`
	// Artifact is the host-side file path (-o flag) and ArtifactMinBytes
	// enforces the post-copy size assertion. Command is reused for
	// `record: cmd` (the text typed into the recording).
	RecordName  string `yaml:"record_name,omitempty"  json:"record_name,omitempty"`  // -n/--name (defaults to "default" in the CLI)
	RecordMode  string `yaml:"record_mode,omitempty"  json:"record_mode,omitempty"`  // -m/--mode: terminal|desktop|auto (start only)
	RecordFps   int    `yaml:"record_fps,omitempty"   json:"record_fps,omitempty"`   // --fps (desktop mode, start only)
	RecordAudio bool   `yaml:"record_audio,omitempty" json:"record_audio,omitempty"` // --audio (desktop mode, start only)
}

// OpVerbs lists valid discriminator keys in stable order (used for
// deterministic error messages). It is the union of the former Task install
// verbs + Check probe/live/meta verbs. (The former free-form `agent:` verb is
// retired — agent prose now lives in the agent-run:/agent-check: step keyword,
// which carries no Op verb.)
var OpVerbs = []string{
	// install/build (imperative — DefaultDo act)
	"mkdir", "copy", "write", "link", "download", "setcap", "build",
	// probe/provision (act-or-assert)
	"file", "package", "service", "port", "process", "command",
	"http", "dns", "user", "group", "interface", "kernel-param",
	"mount", "addr", "matching",
	// live-container (runtime act-or-assert)
	"cdp", "wl", "dbus", "vnc", "mcp",
	"record", "spice", "libvirt", "k8s",
	"adb", "appium",
	// meta
	"summarize", "kill",
}

// Kind returns the check's verb name and an error if zero or multiple
// verb discriminators are set. Matches Task.Kind() semantics.
func (c *Op) Kind() (string, error) {
	set := c.verbsSet()
	if len(set) == 0 {
		return "", fmt.Errorf("check has no verb set (expected exactly one of: %s)", strings.Join(OpVerbs, ", "))
	}
	if len(set) > 1 {
		return "", fmt.Errorf("check has multiple verbs set (%s); exactly one is required", strings.Join(set, ", "))
	}
	return set[0], nil
}

// verbsSet returns the list of verb discriminators that are currently non-zero.
//
// Note on `command:` field: when an `charly-verb` (cdp / wl / dbus / vnc / mcp /
// record / spice / libvirt / k8s) is set, `command:` is interpreted as a
// MODIFIER (e.g. the argv for `libvirt: guest/exec`), NOT a verb of its own.
// Otherwise (no charly-verb set), `command:` is the verb discriminator selecting
// `runCommand` to shell out via the executor. The check-author surface
// stays:
//
//	command: "uname -s"      # alone → command verb
//	libvirt: guest/exec
//	command: "uname -s"      # paired → modifier for guest/exec argv
func (c *Op) verbsSet() []string {
	var set []string
	if c.Mkdir != "" {
		set = append(set, "mkdir")
	}
	if c.Copy != "" {
		set = append(set, "copy")
	}
	if c.Write != "" {
		set = append(set, "write")
	}
	if c.Link != "" {
		set = append(set, "link")
	}
	if c.Download != "" {
		set = append(set, "download")
	}
	if c.Setcap != "" {
		set = append(set, "setcap")
	}
	if c.Build != "" {
		set = append(set, "build")
	}
	if c.File != "" {
		set = append(set, "file")
	}
	if c.Package != "" {
		set = append(set, "package")
	}
	if c.Service != "" {
		set = append(set, "service")
	}
	if c.Port != 0 {
		set = append(set, "port")
	}
	if c.Process != "" {
		set = append(set, "process")
	}
	// Treat `command:` as a verb only when no charly-verb is set; otherwise it's
	// a modifier (e.g. argv for libvirt:guest/exec).
	hasCharlyVerb := c.Cdp != "" || c.Wl != "" || c.Dbus != "" || c.Vnc != "" ||
		c.Mcp != "" || c.Record != "" || c.Spice != "" || c.Libvirt != "" ||
		c.K8s != "" || c.Adb != "" || c.Appium != ""
	if c.Command != "" && !hasCharlyVerb {
		set = append(set, "command")
	}
	if c.HTTP != "" {
		set = append(set, "http")
	}
	if c.DNS != "" {
		set = append(set, "dns")
	}
	if c.User != "" {
		set = append(set, "user")
	}
	if c.Group != "" {
		set = append(set, "group")
	}
	if c.Interface != "" {
		set = append(set, "interface")
	}
	if c.KernelParam != "" {
		set = append(set, "kernel-param")
	}
	if c.Mount != "" {
		set = append(set, "mount")
	}
	if c.Addr != "" {
		set = append(set, "addr")
	}
	if c.Matching != nil {
		set = append(set, "matching")
	}
	if c.Cdp != "" {
		set = append(set, "cdp")
	}
	if c.Wl != "" {
		set = append(set, "wl")
	}
	if c.Dbus != "" {
		set = append(set, "dbus")
	}
	if c.Vnc != "" {
		set = append(set, "vnc")
	}
	if c.Mcp != "" {
		set = append(set, "mcp")
	}
	if c.Record != "" {
		set = append(set, "record")
	}
	if c.Spice != "" {
		set = append(set, "spice")
	}
	if c.Libvirt != "" {
		set = append(set, "libvirt")
	}
	if c.K8s != "" {
		set = append(set, "k8s")
	}
	if c.Adb != "" {
		set = append(set, "adb")
	}
	if c.Appium != "" {
		set = append(set, "appium")
	}
	if c.Summarize != "" {
		set = append(set, "summarize")
	}
	if c.Kill != "" {
		set = append(set, "kill")
	}
	return set
}

// Matcher represents a goss-style matcher that can be either a scalar value
// (decoded as an implicit equality match) or an operator map such as
// {equals: X}, {contains: [...]}, {matches: "regex"}, {lt: N}, {gt: N}.
//
// Scalar form decodes to Op="equals", Value=<scalar>.
// Map form decodes to Op=<first key>, Value=<its value>.
type Matcher struct {
	Op    string `json:"op"`
	Value any    `json:"value,omitempty"`
}

// UnmarshalYAML accepts either a scalar or a single-key map.
func (m *Matcher) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		var v any
		if err := node.Decode(&v); err != nil {
			return fmt.Errorf("decoding matcher scalar: %w", err)
		}
		m.Op = "equals"
		m.Value = v
		return nil
	case yaml.SequenceNode:
		var v []any
		if err := node.Decode(&v); err != nil {
			return fmt.Errorf("decoding matcher sequence: %w", err)
		}
		m.Op = "equals"
		m.Value = v
		return nil
	case yaml.MappingNode:
		var raw map[string]any
		if err := node.Decode(&raw); err != nil {
			return fmt.Errorf("decoding matcher map: %w", err)
		}
		if len(raw) != 1 {
			return fmt.Errorf("matcher map must have exactly one operator key, got %d", len(raw))
		}
		for k, v := range raw {
			m.Op = k
			m.Value = v
		}
		return nil
	default:
		return fmt.Errorf("matcher: unsupported YAML kind %d", node.Kind)
	}
}

// MarshalYAML emits the canonical operator-map form so round-tripping is stable.
func (m Matcher) MarshalYAML() (any, error) { //nolint:unparam // error return kept for interface/API stability
	if m.Op == "equals" {
		return m.Value, nil
	}
	return map[string]any{m.Op: m.Value}, nil
}

// UnmarshalJSON keeps the JSON read path symmetric with UnmarshalYAML: it
// accepts the canonical `{"op":"...","value":...}` form (what our own
// writeJSONLabel emits) AND a bare scalar (`"PONG"` → equals). Hand-crafted
// OCI labels that use the authoring shorthand now parse without the
// otherwise-cryptic "cannot unmarshal string into struct" error.
func (m *Matcher) UnmarshalJSON(data []byte) error {
	s := bytes.TrimSpace(data)
	if len(s) == 0 {
		return nil
	}
	if s[0] == '{' {
		// Canonical or operator-map form.
		var tmp struct {
			Op    string `json:"op"`
			Value any    `json:"value"`
		}
		if err := json.Unmarshal(data, &tmp); err == nil && tmp.Op != "" {
			m.Op = tmp.Op
			m.Value = tmp.Value
			return nil
		}
		// Operator-map form: {"equals": X} / {"contains": [...]}
		var raw map[string]any
		if err := json.Unmarshal(data, &raw); err != nil {
			return err
		}
		if len(raw) != 1 {
			return fmt.Errorf("matcher map must have exactly one operator key, got %d", len(raw))
		}
		for k, v := range raw {
			m.Op = k
			m.Value = v
		}
		return nil
	}
	// Scalar / array: decode into the Value field, Op defaults to equals.
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	m.Op = "equals"
	m.Value = v
	return nil
}

// MatcherList lets users write scalar/single-map shorthand where a list of
// Matchers is expected. `stdout: PONG` and `stdout: [PONG]` decode identically.
type MatcherList []Matcher

// UnmarshalYAML accepts a sequence (normal list) OR a scalar/single-map
// (wrapped in a one-element list).
func (ml *MatcherList) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.SequenceNode {
		var list []Matcher
		if err := node.Decode(&list); err != nil {
			return err
		}
		*ml = list
		return nil
	}
	var m Matcher
	if err := m.UnmarshalYAML(node); err != nil {
		return err
	}
	*ml = []Matcher{m}
	return nil
}

// UnmarshalJSON mirrors UnmarshalYAML's scalar-shorthand behavior for the
// JSON read path (OCI labels, hand-crafted JSON fixtures). Accepts:
//   - JSON array  → []Matcher
//   - JSON scalar → [{equals: scalar}]
//   - JSON object → [{op/value OR operator-map}]
func (ml *MatcherList) UnmarshalJSON(data []byte) error {
	s := bytes.TrimSpace(data)
	if len(s) == 0 {
		*ml = nil
		return nil
	}
	if s[0] == '[' {
		var list []Matcher
		if err := json.Unmarshal(data, &list); err != nil {
			return err
		}
		*ml = list
		return nil
	}
	var m Matcher
	if err := m.UnmarshalJSON(data); err != nil {
		return err
	}
	*ml = []Matcher{m}
	return nil
}

// ContainsList is a MatcherList variant whose bare-scalar/sequence elements
// default to Op="contains" instead of Op="equals". Used on probe fields whose
// YAML key is literally `contains:` (e.g. file probes) — the field name's
// promise is "the content contains these substrings". The pre-2026-04-27
// authoring shorthand `contains: ["X", "Y"]` silently promoted to Op="equals"
// because MatcherList.UnmarshalYAML routes scalars through Matcher's
// ScalarNode branch, which hardcodes Op="equals". That made the probe ask
// "does the entire content EQUAL X" — semantically wrong for a `contains:`
// field. ContainsList fixes the field-level intent without changing
// MatcherList's behavior on stdout/body/etc., where Op="equals" is the
// correct default for bare scalars.
//
// Explicit operator-map elements (`{equals: X}`, `{matches: R}`, `{lt: N}`)
// keep the authored Op verbatim — only bare scalars/sequences are promoted.
type ContainsList []Matcher

// UnmarshalYAML promotes bare scalar/sequence elements to Op="contains" while
// preserving the authored Op for explicit operator-map elements.
func (cl *ContainsList) UnmarshalYAML(node *yaml.Node) error {
	promote := func(child *yaml.Node) (Matcher, error) {
		var m Matcher
		switch child.Kind {
		case yaml.ScalarNode:
			var v any
			if err := child.Decode(&v); err != nil {
				return m, fmt.Errorf("decoding contains scalar: %w", err)
			}
			m.Op = "contains"
			m.Value = v
			return m, nil
		case yaml.MappingNode, yaml.SequenceNode:
			// Defer to Matcher; explicit {op: value} keeps the authored Op.
			// Nested sequences fall through to Matcher's SequenceNode branch
			// (Op="equals", Value=[]any) — the field-level promotion does not
			// recurse into a list of lists.
			if err := m.UnmarshalYAML(child); err != nil {
				return m, err
			}
			return m, nil
		default:
			return m, fmt.Errorf("contains: unsupported YAML kind %d", child.Kind)
		}
	}
	if node.Kind == yaml.SequenceNode {
		list := make([]Matcher, 0, len(node.Content))
		for i, child := range node.Content {
			m, err := promote(child)
			if err != nil {
				return fmt.Errorf("contains[%d]: %w", i, err)
			}
			list = append(list, m)
		}
		*cl = ContainsList(list)
		return nil
	}
	m, err := promote(node)
	if err != nil {
		return err
	}
	*cl = ContainsList{m}
	return nil
}

// LabelDescriptionSet (labelset.go) is the three-section label set carrying an
// image's baked plan steps; the LabelSet aggregate there wraps it.

// ---------------------------------------------------------------------------
// Variable expansion (extended grammar shared with tasks)
//
// The existing taskVarRefPattern in charly/tasks.go matches ${NAME}. Tests need
// parameterized refs like ${HOST_PORT:6379} and ${VOLUME_PATH:workspace} to
// address deploy-time values. testVarRefPattern is the extended grammar;
// it is a superset of the task pattern so task refs continue to work here.
// ---------------------------------------------------------------------------

// testVarRefPattern matches ${NAME} and ${NAME:arg} references. Group 1 is
// the variable name; group 2 is the optional argument (empty when absent).
//
// Backward-compatible widening of taskVarRefPattern at charly/tasks.go.
var testVarRefPattern = regexp.MustCompile(`\$\{([A-Z_][A-Z0-9_]*)(?::([^}]+))?\}`)

// ExpandTestVars substitutes ${NAME} and ${NAME:arg} references using the
// supplied environment map.
//
// Keys in env for plain refs use just the name: env["HOME"] = "/home/user".
// Keys for parameterized refs combine name and argument with a colon:
// env["HOST_PORT:6379"] = "16379", env["VOLUME_PATH:workspace"] = "/var/lib/…".
//
// Returns the substituted string and a list of unresolved refs (in encounter
// order, deduplicated). The caller decides whether unresolved refs are an
// error (build-time validation) or a skip reason (runtime).
func ExpandTestVars(s string, env map[string]string) (string, []string) {
	seen := map[string]bool{}
	var missing []string
	out := testVarRefPattern.ReplaceAllStringFunc(s, func(match string) string {
		sub := testVarRefPattern.FindStringSubmatch(match)
		name, arg := sub[1], sub[2]
		key := name
		if arg != "" {
			key = name + ":" + arg
		}
		if v, ok := env[key]; ok {
			return v
		}
		if !seen[key] {
			seen[key] = true
			missing = append(missing, key)
		}
		return match // leave unresolved refs visible in output
	})
	return out, missing
}

// TestVarRefs returns the set of ${NAME[:arg]} references in s, as their
// fully-qualified keys (matching the env-map format used by ExpandTestVars).
// Used by the validator to catch typos at config time.
func TestVarRefs(s string) []string {
	matches := testVarRefPattern.FindAllStringSubmatch(s, -1)
	var out []string
	seen := map[string]bool{}
	for _, m := range matches {
		key := m[1]
		if m[2] != "" {
			key = m[1] + ":" + m[2]
		}
		if !seen[key] {
			seen[key] = true
			out = append(out, key)
		}
	}
	return out
}

// runtimeOnlyVarPrefixes lists variable name prefixes that are only resolvable
// against a running container. scope:"build" checks must not reference these.
var runtimeOnlyVarPrefixes = []string{
	"HOST_PORT",
	"VOLUME_PATH",
	"VOLUME_CONTAINER_PATH",
	"CONTAINER_IP",
	"CONTAINER_NAME",
	"INSTANCE",
	"ENV_",
	// Capture store + step id are populated only at plan-run
	// execution time, so they're effectively runtime-only.
	"CAPTURED",
	"STEP_ID",
	// VM live-check intent: how many <hostdev> the VM's spec declares. Resolved
	// only against a live VM deployment (check_cmd.go VM path), so a build-scope
	// check must not reference it.
	"VM_HOSTDEV_COUNT",
	// The sanitized deploy name of the deployment under check — the same value
	// K3sPostProvision uses for the kubeconfig context + ClusterProfile name, so
	// a deploy-scope k8s check can address its own cluster generically via
	// cluster: "${DEPLOY_NAME}". Resolved only against a live deployment.
	"DEPLOY_NAME",
	// Cross-deployment address vars (check_peer.go): ${PEER_HOST:name} and
	// ${PEER_ENDPOINT:name:port} let a driven probe (a check with `on:`) reach
	// a SEPARATE subject deployment. Resolved only against running deployments,
	// so a build-scope check must not reference them.
	"PEER_HOST",
	"PEER_ENDPOINT",
}

// IsRuntimeOnlyVar reports whether the given variable key (as returned by
// TestVarRefs) refers to a runtime-only value. The check matches on name
// prefix because parameterized vars share a common prefix with their arg.
func IsRuntimeOnlyVar(key string) bool {
	name := key
	if before, _, ok := strings.Cut(key, ":"); ok {
		name = before
	}
	for _, p := range runtimeOnlyVarPrefixes {
		if name == p || strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Field-walking helpers
// ---------------------------------------------------------------------------

// StringFields returns pointers to every string-typed attribute on a Check
// that may contain ${VAR} references. Used by the expander and validator to
// iterate over substitutable fields without reflecting over the entire struct.
//
// The slice is stable (not affected by which verb is set) so callers can
// safely iterate and mutate in place.
func (c *Op) StringFields() []*string {
	return []*string{
		&c.File, &c.Package, &c.Service, &c.Process, &c.Command,
		&c.HTTP, &c.DNS, &c.User, &c.Group, &c.Interface,
		&c.KernelParam, &c.Mount, &c.Addr,
		&c.ID, &c.Description, &c.Timeout,
		&c.Mode, &c.Owner, &c.GroupOf, &c.Filetype, &c.Sha256,
		&c.IP, &c.CAFile, &c.Method, &c.RequestBody,
		&c.Server, &c.Home, &c.Shell,
		&c.MountSource, &c.Filesystem,
		// Test-mode live-container verb discriminators + modifiers.
		&c.Cdp, &c.Wl, &c.Dbus, &c.Vnc, &c.Mcp,
		&c.Record, &c.Spice, &c.Libvirt, &c.K8s,
		&c.Adb, &c.Appium,
		// k8s + shared resource-identity modifiers
		&c.Name, &c.Namespace, &c.Label, &c.Cluster, &c.Manifest,
		&c.K8sKind, &c.K8sContext, &c.Kubeconfig,
		&c.K8sResource, &c.K8sGroup, &c.K8sVersion,
		&c.Tab, &c.Expression, &c.URL, &c.Selector,
		&c.Dest, &c.Path, &c.Artifact,
		&c.Button, &c.Text, &c.KeyName, &c.Combo,
		&c.Direction, &c.Target, &c.Action, &c.Query,
		// mcp-specific modifiers
		&c.McpName, &c.Tool, &c.URI, &c.Input,
		// adb / appium-specific modifiers
		&c.Apk, &c.Property, &c.Caps, &c.Strategy, &c.Session,
		// record-specific modifiers
		&c.RecordName, &c.RecordMode,
		// BDD-era modifiers — On may contain ${VAR}; Capture/Eventually/RetryInterval
		// are identifiers/durations but still run through the expander for symmetry.
		&c.On, &c.Capture, &c.CaptureExtract, &c.Eventually, &c.RetryInterval,
		// kill: verb's PID arg is typically ${CAPTURED:<name>} from a prior
		// background command; Signal is a literal but expanded for symmetry.
		&c.Kill, &c.Signal,
		// Install/build verb discriminators + path-like modifiers (the former
		// Task surface). Content is INTENTIONALLY excluded — write: bodies are
		// verbatim bytes, never ${VAR}-substituted (matches the task rule).
		&c.Mkdir, &c.Copy, &c.Write, &c.Link, &c.Download, &c.Setcap, &c.Build,
		&c.RunAs, &c.To, &c.Extract,
	}
}

// ExpandVars rewrites every ${...} reference on this Check in place using
// the supplied environment map. Returns the combined list of unresolved refs
// encountered across all string fields.
func (c *Op) ExpandVars(env map[string]string) []string {
	seen := map[string]bool{}
	var missing []string
	for _, p := range c.StringFields() {
		if *p == "" {
			continue
		}
		replaced, unresolved := ExpandTestVars(*p, env)
		*p = replaced
		for _, k := range unresolved {
			if !seen[k] {
				seen[k] = true
				missing = append(missing, k)
			}
		}
	}
	sort.Strings(missing)
	return missing
}

// ---------------------------------------------------------------------------
// Unified verb vocabulary — execution context, do-mode, and the VerbCatalog
// single source of truth for per-verb legality + lowering.
// ---------------------------------------------------------------------------

// ExecContext is where an op runs. An op's Context list (or its VerbCatalog
// default) declares legality; the active engine supplies the running context
// and skips ops whose context set does not include it (VenueSkip).
type ExecContext string

const (
	CtxBuild   ExecContext = "build"   // image construction (OCITarget → Containerfile)
	CtxDeploy  ExecContext = "deploy"  // host/VM/pod provisioning (DeployExecutor)
	CtxRuntime ExecContext = "runtime" // a running target (check Runner)
)

// DoMode is the act/assert/instruct axis. act = perform a side-effect;
// assert = run the matchers (read-only); instruct = hand free-form text to the
// agent grader.
type DoMode string

const (
	DoAct      DoMode = "act"
	DoAssert   DoMode = "assert"
	DoInstruct DoMode = "instruct"
)

// VerbSpec is the per-verb metadata in VerbCatalog. Contexts[0] is the
// canonical default context. LowersTo names the InstallPlan step kind an
// act-mode op of this verb lowers to ("" → a generic OpStep). Reversible marks
// whether act-mode reversal is automatic (an auto ReverseOp); when false an
// act-mode op needs an explicit `uninstall:` or is reversed via plan
// teardown (live verbs) — enforced in validation.
type VerbSpec struct {
	Contexts   []ExecContext
	DefaultDo  DoMode
	Reversible bool
	LowersTo   StepKind
}

// HasContext reports whether the verb is legal in ctx.
func (s VerbSpec) HasContext(ctx ExecContext) bool {
	return slices.Contains(s.Contexts, ctx)
}

var (
	ctxBuildDeploy        = []ExecContext{CtxBuild, CtxDeploy}
	ctxBuildDeployRuntime = []ExecContext{CtxBuild, CtxDeploy, CtxRuntime}
	ctxDeployRuntime      = []ExecContext{CtxDeploy, CtxRuntime}
	ctxRuntimeOnly        = []ExecContext{CtxRuntime}
)

// VerbCatalog is the single source of truth for every verb's legality, default
// do-mode, reversibility, and act-mode lowering target — one table driving
// validation, dispatch, and lowering. Keys match OpVerbs.
var VerbCatalog = map[string]VerbSpec{
	// install/build — imperative; build+deploy only (no live-runtime form).
	"mkdir":    {ctxBuildDeploy, DoAct, false, ""},
	"copy":     {ctxBuildDeploy, DoAct, true, ""}, // build → COPY, deploy → PutFile (venue-lowered)
	"write":    {ctxBuildDeploy, DoAct, true, ""},
	"link":     {ctxBuildDeploy, DoAct, true, ""},
	"download": {ctxBuildDeploy, DoAct, true, ""},
	"setcap":   {ctxBuildDeploy, DoAct, false, ""},
	"build":    {ctxBuildDeploy, DoAct, false, ""},

	// shell — act-or-assert; portable across build/deploy/runtime. No
	// auto-reverse (opaque); act-mode needs explicit uninstall:.
	"command": {ctxBuildDeployRuntime, DoAssert, false, ""},

	// system-state probe/provision — assert by default; the act-capable subset
	// lowers into existing reversible InstallPlan step kinds.
	"file":         {ctxBuildDeployRuntime, DoAssert, true, ""}, // probe; file-creation is the write/copy verbs (act → runtime executor)
	"package":      {ctxBuildDeployRuntime, DoAssert, true, StepKindSystemPackages},
	"service":      {ctxBuildDeployRuntime, DoAssert, true, StepKindServicePackaged}, // act → enable the named packaged unit
	"user":         {ctxBuildDeployRuntime, DoAssert, true, ""},                      // act → useradd (+ ReverseOpUserRemove)
	"group":        {ctxBuildDeployRuntime, DoAssert, true, ""},                      // act → groupadd (+ ReverseOpGroupRemove)
	"kernel-param": {ctxBuildDeployRuntime, DoAssert, true, ""},                      // act → sysctl (+ ReverseOpSysctlRestore)
	"mount":        {ctxDeployRuntime, DoAssert, true, ""},                           // act → mount (+ ReverseOpUmount)
	"port":         {ctxBuildDeployRuntime, DoAssert, false, ""},                     // observe-only
	"process":      {ctxBuildDeployRuntime, DoAssert, false, ""},                     // observe-only
	"http":         {ctxDeployRuntime, DoAssert, false, ""},                          // act → request (no reverse)
	"dns":          {ctxDeployRuntime, DoAssert, false, ""},                          // observe-only
	"interface":    {ctxRuntimeOnly, DoAssert, false, ""},                            // observe-only
	"addr":         {ctxDeployRuntime, DoAssert, false, ""},                          // observe-only
	"matching":     {ctxBuildDeployRuntime, DoAssert, false, ""},

	// live-container — runtime only; act drives UI/config, reversed via plan
	// teardown (never the ledger). k8s also legal at deploy (apply manifest).
	"cdp":     {ctxRuntimeOnly, DoAssert, false, ""},
	"wl":      {ctxRuntimeOnly, DoAssert, false, ""},
	"dbus":    {ctxRuntimeOnly, DoAssert, false, ""},
	"vnc":     {ctxRuntimeOnly, DoAssert, false, ""},
	"mcp":     {ctxRuntimeOnly, DoAssert, false, ""},
	"record":  {ctxRuntimeOnly, DoAssert, false, ""},
	"spice":   {ctxRuntimeOnly, DoAssert, false, ""},
	"libvirt": {ctxRuntimeOnly, DoAssert, false, ""},
	"k8s":     {ctxDeployRuntime, DoAssert, false, ""},
	"adb":     {ctxRuntimeOnly, DoAssert, false, ""},
	"appium":  {ctxRuntimeOnly, DoAssert, false, ""},

	// meta.
	"summarize": {ctxRuntimeOnly, DoAssert, false, ""},
	"kill":      {ctxRuntimeOnly, DoAct, false, ""},
}

// installVerbs are the verbs that render directly to a generic OpStep install
// step (a Containerfile directive at build, a deploy shell command at deploy).
// Distinct from the LowersTo verbs, which lower into a typed install step.
var installVerbs = map[string]bool{
	"mkdir": true, "copy": true, "write": true, "link": true,
	"download": true, "setcap": true, "build": true, "command": true,
}

// ActsInBuildDeploy reports whether a do:act op with this verb has a real
// build/deploy install path — a generic OpStep (the install verbs + command)
// or a typed lowering (VerbCatalog.LowersTo). Every other verb's act form runs
// only at runtime (the check Runner's executor), so a build/deploy do:act op of
// such a verb would be silently dropped by the compiler — the validator
// rejects it instead (file creation in build/deploy is the write/copy verbs).
func ActsInBuildDeploy(verb string) bool {
	return installVerbs[verb] || VerbCatalog[verb].LowersTo != ""
}

// EffectiveDo returns the op's resolved do-mode: the keyword-stamped intentDo
// wins (set by the enclosing Step at run/collect time), else the verb's
// VerbCatalog default, else DoAssert.
func (c *Op) EffectiveDo() DoMode {
	switch c.intentDo {
	case DoAct, DoAssert, DoInstruct:
		return c.intentDo
	}
	verb, err := c.Kind()
	if err == nil {
		if spec, ok := VerbCatalog[verb]; ok && spec.DefaultDo != "" {
			return spec.DefaultDo
		}
	}
	return DoAssert
}

// EffectiveContexts returns the op's resolved execution contexts: an explicit
// Context wins, else the verb's VerbCatalog default, else nil.
func (c *Op) EffectiveContexts() []ExecContext {
	if len(c.Context) > 0 {
		out := make([]ExecContext, 0, len(c.Context))
		for _, s := range c.Context {
			out = append(out, ExecContext(s))
		}
		return out
	}
	if verb, err := c.Kind(); err == nil {
		if spec, ok := VerbCatalog[verb]; ok {
			return spec.Contexts
		}
	}
	return nil
}

// InContext reports whether the op is legal in ctx per its effective contexts.
func (c *Op) InContext(ctx ExecContext) bool {
	return slices.Contains(c.EffectiveContexts(), ctx)
}
