#!/usr/bin/env bash

# Shared setup for real Codex acceptance suites. The Multica runtime sets a
# private CODEX_HOME for the coding agent itself; operators can explicitly
# select their local Codex installation with ADRO_CODEX_HOME without copying
# credentials into the repository or mutating the user's config. The isolated
# home deliberately contains only the model/provider settings needed by the
# real executor; workspace plugins and MCP servers are not part of these
# black-box acceptance contracts.

select_real_codex() {
  local configured="${ADRO_EXECUTOR:-}"
  local candidate=""

  # An explicit executor is an operator decision and must win over discovery.
  # This also lets CI keep using a PATH-provided Codex on self-hosted runners.
  if [ -n "$configured" ]; then
    if [ -x "$configured" ]; then
      printf '%s' "$configured"
      return 0
    fi
    command -v "$configured" 2>/dev/null
    return $?
  fi

  # On macOS the application-bundled binary is the native Codex runtime. A
  # separately installed npm CLI can share the same name but may not expose
  # the terminal tool surface required by these real acceptance suites.
  for candidate in "${ADRO_CODEX_BIN:-}" \
    "/Applications/ChatGPT.app/Contents/Resources/codex" \
    "${HOME:-}/.local/bin/codex"; do
    if [ -n "$candidate" ] && [ -x "$candidate" ]; then
      printf '%s' "$candidate"
      return 0
    fi
  done
  command -v codex 2>/dev/null
}

write_minimal_codex_config() {
  local source_file="$1"
  local destination="$2"

  # Keep scalar execution settings and the selected custom provider, while
  # excluding plugin, MCP, marketplace, shell-secret, and unrelated project
  # sections from the operator's interactive Codex profile. This is a small
  # line-oriented projection of the stable TOML sections used by the local
  # Codex CLI; the original file remains untouched.
  awk '
    BEGIN { section = "" }
    /^[[:space:]]*\[/ {
      if ($0 == "[model_providers.custom]") {
        section = "custom"
        print
      } else {
        section = "other"
      }
      next
    }
    section == "" && $0 ~ /^[[:space:]]*(model_provider|model|review_model|model_reasoning_effort|disable_response_storage|service_tier)[[:space:]]*=/ {
      print
      next
    }
    section == "custom" { print }
  ' "$source_file" >"$destination"

  # Keep shell commands deterministic and prevent the model from inheriting
  # unrelated environment values configured for the interactive client.
  printf '\n[shell_environment_policy]\ninherit = "core"\n' >>"$destination"
}

prepare_real_codex_home() {
  local run_home="$1"
  local source_home="${ADRO_CODEX_HOME:-${CODEX_HOME:-}}"
  local auth_file=""
  local config_file=""
  local uses_experimental_bearer="false"

  if [ -n "${ADRO_CODEX_HOME:-}" ] && [ ! -d "$source_home" ]; then
    printf '%s\n' "ADRO_CODEX_HOME does not exist: $source_home" >&2
    return 1
  fi
  if [ -f "$source_home/auth.json" ]; then
    auth_file="$source_home/auth.json"
  elif [ -f "${HOME:-}/.codex/auth.json" ]; then
    auth_file="${HOME}/.codex/auth.json"
  fi
  if [ -f "$source_home/config.toml" ]; then
    config_file="$source_home/config.toml"
  elif [ -f "${HOME:-}/.codex/config.toml" ]; then
    config_file="${HOME}/.codex/config.toml"
  fi
  if [ -n "$config_file" ] && grep -Eq '^[[:space:]]*experimental_bearer_token[[:space:]]*=' "$config_file"; then
    uses_experimental_bearer="true"
  fi

  mkdir -p "$run_home"
  # A CC Switch/OpenAI-compatible profile with an explicit experimental bearer
  # is self-authenticating. Do not symlink the user's ChatGPT auth database into
  # the run: an expired refresh token would trigger refresh attempts and a
  # concurrent real suite could invalidate the shared token for every runner.
  if [ -n "$auth_file" ] && [ "$uses_experimental_bearer" != "true" ]; then
    ln -s "$auth_file" "$run_home/auth.json"
  fi
  if [ -n "$config_file" ]; then
    write_minimal_codex_config "$config_file" "$run_home/config.toml"
    chmod 600 "$run_home/config.toml"
  fi
  export CODEX_HOME="$run_home"
  unset CODEX_SESSION_ID CODEX_THREAD_ID CODEX_CI
}

trust_real_codex_project() {
  local run_home="$1"
  local project_path="$2"
  local config_file="$run_home/config.toml"

  # Codex only permits model-backed execution from trusted project roots.
  # Real E2E suites clone into a private temporary tree, so add that exact
  # runtime root to the isolated copy without changing the operator config.
  [ -f "$config_file" ] || return 0
  case "$project_path" in
    ""|*'"'*|*$'\n'*)
      printf '%s\n' 'Codex trusted project path is empty or contains unsupported quoting' >&2
      return 1
      ;;
  esac
  if grep -Fq "[projects.\"$project_path\"]" "$config_file"; then
    return 0
  fi
  printf '\n[projects."%s"]\ntrust_level = "trusted"\n' "$project_path" >>"$config_file"
}

