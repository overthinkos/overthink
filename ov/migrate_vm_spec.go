package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// MigrateVmSpecCmd is `ov migrate vm-spec`. Walks every kind:image
// entity with `bootc: true` (or non-empty `vm:` / `libvirt:` fields),
// synthesizes a paired kind:vm entity that carries the equivalent
// VmSpec, and writes the output to vms.yml at the repo root. Also
// converts any layer.yml with a list-of-strings `libvirt:` field to
// the new structured `libvirt.snippets:` form.
//
// Idempotent: running twice produces identical output (the emitted
// kind:vm entity for a given image is deterministically derived from
// the same inputs).
//
// Does NOT delete the legacy fields — that's Task 21's Hard Cutover
// commit, which removes VmConfig, ImageConfig.{Bootc,Vm,Libvirt}, and
// the OCI label emitters in one atomic step. This migration emits the
// kind:vm entities alongside the legacy fields so the two code paths
// coexist until the cutover removes the old branch.
type MigrateVmSpecCmd struct {
	DryRun bool `long:"dry-run" help:"Print files that would be written, don't touch the filesystem"`
}

// Run executes the migration.
func (c *MigrateVmSpecCmd) Run() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	written, err := MigrateVmSpec(MigrateVmSpecOpts{
		Dir:    cwd,
		DryRun: c.DryRun,
	})
	if err != nil {
		return err
	}
	prefix := "wrote "
	if c.DryRun {
		prefix = "[dry-run] would write "
	}
	for _, p := range written {
		fmt.Println(prefix + p)
	}
	if len(written) == 0 {
		fmt.Println("No legacy VM definitions found — nothing to migrate.")
	}
	return nil
}

// MigrateVmSpecOpts carries the migration-command inputs.
type MigrateVmSpecOpts struct {
	Dir    string
	DryRun bool
}

