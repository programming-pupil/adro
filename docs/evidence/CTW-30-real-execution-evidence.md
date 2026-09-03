# CTW-30 real execution evidence

This evidence was collected on the `agent/adro/a105f25b90e9` branch with
`codex-cli 0.152.1`, Go 1.24.1, Node 22.14.0, and real Chromium. It records
native process and browser runs; mock/provider fixtures are not counted as
real-executor evidence.

## Real Codex failure/repair pipeline

The release pipeline executed requirement creation, a real Agent development
turn, unit/integration failure, linked Bug creation, repair in the original
native Codex session/workdir, revalidation, and final reporting.

```text
result=PASS
pipeline=ea0746df2fa87d1e85c6c6e10fe17eb7 requirement=34d6d220a6f99689f76acf698dc50682
bug=f4e3b5aab91988fad0d4f600cabf3dfd
session=01a06737-acff-7a82-8b17-d065a856af69
```

Command:

```bash
ADRO_REQUIRE_CODEX=1 bash scripts/real-pipeline-e2e.sh
```

## Real API and browser

The full black-box system run passed project chat, text attachment, screenshot
delivery, custom Agents/workflow, concurrent requirements, multi-workspace
isolation, idempotency/error contracts, and restart recovery. The native
orchestration Playwright scenario additionally created an active Agent and a
published Squad, froze an immutable plan, exercised validation/dry-run, and
read timeline plus plan-level replay in Chromium.

```bash
ADRO_REQUIRE_CODEX=1 bash scripts/release-system-e2e.sh
npm run test:e2e:adro -- --grep "native Agent, Squad"
```

Both commands completed with exit code 0; the focused native Agent/Squad
browser scenario reported `1 passed`.

## Fault and production persistence evidence

`node scripts/fault-matrix.mjs` executes twelve isolated fail-closed cases:
executor interruption, missing `thread.started`, torn journal tail, fsync
failure, duplicate outbox delivery, event gap/out-of-order input, lease
takeover, context overflow, approval denial, attachment tamper, concurrent
comment edit, and database unavailability. The generated report contains each
command, exit code, duration, and log SHA-256.

`make postgres-conformance` runs a real PostgreSQL server, RLS and two-replica
repository conformance, then performs an operational `pg_dump`/`pg_restore`
into a newly created database. The source and restored durable snapshot
fingerprints matched, with `rpo_records=0`; measured RTO is recorded in the
generated JSON evidence rather than copied into this stable source document.
