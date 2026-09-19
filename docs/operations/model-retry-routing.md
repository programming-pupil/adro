# Model retry and routing contract

Model failures are classified into stable categories (`transport`, `rate_limit`, `server`, `invalid_request`, `context_overflow`, `auth`, `cancelled`, and `stream_interrupted`). Recovery code branches on this category and the explicit dispatch facts. Error text is never a retry policy.

`ModelRetryPolicy` computes bounded exponential delays with an injected random value for deterministic jitter. A provider supplied `Retry-After` is treated as a lower bound and the result is capped by the configured maximum. `DecideModelRetry` refuses to replay a dispatched request unless the provider proves it was not accepted. A stream interruption may resume or query only when the adapter advertises that capability; otherwise the model call is suspended as unknown.

`ModelRetryTimerSpec` turns a retry decision into an idempotent durable `model.retry` timer. The command binds the frozen request digest and next attempt, so a worker can reject a stale command before dispatch. The timer is the scheduling boundary; callers must claim it with a lease/fencing token and persist the retry event before dispatching the next attempt.

`SelectModelRoute` records every candidate, health/rate-limit exclusion, capability check, score, and final reason. If a historical decision is supplied, it is reused only when the request digest and selected adapter/model still match; the router never silently chooses a new provider during replay. `ModelCircuitBreaker` has independent closed/open/half-open states and admits at most one half-open probe, which keeps health probes outside normal session admission.

The current implementation is a provider-neutral reference contract. Provider adapters still need to emit these structured facts, persist retry events in the authoritative model stream, and expose query/resume operations before the capability can be marked stable.

## Durable worker boundary

`ModelRetryScheduler` is the only reference helper that creates a retry timer.
It stores the original request digest, the exact next attempt, and the recovery
action in the command payload. `NewModelRetryCommandHandler` must be installed
behind `TimerDispatcher`; before invoking a provider it verifies the claimed
occurrence, generation, request scope, request digest, and contiguous attempt
number. A stale request therefore becomes a terminal timer failure instead of
an untracked provider call. The lookup and dispatch callbacks are deliberately
separate so the authoritative model journal can commit the next request before
adapter dispatch.
