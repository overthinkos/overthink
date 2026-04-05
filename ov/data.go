package main

import (
	"fmt"
	"os"
	"os/exec"
)

// DataProvisionMode controls how provisionData copies files.
type DataProvisionMode string

const (
	// DataProvisionInitial copies all data into empty directories (cp -a).
	// Used by ov config for first-time setup.
	DataProvisionInitial DataProvisionMode = "initial"

	// DataProvisionMerge adds missing files without overwriting existing ones (cp -an).
	// Used by ov update to add new data while preserving user modifications.
	DataProvisionMerge DataProvisionMode = "merge"

	// DataProvisionForce overwrites all data unconditionally (cp -a).
	// Used by ov config --force-seed.
	DataProvisionForce DataProvisionMode = "force"
)

// provisionData copies data from image staging (/data/) into bind-backed volumes.
// It matches data entries from image metadata against the resolved bind mounts,
// and copies staging data into the corresponding host directories.
//
// For data images (FROM scratch, no shell), it uses engine create + cp.
// For regular images, it uses engine run with cp.
func provisionData(engine string, imageRef string, meta *ImageMetadata,
	bindMounts []ResolvedBindMount, mode DataProvisionMode) (int, error) {

	if len(meta.DataEntries) == 0 || len(bindMounts) == 0 {
		return 0, nil
	}

	// Build lookup: volume name -> bind mount
	bmByVolume := make(map[string]*ResolvedBindMount, len(bindMounts))
	for i := range bindMounts {
		// Volume name in bind mount is prefixed with ov-<image>-; data entries use bare name.
		// The ResolvedBindMount.Name is the bare volume name.
		bmByVolume[bindMounts[i].Name] = &bindMounts[i]
	}

	seeded := 0
	for _, entry := range meta.DataEntries {
		bm, ok := bmByVolume[entry.Volume]
		if !ok {
			continue // volume not configured as bind mount
		}

		// Check whether to seed based on mode
		switch mode {
		case DataProvisionInitial:
			if !isDirEmpty(bm.HostPath) {
				fmt.Fprintf(os.Stderr, "  %s: skipping (not empty)\n", entry.Volume)
				continue
			}
		case DataProvisionMerge:
			// Always attempt merge (cp -an is safe on non-empty dirs)
		case DataProvisionForce:
			// Always copy (overwrites existing)
		}

		fmt.Fprintf(os.Stderr, "  %s: provisioning from %s ...\n", entry.Volume, entry.Staging)

		// Resolve target directory: preserve dest subdirectory within volume
		targetPath := bm.HostPath
		if entry.Dest != "" {
			targetPath = targetPath + "/" + entry.Dest
		}

		// Ensure host directory exists
		if err := os.MkdirAll(targetPath, 0755); err != nil {
			return seeded, fmt.Errorf("creating directory %s: %w", targetPath, err)
		}

		var err error
		if meta.DataImage {
			err = provisionFromScratchImage(engine, imageRef, entry, targetPath)
		} else {
			err = provisionFromRunnableImage(engine, imageRef, meta, entry, targetPath, mode)
		}
		if err != nil {
			return seeded, fmt.Errorf("provisioning %s: %w", entry.Volume, err)
		}

		fmt.Fprintf(os.Stderr, "  %s: done\n", entry.Volume)
		seeded++
	}

	return seeded, nil
}

// provisionFromScratchImage copies data from a scratch-based data image using
// engine create + cp (since scratch images have no shell).
func provisionFromScratchImage(engine string, imageRef string, entry LabelDataEntry, hostPath string) error {
	binary := EngineBinary(engine)
	containerName := fmt.Sprintf("ov-data-seed-%d", os.Getpid())

	// Create temporary container (doesn't need to run)
	createCmd := exec.Command(binary, "create", "--name", containerName, imageRef)
	createCmd.Stderr = os.Stderr
	if err := createCmd.Run(); err != nil {
		return fmt.Errorf("creating container: %w", err)
	}

	// Clean up container when done
	defer func() {
		rmCmd := exec.Command(binary, "rm", containerName)
		rmCmd.Run() //nolint:errcheck
	}()

	// Copy data from container to host
	src := fmt.Sprintf("%s:%s.", containerName, entry.Staging)
	cpCmd := exec.Command(binary, "cp", src, hostPath+"/")
	cpCmd.Stdout = os.Stdout
	cpCmd.Stderr = os.Stderr
	return cpCmd.Run()
}

// provisionFromRunnableImage copies data from a regular image (has shell) by
// running a temporary container with cp.
func provisionFromRunnableImage(engine string, imageRef string, meta *ImageMetadata,
	entry LabelDataEntry, hostPath string, mode DataProvisionMode) error {

	binary := EngineBinary(engine)

	// Build cp command based on mode
	var cpFlag string
	switch mode {
	case DataProvisionMerge:
		cpFlag = "cp -an" // no-clobber: add new files, preserve existing
	default:
		cpFlag = "cp -a" // full copy (initial and force)
	}

	args := []string{
		binary, "run", "--rm",
		"-v", fmt.Sprintf("%s:/seed", hostPath),
	}
	if engine == "podman" {
		args = append(args, fmt.Sprintf("--userns=keep-id:uid=%d,gid=%d", meta.UID, meta.GID))
	}
	args = append(args, imageRef, "bash", "-c",
		fmt.Sprintf("%s %s. /seed/ 2>/dev/null; true", cpFlag, entry.Staging))

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// isDirEmpty returns true if the directory is empty, doesn't exist, or is not a directory.
func isDirEmpty(path string) bool {
	entries, err := os.ReadDir(path)
	if err != nil {
		return true // treat errors (including not-exist) as empty
	}
	return len(entries) == 0
}
