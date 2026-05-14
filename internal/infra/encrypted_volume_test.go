package infra

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kazuhideoki/veil/internal/domain"
)

func TestValidateEncryptedConfigRequiresSessionDirectory(t *testing.T) {
	config := domain.DefaultConfig()
	config.Store.Backend = domain.EncryptedVolumeBackend
	config.Store.BundlePath = "/tmp/VeilStore.sparsebundle"
	config.Store.MountPath = "/tmp/veil-mount"
	config.KeyProvider.Type = "1password"
	config.KeyProvider.Ref = "op://Private/Veil/store-passphrase"

	err := validateEncryptedConfig(config)
	if err == nil {
		t.Fatal("validateEncryptedConfig() returned nil error")
	}
	if !strings.Contains(err.Error(), "session.directory") {
		t.Fatalf("error = %q", err)
	}
}

func TestEnsureMountedRejectsActiveOtherSessionWithoutForce(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	sessionDir := filepath.Join(tempHome, "sessions")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}

	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	sessionJSON := fmt.Sprintf(`{"version":1,"session_id":"other","store_id":"default","host":"other-mac","last_seen_at":%q,"mount_path":"/tmp/mount","state":"mounted"}`, now.Add(-time.Minute).Format(time.RFC3339))
	if err := os.WriteFile(filepath.Join(sessionDir, "other.json"), []byte(sessionJSON), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}

	config := encryptedConfigForInfraTest(tempHome, sessionDir)
	var warnings bytes.Buffer
	err := EncryptedVolumeRuntime{}.EnsureMounted(config, now, &warnings, false, true)
	if err == nil {
		t.Fatal("EnsureMounted() returned nil error")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Fatalf("error = %q", err)
	}
	if !strings.Contains(warnings.String(), "Refusing to mount VeilStore") {
		t.Fatalf("warnings = %q", warnings.String())
	}
}

func TestEnsureMountedRejectsActiveOtherSessionWithoutForceAvailable(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	sessionDir := filepath.Join(tempHome, "sessions")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}

	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	sessionJSON := fmt.Sprintf(`{"version":1,"session_id":"other","store_id":"default","host":"other-mac","last_seen_at":%q,"mount_path":"/tmp/mount","state":"mounted"}`, now.Add(-time.Minute).Format(time.RFC3339))
	if err := os.WriteFile(filepath.Join(sessionDir, "other.json"), []byte(sessionJSON), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}

	config := encryptedConfigForInfraTest(tempHome, sessionDir)
	var warnings bytes.Buffer
	err := EncryptedVolumeRuntime{}.EnsureMounted(config, now, &warnings, false, false)
	if err == nil {
		t.Fatal("EnsureMounted() returned nil error")
	}
	if strings.Contains(err.Error(), "--force") {
		t.Fatalf("error = %q, want no --force guidance", err)
	}
	if strings.Contains(warnings.String(), "--force") {
		t.Fatalf("warnings = %q, want no --force guidance", warnings.String())
	}
	if !strings.Contains(err.Error(), "wait for iCloud sync") {
		t.Fatalf("error = %q, want retry guidance", err)
	}
}

func TestEnsureMountedRejectsActiveOtherSessionWhenAlreadyMounted(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	sessionDir := filepath.Join(tempHome, "sessions")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}

	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	sessionJSON := fmt.Sprintf(`{"version":1,"session_id":"other","store_id":"default","host":"other-mac","last_seen_at":%q,"mount_path":"/tmp/mount","state":"mounted"}`, now.Add(-time.Minute).Format(time.RFC3339))
	if err := os.WriteFile(filepath.Join(sessionDir, "other.json"), []byte(sessionJSON), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}

	config := encryptedConfigForInfraTest(tempHome, sessionDir)
	if err := os.MkdirAll(config.Store.MountPath, 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(config.Store.MountPath, ".veil-store"), []byte(`{"version":1,"store_id":"default"}`), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}

	var warnings bytes.Buffer
	err := EncryptedVolumeRuntime{}.EnsureMounted(config, now, &warnings, false, true)
	if err == nil {
		t.Fatal("EnsureMounted() returned nil error")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Fatalf("error = %q", err)
	}
}

func TestEnsureMountedWithForceWarnsAndContinuesPastActiveOtherSession(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	sessionDir := filepath.Join(tempHome, "sessions")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}

	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	sessionJSON := fmt.Sprintf(`{"version":1,"session_id":"other","store_id":"default","host":"other-mac","last_seen_at":%q,"mount_path":"/tmp/mount","state":"mounted"}`, now.Add(-time.Minute).Format(time.RFC3339))
	if err := os.WriteFile(filepath.Join(sessionDir, "other.json"), []byte(sessionJSON), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}

	config := encryptedConfigForInfraTest(tempHome, sessionDir)
	var warnings bytes.Buffer
	err := EncryptedVolumeRuntime{}.EnsureMounted(config, now, &warnings, true, true)
	if err == nil {
		t.Fatal("EnsureMounted() returned nil error")
	}
	if !strings.Contains(err.Error(), "1Password") {
		t.Fatalf("error = %q", err)
	}
	if !strings.Contains(warnings.String(), "continuing because --force was specified") {
		t.Fatalf("warnings = %q", warnings.String())
	}
}

