package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

// deepQueryJS is a JavaScript helper that recursively searches through
// shadow DOM boundaries to find an element matching a CSS selector.
// Standard document.querySelector() only searches the light DOM.
const deepQueryJS = `function deepQuery(sel, root) {
  root = root || document;
  var matches = root.querySelectorAll(sel);
  for (var m = 0; m < matches.length; m++) {
    var r = matches[m].getBoundingClientRect();
    if (r.width > 0 && r.height > 0) return matches[m];
  }
  var all = root.querySelectorAll('*');
  for (var i = 0; i < all.length; i++) {
    if (all[i].shadowRoot) {
      var el = deepQuery(sel, all[i].shadowRoot);
      if (el) return el;
    }
  }
  return null;
}`

// CdpCmd manages Chrome browser tabs in running containers
type CdpCmd struct {
	Open       CdpOpenCmd       `cmd:"" help:"Open a URL in the container's Chrome browser"`
	List       CdpListCmd       `cmd:"" help:"List open Chrome browser tabs"`
	Close      CdpCloseCmd      `cmd:"" help:"Close a Chrome browser tab"`
	Text       CdpTextCmd       `cmd:"" help:"Get page text content"`
	Html       CdpHtmlCmd       `cmd:"" help:"Get page HTML"`
	Url        CdpUrlCmd        `cmd:"" help:"Get current page URL and title"`
	Screenshot CdpScreenshotCmd `cmd:"" help:"Capture a screenshot"`
	Click      CdpClickCmd      `cmd:"" help:"Click an element by CSS selector"`
	Type       CdpTypeCmd       `cmd:"" help:"Type text into an input field"`
	Eval       CdpEvalCmd       `cmd:"" help:"Evaluate JavaScript expression"`
	Wait       CdpWaitCmd       `cmd:"" help:"Wait for an element to appear"`
	Raw        CdpRawCmd        `cmd:"" help:"Send a raw CDP command"`
	Coords     CdpCoordsCmd     `cmd:"" help:"Show element coordinates in viewport and desktop systems"`
	Status     CdpStatusCmd     `cmd:"" help:"Check Chrome DevTools Protocol availability"`
}

// CdpStatusCmd checks if CDP is reachable.
type CdpStatusCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *CdpStatusCmd) Run() error {
	engine, name, err := resolveCdpContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	ts := checkCdpStatus(engine, name)
	if ts.Port > 0 {
		fmt.Printf("CDP:       %s (port %d)\n", ts.Status, ts.Port)
	} else {
		fmt.Printf("CDP:       %s\n", ts.Status)
	}
	if ts.Detail != "" {
		fmt.Printf("Detail:    %s\n", ts.Detail)
	}
	return nil
}

// checkCdpStatus probes CDP availability on port 9222.
// Returns ToolStatus{Status: "-"} if port 9222 is not mapped (tool not configured).
func checkCdpStatus(engine, containerName string) ToolStatus {
	ts := ToolStatus{Name: "cdp", Status: "-"}
	devtoolsURL, err := resolveDevToolsURL(engine, containerName)
	if err != nil {
		return ts
	}

	// Extract port from resolved URL
	ts.Port = extractPortFromURL(devtoolsURL)
	ts.Status = "unreachable"

	// Probe: GET /json to list tabs
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(devtoolsURL + "/json")
	if err != nil {
		return ts
	}
	defer resp.Body.Close()

	var tabs []devToolsTab
	if err := json.NewDecoder(resp.Body).Decode(&tabs); err != nil {
		return ts
	}

	ts.Status = "ok"
	ts.Detail = fmt.Sprintf("%d tabs", len(tabs))
	return ts
}

// extractPortFromURL extracts the port number from a URL like "http://127.0.0.1:9222".
func extractPortFromURL(rawURL string) int {
	u, err := url.Parse(rawURL)
	if err != nil {
		return 0
	}
	portStr := u.Port()
	if portStr == "" {
		return 0
	}
	var port int
	fmt.Sscanf(portStr, "%d", &port)
	return port
}

