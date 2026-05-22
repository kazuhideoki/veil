package usecase

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type RemoveWorkspace struct {
	FileSystem  removeFileSystem
	Stdout      io.Writer
	WorkspaceID string
}

type PurgeWorkspace struct {
	FileSystem  removeFileSystem
	Stdin       io.Reader
	Stdout      io.Writer
	Interactive bool
	AssumeYes   bool
}

func (u RemoveWorkspace) Run() error {
	if u.WorkspaceID != "" {
		return u.removeRegisteredWorkspaceByID()
	}

	ctx, err := loadActiveWorkspaceContext(u.FileSystem)
	if err != nil {
		return err
	}

	if err := requireOnePasswordConfig(ctx.config); err != nil {
		return err
	}
	return u.removeOnePasswordWorkspace(ctx)
}

func (u RemoveWorkspace) removeRegisteredWorkspaceByID() error {
	configPath, config, err := loadConfig(u.FileSystem)
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

	workspace, exists := config.Workspaces[u.WorkspaceID]
	if !exists {
		return fmt.Errorf("workspace does not exist: %s", u.WorkspaceID)
	}
	if err := requireWorkspaceRootMissing(u.FileSystem, u.WorkspaceID, workspace.Root); err != nil {
		return err
	}

	if err := config.RemoveWorkspace(u.WorkspaceID); err != nil {
		return err
	}
	if err := config.RemoveWorkspaceDocuments(u.WorkspaceID); err != nil {
		return err
	}

	configData, err := config.RenderTOML()
	if err != nil {
		return err
	}
	if err := u.FileSystem.WriteFile(configPath, configData, 0o644); err != nil {
		return fmt.Errorf("write config file: %w", err)
	}
	if err := clearWorkspaceLeases(u.FileSystem, u.WorkspaceID); err != nil {
		return err
	}

	fmt.Fprintf(u.Stdout, "removed workspace: %s\n", u.WorkspaceID)
	return nil
}

func (u RemoveWorkspace) removeOnePasswordWorkspace(ctx activeWorkspaceContext) error {
	for _, target := range ctx.workspace.Targets {
		workspaceTargetPath := filepath.Join(ctx.workspace.Root, target)
		if _, err := u.FileSystem.Lstat(workspaceTargetPath); err == nil {
			return fmt.Errorf("workspace target still exists: %s; run veil vanish before workspace remove", target)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat workspace target: %w", err)
		}
	}

	if err := ctx.config.RemoveWorkspace(ctx.workspaceID); err != nil {
		return err
	}
	if err := ctx.config.RemoveWorkspaceDocuments(ctx.workspaceID); err != nil {
		return err
	}
	if err := ctx.persistRenderedConfig(u.FileSystem, u.Stdout); err != nil {
		return err
	}
	if err := clearWorkspaceLeases(u.FileSystem, ctx.workspaceID); err != nil {
		return err
	}

	fmt.Fprintf(u.Stdout, "removed workspace: %s\n", ctx.workspaceID)
	return nil
}

func requireWorkspaceRootMissing(fs removeFileSystem, workspaceID, root string) error {
	info, err := fs.Stat(root)
	if err == nil {
		if info.IsDir() {
			return fmt.Errorf("workspace root exists: %s; run veil workspace remove from that workspace after veil vanish", root)
		}
		return fmt.Errorf("workspace root path exists and is not a directory: %s", root)
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("stat workspace root for %s: %w", workspaceID, err)
}

func (u PurgeWorkspace) Run() error {
	ctx, err := loadActiveWorkspaceContext(u.FileSystem)
	if err != nil {
		return err
	}
	if err := requireOnePasswordConfig(ctx.config); err != nil {
		return err
	}

	if err := u.confirm(ctx.workspaceID, len(ctx.workspace.Targets)); err != nil {
		return err
	}

	remover := RemoveWorkspace{FileSystem: u.FileSystem, Stdout: u.Stdout}
	if err := remover.removeOnePasswordWorkspace(ctx); err != nil {
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

func clearWorkspaceLeases(fs stateFileSystem, workspaceID string) error {
	statePath, state, err := loadState(fs)
	if err != nil {
		return err
	}

	if err := state.RemoveWorkspaceLeases(workspaceID); err != nil {
		return err
	}

	return persistState(fs, statePath, state)
}
