package usecase

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kazuhideoki/veil/internal/infra"
)

func TestAddTargetMovesFileIntoStoreAndUpdatesConfig(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	workspaceRoot := filepath.Join(tempHome, "myapp")
	if err := os.MkdirAll(filepath.Join(workspaceRoot, "config"), 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	resolvedWorkspaceRoot, err := filepath.EvalSymlinks(workspaceRoot)
	if err != nil {
		t.Fatalf("EvalSymlinks() returned error: %v", err)
	}

	storeRoot := filepath.Join(tempHome, "veil-store")

	configPath := filepath.Join(tempHome, ".veil", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}

	const existingConfig = "version = 1\nstore_path = %s\ndefault_ttl = \"24h\"\n\n[workspaces.myapp]\nroot = %s\ntargets = []\n"
	if err := os.WriteFile(configPath, []byte(
		fmt.Sprintf(existingConfig, workspaceRootQuoted(storeRoot), workspaceRootQuoted(resolvedWorkspaceRoot)),
	), 0o644); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}

	targetPath := filepath.Join(workspaceRoot, "config", "service-account.json")
	const targetBody = "{\"key\":\"value\"}\n"
	if err := os.WriteFile(targetPath, []byte(targetBody), 0o600); err != nil {
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
	uc := AddTarget{
		FileSystem:     infra.OSFileSystem{},
		TrackedChecker: infra.GitCLI{},
		Stdout:         &stdout,
		TargetPath:     "config/service-account.json",
	}

	if err := uc.Run(); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	if _, err := os.Stat(targetPath); !os.IsNotExist(err) {
		t.Fatalf("workspace target still exists, stat error = %v", err)
	}

	storePath := filepath.Join(storeRoot, "workspaces", "myapp", "config", "service-account.json")
	storeData, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatalf("ReadFile(store) returned error: %v", err)
	}

	if string(storeData) != targetBody {
		t.Fatalf("store data = %q, want %q", string(storeData), targetBody)
	}

	configData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(config) returned error: %v", err)
	}

	if !strings.Contains(string(configData), "targets = [\"config/service-account.json\"]") {
		t.Fatalf("config contents = %q", string(configData))
	}

	for _, want := range []string{"writing config", "added target: config/service-account.json"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, want substring %q", stdout.String(), want)
		}
	}
}

func TestAddTargetReturnsErrorWhenVeilIsNotInitialized(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	uc := AddTarget{
		FileSystem:     infra.OSFileSystem{},
		TrackedChecker: infra.GitCLI{},
		Stdout:         &bytes.Buffer{},
		TargetPath:     ".env",
	}

	err := uc.Run()
	if err == nil {
		t.Fatal("Run() returned nil error")
	}

	if !strings.Contains(err.Error(), "veil is not initialized") {
		t.Fatalf("error = %q", err)
	}
}

func TestAddTargetReturnsErrorWhenWorkspaceIsNotRegistered(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	workspaceRoot := filepath.Join(tempHome, "myapp")
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}

	writeConfigForTest(t, filepath.Join(tempHome, ".veil", "config.toml"), "version = 1\nstore_path = \"/tmp/veil-store\"\ndefault_ttl = \"24h\"\n")

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

	uc := AddTarget{
		FileSystem:     infra.OSFileSystem{},
		TrackedChecker: infra.GitCLI{},
		Stdout:         &bytes.Buffer{},
		TargetPath:     ".env",
	}

	err = uc.Run()
	if err == nil {
		t.Fatal("Run() returned nil error")
	}

	if !strings.Contains(err.Error(), "workspace is not registered") {
		t.Fatalf("error = %q", err)
	}
}

