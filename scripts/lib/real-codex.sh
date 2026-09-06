#!/usr/bin/env bash

# Shared setup for real Codex acceptance suites. The Multica runtime sets a
# private CODEX_HOME for the coding agent itself; operators can explicitly
# select their local Codex installation with ADRO_CODEX_HOME without copying
# credentials into the repository or mutating the user's config.

prepare_real_codex_home() {
  local run_home="$1"
  local source_home="${ADRO_CODEX_HOME:-${CODEX_HOME:-}}"
  local auth_file=""
  local config_file=""

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

  mkdir -p "$run_home"
  if [ -n "$auth_file" ]; then
    ln -s "$auth_file" "$run_home/auth.json"
  fi
  if [ -n "$config_file" ]; then
    install -m 600 "$config_file" "$run_home/config.toml"
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

real_codex_config_flags() {
  local flags=""
  local base_url="${ADRO_CODEX_BASE_URL:-}"

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
  printf '%s' "$flags"
}

configure_real_codex_command() {
  local executor="$1"
  local flags
  flags="$(real_codex_config_flags)"
  export ADRO_EXECUTOR_COMMAND="$executor exec$flags --json --skip-git-repo-check --dangerously-bypass-approvals-and-sandbox {input}"
}
