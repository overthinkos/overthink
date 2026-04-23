package main

// layer_artifacts.go — retrieves files declared in a layer's `artifacts:`
// block after the layer's setup has completed on the deploy target.
//
// Retrieval uses the DeployExecutor's GetFile back-channel (os.ReadFile
// on host, `ssh vm sudo cat` on VM, `podman exec cat` via the nested
// executor on container-in-container scenarios). Rewrite rules apply
// literal find/replace against the retrieved content before writing to
// the operator-side destination. Missing-file handling depends on the
// artifact's `optional:` flag.
//
// Called from ov/deploy_add_cmd.go after target.Emit succeeds and any
// deploy-scope tests pass — this is the finalization step that ends a
// successful `ov deploy add`.

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// RetrieveLayerArtifacts walks every artifact declared by every layer
// included in the deploy and pulls it back via the executor's GetFile.
// Missing non-optional files are a hard error (R1).
//
// deployName is the deploy-yml name (e.g., "vm:k3s-srv") — exposed to
// rewrite-path expansion as ${deploy_name}. envVars is an additional
// substitution context (e.g., K3S_SERVER_HOSTNAME from the deploy.env
// block, used to rewrite server URLs in a retrieved kubeconfig).
func RetrieveLayerArtifacts(
	ctx context.Context,
	exec DeployExecutor,
	layers []*Layer,
	deployName string,
	envVars map[string]string,
	opts EmitOpts,
) error {
	for _, layer := range layers {
		if layer == nil {
			continue
		}
		artifacts := layer.Artifacts()
		if len(artifacts) == 0 {
			continue
		}
		for _, a := range artifacts {
			if err := retrieveOne(ctx, exec, layer.Name, a, deployName, envVars, opts); err != nil {
				return fmt.Errorf("layer %q artifact %q: %w", layer.Name, a.Name, err)
			}
		}
	}
	return nil
}

// retrieveOne handles a single artifact.
func retrieveOne(
	ctx context.Context,
	exec DeployExecutor,
	layerName string,
	a LayerArtifact,
	deployName string,
	envVars map[string]string,
	opts EmitOpts,
) error {
	if a.Path == "" || a.RetrieveTo == "" {
		return fmt.Errorf("invalid artifact declaration (path and retrieve_to are required)")
	}

	// GetFile with asRoot=true — artifacts are typically system-owned
	// files (kubeconfig, service state) that require sudo to read on the
	// target. Layers that need a user-owned file can add a future
	// `as_user:` flag; the current schema is deliberately narrow.
	data, err := exec.GetFile(ctx, a.Path, true /*asRoot*/, opts)
	if err != nil {
		if a.Optional && isMissingFile(err) {
			return nil
		}
		return fmt.Errorf("retrieving %s: %w", a.Path, err)
	}

	// Dry-run GetFile returns nil data — skip write.
	if data == nil && opts.DryRun {
		fmt.Fprintf(os.Stderr, "[dry-run] would retrieve %s -> %s\n", a.Path, a.RetrieveTo)
		return nil
	}

	// Apply rewrite rules in declared order.
	content := string(data)
	for _, r := range a.Rewrite {
		if r.Find == "" {
			continue
		}
		find := expandArtifactVars(r.Find, deployName, layerName, envVars)
		replace := expandArtifactVars(r.Replace, deployName, layerName, envVars)
		content = strings.ReplaceAll(content, find, replace)
	}

	// Expand ${...} in retrieve_to (most useful: ${deploy_name}).
	destPath := expandArtifactVars(a.RetrieveTo, deployName, layerName, envVars)
	destPath, err = expandArtifactHome(destPath)
	if err != nil {
		return err
	}

	mode := parseArtifactMode(a.Mode)
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(destPath), err)
	}
	if err := os.WriteFile(destPath, []byte(content), mode); err != nil {
		return fmt.Errorf("writing %s: %w", destPath, err)
	}
	fmt.Fprintf(os.Stderr, "retrieved artifact %s -> %s\n", a.Path, destPath)
	return nil
}

// expandArtifactVars resolves ${deploy_name}, ${layer_name}, ${HOME},
// and any caller-supplied env vars. Unknown references are left as-is
// — literal text that happens to look like a variable reference should
// not silently empty-string out.
func expandArtifactVars(s, deployName, layerName string, envVars map[string]string) string {
	// Simple implementation: iterate known mapping and do literal replacement.
	// Does not parse arbitrary ${...}; anything else in envVars is honored
	// by passing through os.Expand-style substitution after the known set.
	mapFn := func(key string) string {
		switch key {
		case "deploy_name":
			return deployName
		case "layer_name":
			return layerName
		case "HOME":
			if home, err := os.UserHomeDir(); err == nil {
				return home
			}
			return os.Getenv("HOME")
		}
		if v, ok := envVars[key]; ok {
			return v
		}
		if v := os.Getenv(key); v != "" {
			return v
		}
		// Leave unknown refs intact.
		return "${" + key + "}"
	}
	return os.Expand(s, mapFn)
}

// expandArtifactHome expands a leading "~" to the user's home directory. Filepath
// joins don't honor "~"; this is the explicit step.
func expandArtifactHome(p string) (string, error) {
	if strings.HasPrefix(p, "~/") || p == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return p, fmt.Errorf("resolving ~: %w", err)
		}
		if p == "~" {
			return home, nil
		}
		return filepath.Join(home, p[2:]), nil
	}
	return p, nil
}

// parseArtifactMode turns an octal mode string ("0644") into an fs.FileMode.
// Empty or malformed defaults to 0644.
func parseArtifactMode(s string) fs.FileMode {
	if s == "" {
		return 0o644
	}
	if n, err := strconv.ParseUint(s, 8, 32); err == nil {
		return fs.FileMode(n)
	}
	return 0o644
}

// isMissingFile heuristically classifies an error as "file does not
// exist". Used to honor `optional: true` on artifacts. Checks both
// os.IsNotExist (local path) and common SSH-cat stderr patterns for
// remote targets.
func isMissingFile(err error) bool {
	if err == nil {
		return false
	}
	if os.IsNotExist(err) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "No such file or directory") ||
		strings.Contains(msg, "cannot access") ||
		strings.Contains(msg, "not found")
}
