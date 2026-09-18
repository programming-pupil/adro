# Durable Runtime Tool Contract

Design source: `ADRO-origin` and the public runtime design contract.

`internal/runtime.ToolContract` is the reference contract at the tool
boundary. A contract is frozen before authorization and dispatch. The frozen
canonical schemas, capability set, field classifications, effect class,
limits and concurrency mode produce `ContractDigest`; a changed digest for
an existing call is rejected as an idempotency conflict.

## Contract boundary

The reference implementation validates:

- schema version, tool name, non-empty normalized capability set and effect
  class;
- `read_only`, `idempotent_write`, `reconcilable_write` and
  `non_retriable_write` retry/reconciliation rules;
- bounded JSON Schema types (`object`, `array`, `string`, `integer`, `number`,
  `boolean` and `null`) with required fields, properties, array items,
  additional-property policy, enum and numeric/string/array bounds;
- input and output byte limits before callback execution;
- public, internal, sensitive and secret field classifications;
- `parallel_safe` and `exclusive` concurrency modes.

Unsupported schema keywords fail closed. Tool names are identifiers only;
authorization requires every capability in the frozen contract to be present
in the caller's capability policy.

## Durable effect boundary

`ToolLoop` records authorization, `tool.started`, effect intent, dispatch
preparation and dispatch before it invokes the external callback. A valid
output commits the effect receipt and `tool.finished` in one journal batch. An
invalid output commits a receipt marked `valid=false` and `tool.failed` in the
same batch, with the output digest recorded for diagnosis. A replay returns
the recorded failure and never invokes the callback again.

An error after dispatch is `outcome_unknown`; write effects are not blindly
retried. Reconciliation remains an explicit policy and a separate durable
transition.

## Ordered batch execution

`ToolLoop.RunBatch` first validates and freezes every input in model order and
commits effect intents in that same order. It then runs parallel-safe groups
through a fixed-size worker pool. An exclusive call is its own barrier: all
previous callbacks finish before it starts, and later calls wait for it. The
returned slice remains in model request order even when callbacks finish in a
different order.

Cancellation has two distinct outcomes:

- a callback that was already dispatched keeps its own receipt, failure or
  `outcome_unknown` result;
- a call that never started receives a durable `tool.not_started` event with
  reason `batch_aborted`.

The pool does not create a worker per input call. The concurrency argument is
required to be positive and bounds the number of callback workers.

## Evidence and remaining boundaries

Reference evidence is in `internal/runtime/tool_contract_test.go` and
`internal/runtime/loop_test.go`, including schema fail-closed behavior,
capability authorization, digest conflicts, invalid input/output handling,
bounded concurrency, exclusive barriers, ordered results and cancellation.

The capability is not stable yet. Real provider/MCP adapter migration,
database-backed tool/effect rows, concrete external reconcile adapters,
cross-process conformance, sandbox/secret broker integration, Runtime
Inspector/API wiring and production fault/security evidence remain required.
