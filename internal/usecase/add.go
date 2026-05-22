package usecase

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/kazuhideoki/veil/internal/domain"
)

type trackedChecker interface {
	IsTracked(workspaceRoot, relativePath string) (bool, error)
}

type addFileSystem interface {
	UserHomeDir() (string, error)
	Getwd() (string, error)
	EvalSymlinks(path string) (string, error)
	MkdirAll(path string, perm os.FileMode) error
	ReadDir(name string) ([]os.DirEntry, error)
	ReadFile(name string) ([]byte, error)
	WriteFile(name string, data []byte, perm os.FileMode) error
	Stat(name string) (os.FileInfo, error)
	Remove(name string) error
}

type AddTarget struct {
	FileSystem      addFileSystem
	TrackedChecker  trackedChecker
	DocumentRuntime OnePasswordDocumentRuntime
	Stdout          io.Writer
	TargetPath      string
	Now             func() time.Time
}

type addCandidate struct {
	targetPath          string
	workspaceTargetPath string
	storeTargetPath     string
	storeMode           os.FileMode
	storeData           []byte
}

func (u AddTarget) Run() error {
	configPath, config, err := loadConfig(u.FileSystem)
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

	config = expandConfigPaths(config, homeDir)
	config = canonicalizeWorkspaceRoots(config, u.FileSystem)
	if err := requireOnePasswordConfig(config); err != nil {
		return err
	}

	workspaceID, workspace, err := config.ResolveWorkspaceByDir(currentDir)
	if err != nil {
		return err
	}

	targetPaths, skippedDirs, err := u.resolveTargetPaths(workspace.Root)
	if err != nil {
		return err
	}

	return u.addOnePasswordDocuments(configPath, config, workspaceID, workspace, targetPaths, skippedDirs)
}

func (u AddTarget) addOnePasswordDocuments(configPath string, config domain.Config, workspaceID string, workspace domain.Workspace, targetPaths, skippedDirs []string) error {
	if err := requireOnePasswordRuntime(u.DocumentRuntime); err != nil {
		return err
	}

	updatedWorkspace := workspace
	candidates := make([]addCandidate, 0, len(targetPaths))
	for _, targetPath := range targetPaths {
		if err := updatedWorkspace.AddTarget(targetPath); err != nil {
			return err
		}
		if _, exists, err := config.DocumentForTarget(workspaceID, targetPath); err != nil {
			return err
		} else if exists {
			return fmt.Errorf("1Password document target already exists: %s", targetPath)
		}

		workspaceTargetPath := filepath.Join(workspace.Root, targetPath)
		targetInfo, err := u.FileSystem.Stat(workspaceTargetPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("target file does not exist: %s", targetPath)
			}
			return fmt.Errorf("stat target file: %w", err)
		}
		if !targetInfo.Mode().IsRegular() {
			return fmt.Errorf("target must be a regular file: %s", targetPath)
		}
		isTracked, err := u.TrackedChecker.IsTracked(workspace.Root, targetPath)
		if err != nil {
			return fmt.Errorf("check git tracking: %w", err)
		}
		if isTracked {
			return fmt.Errorf("target is tracked by git: %s", targetPath)
		}
		targetData, err := u.FileSystem.ReadFile(workspaceTargetPath)
		if err != nil {
			return fmt.Errorf("read target file: %w", err)
		}
		candidates = append(candidates, addCandidate{
			targetPath:          targetPath,
			workspaceTargetPath: workspaceTargetPath,
			storeMode:           targetInfo.Mode().Perm(),
			storeData:           targetData,
		})
	}

	vault := onePasswordVault(config, domain.DocumentConfig{})
	createdDocuments := make([]domain.DocumentConfig, 0, len(candidates))
	for _, candidate := range candidates {
		title := onePasswordTitle(workspaceID, candidate.targetPath)
		itemID, err := u.DocumentRuntime.CreateDocument(vault, title, onePasswordTags(workspaceID), candidate.storeData)
		if err != nil {
			return fmt.Errorf("create 1Password document: %w", err)
		}
		createdDocuments = append(createdDocuments, domain.DocumentConfig{
			WorkspaceID:   workspaceID,
			Target:        candidate.targetPath,
			ItemID:        itemID,
			Vault:         vault,
			Title:         title,
			ContentSHA256: sha256Hex(candidate.storeData),
		})
	}

	config.Workspaces[workspaceID] = updatedWorkspace
	for _, document := range createdDocuments {
		if err := config.UpsertDocument(document); err != nil {
			return err
		}
	}

	configData, err := config.RenderTOML()
	if err != nil {
		return err
	}
	fmt.Fprintf(u.Stdout, "writing config: %s\n", configPath)
	if err := u.FileSystem.WriteFile(configPath, configData, 0o644); err != nil {
		return fmt.Errorf("write config file: %w", err)
	}

	if err := removeWorkspaceTargets(u.FileSystem, candidates); err != nil {
		return err
	}

	for _, skippedDir := range skippedDirs {
		fmt.Fprintf(u.Stdout, "skipped nested directory: %s\n", skippedDir)
	}
	for _, candidate := range candidates {
		fmt.Fprintf(u.Stdout, "added target: %s\n", candidate.targetPath)
	}
	return nil
}

