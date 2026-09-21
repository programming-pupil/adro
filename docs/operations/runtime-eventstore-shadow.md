# Runtime EventStore shadow migration

The runtime shadow is an M2 migration control. The legacy runtime journal
remains authoritative while each committed journal scope is mirrored into the
canonical EventStore and compared from sequence zero. Shadow records never
create outbox messages and never authorize tools, model calls, or external
effects.

## Enable SQLite shadowing

```bash
ADRO_RUNTIME_JOURNAL=true \
ADRO_EVENTSTORE_SHADOW_DRIVER=sqlite \
ADRO_EVENTSTORE_SHADOW_DSN=./var/adro/runtime-shadow.db \
./scripts/e2e-go.sh run ./cmd/adro-api
```

`ADRO_RUNTIME_JOURNAL=true` stores the legacy journal next to
`ADRO_RUN_STATE_FILE`. It may instead contain an explicit journal path.

## Enable PostgreSQL shadowing

```bash
ADRO_RUNTIME_JOURNAL=true \
ADRO_EVENTSTORE_SHADOW_DRIVER=postgres \
ADRO_EVENTSTORE_SHADOW_DSN='postgres://user:password@host/adro?sslmode=require' \
./scripts/e2e-go.sh run ./cmd/adro-api
```

Both shadow variables are required together. The optional
`ADRO_EVENTSTORE_SHADOW_TIMEOUT` is a positive Go duration and defaults to
`2s`. Invalid configuration or an unavailable backend fails startup before the
HTTP listener starts.

On startup, existing journal scopes are backfilled. A matching prefix is
required before any missing suffix is appended. The pending scope queue and
the latest shadow report are part of the same atomic journal snapshot as the
legacy events, leases, and effect fences. This means a process restart can
resume an interrupted backfill without treating a partial shadow append as a
legacy commit.

During normal operation the legacy commit succeeds even when a later shadow
operation times out or fails; the failure is retained in
`LocalProvider.RuntimeEventShadowReports`. Shadow work retries with bounded
exponential backoff and marks a scope degraded after repeated failures. A
provider shutdown cancels the shadow worker and preserves unfinished queue
items; the next process that configures the same journal resumes them. Startup
logs emit a warning for any backfilled mismatch without changing authority.

Each report contains the deterministic shadow stream ID, target, legacy and
shadow counts, canonical projection digests, first divergence sequence,
pending/degraded state, retry attempt and next-attempt time, error, and check
time. A report is matched only when counts and digests agree and no
pending/degraded/error/divergence state exists.

The journal uses a temporary file, file `fsync`, atomic rename, and parent
directory sync. Test-only fault points are `runtime.journal.write`,
`runtime.journal.rename`, and `runtime.journal.directory_sync`; failures leave
authoritative in-memory state unchanged, while a post-rename sync failure is
recovered from the replaced snapshot on restart.

Concurrent journal instances rebase independent appends against the current
snapshot. A stale lease mutation or fenced event is rejected at the commit
boundary; a stale writer cannot reinstate shadow work already drained by a
peer. A repeated idempotency key with different content is rejected. If
directory sync fails after rename, the commit outcome is ambiguous until the
journal is reopened and the event ID or idempotency key is checked; never
dispatch an external effect based on the failed call alone. Shadow setup
returns an error and starts no worker when its queue cannot be persisted.

## Cutover gate

This configuration does not authorize a cutover. The legacy journal can be
retired only after a continuous divergence-free observation window, shared
SQLite/PostgreSQL fault conformance, restart/backfill evidence, and rollback
validation. Until then, the shadow EventStore must remain side-effect free.
