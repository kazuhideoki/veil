package usecase

import (
	"os"
	"path/filepath"
	"testing"
)

type stubTrackedChecker struct {
	tracked bool
	err     error
}

func (s stubTrackedChecker) IsTracked(workspaceRoot, relativePath string) (bool, error) {
	return s.tracked, s.err
}

func writeConfigForTest(t *testing.T, path, body string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() returned error: %v", err)
	}

	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}
}

func workspaceRootQuoted(path string) string {
	return `"` + path + `"`
}
