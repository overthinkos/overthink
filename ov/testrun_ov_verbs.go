package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// testrun_ov_verbs.go implements the cdp/wl/dbus/vnc test verbs. Each verb
// is a thin wrapper around the corresponding `ov test <verb> <method>` CLI
// path — the test framework spawns a subprocess for each check, captures
// stdout/stderr/exit, and feeds the output through the existing matcher
// pipeline (Stdout/Stderr/ExitStatus + artifact size via ArtifactMinBytes).
//
// Architectural notes:
//   - Host-side only: the test runner invokes the host `ov` binary, which
//     internally connects to the container (CDP over TCP, WL via exec,
//     D-Bus via delegation, VNC over TCP). No container-side test runner.
//   - RunModeImageTest short-circuits with a skip: these verbs need a live
//     container with port mappings, which a disposable `podman run --rm`
//     container doesn't expose the same way.
//   - Method allowlists are hand-enumerated here so authoring errors surface
//     at `ov image validate` time, not at test-run time. Drift between the
//     CLI and the allowlist is a documentation issue — see /ov-dev:go for
//     the maintenance rule.

// methodSpec describes one method within a verb group.
//
//	path     — CLI subcommand path after "test", e.g. ["cdp", "eval"] or
//	           ["cdp", "spa", "click"] for nested subcommands.
//	required — Check struct field names that must be non-empty/non-zero at
//	           validation time. Empty list ⇒ no method-specific modifiers.
//	posArgs  — builds positional arguments (after image, before -i instance).
//	           May be nil if the method takes only the image positional.
//	artifact — true if the method produces an output file (screenshot etc.).
//	           When true, an `artifact:` field in the Check is required and
//	           is inserted as a positional arg at the right slot; callers may
//	           also set `artifact_min_bytes:` for a post-run size assertion.
type methodSpec struct {
	path     []string
	required []string
	posArgs  func(c *Check) []string
	artifact bool
}

// ---------------------------------------------------------------------------
// cdp methods
// ---------------------------------------------------------------------------

var cdpMethods = map[string]methodSpec{
	// queries — produce assertable output
	"status":     {path: []string{"cdp", "status"}},
	"list":       {path: []string{"cdp", "list"}},
	"url":        {path: []string{"cdp", "url"}, required: []string{"Tab"}, posArgs: posTab},
	"text":       {path: []string{"cdp", "text"}, required: []string{"Tab"}, posArgs: posTab},
	"html":       {path: []string{"cdp", "html"}, required: []string{"Tab"}, posArgs: posTab},
	"eval":       {path: []string{"cdp", "eval"}, required: []string{"Tab", "Expression"}, posArgs: posTabExpression},
	"axtree":     {path: []string{"cdp", "axtree"}, required: []string{"Tab"}, posArgs: posTabQuery},
	"coords":     {path: []string{"cdp", "coords"}, required: []string{"Tab", "Selector"}, posArgs: posTabSelector},
	"raw":        {path: []string{"cdp", "raw"}, required: []string{"Tab", "Method"}, posArgs: posCdpRaw},
	"wait":       {path: []string{"cdp", "wait"}, required: []string{"Tab", "Selector"}, posArgs: posTabSelector},
	"screenshot": {path: []string{"cdp", "screenshot"}, required: []string{"Tab", "Artifact"}, posArgs: posTabArtifact, artifact: true},

	// side-effect actions — pass on exit 0
	"open":  {path: []string{"cdp", "open"}, required: []string{"URL"}, posArgs: posURL},
	"close": {path: []string{"cdp", "close"}, required: []string{"Tab"}, posArgs: posTab},
	"click": {path: []string{"cdp", "click"}, required: []string{"Tab", "Selector"}, posArgs: posTabSelector},
	"type":  {path: []string{"cdp", "type"}, required: []string{"Tab", "Selector", "Text"}, posArgs: posTabSelectorText},

	// SPA nested subcommands
	"spa-status":    {path: []string{"cdp", "spa", "status"}, required: []string{"Tab"}, posArgs: posTab},
	"spa-click":     {path: []string{"cdp", "spa", "click"}, required: []string{"Tab", "X", "Y"}, posArgs: posTabXY},
	"spa-type":      {path: []string{"cdp", "spa", "type"}, required: []string{"Tab", "Text"}, posArgs: posTabText},
	"spa-key":       {path: []string{"cdp", "spa", "key"}, required: []string{"Tab", "KeyName"}, posArgs: posTabKeyName},
	"spa-key-combo": {path: []string{"cdp", "spa", "key-combo"}, required: []string{"Tab", "Combo"}, posArgs: posTabCombo},
	"spa-mouse":     {path: []string{"cdp", "spa", "mouse"}, required: []string{"Tab", "X", "Y"}, posArgs: posTabXY},
}

