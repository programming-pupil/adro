# PostgreSQL backup and restore

The production orchestration profile and the new authoritative EventStore use
PostgreSQL transaction boundaries. The orchestration repository adds row-level
tenant/workspace policies and advisory locking; the EventStore adds row-lock
sequence CAS, database-time lease fencing, hash-chain verification, snapshots,
and a durable outbox. Backup acceptance is intentionally performed with
PostgreSQL's operational tools rather than an in-process export API.

Run the complete local rehearsal with:

```bash
make postgres-conformance
```

The script creates an isolated PostgreSQL cluster, applies the complete ordered
migration set, and then runs shared EventStore conformance plus the
repository/RLS and two-replica tests. Migration 015 uses
`runtime_event_outbox`, leaving the legacy `event_outbox` intact during shadow
operation. The gate also verifies tenant/stream foreign keys and database-time
lease behavior after a blocked row lock. It seeds a valid
event/outbox/snapshot/lease bundle, takes a custom-format `pg_dump`, restores it
into a newly created database with `pg_restore`, and compares a fingerprint of
the orchestration snapshot and EventStore tables. It emits JSON and Markdown
evidence under `var/test-report/postgres/` with measured RTO and record-level
RPO.

For an existing test server, provide all four values so the restore cannot
silently target the source database:

```bash
ADRO_POSTGRES_TEST_DSN='postgres://.../adro_test?sslmode=require' \
ADRO_POSTGRES_BACKUP_DSN='postgres://backup-role@.../adro_test?sslmode=require' \
ADRO_POSTGRES_ADMIN_DSN='postgres://.../postgres?sslmode=require' \
ADRO_POSTGRES_RESTORE_DSN='postgres://.../adro_restore?sslmode=require' \
ADRO_POSTGRES_RESTORE_DB='adro_restore' \
make postgres-conformance
```

The restore database name is restricted to a simple PostgreSQL identifier.
The script drops only that explicit restore database, never the source. A
fingerprint mismatch, missing tool, incomplete DSN set, failed restore, or RLS
conformance failure exits non-zero and cannot be reported as a pass.

Because orchestration tables use `FORCE ROW LEVEL SECURITY`, the application
identity is intentionally unable to dump all workspaces. `ADRO_POSTGRES_BACKUP_DSN`
must use a separately audited role with read and `BYPASSRLS` privileges (or a
platform-managed equivalent). Do not disable RLS for backup convenience.
