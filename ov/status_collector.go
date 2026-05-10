package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
)

// Collector orchestrates one ov-status invocation. Loop-invariant work
// (deploy.yml load, quadlet dir lookup, runtime resolution) happens once at
// construction; per-container work runs in a worker pool with a NumCPU*2 cap.
type Collector struct {
	rt      *ResolvedRuntime
	engine  *EngineClient
	quadlet string
	deploy  *DeployConfig
}

// NewCollector wires up the runtime + engine + cached deploy + quadlet dir.
// Errors are surfaced for the runtime/engine resolve; deploy.yml validation
// failures degrade gracefully (a stderr warning, deploy lookups skipped).
func NewCollector(rt *ResolvedRuntime) (*Collector, error) {
	c := &Collector{
		rt:     rt,
		engine: NewEngineClient(rt.RunEngine),
	}
	if dc, err := LoadDeployConfig(); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: deploy.yml has validation errors:\n  %v\n", err)
		fmt.Fprintln(os.Stderr, "(showing image-label-driven results below; resolve the errors to see deploy.yml-driven state)")
		fmt.Fprintln(os.Stderr, "")
	} else {
		c.deploy = dc
	}
	if qdir, err := quadletDir(); err == nil {
		c.quadlet = qdir
	}
	return c, nil
}

// All collects status for every running ov-* container, plus enabled-but-
// not-running quadlet entries when includeAll is set. Per-container work is
// fanned out across a NumCPU*2 worker pool.
func (c *Collector) All(ctx context.Context, includeAll bool) ([]ContainerStatus, error) {
	snapshots, err := c.engine.SnapshotAll(includeAll)
	if err != nil {
		return nil, err
	}
	// Filter to ov-* (the ps filter is name=ov- which already matches, but
	// belt-and-braces in case docker fuzz-matches differently).
	filtered := snapshots[:0]
	seen := map[string]bool{}
	for _, s := range snapshots {
		if !strings.HasPrefix(s.Name, "ov-") {
			continue
		}
		filtered = append(filtered, s)
		seen[s.Name] = true
	}
	snapshots = filtered

	// Quadlet enrichment: split joined container name into image + instance.
	for i := range snapshots {
		c.applyQuadletDescription(&snapshots[i])
	}

	// --all in quadlet mode: append enabled-but-not-running entries.
	if includeAll && c.rt.RunMode == "quadlet" {
		for _, q := range c.enabledQuadlets(seen) {
			snapshots = append(snapshots, q)
		}
	}

	// Worker pool fan-out across containers.
	results := make([]ContainerStatus, len(snapshots))
	workers := runtime.NumCPU() * 2
	if workers < 4 {
		workers = 4
	}
	if workers > len(snapshots) {
		workers = len(snapshots)
	}
	if workers < 1 {
		workers = 1
	}
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for i := range snapshots {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			results[i] = c.collectOne(ctx, &snapshots[i])
		}(i)
	}
	wg.Wait()

	sort.SliceStable(results, func(i, j int) bool {
		return cellImage(results[i]) < cellImage(results[j])
	})
	return results, nil
}

// Single collects status for one image+instance. Sequential — only one
// container, no need for the worker pool.
func (c *Collector) Single(ctx context.Context, image, instance string) (ContainerStatus, error) {
	imageName := resolveImageName(image)
	runEngine := ResolveImageEngineForDeploy(imageName, instance, c.rt.RunEngine)
	engine := NewEngineClient(runEngine)
	containerName := containerNameInstance(imageName, instance)

	// Build a snapshot for this single container (bounded engine call surface).
	snapshots, _ := engine.SnapshotAll(true)
	var snap *ContainerSnapshot
	for i := range snapshots {
		if snapshots[i].Name == containerName {
			snap = &snapshots[i]
			break
		}
	}
	if snap == nil {
		// Not in podman: fall back to a synthesized snapshot and let the
		// systemd / quadlet path determine its lifecycle state.
		stub := ContainerSnapshot{
			Name:     containerName,
			Image:    imageName,
			Instance: instance,
		}
		snap = &stub
	} else {
		c.applyQuadletDescription(snap)
		// applyQuadletDescription may fall back to the joined name if the
		// quadlet description is missing. The single-image path knows the
		// caller-supplied (image, instance) authoritatively, so prefer that.
		snap.Image = imageName
		snap.Instance = instance
	}

	cs := c.collectOne(ctx, snap)

	// statusSingle's lifecycle resolution: when the container isn't in
	// podman, consult systemd/quadlet to distinguish stopped vs failed vs
	// enabled vs not configured.
	if cs.Status == "" || cs.Status == "stopped" {
		cs.Status = c.resolveSystemdState(imageName, instance)
	}
	return cs, nil
}

