#!/bin/sh
set -eu

repo_dir=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
test_root=$(mktemp -d)
trap 'rm -rf "$test_root"' EXIT HUP INT TERM

# Generate test PKI for Apple Root CA and Developer ID hierarchy
openssl req -x509 -newkey rsa:2048 -nodes -keyout "$test_root/root.key" -out "$test_root/apple_root.pem" -days 1 \
  -subj "/CN=Apple Root CA/OU=Apple Certification Authority/O=Apple Inc./C=US" 2>/dev/null

openssl req -new -newkey rsa:2048 -nodes -keyout "$test_root/inter.key" -out "$test_root/inter.csr" \
  -subj "/CN=Developer ID Certification Authority/OU=Apple Certification Authority/O=Apple Inc./C=US" 2>/dev/null
printf 'basicConstraints = critical,CA:TRUE\nkeyUsage = critical,keyCertSign,cRLSign\n' > "$test_root/inter.ext"
openssl x509 -req -in "$test_root/inter.csr" -CA "$test_root/apple_root.pem" -CAkey "$test_root/root.key" \
  -set_serial 1 -days 1 -extfile "$test_root/inter.ext" -out "$test_root/devid_inter.pem" 2>/dev/null

printf '1.2.840.113635.100.6.1.13 = critical,ASN1:NULL\n' > "$test_root/leaf.ext"
openssl req -new -newkey rsa:2048 -nodes -keyout "$test_root/leaf.key" -out "$test_root/leaf.csr" \
  -subj "/UID=ABCD/CN=Developer ID Application: Test User (TEAM123456)/OU=TEAM123456/O=Test Org/C=US" 2>/dev/null
openssl x509 -req -in "$test_root/leaf.csr" -CA "$test_root/devid_inter.pem" -CAkey "$test_root/inter.key" \
  -set_serial 2 -days 1 -extfile "$test_root/leaf.ext" -out "$test_root/genuine_devid.pem" 2>/dev/null

# Private CA with copied issuer name and a fake cert with copied OID
openssl req -x509 -newkey rsa:2048 -nodes -keyout "$test_root/priv.key" -out "$test_root/priv_ca.pem" -days 1 \
  -subj "/CN=Developer ID Certification Authority/OU=Apple Certification Authority/O=Apple Inc./C=US" 2>/dev/null
openssl req -new -newkey rsa:2048 -nodes -keyout "$test_root/fake.key" -out "$test_root/fake.csr" \
  -subj "/UID=FAKE1234/CN=Developer ID Application: Fake User (TEAM123456)/OU=TEAM123456/O=Fake Org/C=US" 2>/dev/null
openssl x509 -req -in "$test_root/fake.csr" -CA "$test_root/priv_ca.pem" -CAkey "$test_root/priv.key" \
  -set_serial 3 -days 1 -extfile "$test_root/leaf.ext" -out "$test_root/fake_devid.pem" 2>/dev/null

assert_contains() {
  haystack=$1
  needle=$2
  case "$haystack" in
    *"$needle"*) ;;
    *)
      printf 'expected to find "%s" in:\n%s\n' "$needle" "$haystack" >&2
      exit 1
      ;;
  esac
}

assert_not_contains() {
  haystack=$1
  needle=$2
  case "$haystack" in
    *"$needle"*)
      printf 'expected NOT to find "%s" in:\n%s\n' "$needle" "$haystack" >&2
      exit 1
      ;;
    *) ;;
  esac
}

