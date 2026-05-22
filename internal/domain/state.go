package domain

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	toml "github.com/pelletier/go-toml/v2"
)

const DefaultStateVersion = 2

type State struct {
	Version int     `toml:"version"`
	Leases  []Lease `toml:"leases"`
}

type Lease struct {
	WorkspaceID   string    `toml:"workspace_id"`
	Target        string    `toml:"target"`
	MountedAt     time.Time `toml:"mounted_at"`
	ExpiresAt     time.Time `toml:"expires_at"`
	StoreID       string    `toml:"store_id,omitempty"`
	WorkspacePath string    `toml:"workspace_path,omitempty"`
	StorePath     string    `toml:"store_path,omitempty"`
	PlaintextHash string    `toml:"plaintext_sha256,omitempty"`
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
		if lease.StoreID != "" {
			fmt.Fprintf(&builder, "store_id = %s\n", strconv.Quote(lease.StoreID))
		}
		if lease.WorkspacePath != "" {
			fmt.Fprintf(&builder, "workspace_path = %s\n", strconv.Quote(lease.WorkspacePath))
		}
		if lease.StorePath != "" {
			fmt.Fprintf(&builder, "store_path = %s\n", strconv.Quote(lease.StorePath))
		}
		if lease.PlaintextHash != "" {
			fmt.Fprintf(&builder, "plaintext_sha256 = %s\n", strconv.Quote(lease.PlaintextHash))
		}
	}

	return []byte(builder.String()), nil
}

func (s *State) UpsertLease(workspaceID, target string, mountedAt, expiresAt time.Time) error {
	return s.UpsertLeaseForStore(workspaceID, target, mountedAt, expiresAt, DefaultStoreID, "", "")
}

func (s *State) UpsertLeaseForStore(workspaceID, target string, mountedAt, expiresAt time.Time, storeID, workspacePath, storePath string) error {
	return s.UpsertLeaseWithHash(workspaceID, target, mountedAt, expiresAt, storeID, workspacePath, storePath, "")
}

func (s *State) UpsertLeaseWithHash(workspaceID, target string, mountedAt, expiresAt time.Time, storeID, workspacePath, storePath, plaintextHash string) error {
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
	if storeID == "" {
		storeID = DefaultStoreID
	}

	lease := Lease{
		WorkspaceID:   workspaceID,
		Target:        normalizedTarget,
		MountedAt:     mountedAt.UTC(),
		ExpiresAt:     expiresAt.UTC(),
		StoreID:       storeID,
		WorkspacePath: workspacePath,
		StorePath:     storePath,
		PlaintextHash: plaintextHash,
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

func (s State) HasActiveLeaseForStore(storeID string, now time.Time) bool {
	if storeID == "" {
		storeID = DefaultStoreID
	}
	for _, lease := range s.Leases {
		leaseStoreID := lease.StoreID
		if leaseStoreID == "" {
			leaseStoreID = DefaultStoreID
		}
		if leaseStoreID == storeID && lease.ExpiresAt.After(now) {
			return true
		}
	}
	return false
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