// CdpOpenCmd opens a URL in the container's Chrome browser
type CdpOpenCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	URL      string `arg:"" help:"URL to open"`
	Instance string `short:"i" long:"instance" help:"Instance name for multi-instance containers"`
}

func (c *CdpOpenCmd) Run() error {
	engine, name, err := resolveCdpContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}

	devtoolsURL, err := resolveDevToolsURL(engine, name)
	if err != nil {
		return err
	}

	// Create a new tab navigated to the URL via Chrome DevTools HTTP API.
	// URL-encode the target so its query params don't conflict with the endpoint.
	encoded := url.QueryEscape(c.URL)
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("PUT", devtoolsURL+"/json/new?"+encoded, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return diagnoseCDP(engine, name, c.Image, fmt.Errorf("opening URL in Chrome: %w", err))
	}
	defer resp.Body.Close()

	var tab devToolsTab
	if err := json.NewDecoder(resp.Body).Decode(&tab); err != nil {
		return fmt.Errorf("parsing response: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Opened %s in %s (tab %s)\n", c.URL, name, tab.ID)
	return nil
}

// CdpListCmd lists open Chrome browser tabs
type CdpListCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

// devToolsTab represents a Chrome DevTools Protocol tab entry
type devToolsTab struct {
	ID                   string `json:"id"`
	Title                string `json:"title"`
	URL                  string `json:"url"`
	Type                 string `json:"type"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

func (c *CdpListCmd) Run() error {
	engine, name, err := resolveCdpContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}

	devtoolsURL, err := resolveDevToolsURL(engine, name)
	if err != nil {
		return err
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(devtoolsURL + "/json")
	if err != nil {
		return diagnoseCDP(engine, name, c.Image, fmt.Errorf("failed to connect to Chrome DevTools: %w", err))
	}
	defer resp.Body.Close()

	var tabs []devToolsTab
	if err := json.NewDecoder(resp.Body).Decode(&tabs); err != nil {
		return fmt.Errorf("failed to parse DevTools response: %w", err)
	}

	for _, tab := range tabs {
		if tab.Type != "page" {
			continue
		}
		title := tab.Title
		if len(title) > 60 {
			title = title[:57] + "..."
		}
		url := tab.URL
		if len(url) > 80 {
			url = url[:77] + "..."
		}
		fmt.Printf("%s\t%s\t%s\n", tab.ID, title, url)
	}
	return nil
}

// CdpCloseCmd closes a Chrome browser tab
type CdpCloseCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	TabID    string `arg:"" help:"Tab ID to close (from browser list)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *CdpCloseCmd) Run() error {
	engine, name, err := resolveCdpContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}

	devtoolsURL, err := resolveDevToolsURL(engine, name)
	if err != nil {
		return err
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(devtoolsURL + "/json/close/" + c.TabID)
	if err != nil {
		return fmt.Errorf("failed to close tab: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to close tab %s (HTTP %d)", c.TabID, resp.StatusCode)
	}

	fmt.Fprintf(os.Stderr, "Closed tab %s in %s\n", c.TabID, name)
	return nil
}

// resolveCdpContainer resolves the engine and container name, verifying the container is running.
// Use "." as image name for local mode (direct connection to localhost).
func resolveCdpContainer(image, instance string) (engine, name string, err error) {
	return resolveContainer(image, instance)
}

// resolveDevToolsURL inspects the container's port mapping for port 9222
// and returns the DevTools WebSocket URL.
// When engine is empty (local mode), connects to localhost directly.
func resolveDevToolsURL(engine, containerName string) (string, error) {
	if engine == "" {
		return "http://127.0.0.1:9222", nil
	}
	cmd := exec.Command(engine, "port", containerName, "9222")
	output, err := cmd.Output()
	if err != nil {
		// Host-networked containers have no port mappings — fall back to localhost.
		if isHostNetworked(engine, containerName) {
			return "http://127.0.0.1:9222", nil
		}
		return "", fmt.Errorf("no port mapping found for 9222")
	}
	return parseDevToolsPort(string(output))
}

