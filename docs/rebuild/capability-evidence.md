# Capability Evidence Matrix

This matrix is intentionally conservative. `stable` is prohibited until implementation, conformance, fault evidence, and documentation all exist.

| Capability | Status | Implementation | Conformance / fault evidence | Documentation | Missing before stable |
|---|---|---|---|---|---|
| Deterministic dependencies | experimental | `core/dependencies.go`, `core/system.go`, `core/testkit/dependencies.go` | `core/system_test.go`, `core/testkit/dependencies_test.go`, reducer deterministic test | Development design section 6 | Production Random/Sleeper/Backoff adapters and broader reducer migration |
| Canonical JSON v1 | experimental | `core/encoding/canonical.go` | Golden digest, equivalence test, fuzz smoke | Development design section 6.2 | Cross-process fixtures, duplicate-key policy, N-2 compatibility |
| Authoritative event envelope v1 | experimental | `core/event/envelope.go` | Digest/tamper tests | ADR-0001 | EventStore backend conformance and upcasters |
| EventStore port | dual-backend experimental | `ports/eventstore/eventstore.go`, `adapters/eventstore/sqlite`, `adapters/eventstore/postgres`, `internal/runtime/shadow.go` | Shared CAS, same-key concurrency, idempotency, atomicity, fencing, restart, subscription, tenant and corruption conformance; runtime mirror/retry/divergence/backfill/no-outbox tests; full migration-chain and PostgreSQL backup/restore fingerprint | ADR-0001, ADR-0004, ADR-0005, runtime shadow runbook | Continuous divergence-free window, migration crash/resume, tail repair, scoped reads and authoritative cutover evidence |
| LeaseStore port | dual-backend experimental | `ports/leasestore/leasestore.go`, SQLite and PostgreSQL lease adapters | Shared acquire/busy/takeover/renew/release, monotonic reacquisition and stale append fencing; PostgreSQL lock-wait expiry regression | ADR-0004, ADR-0005 | Distributed failover, stale-region and long-duration soak evidence |
| Pure reducer contract | experimental | `core/reducer/reducer.go` | Deterministic decision and replay tests | Development design section 7.3 | Session/Turn/Step reducers and illegal transition tables |
| Lifecycle manager | experimental | `runtime/lifecycle/manager.go` | Topology, rollback, cancellation, repeated stop, error aggregation | Development design section 5 | Leak tests, health events, production component integration |
| Durable tool effect | partial | `internal/runtime/kernel.go`, `internal/runtime/loop.go` | Unknown write outcome, idempotent intent, stale/race tests | ADR-0002 | Reconcile states, durable store rows, adapter contracts, recovery UI |
