package domain

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderTOMLDoesNotEmitParentWorkspacesTable(t *testing.T) {
	config := DefaultConfig()
	if err := config.AddWorkspace("veil", "/tmp/veil"); err != nil {
		t.Fatalf("AddWorkspace() returned error: %v", err)
	}

	data, err := config.RenderTOML()
	if err != nil {
		t.Fatalf("RenderTOML() returned error: %v", err)
	}

	rendered := string(data)
	if strings.Contains(rendered, "\n[workspaces]\n") {
		t.Fatalf("rendered = %q", rendered)
	}

	if !strings.Contains(rendered, "[workspaces.\"veil\"]") {
		t.Fatalf("rendered = %q", rendered)
	}
}

func TestRenderTOMLQuotesWorkspaceID(t *testing.T) {
	config := DefaultConfig()
	if err := config.AddWorkspace("my.app dev", "/tmp/workspace"); err != nil {
		t.Fatalf("AddWorkspace() returned error: %v", err)
	}

	data, err := config.RenderTOML()
	if err != nil {
		t.Fatalf("RenderTOML() returned error: %v", err)
	}

	rendered := string(data)
	if !strings.Contains(rendered, "[workspaces.\"my.app dev\"]") {
		t.Fatalf("rendered = %q", rendered)
	}

	parsed, err := ParseConfigTOML(data)
	if err != nil {
		t.Fatalf("ParseConfigTOML() returned error: %v", err)
	}

	if _, exists := parsed.Workspaces["my.app dev"]; !exists {
		t.Fatalf("workspaces = %#v", parsed.Workspaces)
	}
}

func TestWorkspaceAddTargetRejectsWorkspaceEscape(t *testing.T) {
	workspace := Workspace{}

	err := workspace.AddTarget("../.env")
	if err == nil {
		t.Fatal("AddTarget() returned nil error")
	}

	if !strings.Contains(err.Error(), "must stay within workspace") {
		t.Fatalf("error = %q", err)
	}
}

func TestWorkspaceAddTargetNormalizesAndSortsTargets(t *testing.T) {
	workspace := Workspace{
		Targets: []string{"z.env"},
	}

	if err := workspace.AddTarget("config/../.env"); err != nil {
		t.Fatalf("AddTarget() returned error: %v", err)
	}

	if got, want := strings.Join(workspace.Targets, ","), ".env,z.env"; got != want {
		t.Fatalf("targets = %q, want %q", got, want)
	}
}

func TestWorkspaceRemoveTargetRemovesNormalizedTarget(t *testing.T) {
	workspace := Workspace{
		Targets: []string{".env", "config/app.json"},
	}

	if err := workspace.RemoveTarget("config/../config/app.json"); err != nil {
		t.Fatalf("RemoveTarget() returned error: %v", err)
	}

	if got, want := strings.Join(workspace.Targets, ","), ".env"; got != want {
		t.Fatalf("targets = %q, want %q", got, want)
	}
}

func TestWorkspaceRemoveTargetReturnsErrorWhenTargetIsMissing(t *testing.T) {
	workspace := Workspace{
		Targets: []string{".env"},
	}

	err := workspace.RemoveTarget("config/app.json")
	if err == nil {
		t.Fatal("RemoveTarget() returned nil error")
	}

	if !strings.Contains(err.Error(), "target does not exist") {
		t.Fatalf("error = %q", err)
	}
}

func TestConfigRemoveWorkspaceRemovesExistingWorkspace(t *testing.T) {
	config := DefaultConfig()
	config.Workspaces["myapp"] = Workspace{Root: "/tmp/myapp"}
	config.Workspaces["other"] = Workspace{Root: "/tmp/other"}

	if err := config.RemoveWorkspace("myapp"); err != nil {
		t.Fatalf("RemoveWorkspace() returned error: %v", err)
	}

	if _, exists := config.Workspaces["myapp"]; exists {
		t.Fatalf("workspaces = %#v", config.Workspaces)
	}

	if _, exists := config.Workspaces["other"]; !exists {
		t.Fatalf("workspaces = %#v", config.Workspaces)
	}
}

func TestConfigRemoveWorkspaceReturnsErrorWhenWorkspaceIsMissing(t *testing.T) {
	config := DefaultConfig()

	err := config.RemoveWorkspace("missing")
	if err == nil {
		t.Fatal("RemoveWorkspace() returned nil error")
	}

	if !strings.Contains(err.Error(), "workspace does not exist") {
		t.Fatalf("error = %q", err)
	}
}

func TestConfigResolveWorkspaceByDirUsesDeepestRoot(t *testing.T) {
	config := DefaultConfig()
	config.Workspaces["root"] = Workspace{Root: "/tmp/app"}
	config.Workspaces["nested"] = Workspace{Root: "/tmp/app/services/api"}

	gotID, gotWorkspace, err := config.ResolveWorkspaceByDir("/tmp/app/services/api/internal")
	if err != nil {
		t.Fatalf("ResolveWorkspaceByDir() returned error: %v", err)
	}

	if gotID != "nested" {
		t.Fatalf("workspace id = %q, want %q", gotID, "nested")
	}

	if gotWorkspace.Root != "/tmp/app/services/api" {
		t.Fatalf("workspace root = %q", gotWorkspace.Root)
	}
}

func TestAddWorkspaceRejectsWorkspaceIDWithPathTraversal(t *testing.T) {
	config := DefaultConfig()

	err := config.AddWorkspace("../tmp", "/tmp/workspace")
	if err == nil {
		t.Fatal("AddWorkspace() returned nil error")
	}

	if !strings.Contains(err.Error(), "must not contain parent directory segments") {
		t.Fatalf("error = %q", err)
	}
}

func TestAddWorkspaceRejectsWorkspaceIDWithPathSeparator(t *testing.T) {
	config := DefaultConfig()

	err := config.AddWorkspace("team/api", "/tmp/workspace")
	if err == nil {
		t.Fatal("AddWorkspace() returned nil error")
	}

	if !strings.Contains(err.Error(), "must not contain path separators") {
		t.Fatalf("error = %q", err)
	}
}

func TestConfigStoreTargetPathBuildsWorkspaceScopedPath(t *testing.T) {
	config := DefaultConfig()
	config.StorePath = "/tmp/veil-store"

	got, err := config.StoreTargetPath("myapp", "config/service-account.json")
	if err != nil {
		t.Fatalf("StoreTargetPath() returned error: %v", err)
	}

	want := filepath.Join("/tmp/veil-store", "workspaces", "myapp", "config", "service-account.json")
	if got != want {
		t.Fatalf("store target path = %q, want %q", got, want)
	}
}

func TestConfigStoreTargetPathRejectsInvalidWorkspaceID(t *testing.T) {
	config := DefaultConfig()
	config.StorePath = "/tmp/veil-store"

	_, err := config.StoreTargetPath("../myapp", ".env")
	if err == nil {
		t.Fatal("StoreTargetPath() returned nil error")
	}

	if !strings.Contains(err.Error(), "workspace id") {
		t.Fatalf("error = %q", err)
	}
}
