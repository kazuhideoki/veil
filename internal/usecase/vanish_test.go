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

func TestVanishTargetsRemovesVeilManagedSymlinks(t *testing.T) {
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
	storeConfigPath := filepath.Join(storeRoot, "workspaces", "myapp", "config", "service-account.json")
	for _, path := range []string{storeEnvPath, storeConfigPath} {
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
	if err := os.Symlink(storeEnvPath, filepath.Join(workspaceRoot, ".env")); err != nil {
		t.Fatalf("Symlink() returned error: %v", err)
	}
	if err := os.Symlink(storeConfigPath, filepath.Join(workspaceRoot, "config", "service-account.json")); err != nil {
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
	uc := VanishTargets{
		FileSystem: infra.OSFileSystem{},
		Stdout:     &stdout,
	}

	if err := uc.Run(); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	for _, target := range []string{".env", "config/service-account.json"} {
		if _, err := os.Lstat(filepath.Join(workspaceRoot, target)); !os.IsNotExist(err) {
			t.Fatalf("workspace target still exists after vanish: %s, err=%v", target, err)
		}
		if !strings.Contains(stdout.String(), "vanished target: "+target) {
			t.Fatalf("stdout = %q, want vanished log for %q", stdout.String(), target)
		}
	}
}

func TestVanishTargetsUnmountsEncryptedVolumeWhenIdle(t *testing.T) {
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
	if err := os.Symlink(storeEnvPath, filepath.Join(workspaceRoot, ".env")); err != nil {
		t.Fatalf("Symlink() returned error: %v", err)
	}

	now := time.Date(2026, 5, 13, 10, 0, 0, 0, time.UTC)
	state := domain.DefaultState()
	if err := state.UpsertLeaseForStore("myapp", ".env", now.Add(-time.Hour), now.Add(time.Hour), domain.DefaultStoreID, filepath.Join(workspaceRoot, ".env"), storeEnvPath); err != nil {
		t.Fatalf("UpsertLeaseForStore() returned error: %v", err)
	}
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

	runtime := &recordingEncryptedStoreRuntime{}
	uc := VanishTargets{
		FileSystem:   infra.OSFileSystem{},
		StoreRuntime: runtime,
		Stdout:       &bytes.Buffer{},
		Now: func() time.Time {
			return now
		},
	}

	if err := uc.Run(); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	if runtime.ensureCalls != 0 {
		t.Fatalf("ensure calls = %d, want 0", runtime.ensureCalls)
	}
	if runtime.unmountCalls != 1 {
		t.Fatalf("unmount calls = %d, want 1", runtime.unmountCalls)
	}
}

func TestVanishTargetsAllWorkspacesDoesNotMountEncryptedVolume(t *testing.T) {
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

	storeTargetPath := filepath.Join(mountRoot, "workspaces", "myapp", ".env")
	workspaceTargetPath := filepath.Join(workspaceRoot, ".env")
	if err := os.Symlink(storeTargetPath, workspaceTargetPath); err != nil {
		t.Fatalf("Symlink() returned error: %v", err)
	}

	state := domain.DefaultState()
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	if err := state.UpsertLeaseForStore("myapp", ".env", now.Add(-time.Hour), now.Add(time.Hour), domain.DefaultStoreID, workspaceTargetPath, storeTargetPath); err != nil {
		t.Fatalf("UpsertLeaseForStore() returned error: %v", err)
	}
	writeStateForTest(t, filepath.Join(tempHome, ".veil", "state.toml"), state)

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

	runtime := &recordingEncryptedStoreRuntime{ensureErr: errors.New("unexpected mount")}
	var stdout bytes.Buffer
	uc := VanishTargets{
		FileSystem:    infra.OSFileSystem{},
		StoreRuntime:  runtime,
		Stdout:        &stdout,
		Now:           func() time.Time { return now },
		AllWorkspaces: true,
	}

	if err := uc.Run(); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}
	if runtime.ensureCalls != 0 {
		t.Fatalf("ensure calls = %d, want 0", runtime.ensureCalls)
	}
	if _, err := os.Lstat(workspaceTargetPath); !os.IsNotExist(err) {
		t.Fatalf("workspace target still exists after vanish --all: err=%v", err)
	}
	for _, want := range []string{"vanished", "repo: myapp", "file: .env"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, want substring %q", stdout.String(), want)
		}
	}

	refreshed := readStateForTest(t, filepath.Join(tempHome, ".veil", "state.toml"))
	if got := len(refreshed.Leases); got != 0 {
		t.Fatalf("lease count = %d, want 0", got)
	}
}

