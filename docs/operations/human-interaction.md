# Durable Human Interaction and Approval

Design source: `ADRO-origin`.

`internal/runtime/human_contract.go`, `human_interaction.go`,
`human_projection.go`, and `human_deadline.go` define the reference durable
state machine for human input. Ordinary interaction and privileged approval are
separate contracts and event families. A question cannot be relabeled as an
approval after it is committed, and approval evidence binds the capability,
risk, policy bundle, context digest, request version, deadline, and eligible
human actors.

## Request contracts

`HumanInteractionRequest` supports the closed kinds `question`, `choice`,
`freeform`, `artifact_review`, and `takeover`. It freezes a bounded response
schema, turn, context digest, sensitivity, deadline, eligible actors, claim
policy, request version, creation idempotency key, and canonical definition
digest.

`ApprovalRequest` uses the same durable timing and claimant rules but adds a
capability, risk, and policy bundle digest. Its response schema is fixed to an
`approved`, `denied`, or `cancelled` decision plus a bounded reason. This keeps
ordinary user steering separate from authorization for a privileged action.

Only verified `human` actors in the request tenant and workspace may appear in
the eligible set. Actor credentials are revalidated at claim, takeover,
response, and withdrawal boundaries. A body field cannot substitute a verified
actor.

## State machine

```text
PENDING --claim--> CLAIMED --respond/decide--> RESPONDED --safe boundary--> APPLIED
   |                  |
   +----timeout-------+--------------------------> TIMED_OUT
   +----withdraw------+--------------------------> WITHDRAWN

CLAIMED --approved takeover--> CLAIMED (generation + 1)
```

`APPLIED`, `TIMED_OUT`, and `WITHDRAWN` are terminal. The request version is
checked on every actor transition. A response contains the complete verified
actor, request version, canonical value, response digest, idempotency key, and
response time.

A request with `claim_policy=required` has a finite claim TTL. Concurrent
claims serialize under the journal write lease, so exactly one eligible actor
wins. A takeover requires a different eligible actor, a reason, and a separate
approval ID; it increments claim generation and invalidates the old claimant.
An expired or displaced claimant cannot decide the request.

Duplicate delivery with the same idempotency key and response digest returns
the original event. A changed answer, actor, version, or request definition
conflicts instead of overwriting durable evidence. Unsupported decision values,
malformed or over-1-MiB JSON, and response schema failures are rejected
without an event.

## Safe context boundary

A response is durable before it affects model-visible context.
`ApplyHumanResponseAtBoundary` accepts only a target step that is still
`pending`, belongs to the request turn, and whose parent turn is
`waiting_input` for ordinary interaction or `waiting_approval` for approval.
It therefore cannot mutate a frozen step, an active model stream, or a
dispatched effect. The apply event binds the response digest and target step;
the next context freeze may consume that evidence.

## Failure and recovery behavior

The state projection is rebuilt only from committed request events. A process
restart preserves request definition, current claimant and generation,
response, terminal state, and applied step. Deadline processing is explicit: late responses fail closed and a timer worker
records `timed_out`. `HumanDeadlineTimerSpec` creates an idempotent one-shot
`TimerStore` command, and `ApplyHumanDeadlineClaim` makes the event/ack boundary
safe for at-least-once delivery: a crash after the timeout event but before
timer acknowledgement replays the same event. Production composition still
needs to schedule every request automatically and run the database-backed
deadline worker.

This reference implementation still commits through `runtime.Journal`.
Authoritative EventStore cutover, API/Runtime Inspector wiring, policy-service
composition, and database-backed deadline delivery remain required before the
capability can be marked stable.

## Verification

`internal/runtime/human_interaction_test.go` covers request and response
idempotency, schema rejection, restart replay, concurrent claim, approved
takeover, displaced claimant rejection, duplicate and conflicting decisions,
ineligible and expired actors, timeout, withdrawal, and refusal to apply input
inside an active step. Threat IDs `TM-HUMAN-001` through `TM-HUMAN-003` map
these controls bidirectionally to the security test suite.
