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
