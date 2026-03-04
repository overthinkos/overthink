package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseEnvFile(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	content := `# This is a comment
FOO=bar
BAZ="quoted value"
SINGLE='single quoted'
EMPTY=
NOVALUE

# Another comment
MULTI=hello world
`
	os.WriteFile(envPath, []byte(content), 0644)

	got, err := ParseEnvFile(envPath)
	if err != nil {
		t.Fatalf("ParseEnvFile() error: %v", err)
	}

	want := []string{
		"FOO=bar",
		"BAZ=quoted value",
		"SINGLE=single quoted",
		"EMPTY=",
		"NOVALUE",
		"MULTI=hello world",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseEnvFile() =\n  %v\nwant\n  %v", got, want)
	}
}

func TestParseEnvFileNotFound(t *testing.T) {
	_, err := ParseEnvFile("/nonexistent/.env")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestLoadWorkspaceEnv(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	os.WriteFile(envPath, []byte("KEY=value\n"), 0644)

	got, err := LoadWorkspaceEnv(dir)
	if err != nil {
		t.Fatalf("LoadWorkspaceEnv() error: %v", err)
	}

	want := []string{"KEY=value"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("LoadWorkspaceEnv() = %v, want %v", got, want)
	}
}

func TestLoadWorkspaceEnvNoFile(t *testing.T) {
	dir := t.TempDir()
	got, err := LoadWorkspaceEnv(dir)
	if err != nil {
		t.Fatalf("LoadWorkspaceEnv() error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil when no .env file, got %v", got)
	}
}

func TestDeduplicateEnv(t *testing.T) {
	input := []string{"FOO=1", "BAR=2", "FOO=3"}
	got := deduplicateEnv(input)
	want := []string{"FOO=3", "BAR=2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("deduplicateEnv() = %v, want %v", got, want)
	}
}

func TestResolveEnvVars(t *testing.T) {
	dir := t.TempDir()

	// Create workspace .env
	os.WriteFile(filepath.Join(dir, ".env"), []byte("WS=workspace\nSHARED=ws\n"), 0644)

	// Create CLI env file
	cliEnvPath := filepath.Join(dir, "cli.env")
	os.WriteFile(cliEnvPath, []byte("CLI_FILE=yes\nSHARED=cli-file\n"), 0644)

	got, err := ResolveEnvVars(
		[]string{"DEPLOY=yes", "SHARED=deploy"},
		"",
		dir,
		cliEnvPath,
		[]string{"CLI=flag", "SHARED=cli-flag"},
	)
	if err != nil {
		t.Fatalf("ResolveEnvVars() error: %v", err)
	}

	want := []string{
		"DEPLOY=yes",
		"SHARED=cli-flag", // CLI flag wins
		"WS=workspace",
		"CLI_FILE=yes",
		"CLI=flag",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ResolveEnvVars() =\n  %v\nwant\n  %v", got, want)
	}
}
