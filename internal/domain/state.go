package domain

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	toml "github.com/pelletier/go-toml/v2"
)

const DefaultStateVersion = 1

type State struct {
	Version int     `toml:"version"`
	Leases  []Lease `toml:"leases"`
}

type Lease struct {
	WorkspaceID string    `toml:"workspace_id"`
	Target      string    `toml:"target"`
	MountedAt   time.Time `toml:"mounted_at"`
	ExpiresAt   time.Time `toml:"expires_at"`
}

func DefaultState() State {
	return State{
		Version: DefaultStateVersion,
		Leases:  []Lease{},
	}
}

func ParseStateTOML(data []byte) (State, error) {
	state := DefaultState()
	if err := toml.Unmarshal(data, &state); err != nil {
		return State{}, err
	}

	if state.Leases == nil {
		state.Leases = []Lease{}
	}

	return state, nil
}

func (s State) RenderTOML() ([]byte, error) {
	if s.Leases == nil {
		s.Leases = []Lease{}
	}

	sortedLeases := append([]Lease(nil), s.Leases...)
	sort.Slice(sortedLeases, func(i, j int) bool {
		if sortedLeases[i].WorkspaceID != sortedLeases[j].WorkspaceID {
			return sortedLeases[i].WorkspaceID < sortedLeases[j].WorkspaceID
		}
		return sortedLeases[i].Target < sortedLeases[j].Target
	})

	var builder strings.Builder
	fmt.Fprintf(&builder, "version = %d\n", s.Version)

	for _, lease := range sortedLeases {
		builder.WriteString("\n[[leases]]\n")
		fmt.Fprintf(&builder, "workspace_id = %s\n", strconv.Quote(lease.WorkspaceID))
		fmt.Fprintf(&builder, "target = %s\n", strconv.Quote(lease.Target))
		fmt.Fprintf(&builder, "mounted_at = %s\n", lease.MountedAt.UTC().Format(time.RFC3339Nano))
		fmt.Fprintf(&builder, "expires_at = %s\n", lease.ExpiresAt.UTC().Format(time.RFC3339Nano))
	}

	return []byte(builder.String()), nil
}

func (s *State) UpsertLease(workspaceID, target string, mountedAt, expiresAt time.Time) error {
	if err := validateWorkspaceID(workspaceID); err != nil {
		return err
	}

	normalizedTarget, err := normalizeTargetPath(target)
	if err != nil {
		return err
	}

	if !expiresAt.After(mountedAt) {
		return fmt.Errorf("lease expiration must be after mount time")
	}

	lease := Lease{
		WorkspaceID: workspaceID,
		Target:      normalizedTarget,
		MountedAt:   mountedAt.UTC(),
		ExpiresAt:   expiresAt.UTC(),
	}

	for idx, existing := range s.Leases {
		if existing.WorkspaceID == workspaceID && existing.Target == normalizedTarget {
			s.Leases[idx] = lease
			return nil
		}
	}

	s.Leases = append(s.Leases, lease)
	return nil
}

func (s *State) RemoveLease(workspaceID, target string) error {
	if err := validateWorkspaceID(workspaceID); err != nil {
		return err
	}

	normalizedTarget, err := normalizeTargetPath(target)
	if err != nil {
		return err
	}

	for idx, lease := range s.Leases {
		if lease.WorkspaceID != workspaceID || lease.Target != normalizedTarget {
			continue
		}

		s.Leases = append(s.Leases[:idx], s.Leases[idx+1:]...)
		return nil
	}

	return nil
}

func (s *State) RemoveWorkspaceLeases(workspaceID string) error {
	if err := validateWorkspaceID(workspaceID); err != nil {
		return err
	}

	filtered := s.Leases[:0]
	for _, lease := range s.Leases {
		if lease.WorkspaceID == workspaceID {
			continue
		}
		filtered = append(filtered, lease)
	}
	s.Leases = filtered
	return nil
}

func (s State) FindLease(workspaceID, target string) (Lease, bool, error) {
	if err := validateWorkspaceID(workspaceID); err != nil {
		return Lease{}, false, err
	}

	normalizedTarget, err := normalizeTargetPath(target)
	if err != nil {
		return Lease{}, false, err
	}

	for _, lease := range s.Leases {
		if lease.WorkspaceID == workspaceID && lease.Target == normalizedTarget {
			return lease, true, nil
		}
	}

	return Lease{}, false, nil
}

func (c Config) EffectiveTTL(workspace Workspace) (time.Duration, error) {
	ttlText := c.DefaultTTL
	if workspace.TTL != "" {
		ttlText = workspace.TTL
	}

	duration, err := time.ParseDuration(ttlText)
	if err != nil {
		return 0, fmt.Errorf("parse ttl: %w", err)
	}

	if duration <= 0 {
		return 0, fmt.Errorf("ttl must be greater than zero")
	}

	return duration, nil
}
