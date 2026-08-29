#!/bin/bash
set -euo pipefail
umask 077

BIN=${COGVAULT_BIN:-$HOME/bin/cogvault}
CONFIG=${COGVAULT_CONFIG:-$HOME/.config/cogvault/config.yaml}
TIMEOUT=${ACCESS_CHECK_TIMEOUT:-120}
PRIVATE=
LABEL=
BOOTSTRAPPED=0
export ACCESS_CHECK_HARNESS_PID=$$

die() { echo "error: $*" >&2; exit 1; }
quote() { printf "'%s'" "${1//\'/\'\\\'\'}"; }
recovery() {
  echo "retained access-check artifacts: $PRIVATE" >&2
  echo "delete after inspection: rm -rf $(quote "$PRIVATE")" >&2
}
cleanup_failure() {
  local status=${1:-$?}
  trap - EXIT INT TERM
  if [[ $BOOTSTRAPPED -eq 1 ]]; then
    if ! launchctl bootout "gui/$(id -u)/$LABEL"; then
      echo "cleanup failure: temporary LaunchAgent is still loaded" >&2
      echo "recover job: launchctl bootout $(quote "gui/$(id -u)/$LABEL")" >&2
    fi
  fi
  [[ -n $PRIVATE ]] && recovery
  exit "$status"
}
trap cleanup_failure EXIT
trap 'cleanup_failure 130' INT
trap 'cleanup_failure 143' TERM

[[ $(uname -s) == Darwin ]] || die "Darwin is required"
UID_VALUE=$(id -u) || die "cannot determine uid"
DOMAIN="gui/$UID_VALUE"
launchctl print "$DOMAIN" >/dev/null || die "GUI launchd domain is unavailable: $DOMAIN"
verify_binary() {
  [[ $BIN == /* && -f $BIN && ! -L $BIN && -x $BIN ]] || die "binary must be an absolute regular executable: $BIN"
  codesign --verify --strict "$BIN" || die "binary signature is invalid"
}
[[ $CONFIG == /* && -f $CONFIG && ! -L $CONFIG && -r $CONFIG ]] || die "config must be an absolute readable regular file: $CONFIG"
verify_binary

seal() {
  local identity hash requirement
  identity=$(stat -f '%d:%i:%p:%HT' "$BIN") || return
  hash=$(shasum -a 256 "$BIN" | awk '{print $1}') || return
  requirement=$(codesign -d -r- "$BIN" 2>&1) || return
  printf '%s\n%s\n%s\n' "$identity" "$hash" "$requirement"
}
INITIAL_SEAL=$(seal) || die "cannot seal binary identity"

PRIVATE=$(mktemp -d "${TMPDIR:-/tmp}/cogvault-access-check.XXXXXX") || die "cannot create private directory"
[[ ! -L $PRIVATE && -d $PRIVATE && $(stat -f '%u' "$PRIVATE") == "$UID_VALUE" && $(stat -f '%Lp' "$PRIVATE") == 700 ]] || die "unsafe private directory: $PRIVATE"
LABEL="com.teslamint.cogvault.access-check.$UID_VALUE.$RANDOM.$RANDOM"
echo "label=$LABEL"
echo "Observe both launches. The second run should not show another permission dialog."
PLIST="$PRIVATE/access-check.plist"
plutil -create xml1 "$PLIST"
plutil -insert Label -string "$LABEL" "$PLIST"
plutil -insert ProgramArguments -xml '<array/>' "$PLIST"
plutil -insert ProgramArguments.0 -string "$BIN" "$PLIST"
plutil -insert ProgramArguments.1 -string access-check "$PLIST"
plutil -insert ProgramArguments.2 -string --config "$PLIST"
plutil -insert ProgramArguments.3 -string "$CONFIG" "$PLIST"
plutil -insert StandardOutPath -string "$PRIVATE/current.stdout" "$PLIST"
plutil -insert StandardErrorPath -string "$PRIVATE/current.stderr" "$PLIST"
chmod 600 "$PLIST"
: >"$PRIVATE/current.stdout"
: >"$PRIVATE/current.stderr"
chmod 600 "$PRIVATE/current.stdout" "$PRIVATE/current.stderr"

launchctl bootstrap "$DOMAIN" "$PLIST"
BOOTSTRAPPED=1

run_once() {
  local n=$1 stdout="$PRIVATE/current.stdout" stderr="$PRIVATE/current.stderr" pid start now
  verify_binary
  [[ $(seal) == "$INITIAL_SEAL" ]] || { echo "binary identity changed before run $n" >&2; return 1; }
  : >"$stdout"; : >"$stderr"
  pid=$(launchctl kickstart -kp "$DOMAIN/$LABEL") || return 1
  [[ $pid =~ ^[0-9]+$ ]] || { echo "invalid kickstart pid: $pid" >&2; return 1; }
  start=$SECONDS
  while kill -0 "$pid" 2>/dev/null; do
    now=$SECONDS
    (( now - start < TIMEOUT )) || { echo "run $n timed out after ${TIMEOUT}s" >&2; return 1; }
    sleep 1
  done
  while ! grep -Fq 'configured ingest access check passed' "$stdout"; do
    now=$SECONDS
    (( now - start < TIMEOUT )) || { echo "run $n missing success marker after ${TIMEOUT}s: $stdout" >&2; return 1; }
    sleep .1
  done
  cp "$stdout" "$PRIVATE/run-$n.stdout"
  cp "$stderr" "$PRIVATE/run-$n.stderr"
  chmod 600 "$PRIVATE/run-$n.stdout" "$PRIVATE/run-$n.stderr"
  [[ $(seal) == "$INITIAL_SEAL" ]] || { echo "binary identity changed during verification" >&2; return 1; }
}

run_once 1
run_once 2
launchctl bootout "$DOMAIN/$LABEL"
BOOTSTRAPPED=0
trap - EXIT INT TERM
rm -rf "$PRIVATE"
echo "scheduled configured-path access check passed twice"
