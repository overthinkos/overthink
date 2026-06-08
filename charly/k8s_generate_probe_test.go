package main

import (
	"reflect"
	"testing"
)

// TestCheckToProbe_HTTP covers the http: → httpGet shape.
func TestCheckToProbe_HTTP(t *testing.T) {
	tests := []struct {
		name string
		http string
		want map[string]any
	}{
		{
			name: "http with explicit port + path",
			http: "http://example.com:8080/healthz",
			want: map[string]any{"httpGet": map[string]any{"path": "/healthz", "port": 8080, "host": "example.com"}},
		},
		{
			name: "https default port",
			http: "https://api.example.com/ready",
			want: map[string]any{"httpGet": map[string]any{"path": "/ready", "port": 443, "host": "api.example.com"}},
		},
		{
			name: "localhost host elided",
			http: "http://localhost:9090/metrics",
			want: map[string]any{"httpGet": map[string]any{"path": "/metrics", "port": 9090}},
		},
		{
			name: "127.0.0.1 host elided",
			http: "http://127.0.0.1:80/",
			want: map[string]any{"httpGet": map[string]any{"path": "/", "port": 80}},
		},
		{
			name: "no path defaults to /",
			http: "http://example.com:8080",
			want: map[string]any{"httpGet": map[string]any{"path": "/", "port": 8080, "host": "example.com"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checkToProbe(&Check{HTTP: tt.http})
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// TestCheckToProbe_Addr covers the addr: → tcpSocket shape.
func TestCheckToProbe_Addr(t *testing.T) {
	tests := []struct {
		name, addr string
		want       map[string]any
	}{
		{
			name: "host:port",
			addr: "example.com:5432",
			want: map[string]any{"tcpSocket": map[string]any{"port": 5432, "host": "example.com"}},
		},
		{
			name: "127.0.0.1 elided",
			addr: "127.0.0.1:6379",
			want: map[string]any{"tcpSocket": map[string]any{"port": 6379}},
		},
		{
			name: "bare port",
			addr: "8080",
			want: map[string]any{"tcpSocket": map[string]any{"port": 8080}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checkToProbe(&Check{Addr: tt.addr})
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// TestCheckToProbe_File covers file: → exec test -e.
func TestCheckToProbe_File(t *testing.T) {
	got := checkToProbe(&Check{File: "/etc/ready"})
	want := map[string]any{"exec": map[string]any{"command": []string{"test", "-e", "/etc/ready"}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestCheckToProbe_Command covers command: → exec sh -c.
func TestCheckToProbe_Command(t *testing.T) {
	got := checkToProbe(&Check{Command: "redis-cli ping | grep PONG"})
	want := map[string]any{"exec": map[string]any{"command": []string{"sh", "-c", "redis-cli ping | grep PONG"}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestCheckToProbe_NilAndEmpty covers the no-op paths.
func TestCheckToProbe_NilAndEmpty(t *testing.T) {
	if got := checkToProbe(nil); got != nil {
		t.Errorf("nil check: got %v, want nil", got)
	}
	if got := checkToProbe(&Check{}); got != nil {
		t.Errorf("empty check: got %v, want nil", got)
	}
}

// TestCheckToProbe_HTTPPriority documents the priority order: HTTP wins
// over Addr/File/Command when multiple are set (no real check carries
// more than one verb after validation, but the function is robust).
func TestCheckToProbe_HTTPPriority(t *testing.T) {
	got := checkToProbe(&Check{HTTP: "http://example.com:80/health", File: "/etc/ready"})
	if _, ok := got["httpGet"]; !ok {
		t.Errorf("expected httpGet to win over file, got %v", got)
	}
}
