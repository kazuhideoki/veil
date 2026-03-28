package domain

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const (
	DefaultStorePath = "~/Library/Mobile Documents/com~apple~CloudDocs/VeilStore"
	DefaultTTL       = "24h"
)

type Config struct {
	Version    int
	StorePath  string
	DefaultTTL string
	Workspaces map[string]Workspace
}

type Workspace struct {
	Root    string
	Targets []string
	TTL     string
}

func DefaultConfig() Config {
	return Config{
		Version:    1,
		StorePath:  DefaultStorePath,
		DefaultTTL: DefaultTTL,
		Workspaces: map[string]Workspace{},
	}
}

func (c *Config) AddWorkspace(id, root string) error {
	if id == "" {
		return fmt.Errorf("workspace id must not be empty")
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

func (c Config) RenderTOML() string {
	var builder strings.Builder

	fmt.Fprintf(&builder, "version = %d\n", c.Version)
	fmt.Fprintf(&builder, "store_path = %q\n", c.StorePath)
	fmt.Fprintf(&builder, "default_ttl = %q\n", c.DefaultTTL)

	ids := make([]string, 0, len(c.Workspaces))
	for id := range c.Workspaces {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		workspace := c.Workspaces[id]
		builder.WriteString("\n")
		fmt.Fprintf(&builder, "[workspaces.%s]\n", id)
		fmt.Fprintf(&builder, "root = %q\n", workspace.Root)
		fmt.Fprintf(&builder, "targets = %s\n", renderStringArray(workspace.Targets))
		if workspace.TTL != "" {
			fmt.Fprintf(&builder, "ttl = %q\n", workspace.TTL)
		}
	}

	return builder.String()
}

func ParseConfigTOML(data []byte) (Config, error) {
	config := DefaultConfig()
	var currentWorkspaceID string

	lines := strings.Split(string(data), "\n")
	for index, rawLine := range lines {
		lineNumber := index + 1
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section := strings.TrimSuffix(strings.TrimPrefix(line, "["), "]")
			if !strings.HasPrefix(section, "workspaces.") {
				return Config{}, fmt.Errorf("line %d: unsupported section %q", lineNumber, section)
			}

			currentWorkspaceID = strings.TrimPrefix(section, "workspaces.")
			if currentWorkspaceID == "" {
				return Config{}, fmt.Errorf("line %d: workspace id must not be empty", lineNumber)
			}

			if _, exists := config.Workspaces[currentWorkspaceID]; exists {
				return Config{}, fmt.Errorf("line %d: duplicate workspace %q", lineNumber, currentWorkspaceID)
			}

			config.Workspaces[currentWorkspaceID] = Workspace{}
			continue
		}

		key, value, err := splitKeyValue(line)
		if err != nil {
			return Config{}, fmt.Errorf("line %d: %w", lineNumber, err)
		}

		if currentWorkspaceID == "" {
			if err := assignTopLevelConfig(&config, key, value); err != nil {
				return Config{}, fmt.Errorf("line %d: %w", lineNumber, err)
			}
			continue
		}

		workspace := config.Workspaces[currentWorkspaceID]
		if err := assignWorkspaceConfig(&workspace, key, value); err != nil {
			return Config{}, fmt.Errorf("line %d: %w", lineNumber, err)
		}
		config.Workspaces[currentWorkspaceID] = workspace
	}

	return config, nil
}

func assignTopLevelConfig(config *Config, key, value string) error {
	switch key {
	case "version":
		version, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid version %q", value)
		}
		config.Version = version
		return nil
	case "store_path":
		parsed, err := parseQuotedString(value)
		if err != nil {
			return fmt.Errorf("invalid store_path: %w", err)
		}
		config.StorePath = parsed
		return nil
	case "default_ttl":
		parsed, err := parseQuotedString(value)
		if err != nil {
			return fmt.Errorf("invalid default_ttl: %w", err)
		}
		config.DefaultTTL = parsed
		return nil
	default:
		return fmt.Errorf("unsupported top-level key %q", key)
	}
}

func assignWorkspaceConfig(workspace *Workspace, key, value string) error {
	switch key {
	case "root":
		parsed, err := parseQuotedString(value)
		if err != nil {
			return fmt.Errorf("invalid root: %w", err)
		}
		workspace.Root = parsed
		return nil
	case "targets":
		parsed, err := parseStringArray(value)
		if err != nil {
			return fmt.Errorf("invalid targets: %w", err)
		}
		workspace.Targets = parsed
		return nil
	case "ttl":
		parsed, err := parseQuotedString(value)
		if err != nil {
			return fmt.Errorf("invalid ttl: %w", err)
		}
		workspace.TTL = parsed
		return nil
	default:
		return fmt.Errorf("unsupported workspace key %q", key)
	}
}

func splitKeyValue(line string) (string, string, error) {
	parts := strings.SplitN(line, "=", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("expected key = value")
	}

	key := strings.TrimSpace(parts[0])
	value := strings.TrimSpace(parts[1])
	if key == "" {
		return "", "", fmt.Errorf("key must not be empty")
	}

	return key, value, nil
}

func parseQuotedString(value string) (string, error) {
	parsed, err := strconv.Unquote(value)
	if err != nil {
		return "", err
	}
	return parsed, nil
}

func parseStringArray(value string) ([]string, error) {
	value = strings.TrimSpace(value)
	if value == "[]" {
		return []string{}, nil
	}

	if !strings.HasPrefix(value, "[") || !strings.HasSuffix(value, "]") {
		return nil, fmt.Errorf("expected array")
	}

	content := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(value, "["), "]"))
	if content == "" {
		return []string{}, nil
	}

	parts := strings.Split(content, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		item, err := parseQuotedString(strings.TrimSpace(part))
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}

	return result, nil
}

func renderStringArray(values []string) string {
	if len(values) == 0 {
		return "[]"
	}

	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, strconv.Quote(value))
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}
