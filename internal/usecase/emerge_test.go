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

type targetRefResolverFileSystem struct{}

func (targetRefResolverFileSystem) Getwd() (string, error) {
	return "", errors.New("cwd should not be inspected")
}

func (targetRefResolverFileSystem) EvalSymlinks(string) (string, error) {
	return "", errors.New("cwd should not be canonicalized")
}

func TestResolveEmergeWorkspacesSelectsTargetRefWithoutCurrentDirectory(t *testing.T) {
	config := domain.DefaultConfig()
	config.Workspaces["myapp"] = domain.Workspace{Root: "/tmp/myapp", Targets: []string{".env", "config/app.json"}}

	workspaces, err := resolveEmergeWorkspaces(targetRefResolverFileSystem{}, config, false, "myapp:.env")
	if err != nil {
		t.Fatalf("resolveEmergeWorkspaces() returned error: %v", err)
	}
	if len(workspaces) != 1 || workspaces[0].id != "myapp" || workspaces[0].workspace.Root != "/tmp/myapp" {
		t.Fatalf("workspaces = %#v", workspaces)
	}
	if got := workspaces[0].workspace.Targets; len(got) != 1 || got[0] != ".env" {
		t.Fatalf("targets = %q, want [.env]", got)
	}
}

func TestResolveEmergeWorkspacesRejectsAllWithTargetRef(t *testing.T) {
	_, err := resolveEmergeWorkspaces(targetRefResolverFileSystem{}, domain.DefaultConfig(), true, "myapp:.env")
	if err == nil || !strings.Contains(err.Error(), "--all and a target ref") {
		t.Fatalf("error = %v, want --all and target ref conflict", err)
	}
}

func TestResolveEmergeWorkspacesRejectsUnknownWorkspaceInTargetRef(t *testing.T) {
	_, err := resolveEmergeWorkspaces(targetRefResolverFileSystem{}, domain.DefaultConfig(), false, "missing:.env")
	if err == nil || !strings.Contains(err.Error(), "workspace is not registered: missing") {
		t.Fatalf("error = %v, want unknown workspace error", err)
	}
}

func TestResolveEmergeWorkspacesRejectsMalformedTargetRef(t *testing.T) {
	for _, ref := range []string{":.env", "myapp:"} {
		_, err := resolveEmergeWorkspaces(targetRefResolverFileSystem{}, domain.DefaultConfig(), false, ref)
		if err == nil || !strings.Contains(err.Error(), "expected workspace_id:target") {
			t.Fatalf("resolveEmergeWorkspaces(%q) error = %v", ref, err)
		}
	}
}

func TestResolveEmergeWorkspacesRejectsUnknownTargetInTargetRef(t *testing.T) {
	config := domain.DefaultConfig()
	config.Workspaces["myapp"] = domain.Workspace{Root: "/tmp/myapp", Targets: []string{".env"}}

	_, err := resolveEmergeWorkspaces(targetRefResolverFileSystem{}, config, false, "myapp:missing.env")
	if err == nil || !strings.Contains(err.Error(), "target is not registered: myapp:missing.env") {
		t.Fatalf("error = %v, want unknown target error", err)
	}
}

