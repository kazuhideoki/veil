package domain

import (
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
