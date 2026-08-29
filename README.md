# ADRO

ADRO (Agentic Delivery & Release Orchestrator) is a provider-independent control plane for auditable, multi-repository AI delivery. It owns requirements, work items, evidence, provenance and workflow state; execution providers such as Multica are replaceable adapters.

This repository contains a dependency-free, directly runnable reference profile that is useful on a laptop, in CI, or as the control-plane core of a production deployment:

- versioned requirement and bug REST APIs under `/api/v1`;
- explicit requirement state transitions with optimistic versions;
- deterministic multi-repository work-item fan-out;
- `ExecutionProvider` SPI and a deterministic `MockProvider`;
- content-addressed filesystem ArtifactStore with atomic writes, tenant-scoped logical URIs and HTTP Range downloads;
- deduplicated versioned events with cursor replay;
- WebSocket workspace streams with cursor replay and changed-files snapshots;
- optional durable single-node JSON snapshots for control-plane, events, audit,
  runner registration, context manifests and repair attempts;
- argv-only Runner execution confined to a registered workspace root with bounded
  environment/output and command-digest auditing;
- real MCP JSON-RPC tools/list and tools/call over HTTP/SSE with bounded
  responses, schema digests and failure-aware health/invocation records;
- governed MCP, Skill and Automation resources with audit records;
- artifact upload verification, Range/HEAD downloads and migration control records;
- a responsive Chinese/English browser workbench in `apps/web` and PostgreSQL migration boundary in `migrations`;
- a repeatable Playwright acceptance suite that opens all 18 workbench menus and exercises requirement, WebSocket, i18n, and screenshot delivery paths.

The ADRO workbench is the product surface: agents are created from **Agents &
squads**, requirements are submitted and advanced from **Requirements** and
their detail view, and evidence is captured from **Extensions & storage**. The
browser calls ADRO APIs only; it does not embed or depend on the Multica WebUI.
The Provider remains an explicit SPI boundary, so a local demo can run offline
while a production Multica gateway must be configured and contract-tested.

The full architecture and production acceptance criteria are specified in [ADRO-production-blueprint.zh-CN.md](ADRO-production-blueprint.zh-CN.md). The local profile owns the contracts and deterministic behavior; deployments can replace its in-memory/default JSON store, event bus, filesystem driver and MockProvider with PostgreSQL, NATS/Temporal, an external ArtifactStore and Multica/Git/CI adapters without changing domain APIs. It never claims an external service is connected when it is not.

The implementation-to-GA boundary is tracked in [docs/architecture/ga-readiness.md](docs/architecture/ga-readiness.md); release status must not be inferred from unit-test results alone.

Production/HA fail-closed rules, state ownership, migration gates, and adapter
acceptance are documented in
[docs/architecture/production-deployment.md](docs/architecture/production-deployment.md).
The reproducible release procedure is in
[docs/operations/release-runbook.md](docs/operations/release-runbook.md), and
browser/platform claims are bounded by [docs/compatibility.md](docs/compatibility.md).

Set `ADRO_PROVIDER=multica`, `ADRO_MULTICA_URL` and `ADRO_MULTICA_TOKEN` to use the included HTTP Provider adapter. The token is read only from the process environment and is never serialized into requirements, events or artifacts. `ADRO_MULTICA_WORKSPACE_ID` and `ADRO_MULTICA_RUNTIME_ID` pin the provider-native workspace and runtime; a single visible workspace and a single/online runtime are auto-discovered when those variables are omitted. Ambiguous discovery fails closed. Optional `ADRO_MULTICA_WS_URL` enables the upstream run-event WebSocket; `ADRO_MULTICA_CAPABILITIES_PATH` and `ADRO_MULTICA_ATTACHMENT_PATH` override gateway paths when a daemon exposes a versioned route. The API exposes a secret-free probe at `GET /api/v1/provider/diagnostics` and reports the connection as unavailable until both health and capabilities are reachable.

Authentication mode is controlled by `ADRO_AUTH_MODE`: `optional` (the
single-node default) permits unauthenticated local API use, while `required`
requires an active local identity or machine token for protected API routes.
Only these two values are accepted; a typo fails startup and readiness instead
of disabling authentication silently.

For the current public Multica API, set `ADRO_MULTICA_AGENT_ID` to a real
workspace Agent UUID when you want newly materialized Work Items assigned to
that Agent. The adapter sends Multica's required `workspace_id`, issue title,
description and stage fields, and falls back from the optional `/api/runs`
contract to Multica's native `POST /api/issues/{id}/rerun` task action. Full
run snapshots, messages, cancellation, usage and event replay still require a
gateway that exposes those provider-neutral endpoints (or an explicitly
configured WebSocket), so diagnostics never over-claims those capabilities.

Multiple Multica Agents can be routed per workspace with the optional
`ADRO_MULTICA_AGENT_MAP` JSON environment variable. It is parsed strictly at
startup (64 KiB / 1000 route limit, UUID-only native IDs, no unknown fields):

