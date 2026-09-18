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
required before any missing suffix is appended. During normal operation the
legacy commit succeeds even when a later shadow operation times out or fails;
the failure is retained in `LocalProvider.RuntimeEventShadowReports`. Startup
logs emit a warning for any backfilled mismatch without changing authority.

Each report contains the deterministic shadow stream ID, legacy and shadow
counts, canonical projection digests, first divergence sequence, error, and
check time. A report is matched only when counts and digests agree and no
error or divergence exists.

## Cutover gate

This configuration does not authorize a cutover. The legacy journal can be
retired only after a continuous divergence-free observation window, shared
SQLite/PostgreSQL fault conformance, restart/backfill evidence, and rollback
validation. Until then, the shadow EventStore must remain side-effect free.
