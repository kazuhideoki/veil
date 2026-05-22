package usecase

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/kazuhideoki/veil/internal/domain"
)

type ttlCleanerFileSystem interface {
	removeFileSystem
}

type RunTTLCleaner struct {
	FileSystem ttlCleanerFileSystem
	Stdout     io.Writer
	Now        func() time.Time
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
	if err := requireOnePasswordConfig(config); err != nil {
		return err
	}

	statePath, state, err := loadState(u.FileSystem)
	if err != nil {
		return err
	}

	now := currentTime(u.Now)
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
		status, err := cleanupExpiredOnePasswordTarget(u.FileSystem, workspaceTargetPath, lease)
		if err != nil {
			return fmt.Errorf("%s/%s: %w", lease.workspaceID, lease.target, err)
		}
		if status == "modified" {
			if u.Stdout != nil {
				fmt.Fprintf(u.Stdout, "expired modified target: %s/%s  note: uncommitted changes kept\n", lease.workspaceID, lease.target)
			}
			continue
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

	return nil
}

func cleanupExpiredOnePasswordTarget(fs ttlCleanerFileSystem, workspaceTargetPath string, lease domainLease) (string, error) {
	info, err := fs.Lstat(workspaceTargetPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "already vanished", nil
		}
		return "", fmt.Errorf("stat workspace target: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", fmt.Errorf("workspace target must be a Veil materialized regular file")
	}
	if lease.plaintextHash == "" {
		return "modified", nil
	}
	data, err := fs.ReadFile(workspaceTargetPath)
	if err != nil {
		return "", fmt.Errorf("read workspace target: %w", err)
	}
	if sha256Hex(data) != lease.plaintextHash {
		return "modified", nil
	}
	if err := fs.Remove(workspaceTargetPath); err != nil {
		return "", fmt.Errorf("remove workspace target: %w", err)
	}
	return "vanished", nil
}

type domainLease struct {
	workspaceID   string
	target        string
	expiresAt     time.Time
	storeID       string
	plaintextHash string
}

func convertLeases(leases []domain.Lease) []domainLease {
	converted := make([]domainLease, 0, len(leases))
	for _, lease := range leases {
		converted = append(converted, domainLease{
			workspaceID:   lease.WorkspaceID,
			target:        lease.Target,
			expiresAt:     lease.ExpiresAt,
			storeID:       lease.StoreID,
			plaintextHash: lease.PlaintextHash,
		})
	}

	return converted
}