// collectOne builds a ContainerStatus from a snapshot. Pure function over
// (snapshot, deploy, engine); no global state, safe to call concurrently
// from worker goroutines.
func (c *Collector) collectOne(ctx context.Context, snap *ContainerSnapshot) ContainerStatus {
	cs := ContainerStatus{
		Image:     snap.Image,
		Instance:  snap.Instance,
		Status:    statusFromState(snap.State),
		Uptime:    snap.Status,
		Container: snap.Name,
		Devices:   snap.Devices,
		Network:   snap.NetworkMode,
		RunMode:   c.rt.RunMode,
		Ports:     snap.Ports, // RUNTIME truth, always wins for running containers
	}

	// LIVE mounts always win for running containers. snap.Mounts is the
	// `podman inspect .Mounts[]` view — the actual host paths the
	// container is bound to RIGHT NOW. This is the source of truth that
	// distinguishes a `--bind` / `--encrypt` deploy override from the
	// OCI-label default volume backing. Pre-cutover behavior fell through
	// to the deploy.yml volume names + image-label fallback — both of
	// which describe what SHOULD be mounted, not what IS, and missed the
	// "FUSE-via-`<cipher>/plain`" case that triggered the immich
	// 2026-04-18 incident's misdiagnosis.
	if cs.Status == "running" && len(snap.Mounts) > 0 {
		cs.Volumes = formatLiveMounts(snap.Mounts)
	}

	// deploy.yml enrichment — preferred for tunnel; only fills ports when
	// runtime didn't. Volume fallback only fires when live mounts are
	// unavailable (stopped container).
	if dn, ok := c.lookupDeploy(snap.Image, snap.Instance, snap.Name); ok {
		if cs.Tunnel == "" && dn.Tunnel != nil {
			cs.Tunnel = formatTunnelSummary(dn.Tunnel)
		}
		if len(cs.Ports) == 0 {
			cs.Ports = parsePortStrings(dn.Port)
		}
		if cs.Network == "" {
			cs.Network = dn.Network
		}
		if len(cs.Volumes) == 0 {
			for _, v := range dn.Volume {
				cs.Volumes = append(cs.Volumes, v.Name)
			}
		}
	}

	// Image-label fallback for stopped/enabled rows (and any running row
	// that had no published ports). Use the BASE image name from the
	// snapshot, not the joined container name.
	if (len(cs.Ports) == 0 || len(cs.Volumes) == 0 || cs.Network == "") && snap.Image != "" {
		ref, _ := ResolveNewestLocalCalVer(c.engine.Bin(), snap.Image)
		if ref != "" {
			if meta, _ := ExtractMetadata(c.engine.Bin(), ref); meta != nil {
				if len(cs.Ports) == 0 {
					cs.Ports = parsePortStrings(meta.Ports)
				}
				if cs.Network == "" {
					cs.Network = meta.Network
				}
				if len(cs.Volumes) == 0 {
					for _, v := range meta.Volumes {
						cs.Volumes = append(cs.Volumes,
							fmt.Sprintf("%s -> %s", v.VolumeName, v.ContainerPath))
					}
				}
			}
		}
	}

	if cs.Status != "running" {
		return cs
	}
	cs.Tools = c.runProbes(ctx, snap)
	return cs
}

