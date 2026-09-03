# PostgreSQL backup and restore

The production orchestration profile uses PostgreSQL transaction boundaries,
row-level tenant/workspace policies, advisory locking, compare-and-swap state
revisions, and a durable outbox. Backup acceptance is intentionally performed
with PostgreSQL's operational tools rather than the in-process repository
export API.

Run the complete local rehearsal with:

```bash
make postgres-conformance
```

The script creates an isolated PostgreSQL cluster, runs the repository/RLS and
two-replica conformance test, takes a custom-format `pg_dump`, restores it into
a newly created database with `pg_restore`, and compares the exact durable
snapshot revision and content fingerprint. It emits JSON and Markdown evidence
under `var/test-report/postgres/` with measured RTO and record-level RPO.

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
