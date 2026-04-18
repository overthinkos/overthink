package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// CdpSpaCmd groups SPA-aware remote desktop interaction commands.
// These commands target a Selkies-style SPA that renders a remote desktop
// on a <canvas> element. Input events are dispatched via CDP and forwarded
// by the SPA's JavaScript to the remote compositor via WebSocket.
//
// Key advantage over ov wl/vnc: CDP Input events bypass the local compositor
// and Chrome shortcut handlers, so Super+e and Ctrl+T pass through to the SPA.
type CdpSpaCmd struct {
	Click    CdpSpaClickCmd    `cmd:"" help:"Click at remote-desktop coordinates (auto-scaled for SPA)"`
	Key      CdpSpaKeyCmd      `cmd:"" help:"Send a key press to the remote desktop via SPA"`
	KeyCombo CdpSpaKeyComboCmd `cmd:"key-combo" help:"Send a modifier key combo (super+e, ctrl+t, alt+F4) via SPA"`
	Mouse    CdpSpaMouseCmd    `cmd:"" help:"Move pointer on the remote desktop without clicking"`
	Status   CdpSpaStatusCmd   `cmd:"" help:"Show SPA state (coordinates, scale, connection)"`
	Type     CdpSpaTypeCmd     `cmd:"" help:"Type text into the remote desktop via SPA"`
}

// spaState holds the detected SPA state from a CDP eval query.
type spaState struct {
	CanvasWidth  int     `json:"canvasWidth"`
	CanvasHeight int     `json:"canvasHeight"`
	HasOverlay   bool    `json:"hasOverlay"`
	OverlayID    string  `json:"overlayId"`
	Focused      bool    `json:"focused"`
	ScaleX       float64 `json:"scaleX"` // 0 if not detected
	ScaleY       float64 `json:"scaleY"` // 0 if not detected
	Secure       bool    `json:"secure"`
	HasDecoder   bool    `json:"hasDecoder"`
	Error        string  `json:"error"`
}

// spaDetect queries the SPA state via CDP eval.
func spaDetect(client *CDPClient) (spaState, error) {
	js := `(function() {
		var c = document.getElementById("videoCanvas");
		if (!c) return JSON.stringify({error: "no #videoCanvas element — not a Selkies SPA"});
		var oi = document.getElementById("overlayInput");
		var state = {
			canvasWidth: c.width,
			canvasHeight: c.height,
			hasOverlay: !!oi,
			overlayId: oi ? oi.id : "",
			focused: document.activeElement === oi,
			secure: window.isSecureContext,
			hasDecoder: typeof VideoDecoder !== "undefined",
			scaleX: 0,
			scaleY: 0,
			error: ""
		};
		return JSON.stringify(state);
	})()`

	result, err := cdpEvaluate(client, js)
	if err != nil {
		return spaState{}, fmt.Errorf("querying SPA state: %w", err)
	}
	var s spaState
	if err := json.Unmarshal([]byte(result), &s); err != nil {
		return spaState{}, fmt.Errorf("parsing SPA state: %w", err)
	}
	if s.Error != "" {
		return s, fmt.Errorf("%s", s.Error)
	}
	return s, nil
}

// spaEnsureFocus ensures the overlayInput has focus for keyboard events.
func spaEnsureFocus(client *CDPClient) error {
	js := `(function() {
		var oi = document.getElementById("overlayInput");
		if (!oi) return "no overlayInput";
		oi.focus();
		return document.activeElement === oi ? "ok" : "focus failed";
	})()`
	result, err := cdpEvaluate(client, js)
	if err != nil {
		return fmt.Errorf("focusing overlayInput: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("focusing overlayInput: %s", result)
	}
	return nil
}

// parseScale parses a "scaleX,scaleY" string.
func parseScale(s string) (float64, float64, error) {
	parts := strings.SplitN(s, ",", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("scale must be 'scaleX,scaleY' (e.g., 0.824,0.836)")
	}
	sx, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid scaleX: %w", err)
	}
	sy, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid scaleY: %w", err)
	}
	return sx, sy, nil
}

// spaApplyScale converts canvas coordinates to CDP Input coordinates.
// The user provides (x, y) in canvas-space (what CDP screenshots show).
// The SPA maps mouse events with an internal scaling factor, so we
// need to divide by the scale to compensate.
func spaApplyScale(x, y int, sx, sy float64) (float64, float64) {
	if sx == 0 || sy == 0 {
		return float64(x), float64(y)
	}
	return float64(x) / sx, float64(y) / sy
}

