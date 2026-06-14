package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg" // register JPEG decoder for image.DecodeConfig / image.Decode
	_ "image/png"  // register PNG decoder for image.DecodeConfig / image.Decode
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// artifactValidatableMethods lists the verb/method pairs that
// `validate_ai_artifacts: true` swaps to AI-artifact validation. ALL
// OTHER methods always re-run via the harness's own subprocess — the
// harness is authoritative for non-state-dependent probes (status,
// checkuation, listing, info, file/process/package/port/service/etc.).
//
// The justification for each entry is "re-running this probe a few
// seconds later against the same logically-correct state can yield
// different bytes" — chrome paints at vsync, wayland frame timing,
// VNC/RFB framebuffer at re-capture moment, libvirt/SPICE display
// surfaces, terminal recordings (asciinema cast files become final
// once `record stop` finalizes them).
//
// Anti-deception properties around this allowlist:
//
//   - The set of `spec.artifact == true` methods must be the SAME as
//     this allowlist. Drift is caught at compile/test time by
//     TestArtifactValidatableMethods_MatchesArtifactProducingMethodSpecs.
//
//   - When validate_ai_artifacts is true AND the method is in this
//     allowlist, runCharlyVerb skips subprocess execution and runs the
//     post-run validators (artifact_min_bytes / artifact_min_dimensions
//     / artifact_not_uniform / artifact_min_cast_events) against the
//     existing file at the plan-declared `artifact:` path.
//
//   - The freshness mtime gate (artifact mtime ≥ Runner.IterStartTime)
//     prevents pre-staged or stale files from passing.
var artifactValidatableMethods = map[string]bool{
	"cdp/screenshot":     true,
	"wl/screenshot":      true,
	"vnc/screenshot":     true,
	"libvirt/screenshot": true,
	"spice/screenshot":   true,
	"spice/cursor":       true,
	"record/stop":        true,
}

// checkrun_charly_verbs.go implements the cdp/wl/dbus/vnc test verbs. Each verb
// is a thin wrapper around the corresponding `charly check <verb> <method>` CLI
// path — the test framework spawns a subprocess for each check, captures
// stdout/stderr/exit, and feeds the output through the existing matcher
// pipeline (Stdout/Stderr/ExitStatus + artifact size via ArtifactMinBytes).
//
// Architectural notes:
//   - Host-side only: the test runner invokes the host `charly` binary, which
//     internally connects to the container (CDP over TCP, WL via exec,
//     D-Bus via delegation, VNC over TCP). No container-side test runner.
//   - RunModeBox short-circuits with a skip: these verbs need a live
//     container with port mappings, which a disposable `podman run --rm`
//     container doesn't expose the same way.
//   - Method allowlists are hand-enumerated here so authoring errors surface
//     at `charly box validate` time, not at test-run time. Drift between the
//     CLI and the allowlist is a documentation issue — see /charly-internals:go for
//     the maintenance rule.

// methodSpec describes one method within a verb group.
//
//	path     — CLI subcommand path after "check", e.g. ["cdp", "check"] or
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
	posArgs  func(c *Op) []string
	artifact bool
	// skipBox = true means the verb operates against a cluster or other
	// non-image target, so the usual image/deploy-name positional must NOT
	// be appended between the method path and posArgs. Used by k8s verbs.
	skipBox bool
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
	"check":      {path: []string{"cdp", "check"}, required: []string{"Tab", "Expression"}, posArgs: posTabExpression},
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
	"drag":         {path: []string{"wl", "drag"}, required: []string{"X", "Y", "X2", "Y2"}, posArgs: posXYXY}, // start (X,Y) → end (X2,Y2)
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
// The mcp verb dispatches to `charly check mcp <method> <image> …`, which uses
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
// record methods — `charly check record <method>` drives in-container recording
// sessions (asciinema terminal / pixelflux-record / wf-recorder desktop).
// Container-only: resolveContainer does not know about VMs, so a `record:`
// check on a `vm:<name>` deploy will fail at subprocess dispatch. Documented
// in /charly:record; validator does not pre-filter by deploy kind.
// ---------------------------------------------------------------------------

var recordMethods = map[string]methodSpec{
	"list":  {path: []string{"record", "list"}},
	"start": {path: []string{"record", "start"}, posArgs: posRecordStart},
	// stop's artifact: true asserts the recording was copied out AND (when
	// ArtifactMinBytes is set) that the file is at least N bytes — a strong
	// "the recorder actually produced output" invariant.
	"stop": {path: []string{"record", "stop"}, required: []string{"Artifact"}, posArgs: posRecordStop, artifact: true},
	// `record: cmd` sends a text line into the recording's tmux session.
	// Text (not Command) is used because Command is itself a verb
	// discriminator — setting both would trip the Kind() uniqueness check.
	"cmd": {path: []string{"record", "cmd"}, required: []string{"Text"}, posArgs: posRecordCmd},
}

// ---------------------------------------------------------------------------
// spice methods — `charly check spice <method>` speaks the SPICE wire protocol
// (github.com/Shells-com/spice) to a running VM's SPICE port. Host-side;
// only applicable to `vm:<name>` deploys that expose SPICE graphics.
// ---------------------------------------------------------------------------

var spiceMethods = map[string]methodSpec{
	"status":     {path: []string{"spice", "status"}},
	"screenshot": {path: []string{"spice", "screenshot"}, posArgs: posArtifact, artifact: true},
	"cursor":     {path: []string{"spice", "cursor"}, posArgs: posArtifact, artifact: true},
	"click":      {path: []string{"spice", "click"}, posArgs: posXY},
	"mouse":      {path: []string{"spice", "mouse"}, posArgs: posXY},
	"type":       {path: []string{"spice", "type"}, required: []string{"Text"}, posArgs: posText},
	"key":        {path: []string{"spice", "key"}, required: []string{"KeyName"}, posArgs: posKeyName},
}

// ---------------------------------------------------------------------------
// libvirt methods — `charly check libvirt <method>` uses go-libvirt RPC against
// a running VM. Host-side; only applicable to `vm:<name>` deploys. Nested
// subgroups (guest/*, snapshot/*) are flattened via slash-separated method
// names so authors write `libvirt: guest/ping` or `libvirt: snapshot/list`.
// ---------------------------------------------------------------------------

