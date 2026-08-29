#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/lib/env-file.sh
source "$ROOT_DIR/scripts/lib/env-file.sh"
STATE_DIR="${ADRO_HOME:-$ROOT_DIR/.adro}"
BIN_DIR="$STATE_DIR/bin"
ADRO_ENV_FILE="$STATE_DIR/adro.env"
MULTICA_DIR="$STATE_DIR/multica-server"
MULTICA_VERSION="${MULTICA_VERSION:-0.4.35}"
MULTICA_PROFILE="${MULTICA_PROFILE:-adro-local}"
MULTICA_BACKEND_PORT="${MULTICA_BACKEND_PORT:-19080}"
MULTICA_FRONTEND_PORT="${MULTICA_FRONTEND_PORT:-19081}"
ADRO_API_PORT="${ADRO_API_PORT:-8080}"
ADRO_WEB_PORT="${ADRO_WEB_PORT:-8081}"
COMPOSE_FILE="$ROOT_DIR/deploy/compose/docker-compose.yml"
LOCAL_API_PID_FILE="$STATE_DIR/adro-api.pid"
LOCAL_WEB_PID_FILE="$STATE_DIR/adro-web.pid"
LOCAL_API_LOG="$STATE_DIR/adro-api.log"
LOCAL_WEB_LOG="$STATE_DIR/adro-web.log"
DIRECT_MODE_FILE="$STATE_DIR/direct-mode"
MODE="start"
WITH_MULTICA=true
NO_DOCKER=false
INTERACTIVE=true
OPEN_BROWSER=true

log() { printf '[ADRO] %s\n' "$*"; }
warn() { printf '[ADRO] WARNING: %s\n' "$*" >&2; }
fail() { printf '[ADRO] ERROR: %s\n' "$*" >&2; exit 1; }
has() { command -v "$1" >/dev/null 2>&1; }

usage() {
  cat <<'EOF'
Usage: ./start.sh [options]

Options:
  --without-multica  Start ADRO only with its deterministic MockProvider.
  --no-docker         Start ADRO API and WebUI directly with Go. Uses remote
                      Multica when ADRO_MULTICA_URL and ADRO_MULTICA_TOKEN are set.
  --non-interactive  Do not launch the first-time Multica login flow.
  --no-open          Do not open the ADRO WebUI in a browser.
  --status           Show ADRO, Multica, and daemon status.
  --stop             Stop this ADRO stack and its isolated Multica profile.
  -h, --help         Show this help.

Environment:
  ADRO_MULTICA_TOKEN     PAT used by ADRO's Multica HTTP Provider.
  ADRO_MULTICA_URL       Remote Multica API URL used with --no-docker.
  ADRO_MULTICA_AGENT_ID  Optional default Multica Agent UUID.
  ADRO_MULTICA_WORKSPACE_ID  Optional Multica workspace UUID (auto-discovered when unique).
  ADRO_MULTICA_RUNTIME_ID    Optional Multica runtime UUID (online runtime auto-discovered when unique).
  ADRO_MULTICA_PROJECT_ID    Optional Multica project UUID for created Work Items.
  ADRO_MULTICA_AGENT_MAP     Optional JSON workspace/member/role routing map.
  ADRO_MULTICA_CAPABILITIES_PATH  Optional provider capabilities path override.
  ADRO_MULTICA_ATTACHMENT_PATH    Optional provider attachment path override.
  ADRO_MULTICA_WS_URL        Optional provider run-event WebSocket URL.
  ADRO_ADMIN_USERNAME    Initial ADRO administrator (default: admin).
  ADRO_ADMIN_PASSWORD    Initial ADRO administrator password.
  ADRO_API_PORT          Local API port for --no-docker (default: 8080).
  ADRO_WEB_PORT          Local WebUI port (default: 8081).
  MULTICA_VERSION        Pinned Multica release (default: 0.4.35).
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --without-multica) WITH_MULTICA=false ;;
    --no-docker) NO_DOCKER=true; WITH_MULTICA=false ;;
    --non-interactive) INTERACTIVE=false ;;
    --no-open) OPEN_BROWSER=false ;;
    --status) MODE="status" ;;
    --stop) MODE="stop" ;;
    -h|--help) usage; exit 0 ;;
    *) fail "unknown option: $1" ;;
  esac
  shift
done

compose_adro() {
  docker compose --env-file "$ADRO_ENV_FILE" --project-name adro -f "$COMPOSE_FILE" "$@"
}

compose_multica() {
  (
    cd "$MULTICA_DIR"
    docker compose --env-file .env --project-name adro-multica -f docker-compose.selfhost.yml "$@"
  )
}

