package usecase

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/kazuhideoki/veil/internal/infra"
)

func TestInitConfigCreatesConfigFile(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	tempWorkspace := filepath.Join(tempHome, "myapp")
	if err := os.MkdirAll(tempWorkspace, 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}

	previousWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() returned error: %v", err)
	}

	if err := os.Chdir(tempWorkspace); err != nil {
		t.Fatalf("Chdir() returned error: %v", err)
	}
	defer func() {
		if err := os.Chdir(previousWD); err != nil {
			t.Fatalf("restore Chdir() returned error: %v", err)
		}
	}()

	currentDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() returned error: %v", err)
	}

	var stdout bytes.Buffer
	uc := InitConfig{
		FileSystem: infra.OSFileSystem{},
		Stdout:     &stdout,
	}

	if err := uc.Run(); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	configPath := filepath.Join(tempHome, ".veil", "config.toml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(%q) returned error: %v", configPath, err)
	}

	if string(data) != "version = 1\nstore_path = \"~/Library/Mobile Documents/com~apple~CloudDocs/VeilStore\"\ndefault_ttl = \"24h\"\n\n[workspaces.myapp]\nroot = "+strconv.Quote(currentDir)+"\ntargets = []\n" {
		t.Fatalf("config contents = %q", string(data))
	}

	for _, want := range []string{"creating config directory", "writing config", "initialized config", "added workspace: myapp"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, want substring %q", stdout.String(), want)
		}
	}
}

func TestInitConfigAddsWorkspaceToExistingConfig(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	tempWorkspace := filepath.Join(tempHome, "another-app")
	if err := os.MkdirAll(tempWorkspace, 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}

	previousWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() returned error: %v", err)
	}

	if err := os.Chdir(tempWorkspace); err != nil {
		t.Fatalf("Chdir() returned error: %v", err)
	}
	defer func() {
		if err := os.Chdir(previousWD); err != nil {
			t.Fatalf("restore Chdir() returned error: %v", err)
		}
	}()

	configDir := filepath.Join(tempHome, ".veil")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}

	configPath := filepath.Join(configDir, "config.toml")
	const existing = "version = 1\nstore_path = \"~/Library/Mobile Documents/com~apple~CloudDocs/VeilStore\"\ndefault_ttl = \"24h\"\n\n[workspaces.myapp]\nroot = \"/tmp/myapp\"\ntargets = [\".env\"]\n"
	if err := os.WriteFile(configPath, []byte(existing), 0o644); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}

	var stdout bytes.Buffer
	uc := InitConfig{
		FileSystem: infra.OSFileSystem{},
		Stdout:     &stdout,
	}

	if err := uc.Run(); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile() returned error: %v", err)
	}

	got := string(data)
	if !strings.Contains(got, "[workspaces.myapp]") || !strings.Contains(got, "[workspaces.another-app]") {
		t.Fatalf("config contents = %q", got)
	}

	if strings.Contains(stdout.String(), "initialized config") {
		t.Fatalf("stdout = %q, do not want init log", stdout.String())
	}

	if !strings.Contains(stdout.String(), "added workspace: another-app") {
		t.Fatalf("stdout = %q, want workspace log", stdout.String())
	}
}

func TestInitConfigReturnsErrorWhenConfigPathIsDirectory(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	configPath := filepath.Join(tempHome, ".veil", "config.toml")
	if err := os.MkdirAll(configPath, 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}

	var stdout bytes.Buffer
	uc := InitConfig{
		FileSystem: infra.OSFileSystem{},
		Stdout:     &stdout,
	}

	err := uc.Run()
	if err == nil {
		t.Fatal("Run() returned nil error")
	}

	if !strings.Contains(err.Error(), "config path is a directory") {
		t.Fatalf("error = %q", err)
	}

	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want no log output", stdout.String())
	}
}

func TestInitConfigRejectsDuplicateWorkspaceID(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	tempWorkspace := filepath.Join(tempHome, "workspace-root")
	if err := os.MkdirAll(tempWorkspace, 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}

	previousWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() returned error: %v", err)
	}

	if err := os.Chdir(tempWorkspace); err != nil {
		t.Fatalf("Chdir() returned error: %v", err)
	}
	defer func() {
		if err := os.Chdir(previousWD); err != nil {
			t.Fatalf("restore Chdir() returned error: %v", err)
		}
	}()

	configDir := filepath.Join(tempHome, ".veil")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}

	configPath := filepath.Join(configDir, "config.toml")
	const existing = "version = 1\nstore_path = \"~/Library/Mobile Documents/com~apple~CloudDocs/VeilStore\"\ndefault_ttl = \"24h\"\n\n[workspaces.workspace-root]\nroot = \"/tmp/other-root\"\ntargets = []\n"
	if err := os.WriteFile(configPath, []byte(existing), 0o644); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}

	var stdout bytes.Buffer
	uc := InitConfig{
		FileSystem: infra.OSFileSystem{},
		Stdout:     &stdout,
	}

	err = uc.Run()
	if err == nil {
		t.Fatal("Run() returned nil error")
	}

	if !strings.Contains(err.Error(), "workspace already exists") {
		t.Fatalf("error = %q", err)
	}
}
