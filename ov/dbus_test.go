package main

import "testing"

func TestDbusNotifyLocalNoBus(t *testing.T) {
	// With no session bus, should return error (not panic)
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/dev/null/invalid")
	err := dbusNotifyLocal("test title", "test body")
	if err == nil {
		t.Error("expected error with invalid bus address")
	}
}

func TestParseDbusArgs(t *testing.T) {
	tests := []struct {
		input   string
		wantErr bool
	}{
		{"string:hello", false},
		{"uint32:42", false},
		{"int32:-1", false},
		{"boolean:true", false},
		{"bool:false", false},
		{"int64:9999999999", false},
		{"uint64:0", false},
		{"double:3.14", false},
		{"invalid", true},           // no colon
		{"unknown:value", true},     // unsupported type
		{"uint32:notanumber", true}, // bad value
		{"int32:abc", true},         // bad value
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			_, err := parseDbusArgs([]string{tt.input})
			if tt.wantErr && err == nil {
				t.Errorf("expected error for %q", tt.input)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error for %q: %v", tt.input, err)
			}
		})
	}
}

func TestParseDbusArgsMultiple(t *testing.T) {
	args := []string{"string:ov", "uint32:0", "string:", "string:title", "string:body", "int32:-1"}
	result, err := parseDbusArgs(args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 6 {
		t.Fatalf("expected 6 results, got %d", len(result))
	}
	if s, ok := result[0].(string); !ok || s != "ov" {
		t.Errorf("result[0] = %v, want string(ov)", result[0])
	}
	if n, ok := result[1].(uint32); !ok || n != 0 {
		t.Errorf("result[1] = %v, want uint32(0)", result[1])
	}
	if n, ok := result[5].(int32); !ok || n != -1 {
		t.Errorf("result[5] = %v, want int32(-1)", result[5])
	}
}

func TestParseDbusArgsEmpty(t *testing.T) {
	result, err := parseDbusArgs(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 0 {
		t.Fatalf("expected 0 results, got %d", len(result))
	}
}

func TestDbusNotifyRemoteNonexistent(t *testing.T) {
	// Should not panic with bogus engine/container
	sendContainerNotification("nonexistent-engine", "nonexistent-container", "test", "body")
}
