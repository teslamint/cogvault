#!/bin/bash
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
HARNESS=${HARNESS:-$ROOT/scripts/check-scheduled-access.sh}
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

fail() { echo "FAIL: $*" >&2; exit 1; }
assert_contains() { [[ $1 == *"$2"* ]] || fail "expected [$2] in [$1]"; }
assert_mode_600() { [[ -f $1 && $(/usr/bin/stat -f '%Lp' "$1") == 600 ]] || fail "required 0600 artifact missing: $1"; }
assert_retained() {
  local dir=$1 runs=${2:-0}
  assert_mode_600 "$dir/state/private/access-check.plist"
  assert_mode_600 "$dir/state/private/current.stdout"
  assert_mode_600 "$dir/state/private/current.stderr"
  local n
  for ((n=1; n<=runs; n++)); do
    assert_mode_600 "$dir/state/private/run-$n.stdout"
    assert_mode_600 "$dir/state/private/run-$n.stderr"
  done
}
assert_job_absent() { [[ ! -e $1/state/bootstrapped ]] || fail "temporary job still loaded: $1"; }
assert_job_present() { [[ -e $1/state/bootstrapped ]] || fail "expected loaded job state: $1"; }

make_fixture() {
  local name=$1 mode=${2:-happy} dir
  dir="$TMP/$name"
  mkdir -p "$dir/bin" "$dir/home" "$dir/state"
  printf 'config\n' >"$dir/config file & test.yml"
  cat >"$dir/cogvault bin" <<'EOF'
#!/bin/bash
printf '%s\n' "$(( $(cat "$FAKE_STATE/execution-count" 2>/dev/null || echo 0) + 1 ))" >"$FAKE_STATE/execution-count"
printf '%q ' "$0" "$@" >>"$FAKE_STATE/executions"; printf '\n' >>"$FAKE_STATE/executions"
echo 'configured ingest access check passed'
EOF
  chmod +x "$dir/cogvault bin"
  cat >"$dir/bin/uname" <<'EOF'
#!/bin/bash
[[ ${FAKE_MODE:-} == non-darwin ]] && { echo Linux; exit; }
echo Darwin
EOF
  cat >"$dir/bin/id" <<'EOF'
#!/bin/bash
[[ ${FAKE_MODE:-} == uid-failure ]] && exit 1
[[ ${1:-} == -u ]] && echo 501
EOF
  cat >"$dir/bin/codesign" <<'EOF'
#!/bin/bash
[[ ${FAKE_MODE:-} == invalid-signature ]] && exit 1
if [[ $1 == --verify ]]; then
  c="$FAKE_STATE/verify-count"; n=0; [[ -f $c ]] && read -r n <"$c"; n=$((n+1)); echo "$n" >"$c"
  [[ ${FAKE_MODE:-} == second-run-signature-failure && $n -ge 3 ]] && exit 1
fi
if [[ $1 == -d ]]; then
  c="$FAKE_STATE/requirement-count"; n=0; [[ -f $c ]] && read -r n <"$c"; echo $((n+1)) >"$c"
  echo 'designated => identifier "dev.tmint.cogvault"' >&2
fi
EOF
  cat >"$dir/bin/shasum" <<'EOF'
#!/bin/bash
file=${@: -1}; c="$FAKE_STATE/shasum-count"; n=0; [[ -f $c ]] && read -r n <"$c"; n=$((n+1)); echo "$n" >"$c"
if [[ ${FAKE_MODE:-} == identity-change && $n -gt 1 ]] ||
   [[ ${FAKE_MODE:-} == after-first-identity-change && $n -eq 3 ]] ||
   [[ ${FAKE_MODE:-} == after-second-identity-change && $n -eq 5 ]]; then
  echo "changed  $file"
else
  echo "stable  $file"
fi
EOF
  cat >"$dir/bin/stat" <<'EOF'
#!/bin/bash
path=${@: -1}
if [[ $path == "$COGVAULT_BIN" && ${1:-} == -f ]]; then
  c="$FAKE_STATE/identity-count"; n=0; [[ -f $c ]] && read -r n <"$c"; echo $((n+1)) >"$c"
  echo '1:2:755:regular file'; exit
fi
if [[ $path == "$FAKE_STATE/private"* ]]; then
  [[ ${1:-} == -f ]] && { [[ $2 == %u ]] && echo 501 || { [[ -d $path ]] && echo 700 || echo 600; }; exit; }
fi
[[ ${1:-} == -f ]] && echo '1:2:755:regular file' || /usr/bin/stat "$@"
EOF
  cat >"$dir/bin/plutil" <<'EOF'
#!/bin/bash
printf '%q ' "$@" >>"$FAKE_STATE/plutil-transcript"; printf '\n' >>"$FAKE_STATE/plutil-transcript"
[[ $1 == -create ]] && : >"${@: -1}"
if [[ $1 == -insert ]]; then
  key=$2
  if [[ $3 == -string ]]; then printf '%s' "$4" >"$FAKE_STATE/plist.$key"; fi
fi
exit 0
EOF
  cat >"$dir/bin/mktemp" <<'EOF'
#!/bin/bash
mkdir -p "$FAKE_STATE/private"; chmod 700 "$FAKE_STATE/private"; echo "$FAKE_STATE/private"
EOF
  cat >"$dir/bin/launchctl" <<'EOF'
#!/bin/bash
set -e
cmd=$1; shift
echo "$cmd $*" >>"$FAKE_STATE/transcript"
case $cmd in
  print) [[ ${FAKE_MODE:-} == gui-unavailable ]] && exit 1; exit 0 ;;
  bootstrap)
    for key in Label ProgramArguments.0 ProgramArguments.1 ProgramArguments.2 ProgramArguments.3; do
      [[ -f "$FAKE_STATE/plist.$key" ]] || exit 1
      cp "$FAKE_STATE/plist.$key" "$FAKE_STATE/registered.$key"
    done
    touch "$FAKE_STATE/bootstrapped"
    /usr/bin/stat -f '%Lp' "$FAKE_STATE/private" >"$FAKE_STATE/dir-mode"
    /usr/bin/stat -f '%Lp' "$FAKE_STATE/private/access-check.plist" >"$FAKE_STATE/plist-mode"
    ;;
  kickstart)
    target=${@: -1}; registered_label=$(<"$FAKE_STATE/registered.Label")
    [[ $target == "gui/501/$registered_label" ]] || exit 1
    c="$FAKE_STATE/kicks"; n=0; [[ -f $c ]] && read -r n <"$c"; n=$((n+1)); echo "$n" >"$c"
    plist="$FAKE_STATE/private/access-check.plist"; out="$FAKE_STATE/private/current.stdout"; err="$FAKE_STATE/private/current.stderr"
    program=$(<"$FAKE_STATE/registered.ProgramArguments.0")
    arg1=$(<"$FAKE_STATE/registered.ProgramArguments.1"); arg2=$(<"$FAKE_STATE/registered.ProgramArguments.2"); arg3=$(<"$FAKE_STATE/registered.ProgramArguments.3")
    "$program" "$arg1" "$arg2" "$arg3" >"$out" 2>"$err"
    [[ ${FAKE_MODE:-} == missing-marker ]] && : >"$out"
    if [[ ${FAKE_MODE:-} == signal-INT ]]; then (sleep .1; kill -INT "$ACCESS_CHECK_HARNESS_PID") >/dev/null 2>&1 & fi
    if [[ ${FAKE_MODE:-} == signal-TERM ]]; then (sleep .1; kill -TERM "$ACCESS_CHECK_HARNESS_PID") >/dev/null 2>&1 & fi
    if [[ ${FAKE_MODE:-} == timeout || ${FAKE_MODE:-} == signal-INT || ${FAKE_MODE:-} == signal-TERM ]]; then sleep 10 >/dev/null 2>&1 & echo $!; else echo 999999; fi
    ;;
  bootout)
    [[ ${INJECT_ARTIFACT_LOSS:-0} == 1 ]] && rm -f "$FAKE_STATE/private/access-check.plist"
    [[ ${FAKE_MODE:-} == bootout-failure ]] && exit 1
    rm -f "$FAKE_STATE/bootstrapped"
    ;;
