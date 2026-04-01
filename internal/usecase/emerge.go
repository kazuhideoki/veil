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

type emergeFileSystem interface {
	UserHomeDir() (string, error)
	Getwd() (string, error)
	EvalSymlinks(path string) (string, error)
	MkdirAll(path string, perm os.FileMode) error
	ReadFile(name string) ([]byte, error)
	WriteFile(name string, data []byte, perm os.FileMode) error
	Rename(oldpath, newpath string) error
	Stat(name string) (os.FileInfo, error)
	Lstat(name string) (os.FileInfo, error)
	Readlink(name string) (string, error)
	Symlink(oldname, newname string) error
	Remove(name string) error
}

type ttlCleanerStarter interface {
	Start() error
}

type EmergeTargets struct {
	FileSystem     emergeFileSystem
	Stdout         io.Writer
	Now            func() time.Time
	CleanerStarter ttlCleanerStarter
}

func (u EmergeTargets) Run() error {
	_, config, err := loadConfig(u.FileSystem)
	if err != nil {
		return err
	}

	currentDir, err := u.FileSystem.Getwd()
	if err != nil {
		return fmt.Errorf("resolve current directory: %w", err)
	}

	currentDir, err = u.FileSystem.EvalSymlinks(currentDir)
	if err != nil {
		return fmt.Errorf("canonicalize current directory: %w", err)
	}

	homeDir, err := u.FileSystem.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}

	config.StorePath = expandHomeDir(config.StorePath, homeDir)
	config = canonicalizeWorkspaceRoots(config, u.FileSystem)

	workspaceID, workspace, err := config.ResolveWorkspaceByDir(currentDir)
	if err != nil {
		return err
	}

	statePath, state, lock, err := loadStateLocked(u.FileSystem)
	if err != nil {
		return err
	}
	defer func() {
		_ = lock.Unlock()
	}()

	now := currentTime(u.Now)
	ttl, err := config.EffectiveTTL(workspace)
	if err != nil {
		return err
	}

	originalState := cloneState(state)
	createdTargetPaths := []string{}

	for _, target := range workspace.Targets {
		storeTargetPath, err := config.StoreTargetPath(workspaceID, target)
		if err != nil {
			return rollbackEmergeChanges(u.FileSystem, statePath, originalState, createdTargetPaths, err)
		}

		storeInfo, err := u.FileSystem.Stat(storeTargetPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return rollbackEmergeChanges(u.FileSystem, statePath, originalState, createdTargetPaths, fmt.Errorf("store target does not exist: %s", target))
			}
			return rollbackEmergeChanges(u.FileSystem, statePath, originalState, createdTargetPaths, fmt.Errorf("stat store target: %w", err))
		}

		if !storeInfo.Mode().IsRegular() {
			return rollbackEmergeChanges(u.FileSystem, statePath, originalState, createdTargetPaths, fmt.Errorf("store target must be a regular file: %s", target))
		}

		workspaceTargetPath := filepath.Join(workspace.Root, target)
		// TODO: Reject targets whose resolved parent path escapes workspace.Root via symlinked directories.
		if err := u.FileSystem.MkdirAll(filepath.Dir(workspaceTargetPath), 0o755); err != nil {
			return rollbackEmergeChanges(u.FileSystem, statePath, originalState, createdTargetPaths, fmt.Errorf("create workspace target directory: %w", err))
		}

		created, err := ensureEmergedTarget(u.FileSystem, workspaceTargetPath, storeTargetPath)
		if err != nil {
			return rollbackEmergeChanges(u.FileSystem, statePath, originalState, createdTargetPaths, fmt.Errorf("%s: %w", target, err))
		}

		if created {
			createdTargetPaths = append(createdTargetPaths, workspaceTargetPath)
			fmt.Fprintf(u.Stdout, "emerged target: %s\n", target)
		} else {
			fmt.Fprintf(u.Stdout, "already emerged target: %s\n", target)
		}

		if err := state.UpsertLease(workspaceID, target, now, now.Add(ttl)); err != nil {
			return rollbackEmergeChanges(u.FileSystem, statePath, originalState, createdTargetPaths, err)
		}
	}

	if err := persistState(u.FileSystem, statePath, state); err != nil {
		return rollbackEmergeChanges(u.FileSystem, statePath, originalState, createdTargetPaths, err)
	}

	if u.CleanerStarter != nil {
		if err := u.CleanerStarter.Start(); err != nil {
			return rollbackEmergeChanges(u.FileSystem, statePath, originalState, createdTargetPaths, fmt.Errorf("start ttl cleaner: %w", err))
		}
	}

	return nil
}

func rollbackEmergeChanges(fs emergeFileSystem, statePath string, originalState domain.State, createdTargetPaths []string, cause error) error {
	var rollbackErr error

	for i := len(createdTargetPaths) - 1; i >= 0; i-- {
		if err := fs.Remove(createdTargetPaths[i]); err != nil && !errors.Is(err, os.ErrNotExist) {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("rollback emerged target %s: %w", createdTargetPaths[i], err))
		}
	}

	if err := persistState(fs, statePath, originalState); err != nil {
		rollbackErr = errors.Join(rollbackErr, fmt.Errorf("rollback state file: %w", err))
	}

	if rollbackErr != nil {
		return errors.Join(cause, rollbackErr)
	}

	return cause
}

func cloneState(state domain.State) domain.State {
	cloned := state
	cloned.Leases = append([]domain.Lease(nil), state.Leases...)
	return cloned
}

func ensureEmergedTarget(fs emergeFileSystem, workspaceTargetPath, storeTargetPath string) (bool, error) {
	info, err := fs.Lstat(workspaceTargetPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return false, fmt.Errorf("stat workspace target: %w", err)
		}

		if err := fs.Symlink(storeTargetPath, workspaceTargetPath); err != nil {
			return false, fmt.Errorf("create workspace symlink: %w", err)
		}
		return true, nil
	}

	if info.Mode()&os.ModeSymlink == 0 {
		return false, fmt.Errorf("workspace target already exists")
	}

	linkTarget, err := fs.Readlink(workspaceTargetPath)
	if err != nil {
		return false, fmt.Errorf("read workspace symlink: %w", err)
	}

	resolvedLinkTarget, err := resolveLinkTarget(fs, workspaceTargetPath, linkTarget)
	if err != nil {
		return false, err
	}

	resolvedStoreTargetPath, err := fs.EvalSymlinks(storeTargetPath)
	if err != nil {
		return false, fmt.Errorf("canonicalize store target: %w", err)
	}

	if resolvedLinkTarget != resolvedStoreTargetPath {
		return false, fmt.Errorf("workspace target already exists")
	}

	return false, nil
}

func resolveLinkTarget(fs symlinkEvaluator, workspaceTargetPath, linkTarget string) (string, error) {
	if !filepath.IsAbs(linkTarget) {
		linkTarget = filepath.Join(filepath.Dir(workspaceTargetPath), linkTarget)
	}

	resolvedLinkTarget, err := fs.EvalSymlinks(linkTarget)
	if err != nil {
		return "", fmt.Errorf("canonicalize workspace symlink target: %w", err)
	}

	return resolvedLinkTarget, nil
}
