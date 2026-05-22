package usecase

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type removeFileSystem interface {
	activeWorkspaceFileSystem
	MkdirAll(path string, perm os.FileMode) error
	ReadFile(name string) ([]byte, error)
	Rename(oldpath, newpath string) error
	Stat(name string) (os.FileInfo, error)
	Lstat(name string) (os.FileInfo, error)
	Readlink(name string) (string, error)
	Symlink(oldname, newname string) error
	Remove(name string) error
}

type RemoveTarget struct {
	FileSystem removeFileSystem
	Stdout     io.Writer
	TargetPath string
}

type PurgeTarget struct {
	FileSystem removeFileSystem
	Stdout     io.Writer
	TargetPath string
}

type preparedTargetChange struct {
	target                 string
	rollbackPersistFailure func() error
	postCommit             func() error
}

func (u RemoveTarget) Run() error {
	ctx, err := loadActiveWorkspaceContext(u.FileSystem)
	if err != nil {
		return err
	}
	if err := requireOnePasswordConfig(ctx.config); err != nil {
		return err
	}
	return u.removeOnePasswordTarget(ctx)
}

func (u RemoveTarget) removeOnePasswordTarget(ctx activeWorkspaceContext) error {
	targetPath, err := prepareOnePasswordTargetUnregister(u.FileSystem, &ctx, u.TargetPath, "remove")
	if err != nil {
		return err
	}

	if err := ctx.persistConfig(u.FileSystem, u.Stdout); err != nil {
		return err
	}
	if err := clearTargetLease(u.FileSystem, ctx.workspaceID, targetPath); err != nil {
		return err
	}

	fmt.Fprintf(u.Stdout, "removed target: %s\n", targetPath)
	return nil
}

func (u PurgeTarget) Run() error {
	ctx, err := loadActiveWorkspaceContext(u.FileSystem)
	if err != nil {
		return err
	}
	if err := requireOnePasswordConfig(ctx.config); err != nil {
		return err
	}
	return u.purgeOnePasswordTarget(ctx)
}

func (u PurgeTarget) purgeOnePasswordTarget(ctx activeWorkspaceContext) error {
	targetPath, err := prepareOnePasswordTargetUnregister(u.FileSystem, &ctx, u.TargetPath, "purge")
	if err != nil {
		return err
	}

	if err := ctx.persistConfig(u.FileSystem, u.Stdout); err != nil {
		return err
	}
	if err := clearTargetLease(u.FileSystem, ctx.workspaceID, targetPath); err != nil {
		return err
	}

	fmt.Fprintf(u.Stdout, "purged target: %s\n", targetPath)
	return nil
}

func prepareOnePasswordTargetUnregister(fs removeFileSystem, ctx *activeWorkspaceContext, target, action string) (string, error) {
	targetPath, err := normalizeEditTargetPath(target)
	if err != nil {
		return "", err
	}
	if !hasTarget(ctx.workspace.Targets, targetPath) {
		return "", fmt.Errorf("target is not registered: %s", targetPath)
	}
	if _, ok, err := ctx.config.DocumentForTarget(ctx.workspaceID, targetPath); err != nil {
		return "", err
	} else if !ok {
		return "", fmt.Errorf("1Password document is not registered: %s", targetPath)
	}

	workspaceTargetPath := filepath.Join(ctx.workspace.Root, targetPath)
	if _, err := fs.Lstat(workspaceTargetPath); err == nil {
		return "", fmt.Errorf("workspace target still exists: %s; run veil vanish before %s", targetPath, action)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("stat workspace target: %w", err)
	}

	if err := ctx.workspace.RemoveTarget(targetPath); err != nil {
		return "", err
	}
	if err := ctx.config.RemoveDocument(ctx.workspaceID, targetPath); err != nil {
		return "", err
	}
	return targetPath, nil
}

func clearTargetLease(fs stateFileSystem, workspaceID, target string) error {
	statePath, state, err := loadState(fs)
	if err != nil {
		return err
	}

	if err := state.RemoveLease(workspaceID, target); err != nil {
		return err
	}

	return persistState(fs, statePath, state)
}