var libvirtMethods = map[string]methodSpec{
	// Top-level verbs
	"list":       {path: []string{"libvirt", "list"}},
	"info":       {path: []string{"libvirt", "info"}},
	"screenshot": {path: []string{"libvirt", "screenshot"}, posArgs: posArtifact, artifact: true},
	"send-key":   {path: []string{"libvirt", "send-key"}, required: []string{"KeyName"}, posArgs: posKeyNameSplit},
	"passwd":     {path: []string{"libvirt", "passwd"}, required: []string{"Text"}, posArgs: posText},
	// qmp takes a QMP method name + optional JSON args. Text holds the
	// method name (Command would collide with the command: verb).
	"qmp":        {path: []string{"libvirt", "qmp"}, required: []string{"Text"}, posArgs: posLibvirtQmp},
	"domain-xml": {path: []string{"libvirt", "domain-xml"}},
	"console":    {path: []string{"libvirt", "console"}},
	"events":     {path: []string{"libvirt", "events"}},

	// qemu-guest-agent subgroup
	"guest/ping":       {path: []string{"libvirt", "guest", "ping"}},
	"guest/info":       {path: []string{"libvirt", "guest", "info"}},
	"guest/os-info":    {path: []string{"libvirt", "guest", "os-info"}},
	"guest/time":       {path: []string{"libvirt", "guest", "time"}},
	"guest/hostname":   {path: []string{"libvirt", "guest", "hostname"}},
	"guest/users":      {path: []string{"libvirt", "guest", "users"}},
	"guest/interfaces": {path: []string{"libvirt", "guest", "interfaces"}},
	"guest/disks":      {path: []string{"libvirt", "guest", "disks"}},
	"guest/fsinfo":     {path: []string{"libvirt", "guest", "fsinfo"}},
	"guest/vcpus":      {path: []string{"libvirt", "guest", "vcpus"}},
	// guest/exec runs a command via qemu-guest-agent inside the VM. Reuses the
	// `command:` field as a sub-modifier (verbsSet treats it as a modifier when
	// libvirt: is set). The string is split on whitespace into argv (no shell
	// metacharacter handling — guest-exec wants a real argv list).
	"guest/exec":   {path: []string{"libvirt", "guest", "exec"}, required: []string{"Command"}, posArgs: posCommandFields},
	"guest/fstrim": {path: []string{"libvirt", "guest", "fstrim"}},

	// Snapshot subgroup — Target holds the snapshot name.
	"snapshot/list":   {path: []string{"libvirt", "snapshot", "list"}},
	"snapshot/create": {path: []string{"libvirt", "snapshot", "create"}, required: []string{"Target"}, posArgs: posTarget},
	"snapshot/info":   {path: []string{"libvirt", "snapshot", "info"}, required: []string{"Target"}, posArgs: posTarget},
	"snapshot/revert": {path: []string{"libvirt", "snapshot", "revert"}, required: []string{"Target"}, posArgs: posTarget},
	"snapshot/delete": {path: []string{"libvirt", "snapshot", "delete"}, required: []string{"Target"}, posArgs: posTarget},
}

// ---------------------------------------------------------------------------
// k8s methods — `charly check k8s <method>` probes a Kubernetes cluster via the
// vendored client-go SDK. Cluster selection is via --cluster <profile> /
// --context / --kubeconfig (see cmd_test_k8s.go). Host-side; applicable to
// any deploy whose post-provision registered a ClusterProfile (typically
// a k3s-server candy).
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// adb methods — `charly check adb <method>` speaks the ADB wire protocol to the
// container's host-mapped adb-server port (5037 → host's HOST_PORT:5037).
// Deploy-scope only; the runner shells out to `charly check adb <method> <image>
// [args]` via runCharlyVerb. Implementation lives in charly/adb.go.
// ---------------------------------------------------------------------------

var adbMethods = map[string]methodSpec{
	"devices":         {path: []string{"adb", "devices"}},
	"shell":           {path: []string{"adb", "shell"}, required: []string{"Args"}, posArgs: posShellArgs},
	"install":         {path: []string{"adb", "install"}, required: []string{"Apk"}, posArgs: posApkFlag},
	"install-app":     {path: []string{"adb", "install-app"}, required: []string{"AppId"}, posArgs: posInstallApp},
	"uninstall":       {path: []string{"adb", "uninstall"}, required: []string{"Args"}, posArgs: posPackageArg},
	"getprop":         {path: []string{"adb", "getprop"}, required: []string{"Property"}, posArgs: posPropertyArg},
	"screencap":       {path: []string{"adb", "screencap"}, required: []string{"Artifact"}, posArgs: posArtifactFlag, artifact: true},
	"logcat-tail":     {path: []string{"adb", "logcat-tail"}, posArgs: posLogcatTail},
	"wait-for-device": {path: []string{"adb", "wait-for-device"}, posArgs: posWaitForDevice},
	"wait-ui-settled": {path: []string{"adb", "wait-ui-settled"}, posArgs: posWaitForDevice},
	"current-focus":   {path: []string{"adb", "current-focus"}},
	"keyevent":        {path: []string{"adb", "keyevent"}, required: []string{"KeyName"}, posArgs: posKeyName},
}

// ---------------------------------------------------------------------------
// appium methods — `charly check appium <method>` drives Appium WebDriver via the
// tebeka/selenium SDK against the container's host-mapped 4723 port. Session
// lifecycle (create / delete) persists to ~/.cache/charly/appium/sessions/
// <image>[_<instance>].json so multi-step tests share a session efficiently.
// Implementation in charly/appium.go + charly/appium_session.go.
// ---------------------------------------------------------------------------

