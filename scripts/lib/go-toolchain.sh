#!/usr/bin/env bash

# Resolve the Go installation paired with an explicitly selected go binary.
# Some local Go distributions report a different compiled-in GOROOT unless
# the environment overrides it, so prefer the binary's validated install root.
select_go_bin() {
  local configured_path="${ADRO_GO_BIN:-}"
  if [ -n "$configured_path" ] && [ -x "$configured_path" ]; then
    printf '%s' "$configured_path"
    return 0
  fi
  if [ -n "${ROOT_DIR:-}" ] && [ -x "$ROOT_DIR/scripts/e2e-go.sh" ]; then
    printf '%s' "$ROOT_DIR/scripts/e2e-go.sh"
    return 0
  fi
  if [ -x "/Users/shareit/.gvm/gos/go1.25.0/bin/go" ]; then
    printf '%s' "/Users/shareit/.gvm/gos/go1.25.0/bin/go"
    return 0
  fi
  command -v go 2>/dev/null
}

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
