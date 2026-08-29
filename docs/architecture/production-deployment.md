# Production deployment boundary

ADRO 0.1.0 ships one supported deployment profile: a single API process with a
single ReadWriteOnce volume. It is a reference profile, not an HA control
plane. `ADRO_PROFILE=production`, `ADRO_PROFILE=ha`, `ADRO_REPLICA_COUNT>1`, or
selecting any unshipped backend causes startup to fail with a `blocked:` error.
The Helm schema also restricts the shipped chart to one replica and the upgrade
strategy is `Recreate`, preventing overlapping snapshot writers.

Run the same check used by the API before deployment:

```bash
go run ./cmd/adroctl config-check
ADRO_PROFILE=production ADRO_REPLICA_COUNT=2 go run ./cmd/adroctl config-check
```

The second command must fail. A successful local readiness probe proves only
the single-node profile.

## State ownership

| State | Shipped owner | Production/HA requirement | Failure rule |
| --- | --- | --- | --- |
| Requirements, Work Items, provenance, context, repair, idempotency | atomic JSON snapshot | PostgreSQL transactions and RLS | block production while `file` is selected |
| Events | process memory plus optional JSON snapshot | NATS JetStream or equivalent durable ordered bus | block production while `memory` is selected |
| Workflow timers/retries | in-process calls | Temporal or equivalent durable workflow engine | block production while `in-process` is selected |
| Audit | atomic JSON hash chain | append-only shared store with retention/verification | included in the persistence adapter gate |
| Users | atomic JSON; sessions remain process-local | OIDC/mTLS identity and shared revocation/session policy | block production while `local` is selected |
| Artifacts | local filesystem | S3-compatible immutable/object-lock store | block production while `filesystem` is selected |
| Runner registry | atomic JSON; commands execute as local argv | rootless container or VM workers with quotas and egress policy | block production while `argv` is selected |
| Secrets | process environment | workload identity and external SecretStore | block production while `environment` is selected |
| Git and CI | interfaces only | authenticated adapters with webhook verification | block production while `none` is selected |

The production target names are `postgres`, `nats`, `temporal`, `s3`,
`oidc`/`mtls`, `external`, `rootless`/`container`/`vm`, `git`, and `external`.
Those values describe required adapter classes; they do not activate an
implementation in this repository. Even when every value is set, 0.1.0 stays
blocked because the adapters are not shipped or dynamically loaded.

## Adapter acceptance

An integrator must provide versioned implementations for the SPIs under `sdk/`
and prove all of the following before changing the startup gate:

- tenant/workspace identity comes from verified OIDC or mTLS claims and every
  PostgreSQL transaction sets `app.tenant_id` and `app.workspace_id` before
  touching RLS tables;
- mutation, outbox publication, audit append, and idempotency record have an
  explicit atomicity/reconciliation policy;
- event redelivery, ordering, cursor replay, workflow retries, and cancellation
  have fault-injection evidence;
- artifacts support immutable writes, digest verification, Range reads,
  encryption, retention/legal hold, and migration rollback;
- runner workers are rootless or VM-isolated with seccomp, network allowlists,
  CPU/memory/disk limits, an explicit workspace mount, and no ambient secrets;
- Git/CI credentials are short lived or secret references, webhook requests are
  signed and replay bounded, and logs/artifacts are redacted;
- each adapter passes an independently runnable conformance suite at the exact
  version recorded in the release manifest.

Local argv execution and the reference SDK types are never sufficient evidence
for any of these controls.

## Migration and recovery gate

The SQL files in `migrations/` and `artifact.Migrator.Copy` are boundaries, not
a wired online migration system. A production rollout needs a tested sequence:

1. Back up the source state and verify restore in an isolated environment.
2. Apply expand-only schema changes and verify RLS policies with two tenants.
3. Start idempotent backfill workers with checkpoints and digest comparison.
4. Enable event/artifact double-write, reconcile counts and hashes, then switch
   reads only after the error budget remains green.
5. Retain a tested rollback window and reverse the read switch before removing
   old fields or objects.
6. Run pod/node/AZ loss, bus outage, database failover, object-store failure,
   restore-time and restore-point tests.

No worker, cross-AZ test, or measured RTO/RPO is shipped today. That is a P0
production blocker, not a documentation-only known limitation.
