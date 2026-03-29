package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunWithoutArgs(t *testing.T) {
	if err := run(nil, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("run(nil) returned error: %v", err)
	}
}

func TestRunWithUnsupportedArgsReturnsError(t *testing.T) {
	err := run([]string{"unsupported"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("run(args) returned nil error")
	}
}

func TestRunInitCreatesConfig(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	var stdout bytes.Buffer
	err := run([]string{"init"}, &stdout, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("run(init) returned error: %v", err)
	}

	if !strings.Contains(stdout.String(), "initialized config") {
		t.Fatalf("stdout = %q, want init logs", stdout.String())
	}
}

func TestRunInitRejectsExtraArgs(t *testing.T) {
	err := run([]string{"init", "unexpected"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("run(init, extra args) returned nil error")
	}

	if !strings.Contains(err.Error(), "init does not accept positional arguments") {
		t.Fatalf("error = %q", err)
	}
}

func TestRunInitWithWorkspaceIDFlag(t *testing.T) {
	tempHome := t.TempDir()
	tempWorkspace := filepath.Join(tempHome, "workspace-root")
	if err := os.MkdirAll(tempWorkspace, 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}

	t.Setenv("HOME", tempHome)

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

	var stdout bytes.Buffer
	err = run([]string{"init", "--workspace-id", "myapp"}, &stdout, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("run(init --workspace-id) returned error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tempHome, ".veil", "config.toml"))
	if err != nil {
		t.Fatalf("ReadFile() returned error: %v", err)
	}

	if !strings.Contains(string(data), "[workspaces.\"myapp\"]") {
		t.Fatalf("config contents = %q", string(data))
	}
}

func TestRunAddRequiresExactlyOneTargetPath(t *testing.T) {
	err := run([]string{"add"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("run(add) returned nil error")
	}

	if !strings.Contains(err.Error(), "add requires exactly one target path") {
		t.Fatalf("error = %q", err)
	}
}

func TestRunEmergeUsesRegisteredWorkspace(t *testing.T) {
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
	if err := run([]string{"emerge"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("run(emerge) returned error: %v", err)
	}

	linkPath := filepath.Join(workspaceRoot, ".env")
	if got, err := os.Readlink(linkPath); err != nil {
		t.Fatalf("Readlink() returned error: %v", err)
	} else if got != storeTargetPath {
		t.Fatalf("link target = %q, want %q", got, storeTargetPath)
	}

	if !strings.Contains(stdout.String(), "emerged target: .env") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}
