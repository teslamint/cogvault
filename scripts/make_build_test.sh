#!/bin/sh
set -eu

repo_dir=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT HUP INT TERM

fake_bin="$tmp_dir/bin"
mkdir -p "$fake_bin"
cp "$repo_dir/Makefile" "$tmp_dir/Makefile"
printf 'stale-output\n' >"$tmp_dir/cogvault"

cat >"$fake_bin/go" <<'EOF'
#!/bin/sh
output=
while [ "$#" -gt 0 ]; do
  if [ "$1" = '-o' ]; then
    output=$2
    break
  fi
  shift
done
if [ -e "$output" ]; then
  printf 'build output "%s" already exists and is not an object file\n' "$output" >&2
  exit 1
fi
printf 'new-build\n' >"$output"
EOF

cat >"$fake_bin/codesign" <<'EOF'
#!/bin/sh
exit 0
EOF

chmod +x "$fake_bin/go" "$fake_bin/codesign"
PATH="$fake_bin:/usr/bin:/bin" make -C "$tmp_dir" build

actual=$(cat "$tmp_dir/cogvault")
if [ "$actual" != 'new-build' ]; then
  printf 'build did not replace the stale output: %s\n' "$actual" >&2
  exit 1
fi

if [ -e "$tmp_dir/.cogvault.build" ]; then
  printf 'temporary build output remains\n' >&2
  exit 1
fi
