#!/bin/sh
set -eu

repo_dir=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT HUP INT TERM

fake_bin="$tmp_dir/bin"
fake_home="$tmp_dir/home"
mkdir -p "$fake_bin" "$fake_home/bin"
printf 'previous-install\n' >"$fake_home/bin/cogvault"
cp "$repo_dir/Makefile" "$tmp_dir/Makefile"

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
case "$*" in
  *"$HOME/bin/"*) exit 1 ;;
  *) exit 0 ;;
esac
EOF

chmod +x "$fake_bin/go" "$fake_bin/codesign"

if PATH="$fake_bin:/usr/bin:/bin" HOME="$fake_home" make -C "$tmp_dir" install; then
  printf 'expected destination signing to fail\n' >&2
  exit 1
fi

actual=$(cat "$fake_home/bin/cogvault")
if [ "$actual" != 'previous-install' ]; then
  printf 'failed install replaced the previous binary: %s\n' "$actual" >&2
  exit 1
fi
