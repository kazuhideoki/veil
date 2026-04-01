package usecase

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

type statusFileSystem interface {
	UserHomeDir() (string, error)
	Getwd() (string, error)
	EvalSymlinks(path string) (string, error)
	ReadFile(name string) ([]byte, error)
	Stat(name string) (os.FileInfo, error)
	Lstat(name string) (os.FileInfo, error)
	Readlink(name string) (string, error)
}

type StatusTargets struct {
	FileSystem statusFileSystem
	Stdout     io.Writer
	Now        func() time.Time
}

func (u StatusTargets) Run() error {
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

	_, state, lock, err := loadStateLocked(statusStateFileSystem{statusFileSystem: u.FileSystem})
	if err != nil {
		return err
	}
	defer func() {
		_ = lock.Unlock()
	}()

	now := currentTime(u.Now)

	for _, target := range workspace.Targets {
		storeTargetPath, err := config.StoreTargetPath(workspaceID, target)
		if err != nil {
			return err
		}

		workspaceTargetPath := filepath.Join(workspace.Root, target)
		status, err := detectTargetStatus(u.FileSystem, workspaceTargetPath, storeTargetPath)
		if err != nil {
			return fmt.Errorf("%s: %w", target, err)
		}

		lease, ok, err := state.FindLease(workspaceID, target)
		if err != nil {
			return err
		}
		if ok && status == "mounted" && !lease.ExpiresAt.After(now) {
			status = "expired"
		}

		fmt.Fprintf(u.Stdout, "%s target: %s\n", status, target)
	}

	return nil
}

func detectTargetStatus(fs statusFileSystem, workspaceTargetPath, storeTargetPath string) (string, error) {
	if _, err := fs.Stat(storeTargetPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "missing-source", nil
		}
		return "", fmt.Errorf("stat store target: %w", err)
	}

	info, err := fs.Lstat(workspaceTargetPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "absent", nil
		}
		return "", fmt.Errorf("stat workspace target: %w", err)
	}

	// Regular files and foreign symlinks hide the Veil-managed mount point.
	if info.Mode()&os.ModeSymlink == 0 {
		return "shadowed", nil
	}

	linkTarget, err := fs.Readlink(workspaceTargetPath)
	if err != nil {
		return "", fmt.Errorf("read workspace symlink: %w", err)
	}

	resolvedLinkTarget, err := resolveLinkTarget(fs, workspaceTargetPath, linkTarget)
	if err != nil {
		return "shadowed", nil
	}

	resolvedStoreTargetPath, err := fs.EvalSymlinks(storeTargetPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "missing-source", nil
		}
		return "", fmt.Errorf("canonicalize store target: %w", err)
	}

	if resolvedLinkTarget != resolvedStoreTargetPath {
		return "shadowed", nil
	}

	return "mounted", nil
}

type statusStateFileSystem struct {
	statusFileSystem
}

func (fs statusStateFileSystem) MkdirAll(path string, perm os.FileMode) error {
	return fmt.Errorf("mkdir all is not supported: %s", path)
}

func (fs statusStateFileSystem) WriteFile(name string, data []byte, perm os.FileMode) error {
	return fmt.Errorf("write file is not supported: %s", name)
}

func (fs statusStateFileSystem) Rename(oldpath, newpath string) error {
	return fmt.Errorf("rename is not supported: %s", oldpath)
}

func (fs statusStateFileSystem) Remove(name string) error {
	return fmt.Errorf("remove is not supported: %s", name)
}
