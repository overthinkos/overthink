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

func TestParseEnvBytes(t *testing.T) {
	content := []byte(`# This is a comment
FOO=bar
BAZ="quoted value"
SINGLE='single quoted'
EMPTY=
NOVALUE

# Another comment
MULTI=hello world
`)

	got, err := ParseEnvBytes(content)
	if err != nil {
		t.Fatalf("ParseEnvBytes() error: %v", err)
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
		t.Errorf("ParseEnvBytes() =\n  %v\nwant\n  %v", got, want)
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

func TestLoadProcessDotenv(t *testing.T) {
	resetDotenvLoaded()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".env"), []byte("TEST_DOTENV_VAR=from_dotenv\nTEST_DOTENV_EMPTY=\n"), 0644)

	t.Cleanup(func() {
		os.Unsetenv("TEST_DOTENV_VAR")
		os.Unsetenv("TEST_DOTENV_EMPTY")
		resetDotenvLoaded()
	})

	err := LoadProcessDotenv(dir)
	if err != nil {
		t.Fatalf("LoadProcessDotenv() error: %v", err)
	}

	if got := os.Getenv("TEST_DOTENV_VAR"); got != "from_dotenv" {
		t.Errorf("TEST_DOTENV_VAR = %q, want %q", got, "from_dotenv")
	}
	if !DotenvLoaded("TEST_DOTENV_VAR") {
		t.Error("TEST_DOTENV_VAR should be marked as dotenv-loaded")
	}
	if got := os.Getenv("TEST_DOTENV_EMPTY"); got != "" {
		t.Errorf("TEST_DOTENV_EMPTY = %q, want empty", got)
	}
	if !DotenvLoaded("TEST_DOTENV_EMPTY") {
		t.Error("TEST_DOTENV_EMPTY should be marked as dotenv-loaded")
	}
}

func TestLoadProcessDotenvRealEnvWins(t *testing.T) {
	resetDotenvLoaded()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".env"), []byte("TEST_EXISTING=from_dotenv\n"), 0644)

	os.Setenv("TEST_EXISTING", "from_real_env")
	t.Cleanup(func() {
		os.Unsetenv("TEST_EXISTING")
		resetDotenvLoaded()
	})

	err := LoadProcessDotenv(dir)
	if err != nil {
		t.Fatalf("LoadProcessDotenv() error: %v", err)
	}

	if got := os.Getenv("TEST_EXISTING"); got != "from_real_env" {
		t.Errorf("TEST_EXISTING = %q, want %q (real env should win)", got, "from_real_env")
	}
	if DotenvLoaded("TEST_EXISTING") {
		t.Error("TEST_EXISTING should NOT be marked as dotenv-loaded (was already set)")
	}
}

func TestLoadProcessDotenvNoFile(t *testing.T) {
	resetDotenvLoaded()
	dir := t.TempDir()
	err := LoadProcessDotenv(dir)
	if err != nil {
		t.Fatalf("LoadProcessDotenv() should not error when .env missing, got: %v", err)
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
		nil, // no global env
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

func TestResolveEnvVarsWithGlobalEnv(t *testing.T) {
	got, err := ResolveEnvVars(
		[]string{"GLOBAL=yes", "SHARED=global"},   // global env (lowest priority)
		[]string{"DEPLOY=yes", "SHARED=deploy"},     // per-image deploy env
		"",
		"",
		"",
		[]string{"CLI=flag"},
	)
	if err != nil {
		t.Fatalf("ResolveEnvVars() error: %v", err)
	}

	want := []string{
		"GLOBAL=yes",
		"SHARED=deploy", // per-image deploy overrides global
		"DEPLOY=yes",
		"CLI=flag",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ResolveEnvVars() =\n  %v\nwant\n  %v", got, want)
	}
}
