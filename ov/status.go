package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"text/tabwriter"
)

// ToolStatus represents the live availability of a desktop automation tool.
type ToolStatus struct {
	Name   string `json:"name"`             // "cdp", "vnc", "sway", "wl"
	Status string `json:"status"`           // "ok", "unreachable", "-"
	Port   int    `json:"port,omitempty"`   // actual host port probed (0 for socket-based)
	Detail string `json:"detail,omitempty"` // extra info: tab count, resolution, version
}

// ContainerStatus represents the unified status of one ov service.
type ContainerStatus struct {
	Image     string       `json:"image"`
	Instance  string       `json:"instance,omitempty"`
	Status    string       `json:"status"`              // "running", "stopped", "failed", "enabled", "exited"
	Uptime    string       `json:"uptime,omitempty"`    // "Up 3 days" or ""
	Container string       `json:"container"`           // ov-<image>[-<instance>]
	Ports     []string     `json:"ports,omitempty"`     // configured port mappings
	Devices   []string     `json:"devices,omitempty"`   // device paths / CDI devices
	Tools     []ToolStatus `json:"tools,omitempty"`     // live tool probe results
	Volumes   []string     `json:"volumes,omitempty"`   // volume summaries
	Network   string       `json:"network,omitempty"`   // network mode
	Tunnel    string       `json:"tunnel,omitempty"`    // tunnel summary
	RunMode   string       `json:"run_mode"`            // "quadlet" or "direct"
}

