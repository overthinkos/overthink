package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tebeka/selenium"
)

// appium.go implements `charly eval appium …` — the host-side Appium
// WebDriver client. The host `ov` binary connects to the container's
// host-published Appium port (container :4723 → host's HOST_PORT:4723,
// e.g. 35001 on eval-android-emulator-pod) using github.com/tebeka/selenium
// (W3C WebDriver client, talks Appium 2.x and 3.x because W3C is stable).
//
// Appium-specific endpoints not in the standard WebDriver surface
// (install_app, is_app_installed, remove_app) route through the W3C
// escape hatch: driver.ExecuteScript("mobile: installApp", ...).
//
// Session lifecycle: a persistent JSON file at
// ~/.cache/charly/appium/sessions/<image>[_<instance>].json carries the
// session id between separate `charly eval appium …` invocations. See
// appium_session.go for the file management.

// AppiumCmd groups the `charly eval appium …` surface across three tiers:
//
//	Tier 1 — typed leaves for the hot discrete ops (status/session/find/click/
//	         send-keys/screenshot/install-app + get-text/get-attribute/clear/
//	         find-all/source/back);
//	Tier 2 — per-class sugar groups (gesture/app/key/device), mirroring the
//	         wl sway/overlay nested-group pattern;
//	Tier 3 — the generic escape hatch (execute = mobile:/JS, raw = any W3C HTTP
//	         call) — `raw` alone reaches 100% of the WebDriver + UiAutomator2
//	         surface.
type AppiumCmd struct {
	Status        AppiumStatusCmd        `cmd:"" help:"Check Appium server health (/status endpoint)"`
	SessionCreate AppiumSessionCreateCmd `cmd:"session-create" help:"Create a W3C WebDriver session and persist its id"`
	SessionDelete AppiumSessionDeleteCmd `cmd:"session-delete" help:"Terminate the persisted session and remove the file"`
	InstallApp    AppiumInstallAppCmd    `cmd:"install-app" help:"Install an APK via mobile:installApp"`
	Find          AppiumFindCmd          `cmd:"" help:"Find an element by locator strategy + selector"`
	Click         AppiumClickCmd         `cmd:"" help:"Click an element matched by locator"`
	SendKeys      AppiumSendKeysCmd      `cmd:"send-keys" help:"Type text into an element matched by locator"`
	Screenshot    AppiumScreenshotCmd    `cmd:"" help:"Capture a PNG screenshot via /session/<id>/screenshot"`

	// Tier 1 — element introspection / navigation
	GetText      AppiumGetTextCmd      `cmd:"get-text" help:"Read an element's text (find + GET .../text)"`
	GetAttribute AppiumGetAttributeCmd `cmd:"get-attribute" help:"Read an element attribute (checked/enabled/text/...)"`
	Clear        AppiumClearCmd        `cmd:"" help:"Clear an editable element"`
	FindAll      AppiumFindAllCmd      `cmd:"find-all" help:"Find all matching elements; print count + ids"`
	Source       AppiumSourceCmd       `cmd:"" help:"Dump the UI hierarchy XML (GET /source)"`
	Back         AppiumBackCmd         `cmd:"" help:"Navigate back (POST /back)"`

	// Tier 2 — per-class sugar groups (nested → flat `gesture-*` etc. in eval YAML)
	Gesture AppiumGestureCmd `cmd:"" help:"UiAutomator2 touch gestures (tap/swipe/scroll/drag/...)"`
	App     AppiumAppCmd     `cmd:"" help:"App lifecycle + activity (activate/start-activity/is-installed/...)"`
	Key     AppiumKeyCmd     `cmd:"" help:"Keys + keyboard (press/hide/shown)"`
	Device  AppiumDeviceCmd  `cmd:"" help:"Device/system + WebView context (info/orientation/contexts/...)"`

	// Tier 3 — generic escape hatch
	Execute AppiumExecuteCmd `cmd:"" help:"Run any mobile: command / JS via execute/sync"`
	Raw     AppiumRawCmd     `cmd:"" help:"Issue any raw W3C WebDriver HTTP call"`
}

// AppiumGestureCmd — `charly eval appium gesture <op>`; flat `gesture-<op>` in eval YAML.
type AppiumGestureCmd struct {
	Tap        AppiumGestureTapCmd        `cmd:"" help:"Single tap (mobile: clickGesture)"`
	DoubleTap  AppiumGestureDoubleTapCmd  `cmd:"double-tap" help:"Double tap (mobile: doubleClickGesture)"`
	LongPress  AppiumGestureLongPressCmd  `cmd:"long-press" help:"Long press (mobile: longClickGesture)"`
	Drag       AppiumGestureDragCmd       `cmd:"" help:"Drag (mobile: dragGesture)"`
	Swipe      AppiumGestureSwipeCmd      `cmd:"" help:"Swipe (mobile: swipeGesture)"`
	Scroll     AppiumGestureScrollCmd     `cmd:"" help:"Scroll (mobile: scrollGesture)"`
	Fling      AppiumGestureFlingCmd      `cmd:"" help:"Fling (mobile: flingGesture)"`
	PinchOpen  AppiumGesturePinchOpenCmd  `cmd:"pinch-open" help:"Pinch open (mobile: pinchOpenGesture)"`
	PinchClose AppiumGesturePinchCloseCmd `cmd:"pinch-close" help:"Pinch close (mobile: pinchCloseGesture)"`
}

// AppiumAppCmd — `charly eval appium app <op>`; flat `app-<op>` in eval YAML.
type AppiumAppCmd struct {
	StartActivity   AppiumAppStartActivityCmd   `cmd:"start-activity" help:"Launch an activity (mobile: startActivity, intent form)"`
	Activate        AppiumAppActivateCmd        `cmd:"" help:"Bring an app to foreground (mobile: activateApp)"`
	Terminate       AppiumAppTerminateCmd       `cmd:"" help:"Stop an app (mobile: terminateApp)"`
	Remove          AppiumAppRemoveCmd          `cmd:"" help:"Uninstall an app (mobile: removeApp)"`
	Clear           AppiumAppClearCmd           `cmd:"" help:"Clear app data (mobile: clearApp)"`
	IsInstalled     AppiumAppIsInstalledCmd     `cmd:"is-installed" help:"Check if an app is installed (mobile: isAppInstalled)"`
	State           AppiumAppStateCmd           `cmd:"" help:"Query app state (mobile: queryAppState)"`
	CurrentActivity AppiumAppCurrentActivityCmd `cmd:"current-activity" help:"Current foreground activity"`
	CurrentPackage  AppiumAppCurrentPackageCmd  `cmd:"current-package" help:"Current foreground package"`
}

