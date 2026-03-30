package usecase

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kazuhideoki/veil/internal/infra"
)

func TestRemoveTargetRestoresWorkspaceFileAndUpdatesConfig(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	storeRoot := filepath.Join(tempHome, "veil-store")
	workspaceRoot := filepath.Join(tempHome, "myapp")
	if err := os.MkdirAll(filepath.Join(workspaceRoot, "config"), 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}

	resolvedWorkspaceRoot, err := filepath.EvalSymlinks(workspaceRoot)
	if err != nil {
		t.Fatalf("EvalSymlinks() returned error: %v", err)
	}

	configPath := filepath.Join(tempHome, ".veil", "config.toml")
	writeConfigForTest(t, configPath, "version = 1\nstore_path = "+workspaceRootQuoted(storeRoot)+"\ndefault_ttl = \"24h\"\n\n[workspaces.myapp]\nroot = "+workspaceRootQuoted(resolvedWorkspaceRoot)+"\ntargets = [\"config/service-account.json\"]\n")

	storeTargetPath := filepath.Join(storeRoot, "workspaces", "myapp", "config", "service-account.json")
	const targetBody = "{\"key\":\"value\"}\n"
	if err := os.MkdirAll(filepath.Dir(storeTargetPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	if err := os.WriteFile(storeTargetPath, []byte(targetBody), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}

	workspaceTargetPath := filepath.Join(workspaceRoot, "config", "service-account.json")
	if err := os.Symlink(storeTargetPath, workspaceTargetPath); err != nil {
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
	uc := RemoveTarget{
		FileSystem: infra.OSFileSystem{},
		Stdout:     &stdout,
		TargetPath: "config/service-account.json",
	}

	if err := uc.Run(); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	workspaceData, err := os.ReadFile(workspaceTargetPath)
	if err != nil {
		t.Fatalf("ReadFile(workspace target) returned error: %v", err)
	}
	if string(workspaceData) != targetBody {
		t.Fatalf("workspace data = %q, want %q", string(workspaceData), targetBody)
	}

	if _, err := os.Lstat(storeTargetPath); !os.IsNotExist(err) {
		t.Fatalf("store target still exists, stat error = %v", err)
	}

	configData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(config) returned error: %v", err)
	}
	if !strings.Contains(string(configData), "targets = []") {
		t.Fatalf("config contents = %q", string(configData))
	}

	for _, want := range []string{"writing config", "removed target: config/service-account.json"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, want substring %q", stdout.String(), want)
		}
	}
}

func TestRemoveTargetReturnsErrorWhenWorkspacePathIsShadowed(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	storeRoot := filepath.Join(tempHome, "veil-store")
	workspaceRoot := filepath.Join(tempHome, "myapp")
	if err := os.MkdirAll(filepath.Join(workspaceRoot, "config"), 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}

	resolvedWorkspaceRoot, err := filepath.EvalSymlinks(workspaceRoot)
	if err != nil {
		t.Fatalf("EvalSymlinks() returned error: %v", err)
	}

	writeConfigForTest(t, filepath.Join(tempHome, ".veil", "config.toml"), "version = 1\nstore_path = "+workspaceRootQuoted(storeRoot)+"\ndefault_ttl = \"24h\"\n\n[workspaces.myapp]\nroot = "+workspaceRootQuoted(resolvedWorkspaceRoot)+"\ntargets = [\"config/service-account.json\"]\n")

	storeTargetPath := filepath.Join(storeRoot, "workspaces", "myapp", "config", "service-account.json")
	if err := os.MkdirAll(filepath.Dir(storeTargetPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	if err := os.WriteFile(storeTargetPath, []byte("secret\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}

	workspaceTargetPath := filepath.Join(workspaceRoot, "config", "service-account.json")
	if err := os.WriteFile(workspaceTargetPath, []byte("{}\n"), 0o600); err != nil {
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

	uc := RemoveTarget{
		FileSystem: infra.OSFileSystem{},
		Stdout:     &bytes.Buffer{},
		TargetPath: "config/service-account.json",
	}

	err = uc.Run()
	if err == nil {
		t.Fatal("Run() returned nil error")
	}
	if !strings.Contains(err.Error(), "workspace target already exists") {
		t.Fatalf("error = %q", err)
	}
}

func TestRemoveTargetRollsBackWorkspaceChangesWhenConfigWriteFails(t *testing.T) {
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

	configPath := filepath.Join(tempHome, ".veil", "config.toml")
	writeConfigForTest(t, configPath, "version = 1\nstore_path = "+workspaceRootQuoted(storeRoot)+"\ndefault_ttl = \"24h\"\n\n[workspaces.myapp]\nroot = "+workspaceRootQuoted(resolvedWorkspaceRoot)+"\ntargets = [\".env\"]\n")

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

	uc := RemoveTarget{
		FileSystem: failingConfigWriteFS{
			homeDir:        tempHome,
			configPath:     configPath,
			configWriteErr: errors.New("config locked"),
		},
		Stdout:     &bytes.Buffer{},
		TargetPath: ".env",
	}

	err = uc.Run()
	if err == nil {
		t.Fatal("Run() returned nil error")
	}
	if !strings.Contains(err.Error(), "write config file") {
		t.Fatalf("error = %q", err)
	}

	if got, err := os.Readlink(workspaceTargetPath); err != nil {
		t.Fatalf("Readlink() returned error: %v", err)
	} else if got != storeTargetPath {
		t.Fatalf("link target = %q, want %q", got, storeTargetPath)
	}

	if _, err := os.Stat(storeTargetPath); err != nil {
		t.Fatalf("Stat(store target) returned error: %v", err)
	}
}

func TestPurgeTargetRemovesStoreAndConfigWhileLeavingForeignWorkspaceFile(t *testing.T) {
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

	configPath := filepath.Join(tempHome, ".veil", "config.toml")
	writeConfigForTest(t, configPath, "version = 1\nstore_path = "+workspaceRootQuoted(storeRoot)+"\ndefault_ttl = \"24h\"\n\n[workspaces.myapp]\nroot = "+workspaceRootQuoted(resolvedWorkspaceRoot)+"\ntargets = [\".env\"]\n")

	storeTargetPath := filepath.Join(storeRoot, "workspaces", "myapp", ".env")
	if err := os.MkdirAll(filepath.Dir(storeTargetPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	if err := os.WriteFile(storeTargetPath, []byte("TOKEN=secret\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}

	workspaceTargetPath := filepath.Join(workspaceRoot, ".env")
	const localBody = "TOKEN=local\n"
	if err := os.WriteFile(workspaceTargetPath, []byte(localBody), 0o600); err != nil {
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
	uc := PurgeTarget{
		FileSystem: infra.OSFileSystem{},
		Stdout:     &stdout,
		TargetPath: ".env",
	}

	if err := uc.Run(); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	workspaceData, err := os.ReadFile(workspaceTargetPath)
	if err != nil {
		t.Fatalf("ReadFile(workspace target) returned error: %v", err)
	}
	if string(workspaceData) != localBody {
		t.Fatalf("workspace data = %q, want %q", string(workspaceData), localBody)
	}

	if _, err := os.Lstat(storeTargetPath); !os.IsNotExist(err) {
		t.Fatalf("store target still exists, stat error = %v", err)
	}

	configData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(config) returned error: %v", err)
	}
	if !strings.Contains(string(configData), "targets = []") {
		t.Fatalf("config contents = %q", string(configData))
	}

	for _, want := range []string{"writing config", "purged target: .env"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, want substring %q", stdout.String(), want)
		}
	}
}

func TestPurgeTargetRemovesBrokenManagedSymlinkAndUnregistersMissingStore(t *testing.T) {
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

	configPath := filepath.Join(tempHome, ".veil", "config.toml")
	writeConfigForTest(t, configPath, "version = 1\nstore_path = "+workspaceRootQuoted(storeRoot)+"\ndefault_ttl = \"24h\"\n\n[workspaces.myapp]\nroot = "+workspaceRootQuoted(resolvedWorkspaceRoot)+"\ntargets = [\".env\"]\n")

	storeTargetPath := filepath.Join(storeRoot, "workspaces", "myapp", ".env")
	if err := os.MkdirAll(filepath.Dir(storeTargetPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}

	workspaceTargetPath := filepath.Join(workspaceRoot, ".env")
	if err := os.Symlink(storeTargetPath, workspaceTargetPath); err != nil {
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

	uc := PurgeTarget{
		FileSystem: infra.OSFileSystem{},
		Stdout:     &bytes.Buffer{},
		TargetPath: ".env",
	}

	if err := uc.Run(); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	if _, err := os.Lstat(workspaceTargetPath); !os.IsNotExist(err) {
		t.Fatalf("workspace target still exists, stat error = %v", err)
	}

	configData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(config) returned error: %v", err)
	}
	if !strings.Contains(string(configData), "targets = []") {
		t.Fatalf("config contents = %q", string(configData))
	}
}

func TestPurgeTargetKeepsStoreAndWorkspaceStateWhenConfigWriteFails(t *testing.T) {
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

	configPath := filepath.Join(tempHome, ".veil", "config.toml")
	writeConfigForTest(t, configPath, "version = 1\nstore_path = "+workspaceRootQuoted(storeRoot)+"\ndefault_ttl = \"24h\"\n\n[workspaces.myapp]\nroot = "+workspaceRootQuoted(resolvedWorkspaceRoot)+"\ntargets = [\".env\"]\n")

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

	uc := PurgeTarget{
		FileSystem: failingConfigWriteFS{
			homeDir:        tempHome,
			configPath:     configPath,
			configWriteErr: errors.New("config locked"),
		},
		Stdout:     &bytes.Buffer{},
		TargetPath: ".env",
	}

	err = uc.Run()
	if err == nil {
		t.Fatal("Run() returned nil error")
	}
	if !strings.Contains(err.Error(), "write config file") {
		t.Fatalf("error = %q", err)
	}

	if got, err := os.Readlink(workspaceTargetPath); err != nil {
		t.Fatalf("Readlink() returned error: %v", err)
	} else if got != storeTargetPath {
		t.Fatalf("link target = %q, want %q", got, storeTargetPath)
	}

	if _, err := os.Stat(storeTargetPath); err != nil {
		t.Fatalf("Stat(store target) returned error: %v", err)
	}

	configData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(config) returned error: %v", err)
	}
	if !strings.Contains(string(configData), "targets = [\".env\"]") {
		t.Fatalf("config contents = %q", string(configData))
	}
}
