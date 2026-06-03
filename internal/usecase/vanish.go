package usecase

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/kazuhideoki/veil/internal/domain"
)

type vanishFileSystem interface {
	UserHomeDir() (string, error)
	Getwd() (string, error)
	EvalSymlinks(path string) (string, error)
	MkdirAll(path string, perm os.FileMode) error
	ReadFile(name string) ([]byte, error)
	WriteFile(name string, data []byte, perm os.FileMode) error
	Rename(oldpath, newpath string) error
	Stat(name string) (os.FileInfo, error)
	Lstat(name string) (os.FileInfo, error)
	Remove(name string) error
}

type VanishTargets struct {
	FileSystem      vanishFileSystem
	DocumentRuntime OnePasswordDocumentRuntime
	Stdout          io.Writer
	Now             func() time.Time
	AllWorkspaces   bool
	Commit          bool
	Discard         bool
}

func (u VanishTargets) Run() error {
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
	now := currentTime(u.Now)

	statePath, state, err := loadState(u.FileSystem)
	if err != nil {
		return err
	}

	workspaces, err := resolveEmergeWorkspaces(u.FileSystem, config, u.AllWorkspaces)
	if err != nil {
		return err
	}

	return u.vanishOnePasswordDocuments(config, statePath, state, workspaces, now)
}

func (u VanishTargets) vanishOnePasswordDocuments(config domain.Config, statePath string, state domain.State, workspaces []emergeWorkspace, now time.Time) error {
	if u.Commit && u.Discard {
		return fmt.Errorf("vanish accepts only one of --commit or --discard")
	}
	if u.Commit {
		if err := requireOnePasswordRuntime(u.DocumentRuntime); err != nil {
			return err
		}
	}

	outputLayout := newVanishOutputLayout(u.AllWorkspaces, workspaces)
	var vanishErr error
	configChanged := false

	for _, entry := range workspaces {
		ttl, err := config.EffectiveTTL(entry.workspace)
		if err != nil {
			return err
		}
		for _, target := range entry.workspace.Targets {
			workspaceTargetPath := filepath.Join(entry.workspace.Root, target)
			lease, hasLease, err := state.FindLease(entry.id, target)
			if err != nil {
				return err
			}
			info, statErr := u.FileSystem.Lstat(workspaceTargetPath)
			if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
				wrappedErr := wrapVanishTargetError(u.AllWorkspaces, entry.id, target, fmt.Errorf("stat workspace target: %w", statErr))
				if u.AllWorkspaces {
					outputLayout.writeTargetFailure(u.Stdout, entry.id, target, wrappedErr)
					vanishErr = errors.Join(vanishErr, wrappedErr)
					continue
				}
				return wrappedErr
			}
			if errors.Is(statErr, os.ErrNotExist) {
				if err := state.RemoveLease(entry.id, target); err != nil {
					return err
				}
				outputLayout.writeTarget(u.Stdout, entry.id, target, "already vanished")
				continue
			}
			if !hasLease {
				wrappedErr := wrapVanishTargetError(u.AllWorkspaces, entry.id, target, fmt.Errorf("workspace target is not emerged by Veil: %s", target))
				if u.AllWorkspaces {
					outputLayout.writeTargetFailure(u.Stdout, entry.id, target, wrappedErr)
					vanishErr = errors.Join(vanishErr, wrappedErr)
					continue
				}
				return wrappedErr
			}
			if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
				wrappedErr := wrapVanishTargetError(u.AllWorkspaces, entry.id, target, fmt.Errorf("workspace target must be a Veil materialized regular file"))
				if u.AllWorkspaces {
					outputLayout.writeTargetFailure(u.Stdout, entry.id, target, wrappedErr)
					vanishErr = errors.Join(vanishErr, wrappedErr)
					continue
				}
				return wrappedErr
			}
			data, err := u.FileSystem.ReadFile(workspaceTargetPath)
			if err != nil {
				return fmt.Errorf("read workspace target: %w", err)
			}
			hash := sha256Hex(data)
			if lease.PlaintextHash == "" && !u.Discard {
				wrappedErr := wrapVanishTargetError(u.AllWorkspaces, entry.id, target, fmt.Errorf("workspace target has no recorded plaintext hash; rerun with --discard to remove it"))
				if u.AllWorkspaces {
					outputLayout.writeTargetFailure(u.Stdout, entry.id, target, wrappedErr)
					vanishErr = errors.Join(vanishErr, wrappedErr)
					continue
				}
				return wrappedErr
			}
			modified := hasLease && lease.PlaintextHash != "" && hash != lease.PlaintextHash
			if modified && !u.Commit && !u.Discard {
				wrappedErr := wrapVanishTargetError(u.AllWorkspaces, entry.id, target, fmt.Errorf("workspace target has uncommitted changes; rerun with --commit or --discard"))
				if u.AllWorkspaces {
					outputLayout.writeTargetFailure(u.Stdout, entry.id, target, wrappedErr)
					vanishErr = errors.Join(vanishErr, wrappedErr)
					continue
				}
				return wrappedErr
			}
			if u.Commit {
				document, ok, err := config.DocumentForTarget(entry.id, target)
				if err == nil && !ok {
					err = fmt.Errorf("1Password document is not registered: %s", target)
				}
				if err != nil {
					return err
				}
				updatedDocument, _, err := updateOnePasswordDocument(u.DocumentRuntime, config, document, data, lease.PlaintextHash)
				if err != nil {
					return fmt.Errorf("%s: %w", target, err)
				}
				if err := config.UpsertDocument(updatedDocument); err != nil {
					return err
				}
				if updatedDocument.Vault != document.Vault {
					configChanged = true
				}
				if err := updateLeaseHash(&state, entry.id, target, workspaceTargetPath, updatedDocument.ItemID, updatedDocument.ContentSHA256, now, ttl); err != nil {
					return err
				}
			}
			if err := removeMaterializedTarget(u.FileSystem, workspaceTargetPath); err != nil {
				return fmt.Errorf("%s: %w", target, err)
			}
			if err := state.RemoveLease(entry.id, target); err != nil {
				return err
			}
			outputLayout.writeTarget(u.Stdout, entry.id, target, "vanished")
		}
	}

	if err := persistState(u.FileSystem, statePath, state); err != nil {
		return err
	}
	if configChanged {
		configPath, _, err := loadConfig(u.FileSystem)
		if err != nil {
			return err
		}
		configData, err := config.RenderTOML()
		if err != nil {
			return err
		}
		if err := u.FileSystem.WriteFile(configPath, configData, 0o644); err != nil {
			return fmt.Errorf("write config file: %w", err)
		}
	}
	if vanishErr != nil {
		return vanishErr
	}
	return nil
}

