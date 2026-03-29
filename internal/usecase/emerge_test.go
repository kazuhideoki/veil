package usecase

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