setup_env() {
  name=$1
  security_body=${2-}
  codesign_id=${3-dev.tmint.cogvault}
  launchctl_exit=${4-0}
  codesign_req_exit=${5-0}

  case_dir="$test_root/$name"
  mkdir -p "$case_dir/bin" "$case_dir/home/bin"
  cat >"$case_dir/bin/security" <<EOF
#!/bin/sh
printf 'security' >>"\$INSTALL_SIGNED_TEST_LOG"
for arg in "\$@"; do printf ' <%s>' "\$arg" >>"\$INSTALL_SIGNED_TEST_LOG"; done
printf '\n' >>"\$INSTALL_SIGNED_TEST_LOG"
case " \$* " in
  *"find-identity"*)
    $security_body
    ;;
  *"Apple Root CA"*)
    cat "$test_root/apple_root.pem"
    ;;
  *"Developer ID Certification Authority"*)
    cat "$test_root/devid_inter.pem"
    ;;
  *"find-certificate"*)
    printf 'SHA-1 hash: ABCD\n'
    cat "$test_root/genuine_devid.pem"
    printf 'SHA-1 hash: EFGH\n'
    cat "$test_root/genuine_devid.pem"
    printf 'SHA-1 hash: ABCD1234EF567890\n'
    cat "$test_root/genuine_devid.pem"
    printf 'SHA-1 hash: 1111111111111111\n'
    cat "$test_root/genuine_devid.pem"
    printf 'SHA-1 hash: 2222222222222222\n'
    cat "$test_root/genuine_devid.pem"
    printf 'SHA-1 hash: FAKE1234\n'
    cat "$test_root/fake_devid.pem"
    ;;
  *)
    $security_body
    ;;
esac
EOF

  cat >"$case_dir/bin/make" <<'EOF'
#!/bin/sh
printf 'make' >>"$INSTALL_SIGNED_TEST_LOG"
for arg in "$@"; do printf ' <%s>' "$arg" >>"$INSTALL_SIGNED_TEST_LOG"; done
printf '\n' >>"$INSTALL_SIGNED_TEST_LOG"
: >"$HOME/bin/cogvault"
EOF

  cat >"$case_dir/bin/codesign" <<EOF
#!/bin/sh
printf 'codesign' >>"\$INSTALL_SIGNED_TEST_LOG"
for arg in "\$@"; do printf ' <%s>' "\$arg" >>"\$INSTALL_SIGNED_TEST_LOG"; done
printf '\n' >>"\$INSTALL_SIGNED_TEST_LOG"
case " \$* " in
  *' -d '*) printf '%s\n' 'Identifier=$codesign_id' >&2 ;;
  *' -R '*)
    if [ "$codesign_req_exit" -ne 0 ]; then
      printf '%s\n' 'test-requirement: code failed to satisfy specified code requirement(s)' >&2
      exit "$codesign_req_exit"
    fi
    ;;
esac
EOF

  cat >"$case_dir/bin/launchctl" <<EOF
#!/bin/sh
printf 'launchctl' >>"\$INSTALL_SIGNED_TEST_LOG"
for arg in "\$@"; do printf ' <%s>' "\$arg" >>"\$INSTALL_SIGNED_TEST_LOG"; done
printf '\n' >>"\$INSTALL_SIGNED_TEST_LOG"
exit $launchctl_exit
EOF

  chmod +x "$case_dir/bin/security" "$case_dir/bin/make" "$case_dir/bin/codesign" "$case_dir/bin/launchctl"
  printf '%s' "$case_dir"
}
single_dev_id_security="printf '%s\n' '  1) ABCD \"Developer ID Application: Test User (TEAM123456)\"'"
make_install_cmd='make <install>'

# 1. Automatic selection of a single Developer ID Application identity
case_dir=$(setup_env "single_dev_id" "$single_dev_id_security")
log="$case_dir/calls.log"
stdout="$case_dir/out.log"
stderr="$case_dir/err.log"

PATH="$case_dir/bin:/usr/bin:/bin" HOME="$case_dir/home" INSTALL_SIGNED_TEST_LOG="$log" \
  "$repo_dir/scripts/install-signed.sh" >"$stdout" 2>"$stderr"

