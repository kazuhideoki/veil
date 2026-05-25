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

func TestWriteErrorPrintsErrorInRed(t *testing.T) {
	var stderr bytes.Buffer

	writeError(&stderr, fmt.Errorf("unsupported arguments: [unsupported]"))

	const want = "\x1b[31munsupported arguments: [unsupported]\x1b[0m\n"
	if stderr.String() != want {
		t.Fatalf("stderr = %q, want %q", stderr.String(), want)
	}
}

func TestRunInitCreatesOnePasswordConfig(t *testing.T) {
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
	if err := run([]string{"init"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("run(init) returned error: %v", err)
	}

	configData, err := os.ReadFile(filepath.Join(tempHome, ".veil", "config.toml"))
	if err != nil {
		t.Fatalf("ReadFile(config) returned error: %v", err)
	}
	configText := string(configData)
	for _, want := range []string{
		`backend = "1password_document"`,
		`vault = "Personal"`,
		`[workspaces."workspace-root"]`,
	} {
		if !strings.Contains(configText, want) {
			t.Fatalf("config = %q, want substring %q", configText, want)
		}
	}
}

func TestWithStateLockCreatesLockFile(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	called := false

	if err := withStateLock(func() error {
		called = true
		if _, err := os.Stat(filepath.Join(homeDir, ".veil", "state.lock")); err != nil {
			t.Fatalf("state lock was not created: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatalf("withStateLock() returned error: %v", err)
	}
	if !called {
		t.Fatal("lock callback was not called")
	}
}