// parseDevToolsPort parses the output of `docker/podman port <name> 9222`
// and returns an HTTP URL for the DevTools endpoint.
func parseDevToolsPort(output string) (string, error) {
	// Output may contain multiple lines (IPv4 + IPv6); use the first one.
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return "", fmt.Errorf("no port mapping found for 9222")
	}
	hostPort := strings.TrimSpace(lines[0])
	// Replace 0.0.0.0 with 127.0.0.1 for local connections
	hostPort = strings.Replace(hostPort, "0.0.0.0", "127.0.0.1", 1)
	// Handle IPv6 [::] -> 127.0.0.1
	if strings.HasPrefix(hostPort, "[::]:") {
		hostPort = "127.0.0.1:" + strings.TrimPrefix(hostPort, "[::]:")
	}
	return "http://" + hostPort, nil
}

// resolveTabWS fetches /json from the DevTools HTTP endpoint and returns the
// WebSocket debugger URL for the tab with the given ID.
func resolveTabWS(devtoolsURL, tabID string) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(devtoolsURL + "/json")
	if err != nil {
		return "", fmt.Errorf("fetching tab list: %w", err)
	}
	defer resp.Body.Close()

	var tabs []devToolsTab
	if err := json.NewDecoder(resp.Body).Decode(&tabs); err != nil {
		return "", fmt.Errorf("parsing tab list: %w", err)
	}

	for _, tab := range tabs {
		if tab.ID == tabID {
			if tab.WebSocketDebuggerURL == "" {
				return "", fmt.Errorf("tab %s has no WebSocket debugger URL", tabID)
			}
			return tab.WebSocketDebuggerURL, nil
		}
	}
	return "", fmt.Errorf("tab %s not found", tabID)
}

// connectTab resolves container -> devtools URL -> tab WS URL -> CDPClient.
// On connection failure, runs CDP diagnostics to help identify the cause.
func connectTab(image, tabID, instance string) (*CDPClient, error) {
	engine, name, err := resolveCdpContainer(image, instance)
	if err != nil {
		return nil, err
	}

	devtoolsURL, err := resolveDevToolsURL(engine, name)
	if err != nil {
		return nil, err
	}

	wsURL, err := resolveTabWS(devtoolsURL, tabID)
	if err != nil {
		return nil, diagnoseCDP(engine, name, image, err)
	}

	client, err := NewCDPClient(wsURL)
	if err != nil {
		return nil, diagnoseCDP(engine, name, image, err)
	}
	return client, nil
}

