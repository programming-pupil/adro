#!/usr/bin/env bash

# Resolve the Go installation paired with an explicitly selected go binary.
# Some local Go distributions report a different compiled-in GOROOT unless
# the environment overrides it, so prefer the binary's validated install root.
resolve_go_root() {
  local go_bin="$1"
  local resolved_bin=""
  local candidate_root=""

  if [ -x "$go_bin" ]; then
    resolved_bin="$go_bin"
  else
    resolved_bin="$(command -v "$go_bin" 2>/dev/null || true)"
  fi
  if [ -n "$resolved_bin" ] && [ "$(basename "$resolved_bin")" = "go" ]; then
    candidate_root="$(CDPATH= cd -- "$(dirname "$resolved_bin")/.." 2>/dev/null && pwd || true)"
    if [ -f "$candidate_root/VERSION" ] && [ -d "$candidate_root/pkg/tool" ]; then
      printf '%s' "$candidate_root"
      return 0
    fi
  fi

  "$go_bin" env GOROOT 2>/dev/null
}