func TestEnsureMountedRejectsUnreadableSessionMetadataWithoutForce(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	sessionDir := filepath.Join(tempHome, "sessions")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "partial.json"), []byte(`{"version":1,`), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}

	config := encryptedConfigForInfraTest(tempHome, sessionDir)
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	var warnings bytes.Buffer
	err := EncryptedVolumeRuntime{}.EnsureMounted(config, now, &warnings, false, true)
	if err == nil {
		t.Fatal("EnsureMounted() returned nil error")
	}
	if !strings.Contains(err.Error(), "session metadata") {
		t.Fatalf("error = %q", err)
	}
	if !strings.Contains(warnings.String(), "failed to read session metadata") {
		t.Fatalf("warnings = %q", warnings.String())
	}
}

func TestEnsureMountedRejectsUnreadableSessionMetadataWithoutForceAvailable(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	sessionDir := filepath.Join(tempHome, "sessions")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "partial.json"), []byte(`{"version":1,`), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}

	config := encryptedConfigForInfraTest(tempHome, sessionDir)
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	var warnings bytes.Buffer
	err := EncryptedVolumeRuntime{}.EnsureMounted(config, now, &warnings, false, false)
	if err == nil {
		t.Fatal("EnsureMounted() returned nil error")
	}
	if strings.Contains(err.Error(), "--force") {
		t.Fatalf("error = %q, want no --force guidance", err)
	}
	if !strings.Contains(err.Error(), "wait for iCloud sync") {
		t.Fatalf("error = %q, want retry guidance", err)
	}
}

func TestEnsureMountedWithForceContinuesPastUnreadableSessionMetadata(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	sessionDir := filepath.Join(tempHome, "sessions")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "partial.json"), []byte(`{"version":1,`), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}

	config := encryptedConfigForInfraTest(tempHome, sessionDir)
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	var warnings bytes.Buffer
	err := EncryptedVolumeRuntime{}.EnsureMounted(config, now, &warnings, true, true)
	if err == nil {
		t.Fatal("EnsureMounted() returned nil error")
	}
	if !strings.Contains(err.Error(), "1Password") {
		t.Fatalf("error = %q", err)
	}
	if !strings.Contains(warnings.String(), "failed to read session metadata") {
		t.Fatalf("warnings = %q", warnings.String())
	}
}

func TestEnsureMountedRejectsInvalidSessionStaleAfterWithoutForce(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	sessionDir := filepath.Join(tempHome, "sessions")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}

	config := encryptedConfigForInfraTest(tempHome, sessionDir)
	config.Session.StaleAfter = "tomorrow"
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	var warnings bytes.Buffer
	err := EncryptedVolumeRuntime{}.EnsureMounted(config, now, &warnings, false, true)
	if err == nil {
		t.Fatal("EnsureMounted() returned nil error")
	}
	if !strings.Contains(err.Error(), "session metadata") {
		t.Fatalf("error = %q", err)
	}
	if !strings.Contains(warnings.String(), "parse session stale_after") {
		t.Fatalf("warnings = %q", warnings.String())
	}
}

func encryptedConfigForInfraTest(tempHome, sessionDir string) domain.Config {
	config := domain.DefaultConfig()
	config.Store.Backend = domain.EncryptedVolumeBackend
	config.Store.BundlePath = filepath.Join(tempHome, "VeilStore.sparsebundle")
	config.Store.MountPath = filepath.Join(tempHome, "mount")
	config.KeyProvider.Type = "1password"
	config.KeyProvider.Ref = "op://Personal/VeilStore/password"
	config.Session.Directory = sessionDir
	config.Session.StaleAfter = "24h"
	return config
}

func TestMountOutputContainsPath(t *testing.T) {
	output := "/dev/disk4s1 on /Users/kaz/Library/Application Support/veil/mounts/default (apfs, local, nodev, nosuid)\n"

	if !mountOutputContainsPath(output, "/Users/kaz/Library/Application Support/veil/mounts/default") {
		t.Fatal("mountOutputContainsPath() returned false")
	}
	if mountOutputContainsPath(output, "/Users/kaz/Library/Application Support/veil/mounts") {
		t.Fatal("mountOutputContainsPath() returned true for parent path")
	}
}
