#!/bin/sh
set -eu

repo_dir=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
identity=${1-}

if [ -z "$identity" ]; then
  identities=$(security find-identity -v -p codesigning 2>/dev/null |
    awk -F '"' '/Apple Development:/ { print $2 }')
  identity_count=$(printf '%s\n' "$identities" | awk 'NF { count++ } END { print count + 0 }')
  if [ "$identity_count" -ne 1 ]; then
    printf 'expected exactly one valid Developer ID Application or Apple Development identity; found %s\n' "$identity_count" >&2
    printf 'pass the full identity as the first argument when more than one exists\n' >&2
    exit 1
  fi
  identity=$identities
fi

cd "$repo_dir"
make install "CODESIGN_IDENTITY=$identity"

installed_binary=$HOME/bin/cogvault
codesign --verify --strict --verbose=4 "$installed_binary"
signature=$(codesign -d --verbose=4 "$installed_binary" 2>&1)
case "$signature" in
  *'Identifier=dev.tmint.cogvault'*) ;;
  *)
    printf 'installed binary has the wrong code-signing identifier\n' >&2
    exit 1
    ;;
esac

job="gui/$(id -u)/com.teslamint.cogvault.ingest"
if launchctl print "$job" >/dev/null 2>&1; then
  launchctl kickstart -k "$job"
  printf 'installed, verified, and restarted %s\n' "$job"
else
  printf 'installed and verified; launchd ingest job is not loaded\n'
fi
