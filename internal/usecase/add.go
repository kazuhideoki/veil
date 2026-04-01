package usecase

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

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
	FileSystem     addFileSystem
	TrackedChecker trackedChecker
	Stdout         io.Writer
	TargetPath     string
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

	config.StorePath = expandHomeDir(config.StorePath, homeDir)
	config = canonicalizeWorkspaceRoots(config, u.FileSystem)

	workspaceID, workspace, err := config.ResolveWorkspaceByDir(currentDir)
	if err != nil {
		return err
	}

	targetPaths, skippedDirs, err := u.resolveTargetPaths(workspace.Root)
	if err != nil {
		return err
	}

	candidates, updatedWorkspace, err := u.collectAddCandidates(config, workspaceID, workspace, targetPaths)
	if err != nil {
		return err
	}
	config.Workspaces[workspaceID] = updatedWorkspace

	writtenStorePaths := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if err := u.FileSystem.MkdirAll(filepath.Dir(candidate.storeTargetPath), 0o755); err != nil {
			rollbackAddedStoreTargets(u.FileSystem, writtenStorePaths)
			return fmt.Errorf("create store target directory: %w", err)
		}

		if err := u.FileSystem.WriteFile(candidate.storeTargetPath, candidate.storeData, candidate.storeMode); err != nil {
			rollbackAddedStoreTargets(u.FileSystem, writtenStorePaths)
			return fmt.Errorf("write store target: %w", err)
		}

		writtenStorePaths = append(writtenStorePaths, candidate.storeTargetPath)
	}

	configData, err := config.RenderTOML()
	if err != nil {
		rollbackAddedStoreTargets(u.FileSystem, writtenStorePaths)
		return err
	}

	fmt.Fprintf(u.Stdout, "writing config: %s\n", configPath)
	if err := u.FileSystem.WriteFile(configPath, configData, 0o644); err != nil {
		rollbackAddedStoreTargets(u.FileSystem, writtenStorePaths)
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

func (u AddTarget) collectAddCandidates(config domain.Config, workspaceID string, workspace domain.Workspace, targetPaths []string) ([]addCandidate, domain.Workspace, error) {
	updatedWorkspace := workspace
	candidates := make([]addCandidate, 0, len(targetPaths))

	// Validate the full batch before mutating the store so directory adds stay all-or-nothing.
	for _, targetPath := range targetPaths {
		if err := updatedWorkspace.AddTarget(targetPath); err != nil {
			return nil, workspace, err
		}

		workspaceTargetPath := filepath.Join(workspace.Root, targetPath)
		targetInfo, err := u.FileSystem.Stat(workspaceTargetPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, workspace, fmt.Errorf("target file does not exist: %s", targetPath)
			}
			return nil, workspace, fmt.Errorf("stat target file: %w", err)
		}

		if !targetInfo.Mode().IsRegular() {
			return nil, workspace, fmt.Errorf("target must be a regular file: %s", targetPath)
		}

		isTracked, err := u.TrackedChecker.IsTracked(workspace.Root, targetPath)
		if err != nil {
			return nil, workspace, fmt.Errorf("check git tracking: %w", err)
		}

		if isTracked {
			return nil, workspace, fmt.Errorf("target is tracked by git: %s", targetPath)
		}

		storeTargetPath, err := config.StoreTargetPath(workspaceID, targetPath)
		if err != nil {
			return nil, workspace, err
		}

		if _, err := u.FileSystem.Stat(storeTargetPath); err == nil {
			return nil, workspace, fmt.Errorf("store target already exists: %s", targetPath)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, workspace, fmt.Errorf("stat store target: %w", err)
		}

		targetData, err := u.FileSystem.ReadFile(workspaceTargetPath)
		if err != nil {
			return nil, workspace, fmt.Errorf("read target file: %w", err)
		}

		candidates = append(candidates, addCandidate{
			targetPath:          targetPath,
			workspaceTargetPath: workspaceTargetPath,
			storeTargetPath:     storeTargetPath,
			storeMode:           targetInfo.Mode().Perm(),
			storeData:           targetData,
		})
	}

	return candidates, updatedWorkspace, nil
}

func rollbackAddedStoreTargets(fs addFileSystem, storeTargetPaths []string) {
	for i := len(storeTargetPaths) - 1; i >= 0; i-- {
		_ = fs.Remove(storeTargetPaths[i])
	}
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
