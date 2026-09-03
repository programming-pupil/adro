#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
API_PORT="${ADRO_API_PORT:-18082}"
WEB_PORT="${ADRO_WEB_PORT:-18083}"
TIMEOUT_SECONDS="${ADRO_REAL_E2E_TIMEOUT:-1800}"
RUN_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/adro-real-pipeline.XXXXXX")"
STATE_HOME="$RUN_ROOT/state"
FIXTURE="$RUN_ROOT/fixture"
INTEGRATION_COUNTER="$RUN_ROOT/integration-counter"
LOG="$RUN_ROOT/start.log"
API="http://127.0.0.1:$API_PORT"
WORKSPACE="adro-real-e2e"

# Keep the model-backed test independent from the parent Codex runtime. The
# parent exports CODEX_* session variables and skills for this coding run; if
# they leak into the child, the real executor can resume the wrong thread or
# spend the test processing the agent-runtime instructions. Reuse the
# operator's real auth and provider configuration in an isolated home so a
# configured OpenAI-compatible relay (for example a custom model_provider)
# remains active without sharing the parent session database.
SOURCE_CODEX_HOME="${CODEX_HOME:-}"
CODEX_AUTH_FILE=""
if [ -f "$SOURCE_CODEX_HOME/auth.json" ]; then
  CODEX_AUTH_FILE="$SOURCE_CODEX_HOME/auth.json"
elif [ -f "${HOME:-}/.codex/auth.json" ]; then
  CODEX_AUTH_FILE="${HOME}/.codex/auth.json"
fi
CODEX_RUN_HOME="$RUN_ROOT/codex-home"
mkdir -p "$CODEX_RUN_HOME"
if [ -n "$CODEX_AUTH_FILE" ]; then
  ln -s "$CODEX_AUTH_FILE" "$CODEX_RUN_HOME/auth.json"
fi
CODEX_CONFIG_FILE=""
if [ -f "$SOURCE_CODEX_HOME/config.toml" ]; then
  CODEX_CONFIG_FILE="$SOURCE_CODEX_HOME/config.toml"
elif [ -f "${HOME:-}/.codex/config.toml" ]; then
  CODEX_CONFIG_FILE="${HOME}/.codex/config.toml"
fi
if [ -n "$CODEX_CONFIG_FILE" ]; then
  install -m 600 "$CODEX_CONFIG_FILE" "$CODEX_RUN_HOME/config.toml"
fi
export CODEX_HOME="$CODEX_RUN_HOME"
unset CODEX_SESSION_ID CODEX_THREAD_ID CODEX_CI

log() { printf '[ADRO REAL E2E] %s\n' "$*"; }
fail() { printf '[ADRO REAL E2E] ERROR: %s\n' "$*" >&2; exit 1; }

cleanup() {
  ADRO_HOME="$STATE_HOME" ADRO_API_PORT="$API_PORT" ADRO_WEB_PORT="$WEB_PORT" \
    "$ROOT_DIR/start.sh" --stop --no-open >/dev/null 2>&1 || true
  if [ "${ADRO_E2E_KEEP:-0}" != "1" ]; then
    # The real Codex process may finish its cache write just after ADRO stops
    # the local services. Retry removal so a harmless filesystem race cannot
    # turn a completed pipeline assertion into a failed test command.
    for attempt in 1 2 3 4 5; do
      rm -rf "$RUN_ROOT" 2>/dev/null || true
      [ ! -e "$RUN_ROOT" ] && return 0
      sleep 1
    done
    log "WARN: unable to remove temporary run directory: $RUN_ROOT"
  else
    log "Evidence retained at $RUN_ROOT"
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

command -v curl >/dev/null 2>&1 || fail "curl is required"
command -v git >/dev/null 2>&1 || fail "git is required"
command -v ruby >/dev/null 2>&1 || fail "ruby is required"
command -v go >/dev/null 2>&1 || [ -x "${ADRO_GO_BIN:-$ROOT_DIR/scripts/e2e-go.sh}" ] || fail "go is required"
# Keep the API build and the fixture's verification command on one Go
# toolchain. The child Codex process inherits this explicit setting when the
# executor allows environment propagation; the fixture still falls back to
# PATH for portable CI images.
export ADRO_GO_BIN="${ADRO_GO_BIN:-$ROOT_DIR/scripts/e2e-go.sh}"

executor="${ADRO_EXECUTOR:-}"
if [ -z "$executor" ] && [ -n "${ADRO_EXECUTOR_COMMAND:-}" ]; then
  executor="${ADRO_EXECUTOR_COMMAND%% *}"
fi
if [ "${ADRO_REQUIRE_CODEX:-0}" = "1" ]; then
  [ -n "$executor" ] || executor="$(command -v codex 2>/dev/null || true)"
  [ -n "$executor" ] || fail "Codex is required for the real pipeline suite; install codex or set ADRO_EXECUTOR"
  case "$(basename "$executor")" in
    codex|codex.exe) ;;
    *) fail "real pipeline suite requires Codex, got $(basename "$executor"); refusing to substitute another client" ;;
  esac
