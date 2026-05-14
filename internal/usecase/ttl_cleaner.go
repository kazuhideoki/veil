package usecase

import (
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/kazuhideoki/veil/internal/domain"
)

type ttlCleanerFileSystem interface {
	removeFileSystem
}

type RunTTLCleaner struct {
	FileSystem     ttlCleanerFileSystem
	StoreRuntime   EncryptedStoreRuntime
	Stdout         io.Writer
	Now            func() time.Time
	Force          bool
	ForceAvailable bool
}

func (u RunTTLCleaner) Run() error {
	return u.cleanupExpiredLeases()
}

func (u RunTTLCleaner) cleanupExpiredLeases() error {
	_, config, err := loadConfig(u.FileSystem)
	if err != nil {
		return err
	}

	homeDir, err := u.FileSystem.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}

	config = expandConfigPaths(config, homeDir)
	config = canonicalizeWorkspaceRoots(config, u.FileSystem)

	statePath, state, err := loadState(u.FileSystem)
	if err != nil {
		return err
	}

	now := currentTime(u.Now)
	needsStoreMount := false
	for _, lease := range state.Leases {
		if lease.ExpiresAt.After(now) {
			continue
		}
		workspace, ok := config.Workspaces[lease.WorkspaceID]
		if ok && hasTarget(workspace.Targets, lease.Target) {
			needsStoreMount = true
			break
		}
	}
	if needsStoreMount {
		if err := ensureStoreAvailable(u.StoreRuntime, config, now, u.Stdout, u.Force, u.ForceAvailable); err != nil {
			return err
		}
	}

	for _, lease := range append([]domainLease(nil), convertLeases(state.Leases)...) {
		if lease.expiresAt.After(now) {
			continue
		}

		workspace, ok := config.Workspaces[lease.workspaceID]
		if !ok || !hasTarget(workspace.Targets, lease.target) {
			if err := state.RemoveLease(lease.workspaceID, lease.target); err != nil {
				return err
			}
			continue
		}

		workspaceTargetPath := filepath.Join(workspace.Root, lease.target)
		storeTargetPath, err := config.StoreTargetPath(lease.workspaceID, lease.target)
		if err != nil {
			return err
		}

		status, err := vanishTarget(u.FileSystem, workspaceTargetPath, storeTargetPath)
		if err != nil {
			return fmt.Errorf("%s/%s: %w", lease.workspaceID, lease.target, err)
		}

		if err := state.RemoveLease(lease.workspaceID, lease.target); err != nil {
			return err
		}

		if u.Stdout != nil {
			fmt.Fprintf(u.Stdout, "expired %s target: %s/%s\n", status, lease.workspaceID, lease.target)
		}
	}

	if err := persistState(u.FileSystem, statePath, state); err != nil {
		return err
	}

	if needsStoreMount {
		if err := unmountStoreIfIdle(u.StoreRuntime, config, state, now, u.Stdout); err != nil {
			return err
		}
	}

	return nil
}

type domainLease struct {
	workspaceID string
	target      string
	expiresAt   time.Time
}

func convertLeases(leases []domain.Lease) []domainLease {
	converted := make([]domainLease, 0, len(leases))
	for _, lease := range leases {
		converted = append(converted, domainLease{
			workspaceID: lease.WorkspaceID,
			target:      lease.Target,
			expiresAt:   lease.ExpiresAt,
		})
	}

	return converted
}
