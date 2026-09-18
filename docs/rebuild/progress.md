# Rebuild Progress

Updated: 2026-09-18.

The authoritative issue TodoList remains the complete scope. This file records evidence for landed increments; absence from this table means not completed.

| Todo ID | State | Evidence |
|---|---|---|
| P0-BASE-001 | complete locally | Annotated tag `legacy-delivery-control-plane-20260917` at `2d480a87f0ecdf974ab09cdece2729f209a8e813` |
| P0-BASE-002 | partial | Git tag freezes database migrations, workspace fixtures, OpenAPI and tracked browser assets; generated screenshot fixture still pending |
| P0-BASE-003 | complete | `docs/rebuild/baseline-report.md` records exact-tag serial test, race, fault, benchmark discovery and isolated browser reruns |
| P0-BASE-004 | complete | `docs/rebuild/decision-log.md` |
| P0-CORE-001 | complete | `core/ids` typed IDs and validation |
| P0-CORE-002 | partial | Generic reducer/replay contract exists; legacy handlers are not migrated |
| P0-CORE-003 | partial | Canonical envelope v1 exists; the legacy runtime journal now maps into it in shadow mode, while other producers remain pending |
| P0-CORE-004 | partial | Runtime journal mapping and restart backfill exist in `internal/runtime/shadow.go`; Harness, Orchestration and Audit mappings remain pending |
| P0-CORE-006 | partial | Canonical JSON v1, golden digest and fuzz smoke |
| P0-CORE-009 | partial | `docs/rebuild/event-source-inventory.md`; the runtime journal has a legacy-authoritative EventStore shadow, while source cutover and the other producers remain pending |
| P0-CORE-010 | partial | Runtime shadow projections rebuild and compare canonical digests from sequence zero; all other projections remain pending |
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
| P0-EFFECT-001 | partial | Intent, prepare, dispatch, receipt, unknown-outcome and policy-valid reconciled facts in `internal/runtime/kernel.go`; external adapter reconcile implementations remain pending |
| P0-EFFECT-002 | complete for legacy ToolLoop | Intent commits before callback dispatch |
| P0-EFFECT-003 | partial | Effect ID, input digest, class, explicit reconcile policy and fence are durable; adapter idempotency key contract pending |
| P0-EFFECT-006 | partial | Journal enforces query/compensate/human/unrecoverable decisions and idempotent resolution; concrete external reconcile adapters remain pending |
| P0-EFFECT-005 | complete for legacy ToolLoop | Dispatched writes without receipts return `ErrEffectOutcomeUnknown` and are not replayed |
| P0-EFFECT-007 | partial | Effect receipt and tool terminal event commit atomically; final checkpoint integration pending |
| P0-STORE-001 | partial | EventStore and LeaseStore ports exist; Snapshot/Blob/Secret/Projection ports remain pending |
| P0-STORE-002 | complete for EventStore | `adapters/eventstore/sqlite` is explicitly single-node and does not claim HA |
| P0-STORE-003 | complete for EventStore | `adapters/eventstore/postgres`; migrations 001-015 apply together; real PostgreSQL 17 lock-wait, conformance and restore evidence |
| P0-STORE-004 | complete for EventStore/LeaseStore | SQLite and PostgreSQL run `conformance/eventstore` |
| P0-STORE-005 | complete for both backends | Expected-sequence CAS, concurrent single-winner and concurrent same-key replay evidence |
| P0-STORE-006 | complete for both EventStores | Event, outbox and terminal snapshot share one rollback-tested transaction |
| P0-STORE-015 | partial | Composite tenant/stream foreign keys reject cross-tenant EventStore rows; authenticated scoped read/RLS ports remain pending |
| P0-EVAL-004 | partial | Shared suite covers CAS, idempotency, atomicity, lease fencing, concurrency, restart, subscription, tenant isolation and corruption; migration crash/resume and tail repair pending |
| P0-TIMER-001 | partial | Durable reference `internal/runtime/TimerStore` persists timer records and survives restart; timeout/retry/approval/sleep call sites are not all migrated |
| P0-TIMER-002 | complete locally | `Timer` persists UTC due time, command digest, stream sequence, generation, owner, state, lease expiry and fencing token in `internal/runtime/timer.go` |
| P0-TIMER-003 | complete locally | Atomic `ClaimDue`, fencing-token takeover, occurrence-key idempotency and release/ack tests in `internal/runtime/timer_test.go` |
| P0-TIMER-004 | partial | Manual-clock ordering, clock jump, DST normalization and leap-second-shaped input tests exist; broader cross-process and calendar compatibility fixtures remain pending |
| P0-TIMER-005 | complete locally for reference backend | Bounded catch-up, coalesce, suppression and expiry are persisted and tested; integration with every runtime retry/deadline path remains pending |
| P0-TIMER-006 | partial | `TimerExplanation` is available as a read model; Runtime Inspector/API/CLI listing and cancellation endpoints remain pending |
| P0-LOOP-001 | partial | `internal/runtime/kernel.go` now has a durable model lifecycle projection; Session/Turn/Step and checkpoint integration remain pending |
| P0-LOOP-003 | partial | `ModelRequest` canonical prompt/config/context/policy digest and Journal replay tests exist; all provider adapters are not migrated |
| P0-LOOP-009 | partial | `ModelRequest` freezes request digest, adapter identity, attempt and idempotency key before dispatch; config snapshot persistence is still a contract-level digest |
| P0-LOOP-010 | partial | `model.outcome_unknown` blocks late stream frames and redispatch; provider query/human recovery implementation remains pending |
| P0-LOOP-011 | partial | Model event validation distinguishes stream frames, finish and provider error; full step reducer and cancellation taxonomy remain pending |
| P0-MODEL-001 | partial | Versioned `ModelEvent` types and cursor validation in `internal/runtime/model.go`; adapter migration and API wire compatibility remain pending |
| P0-MODEL-003 | complete locally for reference contract | `ProviderCapabilities` validates protocol/model/features fail-closed with tests |
| P0-MODEL-010 | complete locally for reference contract | `ModelRequest`, `ModelEvent`, `ProviderCapabilities`, `ContinuationToken` and `Usage` are defined and tested |
| P0-MODEL-011 | complete locally for reference contract | Canonical request digest and idempotency conflict tests |
| P0-MODEL-015 | complete locally for reference contract | Incompatible protocol/model/capability negotiation is rejected without adapter-name inference |
| P0-STREAM-001 | partial | Monotonic event sequence and cursor with replay/gap tests; persistent stream adapter and all transport mappings remain pending |
| P0-STREAM-002 | complete locally for reference stream | Bounded capacity and explicit block/drop/disconnect policies are implemented and tested |
| P0-STREAM-003 | complete locally for reference stream | Retention and dropped-optional-delta conditions return structured gap errors |
| P0-STREAM-004 | partial | UTF-8 and complete canonical tool argument validation exists; provider chunk assembler integration remains pending |
| P0-STREAM-005 | complete locally for reference contract | Distinct text/reasoning/tool/usage/finish/error frame types and terminal validation |
| P0-STREAM-007 | partial | Context cancellation is available at gateway boundary; adapter socket/goroutine cleanup evidence remains pending |
| P0-STREAM-009 | partial | Reference tests cover retention gap, optional drop, overflow disconnect, duplicate/out-of-order rejection; provider matrix remains pending |
