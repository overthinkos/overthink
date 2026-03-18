package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

// BrowserCmd manages Chrome browser tabs in running containers
type BrowserCmd struct {
	Open  BrowserOpenCmd  `cmd:"" help:"Open a URL in the container's Chrome browser"`
	List  BrowserListCmd  `cmd:"" help:"List open Chrome browser tabs"`
	Close BrowserCloseCmd `cmd:"" help:"Close a Chrome browser tab"`
}

// BrowserOpenCmd opens a URL in the container's Chrome browser
type BrowserOpenCmd struct {
	Image    string `arg:"" help:"Image name from images.yml"`
	URL      string `arg:"" help:"URL to open"`
	Instance string `short:"i" long:"instance" help:"Instance name for multi-instance containers"`
}

func (c *BrowserOpenCmd) Run() error {
	engine, name, err := resolveBrowserContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}

	devtoolsURL, err := resolveDevToolsURL(engine, name)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	allocCtx, allocCancel := chromedp.NewRemoteAllocator(ctx, devtoolsURL)
	defer allocCancel()

	tabCtx, tabCancel := chromedp.NewContext(allocCtx)
	defer tabCancel()

	if err := chromedp.Run(tabCtx, chromedp.Navigate(c.URL)); err != nil {
		return fmt.Errorf("failed to open URL: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Opened %s in %s\n", c.URL, name)
	return nil
}

// BrowserListCmd lists open Chrome browser tabs
type BrowserListCmd struct {
	Image    string `arg:"" help:"Image name from images.yml"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

// devToolsTab represents a Chrome DevTools Protocol tab entry
type devToolsTab struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	URL   string `json:"url"`
	Type  string `json:"type"`
}

func (c *BrowserListCmd) Run() error {
	engine, name, err := resolveBrowserContainer(c.Image, c.Instance)
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

// BrowserCloseCmd closes a Chrome browser tab
type BrowserCloseCmd struct {
	Image    string `arg:"" help:"Image name from images.yml"`
	TabID    string `arg:"" help:"Tab ID to close (from browser list)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *BrowserCloseCmd) Run() error {
	engine, name, err := resolveBrowserContainer(c.Image, c.Instance)
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

// resolveBrowserContainer resolves the engine and container name, verifying the container is running.
func resolveBrowserContainer(image, instance string) (engine, name string, err error) {
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
func resolveDevToolsURL(engine, containerName string) (string, error) {
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