esac
EOF
  chmod +x "$dir/bin/"*
  printf '%s\n' "$dir"
}

run_case() {
  local name=$1 mode=${2:-happy}; shift 2 || true
  local dir; dir=$(make_fixture "$name" "$mode")
  set +e
  output=$(env PATH="$dir/bin:/usr/bin:/bin" HOME="$dir/home" USER="${USER:-tester}" FAKE_STATE="$dir/state" FAKE_MODE="$mode" ACCESS_CHECK_TIMEOUT=1 COGVAULT_BIN="$dir/cogvault bin" COGVAULT_CONFIG="$dir/config file & test.yml" "$HARNESS" "$@" 2>&1)
  status=$?
  set -e
  CASE_DIR=$dir CASE_OUTPUT=$output CASE_STATUS=$status
}

if [[ ${ARTIFACT_LOSS_PROBE:-0} == 1 ]]; then
  run_case artifact-loss missing-marker
  [[ $CASE_STATUS -ne 0 ]] || fail "artifact-loss probe unexpectedly passed harness"
  assert_retained "$CASE_DIR" 0
  exit 0
fi

run_case happy happy
[[ $CASE_STATUS -eq 0 ]] || fail "happy path: $CASE_OUTPUT"
assert_contains "$CASE_OUTPUT" "label=com.teslamint.cogvault.access-check."
assert_contains "$CASE_OUTPUT" "The second run should not show another permission dialog."
[[ $(<"$CASE_DIR/state/kicks") == 2 ]] || fail "expected two runs"
[[ $(<"$CASE_DIR/state/verify-count") == 3 ]] || fail "signature was not checked at preflight and before both runs"
[[ $(<"$CASE_DIR/state/shasum-count") == 5 ]] || fail "seal was not checked at all five boundaries"
[[ $(<"$CASE_DIR/state/requirement-count") == 5 ]] || fail "designated requirement call count"
[[ $(<"$CASE_DIR/state/identity-count") == 5 ]] || fail "file identity call count"
[[ $(<"$CASE_DIR/state/execution-count") == 2 ]] || fail "registered program was not executed twice"
[[ $(<"$CASE_DIR/state/registered.ProgramArguments.0") == "$CASE_DIR/cogvault bin" ]] || fail "registered binary differs"
[[ $(<"$CASE_DIR/state/registered.ProgramArguments.1") == access-check ]] || fail "registered subcommand differs"
[[ $(<"$CASE_DIR/state/registered.ProgramArguments.2") == --config ]] || fail "registered config flag differs"
[[ $(<"$CASE_DIR/state/registered.ProgramArguments.3") == "$CASE_DIR/config file & test.yml" ]] || fail "registered config path differs"
[[ $(<"$CASE_DIR/state/dir-mode") == 700 && $(<"$CASE_DIR/state/plist-mode") == 600 ]] || fail "private modes"
[[ ! -e "$CASE_DIR/state/private" ]] || fail "success artifacts retained"
grep -q 'bootstrap gui/501' "$CASE_DIR/state/transcript" || fail "wrong GUI bootstrap"
[[ $(grep -c '^kickstart ' "$CASE_DIR/state/transcript") == 2 ]] || fail "kickstart count"
[[ $(sed -n 's/^kickstart -kp //p' "$CASE_DIR/state/transcript" | sort -u | wc -l | tr -d ' ') == 1 ]] || fail "runs used different labels"
grep -Fq "ProgramArguments.0 -string $CASE_DIR/cogvault\\ bin" "$CASE_DIR/state/plutil-transcript" || fail "selected binary was not the direct program argument"
grep -Fq "ProgramArguments.1 -string access-check" "$CASE_DIR/state/plutil-transcript" || fail "access-check subcommand was not preserved"
grep -Fq "ProgramArguments.2 -string --config" "$CASE_DIR/state/plutil-transcript" || fail "config flag was not preserved"
grep -Fq "ProgramArguments.3 -string $CASE_DIR/config\\ file\\ \\&\\ test.yml" "$CASE_DIR/state/plutil-transcript" || fail "config argument was not preserved"
! grep -Eq 'ProgramArguments\.[0-9]+ -string (/(bin|usr/bin)/(ba)?sh|.*shell)' "$CASE_DIR/state/plutil-transcript" || fail "shell wrapper inserted"
[[ ${MUTATION_PROBE:-0} == 1 ]] && exit 0

