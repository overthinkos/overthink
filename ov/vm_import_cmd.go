package main

import (
	"fmt"
	"os"
	"text/tabwriter"
)

// vm_import_cmd.go — Kong subcommand wiring for `ov vm import`.

// VmImportCmd implements `ov vm import {<domain>, --all, --list}`.
type VmImportCmd struct {
	// Domain is the libvirt domain name to import. Empty when --all
	// or --list is set.
	Domain string `arg:"" optional:"" help:"Libvirt domain name to adopt (e.g. as listed by 'virsh -c qemu:///session list --all'). Omit when using --all or --list."`

	// TargetName overrides the vm.yml entry key. Default: domain name
	// with any "ov-" prefix stripped.
	TargetName string `long:"target-name" help:"Override the kind:vm entry name in vm.yml"`

	// All adopts every libvirt domain not already in vm.yml.
	All bool `long:"all" help:"Adopt every unmanaged libvirt domain"`

	// List shows libvirt domains absent from vm.yml without writing
	// anything. Diagnostic mode.
	List bool `long:"list" help:"Show libvirt domains absent from vm.yml; do not write"`
}

// Run executes `ov vm import`.
func (c *VmImportCmd) Run() error {
	if c.List {
		domains, err := ListUnmanagedDomains()
		if err != nil {
			return err
		}
		if len(domains) == 0 {
			fmt.Println("no unmanaged libvirt domains")
			return nil
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "DOMAIN\tWOULD-IMPORT-AS")
		for _, d := range domains {
			fmt.Fprintf(tw, "%s\t%s\n", d, stripOvPrefix(d))
		}
		return tw.Flush()
	}

	if c.All {
		domains, err := ListUnmanagedDomains()
		if err != nil {
			return err
		}
		if len(domains) == 0 {
			fmt.Println("no unmanaged libvirt domains to import")
			return nil
		}
		var failed []string
		for _, d := range domains {
			if err := importOne(d, ""); err != nil {
				fmt.Fprintf(os.Stderr, "import %s: %v\n", d, err)
				failed = append(failed, d)
				continue
			}
			fmt.Printf("imported %s\n", d)
		}
		if len(failed) > 0 {
			return fmt.Errorf("import-all completed with %d failures: %v", len(failed), failed)
		}
		return nil
	}

	if c.Domain == "" {
		return fmt.Errorf("ov vm import: domain name is required (or pass --list / --all)")
	}
	if err := importOne(c.Domain, c.TargetName); err != nil {
		return err
	}
	fmt.Printf("imported %s\n", c.Domain)
	return nil
}

// importOne handles a single domain import end-to-end.
func importOne(domainName, targetName string) error {
	name, spec, err := ImportFromLibvirt(domainName, targetName)
	if err != nil {
		return err
	}
	if err := WriteVmImportDeclaration(name, spec); err != nil {
		return err
	}
	return nil
}

// stripOvPrefix removes a leading "ov-" if present (mirrors the
// default-target-name logic in ImportFromLibvirt).
func stripOvPrefix(s string) string {
	const p = "ov-"
	if len(s) > len(p) && s[:len(p)] == p {
		return s[len(p):]
	}
	return s
}
