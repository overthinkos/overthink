package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// SeedCmd copies image data into empty bind mount directories that override layer volumes.
type SeedCmd struct {
	Image string `arg:"" help:"Image name from images.yml"`
	Tag   string `long:"tag" default:"latest" help:"Image tag (default: latest)"`
}

func (c *SeedCmd) Run() error {
	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	// Resolve from image labels (no images.yml dependency)
	engine := ResolveImageEngineForDeploy(c.Image, rt.RunEngine)
	ref := fmt.Sprintf("%s:%s", c.Image, c.Tag)
	meta, metaErr := ExtractMetadata(engine, ref)
	if metaErr != nil {
		return metaErr
	}
	if meta == nil {
		return fmt.Errorf("image %s has no embedded metadata; rebuild with latest ov", ref)
	}
	dc, _ := LoadDeployConfig()
	MergeDeployOntoMetadata(meta, dc)

	var imageRef string
	if meta.Registry != "" {
		imageRef = fmt.Sprintf("%s/%s:%s", meta.Registry, c.Image, c.Tag)
	} else {
		imageRef = ref
	}
	uid, gid := meta.UID, meta.GID

	// Resolve volume backing from labels + deploy config
	var deployVolumes []DeployVolumeConfig
	if dc != nil {
		if overlay, ok := dc.Images[c.Image]; ok {
			deployVolumes = overlay.Volumes
		}
	}
	_, bindMounts := ResolveVolumeBacking(c.Image, meta.Volumes, deployVolumes, meta.Home, rt.EncryptedStoragePath, rt.VolumesPath)

	if len(bindMounts) == 0 {
		fmt.Fprintln(os.Stderr, "No bind-backed volumes configured (use ov config --bind to configure)")
		return nil
	}

	seeded := 0
	for _, bm := range bindMounts {
		contPath := bm.ContPath

		if !isDirEmpty(bm.HostPath) {
			fmt.Fprintf(os.Stderr, "%s: skipping (not empty)\n", bm.Name)
			continue
		}

		fmt.Fprintf(os.Stderr, "%s: seeding from %s ...\n", bm.Name, contPath)

		engine := ResolveImageEngineForDeploy(c.Image, rt.RunEngine)
		args := []string{
			EngineBinary(engine), "run", "--rm",
			"-v", fmt.Sprintf("%s:/seed", bm.HostPath),
		}
		if engine == "podman" {
			args = append(args, fmt.Sprintf("--userns=keep-id:uid=%d,gid=%d", uid, gid))
		}
		args = append(args, imageRef, "bash", "-c",
			fmt.Sprintf("cp -a %s/. /seed/ 2>/dev/null; true", contPath))

		cmd := exec.Command(args[0], args[1:]...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("seeding %s: %w", bm.Name, err)
		}
		fmt.Fprintf(os.Stderr, "%s: done\n", bm.Name)
		seeded++
	}

	if seeded == 0 {
		fmt.Fprintln(os.Stderr, "Nothing to seed (all bind mount directories already have data)")
	} else {
		fmt.Fprintf(os.Stderr, "Seeded %d bind mount(s)\n", seeded)
	}
	return nil
}

// isDirEmpty returns true if the directory is empty, doesn't exist, or is not a directory.
func isDirEmpty(path string) bool {
	entries, err := os.ReadDir(path)
	if err != nil {
		return true // treat errors (including not-exist) as empty
	}
	return len(entries) == 0
}

// collectLayerVolumePaths returns a map of volume name -> expanded container path
// for all layer volumes in the full image chain (image -> base -> base's base).
func collectLayerVolumePaths(cfg *Config, layers map[string]*Layer, imageName string, home string) map[string]string {
	result := make(map[string]string)

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

		for _, layerName := range resolved {
			layer, ok := layers[layerName]
			if !ok || !layer.HasVolumes {
				continue
			}
			for _, vol := range layer.Volumes() {
				if _, exists := result[vol.Name]; !exists {
					result[vol.Name] = expandHome(vol.Path, home)
				}
			}
		}

		if baseImg, isInternal := cfg.Images[img.Base]; isInternal && baseImg.IsEnabled() {
			current = img.Base
		} else {
			break
		}
	}

	return result
}

// seedSummary returns a human-readable representation of the seed operation.
func seedSummary(volPaths map[string]string, bindMounts []ResolvedBindMount) string {
	var b strings.Builder
	for _, bm := range bindMounts {
		contPath, ok := volPaths[bm.Name]
		if !ok {
			continue
		}
		empty := isDirEmpty(bm.HostPath)
		status := "has data"
		if empty {
			status = "EMPTY (will seed)"
		}
		fmt.Fprintf(&b, "  %s: %s -> %s [%s]\n", bm.Name, contPath, bm.HostPath, status)
	}
	return b.String()
}
