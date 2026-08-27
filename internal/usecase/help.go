package usecase

func HelpText() string {
	return `Veil is a lightweight secret management CLI for personal development.

Usage:
  veil [command]

Commands:
  init      Initialize config and add the current workspace
  add       Move a target file, or a directory's direct files, into 1Password and register them
  edit      Open a registered 1Password document with $EDITOR
  commit    Commit one materialized target back to 1Password (--overwrite-remote)
  remove    Stop managing a target and keep it as a workspace file
  purge     Permanently delete a registered target from Veil config and 1Password
  workspace Remove or purge the active workspace registration
  emerge    Materialize one target ref, the active workspace, or all workspaces (--all, --verbose)
  status    Show target states for all registered workspaces
  diff      Show workspace changes against 1Password document targets (--all, --summary)
  vanish    Remove one target ref, active-workspace targets, or all targets (--all, --commit, --discard)
  ttl-agent Install, uninstall, or show the macOS TTL cleanup LaunchAgent
  ttl-cleaner
            Remove expired materialized targets once

Options:
  --help    Show this help message
`
}
