# Snapshot and blob storage contract

Design source: `ADRO-origin`.

`ports/snapshot` and `ports/blobstore` keep replay acceleration and large
content outside the authoritative event stream. A snapshot is a verified,
versioned cache: `Put` uses an expected-sequence CAS, accepts an exact
idempotent retry, rejects regressions or a changed payload, and enforces a
bounded payload size. `Get` verifies the SHA-256 payload digest before
returning it; a corrupt snapshot is an explicit failure so recovery can rebuild
it from events. Every PostgreSQL read and write requires a verified tenant
scope.

`adapters/snapshot/filesystem` writes JSON snapshots through a synced
temporary file and atomic rename. Its path is derived from both the
authenticated tenant and stream, and `Get`/`Put` reject an absent or mismatched
tenant scope. The reference adapter is single-node and is suitable for local
recovery tests; it is not presented as an HA backend.

`adapters/blob/filesystem` streams into a bounded temporary file, fsyncs before
publication, deduplicates identical tenant-scoped digests, checks content
integrity on `Open`, `Stat`, and inventory scans, and keeps tombstoned bytes
inaccessible. `List` and `Purge` form the inventory boundary used by the
mark-and-sweep lifecycle worker. Legal-hold metadata blocks tombstoning and
physical deletion.

`ports/blob/postgres` is the production reference boundary for PostgreSQL. It
stores tenant-scoped content-addressed rows, validates plaintext digests on
idempotent replay and inventory scans, supports optional AES-GCM envelope
bytes through a caller-owned key resolver, and separates tombstone from
physical purge. It also stores a SHA-256 `stored_digest` for the bytes held by
the database. Inventory verifies that digest for tombstoned rows without
requiring a decryption key; rows created before migration 017 have no proof
and are rejected by the inventory boundary until they are rewritten. The
encryption key is a reference only; key material never enters events or
database metadata.

`internal/artifact.BlobLifecycle` performs two-phase collection: an unrooted
object is tombstoned on the first pass and physically purged only after its
marker is observed on a later pass. Roots, legal holds, failed operations, and
a content-addressed deletion proof are durable and reloadable. A key-manager
integration must separately record destruction; deleting bytes alone is not
reported as key destruction.

The PostgreSQL migrations are `migrations/016_runtime_snapshot_blob.sql` and
`migrations/017_runtime_blob_stored_digest.sql`.
Production object-storage adapters, cross-process lifecycle leases, backup/
restore proof, and full EventStore projection wiring remain release gates.