multica_bin() {
  if [ -x "$BIN_DIR/multica" ]; then
    printf '%s' "$BIN_DIR/multica"
  elif has multica; then
    command -v multica
  else
    printf '%s' "$BIN_DIR/multica"
  fi
}

stop_local_processes() {
  local pid_file pid
  for pid_file in "$LOCAL_API_PID_FILE" "$LOCAL_WEB_PID_FILE"; do
    if [ ! -f "$pid_file" ]; then
      continue
    fi
    pid=""
    read -r pid < "$pid_file" || true
    if [[ "$pid" =~ ^[0-9]+$ ]] && kill -0 "$pid" 2>/dev/null; then
      log "Stopping direct process $pid"
      kill "$pid" 2>/dev/null || true
    fi
    rm -f "$pid_file"
  done
}

show_status() {
  local direct_mode=false
  [ -f "$DIRECT_MODE_FILE" ] && direct_mode=true
  if [ "$direct_mode" = true ]; then
    warn "Direct no-Docker profile is active; skipping Docker and Multica checks"
  elif has docker; then
    if [ -f "$ADRO_ENV_FILE" ]; then
      log "ADRO services"
      compose_adro ps || true
    else
      warn "ADRO has not been initialized"
    fi
    if [ -f "$MULTICA_DIR/docker-compose.selfhost.yml" ]; then
      log "Multica services"
      compose_multica ps || true
    fi
  else
    warn "Docker is not installed; showing direct local services only"
  fi
  if has curl; then
    if curl -fsS "http://127.0.0.1:$ADRO_API_PORT/readyz" >/dev/null 2>&1; then
      log "Direct ADRO API is ready at http://127.0.0.1:$ADRO_API_PORT"
    fi
    if curl -fsS "http://127.0.0.1:$ADRO_WEB_PORT/" >/dev/null 2>&1; then
      log "Direct ADRO WebUI is ready at http://127.0.0.1:$ADRO_WEB_PORT"
    fi
  fi
  local pid pid_file
  for pid_file in "$LOCAL_API_PID_FILE" "$LOCAL_WEB_PID_FILE"; do
    if [ -f "$pid_file" ]; then
      pid=""
      read -r pid < "$pid_file" || true
      if [[ "$pid" =~ ^[0-9]+$ ]] && kill -0 "$pid" 2>/dev/null; then
        log "direct process $pid_file: running (pid $pid)"
      else
        warn "direct process $pid_file: stale pid file"
      fi
    fi
  done
  if [ "$direct_mode" = false ]; then
    local cli
    cli="$(multica_bin)"
    if [ -x "$cli" ]; then
      log "Multica daemon profile: $MULTICA_PROFILE"
      "$cli" --profile "$MULTICA_PROFILE" daemon status --output json || true
    fi
  fi
}

stop_all() {
  stop_local_processes
  if [ ! -f "$DIRECT_MODE_FILE" ] && has docker && [ -f "$ADRO_ENV_FILE" ]; then
    compose_adro down || true
  fi
  if [ ! -f "$DIRECT_MODE_FILE" ] && has docker && [ -f "$MULTICA_DIR/docker-compose.selfhost.yml" ]; then
    compose_multica down || true
  fi
  if [ ! -f "$DIRECT_MODE_FILE" ]; then
    local cli
    cli="$(multica_bin)"
    if [ -x "$cli" ]; then
      "$cli" --profile "$MULTICA_PROFILE" daemon stop || true
    fi
  fi
  log "Stopped ADRO and the isolated Multica stack"
}

if [ "$MODE" = "status" ]; then show_status; exit 0; fi
if [ "$MODE" = "stop" ]; then stop_all; exit 0; fi

if [ "$NO_DOCKER" = true ]; then
  for command_name in go curl openssl; do
    has "$command_name" || fail "$command_name is required for --no-docker"
  done
else
  for command_name in docker curl tar openssl; do
    has "$command_name" || fail "$command_name is required"
  done
  docker compose version >/dev/null 2>&1 || fail "Docker Compose v2 is required"
  docker info >/dev/null 2>&1 || fail "Docker is installed but the daemon is not running"
fi
mkdir -p "$STATE_DIR" "$BIN_DIR"
# State contains administrator credentials and provider credentials in the
# container profile. Keep it private before writing any files into it.
chmod 0700 "$STATE_DIR"

detect_platform() {
  case "$(uname -s)" in
    Darwin) MULTICA_OS="darwin" ;;
    Linux) MULTICA_OS="linux" ;;
    *) fail "Multica bootstrap supports macOS and Linux; use --without-multica on this platform" ;;
  esac
  case "$(uname -m)" in
    x86_64|amd64) MULTICA_ARCH="amd64" ;;
    arm64|aarch64) MULTICA_ARCH="arm64" ;;
    *) fail "unsupported architecture: $(uname -m)" ;;
  esac
}

