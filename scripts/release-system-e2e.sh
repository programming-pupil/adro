#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
API_PORT="${ADRO_SYSTEM_E2E_API_PORT:-18180}"
WEB_PORT="${ADRO_SYSTEM_E2E_WEB_PORT:-18181}"
WORKSPACE_A="adro-system-e2e-a"
WORKSPACE_B="adro-system-e2e-b"
RUN_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/adro-system-e2e.XXXXXX")"
STATE_HOME="$RUN_ROOT/state"
LOG="$RUN_ROOT/start.log"
API="http://127.0.0.1:$API_PORT"

log() { printf '[ADRO SYSTEM E2E] %s\n' "$*"; }
fail() { printf '[ADRO SYSTEM E2E] ERROR: %s\n' "$*" >&2; exit 1; }

cleanup() {
  ADRO_HOME="$STATE_HOME" ADRO_API_PORT="$API_PORT" ADRO_WEB_PORT="$WEB_PORT" \
    "$ROOT_DIR/start.sh" --stop --no-open >/dev/null 2>&1 || true
  if [ "${ADRO_E2E_KEEP:-0}" = "1" ]; then
    log "Evidence retained at $RUN_ROOT"
  else
    rm -rf "$RUN_ROOT"
  fi
}
trap cleanup EXIT

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

require_command() { command -v "$1" >/dev/null 2>&1 || fail "$1 is required"; }
require_command curl
require_command ruby
require_command go

executor="${ADRO_EXECUTOR:-}"
if [ -z "$executor" ]; then executor="$(command -v codex 2>/dev/null || true)"; fi
[ -n "$executor" ] || fail "Codex is required for the real system suite; install codex or set ADRO_EXECUTOR to a Codex executable"
case "$(basename "$executor")" in
  codex|codex.exe) ;;
  *) fail "real system suite requires Codex, got $(basename "$executor"); refusing to substitute another client" ;;
esac
"$executor" --version >/dev/null 2>&1 || fail "Codex executable is not runnable: $executor"
if [ -z "${ADRO_EXECUTOR_COMMAND:-}" ]; then
  export ADRO_EXECUTOR_COMMAND="$executor exec --json --skip-git-repo-check --dangerously-bypass-approvals-and-sandbox {input}"
fi

mkdir -p "$STATE_HOME"
export ADRO_HOME="$STATE_HOME" ADRO_API_PORT="$API_PORT" ADRO_WEB_PORT="$WEB_PORT"
export ADRO_AUTH_MODE=optional ADRO_EXECUTOR="$executor"
export ADRO_ARTIFACT_ROOT="$STATE_HOME/artifacts" ADRO_WORK_ROOT="$STATE_HOME/workspaces"
export ADRO_ALLOWED_ORIGINS="http://127.0.0.1:$WEB_PORT,http://localhost:$WEB_PORT"
"$ROOT_DIR/start.sh" --no-open >"$LOG" 2>&1 || { cat "$LOG" >&2; fail "local ADRO did not start"; }
curl -fsS "$API/readyz" >/dev/null || fail "readyz is not healthy"

headers_a=(-H "X-Workspace-ID: $WORKSPACE_A" -H 'Content-Type: application/json')
headers_b=(-H "X-Workspace-ID: $WORKSPACE_B" -H 'Content-Type: application/json')
api_json() { curl -fsS "$@"; }
status_code() { curl -sS -o "$RUN_ROOT/response.json" -w '%{http_code}' "$@"; }
expect_code() {
  local expected="$1"; shift
  local actual
  actual="$(status_code "$@")"
  [ "$actual" = "$expected" ] || { cat "$RUN_ROOT/response.json" >&2; fail "expected HTTP $expected, got $actual"; }
}

diagnostics="$(api_json "$API/api/v1/provider/diagnostics")"
printf '%s' "$diagnostics" | ruby -rjson -e 'v=JSON.parse(STDIN.read); abort("provider is not local") unless v["provider"] == "local"; abort("executor is not configured") unless v["configuration_state"] == "configured"'