// AppiumKeyCmd — `charly eval appium key <op>`; flat `key-<op>` in eval YAML.
type AppiumKeyCmd struct {
	Press AppiumKeyPressCmd `cmd:"" help:"Press an Android keycode (mobile: pressKey)"`
	Hide  AppiumKeyHideCmd  `cmd:"" help:"Hide the soft keyboard (mobile: hideKeyboard)"`
	Shown AppiumKeyShownCmd `cmd:"" help:"Report whether the soft keyboard is shown"`
}

// AppiumDeviceCmd — `charly eval appium device <op>`; flat `device-<op>` in eval YAML.
type AppiumDeviceCmd struct {
	Info           AppiumDeviceInfoCmd           `cmd:"" help:"Device info (mobile: deviceInfo)"`
	Battery        AppiumDeviceBatteryCmd        `cmd:"" help:"Battery info (mobile: batteryInfo)"`
	Time           AppiumDeviceTimeCmd           `cmd:"" help:"Device time (mobile: getDeviceTime)"`
	Orientation    AppiumDeviceOrientationCmd    `cmd:"" help:"Get screen orientation"`
	SetOrientation AppiumDeviceSetOrientationCmd `cmd:"set-orientation" help:"Set screen orientation (PORTRAIT|LANDSCAPE)"`
	Notifications  AppiumDeviceNotificationsCmd  `cmd:"" help:"Open the notification shade (mobile: openNotifications)"`
	GetClipboard   AppiumDeviceGetClipboardCmd   `cmd:"get-clipboard" help:"Read the clipboard"`
	SetClipboard   AppiumDeviceSetClipboardCmd   `cmd:"set-clipboard" help:"Set the clipboard text"`
	Contexts       AppiumDeviceContextsCmd       `cmd:"" help:"List contexts (NATIVE_APP, WEBVIEW_*)"`
	Context        AppiumDeviceContextCmd        `cmd:"" help:"Get or set the current context (WebView switch)"`
}

// appiumCommonFlags carries the deploy-addressing fields every leaf needs.
type appiumCommonFlags struct {
	Instance string `short:"i" long:"instance" help:"Instance name"`
	BasePath string `long:"base-path" default:"/wd/hub" help:"Appium server base path (matches --base-path on the server)"`
}

// appiumBaseURL resolves the running container's Appium server URL. Reads
// HOST_PORT:4723 from podman inspect, prepends 127.0.0.1 + the configured
// base path. Returns the URL ready to hand to selenium.NewRemote.
func appiumBaseURL(image, instance, basePath string) (string, error) {
	engine, containerName, err := resolveContainer(image, instance)
	if err != nil {
		return "", err
	}
	insp, err := InspectContainer(engine, containerName)
	if err != nil {
		return "", fmt.Errorf("inspect %s: %w", containerName, err)
	}
	port, err := findHostPort(insp, 4723)
	if err != nil {
		return "", err
	}
	if basePath == "" {
		basePath = "/wd/hub"
	}
	if !strings.HasPrefix(basePath, "/") {
		basePath = "/" + basePath
	}
	return fmt.Sprintf("http://127.0.0.1:%d%s", port, basePath), nil
}

// ---------------------------------------------------------------------------
// appium status — server health check (no session needed)
// ---------------------------------------------------------------------------

// AppiumStatusCmd: `charly eval appium status <image>` — issues GET <base>/status
// and prints the response body. Appium 3.x returns `{"value":{"ready":true,...}}`
// on a healthy server. Stays away from the SDK because /status is the one
// endpoint that doesn't need a session — bypassing selenium.NewRemote keeps
// it cheap and surfaces any HTTP transport problems directly.
type AppiumStatusCmd struct {
	Image string `arg:"" help:"Image name"`
	appiumCommonFlags
	Timeout time.Duration `long:"timeout" default:"10s" help:"HTTP timeout"`
}