// CdpSpaClickCmd clicks at canvas coordinates with SPA scaling correction.
type CdpSpaClickCmd struct {
	Image    string `arg:"" help:"Image name"`
	TabID    string `arg:"" help:"Tab ID (from cdp list)"`
	X        int    `arg:"" help:"X coordinate (in canvas/CDP screenshot space)"`
	Y        int    `arg:"" help:"Y coordinate (in canvas/CDP screenshot space)"`
	Button   string `long:"button" default:"left" help:"Mouse button (left, right, middle)"`
	Scale    string `long:"scale" help:"Manual scale correction 'scaleX,scaleY' (e.g., 0.824,0.836)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *CdpSpaClickCmd) Run() error {
	client, err := connectTab(c.Image, c.TabID, c.Instance)
	if err != nil {
		return err
	}
	defer client.Close()

	// Detect SPA and get scale.
	state, err := spaDetect(client)
	if err != nil {
		return err
	}

	sx, sy := state.ScaleX, state.ScaleY
	if c.Scale != "" {
		sx, sy, err = parseScale(c.Scale)
		if err != nil {
			return err
		}
	}

	inputX, inputY := spaApplyScale(c.X, c.Y, sx, sy)

	btn := c.Button
	if btn != "left" && btn != "right" && btn != "middle" {
		return fmt.Errorf("unknown button %q (valid: left, right, middle)", btn)
	}

	// Ensure overlayInput has focus (clicks still land on it).
	if err := spaEnsureFocus(client); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
	}

	// Dispatch mouse events: move → press → release.
	moveParams := map[string]any{
		"type": "mouseMoved",
		"x":    inputX,
		"y":    inputY,
	}
	if _, err := client.Call("Input.dispatchMouseEvent", moveParams); err != nil {
		return fmt.Errorf("dispatching mouseMoved: %w", err)
	}

	pressParams := map[string]any{
		"type":       "mousePressed",
		"x":          inputX,
		"y":          inputY,
		"button":     btn,
		"clickCount": 1,
	}
	if _, err := client.Call("Input.dispatchMouseEvent", pressParams); err != nil {
		return fmt.Errorf("dispatching mousePressed: %w", err)
	}

	releaseParams := map[string]any{
		"type":       "mouseReleased",
		"x":          inputX,
		"y":          inputY,
		"button":     btn,
		"clickCount": 1,
	}
	if _, err := client.Call("Input.dispatchMouseEvent", releaseParams); err != nil {
		return fmt.Errorf("dispatching mouseReleased: %w", err)
	}

	if sx != 0 && sy != 0 {
		fmt.Fprintf(os.Stderr, "SPA click: canvas (%d, %d) → input (%.0f, %.0f) [scale %.3f, %.3f] button=%s\n",
			c.X, c.Y, inputX, inputY, sx, sy, btn)
	} else {
		fmt.Fprintf(os.Stderr, "SPA click: (%d, %d) button=%s (no scale correction)\n", c.X, c.Y, btn)
	}
	return nil
}

// CdpSpaMouseCmd moves the pointer without clicking.
type CdpSpaMouseCmd struct {
	Image    string `arg:"" help:"Image name"`
	TabID    string `arg:"" help:"Tab ID"`
	X        int    `arg:"" help:"X coordinate (canvas space)"`
	Y        int    `arg:"" help:"Y coordinate (canvas space)"`
	Scale    string `long:"scale" help:"Manual scale correction 'scaleX,scaleY'"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *CdpSpaMouseCmd) Run() error {
	client, err := connectTab(c.Image, c.TabID, c.Instance)
	if err != nil {
		return err
	}
	defer client.Close()

	state, err := spaDetect(client)
	if err != nil {
		return err
	}

	sx, sy := state.ScaleX, state.ScaleY
	if c.Scale != "" {
		sx, sy, err = parseScale(c.Scale)
		if err != nil {
			return err
		}
	}

	inputX, inputY := spaApplyScale(c.X, c.Y, sx, sy)

	params := map[string]any{
		"type": "mouseMoved",
		"x":    inputX,
		"y":    inputY,
	}
	if _, err := client.Call("Input.dispatchMouseEvent", params); err != nil {
		return fmt.Errorf("dispatching mouseMoved: %w", err)
	}

	if sx != 0 && sy != 0 {
		fmt.Fprintf(os.Stderr, "SPA mouse: canvas (%d, %d) → input (%.0f, %.0f) [scale %.3f, %.3f]\n",
			c.X, c.Y, inputX, inputY, sx, sy)
	} else {
		fmt.Fprintf(os.Stderr, "SPA mouse: (%d, %d) (no scale correction)\n", c.X, c.Y)
	}
	return nil
}

