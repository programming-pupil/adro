#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
API_PORT="${ADRO_GRAPH_API_PORT:-18084}"
WEB_PORT="${ADRO_GRAPH_WEB_PORT:-18085}"
TIMEOUT_SECONDS="${ADRO_GRAPH_E2E_TIMEOUT:-1800}"
WORKSPACE="adro-real-graph-e2e"
RUN_ID="$(date -u +%Y%m%dT%H%M%SZ)-$$"
REPORT_DIR="$ROOT_DIR/var/test-report/real-codex/$RUN_ID"
RUN_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/adro-real-graph.XXXXXX")"
STATE_HOME="$RUN_ROOT/state"
FIXTURE_REPO="$RUN_ROOT/fixture-repo"
REPOSITORY_ID="real-graph-repository"
LOG="$RUN_ROOT/start.log"
API="http://127.0.0.1:$API_PORT"
mkdir -p "$REPORT_DIR"
COMMIT_SHA="$(git -C "$ROOT_DIR" rev-parse HEAD 2>/dev/null || true)"
GO_VERSION=""
CODEX_VERSION=""

log() { printf '[ADRO REAL GRAPH E2E] %s\n' "$*"; }
fail() { log "ERROR: $*" >&2; exit 1; }
json_field() {
  local path="$1"
  ruby -rjson -e '
    value = JSON.parse(STDIN.read)
    ARGV[0].split(".").each { |key| value = value.is_a?(Array) ? value.fetch(key.to_i) : value[key] }
    puts(value.is_a?(String) ? value : JSON.generate(value)) unless value.nil?
  ' "$path"
}
sha256_file() { shasum -a 256 "$1" | awk '{print $1}'; }

collect_provider_evidence() {
  local projection_file="$1"
  [ -s "$projection_file" ] || return 0
  : >"$REPORT_DIR/provider-runs.jsonl"
  : >"$REPORT_DIR/provider-events.jsonl"
  while IFS=$'\t' read -r attempt_id run_id; do
    [ -n "$run_id" ] || continue
    curl -fsS "$API/api/v1/runs/$run_id" >"$REPORT_DIR/run-$run_id.json" || true
    curl -fsS "$API/api/v1/runs/$run_id/events?limit=250" >"$REPORT_DIR/run-$run_id-events.json" || true
    [ -s "$REPORT_DIR/run-$run_id.json" ] && ruby -rjson -e 'File.open(ARGV.fetch(1), "a") { |f| f.puts JSON.generate(JSON.parse(File.read(ARGV.fetch(0)))) }' "$REPORT_DIR/run-$run_id.json" "$REPORT_DIR/provider-runs.jsonl" || true
    [ -s "$REPORT_DIR/run-$run_id-events.json" ] && ruby -rjson -e 'File.open(ARGV.fetch(1), "a") { |f| f.puts JSON.generate(JSON.parse(File.read(ARGV.fetch(0)))) }' "$REPORT_DIR/run-$run_id-events.json" "$REPORT_DIR/provider-events.jsonl" || true
  done < <(ruby -rjson -e 'JSON.parse(File.read(ARGV.fetch(0))).fetch("attempts").values.each { |a| puts [a["id"], a["run_id"]].join("\t") }' "$projection_file")
}

