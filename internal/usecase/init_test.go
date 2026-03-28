package usecase

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kazuhideoki/veil/internal/infra"
)

func TestInitConfigCreatesConfigFile(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

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

	if string(data) != "version = 1\nstore_path = \"~/Library/Mobile Documents/com~apple~CloudDocs/VeilStore\"\ndefault_ttl = \"24h\"\n" {
		t.Fatalf("config contents = %q", string(data))
	}

	for _, want := range []string{"creating config directory", "writing config", "initialized config"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, want substring %q", stdout.String(), want)
		}
	}
}

func TestInitConfigDoesNotOverwriteExistingConfig(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	configDir := filepath.Join(tempHome, ".veil")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}

	configPath := filepath.Join(configDir, "config.toml")
	const existing = "version = 99\n"
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

	if string(data) != existing {
		t.Fatalf("config contents = %q, want %q", string(data), existing)
	}

	if !strings.Contains(stdout.String(), "config already exists") {
		t.Fatalf("stdout = %q, want existing-config message", stdout.String())
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
