package infra

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type OSFileSystem struct{}

func (OSFileSystem) UserHomeDir() (string, error) {
	return os.UserHomeDir()
}

func (OSFileSystem) MkdirAll(path string, perm os.FileMode) error {
	return os.MkdirAll(path, perm)
}

func (OSFileSystem) Getwd() (string, error) {
	return os.Getwd()
}

func (OSFileSystem) EvalSymlinks(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}
func (OSFileSystem) ReadFile(name string) ([]byte, error) {
	return os.ReadFile(name)
}

func (OSFileSystem) WriteFile(name string, data []byte, perm os.FileMode) error {
	return os.WriteFile(name, data, perm)
}

func (OSFileSystem) Stat(name string) (os.FileInfo, error) {
	return os.Stat(name)
}

func (OSFileSystem) Remove(name string) error {
	return os.Remove(name)
}

type GitCLI struct{}

func (GitCLI) IsTracked(workspaceRoot, relativePath string) (bool, error) {
	cmd := exec.Command("git", "-C", workspaceRoot, "ls-files", "--error-unmatch", "--", relativePath)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderrText := stderr.String()
			if exitErr.ExitCode() == 1 {
				return false, nil
			}

			// Non-git directories should behave like an untracked workspace.
			if strings.Contains(stderrText, "not a git repository") {
				return false, nil
			}
		}

		return false, err
	}

	return true, nil
}