calls=$(cat "$log")
assert_contains "$calls" "$make_install_cmd"
assert_contains "$calls" '<CODESIGN_IDENTITY=ABCD>'
assert_contains "$calls" 'CODESIGN_REQUIREMENT==anchor apple generic and certificate 1[field.1.2.840.113635.100.6.2.6] exists and certificate leaf[field.1.2.840.113635.100.6.1.13] exists'
assert_contains "$calls" "codesign <--verify> <--strict> <--verbose=4> <$case_dir/home/bin/cogvault>"
assert_contains "$calls" 'codesign <--verify> <--strict> <-R> <=anchor apple generic and certificate 1[field.1.2.840.113635.100.6.2.6] exists and certificate leaf[field.1.2.840.113635.100.6.1.13] exists>'
assert_contains "$calls" "codesign <-d> <--verbose=4> <$case_dir/home/bin/cogvault>"
assert_contains "$calls" 'launchctl <kickstart> <-k> <gui/'
assert_contains "$(cat "$stdout")" 'installed, verified, and restarted gui/'

# 2. Coexistence with Apple Development: automatically selects Developer ID Application
case_dir=$(setup_env "dev_id_and_apple_dev" "printf '%s\n' '  1) ABCD \"Developer ID Application: Org User (TEAM123456)\"' '  2) EFGH \"Apple Development: Dev User (TEAM999999)\"'")
log="$case_dir/calls.log"

PATH="$case_dir/bin:/usr/bin:/bin" HOME="$case_dir/home" INSTALL_SIGNED_TEST_LOG="$log" \
  "$repo_dir/scripts/install-signed.sh" >/dev/null

calls=$(cat "$log")
assert_contains "$calls" "$make_install_cmd"
assert_contains "$calls" '<CODESIGN_IDENTITY=ABCD>'
# 2a. Duplicate copies of the same certificate across keychains (identical fingerprint): deduplicates to 1
case_dir=$(setup_env "duplicate_same_cert" "printf '%s\n' '  1) ABCD1234EF567890 \"Developer ID Application: Shared Cert (TEAM123)\"' '  2) ABCD1234EF567890 \"Developer ID Application: Shared Cert (TEAM123)\"'")
log="$case_dir/calls.log"

PATH="$case_dir/bin:/usr/bin:/bin" HOME="$case_dir/home" INSTALL_SIGNED_TEST_LOG="$log" \
  "$repo_dir/scripts/install-signed.sh" >/dev/null

calls=$(cat "$log")
assert_contains "$calls" "$make_install_cmd"
assert_contains "$calls" '<CODESIGN_IDENTITY=ABCD1234EF567890>'
# 2b. Distinct certificates sharing the same name (different fingerprints): not deduplicated, fails with found 2
case_dir=$(setup_env "different_certs_same_name" "printf '%s\n' '  1) 1111111111111111 \"Developer ID Application: Same Name (TEAM123)\"' '  2) 2222222222222222 \"Developer ID Application: Same Name (TEAM123)\"'")
log="$case_dir/calls.log"
stderr="$case_dir/err.log"

if PATH="$case_dir/bin:/usr/bin:/bin" HOME="$case_dir/home" INSTALL_SIGNED_TEST_LOG="$log" \
  "$repo_dir/scripts/install-signed.sh" >/dev/null 2>"$stderr"; then
  printf 'expected install-signed to fail when distinct certs share the same name\n' >&2
  exit 1
fi
assert_contains "$(cat "$stderr")" 'expected exactly one valid Developer ID Application identity; found 2'

# 2c. Non-Developer ID cert containing "Developer ID Application:" in the name: not selected
case_dir=$(setup_env "name_contains_but_not_prefix" "printf '%s\n' '  1) 9999999999999999 \"Apple Development: Developer ID Application: Fake (TEAM123)\"'")
log="$case_dir/calls.log"
stderr="$case_dir/err.log"

if PATH="$case_dir/bin:/usr/bin:/bin" HOME="$case_dir/home" INSTALL_SIGNED_TEST_LOG="$log" \
  "$repo_dir/scripts/install-signed.sh" >/dev/null 2>"$stderr"; then
  printf 'expected install-signed to fail when cert only contains Developer ID in substring\n' >&2
  exit 1
fi
assert_contains "$(cat "$stderr")" 'expected exactly one valid Developer ID Application identity; found 0'

# 2d. Non-Apple or self-signed cert with "Developer ID Application:" name: rejected by Apple trust requirement
# 2d. Non-Apple / private CA cert with copied issuer and copied OID: rejected by Apple Root CA verification
case_dir=$(setup_env "non_apple_ca_rejected" \
  "printf '%s\n' '  1) FAKE1234 \"Developer ID Application: Fake User (TEAM123456)\"'")
