package domain

import (
	"strings"
	"testing"
	"time"
)

func TestConfigEffectiveTTLUsesDefaultTTL(t *testing.T) {
	config := DefaultConfig()

	got, err := config.EffectiveTTL(Workspace{})
	if err != nil {
		t.Fatalf("EffectiveTTL() returned error: %v", err)
	}

	if got != 24*time.Hour {
		t.Fatalf("ttl = %v, want %v", got, 24*time.Hour)
	}
}

func TestConfigEffectiveTTLRejectsNonPositiveTTL(t *testing.T) {
	config := DefaultConfig()
	config.DefaultTTL = "0s"

	_, err := config.EffectiveTTL(Workspace{})
	if err == nil {
		t.Fatal("EffectiveTTL() returned nil error")
	}

	if !strings.Contains(err.Error(), "greater than zero") {
		t.Fatalf("error = %q", err)
	}
}

func TestConfigEffectiveTTLPrefersWorkspaceTTL(t *testing.T) {
	config := DefaultConfig()

	got, err := config.EffectiveTTL(Workspace{TTL: "90m"})
	if err != nil {
		t.Fatalf("EffectiveTTL() returned error: %v", err)
	}

	if got != 90*time.Minute {
		t.Fatalf("ttl = %v, want %v", got, 90*time.Minute)
	}
}

func TestStateRenderTOMLSortsLeases(t *testing.T) {
	state := DefaultState()
	mountedAt := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)

	state.Leases = []Lease{
		{WorkspaceID: "zeta", Target: "b.env", MountedAt: mountedAt, ExpiresAt: mountedAt.Add(time.Hour)},
		{WorkspaceID: "alpha", Target: "z.env", MountedAt: mountedAt, ExpiresAt: mountedAt.Add(2 * time.Hour)},
		{WorkspaceID: "alpha", Target: "a.env", MountedAt: mountedAt, ExpiresAt: mountedAt.Add(3 * time.Hour)},
	}

	data, err := state.RenderTOML()
	if err != nil {
		t.Fatalf("RenderTOML() returned error: %v", err)
	}

	rendered := string(data)
	first := strings.Index(rendered, "workspace_id = \"alpha\"\ntarget = \"a.env\"")
	second := strings.Index(rendered, "workspace_id = \"alpha\"\ntarget = \"z.env\"")
	third := strings.Index(rendered, "workspace_id = \"zeta\"\ntarget = \"b.env\"")
	if !(first >= 0 && second > first && third > second) {
		t.Fatalf("rendered = %q", rendered)
	}
}

func TestParseStateTOMLRoundTripsRenderedState(t *testing.T) {
	state := DefaultState()
	mountedAt := time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC)
	if err := state.UpsertLease("myapp", ".env", mountedAt, mountedAt.Add(24*time.Hour)); err != nil {
		t.Fatalf("UpsertLease() returned error: %v", err)
	}

	data, err := state.RenderTOML()
	if err != nil {
		t.Fatalf("RenderTOML() returned error: %v", err)
	}

	parsed, err := ParseStateTOML(data)
	if err != nil {
		t.Fatalf("ParseStateTOML() returned error: %v", err)
	}

	lease, ok, err := parsed.FindLease("myapp", ".env")
	if err != nil {
		t.Fatalf("FindLease() returned error: %v", err)
	}
	if !ok {
		t.Fatal("lease not found")
	}

	if !lease.ExpiresAt.Equal(mountedAt.Add(24 * time.Hour)) {
		t.Fatalf("expires_at = %v", lease.ExpiresAt)
	}
}

func TestStateUpsertLeaseReplacesExistingLease(t *testing.T) {
	state := DefaultState()
	mountedAt := time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC)

	if err := state.UpsertLease("myapp", ".env", mountedAt, mountedAt.Add(time.Hour)); err != nil {
		t.Fatalf("UpsertLease() returned error: %v", err)
	}
	if err := state.UpsertLease("myapp", ".env", mountedAt.Add(time.Minute), mountedAt.Add(2*time.Hour)); err != nil {
		t.Fatalf("UpsertLease() returned error: %v", err)
	}

	if got := len(state.Leases); got != 1 {
		t.Fatalf("lease count = %d, want 1", got)
	}

	if !state.Leases[0].ExpiresAt.Equal(mountedAt.Add(2 * time.Hour)) {
		t.Fatalf("expires_at = %v", state.Leases[0].ExpiresAt)
	}
}

func TestStateRemoveWorkspaceLeasesRemovesMatchingWorkspace(t *testing.T) {
	state := DefaultState()
	mountedAt := time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC)
	for _, lease := range []struct {
		workspaceID string
		target      string
	}{
		{workspaceID: "myapp", target: ".env"},
		{workspaceID: "myapp", target: "config/app.json"},
		{workspaceID: "other", target: ".env"},
	} {
		if err := state.UpsertLease(lease.workspaceID, lease.target, mountedAt, mountedAt.Add(time.Hour)); err != nil {
			t.Fatalf("UpsertLease() returned error: %v", err)
		}
	}

	if err := state.RemoveWorkspaceLeases("myapp"); err != nil {
		t.Fatalf("RemoveWorkspaceLeases() returned error: %v", err)
	}

	if got := len(state.Leases); got != 1 {
		t.Fatalf("lease count = %d, want 1", got)
	}
	if state.Leases[0].WorkspaceID != "other" {
		t.Fatalf("leases = %#v", state.Leases)
	}
}