var appiumMethods = map[string]methodSpec{
	// lifecycle + element basics (existing)
	"status":         {path: []string{"appium", "status"}},
	"session-create": {path: []string{"appium", "session-create"}, required: []string{"Caps"}, posArgs: posCapsFlag},
	"session-delete": {path: []string{"appium", "session-delete"}},
	"install-app":    {path: []string{"appium", "install-app"}, required: []string{"Apk"}, posArgs: posApkFlag},
	"find":           {path: []string{"appium", "find"}, required: []string{"Selector"}, posArgs: posSelectorStrategy},
	"click":          {path: []string{"appium", "click"}, required: []string{"Selector"}, posArgs: posSelectorStrategy},
	"send-keys":      {path: []string{"appium", "send-keys"}, required: []string{"Selector", "Text"}, posArgs: posSelectorTextStrategy},
	"screenshot":     {path: []string{"appium", "screenshot"}, required: []string{"Artifact"}, posArgs: posArtifactFlag, artifact: true},

	// Tier 1 — typed element introspection / navigation
	"get-text":      {path: []string{"appium", "get-text"}, required: []string{"Selector"}, posArgs: posSelectorStrategy},
	"get-attribute": {path: []string{"appium", "get-attribute"}, required: []string{"Selector", "Attribute"}, posArgs: posSelectorAttribute},
	"clear":         {path: []string{"appium", "clear"}, required: []string{"Selector"}, posArgs: posSelectorStrategy},
	"find-all":      {path: []string{"appium", "find-all"}, required: []string{"Selector"}, posArgs: posSelectorStrategy},
	"source":        {path: []string{"appium", "source"}, posArgs: posSessionOnly},
	"back":          {path: []string{"appium", "back"}, posArgs: posSessionOnly},

	// Tier 2 — gesture group (wl sway-style flat names → `charly check appium gesture <op>`)
	"gesture-tap":         {path: []string{"appium", "gesture", "tap"}, posArgs: posElemOrXY},
	"gesture-double-tap":  {path: []string{"appium", "gesture", "double-tap"}, posArgs: posElemOrXY},
	"gesture-long-press":  {path: []string{"appium", "gesture", "long-press"}, posArgs: posElemOrXY},
	"gesture-drag":        {path: []string{"appium", "gesture", "drag"}, posArgs: posElemOrXY},
	"gesture-swipe":       {path: []string{"appium", "gesture", "swipe"}, required: []string{"Direction"}, posArgs: posGesture},
	"gesture-scroll":      {path: []string{"appium", "gesture", "scroll"}, required: []string{"Direction"}, posArgs: posGesture},
	"gesture-fling":       {path: []string{"appium", "gesture", "fling"}, required: []string{"Direction"}, posArgs: posGesture},
	"gesture-pinch-open":  {path: []string{"appium", "gesture", "pinch-open"}, posArgs: posGesture},
	"gesture-pinch-close": {path: []string{"appium", "gesture", "pinch-close"}, posArgs: posGesture},

	// Tier 2 — app lifecycle + activity group
	"app-start-activity":   {path: []string{"appium", "app", "start-activity"}, required: []string{"Activity"}, posArgs: posActivity},
	"app-activate":         {path: []string{"appium", "app", "activate"}, required: []string{"AppId"}, posArgs: posAppId},
	"app-terminate":        {path: []string{"appium", "app", "terminate"}, required: []string{"AppId"}, posArgs: posAppId},
	"app-remove":           {path: []string{"appium", "app", "remove"}, required: []string{"AppId"}, posArgs: posAppId},
	"app-clear":            {path: []string{"appium", "app", "clear"}, required: []string{"AppId"}, posArgs: posAppId},
	"app-is-installed":     {path: []string{"appium", "app", "is-installed"}, required: []string{"AppId"}, posArgs: posAppId},
	"app-state":            {path: []string{"appium", "app", "state"}, required: []string{"AppId"}, posArgs: posAppId},
	"app-current-activity": {path: []string{"appium", "app", "current-activity"}, posArgs: posSessionOnly},
	"app-current-package":  {path: []string{"appium", "app", "current-package"}, posArgs: posSessionOnly},

	// Tier 2 — keys + keyboard group
	"key-press": {path: []string{"appium", "key", "press"}, required: []string{"Keycode"}, posArgs: posKeycode},
	"key-hide":  {path: []string{"appium", "key", "hide"}, posArgs: posSessionOnly},
	"key-shown": {path: []string{"appium", "key", "shown"}, posArgs: posSessionOnly},

	// Tier 2 — device / system + WebView context group
	"device-info":            {path: []string{"appium", "device", "info"}, posArgs: posSessionOnly},
	"device-battery":         {path: []string{"appium", "device", "battery"}, posArgs: posSessionOnly},
	"device-time":            {path: []string{"appium", "device", "time"}, posArgs: posSessionOnly},
	"device-orientation":     {path: []string{"appium", "device", "orientation"}, posArgs: posParamsOnly},
	"device-set-orientation": {path: []string{"appium", "device", "set-orientation"}, required: []string{"Params"}, posArgs: posParamsOnly},
	"device-notifications":   {path: []string{"appium", "device", "notifications"}, posArgs: posSessionOnly},
	"device-get-clipboard":   {path: []string{"appium", "device", "get-clipboard"}, posArgs: posSessionOnly},
	"device-set-clipboard":   {path: []string{"appium", "device", "set-clipboard"}, required: []string{"Params"}, posArgs: posParamsOnly},
	"device-contexts":        {path: []string{"appium", "device", "contexts"}, posArgs: posSessionOnly},
	"device-context":         {path: []string{"appium", "device", "context"}, posArgs: posParamsOnly},

	// Tier 3 — generic escape hatch (cdp raw / check equivalents)
	"execute": {path: []string{"appium", "execute"}, required: []string{"Expression"}, posArgs: posAppiumExecute},
	"raw":     {path: []string{"appium", "raw"}, required: []string{"Method", "Path"}, posArgs: posAppiumRaw},
}

// k8s methods all run against a cluster, not an image/container, so
// skipBox=true across the board.
var k8sMethods = map[string]methodSpec{
	"nodes":          {path: []string{"k8s", "nodes"}, posArgs: posK8sCluster, skipBox: true},
	"wait-nodes":     {path: []string{"k8s", "wait-nodes"}, posArgs: posK8sWaitNodes, skipBox: true},
	"pods":           {path: []string{"k8s", "pods"}, posArgs: posK8sPods, skipBox: true},
	"wait-ready":     {path: []string{"k8s", "wait-ready"}, required: []string{"Kind", "Name"}, posArgs: posK8sWaitReady, skipBox: true},
	"ingress":        {path: []string{"k8s", "ingress"}, posArgs: posK8sNamespaceOpt, skipBox: true},
	"ingressclass":   {path: []string{"k8s", "ingressclass"}, posArgs: posK8sCluster, skipBox: true},
	"storageclass":   {path: []string{"k8s", "storageclass"}, posArgs: posK8sCluster, skipBox: true},
	"service":        {path: []string{"k8s", "service"}, posArgs: posK8sNamespaceOpt, skipBox: true},
	"lb-external-ip": {path: []string{"k8s", "lb-external-ip"}, required: []string{"Namespace", "Name"}, posArgs: posK8sLbExternal, skipBox: true},
	"addons":         {path: []string{"k8s", "addons"}, posArgs: posK8sAddons, skipBox: true},
	"apply":          {path: []string{"k8s", "apply"}, required: []string{"Manifest"}, posArgs: posK8sApply, skipBox: true},
	"delete":         {path: []string{"k8s", "delete"}, required: []string{"Manifest"}, posArgs: posK8sApply, skipBox: true},
	"raw":            {path: []string{"k8s", "raw"}, required: []string{"Resource"}, posArgs: posK8sRaw, skipBox: true},
}

// ---------------------------------------------------------------------------
// k8s positional-arg builders — every method emits --cluster/--context/
// --kubeconfig + its method-specific flags. Because k8s probes are run
// against a cluster (not a container/image), the --image positional from
// runCharlyVerb is still passed, but `charly check k8s ...` ignores it by accepting
// arbitrary trailing args under Kong's default catch-all policy.
// ---------------------------------------------------------------------------

// posK8sCluster emits only the shared cluster-selection flags. Used by
// methods that take no other parameters (nodes, ingressclass, storageclass).
func posK8sCluster(c *Op) []string {
	return k8sClusterArgs(c)
}

func posK8sWaitNodes(c *Op) []string {
	args := k8sClusterArgs(c)
	if c.K8sCount > 0 {
		args = append(args, "--count", strconv.Itoa(c.K8sCount))
	}
	if c.Name != "" {
		args = append(args, "--name", c.Name)
	}
	if c.Timeout != "" {
		args = append(args, "--timeout", c.Timeout)
	}
	return args
}

func posK8sPods(c *Op) []string {
	args := k8sClusterArgs(c)
	if c.Namespace != "" {
		args = append(args, "--namespace", c.Namespace)
	}
	if c.Label != "" {
		args = append(args, "--label", c.Label)
	}
	return args
}

func posK8sWaitReady(c *Op) []string {
	args := k8sClusterArgs(c)
	args = append(args, "--kind", c.K8sKind, "--name", c.Name)
	if c.Namespace != "" {
		args = append(args, "--namespace", c.Namespace)
	}
	if c.Timeout != "" {
		args = append(args, "--timeout", c.Timeout)
	}
	return args
}