func TestEmergeAndVanishSelectOneTargetRefOutsideWorkspace(t *testing.T) {
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

[[documents]]
workspace_id = "second"
target = "config/app.json"
item_id = "second-app-item"

[workspaces.first]
root = "`+firstRoot+`"
targets = [".env"]

[workspaces.second]
root = "`+secondRoot+`"
targets = [".env", "config/app.json"]
`)
	restoreWD := chdirForTest(t, tempHome)
	defer restoreWD()

	runtime := newFakeOnePasswordRuntime()
	runtime.documents["first-item"] = []byte("FIRST=true\n")
	runtime.documents["second-item"] = []byte("SECOND=true\n")
	runtime.documents["second-app-item"] = []byte("APP=true\n")
	now := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	if err := (EmergeTargets{
		FileSystem: infra.OSFileSystem{}, DocumentRuntime: runtime, Stdout: &bytes.Buffer{}, Now: func() time.Time { return now }, TargetRef: "second:.env",
	}).Run(); err != nil {
		t.Fatalf("EmergeTargets.Run() returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(firstRoot, ".env")); !os.IsNotExist(err) {
		t.Fatalf("first repo target was unexpectedly emerged: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(secondRoot, ".env")); err != nil || string(data) != "SECOND=true\n" {
		t.Fatalf("second repo target = %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(secondRoot, "config", "app.json")); !os.IsNotExist(err) {
		t.Fatalf("unselected second target was unexpectedly emerged: %v", err)
	}
	if err := (EmergeTargets{
		FileSystem: infra.OSFileSystem{}, DocumentRuntime: runtime, Stdout: &bytes.Buffer{}, Now: func() time.Time { return now }, TargetRef: "second:config/app.json",
	}).Run(); err != nil {
		t.Fatalf("EmergeTargets.Run(second:config/app.json) returned error: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(secondRoot, "config", "app.json")); err != nil || string(data) != "APP=true\n" {
		t.Fatalf("second app target = %q, %v", data, err)
	}

	if err := (VanishTargets{
		FileSystem: infra.OSFileSystem{}, DocumentRuntime: runtime, Stdout: &bytes.Buffer{}, Now: func() time.Time { return now }, TargetRef: "second:.env",
	}).Run(); err != nil {
		t.Fatalf("VanishTargets.Run() returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(secondRoot, ".env")); !os.IsNotExist(err) {
		t.Fatalf("second repo target was not vanished: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(secondRoot, "config", "app.json")); err != nil || string(data) != "APP=true\n" {
		t.Fatalf("unselected second target was changed by vanish: %q, %v", data, err)
	}
}

func TestVanishTargetRefRejectsUnboundOrStaleLease(t *testing.T) {
	for _, tc := range []struct {
		name          string
		workspacePath func(string) string
		storePath     string
		want          string
	}{
		{name: "unbound", workspacePath: func(string) string { return "" }, storePath: "", want: "not bound to the selected target"},
		{name: "workspace path mismatch", workspacePath: func(string) string { return "/tmp/stale/.env" }, storePath: "item-1", want: "workspace path does not match active lease"},
		{name: "store path mismatch", workspacePath: func(path string) string { return path }, storePath: "other-item", want: "does not match registered 1Password document"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tempHome := t.TempDir()
			t.Setenv("HOME", tempHome)
			workspaceRoot := prepareOnePasswordWorkspace(t, tempHome, `targets = [".env"]`)
			appendDocumentConfig(t, tempHome, ".env", "item-1", sha256Hex([]byte("TOKEN=test\n")))
			targetPath := filepath.Join(workspaceRoot, ".env")
			if err := os.WriteFile(targetPath, []byte("TOKEN=test\n"), 0o600); err != nil {
				t.Fatalf("WriteFile() returned error: %v", err)
			}
			resolvedTargetPath, err := filepath.EvalSymlinks(targetPath)
			if err != nil {
				t.Fatalf("EvalSymlinks() returned error: %v", err)
			}
			now := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
			state := domain.DefaultState()
			if err := state.UpsertLeaseWithHash("myapp", ".env", now.Add(-time.Hour), now.Add(time.Hour), onePasswordStoreID, tc.workspacePath(resolvedTargetPath), tc.storePath, sha256Hex([]byte("TOKEN=test\n"))); err != nil {
				t.Fatalf("UpsertLeaseWithHash() returned error: %v", err)
			}
			writeStateForTest(t, filepath.Join(tempHome, ".veil", "state.toml"), state)
			restoreWD := chdirForTest(t, tempHome)
			defer restoreWD()

			err = (VanishTargets{
				FileSystem: infra.OSFileSystem{}, Stdout: &bytes.Buffer{}, Now: func() time.Time { return now }, TargetRef: "myapp:.env", Discard: true,
			}).Run()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("VanishTargets.Run() error = %v, want %q", err, tc.want)
			}
			if data, err := os.ReadFile(targetPath); err != nil || string(data) != "TOKEN=test\n" {
				t.Fatalf("target changed after rejected vanish: %q, %v", data, err)
			}
		})
	}
}
