#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=scripts/lib/real-codex.sh
source "$ROOT_DIR/scripts/lib/real-codex.sh"
# shellcheck source=scripts/lib/go-toolchain.sh
source "$ROOT_DIR/scripts/lib/go-toolchain.sh"

API_PORT="${ADRO_GRAPH_BROWSER_API_PORT:-18086}"
WEB_PORT="${ADRO_GRAPH_BROWSER_WEB_PORT:-18087}"
RUN_ID="$(date -u +%Y%m%dT%H%M%SZ)-$$"
REPORT_DIR="${ADRO_GRAPH_BROWSER_REPORT_DIR:-$ROOT_DIR/var/test-report/real-codex/$RUN_ID}"
RUN_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/adro-browser-graph.XXXXXX")"
STATE_HOME="$RUN_ROOT/state"
FIXTURE_REPO="$RUN_ROOT/fixture-repo"
API="http://127.0.0.1:$API_PORT"
WEB="http://127.0.0.1:$WEB_PORT"
START_LOG="$RUN_ROOT/start.log"
WEB_LOG="$RUN_ROOT/web.log"
WEB_PID=""
COMMIT_SHA="$(git -C "$ROOT_DIR" rev-parse HEAD 2>/dev/null || true)"
CODEX_VERSION=""
GO_VERSION=""
EVIDENCE="$REPORT_DIR/browser-graph-evidence.json"

log() { printf '[ADRO BROWSER GRAPH E2E] %s\n' "$*"; }
fail() { log "ERROR: $*" >&2; exit 1; }
sha256_file() { shasum -a 256 "$1" | awk '{print $1}'; }

cleanup() {
  local exit_status=$?
  if [ -f "$START_LOG" ]; then
    sed -E \
      -e "s#${HOME:-}#<home>#g" \
      -e "s#${RUN_ROOT}#<run-root>#g" \
      -e 's/(sk-[A-Za-z0-9_-]{10,})/<redacted-secret>/g' \
      "$START_LOG" >"$REPORT_DIR/start.log" 2>/dev/null || true
  fi
  [ -f "$WEB_LOG" ] && cp "$WEB_LOG" "$REPORT_DIR/browser-server.log" || true
  [ -n "$CODEX_VERSION" ] && printf '%s\n' "$CODEX_VERSION" >"$REPORT_DIR/codex-version.txt"
  REPORT_DIR="$REPORT_DIR" RUN_ID="$RUN_ID" EXIT_STATUS="$exit_status" COMMIT_SHA="$COMMIT_SHA" CODEX_VERSION="$CODEX_VERSION" GO_VERSION="$GO_VERSION" ruby -rjson -rdigest -e '
    dir = ENV.fetch("REPORT_DIR")
    files = Dir[File.join(dir, "*")].sort
    report = {
      "status" => ENV.fetch("EXIT_STATUS").to_i == 0 ? "passed" : "failed",
      "exit_status" => ENV.fetch("EXIT_STATUS").to_i,
      "run_id" => ENV.fetch("RUN_ID"),
      "commit_sha" => ENV.fetch("COMMIT_SHA"),
      "command" => "ADRO_REQUIRE_CODEX=1 bash scripts/browser-graph-real-e2e.sh",
      "codex_version" => ENV.fetch("CODEX_VERSION"),
      "go_version" => ENV.fetch("GO_VERSION"),
      "evidence_files" => files.map { |path| {"path" => File.basename(path), "sha256" => Digest::SHA256.file(path).hexdigest} }
    }
    if File.file?(File.join(dir, "browser-graph-evidence.json"))
      evidence = JSON.parse(File.read(File.join(dir, "browser-graph-evidence.json")))
      report["plan_id"] = evidence["plan_id"]
      report["requirement_id"] = evidence["requirement_id"]
      report["work_item_id"] = evidence["work_item_id"]
      report["agent_id"] = evidence["agent_id"]
      report["terminal_outcome"] = evidence.dig("timeline", "projection", "terminal_outcome")
      report["event_cursor"] = evidence.dig("timeline", "cursor")
      report["timeline_hash"] = Digest::SHA256.hexdigest(JSON.generate(evidence["timeline"])) if evidence["timeline"]
      report["replay_hash"] = Digest::SHA256.hexdigest(JSON.generate(evidence["replay"])) if evidence["replay"]
    end
    File.write(File.join(dir, "manifest.json"), JSON.pretty_generate(report) + "\n")
  ' || true
  if [ -n "$WEB_PID" ]; then kill "$WEB_PID" >/dev/null 2>&1 || true; wait "$WEB_PID" >/dev/null 2>&1 || true; fi
  ADRO_HOME="$STATE_HOME" ADRO_API_PORT="$API_PORT" ADRO_WEB_PORT="$WEB_PORT" "$ROOT_DIR/start.sh" --stop --no-open >/dev/null 2>&1 || true
  if [ "${ADRO_E2E_KEEP:-0}" = "1" ]; then
    log "Evidence retained at $RUN_ROOT"
  else
    rm -rf "$RUN_ROOT" 2>/dev/null || true
  fi
}
trap cleanup EXIT
trap 'exit 130' INT TERM HUP

