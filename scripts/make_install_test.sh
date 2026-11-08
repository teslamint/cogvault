#!/bin/sh
set -eu

repo_dir=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT HUP INT TERM

fake_bin="$tmp_dir/bin"
fake_home="$tmp_dir/home"
mkdir -p "$fake_bin" "$fake_home/bin"
printf 'previous-install\n' >"$fake_home/bin/cogvault"
mkdir -p "$tmp_dir/scripts"
cp "$repo_dir/Makefile" "$tmp_dir/Makefile"
cp "$repo_dir/scripts/install-lock.sh" "$tmp_dir/scripts/install-lock.sh"
chmod +x "$tmp_dir/scripts/install-lock.sh"
cat >"$fake_bin/go" <<'EOF'
#!/bin/sh
while [ "$#" -gt 0 ]; do
  if [ "$1" = '-o' ]; then
    printf 'new-build\n' >"$2"
    exit 0
  fi
  shift
done
printf 'missing -o output\n' >&2
exit 1
EOF

cat >"$fake_bin/codesign" <<'EOF'
#!/bin/sh
case " $* " in
  *" -R fail-req "*) exit 1 ;;
  *"$HOME/bin/fail-sign"*) exit 1 ;;
  *) exit 0 ;;
esac
EOF

chmod +x "$fake_bin/go" "$fake_bin/codesign"
mkdir -p "$fake_home/bin/fail-sign"
printf 'previous-install\n' >"$fake_home/bin/fail-sign/cogvault"

if PATH="$fake_bin:/usr/bin:/bin" HOME="$fake_home" make -C "$tmp_dir" install INSTALL_DIR="$fake_home/bin/fail-sign"; then
  printf 'expected destination signing to fail\n' >&2
  exit 1
fi

actual=$(cat "$fake_home/bin/fail-sign/cogvault")
if [ "$actual" != 'previous-install' ]; then
  printf 'failed install replaced the previous binary: %s\n' "$actual" >&2
  exit 1
fi

# 2. Staged requirement failure preserves previous-install
if PATH="$fake_bin:/usr/bin:/bin" HOME="$fake_home" make -C "$tmp_dir" install CODESIGN_REQUIREMENT="fail-req"; then
  printf 'expected requirement verification on staged binary to fail\n' >&2
  exit 1
fi

actual=$(cat "$fake_home/bin/cogvault")
if [ "$actual" != 'previous-install' ]; then
  printf 'failed requirement check replaced the previous binary: %s\n' "$actual" >&2
  exit 1
fi

# 3. Concurrent make install invocations stage separately and succeed
pids=""
for i in 1 2; do
  PATH="$fake_bin:/usr/bin:/bin" HOME="$fake_home" make -C "$tmp_dir" install >/dev/null &
  pids="$pids $!"
done

for pid in $pids; do
  wait "$pid"
done

actual=$(cat "$fake_home/bin/cogvault")
if [ "$actual" != 'new-build' ]; then
  printf 'concurrent installs failed to leave valid installed binary: %s\n' "$actual" >&2
  exit 1
fi

# 4. Empty or stale lock file left behind from crashed process is immediately acquired
touch "$fake_home/bin/.cogvault.install.lock"
PATH="$fake_bin:/usr/bin:/bin" HOME="$fake_home" make -C "$tmp_dir" install >/dev/null
actual=$(cat "$fake_home/bin/cogvault")
if [ "$actual" != 'new-build' ]; then
  printf 'install failed with pre-existing lock file: %s\n' "$actual" >&2
  exit 1
fi

# 5. Custom INSTALL_DIR installs to specified directory under lock
mkdir -p "$fake_home/custom"
PATH="$fake_bin:/usr/bin:/bin" HOME="$fake_home" make -C "$tmp_dir" install INSTALL_DIR="$fake_home/custom" >/dev/null
if [ ! -f "$fake_home/custom/cogvault" ]; then
  printf 'install failed to install into custom INSTALL_DIR\n' >&2
  exit 1
fi
