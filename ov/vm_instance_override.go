package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// VmInstanceOverride is the operator-side per-libvirt-domain override
// file. Lives at ~/.local/share/ov/vm/<domain-name>/instance.yml — a
// sibling to the SSH key, NVRAM, and console socket already kept
// there. The presence of this file lets an operator override the
// project-level VM classification for their specific instance
// without modifying the project's deploy.yml or vm.yml.
//
// Today the override only carries `disposable:` and `lifecycle:` —
// the two fields that gate `ov rebuild <vm-entity>`. Future fields
// (per-instance ports, env, add_layers) can be added without
// breaking the on-disk format because yaml.v3 unknown-keys defaults
// to forgiving.
//
// Pointer to bool for Disposable lets the loader distinguish "field
// absent → use upstream classification" from "field set to false →
// override-explicit no". A bare bool would conflate the two.
//
// Use case: project deploy.yml's arch-vm has disposable: true. An
// operator who wants to use the arch-vm bed for a long-running
// experiment can write
//   ~/.local/share/ov/vm/ov-arch/instance.yml:
//     disposable: false
//     lifecycle: long-running
// and `ov rebuild arch` will refuse with the standard refusal
// message — no need to edit the project's deploy.yml or stash a
// different lifecycle tag in version control.
type VmInstanceOverride struct {
	// Disposable, when non-nil, overrides the upstream classification
	// for the libvirt domain at this path. Pointer-typed to
	// distinguish "absent" from "explicit false".
	Disposable *bool `yaml:"disposable,omitempty"`
	// Lifecycle, when non-empty, overrides the upstream lifecycle tag.
	Lifecycle string `yaml:"lifecycle,omitempty"`
}

// VmInstanceOverridePath returns the canonical path for a domain's
// instance override file. The domainName is the libvirt/qemu
// domain identifier (typically "ov-<vmEntityName>").
func VmInstanceOverridePath(domainName string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home dir: %w", err)
	}
	return filepath.Join(home, ".local", "share", "ov", "vm", domainName, "instance.yml"), nil
}

// LoadVmInstanceOverride reads the per-domain override file and
// returns a parsed VmInstanceOverride. Returns (nil, nil) when the
// file doesn't exist (the common case — most operators don't
// override). Returns (nil, error) when the file exists but is
// unreadable or contains invalid YAML — silent fall-through there
// would mask real config problems.
func LoadVmInstanceOverride(domainName string) (*VmInstanceOverride, error) {
	path, err := VmInstanceOverridePath(domainName)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var ov VmInstanceOverride
	if err := yaml.Unmarshal(data, &ov); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &ov, nil
}

// ApplyToVmClassification merges an override on top of an upstream
// (disposable, lifecycle) pair. When the override is nil or empty,
// returns the upstream pair unchanged. When set, the override wins.
//
// Centralised here so every caller (rebuild.go's VM-entity path,
// future commands that want per-instance classification) gets the
// same semantics.
func (o *VmInstanceOverride) ApplyToVmClassification(disposable bool, lifecycle string) (bool, string) {
	if o == nil {
		return disposable, lifecycle
	}
	if o.Disposable != nil {
		disposable = *o.Disposable
	}
	if o.Lifecycle != "" {
		lifecycle = o.Lifecycle
	}
	return disposable, lifecycle
}