// CdpSpaTypeCmd types text into the remote desktop via the SPA's overlayInput.
// Uses CDP Input.dispatchKeyEvent which bypasses local compositor and Chrome shortcuts.
type CdpSpaTypeCmd struct {
	Image    string `arg:"" help:"Image name"`
	TabID    string `arg:"" help:"Tab ID"`
	Text     string `arg:"" help:"Text to type"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *CdpSpaTypeCmd) Run() error {
	client, err := connectTab(c.Image, c.TabID, c.Instance)
	if err != nil {
		return err
	}
	defer client.Close()

	if _, err := spaDetect(client); err != nil {
		return err
	}

	if err := spaEnsureFocus(client); err != nil {
		return err
	}

	// Dispatch each character via CDP Input.dispatchKeyEvent.
	// SPA-specific: only send keyDown + keyUp (no "char" event).
	// The SPA's onkeydown handler captures keyDown and forwards to the
	// remote compositor. Sending "char" would cause double input.
	for _, ch := range c.Text {
		key := string(ch)
		if err := spaDispatchCharKeyDown(client, key); err != nil {
			return fmt.Errorf("dispatching keyDown for %q: %w", key, err)
		}
		if err := spaDispatchCharKeyUp(client, key); err != nil {
			return fmt.Errorf("dispatching keyUp for %q: %w", key, err)
		}
	}

	fmt.Fprintf(os.Stderr, "SPA typed %d characters\n", len(c.Text))
	return nil
}

// CdpSpaKeyCmd sends a single key press to the remote desktop via the SPA.
type CdpSpaKeyCmd struct {
	Image    string `arg:"" help:"Image name"`
	TabID    string `arg:"" help:"Tab ID"`
	KeyName  string `arg:"" help:"Key name (Return, Escape, Tab, BackSpace, F1-F12, Up, Down, Left, Right, etc.)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *CdpSpaKeyCmd) Run() error {
	client, err := connectTab(c.Image, c.TabID, c.Instance)
	if err != nil {
		return err
	}
	defer client.Close()

	if _, err := spaDetect(client); err != nil {
		return err
	}

	if err := spaEnsureFocus(client); err != nil {
		return err
	}

	key, ok := spaKeyMap[c.KeyName]
	if !ok {
		return fmt.Errorf("unknown key %q (valid: %s)", c.KeyName, spaKeyNames())
	}

	if err := spaDispatchKeyPress(client, key); err != nil {
		return fmt.Errorf("dispatching key %s: %w", c.KeyName, err)
	}

	fmt.Fprintf(os.Stderr, "SPA key: %s\n", c.KeyName)
	return nil
}

