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

	change, err := prepareTargetRemove(u.FileSystem, &ctx, u.TargetPath)
	if err != nil {
		return err
	}

	if err := ctx.persistConfig(u.FileSystem, u.Stdout); err != nil {
		if rollbackErr := rollbackPreparedChanges([]preparedTargetChange{change}, "rollback remove target"); rollbackErr != nil {
			return errors.Join(err, rollbackErr)
		}
		return err
	}

	if err := runPostCommitChanges([]preparedTargetChange{change}); err != nil {
		return err
	}

	fmt.Fprintf(u.Stdout, "removed target: %s\n", change.target)
	return nil
}

func (u PurgeTarget) Run() error {
	ctx, err := loadActiveWorkspaceContext(u.FileSystem)
	if err != nil {
		return err
	}

	change, err := prepareTargetPurge(u.FileSystem, &ctx, u.TargetPath)
	if err != nil {
		return err
	}

	if err := ctx.persistConfig(u.FileSystem, u.Stdout); err != nil {
		if rollbackErr := rollbackPreparedChanges([]preparedTargetChange{change}, "rollback purge target"); rollbackErr != nil {
			return errors.Join(err, rollbackErr)
		}
		return err
	}

	if err := runPostCommitChanges([]preparedTargetChange{change}); err != nil {
		return err
	}

	fmt.Fprintf(u.Stdout, "purged target: %s\n", change.target)
	return nil
}

func prepareTargetRemove(fs removeFileSystem, ctx *activeWorkspaceContext, target string) (preparedTargetChange, error) {
	targetPath, err := normalizeEditTargetPath(target)
	if err != nil {
		return preparedTargetChange{}, err
	}

	if !hasTarget(ctx.workspace.Targets, targetPath) {
		return preparedTargetChange{}, fmt.Errorf("target is not registered: %s", targetPath)
	}

	storeTargetPath, err := ctx.config.StoreTargetPath(ctx.workspaceID, targetPath)
	if err != nil {
		return preparedTargetChange{}, err
	}

	storeInfo, err := fs.Stat(storeTargetPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return preparedTargetChange{}, fmt.Errorf("store target does not exist: %s", targetPath)
		}
		return preparedTargetChange{}, fmt.Errorf("stat store target: %w", err)
	}

	if !storeInfo.Mode().IsRegular() {
		return preparedTargetChange{}, fmt.Errorf("store target must be a regular file: %s", targetPath)
	}

	workspaceTargetPath := filepath.Join(ctx.workspace.Root, targetPath)
	managedSymlink, err := canReplaceWithWorkspaceFile(fs, workspaceTargetPath, storeTargetPath)
	if err != nil {
		return preparedTargetChange{}, err
	}

	storeData, err := fs.ReadFile(storeTargetPath)
	if err != nil {
		return preparedTargetChange{}, fmt.Errorf("read store target: %w", err)
	}

	if managedSymlink {
		if err := fs.Remove(workspaceTargetPath); err != nil {
			return preparedTargetChange{}, fmt.Errorf("remove workspace symlink: %w", err)
		}
	}

	if err := fs.MkdirAll(filepath.Dir(workspaceTargetPath), 0o755); err != nil {
		return preparedTargetChange{}, fmt.Errorf("create workspace target directory: %w", err)
	}

	if err := fs.WriteFile(workspaceTargetPath, storeData, storeInfo.Mode().Perm()); err != nil {
		return preparedTargetChange{}, fmt.Errorf("write workspace target: %w", err)
	}

	if err := ctx.workspace.RemoveTarget(targetPath); err != nil {
		return preparedTargetChange{}, err
	}

	return preparedTargetChange{
		target: targetPath,
		rollbackPersistFailure: func() error {
			if err := fs.Remove(workspaceTargetPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("remove restored workspace target: %w", err)
			}

			if managedSymlink {
				if err := fs.Symlink(storeTargetPath, workspaceTargetPath); err != nil {
					return fmt.Errorf("restore workspace symlink: %w", err)
				}
			}

			return nil
		},
		postCommit: func() error {
			if err := fs.Remove(storeTargetPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("remove store target: %w", err)
			}

			cleanupEmptyStoreDirs(fs, storeTargetPath, filepath.Join(ctx.config.StorePath, "workspaces", ctx.workspaceID))
			return nil
		},
	}, nil
}

