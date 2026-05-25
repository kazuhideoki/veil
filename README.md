# Veil

Veil is a lightweight CLI for personal secret file management on macOS.
It keeps files like `.env` and service account JSONs out of your workspace by default, stores them in a dedicated location, and makes them appear only when you need them.

## The Problem

In small personal projects, secret files often live directly in the workspace.
That is convenient, but it creates a few recurring problems:

- You can accidentally stage or commit secrets.
- Coding agents and helper tools can always read plaintext files sitting in the repo.
- Syncing secrets across multiple Macs becomes a manual and messy process.
- Temporary files tend to stay around longer than intended.

Veil is meant to solve this without introducing a heavy secret management platform.

## The Approach

Veil uses a simple model:

- Store the real secret files outside the workspace in 1Password.
- Materialize them into the workspace only when needed.
- Remove materialized files explicitly, or clean up expired files with the TTL cleaner.

For the application or tool using the file, nothing special changes. It still sees the file at the usual path.
The default store backend keeps each secret file as a 1Password Document and materializes files into the workspace only while you need them.

## Example Workflow

```bash
# initialize workspace (and global config initially) 
veil init

# move an existing secret file into Veil management
veil add .env

# move all direct files in a directory into Veil management
veil add config/secrets

# make managed files appear in the current workspace
# also ensures the macOS TTL cleanup agent is installed
veil emerge

# edit the source document safely
veil edit .env

# commit materialized edits back to 1Password
veil update .env

# remove materialized files from the workspace
veil vanish

# remove materialized files from every registered workspace
veil vanish --all

# manually repair or change the background cleanup interval
veil ttl-agent install --interval 60
```

## TTL Cleanup

`default_ttl` controls how long a materialized file lease is valid. Expired clean files are removed by `veil ttl-cleaner`, and modified files are kept so local edits are not lost.

`veil emerge` automatically installs and loads the macOS LaunchAgent that runs TTL cleanup in the background. Manual agent commands are available for repair, inspection, and interval changes:

```bash
veil ttl-agent install --interval 60
veil ttl-agent status
veil ttl-agent uninstall
```

The LaunchAgent runs `veil ttl-cleaner` at the configured interval and once at load. If the Mac is asleep or powered off at the exact expiration time, cleanup happens on the next scheduled run after the machine is awake.

## Configuration

Veil is designed around a single global config file:

```toml
version = 2
default_ttl = "24h"

[store]
backend = "1password_document"
vault = "Personal"

[workspaces.myapp]
root = "/Users/kaz/dev/myapp"
targets = [".env", "config/service-account.json"]
```

Each managed target has document metadata:

```toml
[[documents]]
workspace_id = "myapp"
target = ".env"
item_id = "op-item-id"
vault = "Personal"
title = "Veil: myapp: .env"
content_sha256 = "..."
```

## Development

Run the test suite:

```bash
go test ./...
```

Update the local `./veil` binary from source:

```bash
go build -o veil ./cmd/veil
```

## Scope

Veil is intentionally narrow in scope:

- Personal use, not team secret management
- File-level management, not key-level editing
- Simple CLI workflow, not a large platform
- Better separation and safer defaults, not a full security boundary

## Status

This project is in early development.

## License

Veil is licensed under the MIT License. See [LICENSE](LICENSE) for details.