sha256_file() {
  if has sha256sum; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

install_multica_cli() {
  detect_platform
  local current archive tag base temp_dir expected actual
  current=""
  if [ -x "$BIN_DIR/multica" ]; then
    current="$($BIN_DIR/multica version 2>/dev/null | awk 'NR == 1 {print $2}')"
  fi
  if [ "${current#v}" = "$MULTICA_VERSION" ]; then
    log "Multica CLI $MULTICA_VERSION is already installed"
    return
  fi
  tag="v$MULTICA_VERSION"
  archive="multica-cli-${MULTICA_VERSION}-${MULTICA_OS}-${MULTICA_ARCH}.tar.gz"
  base="https://github.com/multica-ai/multica/releases/download/$tag"
  temp_dir="$(mktemp -d "${TMPDIR:-/tmp}/adro-multica.XXXXXX")"
  log "Downloading Multica CLI $tag"
  curl -fL --retry 3 "$base/$archive" -o "$temp_dir/$archive"
  curl -fL --retry 3 "$base/checksums.txt" -o "$temp_dir/checksums.txt"
  expected="$(awk -v file="$archive" '$2 == file || $2 == "*" file {print $1; exit}' "$temp_dir/checksums.txt")"
  [ -n "$expected" ] || fail "official checksum does not contain $archive"
  actual="$(sha256_file "$temp_dir/$archive")"
  [ "$actual" = "$expected" ] || fail "checksum verification failed for $archive"
  tar -xzf "$temp_dir/$archive" -C "$temp_dir" multica
  install -m 0755 "$temp_dir/multica" "$BIN_DIR/multica"
  rm -rf "$temp_dir"
  log "Verified and installed Multica CLI at $BIN_DIR/multica"
}

prepare_multica_server() {
  has git || fail "git is required to install the pinned Multica self-host assets"
  if [ ! -d "$MULTICA_DIR/.git" ]; then
    log "Cloning Multica v$MULTICA_VERSION self-host assets"
    git clone --depth 1 --branch "v$MULTICA_VERSION" https://github.com/multica-ai/multica.git "$MULTICA_DIR"
  fi
  local checked_out_ref
  checked_out_ref="$(git -C "$MULTICA_DIR" describe --tags --exact-match HEAD 2>/dev/null || true)"
  [ "$checked_out_ref" = "v$MULTICA_VERSION" ] || fail "Multica assets are $checked_out_ref, expected v$MULTICA_VERSION; move $MULTICA_DIR aside and retry"
  [ -f "$MULTICA_DIR/docker-compose.selfhost.yml" ] || fail "Multica self-host compose file is missing"
  if [ ! -f "$MULTICA_DIR/.env" ]; then
    cp "$MULTICA_DIR/.env.example" "$MULTICA_DIR/.env"
    set_env_key "$MULTICA_DIR/.env" POSTGRES_PASSWORD "$(openssl rand -hex 24)"
    set_env_key "$MULTICA_DIR/.env" JWT_SECRET "$(openssl rand -hex 32)"
  fi
  set_env_key "$MULTICA_DIR/.env" PORT "$MULTICA_BACKEND_PORT"
  set_env_key "$MULTICA_DIR/.env" FRONTEND_PORT "$MULTICA_FRONTEND_PORT"
  set_env_key "$MULTICA_DIR/.env" FRONTEND_ORIGIN "http://localhost:$MULTICA_FRONTEND_PORT"
  set_env_key "$MULTICA_DIR/.env" MULTICA_APP_URL "http://localhost:$MULTICA_FRONTEND_PORT"
  set_env_key "$MULTICA_DIR/.env" APP_ENV development
  set_env_key "$MULTICA_DIR/.env" MULTICA_DEV_VERIFICATION_CODE 888888
  chmod 0600 "$MULTICA_DIR/.env"
  log "Starting pinned Multica server and PostgreSQL"
  compose_multica pull
  compose_multica up -d
  local attempt
  for attempt in $(seq 1 60); do
    if curl -fsS "http://127.0.0.1:$MULTICA_BACKEND_PORT/health" >/dev/null 2>&1; then
      log "Multica API is ready at http://127.0.0.1:$MULTICA_BACKEND_PORT"
      return
    fi
    sleep 2
  done
  fail "Multica did not become ready; inspect with ./start.sh --status"
}

configure_multica_profile() {
  local cli="$BIN_DIR/multica"
  local auth_status daemon_status
  auth_status="$("$cli" --profile "$MULTICA_PROFILE" auth status 2>&1 || true)"
  if [[ "$auth_status" == *"Token:"* ]]; then
    daemon_status="$("$cli" --profile "$MULTICA_PROFILE" daemon status --output json 2>/dev/null || true)"
    if [[ "$daemon_status" != *'"status": "running"'* && "$daemon_status" != *'"status": "starting"'* ]]; then
      "$cli" --profile "$MULTICA_PROFILE" daemon start
    fi
    return
  fi
  if [ "$INTERACTIVE" = true ]; then
    log "First-time Multica authentication is required; verification code: 888888"
    "$cli" --profile "$MULTICA_PROFILE" setup self-host \
      --server-url "http://127.0.0.1:$MULTICA_BACKEND_PORT" \
      --app-url "http://127.0.0.1:$MULTICA_FRONTEND_PORT"
  else
    warn "Multica profile is not authenticated; run: $cli --profile $MULTICA_PROFILE setup self-host --server-url http://127.0.0.1:$MULTICA_BACKEND_PORT --app-url http://127.0.0.1:$MULTICA_FRONTEND_PORT"
  fi
}

collect_multica_provider_token() {
  [ -z "${ADRO_MULTICA_TOKEN:-}" ] || return
  if [ -f "$ADRO_ENV_FILE" ] && awk -F= '$1 == "ADRO_MULTICA_TOKEN" && length($2) > 0 {found = 1} END {exit !found}' "$ADRO_ENV_FILE"; then
    return
  fi
  local cli config_output config_file profile_token
  cli="$BIN_DIR/multica"
  config_output="$("$cli" --profile "$MULTICA_PROFILE" config show 2>/dev/null || true)"
  config_file="$(printf '%s\n' "$config_output" | awk -F': ' '$1 == "Config file" {print $2; exit}')"
  if [ -f "$config_file" ]; then
    profile_token="$(awk -F'"' '$2 == "token" {print $4; exit}' "$config_file")"
    case "$profile_token" in
      mul_*|mcn_*)
        ADRO_MULTICA_TOKEN="$profile_token"
        export ADRO_MULTICA_TOKEN
        log "Using the PAT created by the isolated Multica profile"
        return
        ;;
    esac
  fi
  [ "$INTERACTIVE" = true ] || return
  [ -t 0 ] || return
  printf '\nADRO needs a Multica PAT for real Provider calls.\n'
  printf 'Create one in http://127.0.0.1:%s/settings?tab=tokens\n' "$MULTICA_FRONTEND_PORT"
  printf 'Paste the PAT now, or press Enter to start with MockProvider: '
  IFS= read -r -s ADRO_MULTICA_TOKEN
  export ADRO_MULTICA_TOKEN
  printf '\n'
}

