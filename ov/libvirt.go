package main

import (
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// libvirtDeviceElements lists element names that belong inside <devices> in libvirt domain XML.
var libvirtDeviceElements = map[string]bool{
	"channel":     true,
	"disk":        true,
	"controller":  true,
	"filesystem":  true,
	"hostdev":     true,
	"interface":   true,
	"serial":      true,
	"console":     true,
	"input":       true,
	"graphics":    true,
	"video":       true,
	"sound":       true,
	"audio":       true,
	"watchdog":    true,
	"memballoon":  true,
	"rng":         true,
	"tpm":         true,
	"redirdev":    true,
	"smartcard":   true,
	"hub":         true,
	"panic":       true,
	"shmem":       true,
	"memory":      true,
	"iommu":       true,
	"vsock":       true,
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

// CollectLibvirtSnippets gathers libvirt XML snippets from all layers in an image
// plus image-level snippets, deduplicating by exact string match.
func CollectLibvirtSnippets(cfg *Config, layers map[string]*Layer, imageName string) []string {
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

	// Collect from image's layers
	img, ok := cfg.Images[imageName]
	if !ok {
		return nil
	}
	for _, layerRef := range img.Layers {
		layerName := BareRef(layerRef)
		layer, ok := layers[layerName]
		if !ok || !layer.HasLibvirt {
			continue
		}
		for _, s := range layer.Libvirt() {
			addSnippet(s)
		}
	}

	// Collect from image-level config
	for _, s := range img.Libvirt {
		addSnippet(s)
	}

	return snippets
}

// InjectLibvirtXML modifies a libvirt domain's XML to include the given snippets.
// Device elements are inserted into <devices>, others replace/insert at <domain> level.
func InjectLibvirtXML(vmName string, snippets []string) error {
	if len(snippets) == 0 {
		return nil
	}

	// 1. Dump current XML
	cmd := virshCmd("dumpxml", vmName)
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("virsh dumpxml %s failed: %w", vmName, err)
	}
	domainXML := string(out)

	// 2. Classify and inject snippets
	var deviceSnippets, domainSnippets []string
	for _, s := range snippets {
		if isDeviceElement(s) {
			deviceSnippets = append(deviceSnippets, s)
		} else {
			domainSnippets = append(domainSnippets, s)
		}
	}

	// Inject device snippets before </devices>
	if len(deviceSnippets) > 0 {
		insertion := "\n    " + strings.Join(deviceSnippets, "\n    ") + "\n  "
		domainXML = strings.Replace(domainXML, "</devices>", insertion+"</devices>", 1)
	}

	// Inject domain-level snippets before </domain>
	if len(domainSnippets) > 0 {
		insertion := "\n  " + strings.Join(domainSnippets, "\n  ") + "\n"
		domainXML = strings.Replace(domainXML, "</domain>", insertion+"</domain>", 1)
	}

	// 3. Write modified XML to temp file
	tmpFile, err := os.CreateTemp("", "ov-libvirt-*.xml")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(domainXML); err != nil {
		tmpFile.Close()
		return fmt.Errorf("writing temp XML: %w", err)
	}
	tmpFile.Close()

	// 4. Check if VM is running and force-stop it for redefinition
	wasRunning := false
	stateCmd := virshCmd("domstate", vmName)
	stateOut, err := stateCmd.Output()
	if err == nil && strings.TrimSpace(string(stateOut)) == "running" {
		wasRunning = true
		fmt.Fprintf(os.Stderr, "Stopping VM %s to apply libvirt config...\n", vmName)
		// Use destroy (force stop) since the VM may still be booting
		// and won't respond to graceful shutdown
		destroyCmd := virshCmd("destroy", vmName)
		if err := destroyCmd.Run(); err != nil {
			return fmt.Errorf("virsh destroy %s failed: %w", vmName, err)
		}
		// Wait for shutoff (up to 10 seconds)
		for i := 0; i < 10; i++ {
			checkCmd := virshCmd("domstate", vmName)
			checkOut, err := checkCmd.Output()
			if err != nil || strings.TrimSpace(string(checkOut)) == "shut off" {
				break
			}
			time.Sleep(time.Second)
		}
	}

	// 5. Redefine the domain with modified XML
	defineCmd := virshCmd("define", tmpFile.Name())
	defineCmd.Stderr = os.Stderr
	if err := defineCmd.Run(); err != nil {
		return fmt.Errorf("virsh define failed: %w", err)
	}

	// 6. Restart VM if it was running before
	if wasRunning {
		startCmd := virshCmd("start", vmName)
		startCmd.Stderr = os.Stderr
		if err := startCmd.Run(); err != nil {
			return fmt.Errorf("virsh start failed after libvirt config injection: %w", err)
		}
	}

	fmt.Fprintf(os.Stderr, "Injected %d libvirt config snippet(s) into VM %s\n", len(snippets), vmName)
	return nil
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
			if err == io.EOF {
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

