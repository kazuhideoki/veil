package usecase

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kazuhideoki/veil/internal/domain"
	"github.com/kazuhideoki/veil/internal/infra"
)

func TestRunTTLCleanerRemovesExpiredManagedSymlinkAndLease(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	storeRoot := filepath.Join(tempHome, "veil-store")
	workspaceRoot := filepath.Join(tempHome, "myapp")
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}

	resolvedWorkspaceRoot, err := filepath.EvalSymlinks(workspaceRoot)
	if err != nil {
		t.Fatalf("EvalSymlinks() returned error: %v", err)
	}

	writeConfigForTest(t, filepath.Join(tempHome, ".veil", "config.toml"), "version = 1\nstore_path = "+workspaceRootQuoted(storeRoot)+"\ndefault_ttl = \"24h\"\n\n[workspaces.myapp]\nroot = "+workspaceRootQuoted(resolvedWorkspaceRoot)+"\ntargets = [\".env\"]\n")

	storeTargetPath := filepath.Join(storeRoot, "workspaces", "myapp", ".env")
	if err := os.MkdirAll(filepath.Dir(storeTargetPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	if err := os.WriteFile(storeTargetPath, []byte("TOKEN=secret\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}
	workspaceTargetPath := filepath.Join(workspaceRoot, ".env")
	if err := os.Symlink(storeTargetPath, workspaceTargetPath); err != nil {
		t.Fatalf("Symlink() returned error: %v", err)
	}

	state := domain.DefaultState()
	now := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	mustUpsertLease(t, &state, "myapp", ".env", now.Add(-2*time.Hour), now.Add(-time.Hour))
	writeStateForTest(t, filepath.Join(tempHome, ".veil", "state.toml"), state)

	var stdout bytes.Buffer
	uc := RunTTLCleaner{
		FileSystem: infra.OSFileSystem{},
		Lock:       stubTTLCleanerLock{acquired: true},
		Stdout:     &stdout,
		Now:        func() time.Time { return now },
		Sleep: func(duration time.Duration) {
			t.Fatalf("Sleep() should not be called for expired lease: %v", duration)
		},
	}

	if err := uc.Run(); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	if _, err := os.Lstat(workspaceTargetPath); !os.IsNotExist(err) {
		t.Fatalf("workspace target still exists, err = %v", err)
	}

	refreshed := readStateForTest(t, filepath.Join(tempHome, ".veil", "state.toml"))
	if got := len(refreshed.Leases); got != 0 {
		t.Fatalf("lease count = %d, want 0", got)
	}

	if !strings.Contains(stdout.String(), "expired vanished target: myapp/.env") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunTTLCleanerExitsWhenAnotherCleanerOwnsTheLock(t *testing.T) {
	uc := RunTTLCleaner{
		FileSystem: infra.OSFileSystem{},
		Lock:       stubTTLCleanerLock{acquired: false},
		Stdout:     &bytes.Buffer{},
	}

	if err := uc.Run(); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}
}
