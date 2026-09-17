# Legacy Baseline Report

Date: 2026-09-17.

## Identity

- Commit: `2d480a87f0ecdf974ab09cdece2729f209a8e813`.
- Annotated tag: `legacy-delivery-control-plane-20260917`.
- Baseline worktree: detached at the commit above and clean before every recorded rerun.
- Go: `go1.25.0 darwin/amd64`.
- Node: `v26.7.0`.

Frozen file digests:

| File | SHA-256 |
|---|---|
| `openapi/openapi.yaml` | `9df062836b58d52f394c7f5fc62bee075090e14dd18b377f4ba09260553b5337` |
| `SBOM` | `dd5b5b300b136d09dc100776cc079826ee9eae2bf8ffb78ae9f556cf2418d2cb` |
| `package-lock.json` | `5895b280c69c47c6640cd25b8f15e269cf73718c5a2936e8a9c078f974a2b2f6` |
| `go.sum` | `6bd349b368d5e15eee5263bb0f1b1ac0c1918d3f4dba319656458898845e75b3` |

## Reproducible Results

All commands in this section ran alone from the detached baseline worktree.

| Command | Exit | Result |
|---|---:|---|
| `./scripts/e2e-go.sh test ./... -count=1 -p 1` | 0 | All Go tests passed. |
| `./scripts/e2e-go.sh test -race ./... -count=1 -p 1` | 0 | All Go race tests passed. |
| `make fault-matrix` | 0 | All 12 fault-injection cases passed. |
| `./scripts/e2e-go.sh test ./... -run '^$' -bench . -benchtime=1x -p 1` | 0 | Benchmark discovery passed; the repository defines no Go benchmark functions. |
| `npx playwright test e2e/chat-workspace.spec.js --grep 'replays an idempotent chat creation after a lost response body' --workers=1` | 0 | Lost-response idempotency replay passed in 16.2 seconds. |
| `npx playwright test e2e/workbench.spec.js --grep 'creates and operates native Agent, Squad, and immutable Plan records' --workers=1` | 0 | Agent/Squad/Plan browser flow passed in 31.7 seconds. |

The generated fault report records individual commands, durations, and log digests under `var/test-report/fault-matrix/` in a baseline checkout.

## Overloaded Run Observation

An earlier exploratory run incorrectly started the full Go tests, race tests, and browser suite concurrently on the same host. That run is retained only as load-sensitivity evidence:

- the Go suite observed model-discovery timeout/fallback and a provider EOF timeout;
- the browser suite timed out waiting for the selected chat project after a lost response body;
- a later workbench assertion was interrupted when that browser run was stopped.

Those results are not accepted as baseline failures. The serial Go suites and both isolated browser cases passed with the exact commands above. Future baseline comparisons must use the serial commands and must not overlap the API/model-discovery workloads.

## Coverage Gaps

- No Go benchmark functions exist, so this baseline freezes benchmark discovery rather than latency or throughput thresholds.
- Generated browser screenshots are not yet stored as a dedicated immutable rebuild fixture set.
- The full browser matrix was not rerun after the overloaded observation; the two affected cases were rerun and passed in isolation.