write_failure_manifest() {
  local exit_status="$1"
  [ -f "$REPORT_DIR/manifest.json" ] && return 0
  local projection_file="$REPORT_DIR/projection.json"
  if [ ! -s "$projection_file" ] && [ -s "$REPORT_DIR/timeline-last.json" ]; then
    ruby -rjson -e 'timeline = JSON.parse(File.read(ARGV.fetch(0))); File.write(ARGV.fetch(1), JSON.pretty_generate(timeline.fetch("projection")) + "\n")' "$REPORT_DIR/timeline-last.json" "$projection_file" || true
  fi
  collect_provider_evidence "$projection_file"
  local status="failed"
  [ "$exit_status" -eq 130 ] && status="interrupted"
  STATUS="$status" EXIT_STATUS="$exit_status" REPORT_DIR="$REPORT_DIR" COMMIT_SHA="$COMMIT_SHA" CODEX_VERSION="$CODEX_VERSION" GO_VERSION="$GO_VERSION" ruby -rjson -rdigest -e '
    dir = ENV.fetch("REPORT_DIR")
    files = Dir[File.join(dir, "*")].sort
    hash = ->(path) { Digest::SHA256.file(path).hexdigest }
    projection_path = File.join(dir, "projection.json")
    artifact_paths = files.reject { |f| File.basename(f) == "manifest.json" }
    report = {
      status: ENV.fetch("STATUS"), exit_status: ENV.fetch("EXIT_STATUS").to_i,
      run_id: File.basename(dir), commit_sha: ENV.fetch("COMMIT_SHA"), command: "ADRO_REQUIRE_CODEX=1 bash scripts/real-graph-orchestration-e2e.sh", codex_version: ENV.fetch("CODEX_VERSION"), go_version: ENV.fetch("GO_VERSION"), plan_id: nil, graph_id: nil, revision: nil,
      session_id: File.file?(File.join(dir, "session.json")) ? JSON.parse(File.read(File.join(dir, "session.json")))["id"] : nil,
      work_item_id: "real-graph-work-item",
      event_cursor: nil, projection_hash: File.file?(File.join(dir, "projection.json")) ? hash.call(File.join(dir, "projection.json")) : nil,
      projection_canonical_hash: File.file?(projection_path) ? Digest::SHA256.hexdigest(JSON.generate(JSON.parse(File.read(projection_path)))) : nil,
      artifact_hash: Digest::SHA256.hexdigest(artifact_paths.map { |f| File.basename(f) + ":" + hash.call(f) }.sort.join("\n")),
      timeline_hash: File.file?(File.join(dir, "timeline-last.json")) ? hash.call(File.join(dir, "timeline-last.json")) : nil,
      evidence_files: files.map { |f| {path: File.basename(f), sha256: hash.call(f)} },
      assertions: {"archived_on_failure" => true}
    }
    if File.file?(File.join(dir, "plan.json"))
      plan = JSON.parse(File.read(File.join(dir, "plan.json")))
      report[:plan_id] = plan["id"]
      report[:graph_id] = plan.dig("graph_snapshot", "id")
      report[:revision] = plan["revision"]
    end
    if File.file?(File.join(dir, "timeline-last.json"))
      timeline = JSON.parse(File.read(File.join(dir, "timeline-last.json")))
      report[:event_cursor] = timeline["cursor"]
    end
    File.write(File.join(dir, "manifest.json"), JSON.pretty_generate(report) + "\n")
  '
}

cleanup() {
  local exit_status=$?
  if [ -f "$LOG" ]; then
    # Keep uploaded diagnostics useful without exposing local paths or
    # token-shaped values from the provider process.
    sed -E \
      -e "s#${HOME:-}#<home>#g" \
      -e "s#${RUN_ROOT}#<run-root>#g" \
      -e 's/(sk-[A-Za-z0-9_-]{10,})/<redacted-secret>/g' \
      "$LOG" >"$REPORT_DIR/start.log" 2>/dev/null || true
  fi
  write_failure_manifest "$exit_status" || true
  ADRO_HOME="$STATE_HOME" ADRO_API_PORT="$API_PORT" ADRO_WEB_PORT="$WEB_PORT" "$ROOT_DIR/start.sh" --stop --no-open >/dev/null 2>&1 || true
  [ "${ADRO_E2E_KEEP:-0}" = "1" ] || rm -rf "$RUN_ROOT" 2>/dev/null || true
}
trap cleanup EXIT
trap 'exit 130' INT TERM HUP

command -v curl >/dev/null 2>&1 || fail "curl is required"
command -v ruby >/dev/null 2>&1 || fail "ruby is required"
command -v shasum >/dev/null 2>&1 || fail "shasum is required"
executor="${ADRO_EXECUTOR:-}"
[ -n "$executor" ] || executor="$(command -v codex 2>/dev/null || true)"
[ -n "$executor" ] || fail "Codex is required; install codex or set ADRO_EXECUTOR"
case "$(basename "$executor")" in codex|codex.exe) ;; *) fail "real graph suite requires Codex" ;; esac
executor="$(command -v "$executor" 2>/dev/null || printf '%s' "$executor")"
CODEX_VERSION="$("$executor" --version 2>&1 || true)"
GO_VERSION="$(go version 2>/dev/null || true)"
printf '%s\n' "$CODEX_VERSION" >"$REPORT_DIR/codex-version.txt"

