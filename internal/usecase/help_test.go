package usecase

import (
	"strings"
	"testing"
)

func TestHelpText(t *testing.T) {
	help := HelpText()

	for _, want := range []string{"Usage:", "veil [command]", "init", "add", "edit", "remove", "purge", "workspace", "emerge", "current workspace", "--help"} {
		if !strings.Contains(help, want) {
			t.Fatalf("HelpText() does not contain %q", want)
		}
	}
}
