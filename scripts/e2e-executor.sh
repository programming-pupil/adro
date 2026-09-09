#!/usr/bin/env sh
set -eu

# This executable is only a discovery shim for the browser control-plane job.
# It must never manufacture a successful model result. When an actual coding
# client is present, delegate to it; otherwise return a stable prerequisite
# failure that the provider records as blocked/failed evidence.
client="${ADRO_REAL_EXECUTOR:-}"
if [ -z "$client" ]; then
  if [ -x "/Applications/ChatGPT.app/Contents/Resources/codex" ]; then
    client="/Applications/ChatGPT.app/Contents/Resources/codex"
  else
    client="$(command -v codex 2>/dev/null || true)"
  fi
fi
if [ -z "$client" ]; then
  printf '%s\n' 'blocked_external_prerequisite: codex executable is not installed' >&2
  exit 127
fi
exec "$client" "$@"
