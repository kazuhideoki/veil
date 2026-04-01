package usecase

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

type RemoveWorkspace struct {
	FileSystem removeFileSystem
	Stdout     io.Writer
}

type PurgeWorkspace struct {
	FileSystem  removeFileSystem
	Stdin       io.Reader
	Stdout      io.Writer
	Interactive bool
	AssumeYes   bool
}

func (u RemoveWorkspace) Run() error {
	ctx, err := loadActiveWorkspaceContext(u.FileSystem)
	if err != nil {
		return err
	}

	targets := append([]string(nil), ctx.workspace.Targets...)
	changes, err := prepareWorkspaceTargetChanges(u.FileSystem, &ctx, targets, prepareTargetRemove)
	if err != nil {
		return err
	}

	if err := ctx.config.RemoveWorkspace(ctx.workspaceID); err != nil {
		if rollbackErr := rollbackPreparedChanges(changes, "rollback remove workspace"); rollbackErr != nil {
			return errors.Join(err, rollbackErr)
		}
		return err
	}

	if err := ctx.persistRenderedConfig(u.FileSystem, u.Stdout); err != nil {
		if rollbackErr := rollbackPreparedChanges(changes, "rollback remove workspace"); rollbackErr != nil {
			return errors.Join(err, rollbackErr)
		}
		return err
	}

	if err := finishWorkspaceChanges(u.FileSystem, &ctx, changes); err != nil {
		return err
	}

	if err := clearWorkspaceLeases(u.FileSystem, ctx.workspaceID); err != nil {
		return err
	}

	fmt.Fprintf(u.Stdout, "removed workspace: %s\n", ctx.workspaceID)
	return nil
}

func (u PurgeWorkspace) Run() error {
	ctx, err := loadActiveWorkspaceContext(u.FileSystem)
	if err != nil {
		return err
	}

	if err := u.confirm(ctx.workspaceID, len(ctx.workspace.Targets)); err != nil {
		return err
	}

	targets := append([]string(nil), ctx.workspace.Targets...)
	changes, err := prepareWorkspaceTargetChanges(u.FileSystem, &ctx, targets, prepareTargetPurge)
	if err != nil {
		return err
	}

	if err := ctx.config.RemoveWorkspace(ctx.workspaceID); err != nil {
		if rollbackErr := rollbackPreparedChanges(changes, "rollback purge workspace"); rollbackErr != nil {
			return errors.Join(err, rollbackErr)
		}
		return err
	}

	if err := ctx.persistRenderedConfig(u.FileSystem, u.Stdout); err != nil {
		if rollbackErr := rollbackPreparedChanges(changes, "rollback purge workspace"); rollbackErr != nil {
			return errors.Join(err, rollbackErr)
		}
		return err
	}

	if err := finishWorkspaceChanges(u.FileSystem, &ctx, changes); err != nil {
		return err
	}

	if err := clearWorkspaceLeases(u.FileSystem, ctx.workspaceID); err != nil {
		return err
	}

	fmt.Fprintf(u.Stdout, "purged workspace: %s\n", ctx.workspaceID)
	return nil
}

func (u PurgeWorkspace) confirm(workspaceID string, targetCount int) error {
	if u.AssumeYes {
		return nil
	}

	if !u.Interactive {
		return fmt.Errorf("workspace purge requires --yes when stdin is not a terminal")
	}

	fmt.Fprintf(u.Stdout, "purge workspace %q and delete %d registered target(s)? [y/N]: ", workspaceID, targetCount)

	reader := bufio.NewReader(u.Stdin)
	answer, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read workspace purge confirmation: %w", err)
	}

	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return nil
	default:
		return fmt.Errorf("workspace purge canceled")
	}
}

func prepareWorkspaceTargetChanges(
	fs removeFileSystem,
	ctx *activeWorkspaceContext,
	targets []string,
	prepare func(removeFileSystem, *activeWorkspaceContext, string) (preparedTargetChange, error),
) ([]preparedTargetChange, error) {
	changes := make([]preparedTargetChange, 0, len(targets))

	for _, target := range targets {
		change, err := prepare(fs, ctx, target)
		if err != nil {
			if rollbackErr := rollbackPreparedChanges(changes, "rollback prepared workspace change"); rollbackErr != nil {
				return nil, errors.Join(err, rollbackErr)
			}
			return nil, err
		}

		changes = append(changes, change)
	}

	return changes, nil
}

func finishWorkspaceChanges(fs removeFileSystem, ctx *activeWorkspaceContext, changes []preparedTargetChange) error {
	postCommitErr := runPostCommitChanges(changes)

	workspaceStoreRoot := filepath.Join(ctx.config.StorePath, "workspaces", ctx.workspaceID)
	if err := fs.Remove(workspaceStoreRoot); err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, syscall.ENOTEMPTY) {
		postCommitErr = errors.Join(postCommitErr, err)
	}

	return postCommitErr
}

func clearWorkspaceLeases(fs stateFileSystem, workspaceID string) error {
	statePath, state, lock, err := loadStateLocked(fs)
	if err != nil {
		return err
	}
	defer func() {
		_ = lock.Unlock()
	}()

	if err := state.RemoveWorkspaceLeases(workspaceID); err != nil {
		return err
	}

	return persistState(fs, statePath, state)
}