SOURCE_CODEX_HOME="${CODEX_HOME:-}"
CODEX_RUN_HOME="$RUN_ROOT/codex-home"
mkdir -p "$CODEX_RUN_HOME"
if [ -f "$SOURCE_CODEX_HOME/auth.json" ]; then ln -s "$SOURCE_CODEX_HOME/auth.json" "$CODEX_RUN_HOME/auth.json"; fi
if [ -f "$SOURCE_CODEX_HOME/config.toml" ]; then install -m 600 "$SOURCE_CODEX_HOME/config.toml" "$CODEX_RUN_HOME/config.toml"; fi
if [ ! -f "$CODEX_RUN_HOME/auth.json" ] && [ -f "${HOME:-}/.codex/auth.json" ]; then ln -s "${HOME}/.codex/auth.json" "$CODEX_RUN_HOME/auth.json"; fi
if [ ! -f "$CODEX_RUN_HOME/config.toml" ] && [ -f "${HOME:-}/.codex/config.toml" ]; then install -m 600 "${HOME}/.codex/config.toml" "$CODEX_RUN_HOME/config.toml"; fi
export CODEX_HOME="$CODEX_RUN_HOME"
unset CODEX_SESSION_ID CODEX_THREAD_ID CODEX_CI
if [ -z "${ADRO_EXECUTOR_COMMAND:-}" ]; then
  # The parent coding runtime may intentionally use an OpenAI-compatible relay
  # in its config.toml. Real release evidence must be able to select the
  # operator's authenticated default Codex endpoint without inheriting that
  # relay accidentally. The flag only skips config.toml; auth.json remains
  # isolated and is still required for the real provider call.
  codex_config_flag=""
  if [ "${ADRO_CODEX_IGNORE_USER_CONFIG:-0}" = "1" ]; then
    codex_config_flag="--ignore-user-config"
  fi
  export ADRO_EXECUTOR_COMMAND="$executor exec $codex_config_flag --json --skip-git-repo-check --dangerously-bypass-approvals-and-sandbox {input}"
fi
export ADRO_HOME="$STATE_HOME" ADRO_API_PORT="$API_PORT" ADRO_WEB_PORT="$WEB_PORT" ADRO_EXECUTOR="$executor" ADRO_AUTH_MODE=optional
# Keep the child-process deadline inside the suite deadline. A real Codex
# invocation can spend its final seconds reconnecting after an upstream
# failure; without an executor deadline the script exits while the durable
# provider snapshot still says `running`, which loses the terminal failure
# evidence that the graph worker is supposed to reconcile.
if [ -z "${ADRO_EXECUTOR_TIMEOUT:-}" ]; then
  # Leave enough wall-clock budget for the graph worker to reconcile the last
  # provider attempt after a transient failure.
  executor_timeout=$((TIMEOUT_SECONDS / 4))
  [ "$executor_timeout" -lt 30 ] && executor_timeout=30
  [ "$executor_timeout" -gt 120 ] && executor_timeout=120
  export ADRO_EXECUTOR_TIMEOUT="${executor_timeout}s"
fi
# Give the server-side watcher a full grace window after the client-side poll
# deadline. This lets Worker.closeOnContextCancellation commit timeout evidence
# instead of racing the script's cleanup while the latest provider attempt is
# still running.
WATCH_TIMEOUT_SECONDS=$((TIMEOUT_SECONDS + 120))
export ADRO_GRAPH_WATCH_TIMEOUT="${ADRO_GRAPH_WATCH_TIMEOUT:-${WATCH_TIMEOUT_SECONDS}s}"
export ADRO_GRAPH_WATCH_INTERVAL="${ADRO_GRAPH_WATCH_INTERVAL:-100ms}"

"$ROOT_DIR/start.sh" --no-open >"$LOG" 2>&1 || { cat "$LOG" >&2; fail "ADRO did not start"; }
curl -fsS "$API/readyz" >"$REPORT_DIR/readyz.json" || fail "ADRO is not ready"
headers=(-H "X-Workspace-ID: $WORKSPACE" -H 'Content-Type: application/json')