for mode in invalid-signature second-run-signature-failure identity-change after-first-identity-change after-second-identity-change missing-marker timeout; do
  run_case "$mode" "$mode"
  [[ $CASE_STATUS -ne 0 ]] || fail "$mode unexpectedly passed"
done
for mode in second-run-signature-failure identity-change after-first-identity-change after-second-identity-change missing-marker timeout; do
  [[ -d "$TMP/$mode/state/private" ]] || fail "$mode lost retained artifacts"
  assert_job_absent "$TMP/$mode"
done
assert_retained "$TMP/second-run-signature-failure" 1
assert_retained "$TMP/identity-change" 0
assert_retained "$TMP/after-first-identity-change" 1
assert_retained "$TMP/after-second-identity-change" 2
assert_retained "$TMP/missing-marker" 0
assert_retained "$TMP/timeout" 0

run_case bootout bootout-failure
[[ $CASE_STATUS -ne 0 && -d "$CASE_DIR/state/private" ]] || fail "bootout failure did not retain artifacts"
assert_contains "$CASE_OUTPUT" "launchctl bootout"
assert_contains "$CASE_OUTPUT" "rm -rf"
assert_retained "$CASE_DIR" 2
assert_job_present "$CASE_DIR"

run_case preflight happy
rm -f "$CASE_DIR/config file & test.yml"
set +e; output=$(env PATH="$CASE_DIR/bin:/usr/bin:/bin" HOME="$CASE_DIR/home" USER="${USER:-tester}" FAKE_STATE="$CASE_DIR/state" COGVAULT_BIN="$CASE_DIR/cogvault bin" COGVAULT_CONFIG="$CASE_DIR/config file & test.yml" "$HARNESS" 2>&1); status=$?; set -e
[[ $status -ne 0 && ! -e "$CASE_DIR/state/bootstrapped" ]] || fail "preflight bootstrapped"

