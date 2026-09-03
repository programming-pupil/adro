#!/usr/bin/env sh
set -eu

# Playwright starts the API from a child process. Prefer the repository's
# pinned Go toolchain when it is installed, while retaining a portable fallback
# for CI images that expose only `go` on PATH.
script_path="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)/$(basename -- "$0")"
configured_path="${ADRO_GO_BIN:-}"
configured_real=""
if [ -n "$configured_path" ] && [ -e "$configured_path" ]; then
  configured_real="$(CDPATH= cd -- "$(dirname -- "$configured_path")" && pwd)/$(basename -- "$configured_path")"
fi
if [ -x "$configured_path" ] && [ "$configured_real" != "$script_path" ]; then
  candidate="${ADRO_GO_BIN}"
  root="$(CDPATH= cd -- "$(dirname -- "$candidate")/.." && pwd)"
  if [ -f "$root/VERSION" ] && [ -d "$root/pkg/tool" ]; then
    exec env GOROOT="$root" "$candidate" "$@"
  fi
  exec "$candidate" "$@"
fi
for candidate in \
  /Users/shareit/.gvm/gos/go1.24.1/bin/go \
  "${GOROOT:-}/bin/go"; do
  if [ -n "$candidate" ] && [ -x "$candidate" ]; then
    root="$(CDPATH= cd -- "$(dirname -- "$candidate")/.." && pwd)"
    if [ -f "$root/VERSION" ] && [ -d "$root/pkg/tool" ]; then
      exec env GOROOT="$root" "$candidate" "$@"
    fi
    exec "$candidate" "$@"
  fi
done
exec go "$@"