# This is a real repository, not a fixture response. Every graph attempt must
# use the same cloned workdir so source changes, failing tests, repair patches,
# and final verification can be hashed from the provider's durable provenance.
mkdir -p "$FIXTURE_REPO"
printf '%s\n' 'module example.com/adro-real-graph' 'go 1.24.1' >"$FIXTURE_REPO/go.mod"
printf '%s\n' 'package calculator' '' 'func Add(a, b int) int {' '    return a + b' '}' >"$FIXTURE_REPO/calculator.go"
printf '%s\n' 'package calculator' '' 'import "testing"' '' 'func TestAdd(t *testing.T) {' '    if got := Add(2, 3); got != 5 {' '        t.Fatalf("Add(2, 3) = %d", got)' '    }' '}' >"$FIXTURE_REPO/calculator_test.go"
git -C "$FIXTURE_REPO" init -q
git -C "$FIXTURE_REPO" config user.email adro-real-graph@example.invalid
git -C "$FIXTURE_REPO" config user.name ADRO-Real-Graph
git -C "$FIXTURE_REPO" add .
git -C "$FIXTURE_REPO" commit -qm 'fixture: initial passing calculator'
git -C "$FIXTURE_REPO" branch -M main

repo_body="$(REPOSITORY_ID="$REPOSITORY_ID" WORKSPACE="$WORKSPACE" FIXTURE_REPO="$FIXTURE_REPO" ruby -rjson -e 'puts JSON.generate(id: ENV.fetch("REPOSITORY_ID"), workspace_id: ENV.fetch("WORKSPACE"), canonical_name: "real-graph-repository", clone_url: ENV.fetch("FIXTURE_REPO"), provider: "git", default_branch: "main", metadata: {local_path: ENV.fetch("FIXTURE_REPO")})')"
curl -fsS -X POST "$API/api/v1/repositories" "${headers[@]}" -d "$repo_body" >"$REPORT_DIR/repository.json"

create_requirement() {
  local body
  body="$(WORKSPACE="$WORKSPACE" REPOSITORY_ID="$REPOSITORY_ID" ruby -rjson -e 'puts JSON.generate(workspace_id: ENV.fetch("WORKSPACE"), title: "Real graph source acceptance", description: "Use the real checkout to implement, fail unit tests, repair the source, inject a QA regression, repair again, and revalidate.", acceptance_criteria: ["source is changed in the provider workdir", "unit failure and QA bug are observed", "final unit and QA verification pass"], assignee_member_ids: ["real-graph-e2e"], repository_ids: [ENV.fetch("REPOSITORY_ID")])')"
  curl -fsS -X POST "$API/api/v1/requirements" "${headers[@]}" -d "$body"
}

requirement_json="$(create_requirement)"
printf '%s' "$requirement_json" >"$REPORT_DIR/requirement.json"
requirement_id="$(printf '%s' "$requirement_json" | json_field id)"
curl -fsS -X POST "$API/api/v1/requirements/$requirement_id/start" "${headers[@]}" -d '{}' >"$REPORT_DIR/requirement-start.json"
work_items_json="$(curl -fsS "$API/api/v1/requirements/$requirement_id/work-items" -H "X-Workspace-ID: $WORKSPACE")"
printf '%s' "$work_items_json" >"$REPORT_DIR/work-items.json"
work_item_id="$(printf '%s' "$work_items_json" | json_field items.0.id)"
[ -n "$work_item_id" ] || fail "requirement did not materialize a repository work item"

