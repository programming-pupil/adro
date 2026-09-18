# ADR-0001: Authoritative Event Log

- Status: accepted
- Date: 2026-09-17
- Design source: `ADRO-origin`

## Context

Runtime journal, transcript, delivery bus, audit ledger, and orchestration events can all currently receive writes. A crash or retry can therefore leave several plausible histories with no deterministic authority.

## Decision

ADRO will have one authoritative event stream behind `ports/eventstore.Store`. Runtime state, transcript, audit, API views, WebUI timelines, cost, and search are rebuildable projections. Snapshots only accelerate replay and cannot resolve conflicts.

An append atomically enforces expected sequence and lease fencing, appends events, inserts outbox messages, and updates an optional checkpoint pointer. SQLite is the reference single-node adapter; PostgreSQL must pass the same conformance suite before production claims are allowed.

## Consequences

- Existing stores migrate through shadow writes and projection digest comparison.
- The legacy runtime journal shadow compares from sequence zero, refuses to extend a divergent prefix, and emits no outbox messages.
- The event delivery bus cannot originate business facts.
- Unknown schema versions fail closed unless an explicit upcaster exists.
- A rollback cannot use a binary that ignores events already committed under a newer schema.