func posK8sNamespaceOpt(c *Op) []string {
	args := k8sClusterArgs(c)
	if c.Namespace != "" {
		args = append(args, "--namespace", c.Namespace)
	}
	return args
}

func posK8sLbExternal(c *Op) []string {
	args := k8sClusterArgs(c)
	args = append(args, "--namespace", c.Namespace, "--name", c.Name)
	if c.Timeout != "" {
		args = append(args, "--timeout", c.Timeout)
	}
	return args
}

func posK8sAddons(c *Op) []string {
	args := k8sClusterArgs(c)
	if c.Namespace != "" {
		args = append(args, "--namespace", c.Namespace)
	}
	if c.Timeout != "" {
		args = append(args, "--timeout", c.Timeout)
	}
	return args
}

func posK8sApply(c *Op) []string {
	args := k8sClusterArgs(c)
	args = append(args, "--file", c.Manifest)
	if c.Namespace != "" {
		args = append(args, "--namespace", c.Namespace)
	}
	return args
}

func posK8sRaw(c *Op) []string {
	args := k8sClusterArgs(c)
	args = append(args, "--resource", c.K8sResource)
	if c.K8sGroup != "" {
		args = append(args, "--group", c.K8sGroup)
	}
	if c.K8sVersion != "" {
		args = append(args, "--version", c.K8sVersion)
	}
	if c.Name != "" {
		args = append(args, "--name", c.Name)
	}
	if c.Namespace != "" {
		args = append(args, "--namespace", c.Namespace)
	}
	if c.JSON {
		// A check's `json: true` → `--json` flag on the underlying
		// `charly check k8s raw` invocation. List-mode then emits the
		// full Kubernetes List JSON document instead of one
		// `<namespace>/<name>` per line.
		args = append(args, "--json")
	}
	return args
}

// k8sClusterArgs renders the shared --cluster / --context / --kubeconfig
// selection flags from the Check.
func k8sClusterArgs(c *Op) []string {
	var args []string
	if c.Cluster != "" {
		args = append(args, "--cluster", c.Cluster)
	}
	if c.K8sContext != "" {
		args = append(args, "--context", c.K8sContext)
	}
	if c.Kubeconfig != "" {
		args = append(args, "--kubeconfig", c.Kubeconfig)
	}
	return args
}

// ---------------------------------------------------------------------------
// positional-arg builders — reused across verbs.
// Each returns the positional args to insert AFTER the image name,
// BEFORE any -i instance flag. They never fail: required-modifier checks
// run before this point.
// ---------------------------------------------------------------------------

func posTab(c *Op) []string             { return []string{c.Tab} }
func posURL(c *Op) []string             { return []string{c.URL} }
func posText(c *Op) []string            { return []string{c.Text} }
func posKeyName(c *Op) []string         { return []string{c.KeyName} }
func posCombo(c *Op) []string           { return []string{c.Combo} }
func posTarget(c *Op) []string          { return []string{c.Target} }
func posCommand(c *Op) []string         { return []string{c.Command} }
func posArtifact(c *Op) []string        { return []string{c.Artifact} }
func posTabExpression(c *Op) []string   { return []string{c.Tab, c.Expression} }
func posTabSelector(c *Op) []string     { return []string{c.Tab, c.Selector} }
func posTabSelectorText(c *Op) []string { return []string{c.Tab, c.Selector, c.Text} }
func posTabQuery(c *Op) []string {
	if c.Query == "" {
		return []string{c.Tab}
	}
	return []string{c.Tab, c.Query}
}
func posTabText(c *Op) []string    { return []string{c.Tab, c.Text} }
func posTabKeyName(c *Op) []string { return []string{c.Tab, c.KeyName} }
func posTabCombo(c *Op) []string   { return []string{c.Tab, c.Combo} }
func posTabXY(c *Op) []string {
	return []string{c.Tab, strconv.Itoa(c.X), strconv.Itoa(c.Y)}
}
func posTabArtifact(c *Op) []string { return []string{c.Tab, c.Artifact} }
func posCdpRaw(c *Op) []string {
	args := []string{c.Tab, c.Method}
	if c.RequestBody != "" {
		args = append(args, c.RequestBody)
	}
	return args
}
func posXY(c *Op) []string {
	return []string{strconv.Itoa(c.X), strconv.Itoa(c.Y)}
}