// cdpEvaluate calls Runtime.evaluate and returns the result value as a string.
func cdpEvaluate(client *CDPClient, expression string) (string, error) {
	result, err := client.Call("Runtime.evaluate", map[string]any{
		"expression":    expression,
		"returnByValue": true,
	})
	if err != nil {
		return "", err
	}

	var evalResult struct {
		Result struct {
			Type        string          `json:"type"`
			Value       json.RawMessage `json:"value"`
			Description string          `json:"description"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(result, &evalResult); err != nil {
		return "", fmt.Errorf("parsing evaluate result: %w", err)
	}
	if evalResult.ExceptionDetails != nil {
		return "", fmt.Errorf("JavaScript exception: %s", evalResult.ExceptionDetails.Text)
	}

	// For string values, unmarshal the JSON string.
	if evalResult.Result.Type == "string" {
		var s string
		if err := json.Unmarshal(evalResult.Result.Value, &s); err != nil {
			return string(evalResult.Result.Value), nil
		}
		return s, nil
	}
	// For undefined/null, return empty.
	if evalResult.Result.Type == "undefined" || evalResult.Result.Value == nil {
		if evalResult.Result.Description != "" {
			return evalResult.Result.Description, nil
		}
		return "", nil
	}
	// For other types (number, boolean, object), return the raw JSON.
	return string(evalResult.Result.Value), nil
}

// CdpTextCmd gets the page text content of a tab.
type CdpTextCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	TabID    string `arg:"" help:"Tab ID (from browser list)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *CdpTextCmd) Run() error {
	client, err := connectTab(c.Image, c.TabID, c.Instance)
	if err != nil {
		return err
	}
	defer client.Close()

	text, err := cdpEvaluate(client, `document.body.innerText`)
	if err != nil {
		return fmt.Errorf("getting page text: %w", err)
	}
	fmt.Println(text)
	return nil
}

// CdpHtmlCmd gets the page HTML of a tab.
type CdpHtmlCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	TabID    string `arg:"" help:"Tab ID (from browser list)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *CdpHtmlCmd) Run() error {
	client, err := connectTab(c.Image, c.TabID, c.Instance)
	if err != nil {
		return err
	}
	defer client.Close()

	html, err := cdpEvaluate(client, `document.documentElement.outerHTML`)
	if err != nil {
		return fmt.Errorf("getting page HTML: %w", err)
	}
	fmt.Println(html)
	return nil
}

// CdpUrlCmd gets the current page URL and title of a tab.
type CdpUrlCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	TabID    string `arg:"" help:"Tab ID (from browser list)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *CdpUrlCmd) Run() error {
	client, err := connectTab(c.Image, c.TabID, c.Instance)
	if err != nil {
		return err
	}
	defer client.Close()

	result, err := cdpEvaluate(client, `JSON.stringify({title: document.title, url: location.href})`)
	if err != nil {
		return fmt.Errorf("getting page URL: %w", err)
	}

	var info struct {
		Title string `json:"title"`
		URL   string `json:"url"`
	}
	if err := json.Unmarshal([]byte(result), &info); err != nil {
		// Fallback: print the raw result.
		fmt.Println(result)
		return nil
	}
	fmt.Printf("Title: %s\nURL:   %s\n", info.Title, info.URL)
	return nil
}

// CdpScreenshotCmd captures a screenshot from a tab.
type CdpScreenshotCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	TabID    string `arg:"" help:"Tab ID (from browser list)"`
	File     string `arg:"" optional:"" default:"screenshot.png" help:"Output file path"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *CdpScreenshotCmd) Run() error {
	client, err := connectTab(c.Image, c.TabID, c.Instance)
	if err != nil {
		return err
	}
	defer client.Close()

	result, err := client.Call("Page.captureScreenshot", map[string]any{
		"format": "png",
	})
	if err != nil {
		return fmt.Errorf("capturing screenshot: %w", err)
	}

	var screenshotResult struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(result, &screenshotResult); err != nil {
		return fmt.Errorf("parsing screenshot result: %w", err)
	}

	data, err := base64.StdEncoding.DecodeString(screenshotResult.Data)
	if err != nil {
		return fmt.Errorf("decoding screenshot data: %w", err)
	}

	if err := os.WriteFile(c.File, data, 0644); err != nil {
		return fmt.Errorf("writing screenshot to %s: %w", c.File, err)
	}

	fmt.Fprintf(os.Stderr, "Screenshot saved to %s (%d bytes)\n", c.File, len(data))
	return nil
}

// CdpClickCmd clicks an element by CSS selector.
type CdpClickCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	TabID    string `arg:"" help:"Tab ID (from browser list)"`
	Selector string `arg:"" help:"CSS selector of element to click"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
	VNC      bool   `long:"vnc" help:"Deliver click via VNC instead of CDP (translates viewport coords to desktop coords)"`
}