func TestAddTargetReturnsErrorWhenTargetIsTrackedByGit(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	workspaceRoot := filepath.Join(tempHome, "myapp")
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	resolvedWorkspaceRoot, err := filepath.EvalSymlinks(workspaceRoot)
	if err != nil {
		t.Fatalf("EvalSymlinks() returned error: %v", err)
	}

	writeConfigForTest(t, filepath.Join(tempHome, ".veil", "config.toml"), "version = 1\nstore_path = \"/tmp/veil-store\"\ndefault_ttl = \"24h\"\n\n[workspaces.myapp]\nroot = "+workspaceRootQuoted(resolvedWorkspaceRoot)+"\ntargets = []\n")

	targetPath := filepath.Join(workspaceRoot, ".env")
	if err := os.WriteFile(targetPath, []byte("TOKEN=test\n"), 0o600); err != nil {
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

	uc := AddTarget{
		FileSystem:     infra.OSFileSystem{},
		TrackedChecker: stubTrackedChecker{tracked: true},
		Stdout:         &bytes.Buffer{},
		TargetPath:     ".env",
	}

	err = uc.Run()
	if err == nil {
		t.Fatal("Run() returned nil error")
	}

	if !strings.Contains(err.Error(), "target is tracked by git") {
		t.Fatalf("error = %q", err)
	}
}

func TestAddTargetReturnsErrorWhenStoreFileAlreadyExists(t *testing.T) {
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

	writeConfigForTest(t, filepath.Join(tempHome, ".veil", "config.toml"), "version = 1\nstore_path = "+workspaceRootQuoted(storeRoot)+"\ndefault_ttl = \"24h\"\n\n[workspaces.myapp]\nroot = "+workspaceRootQuoted(resolvedWorkspaceRoot)+"\ntargets = []\n")

	targetPath := filepath.Join(workspaceRoot, ".env")
	if err := os.WriteFile(targetPath, []byte("TOKEN=test\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}

	storeTargetPath := filepath.Join(storeRoot, "workspaces", "myapp", ".env")
	if err := os.MkdirAll(filepath.Dir(storeTargetPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	if err := os.WriteFile(storeTargetPath, []byte("existing\n"), 0o600); err != nil {
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

	uc := AddTarget{
		FileSystem:     infra.OSFileSystem{},
		TrackedChecker: stubTrackedChecker{},
		Stdout:         &bytes.Buffer{},
		TargetPath:     ".env",
	}

	err = uc.Run()
	if err == nil {
		t.Fatal("Run() returned nil error")
	}

	if !strings.Contains(err.Error(), "store target already exists") {
		t.Fatalf("error = %q", err)
	}
}

func TestAddTargetExpandsStorePathFromConfig(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	workspaceRoot := filepath.Join(tempHome, "myapp")
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	resolvedWorkspaceRoot, err := filepath.EvalSymlinks(workspaceRoot)
	if err != nil {
		t.Fatalf("EvalSymlinks() returned error: %v", err)
	}

	writeConfigForTest(t, filepath.Join(tempHome, ".veil", "config.toml"), "version = 1\nstore_path = \"~/Library/Mobile Documents/com~apple~CloudDocs/VeilStore\"\ndefault_ttl = \"24h\"\n\n[workspaces.myapp]\nroot = "+workspaceRootQuoted(resolvedWorkspaceRoot)+"\ntargets = []\n")

	targetPath := filepath.Join(workspaceRoot, ".env")
	if err := os.WriteFile(targetPath, []byte("TOKEN=test\n"), 0o600); err != nil {
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

	uc := AddTarget{
		FileSystem:     infra.OSFileSystem{},
		TrackedChecker: stubTrackedChecker{},
		Stdout:         &bytes.Buffer{},
		TargetPath:     ".env",
	}

	if err := uc.Run(); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	storeTargetPath := filepath.Join(tempHome, "Library", "Mobile Documents", "com~apple~CloudDocs", "VeilStore", "workspaces", "myapp", ".env")
	if _, err := os.Stat(storeTargetPath); err != nil {
		t.Fatalf("Stat(store target) returned error: %v", err)
	}
}

func TestAddTargetResolvesCanonicalWorkspaceRoot(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	realWorkspace := filepath.Join(tempHome, "real-workspace")
	if err := os.MkdirAll(realWorkspace, 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}

	symlinkWorkspace := filepath.Join(tempHome, "workspace-link")
	if err := os.Symlink(realWorkspace, symlinkWorkspace); err != nil {
		t.Fatalf("Symlink() returned error: %v", err)
	}

	writeConfigForTest(t, filepath.Join(tempHome, ".veil", "config.toml"), "version = 1\nstore_path = "+workspaceRootQuoted(filepath.Join(tempHome, "veil-store"))+"\ndefault_ttl = \"24h\"\n\n[workspaces.myapp]\nroot = "+workspaceRootQuoted(symlinkWorkspace)+"\ntargets = []\n")

	targetPath := filepath.Join(realWorkspace, ".env")
	if err := os.WriteFile(targetPath, []byte("TOKEN=test\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}

	previousWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() returned error: %v", err)
	}

	if err := os.Chdir(realWorkspace); err != nil {
		t.Fatalf("Chdir() returned error: %v", err)
	}
	defer func() {
		if err := os.Chdir(previousWD); err != nil {
			t.Fatalf("restore Chdir() returned error: %v", err)
		}
	}()

	uc := AddTarget{
		FileSystem:     infra.OSFileSystem{},
		TrackedChecker: stubTrackedChecker{},
		Stdout:         &bytes.Buffer{},
		TargetPath:     ".env",
	}

	if err := uc.Run(); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}
}

func TestAddTargetKeepsWorkspaceFileWhenConfigWriteFails(t *testing.T) {
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
	writeConfigForTest(t, configPath, "version = 1\nstore_path = "+workspaceRootQuoted(storeRoot)+"\ndefault_ttl = \"24h\"\n\n[workspaces.myapp]\nroot = "+workspaceRootQuoted(resolvedWorkspaceRoot)+"\ntargets = []\n")

	targetPath := filepath.Join(workspaceRoot, ".env")
	const targetBody = "TOKEN=test\n"
	if err := os.WriteFile(targetPath, []byte(targetBody), 0o600); err != nil {
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

	uc := AddTarget{
		FileSystem: failingConfigWriteFS{
			homeDir:        tempHome,
			configPath:     configPath,
			configWriteErr: errors.New("config locked"),
		},
		TrackedChecker: stubTrackedChecker{},
		Stdout:         &bytes.Buffer{},
		TargetPath:     ".env",
	}

	err = uc.Run()
	if err == nil {
		t.Fatal("Run() returned nil error")
	}

	if !strings.Contains(err.Error(), "write config file") {
		t.Fatalf("error = %q", err)
	}

	workspaceData, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("ReadFile(workspace target) returned error: %v", err)
	}

	if string(workspaceData) != targetBody {
		t.Fatalf("workspace data = %q, want %q", string(workspaceData), targetBody)
	}

	storeTargetPath := filepath.Join(storeRoot, "workspaces", "myapp", ".env")
	if _, err := os.Stat(storeTargetPath); !os.IsNotExist(err) {
		t.Fatalf("store target exists after failure, stat error = %v", err)
	}
}

type stubTrackedChecker struct {
	tracked bool
	err     error
}

func (s stubTrackedChecker) IsTracked(workspaceRoot, relativePath string) (bool, error) {
	return s.tracked, s.err
}

func writeConfigForTest(t *testing.T, path, body string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}

	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}
}

func workspaceRootQuoted(path string) string {
	return `"` + path + `"`
}

type failingConfigWriteFS struct {
	homeDir        string
	configPath     string
	configWriteErr error
}

func (fs failingConfigWriteFS) UserHomeDir() (string, error) {
	return fs.homeDir, nil
}

func (fs failingConfigWriteFS) Getwd() (string, error) {
	return os.Getwd()
}

func (fs failingConfigWriteFS) EvalSymlinks(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}

func (fs failingConfigWriteFS) MkdirAll(path string, perm os.FileMode) error {
	return os.MkdirAll(path, perm)
}

func (fs failingConfigWriteFS) ReadFile(name string) ([]byte, error) {
	return os.ReadFile(name)
}

func (fs failingConfigWriteFS) WriteFile(name string, data []byte, perm os.FileMode) error {
	if name == fs.configPath {
		return fs.configWriteErr
	}

	return os.WriteFile(name, data, perm)
}

func (fs failingConfigWriteFS) Stat(name string) (os.FileInfo, error) {
	return os.Stat(name)
}

func (fs failingConfigWriteFS) Remove(name string) error {
	return os.Remove(name)
}
