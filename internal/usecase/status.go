package usecase

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
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

	workspaceID, workspace, registered, err := config.FindWorkspaceByDir(currentDir)
	if err != nil {
		return err
	}

	_, state, err := loadState(statusStateFileSystem{statusFileSystem: u.FileSystem})
	if err != nil {
		return err
	}

	now := currentTime(u.Now)
	writeStoreStatus(u.Stdout, u.FileSystem, u.StoreStatusChecker, config, state, now)
	writeWorkspaceStatus(u.Stdout, currentDir, workspaceID, workspace, registered)
	return writeAllWorkspaceTargetStatus(u.Stdout, u.FileSystem, config, state, now)
}

func writeWorkspaceStatus(w io.Writer, currentDir, workspaceID string, workspace domain.Workspace, registered bool) {
	fmt.Fprintln(w, "Workspace:")
	fmt.Fprintf(w, "  current_dir: %s\n", currentDir)
	if !registered {
		fmt.Fprintln(w, "  registered: no")
		return
	}

	fmt.Fprintln(w, "  registered: yes")
	fmt.Fprintf(w, "  id: %s\n", workspaceID)
	fmt.Fprintf(w, "  root: %s\n", workspace.Root)
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

func writeAllWorkspaceTargetStatus(w io.Writer, fs statusFileSystem, config domain.Config, state domain.State, now time.Time) error {
	fmt.Fprintln(w, "Targets:")
	workspaceIDs := make([]string, 0, len(config.Workspaces))
	for workspaceID := range config.Workspaces {
		workspaceIDs = append(workspaceIDs, workspaceID)
	}
	sort.Strings(workspaceIDs)

	if len(workspaceIDs) == 0 {
		fmt.Fprintln(w, "  none")
		return nil
	}

	width := 0
	for _, workspaceID := range workspaceIDs {
		if len(workspaceID) > width {
			width = len(workspaceID)
		}
	}

	for _, workspaceID := range workspaceIDs {
		workspace := config.Workspaces[workspaceID]
		if len(workspace.Targets) == 0 {
			fmt.Fprintf(w, "  %-*s  none\n", width, workspaceID)
			continue
		}
		for _, target := range workspace.Targets {
			status, err := detectWorkspaceTargetStatus(fs, config, state, workspaceID, workspace, target, now)
			if err != nil {
				return fmt.Errorf("%s/%s: %w", workspaceID, target, err)
			}
			fmt.Fprintf(w, "  %-*s  %-16s  %s\n", width, workspaceID, status, target)
		}
	}
	return nil
}

func detectWorkspaceTargetStatus(fs statusFileSystem, config domain.Config, state domain.State, workspaceID string, workspace domain.Workspace, target string, now time.Time) (string, error) {
	workspaceTargetPath := filepath.Join(workspace.Root, target)
	if config.IsOnePasswordStore() {
		return detectOnePasswordTargetStatus(fs, config, state, workspaceID, target, workspaceTargetPath, now)
	}

	storeTargetPath, err := config.StoreTargetPath(workspaceID, target)
	if err != nil {
		return "", err
	}
	status, err := detectTargetStatus(fs, workspaceTargetPath, storeTargetPath)
	if err != nil {
		return "", err
	}

	lease, ok, err := state.FindLease(workspaceID, target)
	if err != nil {
		return "", err
	}
	if ok && status == "mounted" && !lease.ExpiresAt.After(now) {
		return "expired", nil
	}
	return status, nil
}

func detectOnePasswordTargetStatus(fs statusFileSystem, config domain.Config, state domain.State, workspaceID, target, workspaceTargetPath string, now time.Time) (string, error) {
	document, ok, err := config.DocumentForTarget(workspaceID, target)
	if err != nil {
		return "", err
	}
	if !ok {
		return "missing-document", nil
	}

	info, err := fs.Lstat(workspaceTargetPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "absent", nil
		}
		return "", fmt.Errorf("stat workspace target: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "shadowed", nil
	}

	lease, ok, err := state.FindLease(workspaceID, target)
	if err != nil {
		return "", err
	}
	if !ok || lease.StoreID != onePasswordStoreID || lease.StorePath != document.ItemID || lease.PlaintextHash == "" {
		return "untracked", nil
	}
	if !lease.ExpiresAt.After(now) {
		return "expired", nil
	}
	if lease.WorkspacePath != "" && filepath.Clean(lease.WorkspacePath) != filepath.Clean(workspaceTargetPath) {
		return "untracked", nil
	}

	data, err := fs.ReadFile(workspaceTargetPath)
	if err != nil {
		return "", fmt.Errorf("read workspace target: %w", err)
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != lease.PlaintextHash {
		return "modified", nil
	}
	return "materialized", nil
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
