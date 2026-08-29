# GA Readiness Matrix

This matrix is the release gate for the implementation described in
`ADRO-production-blueprint.zh-CN.md`. A green test suite proves the local
reference profile; it does not prove that an external production dependency is
connected. Status values are deliberately explicit:

- `implemented`: exercised by local code and contract tests.
- `reference-only`: a stable contract or deterministic mock exists, but the
  production adapter is not shipped or verified here.
- `blocked`: acceptance requires an external system, a security boundary, or
  infrastructure that is not present in this repository/runtime.

| Blueprint acceptance area | Status | Evidence / remaining gate |
| --- | --- | --- |
| Requirement state, optimistic version, idempotency | implemented | `internal/domain`, `internal/store`, `internal/api/server_test.go` |
| Multi-assignee, multi-repository Work Items | implemented | `internal/api/server.go` materialization and API tests |
| Multi-Agent routing and auditable diagnostics | implemented | strict `ADRO_MULTICA_AGENT_MAP`, immutable WorkItem bindings, redacted diagnostics, precedence/concurrency/API contract tests |
| Gates, Evidence, Provenance, Bug fingerprint, RepairBrief, repair cap and ContextManifest continuity | implemented | `internal/workflow`, `internal/domain`, `internal/store`, API context/repair tests |
| Repository graph, Runner registration/capacity, argv execution boundary, audit chain | implemented | `internal/store`, `internal/runner`, `internal/audit`, runner API tests; production sandbox isolation remains a separate gate |
| MCP/Skill/Automation governance and Agent bindings | reference-only | CRUD/audit contracts exist; signed extension processes and durable history remain |
| Provider SPI and MockProvider | implemented | `internal/provider/provider.go` and tests |
| Real Multica Agent/Issue/Runtime/run integration | reference-only | Real `/api/config` + readiness, workspace/runtime discovery, native Agent/Issue creation, and `/api/issues/{id}/rerun` fallback are covered by pinned `v0.4.35` HTTP contracts; generic run snapshot/messages/cancel/usage and live daemon conformance remain unavailable |
| Native Multica WebSocket event bridge | reference-only | `MulticaProvider` supports an explicit `ADRO_MULTICA_WS_URL` stream with cursor/auth; upstream daemon conformance is still unverified |
| Real MCP HTTP/SSE transport and capability-aware invocation | implemented | `internal/mcp`, schema/health/invocation API tests; provider-specific auth, signing and egress policy remain deployment gates |
| Git/CI/deploy/log/data integrations and production-isolated Runner execution | blocked | integration SPI and local argv boundary exist; no external systems or rootless/container/VM sandbox runner is shipped |
| Filesystem ArtifactStore, hash, immutable, Range/HEAD | implemented | `internal/artifact` tests |
| S3/cloud ArtifactStore drivers and contract suite | blocked | only filesystem driver is included |
| Online Artifact migration worker, double-write, resume, rollback window | reference-only | migration state and digest-copy contracts exist; no worker is wired to API |
| Durable single-node JSON persistence for control-plane/event/audit/runner/context state | implemented | `ADRO_*_STATE_FILE`, atomic mode-0600 snapshots, restart round-trip tests; single-process profile only |
| PostgreSQL persistence/RLS, NATS JetStream, Temporal | blocked | SQL migration and event publisher boundaries only |
| Local identity, login, sessions, menu RBAC | implemented | persistent PBKDF2 credentials, hashed server-side sessions, login throttling, last-admin guard, per-user 18-menu authorization, API/Playwright tests |
| Federated OIDC/ABAC, mTLS, SecretStore, OPA | blocked | local identity is not an enterprise federation or workload-identity boundary |
| Compose/Helm and filesystem local profile | implemented | one replica, Recreate upgrade, values schema and startup validation fail closed on HA/unshipped adapters; `deploy/compose`, `charts/adro`, `adroctl install --profile single-node` |
| Zero-to-one local/remote stack including Multica and daemon | reference-only | `start.sh` checksum-verifies and bootstraps pinned self-host Multica with Docker; without Docker it can now connect directly to a remote Multica URL/PAT and pins workspace/runtime/project, but first-time credentials and the remote deployment remain external prerequisites |
| Web UI identity, forms, menu coverage and responsive workbench | implemented | login/logout, administrator user/menu matrix, project/executor selectors, requirement/bug status, related requirement and attachments, runner registration/execute action plus 18 data-backed views and i18n |
| Web UI screenshot capture, immutable Artifact and provider delivery | implemented | `POST /api/v1/screenshots`, `AttachmentPublisher`, and Playwright screenshot flow; remote delivery is capability-gated |
| Next.js/React/Monaco enterprise console | blocked | the dependency-free HTML workbench is intentionally the reference UI |
| Playwright E2E and browser matrix | implemented | full Chromium workflow plus desktop Chromium/Firefox/WebKit and mobile Chromium/WebKit smoke; physical device lab remains blocked |
| Load, disaster recovery, upgrade/rollback tests | blocked | fail-closed runbook exists; no measured RTO/RPO, cross-AZ or external fault-injection evidence is shipped |
| Public ADRO Bench results and extension conformance suites | reference-only | real Multica executable suite now emits pass/blocked/fail JSON; no passing upstream report or public benchmark result is available in this runtime |
| SPDX SBOM, notices, licenses and release traceability | reference-only | dependency-derived artifacts verify locally; commit/tag/tree manifest generation refuses this export because `.git` is unavailable |
| License, governance, security, threat model, support docs | implemented | repository policy and Apache-2.0 core metadata |

The release is not GA while any `blocked` row is part of the target deployment.
The local profile is suitable for offline evaluation and adapter development;
production claims must include the adapter version, dependency versions,
contract-test results, benchmark data, and the configured security controls.
