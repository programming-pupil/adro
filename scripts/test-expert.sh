#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
REPORT_DIR="${ADRO_TEST_REPORT_DIR:-$ROOT_DIR/var/test-report}"
mkdir -p "$REPORT_DIR"
STEPS_FILE="$REPORT_DIR/steps.tsv"
: > "$STEPS_FILE"

started_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
overall=0
GO_BIN="${ADRO_GO_BIN:-$ROOT_DIR/scripts/e2e-go.sh}"

run_step() {
  local name="$1"
  shift
  local log="$REPORT_DIR/$name.log"
  local status="passed"
  local code=0
  printf '[TEST-EXPERT] %s\n' "$name"
  if (cd "$ROOT_DIR" && "$@") >"$log" 2>&1; then
    :
  else
    code=$?
    status="failed"
    overall=1
  fi
  printf '%s\t%s\t%s\t%s\n' "$name" "$status" "$code" "$log" >> "$STEPS_FILE"
}

run_step go_test "$GO_BIN" test ./... -count=1
run_step go_race "$GO_BIN" test -race ./... -count=1 -p 1
run_step go_vet "$GO_BIN" vet ./...
run_step go_build "$GO_BIN" build ./...
run_step contracts make contracts
run_step supply_chain make supply-chain
run_step fault_matrix make fault-matrix
run_step postgres_conformance make postgres-conformance
run_step git_diff_check git diff --check

if [[ "${ADRO_TEST_EXPERT_SKIP_BROWSER:-0}" == "1" ]]; then
  printf '%s\t%s\t%s\t%s\n' browser skipped 0 "browser checks disabled by ADRO_TEST_EXPERT_SKIP_BROWSER=1" >> "$STEPS_FILE"
else
  run_step browser npm run test:e2e:adro
  run_step browser_matrix npm run test:e2e:matrix
fi

if [[ "${ADRO_TEST_EXPERT_REAL:-0}" == "1" ]]; then
  run_step real_e2e make real-e2e
else
  overall=1
  printf '%s\t%s\t%s\t%s\n' real_e2e blocked 2 'set ADRO_TEST_EXPERT_REAL=1 on a Codex-authenticated runner' >> "$STEPS_FILE"
fi

finished_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
REPORT_DIR="$REPORT_DIR" STARTED_AT="$started_at" FINISHED_AT="$finished_at" OVERALL="$overall" ruby -rjson -e '
  dir = ENV.fetch("REPORT_DIR")
  # Keep the report generator compatible with the Ruby versions commonly
  # available on operator laptops (filter_map was only added in Ruby 2.7).
  steps = File.readlines(File.join(dir, "steps.tsv"), chomp: true).map do |line|
    name, status, code, log = line.split("\t", 4)
    next if name.nil? || status.nil?
    {"name" => name, "status" => status, "exit_code" => code.to_i, "log" => log}
  end.compact
  report = {"started_at" => ENV.fetch("STARTED_AT"), "finished_at" => ENV.fetch("FINISHED_AT"), "status" => ENV.fetch("OVERALL") == "0" ? "passed" : "failed", "steps" => steps}
  File.write(File.join(dir, "report.json"), JSON.pretty_generate(report) + "\n")
  File.write(File.join(dir, "report.junit.xml"), "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n" + "<testsuite name=\"adro-test-expert\" tests=\"#{steps.length}\" failures=\"#{steps.count { |s| s["status"] == "failed" }}\" skipped=\"#{steps.count { |s| s["status"] == "skipped" || s["status"] == "blocked" }}\">\n" + steps.map { |s| body = "<testcase classname=\"adro\" name=\"#{s["name"]}\"/>"; s["status"] == "failed" ? body.sub("/>", "><failure message=\"exit #{s["exit_code"]}\"/></testcase>") : s["status"] == "blocked" || s["status"] == "skipped" ? body.sub("/>", "><skipped/></testcase>") : body }.join + "</testsuite>\n")
  puts JSON.generate(report)
'

exit "$overall"
