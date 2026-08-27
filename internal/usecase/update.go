package usecase

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

type commitFileSystem interface {
	UserHomeDir() (string, error)
	Getwd() (string, error)
	EvalSymlinks(path string) (string, error)
	ReadFile(name string) ([]byte, error)
	WriteFile(name string, data []byte, perm os.FileMode) error
	Stat(name string) (os.FileInfo, error)
	Lstat(name string) (os.FileInfo, error)
	MkdirAll(path string, perm os.FileMode) error
	Rename(oldpath, newpath string) error
	Remove(name string) error
}

type CommitTarget struct {
	FileSystem      commitFileSystem
	DocumentRuntime OnePasswordDocumentRuntime
	Stdout          io.Writer
	TargetPath      string
	Repo            string
	Now             func() time.Time
	OverwriteRemote bool
}

func (u CommitTarget) Run() error {
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
	if !config.IsOnePasswordStore() {
		return fmt.Errorf("commit is only supported for 1Password document stores")
	}
	if err := requireOnePasswordRuntime(u.DocumentRuntime); err != nil {
		return err
	}

	workspaceID, workspace, err := resolveSelectedWorkspace(u.FileSystem, config, u.Repo)
	if err != nil {
		return err
	}
	targetPath, err := normalizeEditTargetPath(u.TargetPath)
	if err != nil {
		return err
	}
	if !hasTarget(workspace.Targets, targetPath) {
		return fmt.Errorf("target is not registered: %s", targetPath)
	}
	document, ok, err := config.DocumentForTarget(workspaceID, targetPath)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("1Password document is not registered: %s", targetPath)
	}

	workspaceTargetPath := filepath.Join(workspace.Root, targetPath)
	_, state, err := loadState(u.FileSystem)
	if err != nil {
		return err
	}
	lease, ok, err := state.FindLease(workspaceID, targetPath)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("target is not emerged: %s", targetPath)
	}
	if u.Repo != "" && (lease.WorkspacePath == "" || lease.StorePath == "") {
		return fmt.Errorf("target lease is not bound to the selected repo; re-run veil emerge --repo %s: %s", u.Repo, targetPath)
	}
	if lease.StoreID != onePasswordStoreID {
		return fmt.Errorf("target is not emerged from 1Password document store: %s", targetPath)
	}
	if lease.PlaintextHash == "" {
		return fmt.Errorf("target has no recorded plaintext hash; re-run veil emerge before commit: %s", targetPath)
	}
	if !lease.ExpiresAt.After(currentTime(u.Now)) {
		return fmt.Errorf("target lease is expired; re-run veil emerge before commit: %s", targetPath)
	}
	if lease.WorkspacePath != "" && filepath.Clean(lease.WorkspacePath) != filepath.Clean(workspaceTargetPath) {
		return fmt.Errorf("target workspace path does not match active lease: %s", targetPath)
	}
	workspaceData, err := validateOnePasswordMaterializedTarget(u.FileSystem, lease, workspaceTargetPath, targetPath, document.ItemID, currentTime(u.Now))
	if err != nil {
		return err
	}
	updatedDocument, changed, err := updateOnePasswordDocumentWithOptions(u.DocumentRuntime, config, document, workspaceData, lease.PlaintextHash, updateOnePasswordOptions{
		OverwriteRemote: u.OverwriteRemote,
	})
	if err != nil {
		return fmt.Errorf("%s: %w", targetPath, err)
	}
	if err := config.UpsertDocument(updatedDocument); err != nil {
		return err
	}

	statePath, err := statePath(u.FileSystem)
	if err != nil {
		return err
	}
	ttl, err := config.EffectiveTTL(workspace)
	if err != nil {
		return err
	}
	if err := updateLeaseHash(&state, workspaceID, targetPath, workspaceTargetPath, updatedDocument.ItemID, updatedDocument.ContentSHA256, currentTime(u.Now), ttl); err != nil {
		return err
	}
	if err := persistState(u.FileSystem, statePath, state); err != nil {
		return err
	}
	if updatedDocument.Vault != document.Vault {
		configData, err := config.RenderTOML()
		if err != nil {
			return err
		}
		if err := u.FileSystem.WriteFile(configPath, configData, 0o644); err != nil {
			return fmt.Errorf("write config file: %w", err)
		}
	}
	writeCommittedTarget(u.Stdout, targetPath, changed, u.OverwriteRemote)
	return nil
}