// runProbes runs all host probes in parallel goroutines and ALL guest probes
// in a single batched podman exec. Per-container subprocess count: ~1 (the
// guest batch) plus N HTTP/TCP probes (host probes don't fork subprocesses).
func (c *Collector) runProbes(ctx context.Context, snap *ContainerSnapshot) []ToolStatus {
	var (
		wg       sync.WaitGroup
		hostRes  = make([]ToolStatus, len(hostProbes))
		guestRes []ToolStatus
	)
	for i, p := range hostProbes {
		wg.Add(1)
		go func(i int, p HostProbe) {
			defer wg.Done()
			hostRes[i] = p.ProbeHost(ctx, snap)
		}(i, p)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		guestRes = runGuestProbes(ctx, c.engine, snap.Name, guestProbes)
	}()
	wg.Wait()

	all := append([]ToolStatus{}, hostRes...)
	all = append(all, guestRes...)
	out := all[:0]
	for _, t := range all {
		if t.Status == "-" {
			continue
		}
		out = append(out, t)
	}
	return out
}

// applyQuadletDescription fills snap.Image and snap.Instance from the
// `Description=Overthink <image> (<instance>)` line of the matching quadlet
// unit. Falls through to the joined `ov-*` name when the description isn't
// present (legacy / hand-rolled units).
func (c *Collector) applyQuadletDescription(snap *ContainerSnapshot) {
	joined := strings.TrimPrefix(snap.Name, "ov-")
	snap.Image = joined
	snap.Instance = ""
	if c.quadlet == "" {
		return
	}
	img, inst := parseQuadletDescription(filepath.Join(c.quadlet, snap.Name+".container"))
	if img != "" {
		snap.Image = img
		snap.Instance = inst
	}
}

// enabledQuadlets returns synthetic snapshots for quadlet units present on
// disk but not represented in `podman ps -a`. Used by --all to surface
// enabled-but-never-run deployments.
func (c *Collector) enabledQuadlets(seen map[string]bool) []ContainerSnapshot {
	if c.quadlet == "" {
		return nil
	}
	matches, _ := filepath.Glob(filepath.Join(c.quadlet, "ov-*.container"))
	var out []ContainerSnapshot
	for _, path := range matches {
		joined := strings.TrimSuffix(filepath.Base(path), ".container")
		if seen[joined] {
			continue
		}
		image, instance := parseQuadletDescription(path)
		if image == "" {
			image = strings.TrimPrefix(joined, "ov-")
		}
		out = append(out, ContainerSnapshot{
			Name:     joined,
			State:    "enabled",
			Image:    image,
			Instance: instance,
		})
	}
	return out
}

// lookupDeploy resolves the deploy.yml entry for one image+instance. Tries
// the canonical deployKey() shape first, then a few legacy fallbacks for
// bed-rolled keys (joined container name minus ov- prefix).
func (c *Collector) lookupDeploy(image, instance, joinedContainerName string) (DeploymentNode, bool) {
	if c.deploy == nil || c.deploy.Deploy == nil {
		return DeploymentNode{}, false
	}
	if image != "" {
		if dn, ok := c.deploy.Deploy[deployKey(image, instance)]; ok {
			return dn, true
		}
		if dn, ok := c.deploy.Deploy[image]; ok && instance == "" {
			return dn, true
		}
	}
	stripped := strings.TrimPrefix(joinedContainerName, "ov-")
	if dn, ok := c.deploy.Deploy[stripped]; ok {
		return dn, true
	}
	return DeploymentNode{}, false
}

// resolveSystemdState consults systemctl + the quadlet dir to decide whether
// a non-podman-listed deployment is stopped, failed, enabled, or not
// configured. Used by Single().
func (c *Collector) resolveSystemdState(image, instance string) string {
	if c.rt.RunMode != "quadlet" {
		return "stopped"
	}
	svc := serviceNameInstance(image, instance)
	out, err := exec.Command("systemctl", "--user", "is-active", svc).Output()
	if err == nil {
		switch strings.TrimSpace(string(out)) {
		case "active":
			return "running"
		case "failed":
			return "failed"
		default:
			return "stopped"
		}
	}
	exists, _ := quadletExistsInstance(image, instance)
	if exists {
		return "enabled"
	}
	return "not configured"
}

