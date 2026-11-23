#!/bin/sh
set -eu

if [ "${1:-}" = '--help' ]; then
  echo 'Remove the decide binary. Optional: DECIDE_INSTALL_DIR. Credentials, MCP entries, and skills are kept.'
  exit 0
fi
dest="${DECIDE_INSTALL_DIR:-$HOME/.local/bin}"
if [ -e "$dest/decide" ]; then
  rm -f "$dest/decide"
  echo "Removed $dest/decide"
else
  echo "decide is not installed in $dest"
fi
echo 'Kept: saved credentials (~/.config/decide, XDG_CONFIG_HOME, or DECIDE_CREDENTIALS_FILE), client MCP entries, and installed skills.'
echo 'Remove those by hand, and revoke provider keys in each provider console if needed.'
