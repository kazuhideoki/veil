package usecase

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

type editFileSystem interface {
	UserHomeDir() (string, error)
	Getwd() (string, error)
	EvalSymlinks(path string) (string, error)
	ReadFile(name string) ([]byte, error)
	Stat(name string) (os.FileInfo, error)
}

type editorRunner interface {
	Run(editorPath string, editorArgs []string, targetPath string) error
}

type EditTarget struct {
	FileSystem   editFileSystem
	EditorRunner editorRunner
	EditorPath   string
	TargetPath   string
}

func (u EditTarget) Run() error {
	editorPath, editorArgs, err := parseEditorCommand(u.EditorPath)
	if err != nil {
		return err
	}

	_, config, err := loadConfig(u.FileSystem)
	if err != nil {
		return err
	}

	currentDir, err := u.FileSystem.Getwd()
	if err != nil {
		return fmt.Errorf("resolve current directory: %w", err)
	}

	currentDir, err = u.FileSystem.EvalSymlinks(currentDir)
	if err != nil {
		return fmt.Errorf("canonicalize current directory: %w", err)
	}

	homeDir, err := u.FileSystem.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}

	config.StorePath = expandHomeDir(config.StorePath, homeDir)
	config = canonicalizeWorkspaceRoots(config, u.FileSystem)

	workspaceID, workspace, err := config.ResolveWorkspaceByDir(currentDir)
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

	storeTargetPath, err := config.StoreTargetPath(workspaceID, targetPath)
	if err != nil {
		return err
	}

	targetInfo, err := u.FileSystem.Stat(storeTargetPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("store target does not exist: %s", targetPath)
		}
		return fmt.Errorf("stat store target: %w", err)
	}

	if !targetInfo.Mode().IsRegular() {
		return fmt.Errorf("store target must be a regular file: %s", targetPath)
	}

	if err := u.EditorRunner.Run(editorPath, editorArgs, filepath.Clean(storeTargetPath)); err != nil {
		return fmt.Errorf("run editor: %w", err)
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
