package usecase

func HelpText() string {
	return `Veil is a lightweight secret management CLI for personal development.

Usage:
  veil [command]

Commands:
  init      Initialize config and add the current workspace
  add       Move a target file into Veil store and register it

Options:
  --help    Show this help message
`
}
