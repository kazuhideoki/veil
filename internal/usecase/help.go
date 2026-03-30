package usecase

func HelpText() string {
	return `Veil is a lightweight secret management CLI for personal development.

Usage:
  veil [command]

Commands:
  init      Initialize config and add the current workspace
  add       Move a target file into Veil store and register it
  edit      Open a registered store file with $EDITOR
  remove    Stop managing a target and restore it into the workspace
  purge     Delete a registered target from Veil store and config
  emerge    Create workspace symlinks for registered targets
  status    Show current target states for the active workspace
  vanish    Remove Veil-managed workspace symlinks for registered targets

Options:
  --help    Show this help message
`
}