write_real_codex_wrapper() {
  local wrapper="$1"
  local executor="$2"
  local go_root="${3:-}"
  local executor_quoted
  local go_root_quoted

  executor_quoted="$(printf '%q' "$executor")"
  go_root_quoted="$(printf '%q' "$go_root")"
  {
    printf '%s\n' '#!/bin/sh'
    printf '%s\n' 'set -u'
    # The parent process is a Multica task runner. Those variables describe
    # the orchestration host and can make a standalone real Codex invocation
    # select internal collaboration tools instead of the requested shell.
    printf '%s\n' 'for variable in $(env | sed -n '\''s/^\(MULTICA_[A-Za-z0-9_]*\)=.*/\1/p'\''); do unset "$variable"; done'
    printf '%s\n' 'unset CODEX_CI CODEX_SESSION_ID CODEX_THREAD_ID'
    if [ -n "$go_root" ]; then
      printf 'export GOROOT=%s\n' "$go_root_quoted"
    fi
    # Some real relay turns occasionally emit an invalid optional-tool call
    # instead of opening the shell tool. Buffer those attempts and retry the
    # same prompt through the same local Codex binary. The provider sees only
    # the terminal, real Codex attempt; no result is synthesized here.
    printf '%s\n' 'attempt_root="$(mktemp -d "${TMPDIR:-/tmp}/adro-codex-attempt.XXXXXX")"'
    printf '%s\n' 'trap '\''rm -rf "$attempt_root"'\'' EXIT HUP INT TERM'
    printf '%s\n' 'prompt_file="$attempt_root/prompt"'
    printf '%s\n' 'cat >"$prompt_file"'
    printf '%s\n' 'max_retries="${ADRO_CODEX_MAX_RETRIES:-2}"'
    printf '%s\n' 'case "$max_retries" in ""|*[!0-9]*) max_retries=2 ;; esac'
    printf '%s\n' 'attempt_timeout="${ADRO_CODEX_ATTEMPT_TIMEOUT:-35}"'
    printf '%s\n' 'case "$attempt_timeout" in ""|*[!0-9]*) attempt_timeout=35 ;; esac'
    printf '%s\n' 'require_terminal="${ADRO_CODEX_REQUIRE_TERMINAL:-0}"'
    printf '%s\n' 'attempt=0'
    printf '%s\n' 'while :; do'
    printf '%s\n' '  attempt=$((attempt + 1))'
    printf '%s\n' '  output_file="$attempt_root/output-$attempt"'
    printf '%s\n' '  status=0'
    printf '  perl -e '\''alarm shift; exec @ARGV'\'' "$attempt_timeout" %s "$@" <"$prompt_file" >"$output_file" 2>&1 || status=$?\n' "$executor_quoted"
    printf '%s\n' '  has_completion=0'
    printf '%s\n' '  grep -Fq '"'"'"type":"turn.completed"'"'"' "$output_file" && grep -Fq ADRO_RESULT_JSON= "$output_file" && has_completion=1'
    printf '%s\n' '  if [ "$require_terminal" = 1 ] && ! grep -Fq '"'"'"type":"command_execution"'"'"' "$output_file"; then has_completion=0; fi'
    printf '%s\n' '  if [ "$has_completion" = 1 ]; then'
    printf '%s\n' '    cat "$output_file"'
    printf '%s\n' '    exit 0'
    printf '%s\n' '  fi'
    printf '%s\n' '  if [ "$attempt" -gt "$max_retries" ]; then'
    printf '%s\n' '    cat "$output_file"'
    printf '%s\n' '    exit "$status"'
    printf '%s\n' '  fi'
    printf '%s\n' 'done'
  } >"$wrapper"
  chmod 700 "$wrapper"
}

