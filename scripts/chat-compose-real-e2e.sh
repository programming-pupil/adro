#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=scripts/lib/real-codex.sh
source "$ROOT_DIR/scripts/lib/real-codex.sh"

API_PORT="${ADRO_CHAT_REAL_API_PORT:-18090}"
WEB_PORT="${ADRO_CHAT_REAL_WEB_PORT:-18091}"
API="http://127.0.0.1:$API_PORT"
RUN_ID="$(date -u +%Y%m%dT%H%M%SZ)-$$"
RUN_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/adro-chat-compose-real.XXXXXX")"
STATE_HOME="$RUN_ROOT/state"
REPORT_DIR="${ADRO_CHAT_REAL_REPORT_DIR:-$ROOT_DIR/var/test-report/real-codex/$RUN_ID}"
START_LOG="$RUN_ROOT/start.log"
COMMIT_SHA="$(git -C "$ROOT_DIR" rev-parse HEAD 2>/dev/null || true)"
CODEX_VERSION=""
CODEX_COMMAND=""
chat_id=""
compose_run_id=""
created_agent_id=""

mkdir -p "$REPORT_DIR" "$STATE_HOME"

log() { printf '[ADRO CHAT COMPOSE REAL E2E] %s\n' "$*"; }
fail() { log "ERROR: $*" >&2; exit 1; }

json_field() {
  local path="$1"
  ruby -rjson -e '
    value = JSON.parse(STDIN.read)
    ARGV[0].split(".").each do |key|
      value = value.is_a?(Array) ? value.fetch(key.to_i) : value[key]
    end
    exit 0 if value.nil?
    puts(value.is_a?(String) ? value : JSON.generate(value))
  ' "$path"
}

redact_file() {
  local source="$1"
  local target="$2"
  sed -E \
    -e "s#${HOME:-}#<home>#g" \
    -e "s#${RUN_ROOT}#<run-root>#g" \
    -e 's#(^|[^A-Za-z0-9])sk-[A-Za-z0-9_-]{20,}#\1<redacted-secret>#g' \
    "$source" >"$target" 2>/dev/null || true
}

capture_run() {
  local label="$1"
  local run_id="$2"
  curl -fsS "$API/api/v1/runs/$run_id" -H 'X-Workspace-ID: local' >"$REPORT_DIR/run-$label.json"
  curl -fsS "$API/api/v1/runs/$run_id/events?limit=250" -H 'X-Workspace-ID: local' >"$REPORT_DIR/run-$label-events.json"
}

