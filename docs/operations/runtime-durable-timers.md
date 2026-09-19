# Durable Runtime Timers

Design source: `ADRO-origin`.

`internal/runtime.TimerStore` is the reference single-node timer backend. It
stores an atomic JSON snapshot behind the same inter-process lock and rename
boundary used by the local durable runtime. A process restart therefore
replays pending timers from disk instead of relying on an in-process timer.

## Record contract

Each timer persists:

- UTC due time, optional interval and immutable command payload digest.
- Scope, stream ID and expected sequence for recovery diagnostics.
- Generation, owner, lease expiry and fencing token for worker claims.
- `pending`, `claimed`, `fired`, `cancelled` or `expired` state.
- A command idempotency key and occurrence key (`key:generation:N`).

`Schedule` is idempotent for the same scope, schedule key, command, due time
and interval. A changed command or schedule is rejected with
`ErrTimerIdempotencyConflict`; it cannot silently rewrite a pending timer.

## Recovery policies

Recurring timers use one of three explicit policies:

- `catch_up`: claim at most one bounded current occurrence and record the
  suppressed missed count when downtime exceeds `MaxCatchUp`.
- `coalesce`: collapse all missed occurrences into one claim at the current
  clock position.
- `expire`: move a timer to `expired` when lateness or the bounded catch-up
  limit is exceeded.

`ClaimDue` has an explicit limit, and an expired claim can only be taken over
with a new fencing token. `Acknowledge` and `ReleaseClaim` require the owner,
fencing token and occurrence key. Repeated acknowledgement returns the
durable execution result without re-running the command.

The reference tests use a virtual clock for stable ordering, clock jumps,
timezone/DST normalization and leap-second-shaped input. Go normalizes the
latter to the next minute before persistence; timer storage always writes UTC.

## Current boundaries

This increment supplies the durable store and recovery semantics. Human
interaction and approval deadlines now expose an idempotent timer command and
at-least-once handler in `internal/runtime/human_interaction.go`; its restart
test covers a crash after timeout commit but before timer acknowledgement.
Production composition and the remaining retry, sleep, model, effect and
scheduled-resume call sites have not all been migrated from existing
in-process paths. There is also no Runtime Inspector API or CLI for timer
listing/cancellation yet. Those gaps keep the timer capability
experimental/partial and block a stable claim.
