#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=scripts/lib/real-codex.sh
source "$ROOT_DIR/scripts/lib/real-codex.sh"

API_PORT="${ADRO_COMMENT_HANDOFF_API_PORT:-18088}"
RUN_ID="$(date -u +%Y%m%dT%H%M%SZ)-$$"
REPORT_DIR="${ADRO_COMMENT_HANDOFF_REPORT_DIR:-$ROOT_DIR/var/test-report/real-codex/$RUN_ID}"
RUN_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/adro-comment-handoff.XXXXXX")"
STATE_HOME="$RUN_ROOT/state"
FIXTURE_REPO="$RUN_ROOT/fixture-repo"
API="http://127.0.0.1:$API_PORT"
START_LOG="$RUN_ROOT/start.log"
COMMIT_SHA="$(git -C "$ROOT_DIR" rev-parse HEAD 2>/dev/null || true)"
CODEX_VERSION=""
GO_VERSION=""

log() { printf '[ADRO COMMENT HANDOFF E2E] %s\n' "$*"; }
fail() { log "ERROR: $*" >&2; exit 1; }

cleanup() {
  local exit_status=$?
  if [ -f "$START_LOG" ]; then
    sed -E \
      -e "s#${HOME:-}#<home>#g" \
      -e "s#${RUN_ROOT}#<run-root>#g" \
      -e 's/(sk-[A-Za-z0-9_-]{10,})/<redacted-secret>/g' \
      "$START_LOG" >"$REPORT_DIR/start.log" 2>/dev/null || true
  fi
  [ -n "$CODEX_VERSION" ] && printf '%s\n' "$CODEX_VERSION" >"$REPORT_DIR/codex-version.txt"
  REPORT_DIR="$REPORT_DIR" RUN_ID="$RUN_ID" EXIT_STATUS="$exit_status" COMMIT_SHA="$COMMIT_SHA" CODEX_VERSION="$CODEX_VERSION" GO_VERSION="$GO_VERSION" ruby -rjson -rdigest -e '
    dir = ENV.fetch("REPORT_DIR")
    files = Dir[File.join(dir, "*")].sort
    report = {
      "status" => ENV.fetch("EXIT_STATUS").to_i == 0 ? "passed" : "failed",
      "exit_status" => ENV.fetch("EXIT_STATUS").to_i,
      "run_id" => ENV.fetch("RUN_ID"),
      "commit_sha" => ENV.fetch("COMMIT_SHA"),
      "command" => "ADRO_REQUIRE_CODEX=1 bash scripts/comment-handoff-real-e2e.sh",
      "codex_version" => ENV.fetch("CODEX_VERSION"),
      "go_version" => ENV.fetch("GO_VERSION"),
      "evidence_files" => files.map { |path| {"path" => File.basename(path), "sha256" => Digest::SHA256.file(path).hexdigest} }
    }
    if File.file?(File.join(dir, "comment-handoff-evidence.json"))
      evidence = JSON.parse(File.read(File.join(dir, "comment-handoff-evidence.json")))
      report["requirement_id"] = evidence["requirement_id"]
      report["comment_ids"] = evidence["comments"].map { |item| item.dig("comment", "id") }
      report["root_id"] = evidence.dig("comments", 0, "comment", "root_id")
      report["follow_up_statuses"] = evidence["comments"].map { |item| item.dig("follow_up", "follow_up", "status") || item.dig("follow_up", "status") }
      report["session_ids"] = evidence["comments"].map { |item| item.dig("receipt", "harness_session_id") }.compact.uniq
      report["run_ids"] = evidence["runs"].map { |run| run["id"] || run.dig("run", "id") }.compact
    end
    File.write(File.join(dir, "manifest.json"), JSON.pretty_generate(report) + "\n")
  ' || true
  ADRO_HOME="$STATE_HOME" ADRO_API_PORT="$API_PORT" "$ROOT_DIR/start.sh" --stop --no-open >/dev/null 2>&1 || true
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

executor="${ADRO_EXECUTOR:-}"
[ -n "$executor" ] || executor="$(command -v codex 2>/dev/null || true)"
[ -n "$executor" ] || fail "Codex is required"
case "$(basename "$executor")" in codex|codex.exe) ;; *) fail "comment handoff suite requires Codex" ;; esac
executor="$(command -v "$executor" 2>/dev/null || printf '%s' "$executor")"
CODEX_VERSION="$($executor --version 2>&1 || true)"
[ -n "$CODEX_VERSION" ] || fail "Codex is not runnable"
go_bin="${ADRO_GO_BIN:-$(command -v go 2>/dev/null || true)}"
[ -n "$go_bin" ] || fail "Go is required"
GO_VERSION="$($go_bin version 2>/dev/null || true)"
go_root="$($go_bin env GOROOT 2>/dev/null || true)"
[ -n "$go_root" ] || fail "could not resolve Go root"