prepare_adro_env() {
  local admin_user="${ADRO_ADMIN_USERNAME:-admin}"
  local admin_password="${ADRO_ADMIN_PASSWORD:-}"
  local provider_token="${ADRO_MULTICA_TOKEN:-}"
  local provider_agent_id="${ADRO_MULTICA_AGENT_ID:-}"
  local provider_workspace_id="${ADRO_MULTICA_WORKSPACE_ID:-}"
  local provider_runtime_id="${ADRO_MULTICA_RUNTIME_ID:-}"
  local provider_project_id="${ADRO_MULTICA_PROJECT_ID:-}"
  local provider_agent_map="${ADRO_MULTICA_AGENT_MAP:-}"
  local provider_capabilities_path="${ADRO_MULTICA_CAPABILITIES_PATH:-}"
  local provider_attachment_path="${ADRO_MULTICA_ATTACHMENT_PATH:-}"
  local provider_ws_url="${ADRO_MULTICA_WS_URL:-}"
  rm -f "$DIRECT_MODE_FILE"
  if [ -f "$ADRO_ENV_FILE" ]; then
    admin_user="$(awk -F= '$1 == "ADRO_ADMIN_USERNAME" {sub(/^[^=]*=/, ""); print; exit}' "$ADRO_ENV_FILE")"
    admin_password="$(awk -F= '$1 == "ADRO_ADMIN_PASSWORD" {sub(/^[^=]*=/, ""); print; exit}' "$ADRO_ENV_FILE")"
    if [ -z "$provider_token" ]; then
      provider_token="$(awk -F= '$1 == "ADRO_MULTICA_TOKEN" {sub(/^[^=]*=/, ""); print; exit}' "$ADRO_ENV_FILE")"
    fi
    if [ -z "$provider_agent_id" ]; then
      provider_agent_id="$(awk -F= '$1 == "ADRO_MULTICA_AGENT_ID" {sub(/^[^=]*=/, ""); print; exit}' "$ADRO_ENV_FILE")"
    fi
    if [ -z "$provider_workspace_id" ]; then
      provider_workspace_id="$(awk -F= '$1 == "ADRO_MULTICA_WORKSPACE_ID" {sub(/^[^=]*=/, ""); print; exit}' "$ADRO_ENV_FILE")"
    fi
    if [ -z "$provider_runtime_id" ]; then
      provider_runtime_id="$(awk -F= '$1 == "ADRO_MULTICA_RUNTIME_ID" {sub(/^[^=]*=/, ""); print; exit}' "$ADRO_ENV_FILE")"
    fi
    if [ -z "$provider_project_id" ]; then
      provider_project_id="$(awk -F= '$1 == "ADRO_MULTICA_PROJECT_ID" {sub(/^[^=]*=/, ""); print; exit}' "$ADRO_ENV_FILE")"
    fi
    if [ -z "$provider_agent_map" ]; then
      provider_agent_map="$(awk -F= '$1 == "ADRO_MULTICA_AGENT_MAP" {sub(/^[^=]*=/, ""); print; exit}' "$ADRO_ENV_FILE")"
    fi
    if [ -z "$provider_capabilities_path" ]; then
      provider_capabilities_path="$(awk -F= '$1 == "ADRO_MULTICA_CAPABILITIES_PATH" {sub(/^[^=]*=/, ""); print; exit}' "$ADRO_ENV_FILE")"
    fi
    if [ -z "$provider_attachment_path" ]; then
      provider_attachment_path="$(awk -F= '$1 == "ADRO_MULTICA_ATTACHMENT_PATH" {sub(/^[^=]*=/, ""); print; exit}' "$ADRO_ENV_FILE")"
    fi
    if [ -z "$provider_ws_url" ]; then
      provider_ws_url="$(awk -F= '$1 == "ADRO_MULTICA_WS_URL" {sub(/^[^=]*=/, ""); print; exit}' "$ADRO_ENV_FILE")"
    fi
  fi
  [ -n "$admin_user" ] || admin_user=admin
  [ -n "$admin_password" ] || admin_password="$(openssl rand -hex 16)"
  : > "$ADRO_ENV_FILE"
  chmod 0600 "$ADRO_ENV_FILE"
  set_env_key "$ADRO_ENV_FILE" ADRO_ADMIN_USERNAME "$admin_user"
  set_env_key "$ADRO_ENV_FILE" ADRO_ADMIN_PASSWORD "$admin_password"
  set_env_key "$ADRO_ENV_FILE" ADRO_API_PORT "${ADRO_API_PORT:-8080}"
  set_env_key "$ADRO_ENV_FILE" ADRO_WEB_PORT "$ADRO_WEB_PORT"
  set_env_key "$ADRO_ENV_FILE" ADRO_STATE_FILE "/var/lib/adro/control-plane.json"
  set_env_key "$ADRO_ENV_FILE" ADRO_EVENT_STATE_FILE "/var/lib/adro/events.json"
  set_env_key "$ADRO_ENV_FILE" ADRO_AUDIT_STATE_FILE "/var/lib/adro/audit.json"
  set_env_key "$ADRO_ENV_FILE" ADRO_RUNNER_STATE_FILE "/var/lib/adro/runners.json"
  if [ "$WITH_MULTICA" = true ] && [ -n "$provider_token" ]; then
    set_env_key "$ADRO_ENV_FILE" ADRO_PROVIDER multica
    set_env_key "$ADRO_ENV_FILE" ADRO_MULTICA_URL "http://host.docker.internal:$MULTICA_BACKEND_PORT"
    set_env_key "$ADRO_ENV_FILE" ADRO_MULTICA_TOKEN "$provider_token"
    set_env_key "$ADRO_ENV_FILE" ADRO_MULTICA_AGENT_ID "$provider_agent_id"
    set_env_key "$ADRO_ENV_FILE" ADRO_MULTICA_WORKSPACE_ID "$provider_workspace_id"
    set_env_key "$ADRO_ENV_FILE" ADRO_MULTICA_RUNTIME_ID "$provider_runtime_id"
    set_env_key "$ADRO_ENV_FILE" ADRO_MULTICA_PROJECT_ID "$provider_project_id"
    set_env_key "$ADRO_ENV_FILE" ADRO_MULTICA_AGENT_MAP "$provider_agent_map"
    set_env_key "$ADRO_ENV_FILE" ADRO_MULTICA_CAPABILITIES_PATH "$provider_capabilities_path"
    set_env_key "$ADRO_ENV_FILE" ADRO_MULTICA_ATTACHMENT_PATH "$provider_attachment_path"
    set_env_key "$ADRO_ENV_FILE" ADRO_MULTICA_WS_URL "$provider_ws_url"
  else
    set_env_key "$ADRO_ENV_FILE" ADRO_PROVIDER mock
    set_env_key "$ADRO_ENV_FILE" ADRO_MULTICA_URL ""
    set_env_key "$ADRO_ENV_FILE" ADRO_MULTICA_TOKEN ""
    set_env_key "$ADRO_ENV_FILE" ADRO_MULTICA_AGENT_ID ""
    set_env_key "$ADRO_ENV_FILE" ADRO_MULTICA_WORKSPACE_ID ""
    set_env_key "$ADRO_ENV_FILE" ADRO_MULTICA_RUNTIME_ID ""
    set_env_key "$ADRO_ENV_FILE" ADRO_MULTICA_PROJECT_ID ""
    set_env_key "$ADRO_ENV_FILE" ADRO_MULTICA_AGENT_MAP ""
    set_env_key "$ADRO_ENV_FILE" ADRO_MULTICA_CAPABILITIES_PATH ""
    set_env_key "$ADRO_ENV_FILE" ADRO_MULTICA_ATTACHMENT_PATH ""
    set_env_key "$ADRO_ENV_FILE" ADRO_MULTICA_WS_URL ""
  fi
  chmod 0600 "$ADRO_ENV_FILE"
}

