package usecase

func HelpText() string {
	return `Veil is a lightweight secret management CLI for personal development.

Usage:
  veil [command]

Commands:
  init      Initialize config and add the current workspace
  add       Move a target file, or a directory's direct files, into 1Password and register them
  edit      Open a registered 1Password document with $EDITOR
  update    Commit a materialized 1Password document target back to 1Password
  remove    Stop managing a target and restore it into the workspace
  purge     Delete a registered target from Veil config
  workspace Remove or purge the active workspace registration
  emerge    Materialize registered 1Password document targets into the workspace (--all)
  status    Show target states for all registered workspaces
  vanish    Remove Veil-managed workspace targets (--all, --commit, --discard)
  ttl-agent Install, uninstall, or show the macOS TTL cleanup LaunchAgent
  ttl-cleaner
            Remove expired materialized targets once

Options:
  --help    Show this help message
`
}
