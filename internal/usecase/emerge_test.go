package usecase

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kazuhideoki/veil/internal/domain"
	"github.com/kazuhideoki/veil/internal/infra"
)

func TestEmergeTargetsCreatesWorkspaceSymlinks(t *testing.T) {
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

	writeConfigForTest(t, filepath.Join(tempHome, ".veil", "config.toml"), "version = 1\nstore_path = "+workspaceRootQuoted(storeRoot)+"\ndefault_ttl = \"24h\"\n\n[workspaces.myapp]\nroot = "+workspaceRootQuoted(resolvedWorkspaceRoot)+"\ntargets = [\".env\", \"config/service-account.json\"]\n")

	storeEnvPath := filepath.Join(storeRoot, "workspaces", "myapp", ".env")
	if err := os.MkdirAll(filepath.Dir(storeEnvPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	if err := os.WriteFile(storeEnvPath, []byte("TOKEN=test\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}

	storeConfigPath := filepath.Join(storeRoot, "workspaces", "myapp", "config", "service-account.json")
	if err := os.MkdirAll(filepath.Dir(storeConfigPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	if err := os.WriteFile(storeConfigPath, []byte("{\"key\":\"value\"}\n"), 0o600); err != nil {
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
	uc := EmergeTargets{
		FileSystem: infra.OSFileSystem{},
		Stdout:     &stdout,
	}

	if err := uc.Run(); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	for _, target := range []string{".env", "config/service-account.json"} {
		linkPath := filepath.Join(workspaceRoot, target)
		info, err := os.Lstat(linkPath)
		if err != nil {
			t.Fatalf("Lstat(%q) returned error: %v", linkPath, err)
		}

		if info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("%q is not a symlink: mode=%v", linkPath, info.Mode())
		}
	}

	if got, err := os.Readlink(filepath.Join(workspaceRoot, ".env")); err != nil {
		t.Fatalf("Readlink(.env) returned error: %v", err)
	} else if got != storeEnvPath {
		t.Fatalf("link target = %q, want %q", got, storeEnvPath)
	}

	if got, err := os.Readlink(filepath.Join(workspaceRoot, "config", "service-account.json")); err != nil {
		t.Fatalf("Readlink(service-account.json) returned error: %v", err)
	} else if got != storeConfigPath {
		t.Fatalf("link target = %q, want %q", got, storeConfigPath)
	}

	for _, want := range []string{"emerged target: .env", "emerged target: config/service-account.json"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, want substring %q", stdout.String(), want)
		}
	}
}

func TestEmergeTargetsEnsuresEncryptedVolumeBeforeSymlink(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	mountRoot := filepath.Join(tempHome, "veil-mount")
	workspaceRoot := filepath.Join(tempHome, "myapp")
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	resolvedWorkspaceRoot, err := filepath.EvalSymlinks(workspaceRoot)
	if err != nil {
		t.Fatalf("EvalSymlinks() returned error: %v", err)
	}

	writeConfigForTest(t, filepath.Join(tempHome, ".veil", "config.toml"), encryptedConfigForTest(mountRoot, resolvedWorkspaceRoot))

	storeEnvPath := filepath.Join(mountRoot, "workspaces", "myapp", ".env")
	if err := os.MkdirAll(filepath.Dir(storeEnvPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	if err := os.WriteFile(storeEnvPath, []byte("TOKEN=test\n"), 0o600); err != nil {
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

	runtime := &recordingEncryptedStoreRuntime{}
	uc := EmergeTargets{
		FileSystem:   infra.OSFileSystem{},
		StoreRuntime: runtime,
		Stdout:       &bytes.Buffer{},
		Now: func() time.Time {
			return time.Date(2026, 5, 13, 10, 0, 0, 0, time.UTC)
		},
	}

	if err := uc.Run(); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	if runtime.ensureCalls != 1 {
		t.Fatalf("ensure calls = %d, want 1", runtime.ensureCalls)
	}
	if got, err := os.Readlink(filepath.Join(workspaceRoot, ".env")); err != nil {
		t.Fatalf("Readlink(.env) returned error: %v", err)
	} else if got != storeEnvPath {
		t.Fatalf("link target = %q, want %q", got, storeEnvPath)
	}

	state := readStateForTest(t, filepath.Join(tempHome, ".veil", "state.toml"))
	lease, ok, err := state.FindLease("myapp", ".env")
	if err != nil {
		t.Fatalf("FindLease() returned error: %v", err)
	}
	if !ok {
		t.Fatal("lease not found")
	}
	if lease.StoreID != domain.DefaultStoreID || lease.StorePath != storeEnvPath {
		t.Fatalf("lease = %#v", lease)
	}
}

func TestEmergeTargetsPassesForceToEncryptedVolume(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	mountRoot := filepath.Join(tempHome, "veil-mount")
	workspaceRoot := filepath.Join(tempHome, "myapp")
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	resolvedWorkspaceRoot, err := filepath.EvalSymlinks(workspaceRoot)
	if err != nil {
		t.Fatalf("EvalSymlinks() returned error: %v", err)
	}

	writeConfigForTest(t, filepath.Join(tempHome, ".veil", "config.toml"), encryptedConfigForTest(mountRoot, resolvedWorkspaceRoot))
	storeEnvPath := filepath.Join(mountRoot, "workspaces", "myapp", ".env")
	if err := os.MkdirAll(filepath.Dir(storeEnvPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	if err := os.WriteFile(storeEnvPath, []byte("TOKEN=test\n"), 0o600); err != nil {
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

	runtime := &recordingEncryptedStoreRuntime{}
	uc := EmergeTargets{
		FileSystem:   infra.OSFileSystem{},
		StoreRuntime: runtime,
		Stdout:       &bytes.Buffer{},
		Force:        true,
	}

	if err := uc.Run(); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}
	if len(runtime.forceValues) != 1 || !runtime.forceValues[0] {
		t.Fatalf("force values = %#v, want [true]", runtime.forceValues)
	}
}

func TestEmergeTargetsReturnsErrorWhenStoreSourceIsMissing(t *testing.T) {
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

	uc := EmergeTargets{
		FileSystem: infra.OSFileSystem{},
		Stdout:     &bytes.Buffer{},
	}

	err = uc.Run()
	if err == nil {
		t.Fatal("Run() returned nil error")
	}

	if !strings.Contains(err.Error(), "store target does not exist") {
		t.Fatalf("error = %q", err)
	}
	if !strings.Contains(err.Error(), "cannot be reconstructed") {
		t.Fatalf("error = %q", err)
	}
}

func TestEmergeTargetsUnmountsEncryptedVolumeWhenStoreSourceIsMissing(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	mountRoot := filepath.Join(tempHome, "veil-mount")
	workspaceRoot := filepath.Join(tempHome, "myapp")
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	resolvedWorkspaceRoot, err := filepath.EvalSymlinks(workspaceRoot)
	if err != nil {
		t.Fatalf("EvalSymlinks() returned error: %v", err)
	}

	writeConfigForTest(t, filepath.Join(tempHome, ".veil", "config.toml"), encryptedConfigForTest(mountRoot, resolvedWorkspaceRoot))

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

	runtime := &recordingEncryptedStoreRuntime{}
	uc := EmergeTargets{
		FileSystem:   infra.OSFileSystem{},
		StoreRuntime: runtime,
		Stdout:       &bytes.Buffer{},
		Now: func() time.Time {
			return time.Date(2026, 5, 13, 10, 0, 0, 0, time.UTC)
		},
	}

	err = uc.Run()
	if err == nil {
		t.Fatal("Run() returned nil error")
	}
	if runtime.ensureCalls != 1 {
		t.Fatalf("ensure calls = %d, want 1", runtime.ensureCalls)
	}
	if runtime.unmountCalls != 1 {
		t.Fatalf("unmount calls = %d, want 1", runtime.unmountCalls)
	}
}

func TestEmergeTargetsRejectsExistingRegularFile(t *testing.T) {
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

	storeEnvPath := filepath.Join(storeRoot, "workspaces", "myapp", ".env")
	if err := os.MkdirAll(filepath.Dir(storeEnvPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	if err := os.WriteFile(storeEnvPath, []byte("TOKEN=test\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}

	workspaceTargetPath := filepath.Join(workspaceRoot, ".env")
	if err := os.WriteFile(workspaceTargetPath, []byte("shadowed\n"), 0o600); err != nil {
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

	uc := EmergeTargets{
		FileSystem: infra.OSFileSystem{},
		Stdout:     &bytes.Buffer{},
	}

	err = uc.Run()
	if err == nil {
		t.Fatal("Run() returned nil error")
	}

	if !strings.Contains(err.Error(), "workspace target already exists") {
		t.Fatalf("error = %q", err)
	}
}

func TestEmergeTargetsKeepsMatchingSymlink(t *testing.T) {
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

	storeEnvPath := filepath.Join(storeRoot, "workspaces", "myapp", ".env")
	if err := os.MkdirAll(filepath.Dir(storeEnvPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	if err := os.WriteFile(storeEnvPath, []byte("TOKEN=test\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}

	workspaceTargetPath := filepath.Join(workspaceRoot, ".env")
	if err := os.Symlink(storeEnvPath, workspaceTargetPath); err != nil {
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
	uc := EmergeTargets{
		FileSystem: infra.OSFileSystem{},
		Stdout:     &stdout,
	}

	if err := uc.Run(); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	if got, err := os.Readlink(workspaceTargetPath); err != nil {
		t.Fatalf("Readlink() returned error: %v", err)
	} else if got != storeEnvPath {
		t.Fatalf("link target = %q, want %q", got, storeEnvPath)
	}

	if !strings.Contains(stdout.String(), "already emerged target: .env") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestEmergeTargetsKeepsEquivalentRelativeSymlink(t *testing.T) {
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

	storeEnvPath := filepath.Join(storeRoot, "workspaces", "myapp", ".env")
	if err := os.MkdirAll(filepath.Dir(storeEnvPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	if err := os.WriteFile(storeEnvPath, []byte("TOKEN=test\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}

	workspaceTargetPath := filepath.Join(workspaceRoot, ".env")
	relativeLinkTarget, err := filepath.Rel(filepath.Dir(workspaceTargetPath), storeEnvPath)
	if err != nil {
		t.Fatalf("Rel() returned error: %v", err)
	}
	if err := os.Symlink(relativeLinkTarget, workspaceTargetPath); err != nil {
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
	uc := EmergeTargets{
		FileSystem: infra.OSFileSystem{},
		Stdout:     &stdout,
	}

	if err := uc.Run(); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	if got, err := os.Readlink(workspaceTargetPath); err != nil {
		t.Fatalf("Readlink() returned error: %v", err)
	} else if got != relativeLinkTarget {
		t.Fatalf("link target = %q, want %q", got, relativeLinkTarget)
	}

	if !strings.Contains(stdout.String(), "already emerged target: .env") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestEmergeTargetsWritesLeases(t *testing.T) {
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

	storeEnvPath := filepath.Join(storeRoot, "workspaces", "myapp", ".env")
	if err := os.MkdirAll(filepath.Dir(storeEnvPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	if err := os.WriteFile(storeEnvPath, []byte("TOKEN=test\n"), 0o600); err != nil {
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

	now := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	uc := EmergeTargets{
		FileSystem: infra.OSFileSystem{},
		Stdout:     &bytes.Buffer{},
		Now:        func() time.Time { return now },
	}

	if err := uc.Run(); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	state := readStateForTest(t, filepath.Join(tempHome, ".veil", "state.toml"))
	lease, ok, err := state.FindLease("myapp", ".env")
	if err != nil {
		t.Fatalf("FindLease() returned error: %v", err)
	}
	if !ok {
		t.Fatal("lease not found")
	}
	if !lease.MountedAt.Equal(now) {
		t.Fatalf("mounted_at = %v, want %v", lease.MountedAt, now)
	}
	if !lease.ExpiresAt.Equal(now.Add(24 * time.Hour)) {
		t.Fatalf("expires_at = %v, want %v", lease.ExpiresAt, now.Add(24*time.Hour))
	}
}

func TestEmergeTargetsRefreshesExistingLease(t *testing.T) {
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

	storeEnvPath := filepath.Join(storeRoot, "workspaces", "myapp", ".env")
	if err := os.MkdirAll(filepath.Dir(storeEnvPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	if err := os.WriteFile(storeEnvPath, []byte("TOKEN=test\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}

	workspaceTargetPath := filepath.Join(workspaceRoot, ".env")
	if err := os.Symlink(storeEnvPath, workspaceTargetPath); err != nil {
		t.Fatalf("Symlink() returned error: %v", err)
	}

	state := domain.DefaultState()
	initialMountedAt := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	mustUpsertLease(t, &state, "myapp", ".env", initialMountedAt, initialMountedAt.Add(time.Hour))
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

	now := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	uc := EmergeTargets{
		FileSystem: infra.OSFileSystem{},
		Stdout:     &bytes.Buffer{},
		Now:        func() time.Time { return now },
	}

	if err := uc.Run(); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	refreshed := readStateForTest(t, filepath.Join(tempHome, ".veil", "state.toml"))
	lease, ok, err := refreshed.FindLease("myapp", ".env")
	if err != nil {
		t.Fatalf("FindLease() returned error: %v", err)
	}
	if !ok {
		t.Fatal("lease not found")
	}
	if !lease.MountedAt.Equal(now) {
		t.Fatalf("mounted_at = %v, want %v", lease.MountedAt, now)
	}
	if !lease.ExpiresAt.Equal(now.Add(24 * time.Hour)) {
		t.Fatalf("expires_at = %v, want %v", lease.ExpiresAt, now.Add(24*time.Hour))
	}
}

func TestEmergeTargetsCanEmergeAllWorkspaces(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	storeRoot := filepath.Join(tempHome, "veil-store")
	alphaWorkspaceRoot := filepath.Join(tempHome, "alpha-workspace")
	betaWorkspaceRoot := filepath.Join(tempHome, "beta-workspace")
	for _, root := range []string{alphaWorkspaceRoot, betaWorkspaceRoot} {
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatalf("MkdirAll() returned error: %v", err)
		}
	}

	resolvedAlphaWorkspaceRoot, err := filepath.EvalSymlinks(alphaWorkspaceRoot)
	if err != nil {
		t.Fatalf("EvalSymlinks() returned error: %v", err)
	}
	resolvedBetaWorkspaceRoot, err := filepath.EvalSymlinks(betaWorkspaceRoot)
	if err != nil {
		t.Fatalf("EvalSymlinks() returned error: %v", err)
	}

	writeConfigForTest(
		t,
		filepath.Join(tempHome, ".veil", "config.toml"),
		"version = 1\nstore_path = "+workspaceRootQuoted(storeRoot)+"\ndefault_ttl = \"24h\"\n\n"+
			"[workspaces.alpha]\nroot = "+workspaceRootQuoted(resolvedAlphaWorkspaceRoot)+"\ntargets = [\".env\"]\n\n"+
			"[workspaces.beta]\nroot = "+workspaceRootQuoted(resolvedBetaWorkspaceRoot)+"\ntargets = [\"config/app.json\"]\n",
	)

	alphaStorePath := filepath.Join(storeRoot, "workspaces", "alpha", ".env")
	if err := os.MkdirAll(filepath.Dir(alphaStorePath), 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	if err := os.WriteFile(alphaStorePath, []byte("TOKEN=alpha\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}

	betaStorePath := filepath.Join(storeRoot, "workspaces", "beta", "config", "app.json")
	if err := os.MkdirAll(filepath.Dir(betaStorePath), 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	if err := os.WriteFile(betaStorePath, []byte("{\"project\":\"beta\"}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}

	previousWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() returned error: %v", err)
	}

	if err := os.Chdir(tempHome); err != nil {
		t.Fatalf("Chdir() returned error: %v", err)
	}
	defer func() {
		if err := os.Chdir(previousWD); err != nil {
			t.Fatalf("restore Chdir() returned error: %v", err)
		}
	}()

	now := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	var stdout bytes.Buffer
	uc := EmergeTargets{
		FileSystem:    infra.OSFileSystem{},
		Stdout:        &stdout,
		Now:           func() time.Time { return now },
		AllWorkspaces: true,
	}

	if err := uc.Run(); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	for linkPath, wantTarget := range map[string]string{
		filepath.Join(alphaWorkspaceRoot, ".env"):              alphaStorePath,
		filepath.Join(betaWorkspaceRoot, "config", "app.json"): betaStorePath,
	} {
		info, err := os.Lstat(linkPath)
		if err != nil {
			t.Fatalf("Lstat(%q) returned error: %v", linkPath, err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("%q is not a symlink: mode=%v", linkPath, info.Mode())
		}

		gotTarget, err := os.Readlink(linkPath)
		if err != nil {
			t.Fatalf("Readlink(%q) returned error: %v", linkPath, err)
		}
		if gotTarget != wantTarget {
			t.Fatalf("link target = %q, want %q", gotTarget, wantTarget)
		}
	}

	for _, want := range []string{
		"emerged          repo: alpha  file: .env",
		"emerged          repo: beta   file: config/app.json",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, want substring %q", stdout.String(), want)
		}
	}

	state := readStateForTest(t, filepath.Join(tempHome, ".veil", "state.toml"))
	for workspaceID, target := range map[string]string{
		"alpha": ".env",
		"beta":  "config/app.json",
	} {
		lease, ok, err := state.FindLease(workspaceID, target)
		if err != nil {
			t.Fatalf("FindLease(%q, %q) returned error: %v", workspaceID, target, err)
		}
		if !ok {
			t.Fatalf("lease not found for %s:%s", workspaceID, target)
		}
		if !lease.MountedAt.Equal(now) {
			t.Fatalf("mounted_at = %v, want %v", lease.MountedAt, now)
		}
		if !lease.ExpiresAt.Equal(now.Add(24 * time.Hour)) {
			t.Fatalf("expires_at = %v, want %v", lease.ExpiresAt, now.Add(24*time.Hour))
		}
	}
}

func TestEmergeOutputLayoutAlignsAllWorkspaceColumns(t *testing.T) {
	workspaces := []emergeWorkspace{
		{id: "short"},
		{id: "longer-repo"},
	}
	layout := newEmergeOutputLayout(true, workspaces)

	var stdout bytes.Buffer
	layout.writeTarget(&stdout, "short", "short:.env", ".env", true)
	layout.writeTarget(&stdout, "longer-repo", "longer-repo:config/app.json", "config/app.json", false)

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %q, want 2 lines", lines)
	}

	want := []string{
		"emerged          repo: short        file: .env",
		"already emerged  repo: longer-repo  file: config/app.json",
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Fatalf("line %d = %q, want %q", i, lines[i], want[i])
		}
	}

	if strings.Index(lines[0], "repo:") != strings.Index(lines[1], "repo:") {
		t.Fatalf("repo columns are not aligned: %q", lines)
	}
	if strings.Index(lines[0], "file:") != strings.Index(lines[1], "file:") {
		t.Fatalf("file columns are not aligned: %q", lines)
	}
}

func TestEmergeTargetsAllWorkspacesContinuesAfterTargetFailure(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	storeRoot := filepath.Join(tempHome, "veil-store")
	alphaWorkspaceRoot := filepath.Join(tempHome, "alpha-workspace")
	betaWorkspaceRoot := filepath.Join(tempHome, "beta-workspace")
	for _, root := range []string{alphaWorkspaceRoot, betaWorkspaceRoot} {
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatalf("MkdirAll() returned error: %v", err)
		}
	}

	resolvedAlphaWorkspaceRoot, err := filepath.EvalSymlinks(alphaWorkspaceRoot)
	if err != nil {
		t.Fatalf("EvalSymlinks() returned error: %v", err)
	}
	resolvedBetaWorkspaceRoot, err := filepath.EvalSymlinks(betaWorkspaceRoot)
	if err != nil {
		t.Fatalf("EvalSymlinks() returned error: %v", err)
	}

	writeConfigForTest(
		t,
		filepath.Join(tempHome, ".veil", "config.toml"),
		"version = 1\nstore_path = "+workspaceRootQuoted(storeRoot)+"\ndefault_ttl = \"24h\"\n\n"+
			"[workspaces.alpha]\nroot = "+workspaceRootQuoted(resolvedAlphaWorkspaceRoot)+"\ntargets = [\".env\"]\n\n"+
			"[workspaces.beta]\nroot = "+workspaceRootQuoted(resolvedBetaWorkspaceRoot)+"\ntargets = [\"config/app.json\"]\n",
	)

	alphaStorePath := filepath.Join(storeRoot, "workspaces", "alpha", ".env")
	if err := os.MkdirAll(filepath.Dir(alphaStorePath), 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	if err := os.WriteFile(alphaStorePath, []byte("TOKEN=alpha\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}

	previousWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() returned error: %v", err)
	}

	if err := os.Chdir(tempHome); err != nil {
		t.Fatalf("Chdir() returned error: %v", err)
	}
	defer func() {
		if err := os.Chdir(previousWD); err != nil {
			t.Fatalf("restore Chdir() returned error: %v", err)
		}
	}()

	var stdout bytes.Buffer
	uc := EmergeTargets{
		FileSystem:    infra.OSFileSystem{},
		Stdout:        &stdout,
		AllWorkspaces: true,
	}

	err = uc.Run()
	if err == nil {
		t.Fatal("Run() returned nil error")
	}
	if !strings.Contains(err.Error(), "store target does not exist") {
		t.Fatalf("error = %q", err)
	}
	if !strings.Contains(err.Error(), "beta:config/app.json") {
		t.Fatalf("error = %q", err)
	}

	linkPath := filepath.Join(alphaWorkspaceRoot, ".env")
	gotTarget, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("Readlink(%q) returned error: %v", linkPath, err)
	}
	if gotTarget != alphaStorePath {
		t.Fatalf("link target = %q, want %q", gotTarget, alphaStorePath)
	}

	for _, want := range []string{
		"emerged          repo: alpha  file: .env",
		"failed           repo: beta   file: config/app.json",
		"store target does not exist",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, want substring %q", stdout.String(), want)
		}
	}

	_, state, err := loadState(infra.OSFileSystem{})
	if err != nil {
		t.Fatalf("loadState() returned error: %v", err)
	}
	if got := len(state.Leases); got != 1 {
		t.Fatalf("lease count = %d, want 1", got)
	}
	if _, ok, err := state.FindLease("alpha", ".env"); err != nil || !ok {
		t.Fatalf("FindLease(alpha, .env) = _, %v, %v; want lease", ok, err)
	}
}

func TestEmergeTargetsAllWorkspacesContinuesAfterMissingWorkspaceRoot(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	storeRoot := filepath.Join(tempHome, "veil-store")
	alphaWorkspaceRoot := filepath.Join(tempHome, "alpha-workspace")
	missingWorkspaceRoot := filepath.Join(tempHome, "missing-workspace")
	if err := os.MkdirAll(alphaWorkspaceRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}

	resolvedAlphaWorkspaceRoot, err := filepath.EvalSymlinks(alphaWorkspaceRoot)
	if err != nil {
		t.Fatalf("EvalSymlinks() returned error: %v", err)
	}

	writeConfigForTest(
		t,
		filepath.Join(tempHome, ".veil", "config.toml"),
		"version = 1\nstore_path = "+workspaceRootQuoted(storeRoot)+"\ndefault_ttl = \"24h\"\n\n"+
			"[workspaces.alpha]\nroot = "+workspaceRootQuoted(resolvedAlphaWorkspaceRoot)+"\ntargets = [\".env\"]\n\n"+
			"[workspaces.beta]\nroot = "+workspaceRootQuoted(missingWorkspaceRoot)+"\ntargets = [\"config/app.json\"]\n",
	)

	alphaStorePath := filepath.Join(storeRoot, "workspaces", "alpha", ".env")
	if err := os.MkdirAll(filepath.Dir(alphaStorePath), 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	if err := os.WriteFile(alphaStorePath, []byte("TOKEN=alpha\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}

	betaStorePath := filepath.Join(storeRoot, "workspaces", "beta", "config", "app.json")
	if err := os.MkdirAll(filepath.Dir(betaStorePath), 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	if err := os.WriteFile(betaStorePath, []byte("{\"project\":\"beta\"}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}

	previousWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() returned error: %v", err)
	}

	if err := os.Chdir(tempHome); err != nil {
		t.Fatalf("Chdir() returned error: %v", err)
	}
	defer func() {
		if err := os.Chdir(previousWD); err != nil {
			t.Fatalf("restore Chdir() returned error: %v", err)
		}
	}()

	var stdout bytes.Buffer
	uc := EmergeTargets{
		FileSystem:    infra.OSFileSystem{},
		Stdout:        &stdout,
		AllWorkspaces: true,
	}

	err = uc.Run()
	if err == nil {
		t.Fatal("Run() returned nil error")
	}
	if !strings.Contains(err.Error(), "workspace root does not exist") {
		t.Fatalf("error = %q", err)
	}
	if !strings.Contains(err.Error(), "beta:") {
		t.Fatalf("error = %q", err)
	}

	linkPath := filepath.Join(alphaWorkspaceRoot, ".env")
	gotTarget, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("Readlink(%q) returned error: %v", linkPath, err)
	}
	if gotTarget != alphaStorePath {
		t.Fatalf("link target = %q, want %q", gotTarget, alphaStorePath)
	}

	for _, want := range []string{
		"emerged          repo: alpha  file: .env",
		"failed           repo: beta",
		"workspace root does not exist",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, want substring %q", stdout.String(), want)
		}
	}

	if _, err := os.Stat(missingWorkspaceRoot); !os.IsNotExist(err) {
		t.Fatalf("missing workspace root was created, err = %v", err)
	}

	_, state, err := loadState(infra.OSFileSystem{})
	if err != nil {
		t.Fatalf("loadState() returned error: %v", err)
	}
	if got := len(state.Leases); got != 1 {
		t.Fatalf("lease count = %d, want 1", got)
	}
	if _, ok, err := state.FindLease("alpha", ".env"); err != nil || !ok {
		t.Fatalf("FindLease(alpha, .env) = _, %v, %v; want lease", ok, err)
	}
}

func TestEmergeTargetsRollsBackNewSymlinkWhenStateWriteFails(t *testing.T) {
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

	storeEnvPath := filepath.Join(storeRoot, "workspaces", "myapp", ".env")
	if err := os.MkdirAll(filepath.Dir(storeEnvPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	if err := os.WriteFile(storeEnvPath, []byte("TOKEN=test\n"), 0o600); err != nil {
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

	uc := EmergeTargets{
		FileSystem: failingStateWriteFS{
			homeDir:       tempHome,
			stateWriteErr: errors.New("state write failed"),
		},
		Stdout: &bytes.Buffer{},
	}

	err = uc.Run()
	if err == nil {
		t.Fatal("Run() returned nil error")
	}
	if !strings.Contains(err.Error(), "write temporary state file") {
		t.Fatalf("error = %q", err)
	}

	if _, err := os.Lstat(filepath.Join(workspaceRoot, ".env")); !os.IsNotExist(err) {
		t.Fatalf("workspace symlink still exists, err = %v", err)
	}

	_, state, err := loadState(failingStateWriteFS{homeDir: tempHome})
	if err != nil {
		t.Fatalf("loadState() returned error: %v", err)
	}
	if got := len(state.Leases); got != 0 {
		t.Fatalf("lease count = %d, want 0", got)
	}
}
