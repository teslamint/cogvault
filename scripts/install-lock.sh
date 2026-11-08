#!/bin/sh
set -eu

if [ "$#" -lt 2 ]; then
  printf 'usage: %s <target_dir> <command> [args...]\n' "$0" >&2
  exit 1
fi

target_dir="$1"
shift

mkdir -p "$target_dir"
canonical_dir=$(cd "$target_dir" && pwd -P)
lock_file="$canonical_dir/.cogvault.install.lock"
touch "$lock_file"

if [ "${COGVAULT_INSTALL_LOCK_HELD:-}" = "$canonical_dir" ]; then
  exec "$@"
fi

export COGVAULT_INSTALL_LOCK_HELD="$canonical_dir"
if command -v lockf >/dev/null 2>&1; then
  exec lockf -k -t 15 "$lock_file" "$@"
elif command -v flock >/dev/null 2>&1; then
  exec flock -w 15 "$lock_file" "$@"
else
  printf 'unsupported platform: neither lockf nor flock available for advisory locking\n' >&2
  exit 1
fi