func prepareTargetPurge(fs removeFileSystem, ctx *activeWorkspaceContext, target string) (preparedTargetChange, error) {
	targetPath, err := normalizeEditTargetPath(target)
	if err != nil {
		return preparedTargetChange{}, err
	}

	if !hasTarget(ctx.workspace.Targets, targetPath) {
		return preparedTargetChange{}, fmt.Errorf("target is not registered: %s", targetPath)
	}

	storeTargetPath, err := ctx.config.StoreTargetPath(ctx.workspaceID, targetPath)
	if err != nil {
		return preparedTargetChange{}, err
	}

	workspaceTargetPath := filepath.Join(ctx.workspace.Root, targetPath)
	shouldVanish, err := shouldVanishTarget(fs, workspaceTargetPath, storeTargetPath)
	if err != nil {
		return preparedTargetChange{}, fmt.Errorf("%s: %w", targetPath, err)
	}

	if info, err := fs.Stat(storeTargetPath); err == nil {
		if !info.Mode().IsRegular() {
			return preparedTargetChange{}, fmt.Errorf("store target must be a regular file: %s", targetPath)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return preparedTargetChange{}, fmt.Errorf("stat store target: %w", err)
	}

	if err := ctx.workspace.RemoveTarget(targetPath); err != nil {
		return preparedTargetChange{}, err
	}

	return preparedTargetChange{
		target: targetPath,
		postCommit: func() error {
			if shouldVanish {
				if err := fs.Remove(workspaceTargetPath); err != nil && !errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf("remove workspace symlink: %w", err)
				}
			}

			if err := fs.Remove(storeTargetPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("remove store target: %w", err)
			}

			cleanupEmptyStoreDirs(fs, storeTargetPath, filepath.Join(ctx.config.StorePath, "workspaces", ctx.workspaceID))
			return nil
		},
	}, nil
}

func rollbackPreparedChanges(changes []preparedTargetChange, action string) error {
	var rollbackErr error

	for i := len(changes) - 1; i >= 0; i-- {
		change := changes[i]
		if change.rollbackPersistFailure == nil {
			continue
		}

		if err := change.rollbackPersistFailure(); err != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("%s %s: %w", action, change.target, err))
		}
	}

	return rollbackErr
}

func runPostCommitChanges(changes []preparedTargetChange) error {
	var postCommitErr error

	for _, change := range changes {
		if change.postCommit == nil {
			continue
		}

		if err := change.postCommit(); err != nil {
			postCommitErr = errors.Join(postCommitErr, fmt.Errorf("%s: %w", change.target, err))
		}
	}

	return postCommitErr
}

func shouldVanishTarget(fs removeFileSystem, workspaceTargetPath, storeTargetPath string) (bool, error) {
	info, err := fs.Lstat(workspaceTargetPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("stat workspace target: %w", err)
	}

	if info.Mode()&os.ModeSymlink == 0 {
		return false, nil
	}

	return isManagedWorkspaceSymlink(fs, workspaceTargetPath, storeTargetPath)
}

func canReplaceWithWorkspaceFile(fs removeFileSystem, workspaceTargetPath, storeTargetPath string) (bool, error) {
	info, err := fs.Lstat(workspaceTargetPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("stat workspace target: %w", err)
	}

	if info.Mode()&os.ModeSymlink == 0 {
		return false, fmt.Errorf("workspace target already exists")
	}

	managed, err := isManagedWorkspaceSymlink(fs, workspaceTargetPath, storeTargetPath)
	if err != nil {
		return false, err
	}

	if !managed {
		return false, fmt.Errorf("workspace target already exists")
	}

	return true, nil
}

// cleanupEmptyStoreDirs removes empty target parent directories without touching the shared store root.
func cleanupEmptyStoreDirs(fs interface{ Remove(name string) error }, storeTargetPath, workspaceStoreRoot string) {
	current := filepath.Dir(storeTargetPath)
	stop := filepath.Clean(workspaceStoreRoot)

	for {
		if filepath.Clean(current) == "." {
			return
		}

		_ = fs.Remove(current)
		if filepath.Clean(current) == stop {
			return
		}

		next := filepath.Dir(current)
		if next == current {
			return
		}
		current = next
	}
}