elif [ -z "$executor" ]; then
  for candidate in claude codex claude-code; do
    if command -v "$candidate" >/dev/null 2>&1; then
      executor="$(command -v "$candidate")"
      break
    fi
  done
fi
[ -n "$executor" ] || fail "install claude/codex/claude-code and authenticate it, or set ADRO_EXECUTOR"
executor="$(command -v "$executor" 2>/dev/null || printf '%s' "$executor")"
"$executor" --version >/dev/null 2>&1 || fail "coding client is not executable: $executor"
if [ -z "${ADRO_EXECUTOR_COMMAND:-}" ]; then
	case "$(basename "$executor")" in
		codex)
			export ADRO_EXECUTOR_COMMAND="$executor exec --json --skip-git-repo-check --dangerously-bypass-approvals-and-sandbox {input}"
			;;
		*)
			export ADRO_EXECUTOR_COMMAND="$executor --dangerously-skip-permissions --output-format json --permission-mode acceptEdits {input}"
			;;
	esac
fi

mkdir -p "$FIXTURE"
cat > "$FIXTURE/go.mod" <<'EOF'
module example.com/adro-real-e2e

go 1.21
EOF
cat > "$FIXTURE/calculator.go" <<'EOF'
package calculator

func Add(a, b int) int {
	return a + b
}
EOF
cat > "$FIXTURE/calculator_test.go" <<'EOF'
package calculator

import "testing"

func TestAdd(t *testing.T) {
	if got := Add(2, 3); got != 5 {
		t.Fatalf("Add(2, 3) = %d", got)
	}
}
EOF
cat > "$FIXTURE/integration-check.sh" <<'EOF'
#!/usr/bin/env sh
set -eu
# Codex command sandboxes may strip orchestration-only environment variables;
# retain an explicit override while keeping the fixture deterministic there.
counter_path="${ADRO_E2E_INTEGRATION_COUNTER:-.adro-e2e-integration-counter}"
if [ ! -f "$counter_path" ]; then
  : > "$counter_path"
  printf '%s\n' 'intentional first integration failure' >&2
  exit 1
fi
go_bin="${ADRO_GO_BIN:-}"
if [ -x "$go_bin" ] || [ -f "$go_bin" ]; then
  "$go_bin" test ./...
else
  go test ./...
fi
EOF
chmod +x "$FIXTURE/integration-check.sh"
git -C "$FIXTURE" init -q
git -C "$FIXTURE" config user.email adro-e2e@example.invalid
git -C "$FIXTURE" config user.name ADRO-E2E
git -C "$FIXTURE" add .
git -C "$FIXTURE" commit -qm "fixture: initial calculator"
git -C "$FIXTURE" branch -M main

export ADRO_HOME="$STATE_HOME"
export ADRO_API_PORT="$API_PORT"
export ADRO_WEB_PORT="$WEB_PORT"
export ADRO_EXECUTOR="$executor"
export ADRO_AUTH_MODE=optional
export ADRO_E2E_INTEGRATION_COUNTER="$INTEGRATION_COUNTER"
"$ROOT_DIR/start.sh" --no-open >"$LOG" 2>&1 || {
  cat "$LOG" >&2
  fail "local ADRO did not start"
}

curl -fsS "$API/readyz" >/dev/null
headers=(-H "X-Workspace-ID: $WORKSPACE" -H 'Content-Type: application/json')

repo_body="$(WORKSPACE="$WORKSPACE" FIXTURE="$FIXTURE" ruby -rjson -e 'puts JSON.generate(workspace_id: ENV.fetch("WORKSPACE"), canonical_name: "calculator", clone_url: ENV.fetch("FIXTURE"), provider: "git", default_branch: "main", metadata: {local_path: ENV.fetch("FIXTURE")})')"
repo_json="$(curl -fsS -X POST "$API/api/v1/repositories" "${headers[@]}" -d "$repo_body")"
repo_id="$(printf '%s' "$repo_json" | json_field id)"

requirement_body="$(WORKSPACE="$WORKSPACE" REPO_ID="$repo_id" ruby -rjson -e '
  puts JSON.generate(
    workspace_id: ENV.fetch("WORKSPACE"),
    title: "Implement Multiply with an audited repair loop",
    description: "Implement Multiply(a,b) in calculator.go and add unit coverage. Run go test ./... in stage 3. In stage 4 run ./integration-check.sh; it intentionally fails exactly once using ADRO_E2E_INTEGRATION_COUNTER. Treat that failure as a real bug, preserve the original development session and worktree, repair the code incrementally, then rerun unit and integration checks.",
    acceptance_criteria: ["Multiply is implemented and tested", "the intentional integration failure is recorded", "the same provider session and workdir are used for repair", "the final report contains test evidence"],
    assignee_member_ids: ["real-e2e-product"],
    repository_ids: [ENV.fetch("REPO_ID")],
    priority: "urgent"
  )
')"
requirement_json="$(curl -fsS -X POST "$API/api/v1/requirements" "${headers[@]}" -d "$requirement_body")"
requirement_id="$(printf '%s' "$requirement_json" | json_field id)"

