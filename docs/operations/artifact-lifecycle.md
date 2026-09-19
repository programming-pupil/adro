# Artifact Retention and Deletion Proofs

The local artifact store exposes verified metadata listing by tenant. `artifact.Lifecycle` maintains durable GC roots and legal holds, performs mark-and-sweep collection before a cutoff, and persists a deletion proof containing scanned, deleted, protected, failed, and key-destruction-pending sets.

A retained root or legal hold always wins over ordinary retention. The proof is content-addressed and can be revalidated after restart. The reference filesystem backend removes bytes but does not own an encryption key manager; deleted objects are therefore explicitly reported as `key_destruction_pending` until a production key-management adapter completes destruction. Event, snapshot, artifact, and legal-hold projections must register roots before collection.
