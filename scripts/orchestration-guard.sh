#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

failed=0

reject() {
  local description="$1"
  local pattern="$2"
  shift 2
  local matches
  matches="$(rg -n "$pattern" "$@" 2>/dev/null || true)"
  if [[ -n "$matches" ]]; then
    printf 'orchestration guard: %s\n%s\n' "$description" "$matches" >&2
    failed=1
  fi
}

# Numeric stages are allowed only in the explicitly named compatibility
# adapter and legacy API packages. The graph kernel must never regain a stage
# dependency through a convenient import.
reject 'graph kernel depends on the legacy seven-stage runtime' \
  'PipelineStage|NextSelectedStage|nextCustomStage' \
  internal/orchestration internal/context internal/memory internal/mentions internal/runtime

# Structured mentions are parsed as stable URIs. Tokenizing visible @names is
# forbidden in both the parser and the comment dispatch boundary.
reject 'comment routing uses display-name tokenization' \
  'strings[.]Fields|FieldsFunc' internal/mentions internal/api/comments.go

# New runtime/control-plane code must not ship unfinished markers. Historical
# product documents are outside this executable guard.
reject 'runtime contains unfinished TODO/FIXME markers' \
  'TODO|FIXME' internal/orchestration internal/context internal/memory internal/mentions internal/runtime internal/provider internal/api/orchestration.go internal/api/orchestration_api.go internal/api/orchestration_diagnostics.go

if [[ "$failed" -ne 0 ]]; then
  exit 1
fi
printf 'orchestration compatibility and completeness guard passed\n'
