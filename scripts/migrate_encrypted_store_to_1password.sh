#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Migrate a Veil encrypted sparsebundle store to 1Password Document storage.

Usage:
  scripts/migrate_encrypted_store_to_1password.sh [options]

Options:
  --vault NAME       Destination 1Password vault name (default: Personal)
  --config-path PATH Veil config path to migrate (default: ~/.veil/config.toml)
  --dry-run          Validate inputs and print the planned migration without creating items
  --keep-mounted     Do not detach the encrypted volume after migration
  --force-config     Overwrite config after creating a .bak backup (required unless --dry-run)
  --skip-state-check Do not fail when ~/.veil/state.toml still has leases
  -h, --help         Show this help

The existing config must use store.backend = "encrypted_volume". The script
mounts the configured sparsebundle if needed, creates one 1Password Document
item per registered target, then rewrites the config to
store.backend = "1password_document".
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

require_command() {
  command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"
}

config_path="$HOME/.veil/config.toml"
vault="Personal"
dry_run=0
keep_mounted=0
force_config=0
skip_state_check=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --vault)
      vault="${2:?--vault requires a value}"
      shift 2
      ;;
    --config-path)
      config_path="$(expand_tilde "${2:?--config-path requires a value}")"
      shift 2
      ;;
    --dry-run)
      dry_run=1
      shift
      ;;
    --keep-mounted)
      keep_mounted=1
      shift
      ;;
    --force-config)
      force_config=1
      shift
      ;;
    --skip-state-check)
      skip_state_check=1
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

require_command go
require_command op
require_command hdiutil
[[ -f "$config_path" ]] || fail "config not found: $config_path"

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/.." && pwd)"
cd "$repo_root"

if [[ -z "${GOCACHE:-}" ]]; then
  export GOCACHE="${TMPDIR:-/tmp}/veil-go-build-cache"
  mkdir -p "$GOCACHE"
fi

state_path="$(dirname "$config_path")/state.toml"
if [[ "$skip_state_check" -eq 0 && -f "$state_path" ]] && grep -F '[[leases]]' "$state_path" >/dev/null; then
  fail "state has active/stale leases; run 'veil vanish --all' before migration or rerun with --skip-state-check"
fi

metadata_json="$(go run ./scripts/migrate_encrypted_store_to_1password.go inspect --config-path "$config_path")"
backend="$(printf '%s\n' "$metadata_json" | sed -n 's/.*"backend":"\([^"]*\)".*/\1/p')"
bundle_path="$(printf '%s\n' "$metadata_json" | sed -n 's/.*"bundle_path":"\([^"]*\)".*/\1/p')"
mount_path="$(printf '%s\n' "$metadata_json" | sed -n 's/.*"mount_path":"\([^"]*\)".*/\1/p')"
key_ref="$(printf '%s\n' "$metadata_json" | sed -n 's/.*"key_ref":"\([^"]*\)".*/\1/p')"

[[ "$backend" == "encrypted_volume" ]] || fail "config backend must be encrypted_volume, got: $backend"
[[ -n "$bundle_path" ]] || fail "config is missing store.bundle_path"
[[ -n "$mount_path" ]] || fail "config is missing store.mount_path"
[[ -n "$key_ref" ]] || fail "config is missing key_provider.ref"
[[ -e "$bundle_path" ]] || fail "bundle not found: $bundle_path"

mounted_before=0
if hdiutil info | grep -F -- "$mount_path" >/dev/null; then
  mounted_before=1
fi

if [[ "$mounted_before" -eq 0 ]]; then
  printf 'mounting encrypted store: %s\n' "$bundle_path"
  passphrase="$(op read "$key_ref")"
  [[ -n "$passphrase" ]] || fail "1Password returned an empty passphrase for $key_ref"
  mkdir -p "$mount_path"
  printf '%s' "$passphrase" | hdiutil attach \
    "$bundle_path" \
    -stdinpass \
    -mountpoint "$mount_path" \
    -nobrowse >/dev/null
  unset passphrase
fi

cleanup() {
  if [[ "$mounted_before" -eq 0 && "$keep_mounted" -eq 0 ]]; then
    hdiutil detach "$mount_path" >/dev/null || printf 'warning: failed to detach encrypted store: %s\n' "$mount_path" >&2
  fi
}
trap cleanup EXIT

helper_args=(migrate --config-path "$config_path" --store-root "$mount_path" --vault "$vault")
if [[ "$dry_run" -eq 1 ]]; then
  helper_args+=(--dry-run)
else
  [[ "$force_config" -eq 1 ]] || fail "refusing to rewrite config without --force-config"
fi

go run ./scripts/migrate_encrypted_store_to_1password.go "${helper_args[@]}"
