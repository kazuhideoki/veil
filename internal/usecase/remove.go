package usecase

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kazuhideoki/veil/internal/domain"
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
	FileSystem      removeFileSystem
	DocumentRuntime OnePasswordDocumentRuntime
	Stdout          io.Writer
	TargetPath      string
}

type PurgeTarget struct {
	FileSystem      removeFileSystem
	DocumentRuntime OnePasswordDocumentRuntime
	Stdin           io.Reader
	Stdout          io.Writer
	Interactive     bool
	AssumeYes       bool
	TargetPath      string
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
	if err := requireOnePasswordRuntime(u.DocumentRuntime); err != nil {
		return err
	}
	return u.removeOnePasswordTarget(ctx)
}

func (u RemoveTarget) removeOnePasswordTarget(ctx activeWorkspaceContext) error {
	targetPath, _, err := prepareOnePasswordTargetForRemove(u.FileSystem, u.DocumentRuntime, &ctx, u.TargetPath)
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
	if err := requireOnePasswordRuntime(u.DocumentRuntime); err != nil {
		return err
	}
	if err := u.confirm(u.TargetPath); err != nil {
		return err
	}
	return u.purgeOnePasswordTarget(ctx)
}

func (u PurgeTarget) purgeOnePasswordTarget(ctx activeWorkspaceContext) error {
	targetPath, document, workspaceTargetPath, removeWorkspaceTarget, err := prepareOnePasswordTargetForPurge(u.FileSystem, u.DocumentRuntime, &ctx, u.TargetPath)
	if err != nil {
		return err
	}

	if removeWorkspaceTarget {
		if err := removeMaterializedTarget(u.FileSystem, workspaceTargetPath); err != nil {
			return err
		}
	}
	if err := ctx.persistConfig(u.FileSystem, u.Stdout); err != nil {
		return err
	}
	if err := clearTargetLease(u.FileSystem, ctx.workspaceID, targetPath); err != nil {
		return err
	}
	if err := u.DocumentRuntime.DeleteDocument(onePasswordVault(ctx.config, document), document.ItemID); err != nil {
		return err
	}

	fmt.Fprintf(u.Stdout, "purged target: %s\n", targetPath)
	return nil
}

func (u PurgeTarget) confirm(target string) error {
	if u.AssumeYes {
		return nil
	}

	if !u.Interactive {
		return fmt.Errorf("target purge requires --yes when stdin is not a terminal")
	}

	fmt.Fprintf(u.Stdout, "purge target %q and permanently delete its 1Password document? [y/N]: ", target)

	reader := bufio.NewReader(u.Stdin)
	answer, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read target purge confirmation: %w", err)
	}

	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return nil
	default:
		return fmt.Errorf("target purge canceled")
	}
}

func prepareOnePasswordTargetForRemove(fs removeFileSystem, runtime OnePasswordDocumentRuntime, ctx *activeWorkspaceContext, target string) (string, domain.DocumentConfig, error) {
	targetPath, document, workspaceTargetPath, err := resolveOnePasswordTarget(ctx, target)
	if err != nil {
		return "", domain.DocumentConfig{}, err
	}
	if err := ensureWorkspacePlaintextMatchesDocument(fs, runtime, ctx.config, document, targetPath, workspaceTargetPath); err != nil {
		return "", domain.DocumentConfig{}, err
	}
	if err := unregisterOnePasswordTarget(ctx, targetPath); err != nil {
		return "", domain.DocumentConfig{}, err
	}
	return targetPath, document, nil
}

func prepareOnePasswordTargetForPurge(fs removeFileSystem, runtime OnePasswordDocumentRuntime, ctx *activeWorkspaceContext, target string) (string, domain.DocumentConfig, string, bool, error) {
	targetPath, document, workspaceTargetPath, err := resolveOnePasswordTarget(ctx, target)
	if err != nil {
		return "", domain.DocumentConfig{}, "", false, err
	}
	removeWorkspaceTarget, err := validateWorkspaceTargetBeforePurge(fs, runtime, ctx.config, document, targetPath, workspaceTargetPath)
	if err != nil {
		return "", domain.DocumentConfig{}, "", false, err
	}
	if err := unregisterOnePasswordTarget(ctx, targetPath); err != nil {
		return "", domain.DocumentConfig{}, "", false, err
	}
	return targetPath, document, workspaceTargetPath, removeWorkspaceTarget, nil
}

