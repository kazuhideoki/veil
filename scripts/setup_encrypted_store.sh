#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Set up a Veil encrypted sparsebundle store and its 1Password passphrase item.

Usage:
  scripts/setup_encrypted_store.sh [options]

Options:
  --vault NAME              1Password vault name (default: Personal)
  --item-title TITLE        1Password item title (default: VeilStore)
  --bundle-path PATH        sparsebundle path (default: iCloud Drive/VeilStore.sparsebundle)
  --mount-path PATH         local mount path for Veil (default: ~/Library/Application Support/veil/mounts/default)
  --session-directory PATH  shared session metadata directory (default: iCloud Drive/VeilStore.sessions)
  --size SIZE               sparsebundle max size (default: 1g)
  --volume-name NAME        mounted volume name (default: VeilStore)
  --config-path PATH        Veil config path to write (default: ~/.veil/config.toml)
  --no-config               Do not write config; print the TOML block only
  --force-config            Overwrite an existing config file after creating a .bak backup
  -h, --help                Show this help

The script creates a Password item with a generated password, reads it back
through the 1Password secret reference, and passes it to hdiutil via stdin.
USAGE
}

fail() {
  printf 'error: %s\n' "$1" >&2
  exit 1
}

expand_tilde() {
  case "$1" in
    \~) printf '%s\n' "$HOME" ;;
    \~/*) printf '%s/%s\n' "$HOME" "${1#\~/}" ;;
    *) printf '%s\n' "$1" ;;
  esac
}

quote_toml_string() {
  local value="$1"
  value=${value//\\/\\\\}
  value=${value//\"/\\\"}
  printf '"%s"' "$value"
}

extract_workspace_tables() {
  local path="$1"
  [[ -f "$path" ]] || return 0

  # Preserve existing workspace registrations while replacing only store setup.
  awk '
    /^\[workspaces\./ { in_workspace = 1 }
    /^\[/ && $0 !~ /^\[workspaces\./ { in_workspace = 0 }
    in_workspace { print }
  ' "$path"
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"
}

icloud_root="$HOME/Library/Mobile Documents/com~apple~CloudDocs"
vault="Personal"
item_title="VeilStore"
bundle_path="$icloud_root/VeilStore.sparsebundle"
mount_path="$HOME/Library/Application Support/veil/mounts/default"
session_directory="$icloud_root/VeilStore.sessions"
size="1g"
volume_name="VeilStore"
config_path="$HOME/.veil/config.toml"
write_config=1
force_config=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --vault)
      vault="${2:?--vault requires a value}"
      shift 2
      ;;
    --item-title)
      item_title="${2:?--item-title requires a value}"
      shift 2
      ;;
    --bundle-path)
      bundle_path="$(expand_tilde "${2:?--bundle-path requires a value}")"
      shift 2
      ;;
    --mount-path)
      mount_path="$(expand_tilde "${2:?--mount-path requires a value}")"
      shift 2
      ;;
    --session-directory)
      session_directory="$(expand_tilde "${2:?--session-directory requires a value}")"
      shift 2
      ;;
    --size)
      size="${2:?--size requires a value}"
      shift 2
      ;;
    --volume-name)
      volume_name="${2:?--volume-name requires a value}"
      shift 2
      ;;
    --config-path)
      config_path="$(expand_tilde "${2:?--config-path requires a value}")"
      shift 2
      ;;
    --no-config)
      write_config=0
      shift
      ;;
    --force-config)
      force_config=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      fail "unknown option: $1"
      ;;
  esac
done

require_command op
require_command hdiutil

[[ -e "$bundle_path" ]] && fail "bundle already exists: $bundle_path"

key_ref="op://${vault}/${item_title}/password"
config_toml=$(cat <<CONFIG
version = 2
default_ttl = "24h"

[store]
backend = "encrypted_volume"
bundle_path = $(quote_toml_string "$bundle_path")
mount_path = $(quote_toml_string "$mount_path")
volume_name = $(quote_toml_string "$volume_name")

[key_provider]
type = "1password"
ref = $(quote_toml_string "$key_ref")

[session]
directory = $(quote_toml_string "$session_directory")
stale_after = "24h"
CONFIG
)

workspace_toml=""
if [[ -f "$config_path" ]]; then
  workspace_toml="$(extract_workspace_tables "$config_path")"
fi
if [[ -n "$workspace_toml" ]]; then
  config_toml="${config_toml}

${workspace_toml}"
fi

printf 'creating 1Password item: vault=%s title=%s\n' "$vault" "$item_title"
op item create \
  --category=password \
  --title="$item_title" \
  --vault="$vault" \
  --generate-password='letters,digits,symbols,32' >/dev/null

printf 'creating sparsebundle: %s\n' "$bundle_path"
mkdir -p "$(dirname "$bundle_path")" "$mount_path" "$session_directory"
passphrase="$(op read "$key_ref")"
if [[ -z "$passphrase" ]]; then
  fail "1Password returned an empty passphrase for $key_ref"
fi
printf '%s' "$passphrase" | hdiutil create \
  -type SPARSEBUNDLE \
  -encryption AES-256 \
  -stdinpass \
  -volname "$volume_name" \
  -fs APFS \
  -size "$size" \
  "$bundle_path" >/dev/null
unset passphrase

if [[ "$write_config" -eq 1 ]]; then
  if [[ -e "$config_path" && "$force_config" -ne 1 ]]; then
    printf 'config already exists, not overwriting: %s\n' "$config_path"
    printf 'rerun with --force-config or copy this TOML manually:\n\n%s\n' "$config_toml"
  else
    mkdir -p "$(dirname "$config_path")"
    if [[ -e "$config_path" ]]; then
      cp "$config_path" "$config_path.bak"
      printf 'backup config: %s.bak\n' "$config_path"
    fi
    printf '%s\n' "$config_toml" > "$config_path"
    printf 'wrote config: %s\n' "$config_path"
  fi
else
  printf '%s\n' "$config_toml"
fi

printf 'done\n'