create_agent() {
  local id="$1" name="$2" role="$3" instructions="$4"
  local body
  body="$(ID="$id" NAME="$name" ROLE="$role" INSTRUCTIONS="$instructions" WORKSPACE="$WORKSPACE" ruby -rjson -e 'puts JSON.generate(id: ENV.fetch("ID"), workspace_id: ENV.fetch("WORKSPACE"), revision: 1, name: ENV.fetch("NAME"), role: ENV.fetch("ROLE"), instructions: ENV.fetch("INSTRUCTIONS"), status: "active", executor_binding: {provider_id: "local", required_caps: ["run.snapshot.v1"]}, input_schema: {id: "graph-input", version: 1}, output_schema: {id: "graph-output", version: 1})')"
  curl -fsS -X POST "$API/api/v1/workspaces/$WORKSPACE/agents" "${headers[@]}" -d "$body"
}
create_agent architect 'Graph Architect' architect 'In the checkout, inspect the small Go calculator and write a plan in your response only. Do not modify files and do not call tools after inspection. Finish with exactly one ADRO_RESULT_JSON marker: outcome pass, reason_code architect_plan, summary architect plan recorded, evidence_ids [architect-plan-1], fields {}.' >"$REPORT_DIR/agent-architect.json"
create_agent developer 'Graph Developer' developer 'Read the ADRO_GRAPH_NODE_JSON line in your prompt to determine attempt_no. Work in the checkout. On attempt_no 1, implement the requested Add function but intentionally leave one reproducible defect by changing Add to return a-b; do not hide the defect and finish with outcome pass only after the source is changed. On attempt_no 2 or later, inspect the current source, fix Add to return a+b, run go test ./..., and finish with outcome pass only when it exits zero. Always include a concise summary and evidence_ids. Do not edit ADRO state outside the checkout.' >"$REPORT_DIR/agent-developer.json"
create_agent unit 'Graph Unit Gate' unit 'Run go test ./... in the checkout and report the real exit status. If the test command fails, finish with exactly one ADRO_RESULT_JSON marker using outcome failure, reason_code unit_failure_injected, summary containing the observed failure, evidence_ids [unit-failure-1], and fields including the command and exit status. If it passes, finish with outcome pass, reason_code unit_reverified, evidence_ids [unit-pass-2], and fields including the command and exit status. Do not edit source files.' >"$REPORT_DIR/agent-unit.json"
create_agent qa 'Graph QA Gate' qa 'Read the ADRO_GRAPH_NODE_JSON line in your prompt. On attempt_no 1, run go test ./... and then inject a real QA regression by changing Add to return a-b in the checkout; finish with outcome bug, reason_code qa_bug_injected, evidence_ids [qa-bug-1], and fields recording both commands. On later attempts, run go test ./... without changing source and finish with outcome pass, reason_code qa_reverified, evidence_ids [qa-pass-2], and fields including the command and exit status. Do not edit ADRO state outside the checkout.' >"$REPORT_DIR/agent-qa.json"

session_body="$(WORKSPACE="$WORKSPACE" ruby -rjson -e 'puts JSON.generate(workspace_id: ENV.fetch("WORKSPACE"), budget_tokens: 32768, auto_compaction: false)')"
session_json="$(curl -fsS -X POST "$API/api/v1/sessions" "${headers[@]}" -d "$session_body")"
printf '%s' "$session_json" >"$REPORT_DIR/session.json"
session_id="$(printf '%s' "$session_json" | json_field id)"
turn_body="$(ruby -rjson -e 'puts JSON.generate(role: "user", content: "Graph acceptance: architect -> developer -> unit failure -> developer repair -> unit pass -> QA bug -> developer repair -> unit and QA re-verification. Preserve immutable attempt lineage, feedback decisions, bounded loops, leases, idempotency, stream replay, and evidence hashes. Node instructions define the injected outcomes.", idempotency_key: "real-graph-objective")')"
curl -fsS -X POST "$API/api/v1/sessions/$session_id/turns" "${headers[@]}" -d "$turn_body" >"$REPORT_DIR/objective-turn.json"
compile_json="$(curl -fsS "$API/api/v1/sessions/$session_id/context/compile?max_tokens=8192" -H "X-Workspace-ID: $WORKSPACE")"
printf '%s' "$compile_json" >"$REPORT_DIR/context-compile.json"
envelope_json="$(printf '%s' "$compile_json" | json_field envelope)"
[ -n "$envelope_json" ] || fail "context compiler did not return envelope"

graph_json="$(RUN_ID="$RUN_ID" ruby -rjson -e '
  node = ->(id, agent) { {id: id, kind: "agent", agent_ref: {id: agent, revision: 1}, retry_policy: {max_attempts: 3}, budget: {tokens: 24000, duration: 900000000000}} }
  nodes = [node.call("architect", "architect"), node.call("developer", "developer"), node.call("unit", "unit"), node.call("qa", "qa"), {id: "repair", kind: "repair", repair_policy: {target_node_id: "developer", verification_node_ids: ["unit", "qa"], max_rounds: 2}}]
  edges = [
    {id: "architect-success", from: "architect", to: "developer", on: "success", max_traversals: 1},
    {id: "developer-success", from: "developer", to: "unit", on: "success", max_traversals: 4},
    {id: "unit-failure-feedback", from: "unit", to: "repair", on: "failure", max_traversals: 2},
    {id: "repair-developer", from: "repair", to: "developer", on: "success", max_traversals: 2},
    {id: "unit-success-qa", from: "unit", to: "qa", on: "success", max_traversals: 2},
    {id: "qa-bug-feedback", from: "qa", to: "repair", on: "bug", max_traversals: 2}
  ]
  puts JSON.generate(id: "real-graph-#{ENV.fetch("RUN_ID")}", version: 1, entry_node_ids: ["architect"], exit_node_ids: ["qa"], nodes: nodes, edges: edges)