repo_body="$(WS="$WORKSPACE_A" ruby -rjson -e 'puts JSON.generate(workspace_id: ENV.fetch("WS"), canonical_name: "system-e2e-service", clone_url: "https://example.invalid/system-e2e.git", default_branch: "main")')"
repo_json="$(api_json -X POST "$API/api/v1/repositories" "${headers_a[@]}" -d "$repo_body")"
repo_id="$(printf '%s' "$repo_json" | json_field id)"
[ -n "$repo_id" ] || fail "repository id missing"

chat_json="$(api_json -X POST "$API/api/v1/chats" "${headers_a[@]}" -d "$(WS="$WORKSPACE_A" PROJECT="$repo_id" ruby -rjson -e 'puts JSON.generate(workspace_id: ENV.fetch("WS"), project_id: ENV.fetch("PROJECT"), title: "Project support")')")"
chat_id="$(printf '%s' "$chat_json" | json_field id)"
[ -n "$chat_id" ] || fail "chat id missing"

printf '%s\n' 'chat attachment with project context' > "$RUN_ROOT/chat.txt"
attachment_json="$(curl -fsS -X POST "$API/api/v1/attachments" -H "X-Workspace-ID: $WORKSPACE_A" -H 'X-Member-ID: user-a' -F owner_type=chat_session -F owner_id="$chat_id" -F file=@"$RUN_ROOT/chat.txt;type=text/plain")"
attachment_id="$(printf '%s' "$attachment_json" | json_field id)"
[ -n "$attachment_id" ] || fail "chat attachment id missing"

chat_b_json="$(api_json -X POST "$API/api/v1/chats" "${headers_a[@]}" -d "$(WS="$WORKSPACE_A" ruby -rjson -e 'puts JSON.generate(workspace_id: ENV.fetch("WS"), project_id: "standalone", title: "Second conversation")')")"
chat_b_id="$(printf '%s' "$chat_b_json" | json_field id)"
[ -n "$chat_b_id" ] || fail "second chat id missing"
expect_code 422 -X POST "$API/api/v1/chats/$chat_b_id/messages" -H "X-Workspace-ID: $WORKSPACE_A" -H 'Content-Type: application/json' -d "$(ATTACHMENT="$attachment_id" ruby -rjson -e 'puts JSON.generate(content: "must reject a foreign attachment", attachment_ids: [ENV.fetch("ATTACHMENT")])')"

png_file="$RUN_ROOT/evidence.png"
printf '%s' 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=' | base64 -d > "$png_file"
screenshot_json="$(curl -fsS -X POST "$API/api/v1/screenshots" -H "X-Workspace-ID: $WORKSPACE_A" -F target_type=workspace -F target_id="$WORKSPACE_A" -F file=@"$png_file;type=image/png")"
printf '%s' "$screenshot_json" | ruby -rjson -e 'v=JSON.parse(STDIN.read); abort("screenshot was not stored") unless v["delivery"] == "delivered" && v["uri"].to_s.start_with?("artifact://")'
printf '%s\n' 'not an image' > "$RUN_ROOT/not-image.txt"
expect_code 415 -X POST "$API/api/v1/screenshots" -H "X-Workspace-ID: $WORKSPACE_A" -F file=@"$RUN_ROOT/not-image.txt;type=text/plain"

message_json="$(api_json -X POST "$API/api/v1/chats/$chat_id/messages" -H "X-Workspace-ID: $WORKSPACE_A" -H 'Content-Type: application/json' -H 'Idempotency-Key: chat-combination-1' -d "$(ATTACHMENT="$attachment_id" ruby -rjson -e 'puts JSON.generate(content: "Review the attached brief and screenshot in this project", attachment_ids: [ENV.fetch("ATTACHMENT")])')")"
printf '%s' "$message_json" | ATTACHMENT="$attachment_id" ruby -rjson -e 'v=JSON.parse(STDIN.read); abort("chat message missing attachment") unless v.dig("message", "attachment_ids").include?(ENV.fetch("ATTACHMENT"))'
chat_read="$(api_json "$API/api/v1/chats/$chat_id" -H "X-Workspace-ID: $WORKSPACE_A")"
printf '%s' "$chat_read" | ruby -rjson -e 'v=JSON.parse(STDIN.read); abort("chat transcript is not durable") unless v.dig("context", "transcript_durable") == true'

