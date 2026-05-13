package usecase

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
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
	Readlink(name string) (string, error)
	Remove(name string) error
}

type VanishTargets struct {
	FileSystem    vanishFileSystem
	StoreRuntime  EncryptedStoreRuntime
	Stdout        io.Writer
	Now           func() time.Time
	AllWorkspaces bool
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
	now := currentTime(u.Now)
	if err := ensureStoreAvailable(u.StoreRuntime, config, now, u.Stdout, false); err != nil {
		return err
	}

	statePath, state, err := loadState(u.FileSystem)
	if err != nil {
		return err
	}

	workspaces, err := resolveEmergeWorkspaces(u.FileSystem, config, u.AllWorkspaces)
	if err != nil {
		return err
	}

	outputLayout := newVanishOutputLayout(u.AllWorkspaces, workspaces)
	var vanishErr error

	for _, entry := range workspaces {
		workspaceRootMissing := false
		if u.AllWorkspaces {
			if _, err := u.FileSystem.Stat(entry.workspace.Root); err != nil && errors.Is(err, os.ErrNotExist) {
				workspaceRootMissing = true
			}
		}

		for _, target := range entry.workspace.Targets {
			workspaceTargetPath := filepath.Join(entry.workspace.Root, target)
			if workspaceRootMissing {
				if err := state.RemoveLease(entry.id, target); err != nil {
					wrappedErr := wrapVanishTargetError(u.AllWorkspaces, entry.id, target, err)
					outputLayout.writeTargetFailure(u.Stdout, entry.id, target, wrappedErr)
					vanishErr = errors.Join(vanishErr, wrappedErr)
					continue
				}

				outputLayout.writeMissingWorkspaceTarget(u.Stdout, entry.id, target, entry.workspace.Root)
				continue
			}

			storeTargetPath, err := config.StoreTargetPath(entry.id, target)
			if err != nil {
				if u.AllWorkspaces {
					wrappedErr := wrapVanishTargetError(u.AllWorkspaces, entry.id, target, err)
					outputLayout.writeTargetFailure(u.Stdout, entry.id, target, wrappedErr)
					vanishErr = errors.Join(vanishErr, wrappedErr)
					continue
				}
				return wrapVanishTargetError(u.AllWorkspaces, entry.id, target, err)
			}

			status, err := vanishTarget(u.FileSystem, workspaceTargetPath, storeTargetPath)
			if err != nil {
				wrappedErr := wrapVanishTargetError(u.AllWorkspaces, entry.id, target, err)
				if u.AllWorkspaces {
					outputLayout.writeTargetFailure(u.Stdout, entry.id, target, wrappedErr)
					vanishErr = errors.Join(vanishErr, wrappedErr)
					continue
				}
				return wrappedErr
			}

			if err := state.RemoveLease(entry.id, target); err != nil {
				wrappedErr := wrapVanishTargetError(u.AllWorkspaces, entry.id, target, err)
				if u.AllWorkspaces {
					outputLayout.writeTargetFailure(u.Stdout, entry.id, target, wrappedErr)
					vanishErr = errors.Join(vanishErr, wrappedErr)
					continue
				}
				return wrappedErr
			}

			outputLayout.writeTarget(u.Stdout, entry.id, target, status)
		}
	}

	if err := persistState(u.FileSystem, statePath, state); err != nil {
		return err
	}

	if err := unmountStoreIfIdle(u.StoreRuntime, config, state, now, u.Stdout); err != nil {
		return err
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

func vanishTarget(fs vanishFileSystem, workspaceTargetPath, storeTargetPath string) (string, error) {
	info, err := fs.Lstat(workspaceTargetPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "already vanished", nil
		}
		return "", fmt.Errorf("stat workspace target: %w", err)
	}

	// Only Veil-managed symlinks should be removed from the workspace.
	if info.Mode()&os.ModeSymlink == 0 {
		return "skipped", nil
	}

	managed, err := isManagedWorkspaceSymlink(fs, workspaceTargetPath, storeTargetPath)
	if err != nil {
		return "", err
	}
	if !managed {
		return "skipped", nil
	}

	if err := fs.Remove(workspaceTargetPath); err != nil {
		return "", fmt.Errorf("remove workspace symlink: %w", err)
	}

	return "vanished", nil
}

func absoluteLinkTargetPath(workspaceTargetPath, linkTarget string) string {
	if !filepath.IsAbs(linkTarget) {
		linkTarget = filepath.Join(filepath.Dir(workspaceTargetPath), linkTarget)
	}

	return filepath.Clean(linkTarget)
}

func isManagedWorkspaceSymlink(fs vanishFileSystem, workspaceTargetPath, storeTargetPath string) (bool, error) {
	linkTarget, err := fs.Readlink(workspaceTargetPath)
	if err != nil {
		return false, fmt.Errorf("read workspace symlink: %w", err)
	}

	absoluteLinkTarget := absoluteLinkTargetPath(workspaceTargetPath, linkTarget)
	absoluteStoreTargetPath := filepath.Clean(storeTargetPath)

	resolvedLinkTarget, err := resolveLinkTarget(fs, workspaceTargetPath, linkTarget)
	if err != nil {
		// Broken managed symlinks should still be removable if they point at the expected store path.
		return absoluteLinkTarget == absoluteStoreTargetPath, nil
	}

	resolvedStoreTargetPath, err := fs.EvalSymlinks(storeTargetPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return absoluteLinkTarget == absoluteStoreTargetPath, nil
		}
		return false, fmt.Errorf("canonicalize store target: %w", err)
	}

	return resolvedLinkTarget == resolvedStoreTargetPath, nil
}
