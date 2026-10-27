#!/bin/sh
set -eu
umask 077

if [ "${1:-}" = '--help' ]; then
  echo 'Install decide from GitHub Releases. Optional: DECIDE_RELEASE_REPO, DECIDE_VERSION, DECIDE_INSTALL_DIR.'
  exit 0
fi
repo="${DECIDE_RELEASE_REPO:-koltyakov/decide}"
case "$repo" in *[!a-zA-Z0-9_./-]*|'') echo 'Invalid release repository' >&2; exit 1;; esac
case "$(uname -s)" in Linux) os=linux;; Darwin) os=darwin;; *) echo 'Unsupported OS' >&2; exit 1;; esac
case "$(uname -m)" in x86_64|amd64) arch=amd64;; arm64|aarch64) arch=arm64;; *) echo 'Unsupported architecture' >&2; exit 1;; esac
base="https://github.com/$repo/releases/latest/download"
if [ -n "${DECIDE_VERSION:-}" ]; then
  case "$DECIDE_VERSION" in *[!a-zA-Z0-9_.+-]*) echo 'Invalid version' >&2; exit 1;; esac
  base="https://github.com/$repo/releases/download/$DECIDE_VERSION"
fi
asset="decide_${os}_${arch}"
tmp="$(mktemp -d)"
staged=''
cleanup() { rm -rf "$tmp"; if [ -n "$staged" ]; then rm -f "$staged"; fi; }
trap cleanup EXIT HUP INT TERM
download() {
  if command -v curl >/dev/null 2>&1; then curl -fsSL "$1" -o "$2"; else wget -qO "$2" "$1"; fi
}
download "$base/checksums.txt" "$tmp/checksums.txt"
download "$base/$asset" "$tmp/decide"
expected="$(awk -v file="$asset" '$2 == file {print $1}' "$tmp/checksums.txt")"
if command -v sha256sum >/dev/null 2>&1; then actual="$(sha256sum "$tmp/decide" | awk '{print $1}')"; else actual="$(shasum -a 256 "$tmp/decide" | awk '{print $1}')"; fi
if [ -z "$expected" ] || [ "$expected" != "$actual" ]; then echo 'Binary checksum mismatch' >&2; exit 1; fi
dest="${DECIDE_INSTALL_DIR:-$HOME/.local/bin}"
mkdir -p "$dest"
staged="$(mktemp "$dest/.decide-install-XXXXXX")"
cp "$tmp/decide" "$staged"
chmod 755 "$staged"
mv -f "$staged" "$dest/decide"
staged=''
echo "Installed $dest/decide"
echo 'Add the installation directory to PATH if needed.'
echo 'Next: decide providers connect official; decide install-mcp opencode; decide install-skill opencode'
