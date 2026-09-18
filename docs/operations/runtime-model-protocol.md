# Durable Model And Stream Protocol

Design source: `ADRO-origin`.

The runtime model boundary is provider-neutral. Adapters implement
`ModelGateway`; they do not own request state, terminal truth, or recovery
policy.

## Request contract

`ModelRequest` freezes the request ID, scoped session, model, adapter identity,
canonical prompt, context/policy/config digests, continuation token, attempt
and idempotency key. `request_digest` is calculated from those fields. A
replay with the same request ID and key must have the same digest; a changed
payload is rejected.

Provider capability negotiation is versioned and fail-closed. Runtime only
uses a model and feature when the provider advertises the exact protocol
version, model and capability. It never infers support from adapter name or
reachability.

## Durable lifecycle

`Journal` records:

`model.requested -> model.dispatch_prepared -> model.dispatched ->
model.stream_event* -> model.completed`

If dispatch has happened and no terminal outcome is durable, recovery records
`model.outcome_unknown`. Unknown calls cannot accept late stream events or be
dispatched again. A future provider-specific query or human recovery command
must close this state explicitly; this increment intentionally does not claim a
provider query implementation.

The finish frame is written together with the final `model.stream_event` in
one journal batch. Restart replays the request digest, stream sequence and
finish reason from committed events.

## Stream contract

`ModelEvent` is versioned by the runtime contract and uses a monotonic
sequence plus a cursor derived from request ID, sequence and event ID. Text,
reasoning, tool request, usage, finish and provider error frames are distinct;
tool arguments must be complete canonical JSON and text must be valid UTF-8.

`BoundedModelStream` has an explicit capacity, retention window and overflow
policy. It can block, disconnect, or drop only optional text/reasoning deltas.
Dropped deltas are reported as a structured gap, and a cursor outside
retention returns a gap error rather than silently resuming from an arbitrary
point.

## Remaining scope

The contract and reference tests are experimental. Existing provider adapters
still need migration to `ModelGateway`, durable retry/backoff and routing
records, provider query/reconcile adapters, persistent stream storage, API/SSE
and WebSocket unification, and browser/slow-consumer evidence. Those gaps keep
the model and stream capabilities below stable.