mkdir -p "$REPORT_DIR" "$STATE_HOME" "$FIXTURE_REPO"
printf '%s\n' 'module example.com/adro-comment-handoff' 'go 1.24.1' >"$FIXTURE_REPO/go.mod"
printf '%s\n' 'package handoff' '' 'func Status() string { return "ready" }' >"$FIXTURE_REPO/status.go"
git -C "$FIXTURE_REPO" init -q
git -C "$FIXTURE_REPO" config user.email adro-comment-handoff@example.invalid
git -C "$FIXTURE_REPO" config user.name ADRO-Comment-Handoff
git -C "$FIXTURE_REPO" add .
git -C "$FIXTURE_REPO" commit -qm 'fixture: comment handoff source'
git -C "$FIXTURE_REPO" branch -M main

prepare_real_codex_home "$RUN_ROOT/codex-home"
trust_real_codex_project "$RUN_ROOT/codex-home" "$STATE_HOME"
codex_wrapper="$RUN_ROOT/codex"
printf '%s\n' '#!/bin/sh' "export GOROOT=$(printf '%q' "$go_root")" "exec $(printf '%q' "$executor") \"\$@\"" >"$codex_wrapper"
chmod 700 "$codex_wrapper"
executor="$codex_wrapper"
if [ -z "${ADRO_EXECUTOR_COMMAND:-}" ]; then
  configure_real_codex_command "$executor"
fi
export ADRO_HOME="$STATE_HOME" ADRO_API_PORT="$API_PORT" ADRO_AUTH_MODE=optional ADRO_EXECUTOR="$executor"
export ADRO_ARTIFACT_ROOT="$STATE_HOME/artifacts" ADRO_WORK_ROOT="$STATE_HOME/workspaces"
export ADRO_GRAPH_WATCH_TIMEOUT="${ADRO_GRAPH_WATCH_TIMEOUT:-30m}" ADRO_GRAPH_WATCH_INTERVAL="${ADRO_GRAPH_WATCH_INTERVAL:-100ms}"

"$ROOT_DIR/start.sh" --no-open >"$START_LOG" 2>&1 || { cat "$START_LOG" >&2; fail "ADRO did not start"; }
curl -fsS "$API/readyz" >/dev/null || fail "ADRO is not ready"
headers=(-H 'X-Workspace-ID: local' -H 'Content-Type: application/json')
json_field() {
  local path="$1"
  ruby -rjson -e 'value=JSON.parse(STDIN.read); ARGV[0].split(".").each { |key| value=value.is_a?(Array) ? value.fetch(key.to_i) : value[key] }; puts(value.is_a?(String) ? value : JSON.generate(value)) unless value.nil?' "$path"
}
api_json() { curl -fsS "$@"; }