func (c *CdpClickCmd) Run() error {
	client, err := connectTab(c.Image, c.TabID, c.Instance)
	if err != nil {
		return err
	}
	defer client.Close()

	// Find element (piercing shadow DOM), scroll into view, and get its center coordinates.
	js := fmt.Sprintf(`(function() {
		%s
		const el = deepQuery(%s);
		if (!el) return JSON.stringify({error: 'Element not found'});
		el.scrollIntoViewIfNeeded();
		const rect = el.getBoundingClientRect();
		return JSON.stringify({x: rect.x + rect.width/2, y: rect.y + rect.height/2});
	})()`, deepQueryJS, jsQuote(c.Selector))

	result, err := cdpEvaluate(client, js)
	if err != nil {
		return fmt.Errorf("finding element: %w", err)
	}

	var coords struct {
		X     float64 `json:"x"`
		Y     float64 `json:"y"`
		Error string  `json:"error"`
	}
	if err := json.Unmarshal([]byte(result), &coords); err != nil {
		return fmt.Errorf("parsing element position: %w", err)
	}
	if coords.Error != "" {
		return fmt.Errorf("%s", coords.Error)
	}

	// If --vnc is set, translate viewport coords to desktop coords and click via VNC.
	if c.VNC {
		offset, err := cdpGetWindowOffset(client)
		if err != nil {
			return fmt.Errorf("getting window offset for VNC translation: %w", err)
		}
		desktopX := uint16(coords.X + offset.ScreenX)
		desktopY := uint16(coords.Y + offset.ScreenY + offset.ChromeHeight)
		vncClient, err := connectVNC(c.Image, c.Instance)
		if err != nil {
			return fmt.Errorf("connecting to VNC: %w", err)
		}
		defer vncClient.Close()
		if err := vncClient.PointerClick(desktopX, desktopY, 1); err != nil {
			return fmt.Errorf("VNC click at (%d, %d): %w", desktopX, desktopY, err)
		}
		fmt.Fprintf(os.Stderr, "Clicked element at viewport (%.0f, %.0f) → desktop (%d, %d) via VNC\n",
			coords.X, coords.Y, desktopX, desktopY)
		return nil
	}

	// Dispatch mouse events: press then release.
	mouseParams := map[string]any{
		"x":          coords.X,
		"y":          coords.Y,
		"button":     "left",
		"clickCount": 1,
	}

	mouseParams["type"] = "mousePressed"
	if _, err := client.Call("Input.dispatchMouseEvent", mouseParams); err != nil {
		return fmt.Errorf("dispatching mousePressed: %w", err)
	}

	mouseParams["type"] = "mouseReleased"
	if _, err := client.Call("Input.dispatchMouseEvent", mouseParams); err != nil {
		return fmt.Errorf("dispatching mouseReleased: %w", err)
	}

	// Report new page state (best-effort).
	info, err := cdpEvaluate(client, `JSON.stringify({title: document.title, url: location.href})`)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Clicked element at (%.0f, %.0f)\n", coords.X, coords.Y)
		return nil
	}
	var page struct {
		Title string `json:"title"`
		URL   string `json:"url"`
	}
	if err := json.Unmarshal([]byte(info), &page); err == nil {
		fmt.Fprintf(os.Stderr, "Clicked element at (%.0f, %.0f)\n", coords.X, coords.Y)
		fmt.Printf("Title: %s\nURL:   %s\n", page.Title, page.URL)
	} else {
		fmt.Fprintf(os.Stderr, "Clicked element at (%.0f, %.0f)\n", coords.X, coords.Y)
	}
	return nil
}

