package main

import "testing"

func TestRecordSessionName(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"default", "record-default"},
		{"demo", "record-demo"},
		{"my-recording", "record-my-recording"},
		{"test_123", "record-test_123"},
	}
	for _, tt := range tests {
		got := recordSessionName(tt.name)
		if got != tt.want {
			t.Errorf("recordSessionName(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestRecordingFilePath(t *testing.T) {
	tests := []struct {
		name string
		mode string
		want string
	}{
		{"demo", "terminal", "/tmp/ov-recordings/demo.cast"},
		{"demo", "desktop", "/tmp/ov-recordings/demo.mp4"},
		{"walkthrough", "desktop", "/tmp/ov-recordings/walkthrough.mp4"},
		{"test-1", "terminal", "/tmp/ov-recordings/test-1.cast"},
	}
	for _, tt := range tests {
		got := recordingFilePath(tt.name, tt.mode)
		if got != tt.want {
			t.Errorf("recordingFilePath(%q, %q) = %q, want %q", tt.name, tt.mode, got, tt.want)
		}
	}
}

func TestFormatSize(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{0, "0 bytes"},
		{512, "512 bytes"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1572864, "1.5 MB"},
		{1073741824, "1.0 GB"},
	}
	for _, tt := range tests {
		got := formatSize(tt.bytes)
		if got != tt.want {
			t.Errorf("formatSize(%d) = %q, want %q", tt.bytes, got, tt.want)
		}
	}
}
