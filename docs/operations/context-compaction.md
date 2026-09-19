# Context Diff and Compaction Lineage

`internal/context` treats a manifest as the immutable description of the model-visible world for one step. `Manifest.Diff` compares two validated manifests by session, version, digest, token accounting, block identity, provenance, selection metadata, and mandatory status. The result never embeds block content, so it can be stored in diagnostics or exported safely and regenerated during replay.

A compaction evidence record must connect the source manifest, compacted manifest, source window digest, immutable archive reference, summary digest, and a recall probe. `BuildCompactionLineage` rejects a missing archive, a non-reducing summary, missing mandatory facts, or an unverified recall probe. The source window remains external immutable evidence; a summary cannot overwrite it.

The reference compiler keeps tool transactions atomic and never truncates Unicode or mandatory blocks. Provider-native caching must use the manifest digest and tokenizer identity as its cache identity. Production object storage, key destruction, and provider cache adapters remain separate deployment work and must not be inferred from the local reference implementation.
