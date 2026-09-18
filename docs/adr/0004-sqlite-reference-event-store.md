# ADR-0004: SQLite Is the Single-Node Reference EventStore

Date: 2026-09-17
Status: accepted
Source classification: `ADRO-origin`

## Decision

ADRO uses `adapters/eventstore/sqlite` as the reference implementation of the authoritative EventStore contract for a single-node runtime profile.

Each append executes under `BEGIN IMMEDIATE` and atomically applies expected-sequence CAS, optional lease/fencing validation, canonical event commits, outbox inserts, an optional terminal snapshot, and stream-head advancement. Reads verify sequence continuity, envelope and payload digests, indexed integrity fields, and the hash chain. Corruption fails closed.

SQLite uses foreign keys, WAL mode, full synchronous commits, and one owned writer connection. It is not a high-availability, active-active, or multi-region backend.

The public conformance suite in `conformance/eventstore` is backend-neutral. PostgreSQL implements the same contract under ADR-0005.

## Consequences

- Local durable-runtime development has a real SQL transaction boundary instead of an in-memory approximation.
- Idempotent replay returns the original committed envelopes and rejects a reused key with different content.
- A stale fencing token cannot append events, outbox messages, or snapshots.
- Event, outbox, and snapshot writes roll back together on any failure.
- Snapshot rows remain disposable caches; events and their hash chain remain authoritative.
- Tail repair, schema migration crash recovery, archival, and shadow cutover remain separate work.
