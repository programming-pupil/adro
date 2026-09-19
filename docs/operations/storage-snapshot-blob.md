# Snapshot and blob storage contract

Design source: `ADRO-origin`.

`ports/snapshot` and `ports/blobstore` keep replay acceleration and large
content outside the authoritative event stream. A snapshot is a verified,
versioned cache: `Put` uses an expected-sequence CAS, accepts an exact
idempotent retry, and rejects regressions or a changed payload. `Get` verifies
the SHA-256 payload digest before returning it; a corrupt snapshot is an
explicit failure so recovery can rebuild it from events.

`adapters/snapshot/filesystem` writes JSON snapshots through a synced temporary
file and atomic rename. The reference adapter is single-node and is suitable
for local recovery tests; it is not presented as an HA backend.

`ports/blobstore` stores only content-addressed identity and policy metadata in
events. `adapters/blob/filesystem` streams into a bounded temporary file,
fsyncs before publication, deduplicates identical tenant-scoped digests, checks
content integrity on `Open`/`Stat`, and keeps tombstoned bytes inaccessible.
Legal-hold metadata blocks tombstoning. Encryption keys are references in the
metadata; the filesystem adapter does not claim to provide encryption itself.

The current batch covers the reference ports and fault tests. Production
object storage, envelope encryption, mark-and-sweep GC, archive retention,
proof-of-deletion and cross-process lease coordination remain required before
these capabilities can be called stable.
