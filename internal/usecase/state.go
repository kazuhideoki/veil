package usecase

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

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

type stateLock struct {
	file *os.File
}

func (l stateLock) Unlock() error {
	if l.file == nil {
		return nil
	}

	if err := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN); err != nil {
		_ = l.file.Close()
		return err
	}

	return l.file.Close()
}

func statePaths(fs stateReaderFileSystem) (string, string, error) {
	homeDir, err := fs.UserHomeDir()
	if err != nil {
		return "", "", fmt.Errorf("resolve home directory: %w", err)
	}

	statePath := filepath.Join(homeDir, ".veil", "state.toml")
	lockPath := filepath.Join(homeDir, ".veil", "state.lock")
	return statePath, lockPath, nil
}

func lockState(fs stateReaderFileSystem) (stateLock, error) {
	_, lockPath, err := statePaths(fs)
	if err != nil {
		return stateLock{}, err
	}

	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return stateLock{}, fmt.Errorf("create state lock directory: %w", err)
	}

	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return stateLock{}, fmt.Errorf("open state lock file: %w", err)
	}

	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		_ = file.Close()
		return stateLock{}, fmt.Errorf("lock state file: %w", err)
	}

	return stateLock{file: file}, nil
}

func loadState(fs stateReaderFileSystem) (string, domain.State, error) {
	statePath, _, err := statePaths(fs)
	if err != nil {
		return "", domain.State{}, err
	}

	return loadStateAtPath(fs, statePath)
}

func loadStateLocked(fs stateReaderFileSystem) (string, domain.State, stateLock, error) {
	lock, err := lockState(fs)
	if err != nil {
		return "", domain.State{}, stateLock{}, err
	}

	statePath, state, err := loadState(fs)
	if err != nil {
		_ = lock.Unlock()
		return "", domain.State{}, stateLock{}, err
	}

	return statePath, state, lock, nil
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
