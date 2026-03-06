package main

import "testing"

func TestResolveNetworkDefault(t *testing.T) {
	orig := EnsureOvNetwork
	defer func() { EnsureOvNetwork = orig }()
	EnsureOvNetwork = func(engine string) error { return nil }

	got, err := ResolveNetwork("", "podman")
	if err != nil {
		t.Fatalf("ResolveNetwork error: %v", err)
	}
	if got != OvNetworkName {
		t.Errorf("ResolveNetwork(\"\", \"podman\") = %q, want %q", got, OvNetworkName)
	}
}

func TestResolveNetworkExplicitHost(t *testing.T) {
	orig := EnsureOvNetwork
	defer func() { EnsureOvNetwork = orig }()
	EnsureOvNetwork = func(engine string) error {
		t.Error("EnsureOvNetwork should not be called for explicit network")
		return nil
	}

	got, err := ResolveNetwork("host", "podman")
	if err != nil {
		t.Fatalf("ResolveNetwork error: %v", err)
	}
	if got != "host" {
		t.Errorf("ResolveNetwork(\"host\", \"podman\") = %q, want \"host\"", got)
	}
}

func TestResolveNetworkExplicitNone(t *testing.T) {
	got, err := ResolveNetwork("none", "docker")
	if err != nil {
		t.Fatalf("ResolveNetwork error: %v", err)
	}
	if got != "none" {
		t.Errorf("ResolveNetwork(\"none\", \"docker\") = %q, want \"none\"", got)
	}
}
