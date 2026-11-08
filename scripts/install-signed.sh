#!/bin/sh
set -eu

repo_dir=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
identity=${1-}

dev_id_requirement='=anchor apple generic and certificate 1[field.1.2.840.113635.100.6.2.6] exists and certificate leaf[field.1.2.840.113635.100.6.1.13] exists'
identity_auto_selected=0

verify_apple_developer_id() {
  candidate_fp=$1
  probe_bin=$(mktemp "${TMPDIR:-/tmp}/codesign-probe.XXXXXX")
  if [ -f /usr/bin/true ]; then
    cp /usr/bin/true "$probe_bin"
  elif [ -f /bin/echo ]; then
    cp /bin/echo "$probe_bin"
  else
    : >"$probe_bin"
  fi

  if ! codesign --force --sign "$candidate_fp" "$probe_bin" >/dev/null 2>&1; then
    rm -f "$probe_bin"
    return 1
  fi

  if ! codesign --verify --strict -R "$dev_id_requirement" "$probe_bin" >/dev/null 2>&1; then
    rm -f "$probe_bin"
    return 1
  fi

  rm -f "$probe_bin"
  return 0
}

if [ -z "$identity" ]; then
  identity_auto_selected=1
  raw_candidates=$(security find-identity -v -p codesigning 2>/dev/null |
    awk '
      {
        split($0, parts, "\"")
        name = parts[2]
        fp = $2
        if (fp != "" && name ~ /^Developer ID Application:/ && !seen[fp]++) {
          print fp "\t" name
        }
      }
    ')

  identities=""
  identity_count=0
  if [ -n "$raw_candidates" ]; then
    while IFS="$(printf '\t')" read -r cand_fp cand_name; do
      [ -n "$cand_fp" ] || continue
      if verify_apple_developer_id "$cand_fp"; then
        identities="$cand_fp"
        identity_count=$((identity_count + 1))
      fi
    done <<EOF
$raw_candidates
EOF
  fi

  if [ "$identity_count" -ne 1 ]; then
    printf 'expected exactly one valid Developer ID Application identity; found %s\n' "$identity_count" >&2
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

if [ "$identity_auto_selected" -eq 1 ]; then
  codesign --verify --strict -R "$dev_id_requirement" "$installed_binary"
fi

job="gui/$(id -u)/com.teslamint.cogvault.ingest"
if launchctl print "$job" >/dev/null 2>&1; then
  launchctl kickstart -k "$job"
  printf 'installed, verified, and restarted %s\n' "$job"
else
  printf 'installed and verified; launchd ingest job is not loaded\n'
fi