prepare_local_env() {
  local admin_user="${ADRO_ADMIN_USERNAME:-admin}"
  local admin_password="${ADRO_ADMIN_PASSWORD:-}"
  local provider_url="${ADRO_MULTICA_URL:-}"
  local provider_token="${ADRO_MULTICA_TOKEN:-}"
  local provider_agent_map="${ADRO_MULTICA_AGENT_MAP:-}"
  if [ -f "$ADRO_ENV_FILE" ]; then
    local persisted_user persisted_password
    persisted_user="$(awk -F= '$1 == "ADRO_ADMIN_USERNAME" {sub(/^[^=]*=/, ""); print; exit}' "$ADRO_ENV_FILE")"
    persisted_password="$(awk -F= '$1 == "ADRO_ADMIN_PASSWORD" {sub(/^[^=]*=/, ""); print; exit}' "$ADRO_ENV_FILE")"
    [ -z "$persisted_user" ] || admin_user="$persisted_user"
    [ -z "$persisted_password" ] || admin_password="$persisted_password"
    if [ -z "$provider_agent_map" ]; then
      provider_agent_map="$(awk -F= '$1 == "ADRO_MULTICA_AGENT_MAP" {sub(/^[^=]*=/, ""); print; exit}' "$ADRO_ENV_FILE")"
    fi
  fi
  [ -n "$admin_user" ] || admin_user=admin
  [ -n "$admin_password" ] || admin_password="$(openssl rand -hex 16)"
  : > "$ADRO_ENV_FILE"
  chmod 0600 "$ADRO_ENV_FILE"
  set_env_key "$ADRO_ENV_FILE" ADRO_ADMIN_USERNAME "$admin_user"
  set_env_key "$ADRO_ENV_FILE" ADRO_ADMIN_PASSWORD "$admin_password"
  set_env_key "$ADRO_ENV_FILE" ADRO_API_PORT "$ADRO_API_PORT"
  set_env_key "$ADRO_ENV_FILE" ADRO_WEB_PORT "$ADRO_WEB_PORT"
  if { [ -n "$provider_url" ] && [ -z "$provider_token" ]; } || { [ -z "$provider_url" ] && [ -n "$provider_token" ]; }; then
    fail "--no-docker remote Multica requires both ADRO_MULTICA_URL and ADRO_MULTICA_TOKEN"
  fi
  if [ -n "$provider_url" ]; then
    set_env_key "$ADRO_ENV_FILE" ADRO_PROVIDER multica
    set_env_key "$ADRO_ENV_FILE" ADRO_MULTICA_URL "$provider_url"
    # Never persist a remote credential. The caller supplies it on each start.
    set_env_key "$ADRO_ENV_FILE" ADRO_MULTICA_TOKEN ""
    set_env_key "$ADRO_ENV_FILE" ADRO_MULTICA_AGENT_ID "${ADRO_MULTICA_AGENT_ID:-}"
    set_env_key "$ADRO_ENV_FILE" ADRO_MULTICA_WORKSPACE_ID "${ADRO_MULTICA_WORKSPACE_ID:-}"
    set_env_key "$ADRO_ENV_FILE" ADRO_MULTICA_RUNTIME_ID "${ADRO_MULTICA_RUNTIME_ID:-}"
    set_env_key "$ADRO_ENV_FILE" ADRO_MULTICA_PROJECT_ID "${ADRO_MULTICA_PROJECT_ID:-}"
    set_env_key "$ADRO_ENV_FILE" ADRO_MULTICA_AGENT_MAP "$provider_agent_map"
    set_env_key "$ADRO_ENV_FILE" ADRO_MULTICA_CAPABILITIES_PATH "${ADRO_MULTICA_CAPABILITIES_PATH:-}"
    set_env_key "$ADRO_ENV_FILE" ADRO_MULTICA_ATTACHMENT_PATH "${ADRO_MULTICA_ATTACHMENT_PATH:-}"
    set_env_key "$ADRO_ENV_FILE" ADRO_MULTICA_WS_URL "${ADRO_MULTICA_WS_URL:-}"
    export ADRO_PROVIDER=multica
    export ADRO_MULTICA_URL="$provider_url"
    export ADRO_MULTICA_TOKEN="$provider_token"
    export ADRO_MULTICA_AGENT_ID="${ADRO_MULTICA_AGENT_ID:-}"
    export ADRO_MULTICA_WORKSPACE_ID="${ADRO_MULTICA_WORKSPACE_ID:-}"
    export ADRO_MULTICA_RUNTIME_ID="${ADRO_MULTICA_RUNTIME_ID:-}"
    export ADRO_MULTICA_PROJECT_ID="${ADRO_MULTICA_PROJECT_ID:-}"
    export ADRO_MULTICA_AGENT_MAP="$provider_agent_map"
    export ADRO_MULTICA_CAPABILITIES_PATH="${ADRO_MULTICA_CAPABILITIES_PATH:-}"
    export ADRO_MULTICA_ATTACHMENT_PATH="${ADRO_MULTICA_ATTACHMENT_PATH:-}"
    export ADRO_MULTICA_WS_URL="${ADRO_MULTICA_WS_URL:-}"
  else
    set_env_key "$ADRO_ENV_FILE" ADRO_PROVIDER mock
    set_env_key "$ADRO_ENV_FILE" ADRO_MULTICA_URL ""
    set_env_key "$ADRO_ENV_FILE" ADRO_MULTICA_TOKEN ""
    set_env_key "$ADRO_ENV_FILE" ADRO_MULTICA_AGENT_MAP ""
    export ADRO_PROVIDER=mock
  fi
  set_env_key "$ADRO_ENV_FILE" ADRO_AUTH_MODE required
  set_env_key "$ADRO_ENV_FILE" ADRO_AUTH_STATE_FILE "$STATE_DIR/auth.json"
  set_env_key "$ADRO_ENV_FILE" ADRO_STATE_FILE "$STATE_DIR/control-plane.json"
  set_env_key "$ADRO_ENV_FILE" ADRO_EVENT_STATE_FILE "$STATE_DIR/events.json"
  set_env_key "$ADRO_ENV_FILE" ADRO_AUDIT_STATE_FILE "$STATE_DIR/audit.json"
  set_env_key "$ADRO_ENV_FILE" ADRO_RUNNER_STATE_FILE "$STATE_DIR/runners.json"
  chmod 0600 "$ADRO_ENV_FILE"
  : > "$DIRECT_MODE_FILE"
  export ADRO_ADMIN_USERNAME="$admin_user"
  export ADRO_ADMIN_PASSWORD="$admin_password"
  export ADRO_AUTH_MODE=required
  export ADRO_AUTH_STATE_FILE="$STATE_DIR/auth.json"
  export ADRO_STATE_FILE="$STATE_DIR/control-plane.json"
  export ADRO_EVENT_STATE_FILE="$STATE_DIR/events.json"
  export ADRO_AUDIT_STATE_FILE="$STATE_DIR/audit.json"
  export ADRO_RUNNER_STATE_FILE="$STATE_DIR/runners.json"
}