cleanup() {
  local exit_status=$?
  if [ -f "$START_LOG" ]; then
    redact_file "$START_LOG" "$REPORT_DIR/start.log"
  fi
  local first_run_id second_run_id switched_run_id restart_run_id
  first_run_id=""
  second_run_id=""
  switched_run_id=""
  restart_run_id=""
  if [ -f "$REPORT_DIR/chat-first.json" ]; then first_run_id="$(json_field run.id <"$REPORT_DIR/chat-first.json" 2>/dev/null || true)"; fi
  if [ -f "$REPORT_DIR/chat-second.json" ]; then second_run_id="$(json_field run.id <"$REPORT_DIR/chat-second.json" 2>/dev/null || true)"; fi
  if [ -f "$REPORT_DIR/chat-switched.json" ]; then switched_run_id="$(json_field run.id <"$REPORT_DIR/chat-switched.json" 2>/dev/null || true)"; fi
  if [ -f "$REPORT_DIR/chat-restart.json" ]; then restart_run_id="$(json_field run.id <"$REPORT_DIR/chat-restart.json" 2>/dev/null || true)"; fi
  for pair in first:"$first_run_id" second:"$second_run_id" switched:"$switched_run_id" restart:"$restart_run_id"; do
    label="${pair%%:*}"
    value="${pair#*:}"
    [ -n "$value" ] || continue
    [ -f "$REPORT_DIR/run-$label.json" ] || curl -sS "$API/api/v1/runs/$value" -H 'X-Workspace-ID: local' >"$REPORT_DIR/run-$label.json" 2>/dev/null || true
    [ -f "$REPORT_DIR/run-$label-events.json" ] || curl -sS "$API/api/v1/runs/$value/events?limit=250" -H 'X-Workspace-ID: local' >"$REPORT_DIR/run-$label-events.json" 2>/dev/null || true
  done
  RUN_ID="$RUN_ID" EXIT_STATUS="$exit_status" REPORT_DIR="$REPORT_DIR" COMMIT_SHA="$COMMIT_SHA" CODEX_VERSION="$CODEX_VERSION" CODEX_COMMAND="$CODEX_COMMAND" CHAT_ID="$chat_id" COMPOSE_RUN_ID="$compose_run_id" AGENT_ID="$created_agent_id" ruby -rjson -rdigest -e '
    dir = ENV.fetch("REPORT_DIR")
    files = Dir[File.join(dir, "*")].sort
    read = ->(name) { path = File.join(dir, name); File.file?(path) ? (JSON.parse(File.read(path)) rescue {}) : {} }
    event_cursor = ->(name) {
      value = read.call(name)
      items = value["items"] || value["events"] || []
      {"count" => items.length, "last_event_id" => items.last && (items.last["event_id"] || items.last["id"])}
    }
    run = ->(name) { value = read.call(name); {
      "id" => value["id"], "status" => value["status"], "executor_pid" => value["executor_pid"],
      "executor_path" => value["executor_path"], "work_dir" => value["work_dir"], "session_id" => value["session_id"],
      "session_continuity" => value["session_continuity"], "output_sha256" => value["output_sha256"],
      "worktree_sha256" => value["worktree_sha256"], "tool_events_sha256" => value["tool_events_sha256"]
    } }
    report = {
      "status" => ENV.fetch("EXIT_STATUS").to_i == 0 ? "passed" : "failed",
      "exit_status" => ENV.fetch("EXIT_STATUS").to_i,
      "run_id" => ENV.fetch("RUN_ID"),
      "commit_sha" => ENV.fetch("COMMIT_SHA"),
      "command" => "ADRO_REQUIRE_CODEX=1 bash scripts/chat-compose-real-e2e.sh",
      "codex_version" => ENV.fetch("CODEX_VERSION"),
      "codex_command" => ENV.fetch("CODEX_COMMAND"),
      "runtime" => "codex",
      "model" => "operator-selected Codex relay profile",
      "chat_id" => ENV.fetch("CHAT_ID"),
      "compose_run_id" => ENV.fetch("COMPOSE_RUN_ID"),
      "agent_id" => ENV.fetch("AGENT_ID"),
      "chat_runs" => {
        "first" => run.call("run-first.json"), "second" => run.call("run-second.json"),
        "switched" => run.call("run-switched.json"), "restart" => run.call("run-restart.json")
      },
      "event_cursors" => {
        "first" => event_cursor.call("run-first-events.json"), "second" => event_cursor.call("run-second-events.json"),
        "switched" => event_cursor.call("run-switched-events.json"), "restart" => event_cursor.call("run-restart-events.json")
      },
      "secret_redaction" => {"api_keys_written" => false, "token_patterns_redacted" => true},
      "evidence_files" => files.map { |path| {"path" => File.basename(path), "sha256" => Digest::SHA256.file(path).hexdigest} }
    }
    File.write(File.join(dir, "manifest.json"), JSON.pretty_generate(report) + "\n")
  ' || true
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
[ "${ADRO_REQUIRE_CODEX:-0}" = "1" ] || fail "set ADRO_REQUIRE_CODEX=1 for this real suite"

executor="${ADRO_EXECUTOR:-}"
[ -n "$executor" ] || executor="$(select_real_codex || true)"
[ -n "$executor" ] || fail "Codex is required"
case "$(basename "$executor")" in
  codex|codex.exe) ;;
  *) fail "chat/compose suite requires Codex, got $(basename "$executor")" ;;
esac
executor="$(command -v "$executor" 2>/dev/null || printf '%s' "$executor")"
CODEX_COMMAND="$executor"
CODEX_VERSION="$($executor --version 2>&1 || true)"
[ -n "$CODEX_VERSION" ] || fail "Codex is not runnable"
prepare_real_codex_home "$RUN_ROOT/codex-home"
trust_real_codex_project "$RUN_ROOT/codex-home" "$STATE_HOME"
codex_wrapper="$RUN_ROOT/codex"
write_real_codex_wrapper "$codex_wrapper" "$executor"
executor="$codex_wrapper"
configure_real_codex_command "$executor"

