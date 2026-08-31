# GA Readiness Matrix

This matrix describes the independent ADRO release. `implemented` means the
repository has executable code and tests; `reference-only` means an SPI or
contract is present but a production adapter is intentionally external;
`blocked` means an enterprise deployment still needs infrastructure evidence.

| Area | Status | Evidence / gate |
| --- | --- | --- |
| Requirements, bugs, work items, idempotency | implemented | domain/store/API tests |
| Multi-tenant and multi-project authorization | implemented | authenticated API and persistence tests |
| Agent routing and role orchestration | implemented | immutable bindings and route tests |
| Seven-stage pipeline and same-session repair | implemented | pipeline engine/API tests and context manifests |
| Durable harness transcript and context recovery | implemented for local profile | hash-linked turns, checkpoints, exact archive windows, recovery API and restart tests |
| Lease/outbox crash recovery | implemented for local profile | durable claim/ack/nack, expiry requeue and dispatcher fault-injection tests |
| Local client discovery and real process boundary | implemented | `LocalProvider`, `start.sh`, real Claude version/readiness smoke |
| Run snapshot, git baseline/head, checks and usage | implemented for local process | captured from process exit and git; external CI/Git adapters remain optional |
| Evidence, artifacts, attachment receipt and audit | implemented | filesystem artifact and API tests |
| MCP, Skill, Automation and signed plugin governance | implemented for control plane | manifests, digest/signature verification, activation, health and quarantine are durable; plugin binaries remain external |
| GitHub/Git/CI/deploy integrations | reference-only | SPI and evidence model exist; credentials and adapters are deployment inputs |
| Durable single-node state | implemented | atomic mode-0600 JSON snapshots and restart tests |
| PostgreSQL/RLS, NATS, Temporal, cloud artifacts | blocked | production adapters are not shipped in this profile; the harness SDK and migration boundary are ready |
| Local identity, login and menu RBAC | implemented | auth and browser tests |
| OIDC/mTLS/enterprise secret management | blocked | install and conformance external adapters before GA |
| Web workbench and responsive menus | implemented | HTML checks and Playwright matrix |
| Load, disaster recovery and multi-node upgrades | blocked | collect measured RTO/RPO and cross-node adapter evidence; local crash recovery is covered |
| Open-source license/SBOM/release traceability | reference-only | run release scripts from a clean tagged checkout |

The local profile is suitable for evaluation and real client smoke tests. A
production claim must name every installed plugin, client version, repository,
security control, and evidence report; passing deterministic tests alone is not
enough.
