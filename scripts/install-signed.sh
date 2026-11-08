#!/bin/sh
set -eu

repo_dir=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
identity=${1-}

dev_id_requirement='=anchor apple generic and certificate 1[field.1.2.840.113635.100.6.2.6] exists and certificate leaf[field.1.2.840.113635.100.6.1.13] exists'
identity_auto_selected=0

verify_apple_developer_id() {
  candidate_fp=$1

  cert_pem=$(security find-certificate -a -Z -p 2>/dev/null | awk -v target_fp="$candidate_fp" '
    /^SHA-1 hash:/ { match_fp = ($3 == target_fp) }
    match_fp && /^-----BEGIN CERTIFICATE-----/ { in_cert = 1 }
    in_cert { print }
    in_cert && /^-----END CERTIFICATE-----/ { exit }
  ')

  if [ -z "$cert_pem" ]; then
    return 1
  fi

  cert_text=$(printf '%s\n' "$cert_pem" | openssl x509 -text -noout 2>/dev/null || true)
  if [ -z "$cert_text" ]; then
    return 1
  fi

  case "$cert_text" in
    *'1.2.840.113635.100.6.1.13'*'Developer ID Certification Authority'*) ;;
    *'Developer ID Certification Authority'*'1.2.840.113635.100.6.1.13'*) ;;
    *)
      return 1
      ;;
  esac

  tmp_cand=$(mktemp "${TMPDIR:-/tmp}/cand-cert.XXXXXX")
  tmp_root=$(mktemp "${TMPDIR:-/tmp}/apple-root.XXXXXX")
  tmp_inter=$(mktemp "${TMPDIR:-/tmp}/devid-inter.XXXXXX")
  empty_dir=$(mktemp -d "${TMPDIR:-/tmp}/empty-capath.XXXXXX")

  printf '%s\n' "$cert_pem" >"$tmp_cand"

  security find-certificate -a -c "Apple Root CA" -p /System/Library/Keychains/SystemRootCertificates.keychain >"$tmp_root" 2>/dev/null || true
  if [ ! -s "$tmp_root" ]; then
    rm -rf "$tmp_cand" "$tmp_root" "$tmp_inter" "$empty_dir"
    return 1
  fi
  # -ignore_critical is required because Apple marks Developer ID Application extension
  # 1.2.840.113635.100.6.1.13 as critical (TN3161), which standard OpenSSL verify flags as
  # unhandled critical extension. Cryptographic signature and chaining up to Apple Root CA
  # remain enforced by OpenSSL; the required Developer ID leaf OID and issuer are verified above.
  # -CApath is constrained to an empty directory to prevent fallback to system or default CA paths.
  security find-certificate -a -c "Developer ID Certification Authority" -p >"$tmp_inter" 2>/dev/null || true
  if [ -s "$tmp_inter" ]; then
    if ! openssl verify -CAfile "$tmp_root" -untrusted "$tmp_inter" -CApath "$empty_dir" -ignore_critical "$tmp_cand" >/dev/null 2>&1; then
      rm -rf "$tmp_cand" "$tmp_root" "$tmp_inter" "$empty_dir"
      return 1
    fi
  else
    if ! openssl verify -CAfile "$tmp_root" -CApath "$empty_dir" -ignore_critical "$tmp_cand" >/dev/null 2>&1; then
      rm -rf "$tmp_cand" "$tmp_root" "$tmp_inter" "$empty_dir"
      return 1
    fi
  fi
  rm -rf "$tmp_cand" "$tmp_root" "$tmp_inter" "$empty_dir"
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
if [ "$identity_auto_selected" -eq 1 ]; then
  make install "CODESIGN_IDENTITY=$identity" "CODESIGN_REQUIREMENT=$dev_id_requirement"
else
  make install "CODESIGN_IDENTITY=$identity"
fi
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
