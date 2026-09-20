# Durable projection store

A projection is a rebuildable read model. The event stream remains authoritative;
projection rows only cache a derived state and therefore carry the source stream,
source sequence, version, and a SHA-256 digest of the serialized payload.

`ports/projection.Store` is implemented by the deterministic in-memory adapter,
the single-node SQLite adapter, and the PostgreSQL adapter. Every read and write
requires a verified tenant in `scope.WithTenant(ctx, tenantID)`. A missing or
mismatched scope fails closed before the backend is queried.

`Put` uses an explicit compare-and-swap version. A new key must use version 1
with expected version 0. An update must advance both the row version and source
sequence on the same source stream. Retrying the identical record at its current
version is idempotent; stale, out-of-order, cross-stream, or conflicting writes
return `projection.ErrConflict`.

Adapters verify the stored digest and payload size on every read, list, and
update. Corrupt rows return `projection.ErrCorrupt` rather than being silently
overwritten. SQLite uses one writer connection and `BEGIN IMMEDIATE`; PostgreSQL
locks the row with `SELECT ... FOR UPDATE`. Both schemas include tenant in the
primary key and source index.

The shared suite in `conformance/projection` runs the same CAS, replay, ordering,
scope, integrity, identity, deletion, and timestamp checks against the reference
and SQLite adapters.
PostgreSQL runs the suite when `ADRO_POSTGRES_TEST_DSN` is supplied. The schema
is also captured in `migrations/018_runtime_projection.sql` for deployments that
apply migrations separately from adapter startup.