for mode in invalid-signature non-darwin uid-failure gui-unavailable; do
  run_case "preflight-$mode" "$mode"
  [[ $CASE_STATUS -ne 0 && ! -e "$CASE_DIR/state/bootstrapped" ]] || fail "$mode crossed bootstrap boundary"
done

dir=$(make_fixture non-executable happy); chmod -x "$dir/cogvault bin"
set +e; env PATH="$dir/bin:/usr/bin:/bin" HOME="$dir/home" USER="${USER:-tester}" FAKE_STATE="$dir/state" COGVAULT_BIN="$dir/cogvault bin" COGVAULT_CONFIG="$dir/config file & test.yml" "$HARNESS" >/dev/null 2>&1; status=$?; set -e
[[ $status -ne 0 && ! -e "$dir/state/bootstrapped" ]] || fail "non-executable binary crossed bootstrap"

dir=$(make_fixture unreadable-config happy); chmod 000 "$dir/config file & test.yml"
set +e; env PATH="$dir/bin:/usr/bin:/bin" HOME="$dir/home" USER="${USER:-tester}" FAKE_STATE="$dir/state" COGVAULT_BIN="$dir/cogvault bin" COGVAULT_CONFIG="$dir/config file & test.yml" "$HARNESS" >/dev/null 2>&1; status=$?; set -e
chmod 600 "$dir/config file & test.yml"
[[ $status -ne 0 && ! -e "$dir/state/bootstrapped" ]] || fail "unreadable config crossed bootstrap"

