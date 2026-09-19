# Session, Turn, and Step State Machine

Design source: `ADRO-origin`.

This document describes the reference lifecycle state machine implemented in
`internal/runtime/lifecycle_state.go`. It is migration evidence, not a stable
wire contract. The implementation currently commits through the legacy
`runtime.Journal`; authoritative `EventStore` cutover, pure reducer extraction,
and production engine integration remain required before this capability can
be marked stable.

## State hierarchy

A session owns turns and a turn owns steps. Parent terminal transitions are
rejected while a child is non-terminal. A terminal session cannot create a new
turn, and a terminal turn cannot create a new step.

```text
Session
  NEW -> ACTIVE <-> SUSPENDED
            |          |
            +-> CANCELLING -> CANCELLED
            +----------------> COMPLETED
            +----------------> FAILED

Turn
  CREATED -> CONTEXT_FROZEN -> RUNNING
                              |  |  |
                              |  |  +-> SUSPENDED --+
                              |  +----> WAITING_INPUT|
                              +-------> WAITING_APPROVAL
                                      |              |
                                      +----resume----+
  RUNNING -> CHECKPOINTING -> COMPLETED
  any non-terminal, after child settlement -> FAILED | CANCELLED

Step
  PENDING -> CONTEXT_FROZEN -> MODEL_REQUEST_COMMITTED
          -> MODEL_STREAMING -> TOOL_REQUESTS_READY
          -> EFFECTS_RUNNING -> RESULT_ASSEMBLED
          -> CHECKPOINTED -> COMPLETED

  MODEL_REQUEST_COMMITTED | MODEL_STREAMING -> MODEL_OUTCOME_UNKNOWN
  EFFECTS_RUNNING -> EFFECT_OUTCOME_UNKNOWN
  any non-terminal -> CANCELLING -> CANCELLED
  any non-terminal -> FAILED
```

`COMPLETED`, `CANCELLED`, and `FAILED` are terminal. Unknown model or effect
outcomes cannot advance, redispatch, assemble a result, checkpoint, or complete.
They may only enter explicit cancellation or failure until a dedicated recovery
command is added. The state machine never interprets error strings to choose a
recovery path.

## Frozen boundaries

A turn accepts only a pre-frozen `ConfigSnapshot`. The snapshot includes the
configuration version, feature gates, adapter and protocol versions, policy
bundle identity, tokenizer identity, capture time, and canonical digest. Empty
or non-normalized map keys fail closed. Reordering a map does not change the
digest, and changing any semantic field invalidates it.

A step accepts only a pre-frozen `StepContextSnapshot`. It binds the context
manifest, tool catalog, policy snapshot, config snapshot, freeze time, and
canonical digest before a model request can be committed. The committed event
stores both the snapshot and digest. A caller cannot pass a mutable, unhashed,
or tampered snapshot through the transition API.

## Durable transition rules

Every transition requires a valid tenant/workspace/session/run scope, an active
write lease, its exact owner, and a positive fencing token. Lease validation is
performed before idempotent replay, so a replaced or expired worker cannot use
an old transition as a write-capability oracle.

Transition idempotency is bound to the previous aggregate event. A retry after a
lost response returns the original event when its payload is identical and
returns `ErrIdempotencyConflict` when the payload changed. A later legal cycle,
such as a second suspend/resume, has a different previous-event boundary and
therefore a distinct idempotency key.

The parent/child invariants are:

- session completion, cancellation, or failure requires all turns terminal;
- turn checkpointing, cancellation, or failure requires all steps terminal;
- ordinary step progress requires its parent turn to be running;
- step cancellation and failure remain available while the turn is suspended
  or settling;
- session cancellation and step cancellation use an explicit `CANCELLING`
  boundary;
- a model request cannot commit before `StepContextFrozen`;
- model/effect unknown states cannot silently retry or complete.

## Stop reasons

Terminal and cancellation events use the closed `StopReason` vocabulary:

- `completed`, `max_steps`, `budget_exhausted`, `deadline_exceeded`;
- `cancelled_by_user`, `cancelled_by_parent`;
- `policy_denied`, `approval_denied`;
- `provider_failed`, `provider_outcome_unknown`;
- `effect_outcome_unknown`, `context_overflow`, `sandbox_unavailable`;
- `storage_conflict`, `runtime_invariant_failed`.

Each transition validates the subset valid for that terminal category. Unknown
values and category mismatches fail with `ErrStopReasonInvalid`.

## Recovery and transaction boundary

A lifecycle event is appended only after replaying the aggregate and validating
its lease, current state, parent state, child settlement, snapshot digest, stop
reason, and idempotency boundary. The reference journal persists the event and
lease state atomically in its existing single-file transaction. Restart tests
reconstruct session, turn, and step projections exclusively from committed
events and compare them with the pre-restart state.

This boundary does not yet satisfy the final architecture by itself. Remaining
work includes moving decisions into pure command reducers, committing through
the authoritative SQLite/PostgreSQL `EventStore` with expected-sequence CAS,
including terminal checkpoint/outbox writes in the same backend transaction,
adding dedicated recovery commands for unknown outcomes, and wiring the runtime
engine, API, projections, traces, and Runtime Inspector to the new stream.

Human input and privileged approval use the separate durable state machine documented in `docs/operations/human-interaction.md`. Its response becomes visible only at a pending step boundary; it cannot mutate a frozen model or effect execution.

## Verification

`internal/runtime/lifecycle_state_test.go` covers the full successful path,
restart replay, canonical snapshot equivalence and tamper rejection, repeated
suspend/resume cycles, payload conflicts, stale fencing, parent/child terminal
invariants, cancellation boundaries, invalid stop reasons, and unknown-outcome
fail-closed behavior.
