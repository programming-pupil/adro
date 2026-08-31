#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=scripts/lib/env-file.sh
source "$ROOT_DIR/scripts/lib/env-file.sh"

mode_of() {
  if stat -f '%Lp' "$1" >/dev/null 2>&1; then
    stat -f '%Lp' "$1"
  else
    stat -c '%a' "$1"
  fi
}

TEST_DIR="$(mktemp -d "${TMPDIR:-/tmp}/adro-env-mode.XXXXXX")"
trap 'rm -rf "$TEST_DIR"' EXIT
ENV_FILE="$TEST_DIR/adro.env"

printf 'ADRO_ADMIN_PASSWORD=first\n' > "$ENV_FILE"
chmod 0644 "$ENV_FILE"
set_env_key "$ENV_FILE" ADRO_EXECUTOR_COMMAND 'claude -p {input}'
set_env_key "$ENV_FILE" ADRO_ADMIN_PASSWORD 'second'

[ "$(mode_of "$ENV_FILE")" = "600" ] || {
  printf 'expected %s to be mode 600, got %s\n' "$ENV_FILE" "$(mode_of "$ENV_FILE")" >&2
  exit 1
}
[ "$(awk -F= '$1 == "ADRO_EXECUTOR_COMMAND" {print $2}' "$ENV_FILE")" = 'claude -p {input}' ]
[ "$(awk -F= '$1 == "ADRO_ADMIN_PASSWORD" {print $2}' "$ENV_FILE")" = 'second' ]

printf 'credential env rewrite preserves mode 0600\n'