')"
plan_body="$(GRAPH_JSON="$graph_json" ruby -rjson -e 'puts JSON.generate(graph: JSON.parse(ENV.fetch("GRAPH_JSON")), idempotency_key: "real-graph-plan")')"
plan_status="$(curl -sS -o "$REPORT_DIR/plan-response.json" -w '%{http_code}' -X POST "$API/api/v1/requirements/$requirement_id/execution-plan" "${headers[@]}" -d "$plan_body")"
case "$plan_status" in
  2*) ;;
  *) fail "graph plan creation failed with HTTP $plan_status: $(tr '\n' ' ' < "$REPORT_DIR/plan-response.json")" ;;
esac
plan_json="$(< "$REPORT_DIR/plan-response.json")"
printf '%s' "$plan_json" >"$REPORT_DIR/plan.json"
plan_id="$(printf '%s' "$plan_json" | json_field id)"
graph_id="$(printf '%s' "$plan_json" | json_field graph_snapshot.id)"
revision="$(printf '%s' "$plan_json" | json_field revision)"

tick_body="$(ENVELOPE_JSON="$envelope_json" WORK_ITEM_ID="$work_item_id" ruby -rjson -e 'puts JSON.generate(context_envelope: JSON.parse(ENV.fetch("ENVELOPE_JSON")), work_item_id: ENV.fetch("WORK_ITEM_ID"))')"
tick_status="$(curl -sS -o "$REPORT_DIR/initial-tick.json" -w '%{http_code}' -X POST "$API/api/v1/execution-plans/$plan_id/tick" "${headers[@]}" -d "$tick_body")"
case "$tick_status" in
  2*) ;;
  *) fail "graph initial tick failed with HTTP $tick_status: $(tr '\n' ' ' < "$REPORT_DIR/initial-tick.json")" ;;
esac

deadline=$((SECONDS + WATCH_TIMEOUT_SECONDS + 10))
timeline=""
while [ "$SECONDS" -lt "$deadline" ]; do
  timeline="$(curl -fsS "$API/api/v1/execution-plans/$plan_id/timeline" -H "X-Workspace-ID: $WORKSPACE")"
  printf '%s' "$timeline" >"$REPORT_DIR/timeline-last.json"
  status="$(printf '%s' "$timeline" | json_field projection.status 2>/dev/null || true)"
  [ "$status" = terminal ] && break
  sleep 2
done
[ "$SECONDS" -lt "$deadline" ] || {
  [ -f "$REPORT_DIR/timeline-last.json" ] && cp "$REPORT_DIR/timeline-last.json" "$REPORT_DIR/timeline.json"
  fail "graph did not reach terminal state"
}
printf '%s' "$timeline" >"$REPORT_DIR/timeline.json"
replay_status="$(curl -sS -o "$REPORT_DIR/replay.json" -w '%{http_code}' "$API/api/v1/execution-plans/$plan_id/replay" -H "X-Workspace-ID: $WORKSPACE" || true)"
printf '%s\n' "$replay_status" >"$REPORT_DIR/replay-status.txt"
projection_json="$(printf '%s' "$timeline" | json_field projection)"
printf '%s' "$projection_json" >"$REPORT_DIR/projection.json"

