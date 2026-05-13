package infra

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kazuhideoki/veil/internal/domain"
)

type EncryptedVolumeRuntime struct{}

type sessionFile struct {
	Version    int    `json:"version"`
	SessionID  string `json:"session_id"`
	StoreID    string `json:"store_id"`
	Host       string `json:"host"`
	StartedAt  string `json:"started_at"`
	LastSeenAt string `json:"last_seen_at"`
	MountPath  string `json:"mount_path"`
	State      string `json:"state"`
}

type activeSession struct {
	Host     string
	LastSeen time.Time
}

func (EncryptedVolumeRuntime) IsMounted(config domain.Config) bool {
	return isMountedStore(config)
}

func (EncryptedVolumeRuntime) EnsureMounted(config domain.Config, now time.Time, warnings io.Writer, force bool) error {
	if err := validateEncryptedConfig(config); err != nil {
		return err
	}
	activeSessions, err := activeOtherSessions(config, now)
	if err != nil {
		fmt.Fprintf(warnings, "warning: failed to read session metadata: %v\n", err)
		if !force {
			return fmt.Errorf("failed to verify VeilStore session metadata; rerun with --force to continue without consistency guarantees: %w", err)
		}
	}
	if len(activeSessions) > 0 {
		writeActiveSessionWarning(warnings, activeSessions, force)
		if !force {
			return fmt.Errorf("VeilStore appears active on another device; rerun with --force to continue without consistency guarantees")
		}
	}
	if isMountedStore(config) {
		return touchOwnSession(config, now, warnings)
	}

	passphrase, err := readOnePasswordSecret(config.KeyProvider.Ref)
	if err != nil {
		return err
	}
	if err := attachEncryptedVolume(config, passphrase); err != nil {
		return err
	}
	if err := writeStoreMarker(config); err != nil {
		fmt.Fprintf(warnings, "warning: failed to write store marker: %v\n", err)
	}
	return touchOwnSession(config, now, warnings)
}

func (EncryptedVolumeRuntime) UnmountIfIdle(config domain.Config, state domain.State, now time.Time, warnings io.Writer) error {
	if state.HasActiveLeaseForStore(domain.DefaultStoreID, now) {
		return nil
	}
	if !isMountedStore(config) {
		if err := removeOwnSession(config); err != nil {
			fmt.Fprintf(warnings, "warning: failed to remove session metadata: %v\n", err)
		}
		return nil
	}
	cmd := exec.Command("hdiutil", "detach", config.Store.MountPath)
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(warnings, "warning: failed to unmount VeilStore: volume is busy or detach failed\n")
		return nil
	}
	if err := removeOwnSession(config); err != nil {
		fmt.Fprintf(warnings, "warning: failed to remove session metadata: %v\n", err)
	}
	return nil
}

func validateEncryptedConfig(config domain.Config) error {
	if config.Store.BundlePath == "" {
		return fmt.Errorf("encrypted volume store requires store.bundle_path")
	}
	if config.Store.MountPath == "" {
		return fmt.Errorf("encrypted volume store requires store.mount_path")
	}
	if config.KeyProvider.Type != "1password" {
		return fmt.Errorf("encrypted volume store requires key_provider.type = \"1password\"")
	}
	if config.KeyProvider.Ref == "" {
		return fmt.Errorf("encrypted volume store requires key_provider.ref")
	}
	if config.Session.Directory == "" {
		return fmt.Errorf("encrypted volume store requires session.directory")
	}
	return nil
}

func isMountedStore(config domain.Config) bool {
	if !isListedMountPoint(config.Store.MountPath) {
		return false
	}
	markerPath := filepath.Join(config.Store.MountPath, ".veil-store")
	data, err := os.ReadFile(markerPath)
	if err != nil {
		return false
	}
	var marker struct {
		StoreID string `json:"store_id"`
	}
	if err := json.Unmarshal(data, &marker); err != nil {
		return false
	}
	return marker.StoreID == "" || marker.StoreID == domain.DefaultStoreID
}

func isListedMountPoint(mountPath string) bool {
	output, err := exec.Command("mount").Output()
	if err != nil {
		return false
	}
	return mountOutputContainsPath(string(output), filepath.Clean(mountPath))
}

func mountOutputContainsPath(output, mountPath string) bool {
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, " on "+mountPath+" ") || strings.HasSuffix(line, " on "+mountPath) {
			return true
		}
	}
	return false
}

func readOnePasswordSecret(ref string) (string, error) {
	cmd := exec.Command("op", "read", ref)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to get VeilStore passphrase from 1Password")
	}
	return strings.TrimRight(stdout.String(), "\r\n"), nil
}

func attachEncryptedVolume(config domain.Config, passphrase string) error {
	if err := os.MkdirAll(config.Store.MountPath, 0o700); err != nil {
		return fmt.Errorf("create mount path: %w", err)
	}
	cmd := exec.Command("hdiutil", "attach", config.Store.BundlePath, "-stdinpass", "-mountpoint", config.Store.MountPath, "-nobrowse")
	cmd.Stdin = strings.NewReader(passphrase)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to mount encrypted VeilStore")
	}
	return nil
}

