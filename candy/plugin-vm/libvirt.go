package main

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	libvirt "github.com/digitalocean/go-libvirt"
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

// InjectLibvirtXML modifies a libvirt domain's XML to include the given snippets.
// Device elements are inserted into <devices>, others replace/insert at <domain> level.
func InjectLibvirtXML(vmName string, snippets []string) error {
	if len(snippets) == 0 {
		return nil
	}

	conn, err := connectLibvirt("")
	if err != nil {
		return fmt.Errorf("connecting to libvirt: %w", err)
	}
	defer conn.Close() //nolint:errcheck

	dom, err := conn.lookupDomain(vmName)
	if err != nil {
		return fmt.Errorf("looking up VM %s: %w", vmName, err)
	}

	// 1. Get current XML
	domainXML, err := conn.getDomainXML(dom)
	if err != nil {
		return fmt.Errorf("getting XML for %s: %w", vmName, err)
	}

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

	// 3. Check if VM is running and force-stop it for redefinition
	wasRunning := false
	state, _ := conn.domainState(dom)
	if state == libvirt.DomainRunning {
		wasRunning = true
		fmt.Fprintf(os.Stderr, "Stopping VM %s to apply libvirt config...\n", vmName)
		if err := conn.destroyDomain(dom); err != nil {
			return fmt.Errorf("stopping VM %s: %w", vmName, err)
		}
		// StopGate (poll.go): wait for the domain to reach shutoff before
		// redefining — was a brittle fixed 10×1s loop with NO real deadline that
		// silently proceeded while still running. On stall/cap, WARN (the destroy
		// above was already issued) rather than silently redefine a live domain.
		cfg := loadedReadiness().StopGate("shutoff " + vmName)
		if perr := pollUntil(context.Background(), cfg, func(context.Context) (bool, float64, error) {
			d, lerr := conn.lookupDomain(vmName)
			if lerr != nil {
				return true, 0, nil // domain gone — effectively shut off
			}
			s, _ := conn.domainState(d)
			return s == libvirt.DomainShutoff, 0, nil
		}); perr != nil {
			fmt.Fprintf(os.Stderr, "warning: VM %s did not reach shutoff within the stop grace before redefine: %v\n", vmName, perr)
		}
	}

	// 4. Redefine the domain with modified XML
	if err := conn.redefineDomain(domainXML); err != nil {
		return fmt.Errorf("redefining VM %s: %w", vmName, err)
	}

	// 5. Restart VM if it was running before
	if wasRunning {
		dom, err = conn.lookupDomain(vmName)
		if err != nil {
			return fmt.Errorf("looking up VM %s after redefine: %w", vmName, err)
		}
		if err := conn.startDomain(dom); err != nil {
			return fmt.Errorf("restarting VM %s after config injection: %w", vmName, err)
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
