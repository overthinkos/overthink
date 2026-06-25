package kit

import (
	"strconv"
	"strings"

	"github.com/overthinkos/overthink/charly/spec"
)

// MethodSpec is a live-container verb's per-method dispatch spec: the CLI subcommand
// path, the required #Op modifiers, the positional-arg builder, and the artifact /
// skip-box flags. The importable form of charly's methodSpec — a live-verb's allowlist
// is map[string]MethodSpec, consumed by the host's runCharlyVerb. Fields are exported so
// a candy (or package main) can author the allowlist.
type MethodSpec struct {
	// Path is the `charly check <verb> <method...>` subcommand path.
	Path []string
	// Required names the #Op modifier fields that must be set for this method.
	Required []string
	// PosArgs builds the positional args inserted after the image name, before -i.
	PosArgs func(c *spec.Op) []string
	// Artifact marks a state-dependent capture method (screenshot/record-stop) whose
	// produced file is validated (and which participates in validate_ai_artifacts mode).
	Artifact bool
	// SkipBox = true means the verb targets a cluster/other non-image target, so the
	// usual image/deploy-name positional is NOT inserted (used by kube-style verbs).
	SkipBox bool
}

// ---------------------------------------------------------------------------
// positional-arg builders — the shared library reused across live verbs.
// Each returns the positional args to insert AFTER the image name, BEFORE any
// -i instance flag. They never fail: required-modifier checks run before this.
// ---------------------------------------------------------------------------

func PosTab(c *spec.Op) []string             { return []string{c.Tab} }
func PosURL(c *spec.Op) []string             { return []string{c.URL} }
func PosText(c *spec.Op) []string            { return []string{c.Text} }
func PosKeyName(c *spec.Op) []string         { return []string{c.KeyName} }
func PosCombo(c *spec.Op) []string           { return []string{c.Combo} }
func PosTarget(c *spec.Op) []string          { return []string{c.Target} }
func PosCommand(c *spec.Op) []string         { return []string{c.Command} }
func PosArtifact(c *spec.Op) []string        { return []string{c.Artifact} }
func PosTabExpression(c *spec.Op) []string   { return []string{c.Tab, c.Expression} }
func PosTabSelector(c *spec.Op) []string     { return []string{c.Tab, c.Selector} }
func PosTabSelectorText(c *spec.Op) []string { return []string{c.Tab, c.Selector, c.Text} }

func PosTabQuery(c *spec.Op) []string {
	if c.Query == "" {
		return []string{c.Tab}
	}
	return []string{c.Tab, c.Query}
}

func PosTabText(c *spec.Op) []string    { return []string{c.Tab, c.Text} }
func PosTabKeyName(c *spec.Op) []string { return []string{c.Tab, c.KeyName} }
func PosTabCombo(c *spec.Op) []string   { return []string{c.Tab, c.Combo} }

func PosTabXY(c *spec.Op) []string {
	return []string{c.Tab, strconv.Itoa(c.X), strconv.Itoa(c.Y)}
}

func PosTabArtifact(c *spec.Op) []string { return []string{c.Tab, c.Artifact} }

func PosCdpRaw(c *spec.Op) []string {
	args := []string{c.Tab, c.Method}
	if c.RequestBody != "" {
		args = append(args, c.RequestBody)
	}
	return args
}

func PosXY(c *spec.Op) []string {
	return []string{strconv.Itoa(c.X), strconv.Itoa(c.Y)}
}

// PosXYXY emits four positionals (start + end) for `<image> <x1> <y1> <x2> <y2>` (e.g. wl drag).
func PosXYXY(c *spec.Op) []string {
	return []string{strconv.Itoa(c.X), strconv.Itoa(c.Y), strconv.Itoa(c.X2), strconv.Itoa(c.Y2)}
}

func PosScroll(c *spec.Op) []string {
	amount := c.Amount
	if amount == 0 {
		amount = 1
	}
	return []string{strconv.Itoa(c.X), strconv.Itoa(c.Y), c.Direction, strconv.Itoa(amount)}
}

func PosAtspi(c *spec.Op) []string {
	args := []string{c.Action}
	if c.Query != "" {
		args = append(args, c.Query)
	}
	return args
}

func PosClipboard(c *spec.Op) []string {
	args := []string{c.Action}
	if c.Action == "set" && c.Text != "" {
		args = append(args, c.Text)
	}
	return args
}

func PosTargetOptional(c *spec.Op) []string {
	if c.Target == "" {
		return nil
	}
	return []string{c.Target}
}

func PosOverlayShow(c *spec.Op) []string {
	args := []string{"--type", "text", "--text", c.Text}
	if c.Target != "" {
		args = append(args, "--name", c.Target)
	}
	return args
}

func PosDbusCall(c *spec.Op) []string {
	args := make([]string, 0, 3+len(c.Args))
	args = append(args, c.Dest, c.Path, c.Method)
	args = append(args, c.Args...)
	return args
}

func PosDbusIntrospect(c *spec.Op) []string { return []string{c.Dest, c.Path} }

func PosDbusNotify(c *spec.Op) []string {
	args := []string{c.Text} // text = title
	if c.Description != "" {
		args = append(args, c.Description)
	}
	return args
}

func PosMcpCommon(c *spec.Op) []string {
	if c.McpName == "" {
		return nil
	}
	return []string{"--name", c.McpName}
}

func PosMcpCall(c *spec.Op) []string {
	args := []string{c.Tool}
	if c.Input != "" {
		args = append(args, c.Input)
	}
	if c.McpName != "" {
		args = append(args, "--name", c.McpName)
	}
	return args
}

func PosMcpRead(c *spec.Op) []string {
	args := []string{c.URI}
	if c.McpName != "" {
		args = append(args, "--name", c.McpName)
	}
	return args
}

func PosRecordStart(c *spec.Op) []string {
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

func PosRecordStop(c *spec.Op) []string {
	var args []string
	if c.RecordName != "" {
		args = append(args, "-n", c.RecordName)
	}
	if c.Artifact != "" {
		args = append(args, "-o", c.Artifact)
	}
	return args
}

func PosRecordCmd(c *spec.Op) []string {
	args := []string{c.Text}
	if c.RecordName != "" {
		args = append(args, "-n", c.RecordName)
	}
	return args
}

// PosKeyNameSplit splits c.KeyName on whitespace (libvirt send-key: "ctrl alt F2" → 3 slots).
func PosKeyNameSplit(c *spec.Op) []string {
	return strings.Fields(c.KeyName)
}

// PosCommandFields splits c.Command into argv slots, prefixed with `--` so kong does not
// treat embedded -flags as its own (libvirt:guest/exec). For shell metachars use
// `command: "sh -c '<full>'"`.
func PosCommandFields(c *spec.Op) []string {
	fields := strings.Fields(c.Command)
	if len(fields) == 0 {
		return nil
	}
	out := make([]string, 0, len(fields)+1)
	out = append(out, "--")
	out = append(out, fields...)
	return out
}

// PosLibvirtQmp emits a QMP method name + optional JSON args (Text = method, Input = JSON).
func PosLibvirtQmp(c *spec.Op) []string {
	args := []string{c.Text}
	if c.Input != "" {
		args = append(args, c.Input)
	}
	return args
}
