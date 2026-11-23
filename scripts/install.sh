#!/bin/sh
set -eu
umask 077

if [ "${1:-}" = '--help' ]; then
  echo 'Install or upgrade decide from GitHub Releases. Optional: DECIDE_RELEASE_REPO, DECIDE_VERSION, DECIDE_INSTALL_DIR.'
  exit 0
fi
fail() { echo "$1" >&2; exit 1; }
repo="${DECIDE_RELEASE_REPO:-koltyakov/decide}"
case "$repo" in *[!a-zA-Z0-9_./-]*|''|.*|*..*) fail 'Invalid release repository';; esac
if command -v curl >/dev/null 2>&1; then
  download() { curl -fsSL "$1" -o "$2"; }
elif command -v wget >/dev/null 2>&1; then
  download() { wget -qO "$2" "$1"; }
else
  fail 'curl or wget is required'
fi
if command -v sha256sum >/dev/null 2>&1; then
  sha256() { sha256sum "$1" | awk '{print $1}'; }
elif command -v shasum >/dev/null 2>&1; then
  sha256() { shasum -a 256 "$1" | awk '{print $1}'; }
else
  fail 'sha256sum or shasum is required to verify the download'
fi
valid_version() {
  case "$1" in ''|[!a-zA-Z0-9]*|*[!a-zA-Z0-9_.+-]*|*..*) return 1;; esac
}
case "$(uname -s)" in Linux) os=linux;; Darwin) os=darwin;; *) fail 'Unsupported OS';; esac
case "$(uname -m)" in x86_64|amd64) arch=amd64;; arm64|aarch64) arch=arm64;; *) fail 'Unsupported architecture';; esac
# A Rosetta-translated shell reports x86_64 on Apple silicon; install the native binary.
if [ "$os" = darwin ] && [ "$arch" = amd64 ] && [ "$(sysctl -n sysctl.proc_translated 2>/dev/null || true)" = 1 ]; then
  arch=arm64
fi
asset="decide_${os}_${arch}"
tmp="$(mktemp -d)"
staged=''
cleanup() { rm -rf "$tmp"; if [ -n "$staged" ]; then rm -f "$staged"; fi; }
trap cleanup EXIT HUP INT TERM

version="${DECIDE_VERSION:-}"
if [ -z "$version" ]; then
  # Resolve "latest" once, so the checksum list and binary come from one release.
  download "https://github.com/$repo/releases/latest/download/decide-manifest.json" "$tmp/manifest.json"
  version="$(sed -n 's/^ *"version": *"\([^"]*\)".*/\1/p' "$tmp/manifest.json" | head -n 1)"
fi
valid_version "$version" || fail 'Invalid version'
base="https://github.com/$repo/releases/download/$version"
download "$base/checksums.txt" "$tmp/checksums.txt"
matches="$(awk -v file="$asset" '$2 == file' "$tmp/checksums.txt" | wc -l | tr -d ' ')"
[ "$matches" = 1 ] || { [ "$matches" = 0 ] && fail "Binary checksum missing for $asset in $version"; fail "Duplicate binary checksum for $asset"; }
expected="$(awk -v file="$asset" '$2 == file {print $1}' "$tmp/checksums.txt")"
case "$expected" in *[!a-f0-9]*|'') fail 'Invalid binary checksum';; esac
[ ${#expected} -eq 64 ] || fail 'Invalid binary checksum'
download "$base/$asset" "$tmp/decide"
[ "$(sha256 "$tmp/decide")" = "$expected" ] || fail 'Binary checksum mismatch'

dest="${DECIDE_INSTALL_DIR:-$HOME/.local/bin}"
mkdir -p "$dest"
staged="$(mktemp "$dest/.decide-install-XXXXXX")"
cp "$tmp/decide" "$staged"
chmod 755 "$staged"
# Run the verified binary from its destination filesystem before replacing anything.
"$staged" --version >/dev/null 2>&1 || fail 'Downloaded binary does not run on this system'
mv -f "$staged" "$dest/decide"
staged=''
echo "Installed decide $version to $dest/decide"
case ":${PATH:-}:" in *":$dest:"*) ;; *) echo "Add $dest to PATH.";; esac
echo 'After an upgrade, run: decide install-skill --refresh'
echo 'New setup: decide providers connect official; decide install-mcp opencode; decide install-skill opencode'