// posXYXY emits four positionals (start + end) for verbs whose CLI
// signature is `<image> <x1> <y1> <x2> <y2>` — e.g. `wl drag`.
// Reuses X/Y as the start and X2/Y2 as the end so click/drag share
// the X/Y idiom for the start point.
func posXYXY(c *Op) []string {
	return []string{strconv.Itoa(c.X), strconv.Itoa(c.Y), strconv.Itoa(c.X2), strconv.Itoa(c.Y2)}
}
func posScroll(c *Op) []string {
	amount := c.Amount
	if amount == 0 {
		amount = 1
	}
	return []string{strconv.Itoa(c.X), strconv.Itoa(c.Y), c.Direction, strconv.Itoa(amount)}
}
func posAtspi(c *Op) []string {
	args := []string{c.Action}
	if c.Query != "" {
		args = append(args, c.Query)
	}
	return args
}
func posClipboard(c *Op) []string {
	args := []string{c.Action}
	if c.Action == "set" && c.Text != "" {
		args = append(args, c.Text)
	}
	return args
}
func posTargetOptional(c *Op) []string {
	if c.Target == "" {
		return nil
	}
	return []string{c.Target}
}
func posOverlayShow(c *Op) []string {
	// Minimal overlay-show: --type text --text <text> [--name <target>]
	args := []string{"--type", "text", "--text", c.Text}
	if c.Target != "" {
		args = append(args, "--name", c.Target)
	}
	return args
}
func posDbusCall(c *Op) []string {
	args := make([]string, 0, 3+len(c.Args))
	args = append(args, c.Dest, c.Path, c.Method)
	args = append(args, c.Args...)
	return args
}
func posDbusIntrospect(c *Op) []string { return []string{c.Dest, c.Path} }
func posDbusNotify(c *Op) []string {
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

func posMcpCommon(c *Op) []string {
	if c.McpName == "" {
		return nil
	}
	return []string{"--name", c.McpName}
}

func posMcpCall(c *Op) []string {
	args := []string{c.Tool}
	if c.Input != "" {
		args = append(args, c.Input)
	}
	if c.McpName != "" {
		args = append(args, "--name", c.McpName)
	}
	return args
}

func posMcpRead(c *Op) []string {
	args := []string{c.URI}
	if c.McpName != "" {
		args = append(args, "--name", c.McpName)
	}
	return args
}

// record positional builders. The subprocess already defaults -n to "default"
// when RecordName is empty, so omit the flag in that case.
func posRecordStart(c *Op) []string {
	var args []string
	if c.RecordName != "" {
		args = append(args, "-n", c.RecordName)
	}
	if c.RecordMode != "" {
		args = append(args, "-m", c.RecordMode)
	}
	if c.RecordFps > 0 {
		args = append(args, "--fps", strconv.Itoa(c.RecordFps))
	}
	if c.RecordAudio {
		args = append(args, "--audio")
	}
	return args
}

func posRecordStop(c *Op) []string {
	var args []string
	if c.RecordName != "" {
		args = append(args, "-n", c.RecordName)
	}
	// Artifact is required (methodSpec artifact:true) and becomes -o <path>
	// so the recording ends up on the host filesystem for the size check.
	if c.Artifact != "" {
		args = append(args, "-o", c.Artifact)
	}
	return args
}

func posRecordCmd(c *Op) []string {
	args := []string{c.Text}
	if c.RecordName != "" {
		args = append(args, "-n", c.RecordName)
	}
	return args
}

// libvirt positional builders.
//
// LibvirtSendKey takes a variadic `Keys []string` positional so
// "ctrl alt F2" maps to three separate argv slots.
func posKeyNameSplit(c *Op) []string {
	return strings.Fields(c.KeyName)
}

// posCommandFields splits c.Command on whitespace into argv slots. Used for
// libvirt:guest/exec where the check surface is `command: "uname -s"` and
// the QEMU guest-agent wants a real argv list (no shell, no metachars).
// Prefixes `--` so kong does not interpret embedded `-flag`-like tokens
// (e.g. `-s` in `uname -s`, `-fsS` in `curl -fsS …`) as CLI flags of the
// outer `charly check libvirt guest exec` invocation.
// For commands containing real shell metacharacters (pipes, redirects,
// quoted spaces), use `command: "sh -c '<full command>'"` so the check-side
// argv is `sh`, `-c`, `<full command>`.
func posCommandFields(c *Op) []string {
	fields := strings.Fields(c.Command)
	if len(fields) == 0 {
		return nil
	}
	out := make([]string, 0, len(fields)+1)
	out = append(out, "--")
	out = append(out, fields...)
	return out
}

// LibvirtQmp takes a method name + optional JSON args string. Text holds the
// QMP method name (e.g. "query-status"); Input the JSON arg payload.
func posLibvirtQmp(c *Op) []string {
	args := []string{c.Text}
	if c.Input != "" {
		args = append(args, c.Input)
	}
	return args
}

// adb / appium positional builders. All flag-form (--apk, --caps, --selector,
// --strategy, --text) so the CLI subcommand structs in adb.go / appium.go
// can use Kong's flag parser directly without positional ordering surprises.

// posShellArgs prefixes "--" so kong doesn't interpret `-l` / `-p` / etc.
// shell args as flags of the outer `charly check adb shell` invocation.
func posShellArgs(c *Op) []string {
	return append([]string{"--"}, c.Args...)
}

// posPackageArg takes the package id from Args[0] for `adb uninstall`. The
// Args:[0] convention (vs. a dedicated Package modifier) keeps the modifier
// surface flat and avoids overloading c.Property.
func posPackageArg(c *Op) []string {
	if len(c.Args) == 0 {
		return nil
	}
	return []string{c.Args[0]}
}

func posPropertyArg(c *Op) []string { return []string{c.Property} }

// posInstallApp builds the flags for `adb install-app` from the install-app
// modifiers. Source/Arch/AppVersion are passed only when set so the CLI
// defaults (apk-pure / x86_64) apply otherwise.
func posInstallApp(c *Op) []string {
	args := []string{"--package", c.AppId}
	if c.Source != "" {
		args = append(args, "--source", c.Source)
	}
	if c.Arch != "" {
		args = append(args, "--arch", c.Arch)
	}
	if c.AppVersion != "" {
		args = append(args, "--app-version", c.AppVersion)
	}
	return args
}

// resolveCheckApk resolves a relative committed-APK path (adb: install /
// appium: install-app, apk: ./tests/data/...) against the AUTHORING candy's
// source tree, so a check resolves its fixture whether the candy is local OR
// fetched via @github (mirrors the deploy resolveApkPath, R3). The check's
// Origin is "candy:<key>" where <key> is the candy MAP KEY (a bare name for a
// local candy, the bare @github ref for a fetched one) — CandyDirs is keyed by
// that same key (candySourceDirs), so the lookup matches in both cases.
// Absolute paths pass through; a non-candy origin or an unknown key leaves the
// ref verbatim (cwd-relative, no regression).
func (r *Runner) resolveCheckApk(apk, origin string) string {
	if apk == "" || filepath.IsAbs(apk) {
		return apk
	}
	key := strings.TrimPrefix(origin, "candy:")
	if key == origin {
		return apk // not candy-originated
	}
	dir := r.CandyDirs[key]
	if dir == "" {
		return apk // authoring candy's source dir unknown
	}
	return resolveApkPath(apk, dir)
}

func posApkFlag(c *Op) []string      { return []string{"--apk", c.Apk} }
func posArtifactFlag(c *Op) []string { return []string{"--artifact", c.Artifact} }
func posCapsFlag(c *Op) []string     { return []string{"--caps", c.Caps} }

// appendSession appends --session <id> when an explicit session override is
// set. Shared by every appium builder (R3: one session-flag rule).
func appendSession(args []string, c *Op) []string {
	if c.Session != "" {
		return append(args, "--session", c.Session)
	}
	return args
}

// appendSelector appends --selector + optional --strategy. Shared prefix for
// element-targeted appium builders.
func appendSelector(args []string, c *Op) []string {
	args = append(args, "--selector", c.Selector)
	if c.Strategy != "" {
		args = append(args, "--strategy", c.Strategy)
	}
	return args
}

// appendOptSelector appends --selector(+--strategy) only when a selector is set
// (used by execute/raw, where the element is optional and substituted via the
// {element} token).
func appendOptSelector(args []string, c *Op) []string {
	if c.Selector == "" {
		return args
	}
	return appendSelector(args, c)
}

// appendElemOrXY appends either --selector(+--strategy) or --x/--y. The CLI
// gesture leaves require exactly one of the two targeting modes.
func appendElemOrXY(args []string, c *Op) []string {
	if c.Selector != "" {
		return appendSelector(args, c)
	}
	return append(args, "--x", strconv.Itoa(c.X), "--y", strconv.Itoa(c.Y))
}

// posSelectorStrategy emits --selector + optional --strategy + session. Used by
// appium find / click / get-text / clear / find-all. Default strategy (xpath)
// is applied subprocess-side when --strategy is omitted.
func posSelectorStrategy(c *Op) []string {
	return appendSession(appendSelector(nil, c), c)
}

// posSelectorTextStrategy adds --text for send-keys.
func posSelectorTextStrategy(c *Op) []string {
	return appendSession(append(appendSelector(nil, c), "--text", c.Text), c)
}

// posSelectorAttribute adds --attribute for get-attribute.
func posSelectorAttribute(c *Op) []string {
	return appendSession(append(appendSelector(nil, c), "--attribute", c.Attribute), c)
}

// posSessionOnly emits only --session when set (source/back/contexts/info/...).
func posSessionOnly(c *Op) []string { return appendSession(nil, c) }

// posAppId emits --app-id for the app-lifecycle group.
func posAppId(c *Op) []string { return appendSession([]string{"--app-id", c.AppId}, c) }

// posActivity emits --activity (+ optional --params) for app-start-activity.
func posActivity(c *Op) []string {
	args := []string{"--activity", c.Activity}
	if c.Params != "" {
		args = append(args, "--params", c.Params)
	}
	return appendSession(args, c)
}

// posKeycode emits --keycode (+ optional --params) for key-press.
func posKeycode(c *Op) []string {
	args := []string{"--keycode", strconv.Itoa(c.Keycode)}
	if c.Params != "" {
		args = append(args, "--params", c.Params)
	}
	return appendSession(args, c)
}

// posParamsOnly emits optional --params (+ session). device-* get/set ops:
// empty params = get, non-empty = the value/JSON to apply.
func posParamsOnly(c *Op) []string {
	var args []string
	if c.Params != "" {
		args = append(args, "--params", c.Params)
	}
	return appendSession(args, c)
}

// posElemOrXY: element-or-coordinate target (+ optional --params + session).
// Used by gesture-tap/double-tap/long-press/drag.
func posElemOrXY(c *Op) []string {
	args := appendElemOrXY(nil, c)
	if c.Params != "" {
		args = append(args, "--params", c.Params)
	}
	return appendSession(args, c)
}

// posGesture: element-or-coordinate + optional --direction/--percent (+ --params
// + session). Used by gesture-swipe/scroll/fling/pinch-open/pinch-close.
func posGesture(c *Op) []string {
	args := appendElemOrXY(nil, c)
	if c.Direction != "" {
		args = append(args, "--direction", c.Direction)
	}
	if c.Percent != "" {
		args = append(args, "--percent", c.Percent)
	}
	if c.Params != "" {
		args = append(args, "--params", c.Params)
	}
	return appendSession(args, c)
}

// posAppiumExecute: --expression (+ optional --request-body + optional selector
// for {element} substitution + session). The mobile:/execute-script escape hatch.
func posAppiumExecute(c *Op) []string {
	args := []string{"--expression", c.Expression}
	if c.RequestBody != "" {
		args = append(args, "--request-body", c.RequestBody)
	}
	return appendSession(appendOptSelector(args, c), c)
}

// posAppiumRaw: --method + --path (+ optional --request-body + optional selector
// + session). The full W3C WebDriver HTTP escape hatch.
func posAppiumRaw(c *Op) []string {
	args := []string{"--method", c.Method, "--path", c.Path}
	if c.RequestBody != "" {
		args = append(args, "--request-body", c.RequestBody)
	}
	return appendSession(appendOptSelector(args, c), c)
}

// posLogcatTail emits --lines / --filter optionals only when set; the CLI
// defaults handle the unset case.
func posLogcatTail(c *Op) []string {
	var args []string
	if c.Amount > 0 {
		args = append(args, "--lines", strconv.Itoa(c.Amount))
	}
	if c.Query != "" {
		args = append(args, "--filter", c.Query)
	}
	return args
}

// posWaitForDevice emits --timeout when set; default lives subprocess-side.
func posWaitForDevice(c *Op) []string {
	if c.Timeout == "" {
		return nil
	}
	return []string{"--timeout", c.Timeout}
}

// ---------------------------------------------------------------------------
// Verb dispatchers
// ---------------------------------------------------------------------------

func (r *Runner) runCdp(ctx context.Context, c *Op) CheckResult {
	return r.runCharlyVerb(ctx, c, "cdp", c.Cdp, cdpMethods)
}

func (r *Runner) runWl(ctx context.Context, c *Op) CheckResult {
	return r.runCharlyVerb(ctx, c, "wl", c.Wl, wlMethods)
}

func (r *Runner) runDbus(ctx context.Context, c *Op) CheckResult {
	return r.runCharlyVerb(ctx, c, "dbus", c.Dbus, dbusMethods)
}

func (r *Runner) runVnc(ctx context.Context, c *Op) CheckResult {
	return r.runCharlyVerb(ctx, c, "vnc", c.Vnc, vncMethods)
}

func (r *Runner) runMcp(ctx context.Context, c *Op) CheckResult {
	return r.runCharlyVerb(ctx, c, "mcp", c.Mcp, mcpMethods)
}

func (r *Runner) runRecord(ctx context.Context, c *Op) CheckResult {
	return r.runCharlyVerb(ctx, c, "record", c.Record, recordMethods)
}

func (r *Runner) runSpice(ctx context.Context, c *Op) CheckResult {
	return r.runCharlyVerb(ctx, c, "spice", c.Spice, spiceMethods)
}

func (r *Runner) runLibvirt(ctx context.Context, c *Op) CheckResult {
	return r.runCharlyVerb(ctx, c, "libvirt", c.Libvirt, libvirtMethods)
}

func (r *Runner) runK8s(ctx context.Context, c *Op) CheckResult {
	return r.runCharlyVerb(ctx, c, "k8s", c.K8s, k8sMethods)
}

func (r *Runner) runAdb(ctx context.Context, c *Op) CheckResult {
	return r.runCharlyVerb(ctx, c, "adb", c.Adb, adbMethods)
}

func (r *Runner) runAppium(ctx context.Context, c *Op) CheckResult {
	return r.runCharlyVerb(ctx, c, "appium", c.Appium, appiumMethods)
}

// runCharlyVerb is the shared dispatch path: skip checks, method lookup,
// argv building, subprocess exec, matcher pipeline, optional artifact size
// assertion. Returns the CheckResult directly.
// vmDisplayDeviceAbsent reports whether a VM-display verb (spice/vnc) failed
// because the target VM declares no such display device — a legitimate N/A
// SKIP, NOT a check failure. The cachyos-gpu operator drops SPICE (the
// passed-through RTX heads ARE the display), so the SHARED
// cachyos-gpu-desktop-check SPICE checks skip on the operator while still
// asserting on the disposable check bed (which keeps a virtio/SPICE head) — one
// shared candy, no operator/bed split (R3). The signal is the VM-target
// resolver's own "VM <name> has no SPICE graphics device declared in vm.yml"
// error (charly/vm_target.go), surfaced on the verb subprocess's stderr.
func vmDisplayDeviceAbsent(verb, stderr string) bool {
	return (verb == "spice" || verb == "vnc") &&
		strings.Contains(stderr, "graphics device declared in vm.yml")
}

func (r *Runner) runCharlyVerb(ctx context.Context, c *Op, verb, method string, allowlist map[string]methodSpec) CheckResult {
	if r.Mode == RunModeBox {
		return skipf(c, fmt.Sprintf("%s: %s requires a running container (skip under charly check box)", verb, method))
	}
	if r.Box == "" {
		return skipf(c, fmt.Sprintf("%s: %s runner has no image context (should not happen under charly check)", verb, method))
	}

	spec, ok := allowlist[method]
	if !ok {
		return failf(c, "%s: unknown method %q (see /charly:test for the allowlist)", verb, method)
	}

	// Required-modifier check mirrors validate_tests.go but guards against
	// runs where validation was bypassed (e.g. tests loaded directly from a
	// label without re-validating).
	if err := checkRequiredFields(c, spec.required); err != nil {
		return failf(c, "%s: %s: %v", verb, method, err)
	}

	// Branch: AI-artifact validation mode for state-dependent capture
	// probes ONLY. Activated when score.validate_ai_artifacts is set
	// AND the verb/method is in the narrow artifactValidatableMethods
	// allowlist. The harness scorer skips the subprocess re-execution
	// (which would overwrite the AI's iteration artifact and capture a
	// different chrome/wayland/etc. moment) and instead validates the
	// AI-produced file at the plan-declared `artifact:` path.
	//
	// The freshness mtime gate enforces that the file was written
	// during the current iteration — pre-staged or stale files are
	// rejected with a clear actionable error. This is the load-bearing
	// anti-deception mechanism.
	//
	// stdout/stderr/exit_status matchers are incompatible with this
	// mode: without re-running the command there is no captured
	// output to match against. Authors hitting this combination need
	// to either remove the matchers or split into separate steps.
	key := verb + "/" + method
	if r.ValidateAiArtifacts && artifactValidatableMethods[key] {
		if c.Stdout != nil || c.Stderr != nil || c.ExitStatus != nil {
			return failf(c,
				"%s: %s: validate_ai_artifacts skips command execution; "+
					"stdout/stderr/exit_status matchers cannot be evaluated — "+
					"remove them or split into a separate step", verb, method)
		}
		info, err := os.Stat(c.Artifact)
		if err != nil {
			return failf(c,
				"%s: %s: validate_ai_artifacts requires the AI to have produced %q "+
					"during its iteration (e.g. via `charly check self-evaluate`); "+
					"file not found: %v", verb, method, c.Artifact, err)
		}
		if !r.IterStartTime.IsZero() && info.ModTime().Before(r.IterStartTime) {
			return failf(c,
				"%s: %s: artifact %q is stale (mtime %s, iter started %s) — "+
					"the AI must produce this artifact during the current iteration; "+
					"pre-staged or carried-forward files are not accepted",
				verb, method, c.Artifact,
				info.ModTime().UTC().Format(time.RFC3339),
				r.IterStartTime.UTC().Format(time.RFC3339))
		}
		// Run the artifact validators against the existing AI-produced
		// file. Identical pipeline to the post-execution branch below;
		// validators inspect the binary content and dimensions
		// independently of who wrote the file.
		if err := runArtifactValidators(c); err != nil {
			return failf(c, "%s: %s: %v", verb, method, err)
		}
		return passf(c, fmt.Sprintf("%s %s: validated AI-produced artifact at %s (mtime %s)",
			verb, method, c.Artifact, info.ModTime().UTC().Format(time.RFC3339)))
	}

	// Resolve a relative committed-APK path (adb: install / appium: install-app,
	// `apk: ./tests/data/...`) against the ORIGINATING candy's source tree, so a
	// check authored on a candy resolves to that candy's copy — local OR fetched
	// via @github — instead of the check cwd. Reuses the deploy walk-up (R3).
	if c.Apk != "" {
		if resolved := r.resolveCheckApk(c.Apk, c.Origin); resolved != c.Apk {
			cc := *c
			cc.Apk = resolved
			c = &cc
		}
	}

	// Build argv: ["check"] + spec.path + [image?] + spec.posArgs(c) + ["-i", instance]
	// spec.skipBox=true elides the image/deploy-name positional (used by
	// k8s verbs that operate against a cluster instead of an image).
	argv := append([]string{"check"}, spec.path...)
	if !spec.skipBox {
		argv = append(argv, r.Box)
	}
	if spec.posArgs != nil {
		argv = append(argv, spec.posArgs(c)...)
	}
	if r.Instance != "" && !spec.skipBox {
		argv = append(argv, "-i", r.Instance)
	}

	charlyBinary, err := findCharlyBinary()
	if err != nil {
		return failf(c, "%s: %s: %v", verb, method, err)
	}
	cmd := exec.CommandContext(ctx, charlyBinary, argv...)
	stdout, stderr, exit, execErr := runCaptureCmd(cmd)
	if execErr != nil {
		return failf(c, "%s: %s: execution error: %v", verb, method, execErr)
	}
	// Precondition-not-met gate: a VM-display verb run against a deployment that
	// declares no such display device is N/A, not a failure (the SPICE-less
	// cachyos-gpu operator vs the SPICE-having check bed). See vmDisplayDeviceAbsent.
	if vmDisplayDeviceAbsent(verb, stderr) {
		return skipf(c, fmt.Sprintf("%s %s — N/A: deployment has no %s graphics device (SPICE-less GPU desktop)",
			verb, method, strings.ToUpper(verb)))
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

	if spec.artifact {
		if err := runArtifactValidators(c); err != nil {
			return failf(c, "%s: %s: %v", verb, method, err)
		}
	}

	// On PASS, return the captured stdout as the Message (or stderr if
	// stdout is empty — some verbs print to stderr per /charly-build:check
	// "Know which stream a --version-style command writes to"). This
	// makes the captured subprocess output available to downstream
	// `capture: <name>` / `capture_extract:` chains; the docstring on
	// CaptureFromResult promises this and runCommand already does it.
	// Falls back to the exit summary when both streams are empty so
	// the report still has something human-readable.
	body := stdout
	if strings.TrimSpace(body) == "" {
		body = stderr
	}
	if strings.TrimSpace(body) == "" {
		body = fmt.Sprintf("%s %s: exit=%d", verb, method, exit)
	}
	return passf(c, body)
}

// runArtifactValidators is the shared post-validator pipeline used by
// both code paths in runCharlyVerb: (a) after the harness's own subprocess
// exec produced the file, and (b) after the freshness mtime gate
// confirmed the AI's file is fresh in validate_ai_artifacts mode.
// Returns nil on all-pass or the first validator's error.
func runArtifactValidators(c *Op) error {
	if c.ArtifactMinBytes > 0 {
		info, err := os.Stat(c.Artifact)
		if err != nil {
			return fmt.Errorf("artifact %q not found: %w", c.Artifact, err)
		}
		if info.Size() < int64(c.ArtifactMinBytes) {
			return fmt.Errorf("artifact %q size %d < required min_bytes %d",
				c.Artifact, info.Size(), c.ArtifactMinBytes)
		}
	}
	if c.ArtifactMinDimensions != "" {
		if err := assertArtifactMinDimensions(c.Artifact, c.ArtifactMinDimensions); err != nil {
			return err
		}
	}
	if c.ArtifactNotUniform {
		if err := assertArtifactNotUniform(c.Artifact); err != nil {
			return err
		}
	}
	if c.ArtifactMinCastEvents > 0 {
		if err := assertArtifactMinCastEvents(c.Artifact, c.ArtifactMinCastEvents); err != nil {
			return err
		}
	}
	return nil
}

// assertArtifactMinDimensions decodes the artifact's image header (PNG/JPEG)
// and fails if width or height is below the "WxH" requirement. Cheap — uses
// image.DecodeConfig which reads only the header, not the full pixel data.
func assertArtifactMinDimensions(path, wxh string) error {
	parts := strings.SplitN(wxh, "x", 2)
	if len(parts) != 2 {
		return fmt.Errorf("artifact_min_dimensions: bad format %q (want WxH)", wxh)
	}
	wantW, err := strconv.Atoi(parts[0])
	if err != nil || wantW <= 0 {
		return fmt.Errorf("artifact_min_dimensions: bad width %q", parts[0])
	}
	wantH, err := strconv.Atoi(parts[1])
	if err != nil || wantH <= 0 {
		return fmt.Errorf("artifact_min_dimensions: bad height %q", parts[1])
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("artifact %q open: %w", path, err)
	}
	defer f.Close() //nolint:errcheck
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return fmt.Errorf("artifact %q decode-config: %w", path, err)
	}
	if cfg.Width < wantW || cfg.Height < wantH {
		return fmt.Errorf("artifact %q dimensions %dx%d < required min %dx%d",
			path, cfg.Width, cfg.Height, wantW, wantH)
	}
	return nil
}

// assertArtifactNotUniform decodes the full image and samples pixels at 100
// deterministic positions; fails if every sampled pixel shares the same RGBA.
// Catches all-black / all-white / blank-canvas screenshot failures that
// artifact_min_bytes alone would pass (a 100KB all-black PNG has the same
// byte profile as a real screenshot of similar dimensions).
func assertArtifactNotUniform(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("artifact %q open: %w", path, err)
	}
	defer f.Close() //nolint:errcheck
	img, _, err := image.Decode(f)
	if err != nil {
		return fmt.Errorf("artifact %q decode: %w", path, err)
	}
	bounds := img.Bounds()
	w := bounds.Dx()
	h := bounds.Dy()
	if w <= 0 || h <= 0 {
		return fmt.Errorf("artifact %q has zero-size bounds %dx%d", path, w, h)
	}
	// Sample 100 pixels on a 10x10 stride. For very small images this still
	// covers every pixel because step rounds up via max(1, dim/10).
	stepX := max(w/10, 1)
	stepY := max(h/10, 1)
	var firstR, firstG, firstB, firstA uint32
	first := true
	for py := bounds.Min.Y; py < bounds.Max.Y; py += stepY {
		for px := bounds.Min.X; px < bounds.Max.X; px += stepX {
			r, g, b, a := img.At(px, py).RGBA()
			if first {
				firstR, firstG, firstB, firstA = r, g, b, a
				first = false
				continue
			}
			if r != firstR || g != firstG || b != firstB || a != firstA {
				return nil // found a varying pixel — not uniform
			}
		}
	}
	return fmt.Errorf("artifact %q is uniformly one color (RGBA=%d,%d,%d,%d) — likely a blank/black/white screenshot",
		path, firstR>>8, firstG>>8, firstB>>8, firstA>>8)
}

