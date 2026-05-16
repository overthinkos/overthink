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
	"strings"
	"time"

	"github.com/tebeka/selenium"
)

// appium.go implements `ov eval appium …` — the host-side Appium
// WebDriver client. The host `ov` binary connects to the container's
// host-published Appium port (container :4723 → host's HOST_PORT:4723,
// e.g. 35001 on android-emulator-pod) using github.com/tebeka/selenium
// (W3C WebDriver client, talks Appium 2.x and 3.x because W3C is stable).
//
// Appium-specific endpoints not in the standard WebDriver surface
// (install_app, is_app_installed, remove_app) route through the W3C
// escape hatch: driver.ExecuteScript("mobile: installApp", ...).
//
// Session lifecycle: a persistent JSON file at
// ~/.cache/ov/appium/sessions/<image>[_<instance>].json carries the
// session id between separate `ov eval appium …` invocations. See
// appium_session.go for the file management.

// AppiumCmd groups the 8 `ov eval appium …` leaves.
type AppiumCmd struct {
	Status        AppiumStatusCmd        `cmd:"" help:"Check Appium server health (/status endpoint)"`
	SessionCreate AppiumSessionCreateCmd `cmd:"session-create" help:"Create a W3C WebDriver session and persist its id"`
	SessionDelete AppiumSessionDeleteCmd `cmd:"session-delete" help:"Terminate the persisted session and remove the file"`
	InstallApp    AppiumInstallAppCmd    `cmd:"install-app" help:"Install an APK via mobile:installApp"`
	Find          AppiumFindCmd          `cmd:"" help:"Find an element by locator strategy + selector"`
	Click         AppiumClickCmd         `cmd:"" help:"Click an element matched by locator"`
	SendKeys      AppiumSendKeysCmd      `cmd:"send-keys" help:"Type text into an element matched by locator"`
	Screenshot    AppiumScreenshotCmd    `cmd:"" help:"Capture a PNG screenshot via /session/<id>/screenshot"`
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

// AppiumStatusCmd: `ov eval appium status <image>` — issues GET <base>/status
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

// AppiumSessionCreateCmd: `ov eval appium session-create <image> --caps <json>`
// — parses the W3C capabilities JSON, creates a session via the selenium
// SDK (which wraps Capabilities under W3C `alwaysMatch`), persists the
// session id at ~/.cache/ov/appium/sessions/<image>[_<instance>].json,
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

// AppiumSessionDeleteCmd: `ov eval appium session-delete <image>`. No-op
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
		return nil, fmt.Errorf("no Appium session for image %q (instance=%q) — run `ov eval appium session-create %s --caps <json>` first", image, instance, image)
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

// ---------------------------------------------------------------------------
// appium install-app — mobile:installApp escape hatch
// ---------------------------------------------------------------------------

// AppiumInstallAppCmd: `ov eval appium install-app <image> --apk <path>`.
// Reads the APK from the host filesystem, base64-encodes it, and calls
// the W3C ExecuteScript escape hatch with `mobile: installApp`. The
// `appPath` arg accepts either a remote path on the device or — when
// the running Appium has the host filesystem visible — a host path.
// Most setups, including android-emulator-pod, expose the host workspace
// inside the container at /workspace, so passing the in-container path
// works without uploading.
//
// Two arg shapes for `mobile: installApp` are accepted by Appium 3.x:
//   - `{"appPath": "/path/inside/container"}` — Appium reads the path
//   - `{"app": "base64data"}` — Appium decodes
// We use appPath since the canonical R10 flow bind-mounts the APK.
type AppiumInstallAppCmd struct {
	Image string `arg:"" help:"Image name"`
	Apk   string `long:"apk" required:"" help:"APK path visible to the Appium server (typically a bind-mounted /workspace path)"`
	appiumCommonFlags
}

func (c *AppiumInstallAppCmd) Run() error {
	sess, err := loadActiveSession(c.Image, c.Instance)
	if err != nil {
		return err
	}
	s := newW3CSession(sess.BaseURL, sess.SessionID)
	args := []interface{}{map[string]interface{}{"appPath": c.Apk}}
	result, err := s.executeScript("mobile: installApp", args)
	if err != nil {
		return fmt.Errorf("mobile: installApp %s: %w", c.Apk, err)
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

// AppiumFindCmd: `ov eval appium find <image> --selector <expr> [--strategy STRAT]`
// — finds the first element matching selector + strategy, prints the
// W3C element id. Useful for chaining (a subsequent shell-driven call
// could use the id), but in practice the recommended pattern is to
// inline find+click via `ov eval appium click`.
type AppiumFindCmd struct {
	Image    string `arg:"" help:"Image name"`
	Selector string `long:"selector" required:"" help:"Locator value"`
	Strategy string `long:"strategy" default:"xpath" help:"Locator strategy: xpath, id, accessibility-id, class-name, android-uiautomator"`
	Session  string `long:"session" help:"Override the persisted session id"`
	appiumCommonFlags
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

// strategyToBy maps the ov authoring surface to the SDK / W3C constants.
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
	Image    string `arg:"" help:"Image name"`
	Selector string `long:"selector" required:"" help:"Locator value"`
	Strategy string `long:"strategy" default:"xpath" help:"Locator strategy"`
	Session  string `long:"session" help:"Override the persisted session id"`
	appiumCommonFlags
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
	Image    string `arg:"" help:"Image name"`
	Selector string `long:"selector" required:"" help:"Locator value"`
	Text     string `long:"text" required:"" help:"Text to type"`
	Strategy string `long:"strategy" default:"xpath" help:"Locator strategy"`
	Session  string `long:"session" help:"Override the persisted session id"`
	appiumCommonFlags
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