```json
{"workspaces":{"<workspace-uuid>":{"default_agent_id":"<agent-uuid>","members":{"alice":"<agent-uuid>"},"roles":{"developer":"<agent-uuid>"}}}}
```

Resolution is captured once in an opaque binding when a Work Item is created:
existing binding, developer profile, member, role, workspace default, then the
legacy `ADRO_MULTICA_AGENT_ID`. Repairs and reruns reuse that binding even if
the environment changes. Diagnostics expose only configuration, reachability,
authentication and routing states plus counts; tokens, map contents and native
Agent UUIDs are never returned.

Token presence alone is reported as authentication `unverified`; the adapter
marks it `verified` only after an authenticated Agent/Issue or run mutation
succeeds. Public configuration and health probes do not count as authentication
evidence.

## Quick start

Docker Compose, Git, curl, and OpenSSL are the only bootstrap prerequisites on
macOS or Linux. From a fresh checkout, run:

```bash
./start.sh
```

The script generates a persistent ADRO administrator, builds ADRO, installs the
pinned Multica `v0.4.35` CLI after checking its official SHA-256 checksum,
starts Multica's self-host backend/PostgreSQL on loopback, and uses the isolated
Multica CLI profile `adro-local`. It never changes a user's existing Multica
profile or daemon. The ADRO WebUI is served at `http://127.0.0.1:8081`.

The login screen has an `EN`/`中文` toggle in the upper-right corner. The choice
is persisted in `localStorage` and applies before authentication as well as in
the signed-in workbench; the document language and date formatting follow the
selected locale.

The first self-host run has one unavoidable interactive trust step: authenticate
the isolated local Multica profile in the browser (the private development
stack uses verification code `888888`). Multica's CLI creates the profile PAT;
the bootstrap reads that token from the profile's own mode-restricted config
and persists it in the mode-0600 `.adro/adro.env` file, so normal users do not
need to visit Multica settings or copy a PAT. If upstream authentication is
skipped, ADRO starts with MockProvider and can be connected later with:

```bash
ADRO_MULTICA_TOKEN='<local Multica PAT>' ./start.sh
```

Useful lifecycle commands:

```bash
./start.sh --status
./start.sh --stop
./start.sh --without-multica
```

When Docker is unavailable, the dependency-free local profile starts both
processes directly with Go. With no Provider credentials it uses MockProvider:

```bash
ADRO_ADMIN_PASSWORD='change-this-local-password' ./start.sh --no-docker
```

This serves the API on `http://127.0.0.1:8080` and the ADRO WebUI on
`http://127.0.0.1:8081/?api=http://127.0.0.1:8080`. It requires Go, curl, and
OpenSSL, persists local identities under `.adro`, and supports `--status`,
`--stop`, and `--no-open` like the container path. It intentionally does not
start Multica or claim a real Provider connection.

To run without Docker against an existing hosted or remote Multica API, provide
a PAT and pin the workspace/runtime when discovery would be ambiguous:

```bash
ADRO_ADMIN_PASSWORD='change-this-local-password' \
ADRO_MULTICA_URL='https://api.multica.ai' \
ADRO_MULTICA_TOKEN='<multica-pat>' \
ADRO_MULTICA_WORKSPACE_ID='<workspace-uuid>' \
ADRO_MULTICA_RUNTIME_ID='<runtime-uuid>' \
ADRO_MULTICA_PROJECT_ID='<project-uuid>' \
./start.sh --no-docker
```

The remote token is inherited only by the API process and is deliberately not
written to `.adro/adro.env`; supply it again on the next start. Diagnostics
remain unavailable until the remote health and capability handshakes succeed,
and authentication is verified only after a successful Agent, Issue, run, or
attachment mutation. This avoids Docker locally, but the remote Multica
deployment and its database/runtime remain external prerequisites.

`ADRO_MULTICA_PROJECT_ID` is important for end-to-end delivery: it makes every
materialized Multica Work Item inherit the project resources (repositories and
local-directory bindings) required by the assigned Agent. ADRO rejects
ambiguous workspace/runtime discovery instead of silently selecting a target.

For a disposable clean-room run, stop the stack first and remove only the
local ADRO/Multica state (this deletes local demo identities, artifacts, and
the self-host database volumes):

```bash
./start.sh --stop || true
docker compose -p adro -f deploy/compose/docker-compose.yml down -v --remove-orphans || true
docker compose -p adro-multica -f .adro/multica-server/docker-compose.selfhost.yml down -v --remove-orphans || true
rm -rf .adro
```

Then set a known local administrator password and bootstrap again:

```bash
ADRO_ADMIN_PASSWORD='change-this-local-password' ./start.sh
```

The script prints the selected administrator credentials and opens the ADRO
WebUI. The only upstream step is the first isolated Multica profile
authentication in the browser; use verification code `888888`, then return to
ADRO at `http://127.0.0.1:8081`. For an ADRO-only smoke test that does not start
Multica, run `ADRO_ADMIN_PASSWORD='change-this-local-password' ./start.sh --without-multica`.

