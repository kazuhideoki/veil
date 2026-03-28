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

	if err := workspace.AddTarget(u.TargetPath); err != nil {
		return err
	}

	targetPath := filepath.Clean(u.TargetPath)
	workspaceTargetPath := filepath.Join(workspace.Root, targetPath)
	// TODO: Resolve the candidate path and reject targets whose real path escapes the workspace via symlinks.
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

	storeTargetPath := filepath.Join(config.StorePath, "workspaces", workspaceID, targetPath)
	if _, err := u.FileSystem.Stat(storeTargetPath); err == nil {
		return fmt.Errorf("store target already exists: %s", targetPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat store target: %w", err)
	}

	if err := u.FileSystem.MkdirAll(filepath.Dir(storeTargetPath), 0o755); err != nil {
		return fmt.Errorf("create store target directory: %w", err)
	}

	targetData, err := u.FileSystem.ReadFile(workspaceTargetPath)
	if err != nil {
		return fmt.Errorf("read target file: %w", err)
	}

	if err := u.FileSystem.WriteFile(storeTargetPath, targetData, targetInfo.Mode().Perm()); err != nil {
		return fmt.Errorf("write store target: %w", err)
	}

	workspace = config.Workspaces[workspaceID]
	if err := workspace.AddTarget(targetPath); err != nil {
		_ = u.FileSystem.Remove(storeTargetPath)
		return err
	}
	config.Workspaces[workspaceID] = workspace

	configData, err := config.RenderTOML()
	if err != nil {
		_ = u.FileSystem.Remove(storeTargetPath)
		return err
	}

	fmt.Fprintf(u.Stdout, "writing config: %s\n", configPath)
	if err := u.FileSystem.WriteFile(configPath, configData, 0o644); err != nil {
		_ = u.FileSystem.Remove(storeTargetPath)
		return fmt.Errorf("write config file: %w", err)
	}

	if err := u.FileSystem.Remove(workspaceTargetPath); err != nil {
		return fmt.Errorf("remove workspace target: %w", err)
	}

	fmt.Fprintf(u.Stdout, "added target: %s\n", targetPath)
	return nil
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
