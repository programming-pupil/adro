# Artifact Retention and Deletion Proofs

The local artifact store requires an authenticated tenant scope for every object operation (`Put`, `Open`, `Stat`, `Delete`, and inventory `List`); the object key is never treated as authority. Cross-tenant and unscoped calls fail closed before the filesystem is touched. Existing metadata is checked against the requested key and a content-addressed object with missing or corrupt metadata cannot be overwritten.

`artifact.Lifecycle` maintains durable GC roots and legal holds, performs mark-and-sweep collection before a cutoff, and persists a deletion proof containing scanned, deleted, protected, failed, and key-destruction-pending sets. Rollback paths in workspace migration carry the imported tenant scope when removing partially written payloads, so a later control-plane failure cannot leave orphaned artifacts.

A retained root or legal hold always wins over ordinary retention. The proof is content-addressed and can be revalidated after restart. The reference filesystem backend removes bytes but does not own an encryption key manager; deleted objects are therefore explicitly reported as `key_destruction_pending` until a production key-management adapter completes destruction. Event, snapshot, artifact, and legal-hold projections must register roots before collection.
