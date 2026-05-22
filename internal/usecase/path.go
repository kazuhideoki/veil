package usecase

import (
	"path/filepath"
	"strings"

	"github.com/kazuhideoki/veil/internal/domain"
)

func expandHomeDir(path, homeDir string) string {
	if path == "~" {
		return homeDir
	}

	homePrefix := "~" + string(filepath.Separator)
	if strings.HasPrefix(path, homePrefix) {
		return filepath.Join(homeDir, path[len(homePrefix):])
	}

	return path
}

func expandConfigPaths(config domain.Config, homeDir string) domain.Config {
	return config
}
