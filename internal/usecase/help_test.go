package usecase

import (
	"strings"
	"testing"
)

func TestHelpText(t *testing.T) {
	help := HelpText()

<<<<<<< HEAD
	for _, want := range []string{"Usage:", "veil [command]", "init", "add", "current workspace", "--help"} {
=======
	for _, want := range []string{"Usage:", "veil [command]", "init", "current workspace", "--help"} {
>>>>>>> main
		if !strings.Contains(help, want) {
			t.Fatalf("HelpText() does not contain %q", want)
		}
	}
}