log="$case_dir/calls.log"
stderr="$case_dir/err.log"

if PATH="$case_dir/bin:/usr/bin:/bin" HOME="$case_dir/home" INSTALL_SIGNED_TEST_LOG="$log" \
  "$repo_dir/scripts/install-signed.sh" >/dev/null 2>"$stderr"; then
  printf 'expected install-signed to fail when candidate cert fails Apple Developer ID requirement\n' >&2
  exit 1
fi
assert_contains "$(cat "$stderr")" 'expected exactly one valid Developer ID Application identity; found 0'
calls=$(cat "$log")
assert_not_contains "$calls" "$make_install_cmd"

# 2e. Coexistence of genuine Apple Developer ID identity and private CA identity with copied issuer/OID
case_dir=$(setup_env "coexistence_genuine_and_fake" \
  "$single_dev_id_security '  2) FAKE1234 \"Developer ID Application: Fake User (TEAM123456)\"'")
log="$case_dir/calls.log"

PATH="$case_dir/bin:/usr/bin:/bin" HOME="$case_dir/home" INSTALL_SIGNED_TEST_LOG="$log" \
  "$repo_dir/scripts/install-signed.sh" >/dev/null

calls=$(cat "$log")
assert_contains "$calls" "$make_install_cmd"
assert_contains "$calls" '<CODESIGN_IDENTITY=ABCD>'
# 3. Refusal when 0 Developer ID Application identities exist (e.g. only Apple Development)
case_dir=$(setup_env "zero_dev_id" "printf '%s\n' '  1) EFGH \"Apple Development: Dev User (TEAM999999)\"'")
log="$case_dir/calls.log"
stderr="$case_dir/err.log"

if PATH="$case_dir/bin:/usr/bin:/bin" HOME="$case_dir/home" INSTALL_SIGNED_TEST_LOG="$log" \
  "$repo_dir/scripts/install-signed.sh" >/dev/null 2>"$stderr"; then
  printf 'expected install-signed to fail when 0 Developer ID identities exist\n' >&2
  exit 1
fi
assert_contains "$(cat "$stderr")" 'expected exactly one valid Developer ID Application identity; found 0'
assert_contains "$(cat "$stderr")" 'pass the full identity as the first argument when more than one exists'

# 4. Refusal when multiple Developer ID Application identities exist
case_dir=$(setup_env "multiple_dev_id" "printf '%s\n' '  1) ABCD \"Developer ID Application: First (TEAM1)\"' '  2) EFGH \"Developer ID Application: Second (TEAM2)\"'")
log="$case_dir/calls.log"
stderr="$case_dir/err.log"

if PATH="$case_dir/bin:/usr/bin:/bin" HOME="$case_dir/home" INSTALL_SIGNED_TEST_LOG="$log" \
  "$repo_dir/scripts/install-signed.sh" >/dev/null 2>"$stderr"; then
  printf 'expected install-signed to fail when multiple Developer ID identities exist\n' >&2
  exit 1
fi
assert_contains "$(cat "$stderr")" 'expected exactly one valid Developer ID Application identity; found 2'
assert_contains "$(cat "$stderr")" 'pass the full identity as the first argument when more than one exists'

# 5. Explicit identity passed as argument: used directly without calling security
case_dir=$(setup_env "explicit_identity" "printf 'unexpected security call\n' >&2; exit 99")
log="$case_dir/calls.log"

PATH="$case_dir/bin:/usr/bin:/bin" HOME="$case_dir/home" INSTALL_SIGNED_TEST_LOG="$log" \
  "$repo_dir/scripts/install-signed.sh" "Apple Development: Explicit (TEAM123)" >/dev/null