printf '%s\n' 'ADRO real E2E requirement evidence' > "$RUN_ROOT/requirement-evidence.txt"
curl -fsS -X POST "$API/api/v1/attachments" \
  -H "X-Workspace-ID: $WORKSPACE" \
  -F owner_type=requirement -F owner_id="$requirement_id" -F file=@"$RUN_ROOT/requirement-evidence.txt" >/dev/null

pipeline_body="$(REQUIREMENT_ID="$requirement_id" ruby -rjson -e '
  puts JSON.generate(
    requirement_id: ENV.fetch("REQUIREMENT_ID"),
    roles: {
      designer_agent_id: "11111111-1111-1111-1111-111111111111",
      developer_agent_id: "22222222-2222-2222-2222-222222222222",
      tester_agent_id: "33333333-3333-3333-3333-333333333333",
      arbitrator_agent_id: "44444444-4444-4444-4444-444444444444"
    },
    max_retries: 2,
    coverage_threshold: 80
  )
')"
pipeline_json="$(curl -fsS -X POST "$API/api/v1/pipelines" "${headers[@]}" -d "$pipeline_body")"
pipeline_id="$(printf '%s' "$pipeline_json" | json_field id)"
log "Pipeline $pipeline_id started with real executor $executor"

deadline=$((SECONDS + TIMEOUT_SECONDS))
final_pipeline="$pipeline_json"
bug_id=""
last_bug_id=""
while [ "$SECONDS" -lt "$deadline" ]; do
  final_pipeline="$(curl -fsS "$API/api/v1/pipelines/$pipeline_id" -H "X-Workspace-ID: $WORKSPACE")"
  status="$(printf '%s' "$final_pipeline" | json_field status)"
  bug_id="$(printf '%s' "$final_pipeline" | json_field bug_id 2>/dev/null || true)"
  if [ -n "$bug_id" ]; then
    bug_json="$(curl -fsS "$API/api/v1/bugs/$bug_id" -H "X-Workspace-ID: $WORKSPACE")"
    [ "$(printf '%s' "$bug_json" | json_field bug.requirement_id)" = "$requirement_id" ] || fail "generated bug is not linked to the requirement"
    if [ "$bug_id" != "$last_bug_id" ]; then
      log "Integration failure became Bug $bug_id"
      last_bug_id="$bug_id"
    fi
  fi
  case "$status" in
    completed) break ;;
    suspended|failed) printf '%s\n' "$final_pipeline" >&2; cat "$LOG" >&2; fail "pipeline stopped in $status" ;;
  esac
  sleep 2
done
[ "$SECONDS" -lt "$deadline" ] || fail "pipeline did not finish within ${TIMEOUT_SECONDS}s"
[ "$(printf '%s' "$final_pipeline" | json_field status)" = "completed" ] || fail "pipeline did not complete"
[ "$(printf '%s' "$final_pipeline" | json_field retry_count)" -ge 1 ] || fail "integration failure did not enter repair"
[ -n "$(printf '%s' "$final_pipeline" | json_field parent_session_id)" ] || fail "provider session was not pinned"
[ -n "$(printf '%s' "$final_pipeline" | json_field work_item_id)" ] || fail "logical work item was not persisted"
[ -n "$bug_id" ] || fail "integration failure did not create a linked bug"
[ -n "$(printf '%s' "$final_pipeline" | json_field final_report)" ] || fail "final report is empty"

work_items="$(curl -fsS "$API/api/v1/requirements/$requirement_id/work-items" -H "X-Workspace-ID: $WORKSPACE")"
printf '%s' "$work_items" | WORK_ITEM_ID="$(printf '%s' "$final_pipeline" | json_field work_item_id)" ruby -rjson -e '
  payload = JSON.parse(STDIN.read)
  id = ENV.fetch("WORK_ITEM_ID")
  abort("pipeline work item missing") unless payload.fetch("items").any? { |item| item["id"] == id }
'

repair_json="$(curl -fsS -X POST "$API/api/v1/bugs/$bug_id/repair" -H "X-Workspace-ID: $WORKSPACE" -H 'Content-Type: application/json' -d '{}')"
repair_reused="$(printf '%s' "$repair_json" | json_field session_reused)"
repair_session="$(printf '%s' "$repair_json" | json_field run.session_id)"
repair_workdir="$(printf '%s' "$repair_json" | json_field run.work_dir)"
[ "$repair_reused" = "true" ] || fail "explicit bug repair did not reuse the native session"
[ "$repair_session" = "$(printf '%s' "$final_pipeline" | json_field parent_session_id)" ] || fail "bug repair session differs from pipeline session"
[ "$repair_workdir" = "$(printf '%s' "$final_pipeline" | json_field provider_work_dir)" ] || fail "bug repair workdir differs from pipeline workdir"

log "PASS: requirement -> real Agent development -> unit/integration failure -> linked Bug -> original-session repair -> revalidation -> report"
log "Evidence: pipeline=$pipeline_id requirement=$requirement_id bug=$bug_id session=$(printf '%s' "$final_pipeline" | json_field parent_session_id)"
