# Durable Runtime Rebuild Decision Log

This log records migration decisions that remove, replace, or reclassify existing behavior. Entries are append-only; corrections add a later entry instead of rewriting history.

## 2026-09-17: Freeze the legacy control-plane baseline

- Decision: tag commit `2d480a87f0ecdf974ab09cdece2729f209a8e813` as `legacy-delivery-control-plane-20260917`.
- Reason: every migration must retain a reproducible source, OpenAPI, dependency, fault, and browser baseline.
- Replacement path: new runtime packages are introduced beside the legacy packages until conformance and replay evidence permit a controlled cutover.
- Design source: `ADRO-origin`.

## 2026-09-17: Introduce the target dependency layers without moving legacy code

- Decision: add `core/`, `ports/`, `runtime/`, and an architecture dependency test before relocating existing implementation.
- Reason: moving code before contracts and golden behavior exist would destroy comparison evidence and encourage another large rewrite.
- Replacement path: legacy packages remain active while new contracts accumulate conformance coverage. Reverse dependencies fail `go test ./internal/architecture`.
- Design source: `ADRO-origin`.

## 2026-09-17: Stop treating an effect fence as an external receipt

- Decision: replace `effect.fenced` with separate intent, dispatch-prepared, dispatched, receipt, and outcome-unknown facts in the existing runtime loop.
- Reason: a local pre-dispatch commit cannot prove that a non-transactional external side effect completed. Replaying a write after a lost response can duplicate the effect.
- Compatibility impact: tool contracts must declare an effect class. Automatic retries are currently limited to read-only tools until idempotent and reconcilable adapters expose explicit protocols.
- Replacement path: durable reconciliation and human-decision transitions will build on the new unknown-outcome state.
- Design source: `ADRO-origin`.
