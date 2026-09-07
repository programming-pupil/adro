#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=scripts/lib/real-codex.sh
source "$ROOT_DIR/scripts/lib/real-codex.sh"

run_root="$(mktemp -d "${TMPDIR:-/tmp}/adro-real-codex-config.XXXXXX")"
trap 'rm -rf "$run_root"' EXIT
mkdir -p "$run_root/source"
printf '%s\n' '{"auth":"redacted-test-fixture"}' >"$run_root/source/auth.json"
printf '%s\n' 'model = "gpt-test"' >"$run_root/source/config.toml"

ADRO_CODEX_HOME="$run_root/source" CODEX_HOME="$run_root/parent" prepare_real_codex_home "$run_root/run"
[ "$CODEX_HOME" = "$run_root/run" ]
[ -L "$run_root/run/auth.json" ]
[ -f "$run_root/run/config.toml" ]
trust_real_codex_project "$run_root/run" "$run_root/project"
grep -Fq "[projects.\"$run_root/project\"]" "$run_root/run/config.toml"

ADRO_CODEX_IGNORE_USER_CONFIG=0 ADRO_CODEX_BASE_URL=https://code.apipod.ai/v1 \
  configure_real_codex_command /usr/local/bin/codex
case "$ADRO_EXECUTOR_COMMAND" in
  '/usr/local/bin/codex exec -c model_providers.custom.base_url="https://code.apipod.ai/v1" --json'*) ;;
  *) printf '%s\n' "unexpected command: $ADRO_EXECUTOR_COMMAND" >&2; exit 1 ;;
esac

if ADRO_CODEX_BASE_URL='not-a-url' real_codex_config_flags >/dev/null; then
  printf '%s\n' 'invalid base URL was accepted' >&2
  exit 1
fi

mkdir -p "$run_root/bearer-source"
printf '%s\n' '{"auth":"redacted-test-fixture"}' >"$run_root/bearer-source/auth.json"
printf '%s\n' '[model_providers.custom]' 'experimental_bearer_token = "redacted-test-fixture"' >"$run_root/bearer-source/config.toml"
ADRO_CODEX_HOME="$run_root/bearer-source" prepare_real_codex_home "$run_root/bearer-run"
[ ! -e "$run_root/bearer-run/auth.json" ]
ADRO_CODEX_IGNORE_USER_CONFIG=0 ADRO_CODEX_BASE_URL=https://code.apipod.ai \
  configure_real_codex_command /usr/local/bin/codex
case "$ADRO_EXECUTOR_COMMAND" in
  *'-c model_providers.custom.requires_openai_auth=false --json'*) ;;
  *) printf '%s\n' 'relay bearer profile did not disable ChatGPT auth' >&2; exit 1 ;;
esac

printf '%s\n' 'real Codex configuration helper passed'
