#!/usr/bin/env bash
set -eu

URL="${HERDR_PLUGIN_CLICKED_URL:-${1:-}}"

if [ -z "$URL" ]; then
    echo "No URL provided." >&2
    exit 1
fi

# Ensure tether binary is available
TETHER_BIN="${HOME}/.local/bin/tether"
if command -v tether >/dev/null 2>&1; then
    TETHER_BIN="tether"
fi

# Open the URL in the Tether tab group
"$TETHER_BIN" open "$URL"