// StatusCmd shows the status of service containers.
type StatusCmd struct {
	Image    string `arg:"" optional:"" help:"Image name or remote ref (omit to list all)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
	All      bool   `short:"a" long:"all" help:"Show all services including stopped/enabled"`
	JSON     bool   `long:"json" help:"Output as JSON"`
}

func (c *StatusCmd) Run() error {
	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	if c.Image == "" {
		return c.statusAll(rt)
	}
	return c.statusSingle(rt)
}

// --- Container PS JSON parsing ---

// ContainerPSEntry holds parsed output from `engine ps --format json`.
// Podman and Docker use different JSON formats:
// - Podman: Names is []string, Ports is []PodmanPort
// - Docker: Names is string, Ports is string
type ContainerPSEntry struct {
	Names  string // normalized to first name
	Status string
	State  string
	Ports  string // raw port info (for display, not parsed)
}

// podmanPSEntry matches Podman's JSON format.
type podmanPSEntry struct {
	Names  []string      `json:"Names"`
	Status string        `json:"Status"`
	State  string        `json:"State"`
	Ports  []podmanPort  `json:"Ports"`
}

type podmanPort struct {
	HostIP        string `json:"host_ip"`
	ContainerPort int    `json:"container_port"`
	HostPort      int    `json:"host_port"`
	Range         int    `json:"range"`
	Protocol      string `json:"protocol"`
}

// dockerPSEntry matches Docker's JSON format.
type dockerPSEntry struct {
	Names  string `json:"Names"`
	Status string `json:"Status"`
	State  string `json:"State"`
	Ports  string `json:"Ports"`
}

// listRunningContainers queries the container engine for all ov-* containers.
var listRunningContainers = defaultListRunningContainers

func defaultListRunningContainers(engine string, includeAll bool) ([]ContainerPSEntry, error) {
	binary := EngineBinary(engine)
	args := []string{"ps", "--filter", "name=ov-", "--format", "json", "--no-trunc"}
	if includeAll {
		args = append(args, "-a")
	}
	cmd := exec.Command(binary, args...)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}
	trimmed := strings.TrimSpace(string(output))
	if trimmed == "" {
		return nil, nil
	}
	return parsePSJSON(trimmed)
}

// parsePSJSON handles both podman (JSON array with typed fields) and docker (newline-delimited JSON with string fields).
func parsePSJSON(data string) ([]ContainerPSEntry, error) {
	if strings.HasPrefix(data, "[") {
		// Try podman format first (Names is []string, Ports is []object)
		var podmanEntries []podmanPSEntry
		if err := json.Unmarshal([]byte(data), &podmanEntries); err == nil {
			var result []ContainerPSEntry
			for _, pe := range podmanEntries {
				name := ""
				if len(pe.Names) > 0 {
					name = pe.Names[0]
				}
				result = append(result, ContainerPSEntry{
					Names:  name,
					Status: pe.Status,
					State:  pe.State,
					Ports:  formatPodmanPorts(pe.Ports),
				})
			}
			return result, nil
		}
		// Fallback: try docker array format
		var dockerEntries []dockerPSEntry
		if err := json.Unmarshal([]byte(data), &dockerEntries); err != nil {
			return nil, fmt.Errorf("parsing container JSON: %w", err)
		}
		var result []ContainerPSEntry
		for _, de := range dockerEntries {
			result = append(result, ContainerPSEntry{
				Names:  de.Names,
				Status: de.Status,
				State:  de.State,
				Ports:  de.Ports,
			})
		}
		return result, nil
	}
	// Docker: newline-delimited JSON objects
	var entries []ContainerPSEntry
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry dockerPSEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return nil, fmt.Errorf("parsing container JSON line: %w", err)
		}
		entries = append(entries, ContainerPSEntry{
			Names:  entry.Names,
			Status: entry.Status,
			State:  entry.State,
			Ports:  entry.Ports,
		})
	}
	return entries, nil
}

// formatPodmanPorts converts podman's structured port list to a display string.
func formatPodmanPorts(ports []podmanPort) string {
	var parts []string
	for _, p := range ports {
		parts = append(parts, fmt.Sprintf("%d:%d/%s", p.HostPort, p.ContainerPort, p.Protocol))
	}
	return strings.Join(parts, ", ")
}

// --- Device inspection ---

// inspectContainerDevices returns device paths for a running container.
var inspectContainerDevices = defaultInspectContainerDevices

func defaultInspectContainerDevices(engine, containerName string) []string {
	binary := EngineBinary(engine)
	var devices []string

	// Check for CDI devices (NVIDIA GPU)
	cmd := exec.Command(binary, "inspect", "--format",
		"{{range .HostConfig.Devices}}{{.PathOnHost}} {{end}}", containerName)
	if out, err := cmd.Output(); err == nil {
		for _, d := range strings.Fields(strings.TrimSpace(string(out))) {
			if d != "" {
				devices = append(devices, d)
			}
		}
	}

	// Check for GPU passthrough (CDI or --gpus)
	cmd = exec.Command(binary, "inspect", "--format", "{{json .HostConfig}}", containerName)
	if out, err := cmd.Output(); err == nil {
		s := string(out)
		if strings.Contains(s, "nvidia.com/gpu") || strings.Contains(s, "\"Gpus\"") {
			devices = append([]string{"nvidia.com/gpu=all"}, devices...)
		}
	}

	return devices
}

// --- Quadlet enumeration ---

// QuadletEntry represents an enabled quadlet service.
type QuadletEntry struct {
	Image    string
	Instance string
}

// listQuadletImages scans the quadlet directory for ov-*.container files.
func listQuadletImages() ([]QuadletEntry, error) {
	qdir, err := quadletDir()
	if err != nil {
		return nil, err
	}
	matches, err := filepath.Glob(filepath.Join(qdir, "ov-*.container"))
	if err != nil {
		return nil, err
	}
	var entries []QuadletEntry
	for _, path := range matches {
		name := strings.TrimSuffix(filepath.Base(path), ".container")
		name = strings.TrimPrefix(name, "ov-")
		// TODO: disambiguate instance from image name if needed
		entries = append(entries, QuadletEntry{Image: name})
	}
	return entries, nil
}

// --- Formatting helpers ---

// summarizePorts extracts host ports from port mappings for table display (sorted numerically).
func summarizePorts(ports []string) string {
	if len(ports) == 0 {
		return "-"
	}
	var nums []int
	for _, p := range ports {
		// Format: "host:container" or "host:container/proto"
		parts := strings.SplitN(p, ":", 2)
		if len(parts) >= 1 {
			portStr := strings.Split(parts[0], "/")[0]
			var port int
			if _, err := fmt.Sscanf(portStr, "%d", &port); err == nil {
				nums = append(nums, port)
			}
		}
	}
	if len(nums) == 0 {
		return "-"
	}
	sort.Ints(nums)
	var strs []string
	for _, n := range nums {
		strs = append(strs, fmt.Sprintf("%d", n))
	}
	return strings.Join(strs, ",")
}

// summarizeDevices creates a compact device summary for table display.
func summarizeDevices(devices []string) string {
	if len(devices) == 0 {
		return "-"
	}
	var tokens []string
	seen := map[string]bool{}
	for _, d := range devices {
		var token string
		switch {
		case strings.Contains(d, "nvidia.com/gpu"):
			token = "gpu"
		case strings.Contains(d, "/dev/nvidia") || strings.Contains(d, "nvidia"):
			token = "gpu"
		case strings.Contains(d, "/dev/kfd"):
			token = "kfd"
		case strings.Contains(d, "/dev/dri"):
			token = "dri"
		case strings.Contains(d, "/dev/kvm"):
			token = "kvm"
		case strings.Contains(d, "/dev/fuse"):
			token = "fuse"
		case strings.Contains(d, "/dev/net/tun"):
			token = "tun"
		default:
			token = filepath.Base(d)
		}
		if !seen[token] {
			seen[token] = true
			tokens = append(tokens, token)
		}
	}
	sort.Strings(tokens)
	return strings.Join(tokens, ",")
}

// summarizeToolsTable creates a compact tool summary for the table TOOLS column (sorted by name).
func summarizeToolsTable(tools []ToolStatus) string {
	sorted := sortedTools(tools)
	var parts []string
	for _, t := range sorted {
		if t.Status == "-" {
			continue
		}
		if t.Port > 0 {
			parts = append(parts, fmt.Sprintf("%s:%d", t.Name, t.Port))
		} else {
			parts = append(parts, t.Name)
		}
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ",")
}

// summarizeToolsDetail creates a verbose tool summary for the detailed view (sorted by name).
func summarizeToolsDetail(tools []ToolStatus) string {
	sorted := sortedTools(tools)
	var parts []string
	for _, t := range sorted {
		if t.Status == "-" {
			continue
		}
		if t.Port > 0 {
			parts = append(parts, fmt.Sprintf("%s:%d (%s)", t.Name, t.Port, t.Status))
		} else {
			parts = append(parts, fmt.Sprintf("%s (%s)", t.Name, t.Status))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ", ")
}

// sortedTools returns a copy of tools sorted alphabetically by name.
func sortedTools(tools []ToolStatus) []ToolStatus {
	sorted := make([]ToolStatus, len(tools))
	copy(sorted, tools)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Name < sorted[j].Name
	})
	return sorted
}

// extractPortFromAddress extracts the port number from a "host:port" string.
func extractPortFromAddress(address string) int {
	idx := strings.LastIndex(address, ":")
	if idx < 0 {
		return 0
	}
	var port int
	fmt.Sscanf(address[idx+1:], "%d", &port)
	return port
}

// --- Tool probing ---

// checkSupervisordStatus checks if supervisord is running and reports service counts.
func checkSupervisordStatus(engine, containerName string) ToolStatus {
	ts := ToolStatus{Name: "supervisord", Status: "-"}

	cmd := exec.Command(engine, "exec", containerName, "which", "supervisorctl")
	if cmd.Run() != nil {
		return ts
	}

	cmd = exec.Command(engine, "exec", containerName, "supervisorctl", "status")
	out, err := cmd.Output()
	if err != nil {
		// supervisorctl exits 3 when any service isn't RUNNING, but still
		// produces valid status output. Only treat as unreachable when
		// there's genuinely no output (supervisord not running / socket error).
		var exitErr *exec.ExitError
		if !(errors.As(err, &exitErr) && len(out) > 0) {
			ts.Status = "unreachable"
			return ts
		}
	}

	ts.Status = "ok"
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	running := 0
	for _, line := range lines {
		if strings.Contains(line, "RUNNING") {
			running++
		}
	}
	ts.Detail = fmt.Sprintf("%d/%d running", running, len(lines))
	return ts
}

// checkDbusStatus checks D-Bus session bus availability inside a container.
func checkDbusStatus(engine, containerName string) ToolStatus {
	ts := ToolStatus{Name: "dbus", Status: "-"}

	cmd := exec.Command(engine, "exec", containerName, "sh", "-c", "pgrep -x dbus-daemon >/dev/null 2>&1")
	if cmd.Run() != nil {
		return ts
	}

	ts.Status = "ok"

	var daemons []string
	for _, daemon := range []string{"swaync", "mako", "dunst"} {
		check := fmt.Sprintf("pgrep -x %s >/dev/null 2>&1", daemon)
		cmd := exec.Command(engine, "exec", containerName, "sh", "-c", check)
		if cmd.Run() == nil {
			daemons = append(daemons, daemon)
		}
	}
	if len(daemons) > 0 {
		ts.Detail = "notify:" + strings.Join(daemons, ",")
	}
	return ts
}

// checkOvStatus checks if the ov binary is available inside a container.
func checkOvStatus(engine, containerName string) ToolStatus {
	ts := ToolStatus{Name: "ov", Status: "-"}
	if checkToolAvailable(engine, containerName, "ov") != nil {
		return ts
	}
	ts.Status = "ok"
	cmd := exec.Command(engine, "exec", containerName, "ov", "version")
	if out, err := cmd.CombinedOutput(); err == nil {
		ts.Detail = strings.TrimSpace(string(out))
	}
	return ts
}

// probeAllTools probes all desktop tools concurrently for a running container.
func probeAllTools(engine, containerName, imageName, instance string) []ToolStatus {
	type indexedResult struct {
		index int
		ts    ToolStatus
	}
	checks := []func() ToolStatus{
		func() ToolStatus { return checkSupervisordStatus(engine, containerName) },
		func() ToolStatus { return checkCdpStatus(engine, containerName) },
		func() ToolStatus { return checkVncStatus(imageName, instance) },
		func() ToolStatus { return checkSwayStatus(engine, containerName) },
		func() ToolStatus { return checkWlStatus(engine, containerName) },
		func() ToolStatus { return checkDbusStatus(engine, containerName) },
		func() ToolStatus { return checkOvStatus(engine, containerName) },
	}

	results := make([]ToolStatus, len(checks))
	var wg sync.WaitGroup
	for i, fn := range checks {
		wg.Add(1)
		go func(idx int, f func() ToolStatus) {
			defer wg.Done()
			results[idx] = f()
		}(i, fn)
	}
	wg.Wait()

	// Filter out tools that aren't configured in this container
	var filtered []ToolStatus
	for _, r := range results {
		if r.Status != "-" {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

// --- Status: all containers ---

func (c *StatusCmd) statusAll(rt *ResolvedRuntime) error {
	containers, err := listRunningContainers(rt.RunEngine, c.All)
	if err != nil {
		return err
	}

	// Build status entries from running/stopped containers
	var statuses []ContainerStatus
	seen := map[string]bool{}
	for _, entry := range containers {
		// Podman may return comma-separated names; use first
		name := strings.Split(entry.Names, ",")[0]
		if !strings.HasPrefix(name, "ov-") {
			continue
		}
		imageName := strings.TrimPrefix(name, "ov-")
		seen[imageName] = true

		status := "running"
		state := strings.ToLower(entry.State)
		if state == "exited" || state == "stopped" || state == "created" {
			status = "stopped"
		} else if state == "dead" || state == "removing" {
			status = state
		}

		runEngine := ResolveImageEngineForDeploy(imageName, "", rt.RunEngine)
		engineBin := EngineBinary(runEngine)

		cs := ContainerStatus{
			Image:     imageName,
			Status:    status,
			Uptime:    entry.Status,
			Container: name,
			RunMode:   rt.RunMode,
		}

		// Get ports from deploy.yml first (try direct match, then scan for instance keys)
		dc, _ := LoadDeployConfig()
		if dc != nil {
			if dcImg, ok := dc.Images[imageName]; ok {
				cs.Ports = dcImg.Ports
			} else {
				// Try to match instance deploy keys (e.g., "selkies-desktop/foo" for stem "selkies-desktop-foo")
				for key, entry := range dc.Images {
					img, inst := parseDeployKey(key)
					if inst != "" && strings.TrimPrefix(containerNameInstance(img, inst), "ov-") == imageName {
						cs.Ports = entry.Ports
						break
					}
				}
			}
		}
		// Fall back to image labels
		if len(cs.Ports) == 0 {
			imageRef := fmt.Sprintf("%s:latest", imageName)
			if meta, _ := ExtractMetadata(engineBin, imageRef); meta != nil {
				cs.Ports = meta.Ports
			}
		}

		// Get devices for running containers
		if status == "running" {
			cs.Devices = inspectContainerDevices(runEngine, name)

			// Live tool probes for running containers
			cs.Tools = probeAllTools(engineBin, name, imageName, "")
		}

		statuses = append(statuses, cs)
	}

	// For --all in quadlet mode: add enabled-but-not-running from quadlet files
	if c.All && rt.RunMode == "quadlet" {
		quadlets, _ := listQuadletImages()
		for _, q := range quadlets {
			if !seen[q.Image] {
				seen[q.Image] = true
				statuses = append(statuses, ContainerStatus{
					Image:     q.Image,
					Instance:  q.Instance,
					Status:    "enabled",
					Container: "ov-" + q.Image,
					RunMode:   "quadlet",
				})
			}
		}
	}

	// Sort by image name
	sort.Slice(statuses, func(i, j int) bool {
		return statuses[i].Image < statuses[j].Image
	})

	if c.JSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(statuses)
	}

	if len(statuses) == 0 {
		fmt.Fprintln(os.Stderr, "No ov containers found")
		return nil
	}

	return printStatusTable(statuses)
}

// printStatusTable renders statuses as an aligned table.
func printStatusTable(statuses []ContainerStatus) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "IMAGE\tSTATUS\tPORTS\tDEVICES\tTOOLS")
	for _, s := range statuses {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			s.Image,
			s.Status,
			summarizePorts(s.Ports),
			summarizeDevices(s.Devices),
			summarizeToolsTable(s.Tools),
		)
	}
	return w.Flush()
}

// --- Status: single container ---

func (c *StatusCmd) statusSingle(rt *ResolvedRuntime) error {
	imageName := resolveImageName(c.Image)
	runEngine := ResolveImageEngineForDeploy(imageName, c.Instance, rt.RunEngine)
	engine := EngineBinary(runEngine)
	name := containerNameInstance(imageName, c.Instance)

	status := ContainerStatus{
		Image:     imageName,
		Instance:  c.Instance,
		Container: name,
		RunMode:   rt.RunMode,
	}

	isRunning := containerRunning(engine, name)

	if isRunning {
		status.Status = "running"

		// Get uptime from ps
		psEntries, _ := listRunningContainers(runEngine, false)
		for _, entry := range psEntries {
			psName := strings.Split(entry.Names, ",")[0]
			if psName == name {
				status.Uptime = entry.Status
				break
			}
		}

		// Get devices
		status.Devices = inspectContainerDevices(runEngine, name)

		// Live tool probes
		status.Tools = probeAllTools(engine, name, imageName, c.Instance)
	} else if rt.RunMode == "quadlet" {
		// Check systemd state
		svc := serviceNameInstance(imageName, c.Instance)
		out, err := exec.Command("systemctl", "--user", "is-active", svc).Output()
		if err == nil {
			state := strings.TrimSpace(string(out))
			switch state {
			case "active":
				status.Status = "running"
			case "failed":
				status.Status = "failed"
			default:
				status.Status = "stopped"
			}
		} else {
			// Check if quadlet file exists
			exists, _ := quadletExistsInstance(imageName, c.Instance)
			if exists {
				status.Status = "enabled"
			} else {
				status.Status = "not configured"
			}
		}
	} else {
		status.Status = "stopped"
	}

	// Enrich from deploy.yml
	dc, _ := LoadDeployConfig()
	if dc != nil {
		if dcImg, ok := dc.Images[deployKey(imageName, c.Instance)]; ok {
			status.Ports = dcImg.Ports
			if dcImg.Network != "" {
				status.Network = dcImg.Network
			}
			if dcImg.Tunnel != nil {
				status.Tunnel = formatTunnelSummary(dcImg.Tunnel)
			}
		}
	}

	// Try to get image labels for additional info (ports, volumes, network)
	imageRef := fmt.Sprintf("%s:latest", imageName)
	meta, _ := ExtractMetadata(engine, imageRef)
	if meta != nil {
		if len(status.Ports) == 0 {
			status.Ports = meta.Ports
		}
		if status.Network == "" {
			status.Network = meta.Network
		}
		for _, vol := range meta.Volumes {
			status.Volumes = append(status.Volumes, fmt.Sprintf("%s -> %s", vol.VolumeName, vol.ContainerPath))
		}
	}

	if c.JSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(status)
	}

	return printStatusDetail(status)
}

// printStatusDetail renders a single container's detailed status.
func printStatusDetail(s ContainerStatus) error {
	fmt.Printf("Image:     %s\n", s.Image)
	if s.Instance != "" {
		fmt.Printf("Instance:  %s\n", s.Instance)
	}
	if s.Uptime != "" {
		fmt.Printf("Status:    %s (%s)\n", s.Status, s.Uptime)
	} else {
		fmt.Printf("Status:    %s\n", s.Status)
	}
	fmt.Printf("Container: %s\n", s.Container)
	fmt.Printf("Mode:      %s\n", s.RunMode)
	if len(s.Ports) > 0 {
		fmt.Printf("Ports:     %s\n", strings.Join(s.Ports, ", "))
	}
	if len(s.Devices) > 0 {
		fmt.Printf("Devices:   %s\n", strings.Join(s.Devices, ", "))
	}
	if toolStr := summarizeToolsDetail(s.Tools); toolStr != "" {
		fmt.Printf("Tools:     %s\n", toolStr)
	}
	if len(s.Volumes) > 0 {
		for i, v := range s.Volumes {
			if i == 0 {
				fmt.Printf("Volumes:   %s\n", v)
			} else {
				fmt.Printf("           %s\n", v)
			}
		}
	}
	if s.Network != "" {
		fmt.Printf("Network:   %s\n", s.Network)
	}
	if s.Tunnel != "" {
		fmt.Printf("Tunnel:    %s\n", s.Tunnel)
	}
	return nil
}

// formatTunnelSummary creates a human-readable tunnel description.
func formatTunnelSummary(t *TunnelYAML) string {
	if t == nil {
		return ""
	}
	provider := t.Provider
	if provider == "" {
		provider = "tailscale"
	}
	if t.Public.All || t.Private.All {
		return fmt.Sprintf("%s (all ports)", provider)
	}
	var ports []int
	ports = append(ports, t.Public.Ports...)
	ports = append(ports, t.Private.Ports...)
	if len(ports) > 0 {
		var ps []string
		for _, p := range ports {
			ps = append(ps, fmt.Sprintf("%d", p))
		}
		return fmt.Sprintf("%s (ports %s)", provider, strings.Join(ps, ","))
	}
	return provider
}
