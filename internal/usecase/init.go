package usecase

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/kazuhideoki/veil/internal/domain"
)

type fileSystem interface {
	UserHomeDir() (string, error)
	Getwd() (string, error)
	MkdirAll(path string, perm os.FileMode) error
	ReadFile(name string) ([]byte, error)
	WriteFile(name string, data []byte, perm os.FileMode) error
	Stat(name string) (os.FileInfo, error)
}

type InitConfig struct {
	FileSystem  fileSystem
	Stdout      io.Writer
	WorkspaceID string
}

func (u InitConfig) Run() error {
	homeDir, err := u.FileSystem.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}

	currentDir, err := u.FileSystem.Getwd()
	if err != nil {
		return fmt.Errorf("resolve current directory: %w", err)
	}

	configDir := filepath.Join(homeDir, ".veil")
	configPath := filepath.Join(configDir, "config.toml")
	workspaceID := u.WorkspaceID
	if workspaceID == "" {
		workspaceID = filepath.Base(currentDir)
	}

	config := domain.DefaultConfig()
	configCreated := false

	if info, err := u.FileSystem.Stat(configPath); err == nil {
		if info.IsDir() {
			return fmt.Errorf("config path is a directory: %s", configPath)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat config path: %w", err)
	} else {
		configCreated = true

		fmt.Fprintf(u.Stdout, "creating config directory: %s\n", configDir)
		if err := u.FileSystem.MkdirAll(configDir, 0o755); err != nil {
			return fmt.Errorf("create config directory: %w", err)
		}
	}

	if !configCreated {
		data, err := u.FileSystem.ReadFile(configPath)
		if err != nil {
			return fmt.Errorf("read config file: %w", err)
		}

		config, err = domain.ParseConfigTOML(data)
		if err != nil {
			return fmt.Errorf("parse config file: %w", err)
		}
	}

	if err := config.AddWorkspace(workspaceID, currentDir); err != nil {
		return err
	}

	configData, err := config.RenderTOML()
	if err != nil {
		return err
	}

	fmt.Fprintf(u.Stdout, "writing config: %s\n", configPath)
	if err := u.FileSystem.WriteFile(configPath, configData, 0o644); err != nil {
		return fmt.Errorf("write config file: %w", err)
	}

	if configCreated {
		fmt.Fprintf(u.Stdout, "initialized config: %s\n", configPath)
	}
	fmt.Fprintf(u.Stdout, "added workspace: %s\n", workspaceID)

	return nil
}