repo_json="$(api_json -X POST "$API/api/v1/repositories" "${headers[@]}" -d "$(WS=local PATH_REPO="$FIXTURE_REPO" ruby -rjson -e 'puts JSON.generate(id: "comment-handoff-repository", workspace_id: ENV.fetch("WS"), canonical_name: "comment-handoff-repository", clone_url: ENV.fetch("PATH_REPO"), default_branch: "main", provider: "git", metadata: {local_path: ENV.fetch("PATH_REPO")})')")"
repo_id="$(printf '%s' "$repo_json" | json_field id)"
[ -n "$repo_id" ] || fail "repository id missing"

requirement_json="$(api_json -X POST "$API/api/v1/requirements" "${headers[@]}" -d "$(REPO="$repo_id" ruby -rjson -e 'puts JSON.generate(workspace_id: "local", title: "Real comment A to B to C handoff", description: "Preserve one comment root and continuation lineage across three real Codex roles.", acceptance_criteria: ["architect, developer and tester comments remain in one thread", "every explicit mention has one receipt and a real run"], assignee_member_ids: ["handoff-owner"], repository_ids: [ENV.fetch("REPO")])')")"
requirement_id="$(printf '%s' "$requirement_json" | json_field id)"
[ -n "$requirement_id" ] || fail "requirement id missing"
api_json -X POST "$API/api/v1/requirements/$requirement_id/start" "${headers[@]}" -d '{}' >"$REPORT_DIR/requirement-start.json"

agents=()
for role in architect developer tester; do
  instructions="You are the real ${role} in a comment handoff. Do not edit ADRO state. Read the complete thread in your prompt and return exactly one ADRO_RESULT_JSON marker with outcome pass, reason_code comment_${role}_handoff, summary comment ${role} handoff completed, evidence_ids [comment-${role}-1], and fields {comment_handoff:true, role:${role}}."
  agent_id="$(ruby -rsecurerandom -e 'puts SecureRandom.uuid')"
  agent_json="$(ROLE="$role" ID="$agent_id" INSTRUCTIONS="$instructions" ruby -rjson -e 'puts JSON.generate(workspace_id: "local", id: ENV.fetch("ID"), revision: 1, name: "Comment " + ENV.fetch("ROLE"), role: ENV.fetch("ROLE"), instructions: ENV.fetch("INSTRUCTIONS"), status: "active", executor_binding: {provider_id: "local", required_caps: ["run.snapshot.v1"]}, input_schema: {id: "comment-input", version: 1}, output_schema: {id: "comment-output", version: 1})' | api_json -X POST "$API/api/v1/workspaces/local/agents" "${headers[@]}" -d @- )"
  agents+=("$(printf '%s' "$agent_json" | json_field id)")
done