// MigrateVmSpec performs the migration and returns the list of files
// it wrote (or would write under --dry-run).
func MigrateVmSpec(opts MigrateVmSpecOpts) ([]string, error) {
	uf, ok, err := LoadUnified(opts.Dir)
	if err != nil {
		return nil, fmt.Errorf("loading overthink.yml: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("no overthink.yml in %s (run `ov migrate unified` first)", opts.Dir)
	}

	// Collect legacy-bootc image names in deterministic (sorted) order
	// so the output is byte-stable across runs.
	var legacyNames []string
	for name, img := range uf.Images {
		if isLegacyBootcImage(&img) {
			legacyNames = append(legacyNames, name)
		}
	}
	sort.Strings(legacyNames)

	// Build the kind:vm entities.
	vms := map[string]*VmSpec{}
	if uf.VMs != nil {
		// Preserve any existing kind:vm entries so the rewrite is additive.
		for k, v := range uf.VMs {
			vms[k] = v
		}
	}
	for _, name := range legacyNames {
		img := uf.Images[name]
		vmName := name + "-bootc"
		if _, already := vms[vmName]; already {
			// Don't clobber an existing entry — user may have customized.
			continue
		}
		vms[vmName] = synthesizeBootcVmSpec(name, &img)
	}

	// If there's nothing new to add, don't touch the filesystem.
	if len(vms) == 0 {
		return nil, nil
	}

	// Serialize to vms.yml at the project root.
	target := filepath.Join(opts.Dir, "vms.yml")
	body, err := renderVmsYaml(vms)
	if err != nil {
		return nil, fmt.Errorf("rendering vms.yml: %w", err)
	}

	// Idempotency guard: if the target file exists with identical body,
	// skip the write (and don't list in the 'written' output).
	if existing, err := os.ReadFile(target); err == nil && string(existing) == body {
		return nil, nil
	}

	if opts.DryRun {
		return []string{target}, nil
	}
	if err := os.WriteFile(target, []byte(body), 0o644); err != nil {
		return nil, fmt.Errorf("writing %s: %w", target, err)
	}

	// Also ensure vms.yml is in the includes list of overthink.yml.
	if err := ensureVmsIncluded(opts.Dir); err != nil {
		return nil, fmt.Errorf("updating overthink.yml includes: %w", err)
	}

	return []string{target}, nil
}

// isLegacyBootcImage returns true when an ImageConfig has fields that
// should migrate into a kind:vm entity: bootc=true, a non-nil vm:, or
// any libvirt: snippets.
func isLegacyBootcImage(img *ImageConfig) bool {
	if img.Bootc {
		return true
	}
	if img.Vm != nil {
		return true
	}
	if len(img.Libvirt) > 0 {
		return true
	}
	return false
}

// synthesizeBootcVmSpec converts legacy ImageConfig fields into a new
// VmSpec (bootc source branch). The mapping mirrors the plan's
// "Hard Cutover & Config Migration" table.
func synthesizeBootcVmSpec(imageName string, img *ImageConfig) *VmSpec {
	spec := &VmSpec{
		Source: VmSource{
			Kind:  "bootc",
			Image: imageName,
		},
	}

	if img.Vm != nil {
		v := img.Vm
		spec.DiskSize = v.DiskSize
		spec.Ram = v.Ram
		spec.Cpus = v.Cpus
		spec.Firmware = v.Firmware

		spec.Source.Rootfs = v.Rootfs
		spec.Source.RootSize = v.RootSize
		spec.Source.KernelArgs = v.KernelArgs
		spec.Source.Transport = v.Transport

		// Network: old string form → structured. Unknown values map
		// through unchanged as a bridge name when not "user".
		if v.Network != "" {
			if v.Network == "user" {
				spec.Network = &VmNetwork{Mode: "user"}
			} else {
				spec.Network = &VmNetwork{Mode: "bridge", Bridge: v.Network}
			}
		}

		// SSH port.
		if v.SshPort != 0 {
			spec.SSH = &VmSSH{Port: v.SshPort, User: "root"}
		}
	}

	// libvirt: [<xml>, …] (list of strings) → libvirt.snippets: [<xml>, …]
	if len(img.Libvirt) > 0 {
		if spec.Libvirt == nil {
			spec.Libvirt = &LibvirtConfig{}
		}
		spec.Libvirt.Snippets = append([]string{}, img.Libvirt...)
	}

	return spec
}

// renderVmsYaml serializes the vm map into a vms.yml body with a
// deterministic header + sorted entries.
func renderVmsYaml(vms map[string]*VmSpec) (string, error) {
	doc := struct {
		VMs map[string]*VmSpec `yaml:"vms"`
	}{VMs: vms}

	body, err := yaml.Marshal(&doc)
	if err != nil {
		return "", err
	}

	header := `# vms.yml — kind:vm entity definitions for the repo.
#
# Produced (or extended) by ` + "`ov migrate vm-spec`" + ` from legacy
# ` + "`bootc: true`" + ` image entries + layer-level ` + "`libvirt: [<xml>, …]`" + `
# snippets. Hand-edit welcome; subsequent ` + "`ov migrate vm-spec`" + ` runs
# preserve pre-existing ` + "`vms:`" + ` keys and never clobber customizations.
#
# Resolved through overthink.yml includes: alongside images.yml.

`
	return header + string(body), nil
}

// ensureVmsIncluded makes sure overthink.yml lists vms.yml under
// includes:. Idempotent.
func ensureVmsIncluded(dir string) error {
	path := filepath.Join(dir, "overthink.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil // overthink.yml not present — skip silently
	}

	var raw struct {
		Version  int      `yaml:"version"`
		Includes []string `yaml:"includes,omitempty"`
		// Preserve anything else by round-tripping as a generic tree.
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parsing overthink.yml: %w", err)
	}
	for _, inc := range raw.Includes {
		if inc == "vms.yml" {
			return nil // already present
		}
	}

	// Rewrite includes: with vms.yml appended, preserving the rest of
	// the file by operating on the yaml.Node tree.
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return err
	}
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		mapping := root.Content[0]
		if mapping.Kind != yaml.MappingNode {
			return fmt.Errorf("overthink.yml root is not a mapping")
		}
		// Find or create `includes:` key.
		var incSeq *yaml.Node
		for i := 0; i < len(mapping.Content); i += 2 {
			if mapping.Content[i].Value == "includes" {
				incSeq = mapping.Content[i+1]
				break
			}
		}
		if incSeq == nil {
			// Append the key/value pair.
			keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: "includes", Tag: "!!str"}
			valNode := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			mapping.Content = append(mapping.Content, keyNode, valNode)
			incSeq = valNode
		}
		// Append "vms.yml" unless already present.
		present := false
		for _, item := range incSeq.Content {
			if item.Value == "vms.yml" {
				present = true
				break
			}
		}
		if !present {
			incSeq.Content = append(incSeq.Content, &yaml.Node{
				Kind: yaml.ScalarNode, Value: "vms.yml", Tag: "!!str",
			})
		}
	}

	out, err := yaml.Marshal(&root)
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}