validation_status=0
ruby -rjson -e '
  projection = JSON.parse(File.read(ARGV.fetch(0)))
  attempts = projection.fetch("attempts").values
  counts = Hash.new(0)
  attempts.each { |a| counts[a.fetch("node_id")] += 1 }
  abort("attempt ids are not immutable") unless attempts.map { |a| a.fetch("id") }.uniq.length == attempts.length
  attempts.each { |a| abort("missing attempt provenance") if [a["run_id"], a["session_id"], a["workdir"]].any? { |v| v.to_s.empty? } }
  decisions = projection.fetch("decisions", [])
  terminal = projection["terminal_outcome"]
  if terminal == "succeeded"
    {"architect" => 1, "developer" => 3, "unit" => 2, "qa" => 2, "repair" => 2}.each { |node, n| abort("#{node} attempts=#{counts[node]} expected_at_least=#{n}") unless counts[node] >= n }
    abort("feedback edge missing") unless decisions.any? { |d| d["edge_id"] == "unit-failure-feedback" } && decisions.any? { |d| d["edge_id"] == "qa-bug-feedback" } && decisions.any? { |d| d["edge_id"] == "repair-developer" }
    repairs = projection.fetch("repair_plans", {}).values
    abort("repair plan missing") unless repairs.length == 1
    repair = repairs.fetch(0)
    abort("repair lifecycle did not verify") unless repair["state"] == "verified"
    expected_states = %w[planned dispatched patched verifying verified]
    abort("repair lifecycle history incomplete: #{repair["state_history"].inspect}") unless expected_states.all? { |state| repair.fetch("state_history", []).include?(state) }
  end
  traversals = projection.fetch("traversals", {})
  abort("loop bound exceeded") if traversals.fetch("unit-failure-feedback", 0) > 2 || traversals.fetch("qa-bug-feedback", 0) > 2 || traversals.fetch("repair-developer", 0) > 2
  File.write(ARGV.fetch(1), JSON.pretty_generate(counts: counts, terminal_outcome: terminal, attempt_count: attempts.length, decision_count: decisions.length, traversals: traversals) + "\n")
' "$REPORT_DIR/projection.json" "$REPORT_DIR/lineage-validation.json" || validation_status=$?

: >"$REPORT_DIR/provider-runs.jsonl"
: >"$REPORT_DIR/provider-events.jsonl"
while IFS=$'\t' read -r attempt_id run_id; do
  [ -n "$run_id" ] || continue
  # Provider run diagnostics are already scoped by the attempt IDs in the
  # projection. Do not send a workspace header here: the local evidence-only
  # work item is intentionally not a persisted legacy Store work item, and the
  # run route would otherwise reject an otherwise valid provider snapshot.
  curl -fsS "$API/api/v1/runs/$run_id" >"$REPORT_DIR/run-$run_id.json" || true
  curl -fsS "$API/api/v1/runs/$run_id/events?limit=250" >"$REPORT_DIR/run-$run_id-events.json" || true
  [ -s "$REPORT_DIR/run-$run_id.json" ] && ruby -rjson -e 'File.open(ARGV.fetch(1), "a") { |f| f.puts JSON.generate(JSON.parse(File.read(ARGV.fetch(0)))) }' "$REPORT_DIR/run-$run_id.json" "$REPORT_DIR/provider-runs.jsonl" || true
  [ -s "$REPORT_DIR/run-$run_id-events.json" ] && ruby -rjson -e 'File.open(ARGV.fetch(1), "a") { |f| f.puts JSON.generate(JSON.parse(File.read(ARGV.fetch(0)))) }' "$REPORT_DIR/run-$run_id-events.json" "$REPORT_DIR/provider-events.jsonl" || true
done < <(ruby -rjson -e 'JSON.parse(File.read(ARGV.fetch(0))).fetch("attempts").values.each { |a| puts [a["id"], a["run_id"]].join("\t") }' "$REPORT_DIR/projection.json")

projection_hash="$(sha256_file "$REPORT_DIR/projection.json")"
projection_canonical_hash="$(ruby -rjson -rdigest -e 'puts Digest::SHA256.hexdigest(JSON.generate(JSON.parse(File.read(ARGV.fetch(0)))))' "$REPORT_DIR/projection.json")"
replay_projection_hash=""
replay_match="false"
if [ "$replay_status" = "200" ] && [ -s "$REPORT_DIR/replay.json" ]; then
  replay_projection_hash="$(ruby -rjson -rdigest -e 'puts Digest::SHA256.hexdigest(JSON.generate(JSON.parse(File.read(ARGV.fetch(0))).fetch("projection")))' "$REPORT_DIR/replay.json")" || true
  [ "$replay_projection_hash" = "$projection_canonical_hash" ] && replay_match="true"
