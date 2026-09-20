# Durable timer command contracts

Design source: `ADRO-origin`.

Timer scheduling is an authoritative journal fact followed by a rebuildable
TimerStore projection. A worker must never create a timer only in process
memory before dispatching work. The event stream carries
`timer.schedule_requested` with a canonical `TimerScheduleRequest`; a projector
can retry materialization after a crash or a backend outage and idempotently
replay an equal request.

The current reference paths are:

- Human interaction and approval requests append the request and its deadline
  schedule in one journal batch. `HumanDeadlineTimerSpec` binds the request
  version and definition digest, so a late answer or a stale deadline cannot
  expire a newer request.
- A write effect with a positive contract timeout appends the timeout intent
  before the `effect.dispatched` fact. `EffectTimeoutTimerSpec` binds the
  effect input digest, reconciliation policy, and generation. The timeout
  handler only records `effect.outcome_unknown`; it never retries an external
  write.
- A model retry decision can be committed by
  `ModelRetryIntentScheduler`. The retry fact and timer projection request are
  one batch, and the payload freezes request digest and next attempt. A timer
  worker must compare those values with the committed model request before
  dispatching.
- Sleep and scheduled-resume commands use the same versioned command shape and
  carry only a continuation identity, generation, and reason. The continuation
  is reconstructed from committed events after the timer fires.

`TimerStore` claims due occurrences with a lease and fencing token. Handlers
must validate the command, scope, generation, digest, due time, and claim lease
before applying a transition. A receipt, human response, reconciliation, or
newer retry that wins the race becomes a durable no-op for the stale timer;
workers can acknowledge that occurrence without changing the authoritative
state. A lost acknowledgement is safe because the next claim replays the same
occurrence key and converges on the existing event.

The reference JSON TimerStore is single-node. PostgreSQL/object-storage timer
persistence, cross-process conformance, and API/Inspector wiring remain
separate release gates; the event intent contract is retained when those
backends are introduced.
