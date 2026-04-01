package usecase

import (
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/kazuhideoki/veil/internal/domain"
)

const maxTTLCleanerSleep = 5 * time.Second

type ttlCleanerFileSystem interface {
	removeFileSystem
}

type ttlCleanerLock interface {
	Lock() (bool, error)
	Unlock() error
}

type RunTTLCleaner struct {
	FileSystem ttlCleanerFileSystem
	Lock       ttlCleanerLock
	Stdout     io.Writer
	Now        func() time.Time
	Sleep      func(time.Duration)
}

func (u RunTTLCleaner) Run() error {
	if u.Lock == nil {
		return fmt.Errorf("ttl cleaner lock is required")
	}

	acquired, err := u.Lock.Lock()
	if err != nil {
		return fmt.Errorf("acquire ttl cleaner lock: %w", err)
	}
	if !acquired {
		return nil
	}
	defer func() {
		_ = u.Lock.Unlock()
	}()

	for {
		waitDuration, done, err := u.cleanupExpiredLeases()
		if err != nil {
			return err
		}
		if done {
			return nil
		}

		sleep := u.Sleep
		if sleep == nil {
			sleep = time.Sleep
		}
		sleep(waitDuration)
	}
}

func (u RunTTLCleaner) cleanupExpiredLeases() (time.Duration, bool, error) {
	_, config, err := loadConfig(u.FileSystem)
	if err != nil {
		return 0, false, err
	}

	homeDir, err := u.FileSystem.UserHomeDir()
	if err != nil {
		return 0, false, fmt.Errorf("resolve home directory: %w", err)
	}

	config.StorePath = expandHomeDir(config.StorePath, homeDir)
	config = canonicalizeWorkspaceRoots(config, u.FileSystem)

	statePath, state, stateLock, err := loadStateLocked(u.FileSystem)
	if err != nil {
		return 0, false, err
	}
	defer func() {
		_ = stateLock.Unlock()
	}()

	now := currentTime(u.Now)
	soonestExpiration := time.Duration(0)
	hasPendingLease := false

	for _, lease := range append([]domainLease(nil), convertLeases(state.Leases)...) {
		if lease.expiresAt.After(now) {
			wait := lease.expiresAt.Sub(now)
			if !hasPendingLease || wait < soonestExpiration {
				hasPendingLease = true
				soonestExpiration = wait
			}
			continue
		}

		workspace, ok := config.Workspaces[lease.workspaceID]
		if !ok || !hasTarget(workspace.Targets, lease.target) {
			if err := state.RemoveLease(lease.workspaceID, lease.target); err != nil {
				return 0, false, err
			}
			continue
		}

		workspaceTargetPath := filepath.Join(workspace.Root, lease.target)
		storeTargetPath, err := config.StoreTargetPath(lease.workspaceID, lease.target)
		if err != nil {
			return 0, false, err
		}

		status, err := vanishTarget(u.FileSystem, workspaceTargetPath, storeTargetPath)
		if err != nil {
			return 0, false, fmt.Errorf("%s/%s: %w", lease.workspaceID, lease.target, err)
		}

		if err := state.RemoveLease(lease.workspaceID, lease.target); err != nil {
			return 0, false, err
		}

		if u.Stdout != nil {
			fmt.Fprintf(u.Stdout, "expired %s target: %s/%s\n", status, lease.workspaceID, lease.target)
		}
	}

	if err := persistState(u.FileSystem, statePath, state); err != nil {
		return 0, false, err
	}

	if len(state.Leases) == 0 {
		return 0, true, nil
	}

	if !hasPendingLease {
		return 0, false, nil
	}

	if soonestExpiration > maxTTLCleanerSleep {
		return maxTTLCleanerSleep, false, nil
	}

	if soonestExpiration <= 0 {
		return 0, false, nil
	}

	return soonestExpiration, false, nil
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
