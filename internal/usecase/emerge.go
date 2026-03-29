package usecase

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type emergeFileSystem interface {
	UserHomeDir() (string, error)
	Getwd() (string, error)
	EvalSymlinks(path string) (string, error)
	MkdirAll(path string, perm os.FileMode) error
	ReadFile(name string) ([]byte, error)
	Stat(name string) (os.FileInfo, error)
	Lstat(name string) (os.FileInfo, error)
	Readlink(name string) (string, error)
	Symlink(oldname, newname string) error
}

type EmergeTargets struct {
	FileSystem emergeFileSystem
	Stdout     io.Writer
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

	for _, target := range workspace.Targets {
		storeTargetPath, err := config.StoreTargetPath(workspaceID, target)
		if err != nil {
			return err
		}

		storeInfo, err := u.FileSystem.Stat(storeTargetPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("store target does not exist: %s", target)
			}
			return fmt.Errorf("stat store target: %w", err)
		}

		if !storeInfo.Mode().IsRegular() {
			return fmt.Errorf("store target must be a regular file: %s", target)
		}

		workspaceTargetPath := filepath.Join(workspace.Root, target)
		// TODO: Reject targets whose resolved parent path escapes workspace.Root via symlinked directories.
		if err := u.FileSystem.MkdirAll(filepath.Dir(workspaceTargetPath), 0o755); err != nil {
			return fmt.Errorf("create workspace target directory: %w", err)
		}

		created, err := ensureEmergedTarget(u.FileSystem, workspaceTargetPath, storeTargetPath)
		if err != nil {
			return fmt.Errorf("%s: %w", target, err)
		}

		if created {
			fmt.Fprintf(u.Stdout, "emerged target: %s\n", target)
			continue
		}

		fmt.Fprintf(u.Stdout, "already emerged target: %s\n", target)
	}

	return nil
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