agent_ids=()
for role in designer developer tester; do
  agent_json="$(api_json -X POST "$API/api/v1/agents" "${headers_a[@]}" -d "$(WS="$WORKSPACE_A" MEMBER="member-$role" NAME="System ${role} agent" ROLE="$role" ruby -rjson -e 'puts JSON.generate(workspace_id: ENV.fetch("WS"), member_id: ENV.fetch("MEMBER"), name: ENV.fetch("NAME"), instructions: "Role: #{ENV.fetch("ROLE")}", role: ENV.fetch("ROLE"))')")"
  agent_ids+=("$(printf '%s' "$agent_json" | json_field profile.default_agent_binding_id)")
done
for id in "${agent_ids[@]}"; do [ -n "$id" ] || fail "agent binding missing"; done

template_json="$(api_json -X POST "$API/api/v1/workflow-templates" "${headers_a[@]}" -d "$(WS="$WORKSPACE_A" DESIGNER="${agent_ids[0]}" DEVELOPER="${agent_ids[1]}" TESTER="${agent_ids[2]}" ruby -rjson -e 'puts JSON.generate(workspace_id: ENV.fetch("WS"), name: "system-e2e-custom", mode: "automatic", steps: [{id: "design", stage: 1, agent_id: ENV.fetch("DESIGNER"), required: true}, {id: "develop", stage: 2, agent_id: ENV.fetch("DEVELOPER"), required: true}, {id: "test", stage: 3, agent_id: ENV.fetch("TESTER"), required: true}, {id: "report", stage: 7, agent_id: ENV.fetch("TESTER"), required: true}])')")"
template_id="$(printf '%s' "$template_json" | json_field id)"
[ -n "$template_id" ] || fail "custom workflow template id missing"
template_read="$(api_json "$API/api/v1/workflow-templates/$template_id" -H "X-Workspace-ID: $WORKSPACE_A")"
printf '%s' "$template_read" | DESIGNER="${agent_ids[0]}" DEVELOPER="${agent_ids[1]}" TESTER="${agent_ids[2]}" ruby -rjson -e '
  template = JSON.parse(STDIN.read)
  steps = template.fetch("steps")
  expected = {1 => ENV.fetch("DESIGNER"), 2 => ENV.fetch("DEVELOPER"), 3 => ENV.fetch("TESTER"), 7 => ENV.fetch("TESTER")}
  expected.each { |stage, agent| abort("workflow agent binding mismatch") unless steps.any? { |step| step["stage"] == stage && step["agent_id"] == agent } }
'