// CdpSpaKeyComboCmd sends a modifier key combination to the remote desktop.
// This bypasses the local compositor and Chrome — Super+e, Ctrl+T, Alt+F4 all pass through.
type CdpSpaKeyComboCmd struct {
	Image    string `arg:"" help:"Image name"`
	TabID    string `arg:"" help:"Tab ID"`
	Combo    string `arg:"" help:"Key combination (super+e, ctrl+t, alt+F4, ctrl+shift+t)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *CdpSpaKeyComboCmd) Run() error {
	client, err := connectTab(c.Image, c.TabID, c.Instance)
	if err != nil {
		return err
	}
	defer client.Close()

	if _, err := spaDetect(client); err != nil {
		return err
	}

	if err := spaEnsureFocus(client); err != nil {
		return err
	}

	// Parse combo: split on "+", last part is the main key, rest are modifiers.
	parts := strings.Split(strings.ToLower(c.Combo), "+")
	if len(parts) < 2 {
		return fmt.Errorf("key combo must have at least one modifier + key (e.g., super+e, ctrl+t)")
	}

	mainKeyName := parts[len(parts)-1]
	modNames := parts[:len(parts)-1]

	// Resolve main key.
	mainKey, ok := spaKeyMap[mainKeyName]
	if !ok {
		// Try as a single character.
		if len(mainKeyName) == 1 {
			ch := strings.ToLower(mainKeyName)
			mainKey = spaKeyDef{
				Key:                ch,
				Code:               "Key" + strings.ToUpper(ch),
				WindowsVirtualCode: int(strings.ToUpper(ch)[0]),
			}
		} else {
			return fmt.Errorf("unknown key %q in combo (valid: %s, or single character)", mainKeyName, spaKeyNames())
		}
	}

	// Resolve modifiers.
	var mods []spaKeyDef
	for _, modName := range modNames {
		mod, ok := spaModifierMap[modName]
		if !ok {
			return fmt.Errorf("unknown modifier %q (valid: ctrl, alt, shift, super/meta)", modName)
		}
		mods = append(mods, mod)
	}

	// Dispatch: modifiers down → main key down+up → modifiers up (reverse order).
	for _, mod := range mods {
		if err := spaDispatchKeyDown(client, mod); err != nil {
			return fmt.Errorf("modifier %s keyDown: %w", mod.Key, err)
		}
	}

	if err := spaDispatchKeyPress(client, mainKey); err != nil {
		return fmt.Errorf("key %s press: %w", mainKey.Key, err)
	}

	for i := len(mods) - 1; i >= 0; i-- {
		if err := spaDispatchKeyUp(client, mods[i]); err != nil {
			return fmt.Errorf("modifier %s keyUp: %w", mods[i].Key, err)
		}
	}

	fmt.Fprintf(os.Stderr, "SPA key-combo: %s\n", c.Combo)
	return nil
}

// CdpSpaStatusCmd shows the SPA state.
type CdpSpaStatusCmd struct {
	Image    string `arg:"" help:"Image name"`
	TabID    string `arg:"" help:"Tab ID"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *CdpSpaStatusCmd) Run() error {
	client, err := connectTab(c.Image, c.TabID, c.Instance)
	if err != nil {
		return err
	}
	defer client.Close()

	state, err := spaDetect(client)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Canvas:     %dx%d (#videoCanvas)\n", state.CanvasWidth, state.CanvasHeight)
	if state.ScaleX > 0 && state.ScaleY > 0 {
		fmt.Fprintf(os.Stderr, "Scale:      %.3fx / %.3fy\n", state.ScaleX, state.ScaleY)
	} else {
		fmt.Fprintf(os.Stderr, "Scale:      not detected (use --scale to set manually)\n")
	}
	overlayStatus := "not found"
	if state.HasOverlay {
		if state.Focused {
			overlayStatus = state.OverlayID + " (focused)"
		} else {
			overlayStatus = state.OverlayID + " (not focused)"
		}
	}
	fmt.Fprintf(os.Stderr, "Input:      %s\n", overlayStatus)
	fmt.Fprintf(os.Stderr, "Secure:     %v\n", state.Secure)
	fmt.Fprintf(os.Stderr, "Decoder:    %v\n", state.HasDecoder)
	return nil
}

// --- SPA Key Definitions ---

// spaKeyDef maps a key name to CDP Input.dispatchKeyEvent properties.
type spaKeyDef struct {
	Key                string
	Code               string
	WindowsVirtualCode int
}

// spaDispatchCharKeyDown sends a keyDown event for a printable character.
// Includes key, code, text, and windowsVirtualKeyCode for proper SPA handling.
func spaDispatchCharKeyDown(client *CDPClient, ch string) error {
	upper := strings.ToUpper(ch)
	code := "Key" + upper
	if ch == " " {
		code = "Space"
	} else if ch == "-" {
		code = "Minus"
	} else if ch >= "0" && ch <= "9" {
		code = "Digit" + ch
	}
	params := map[string]any{
		"type": "keyDown",
		"key":  ch,
		"code": code,
		"text": ch,
	}
	if len(upper) == 1 && upper[0] >= 'A' && upper[0] <= 'Z' {
		params["windowsVirtualKeyCode"] = int(upper[0])
	}
	_, err := client.Call("Input.dispatchKeyEvent", params)
	return err
}

// spaDispatchCharKeyUp sends a keyUp event for a printable character.
func spaDispatchCharKeyUp(client *CDPClient, ch string) error {
	upper := strings.ToUpper(ch)
	code := "Key" + upper
	if ch == " " {
		code = "Space"
	} else if ch == "-" {
		code = "Minus"
	} else if ch >= "0" && ch <= "9" {
		code = "Digit" + ch
	}
	params := map[string]any{
		"type": "keyUp",
		"key":  ch,
		"code": code,
	}
	_, err := client.Call("Input.dispatchKeyEvent", params)
	return err
}

// spaDispatchKeyDown sends a keyDown event.
func spaDispatchKeyDown(client *CDPClient, key spaKeyDef) error {
	params := map[string]any{
		"type":                  "keyDown",
		"key":                   key.Key,
		"code":                  key.Code,
		"windowsVirtualKeyCode": key.WindowsVirtualCode,
	}
	_, err := client.Call("Input.dispatchKeyEvent", params)
	return err
}

// spaDispatchKeyUp sends a keyUp event.
func spaDispatchKeyUp(client *CDPClient, key spaKeyDef) error {
	params := map[string]any{
		"type":                  "keyUp",
		"key":                   key.Key,
		"code":                  key.Code,
		"windowsVirtualKeyCode": key.WindowsVirtualCode,
	}
	_, err := client.Call("Input.dispatchKeyEvent", params)
	return err
}

// spaDispatchKeyPress sends keyDown + keyUp for a single key.
func spaDispatchKeyPress(client *CDPClient, key spaKeyDef) error {
	if err := spaDispatchKeyDown(client, key); err != nil {
		return err
	}
	return spaDispatchKeyUp(client, key)
}

// spaKeyMap maps key names (matching wl.go naming) to CDP key properties.
var spaKeyMap = map[string]spaKeyDef{
	"return":    {Key: "Enter", Code: "Enter", WindowsVirtualCode: 13},
	"enter":     {Key: "Enter", Code: "Enter", WindowsVirtualCode: 13},
	"escape":    {Key: "Escape", Code: "Escape", WindowsVirtualCode: 27},
	"tab":       {Key: "Tab", Code: "Tab", WindowsVirtualCode: 9},
	"backspace": {Key: "Backspace", Code: "Backspace", WindowsVirtualCode: 8},
	"delete":    {Key: "Delete", Code: "Delete", WindowsVirtualCode: 46},
	"home":      {Key: "Home", Code: "Home", WindowsVirtualCode: 36},
	"end":       {Key: "End", Code: "End", WindowsVirtualCode: 35},
	"page_up":   {Key: "PageUp", Code: "PageUp", WindowsVirtualCode: 33},
	"page_down": {Key: "PageDown", Code: "PageDown", WindowsVirtualCode: 34},
	"up":        {Key: "ArrowUp", Code: "ArrowUp", WindowsVirtualCode: 38},
	"down":      {Key: "ArrowDown", Code: "ArrowDown", WindowsVirtualCode: 40},
	"left":      {Key: "ArrowLeft", Code: "ArrowLeft", WindowsVirtualCode: 37},
	"right":     {Key: "ArrowRight", Code: "ArrowRight", WindowsVirtualCode: 39},
	"insert":    {Key: "Insert", Code: "Insert", WindowsVirtualCode: 45},
	"space":     {Key: " ", Code: "Space", WindowsVirtualCode: 32},
	"f1":        {Key: "F1", Code: "F1", WindowsVirtualCode: 112},
	"f2":        {Key: "F2", Code: "F2", WindowsVirtualCode: 113},
	"f3":        {Key: "F3", Code: "F3", WindowsVirtualCode: 114},
	"f4":        {Key: "F4", Code: "F4", WindowsVirtualCode: 115},
	"f5":        {Key: "F5", Code: "F5", WindowsVirtualCode: 116},
	"f6":        {Key: "F6", Code: "F6", WindowsVirtualCode: 117},
	"f7":        {Key: "F7", Code: "F7", WindowsVirtualCode: 118},
	"f8":        {Key: "F8", Code: "F8", WindowsVirtualCode: 119},
	"f9":        {Key: "F9", Code: "F9", WindowsVirtualCode: 120},
	"f10":       {Key: "F10", Code: "F10", WindowsVirtualCode: 121},
	"f11":       {Key: "F11", Code: "F11", WindowsVirtualCode: 122},
	"f12":       {Key: "F12", Code: "F12", WindowsVirtualCode: 123},
}

// spaModifierMap maps modifier names to CDP key properties.
var spaModifierMap = map[string]spaKeyDef{
	"ctrl":    {Key: "Control", Code: "ControlLeft", WindowsVirtualCode: 17},
	"control": {Key: "Control", Code: "ControlLeft", WindowsVirtualCode: 17},
	"alt":     {Key: "Alt", Code: "AltLeft", WindowsVirtualCode: 18},
	"shift":   {Key: "Shift", Code: "ShiftLeft", WindowsVirtualCode: 16},
	"super":   {Key: "Meta", Code: "MetaLeft", WindowsVirtualCode: 91},
	"meta":    {Key: "Meta", Code: "MetaLeft", WindowsVirtualCode: 91},
	"win":     {Key: "Meta", Code: "MetaLeft", WindowsVirtualCode: 91},
	"logo":    {Key: "Meta", Code: "MetaLeft", WindowsVirtualCode: 91},
}

// spaKeyNames returns a sorted list of valid key names.
func spaKeyNames() string {
	seen := make(map[string]bool)
	var names []string
	for name := range spaKeyMap {
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	// Sort for consistent output.
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			if names[i] > names[j] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}
	return strings.Join(names, ", ")
}
