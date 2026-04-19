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

func TestEmergeTargetsWritesLeasesAndStartsCleaner(t *testing.T) {
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
	starter := &stubTTLCleanerStarter{}
	uc := EmergeTargets{
		FileSystem:     infra.OSFileSystem{},
		Stdout:         &bytes.Buffer{},
		Now:            func() time.Time { return now },
		CleanerStarter: starter,
	}

	if err := uc.Run(); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	if starter.startCalls != 1 {
		t.Fatalf("start calls = %d, want 1", starter.startCalls)
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
		FileSystem:     infra.OSFileSystem{},
		Stdout:         &bytes.Buffer{},
		Now:            func() time.Time { return now },
		CleanerStarter: &stubTTLCleanerStarter{},
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
	starter := &stubTTLCleanerStarter{}
	var stdout bytes.Buffer
	uc := EmergeTargets{
		FileSystem:     infra.OSFileSystem{},
		Stdout:         &stdout,
		Now:            func() time.Time { return now },
		CleanerStarter: starter,
		AllWorkspaces:  true,
	}

	if err := uc.Run(); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	if starter.startCalls != 1 {
		t.Fatalf("start calls = %d, want 1", starter.startCalls)
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
		"emerged target: alpha:.env",
		"emerged target: beta:config/app.json",
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

func TestEmergeTargetsAllWorkspacesRollsBackOnFailure(t *testing.T) {
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

	uc := EmergeTargets{
		FileSystem:    infra.OSFileSystem{},
		Stdout:        &bytes.Buffer{},
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

	if _, err := os.Lstat(filepath.Join(alphaWorkspaceRoot, ".env")); !os.IsNotExist(err) {
		t.Fatalf("alpha workspace symlink still exists, err = %v", err)
	}

	_, state, err := loadState(infra.OSFileSystem{})
	if err != nil {
		t.Fatalf("loadState() returned error: %v", err)
	}
	if got := len(state.Leases); got != 0 {
		t.Fatalf("lease count = %d, want 0", got)
	}
}

func TestEmergeTargetsAllWorkspacesRejectsMissingWorkspaceRoot(t *testing.T) {
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

	uc := EmergeTargets{
		FileSystem:    infra.OSFileSystem{},
		Stdout:        &bytes.Buffer{},
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

	if _, err := os.Lstat(filepath.Join(alphaWorkspaceRoot, ".env")); !os.IsNotExist(err) {
		t.Fatalf("alpha workspace symlink still exists, err = %v", err)
	}

	if _, err := os.Stat(missingWorkspaceRoot); !os.IsNotExist(err) {
		t.Fatalf("missing workspace root was created, err = %v", err)
	}

	_, state, err := loadState(infra.OSFileSystem{})
	if err != nil {
		t.Fatalf("loadState() returned error: %v", err)
	}
	if got := len(state.Leases); got != 0 {
		t.Fatalf("lease count = %d, want 0", got)
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

func TestEmergeTargetsRestoresPreviousStateWhenCleanerStartFails(t *testing.T) {
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

	originalState := domain.DefaultState()
	originalMountedAt := time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC)
	mustUpsertLease(t, &originalState, "myapp", ".env", originalMountedAt, originalMountedAt.Add(time.Hour))
	writeStateForTest(t, filepath.Join(tempHome, ".veil", "state.toml"), originalState)

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
		FileSystem:     infra.OSFileSystem{},
		Stdout:         &bytes.Buffer{},
		Now:            func() time.Time { return now },
		CleanerStarter: failingCleanerStarter{err: errCleanerStartFailed},
	}

	err = uc.Run()
	if err == nil {
		t.Fatal("Run() returned nil error")
	}
	if !strings.Contains(err.Error(), "start ttl cleaner") {
		t.Fatalf("error = %q", err)
	}

	if _, err := os.Lstat(filepath.Join(workspaceRoot, ".env")); !os.IsNotExist(err) {
		t.Fatalf("workspace symlink still exists, err = %v", err)
	}

	state := readStateForTest(t, filepath.Join(tempHome, ".veil", "state.toml"))
	lease, ok, err := state.FindLease("myapp", ".env")
	if err != nil {
		t.Fatalf("FindLease() returned error: %v", err)
	}
	if !ok {
		t.Fatal("lease not found")
	}
	if !lease.MountedAt.Equal(originalMountedAt) {
		t.Fatalf("mounted_at = %v, want %v", lease.MountedAt, originalMountedAt)
	}
}
