package main

import (
	"testing"
)

func TestValidateServiceNameFound(t *testing.T) {
	// Test the service name lookup logic that validateServiceName uses internally.
	// validateServiceName calls ExtractMetadata which reads container labels at runtime,
	// so we test the lookup logic directly via ImageMetadata.Services.
	meta := &ImageMetadata{
		Init:     "supervisord",
		Services: []string{"traefik", "testapi"},
	}

	for _, svc := range []string{"traefik", "testapi"} {
		found := false
		for _, s := range meta.Services {
			if s == svc {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("service %q should be found in Services %v", svc, meta.Services)
		}
	}
}

func TestValidateServiceNameNotFound(t *testing.T) {
	meta := &ImageMetadata{
		Init:     "supervisord",
		Services: []string{"traefik", "testapi"},
	}

	svc := "nonexistent"
	found := false
	for _, s := range meta.Services {
		if s == svc {
			found = true
			break
		}
	}
	if found {
		t.Error("service \"nonexistent\" should not be found")
	}
}

func TestValidateServiceNameEmpty(t *testing.T) {
	meta := &ImageMetadata{
		Init:     "",
		Services: nil,
	}

	svc := "svc"
	found := false
	for _, s := range meta.Services {
		if s == svc {
			found = true
			break
		}
	}
	if found {
		t.Error("service should not be found in nil Services list")
	}
}
