package main

import (
	"fmt"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
)

// ImageInfo contains information about a remote image
type ImageInfo struct {
	Ref       string   `json:"ref"`
	Digest    string   `json:"digest"`
	MediaType string   `json:"mediaType"`
	Platforms []string `json:"platforms,omitempty"`
}

// InspectRemoteImage fetches information about a remote image
func InspectRemoteImage(ref string) (*ImageInfo, error) {
	// Parse the reference
	imgRef, err := name.ParseReference(ref)
	if err != nil {
		return nil, fmt.Errorf("parsing reference %q: %w", ref, err)
	}

	// Get the manifest
	manifest, err := crane.Manifest(ref)
	if err != nil {
		return nil, fmt.Errorf("fetching manifest for %q: %w", ref, err)
	}

	// Get the digest
	digest, err := crane.Digest(ref)
	if err != nil {
		return nil, fmt.Errorf("fetching digest for %q: %w", ref, err)
	}

	info := &ImageInfo{
		Ref:    imgRef.Name(),
		Digest: digest,
	}

	// Try to determine media type from manifest
	// The manifest is a byte slice, we can check the first few bytes
	if len(manifest) > 0 {
		// Simple heuristic - if it contains "manifests" it's likely a manifest list
		manifestStr := string(manifest)
		if contains(manifestStr, "\"manifests\"") {
			info.MediaType = "application/vnd.oci.image.index.v1+json"
		} else {
			info.MediaType = "application/vnd.oci.image.manifest.v1+json"
		}
	}

	return info, nil
}

// ImageExists checks if an image exists in the registry
func ImageExists(ref string) (bool, error) {
	_, err := crane.Digest(ref)
	if err != nil {
		// Check if it's a "not found" error
		return false, nil
	}
	return true, nil
}

// contains is a simple string contains check
func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
