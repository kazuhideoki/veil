package usecase

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/kazuhideoki/veil/internal/domain"
)

type editFileSystem interface {
	UserHomeDir() (string, error)
	Getwd() (string, error)
	EvalSymlinks(path string) (string, error)
	ReadFile(name string) ([]byte, error)
	WriteFile(name string, data []byte, perm os.FileMode) error
	MkdirAll(path string, perm os.FileMode) error
	Lstat(name string) (os.FileInfo, error)
	Stat(name string) (os.FileInfo, error)
	Remove(name string) error
	Rename(oldpath, newpath string) error
}

type editorRunner interface {
	Run(editorPath string, editorArgs []string, targetPath string) error
}

type EditTarget struct {
	FileSystem      editFileSystem
	DocumentRuntime OnePasswordDocumentRuntime
	EditorRunner    editorRunner
	EditorPath      string
	TargetPath      string
	Now             func() time.Time
}

func (u EditTarget) Run() error {
	_, config, err := loadConfig(u.FileSystem)
	if err != nil {
		return err
	}

	homeDir, err := u.FileSystem.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}

	config = expandConfigPaths(config, homeDir)
	config = canonicalizeWorkspaceRoots(config, u.FileSystem)
	if err := requireOnePasswordConfig(config); err != nil {
		return err
	}
	return u.editOnePasswordDocument(config)
}

func (u EditTarget) editOnePasswordDocument(config domain.Config) error {
	if err := requireOnePasswordRuntime(u.DocumentRuntime); err != nil {
		return err
	}
	editorPath, editorArgs, err := parseEditorCommand(u.EditorPath)
	if err != nil {
		return err
	}
	workspaceID, workspace, err := currentWorkspace(config, u.FileSystem)
	if err != nil {
		return err
	}
	targetPath, err := normalizeEditTargetPath(u.TargetPath)
	if err != nil {
		return err
	}
	if !hasTarget(workspace.Targets, targetPath) {
		return fmt.Errorf("target is not registered: %s", targetPath)
	}
	document, ok, err := config.DocumentForTarget(workspaceID, targetPath)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("1Password document is not registered: %s", targetPath)
	}
	vault := onePasswordVault(config, document)
	data, err := u.DocumentRuntime.ReadDocument(vault, document.ItemID)
	if err != nil {
		return fmt.Errorf("read 1Password document: %w", err)
	}

	homeDir, err := u.FileSystem.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}
	tempDir := filepath.Join(homeDir, ".veil", "tmp")
	if err := u.FileSystem.MkdirAll(tempDir, 0o700); err != nil {
		return fmt.Errorf("create temp directory: %w", err)
	}
	tempPath := filepath.Join(tempDir, sanitizeTempPart(workspaceID)+"-"+sanitizeTempPart(targetPath))
	if err := u.FileSystem.WriteFile(tempPath, data, 0o600); err != nil {
		return fmt.Errorf("write temporary edit file: %w", err)
	}
	defer func() {
		_ = u.FileSystem.Remove(tempPath)
	}()

	if err := u.EditorRunner.Run(editorPath, editorArgs, tempPath); err != nil {
		return fmt.Errorf("run editor: %w", err)
	}
	editedData, err := u.FileSystem.ReadFile(tempPath)
	if err != nil {
		return fmt.Errorf("read temporary edit file: %w", err)
	}
	if sha256Hex(editedData) == sha256Hex(data) {
		return nil
	}

	workspaceTargetPath := filepath.Join(workspace.Root, targetPath)
	_, state, err := loadState(u.FileSystem)
	if err != nil {
		return err
	}
	statePath, err := statePath(u.FileSystem)
	if err != nil {
		return err
	}
	lease, hasLease, err := state.FindLease(workspaceID, targetPath)
	if err != nil {
		return err
	}
	if hasLease {
		workspaceData, err := validateOnePasswordMaterializedTarget(u.FileSystem, lease, workspaceTargetPath, targetPath, document.ItemID, currentTime(u.Now))
		if err != nil {
			return err
		}
		if sha256Hex(workspaceData) != lease.PlaintextHash {
			return fmt.Errorf("workspace target has uncommitted changes; run veil update %s before edit", targetPath)
		}
	}

	document.ContentSHA256 = sha256Hex(data)
	updatedDocument, _, err := updateOnePasswordDocument(u.DocumentRuntime, config, document, editedData)
	if err != nil {
		return fmt.Errorf("%s: %w", targetPath, err)
	}

	if hasLease {
		if err := u.FileSystem.WriteFile(workspaceTargetPath, editedData, 0o600); err != nil {
			return fmt.Errorf("sync workspace target: %w", err)
		}
		ttl, err := config.EffectiveTTL(workspace)
		if err != nil {
			return err
		}
		if err := updateLeaseHash(&state, workspaceID, targetPath, workspaceTargetPath, updatedDocument.ItemID, updatedDocument.ContentSHA256, currentTime(u.Now), ttl); err != nil {
			return err
		}
		if err := persistState(u.FileSystem, statePath, state); err != nil {
			return err
		}
	}

	configPath, _, err := loadConfig(u.FileSystem)
	if err != nil {
		return err
	}
	updatedDocument.Vault = vault
	if err := config.UpsertDocument(updatedDocument); err != nil {
		return err
	}
	configData, err := config.RenderTOML()
	if err != nil {
		return err
	}
	if err := u.FileSystem.WriteFile(configPath, configData, 0o644); err != nil {
		return fmt.Errorf("write config file: %w", err)
	}
	return nil
}

func hasTarget(targets []string, want string) bool {
	for _, target := range targets {
		if target == want {
			return true
		}
	}

	return false
}

func normalizeEditTargetPath(target string) (string, error) {
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

func parseEditorCommand(editorCommand string) (string, []string, error) {
	fields, err := splitShellWords(editorCommand)
	if err != nil {
		return "", nil, err
	}

	if len(fields) == 0 {
		return "", nil, fmt.Errorf("EDITOR is not set")
	}

	return fields[0], fields[1:], nil
}

func splitShellWords(input string) ([]string, error) {
	var (
		fields      []string
		current     []rune
		inSingle    bool
		inDouble    bool
		escaping    bool
		tokenActive bool
	)

	flush := func() {
		fields = append(fields, string(current))
		current = current[:0]
		tokenActive = false
	}

	for _, r := range input {
		switch {
		case escaping:
			current = append(current, r)
			escaping = false
			tokenActive = true
		case inSingle:
			if r == '\'' {
				inSingle = false
				continue
			}
			current = append(current, r)
			tokenActive = true
		case inDouble:
			switch r {
			case '"':
				inDouble = false
			case '\\':
				escaping = true
			default:
				current = append(current, r)
				tokenActive = true
			}
		default:
			switch {
			case unicode.IsSpace(r):
				if tokenActive {
					flush()
				}
			case r == '\'':
				inSingle = true
				tokenActive = true
			case r == '"':
				inDouble = true
				tokenActive = true
			case r == '\\':
				escaping = true
				tokenActive = true
			default:
				current = append(current, r)
				tokenActive = true
			}
		}
	}

	if escaping {
		return nil, fmt.Errorf("EDITOR has unfinished escape sequence")
	}

	if inSingle || inDouble {
		return nil, fmt.Errorf("EDITOR has unterminated quote")
	}

	if tokenActive {
		flush()
	}

	return fields, nil
}
