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

type repoResolverFileSystem struct{}

func (repoResolverFileSystem) Getwd() (string, error) {
	return "", errors.New("cwd should not be inspected")
}

func (repoResolverFileSystem) EvalSymlinks(string) (string, error) {
	return "", errors.New("cwd should not be canonicalized")
}

func TestResolveEmergeWorkspacesSelectsRepoWithoutCurrentDirectory(t *testing.T) {
	config := domain.DefaultConfig()
	config.Workspaces["myapp"] = domain.Workspace{Root: "/tmp/myapp", Targets: []string{".env"}}

	workspaces, err := resolveEmergeWorkspaces(repoResolverFileSystem{}, config, false, "myapp")
	if err != nil {
		t.Fatalf("resolveEmergeWorkspaces() returned error: %v", err)
	}
	if len(workspaces) != 1 || workspaces[0].id != "myapp" || workspaces[0].workspace.Root != "/tmp/myapp" {
		t.Fatalf("workspaces = %#v", workspaces)
	}
}

func TestResolveEmergeWorkspacesRejectsAllWithRepo(t *testing.T) {
	_, err := resolveEmergeWorkspaces(repoResolverFileSystem{}, domain.DefaultConfig(), true, "myapp")
	if err == nil || !strings.Contains(err.Error(), "--all and --repo") {
		t.Fatalf("error = %v, want --all and --repo conflict", err)
	}
}

func TestResolveEmergeWorkspacesRejectsUnknownRepo(t *testing.T) {
	_, err := resolveEmergeWorkspaces(repoResolverFileSystem{}, domain.DefaultConfig(), false, "missing")
	if err == nil || !strings.Contains(err.Error(), "repo is not registered: missing") {
		t.Fatalf("error = %v, want unknown repo error", err)
	}
}

func TestEmergeAndVanishTargetsSelectRepoOutsideWorkspace(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	firstRoot := filepath.Join(tempHome, "first")
	secondRoot := filepath.Join(tempHome, "second")
	for _, root := range []string{firstRoot, secondRoot} {
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatalf("MkdirAll(%s) returned error: %v", root, err)
		}
	}
	writeConfigForTest(t, filepath.Join(tempHome, ".veil", "config.toml"), `version = 2
default_ttl = "24h"

[store]
backend = "1password_document"
vault = "Private"

[[documents]]
workspace_id = "first"
target = ".env"
item_id = "first-item"

[[documents]]
workspace_id = "second"
target = ".env"
item_id = "second-item"

[workspaces.first]
root = "`+firstRoot+`"
targets = [".env"]

[workspaces.second]
root = "`+secondRoot+`"
targets = [".env"]
`)
	restoreWD := chdirForTest(t, tempHome)
	defer restoreWD()

	runtime := newFakeOnePasswordRuntime()
	runtime.documents["first-item"] = []byte("FIRST=true\n")
	runtime.documents["second-item"] = []byte("SECOND=true\n")
	now := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	if err := (EmergeTargets{
		FileSystem: infra.OSFileSystem{}, DocumentRuntime: runtime, Stdout: &bytes.Buffer{}, Now: func() time.Time { return now }, Repo: "second",
	}).Run(); err != nil {
		t.Fatalf("EmergeTargets.Run() returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(firstRoot, ".env")); !os.IsNotExist(err) {
		t.Fatalf("first repo target was unexpectedly emerged: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(secondRoot, ".env")); err != nil || string(data) != "SECOND=true\n" {
		t.Fatalf("second repo target = %q, %v", data, err)
	}

	if err := (VanishTargets{
		FileSystem: infra.OSFileSystem{}, DocumentRuntime: runtime, Stdout: &bytes.Buffer{}, Now: func() time.Time { return now }, Repo: "second",
	}).Run(); err != nil {
		t.Fatalf("VanishTargets.Run() returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(secondRoot, ".env")); !os.IsNotExist(err) {
		t.Fatalf("second repo target was not vanished: %v", err)
	}
}