func (c *AppiumStatusCmd) Run() error {
	base, err := appiumBaseURL(c.Image, c.Instance, c.BasePath)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: c.Timeout}
	resp, err := client.Get(base + "/status")
	if err != nil {
		return fmt.Errorf("GET %s/status: %w", base, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	fmt.Println(string(body))
	if resp.StatusCode != 200 {
		return fmt.Errorf("appium status returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// ---------------------------------------------------------------------------
// appium session-create — W3C NewRemote + persistence
// ---------------------------------------------------------------------------

// AppiumSessionCreateCmd: `charly eval appium session-create <image> --caps <json>`
// — parses the W3C capabilities JSON, creates a session via the selenium
// SDK (which wraps Capabilities under W3C `alwaysMatch`), persists the
// session id at ~/.cache/charly/appium/sessions/<image>[_<instance>].json,
// and prints the session id.
//
// Authoring forms accepted for --caps:
//   - Flat:     `{"platformName":"Android","appium:automationName":"UiAutomator2"}`
//   - W3C-wrapped: `{"alwaysMatch":{"platformName":"Android",...}}`
//
// If `alwaysMatch` is present at the top level we extract its contents to
// avoid double-wrapping (the SDK adds its own `alwaysMatch` layer at
// NewSession time; doubly-wrapping would silently fail caps matching).
type AppiumSessionCreateCmd struct {
	Image string `arg:"" help:"Image name"`
	Caps  string `long:"caps" required:"" help:"W3C capabilities JSON. Use @path.json to read from file. The 'alwaysMatch' wrapper is optional — both forms work."`
	appiumCommonFlags
}

func (c *AppiumSessionCreateCmd) Run() error {
	capsRaw := c.Caps
	if strings.HasPrefix(capsRaw, "@") {
		data, err := os.ReadFile(capsRaw[1:])
		if err != nil {
			return fmt.Errorf("reading caps file %s: %w", capsRaw[1:], err)
		}
		capsRaw = string(data)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(capsRaw), &parsed); err != nil {
		return fmt.Errorf("parsing caps JSON: %w", err)
	}
	// Unwrap alwaysMatch if present — the SDK's newW3CCapabilities adds
	// its own wrapper, so passing a pre-wrapped map would nest the
	// alwaysMatch twice and break caps matching server-side.
	if inner, ok := parsed["alwaysMatch"].(map[string]interface{}); ok {
		parsed = inner
	}
	base, err := appiumBaseURL(c.Image, c.Instance, c.BasePath)
	if err != nil {
		return err
	}
	// Delete any previous session for this image+instance first (best
	// effort — a stale file from a crashed run shouldn't block creation).
	if prev, _ := loadAppiumSession(c.Image, c.Instance); prev != nil {
		_ = appiumDeleteSessionRemote(base, prev.SessionID)
		_ = deleteAppiumSession(c.Image, c.Instance)
	}
	caps := selenium.Capabilities(parsed)
	wd, err := selenium.NewRemote(caps, base)
	if err != nil {
		return fmt.Errorf("creating Appium session at %s: %w", base, err)
	}
	sid := wd.SessionID()
	if sid == "" {
		return fmt.Errorf("Appium session created but SessionID was empty")
	}
	sess := &AppiumSession{
		SessionID: sid,
		BaseURL:   base,
		CreatedAt: time.Now().UTC(),
		Image:     c.Image,
		Instance:  c.Instance,
		Caps:      parsed,
	}
	if err := saveAppiumSession(sess); err != nil {
		// Try to terminate the session we just created so we don't leak.
		_ = wd.Quit()
		return err
	}
	fmt.Println(sid)
	return nil
}

// ---------------------------------------------------------------------------
// appium session-delete — close + remove file
// ---------------------------------------------------------------------------

// AppiumSessionDeleteCmd: `charly eval appium session-delete <image>`. No-op
// if the session file is missing. Errors during DELETE are warnings (the
// server may have GC'd the session already) but the file is still removed.
type AppiumSessionDeleteCmd struct {
	Image string `arg:"" help:"Image name"`
	appiumCommonFlags
}

func (c *AppiumSessionDeleteCmd) Run() error {
	sess, err := loadAppiumSession(c.Image, c.Instance)
	if err != nil {
		return err
	}
	if sess == nil {
		fmt.Println("no session to delete")
		return nil
	}
	if err := appiumDeleteSessionRemote(sess.BaseURL, sess.SessionID); err != nil {
		fmt.Fprintf(os.Stderr, "warning: DELETE /session/%s: %v (continuing)\n", sess.SessionID, err)
	}
	if err := deleteAppiumSession(c.Image, c.Instance); err != nil {
		return err
	}
	fmt.Println("deleted")
	return nil
}

// appiumDeleteSessionRemote issues the bare DELETE /session/<id> via plain
// HTTP. Bypasses the SDK because reconstructing a WebDriver against an
// existing session id isn't a first-class operation in selenium SDK.
func appiumDeleteSessionRemote(base, sessionID string) error {
	req, err := http.NewRequest(http.MethodDelete, base+"/session/"+sessionID, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 && resp.StatusCode != 404 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// loadActiveSession is the shared "session must exist" helper used by all
// non-lifecycle verbs. Returns a user-friendly error pointing at
// session-create when the file is missing.
func loadActiveSession(image, instance string) (*AppiumSession, error) {
	sess, err := loadAppiumSession(image, instance)
	if err != nil {
		return nil, err
	}
	if sess == nil {
		return nil, fmt.Errorf("no Appium session for image %q (instance=%q) — run `charly eval appium session-create %s --caps <json>` first", image, instance, image)
	}
	return sess, nil
}

// w3cSession is a raw-HTTP client bound to an existing WebDriver session.
// The selenium SDK doesn't expose a "construct against an existing id"
// constructor (NewRemote always POSTs to create), so for every operation
// AFTER session-create we use the W3C wire protocol directly. W3C is
// stable HTTP/JSON — no SDK needed for the small surface we exercise.
type w3cSession struct {
	BaseURL   string
	SessionID string
	HTTP      *http.Client
}

func newW3CSession(base, sessionID string) *w3cSession {
	return &w3cSession{
		BaseURL:   strings.TrimRight(base, "/"),
		SessionID: sessionID,
		HTTP:      &http.Client{Timeout: 60 * time.Second},
	}
}

// call issues a JSON-bodied request to /session/<id>/<endpoint> and
// returns the W3C "value" field of the response. body=nil means GET; any
// other body means POST/DELETE based on method.
func (s *w3cSession) call(method, endpoint string, body interface{}) (json.RawMessage, error) {
	u := s.BaseURL + "/session/" + url.PathEscape(s.SessionID) + endpoint
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, u, reqBody)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, u, err)
	}
	defer resp.Body.Close()
	respBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%s %s: HTTP %d: %s", method, u, resp.StatusCode, strings.TrimSpace(string(respBytes)))
	}
	var envelope struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(respBytes, &envelope); err != nil {
		return nil, fmt.Errorf("decode %s %s response: %w", method, u, err)
	}
	return envelope.Value, nil
}

// findElement returns the W3C element id for the first match. The element
// id is wrapped under a stable known key in the W3C spec: each element
// returns `{"element-6066-11e4-a52e-4f735466cecf": "<id>"}`.
const w3cElementKey = "element-6066-11e4-a52e-4f735466cecf"

func (s *w3cSession) findElement(strategy, selector string) (string, error) {
	by, err := strategyToBy(strategy)
	if err != nil {
		return "", err
	}
	body := map[string]string{"using": by, "value": selector}
	resp, err := s.call(http.MethodPost, "/element", body)
	if err != nil {
		return "", fmt.Errorf("find %s=%q: %w", strategy, selector, err)
	}
	var elemMap map[string]string
	if err := json.Unmarshal(resp, &elemMap); err != nil {
		return "", fmt.Errorf("decode element id: %w", err)
	}
	id, ok := elemMap[w3cElementKey]
	if !ok {
		return "", fmt.Errorf("response missing %s key: %s", w3cElementKey, string(resp))
	}
	return id, nil
}

func (s *w3cSession) click(elemID string) error {
	_, err := s.call(http.MethodPost, "/element/"+url.PathEscape(elemID)+"/click", map[string]string{})
	return err
}

func (s *w3cSession) sendKeys(elemID, text string) error {
	_, err := s.call(http.MethodPost, "/element/"+url.PathEscape(elemID)+"/value", map[string]interface{}{"text": text})
	return err
}

func (s *w3cSession) screenshot() ([]byte, error) {
	resp, err := s.call(http.MethodGet, "/screenshot", nil)
	if err != nil {
		return nil, err
	}
	var b64 string
	if err := json.Unmarshal(resp, &b64); err != nil {
		return nil, fmt.Errorf("decode screenshot value: %w", err)
	}
	return base64.StdEncoding.DecodeString(b64)
}

func (s *w3cSession) executeScript(script string, args []interface{}) (json.RawMessage, error) {
	body := map[string]interface{}{
		"script": script,
		"args":   args,
	}
	return s.call(http.MethodPost, "/execute/sync", body)
}

// W3C element/session helpers — thin wrappers over call(), all reusing the same
// transport + value-unwrap. These back the Tier-1 typed leaves + the device
// group's W3C-endpoint ops (contexts/context/orientation/source/back).

func (s *w3cSession) elementText(elemID string) (string, error) {
	resp, err := s.call(http.MethodGet, "/element/"+url.PathEscape(elemID)+"/text", nil)
	if err != nil {
		return "", err
	}
	var text string
	if err := json.Unmarshal(resp, &text); err != nil {
		return "", fmt.Errorf("decode element text: %w", err)
	}
	return text, nil
}

func (s *w3cSession) elementAttribute(elemID, name string) (string, error) {
	resp, err := s.call(http.MethodGet, "/element/"+url.PathEscape(elemID)+"/attribute/"+url.PathEscape(name), nil)
	if err != nil {
		return "", err
	}
	// Attributes can come back as a JSON string ("true") or null.
	var v string
	if err := json.Unmarshal(resp, &v); err == nil {
		return v, nil
	}
	return strings.TrimSpace(string(resp)), nil
}

func (s *w3cSession) clearElement(elemID string) error {
	_, err := s.call(http.MethodPost, "/element/"+url.PathEscape(elemID)+"/clear", map[string]interface{}{})
	return err
}

func (s *w3cSession) findElements(strategy, selector string) ([]string, error) {
	by, err := strategyToBy(strategy)
	if err != nil {
		return nil, err
	}
	resp, err := s.call(http.MethodPost, "/elements", map[string]string{"using": by, "value": selector})
	if err != nil {
		return nil, fmt.Errorf("find-all %s=%q: %w", strategy, selector, err)
	}
	var elems []map[string]string
	if err := json.Unmarshal(resp, &elems); err != nil {
		return nil, fmt.Errorf("decode elements: %w", err)
	}
	ids := make([]string, 0, len(elems))
	for _, e := range elems {
		if id, ok := e[w3cElementKey]; ok {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func (s *w3cSession) source() (string, error) {
	resp, err := s.call(http.MethodGet, "/source", nil)
	if err != nil {
		return "", err
	}
	var src string
	if err := json.Unmarshal(resp, &src); err != nil {
		return "", fmt.Errorf("decode source: %w", err)
	}
	return src, nil
}

// navigateBack uses the session-scoped W3C /back endpoint (not /session/<id>/back
// nesting — call() already injects /session/<id>).
func (s *w3cSession) navigateBack() error {
	_, err := s.call(http.MethodPost, "/back", map[string]interface{}{})
	return err
}

func (s *w3cSession) contexts() ([]string, error) {
	resp, err := s.call(http.MethodGet, "/contexts", nil)
	if err != nil {
		return nil, err
	}
	var ctxs []string
	if err := json.Unmarshal(resp, &ctxs); err != nil {
		return nil, fmt.Errorf("decode contexts: %w", err)
	}
	return ctxs, nil
}

func (s *w3cSession) currentContext() (string, error) {
	resp, err := s.call(http.MethodGet, "/context", nil)
	if err != nil {
		return "", err
	}
	var name string
	_ = json.Unmarshal(resp, &name)
	return name, nil
}

func (s *w3cSession) setContext(name string) error {
	_, err := s.call(http.MethodPost, "/context", map[string]interface{}{"name": name})
	return err
}

func (s *w3cSession) orientation() (string, error) {
	resp, err := s.call(http.MethodGet, "/orientation", nil)
	if err != nil {
		return "", err
	}
	var o string
	_ = json.Unmarshal(resp, &o)
	return o, nil
}

func (s *w3cSession) setOrientation(o string) error {
	_, err := s.call(http.MethodPost, "/orientation", map[string]interface{}{"orientation": o})
	return err
}

// rawCall issues an arbitrary W3C call relative to /session/<id> (the path the
// caller supplies is appended after /session/<id>). Backs `appium raw`.
func (s *w3cSession) rawCall(method, path string, body interface{}) (json.RawMessage, error) {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return s.call(method, path, body)
}

// printW3CValue prints a W3C value cleanly: JSON strings are unquoted, objects/
// arrays print as compact JSON, null/empty prints the fallback token.
func printW3CValue(result json.RawMessage, fallback string) {
	trimmed := strings.TrimSpace(string(result))
	if trimmed == "" || trimmed == "null" {
		if fallback != "" {
			fmt.Println(fallback)
		}
		return
	}
	var str string
	if json.Unmarshal(result, &str) == nil {
		fmt.Println(str)
		return
	}
	fmt.Println(trimmed)
}

// ---------------------------------------------------------------------------
// appium install-app — mobile:installApp escape hatch
// ---------------------------------------------------------------------------

// AppiumInstallAppCmd: `charly eval appium install-app <image> --apk <host-path>`.
// `--apk` is a HOST filesystem path — symmetric with `adb: install`. The
// in-container Appium server's `mobile: installApp` requires `appPath` (a
// path IT can read; the base64 `{"app": …}` form is rejected with HTTP 400
// "required parameter is missing: appPath"), so the host APK is first staged
// INTO the container via `<engine> cp` to a temp path, then installApp is
// called with that in-container path, then the temp file is removed. This
// makes the verb self-contained: no bind-mount and no external staging step
// are required (the eval-android-emulator-pod bed mounts no host dir).
type AppiumInstallAppCmd struct {
	Image string `arg:"" help:"Image name"`
	Apk   string `long:"apk" required:"" help:"APK path on the HOST (staged into the container automatically, like adb install)"`
	appiumCommonFlags
}

func (c *AppiumInstallAppCmd) Run() error {
	// Fail fast on a bad host path before touching session/container state.
	if _, statErr := os.Stat(c.Apk); statErr != nil {
		return fmt.Errorf("appium install-app: APK not found on host: %w", statErr)
	}
	sess, err := loadActiveSession(c.Image, c.Instance)
	if err != nil {
		return err
	}
	// Stage the host APK into the container so the in-container Appium
	// server can read it via appPath. Same container the port resolution
	// uses (resolveContainer), so this works wherever `appium: status`
	// already works.
	engine, containerName, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	remote := "/tmp/charly-appium-" + filepath.Base(c.Apk)
	if out, cpErr := exec.Command(engine, "cp", c.Apk, containerName+":"+remote).CombinedOutput(); cpErr != nil {
		return fmt.Errorf("staging APK into %s: %v: %s", containerName, cpErr, strings.TrimSpace(string(out)))
	}
	defer exec.Command(engine, "exec", containerName, "rm", "-f", remote).Run()

	s := newW3CSession(sess.BaseURL, sess.SessionID)
	args := []interface{}{map[string]interface{}{"appPath": remote}}
	result, err := s.executeScript("mobile: installApp", args)
	if err != nil {
		return fmt.Errorf("mobile: installApp %s (host %s): %w", remote, c.Apk, err)
	}
	if len(result) > 0 && string(result) != "null" {
		fmt.Println(string(result))
	} else {
		fmt.Println("installed")
	}
	return nil
}

// ---------------------------------------------------------------------------
// appium find — element discovery + id print
// ---------------------------------------------------------------------------

// AppiumFindCmd: `charly eval appium find <image> --selector <expr> [--strategy STRAT]`
// — finds the first element matching selector + strategy, prints the
// W3C element id. Useful for chaining (a subsequent shell-driven call
// could use the id), but in practice the recommended pattern is to
// inline find+click via `charly eval appium click`.
// appiumElementFlags is the shared element-targeting flag set for find / click /
// send-keys / get-text / get-attribute / clear / find-all (R3: one definition).
type appiumElementFlags struct {
	Image    string `arg:"" help:"Image name"`
	Selector string `long:"selector" required:"" help:"Locator value"`
	Strategy string `long:"strategy" default:"xpath" help:"Locator strategy: xpath, id, accessibility-id, class-name, android-uiautomator, name, css"`
	Session  string `long:"session" help:"Override the persisted session id"`
	appiumCommonFlags
}

// appiumSessionFlags is the shared image+session flag set for leaves that need
// only an active session (source / back / app-current-* / device-* / key-*).
type appiumSessionFlags struct {
	Image   string `arg:"" help:"Image name"`
	Session string `long:"session" help:"Override the persisted session id"`
	appiumCommonFlags
}

func (f *appiumSessionFlags) session() (*w3cSession, error) {
	return resolveW3CSession(f.Image, f.Instance, f.Session)
}

type AppiumFindCmd struct {
	appiumElementFlags
}

func (c *AppiumFindCmd) Run() error {
	s, err := resolveW3CSession(c.Image, c.Instance, c.Session)
	if err != nil {
		return err
	}
	id, err := s.findElement(c.Strategy, c.Selector)
	if err != nil {
		return err
	}
	fmt.Println(id)
	return nil
}

// resolveW3CSession reads the session file unless an explicit --session
// override was passed, and returns a w3cSession ready for operations.
func resolveW3CSession(image, instance, override string) (*w3cSession, error) {
	sess, err := loadActiveSession(image, instance)
	if err != nil {
		return nil, err
	}
	sid := sess.SessionID
	if override != "" {
		sid = override
	}
	return newW3CSession(sess.BaseURL, sid), nil
}

// strategyToBy maps the charly authoring surface to the SDK / W3C constants.
// `accessibility-id` is Appium-specific; the others are W3C standard.
func strategyToBy(strategy string) (string, error) {
	switch strings.ToLower(strategy) {
	case "", "xpath":
		return selenium.ByXPATH, nil
	case "id":
		return selenium.ByID, nil
	case "accessibility-id":
		return "accessibility id", nil
	case "class-name":
		return selenium.ByClassName, nil
	case "android-uiautomator":
		return "-android uiautomator", nil
	case "name":
		return selenium.ByName, nil
	case "css":
		return selenium.ByCSSSelector, nil
	}
	return "", fmt.Errorf("unknown strategy %q (allowed: xpath, id, accessibility-id, class-name, android-uiautomator, name, css)", strategy)
}

// ---------------------------------------------------------------------------
// appium click — find + click in one call
// ---------------------------------------------------------------------------

type AppiumClickCmd struct {
	appiumElementFlags
}

func (c *AppiumClickCmd) Run() error {
	s, err := resolveW3CSession(c.Image, c.Instance, c.Session)
	if err != nil {
		return err
	}
	id, err := s.findElement(c.Strategy, c.Selector)
	if err != nil {
		return err
	}
	if err := s.click(id); err != nil {
		return fmt.Errorf("click %s=%q: %w", c.Strategy, c.Selector, err)
	}
	fmt.Println("clicked")
	return nil
}

// ---------------------------------------------------------------------------
// appium send-keys — find + type
// ---------------------------------------------------------------------------

type AppiumSendKeysCmd struct {
	appiumElementFlags
	Text string `long:"text" required:"" help:"Text to type"`
}

func (c *AppiumSendKeysCmd) Run() error {
	s, err := resolveW3CSession(c.Image, c.Instance, c.Session)
	if err != nil {
		return err
	}
	id, err := s.findElement(c.Strategy, c.Selector)
	if err != nil {
		return err
	}
	if err := s.sendKeys(id, c.Text); err != nil {
		return fmt.Errorf("send-keys %s=%q: %w", c.Strategy, c.Selector, err)
	}
	fmt.Println("sent")
	return nil
}

// ---------------------------------------------------------------------------
// appium screenshot — PNG via /session/<id>/screenshot
// ---------------------------------------------------------------------------

type AppiumScreenshotCmd struct {
	Image    string `arg:"" help:"Image name"`
	Artifact string `long:"artifact" required:"" help:"Output PNG path on host"`
	Session  string `long:"session" help:"Override the persisted session id"`
	appiumCommonFlags
}

func (c *AppiumScreenshotCmd) Run() error {
	s, err := resolveW3CSession(c.Image, c.Instance, c.Session)
	if err != nil {
		return err
	}
	pngBytes, err := s.screenshot()
	if err != nil {
		return fmt.Errorf("screenshot: %w", err)
	}
	if err := os.WriteFile(c.Artifact, pngBytes, 0644); err != nil {
		return fmt.Errorf("write %s: %w", c.Artifact, err)
	}
	fmt.Printf("wrote %d bytes to %s\n", len(pngBytes), c.Artifact)
	return nil
}

// ===========================================================================
// Tier 1 — element introspection / navigation
// ===========================================================================

type AppiumGetTextCmd struct{ appiumElementFlags }

func (c *AppiumGetTextCmd) Run() error {
	s, err := resolveW3CSession(c.Image, c.Instance, c.Session)
	if err != nil {
		return err
	}
	id, err := s.findElement(c.Strategy, c.Selector)
	if err != nil {
		return err
	}
	text, err := s.elementText(id)
	if err != nil {
		return fmt.Errorf("get-text %s=%q: %w", c.Strategy, c.Selector, err)
	}
	fmt.Println(text)
	return nil
}

type AppiumGetAttributeCmd struct {
	appiumElementFlags
	Attribute string `long:"attribute" required:"" help:"Attribute name (checked/enabled/selected/text/class/...)"`
}

func (c *AppiumGetAttributeCmd) Run() error {
	s, err := resolveW3CSession(c.Image, c.Instance, c.Session)
	if err != nil {
		return err
	}
	id, err := s.findElement(c.Strategy, c.Selector)
	if err != nil {
		return err
	}
	v, err := s.elementAttribute(id, c.Attribute)
	if err != nil {
		return fmt.Errorf("get-attribute %s on %s=%q: %w", c.Attribute, c.Strategy, c.Selector, err)
	}
	fmt.Println(v)
	return nil
}

type AppiumClearCmd struct{ appiumElementFlags }

func (c *AppiumClearCmd) Run() error {
	s, err := resolveW3CSession(c.Image, c.Instance, c.Session)
	if err != nil {
		return err
	}
	id, err := s.findElement(c.Strategy, c.Selector)
	if err != nil {
		return err
	}
	if err := s.clearElement(id); err != nil {
		return fmt.Errorf("clear %s=%q: %w", c.Strategy, c.Selector, err)
	}
	fmt.Println("cleared")
	return nil
}

type AppiumFindAllCmd struct{ appiumElementFlags }

func (c *AppiumFindAllCmd) Run() error {
	s, err := resolveW3CSession(c.Image, c.Instance, c.Session)
	if err != nil {
		return err
	}
	ids, err := s.findElements(c.Strategy, c.Selector)
	if err != nil {
		return err
	}
	fmt.Println(len(ids))
	for _, id := range ids {
		fmt.Println(id)
	}
	return nil
}

type AppiumSourceCmd struct{ appiumSessionFlags }

func (c *AppiumSourceCmd) Run() error {
	s, err := c.session()
	if err != nil {
		return err
	}
	src, err := s.source()
	if err != nil {
		return err
	}
	fmt.Println(src)
	return nil
}

type AppiumBackCmd struct{ appiumSessionFlags }

func (c *AppiumBackCmd) Run() error {
	s, err := c.session()
	if err != nil {
		return err
	}
	if err := s.navigateBack(); err != nil {
		return fmt.Errorf("back: %w", err)
	}
	fmt.Println("back")
	return nil
}

// ===========================================================================
// Tier 2 — gesture group (mobile: *Gesture)
// ===========================================================================

// appiumTargetFlags: element-or-coordinate target + direction/percent/params.
type appiumTargetFlags struct {
	Image     string `arg:"" help:"Image name"`
	Selector  string `long:"selector" help:"Locator value (element target)"`
	Strategy  string `long:"strategy" default:"xpath" help:"Locator strategy"`
	X         int    `long:"x" help:"X coordinate (when no --selector)"`
	Y         int    `long:"y" help:"Y coordinate (when no --selector)"`
	Direction string `long:"direction" help:"Direction: up|down|left|right (swipe/scroll/fling)"`
	Percent   string `long:"percent" help:"Magnitude fraction, e.g. 0.75 (swipe/scroll/fling/pinch)"`
	Params    string `long:"params" help:"Extra mobile: args as a JSON object (speed/duration/endX/endY/left/top/width/height)"`
	Session   string `long:"session" help:"Override the persisted session id"`
	appiumCommonFlags
}

// runAppiumGesture resolves the target (elementId from --selector, else --x/--y
// unless --params already carries the area form), merges direction/percent and
// any extra --params, and invokes the named mobile: gesture.
func runAppiumGesture(gesture, pastTense string, f *appiumTargetFlags) error {
	s, err := resolveW3CSession(f.Image, f.Instance, f.Session)
	if err != nil {
		return err
	}
	args := map[string]interface{}{}
	if f.Params != "" {
		if err := json.Unmarshal([]byte(f.Params), &args); err != nil {
			return fmt.Errorf("invalid --params JSON: %w", err)
		}
	}
	if f.Selector != "" {
		id, err := s.findElement(f.Strategy, f.Selector)
		if err != nil {
			return err
		}
		args["elementId"] = id
	} else if _, hasArea := args["left"]; !hasArea {
		// coordinate target (click-family); area gestures carry left/top/width/
		// height via --params, in which case x/y are not added.
		args["x"] = f.X
		args["y"] = f.Y
	}
	if f.Direction != "" {
		args["direction"] = f.Direction
	}
	if f.Percent != "" {
		p, perr := strconv.ParseFloat(f.Percent, 64)
		if perr != nil {
			return fmt.Errorf("invalid --percent %q: %w", f.Percent, perr)
		}
		args["percent"] = p
	}
	if _, err := s.executeScript("mobile: "+gesture, []interface{}{args}); err != nil {
		return fmt.Errorf("mobile: %s: %w", gesture, err)
	}
	fmt.Println(pastTense)
	return nil
}

type AppiumGestureTapCmd struct{ appiumTargetFlags }

func (c *AppiumGestureTapCmd) Run() error {
	return runAppiumGesture("clickGesture", "tapped", &c.appiumTargetFlags)
}

type AppiumGestureDoubleTapCmd struct{ appiumTargetFlags }

func (c *AppiumGestureDoubleTapCmd) Run() error {
	return runAppiumGesture("doubleClickGesture", "double-tapped", &c.appiumTargetFlags)
}

type AppiumGestureLongPressCmd struct{ appiumTargetFlags }

func (c *AppiumGestureLongPressCmd) Run() error {
	return runAppiumGesture("longClickGesture", "long-pressed", &c.appiumTargetFlags)
}

type AppiumGestureDragCmd struct{ appiumTargetFlags }

func (c *AppiumGestureDragCmd) Run() error {
	return runAppiumGesture("dragGesture", "dragged", &c.appiumTargetFlags)
}

type AppiumGestureSwipeCmd struct{ appiumTargetFlags }

func (c *AppiumGestureSwipeCmd) Run() error {
	return runAppiumGesture("swipeGesture", "swiped", &c.appiumTargetFlags)
}

type AppiumGestureScrollCmd struct{ appiumTargetFlags }

func (c *AppiumGestureScrollCmd) Run() error {
	return runAppiumGesture("scrollGesture", "scrolled", &c.appiumTargetFlags)
}

type AppiumGestureFlingCmd struct{ appiumTargetFlags }

func (c *AppiumGestureFlingCmd) Run() error {
	return runAppiumGesture("flingGesture", "flung", &c.appiumTargetFlags)
}

type AppiumGesturePinchOpenCmd struct{ appiumTargetFlags }

func (c *AppiumGesturePinchOpenCmd) Run() error {
	return runAppiumGesture("pinchOpenGesture", "pinched-open", &c.appiumTargetFlags)
}

type AppiumGesturePinchCloseCmd struct{ appiumTargetFlags }

func (c *AppiumGesturePinchCloseCmd) Run() error {
	return runAppiumGesture("pinchCloseGesture", "pinched-close", &c.appiumTargetFlags)
}

// ===========================================================================
// Tier 2 — app lifecycle + activity group
// ===========================================================================

// appiumAppIdFlags: an app-lifecycle leaf that takes a package id.
type appiumAppIdFlags struct {
	appiumSessionFlags
	AppId string `long:"app-id" required:"" help:"Package id (e.g. io.appium.android.apis)"`
}

// run invokes mobile: <method> with {appId} and prints the W3C value.
func (f *appiumAppIdFlags) run(method, fallback string) error {
	s, err := f.session()
	if err != nil {
		return err
	}
	result, err := s.executeScript("mobile: "+method, []interface{}{map[string]interface{}{"appId": f.AppId}})
	if err != nil {
		return fmt.Errorf("mobile: %s %s: %w", method, f.AppId, err)
	}
	printW3CValue(result, fallback)
	return nil
}

type AppiumAppStartActivityCmd struct {
	appiumSessionFlags
	Activity string `long:"activity" required:"" help:"Intent component pkg/.activity (e.g. io.appium.android.apis/.view.TextFields)"`
	Params   string `long:"params" help:"Extra mobile: startActivity args as a JSON object (action/flags/extras)"`
}

func (c *AppiumAppStartActivityCmd) Run() error {
	s, err := c.session()
	if err != nil {
		return err
	}
	args := map[string]interface{}{"intent": c.Activity}
	if c.Params != "" {
		var extra map[string]interface{}
		if err := json.Unmarshal([]byte(c.Params), &extra); err != nil {
			return fmt.Errorf("invalid --params JSON: %w", err)
		}
		for k, v := range extra {
			args[k] = v
		}
	}
	if _, err := s.executeScript("mobile: startActivity", []interface{}{args}); err != nil {
		return fmt.Errorf("mobile: startActivity %s: %w", c.Activity, err)
	}
	fmt.Println("started " + c.Activity)
	return nil
}

type AppiumAppActivateCmd struct{ appiumAppIdFlags }

func (c *AppiumAppActivateCmd) Run() error { return c.run("activateApp", "activated") }

type AppiumAppTerminateCmd struct{ appiumAppIdFlags }

func (c *AppiumAppTerminateCmd) Run() error { return c.run("terminateApp", "terminated") }

type AppiumAppRemoveCmd struct{ appiumAppIdFlags }

func (c *AppiumAppRemoveCmd) Run() error { return c.run("removeApp", "removed") }

type AppiumAppClearCmd struct{ appiumAppIdFlags }

func (c *AppiumAppClearCmd) Run() error { return c.run("clearApp", "cleared") }

type AppiumAppIsInstalledCmd struct{ appiumAppIdFlags }

func (c *AppiumAppIsInstalledCmd) Run() error { return c.run("isAppInstalled", "") }

type AppiumAppStateCmd struct{ appiumAppIdFlags }

func (c *AppiumAppStateCmd) Run() error { return c.run("queryAppState", "") }

type AppiumAppCurrentActivityCmd struct{ appiumSessionFlags }

func (c *AppiumAppCurrentActivityCmd) Run() error {
	return appiumDeviceMobile(&c.appiumSessionFlags, "getCurrentActivity")
}

type AppiumAppCurrentPackageCmd struct{ appiumSessionFlags }

func (c *AppiumAppCurrentPackageCmd) Run() error {
	return appiumDeviceMobile(&c.appiumSessionFlags, "getCurrentPackage")
}

// ===========================================================================
// Tier 2 — keys + keyboard group
// ===========================================================================

type AppiumKeyPressCmd struct {
	appiumSessionFlags
	Keycode int    `long:"keycode" required:"" help:"Android keycode (4=BACK, 66=ENTER, 3=HOME, ...)"`
	Params  string `long:"params" help:"Extra mobile: pressKey args as a JSON object (metastate/flags/isLongPress)"`
}

func (c *AppiumKeyPressCmd) Run() error {
	s, err := c.session()
	if err != nil {
		return err
	}
	args := map[string]interface{}{"keycode": c.Keycode}
	if c.Params != "" {
		var extra map[string]interface{}
		if err := json.Unmarshal([]byte(c.Params), &extra); err != nil {
			return fmt.Errorf("invalid --params JSON: %w", err)
		}
		for k, v := range extra {
			args[k] = v
		}
	}
	if _, err := s.executeScript("mobile: pressKey", []interface{}{args}); err != nil {
		return fmt.Errorf("mobile: pressKey %d: %w", c.Keycode, err)
	}
	fmt.Printf("pressed %d\n", c.Keycode)
	return nil
}

type AppiumKeyHideCmd struct{ appiumSessionFlags }

func (c *AppiumKeyHideCmd) Run() error {
	return appiumDeviceMobile(&c.appiumSessionFlags, "hideKeyboard")
}

type AppiumKeyShownCmd struct{ appiumSessionFlags }

func (c *AppiumKeyShownCmd) Run() error {
	return appiumDeviceMobile(&c.appiumSessionFlags, "isKeyboardShown")
}

// ===========================================================================
// Tier 2 — device / system + WebView context group
// ===========================================================================

// appiumDeviceMobile runs an argless mobile: <method> and prints the value.
func appiumDeviceMobile(f *appiumSessionFlags, method string) error {
	s, err := f.session()
	if err != nil {
		return err
	}
	result, err := s.executeScript("mobile: "+method, []interface{}{})
	if err != nil {
		return fmt.Errorf("mobile: %s: %w", method, err)
	}
	printW3CValue(result, "")
	return nil
}

type AppiumDeviceInfoCmd struct{ appiumSessionFlags }

func (c *AppiumDeviceInfoCmd) Run() error {
	return appiumDeviceMobile(&c.appiumSessionFlags, "deviceInfo")
}

type AppiumDeviceBatteryCmd struct{ appiumSessionFlags }

func (c *AppiumDeviceBatteryCmd) Run() error {
	return appiumDeviceMobile(&c.appiumSessionFlags, "batteryInfo")
}

type AppiumDeviceTimeCmd struct{ appiumSessionFlags }

func (c *AppiumDeviceTimeCmd) Run() error {
	return appiumDeviceMobile(&c.appiumSessionFlags, "getDeviceTime")
}

type AppiumDeviceNotificationsCmd struct{ appiumSessionFlags }

func (c *AppiumDeviceNotificationsCmd) Run() error {
	s, err := c.session()
	if err != nil {
		return err
	}
	if _, err := s.executeScript("mobile: openNotifications", []interface{}{}); err != nil {
		return fmt.Errorf("mobile: openNotifications: %w", err)
	}
	fmt.Println("opened")
	return nil
}

type AppiumDeviceOrientationCmd struct {
	appiumSessionFlags
	Params string `long:"params" help:"(ignored for get)"`
}

func (c *AppiumDeviceOrientationCmd) Run() error {
	s, err := c.session()
	if err != nil {
		return err
	}
	o, err := s.orientation()
	if err != nil {
		return fmt.Errorf("get orientation: %w", err)
	}
	fmt.Println(o)
	return nil
}

type AppiumDeviceSetOrientationCmd struct {
	appiumSessionFlags
	Params string `long:"params" required:"" help:"Orientation value: PORTRAIT or LANDSCAPE"`
}

func (c *AppiumDeviceSetOrientationCmd) Run() error {
	s, err := c.session()
	if err != nil {
		return err
	}
	if err := s.setOrientation(strings.ToUpper(strings.TrimSpace(c.Params))); err != nil {
		return fmt.Errorf("set orientation %q: %w", c.Params, err)
	}
	fmt.Println("oriented")
	return nil
}

type AppiumDeviceGetClipboardCmd struct{ appiumSessionFlags }

func (c *AppiumDeviceGetClipboardCmd) Run() error {
	s, err := c.session()
	if err != nil {
		return err
	}
	result, err := s.executeScript("mobile: getClipboard", []interface{}{})
	if err != nil {
		return fmt.Errorf("mobile: getClipboard: %w", err)
	}
	// getClipboard returns base64-encoded content by default; decode for readability.
	var b64 string
	if json.Unmarshal(result, &b64) == nil && b64 != "" {
		if dec, derr := base64.StdEncoding.DecodeString(b64); derr == nil {
			fmt.Println(string(dec))
			return nil
		}
	}
	printW3CValue(result, "")
	return nil
}

type AppiumDeviceSetClipboardCmd struct {
	appiumSessionFlags
	Params string `long:"params" required:"" help:"Clipboard text to set"`
}

func (c *AppiumDeviceSetClipboardCmd) Run() error {
	s, err := c.session()
	if err != nil {
		return err
	}
	content := base64.StdEncoding.EncodeToString([]byte(c.Params))
	arg := map[string]interface{}{"content": content, "contentType": "plaintext"}
	if _, err := s.executeScript("mobile: setClipboard", []interface{}{arg}); err != nil {
		return fmt.Errorf("mobile: setClipboard: %w", err)
	}
	fmt.Println("set")
	return nil
}

type AppiumDeviceContextsCmd struct{ appiumSessionFlags }

func (c *AppiumDeviceContextsCmd) Run() error {
	s, err := c.session()
	if err != nil {
		return err
	}
	ctxs, err := s.contexts()
	if err != nil {
		return err
	}
	for _, ctx := range ctxs {
		fmt.Println(ctx)
	}
	return nil
}

type AppiumDeviceContextCmd struct {
	appiumSessionFlags
	Params string `long:"params" help:"Context name to switch to (empty = print current context)"`
}

func (c *AppiumDeviceContextCmd) Run() error {
	s, err := c.session()
	if err != nil {
		return err
	}
	if c.Params == "" {
		cur, err := s.currentContext()
		if err != nil {
			return err
		}
		fmt.Println(cur)
		return nil
	}
	if err := s.setContext(c.Params); err != nil {
		return fmt.Errorf("set context %q: %w", c.Params, err)
	}
	fmt.Println(c.Params)
	return nil
}

// ===========================================================================
// Tier 3 — generic escape hatch (cdp raw / eval equivalents)
// ===========================================================================

// substituteElement replaces the literal {element} token with a resolved id.
func substituteElement(s, elemID string) string {
	return strings.ReplaceAll(s, "{element}", elemID)
}

type AppiumExecuteCmd struct {
	appiumSessionFlags
	Expression  string `long:"expression" required:"" help:"mobile: command (e.g. 'mobile: clickGesture') or JS"`
	RequestBody string `long:"request-body" help:"Args JSON: an object is wrapped as [obj]; an array is passed as-is"`
	Selector    string `long:"selector" help:"Resolve an element; substitute its id for {element} in --request-body"`
	Strategy    string `long:"strategy" default:"xpath" help:"Locator strategy for --selector"`
}

func (c *AppiumExecuteCmd) Run() error {
	s, err := c.session()
	if err != nil {
		return err
	}
	body := c.RequestBody
	if c.Selector != "" {
		id, ferr := s.findElement(c.Strategy, c.Selector)
		if ferr != nil {
			return ferr
		}
		body = substituteElement(body, id)
	}
	args := []interface{}{}
	if strings.TrimSpace(body) != "" {
		var v interface{}
		if err := json.Unmarshal([]byte(body), &v); err != nil {
			return fmt.Errorf("invalid --request-body JSON: %w", err)
		}
		if arr, ok := v.([]interface{}); ok {
			args = arr
		} else {
			args = []interface{}{v}
		}
	}
	result, err := s.executeScript(c.Expression, args)
	if err != nil {
		return fmt.Errorf("execute %q: %w", c.Expression, err)
	}
	printW3CValue(result, "")
	return nil
}

type AppiumRawCmd struct {
	appiumSessionFlags
	Method      string `long:"method" required:"" help:"HTTP verb: GET, POST, DELETE"`
	Path        string `long:"path" required:"" help:"Endpoint relative to /session/<id> (e.g. /source, /element/{element}/text, /contexts)"`
	RequestBody string `long:"request-body" help:"Optional JSON body"`
	Selector    string `long:"selector" help:"Resolve an element; substitute its id for {element} in --path/--request-body"`
	Strategy    string `long:"strategy" default:"xpath" help:"Locator strategy for --selector"`
}

func (c *AppiumRawCmd) Run() error {
	s, err := c.session()
	if err != nil {
		return err
	}
	path := c.Path
	body := c.RequestBody
	if c.Selector != "" {
		id, ferr := s.findElement(c.Strategy, c.Selector)
		if ferr != nil {
			return ferr
		}
		path = substituteElement(path, id)
		body = substituteElement(body, id)
	}
	var reqBody interface{}
	if strings.TrimSpace(body) != "" {
		if err := json.Unmarshal([]byte(body), &reqBody); err != nil {
			return fmt.Errorf("invalid --request-body JSON: %w", err)
		}
	}
	result, err := s.rawCall(strings.ToUpper(c.Method), path, reqBody)
	if err != nil {
		return err
	}
	printW3CValue(result, "")
	return nil
}
