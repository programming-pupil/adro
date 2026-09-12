#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=scripts/lib/go-toolchain.sh
source "$ROOT_DIR/scripts/lib/go-toolchain.sh"

go_bin="$(command -v go 2>/dev/null || true)"
[ -n "$go_bin" ] && [ -x "$go_bin" ] || exit 0

expected_root="$(resolve_go_root "$go_bin")"
[ -f "$expected_root/VERSION" ] || {
  printf 'resolved Go root is invalid: %s\n' "$expected_root" >&2
  exit 1
}

actual_root="$(ADRO_GO_BIN="$go_bin" GOROOT="$ROOT_DIR/.missing-goroot" "$ROOT_DIR/scripts/e2e-go.sh" env GOROOT)"
[ "$actual_root" = "$expected_root" ] || {
  printf 'wrapper selected %s, expected %s\n' "$actual_root" "$expected_root" >&2
  exit 1
}

wrapper_root="$(ADRO_GO_BIN="$go_bin" resolve_go_root "$ROOT_DIR/scripts/e2e-go.sh")"
[ "$wrapper_root" = "$expected_root" ] || {
  printf 'wrapper resolution selected %s, expected %s\n' "$wrapper_root" "$expected_root" >&2
  exit 1
}

printf 'Go toolchain resolution passed (%s)\n' "$expected_root"
