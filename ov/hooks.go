package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// HooksConfig holds lifecycle hook scripts for a layer
type HooksConfig struct {
	PostEnable string `yaml:"post_enable,omitempty"`
	PreRemove  string `yaml:"pre_remove,omitempty"`
}

// CollectHooks collects and concatenates hooks from all layers in an image's layer chain.
// Hooks from multiple layers are concatenated in layer order.
func CollectHooks(cfg *Config, layers map[string]*Layer, imageName string) *HooksConfig {
	var allLayerNames []string
	current := imageName
	for {
		img, ok := cfg.Images[current]
		if !ok {
			break
		}
		resolved, err := ResolveLayerOrder(img.Layers, layers, nil)
		if err != nil {
			break
		}
		allLayerNames = append(allLayerNames, resolved...)
		if baseImg, isInternal := cfg.Images[img.Base]; isInternal && baseImg.IsEnabled() {
			current = img.Base
		} else {
			break
		}
	}

	var postEnable, preRemove []string
	seen := make(map[string]bool)
	for _, layerName := range allLayerNames {
		if seen[layerName] {
			continue
		}
		seen[layerName] = true
		layer, ok := layers[layerName]
		if !ok {
			continue
		}
		if layer.hooks == nil {
			continue
		}
		if layer.hooks.PostEnable != "" {
			postEnable = append(postEnable, strings.TrimSpace(layer.hooks.PostEnable))
		}
		if layer.hooks.PreRemove != "" {
			preRemove = append(preRemove, strings.TrimSpace(layer.hooks.PreRemove))
		}
	}

	if len(postEnable) == 0 && len(preRemove) == 0 {
		return nil
	}

	return &HooksConfig{
		PostEnable: strings.Join(postEnable, "\n"),
		PreRemove:  strings.Join(preRemove, "\n"),
	}
}

// RunHook executes a hook script inside a running container.
// Environment variables are passed via -e flags.
// Returns nil on success, error on failure.
func RunHook(engine, containerName, hookScript string, envVars []string) error {
	if hookScript == "" {
		return nil
	}

	args := []string{"exec"}
	for _, env := range envVars {
		args = append(args, "-e", env)
	}
	args = append(args, containerName, "sh", "-c", hookScript)

	cmd := exec.Command(engine, args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	fmt.Fprintf(os.Stderr, "Running hook in %s...\n", containerName)
	return cmd.Run()
}

// removeVolumes removes all named volumes matching the image/instance prefix.
func removeVolumes(engine, imageName, instance string) {
	prefix := "ov-" + imageName + "-"
	if instance != "" {
		prefix = "ov-" + imageName + "-" + instance + "-"
	}

	out, err := exec.Command(engine, "volume", "ls", "--format", "{{.Name}}", "--filter", "name="+prefix).Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: listing volumes: %v\n", err)
		return
	}

	for _, name := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if name == "" {
			continue
		}
		rm := exec.Command(engine, "volume", "rm", name)
		rm.Stderr = os.Stderr
		if err := rm.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: removing volume %s: %v\n", name, err)
		} else {
			fmt.Fprintf(os.Stderr, "Removed volume %s\n", name)
		}
	}
}