func resolveOnePasswordTarget(ctx *activeWorkspaceContext, target string) (string, domain.DocumentConfig, string, error) {
	targetPath, err := normalizeEditTargetPath(target)
	if err != nil {
		return "", domain.DocumentConfig{}, "", err
	}
	if !hasTarget(ctx.workspace.Targets, targetPath) {
		return "", domain.DocumentConfig{}, "", fmt.Errorf("target is not registered: %s", targetPath)
	}
	document, ok, err := ctx.config.DocumentForTarget(ctx.workspaceID, targetPath)
	if err != nil {
		return "", domain.DocumentConfig{}, "", err
	} else if !ok {
		return "", domain.DocumentConfig{}, "", fmt.Errorf("1Password document is not registered: %s", targetPath)
	}

	workspaceTargetPath := filepath.Join(ctx.workspace.Root, targetPath)
	return targetPath, document, workspaceTargetPath, nil
}

func unregisterOnePasswordTarget(ctx *activeWorkspaceContext, targetPath string) error {
	if err := ctx.workspace.RemoveTarget(targetPath); err != nil {
		return err
	}
	if err := ctx.config.RemoveDocument(ctx.workspaceID, targetPath); err != nil {
		return err
	}
	return nil
}

func ensureWorkspacePlaintextMatchesDocument(fs removeFileSystem, runtime OnePasswordDocumentRuntime, config domain.Config, document domain.DocumentConfig, targetPath, workspaceTargetPath string) error {
	documentData, err := runtime.ReadDocument(onePasswordVault(config, document), document.ItemID)
	if err != nil {
		return fmt.Errorf("read 1Password document for remove: %w", err)
	}

	info, err := fs.Lstat(workspaceTargetPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if err := fs.MkdirAll(filepath.Dir(workspaceTargetPath), 0o755); err != nil {
				return fmt.Errorf("create workspace target directory: %w", err)
			}
			if err := fs.WriteFile(workspaceTargetPath, documentData, 0o600); err != nil {
				return fmt.Errorf("restore workspace target: %w", err)
			}
			return nil
		}
		return fmt.Errorf("stat workspace target: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("workspace target is a symlink: %s", targetPath)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("workspace target must be a regular file: %s", targetPath)
	}

	workspaceData, err := fs.ReadFile(workspaceTargetPath)
	if err != nil {
		return fmt.Errorf("read workspace target: %w", err)
	}
	if sha256Hex(workspaceData) != sha256Hex(documentData) {
		return fmt.Errorf("workspace target differs from 1Password document: %s; run veil update before remove or vanish --discard and retry", targetPath)
	}
	return nil
}

func validateWorkspaceTargetBeforePurge(fs removeFileSystem, runtime OnePasswordDocumentRuntime, config domain.Config, document domain.DocumentConfig, targetPath, workspaceTargetPath string) (bool, error) {
	info, err := fs.Lstat(workspaceTargetPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("stat workspace target: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("workspace target is a symlink: %s", targetPath)
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("workspace target must be a regular file: %s", targetPath)
	}

	documentData, err := runtime.ReadDocument(onePasswordVault(config, document), document.ItemID)
	if err != nil {
		return false, fmt.Errorf("read 1Password document for purge: %w", err)
	}
	workspaceData, err := fs.ReadFile(workspaceTargetPath)
	if err != nil {
		return false, fmt.Errorf("read workspace target: %w", err)
	}
	if sha256Hex(workspaceData) != sha256Hex(documentData) {
		return false, fmt.Errorf("workspace target differs from 1Password document: %s; run veil update before purge or vanish --discard and retry", targetPath)
	}
	return true, nil
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