func writeStoreMarker(config domain.Config) error {
	marker := map[string]any{
		"version":  1,
		"store_id": domain.DefaultStoreID,
	}
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(config.Store.MountPath, ".veil-store"), data, 0o600)
}

func activeOtherSessions(config domain.Config, now time.Time) ([]activeSession, error) {
	if config.Session.Directory == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(config.Session.Directory)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	staleAfter, err := time.ParseDuration(config.Session.StaleAfter)
	if err != nil {
		return nil, fmt.Errorf("parse session stale_after: %w", err)
	}
	ownID, _ := readOwnSessionID(config)
	active := []activeSession{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		session, err := readSession(filepath.Join(config.Session.Directory, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read session %s: %w", entry.Name(), err)
		}
		if session.SessionID == ownID || session.StoreID != domain.DefaultStoreID {
			continue
		}
		lastSeen, err := time.Parse(time.RFC3339, session.LastSeenAt)
		if err != nil {
			return nil, fmt.Errorf("parse session %s last_seen_at: %w", entry.Name(), err)
		}
		if !lastSeen.Add(staleAfter).After(now) {
			continue
		}
		active = append(active, activeSession{Host: session.Host, LastSeen: lastSeen})
	}
	return active, nil
}

func writeActiveSessionWarning(warnings io.Writer, sessions []activeSession, forced bool) {
	fmt.Fprintln(warnings, "warning: VeilStore appears active on another device:")
	for _, session := range sessions {
		fmt.Fprintf(warnings, "  host: %s\n  last seen: %s\n", session.Host, session.LastSeen.Format(time.RFC3339))
	}
	if forced {
		fmt.Fprintln(warnings, "\ncontinuing because --force was specified")
		fmt.Fprintln(warnings, "Veil does not guarantee file consistency after forced multi-device use.")
		return
	}
	fmt.Fprintln(warnings, "\nRefusing to emerge because concurrent sparsebundle use may read stale data or create conflicts.")
	fmt.Fprintln(warnings, "Run veil vanish on the other device, wait for iCloud sync, then retry. Use --force only if you accept the consistency risk.")
}

func readSession(path string) (sessionFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return sessionFile{}, err
	}
	var session sessionFile
	if err := json.Unmarshal(data, &session); err != nil {
		return sessionFile{}, err
	}
	return session, nil
}

func touchOwnSession(config domain.Config, now time.Time, warnings io.Writer) error {
	if config.Session.Directory == "" {
		return nil
	}
	if err := os.MkdirAll(config.Session.Directory, 0o700); err != nil {
		fmt.Fprintf(warnings, "warning: failed to write session metadata\n")
		return nil
	}
	sessionID, startedAt, err := ensureOwnSessionID(config, now)
	if err != nil {
		fmt.Fprintf(warnings, "warning: failed to write session metadata\n")
		return nil
	}
	host, err := os.Hostname()
	if err != nil {
		host = "unknown"
	}
	session := sessionFile{
		Version:    1,
		SessionID:  sessionID,
		StoreID:    domain.DefaultStoreID,
		Host:       host,
		StartedAt:  startedAt.Format(time.RFC3339),
		LastSeenAt: now.Format(time.RFC3339),
		MountPath:  config.Store.MountPath,
		State:      "mounted",
	}
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		fmt.Fprintf(warnings, "warning: failed to write session metadata\n")
		return nil
	}
	if err := os.WriteFile(filepath.Join(config.Session.Directory, sessionID+".json"), data, 0o600); err != nil {
		fmt.Fprintf(warnings, "warning: failed to write session metadata\n")
	}
	return nil
}

func ensureOwnSessionID(config domain.Config, now time.Time) (string, time.Time, error) {
	if sessionID, err := readOwnSessionID(config); err == nil && sessionID != "" {
		return sessionID, now, nil
	}
	host, err := os.Hostname()
	if err != nil {
		host = "unknown"
	}
	random := make([]byte, 3)
	if _, err := rand.Read(random); err != nil {
		return "", time.Time{}, err
	}
	sessionID := fmt.Sprintf("%s-%s-%s", sanitizeSessionPart(host), now.Format("20060102T150405"), hex.EncodeToString(random))
	if err := os.MkdirAll(filepath.Dir(localSessionIDPath()), 0o700); err != nil {
		return "", time.Time{}, err
	}
	if err := os.WriteFile(localSessionIDPath(), []byte(sessionID), 0o600); err != nil {
		return "", time.Time{}, err
	}
	return sessionID, now, nil
}

func readOwnSessionID(config domain.Config) (string, error) {
	data, err := os.ReadFile(localSessionIDPath())
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func removeOwnSession(config domain.Config) error {
	sessionID, err := readOwnSessionID(config)
	if err != nil || sessionID == "" {
		return err
	}
	if config.Session.Directory != "" {
		if err := os.Remove(filepath.Join(config.Session.Directory, sessionID+".json")); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.Remove(localSessionIDPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func localSessionIDPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".veil", "encrypted-volume-session-id")
	}
	return filepath.Join(home, ".veil", "encrypted-volume-session-id")
}

func sanitizeSessionPart(value string) string {
	value = strings.ToLower(value)
	var builder strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			builder.WriteRune(r)
		}
	}
	if builder.Len() == 0 {
		return "host"
	}
	return builder.String()
}
