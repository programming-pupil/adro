#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
STATE_DIR="${ADRO_HOME:-$ROOT_DIR/.adro}"
BIN_DIR="$STATE_DIR/bin"
ARTIFACT_ROOT="${ADRO_ARTIFACT_ROOT:-$STATE_DIR/artifacts}"
WORK_ROOT="${ADRO_WORK_ROOT:-$STATE_DIR/workspaces}"
API_PORT="${ADRO_API_PORT:-8080}"
WEB_PORT="${ADRO_WEB_PORT:-8081}"
API_PID_FILE="$STATE_DIR/adro-api.pid"
WEB_PID_FILE="$STATE_DIR/adro-web.pid"
API_LOG="$STATE_DIR/adro-api.log"
WEB_LOG="$STATE_DIR/adro-web.log"
PROFILE_FILE="$STATE_DIR/local-profile"
MODE="start"
OPEN_BROWSER=true

log() { printf '[ADRO] %s\n' "$*"; }
warn() { printf '[ADRO] WARNING: %s\n' "$*" >&2; }
fail() { printf '[ADRO] ERROR: %s\n' "$*" >&2; exit 1; }
has() { command -v "$1" >/dev/null 2>&1; }

usage() {
  cat <<'EOF'
Usage: ./start.sh [options]

Options:
  --no-docker       Explicitly select the native local profile (the default).
  --non-interactive Do not open a browser after startup.
  --no-open         Do not open a browser after startup.
  --status          Show local API, WebUI, and executor status.
  --stop            Stop local API and WebUI processes.
  -h, --help        Show this help.

Environment:
  ADRO_EXECUTOR           Executable path (auto-discovers claude, codex, claude-code).
  ADRO_EXECUTOR_COMMAND   Executable plus arguments; use {input} as prompt placeholder.
  ADRO_EXECUTOR_TIMEOUT   Optional per-run deadline (Go duration, e.g. 15m).
  ADRO_PIPELINE_WATCH_TIMEOUT  Local pipeline watchdog deadline (Go duration, e.g. 30m).
  ADRO_HARNESS_RECOVERY_INTERVAL  Harness recovery worker interval (default: 1s).
  ADRO_HARNESS_DISPATCH_LEASE_TTL  Provider intent claim lease (must exceed executor timeout).
  ADRO_HARNESS_STATE_FILE  Durable session/turn/checkpoint state file.
  ADRO_PLUGIN_STATE_FILE   Durable signed plugin installation registry.
  ADRO_HOME               Local state directory (default: ./.adro).
  ADRO_API_PORT           API port (default: 8080).
  ADRO_WEB_PORT           WebUI port (default: 8081).
  ADRO_PUBLIC_API_URL     API URL exposed to executor callbacks (defaults to the local API port).
  ADRO_ADMIN_USERNAME     Initial local administrator (default: admin).
  ADRO_ADMIN_PASSWORD     Initial local administrator password.
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --no-docker) ;;
    --non-interactive|--no-open) OPEN_BROWSER=false ;;
    --status) MODE="status" ;;
    --stop) MODE="stop" ;;
    -h|--help) usage; exit 0 ;;
    *) fail "unknown option: $1" ;;
  esac
  shift
done

executor_path() {
	if [ -n "${ADRO_EXECUTOR:-}" ]; then
    if [ -x "$ADRO_EXECUTOR" ]; then printf '%s' "$ADRO_EXECUTOR"; return 0; fi
    command -v "$ADRO_EXECUTOR" 2>/dev/null && return 0
		return 1
	fi
	if [ -n "${ADRO_EXECUTOR_COMMAND:-}" ]; then
		local command_name="${ADRO_EXECUTOR_COMMAND%% *}"
		if [ -x "$command_name" ]; then printf '%s' "$command_name"; return 0; fi
		if command -v "$command_name" >/dev/null 2>&1; then command -v "$command_name"; return 0; fi
	fi
	local candidate
  for candidate in claude codex claude-code; do
    if has "$candidate"; then command -v "$candidate"; return 0; fi
  done
  return 1
}

pid_running() {
  local pid_file="$1" pid=""
  [ -f "$pid_file" ] || return 1
  read -r pid < "$pid_file" || true
  [[ "$pid" =~ ^[0-9]+$ ]] && kill -0 "$pid" 2>/dev/null
}

stop_process() {
  local pid_file="$1" pid=""
  if [ -f "$pid_file" ]; then
    read -r pid < "$pid_file" || true
    if [[ "$pid" =~ ^[0-9]+$ ]] && kill -0 "$pid" 2>/dev/null; then
      kill "$pid" 2>/dev/null || true
      for _ in 1 2 3 4 5; do
        kill -0 "$pid" 2>/dev/null || break
        sleep 0.2
      done
      kill -9 "$pid" 2>/dev/null || true
    fi
    rm -f "$pid_file"
  fi
}