func TestVanishTargetsSkipsAbsentRegularAndForeignTargets(t *testing.T) {
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

	writeConfigForTest(t, filepath.Join(tempHome, ".veil", "config.toml"), "version = 1\nstore_path = "+workspaceRootQuoted(storeRoot)+"\ndefault_ttl = \"24h\"\n\n[workspaces.myapp]\nroot = "+workspaceRootQuoted(resolvedWorkspaceRoot)+"\ntargets = [\".env\", \"config/app.json\", \"token.txt\"]\n")

	foreignTargetPath := filepath.Join(tempHome, "foreign-secret")
	if err := os.WriteFile(foreignTargetPath, []byte("foreign\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(workspaceRoot, "config"), 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceRoot, "config", "app.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}
	if err := os.Symlink(foreignTargetPath, filepath.Join(workspaceRoot, "token.txt")); err != nil {
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
	uc := VanishTargets{
		FileSystem: infra.OSFileSystem{},
		Stdout:     &stdout,
	}

	if err := uc.Run(); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(workspaceRoot, "config", "app.json")); err != nil {
		t.Fatalf("Stat(app.json) returned error: %v", err)
	}
	if got, err := os.Readlink(filepath.Join(workspaceRoot, "token.txt")); err != nil {
		t.Fatalf("Readlink(token.txt) returned error: %v", err)
	} else if got != foreignTargetPath {
		t.Fatalf("link target = %q, want %q", got, foreignTargetPath)
	}

	for _, want := range []string{
		"already vanished target: .env",
		"skipped target: config/app.json",
		"skipped target: token.txt",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, want substring %q", stdout.String(), want)
		}
	}
}

