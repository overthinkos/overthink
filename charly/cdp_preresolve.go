package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// cdp_preresolve.go is the HOST-side residue of the externalized `cdp` verb. The
// out-of-process candy/plugin-cdp provider speaks the Chrome DevTools Protocol on the
// wire (HTTP /json + the CDP WebSocket) but owns NONE of charly's venue / port-mapping
// machinery — so the host does the deployment → venue → host-reachable-DevTools-port
// resolution (cdpDevTools → resolveCheckEndpoint, opening any ssh -L forward for a
// VM/ssh venue) and hands the plugin a plain DIALABLE DevTools base URL via the
// CheckEnv. This is the cdp analogue of preresolveMcpEndpoint (mcp_preresolve.go) and
// preresolveSpiceEndpoint (spice_preresolve.go): the plugin cannot reach core's podman
// engine / project loader, so the host pre-resolves before marshaling.
//
// The connectTab / cdpDevTools / resolveTabWS / cdpEvaluate / cdpGetWindowOffset
// helpers below ALSO stay host-side because the in-core `charly check wl … --from-cdp`
// coordinate-translation path (wl.go) opens a live CDP WebSocket to read
// window.screenX/screenY — so the host keeps its own minimal CDP client (browser_cdp.go)
// even after the declarative `cdp:` verb moved out-of-process.

// CdpEnv is the host-resolved, DIALABLE Chrome DevTools endpoint shipped to the
// out-of-process candy/plugin-cdp provider via CheckEnv.Cdp. URL is the host-reachable
// DevTools base URL ("http://127.0.0.1:NNNN") that maps to the in-venue CDP port 9222 —
// a container published-port mapping, or a forwarded local address for a VM/ssh venue.
// The plugin just dials it (the /json HTTP surface + the per-tab CDP WebSocket); it
// needs no podman, no venue resolution, no OCI labels.
type CdpEnv struct {
	URL string `json:"url"` // host-reachable DevTools base URL, e.g. "http://127.0.0.1:39001"
}

// preresolveCdpEndpoint resolves a `cdp:` op's target deployment (r.Box) to the dialable
// DevTools base URL host-side. Returns:
//   - env:     the resolved endpoint (nil for a non-cdp op or a box-mode / no-box run —
//     the plugin's own no-endpoint skip then fires);
//   - cleanup: closes any opened ssh -L forward (ALWAYS non-nil — defer it
//     unconditionally); it must outlive the plugin's Invoke (the forward carries the
//     live CDP HTTP/WebSocket connection), so invokeVerbProvider defers it across Invoke;
//   - early:   a pre-dispatch CheckResult to return immediately (a FAIL when the
//     DevTools endpoint cannot be resolved — e.g. CDP port 9222 not published); nil to
//     proceed to dispatch.
//
// Mirrors preresolveMcpEndpoint's early-FAIL-on-resolution-error semantics; like
// preresolveSpiceEndpoint it also returns a cleanup because cdpDevTools opens a
// CheckEndpoint (an ssh -L forward for a VM/ssh venue) the host must release.
func (r *Runner) preresolveCdpEndpoint(c *Op) (env *CdpEnv, cleanup func(), early *CheckResult) {
	noop := func() {}
	// Non-cdp op, or no live container context (box-mode / empty box) → nothing to
	// resolve; the plugin's own box-mode / no-endpoint skip handles the degenerate cases.
	if c.Cdp == "" || r.Mode == RunModeBox || r.Box == "" {
		return nil, noop, nil
	}
	devtoolsURL, ep, err := cdpDevTools(r.Box, r.Instance)
	if err != nil {
		res := failf(c, "cdp: %s: %v", c.Cdp, err)
		return nil, noop, &res
	}
	return &CdpEnv{URL: devtoolsURL}, ep.Close, nil
}

// ---------------------------------------------------------------------------
// Host-side CDP helpers retained for the in-core `charly check wl … --from-cdp`
// coordinate-translation path (wl.go) AND the cdp endpoint pre-resolution
// above. The DECLARATIVE cdp methods (open/list/text/eval/screenshot/click/…) moved to
// the out-of-process candy/plugin-cdp; only these endpoint-resolution + window-offset
// helpers stay host-side.
// ---------------------------------------------------------------------------