func (u AddTarget) resolveTargetPaths(workspaceRoot string) ([]string, []string, error) {
	targetPath, err := normalizeEditTargetPath(u.TargetPath)
	if err != nil {
		return nil, nil, err
	}

	workspaceTargetPath := filepath.Join(workspaceRoot, targetPath)
	// TODO: Resolve the candidate path and reject targets whose real path escapes the workspace via symlinks.
	targetInfo, err := u.FileSystem.Stat(workspaceTargetPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, fmt.Errorf("target file does not exist: %s", targetPath)
		}
		return nil, nil, fmt.Errorf("stat target file: %w", err)
	}

	if targetInfo.Mode().IsRegular() {
		return []string{targetPath}, nil, nil
	}

	if !targetInfo.IsDir() {
		return nil, nil, fmt.Errorf("target must be a regular file or directory: %s", targetPath)
	}

	entries, err := u.FileSystem.ReadDir(workspaceTargetPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read target directory: %w", err)
	}

	targetPaths := make([]string, 0, len(entries))
	skippedDirs := []string{}
	for _, entry := range entries {
		childTargetPath := filepath.Join(targetPath, entry.Name())
		childWorkspacePath := filepath.Join(workspaceRoot, childTargetPath)

		childInfo, err := u.FileSystem.Stat(childWorkspacePath)
		if err != nil {
			return nil, nil, fmt.Errorf("stat target file: %w", err)
		}

		switch {
		case childInfo.Mode().IsRegular():
			targetPaths = append(targetPaths, childTargetPath)
		case childInfo.IsDir():
			// Ignore nested directories for the initial directory-add behavior.
			skippedDirs = append(skippedDirs, childTargetPath)
		default:
			return nil, nil, fmt.Errorf("target must be a regular file: %s", childTargetPath)
		}
	}

	if len(targetPaths) == 0 {
		return nil, nil, fmt.Errorf("target directory does not contain direct files: %s", targetPath)
	}

	return targetPaths, skippedDirs, nil
}

func removeWorkspaceTargets(fs addFileSystem, candidates []addCandidate) error {
	var removeErr error

	for _, candidate := range candidates {
		if err := fs.Remove(candidate.workspaceTargetPath); err != nil {
			removeErr = errors.Join(removeErr, fmt.Errorf("remove workspace target %s: %w", candidate.targetPath, err))
		}
	}

	return removeErr
}

func loadConfig(fs configFileSystem) (string, domain.Config, error) {
	homeDir, err := fs.UserHomeDir()
	if err != nil {
		return "", domain.Config{}, fmt.Errorf("resolve home directory: %w", err)
	}

	configPath := filepath.Join(homeDir, ".veil", "config.toml")
	info, err := fs.Stat(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", domain.Config{}, fmt.Errorf("veil is not initialized: %s", configPath)
		}
		return "", domain.Config{}, fmt.Errorf("stat config path: %w", err)
	}

	if info.IsDir() {
		return "", domain.Config{}, fmt.Errorf("config path is a directory: %s", configPath)
	}

	data, err := fs.ReadFile(configPath)
	if err != nil {
		return "", domain.Config{}, fmt.Errorf("read config file: %w", err)
	}

	config, err := domain.ParseConfigTOML(data)
	if err != nil {
		return "", domain.Config{}, fmt.Errorf("parse config file: %w", err)
	}

	return configPath, config, nil
}

func canonicalizeWorkspaceRoots(config domain.Config, fs symlinkEvaluator) domain.Config {
	for id, workspace := range config.Workspaces {
		canonicalRoot, err := fs.EvalSymlinks(workspace.Root)
		if err != nil {
			continue
		}

		workspace.Root = canonicalRoot
		config.Workspaces[id] = workspace
	}

	return config
}

type configFileSystem interface {
	UserHomeDir() (string, error)
	ReadFile(name string) ([]byte, error)
	Stat(name string) (os.FileInfo, error)
}

type symlinkEvaluator interface {
	EvalSymlinks(path string) (string, error)
}
