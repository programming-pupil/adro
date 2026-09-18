# ADR-0005: PostgreSQL Implements the Production EventStore Contract

Date: 2026-09-17
Status: accepted
Source classification: `ADRO-origin`

## Decision

ADRO implements the authoritative EventStore and LeaseStore ports in `adapters/eventstore/postgres` using PostgreSQL row locks and transactions.

Append locks the stream row with `FOR UPDATE`, checks expected sequence, locks and verifies an optional lease/fencing assertion, writes canonical events, the outbox, and an optional terminal snapshot, then advances the stream head in one transaction. Lease decisions sample PostgreSQL database time only after acquiring the lease row lock, so lock waits and worker clock disagreement cannot revive an expired claim. Reads use repeatable-read transactions and verify sequence, indexed fields, payload/envelope digests, and the complete hash chain.

The PostgreSQL and SQLite adapters run the same public `conformance/eventstore` suite. The production queue is named `runtime_event_outbox` so migration 015 can coexist with the legacy `event_outbox` until cutover. The PostgreSQL operational gate applies migrations 001 through 015, verifies tenant/stream foreign keys, forces a lease lock wait across expiry, seeds a valid event/outbox/snapshot/lease bundle, performs `pg_dump` and `pg_restore`, and compares a fingerprint that includes both the legacy orchestration snapshot and the new EventStore tables.

## Consequences

- PostgreSQL is a delivered backend rather than a migration-only placeholder.
- Concurrent retries of the same idempotency key converge on one committed event identity.
- Fencing tokens remain monotonic across release and reacquisition.
- The backend is not yet the application write source; legacy paths remain authoritative until shadow dual-write and projection-digest evidence permit cutover.
- Multi-region ownership, PITR/failover drills, migration crash/resume, tail repair, scoped read ports, and retention/archive work remain required before a stable production claim.