// assertArtifactMinCastEvents validates an asciinema .cast file as having
// at least the requested number of event lines. The cast format is one
// JSON object per line: line 1 is a header object {"version":2, "width":..,
// "height":.., ...}, subsequent non-empty lines are event arrays
// [time_offset, "o"|"i", payload]. Fails if header is missing/malformed
// or fewer than minEvents non-empty event lines follow.
func assertArtifactMinCastEvents(path string, minEvents int) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("artifact %q open: %w", path, err)
	}
	defer f.Close() //nolint:errcheck
	scan := bufio.NewScanner(f)
	// asciinema events can be long; bump the buffer so a 1MB single line
	// does not silently truncate the count.
	scan.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	if !scan.Scan() {
		return fmt.Errorf("artifact %q is empty (expected asciinema cast header on line 1)", path)
	}
	var header map[string]any
	if err := json.Unmarshal(scan.Bytes(), &header); err != nil {
		return fmt.Errorf("artifact %q line 1: not a JSON object (asciinema header expected): %w", path, err)
	}
	if _, ok := header["version"]; !ok {
		return fmt.Errorf("artifact %q line 1: JSON object missing %q field (not an asciinema cast header)", path, "version")
	}
	events := 0
	for scan.Scan() {
		if len(strings.TrimSpace(scan.Text())) == 0 {
			continue
		}
		events++
		if events >= minEvents {
			return nil // reached the required count; stop reading
		}
	}
	if err := scan.Err(); err != nil {
		return fmt.Errorf("artifact %q scan: %w", path, err)
	}
	return fmt.Errorf("artifact %q has %d events, want >= %d", path, events, minEvents)
}

