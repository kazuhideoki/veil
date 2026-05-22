package usecase

func HelpText() string {
	return `Veil is a lightweight secret management CLI for personal development.

Usage:
  veil [command]

Commands:
  init      Initialize config and add the current workspace
  add       Move a target file, or a directory's direct files, into Veil store and register them
  edit      Open a registered store file with $EDITOR
  update    Commit a materialized 1Password document target back to 1Password
  remove    Stop managing a target and restore it into the workspace
  purge     Delete a registered target from Veil store and config
  workspace Remove or purge the active workspace registration
  emerge    Create workspace symlinks or materialized files for registered targets (--all, --force)
  status    Show target states for all registered workspaces
  vanish    Remove Veil-managed workspace targets (--all, --commit, --discard)

Options:
  --help    Show this help message
`
}