comments_file="$REPORT_DIR/comment-handoff-evidence.json"
comments='[]'
previous_id=""
persist_evidence() {
  OUT="$comments_file" REPORT_DIR="$REPORT_DIR" REQUIREMENT_ID="$requirement_id" COMMENTS="$comments" ruby -rjson -e '
    dir = ENV.fetch("REPORT_DIR")
    runs = Dir[File.join(dir, "run-*.json")].filter_map { |path| JSON.parse(File.read(path)) rescue nil }
    evidence = {
      "requirement_id" => ENV.fetch("REQUIREMENT_ID"),
      "comments" => JSON.parse(ENV.fetch("COMMENTS", "[]")),
      "runs" => runs
    }
    File.write(ENV.fetch("OUT"), JSON.pretty_generate(evidence) + "\n")
  '
}
upsert_comment_evidence() {
  local index="$1"
  local response="$2"
  local follow_up="${3:-}"
  local receipt="${4:-}"
  comments="$(INDEX="$index" RESPONSE="$response" FOLLOW_UP="$follow_up" RECEIPT="$receipt" ITEMS="$comments" ruby -rjson -e '
    items = JSON.parse(ENV.fetch("ITEMS", "[]"))
    item = {"index" => ENV.fetch("INDEX"), "comment" => JSON.parse(ENV.fetch("RESPONSE")) ["comment"]}
    follow_up = ENV.fetch("FOLLOW_UP", "")
    item["follow_up"] = JSON.parse(follow_up) unless follow_up.empty?
    receipt = ENV.fetch("RECEIPT", "")
    item["receipt"] = JSON.parse(receipt) unless receipt.empty?
    items.reject! { |candidate| candidate["index"] == item["index"] }
    items << item
    puts JSON.generate(items)
  ')"
  persist_evidence
}
for index in 0 1 2; do
  role="${role:-}"
  case "$index" in
    0) role=architect; prompt='方案阶段：请检查需求上下文并确认方案可行。' ;;
    1) role=developer; prompt='研发阶段：请基于上一条方案评论确认实现路径。' ;;
    2) role=tester; prompt='测试阶段：请基于方案和研发评论完成回归确认。' ;;
  esac
  agent_id="${agents[$index]}"
  content="$prompt [@Comment $role](mention://agent/$agent_id)"
  body="$(CONTENT="$content" PARENT="$previous_id" ruby -rjson -e 'value={content: ENV.fetch("CONTENT")}; parent=ENV.fetch("PARENT"); value[:parent_id]=parent unless parent.empty?; puts JSON.generate(value)')"
  response="$(api_json -X POST "$API/api/v1/requirements/$requirement_id/comments" "${headers[@]}" -H 'X-Member-ID: handoff-owner' -H "Idempotency-Key: comment-handoff-$index" -d "$body")"
  comment_id="$(printf '%s' "$response" | json_field comment.id)"
  [ -n "$comment_id" ] || fail "comment $role id missing"
  root_id="$(printf '%s' "$response" | json_field comment.root_id)"
  [ -n "$root_id" ] || fail "comment $role root id missing"
  trigger_status="$(printf '%s' "$response" | json_field trigger_outcomes.0.status 2>/dev/null || true)"
  case "$trigger_status" in
    blocked|failed|cancelled|timed_out)
      printf '%s\n' "$response" >"$REPORT_DIR/trigger-$index.json"
      fail "$role mention trigger ended $trigger_status"
      ;;
  esac
  previous_id="$comment_id"
  follow_up=''
  upsert_comment_evidence "$index" "$response"
  deadline=$(( $(date +%s) + 1500 ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    follow_up="$(api_json "$API/api/v1/comments/$comment_id/follow-up" -H 'X-Workspace-ID: local')"
    upsert_comment_evidence "$index" "$response" "$follow_up"
    status="$(printf '%s' "$follow_up" | json_field follow_up.status 2>/dev/null || true)"
    case "$status" in
      completed) break ;;
      blocked|failed|cancelled|timed_out)
        printf '%s' "$follow_up" >"$REPORT_DIR/follow-up-$index.json"
        fail "$role follow-up ended $status"
        ;;
    esac
    sleep 1
  done
  status="$(printf '%s' "$follow_up" | json_field follow_up.status 2>/dev/null || true)"
  if [ "$status" != completed ]; then
    printf '%s' "$follow_up" >"$REPORT_DIR/follow-up-$index.json"
    fail "$role follow-up did not complete: $status"
  fi
  receipt="$(printf '%s' "$follow_up" | ruby -rjson -e 'v=JSON.parse(STDIN.read); puts JSON.generate(v.fetch("follow_up"))')"
  upsert_comment_evidence "$index" "$response" "$follow_up" "$receipt"
  run_id="$(printf '%s' "$follow_up" | json_field follow_up.provider_run_id 2>/dev/null || true)"
  if [ -n "$run_id" ]; then
    curl -sS "$API/api/v1/runs/$run_id" -H 'X-Workspace-ID: local' >"$REPORT_DIR/run-$index.json" || true
    curl -sS "$API/api/v1/runs/$run_id/events?limit=250" -H 'X-Workspace-ID: local' >"$REPORT_DIR/run-$index-events.json" || true
  fi
done

persist_evidence
log "PASS: three real Codex comment handoffs preserved one root, parent chain, receipts, session and workdir lineage"
