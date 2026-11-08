#!/bin/sh
set -eu

repo_dir=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
test_root=$(mktemp -d)
trap 'rm -rf "$test_root"' EXIT HUP INT TERM

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

  case_dir="$test_root/$name"
  mkdir -p "$case_dir/bin" "$case_dir/home/bin"

  cat >"$case_dir/bin/security" <<EOF
#!/bin/sh
$security_body
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

# 1. Automatic selection of a single Developer ID Application identity
case_dir=$(setup_env "single_dev_id" "printf '%s\n' '  1) ABCD \"Developer ID Application: Test User (TEAM123456)\"'")
log="$case_dir/calls.log"
stdout="$case_dir/out.log"
stderr="$case_dir/err.log"

PATH="$case_dir/bin:/usr/bin:/bin" HOME="$case_dir/home" INSTALL_SIGNED_TEST_LOG="$log" \
  "$repo_dir/scripts/install-signed.sh" >"$stdout" 2>"$stderr"

calls=$(cat "$log")
assert_contains "$calls" 'make <install> <CODESIGN_IDENTITY=Developer ID Application: Test User (TEAM123456)>'
assert_contains "$calls" "codesign <--verify> <--strict> <--verbose=4> <$case_dir/home/bin/cogvault>"
assert_contains "$calls" "codesign <-d> <--verbose=4> <$case_dir/home/bin/cogvault>"
assert_contains "$calls" 'launchctl <print> <gui/'
assert_contains "$calls" 'launchctl <kickstart> <-k> <gui/'
assert_contains "$(cat "$stdout")" 'installed, verified, and restarted gui/'

# 2. Coexistence with Apple Development: automatically selects Developer ID Application
case_dir=$(setup_env "dev_id_and_apple_dev" "printf '%s\n' '  1) ABCD \"Developer ID Application: Org User (TEAM123456)\"' '  2) EFGH \"Apple Development: Dev User (TEAM999999)\"'")
log="$case_dir/calls.log"

PATH="$case_dir/bin:/usr/bin:/bin" HOME="$case_dir/home" INSTALL_SIGNED_TEST_LOG="$log" \
  "$repo_dir/scripts/install-signed.sh" >/dev/null

calls=$(cat "$log")
assert_contains "$calls" 'make <install> <CODESIGN_IDENTITY=Developer ID Application: Org User (TEAM123456)>'

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
assert_contains "$calls" 'make <install> <CODESIGN_IDENTITY=Apple Development: Explicit (TEAM123)>'

# 6. Refusal when installed binary has the wrong code-signing identifier
case_dir=$(setup_env "wrong_identifier" "printf '%s\n' '  1) ABCD \"Developer ID Application: Test User (TEAM123456)\"'" "wrong.identifier")
log="$case_dir/calls.log"
stderr="$case_dir/err.log"

if PATH="$case_dir/bin:/usr/bin:/bin" HOME="$case_dir/home" INSTALL_SIGNED_TEST_LOG="$log" \
  "$repo_dir/scripts/install-signed.sh" >/dev/null 2>"$stderr"; then
  printf 'expected install-signed to fail on wrong identifier\n' >&2
  exit 1
fi
assert_contains "$(cat "$stderr")" 'installed binary has the wrong code-signing identifier'

# 7. Unloaded launchd job skips kickstart cleanly without error
case_dir=$(setup_env "unloaded_job" "printf '%s\n' '  1) ABCD \"Developer ID Application: Test User (TEAM123456)\"'" "dev.tmint.cogvault" 1)
log="$case_dir/calls.log"
stdout="$case_dir/out.log"

PATH="$case_dir/bin:/usr/bin:/bin" HOME="$case_dir/home" INSTALL_SIGNED_TEST_LOG="$log" \
  "$repo_dir/scripts/install-signed.sh" >"$stdout"

calls=$(cat "$log")
assert_contains "$calls" 'launchctl <print> <gui/'
assert_not_contains "$calls" 'launchctl <kickstart>'
assert_contains "$(cat "$stdout")" 'installed and verified; launchd ingest job is not loaded'
