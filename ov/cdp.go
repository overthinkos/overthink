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
		return fmt.Errorf("opening URL in Chrome: %w", err)
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
		return fmt.Errorf("failed to connect to Chrome DevTools: %w", err)
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
	if image == "." {
		return "", "", nil
	}
	rt, err := ResolveRuntime()
	if err != nil {
		return "", "", err
	}
	dir, _ := os.Getwd()
	imageName := resolveImageName(image)
	runEngine := ResolveImageEngineFromDir(dir, imageName, rt.RunEngine)
	engine = EngineBinary(runEngine)
	name = containerNameInstance(imageName, instance)
	if !containerRunning(engine, name) {
		return "", "", fmt.Errorf("container %s is not running", name)
	}
	return engine, name, nil
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
		return "", fmt.Errorf("container %s does not expose port 9222 (Chrome DevTools)", containerName)
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
		return nil, err
	}

	return NewCDPClient(wsURL)
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
}

func (c *CdpClickCmd) Run() error {
	client, err := connectTab(c.Image, c.TabID, c.Instance)
	if err != nil {
		return err
	}
	defer client.Close()

	// Find element and get its center coordinates.
	js := fmt.Sprintf(`(function() {
		const el = document.querySelector(%s);
		if (!el) return JSON.stringify({error: 'Element not found'});
		const rect = el.getBoundingClientRect();
		return JSON.stringify({x: rect.x + rect.width/2, y: rect.y + rect.height/2});
	})()`, jsQuote(c.Selector))

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

	// Wait briefly for navigation/effects, then report new page state.
	time.Sleep(1 * time.Second)

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

	js := fmt.Sprintf(`(function() {
		const el = document.querySelector(%s);
		if (!el) return 'Element not found';
		el.focus();
		el.value = %s;
		el.dispatchEvent(new Event('input', {bubbles: true}));
		el.dispatchEvent(new Event('change', {bubbles: true}));
		return 'ok';
	})()`, jsQuote(c.Selector), jsQuote(c.Text))

	result, err := cdpEvaluate(client, js)
	if err != nil {
		return fmt.Errorf("typing text: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("%s", result)
	}

	fmt.Fprintf(os.Stderr, "Typed text into %s\n", c.Selector)
	return nil
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

	js := fmt.Sprintf(`document.querySelector(%s) !== null`, jsQuote(c.Selector))

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
