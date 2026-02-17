package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// OCI label key constants (all namespaced under org.overthink.)
const (
	LabelVersion  = "org.overthink.version"
	LabelImage    = "org.overthink.image"
	LabelRegistry = "org.overthink.registry"
	LabelUID      = "org.overthink.uid"
	LabelGID      = "org.overthink.gid"
	LabelUser     = "org.overthink.user"
	LabelHome     = "org.overthink.home"
	LabelPorts    = "org.overthink.ports"
	LabelVolumes  = "org.overthink.volumes"
	LabelAliases  = "org.overthink.aliases"
)

// LabelSchemaVersion is the current label schema version.
const LabelSchemaVersion = "1"

// LabelVolume represents a volume in the label JSON (short name form).
type LabelVolume struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// ImageMetadata is the runtime-relevant config extracted from image labels.
type ImageMetadata struct {
	Image    string
	Registry string
	UID      int
	GID      int
	User     string
	Home     string
	Ports    []string
	Volumes  []VolumeMount
	Aliases  []CollectedAlias
}

// InspectLabels reads OCI labels from a local image via engine inspect.
// Package-level var for testability.
var InspectLabels = defaultInspectLabels

func defaultInspectLabels(engine, imageRef string) (map[string]string, error) {
	binary := EngineBinary(engine)
	cmd := exec.Command(binary, "inspect", "--format", "{{json .Config.Labels}}", imageRef)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("inspecting %s: %w", imageRef, err)
	}

	trimmed := strings.TrimSpace(string(output))
	if trimmed == "null" || trimmed == "" {
		return nil, nil
	}

	var labels map[string]string
	if err := json.Unmarshal([]byte(trimmed), &labels); err != nil {
		return nil, fmt.Errorf("parsing labels from %s: %w", imageRef, err)
	}
	return labels, nil
}

// ExtractMetadata reads OCI labels from a local image and returns parsed ImageMetadata.
// Returns nil if the image has no org.overthink labels.
func ExtractMetadata(engine, imageRef string) (*ImageMetadata, error) {
	labels, err := InspectLabels(engine, imageRef)
	if err != nil {
		return nil, err
	}

	version := labels[LabelVersion]
	if version == "" {
		return nil, nil
	}

	meta := &ImageMetadata{
		Image:    labels[LabelImage],
		Registry: labels[LabelRegistry],
		User:     labels[LabelUser],
		Home:     labels[LabelHome],
	}

	if v := labels[LabelUID]; v != "" {
		uid, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("parsing %s=%q: %w", LabelUID, v, err)
		}
		meta.UID = uid
	}

	if v := labels[LabelGID]; v != "" {
		gid, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("parsing %s=%q: %w", LabelGID, v, err)
		}
		meta.GID = gid
	}

	if v := labels[LabelPorts]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.Ports); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelPorts, err)
		}
	}

	if v := labels[LabelVolumes]; v != "" {
		var labelVols []LabelVolume
		if err := json.Unmarshal([]byte(v), &labelVols); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelVolumes, err)
		}
		for _, lv := range labelVols {
			meta.Volumes = append(meta.Volumes, VolumeMount{
				VolumeName:    "ov-" + meta.Image + "-" + lv.Name,
				ContainerPath: lv.Path,
			})
		}
	}

	if v := labels[LabelAliases]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.Aliases); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelAliases, err)
		}
	}

	return meta, nil
}
