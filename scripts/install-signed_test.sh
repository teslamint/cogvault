#!/bin/sh
set -eu

repo_dir=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT HUP INT TERM

fake_bin="$tmp_dir/bin"
fake_home="$tmp_dir/home"
log="$tmp_dir/calls.log"
mkdir -p "$fake_bin" "$fake_home/bin"

cat >"$fake_bin/security" <<'EOF'
#!/bin/sh
printf '%s\n' '  1) ABCD "Developer ID Application: Test User (TEAM123456)"'
printf '%s\n' '     1 valid identities found'
EOF

cat >"$fake_bin/make" <<'EOF'
#!/bin/sh
printf 'make' >>"$INSTALL_SIGNED_TEST_LOG"
for arg in "$@"; do printf ' <%s>' "$arg" >>"$INSTALL_SIGNED_TEST_LOG"; done
printf '\n' >>"$INSTALL_SIGNED_TEST_LOG"
: >"$HOME/bin/cogvault"
EOF

cat >"$fake_bin/codesign" <<'EOF'
#!/bin/sh
printf 'codesign' >>"$INSTALL_SIGNED_TEST_LOG"
for arg in "$@"; do printf ' <%s>' "$arg" >>"$INSTALL_SIGNED_TEST_LOG"; done
printf '\n' >>"$INSTALL_SIGNED_TEST_LOG"
case " $* " in
  *' -d '*) printf '%s\n' 'Identifier=dev.tmint.cogvault' >&2 ;;
esac
EOF

cat >"$fake_bin/launchctl" <<'EOF'
#!/bin/sh
printf 'launchctl' >>"$INSTALL_SIGNED_TEST_LOG"
for arg in "$@"; do printf ' <%s>' "$arg" >>"$INSTALL_SIGNED_TEST_LOG"; done
printf '\n' >>"$INSTALL_SIGNED_TEST_LOG"
EOF

chmod +x "$fake_bin/security" "$fake_bin/make" "$fake_bin/codesign" "$fake_bin/launchctl"

PATH="$fake_bin:/usr/bin:/bin" HOME="$fake_home" INSTALL_SIGNED_TEST_LOG="$log" \
  "$repo_dir/scripts/install-signed.sh"

calls=$(cat "$log")
assert_contains() {
  case "$calls" in
    *"$1"*) ;;
    *) printf 'missing call: %s\n' "$1" >&2; exit 1 ;;
  esac
}

assert_contains 'make <install> <CODESIGN_IDENTITY=Developer ID Application: Test User (TEAM123456)>'
assert_contains "codesign <--verify> <--strict> <--verbose=4> <$fake_home/bin/cogvault>"
assert_contains "codesign <-d> <--verbose=4> <$fake_home/bin/cogvault>"
assert_contains 'launchctl <print> <gui/'
assert_contains 'launchctl <kickstart> <-k> <gui/'
