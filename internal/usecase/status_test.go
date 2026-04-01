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

func TestStatusTargetsReportsMountedAbsentMissingSourceAndShadowed(t *testing.T) {
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

	writeConfigForTest(t, filepath.Join(tempHome, ".veil", "config.toml"), "version = 1\nstore_path = "+workspaceRootQuoted(storeRoot)+"\ndefault_ttl = \"24h\"\n\n[workspaces.myapp]\nroot = "+workspaceRootQuoted(resolvedWorkspaceRoot)+"\ntargets = [\".env\", \"config/local.json\", \"config/missing.json\", \"token.txt\"]\n")

	mountedStorePath := filepath.Join(storeRoot, "workspaces", "myapp", ".env")
	absentStorePath := filepath.Join(storeRoot, "workspaces", "myapp", "config", "local.json")
	tokenStorePath := filepath.Join(storeRoot, "workspaces", "myapp", "token.txt")
	for _, path := range []string{mountedStorePath, absentStorePath, tokenStorePath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll() returned error: %v", err)
		}
		if err := os.WriteFile(path, []byte("secret\n"), 0o600); err != nil {
			t.Fatalf("WriteFile() returned error: %v", err)
		}
	}

	if err := os.MkdirAll(filepath.Join(workspaceRoot, "config"), 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	if err := os.Symlink(mountedStorePath, filepath.Join(workspaceRoot, ".env")); err != nil {
		t.Fatalf("Symlink() returned error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceRoot, "token.txt"), []byte("shadowed\n"), 0o600); err != nil {
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
	uc := StatusTargets{
		FileSystem: infra.OSFileSystem{},
		Stdout:     &stdout,
	}

	if err := uc.Run(); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	for _, want := range []string{
		"mounted target: .env",
		"absent target: config/local.json",
		"missing-source target: config/missing.json",
		"shadowed target: token.txt",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, want substring %q", stdout.String(), want)
		}
	}
}

func TestStatusTargetsTreatsForeignAndBrokenSymlinksAsShadowed(t *testing.T) {
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

	writeConfigForTest(t, filepath.Join(tempHome, ".veil", "config.toml"), "version = 1\nstore_path = "+workspaceRootQuoted(storeRoot)+"\ndefault_ttl = \"24h\"\n\n[workspaces.myapp]\nroot = "+workspaceRootQuoted(resolvedWorkspaceRoot)+"\ntargets = [\"foreign.txt\", \"broken.txt\"]\n")

	foreignStorePath := filepath.Join(storeRoot, "workspaces", "myapp", "foreign.txt")
	brokenStorePath := filepath.Join(storeRoot, "workspaces", "myapp", "broken.txt")
	if err := os.MkdirAll(filepath.Dir(foreignStorePath), 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	for _, path := range []string{foreignStorePath, brokenStorePath} {
		if err := os.WriteFile(path, []byte("secret\n"), 0o600); err != nil {
			t.Fatalf("WriteFile() returned error: %v", err)
		}
	}

	foreignTarget := filepath.Join(tempHome, "elsewhere.txt")
	if err := os.WriteFile(foreignTarget, []byte("elsewhere\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}
	if err := os.Symlink(foreignTarget, filepath.Join(workspaceRoot, "foreign.txt")); err != nil {
		t.Fatalf("Symlink() returned error: %v", err)
	}
	if err := os.Symlink(filepath.Join(tempHome, "missing-target"), filepath.Join(workspaceRoot, "broken.txt")); err != nil {
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
	uc := StatusTargets{
		FileSystem: infra.OSFileSystem{},
		Stdout:     &stdout,
	}

	if err := uc.Run(); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	for _, want := range []string{
		"shadowed target: foreign.txt",
		"shadowed target: broken.txt",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, want substring %q", stdout.String(), want)
		}
	}
}

func TestStatusTargetsReportsExpiredWhenLeaseHasElapsed(t *testing.T) {
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
	if err := os.Symlink(storeTargetPath, filepath.Join(workspaceRoot, ".env")); err != nil {
		t.Fatalf("Symlink() returned error: %v", err)
	}

	state := domain.DefaultState()
	now := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	mustUpsertLease(t, &state, "myapp", ".env", now.Add(-2*time.Hour), now.Add(-time.Hour))
	writeStateForTest(t, filepath.Join(tempHome, ".veil", "state.toml"), state)

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
	uc := StatusTargets{
		FileSystem: infra.OSFileSystem{},
		Stdout:     &stdout,
		Now:        func() time.Time { return now },
	}

	if err := uc.Run(); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	if !strings.Contains(stdout.String(), "expired target: .env") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}