Multica's frontend at `http://127.0.0.1:19081` is used only for its bootstrap
authentication and PAT issuance. Normal requirement, bug, agent, permission,
workflow, evidence, extension, and screenshot operations use ADRO's own WebUI.
Eliminating that one-time upstream identity bootstrap requires a supported
Multica service-account/bootstrap API; ADRO does not bypass or scrape Multica
credentials.

## Run from source

```bash
go test ./...
go run ./cmd/adro-api -addr :8080 -artifact-root ./var/artifacts
```

Open `apps/web/index.html` through a static server pointed at the API origin, or use the API directly. For a separately served workbench, pass the API origin as a query parameter, for example `http://localhost:8081/?api=http://localhost:8080`; same-origin deployments need no parameter. The demo API has readiness checks at `GET /readyz` and a MockProvider, so it needs no cloud key, Git credential or model key.

The workbench's **Extensions & storage** view can capture a browser screen with
the native Screen Capture permission or accept an image file. `POST
/api/v1/screenshots` first writes an immutable `artifact://` object, then
forwards the bytes through the provider's optional attachment contract when a
target (`issue`, `comment`, `run`, or `workspace`) is selected. A failed remote
delivery never loses the stored Artifact URI. MockProvider implements this
contract for offline verification; a Multica deployment must advertise
`attachment.v1` in capabilities before the UI marks delivery as supported.

For an ADRO-only container profile (API on `:8080`, workbench on `:8081`),
provide the mandatory initial administrator password:

```bash
ADRO_ADMIN_PASSWORD='replace-with-a-strong-password' \
  docker compose -f deploy/compose/docker-compose.yml up --build
```

The operator CLI exposes the same deterministic bootstrap and a validation-only
mode:

```bash
adroctl install --profile single-node
adroctl install --profile single-node --dry-run
```

The Compose profile starts ADRO, its authentication state, workbench, and
filesystem artifact volume. `start.sh` can provision the separately licensed,
pinned Multica self-host distribution alongside it. PostgreSQL/NATS/Temporal,
OIDC, cloud ArtifactStore, and organization-specific production integrations
remain separate deployment choices.

Create a requirement:

```bash
curl -X POST http://localhost:8080/api/v1/requirements \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: req-demo-1' \
  -d '{"workspace_id":"local","title":"Invite API","description":"Add the invite endpoint","acceptance_criteria":["POST /invite returns code 0"],"assignee_member_ids":["alice","bob"],"repository_ids":["provider-service","caller-service"]}'
```

`POST /api/v1/requirements/{id}/start` advances the workflow to `TRIAGED` and creates one work item per confirmed repository, assigning them round-robin across the requested members. Bug creation computes a stable SHA-256 fingerprint when one is not supplied; repeated failures are deduplicated and automatic repair is capped at three attempts.

After a Work Item run, `GET /api/v1/work-items/{id}/context` exposes the
versioned `ContextManifest` and latest `Provenance`; repair history is available
from `GET /api/v1/work-items/{id}/repair-attempts`. A runner command is sent as
an argv array to `POST /api/v1/runners/{id}/execute`, never as a shell string.

Run the browser acceptance suite locally after installing Chromium:

```bash
npm install
npx playwright install chromium
npm run test:e2e
npx playwright install firefox webkit
npm run test:e2e:matrix
```

## Project layout

`internal/domain` contains business contracts and transitions; `internal/workflow` applies deterministic quality gates and repair limits; `internal/store` is the in-memory reference repository with an optional atomic JSON snapshot profile; `internal/provider` and `internal/artifact` are extension SPIs; `internal/events` is the outbox-compatible event bus; `internal/api` is the transport layer. No business package imports a Multica client.

`internal/observer` provides the runner-side Git Diff snapshotter. It records
changed files and a stable digest without putting repository contents into the
control-plane event payload. The same `DiffSnapshot` contract is used by the
HTTP endpoint and by future filesystem watchers.

## Security notes

Artifact addresses are logical `artifact://tenant/id/version` values and path traversal is rejected. Uploads are published atomically after hashing, immutable objects cannot be overwritten, legal-hold deletion is refused, and problem responses include a stable `error_code` and request ID. The local profile provides password login, hashed server-side sessions, per-user menu RBAC, administrator safeguards, and persisted identities. For an interactive session, the server overwrites member and workspace headers from the authenticated identity; browser-supplied identity headers cannot impersonate another account or select another workspace. Runner execution is a local command boundary, not a production sandbox: deployments must add rootless/container or VM isolation, federated OIDC, PostgreSQL RLS, mTLS, SecretStore and external ArtifactStore controls required by the blueprint.

## License

ADRO core is intended for Apache-2.0 distribution. A Multica adapter or full distribution must carry Multica's applicable license and NOTICE separately; the core profile does not redistribute Multica.
