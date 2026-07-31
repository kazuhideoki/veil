package usecase

import (
	"strings"
	"testing"
)

func TestHelpText(t *testing.T) {
	help := HelpText()

	for _, want := range []string{"Usage:", "veil [command]", "init", "add", "edit", "commit", "diff", "remove", "purge", "workspace", "emerge", "1Password", "current workspace", "--help"} {
		if !strings.Contains(help, want) {
			t.Fatalf("HelpText() does not contain %q", want)
		}
	}
	for _, oldText := range []string{"update", "--force", "symlinks", "store file"} {
		if strings.Contains(help, oldText) {
			t.Fatalf("HelpText() contains obsolete text %q", oldText)
		}
	}
}