fi
# Capture the provider/server log before the manifest is materialized so a
# terminal validation failure also includes the final diagnostic evidence.
if [ -f "$LOG" ]; then
  sed -E \
    -e "s#${HOME:-}#<home>#g" \
    -e "s#${RUN_ROOT}#<run-root>#g" \
    -e 's/(sk-[A-Za-z0-9_-]{10,})/<redacted-secret>/g' \
    "$LOG" >"$REPORT_DIR/start.log" 2>/dev/null || true
fi
artifact_hash="$(ruby -rdigest -e 'paths = Dir[File.join(ARGV.fetch(0), "*")].select { |f| File.file?(f) && File.basename(f) != "manifest.json" }.sort; puts Digest::SHA256.hexdigest(paths.map { |f| File.basename(f) + ":" + Digest::SHA256.file(f).hexdigest }.join("\n"))' "$REPORT_DIR")"
event_cursor="$(printf '%s' "$timeline" | json_field cursor)"
timeline_hash="$(sha256_file "$REPORT_DIR/timeline.json")"

result="pass"
[ "$validation_status" -eq 0 ] || result="failed"
[ "$(printf '%s' "$projection_json" | json_field terminal_outcome)" = succeeded ] || result="failed"
[ "$replay_status" = "200" ] || result="failed"
[ "$replay_match" = "true" ] || result="failed"
RESULT="$result" VALIDATION_STATUS="$validation_status" PROJECTION_CANONICAL_HASH="$projection_canonical_hash" REPLAY_MATCH="$replay_match" RUN_ID="$RUN_ID" PLAN_ID="$plan_id" GRAPH_ID="$graph_id" REVISION="$revision" REQUIREMENT_ID="$requirement_id" SESSION_ID="$session_id" EVENT_CURSOR="$event_cursor" PROJECTION_HASH="$projection_hash" REPLAY_PROJECTION_HASH="$replay_projection_hash" ARTIFACT_HASH="$artifact_hash" TIMELINE_HASH="$timeline_hash" COMMIT_SHA="$COMMIT_SHA" CODEX_VERSION="$CODEX_VERSION" GO_VERSION="$GO_VERSION" REPORT_DIR="$REPORT_DIR" ruby -rjson -rdigest -e '
  dir = ENV.fetch("REPORT_DIR")
  files = Dir[File.join(dir, "*")].sort
  result = ENV.fetch("RESULT")
  report = {status: result, exit_status: result == "pass" ? 0 : 1, run_id: ENV.fetch("RUN_ID"), commit_sha: ENV.fetch("COMMIT_SHA"), command: "ADRO_REQUIRE_CODEX=1 bash scripts/real-graph-orchestration-e2e.sh", codex_version: ENV.fetch("CODEX_VERSION"), go_version: ENV.fetch("GO_VERSION"), plan_id: ENV.fetch("PLAN_ID"), graph_id: ENV.fetch("GRAPH_ID"), revision: ENV.fetch("REVISION"), requirement_id: ENV.fetch("REQUIREMENT_ID"), session_id: ENV.fetch("SESSION_ID"), work_item_id: "real-graph-work-item", event_cursor: ENV.fetch("EVENT_CURSOR"), projection_hash: ENV.fetch("PROJECTION_HASH"), projection_canonical_hash: ENV.fetch("PROJECTION_CANONICAL_HASH"), replay_projection_hash: ENV.fetch("REPLAY_PROJECTION_HASH"), replay_match: ENV.fetch("REPLAY_MATCH"), artifact_hash: ENV.fetch("ARTIFACT_HASH"), timeline_hash: ENV.fetch("TIMELINE_HASH"), evidence_files: files.map { |f| {path: File.basename(f), sha256: Digest::SHA256.file(f).hexdigest} }, assertions: File.file?(File.join(dir, "lineage-validation.json")) ? JSON.parse(File.read(File.join(dir, "lineage-validation.json"))) : {"validation_status" => ENV.fetch("VALIDATION_STATUS")}}
  File.write(File.join(dir, "manifest.json"), JSON.pretty_generate(report) + "\n")
'
[ "$result" = pass ] || fail "graph did not succeed; evidence=$REPORT_DIR/manifest.json"
log "PASS: graph-native real Codex flow completed; evidence=$REPORT_DIR/manifest.json"
