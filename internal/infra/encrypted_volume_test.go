package infra

import (
	"strings"
	"testing"

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

func TestMountOutputContainsPath(t *testing.T) {
	output := "/dev/disk4s1 on /Users/kaz/Library/Application Support/veil/mounts/default (apfs, local, nodev, nosuid)\n"

	if !mountOutputContainsPath(output, "/Users/kaz/Library/Application Support/veil/mounts/default") {
		t.Fatal("mountOutputContainsPath() returned false")
	}
	if mountOutputContainsPath(output, "/Users/kaz/Library/Application Support/veil/mounts") {
		t.Fatal("mountOutputContainsPath() returned true for parent path")
	}
}
