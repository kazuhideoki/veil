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
	DefaultStoreBackend     = OnePasswordBackend
	DefaultOnePasswordVault = "Personal"
	OnePasswordBackend      = "1password_document"
	DefaultStoreID          = "default"
	DefaultTTL              = "24h"
)

type Config struct {
	Version    int                  `toml:"version"`
	DefaultTTL string               `toml:"default_ttl"`
	Store      StoreConfig          `toml:"store"`
	Documents  []DocumentConfig     `toml:"documents,omitempty"`
	Workspaces map[string]Workspace `toml:"workspaces"`
}

type Workspace struct {
	Root    string   `toml:"root"`
	Targets []string `toml:"targets"`
	TTL     string   `toml:"ttl,omitempty"`
}

type StoreConfig struct {
	Backend string `toml:"backend"`
	Vault   string `toml:"vault,omitempty"`
}

type DocumentConfig struct {
	WorkspaceID   string `toml:"workspace_id"`
	Target        string `toml:"target"`
	ItemID        string `toml:"item_id"`
	Vault         string `toml:"vault,omitempty"`
	Title         string `toml:"title,omitempty"`
	ContentSHA256 string `toml:"content_sha256,omitempty"`
}

func DefaultConfig() Config {
	return Config{
		Version:    2,
		DefaultTTL: DefaultTTL,
		Store: StoreConfig{
			Backend: DefaultStoreBackend,
			Vault:   DefaultOnePasswordVault,
		},
		Workspaces: map[string]Workspace{},
	}
}

func ParseConfigTOML(data []byte) (Config, error) {
	var config Config
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
	fmt.Fprintf(&builder, "default_ttl = %s\n", strconv.Quote(c.DefaultTTL))
	builder.WriteString("\n[store]\n")
	fmt.Fprintf(&builder, "backend = %s\n", strconv.Quote(c.Store.Backend))
	if c.Store.Vault != "" {
		fmt.Fprintf(&builder, "vault = %s\n", strconv.Quote(c.Store.Vault))
	}

	documents := append([]DocumentConfig(nil), c.Documents...)
	sort.Slice(documents, func(i, j int) bool {
		if documents[i].WorkspaceID != documents[j].WorkspaceID {
			return documents[i].WorkspaceID < documents[j].WorkspaceID
		}
		return documents[i].Target < documents[j].Target
	})
	for _, document := range documents {
		builder.WriteString("\n[[documents]]\n")
		fmt.Fprintf(&builder, "workspace_id = %s\n", strconv.Quote(document.WorkspaceID))
		fmt.Fprintf(&builder, "target = %s\n", strconv.Quote(document.Target))
		fmt.Fprintf(&builder, "item_id = %s\n", strconv.Quote(document.ItemID))
		if document.Vault != "" {
			fmt.Fprintf(&builder, "vault = %s\n", strconv.Quote(document.Vault))
		}
		if document.Title != "" {
			fmt.Fprintf(&builder, "title = %s\n", strconv.Quote(document.Title))
		}
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

func (c *Config) applyDefaults() {
	if c.Store.Backend == "" {
		c.Store.Backend = DefaultStoreBackend
	}
	if c.Store.Backend == OnePasswordBackend && c.Store.Vault == "" {
		c.Store.Vault = DefaultOnePasswordVault
	}
	if c.DefaultTTL == "" {
		c.DefaultTTL = DefaultTTL
	}
	if c.Store.Backend == OnePasswordBackend && c.Version < 2 {
		c.Version = 2
	}
}

func (c Config) IsOnePasswordStore() bool {
	return c.Store.Backend == OnePasswordBackend
}

func (c Config) DocumentForTarget(workspaceID, target string) (DocumentConfig, bool, error) {
	if err := validateWorkspaceID(workspaceID); err != nil {
		return DocumentConfig{}, false, err
	}
	normalizedTarget, err := normalizeTargetPath(target)
	if err != nil {
		return DocumentConfig{}, false, err
	}
	for _, document := range c.Documents {
		if document.WorkspaceID == workspaceID && document.Target == normalizedTarget {
			return document, true, nil
		}
	}
	return DocumentConfig{}, false, nil
}

func (c *Config) UpsertDocument(document DocumentConfig) error {
	if err := validateWorkspaceID(document.WorkspaceID); err != nil {
		return err
	}
	normalizedTarget, err := normalizeTargetPath(document.Target)
	if err != nil {
		return err
	}
	document.Target = normalizedTarget
	if document.ItemID == "" {
		return fmt.Errorf("document item_id must not be empty")
	}
	for idx, existing := range c.Documents {
		if existing.WorkspaceID == document.WorkspaceID && existing.Target == document.Target {
			c.Documents[idx] = document
			return nil
		}
	}
	c.Documents = append(c.Documents, document)
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
	resolvedID, resolvedWorkspace, ok, err := c.FindWorkspaceByDir(dir)
	if err != nil {
		return "", Workspace{}, err
	}
	if !ok {
		return "", Workspace{}, fmt.Errorf("workspace is not registered for directory: %s", dir)
	}

	return resolvedID, resolvedWorkspace, nil
}

func (c Config) FindWorkspaceByDir(dir string) (string, Workspace, bool, error) {
	if dir == "" {
		return "", Workspace{}, false, fmt.Errorf("workspace directory must not be empty")
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
		return "", Workspace{}, false, nil
	}

	return resolvedID, resolvedWorkspace, true, nil
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

func (c *Config) RemoveWorkspaceDocuments(workspaceID string) error {
	if err := validateWorkspaceID(workspaceID); err != nil {
		return err
	}

	filtered := c.Documents[:0]
	for _, document := range c.Documents {
		if document.WorkspaceID == workspaceID {
			continue
		}
		filtered = append(filtered, document)
	}
	c.Documents = filtered
	return nil
}

func (c *Config) RemoveDocument(workspaceID, target string) error {
	if err := validateWorkspaceID(workspaceID); err != nil {
		return err
	}
	normalizedTarget, err := normalizeTargetPath(target)
	if err != nil {
		return err
	}

	filtered := c.Documents[:0]
	for _, document := range c.Documents {
		if document.WorkspaceID == workspaceID && document.Target == normalizedTarget {
			continue
		}
		filtered = append(filtered, document)
	}
	c.Documents = filtered
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
