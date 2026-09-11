#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
API_PORT="${ADRO_DSH_REAL_API_PORT:-18092}"
WEB_PORT="${ADRO_DSH_REAL_WEB_PORT:-18093}"
RUN_ID="$(date -u +%Y%m%dT%H%M%SZ)-$$"
RUN_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/adro-dsh-real.XXXXXX")"
REPORT_DIR="${ADRO_DSH_REAL_REPORT_DIR:-$ROOT_DIR/var/test-report/real-codex/$RUN_ID}"
STDOUT_LOG="$REPORT_DIR/dsh-test.stdout.log"
STDERR_LOG="$REPORT_DIR/dsh-test.stderr.log"
EVIDENCE_JSON="$REPORT_DIR/dsh-evidence.json"
COMMIT_SHA="$(git -C "$ROOT_DIR" rev-parse HEAD 2>/dev/null || true)"
mkdir -p "$REPORT_DIR"

log() { printf '[ADRO DSH REAL E2E] %s\n' "$*"; }
fail() { log "ERROR: $*" >&2; exit 1; }

cleanup() {
  local exit_status=$?
  sed -E -e "s#${RUN_ROOT}#<run-root>#g" -e "s#${HOME:-}#<home>#g" -e 's#(^|[^A-Za-z0-9])sk-[A-Za-z0-9_-]{20,}#\1<redacted-secret>#g' "$STDOUT_LOG" >"$REPORT_DIR/stdout.log" 2>/dev/null || true
  sed -E -e "s#${RUN_ROOT}#<run-root>#g" -e "s#${HOME:-}#<home>#g" -e 's#(^|[^A-Za-z0-9])sk-[A-Za-z0-9_-]{20,}#\1<redacted-secret>#g' "$STDERR_LOG" >"$REPORT_DIR/stderr.log" 2>/dev/null || true
  if [ -f "$EVIDENCE_JSON" ]; then
    sed -E -e "s#${RUN_ROOT}#<run-root>#g" -e "s#${HOME:-}#<home>#g" -e 's#(^|[^A-Za-z0-9])sk-[A-Za-z0-9_-]{20,}#\1<redacted-secret>#g' "$EVIDENCE_JSON" >"$REPORT_DIR/dsh-evidence-redacted.json" 2>/dev/null || true
  fi
  RUN_ID="$RUN_ID" EXIT_STATUS="$exit_status" REPORT_DIR="$REPORT_DIR" COMMIT_SHA="$COMMIT_SHA" ruby -rjson -rdigest -e '
    dir = ENV.fetch("REPORT_DIR")
    read = ->(name) { path = File.join(dir, name); File.file?(path) ? (JSON.parse(File.read(path)) rescue {}) : {} }
    evidence = read.call("dsh-evidence-redacted.json")
    files = Dir[File.join(dir, "*")].sort
    report = {
      "status" => ENV.fetch("EXIT_STATUS").to_i == 0 ? "passed" : "failed",
      "exit_status" => ENV.fetch("EXIT_STATUS").to_i,
      "run_id" => ENV.fetch("RUN_ID"),
      "commit_sha" => ENV.fetch("COMMIT_SHA"),
      "command" => "ADRO_RUN_REAL_DSH=1 bash scripts/dsh-real-e2e.sh",
      "runtime" => evidence["runtime"], "model" => evidence["model"],
      "work_dir" => evidence["work_dir"], "session_id" => evidence["session_id"],
      "session_reused" => evidence["session_reused"], "first" => evidence["first"], "second" => evidence["second"],
      "artifact_sha256" => evidence["artifact_sha256"], "secret_redaction" => evidence["secret_redaction"],
      "stream_capture" => evidence["stream_capture"],
      "evidence_files" => files.map { |path| {"path" => File.basename(path), "sha256" => Digest::SHA256.file(path).hexdigest} }
    }
    File.write(File.join(dir, "manifest.json"), JSON.pretty_generate(report) + "\n")
  ' || true
  rm -rf "$RUN_ROOT" 2>/dev/null || true
}
trap cleanup EXIT
trap 'exit 130' INT TERM HUP

command -v dsh >/dev/null 2>&1 || fail "dsh is required; install the DSH profile before running this suite"
command -v ruby >/dev/null 2>&1 || fail "ruby is required"
[ -n "${DEEPSEEK_API_KEY:-}" ] || fail "DEEPSEEK_API_KEY is required and is used only for this process"
dsh --profile adro --probe >"$REPORT_DIR/dsh-probe.jsonl"
dsh --profile adro --list-models >"$REPORT_DIR/dsh-models.jsonl"
grep -q 'deepseek-v4-flash' "$REPORT_DIR/dsh-models.jsonl" || fail "DSH profile does not advertise deepseek-v4-flash"

export ADRO_RUN_REAL_DSH=1
export ADRO_REAL_DSH_EXECUTOR="$(command -v dsh)"
export ADRO_DSH_REAL_EVIDENCE_PATH="$EVIDENCE_JSON"
export ADRO_TEST_DSH_HELPER=0
"$ROOT_DIR/scripts/e2e-go.sh" test ./internal/provider -run '^TestDSHRealRuntimeSmoke$' -count=1 -v >"$STDOUT_LOG" 2>"$STDERR_LOG"
[ -s "$EVIDENCE_JSON" ] || fail "DSH test did not write structured evidence"

log "PASS: real DSH ${DEEPSEEK_API_KEY:+credential-present} profile, DeepSeek V4 Flash two-turn execution and session continuation completed"