command -v curl >/dev/null 2>&1 || fail "curl is required"
command -v ruby >/dev/null 2>&1 || fail "ruby is required"
command -v shasum >/dev/null 2>&1 || fail "shasum is required"
command -v npm >/dev/null 2>&1 || fail "npm is required"

executor="${ADRO_EXECUTOR:-}"
[ -n "$executor" ] || executor="$(select_real_codex || true)"
[ -n "$executor" ] || fail "Codex is required"
case "$(basename "$executor")" in codex|codex.exe) ;; *) fail "browser graph suite requires Codex" ;; esac
executor="$(command -v "$executor" 2>/dev/null || printf '%s' "$executor")"
CODEX_VERSION="$($executor --version 2>&1 || true)"
[ -n "$CODEX_VERSION" ] || fail "Codex is not runnable"
go_bin="$(select_go_bin || true)"
[ -n "$go_bin" ] || fail "Go is required"
GO_VERSION="$($go_bin version 2>/dev/null || true)"
go_root="$(resolve_go_root "$go_bin" || true)"
[ -n "$go_root" ] || fail "could not resolve Go root"
export ADRO_GO_BIN="$go_bin" GOROOT="$go_root"

mkdir -p "$REPORT_DIR" "$STATE_HOME" "$FIXTURE_REPO"
printf '%s\n' 'module example.com/adro-browser-graph' 'go 1.24.1' >"$FIXTURE_REPO/go.mod"
printf '%s\n' 'package browsergraph' '' 'func Ready() string { return "ready" }' >"$FIXTURE_REPO/status.go"
git -C "$FIXTURE_REPO" init -q
git -C "$FIXTURE_REPO" config user.email adro-browser-graph@example.invalid
git -C "$FIXTURE_REPO" config user.name ADRO-Browser-Graph
git -C "$FIXTURE_REPO" add .
git -C "$FIXTURE_REPO" commit -qm 'fixture: browser-created graph source'
git -C "$FIXTURE_REPO" branch -M main
prepare_real_codex_home "$RUN_ROOT/codex-home"
trust_real_codex_project "$RUN_ROOT/codex-home" "$STATE_HOME"
codex_wrapper="$RUN_ROOT/codex"
write_real_codex_wrapper "$codex_wrapper" "$executor" "$go_root"
executor="$codex_wrapper"
if [ -z "${ADRO_EXECUTOR_COMMAND:-}" ]; then
  configure_real_codex_command "$executor"
fi
export ADRO_HOME="$STATE_HOME" ADRO_API_PORT="$API_PORT" ADRO_WEB_PORT="$WEB_PORT"
export ADRO_AUTH_MODE=required ADRO_ADMIN_USERNAME=admin ADRO_ADMIN_PASSWORD=AdminPass123!
export ADRO_EXECUTOR="$executor" ADRO_ARTIFACT_ROOT="$STATE_HOME/artifacts" ADRO_WORK_ROOT="$STATE_HOME/workspaces"
export ADRO_ALLOWED_ORIGINS="$WEB,http://localhost:$WEB_PORT,http://[::1]:$WEB_PORT"
export ADRO_GRAPH_WATCH_TIMEOUT="${ADRO_GRAPH_WATCH_TIMEOUT:-30m}" ADRO_GRAPH_WATCH_INTERVAL="${ADRO_GRAPH_WATCH_INTERVAL:-100ms}"

"$ROOT_DIR/start.sh" --no-open >"$START_LOG" 2>&1 || { cat "$START_LOG" >&2; fail "ADRO did not start"; }
curl -fsS "$API/readyz" >/dev/null || fail "ADRO is not ready"
node "$ROOT_DIR/e2e/static-server.js" "$WEB_PORT" >"$WEB_LOG" 2>&1 &
WEB_PID=$!
for _ in $(seq 1 60); do curl -fsS "$WEB/" >/dev/null 2>&1 && break; sleep 1; done
curl -fsS "$WEB/" >/dev/null || fail "browser server is not ready"

export ADRO_RUN_BROWSER_REAL_GRAPH=1 ADRO_GRAPH_BROWSER_API_URL="$API" ADRO_GRAPH_BROWSER_WEB_URL="$WEB" ADRO_GRAPH_BROWSER_REPOSITORY_URL="file://$FIXTURE_REPO"
export ADRO_GRAPH_BROWSER_REPORT="$EVIDENCE" ADRO_GRAPH_BROWSER_SCREENSHOT="$REPORT_DIR/browser-graph.png" ADRO_COMMIT_SHA="$COMMIT_SHA"
npx playwright test e2e/graph-browser.spec.js --config=playwright.real-graph.config.js
log "PASS: browser-created graph reached terminal success and replayed through real Codex"