// checkRequiredFields returns an error naming any required field that is
// zero-valued on the Check. Mirrors the validate_tests.go precondition so
// runtime-only callers (e.g. tests loaded from an OCI label into an
// un-validated runner) still surface authoring errors rather than silent
// wrong behavior.
func checkRequiredFields(c *Op, required []string) error {
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
func isZeroField(c *Op, name string) bool {
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
	case "X2":
		return c.X2 == 0
	case "Y2":
		return c.Y2 == 0
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
	case "Record":
		return c.Record == ""
	case "RecordName":
		return c.RecordName == ""
	case "Spice":
		return c.Spice == ""
	case "Libvirt":
		return c.Libvirt == ""
	case "K8s":
		return c.K8s == ""
	case "Name":
		return c.Name == ""
	case "Namespace":
		return c.Namespace == ""
	case "Label":
		return c.Label == ""
	case "Cluster":
		return c.Cluster == ""
	case "Manifest":
		return c.Manifest == ""
	case "Kind":
		// Kind is a METHOD on Check; required-field lookups of "Kind" target
		// the k8s-specific K8sKind field to avoid the method-vs-field name
		// clash that Go disallows.
		return c.K8sKind == ""
	case "K8sKind":
		return c.K8sKind == ""
	case "K8sContext":
		return c.K8sContext == ""
	case "Kubeconfig":
		return c.Kubeconfig == ""
	case "K8sCount":
		return c.K8sCount == 0
	case "K8sResource":
		return c.K8sResource == ""
	case "K8sGroup":
		return c.K8sGroup == ""
	case "K8sVersion":
		return c.K8sVersion == ""
	case "File":
		return c.File == ""
	case "Args":
		return len(c.Args) == 0
	case "Apk":
		return c.Apk == ""
	case "Property":
		return c.Property == ""
	case "Caps":
		return c.Caps == ""
	case "Strategy":
		return c.Strategy == ""
	case "Session":
		return c.Session == ""
	case "Adb":
		return c.Adb == ""
	case "Appium":
		return c.Appium == ""
	case "AppId":
		return c.AppId == ""
	case "Activity":
		return c.Activity == ""
	case "Attribute":
		return c.Attribute == ""
	case "Percent":
		return c.Percent == ""
	case "Keycode":
		return c.Keycode == 0
	case "Params":
		return c.Params == ""
	}
	// Unknown field name is a programming error: treat as "not zero" so
	// authoring errors surface elsewhere instead of spurious skips.
	return false
}

// findCharlyBinary returns the absolute path to the `charly` binary the test runner
// should spawn. Prefers /proc/self/exe (the currently-running binary so tests
// invoke the same build that collected them), falling back to $PATH lookup.
// Testability var for mocks.
var findCharlyBinary = defaultFindCharlyBinary

func defaultFindCharlyBinary() (string, error) {
	if p, err := os.Executable(); err == nil && p != "" {
		return p, nil
	}
	return exec.LookPath("charly")
}