start_local_stack() {
  prepare_local_env
  stop_local_processes
  if curl -fsS "http://127.0.0.1:$ADRO_API_PORT/readyz" >/dev/null 2>&1; then
    fail "port $ADRO_API_PORT is already serving an API; set ADRO_API_PORT to another port"
  fi
  if curl -fsS "http://127.0.0.1:$ADRO_WEB_PORT/" >/dev/null 2>&1; then
    fail "port $ADRO_WEB_PORT is already serving a WebUI; set ADRO_WEB_PORT to another port"
  fi
  mkdir -p "$STATE_DIR/artifacts"
  log "Building direct ADRO API and WebUI binaries"
  go build -o "$BIN_DIR/adro-api" ./cmd/adro-api
  go build -o "$BIN_DIR/adro-web" ./cmd/adro-web
  log "Starting ADRO API and WebUI without Docker (provider: $ADRO_PROVIDER)"
  nohup "$BIN_DIR/adro-api" -addr "127.0.0.1:$ADRO_API_PORT" -artifact-root "$STATE_DIR/artifacts" \
    >"$LOCAL_API_LOG" 2>&1 </dev/null &
  local api_pid=$!
  printf '%s\n' "$api_pid" > "$LOCAL_API_PID_FILE"
  nohup "$BIN_DIR/adro-web" -addr "127.0.0.1:$ADRO_WEB_PORT" -root "$ROOT_DIR/apps/web" \
    >"$LOCAL_WEB_LOG" 2>&1 </dev/null &
  local web_pid=$!
  printf '%s\n' "$web_pid" > "$LOCAL_WEB_PID_FILE"
  local attempt
  for attempt in $(seq 1 40); do
    if curl -fsS "http://127.0.0.1:$ADRO_API_PORT/readyz" >/dev/null 2>&1 && curl -fsS "http://127.0.0.1:$ADRO_WEB_PORT/" >/dev/null 2>&1; then
      break
    fi
    sleep 0.25
  done
  if ! curl -fsS "http://127.0.0.1:$ADRO_API_PORT/readyz" >/dev/null 2>&1 || ! curl -fsS "http://127.0.0.1:$ADRO_WEB_PORT/" >/dev/null 2>&1; then
    stop_local_processes
    fail "direct ADRO stack did not become ready; inspect $LOCAL_API_LOG and $LOCAL_WEB_LOG"
  fi
  local web_url="http://127.0.0.1:$ADRO_WEB_PORT/?api=http://127.0.0.1:$ADRO_API_PORT"
  printf '\nADRO WebUI: %s\n' "$web_url"
  printf 'Administrator: %s\nPassword: %s\n' "$ADRO_ADMIN_USERNAME" "$ADRO_ADMIN_PASSWORD"
  printf 'Execution provider: %s\n' "$ADRO_PROVIDER"
  printf 'Status: ./start.sh --status\nStop:   ./start.sh --stop\nLogs:   %s, %s\n' "$LOCAL_API_LOG" "$LOCAL_WEB_LOG"
  if [ "$OPEN_BROWSER" = true ]; then
    if has open; then open "$web_url" >/dev/null 2>&1 || true
    elif has xdg-open; then xdg-open "$web_url" >/dev/null 2>&1 || true
    fi
  fi
}

