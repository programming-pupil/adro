# Capability Evidence Matrix

This matrix is intentionally conservative. `stable` is prohibited until implementation, conformance, fault evidence, and documentation all exist.

| Capability | Status | Implementation | Conformance / fault evidence | Documentation | Missing before stable |
|---|---|---|---|---|---|
| Deterministic dependencies | experimental | `core/dependencies.go`, `core/testkit/dependencies.go` | `core/testkit/dependencies_test.go`, reducer deterministic test | Development design section 6 | Production adapters and broader reducer migration |
| Canonical JSON v1 | experimental | `core/encoding/canonical.go` | Golden digest, equivalence test, fuzz smoke | Development design section 6.2 | Cross-process fixtures, duplicate-key policy, N-2 compatibility |
| Authoritative event envelope v1 | experimental | `core/event/envelope.go` | Digest/tamper tests | ADR-0001 | EventStore backend conformance and upcasters |
| EventStore port | reference backend | `ports/eventstore/eventstore.go`, `adapters/eventstore/sqlite` | Shared CAS, idempotency, atomicity, fencing, concurrency, restart, subscription, tenant and corruption conformance | ADR-0001, ADR-0004 | PostgreSQL backend, migration crash/resume, tail repair and shadow divergence evidence |
| LeaseStore port | experimental | `ports/leasestore/leasestore.go`, `adapters/eventstore/sqlite/lease.go` | Acquire/busy/takeover/renew/release and stale append fencing in shared store conformance | ADR-0004 | PostgreSQL implementation and distributed failover evidence |
| Pure reducer contract | experimental | `core/reducer/reducer.go` | Deterministic decision and replay tests | Development design section 7.3 | Session/Turn/Step reducers and illegal transition tables |
| Lifecycle manager | experimental | `runtime/lifecycle/manager.go` | Topology, rollback, cancellation, repeated stop, error aggregation | Development design section 5 | Leak tests, health events, production component integration |
| Durable tool effect | partial | `internal/runtime/kernel.go`, `internal/runtime/loop.go` | Unknown write outcome, idempotent intent, stale/race tests | ADR-0002 | Reconcile states, durable store rows, adapter contracts, recovery UI |
