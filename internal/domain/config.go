package domain

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

const (
	DefaultStorePath = "~/Library/Mobile Documents/com~apple~CloudDocs/VeilStore"
	DefaultTTL       = "24h"
)

type Config struct {
	Version    int                  `toml:"version"`
	StorePath  string               `toml:"store_path"`
	DefaultTTL string               `toml:"default_ttl"`
	Workspaces map[string]Workspace `toml:"workspaces"`
}

type Workspace struct {
	Root    string   `toml:"root"`
	Targets []string `toml:"targets"`
	TTL     string   `toml:"ttl,omitempty"`
}

func DefaultConfig() Config {
	return Config{
		Version:    1,
		StorePath:  DefaultStorePath,
		DefaultTTL: DefaultTTL,
		Workspaces: map[string]Workspace{},
	}
}

func ParseConfigTOML(data []byte) (Config, error) {
	config := DefaultConfig()
	if err := toml.Unmarshal(data, &config); err != nil {
		return Config{}, err
	}

	if config.Workspaces == nil {
		config.Workspaces = map[string]Workspace{}
	}

	return config, nil
}

func (c Config) RenderTOML() ([]byte, error) {
	if c.Workspaces == nil {
		c.Workspaces = map[string]Workspace{}
	}

	var builder strings.Builder

	fmt.Fprintf(&builder, "version = %d\n", c.Version)
	fmt.Fprintf(&builder, "store_path = %s\n", strconv.Quote(c.StorePath))
	fmt.Fprintf(&builder, "default_ttl = %s\n", strconv.Quote(c.DefaultTTL))

	ids := make([]string, 0, len(c.Workspaces))
	for id := range c.Workspaces {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		workspace := c.Workspaces[id]
		builder.WriteString("\n")
		fmt.Fprintf(&builder, "[workspaces.%s]\n", strconv.Quote(id))
		fmt.Fprintf(&builder, "root = %s\n", strconv.Quote(workspace.Root))
		fmt.Fprintf(&builder, "targets = %s\n", renderStringArray(workspace.Targets))
		if workspace.TTL != "" {
			fmt.Fprintf(&builder, "ttl = %s\n", strconv.Quote(workspace.TTL))
		}
	}

	return []byte(builder.String()), nil
}

func (c *Config) AddWorkspace(id, root string) error {
	if id == "" {
		return fmt.Errorf("workspace id must not be empty")
	}

	if err := validateWorkspaceID(id); err != nil {
		return err
	}
	if root == "" {
		return fmt.Errorf("workspace root must not be empty")
	}

	if _, exists := c.Workspaces[id]; exists {
		return fmt.Errorf("workspace already exists: %s", id)
	}

	for existingID, workspace := range c.Workspaces {
		if workspace.Root == root {
			return fmt.Errorf("workspace root already registered: %s (%s)", root, existingID)
		}
	}

	c.Workspaces[id] = Workspace{
		Root:    root,
		Targets: []string{},
	}
	return nil
}

func validateWorkspaceID(id string) error {
	if id == "." || id == ".." {
		return fmt.Errorf("workspace id must not be a relative path: %s", id)
	}

	if strings.Contains(id, "..") {
		return fmt.Errorf("workspace id must not contain parent directory segments: %s", id)
	}

	if strings.Contains(id, string(filepath.Separator)) {
		return fmt.Errorf("workspace id must not contain path separators: %s", id)
	}

	if filepath.Separator != '/' && strings.Contains(id, "/") {
		return fmt.Errorf("workspace id must not contain path separators: %s", id)
	}

	if filepath.Separator != '\\' && strings.Contains(id, "\\") {
		return fmt.Errorf("workspace id must not contain path separators: %s", id)
	}

	return nil
}

func (c Config) ResolveWorkspaceByDir(dir string) (string, Workspace, error) {
	if dir == "" {
		return "", Workspace{}, fmt.Errorf("workspace directory must not be empty")
	}

	var (
		resolvedID        string
		resolvedWorkspace Workspace
		resolvedRootLen   int
	)

	for id, workspace := range c.Workspaces {
		if !isWithinWorkspaceRoot(dir, workspace.Root) {
			continue
		}

		rootLen := len(workspace.Root)
		if resolvedID == "" || rootLen > resolvedRootLen {
			resolvedID = id
			resolvedWorkspace = workspace
			resolvedRootLen = rootLen
		}
	}

	if resolvedID == "" {
		return "", Workspace{}, fmt.Errorf("workspace is not registered for directory: %s", dir)
	}

	return resolvedID, resolvedWorkspace, nil
}

func (w *Workspace) AddTarget(target string) error {
	normalizedTarget, err := normalizeTargetPath(target)
	if err != nil {
		return err
	}

	for _, existing := range w.Targets {
		if existing == normalizedTarget {
			return fmt.Errorf("target already exists: %s", normalizedTarget)
		}
	}

	w.Targets = append(w.Targets, normalizedTarget)
	sort.Strings(w.Targets)
	return nil
}

func normalizeTargetPath(target string) (string, error) {
	if target == "" {
		return "", fmt.Errorf("target path must not be empty")
	}

	if filepath.IsAbs(target) {
		return "", fmt.Errorf("target path must be relative: %s", target)
	}

	cleanTarget := filepath.Clean(target)
	if cleanTarget == "." {
		return "", fmt.Errorf("target path must not be current directory")
	}

	if cleanTarget == ".." || strings.HasPrefix(cleanTarget, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("target path must stay within workspace: %s", target)
	}

	return cleanTarget, nil
}

func isWithinWorkspaceRoot(dir, root string) bool {
	if root == "" {
		return false
	}

	if dir == root {
		return true
	}

	return strings.HasPrefix(dir, root+string(filepath.Separator))
}
func renderStringArray(values []string) string {
	if len(values) == 0 {
		return "[]"
	}

	items := make([]string, 0, len(values))
	for _, value := range values {
		items = append(items, strconv.Quote(value))
	}

	return "[" + strings.Join(items, ", ") + "]"
}
