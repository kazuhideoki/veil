package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunRemoveRequiresExactlyOneTargetPath(t *testing.T) {
	err := run([]string{"remove"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("run(remove) returned nil error")
	}

	if !strings.Contains(err.Error(), "remove requires exactly one target path") {
		t.Fatalf("error = %q", err)
	}
}

func TestRunPurgeRequiresExactlyOneTargetPath(t *testing.T) {
	err := run([]string{"purge"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("run(purge) returned nil error")
	}

	if !strings.Contains(err.Error(), "purge requires exactly one target path") {
		t.Fatalf("error = %q", err)
	}
}

func TestRunRemoveRestoresRegisteredTarget(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	storeRoot := filepath.Join(tempHome, "veil-store")
	workspaceRoot := filepath.Join(tempHome, "workspace-root")
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}

	resolvedWorkspaceRoot, err := filepath.EvalSymlinks(workspaceRoot)
	if err != nil {
		t.Fatalf("EvalSymlinks() returned error: %v", err)
	}

	configPath := filepath.Join(tempHome, ".veil", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}

	const configTemplate = "version = 1\nstore_path = %q\ndefault_ttl = \"24h\"\n\n[workspaces.myapp]\nroot = %q\ntargets = [\".env\"]\n"
	if err := os.WriteFile(configPath, []byte(
		fmt.Sprintf(configTemplate, storeRoot, resolvedWorkspaceRoot),
	), 0o644); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}

	storeTargetPath := filepath.Join(storeRoot, "workspaces", "myapp", ".env")
	if err := os.MkdirAll(filepath.Dir(storeTargetPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	if err := os.WriteFile(storeTargetPath, []byte("TOKEN=test\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}
	if err := os.Symlink(storeTargetPath, filepath.Join(workspaceRoot, ".env")); err != nil {
		t.Fatalf("Symlink() returned error: %v", err)
	}

	previousWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() returned error: %v", err)
	}

	if err := os.Chdir(workspaceRoot); err != nil {
		t.Fatalf("Chdir() returned error: %v", err)
	}
	defer func() {
		if err := os.Chdir(previousWD); err != nil {
			t.Fatalf("restore Chdir() returned error: %v", err)
		}
	}()

	var stdout bytes.Buffer
	if err := run([]string{"remove", ".env"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("run(remove) returned error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(workspaceRoot, ".env"))
	if err != nil {
		t.Fatalf("ReadFile() returned error: %v", err)
	}
	if string(data) != "TOKEN=test\n" {
		t.Fatalf("workspace data = %q", string(data))
	}

	if !strings.Contains(stdout.String(), "removed target: .env") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}
