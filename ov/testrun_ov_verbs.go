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
	// skipImage = true means the verb operates against a cluster or other
	// non-image target, so the usual image/deploy-name positional must NOT
	// be appended between the method path and posArgs. Used by k8s verbs.
	skipImage bool
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
// record methods — `ov test record <method>` drives in-container recording
// sessions (asciinema terminal / pixelflux-record / wf-recorder desktop).
// Container-only: resolveContainer does not know about VMs, so a `record:`
// check on a `vm:<name>` deploy will fail at subprocess dispatch. Documented
// in /ov:record; validator does not pre-filter by deploy kind.
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
// spice methods — `ov test spice <method>` speaks the SPICE wire protocol
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
// libvirt methods — `ov test libvirt <method>` uses go-libvirt RPC against
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
	// guest/exec runs a command via qemu-guest-agent inside the VM. Text holds
	// the full command line (Command would collide with the command: verb).
	"guest/exec":   {path: []string{"libvirt", "guest", "exec"}, required: []string{"Text"}, posArgs: posText},
	"guest/fstrim": {path: []string{"libvirt", "guest", "fstrim"}},

	// Snapshot subgroup — Target holds the snapshot name.
	"snapshot/list":   {path: []string{"libvirt", "snapshot", "list"}},
	"snapshot/create": {path: []string{"libvirt", "snapshot", "create"}, required: []string{"Target"}, posArgs: posTarget},
	"snapshot/info":   {path: []string{"libvirt", "snapshot", "info"}, required: []string{"Target"}, posArgs: posTarget},
	"snapshot/revert": {path: []string{"libvirt", "snapshot", "revert"}, required: []string{"Target"}, posArgs: posTarget},
	"snapshot/delete": {path: []string{"libvirt", "snapshot", "delete"}, required: []string{"Target"}, posArgs: posTarget},
}

// ---------------------------------------------------------------------------
// k8s methods — `ov test k8s <method>` probes a Kubernetes cluster via the
// vendored client-go SDK. Cluster selection is via --cluster <profile> /
// --context / --kubeconfig (see cmd_test_k8s.go). Host-side; applicable to
// any deploy whose post-provision registered a ClusterProfile (typically
// a k3s-server layer).
// ---------------------------------------------------------------------------

// k8s methods all run against a cluster, not an image/container, so
// skipImage=true across the board.
var k8sMethods = map[string]methodSpec{
	"nodes":          {path: []string{"k8s", "nodes"}, posArgs: posK8sCluster, skipImage: true},
	"wait-nodes":     {path: []string{"k8s", "wait-nodes"}, posArgs: posK8sWaitNodes, skipImage: true},
	"pods":           {path: []string{"k8s", "pods"}, posArgs: posK8sPods, skipImage: true},
	"wait-ready":     {path: []string{"k8s", "wait-ready"}, required: []string{"Kind", "Name"}, posArgs: posK8sWaitReady, skipImage: true},
	"ingress":        {path: []string{"k8s", "ingress"}, posArgs: posK8sNamespaceOpt, skipImage: true},
	"ingressclass":   {path: []string{"k8s", "ingressclass"}, posArgs: posK8sCluster, skipImage: true},
	"storageclass":   {path: []string{"k8s", "storageclass"}, posArgs: posK8sCluster, skipImage: true},
	"service":        {path: []string{"k8s", "service"}, posArgs: posK8sNamespaceOpt, skipImage: true},
	"lb-external-ip": {path: []string{"k8s", "lb-external-ip"}, required: []string{"Namespace", "Name"}, posArgs: posK8sLbExternal, skipImage: true},
	"addons":         {path: []string{"k8s", "addons"}, posArgs: posK8sAddons, skipImage: true},
	"apply":          {path: []string{"k8s", "apply"}, required: []string{"Manifest"}, posArgs: posK8sApply, skipImage: true},
	"delete":         {path: []string{"k8s", "delete"}, required: []string{"Manifest"}, posArgs: posK8sApply, skipImage: true},
	"raw":            {path: []string{"k8s", "raw"}, required: []string{"Resource"}, posArgs: posK8sRaw, skipImage: true},
}