export ADRO_HOME="$STATE_HOME" ADRO_API_PORT="$API_PORT" ADRO_WEB_PORT="$WEB_PORT"
export ADRO_AUTH_MODE=optional ADRO_EXECUTOR="$executor"
export ADRO_ARTIFACT_ROOT="$STATE_HOME/artifacts" ADRO_WORK_ROOT="$STATE_HOME/workspaces"
export ADRO_EXECUTOR_TIMEOUT="${ADRO_CHAT_REAL_EXECUTOR_TIMEOUT:-15m}"
export ADRO_CODEX_ATTEMPT_TIMEOUT="${ADRO_CHAT_REAL_CODEX_ATTEMPT_TIMEOUT:-120}"
export ADRO_CODEX_MAX_RETRIES="${ADRO_CHAT_REAL_CODEX_RETRIES:-2}"
export ADRO_CODEX_REQUIRE_TERMINAL=1

"$ROOT_DIR/start.sh" --no-open >"$START_LOG" 2>&1 || { redact_file "$START_LOG" /dev/stderr; fail "ADRO did not start"; }
curl -fsS "$API/readyz" >/dev/null || fail "ADRO is not ready"
curl -fsS "$API/api/v1/provider/diagnostics" >"$REPORT_DIR/diagnostics.json"
curl -fsS "$API/api/v1/runtimes/discovered" >"$REPORT_DIR/runtimes.json"
curl -fsS "$API/api/v1/runtimes/codex/models" >"$REPORT_DIR/codex-models.json"

headers=(-H 'X-Workspace-ID: local' -H 'Content-Type: application/json')
api_json() { curl -fsS "$@"; }

chat_json="$(api_json -X POST "$API/api/v1/chats" "${headers[@]}" -H 'Idempotency-Key: real-chat-create-1' -d '{"workspace_id":"local","title":"Real Codex continuity"}')"
printf '%s\n' "$chat_json" >"$REPORT_DIR/chat-created.json"
chat_id="$(printf '%s' "$chat_json" | json_field id)"
[ -n "$chat_id" ] || fail "chat id missing"

first_body='{"content":"Remember this exact fact for the next turns: the continuity token is ADRO-CHAT-2026. Use the terminal to run pwd, then finish with exactly ADRO_RESULT_JSON={\"outcome\":\"pass\",\"reason_code\":\"chat_first\",\"summary\":\"first durable chat turn completed\",\"evidence_ids\":[\"chat-first\"],\"fields\":{\"continuity_token\":\"ADRO-CHAT-2026\"}}."}'
first_json="$(api_json -X POST "$API/api/v1/chats/$chat_id/messages" "${headers[@]}" -H 'Idempotency-Key: real-chat-first' -d "$first_body")"
printf '%s\n' "$first_json" >"$REPORT_DIR/chat-first.json"
first_continuity="$(printf '%s' "$first_json" | json_field continuity 2>/dev/null || true)"
[ "$first_continuity" = "compiled_context" ] || fail "first chat turn did not use compiled ADRO context: $first_continuity"
first_run_id="$(printf '%s' "$first_json" | json_field run.id)"
[ -n "$first_run_id" ] || fail "first chat run missing"
capture_run first "$first_run_id"

second_body='{"content":"Continue the same conversation. What exact continuity token did I ask you to remember? Use the terminal to run pwd again, and finish with exactly ADRO_RESULT_JSON={\"outcome\":\"pass\",\"reason_code\":\"chat_native_followup\",\"summary\":\"native follow-up completed\",\"evidence_ids\":[\"chat-second\"],\"fields\":{\"continuity_token\":\"ADRO-CHAT-2026\"}}."}'
second_json="$(api_json -X POST "$API/api/v1/chats/$chat_id/messages" "${headers[@]}" -H 'Idempotency-Key: real-chat-second' -d "$second_body")"
printf '%s\n' "$second_json" >"$REPORT_DIR/chat-second.json"
second_continuity="$(printf '%s' "$second_json" | json_field continuity 2>/dev/null || true)"
[ "$second_continuity" = "native_session" ] || fail "native Codex continuation was not proven: $second_continuity"
second_run_id="$(printf '%s' "$second_json" | json_field run.id)"
[ -n "$second_run_id" ] || fail "second chat run missing"
capture_run second "$second_run_id"

