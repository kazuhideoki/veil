package usecase

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kazuhideoki/veil/internal/domain"
)

type statusFileSystem interface {
	UserHomeDir() (string, error)
	Getwd() (string, error)
	EvalSymlinks(path string) (string, error)
	ReadFile(name string) ([]byte, error)
	ReadDir(name string) ([]os.DirEntry, error)
	Stat(name string) (os.FileInfo, error)
	Lstat(name string) (os.FileInfo, error)
	Readlink(name string) (string, error)
}

type StatusTargets struct {
	FileSystem         statusFileSystem
	StoreStatusChecker EncryptedStoreStatusChecker
	Stdout             io.Writer
	Now                func() time.Time
}

func (u StatusTargets) Run() error {
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

	config = expandConfigPaths(config, homeDir)
	config = canonicalizeWorkspaceRoots(config, u.FileSystem)

	workspaceID, workspace, err := config.ResolveWorkspaceByDir(currentDir)
	if err != nil {
		return err
	}

	_, state, err := loadState(statusStateFileSystem{statusFileSystem: u.FileSystem})
	if err != nil {
		return err
	}

	now := currentTime(u.Now)
	writeStoreStatus(u.Stdout, u.FileSystem, u.StoreStatusChecker, config, state, now)

	for _, target := range workspace.Targets {
		storeTargetPath, err := config.StoreTargetPath(workspaceID, target)
		if err != nil {
			return err
		}

		workspaceTargetPath := filepath.Join(workspace.Root, target)
		status, err := detectTargetStatus(u.FileSystem, workspaceTargetPath, storeTargetPath)
		if err != nil {
			return fmt.Errorf("%s: %w", target, err)
		}

		lease, ok, err := state.FindLease(workspaceID, target)
		if err != nil {
			return err
		}
		if ok && status == "mounted" && !lease.ExpiresAt.After(now) {
			status = "expired"
		}

		fmt.Fprintf(u.Stdout, "%s target: %s\n", status, target)
	}

	return nil
}

func writeStoreStatus(w io.Writer, fs statusFileSystem, checker EncryptedStoreStatusChecker, config domain.Config, state domain.State, now time.Time) {
	if !config.IsEncryptedVolumeStore() {
		return
	}

	mounted := "no"
	if checker != nil && checker.IsMounted(config) {
		mounted = "yes"
	}
	fmt.Fprintf(w, "Store:\n  backend: %s\n  mounted: %s\n  mount_path: %s\n", config.Store.Backend, mounted, config.Store.MountPath)

	fmt.Fprintln(w, "Local leases:")
	hasLease := false
	for _, lease := range state.Leases {
		leaseStoreID := lease.StoreID
		if leaseStoreID == "" {
			leaseStoreID = domain.DefaultStoreID
		}
		if leaseStoreID != domain.DefaultStoreID || !lease.ExpiresAt.After(now) {
			continue
		}
		hasLease = true
		fmt.Fprintf(w, "  %s %s expires at %s\n", lease.WorkspaceID, lease.Target, lease.ExpiresAt.Format(time.RFC3339))
	}
	if !hasLease {
		fmt.Fprintln(w, "  none")
	}

	writeOtherSessions(w, fs, config, now)
}

func writeOtherSessions(w io.Writer, fs statusFileSystem, config domain.Config, now time.Time) {
	fmt.Fprintln(w, "Other sessions:")
	if config.Session.Directory == "" {
		fmt.Fprintln(w, "  none")
		return
	}
	entries, err := fs.ReadDir(config.Session.Directory)
	if err != nil {
		fmt.Fprintln(w, "  unavailable")
		return
	}
	staleAfter, err := time.ParseDuration(config.Session.StaleAfter)
	if err != nil {
		fmt.Fprintln(w, "  unavailable")
		return
	}
	ownSessionID := readLocalSessionID(fs)
	found := false
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := fs.ReadFile(filepath.Join(config.Session.Directory, entry.Name()))
		if err != nil {
			continue
		}
		var session struct {
			SessionID  string `json:"session_id"`
			StoreID    string `json:"store_id"`
			Host       string `json:"host"`
			LastSeenAt string `json:"last_seen_at"`
		}
		if err := json.Unmarshal(data, &session); err != nil {
			continue
		}
		if session.SessionID != "" && session.SessionID == ownSessionID {
			continue
		}
		if session.StoreID != domain.DefaultStoreID {
			continue
		}
		lastSeen, err := time.Parse(time.RFC3339, session.LastSeenAt)
		if err != nil || !lastSeen.Add(staleAfter).After(now) {
			continue
		}
		found = true
		fmt.Fprintf(w, "  %s last seen %s\n", session.Host, lastSeen.Format(time.RFC3339))
	}
	if !found {
		fmt.Fprintln(w, "  none")
	}
}

func readLocalSessionID(fs statusFileSystem) string {
	homeDir, err := fs.UserHomeDir()
	if err != nil {
		return ""
	}
	data, err := fs.ReadFile(filepath.Join(homeDir, ".veil", "encrypted-volume-session-id"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func detectTargetStatus(fs statusFileSystem, workspaceTargetPath, storeTargetPath string) (string, error) {
	if _, err := fs.Stat(storeTargetPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "missing-source", nil
		}
		return "", fmt.Errorf("stat store target: %w", err)
	}

	info, err := fs.Lstat(workspaceTargetPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "absent", nil
		}
		return "", fmt.Errorf("stat workspace target: %w", err)
	}

	// Regular files and foreign symlinks hide the Veil-managed mount point.
	if info.Mode()&os.ModeSymlink == 0 {
		return "shadowed", nil
	}

	linkTarget, err := fs.Readlink(workspaceTargetPath)
	if err != nil {
		return "", fmt.Errorf("read workspace symlink: %w", err)
	}

	resolvedLinkTarget, err := resolveLinkTarget(fs, workspaceTargetPath, linkTarget)
	if err != nil {
		return "shadowed", nil
	}

	resolvedStoreTargetPath, err := fs.EvalSymlinks(storeTargetPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "missing-source", nil
		}
		return "", fmt.Errorf("canonicalize store target: %w", err)
	}

	if resolvedLinkTarget != resolvedStoreTargetPath {
		return "shadowed", nil
	}

	return "mounted", nil
}

type statusStateFileSystem struct {
	statusFileSystem
}

func (fs statusStateFileSystem) MkdirAll(path string, perm os.FileMode) error {
	return fmt.Errorf("mkdir all is not supported: %s", path)
}

func (fs statusStateFileSystem) WriteFile(name string, data []byte, perm os.FileMode) error {
	return fmt.Errorf("write file is not supported: %s", name)
}

func (fs statusStateFileSystem) Rename(oldpath, newpath string) error {
	return fmt.Errorf("rename is not supported: %s", oldpath)
}

func (fs statusStateFileSystem) Remove(name string) error {
	return fmt.Errorf("remove is not supported: %s", name)
}
