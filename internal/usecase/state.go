package usecase

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kazuhideoki/veil/internal/domain"
)

type stateReaderFileSystem interface {
	UserHomeDir() (string, error)
	ReadFile(name string) ([]byte, error)
	Stat(name string) (os.FileInfo, error)
}

type stateFileSystem interface {
	stateReaderFileSystem
	MkdirAll(path string, perm os.FileMode) error
	WriteFile(name string, data []byte, perm os.FileMode) error
	Rename(oldpath, newpath string) error
	Remove(name string) error
}

func statePath(fs stateReaderFileSystem) (string, error) {
	homeDir, err := fs.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}

	return filepath.Join(homeDir, ".veil", "state.toml"), nil
}

func loadState(fs stateReaderFileSystem) (string, domain.State, error) {
	statePath, err := statePath(fs)
	if err != nil {
		return "", domain.State{}, err
	}

	return loadStateAtPath(fs, statePath)
}

func loadStateAtPath(fs stateReaderFileSystem, statePath string) (string, domain.State, error) {
	info, err := fs.Stat(statePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return statePath, domain.DefaultState(), nil
		}
		return "", domain.State{}, fmt.Errorf("stat state path: %w", err)
	}

	if info.IsDir() {
		return "", domain.State{}, fmt.Errorf("state path is a directory: %s", statePath)
	}

	data, err := fs.ReadFile(statePath)
	if err != nil {
		return "", domain.State{}, fmt.Errorf("read state file: %w", err)
	}

	state, err := domain.ParseStateTOML(data)
	if err != nil {
		return "", domain.State{}, fmt.Errorf("parse state file: %w", err)
	}

	return statePath, state, nil
}

func persistState(fs stateFileSystem, statePath string, state domain.State) error {
	stateData, err := state.RenderTOML()
	if err != nil {
		return err
	}

	if err := fs.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}

	tempStatePath := statePath + ".tmp"
	if err := fs.WriteFile(tempStatePath, stateData, 0o644); err != nil {
		return fmt.Errorf("write temporary state file: %w", err)
	}
	defer func() {
		_ = fs.Remove(tempStatePath)
	}()

	if err := fs.Rename(tempStatePath, statePath); err != nil {
		return fmt.Errorf("replace state file: %w", err)
	}

	return nil
}
