package usecase

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type vanishFileSystem interface {
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
	Remove(name string) error
}

type VanishTargets struct {
	FileSystem vanishFileSystem
	Stdout     io.Writer
}

func (u VanishTargets) Run() error {
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

	for _, target := range workspace.Targets {
		workspaceTargetPath := filepath.Join(workspace.Root, target)
		storeTargetPath, err := config.StoreTargetPath(workspaceID, target)
		if err != nil {
			return err
		}

		status, err := vanishTarget(u.FileSystem, workspaceTargetPath, storeTargetPath)
		if err != nil {
			return fmt.Errorf("%s: %w", target, err)
		}

		if err := state.RemoveLease(workspaceID, target); err != nil {
			return err
		}

		fmt.Fprintf(u.Stdout, "%s target: %s\n", status, target)
	}

	if err := persistState(u.FileSystem, statePath, state); err != nil {
		return err
	}

	return nil
}

func vanishTarget(fs vanishFileSystem, workspaceTargetPath, storeTargetPath string) (string, error) {
	info, err := fs.Lstat(workspaceTargetPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "already vanished", nil
		}
		return "", fmt.Errorf("stat workspace target: %w", err)
	}

	// Only Veil-managed symlinks should be removed from the workspace.
	if info.Mode()&os.ModeSymlink == 0 {
		return "skipped", nil
	}

	managed, err := isManagedWorkspaceSymlink(fs, workspaceTargetPath, storeTargetPath)
	if err != nil {
		return "", err
	}
	if !managed {
		return "skipped", nil
	}

	if err := fs.Remove(workspaceTargetPath); err != nil {
		return "", fmt.Errorf("remove workspace symlink: %w", err)
	}

	return "vanished", nil
}

func absoluteLinkTargetPath(workspaceTargetPath, linkTarget string) string {
	if !filepath.IsAbs(linkTarget) {
		linkTarget = filepath.Join(filepath.Dir(workspaceTargetPath), linkTarget)
	}

	return filepath.Clean(linkTarget)
}

func isManagedWorkspaceSymlink(fs vanishFileSystem, workspaceTargetPath, storeTargetPath string) (bool, error) {
	linkTarget, err := fs.Readlink(workspaceTargetPath)
	if err != nil {
		return false, fmt.Errorf("read workspace symlink: %w", err)
	}

	absoluteLinkTarget := absoluteLinkTargetPath(workspaceTargetPath, linkTarget)
	absoluteStoreTargetPath := filepath.Clean(storeTargetPath)

	resolvedLinkTarget, err := resolveLinkTarget(fs, workspaceTargetPath, linkTarget)
	if err != nil {
		// Broken managed symlinks should still be removable if they point at the expected store path.
		return absoluteLinkTarget == absoluteStoreTargetPath, nil
	}

	resolvedStoreTargetPath, err := fs.EvalSymlinks(storeTargetPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return absoluteLinkTarget == absoluteStoreTargetPath, nil
		}
		return false, fmt.Errorf("canonicalize store target: %w", err)
	}

	return resolvedLinkTarget == resolvedStoreTargetPath, nil
}
