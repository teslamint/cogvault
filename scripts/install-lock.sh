#!/bin/sh
set -eu

if [ "$#" -lt 2 ]; then
  printf 'usage: %s <target_dir> <command> [args...]\n' "$0" >&2
  exit 1
fi

target_dir="$1"
shift

if [ "${COGVAULT_INSTALL_LOCK_HELD:-0}" = "1" ]; then
  exec "$@"
fi

mkdir -p "$target_dir"
canonical_dir=$(cd "$target_dir" && pwd -P)
lock_file="$canonical_dir/.cogvault.install.lock"
export COGVAULT_INSTALL_LOCK_HELD=1

if command -v lockf >/dev/null 2>&1; then
  exec lockf -k -t 15 "$lock_file" "$@"
elif command -v flock >/dev/null 2>&1; then
  exec flock -w 15 "$lock_file" "$@"
elif command -v python3 >/dev/null 2>&1; then
  exec python3 -c '
import sys, fcntl, time, subprocess
lock_file = sys.argv[1]
cmd = sys.argv[2:]
with open(lock_file, "a") as f:
    deadline = time.time() + 15
    while True:
        try:
            fcntl.flock(f, fcntl.LOCK_EX | fcntl.LOCK_NB)
            break
        except (BlockingIOError, OSError):
            if time.time() >= deadline:
                sys.stderr.write(f"timed out waiting for installation lock: {lock_file}\n")
                sys.exit(1)
            time.sleep(0.05)
    ret = subprocess.call(cmd)
    sys.exit(ret)
' "$lock_file" "$@"
else
  printf 'no supported advisory lock utility found (lockf, flock, python3)\n' >&2
  exit 1
fi
