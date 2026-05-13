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
	DefaultStorePath       = "~/Library/Mobile Documents/com~apple~CloudDocs/VeilStore"
	DefaultStoreBackend    = "plain"
	EncryptedVolumeBackend = "encrypted_volume"
	DefaultStoreID         = "default"
	DefaultTTL             = "24h"
)

type Config struct {
	Version     int                  `toml:"version"`
	StorePath   string               `toml:"store_path"`
	DefaultTTL  string               `toml:"default_ttl"`
	Store       StoreConfig          `toml:"store"`
	KeyProvider KeyProviderConfig    `toml:"key_provider"`
	Session     SessionConfig        `toml:"session"`
	Workspaces  map[string]Workspace `toml:"workspaces"`
}

type Workspace struct {
	Root    string   `toml:"root"`
	Targets []string `toml:"targets"`
	TTL     string   `toml:"ttl,omitempty"`
}

type StoreConfig struct {
	Backend    string `toml:"backend"`
	BundlePath string `toml:"bundle_path,omitempty"`
	MountPath  string `toml:"mount_path,omitempty"`
	VolumeName string `toml:"volume_name,omitempty"`
}

type KeyProviderConfig struct {
	Type string `toml:"type,omitempty"`
	Ref  string `toml:"ref,omitempty"`
}

type SessionConfig struct {
	Directory  string `toml:"directory,omitempty"`
	StaleAfter string `toml:"stale_after,omitempty"`
}

func DefaultConfig() Config {
	return Config{
		Version:    1,
		StorePath:  DefaultStorePath,
		DefaultTTL: DefaultTTL,
		Store: StoreConfig{
			Backend: DefaultStoreBackend,
		},
		Session: SessionConfig{
			StaleAfter: DefaultTTL,
		},
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
	config.applyDefaults()

	return config, nil
}

func (c Config) RenderTOML() ([]byte, error) {
	if c.Workspaces == nil {
		c.Workspaces = map[string]Workspace{}
	}

	var builder strings.Builder

	c.applyDefaults()
	fmt.Fprintf(&builder, "version = %d\n", c.Version)
	if c.Store.Backend == EncryptedVolumeBackend {
		fmt.Fprintf(&builder, "default_ttl = %s\n", strconv.Quote(c.DefaultTTL))
		builder.WriteString("\n[store]\n")
		fmt.Fprintf(&builder, "backend = %s\n", strconv.Quote(c.Store.Backend))
		fmt.Fprintf(&builder, "bundle_path = %s\n", strconv.Quote(c.Store.BundlePath))
		fmt.Fprintf(&builder, "mount_path = %s\n", strconv.Quote(c.Store.MountPath))
		if c.Store.VolumeName != "" {
			fmt.Fprintf(&builder, "volume_name = %s\n", strconv.Quote(c.Store.VolumeName))
		}
		builder.WriteString("\n[key_provider]\n")
		fmt.Fprintf(&builder, "type = %s\n", strconv.Quote(c.KeyProvider.Type))
		fmt.Fprintf(&builder, "ref = %s\n", strconv.Quote(c.KeyProvider.Ref))
		if c.Session.Directory != "" || c.Session.StaleAfter != "" {
			builder.WriteString("\n[session]\n")
			if c.Session.Directory != "" {
				fmt.Fprintf(&builder, "directory = %s\n", strconv.Quote(c.Session.Directory))
			}
			if c.Session.StaleAfter != "" {
				fmt.Fprintf(&builder, "stale_after = %s\n", strconv.Quote(c.Session.StaleAfter))
			}
		}
	} else {
		fmt.Fprintf(&builder, "store_path = %s\n", strconv.Quote(c.StorePath))
		fmt.Fprintf(&builder, "default_ttl = %s\n", strconv.Quote(c.DefaultTTL))
	}

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

func (c Config) StoreTargetPath(workspaceID, target string) (string, error) {
	if err := validateWorkspaceID(workspaceID); err != nil {
		return "", err
	}

	normalizedTarget, err := normalizeTargetPath(target)
	if err != nil {
		return "", err
	}

	return filepath.Join(c.EffectiveStorePath(), "workspaces", workspaceID, normalizedTarget), nil
}

func (c *Config) applyDefaults() {
	if c.Store.Backend == "" {
		c.Store.Backend = DefaultStoreBackend
	}
	if c.StorePath == "" {
		c.StorePath = DefaultStorePath
	}
	if c.DefaultTTL == "" {
		c.DefaultTTL = DefaultTTL
	}
	if c.Store.Backend == EncryptedVolumeBackend && c.Version < 2 {
		c.Version = 2
	}
	if c.Session.StaleAfter == "" {
		c.Session.StaleAfter = DefaultTTL
	}
}

func (c Config) EffectiveStorePath() string {
	if c.Store.Backend == EncryptedVolumeBackend && c.Store.MountPath != "" {
		return c.Store.MountPath
	}
	return c.StorePath
}

func (c Config) IsEncryptedVolumeStore() bool {
	return c.Store.Backend == EncryptedVolumeBackend
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

func (w *Workspace) RemoveTarget(target string) error {
	normalizedTarget, err := normalizeTargetPath(target)
	if err != nil {
		return err
	}

	for idx, existing := range w.Targets {
		if existing != normalizedTarget {
			continue
		}

		w.Targets = append(w.Targets[:idx], w.Targets[idx+1:]...)
		return nil
	}

	return fmt.Errorf("target does not exist: %s", normalizedTarget)
}

func (c *Config) RemoveWorkspace(id string) error {
	if _, exists := c.Workspaces[id]; !exists {
		return fmt.Errorf("workspace does not exist: %s", id)
	}

	delete(c.Workspaces, id)
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