// ---------------------------------------------------------------------------
// wl methods
// ---------------------------------------------------------------------------

var wlMethods = map[string]methodSpec{
	// queries
	"status":     {path: []string{"wl", "status"}},
	"toplevel":   {path: []string{"wl", "toplevel"}},
	"windows":    {path: []string{"wl", "windows"}},
	"geometry":   {path: []string{"wl", "geometry"}, required: []string{"Target"}, posArgs: posTarget},
	"xprop":      {path: []string{"wl", "xprop"}, posArgs: posTargetOptional},
	"atspi":      {path: []string{"wl", "atspi"}, required: []string{"Action"}, posArgs: posAtspi},
	"screenshot": {path: []string{"wl", "screenshot"}, required: []string{"Artifact"}, posArgs: posArtifact, artifact: true},
	"clipboard":  {path: []string{"wl", "clipboard"}, required: []string{"Action"}, posArgs: posClipboard},

	// side-effect actions
	"click":        {path: []string{"wl", "click"}, required: []string{"X", "Y"}, posArgs: posXY},
	"double-click": {path: []string{"wl", "double-click"}, required: []string{"X", "Y"}, posArgs: posXY},
	"mouse":        {path: []string{"wl", "mouse"}, required: []string{"X", "Y"}, posArgs: posXY},
	"scroll":       {path: []string{"wl", "scroll"}, required: []string{"X", "Y", "Direction"}, posArgs: posScroll},
	"drag":         {path: []string{"wl", "drag"}, required: []string{"X", "Y"}, posArgs: posXY}, // simplified — drag takes x1 y1 x2 y2; see Phase 2
	"type":         {path: []string{"wl", "type"}, required: []string{"Text"}, posArgs: posText},
	"key":          {path: []string{"wl", "key"}, required: []string{"KeyName"}, posArgs: posKeyName},
	"key-combo":    {path: []string{"wl", "key-combo"}, required: []string{"Combo"}, posArgs: posCombo},
	"focus":        {path: []string{"wl", "focus"}, required: []string{"Target"}, posArgs: posTarget},
	"close":        {path: []string{"wl", "close"}, required: []string{"Target"}, posArgs: posTarget},
	"fullscreen":   {path: []string{"wl", "fullscreen"}, required: []string{"Target"}, posArgs: posTarget},
	"minimize":     {path: []string{"wl", "minimize"}, required: []string{"Target"}, posArgs: posTarget},
	"exec":         {path: []string{"wl", "exec"}, required: []string{"Command"}, posArgs: posCommand},
	"resolution":   {path: []string{"wl", "resolution"}, required: []string{"Target"}, posArgs: posTarget}, // target here = "WxH"

	// overlay nested
	"overlay-list":   {path: []string{"wl", "overlay", "list"}},
	"overlay-status": {path: []string{"wl", "overlay", "status"}},
	"overlay-show":   {path: []string{"wl", "overlay", "show"}, required: []string{"Text"}, posArgs: posOverlayShow},
	"overlay-hide":   {path: []string{"wl", "overlay", "hide"}, required: []string{"Target"}, posArgs: posTarget},

	// sway nested
	"sway-tree":       {path: []string{"wl", "sway", "tree"}},
	"sway-workspaces": {path: []string{"wl", "sway", "workspaces"}},
	"sway-outputs":    {path: []string{"wl", "sway", "outputs"}},
	"sway-msg":        {path: []string{"wl", "sway", "msg"}, required: []string{"Command"}, posArgs: posCommand},
	"sway-focus":      {path: []string{"wl", "sway", "focus"}, required: []string{"Target"}, posArgs: posTarget},
	"sway-move":       {path: []string{"wl", "sway", "move"}, required: []string{"Target"}, posArgs: posTarget},
	"sway-resize":     {path: []string{"wl", "sway", "resize"}, required: []string{"Target"}, posArgs: posTarget},
	"sway-layout":     {path: []string{"wl", "sway", "layout"}, required: []string{"Target"}, posArgs: posTarget},
	"sway-workspace":  {path: []string{"wl", "sway", "workspace"}, required: []string{"Target"}, posArgs: posTarget},
	"sway-kill":       {path: []string{"wl", "sway", "kill"}},
	"sway-floating":   {path: []string{"wl", "sway", "floating"}},
	"sway-reload":     {path: []string{"wl", "sway", "reload"}},
}