func TestVanishTargetsRemovesBrokenManagedSymlinkWhenStoreFileIsMissing(t *testing.T) {
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

	missingStoreTargetPath := filepath.Join(storeRoot, "workspaces", "myapp", ".env")
	if err := os.MkdirAll(filepath.Dir(missingStoreTargetPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	if err := os.Symlink(missingStoreTargetPath, filepath.Join(workspaceRoot, ".env")); err != nil {
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
	uc := VanishTargets{
		FileSystem: infra.OSFileSystem{},
		Stdout:     &stdout,
	}

	if err := uc.Run(); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	if _, err := os.Lstat(filepath.Join(workspaceRoot, ".env")); !os.IsNotExist(err) {
		t.Fatalf("broken managed symlink still exists, err=%v", err)
	}
	if !strings.Contains(stdout.String(), "vanished target: .env") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestVanishTargetsClearsWorkspaceLeases(t *testing.T) {
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
	mustUpsertLease(t, &state, "myapp", ".env", now.Add(-time.Hour), now.Add(time.Hour))
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

	uc := VanishTargets{
		FileSystem: infra.OSFileSystem{},
		Stdout:     &bytes.Buffer{},
	}

	if err := uc.Run(); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	refreshed := readStateForTest(t, filepath.Join(tempHome, ".veil", "state.toml"))
	if got := len(refreshed.Leases); got != 0 {
		t.Fatalf("lease count = %d, want 0", got)
	}
}

func TestVanishTargetsAllWorkspacesRemovesSymlinksForEveryWorkspace(t *testing.T) {
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
	betaStorePath := filepath.Join(storeRoot, "workspaces", "beta", "config", "app.json")
	for _, path := range []string{alphaStorePath, betaStorePath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll() returned error: %v", err)
		}
		if err := os.WriteFile(path, []byte("secret\n"), 0o600); err != nil {
			t.Fatalf("WriteFile() returned error: %v", err)
		}
	}

	if err := os.MkdirAll(filepath.Join(betaWorkspaceRoot, "config"), 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	if err := os.Symlink(alphaStorePath, filepath.Join(alphaWorkspaceRoot, ".env")); err != nil {
		t.Fatalf("Symlink() returned error: %v", err)
	}
	if err := os.Symlink(betaStorePath, filepath.Join(betaWorkspaceRoot, "config", "app.json")); err != nil {
		t.Fatalf("Symlink() returned error: %v", err)
	}

	now := time.Date(2026, 5, 13, 10, 0, 0, 0, time.UTC)
	state := domain.DefaultState()
	mustUpsertLease(t, &state, "alpha", ".env", now.Add(-time.Hour), now.Add(time.Hour))
	mustUpsertLease(t, &state, "beta", "config/app.json", now.Add(-time.Hour), now.Add(time.Hour))
	writeStateForTest(t, filepath.Join(tempHome, ".veil", "state.toml"), state)

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
	uc := VanishTargets{
		FileSystem:    infra.OSFileSystem{},
		Stdout:        &stdout,
		AllWorkspaces: true,
	}

	if err := uc.Run(); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	for _, targetPath := range []string{
		filepath.Join(alphaWorkspaceRoot, ".env"),
		filepath.Join(betaWorkspaceRoot, "config", "app.json"),
	} {
		if _, err := os.Lstat(targetPath); !os.IsNotExist(err) {
			t.Fatalf("workspace target still exists after vanish --all: %s, err=%v", targetPath, err)
		}
	}

	for _, want := range []string{
		"vanished          repo: alpha  file: .env",
		"vanished          repo: beta   file: config/app.json",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, want substring %q", stdout.String(), want)
		}
	}

	refreshed := readStateForTest(t, filepath.Join(tempHome, ".veil", "state.toml"))
	if got := len(refreshed.Leases); got != 0 {
		t.Fatalf("lease count = %d, want 0", got)
	}
}

func TestVanishOutputLayoutAlignsAllWorkspaceColumns(t *testing.T) {
	workspaces := []emergeWorkspace{
		{id: "short"},
		{id: "longer-repo"},
	}
	layout := newVanishOutputLayout(true, workspaces)

	var stdout bytes.Buffer
	layout.writeTarget(&stdout, "short", ".env", "vanished")
	layout.writeTarget(&stdout, "longer-repo", "config/app.json", "already vanished")

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %q, want 2 lines", lines)
	}

	want := []string{
		"vanished          repo: short        file: .env",
		"already vanished  repo: longer-repo  file: config/app.json",
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

func TestVanishTargetsAllWorkspacesLogsMissingWorkspaceRootAndClearsLease(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	storeRoot := filepath.Join(tempHome, "veil-store")
	missingWorkspaceRoot := filepath.Join(tempHome, "missing-workspace")
	betaWorkspaceRoot := filepath.Join(tempHome, "beta-workspace")
	if err := os.MkdirAll(betaWorkspaceRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}

	resolvedBetaWorkspaceRoot, err := filepath.EvalSymlinks(betaWorkspaceRoot)
	if err != nil {
		t.Fatalf("EvalSymlinks() returned error: %v", err)
	}

	writeConfigForTest(
		t,
		filepath.Join(tempHome, ".veil", "config.toml"),
		"version = 1\nstore_path = "+workspaceRootQuoted(storeRoot)+"\ndefault_ttl = \"24h\"\n\n"+
			"[workspaces.alpha]\nroot = "+workspaceRootQuoted(missingWorkspaceRoot)+"\ntargets = [\".env\"]\n\n"+
			"[workspaces.beta]\nroot = "+workspaceRootQuoted(resolvedBetaWorkspaceRoot)+"\ntargets = [\"config/app.json\"]\n",
	)

	betaStorePath := filepath.Join(storeRoot, "workspaces", "beta", "config", "app.json")
	if err := os.MkdirAll(filepath.Dir(betaStorePath), 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	if err := os.WriteFile(betaStorePath, []byte("{\"project\":\"beta\"}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(betaWorkspaceRoot, "config"), 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	if err := os.Symlink(betaStorePath, filepath.Join(betaWorkspaceRoot, "config", "app.json")); err != nil {
		t.Fatalf("Symlink() returned error: %v", err)
	}

	now := time.Date(2026, 5, 13, 10, 0, 0, 0, time.UTC)
	state := domain.DefaultState()
	mustUpsertLease(t, &state, "alpha", ".env", now.Add(-time.Hour), now.Add(time.Hour))
	mustUpsertLease(t, &state, "beta", "config/app.json", now.Add(-time.Hour), now.Add(time.Hour))
	writeStateForTest(t, filepath.Join(tempHome, ".veil", "state.toml"), state)

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
	uc := VanishTargets{
		FileSystem:    infra.OSFileSystem{},
		Stdout:        &stdout,
		AllWorkspaces: true,
	}

	if err := uc.Run(); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	output := stdout.String()
	for _, want := range []string{
		"missing root      repo: alpha",
		"file: .env",
		"workspace: " + missingWorkspaceRoot,
		"target not inspected; lease cleared",
		"vanished          repo: beta",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("stdout = %q, want substring %q", output, want)
		}
	}

	if _, err := os.Lstat(filepath.Join(betaWorkspaceRoot, "config", "app.json")); !os.IsNotExist(err) {
		t.Fatalf("beta workspace target still exists after vanish --all: err=%v", err)
	}
	refreshed := readStateForTest(t, filepath.Join(tempHome, ".veil", "state.toml"))
	if got := len(refreshed.Leases); got != 0 {
		t.Fatalf("lease count = %d, want 0", got)
	}
}

func TestVanishTargetsAllWorkspacesContinuesAfterTargetFailure(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	storeRoot := filepath.Join(tempHome, "veil-store")
	alphaWorkspaceRoot := filepath.Join(tempHome, "alpha-workspace")
	betaWorkspaceRoot := filepath.Join(tempHome, "beta-workspace")
	if err := os.MkdirAll(alphaWorkspaceRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	if err := os.WriteFile(betaWorkspaceRoot, []byte("not a directory\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
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
			"[workspaces.beta]\nroot = "+workspaceRootQuoted(resolvedBetaWorkspaceRoot)+"\ntargets = [\".env\"]\n",
	)

	alphaStorePath := filepath.Join(storeRoot, "workspaces", "alpha", ".env")
	if err := os.MkdirAll(filepath.Dir(alphaStorePath), 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	if err := os.WriteFile(alphaStorePath, []byte("TOKEN=alpha\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}
	if err := os.Symlink(alphaStorePath, filepath.Join(alphaWorkspaceRoot, ".env")); err != nil {
		t.Fatalf("Symlink() returned error: %v", err)
	}

	now := time.Date(2026, 5, 13, 10, 0, 0, 0, time.UTC)
	state := domain.DefaultState()
	mustUpsertLease(t, &state, "alpha", ".env", now.Add(-time.Hour), now.Add(time.Hour))
	mustUpsertLease(t, &state, "beta", ".env", now.Add(-time.Hour), now.Add(time.Hour))
	writeStateForTest(t, filepath.Join(tempHome, ".veil", "state.toml"), state)

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
	uc := VanishTargets{
		FileSystem:    infra.OSFileSystem{},
		Stdout:        &stdout,
		AllWorkspaces: true,
	}

	err = uc.Run()
	if err == nil {
		t.Fatal("Run() returned nil error")
	}
	if !strings.Contains(err.Error(), "beta:.env") {
		t.Fatalf("error = %q, want beta target label", err)
	}
	if !strings.Contains(err.Error(), "stat workspace target") {
		t.Fatalf("error = %q, want stat workspace target", err)
	}
	if _, err := os.Lstat(filepath.Join(alphaWorkspaceRoot, ".env")); !os.IsNotExist(err) {
		t.Fatalf("alpha workspace target still exists after vanish --all: err=%v", err)
	}

	for _, want := range []string{
		"vanished",
		"repo: alpha",
		"failed",
		"repo: beta",
		"stat workspace target",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, want substring %q", stdout.String(), want)
		}
	}

	refreshed := readStateForTest(t, filepath.Join(tempHome, ".veil", "state.toml"))
	if _, ok, err := refreshed.FindLease("alpha", ".env"); err != nil || ok {
		t.Fatalf("FindLease(alpha, .env) = _, %v, %v; want no lease", ok, err)
	}
	if _, ok, err := refreshed.FindLease("beta", ".env"); err != nil || !ok {
		t.Fatalf("FindLease(beta, .env) = _, %v, %v; want lease", ok, err)
	}
}