switch_json="$(api_json -X PATCH "$API/api/v1/chats/$chat_id" "${headers[@]}" -H 'Idempotency-Key: real-chat-relay-switch' -d '{"model":"ccswitch-profile-2"}')"
printf '%s\n' "$switch_json" >"$REPORT_DIR/chat-switched-binding.json"
switched_json="$(api_json -X POST "$API/api/v1/chats/$chat_id/messages" "${headers[@]}" -H 'Idempotency-Key: real-chat-switched' -d '{"content":"A relay/profile was switched behind the conversation. Use the complete ADRO history and tell me the exact continuity token from the first turn. Use the terminal to run pwd, then finish with exactly ADRO_RESULT_JSON={\"outcome\":\"pass\",\"reason_code\":\"chat_compiled_after_switch\",\"summary\":\"compiled history survived provider binding change\",\"evidence_ids\":[\"chat-switched\"],\"fields\":{\"continuity_token\":\"ADRO-CHAT-2026\"}}."}')"
printf '%s\n' "$switched_json" >"$REPORT_DIR/chat-switched.json"
switched_continuity="$(printf '%s' "$switched_json" | json_field continuity 2>/dev/null || true)"
[ "$switched_continuity" = "compiled_context" ] || fail "provider binding switch did not force compiled context: $switched_continuity"
switched_run_id="$(printf '%s' "$switched_json" | json_field run.id)"
[ -n "$switched_run_id" ] || fail "switched chat run missing"
capture_run switched "$switched_run_id"

"$ROOT_DIR/start.sh" --stop --no-open >/dev/null
"$ROOT_DIR/start.sh" --no-open >>"$START_LOG" 2>&1 || { redact_file "$START_LOG" /dev/stderr; fail "ADRO restart failed"; }
curl -fsS "$API/readyz" >/dev/null
restart_json="$(api_json -X POST "$API/api/v1/chats/$chat_id/messages" "${headers[@]}" -H 'Idempotency-Key: real-chat-after-restart' -d '{"content":"The control plane restarted. Read the durable transcript, answer the original continuity-token question, use the terminal to run pwd, and finish with exactly ADRO_RESULT_JSON={\"outcome\":\"pass\",\"reason_code\":\"chat_restart_recovery\",\"summary\":\"chat recovered after restart\",\"evidence_ids\":[\"chat-restart\"],\"fields\":{\"continuity_token\":\"ADRO-CHAT-2026\"}}."}')"
printf '%s\n' "$restart_json" >"$REPORT_DIR/chat-restart.json"
restart_continuity="$(printf '%s' "$restart_json" | json_field continuity 2>/dev/null || true)"
case "$restart_continuity" in native_session|compiled_context) ;; *) fail "restart chat continuity is invalid: $restart_continuity" ;; esac
restart_run_id="$(printf '%s' "$restart_json" | json_field run.id)"
[ -n "$restart_run_id" ] || fail "restart chat run missing"
capture_run restart "$restart_run_id"

chat_read="$(api_json "$API/api/v1/chats/$chat_id" -H 'X-Workspace-ID: local')"
printf '%s\n' "$chat_read" >"$REPORT_DIR/chat-final.json"
printf '%s' "$chat_read" | CHAT_ID="$chat_id" ruby -rjson -e '
  value = JSON.parse(STDIN.read)
  abort("chat identity was not durable") unless value.dig("chat", "id") == ENV.fetch("CHAT_ID")
  messages = value.fetch("messages")
  abort("chat transcript is not durable") unless messages.length == 8 && messages.map { |item| item["role"] }.each_slice(2).all? { |pair| pair == %w[user assistant] }
  abort("first prompt was lost") unless messages.any? { |item| item["content"].include?("ADRO-CHAT-2026") }
  abort("harness transcript was not durable") unless value.dig("context", "transcript_durable") == true
'