// devToolsTab represents a Chrome DevTools Protocol tab entry.
type devToolsTab struct {
	ID                   string `json:"id"`
	Title                string `json:"title"`
	URL                  string `json:"url"`
	Type                 string `json:"type"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

// cdpDevTools resolves the venue (container / VM / ssh / local) and a host-reachable
// DevTools base URL for the in-venue CDP port 9222 — a container port mapping, or an
// ssh -L forward for VM/ssh venues. The caller MUST close the returned endpoint when
// done; for the persistent-WebSocket path the CDPClient takes ownership and closes it
// on Close.
func cdpDevTools(box, instance string) (devtoolsURL string, ep *CheckEndpoint, err error) {
	venue, err := resolveCheckVenue(box, instance)
	if err != nil {
		return "", nil, err
	}
	ep, err = resolveCheckEndpoint(venue, 9222)
	if err != nil {
		return "", nil, err
	}
	return "http://" + ep.Addr, ep, nil
}

// resolveTabWS fetches /json from the DevTools HTTP endpoint and returns the WebSocket
// debugger URL for the tab with the given ID. A numeric tabID is a 1-based index into
// the type:page tabs (the practical authoring contract — Chrome assigns unpredictable
// hex/UUID tab IDs); a non-numeric tabID falls through to a UUID match.
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

	if idx, err := strconv.Atoi(tabID); err == nil && idx >= 1 {
		var pages []devToolsTab
		for _, tab := range tabs {
			if tab.Type == "page" {
				pages = append(pages, tab)
			}
		}
		if idx <= len(pages) {
			tab := pages[idx-1]
			if tab.WebSocketDebuggerURL == "" {
				return "", fmt.Errorf("tab #%d has no WebSocket debugger URL", idx)
			}
			return tab.WebSocketDebuggerURL, nil
		}
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

// connectTab resolves container -> devtools URL -> tab WS URL -> CDPClient. Used by the
// in-core `charly check wl … --from-cdp` coordinate-translation path.
func connectTab(box, tabID, instance string) (*CDPClient, error) {
	devtoolsURL, ep, err := cdpDevTools(box, instance)
	if err != nil {
		return nil, err
	}

	wsURL, err := resolveTabWS(devtoolsURL, tabID)
	if err != nil {
		ep.Close()
		return nil, err
	}

	client, err := NewCDPClient(wsURL)
	if err != nil {
		ep.Close()
		return nil, err
	}
	// The CDPClient owns the forward for the life of the WebSocket.
	client.endpoint = ep
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

	var checkResult struct {
		Result struct {
			Type        string          `json:"type"`
			Value       json.RawMessage `json:"value"`
			Description string          `json:"description"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(result, &checkResult); err != nil {
		return "", fmt.Errorf("parsing evaluate result: %w", err)
	}
	if checkResult.ExceptionDetails != nil {
		return "", fmt.Errorf("JavaScript exception: %s", checkResult.ExceptionDetails.Text)
	}

	if checkResult.Result.Type == "string" {
		var s string
		if err := json.Unmarshal(checkResult.Result.Value, &s); err != nil {
			return string(checkResult.Result.Value), nil
		}
		return s, nil
	}
	if checkResult.Result.Type == "undefined" || checkResult.Result.Value == nil {
		if checkResult.Result.Description != "" {
			return checkResult.Result.Description, nil
		}
		return "", nil
	}
	return string(checkResult.Result.Value), nil
}

// windowOffset holds Chrome's position on the desktop.
type windowOffset struct {
	ScreenX      float64 `json:"screenX"`
	ScreenY      float64 `json:"screenY"`
	ChromeHeight float64 `json:"chromeHeight"`
}

// cdpGetWindowOffset queries the Chrome window's desktop position and chrome UI height
// via CDP JS. Used by the wl `--from-cdp` viewport→desktop coordinate translation.
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