stop_all() {
  stop_process "$API_PID_FILE"
  stop_process "$WEB_PID_FILE"
  rm -f "$PROFILE_FILE"
  log "Local ADRO processes stopped"
}

show_status() {
  if pid_running "$API_PID_FILE"; then log "API process is running"; else warn "API process is not running"; fi
  if pid_running "$WEB_PID_FILE"; then log "WebUI process is running"; else warn "WebUI process is not running"; fi
  if has curl && curl -fsS "http://127.0.0.1:$API_PORT/readyz" >/dev/null 2>&1; then
    log "API ready: http://127.0.0.1:$API_PORT"
  else
    warn "API is not ready at http://127.0.0.1:$API_PORT"
  fi
  if has curl && curl -fsS "http://127.0.0.1:$WEB_PORT/" >/dev/null 2>&1; then
    log "WebUI ready: http://127.0.0.1:$WEB_PORT"
  else
    warn "WebUI is not ready at http://127.0.0.1:$WEB_PORT"
  fi
  if executor="$(executor_path 2>/dev/null)"; then log "Executor discovered: $executor"; else warn "No coding executor discovered"; fi
}

if [ "$MODE" = "stop" ]; then stop_all; exit 0; fi
if [ "$MODE" = "status" ]; then show_status; exit 0; fi

has go || fail "Go is required to build ADRO locally"
has curl || fail "curl is required to verify local readiness"
executor="$(executor_path 2>/dev/null || true)"
[ -n "$executor" ] || fail "No coding executor found; install claude/codex or set ADRO_EXECUTOR"

mkdir -p "$BIN_DIR" "$ARTIFACT_ROOT" "$WORK_ROOT"
go build -o "$BIN_DIR/adro-api" ./cmd/adro-api
go build -o "$BIN_DIR/adro-web" ./cmd/adro-web
stop_process "$API_PID_FILE"
stop_process "$WEB_PID_FILE"

export ADRO_PROVIDER=local
export ADRO_EXECUTOR="$executor"
export ADRO_ARTIFACT_ROOT="$ARTIFACT_ROOT"
export ADRO_WORK_ROOT="$WORK_ROOT"
export ADRO_PUBLIC_API_URL="${ADRO_PUBLIC_API_URL:-http://127.0.0.1:$API_PORT}"
export ADRO_STATE_FILE="${ADRO_STATE_FILE:-$STATE_DIR/state.json}"
export ADRO_EVENT_STATE_FILE="${ADRO_EVENT_STATE_FILE:-$STATE_DIR/events.json}"
export ADRO_AUDIT_STATE_FILE="${ADRO_AUDIT_STATE_FILE:-$STATE_DIR/audit.json}"
export ADRO_AUTH_STATE_FILE="${ADRO_AUTH_STATE_FILE:-$STATE_DIR/auth.json}"
export ADRO_RUN_STATE_FILE="${ADRO_RUN_STATE_FILE:-$STATE_DIR/runs.json}"
export ADRO_HARNESS_STATE_FILE="${ADRO_HARNESS_STATE_FILE:-$STATE_DIR/harness.json}"
export ADRO_PLUGIN_STATE_FILE="${ADRO_PLUGIN_STATE_FILE:-$STATE_DIR/plugins.json}"

log "Starting native ADRO API on :$API_PORT"
nohup "$BIN_DIR/adro-api" -addr ":$API_PORT" -artifact-root "$ARTIFACT_ROOT" >"$API_LOG" 2>&1 < /dev/null &
printf '%s\n' "$!" > "$API_PID_FILE"
log "Starting ADRO WebUI on :$WEB_PORT"
nohup "$BIN_DIR/adro-web" -addr ":$WEB_PORT" -root "$ROOT_DIR/apps/web" >"$WEB_LOG" 2>&1 < /dev/null &
printf '%s\n' "$!" > "$WEB_PID_FILE"

ready=false
for _ in $(seq 1 40); do
  if curl -fsS "http://127.0.0.1:$API_PORT/readyz" >/dev/null 2>&1 && curl -fsS "http://127.0.0.1:$WEB_PORT/" >/dev/null 2>&1; then
    ready=true
    break
  fi
  sleep 0.5
done
[ "$ready" = true ] || { show_status; fail "local ADRO did not become ready; inspect $API_LOG and $WEB_LOG"; }
: > "$PROFILE_FILE"
log "ADRO is ready: http://127.0.0.1:$WEB_PORT"
if [ "$OPEN_BROWSER" = true ]; then
  if [[ "$(uname -s)" = "Darwin" ]] && has open; then open "http://127.0.0.1:$WEB_PORT" >/dev/null 2>&1 || true; fi
  if [[ "$(uname -s)" = "Linux" ]] && has xdg-open; then xdg-open "http://127.0.0.1:$WEB_PORT" >/dev/null 2>&1 || true; fi
fi