// ---------------------------------------------------------------------------
// dbus methods
// ---------------------------------------------------------------------------

var dbusMethods = map[string]methodSpec{
	"list":       {path: []string{"dbus", "list"}},
	"call":       {path: []string{"dbus", "call"}, required: []string{"Dest", "Path", "Method"}, posArgs: posDbusCall},
	"introspect": {path: []string{"dbus", "introspect"}, required: []string{"Dest", "Path"}, posArgs: posDbusIntrospect},
	"notify":     {path: []string{"dbus", "notify"}, required: []string{"Text"}, posArgs: posDbusNotify},
}

// ---------------------------------------------------------------------------
// vnc methods
// ---------------------------------------------------------------------------

var vncMethods = map[string]methodSpec{
	"status":     {path: []string{"vnc", "status"}},
	"screenshot": {path: []string{"vnc", "screenshot"}, required: []string{"Artifact"}, posArgs: posArtifact, artifact: true},
	"click":      {path: []string{"vnc", "click"}, required: []string{"X", "Y"}, posArgs: posXY},
	"mouse":      {path: []string{"vnc", "mouse"}, required: []string{"X", "Y"}, posArgs: posXY},
	"type":       {path: []string{"vnc", "type"}, required: []string{"Text"}, posArgs: posText},
	"key":        {path: []string{"vnc", "key"}, required: []string{"KeyName"}, posArgs: posKeyName},
	"rfb":        {path: []string{"vnc", "rfb"}, required: []string{"Method"}, posArgs: posCommand}, // Method field reused as rfb method
	"passwd":     {path: []string{"vnc", "passwd"}},
}

// ---------------------------------------------------------------------------
// mcp methods
// ---------------------------------------------------------------------------
//
// The mcp verb dispatches to `ov test mcp <method> <image> …`, which uses
// github.com/modelcontextprotocol/go-sdk to connect to the declared MCP
// server. Methods mirror the SDK's ClientSession surface. See
// mcp.go / mcp_client.go for the host-side implementation.

var mcpMethods = map[string]methodSpec{
	"ping":           {path: []string{"mcp", "ping"}, posArgs: posMcpCommon},
	"servers":        {path: []string{"mcp", "servers"}, posArgs: posMcpCommon},
	"list-tools":     {path: []string{"mcp", "list-tools"}, posArgs: posMcpCommon},
	"list-resources": {path: []string{"mcp", "list-resources"}, posArgs: posMcpCommon},
	"list-prompts":   {path: []string{"mcp", "list-prompts"}, posArgs: posMcpCommon},
	"call":           {path: []string{"mcp", "call"}, required: []string{"Tool"}, posArgs: posMcpCall},
	"read":           {path: []string{"mcp", "read"}, required: []string{"URI"}, posArgs: posMcpRead},
}

// ---------------------------------------------------------------------------
// positional-arg builders — reused across verbs.
// Each returns the positional args to insert AFTER the image name,
// BEFORE any -i instance flag. They never fail: required-modifier checks
// run before this point.
// ---------------------------------------------------------------------------

