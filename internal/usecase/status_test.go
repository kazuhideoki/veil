package usecase

import (
	"bytes"
	"fmt"
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
		"Workspace:\n  current_dir: " + resolvedWorkspaceRoot + "\n  registered: yes\n  id: myapp\n  root: " + resolvedWorkspaceRoot,
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

func TestStatusTargetsReportsUnregisteredWorkspaceWithoutFailing(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	storeRoot := filepath.Join(tempHome, "veil-store")
	workspaceRoot := filepath.Join(tempHome, "myapp")
	otherRoot := filepath.Join(tempHome, "other")
	for _, path := range []string{workspaceRoot, otherRoot} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("MkdirAll() returned error: %v", err)
		}
	}

	resolvedWorkspaceRoot, err := filepath.EvalSymlinks(workspaceRoot)
	if err != nil {
		t.Fatalf("EvalSymlinks() returned error: %v", err)
	}
	resolvedOtherRoot, err := filepath.EvalSymlinks(otherRoot)
	if err != nil {
		t.Fatalf("EvalSymlinks() returned error: %v", err)
	}

	writeConfigForTest(t, filepath.Join(tempHome, ".veil", "config.toml"), "version = 1\nstore_path = "+workspaceRootQuoted(storeRoot)+"\ndefault_ttl = \"24h\"\n\n[workspaces.myapp]\nroot = "+workspaceRootQuoted(resolvedWorkspaceRoot)+"\ntargets = [\".env\"]\n")

	previousWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() returned error: %v", err)
	}

	if err := os.Chdir(otherRoot); err != nil {
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

	want := "Workspace:\n  current_dir: " + resolvedOtherRoot + "\n  registered: no\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
}

func TestStatusTargetsReportsEncryptedStoreWithoutMounting(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	mountRoot := filepath.Join(tempHome, "veil-mount")
	sessionDir := filepath.Join(tempHome, "sessions")
	workspaceRoot := filepath.Join(tempHome, "myapp")
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	resolvedWorkspaceRoot, err := filepath.EvalSymlinks(workspaceRoot)
	if err != nil {
		t.Fatalf("EvalSymlinks() returned error: %v", err)
	}

	configText := strings.ReplaceAll(encryptedConfigForTest(mountRoot, resolvedWorkspaceRoot), `directory = "/tmp/VeilStore.sessions"`, `directory = `+workspaceRootQuoted(sessionDir))
	writeConfigForTest(t, filepath.Join(tempHome, ".veil", "config.toml"), configText)
	if err := os.MkdirAll(mountRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(mountRoot, ".veil-store"), []byte(`{"version":1,"store_id":"default"}`), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}
	storeTargetPath := filepath.Join(mountRoot, "workspaces", "myapp", ".env")
	if err := os.MkdirAll(filepath.Dir(storeTargetPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	if err := os.WriteFile(storeTargetPath, []byte("TOKEN=secret\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}
	if err := os.Symlink(storeTargetPath, filepath.Join(workspaceRoot, ".env")); err != nil {
		t.Fatalf("Symlink() returned error: %v", err)
	}

	now := time.Date(2026, 5, 13, 10, 0, 0, 0, time.UTC)
	state := domain.DefaultState()
	if err := state.UpsertLeaseForStore("myapp", ".env", now.Add(-time.Hour), now.Add(time.Hour), domain.DefaultStoreID, filepath.Join(workspaceRoot, ".env"), storeTargetPath); err != nil {
		t.Fatalf("UpsertLeaseForStore() returned error: %v", err)
	}
	writeStateForTest(t, filepath.Join(tempHome, ".veil", "state.toml"), state)

	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tempHome, ".veil", "encrypted-volume-session-id"), []byte("self"), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}
	selfSessionJSON := fmt.Sprintf(`{"version":1,"session_id":"self","store_id":"default","host":"self-mac","last_seen_at":%q,"mount_path":%q,"state":"mounted"}`, now.Add(-time.Minute).Format(time.RFC3339), mountRoot)
	if err := os.WriteFile(filepath.Join(sessionDir, "self.json"), []byte(selfSessionJSON), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}
	sessionJSON := fmt.Sprintf(`{"version":1,"session_id":"other","store_id":"default","host":"other-mac","last_seen_at":%q,"mount_path":%q,"state":"mounted"}`, now.Add(-time.Minute).Format(time.RFC3339), mountRoot)
	if err := os.WriteFile(filepath.Join(sessionDir, "other.json"), []byte(sessionJSON), 0o600); err != nil {
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
		FileSystem:         infra.OSFileSystem{},
		StoreStatusChecker: stubStoreStatusChecker{mounted: true},
		Stdout:             &stdout,
		Now:                func() time.Time { return now },
	}

	if err := uc.Run(); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	for _, want := range []string{
		"Store:\n  backend: encrypted_volume\n  mounted: yes\n  mount_path: " + mountRoot,
		"Local leases:\n  myapp .env expires at ",
		"Other sessions:\n  other-mac last seen ",
		"mounted target: .env",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, want substring %q", stdout.String(), want)
		}
	}
	if strings.Contains(stdout.String(), "self-mac") {
		t.Fatalf("stdout = %q, should not include own session", stdout.String())
	}
}

type stubStoreStatusChecker struct {
	mounted bool
}

func (s stubStoreStatusChecker) IsMounted(domain.Config) bool {
	return s.mounted
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
