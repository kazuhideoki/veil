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

func TestRemoveWorkspaceRestoresTargetsAndDeletesWorkspaceConfig(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	storeRoot := filepath.Join(tempHome, "veil-store")
	workspaceRoot := filepath.Join(tempHome, "myapp")
	otherWorkspaceRoot := filepath.Join(tempHome, "other")
	if err := os.MkdirAll(filepath.Join(workspaceRoot, "config"), 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	if err := os.MkdirAll(otherWorkspaceRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}

	resolvedWorkspaceRoot, err := filepath.EvalSymlinks(workspaceRoot)
	if err != nil {
		t.Fatalf("EvalSymlinks() returned error: %v", err)
	}
	resolvedOtherWorkspaceRoot, err := filepath.EvalSymlinks(otherWorkspaceRoot)
	if err != nil {
		t.Fatalf("EvalSymlinks() returned error: %v", err)
	}

	configPath := filepath.Join(tempHome, ".veil", "config.toml")
	writeConfigForTest(t, configPath, "version = 1\nstore_path = "+workspaceRootQuoted(storeRoot)+"\ndefault_ttl = \"24h\"\n\n[workspaces.myapp]\nroot = "+workspaceRootQuoted(resolvedWorkspaceRoot)+"\ntargets = [\".env\", \"config/service-account.json\"]\n\n[workspaces.other]\nroot = "+workspaceRootQuoted(resolvedOtherWorkspaceRoot)+"\ntargets = []\n")

	envStorePath := filepath.Join(storeRoot, "workspaces", "myapp", ".env")
	jsonStorePath := filepath.Join(storeRoot, "workspaces", "myapp", "config", "service-account.json")
	if err := os.MkdirAll(filepath.Dir(jsonStorePath), 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	if err := os.WriteFile(envStorePath, []byte("TOKEN=secret\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}
	if err := os.WriteFile(jsonStorePath, []byte("{\"key\":\"value\"}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}
	if err := os.Symlink(envStorePath, filepath.Join(workspaceRoot, ".env")); err != nil {
		t.Fatalf("Symlink() returned error: %v", err)
	}
	if err := os.Symlink(jsonStorePath, filepath.Join(workspaceRoot, "config", "service-account.json")); err != nil {
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
	uc := RemoveWorkspace{
		FileSystem: infra.OSFileSystem{},
		Stdout:     &stdout,
	}

	if err := uc.Run(); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	for path, want := range map[string]string{
		filepath.Join(workspaceRoot, ".env"):                           "TOKEN=secret\n",
		filepath.Join(workspaceRoot, "config", "service-account.json"): "{\"key\":\"value\"}\n",
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%s) returned error: %v", path, err)
		}
		if string(data) != want {
			t.Fatalf("%s data = %q, want %q", path, string(data), want)
		}
	}

	for _, path := range []string{envStorePath, jsonStorePath} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("store target still exists at %s, err = %v", path, err)
		}
	}

	configData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(config) returned error: %v", err)
	}
	if strings.Contains(string(configData), "[workspaces.myapp]") {
		t.Fatalf("config contents = %q", string(configData))
	}
	if !strings.Contains(string(configData), "[workspaces.\"other\"]") {
		t.Fatalf("config contents = %q", string(configData))
	}

	for _, want := range []string{"writing config", "removed workspace: myapp"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, want substring %q", stdout.String(), want)
		}
	}
}

func TestRemoveWorkspaceRollsBackPreparedTargetsWhenConfigWriteFails(t *testing.T) {
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
	writeConfigForTest(t, configPath, "version = 1\nstore_path = "+workspaceRootQuoted(storeRoot)+"\ndefault_ttl = \"24h\"\n\n[workspaces.myapp]\nroot = "+workspaceRootQuoted(resolvedWorkspaceRoot)+"\ntargets = [\".env\", \"config/service-account.json\"]\n")

	envStorePath := filepath.Join(storeRoot, "workspaces", "myapp", ".env")
	jsonStorePath := filepath.Join(storeRoot, "workspaces", "myapp", "config", "service-account.json")
	if err := os.MkdirAll(filepath.Dir(jsonStorePath), 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	if err := os.WriteFile(envStorePath, []byte("TOKEN=secret\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}
	if err := os.WriteFile(jsonStorePath, []byte("{\"key\":\"value\"}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}

	workspaceEnvPath := filepath.Join(workspaceRoot, ".env")
	workspaceJSONPath := filepath.Join(workspaceRoot, "config", "service-account.json")
	if err := os.Symlink(envStorePath, workspaceEnvPath); err != nil {
		t.Fatalf("Symlink() returned error: %v", err)
	}
	if err := os.Symlink(jsonStorePath, workspaceJSONPath); err != nil {
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

	uc := RemoveWorkspace{
		FileSystem: failingConfigWriteFS{
			homeDir:        tempHome,
			configPath:     configPath,
			configWriteErr: errors.New("config locked"),
		},
		Stdout: &bytes.Buffer{},
	}

	err = uc.Run()
	if err == nil {
		t.Fatal("Run() returned nil error")
	}
	if !strings.Contains(err.Error(), "write config file") {
		t.Fatalf("error = %q", err)
	}

	for linkPath, want := range map[string]string{
		workspaceEnvPath:  envStorePath,
		workspaceJSONPath: jsonStorePath,
	} {
		got, err := os.Readlink(linkPath)
		if err != nil {
			t.Fatalf("Readlink(%s) returned error: %v", linkPath, err)
		}
		if got != want {
			t.Fatalf("link target = %q, want %q", got, want)
		}
	}

	configData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(config) returned error: %v", err)
	}
	if !strings.Contains(string(configData), "[workspaces.myapp]") || !strings.Contains(string(configData), "\".env\"") {
		t.Fatalf("config contents = %q", string(configData))
	}
}

func TestPurgeWorkspaceRequiresYesForNonInteractiveInput(t *testing.T) {
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

	uc := PurgeWorkspace{
		FileSystem:  infra.OSFileSystem{},
		Stdin:       bytes.NewBuffer(nil),
		Stdout:      &bytes.Buffer{},
		Interactive: false,
	}

	err = uc.Run()
	if err == nil {
		t.Fatal("Run() returned nil error")
	}
	if !strings.Contains(err.Error(), "requires --yes when stdin is not a terminal") {
		t.Fatalf("error = %q", err)
	}
}

func TestPurgeWorkspacePurgesTargetsAfterInteractiveConfirmation(t *testing.T) {
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
	writeConfigForTest(t, configPath, "version = 1\nstore_path = "+workspaceRootQuoted(storeRoot)+"\ndefault_ttl = \"24h\"\n\n[workspaces.myapp]\nroot = "+workspaceRootQuoted(resolvedWorkspaceRoot)+"\ntargets = [\".env\", \"config/local.json\"]\n")

	envStorePath := filepath.Join(storeRoot, "workspaces", "myapp", ".env")
	jsonStorePath := filepath.Join(storeRoot, "workspaces", "myapp", "config", "local.json")
	if err := os.MkdirAll(filepath.Dir(jsonStorePath), 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	if err := os.WriteFile(envStorePath, []byte("TOKEN=secret\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}
	if err := os.WriteFile(jsonStorePath, []byte("{\"remote\":true}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}

	workspaceEnvPath := filepath.Join(workspaceRoot, ".env")
	workspaceJSONPath := filepath.Join(workspaceRoot, "config", "local.json")
	if err := os.Symlink(envStorePath, workspaceEnvPath); err != nil {
		t.Fatalf("Symlink() returned error: %v", err)
	}
	if err := os.WriteFile(workspaceJSONPath, []byte("{\"local\":true}\n"), 0o600); err != nil {
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
	uc := PurgeWorkspace{
		FileSystem:  infra.OSFileSystem{},
		Stdin:       bytes.NewBufferString("y\n"),
		Stdout:      &stdout,
		Interactive: true,
	}

	if err := uc.Run(); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	if _, err := os.Lstat(workspaceEnvPath); !os.IsNotExist(err) {
		t.Fatalf("workspace env symlink still exists, err = %v", err)
	}

	jsonData, err := os.ReadFile(workspaceJSONPath)
	if err != nil {
		t.Fatalf("ReadFile(local json) returned error: %v", err)
	}
	if string(jsonData) != "{\"local\":true}\n" {
		t.Fatalf("local json = %q", string(jsonData))
	}

	for _, path := range []string{envStorePath, jsonStorePath} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("store target still exists at %s, err = %v", path, err)
		}
	}

	configData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(config) returned error: %v", err)
	}
	if strings.Contains(string(configData), "[workspaces.myapp]") {
		t.Fatalf("config contents = %q", string(configData))
	}

	for _, want := range []string{"[y/N]: ", "writing config", "purged workspace: myapp"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, want substring %q", stdout.String(), want)
		}
	}
}

func TestPurgeWorkspaceKeepsTargetsWhenConfigWriteFails(t *testing.T) {
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

	uc := PurgeWorkspace{
		FileSystem: failingConfigWriteFS{
			homeDir:        tempHome,
			configPath:     configPath,
			configWriteErr: errors.New("config locked"),
		},
		Stdin:       bytes.NewBuffer(nil),
		Stdout:      &bytes.Buffer{},
		Interactive: false,
		AssumeYes:   true,
	}

	err = uc.Run()
	if err == nil {
		t.Fatal("Run() returned nil error")
	}
	if !strings.Contains(err.Error(), "write config file") {
		t.Fatalf("error = %q", err)
	}

	got, err := os.Readlink(workspaceTargetPath)
	if err != nil {
		t.Fatalf("Readlink() returned error: %v", err)
	}
	if got != storeTargetPath {
		t.Fatalf("link target = %q, want %q", got, storeTargetPath)
	}

	if _, err := os.Stat(storeTargetPath); err != nil {
		t.Fatalf("Stat(store target) returned error: %v", err)
	}

	configData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(config) returned error: %v", err)
	}
	if !strings.Contains(string(configData), "[workspaces.myapp]") {
		t.Fatalf("config contents = %q", string(configData))
	}
}
