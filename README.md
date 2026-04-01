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

- Store the real secret files outside the workspace.
- Link them into the workspace only when needed.
- Remove those links explicitly or automatically after a TTL expires.

For the application or tool using the file, nothing special changes. It still sees the file at the usual path.
The initial design targets personal development on macOS and uses iCloud Drive as the default backing store so the same secret files can sync across devices with minimal setup.

## Example Workflow

```bash
# initialize workspace (and global config initially) 
veil init

# move an existing secret file into Veil management
veil add .env

# make managed files appear in the current workspace
veil emerge

# edit the source file safely
veil edit .env

# remove the mounted links from the workspace
veil vanish
```

## Configuration

Veil is designed around a single global config file:

```toml
version = 1
store_path = "~/Library/Mobile Documents/com~apple~CloudDocs/VeilStore"
default_ttl = "24h"

[workspaces.myapp]
root = "/Users/kaz/dev/myapp"
targets = [".env", "config/service-account.json"]
```

Managed source files are derived by convention:

```text
<store_path>/workspaces/<workspace_id>/<target-relative-path>
```

## Scope

Veil is intentionally narrow in scope:

- Personal use, not team secret management
- File-level management, not key-level editing
- Simple CLI workflow, not a large platform
- Better separation and safer defaults, not a full security boundary

## Status

This project is in early development.