calls=$(cat "$log")
assert_contains "$calls" "$make_install_cmd"
assert_contains "$calls" '<CODESIGN_IDENTITY=Apple Development: Explicit (TEAM123)>'
# 6. Refusal when installed binary has the wrong code-signing identifier
case_dir=$(setup_env "wrong_identifier" "$single_dev_id_security" "wrong.identifier")
log="$case_dir/calls.log"
stderr="$case_dir/err.log"

if PATH="$case_dir/bin:/usr/bin:/bin" HOME="$case_dir/home" INSTALL_SIGNED_TEST_LOG="$log" \
  "$repo_dir/scripts/install-signed.sh" >/dev/null 2>"$stderr"; then
  printf 'expected install-signed to fail on wrong identifier\n' >&2
  exit 1
fi
assert_contains "$(cat "$stderr")" 'installed binary has the wrong code-signing identifier'

# 7. Unloaded launchd job skips kickstart cleanly without error
case_dir=$(setup_env "unloaded_job" "$single_dev_id_security" "dev.tmint.cogvault" 1)
log="$case_dir/calls.log"
stdout="$case_dir/out.log"

PATH="$case_dir/bin:/usr/bin:/bin" HOME="$case_dir/home" INSTALL_SIGNED_TEST_LOG="$log" \
  "$repo_dir/scripts/install-signed.sh" >"$stdout"

calls=$(cat "$log")
assert_contains "$calls" 'launchctl <print> <gui/'
assert_not_contains "$calls" 'launchctl <kickstart>'
assert_contains "$(cat "$stdout")" 'installed and verified; launchd ingest job is not loaded'

# 8. Signal interruption (SIGINT) cleans up temporary artifacts and aborts before make/launchctl
case_dir=$(setup_env "interrupt_cleanup" "$single_dev_id_security")
log="$case_dir/calls.log"
tmp_test="$case_dir/tmp"
mkdir -p "$tmp_test"

cat >"$case_dir/bin/openssl" <<'EOF'
#!/bin/sh
case " $* " in
  *" verify "*)
    kill -INT "$PPID"
    exit 0
    ;;
  *)
    exec /usr/bin/openssl "$@"
    ;;
esac
EOF
chmod +x "$case_dir/bin/openssl"

exit_code=0
PATH="$case_dir/bin:/usr/bin:/bin" HOME="$case_dir/home" TMPDIR="$tmp_test" INSTALL_SIGNED_TEST_LOG="$log" \
  "$repo_dir/scripts/install-signed.sh" >/dev/null 2>&1 || exit_code=$?

if [ "$exit_code" -eq 0 ]; then
  printf 'expected install-signed to exit non-zero on SIGINT; got 0\n' >&2
  exit 1
fi

calls=$(cat "$log" 2>/dev/null || true)
assert_not_contains "$calls" "$make_install_cmd"
assert_not_contains "$calls" 'launchctl'

leftover=$(ls -A "$tmp_test" 2>/dev/null || true)
if [ -n "$leftover" ]; then
  printf 'interruption leaked temporary verification artifacts: %s\n' "$leftover" >&2
  exit 1
fi

# 9. Concurrent make install with differing signature requirements is serialized by transaction lock
case_dir=$(setup_env "concurrent_differing_signatures" "$single_dev_id_security")
log="$case_dir/calls.log"
bg_done="$case_dir/bg_done"

# Simulate concurrent make install attempting to overwrite destination binary during install-signed
cat >"$case_dir/bin/make" <<EOF
#!/bin/sh
printf 'make' >>"\$INSTALL_SIGNED_TEST_LOG"
for arg in "\$@"; do printf ' <%s>' "\$arg" >>"\$INSTALL_SIGNED_TEST_LOG"; done
printf '\n' >>"\$INSTALL_SIGNED_TEST_LOG"

case " \$* " in
  *"CODESIGN_REQUIREMENT="*)
    printf 'signed-build\n' >"\$HOME/bin/cogvault"
    # Spawn concurrent plain install under install-lock.sh
    (
      for fd in 3 4 5 6 7 8 9; do eval "exec \$fd>&-" 2>/dev/null || true; done
      env -u COGVAULT_INSTALL_LOCK_HELD sh "$repo_dir/scripts/install-lock.sh" "\$HOME/bin" \
        sh -c 'printf "adhoc-overwrite\n" > "\$HOME/bin/cogvault"; touch "'"$bg_done"'"' 2>"$case_dir/bg_err.log"
    ) &
    echo "\$!" > "$case_dir/bg_pid"
    sleep 0.05
    ;;