if [ "$WITH_MULTICA" = true ]; then
  install_multica_cli
  prepare_multica_server
  configure_multica_profile
  collect_multica_provider_token
fi

if [ "$NO_DOCKER" = true ]; then
  start_local_stack
  exit 0
fi

prepare_adro_env
log "Starting ADRO API and WebUI"
compose_adro up -d --build
for attempt in $(seq 1 60); do
  if curl -fsS "http://127.0.0.1:$ADRO_WEB_PORT/readyz" >/dev/null 2>&1; then
    break
  fi
  sleep 2
done
curl -fsS "http://127.0.0.1:$ADRO_WEB_PORT/readyz" >/dev/null || fail "ADRO did not become ready; inspect with ./start.sh --status"

admin_user="$(awk -F= '$1 == "ADRO_ADMIN_USERNAME" {sub(/^[^=]*=/, ""); print; exit}' "$ADRO_ENV_FILE")"
admin_password="$(awk -F= '$1 == "ADRO_ADMIN_PASSWORD" {sub(/^[^=]*=/, ""); print; exit}' "$ADRO_ENV_FILE")"
printf '\nADRO WebUI: http://127.0.0.1:%s\n' "$ADRO_WEB_PORT"
printf 'Administrator: %s\nPassword: %s\n' "$admin_user" "$admin_password"
printf 'Status: ./start.sh --status\nStop:   ./start.sh --stop\n'
configured_provider="$(awk -F= '$1 == "ADRO_PROVIDER" {print $2; exit}' "$ADRO_ENV_FILE")"
if [ "$WITH_MULTICA" = true ] && [ "$configured_provider" != multica ]; then
  warn "Multica is running, but ADRO uses MockProvider until a Multica PAT is supplied. Run the interactive bootstrap again, or use: ADRO_MULTICA_TOKEN=<PAT> ./start.sh"
fi

if [ "$OPEN_BROWSER" = true ]; then
  if has open; then open "http://127.0.0.1:$ADRO_WEB_PORT" >/dev/null 2>&1 || true
  elif has xdg-open; then xdg-open "http://127.0.0.1:$ADRO_WEB_PORT" >/dev/null 2>&1 || true
  fi
fi