func posTab(c *Check) []string               { return []string{c.Tab} }
func posURL(c *Check) []string               { return []string{c.URL} }
func posText(c *Check) []string              { return []string{c.Text} }
func posKeyName(c *Check) []string           { return []string{c.KeyName} }
func posCombo(c *Check) []string             { return []string{c.Combo} }
func posTarget(c *Check) []string            { return []string{c.Target} }
func posCommand(c *Check) []string           { return []string{c.Command} }
func posArtifact(c *Check) []string          { return []string{c.Artifact} }
func posTabExpression(c *Check) []string     { return []string{c.Tab, c.Expression} }
func posTabSelector(c *Check) []string       { return []string{c.Tab, c.Selector} }
func posTabSelectorText(c *Check) []string   { return []string{c.Tab, c.Selector, c.Text} }
func posTabQuery(c *Check) []string {
	if c.Query == "" {
		return []string{c.Tab}
	}
	return []string{c.Tab, c.Query}
}
func posTabText(c *Check) []string    { return []string{c.Tab, c.Text} }
func posTabKeyName(c *Check) []string { return []string{c.Tab, c.KeyName} }
func posTabCombo(c *Check) []string   { return []string{c.Tab, c.Combo} }
func posTabXY(c *Check) []string {
	return []string{c.Tab, strconv.Itoa(c.X), strconv.Itoa(c.Y)}
}
func posTabArtifact(c *Check) []string { return []string{c.Tab, c.Artifact} }
func posCdpRaw(c *Check) []string {
	args := []string{c.Tab, c.Method}
	if c.RequestBody != "" {
		args = append(args, c.RequestBody)
	}
	return args
}
func posXY(c *Check) []string {
	return []string{strconv.Itoa(c.X), strconv.Itoa(c.Y)}
}
func posScroll(c *Check) []string {
	amount := c.Amount
	if amount == 0 {
		amount = 1
	}
	return []string{strconv.Itoa(c.X), strconv.Itoa(c.Y), c.Direction, strconv.Itoa(amount)}
}
func posAtspi(c *Check) []string {
	args := []string{c.Action}
	if c.Query != "" {
		args = append(args, c.Query)
	}
	return args
}
func posClipboard(c *Check) []string {
	args := []string{c.Action}
	if c.Action == "set" && c.Text != "" {
		args = append(args, c.Text)
	}
	return args
}
func posTargetOptional(c *Check) []string {
	if c.Target == "" {
		return nil
	}
	return []string{c.Target}
}
func posOverlayShow(c *Check) []string {
	// Minimal overlay-show: --type text --text <text> [--name <target>]
	args := []string{"--type", "text", "--text", c.Text}
	if c.Target != "" {
		args = append(args, "--name", c.Target)
	}
	return args
}
func posDbusCall(c *Check) []string {
	args := []string{c.Dest, c.Path, c.Method}
	args = append(args, c.Args...)
	return args
}
func posDbusIntrospect(c *Check) []string { return []string{c.Dest, c.Path} }
func posDbusNotify(c *Check) []string {
	args := []string{c.Text} // text = title
	// c.Body MatcherList is reserved for assertion; for the actual body arg,
	// callers can use c.Description as an authoring convention, or omit for
	// a title-only notification.
	if c.Description != "" {
		args = append(args, c.Description)
	}
	return args
}

// mcp positional builders. Any `--name` flag piggybacks on the positional
// slice — Kong accepts flags in any position, so returning them alongside
// positionals avoids extending methodSpec with a dedicated flag hook.

func posMcpCommon(c *Check) []string {
	if c.McpName == "" {
		return nil
	}
	return []string{"--name", c.McpName}
}

func posMcpCall(c *Check) []string {
	args := []string{c.Tool}
	if c.Input != "" {
		args = append(args, c.Input)
	}
	if c.McpName != "" {
		args = append(args, "--name", c.McpName)
	}
	return args
}

func posMcpRead(c *Check) []string {
	args := []string{c.URI}
	if c.McpName != "" {
		args = append(args, "--name", c.McpName)
	}
	return args
}

// ---------------------------------------------------------------------------
// Verb dispatchers
// ---------------------------------------------------------------------------

func (r *Runner) runCdp(ctx context.Context, c *Check) TestResult {
	return r.runOvVerb(ctx, c, "cdp", c.Cdp, cdpMethods)
}

func (r *Runner) runWl(ctx context.Context, c *Check) TestResult {
	return r.runOvVerb(ctx, c, "wl", c.Wl, wlMethods)
}

func (r *Runner) runDbus(ctx context.Context, c *Check) TestResult {
	return r.runOvVerb(ctx, c, "dbus", c.Dbus, dbusMethods)
}

func (r *Runner) runVnc(ctx context.Context, c *Check) TestResult {
	return r.runOvVerb(ctx, c, "vnc", c.Vnc, vncMethods)
}

func (r *Runner) runMcp(ctx context.Context, c *Check) TestResult {
	return r.runOvVerb(ctx, c, "mcp", c.Mcp, mcpMethods)
}

