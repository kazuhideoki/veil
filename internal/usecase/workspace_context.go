package usecase

import (
	"fmt"
	"io"
	"os"

	"github.com/kazuhideoki/veil/internal/domain"
)

// activeWorkspaceContext centralizes config loading and active workspace resolution
// so target and workspace commands can share the same application-level setup flow.
type activeWorkspaceFileSystem interface {
	configFileSystem
	Getwd() (string, error)
	EvalSymlinks(path string) (string, error)
	WriteFile(name string, data []byte, perm os.FileMode) error
}

type activeWorkspaceContext struct {
	configPath  string
	config      domain.Config
	workspaceID string
	workspace   domain.Workspace
}

func loadActiveWorkspaceContext(fs activeWorkspaceFileSystem) (activeWorkspaceContext, error) {
	configPath, config, err := loadConfig(fs)
	if err != nil {
		return activeWorkspaceContext{}, err
	}

	currentDir, err := fs.Getwd()
	if err != nil {
		return activeWorkspaceContext{}, fmt.Errorf("resolve current directory: %w", err)
	}

	currentDir, err = fs.EvalSymlinks(currentDir)
	if err != nil {
		return activeWorkspaceContext{}, fmt.Errorf("canonicalize current directory: %w", err)
	}

	homeDir, err := fs.UserHomeDir()
	if err != nil {
		return activeWorkspaceContext{}, fmt.Errorf("resolve home directory: %w", err)
	}

	config.StorePath = expandHomeDir(config.StorePath, homeDir)
	config = canonicalizeWorkspaceRoots(config, fs)

	workspaceID, workspace, err := config.ResolveWorkspaceByDir(currentDir)
	if err != nil {
		return activeWorkspaceContext{}, err
	}

	return activeWorkspaceContext{
		configPath:  configPath,
		config:      config,
		workspaceID: workspaceID,
		workspace:   workspace,
	}, nil
}

func (c *activeWorkspaceContext) persistConfig(fs activeWorkspaceFileSystem, stdout io.Writer) error {
	c.config.Workspaces[c.workspaceID] = c.workspace

	configData, err := c.config.RenderTOML()
	if err != nil {
		return err
	}

	fmt.Fprintf(stdout, "writing config: %s\n", c.configPath)
	if err := fs.WriteFile(c.configPath, configData, 0o644); err != nil {
		return fmt.Errorf("write config file: %w", err)
	}

	return nil
}