// CdpTypeCmd types text into an input field.
type CdpTypeCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	TabID    string `arg:"" help:"Tab ID (from browser list)"`
	Selector string `arg:"" help:"CSS selector of input field"`
	Text     string `arg:"" help:"Text to type"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *CdpTypeCmd) Run() error {
	client, err := connectTab(c.Image, c.TabID, c.Instance)
	if err != nil {
		return err
	}
	defer client.Close()

	// Focus the element (piercing shadow DOM) and clear its value.
	js := fmt.Sprintf(`(function() {
		%s
		const el = deepQuery(%s);
		if (!el) return 'Element not found';
		el.scrollIntoViewIfNeeded();
		el.focus();
		el.value = '';
		return 'ok';
	})()`, deepQueryJS, jsQuote(c.Selector))

	result, err := cdpEvaluate(client, js)
	if err != nil {
		return fmt.Errorf("focusing element: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("%s", result)
	}

	// Type each character via Input.dispatchKeyEvent (matches Puppeteer behavior).
	for _, ch := range c.Text {
		key := string(ch)
		if err := cdpDispatchKeyEvent(client, "keyDown", key); err != nil {
			return fmt.Errorf("dispatching keyDown: %w", err)
		}
		if err := cdpDispatchKeyEvent(client, "char", key); err != nil {
			return fmt.Errorf("dispatching char: %w", err)
		}
		if err := cdpDispatchKeyEvent(client, "keyUp", key); err != nil {
			return fmt.Errorf("dispatching keyUp: %w", err)
		}
	}

	fmt.Fprintf(os.Stderr, "Typed text into %s\n", c.Selector)
	return nil
}

// cdpDispatchKeyEvent sends a single CDP Input.dispatchKeyEvent.
func cdpDispatchKeyEvent(client *CDPClient, eventType, key string) error {
	params := map[string]any{
		"type": eventType,
		"key":  key,
	}
	// Only "char" events carry the text field.
	if eventType == "char" {
		params["text"] = key
	}
	_, err := client.Call("Input.dispatchKeyEvent", params)
	return err
}

// CdpEvalCmd evaluates a JavaScript expression in a tab.
type CdpEvalCmd struct {
	Image      string `arg:"" help:"Image name from images.yml"`
	TabID      string `arg:"" help:"Tab ID (from browser list)"`
	Expression string `arg:"" help:"JavaScript expression to evaluate"`
	Instance   string `short:"i" long:"instance" help:"Instance name"`
}

func (c *CdpEvalCmd) Run() error {
	client, err := connectTab(c.Image, c.TabID, c.Instance)
	if err != nil {
		return err
	}
	defer client.Close()

	result, err := cdpEvaluate(client, c.Expression)
	if err != nil {
		return fmt.Errorf("evaluating expression: %w", err)
	}
	fmt.Println(result)
	return nil
}

// CdpWaitCmd waits for an element to appear in the page.
type CdpWaitCmd struct {
	Image    string        `arg:"" help:"Image name from images.yml"`
	TabID    string        `arg:"" help:"Tab ID (from browser list)"`
	Selector string        `arg:"" help:"CSS selector to wait for"`
	Instance string        `short:"i" long:"instance" help:"Instance name"`
	Timeout  time.Duration `long:"timeout" default:"30s" help:"Maximum wait time"`
}

func (c *CdpWaitCmd) Run() error {
	client, err := connectTab(c.Image, c.TabID, c.Instance)
	if err != nil {
		return err
	}
	defer client.Close()

	js := fmt.Sprintf(`(function() { %s; return deepQuery(%s) !== null; })()`, deepQueryJS, jsQuote(c.Selector))

	deadline := time.Now().Add(c.Timeout)
	for {
		result, err := cdpEvaluate(client, js)
		if err != nil {
			return fmt.Errorf("checking for element: %w", err)
		}
		if result == "true" {
			fmt.Fprintf(os.Stderr, "Element %s found\n", c.Selector)
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for element %s after %s", c.Selector, c.Timeout)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// CdpRawCmd sends a raw CDP command to a tab.
type CdpRawCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	TabID    string `arg:"" help:"Tab ID (from browser list)"`
	Method   string `arg:"" help:"CDP method (e.g. Page.navigate)"`
	Params   string `arg:"" optional:"" help:"JSON params"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *CdpRawCmd) Run() error {
	client, err := connectTab(c.Image, c.TabID, c.Instance)
	if err != nil {
		return err
	}
	defer client.Close()

	var params any
	if c.Params != "" {
		var raw json.RawMessage
		if err := json.Unmarshal([]byte(c.Params), &raw); err != nil {
			return fmt.Errorf("invalid JSON params: %w", err)
		}
		params = raw
	}

	result, err := client.Call(c.Method, params)
	if err != nil {
		return err
	}

	// Pretty-print the result JSON.
	var pretty json.RawMessage
	if err := json.Unmarshal(result, &pretty); err != nil {
		fmt.Println(string(result))
		return nil
	}
	out, err := json.MarshalIndent(pretty, "", "  ")
	if err != nil {
		fmt.Println(string(result))
		return nil
	}
	fmt.Println(string(out))
	return nil
}