requirement_payload="$(WS="$WORKSPACE_A" TITLE="Concurrent requirement A" MEMBER=user-a REPO="$repo_id" TEMPLATE="$template_id" ruby -rjson -e 'puts JSON.generate(workspace_id: ENV.fetch("WS"), title: ENV.fetch("TITLE"), description: "compound concurrency requirement", acceptance_criteria: ["preserve project context", "retain evidence"], assignee_member_ids: [ENV.fetch("MEMBER")], repository_ids: [ENV.fetch("REPO")], workflow_template_id: ENV.fetch("TEMPLATE"))')"
req_b_payload="$(printf '%s' "$requirement_payload" | TITLE='Concurrent requirement B' ruby -rjson -e 'v=JSON.parse(STDIN.read); v["title"]=ENV.fetch("TITLE"); puts JSON.generate(v)')"
req_a_file="$RUN_ROOT/requirement-a.json"
req_b_file="$RUN_ROOT/requirement-b.json"
workspace_b_payload="$(WS="$WORKSPACE_B" ruby -rjson -e 'puts JSON.generate(workspace_id: ENV.fetch("WS"), title: "Workspace B", description: "isolation", acceptance_criteria: ["isolated"], assignee_member_ids: ["user-b"])')"
workspace_b_file="$RUN_ROOT/workspace-b.json"
curl -fsS -X POST "$API/api/v1/requirements" "${headers_a[@]}" -H 'Idempotency-Key: requirement-a' -d "$requirement_payload" > "$req_a_file" &
req_a_pid=$!
curl -fsS -X POST "$API/api/v1/requirements" "${headers_a[@]}" -H 'Idempotency-Key: requirement-b' -d "$req_b_payload" > "$req_b_file" &
req_b_pid=$!
curl -fsS -X POST "$API/api/v1/requirements" "${headers_b[@]}" -H 'Idempotency-Key: workspace-b' -d "$workspace_b_payload" > "$workspace_b_file" &
workspace_b_pid=$!
wait "$req_a_pid"
wait "$req_b_pid"
wait "$workspace_b_pid"
req_a="$(<"$req_a_file")"
req_b="$(<"$req_b_file")"
req_a_id="$(printf '%s' "$req_a" | json_field id)"
req_b_id="$(printf '%s' "$req_b" | json_field id)"
[ -n "$req_a_id" ] && [ -n "$req_b_id" ] && [ "$req_a_id" != "$req_b_id" ] || fail "same-user concurrent requirements collided"
printf '%s\n' 'requirement acceptance attachment' > "$RUN_ROOT/requirement.txt"
requirement_attachment="$(curl -fsS -X POST "$API/api/v1/attachments" -H "X-Workspace-ID: $WORKSPACE_A" -F owner_type=requirement -F owner_id="$req_a_id" -F file=@"$RUN_ROOT/requirement.txt;type=text/plain")"
requirement_attachment_id="$(printf '%s' "$requirement_attachment" | json_field id)"
[ -n "$requirement_attachment_id" ] || fail "requirement attachment id missing"
requirement_attachments="$(api_json "$API/api/v1/attachments?owner_type=requirement&owner_id=$req_a_id" -H "X-Workspace-ID: $WORKSPACE_A")"
printf '%s' "$requirement_attachments" | ATTACHMENT="$requirement_attachment_id" ruby -rjson -e 'v=JSON.parse(STDIN.read); abort("requirement attachment was not listed") unless v.fetch("items").any? { |item| item["id"] == ENV.fetch("ATTACHMENT") }'

req_b_replay="$(api_json -X POST "$API/api/v1/requirements" "${headers_a[@]}" -H 'Idempotency-Key: requirement-b' -d "$req_b_payload")"
[ "$(printf '%s' "$req_b_replay" | json_field id)" = "$req_b_id" ] || fail "idempotent requirement replay changed identity"
changed_payload="$(printf '%s' "$req_b_payload" | ruby -rjson -e 'v=JSON.parse(STDIN.read); v["title"]="changed body"; puts JSON.generate(v)')"
expect_code 409 -X POST "$API/api/v1/requirements" "${headers_a[@]}" -H 'Idempotency-Key: requirement-b' -d "$changed_payload"
expect_code 404 "$API/api/v1/chats/$chat_id" -H "X-Workspace-ID: $WORKSPACE_B"
expect_code 422 -X POST "$API/api/v1/requirements" "${headers_a[@]}" -d '{}'
expect_code 404 -X PUT "$API/api/v1/chats/$chat_id" -H "X-Workspace-ID: $WORKSPACE_A"

req_b_workspace="$(<"$workspace_b_file")"
req_b_workspace_id="$(printf '%s' "$req_b_workspace" | json_field id)"
[ -n "$req_b_workspace_id" ] || fail "workspace B requirement missing"
expect_code 404 "$API/api/v1/requirements/$req_b_workspace_id" -H "X-Workspace-ID: $WORKSPACE_A"

"$ROOT_DIR/start.sh" --stop --no-open >/dev/null
"$ROOT_DIR/start.sh" --no-open >>"$LOG" 2>&1 || { cat "$LOG" >&2; fail "restart failed"; }
curl -fsS "$API/readyz" >/dev/null
persisted="$(api_json "$API/api/v1/chats/$chat_id" -H "X-Workspace-ID: $WORKSPACE_A")"
printf '%s' "$persisted" | CHAT="$chat_id" ruby -rjson -e 'v=JSON.parse(STDIN.read); abort("chat was lost after restart") unless v.dig("chat", "id") == ENV.fetch("CHAT") && v["messages"].length == 1'

log "PASS: project chat + attachment + screenshot + custom agents/workflow + concurrent requirements + multi-workspace isolation + idempotency + errors + restart recovery"
log "Evidence: workspace_a=$WORKSPACE_A chat=$chat_id requirements=$req_a_id,$req_b_id template=$template_id"
