package usecase

import (
	"fmt"
	"io"
	"time"

	"github.com/kazuhideoki/veil/internal/domain"
)

type EncryptedStoreRuntime interface {
	EnsureMounted(config domain.Config, now time.Time, warnings io.Writer, force, forceAvailable bool) error
	UnmountIfIdle(config domain.Config, state domain.State, now time.Time, warnings io.Writer) error
}

type EncryptedStoreStatusChecker interface {
	IsMounted(config domain.Config) bool
}

func ensureStoreAvailable(runtime EncryptedStoreRuntime, config domain.Config, now time.Time, warnings io.Writer, force, forceAvailable bool) error {
	if !config.IsEncryptedVolumeStore() {
		return nil
	}
	if runtime == nil {
		return fmt.Errorf("encrypted volume store requires a mount runtime")
	}
	return runtime.EnsureMounted(config, now, warnings, force, forceAvailable)
}

func unmountStoreIfIdle(runtime EncryptedStoreRuntime, config domain.Config, state domain.State, now time.Time, warnings io.Writer) error {
	if !config.IsEncryptedVolumeStore() {
		return nil
	}
	if runtime == nil {
		return fmt.Errorf("encrypted volume store requires a mount runtime")
	}
	return runtime.UnmountIfIdle(config, state, now, warnings)
}
