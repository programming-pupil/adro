# Resource admission and accounting

ADRO dispatches graph attempts through a deterministic admission boundary before calling an execution provider. The boundary has two separate components:

- `FairAdmissionQueue` orders pending work with integer weighted fair queuing, stable claim keys, priority aging, tenant emergency ceilings, and a documented starvation deadline.
- `ResourceLedger` atomically reserves capacity, records normalized and raw usage, settles or releases reservations, and recovers expired reservations after a crash.

The local reference backend is stored in `ADRO_RESOURCE_STATE_FILE` (default `${ADRO_HOME}/resources.json`). It uses an inter-process lock and atomic rename. Production database adapters should preserve the same idempotency, hierarchy, and recovery contracts.

## Resource dimensions

Every request, reservation, usage record, release, and overage uses one `ResourceVector` with these integer dimensions:

- CPU time in nanoseconds
- peak memory bytes
- disk bytes
- output bytes
- network bytes
- model tokens
- tool calls
- wall time in nanoseconds
- concurrency slots

Negative values and integer overflow fail closed. A zero quota dimension means unlimited. Provider usage is untrusted: every record retains the raw provider JSON, a normalized vector, an independent estimate, and their signed discrepancy. Missing provider usage explicitly falls back to the estimate instead of recording zero.

## Admission hierarchy

Quotas apply in tenant → workspace → agent order. An attempt must fit every configured hard limit. Crossing a soft limit admits the request with the actions `warning`, `compact_context`, and `degrade_execution`. A hard-limit decision is:

- `waiting` when active reservations or consumption temporarily occupy capacity;
- `rejected` when the request cannot fit even with no competing work.

Nested work receives a child reservation carved from the parent reservation. Parent accounting includes its own consumption plus every child reservation or child consumption, which prevents recursive delegation from overselling the root budget.

## Recovery and load shedding

Reserve occurs before `provider.StartRun`. Terminal provider observations record usage and settle the reservation. A dispatch that fails before starting releases its reservation. `ReapOrphans` releases expired reservations deepest-first so child reservations are recovered before their parents.

Overload shedding may remove only low-priority, unpersisted, undispatched requests. Persisted work and dispatched-effect recovery requests are never shed. Identical inputs and clocks produce the same claim order using effective priority, virtual finish, and a stable tenant/workspace/agent/submission/ID key.

## Operator projection

`ResourceLedger.Dashboard` returns current exposure, consumed resources, active reservations, overages, missing provider reports, delayed bills, recent usage, and child-Agent attribution. Run diagnostics include this projection when a ledger is configured. Operators should change quotas through the control plane; the dashboard is read-only and the underlying JSON or database rows must not be edited directly.

## Control API and Runtime Inspector

Authenticated operators read `GET /api/v1/resources` for the bounded dashboard and its applicable quotas. `GET`, `PUT`, and `DELETE /api/v1/resources/quotas` list or change exact tenant/workspace/Agent quota scopes; every mutation passes the same validation and durable write path as scheduler reservations. Usage attribution fields such as session, step, model call, tool effect, and cost center are rejected on quota scopes.

The Cost Center consumes the read-only dashboard API. It renders budget exposure against soft and hard limits, active reservations, missing or delayed provider reports, overage evidence, estimate discrepancies, and child-Agent attribution. It never reads or edits the resource state file directly. `e2e/resource-accounting.spec.js` verifies the API request, refresh behavior, all four panels, and narrow-screen containment.

## Benchmarks

The benchmark suite covers the five required scheduler pressure patterns:

```bash
./scripts/run-resource-scheduler-benchmarks.sh
```

`ADRO_BENCHTIME` and `ADRO_BENCH_COUNT` control duration and repetition. The benchmark fails if a weighted quiet tenant is starved, a burst loses work, exhausted quota does not queue explicitly, load shedding removes protected recovery work, or priority aging leaves the old request inverted. These local results do not replace sustained multi-process soak against the future distributed queue adapter.
