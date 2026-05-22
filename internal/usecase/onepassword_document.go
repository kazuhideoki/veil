package usecase

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kazuhideoki/veil/internal/domain"
)

const onePasswordStoreID = "1password"

type OnePasswordDocumentRuntime interface {
	CreateDocument(vault, title string, tags []string, data []byte) (string, error)
	ReadDocument(vault, itemID string) ([]byte, error)
	UpdateDocument(vault, itemID string, data []byte) error
}

type onePasswordMaterializedFileSystem interface {
	Lstat(name string) (os.FileInfo, error)
	ReadFile(name string) ([]byte, error)
}

func onePasswordVault(config domain.Config, document domain.DocumentConfig) string {
	if document.Vault != "" {
		return document.Vault
	}
	if config.Store.Vault != "" {
		return config.Store.Vault
	}
	return "Personal"
}

func onePasswordTitle(workspaceID, target string) string {
	return fmt.Sprintf("Veil: %s: %s", workspaceID, target)
}

func onePasswordTags(workspaceID string) []string {
	return []string{"veil", "veil/workspace/" + workspaceID}
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func requireOnePasswordRuntime(runtime OnePasswordDocumentRuntime) error {
	if runtime == nil {
		return fmt.Errorf("1Password document store requires a document runtime")
	}
	return nil
}

func validateOnePasswordMaterializedTarget(fs onePasswordMaterializedFileSystem, lease domain.Lease, workspaceTargetPath, targetPath, itemID string, now time.Time) ([]byte, error) {
	if lease.StoreID != onePasswordStoreID {
		return nil, fmt.Errorf("target is not emerged from 1Password document store: %s", targetPath)
	}
	if lease.StorePath != "" && lease.StorePath != itemID {
		return nil, fmt.Errorf("target lease does not match registered 1Password document: %s", targetPath)
	}
	if lease.PlaintextHash == "" {
		return nil, fmt.Errorf("target has no recorded plaintext hash; re-run veil emerge before update: %s", targetPath)
	}
	if !lease.ExpiresAt.After(now) {
		return nil, fmt.Errorf("target lease is expired; re-run veil emerge before update: %s", targetPath)
	}
	if lease.WorkspacePath != "" && filepath.Clean(lease.WorkspacePath) != filepath.Clean(workspaceTargetPath) {
		return nil, fmt.Errorf("target workspace path does not match active lease: %s", targetPath)
	}
	info, err := fs.Lstat(workspaceTargetPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("workspace target does not exist: %s", targetPath)
		}
		return nil, fmt.Errorf("stat workspace target: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("workspace target must be a Veil materialized regular file: %s", targetPath)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("workspace target must be a regular file: %s", targetPath)
	}
	data, err := fs.ReadFile(workspaceTargetPath)
	if err != nil {
		return nil, fmt.Errorf("read workspace target: %w", err)
	}
	return data, nil
}

func writeConfig(fs stateFileSystem, configPath string, config domain.Config) error {
	configData, err := config.RenderTOML()
	if err != nil {
		return err
	}
	if err := fs.WriteFile(configPath, configData, 0o644); err != nil {
		return fmt.Errorf("write config file: %w", err)
	}
	return nil
}

func ensureMaterializedFile(fs emergeFileSystem, state domain.State, workspaceID, target, workspaceTargetPath, itemID string, data []byte, now time.Time) (bool, error) {
	info, err := fs.Lstat(workspaceTargetPath)
	if err != nil {
		if os.IsNotExist(err) {
			if err := fs.MkdirAll(filepath.Dir(workspaceTargetPath), 0o755); err != nil {
				return false, fmt.Errorf("create workspace target directory: %w", err)
			}
			if err := fs.WriteFile(workspaceTargetPath, data, 0o600); err != nil {
				return false, fmt.Errorf("write workspace target: %w", err)
			}
			return true, nil
		}
		return false, fmt.Errorf("stat workspace target: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("workspace target is a symlink: %s", target)
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("workspace target must be a regular file: %s", target)
	}

	currentData, err := fs.ReadFile(workspaceTargetPath)
	if err != nil {
		return false, fmt.Errorf("read workspace target: %w", err)
	}
	lease, ok, err := state.FindLease(workspaceID, target)
	if err != nil {
		return false, err
	}
	if ok {
		if _, err := validateOnePasswordMaterializedTarget(fs, lease, workspaceTargetPath, target, itemID, now); err != nil {
			return false, err
		}
		if sha256Hex(currentData) != lease.PlaintextHash {
			return false, fmt.Errorf("workspace target has uncommitted changes: %s", target)
		}
	}
	if !ok && sha256Hex(currentData) != sha256Hex(data) {
		return false, fmt.Errorf("workspace target already exists: %s", target)
	}
	if string(currentData) == string(data) {
		return false, nil
	}
	if err := fs.WriteFile(workspaceTargetPath, data, 0o600); err != nil {
		return false, fmt.Errorf("write workspace target: %w", err)
	}
	return false, nil
}

func updateOnePasswordDocument(runtime OnePasswordDocumentRuntime, config domain.Config, document domain.DocumentConfig, workspaceData []byte) (domain.DocumentConfig, bool, error) {
	vault := onePasswordVault(config, document)
	remoteData, err := runtime.ReadDocument(vault, document.ItemID)
	if err != nil {
		return document, false, err
	}
	remoteHash := sha256Hex(remoteData)
	localHash := sha256Hex(workspaceData)
	if document.ContentSHA256 != "" && remoteHash != document.ContentSHA256 && remoteHash != localHash {
		return document, false, fmt.Errorf("1Password document changed since last Veil sync")
	}
	if localHash == remoteHash {
		document.ContentSHA256 = localHash
		document.Vault = vault
		return document, false, nil
	}
	if err := runtime.UpdateDocument(vault, document.ItemID, workspaceData); err != nil {
		return document, false, err
	}
	document.ContentSHA256 = localHash
	document.Vault = vault
	return document, true, nil
}

func removeMaterializedTarget(fs vanishFileSystem, workspaceTargetPath string) error {
	info, err := fs.Lstat(workspaceTargetPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat workspace target: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("workspace target is a symlink")
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("workspace target must be a regular file")
	}
	if err := fs.Remove(workspaceTargetPath); err != nil {
		return fmt.Errorf("remove workspace target: %w", err)
	}
	return nil
}

func currentWorkspace(config domain.Config, fs workspaceResolverFileSystem) (string, domain.Workspace, error) {
	currentDir, err := fs.Getwd()
	if err != nil {
		return "", domain.Workspace{}, fmt.Errorf("resolve current directory: %w", err)
	}
	currentDir, err = fs.EvalSymlinks(currentDir)
	if err != nil {
		return "", domain.Workspace{}, fmt.Errorf("canonicalize current directory: %w", err)
	}
	return config.ResolveWorkspaceByDir(currentDir)
}

func updateLeaseHash(state *domain.State, workspaceID, target, workspacePath, itemID, plaintextHash string, now time.Time, ttl time.Duration) error {
	return state.UpsertLeaseWithHash(workspaceID, target, now, now.Add(ttl), onePasswordStoreID, workspacePath, itemID, plaintextHash)
}

func writeUpdatedTarget(w io.Writer, target string, changed bool) {
	if changed {
		fmt.Fprintf(w, "updated target: %s\n", target)
		return
	}
	fmt.Fprintf(w, "already up to date target: %s\n", target)
}

func sanitizeTempPart(value string) string {
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-", " ", "-")
	value = replacer.Replace(value)
	if value == "" || value == "." || value == ".." {
		return "target"
	}
	return value
}
