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
	MkdirAll(path string, perm os.FileMode) error
	WriteFile(name string, data []byte, perm os.FileMode) error
	Stat(name string) (os.FileInfo, error)
}

type InitConfig struct {
	FileSystem fileSystem
	Stdout     io.Writer
}

func (u InitConfig) Run() error {
	homeDir, err := u.FileSystem.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}

	configDir := filepath.Join(homeDir, ".veil")
	configPath := filepath.Join(configDir, "config.toml")

	if info, err := u.FileSystem.Stat(configPath); err == nil {
		if info.IsDir() {
			return fmt.Errorf("config path is a directory: %s", configPath)
		}

		fmt.Fprintf(u.Stdout, "config already exists: %s\n", configPath)
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat config path: %w", err)
	}

	fmt.Fprintf(u.Stdout, "creating config directory: %s\n", configDir)
	if err := u.FileSystem.MkdirAll(configDir, 0o755); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}

	fmt.Fprintf(u.Stdout, "writing config: %s\n", configPath)
	if err := u.FileSystem.WriteFile(configPath, []byte(domain.DefaultConfigTOML()), 0o644); err != nil {
		return fmt.Errorf("write config file: %w", err)
	}

	fmt.Fprintf(u.Stdout, "initialized config: %s\n", configPath)
	return nil
}