// runOvVerb is the shared dispatch path: skip checks, method lookup,
// argv building, subprocess exec, matcher pipeline, optional artifact size
// assertion. Returns the TestResult directly.
func (r *Runner) runOvVerb(ctx context.Context, c *Check, verb, method string, allowlist map[string]methodSpec) TestResult {
	if r.Mode == RunModeImageTest {
		return skipf(c, fmt.Sprintf("%s: %s requires a running container (skip under ov image test)", verb, method))
	}
	if r.Image == "" {
		return skipf(c, fmt.Sprintf("%s: %s runner has no image context (should not happen under ov test)", verb, method))
	}

	spec, ok := allowlist[method]
	if !ok {
		return failf(c, "%s: unknown method %q (see /ov:test for the allowlist)", verb, method)
	}

	// Required-modifier check mirrors validate_tests.go but guards against
	// runs where validation was bypassed (e.g. tests loaded directly from a
	// label without re-validating).
	if err := checkRequiredFields(c, spec.required); err != nil {
		return failf(c, "%s: %s: %v", verb, method, err)
	}

	// Build argv: ["test"] + spec.path + [image] + spec.posArgs(c) + ["-i", instance]
	argv := append([]string{"test"}, spec.path...)
	argv = append(argv, r.Image)
	if spec.posArgs != nil {
		argv = append(argv, spec.posArgs(c)...)
	}
	if r.Instance != "" {
		argv = append(argv, "-i", r.Instance)
	}

	ovBinary, err := findOvBinary()
	if err != nil {
		return failf(c, "%s: %s: %v", verb, method, err)
	}
	cmd := exec.CommandContext(ctx, ovBinary, argv...)
	stdout, stderr, exit, execErr := runCapture(cmd)
	if execErr != nil {
		return failf(c, "%s: %s: execution error: %v", verb, method, execErr)
	}

	wantExit := 0
	if c.ExitStatus != nil {
		wantExit = *c.ExitStatus
	}
	if exit != wantExit {
		return failf(c, "%s: %s: exit=%d, want %d (stderr: %s)", verb, method, exit, wantExit, trimPreview(stderr))
	}

	if err := matchAll(stdout, c.Stdout); err != nil {
		return failf(c, "%s: %s: stdout: %v (got: %s)", verb, method, err, trimPreview(stdout))
	}
	if err := matchAll(stderr, c.Stderr); err != nil {
		return failf(c, "%s: %s: stderr: %v (got: %s)", verb, method, err, trimPreview(stderr))
	}

	if spec.artifact && c.ArtifactMinBytes > 0 {
		info, err := os.Stat(c.Artifact)
		if err != nil {
			return failf(c, "%s: %s: artifact %q not found after run: %v", verb, method, c.Artifact, err)
		}
		if info.Size() < int64(c.ArtifactMinBytes) {
			return failf(c, "%s: %s: artifact %q size %d < required min_bytes %d",
				verb, method, c.Artifact, info.Size(), c.ArtifactMinBytes)
		}
	}

	return passf(c, fmt.Sprintf("%s %s: exit=%d", verb, method, exit))
}

// checkRequiredFields returns an error naming any required field that is
// zero-valued on the Check. Mirrors the validate_tests.go precondition so
// runtime-only callers (e.g. tests loaded from an OCI label into an
// un-validated runner) still surface authoring errors rather than silent
// wrong behavior.
func checkRequiredFields(c *Check, required []string) error {
	var missing []string
	for _, f := range required {
		if isZeroField(c, f) {
			missing = append(missing, strings.ToLower(f))
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("missing required modifier(s): %s", strings.Join(missing, ", "))
}

// isZeroField checks whether the named Check field is at its zero value.
// Enumerates the fields the new verbs use — grep-able at the allowlist site
// so adding a new modifier means adding a case here too.
func isZeroField(c *Check, name string) bool {
	switch name {
	case "Tab":
		return c.Tab == ""
	case "Expression":
		return c.Expression == ""
	case "URL":
		return c.URL == ""
	case "Selector":
		return c.Selector == ""
	case "Dest":
		return c.Dest == ""
	case "Path":
		return c.Path == ""
	case "Method":
		return c.Method == ""
	case "Artifact":
		return c.Artifact == ""
	case "X":
		return c.X == 0
	case "Y":
		return c.Y == 0
	case "Button":
		return c.Button == ""
	case "Text":
		return c.Text == ""
	case "KeyName":
		return c.KeyName == ""
	case "Combo":
		return c.Combo == ""
	case "Direction":
		return c.Direction == ""
	case "Amount":
		return c.Amount == 0
	case "Target":
		return c.Target == ""
	case "Action":
		return c.Action == ""
	case "Query":
		return c.Query == ""
	case "Command":
		return c.Command == ""
	case "Tool":
		return c.Tool == ""
	case "URI":
		return c.URI == ""
	case "Input":
		return c.Input == ""
	case "McpName":
		return c.McpName == ""
	}
	// Unknown field name is a programming error: treat as "not zero" so
	// authoring errors surface elsewhere instead of spurious skips.
	return false
}

// findOvBinary returns the absolute path to the `ov` binary the test runner
// should spawn. Prefers /proc/self/exe (the currently-running binary so tests
// invoke the same build that collected them), falling back to $PATH lookup.
// Testability var for mocks.
var findOvBinary = defaultFindOvBinary

func defaultFindOvBinary() (string, error) {
	if p, err := os.Executable(); err == nil && p != "" {
		return p, nil
	}
	return exec.LookPath("ov")
}
