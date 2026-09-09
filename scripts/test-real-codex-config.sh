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

mkdir -p "$run_root/bin"
printf '%s\n' '#!/bin/sh' 'exit 0' >"$run_root/bin/codex"
chmod 700 "$run_root/bin/codex"
selected_codex="$(ADRO_EXECUTOR=codex PATH="$run_root/bin:$PATH" select_real_codex)"
[ "$selected_codex" = "$run_root/bin/codex" ]
selected_codex="$(ADRO_EXECUTOR= ADRO_CODEX_BIN= PATH="$run_root/bin:$PATH" select_real_codex)"
[ "$selected_codex" = "$run_root/bin/codex" ]
selected_codex="$(ADRO_CODEX_BIN="$run_root/bin/codex" ADRO_EXECUTOR= select_real_codex)"
[ "$selected_codex" = "$run_root/bin/codex" ]

ADRO_CODEX_HOME="$run_root/source" CODEX_HOME="$run_root/parent" prepare_real_codex_home "$run_root/run"
[ "$CODEX_HOME" = "$run_root/run" ]
[ -L "$run_root/run/auth.json" ]
[ -f "$run_root/run/config.toml" ]
trust_real_codex_project "$run_root/run" "$run_root/project"
grep -Fq "[projects.\"$run_root/project\"]" "$run_root/run/config.toml"
grep -Fq 'model = "gpt-test"' "$run_root/run/config.toml"
grep -Fq '[shell_environment_policy]' "$run_root/run/config.toml"
! grep -Fq '[plugins]' "$run_root/run/config.toml"
! grep -Fq '[mcp_servers]' "$run_root/run/config.toml"

ADRO_CODEX_IGNORE_USER_CONFIG=0 ADRO_CODEX_BASE_URL=https://code.apipod.ai/v1 \
  configure_real_codex_command /usr/local/bin/codex
case "$ADRO_EXECUTOR_COMMAND" in
  '/usr/local/bin/codex exec -c model_providers.custom.base_url="https://code.apipod.ai/v1" -c model_reasoning_effort=low --enable unified_exec --disable apps --disable plugins --disable multi_agent --disable multi_agent_v2'*) ;;
  *) printf '%s\n' "unexpected command: $ADRO_EXECUTOR_COMMAND" >&2; exit 1 ;;
esac
for feature in apps plugins multi_agent multi_agent_v2 collaboration_modes browser_use browser_use_external browser_use_full_cdp_access computer_use image_generation in_app_browser in_app_chat in_app_local_automation; do
  case " $ADRO_EXECUTOR_COMMAND " in
    *" --disable $feature "*) ;;
    *) printf '%s\n' "real Codex command did not disable $feature" >&2; exit 1 ;;
  esac
done
case " $ADRO_EXECUTOR_COMMAND " in
  *' --enable code_mode_host '*) printf '%s\n' 'real Codex command unexpectedly forced code_mode_host' >&2; exit 1 ;;
  *) ;;
esac
ADRO_CODEX_ENABLE_CODE_MODE=1 ADRO_CODEX_BASE_URL=https://code.apipod.ai/v1 \
  configure_real_codex_command /usr/local/bin/codex
case " $ADRO_EXECUTOR_COMMAND " in
  *' --enable code_mode_host --enable code_mode_only --enable unified_exec '*) ;;
  *) printf '%s\n' 'real Codex command did not honor explicit Code Mode opt-in' >&2; exit 1 ;;
esac
if ADRO_CODEX_REASONING_EFFORT=invalid real_codex_config_flags >/dev/null; then
  printf '%s\n' 'invalid reasoning effort was accepted' >&2
  exit 1
fi

if ADRO_CODEX_BASE_URL='not-a-url' real_codex_config_flags >/dev/null; then
  printf '%s\n' 'invalid base URL was accepted' >&2
  exit 1
fi

mkdir -p "$run_root/bearer-source"
printf '%s\n' '{"auth":"redacted-test-fixture"}' >"$run_root/bearer-source/auth.json"
printf '%s\n' \
  'model_provider = "custom"' \
  'model = "gpt-test"' \
  '[model_providers.custom]' \
  'name = "custom"' \
  'wire_api = "responses"' \
  'requires_openai_auth = true' \
  'base_url = "https://code.apipod.ai"' \
  'experimental_bearer_token = "redacted-test-fixture"' \
  '[plugins]' \
  '[plugins."example"]' \
  'enabled = true' \
  '[mcp_servers]' \
  '[mcp_servers.example]' \
  'command = "example"' >"$run_root/bearer-source/config.toml"
ADRO_CODEX_HOME="$run_root/bearer-source" prepare_real_codex_home "$run_root/bearer-run"
[ ! -e "$run_root/bearer-run/auth.json" ]
grep -Fq 'model_provider = "custom"' "$run_root/bearer-run/config.toml"
grep -Fq '[model_providers.custom]' "$run_root/bearer-run/config.toml"
grep -Fq 'experimental_bearer_token = "redacted-test-fixture"' "$run_root/bearer-run/config.toml"
! grep -Fq '[plugins]' "$run_root/bearer-run/config.toml"
! grep -Fq '[mcp_servers]' "$run_root/bearer-run/config.toml"
ADRO_CODEX_IGNORE_USER_CONFIG=0 ADRO_CODEX_BASE_URL=https://code.apipod.ai \
  configure_real_codex_command /usr/local/bin/codex
case "$ADRO_EXECUTOR_COMMAND" in
  *'-c model_providers.custom.requires_openai_auth=false -c model_reasoning_effort=low --enable unified_exec --disable apps --disable plugins --disable multi_agent --disable multi_agent_v2'*) ;;
  *) printf '%s\n' 'relay bearer profile did not disable ChatGPT auth' >&2; exit 1 ;;
esac

printf '%s\n' 'real Codex configuration helper passed'
