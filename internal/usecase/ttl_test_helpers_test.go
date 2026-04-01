package usecase

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kazuhideoki/veil/internal/domain"
)

type stubTTLCleanerStarter struct {
	startCalls int
	err        error
}

func (s *stubTTLCleanerStarter) Start() error {
	s.startCalls++
	return s.err
}

type stubTTLCleanerLock struct {
	acquired bool
	lockErr  error
}

func (l stubTTLCleanerLock) Lock() (bool, error) {
	if l.lockErr != nil {
		return false, l.lockErr
	}

	return l.acquired, nil
}

func (l stubTTLCleanerLock) Unlock() error {
	return nil
}

func writeStateForTest(t *testing.T, path string, state domain.State) {
	t.Helper()

	data, err := state.RenderTOML()
	if err != nil {
		t.Fatalf("RenderTOML() returned error: %v", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}
}

func readStateForTest(t *testing.T, path string) domain.State {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() returned error: %v", err)
	}

	state, err := domain.ParseStateTOML(data)
	if err != nil {
		t.Fatalf("ParseStateTOML() returned error: %v", err)
	}

	return state
}

func mustUpsertLease(t *testing.T, state *domain.State, workspaceID, target string, mountedAt, expiresAt time.Time) {
	t.Helper()

	if err := state.UpsertLease(workspaceID, target, mountedAt, expiresAt); err != nil {
		t.Fatalf("UpsertLease() returned error: %v", err)
	}
}

type failingStateWriteFS struct {
	homeDir       string
	stateWriteErr error
	renameErr     error
}

func (fs failingStateWriteFS) UserHomeDir() (string, error) {
	return fs.homeDir, nil
}

func (fs failingStateWriteFS) Getwd() (string, error) {
	return os.Getwd()
}

func (fs failingStateWriteFS) EvalSymlinks(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}

func (fs failingStateWriteFS) MkdirAll(path string, perm os.FileMode) error {
	return os.MkdirAll(path, perm)
}

func (fs failingStateWriteFS) ReadFile(name string) ([]byte, error) {
	return os.ReadFile(name)
}

func (fs failingStateWriteFS) WriteFile(name string, data []byte, perm os.FileMode) error {
	if strings.Contains(name, filepath.Join(".veil", "state.toml")) {
		return fs.stateWriteErr
	}

	return os.WriteFile(name, data, perm)
}

func (fs failingStateWriteFS) Rename(oldpath, newpath string) error {
	if strings.Contains(newpath, filepath.Join(".veil", "state.toml")) && fs.renameErr != nil {
		return fs.renameErr
	}

	return os.Rename(oldpath, newpath)
}

func (fs failingStateWriteFS) Stat(name string) (os.FileInfo, error) {
	return os.Stat(name)
}

func (fs failingStateWriteFS) Lstat(name string) (os.FileInfo, error) {
	return os.Lstat(name)
}

func (fs failingStateWriteFS) Readlink(name string) (string, error) {
	return os.Readlink(name)
}

func (fs failingStateWriteFS) Symlink(oldname, newname string) error {
	return os.Symlink(oldname, newname)
}

func (fs failingStateWriteFS) Remove(name string) error {
	return os.Remove(name)
}

type failingCleanerStarter struct {
	err error
}

func (s failingCleanerStarter) Start() error {
	return s.err
}

var errCleanerStartFailed = errors.New("cleaner start failed")
