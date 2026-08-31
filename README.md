# ADRO

ADRO (Agentic Delivery & Release Orchestrator) is an independent control plane
for auditable, multi-repository delivery by coding agents. The repository does
not embed, download, patch, or call any other orchestration product. Its only
runtime dependency is an operator-selected local coding executable.

The product owns requirements, bugs, work items, evidence, provenance, tenant
boundaries, agent routing, pipeline state, durable session transcripts,
checkpoints, context archives, repair attempts, leases, and the browser
workbench. `internal/provider` exposes a small provider-neutral SPI.
`LocalProvider` is the shipped implementation: it discovers `claude`,
`codex`, or `claude-code`, invokes the real executable with `os/exec`, captures
output and git evidence, and keeps the original session/worktree for repairs.
The deterministic provider in tests is not selectable by the runtime profile.

## Quick start

Prerequisites: Go 1.24+, Git, curl, and one installed coding client. Docker is
not required or used by the local profile.

```bash
ADRO_ADMIN_PASSWORD='change-this-password' ./start.sh --no-open
```

`start.sh` builds the API and WebUI binaries, discovers the first available
executor (`ADRO_EXECUTOR` overrides discovery), creates state under `.adro`,
starts both local processes, and verifies `/readyz` plus the WebUI. Open
`http://127.0.0.1:8081` after startup.

Useful lifecycle commands:

```bash
./start.sh --status
./start.sh --stop
```

For a custom command, set `ADRO_EXECUTOR_COMMAND`. Arguments are split without
a shell and `{input}` is replaced with the stage prompt:

```bash
ADRO_EXECUTOR_COMMAND='claude -p {input} --permission-mode acceptEdits' ./start.sh
```

The API listens on `ADRO_API_PORT` (default `8080`) and the WebUI on
`ADRO_WEB_PORT` (default `8081`). `ADRO_PUBLIC_API_URL` is automatically set to
the local API URL for executor callbacks; override it when the executor runs in
another network namespace. `ADRO_HOME`, `ADRO_ARTIFACT_ROOT`,
`ADRO_WORK_ROOT`, `ADRO_RUN_STATE_FILE`, `ADRO_HARNESS_STATE_FILE`, and
`ADRO_PLUGIN_STATE_FILE` control execution checkouts, durable harness state,
and the signed plugin registry. Set
`ADRO_AUTH_MODE=required` with `ADRO_ADMIN_PASSWORD` for protected routes.
Set `ADRO_EXECUTOR_TIMEOUT=15m` (Go duration) to bound an individual client run;
an expired run is recorded as `timed_out` with its session/worktree evidence
intact and is treated as an auditable pipeline failure. `ADRO_PIPELINE_WATCH_TIMEOUT`
(default `30m`) bounds how long a local pipeline may wait for a terminal result;
when it expires ADRO cancels the provider run and suspends the pipeline with a
durable reason instead of leaving `waiting_provider` forever.

## From source

```bash
go test ./...
go run ./cmd/adro-api -addr :8080 -artifact-root ./var/artifacts
go run ./cmd/adro-web -addr :8081 -root ./apps/web
```

The API exposes readiness at `/readyz`, secret-free executor diagnostics at
`/api/v1/provider/diagnostics`, and versioned resources below `/api/v1`. The
session harness is available at `/api/v1/sessions/{id}/turns`,
`/checkpoints`, `/context/status`, `/context/compile`, `/compact`, and
`/recover`; these endpoints preserve the full transcript after compaction.
Signed adapter installations are managed through `/api/v1/plugins` and are
never activated before manifest digest/signature verification.
seven-stage pipeline is design, development, unit test, integration test,
arbitration, revalidation, and report. A failed integration test returns to the
same development session and worktree; a missing or mismatched continuity
record is rejected rather than silently starting over.

## Architecture and extension points

- `internal/domain`: tenant-scoped business contracts and state transitions.
- `internal/pipeline`: strict stage machine and evidence validation.
- `internal/provider`: provider-neutral SPI and the local executable boundary.
- `internal/store`: atomic JSON persistence for the single-node profile.
- `internal/events`: replayable event bus and workspace streams.
- `internal/harness`: append-only turns, hash-linked checkpoints, exact
  compaction archives, memory citations, recoverable leases, and outbox
  delivery.
- `internal/plugins`: signed adapter installation registry with verified
  activation, health tracking, and automatic quarantine.
- `internal/api`: authenticated REST transport and workbench routes.
- `apps/web`: ADRO-owned Chinese/English workbench; no embedded external UI.
- `sdk/*`: extension SPIs for integrations and artifact drivers.

Modules are intentionally replaceable. A community plugin may implement the
provider, artifact, event, identity, or workflow SPI without changing domain
code. Production profiles remain fail-closed until each external adapter has a
contract test and an explicit deployment boundary.

The product and technical contracts are maintained in
`docs/product-requirements.zh-CN.md` and
`docs/architecture/adro-technical-design.zh-CN.md`. Those documents contain
the product scope, implementation contracts, release gates, and deployment
boundaries. No external source code or runtime API is shipped here.

## Verification

Run the complete local checks with:

```bash
make test
make contracts
go vet ./...
go build ./...
```

For the repeatable real workflow (no Docker and no MockProvider), authenticate a
local Claude Code or Codex client and run:

```bash
make real-e2e
```

`scripts/real-pipeline-e2e.sh` creates a temporary Git repository, submits a
Requirement and evidence attachment, starts the seven-stage pipeline, waits for
the real client to implement code, injects and records one integration failure,
checks the generated Bug, verifies same-session/worktree repair and revalidation,
then checks the final report. Set `ADRO_REAL_E2E_TIMEOUT` to change the deadline
and `ADRO_E2E_KEEP=1` to retain the run evidence. Missing client credentials are
reported as an environment prerequisite; the script never substitutes a fake
runtime result.