real_codex_config_flags() {
  local flags=""
  local base_url="${ADRO_CODEX_BASE_URL:-}"
  local use_relay_bearer="${ADRO_CODEX_USE_RELAY_BEARER:-auto}"
  local reasoning_effort="${ADRO_CODEX_REASONING_EFFORT:-low}"

  if [ "${ADRO_CODEX_IGNORE_USER_CONFIG:-0}" = "1" ]; then
    flags=" --ignore-user-config"
  fi
  if [ -n "$base_url" ]; then
    case "$base_url" in
      http://*|https://*) ;;
      *) printf '%s\n' 'ADRO_CODEX_BASE_URL must use http:// or https://' >&2; return 1 ;;
    esac
    case "$base_url" in
      *[[:space:]]*|*\"*) printf '%s\n' 'ADRO_CODEX_BASE_URL contains unsupported whitespace or quotes' >&2; return 1 ;;
    esac
    # ADRO_EXECUTOR_COMMAND is parsed with strings.Fields by the Go provider,
    # so keep this override as two whitespace-delimited arguments. The quotes
    # around the TOML string are intentional and are passed to Codex.
    flags="$flags -c model_providers.custom.base_url=\"$base_url\""
  fi
  if [ "$use_relay_bearer" = "1" ] || {
    [ "$use_relay_bearer" = "auto" ] &&
      [ -f "${CODEX_HOME:-}/config.toml" ] &&
      grep -Eq '^[[:space:]]*experimental_bearer_token[[:space:]]*=' "${CODEX_HOME}/config.toml";
  }; then
    flags="$flags -c model_providers.custom.requires_openai_auth=false"
  fi
  case "$reasoning_effort" in
    "") ;;
    minimal|low|medium|high|xhigh)
      flags="$flags -c model_reasoning_effort=$reasoning_effort"
      ;;
    *)
      printf '%s\n' 'ADRO_CODEX_REASONING_EFFORT must be minimal, low, medium, high, or xhigh' >&2
      return 1
      ;;
  esac
  printf '%s' "$flags"
}

configure_real_codex_command() {
  local executor="$1"
  local flags
  flags="$(real_codex_config_flags)"
  # Real acceptance runs exercise the local Codex executor and shell boundary,
  # not the interactive workspace's app/plugin surface. Disable those optional
  # tools at the CLI boundary so a model cannot drift into collaboration or an
  # unrelated app while trying to satisfy a shell-based test contract.
  local shell_only_flags=' --enable code_mode_host --enable code_mode_only --enable unified_exec --disable apps --disable plugins --disable multi_agent --disable multi_agent_v2 --disable collaboration_modes --disable browser_use --disable browser_use_external --disable browser_use_full_cdp_access --disable computer_use --disable image_generation --disable in_app_browser --disable in_app_chat --disable in_app_local_automation'
  export ADRO_EXECUTOR_COMMAND="$executor exec$flags$shell_only_flags --json --skip-git-repo-check --dangerously-bypass-approvals-and-sandbox {input}"
}