// statusFromState normalises engine state vocabulary to ours.
func statusFromState(state string) string {
	switch strings.ToLower(state) {
	case "running":
		return "running"
	case "exited", "stopped", "created":
		return "stopped"
	case "dead":
		return "dead"
	case "removing":
		return "removing"
	case "paused":
		return "paused"
	case "enabled":
		return "enabled"
	case "":
		return "stopped"
	default:
		return strings.ToLower(state)
	}
}

// parsePortStrings converts a deploy.yml / image-label []string ports list
// to []PortMapping using the canonical ParsePortMapping. Unparseable entries
// log loudly to stderr (matches the existing behaviour for tunnel ports).
func parsePortStrings(ports []string) []PortMapping {
	if len(ports) == 0 {
		return nil
	}
	var out []PortMapping
	for _, raw := range ports {
		p, ok := ParsePortMapping(strings.TrimSpace(raw))
		if !ok {
			fmt.Fprintf(os.Stderr, "WARNING: ov status: cannot parse port mapping %q\n", raw)
			continue
		}
		out = append(out, PortMapping{
			HostIP:   p.BindAddr,
			HostPort: p.Host,
			CtrPort:  p.Container,
			Proto:    p.Protocol,
		})
	}
	return out
}

// parseQuadletDescription reads a `.container` quadlet file and returns
// (image, instance) parsed from its `Description=Overthink <image>
// (<instance>)` line. ("", "") on missing/malformed file — callers fall back
// to the filename-derived joined name.
func parseQuadletDescription(unitPath string) (image, instance string) {
	data, err := os.ReadFile(unitPath)
	if err != nil {
		return "", ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "Description=Overthink ") {
			continue
		}
		body := strings.TrimPrefix(line, "Description=Overthink ")
		if open := strings.LastIndex(body, " ("); open != -1 && strings.HasSuffix(body, ")") {
			image = strings.TrimSpace(body[:open])
			instance = strings.TrimSpace(body[open+2 : len(body)-1])
			return image, instance
		}
		return strings.TrimSpace(body), ""
	}
	return "", ""
}

// formatLiveMounts renders the live `podman inspect .Mounts[]` view as
// the strings shown in `ov status`'s Volumes column / detail field. For
// type=volume entries, format is `<name>: <mountpoint> -> <dest>`. For
// type=bind, format is `<name-or-bind>: <source> -> <dest>` with an
// `(enc)` suffix when the source path matches the gocryptfs convention
// `<...>/encrypted/<vol>/plain` — that's the FUSE-mounted plain dir
// shown to the container, NOT the OCI-label default volume name. The
// (enc) marker is what was missing during the immich-2026-04-18
// diagnosis: `ov status` showed `ov-immich-cache -> /home/user/.immich/
// cache` (the OCI label default) instead of the actual bind to the
// gocryptfs plain dir, masking the encryption state from the operator.
func formatLiveMounts(mounts []MountInfo) []string {
	out := make([]string, 0, len(mounts))
	for _, m := range mounts {
		name := m.Name
		if name == "" {
			name = "bind"
		}
		display := fmt.Sprintf("%s: %s -> %s", name, m.Source, m.Destination)
		if isEncryptedPlainPath(m.Source) {
			display += " (enc)"
		}
		out = append(out, display)
	}
	return out
}

// isEncryptedPlainPath returns true when path looks like a gocryptfs
// plain dir under an ov-managed encrypted-storage tree, i.e. matches
// `.../encrypted/<anything>/plain`. Used to flag live mounts as encryption
// FUSE mountpoints in the status display. Path-only — does NOT verify the
// FUSE mount is actually live (that's handled by the verifyBindMounts
// check in /ov-automation:enc).
func isEncryptedPlainPath(p string) bool {
	if !strings.HasSuffix(p, "/plain") {
		return false
	}
	return strings.Contains(p, "/encrypted/")
}
