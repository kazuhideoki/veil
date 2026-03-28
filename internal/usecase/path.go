package usecase

import (
	"path/filepath"
	"strings"
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
