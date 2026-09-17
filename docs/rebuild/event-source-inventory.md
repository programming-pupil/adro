# Event And State Source Inventory

Design source: `ADRO-origin`.

The repository currently has five independently writable execution records. During migration only the new `ports/eventstore.Store` contract is allowed to become authoritative. Existing stores remain compatibility sources until their projections and fault behavior have golden coverage.

| Current source | Current write path | Valuable behavior | Target role | Cutover condition |
|---|---|---|---|---|
| Runtime journal | `internal/runtime/kernel.go`, `internal/runtime/shadow.go` | Hash chain, batch append, lease/fencing, terminal checkpoint boundary | Legacy-authoritative compatibility producer with side-effect-free EventStore shadow | Continuous divergence-free window, fault conformance, rollback proof and read cutover |
| Harness transcript/checkpoint | `internal/harness/store.go` | Transcript integrity, compaction, checkpoint and recovery fixtures | Model-visible transcript and checkpoint projection over events/blob content | Prompt/context fixtures replay from sequence 0 |
| Event bus | `internal/events/events.go` | Cursor, ACK, bounded subscribers, gap signaling | Delivery and subscription adapter only | No business fact can originate in the bus |
| Audit ledger | `internal/audit/ledger.go` | Append-only security hash chain | Security projection derived from authoritative events | Audit digest can rebuild from the event archive |
| Orchestration events | `internal/orchestration/events.go`, repository append methods | DAG transition history, outbox and lease checks | Execution-plan events in the shared envelope/store | Graph fixtures and repository conformance use the shared stream |

## Rules During Shadow Migration

1. Legacy writes may shadow into the new EventStore, but shadow events cannot drive external side effects.
2. Projection digests must be compared from sequence zero; snapshots are caches and cannot resolve divergence.
3. No new business fact may be added to the event bus, audit ledger, or transcript as an independent source.
4. A source is removed only after compatibility, rollback, and durable-boundary fault tests pass.

The first migration implementation is configured by
`ADRO_EVENTSTORE_SHADOW_DRIVER` and `ADRO_EVENTSTORE_SHADOW_DSN`; operational
details and cutover restrictions are in
`docs/operations/runtime-eventstore-shadow.md`.