compose_prompt='Create a release evidence reviewer Agent. It must inspect release evidence, identify missing proof, and explain its decision to a non-technical user. Keep the configuration practical and include two conversation starters. Return the required <agent_draft> JSON block exactly as instructed, then run pwd in the terminal, and finish with exactly ADRO_RESULT_JSON={"outcome":"pass","reason_code":"agent_compose_real","summary":"AI agent draft composed","evidence_ids":["agent-compose"],"fields":{"compose_real":true}}.'
compose_payload="$(PROMPT="$compose_prompt" ruby -rjson -e 'puts JSON.generate(prompt: ENV.fetch("PROMPT"), runtime_id: "local")')"
compose_response_file="$RUN_ROOT/agent-compose-response.json"
compose_status="$(curl -sS -o "$compose_response_file" -w '%{http_code}' -X POST "$API/api/v1/workspaces/local/agents/compose" "${headers[@]}" -H 'Idempotency-Key: real-agent-compose-1' -d "$compose_payload" || true)"
cp "$compose_response_file" "$REPORT_DIR/agent-compose.json"
if [ "$compose_status" != "200" ]; then
  compose_error="$(tr '\n' ' ' <"$compose_response_file" | sed -E 's/[[:space:]]+/ /g' | cut -c1-1200)"
  fail "real agent compose returned HTTP $compose_status: $compose_error"
fi
compose_json="$(cat "$compose_response_file")"
compose_run_id="$(printf '%s' "$compose_json" | json_field evidence.run_id)"
[ -n "$compose_run_id" ] || fail "real agent compose did not return a provider run"
capture_run compose "$compose_run_id"
compose_name="$(printf '%s' "$compose_json" | json_field draft.name)"
compose_description="$(printf '%s' "$compose_json" | json_field draft.description)"
compose_role="$(printf '%s' "$compose_json" | json_field draft.role)"
compose_instructions="$(printf '%s' "$compose_json" | json_field draft.instructions)"
[ -n "$compose_name" ] && [ -n "$compose_description" ] && [ -n "$compose_role" ] && [ -n "$compose_instructions" ] || fail "real agent compose returned an incomplete draft"
agent_payload="$(COMPOSE="$compose_json" ruby -rjson -e '
  response = JSON.parse(ENV.fetch("COMPOSE"))
  draft = response.fetch("draft")
  body = {
    workspace_id: "local", name: draft.fetch("name"), description: draft.fetch("description"), role: draft.fetch("role"),
    instructions: draft.fetch("instructions"), conversation_starters: draft.fetch("conversation_starters", []),
    access_policy: draft.fetch("access_policy", {"mode" => "private"}), skill_ids: draft.fetch("skill_ids", []),
    mcp_server_ids: draft.fetch("mcp_server_ids", []), status: "active", created_by: "real-compose-owner",
    capabilities: [{name: "session.start", version: "v1"}, {name: "stream.events", version: "v1"}],
    executor_binding: {provider_id: "local", runtime_id: "local", required_caps: ["run.snapshot.v1"], config_version: "real-compose-v1"},
    concurrency_budget: {tokens: draft.fetch("token_budget", 120000), tool_calls: draft.fetch("tool_call_budget", 200), concurrent: draft.fetch("max_concurrent_tasks", 1)},
    input_schema: {id: "adro.context-envelope", version: 1}, output_schema: {id: "adro.structured-result", version: 1},
    tool_policy: {network: draft.fetch("network_access", false)}, memory_policy: {require_evidence: true}
  }
  puts JSON.generate(body)
')"
agent_json="$(api_json -X POST "$API/api/v1/workspaces/local/agents" "${headers[@]}" -H 'X-Member-ID: real-compose-owner' -H 'Idempotency-Key: real-agent-create-1' -d "$agent_payload")"
printf '%s\n' "$agent_json" >"$REPORT_DIR/agent-created.json"
created_agent_id="$(printf '%s' "$agent_json" | json_field id)"
[ -n "$created_agent_id" ] || fail "real composed Agent was not created"
agent_read="$(api_json "$API/api/v1/workspaces/local/agents/$created_agent_id" -H 'X-Workspace-ID: local')"
printf '%s\n' "$agent_read" >"$REPORT_DIR/agent-final.json"
printf '%s' "$agent_read" | AGENT_ID="$created_agent_id" ruby -rjson -e '
  value = JSON.parse(STDIN.read)
  abort("created Agent identity mismatch") unless value["id"] == ENV.fetch("AGENT_ID")
  abort("created Agent is not active") unless value["status"] == "active"
  abort("created Agent lost instructions") unless value["instructions"].to_s.length > 20
  abort("created Agent lost runtime binding") unless value.dig("executor_binding", "runtime_id") == "local"
'

log "PASS: real Codex durable chat, native continuation, binding-switch fallback, restart recovery, AI Agent compose and one-click create completed"
