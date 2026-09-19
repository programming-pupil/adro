#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BENCHTIME="${ADRO_BENCHTIME:-1s}"
COUNT="${ADRO_BENCH_COUNT:-5}"

cd "$ROOT_DIR"
exec ./scripts/e2e-go.sh test ./internal/orchestration \
  -run '^$' \
  -bench '^Benchmark(FairAdmissionNoisyNeighbor|FairAdmissionBurst|AdmissionQuotaExhaustion|FairAdmissionWorkerJitterRecovery|FairAdmissionPriorityInversion)$' \
  -benchmem \
  -benchtime="$BENCHTIME" \
  -count="$COUNT"
