package main

import (
	"context"
	"fmt"
	"os"
)

// StatusCmd shows the runtime status of one or all ov deployments. The
// implementation lives in:
//
//	status_engine.go     — single-touchpoint to podman/docker (one batched ps + inspect)
//	status_collector.go  — Collector orchestration + per-container worker pool
//	status_probes.go     — Probe / HostProbe / GuestProbe interfaces + 7 concrete probes
//	status_render.go     — Table / JSON / Detail renderers + cell formatters
//
// Orphan reaping moved to its own command (`ov reap-orphans`, see status_reap.go).
type StatusCmd struct {
	Image    string `arg:"" optional:"" help:"Image name (omit to list all ov containers)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
	All      bool   `short:"a" long:"all" help:"Include enabled-but-not-running services"`
	Nested   bool   `long:"nested" help:"Probe nested children + live k8s workloads (multi-hop, slower)"`
	JSON     bool   `long:"json" help:"Output as JSON"`
}

func (c *StatusCmd) Run() error {
	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}
	col, err := NewCollector(rt)
	if err != nil {
		return err
	}
	ctx := context.Background()

	c.Image, c.Instance = canonicalizeDeployArg(c.Image, c.Instance)
	if c.Image == "" {
		statuses, err := col.All(ctx, c.All, c.Nested)
		if err != nil {
			return err
		}
		if c.JSON {
			return RenderJSON(os.Stdout, statuses)
		}
		if len(statuses) == 0 {
			fmt.Fprintln(os.Stderr, "No ov containers found")
			return nil
		}
		return RenderTable(os.Stdout, statuses)
	}

	cs, err := col.Single(ctx, c.Image, c.Instance)
	if err != nil {
		return err
	}
	if c.JSON {
		return RenderJSONOne(os.Stdout, cs)
	}
	return RenderDetail(os.Stdout, cs)
}