// ---------------------------------------------------------------------------
// k8s positional-arg builders — every method emits --cluster/--context/
// --kubeconfig + its method-specific flags. Because k8s probes are run
// against a cluster (not a container/image), the --image positional from
// runOvVerb is still passed, but `ov test k8s ...` ignores it by accepting
// arbitrary trailing args under Kong's default catch-all policy.
// ---------------------------------------------------------------------------

// posK8sCluster emits only the shared cluster-selection flags. Used by
// methods that take no other parameters (nodes, ingressclass, storageclass).
func posK8sCluster(c *Check) []string {
	return k8sClusterArgs(c)
}

func posK8sWaitNodes(c *Check) []string {
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

func posK8sPods(c *Check) []string {
	args := k8sClusterArgs(c)
	if c.Namespace != "" {
		args = append(args, "--namespace", c.Namespace)
	}
	if c.Label != "" {
		args = append(args, "--label", c.Label)
	}
	return args
}

func posK8sWaitReady(c *Check) []string {
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

func posK8sNamespaceOpt(c *Check) []string {
	args := k8sClusterArgs(c)
	if c.Namespace != "" {
		args = append(args, "--namespace", c.Namespace)
	}
	return args
}

func posK8sLbExternal(c *Check) []string {
	args := k8sClusterArgs(c)
	args = append(args, "--namespace", c.Namespace, "--name", c.Name)
	if c.Timeout != "" {
		args = append(args, "--timeout", c.Timeout)
	}
	return args
}

func posK8sAddons(c *Check) []string {
	args := k8sClusterArgs(c)
	if c.Namespace != "" {
		args = append(args, "--namespace", c.Namespace)
	}
	if c.Timeout != "" {
		args = append(args, "--timeout", c.Timeout)
	}
	return args
}

func posK8sApply(c *Check) []string {
	args := k8sClusterArgs(c)
	args = append(args, "--file", c.Manifest)
	if c.Namespace != "" {
		args = append(args, "--namespace", c.Namespace)
	}
	return args
}

func posK8sRaw(c *Check) []string {
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
	return args
}

// k8sClusterArgs renders the shared --cluster / --context / --kubeconfig
// selection flags from the Check.
func k8sClusterArgs(c *Check) []string {
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

func posTab(c *Check) []string             { return []string{c.Tab} }
func posURL(c *Check) []string             { return []string{c.URL} }
func posText(c *Check) []string            { return []string{c.Text} }
func posKeyName(c *Check) []string         { return []string{c.KeyName} }
func posCombo(c *Check) []string           { return []string{c.Combo} }
func posTarget(c *Check) []string          { return []string{c.Target} }
func posCommand(c *Check) []string         { return []string{c.Command} }
func posArtifact(c *Check) []string        { return []string{c.Artifact} }
func posTabExpression(c *Check) []string   { return []string{c.Tab, c.Expression} }
func posTabSelector(c *Check) []string     { return []string{c.Tab, c.Selector} }
func posTabSelectorText(c *Check) []string { return []string{c.Tab, c.Selector, c.Text} }
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

// record positional builders. The subprocess already defaults -n to "default"
// when RecordName is empty, so omit the flag in that case.
func posRecordStart(c *Check) []string {
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

func posRecordStop(c *Check) []string {
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

func posRecordCmd(c *Check) []string {
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
func posKeyNameSplit(c *Check) []string {
	return strings.Fields(c.KeyName)
}

// LibvirtQmp takes a method name + optional JSON args string. Text holds the
// QMP method name (e.g. "query-status"); Input the JSON arg payload.
func posLibvirtQmp(c *Check) []string {
	args := []string{c.Text}
	if c.Input != "" {
		args = append(args, c.Input)
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

func (r *Runner) runRecord(ctx context.Context, c *Check) TestResult {
	return r.runOvVerb(ctx, c, "record", c.Record, recordMethods)
}

func (r *Runner) runSpice(ctx context.Context, c *Check) TestResult {
	return r.runOvVerb(ctx, c, "spice", c.Spice, spiceMethods)
}

func (r *Runner) runLibvirt(ctx context.Context, c *Check) TestResult {
	return r.runOvVerb(ctx, c, "libvirt", c.Libvirt, libvirtMethods)
}

func (r *Runner) runK8s(ctx context.Context, c *Check) TestResult {
	return r.runOvVerb(ctx, c, "k8s", c.K8s, k8sMethods)
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

	// Build argv: ["test"] + spec.path + [image?] + spec.posArgs(c) + ["-i", instance]
	// spec.skipImage=true elides the image/deploy-name positional (used by
	// k8s verbs that operate against a cluster instead of an image).
	argv := append([]string{"test"}, spec.path...)
	if !spec.skipImage {
		argv = append(argv, r.Image)
	}
	if spec.posArgs != nil {
		argv = append(argv, spec.posArgs(c)...)
	}
	if r.Instance != "" && !spec.skipImage {
		argv = append(argv, "-i", r.Instance)
	}

	ovBinary, err := findOvBinary()
	if err != nil {
		return failf(c, "%s: %s: %v", verb, method, err)
	}
	cmd := exec.CommandContext(ctx, ovBinary, argv...)
	stdout, stderr, exit, execErr := runCaptureCmd(cmd)
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

	if spec.artifact {
		if c.ArtifactMinBytes > 0 {
			info, err := os.Stat(c.Artifact)
			if err != nil {
				return failf(c, "%s: %s: artifact %q not found after run: %v", verb, method, c.Artifact, err)
			}
			if info.Size() < int64(c.ArtifactMinBytes) {
				return failf(c, "%s: %s: artifact %q size %d < required min_bytes %d",
					verb, method, c.Artifact, info.Size(), c.ArtifactMinBytes)
			}
		}
		if c.ArtifactMinDimensions != "" {
			if err := assertArtifactMinDimensions(c.Artifact, c.ArtifactMinDimensions); err != nil {
				return failf(c, "%s: %s: %v", verb, method, err)
			}
		}
		if c.ArtifactNotUniform {
			if err := assertArtifactNotUniform(c.Artifact); err != nil {
				return failf(c, "%s: %s: %v", verb, method, err)
			}
		}
		if c.ArtifactMinCastEvents > 0 {
			if err := assertArtifactMinCastEvents(c.Artifact, c.ArtifactMinCastEvents); err != nil {
				return failf(c, "%s: %s: %v", verb, method, err)
			}
		}
	}

	return passf(c, fmt.Sprintf("%s %s: exit=%d", verb, method, exit))
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
		return fmt.Errorf("artifact %q open: %v", path, err)
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return fmt.Errorf("artifact %q decode-config: %v", path, err)
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
		return fmt.Errorf("artifact %q open: %v", path, err)
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return fmt.Errorf("artifact %q decode: %v", path, err)
	}
	bounds := img.Bounds()
	w := bounds.Dx()
	h := bounds.Dy()
	if w <= 0 || h <= 0 {
		return fmt.Errorf("artifact %q has zero-size bounds %dx%d", path, w, h)
	}
	// Sample 100 pixels on a 10x10 stride. For very small images this still
	// covers every pixel because step rounds up via max(1, dim/10).
	stepX := w / 10
	if stepX < 1 {
		stepX = 1
	}
	stepY := h / 10
	if stepY < 1 {
		stepY = 1
	}
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
		return fmt.Errorf("artifact %q open: %v", path, err)
	}
	defer f.Close()
	scan := bufio.NewScanner(f)
	// asciinema events can be long; bump the buffer so a 1MB single line
	// does not silently truncate the count.
	scan.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	if !scan.Scan() {
		return fmt.Errorf("artifact %q is empty (expected asciinema cast header on line 1)", path)
	}
	var header map[string]any
	if err := json.Unmarshal(scan.Bytes(), &header); err != nil {
		return fmt.Errorf("artifact %q line 1: not a JSON object (asciinema header expected): %v", path, err)
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
		return fmt.Errorf("artifact %q scan: %v", path, err)
	}
	return fmt.Errorf("artifact %q has %d events, want >= %d", path, events, minEvents)
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