esac
EOF
chmod +x "$case_dir/bin/make"

# In codesign, verify that destination binary remained signed-build during the entire verification window
cat >"$case_dir/bin/codesign" <<'EOF'
#!/bin/sh
target=""
for arg in "$@"; do target="$arg"; done
if [ -f "$target" ]; then
  content=$(cat "$target")
  if [ "$content" = 'adhoc-overwrite' ]; then
    printf 'codesign: concurrent install broke exclusion barrier; binary was overwritten prematurely\n' >&2
    exit 1
  fi
fi

case " $* " in
  *" -d "*)
    printf 'Executable=/test\nIdentifier=dev.tmint.cogvault\nFormat=Mach-O thin (arm64)\n'
    exit 0
    ;;
  *)
    exit 0
    ;;
esac
EOF
chmod +x "$case_dir/bin/codesign"

PATH="$case_dir/bin:/usr/bin:/bin" HOME="$case_dir/home" INSTALL_SIGNED_TEST_LOG="$log" \
  "$repo_dir/scripts/install-signed.sh" >/dev/null

calls=$(cat "$log" 2>/dev/null || true)
assert_contains "$calls" "$make_install_cmd <INSTALL_DIR="
assert_contains "$calls" '<CODESIGN_IDENTITY=ABCD> <CODESIGN_REQUIREMENT='
assert_contains "$calls" 'launchctl <print> <gui/'

# Verify that background concurrent install completes after install-signed releases lock
bg_pid=$(cat "$case_dir/bg_pid" 2>/dev/null || true)
attempts=0
while [ -n "$bg_pid" ] && kill -0 "$bg_pid" 2>/dev/null && [ "$attempts" -lt 200 ]; do
  sleep 0.05
  attempts=$((attempts + 1))
done

if [ ! -f "$bg_done" ]; then
  bg_err=$(cat "$case_dir/bg_err.log" 2>/dev/null || true)
  printf 'expected background concurrent install to complete after lock release; err: %s\n' "$bg_err" >&2
  exit 1
fi

final_content=$(cat "$case_dir/home/bin/cogvault")
if [ "$final_content" != 'adhoc-overwrite' ]; then
  printf 'expected background install to succeed after lock release; got %s\n' "$final_content" >&2
  exit 1
fi

# 10. Custom INSTALL_DIR propagates to make install and verifies custom binary path
case_dir=$(setup_env "custom_install_dir" "$single_dev_id_security")
log="$case_dir/calls.log"
custom_bin="$case_dir/custom_bin"
mkdir -p "$custom_bin"
: >"$custom_bin/cogvault"

PATH="$case_dir/bin:/usr/bin:/bin" HOME="$case_dir/home" INSTALL_DIR="$custom_bin" INSTALL_SIGNED_TEST_LOG="$log" \
  "$repo_dir/scripts/install-signed.sh" >/dev/null

calls=$(cat "$log" 2>/dev/null || true)
assert_contains "$calls" "$make_install_cmd <INSTALL_DIR=$custom_bin> <CODESIGN_IDENTITY=ABCD>"
assert_contains "$calls" "codesign <--verify> <--strict> <--verbose=4> <$custom_bin/cogvault>"

# 11. Pre-existing / empty lock file does not impede installation
: >"$log"
touch "$custom_bin/.cogvault.install.lock"
PATH="$case_dir/bin:/usr/bin:/bin" HOME="$case_dir/home" INSTALL_DIR="$custom_bin" INSTALL_SIGNED_TEST_LOG="$log" \
  "$repo_dir/scripts/install-signed.sh" >/dev/null

calls=$(cat "$log" 2>/dev/null || true)
assert_contains "$calls" "$make_install_cmd <INSTALL_DIR=$custom_bin> <CODESIGN_IDENTITY=ABCD>"