dir=$(make_fixture zero-timeout timeout)
set +e; output=$(env PATH="$dir/bin:/usr/bin:/bin" HOME="$dir/home" USER="${USER:-tester}" FAKE_STATE="$dir/state" FAKE_MODE=timeout ACCESS_CHECK_TIMEOUT=0 COGVAULT_BIN="$dir/cogvault bin" COGVAULT_CONFIG="$dir/config file & test.yml" "$HARNESS" 2>&1); status=$?; set -e
[[ $status -ne 0 ]] || fail "zero timeout passed"
assert_contains "$output" "timed out after 0s"
assert_retained "$dir" 0
assert_job_absent "$dir"

for sig in INT TERM; do
  dir=$(make_fixture "signal-$sig" timeout)
  env PATH="$dir/bin:/usr/bin:/bin" HOME="$dir/home" USER="${USER:-tester}" FAKE_STATE="$dir/state" FAKE_MODE=timeout ACCESS_CHECK_TIMEOUT=5 COGVAULT_BIN="$dir/cogvault bin" COGVAULT_CONFIG="$dir/config file & test.yml" HARNESS_PATH="$HARNESS" SIGNAL_NAME="$sig" /usr/bin/python3 - <<'PY'
import os, pathlib, signal, subprocess, time
p = subprocess.Popen([os.environ["HARNESS_PATH"]], env=os.environ.copy(), stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
marker = pathlib.Path(os.environ["FAKE_STATE"]) / "kicks"
for _ in range(200):
    if marker.exists(): break
    time.sleep(.01)
else:
    raise SystemExit("harness never bootstrapped")
p.send_signal(getattr(signal, "SIG" + os.environ["SIGNAL_NAME"]))
out, _ = p.communicate(timeout=5)
if p.returncode == 0:
    raise SystemExit("signal returned success: " + out)
PY
  [[ -d "$dir/state/private" ]] || fail "$sig lost retained artifacts"
  assert_retained "$dir" 0
  assert_job_absent "$dir"
done

if [[ ${SKIP_MUTATIONS:-0} == 0 ]]; then
  cp "$HARNESS" "$TMP/no-second-run.sh"
  sed -i '' '/^run_once 2$/d' "$TMP/no-second-run.sh"
  chmod +x "$TMP/no-second-run.sh"
  set +e; MUTATION_PROBE=1 SKIP_MUTATIONS=1 HARNESS="$TMP/no-second-run.sh" bash "$0" >/dev/null 2>&1; mutation_status=$?; set -e
  [[ $mutation_status -ne 0 ]] || fail "missing-second-run mutation survived"

  cp "$HARNESS" "$TMP/changed-label.sh"
  sed -i '' 's/^run_once 2$/LABEL="${LABEL}.changed"; run_once 2/' "$TMP/changed-label.sh"
  chmod +x "$TMP/changed-label.sh"
  set +e; MUTATION_PROBE=1 SKIP_MUTATIONS=1 HARNESS="$TMP/changed-label.sh" bash "$0" >/dev/null 2>&1; mutation_status=$?; set -e
  [[ $mutation_status -ne 0 ]] || fail "changed-label mutation survived"

  cp "$HARNESS" "$TMP/no-postrun-seal.sh"
  sed -i '' '/binary identity changed during verification/d' "$TMP/no-postrun-seal.sh"
  chmod +x "$TMP/no-postrun-seal.sh"
  set +e; MUTATION_PROBE=1 SKIP_MUTATIONS=1 HARNESS="$TMP/no-postrun-seal.sh" bash "$0" >/dev/null 2>&1; mutation_status=$?; set -e
  [[ $mutation_status -ne 0 ]] || fail "post-run seal removal mutation survived"

  set +e; INJECT_ARTIFACT_LOSS=1 ARTIFACT_LOSS_PROBE=1 SKIP_MUTATIONS=1 bash "$0" >/dev/null 2>&1; mutation_status=$?; set -e
  [[ $mutation_status -ne 0 ]] || fail "retained artifact loss injection survived"
fi

echo "PASS: scheduled access harness"
