package usecase

func HelpText() string {
	return `Veil is a lightweight secret management CLI for personal development.

Usage:
  veil [command]

Commands:
  init      Create ~/.veil/config.toml

Options:
  --help    Show this help message
`
}
