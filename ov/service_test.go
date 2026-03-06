package main

import (
	"testing"
)

func TestCheckSupervisordService(t *testing.T) {
	available := []string{"traefik", "testapi"}

	// Valid services
	if err := checkSupervisordService(available, "traefik"); err != nil {
		t.Errorf("checkSupervisordService(traefik) = %v, want nil", err)
	}
	if err := checkSupervisordService(available, "testapi"); err != nil {
		t.Errorf("checkSupervisordService(testapi) = %v, want nil", err)
	}

	// Invalid service
	err := checkSupervisordService(available, "nonexistent")
	if err == nil {
		t.Error("checkSupervisordService(nonexistent) = nil, want error")
	}
}

func TestCheckSupervisordServiceEmpty(t *testing.T) {
	err := checkSupervisordService(nil, "svc")
	if err == nil {
		t.Error("checkSupervisordService with nil available = nil, want error")
	}
}