type vanishOutputLayout struct {
	allWorkspaces  bool
	actionWidth    int
	workspaceWidth int
}

func newVanishOutputLayout(allWorkspaces bool, workspaces []emergeWorkspace) vanishOutputLayout {
	layout := vanishOutputLayout{
		allWorkspaces: allWorkspaces,
		actionWidth:   len("already vanished"),
	}
	if !allWorkspaces {
		return layout
	}

	for _, entry := range workspaces {
		if len(entry.id) > layout.workspaceWidth {
			layout.workspaceWidth = len(entry.id)
		}
	}

	return layout
}

func (l vanishOutputLayout) writeTarget(w io.Writer, workspaceID, target, status string) {
	if !l.allWorkspaces {
		fmt.Fprintf(w, "%s target: %s\n", status, target)
		return
	}

	fmt.Fprintf(w, "%-*s  repo: %-*s  file: %s\n", l.actionWidth, status, l.workspaceWidth, workspaceID, target)
}

func (l vanishOutputLayout) writeTargetFailure(w io.Writer, workspaceID, target string, err error) {
	if !l.allWorkspaces {
		fmt.Fprintf(w, "failed target: %s  error: %v\n", target, err)
		return
	}

	fmt.Fprintf(w, "%-*s  repo: %-*s  file: %s  error: %v\n", l.actionWidth, "failed", l.workspaceWidth, workspaceID, target, err)
}

func (l vanishOutputLayout) writeMissingWorkspaceTarget(w io.Writer, workspaceID, target, workspaceRoot string) {
	if !l.allWorkspaces {
		fmt.Fprintf(w, "missing root target: %s  workspace: %s  note: target not inspected; lease cleared\n", target, workspaceRoot)
		return
	}

	fmt.Fprintf(w, "%-*s  repo: %-*s  file: %s  workspace: %s  note: target not inspected; lease cleared\n", l.actionWidth, "missing root", l.workspaceWidth, workspaceID, target, workspaceRoot)
}

func wrapVanishTargetError(allWorkspaces bool, workspaceID, target string, err error) error {
	if !allWorkspaces {
		return fmt.Errorf("%s: %w", target, err)
	}

	return fmt.Errorf("%s: %w", emergeTargetLabel(allWorkspaces, workspaceID, target), err)
}
