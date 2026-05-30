package usecase

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/kazuhideoki/veil/internal/domain"
)

type statusFileSystem interface {
	UserHomeDir() (string, error)
	Getwd() (string, error)
	EvalSymlinks(path string) (string, error)
	ReadFile(name string) ([]byte, error)
	Stat(name string) (os.FileInfo, error)
	Lstat(name string) (os.FileInfo, error)
}

type StatusTargets struct {
	FileSystem statusFileSystem
	Stdout     io.Writer
	Now        func() time.Time
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
	if err := requireOnePasswordConfig(config); err != nil {
		return err
	}

	workspaceID, workspace, registered, err := config.FindWorkspaceByDir(currentDir)
	if err != nil {
		return err
	}

	_, state, err := loadState(statusStateFileSystem{statusFileSystem: u.FileSystem})
	if err != nil {
		return err
	}

	now := currentTime(u.Now)
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
			fmt.Fprintf(w, "  %-*s  no-targets\n", width, workspaceID)
			continue
		}
		for _, target := range workspace.Targets {
			status, err := detectWorkspaceTargetStatus(fs, config, state, workspaceID, workspace, target, now)
			if err != nil {
				return fmt.Errorf("%s/%s: %w", workspaceID, target, err)
			}
			if status.TTLRemaining != "" {
				fmt.Fprintf(w, "  %-*s  %-16s  ttl=%-10s  %s\n", width, workspaceID, status.State, status.TTLRemaining, target)
				continue
			}
			fmt.Fprintf(w, "  %-*s  %-16s  %s\n", width, workspaceID, status.State, target)
		}
	}
	return nil
}

type workspaceTargetStatus struct {
	State        string
	TTLRemaining string
}

func detectWorkspaceTargetStatus(fs statusFileSystem, config domain.Config, state domain.State, workspaceID string, workspace domain.Workspace, target string, now time.Time) (workspaceTargetStatus, error) {
	workspaceTargetPath := filepath.Join(workspace.Root, target)
	return detectOnePasswordTargetStatus(fs, config, state, workspaceID, target, workspaceTargetPath, now)
}

func detectOnePasswordTargetStatus(fs statusFileSystem, config domain.Config, state domain.State, workspaceID, target, workspaceTargetPath string, now time.Time) (workspaceTargetStatus, error) {
	document, ok, err := config.DocumentForTarget(workspaceID, target)
	if err != nil {
		return workspaceTargetStatus{}, err
	}
	if !ok {
		return workspaceTargetStatus{State: "missing-document"}, nil
	}

	info, err := fs.Lstat(workspaceTargetPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return workspaceTargetStatus{State: "absent"}, nil
		}
		return workspaceTargetStatus{}, fmt.Errorf("stat workspace target: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return workspaceTargetStatus{State: "shadowed"}, nil
	}

	lease, ok, err := state.FindLease(workspaceID, target)
	if err != nil {
		return workspaceTargetStatus{}, err
	}
	if !ok || lease.StoreID != onePasswordStoreID || lease.StorePath != document.ItemID || lease.PlaintextHash == "" {
		return workspaceTargetStatus{State: "untracked"}, nil
	}
	if !lease.ExpiresAt.After(now) {
		return workspaceTargetStatus{State: "expired"}, nil
	}
	if lease.WorkspacePath != "" && filepath.Clean(lease.WorkspacePath) != filepath.Clean(workspaceTargetPath) {
		return workspaceTargetStatus{State: "untracked"}, nil
	}

	ttlRemaining := formatTTLRemaining(lease.ExpiresAt.Sub(now))
	data, err := fs.ReadFile(workspaceTargetPath)
	if err != nil {
		return workspaceTargetStatus{}, fmt.Errorf("read workspace target: %w", err)
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != lease.PlaintextHash {
		return workspaceTargetStatus{State: "modified", TTLRemaining: ttlRemaining}, nil
	}
	return workspaceTargetStatus{State: "materialized", TTLRemaining: ttlRemaining}, nil
}

func formatTTLRemaining(remaining time.Duration) string {
	if remaining <= 0 {
		return "00h00m00s"
	}
	if remaining < time.Second {
		return "00h00m01s"
	}

	totalSeconds := int64(remaining.Truncate(time.Second).Seconds())
	hours := totalSeconds / 3600
	minutes := (totalSeconds % 3600) / 60
	seconds := totalSeconds % 60
	return fmt.Sprintf("%02dh%02dm%02ds", hours, minutes, seconds)
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
