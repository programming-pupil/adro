#!/usr/bin/env sh
set -eu

# Playwright starts the API from a child process. Prefer the repository's
# pinned Go toolchain when it is installed, while retaining a portable fallback
# for CI images that expose only `go` on PATH.
script_path="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)/$(basename -- "$0")"
configured_path="${ADRO_GO_BIN:-}"
configured_real=""
if [ -n "$configured_path" ] && [ -e "$configured_path" ]; then
  if ! configured_real="$(realpath "$configured_path" 2>/dev/null)"; then
    configured_real="$(CDPATH= cd -- "$(dirname -- "$configured_path")" && pwd)/$(basename -- "$configured_path")"
  fi
fi
if [ -x "$configured_path" ] && [ "$configured_real" != "$script_path" ]; then
  candidate="$configured_real"
  root="$(CDPATH= cd -- "$(dirname -- "$candidate")/.." && pwd)"
  if [ -f "$root/VERSION" ] && [ -d "$root/pkg/tool" ]; then
    exec env GOROOT="$root" "$candidate" "$@"
  fi
  # A caller-selected binary must not inherit a GOROOT belonging to another
  # installation; that produces opaque compiler/tool version mismatches.
  exec env -u GOROOT "$candidate" "$@"
fi
fallback="$(command -v go 2>/dev/null || true)"
if [ -n "$fallback" ] && [ -x "$fallback" ]; then
  root="$(CDPATH= cd -- "$(dirname -- "$fallback")/.." && pwd)"
  if [ -f "$root/VERSION" ] && [ -d "$root/pkg/tool" ]; then
    exec env GOROOT="$root" "$fallback" "$@"
  fi
  configured_root="$($fallback env GOROOT 2>/dev/null || true)"
  if [ -n "$configured_root" ] && [ -x "$configured_root/bin/go" ] && [ -f "$configured_root/VERSION" ] && [ -d "$configured_root/pkg/tool" ]; then
    exec env GOROOT="$configured_root" "$configured_root/bin/go" "$@"
  fi
  exec env -u GOROOT "$fallback" "$@"
fi
for candidate in \
  /Users/shareit/.gvm/gos/go1.25.0/bin/go \
  "${GOROOT:-}/bin/go"; do
  if [ -n "$candidate" ] && [ -x "$candidate" ]; then
    root="$(CDPATH= cd -- "$(dirname -- "$candidate")/.." && pwd)"
    if [ -f "$root/VERSION" ] && [ -d "$root/pkg/tool" ]; then
      exec env GOROOT="$root" "$candidate" "$@"
    fi
    exec "$candidate" "$@"
  fi
done
exec env -u GOROOT go "$@"
