package main

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

// libvirtDeviceElements lists element names that belong inside <devices> in libvirt domain XML.
var libvirtDeviceElements = map[string]bool{
	"channel":    true,
	"disk":       true,
	"controller": true,
	"filesystem": true,
	"hostdev":    true,
	"interface":  true,
	"serial":     true,
	"console":    true,
	"input":      true,
	"graphics":   true,
	"video":      true,
	"sound":      true,
	"audio":      true,
	"watchdog":   true,
	"memballoon": true,
	"rng":        true,
	"tpm":        true,
	"redirdev":   true,
	"smartcard":  true,
	"hub":        true,
	"panic":      true,
	"shmem":      true,
	"memory":     true,
	"iommu":      true,
	"vsock":      true,
}

// isDeviceElement returns true if the XML snippet's root element belongs inside <devices>.
func isDeviceElement(snippet string) bool {
	decoder := xml.NewDecoder(strings.NewReader(snippet))
	for {
		tok, err := decoder.Token()
		if err != nil {
			return false
		}
		if se, ok := tok.(xml.StartElement); ok {
			return libvirtDeviceElements[se.Name.Local]
		}
	}
}

// CollectLibvirtSnippets gathers libvirt XML snippets from all candies in a box
// plus box-level snippets, deduplicating by exact string match.
func CollectLibvirtSnippets(cfg *Config, layers map[string]*Candy, boxName string) []string {
	seen := make(map[string]bool)
	var snippets []string

	addSnippet := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		snippets = append(snippets, s)
	}

	// Collect from the box's own candies (leaf-specific — VM snippets do NOT
	// inherit from a base box; the shared boxDirectCandies walk). Box-level
	// `libvirt:` was removed in the VM hard-cutover — raw XML snippets now live
	// on the paired `kind: vm` entity's `spec.libvirt.snippets:` in vm.yml.
	resolved, err := cfg.boxDirectCandies(layers, boxName)
	if err != nil {
		return nil
	}
	for _, candyName := range resolved {
		layer, ok := layers[candyName]
		if !ok || !layer.HasLibvirt() {
			continue
		}
		for _, s := range layer.Libvirt() {
			addSnippet(s)
		}
	}

	return snippets
}

// ValidateLibvirtSnippet checks that a string is valid XML with at least one element.
func ValidateLibvirtSnippet(snippet string) error {
	snippet = strings.TrimSpace(snippet)
	if snippet == "" {
		return fmt.Errorf("empty snippet")
	}
	decoder := xml.NewDecoder(strings.NewReader(snippet))
	foundElement := false
	for {
		tok, err := decoder.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				if !foundElement {
					return fmt.Errorf("snippet must contain an XML element")
				}
				return nil
			}
			return fmt.Errorf("invalid XML: %w", err)
		}
		if _, ok := tok.(xml.StartElement); ok {
			foundElement = true
		}
	}
}