// jsQuote returns a JavaScript string literal for use in evaluated expressions.
func jsQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// windowOffset holds Chrome's position on the desktop.
type windowOffset struct {
	ScreenX      float64 `json:"screenX"`
	ScreenY      float64 `json:"screenY"`
	ChromeHeight float64 `json:"chromeHeight"`
}

// cdpGetWindowOffset queries the Chrome window's desktop position and chrome UI height via CDP JS.
func cdpGetWindowOffset(client *CDPClient) (windowOffset, error) {
	result, err := cdpEvaluate(client, `JSON.stringify({screenX: window.screenX, screenY: window.screenY, chromeHeight: window.outerHeight - window.innerHeight})`)
	if err != nil {
		return windowOffset{}, fmt.Errorf("querying window offset: %w", err)
	}
	var offset windowOffset
	if err := json.Unmarshal([]byte(result), &offset); err != nil {
		return windowOffset{}, fmt.Errorf("parsing window offset: %w", err)
	}
	return offset, nil
}

// CdpCoordsCmd shows element coordinates in both viewport and desktop coordinate systems.
type CdpCoordsCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	TabID    string `arg:"" help:"Tab ID (from browser list)"`
	Selector string `arg:"" help:"CSS selector of element"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
	AppID    string `long:"app-id" default:"google-chrome" help:"Sway app_id for window geometry lookup"`
}

func (c *CdpCoordsCmd) Run() error {
	client, err := connectTab(c.Image, c.TabID, c.Instance)
	if err != nil {
		return err
	}
	defer client.Close()

	// Find element and get its bounding rect.
	js := fmt.Sprintf(`(function() {
		%s
		const el = deepQuery(%s);
		if (!el) return JSON.stringify({error: 'Element not found'});
		el.scrollIntoViewIfNeeded();
		const rect = el.getBoundingClientRect();
		return JSON.stringify({x: rect.x, y: rect.y, cx: rect.x + rect.width/2, cy: rect.y + rect.height/2, w: rect.width, h: rect.height});
	})()`, deepQueryJS, jsQuote(c.Selector))

	result, err := cdpEvaluate(client, js)
	if err != nil {
		return fmt.Errorf("finding element: %w", err)
	}

	var rect struct {
		X     float64 `json:"x"`
		Y     float64 `json:"y"`
		CX    float64 `json:"cx"`
		CY    float64 `json:"cy"`
		W     float64 `json:"w"`
		H     float64 `json:"h"`
		Error string  `json:"error"`
	}
	if err := json.Unmarshal([]byte(result), &rect); err != nil {
		return fmt.Errorf("parsing element position: %w", err)
	}
	if rect.Error != "" {
		return fmt.Errorf("%s", rect.Error)
	}

	fmt.Printf("Element:  %s (%.0fx%.0f)\n", c.Selector, rect.W, rect.H)
	fmt.Printf("Viewport: x=%.0f y=%.0f  center=(%.0f, %.0f)\n", rect.X, rect.Y, rect.CX, rect.CY)

	// Get CDP-based desktop offset.
	offset, err := cdpGetWindowOffset(client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not get CDP window offset: %v\n", err)
	} else {
		desktopX := rect.CX + offset.ScreenX
		desktopY := rect.CY + offset.ScreenY + offset.ChromeHeight
		fmt.Printf("Desktop:  x=%.0f y=%.0f  center=(%.0f, %.0f)  (via window.screenX/screenY, chromeHeight=%.0f)\n",
			rect.X+offset.ScreenX, rect.Y+offset.ScreenY+offset.ChromeHeight, desktopX, desktopY, offset.ChromeHeight)
	}

	// Get sway-based desktop offset.
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err == nil && engine != "" {
		swayRect, err := FindWindowRect(engine, name, c.AppID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not get sway window rect for %s: %v\n", c.AppID, err)
		} else {
			fmt.Printf("Sway:     window at (%d, %d) size %dx%d  (app_id=%s)\n",
				swayRect.X, swayRect.Y, swayRect.Width, swayRect.Height, c.AppID)
		}
	}

	return nil
}

// diagnoseCDP runs diagnostic checks when a CDP connection fails and returns
// an enriched error with actionable information printed to stderr.
func diagnoseCDP(engine, containerName, image string, origErr error) error {
	if engine == "" {
		// Local mode — minimal diagnostics
		fmt.Fprintf(os.Stderr, "\nCDP connection failed: %v\n\n", origErr)
		fmt.Fprintf(os.Stderr, "Diagnostics:\n")

		// Check if anything is listening on 9222
		out, err := exec.Command("ss", "-tlnp", "sport", "=", ":9222").Output()
		if err == nil && strings.Contains(string(out), ":9222") {
			fmt.Fprintf(os.Stderr, "  Port 9222:  bound\n")
		} else {
			fmt.Fprintf(os.Stderr, "  Port 9222:  not bound ← Chrome is likely not running\n")
		}
		fmt.Fprintf(os.Stderr, "\nHint: start Chrome with: chrome-wrapper &\n")
		return origErr
	}

	fmt.Fprintf(os.Stderr, "\nCDP connection failed: %v\n\n", origErr)
	fmt.Fprintf(os.Stderr, "Diagnostics:\n")
	fmt.Fprintf(os.Stderr, "  Container:  running (%s)\n", containerName)

	// Check Chrome process
	chromeAlive := false
	cmd := exec.Command(engine, "exec", containerName, "pgrep", "-f", "chrome.*remote-debugging-port")
	if pidOut, err := cmd.Output(); err == nil {
		pid := strings.TrimSpace(string(pidOut))
		if pid != "" {
			chromeAlive = true
			firstPid := strings.Split(pid, "\n")[0]
			fmt.Fprintf(os.Stderr, "  Chrome:     running (pid %s)\n", firstPid)
		}
	}
	if !chromeAlive {
		fmt.Fprintf(os.Stderr, "  Chrome:     NOT RUNNING ← likely cause\n")
	}

	// Check relay
	cmd = exec.Command(engine, "exec", containerName, "supervisorctl", "status", "relay-9222")
	if relayOut, err := cmd.Output(); err == nil {
		line := strings.TrimSpace(string(relayOut))
		if strings.Contains(line, "RUNNING") {
			fmt.Fprintf(os.Stderr, "  Relay 9222: running\n")
		} else {
			fmt.Fprintf(os.Stderr, "  Relay 9222: %s\n", line)
		}
	} else {
		fmt.Fprintf(os.Stderr, "  Relay 9222: unknown (no supervisord?)\n")
	}

	// Check port binding inside container
	cmd = exec.Command(engine, "exec", containerName, "ss", "-tlnp", "sport", "=", ":9222")
	if portOut, err := cmd.Output(); err == nil {
		if strings.Contains(string(portOut), ":9222") {
			fmt.Fprintf(os.Stderr, "  Port 9222:  bound\n")
		} else {
			fmt.Fprintf(os.Stderr, "  Port 9222:  not bound\n")
		}
	}

	// Actionable hints
	imgArg := image
	if imgArg == "" {
		imgArg = "<image>"
	}
	fmt.Fprintln(os.Stderr)
	if !chromeAlive {
		fmt.Fprintf(os.Stderr, "Hint: Chrome is not running. To start it:\n")
		fmt.Fprintf(os.Stderr, "  ov sway exec %s chrome-wrapper\n", imgArg)
	} else {
		fmt.Fprintf(os.Stderr, "Hint: Chrome is running but CDP is not responding.\n")
		fmt.Fprintf(os.Stderr, "  Kill and restart:\n")
		fmt.Fprintf(os.Stderr, "  ov shell %s -c \"pkill -9 -f 'chrome.*remote-debugging'\"\n", imgArg)
		fmt.Fprintf(os.Stderr, "  ov sway exec %s chrome-wrapper\n", imgArg)
	}

	return origErr
}
