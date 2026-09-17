# Rebuild Progress

Updated: 2026-09-17.

The authoritative issue TodoList remains the complete scope. This file records evidence for landed increments; absence from this table means not completed.

| Todo ID | State | Evidence |
|---|---|---|
| P0-BASE-001 | complete locally | Annotated tag `legacy-delivery-control-plane-20260917` at `2d480a87f0ecdf974ab09cdece2729f209a8e813` |
| P0-BASE-002 | partial | Git tag freezes database migrations, workspace fixtures, OpenAPI and tracked browser assets; generated screenshot fixture still pending |
| P0-BASE-003 | complete | `docs/rebuild/baseline-report.md` records exact-tag serial test, race, fault, benchmark discovery and isolated browser reruns |
| P0-BASE-004 | complete | `docs/rebuild/decision-log.md` |
| P0-CORE-001 | complete | `core/ids` typed IDs and validation |
| P0-CORE-002 | partial | Generic reducer/replay contract exists; legacy handlers are not migrated |
| P0-CORE-003 | partial | Canonical envelope v1 exists; legacy event producers are not mapped yet |
| P0-CORE-006 | partial | Canonical JSON v1, golden digest and fuzz smoke |
| P0-CORE-009 | inventory complete | `docs/rebuild/event-source-inventory.md`; source cutover remains pending |
| P0-CORE-013 | partial | Dependency interfaces and deterministic testkit exist; legacy reducers still call system sources |
| P0-CORE-014 | partial | Manual clock, sequence IDs/random and recording sleeper tests |
| P0-CORE-015 | partial | Canonical map/number/Unicode encoding tests; cross-process fixtures pending |
| P0-CORE-016 | partial | Event envelope carries encoding and hash identities; snapshot/blob/manifest migration pending |
| P0-CORE-017 | partial | Reducer byte-determinism test exists; runtime state machines pending |
| P0-LIFE-001 | complete | Lifecycle component contract |
| P0-LIFE-002 | complete | Missing dependency, duplicate and cycle validation with stable order |
| P0-LIFE-003 | complete | Topological start, reverse stop and partial-start rollback |
| P0-LIFE-005 | partial | Root cancellation propagation is implemented; model/tool/sub-agent integration pending |
| P0-LIFE-006 | partial | Health contract and aggregation exist; readiness endpoints pending |
| P0-LIFE-009 | partial | Core lifecycle conformance tests exist; leak and file-descriptor checks pending |
| P0-EFFECT-001 | partial | Intent, prepare, dispatch, receipt and unknown-outcome facts; reconcile states pending |
| P0-EFFECT-002 | complete for legacy ToolLoop | Intent commits before callback dispatch |
| P0-EFFECT-003 | partial | Effect ID, input digest, class and fence are durable; adapter idempotency key contract pending |
| P0-EFFECT-005 | complete for legacy ToolLoop | Dispatched writes without receipts return `ErrEffectOutcomeUnknown` and are not replayed |
| P0-EFFECT-007 | partial | Effect receipt and tool terminal event commit atomically; final checkpoint integration pending |
| P0-STORE-001 | partial | EventStore and LeaseStore ports exist; Snapshot/Blob/Secret/Projection ports remain pending |
| P0-STORE-002 | complete for EventStore | `adapters/eventstore/sqlite` is explicitly single-node and does not claim HA |
| P0-STORE-004 | partial | Public store conformance runs against SQLite; PostgreSQL remains pending |
| P0-STORE-005 | complete for SQLite | Expected-sequence CAS and concurrent single-winner evidence |
| P0-STORE-006 | complete for SQLite EventStore | Event, outbox and terminal snapshot share one rollback-tested transaction |
| P0-EVAL-004 | partial | Store suite covers CAS, idempotency, atomicity, lease fencing, concurrency, restart, subscription, tenant isolation and corruption; migration/tail repair/PostgreSQL pending |
