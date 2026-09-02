## CTW-27 kernel remediation report

This report records the state produced by the current remediation branch. It
does not claim that unavailable external services or a real provider run have
passed.

### Pointers

| Component | Ref | State |
| --- | --- | --- |
| ADRO | `agent/adro/82ccdaf0f89a` (working tree SHA recorded at handoff) | local changes under review |
| ADRO origin/main | `4d5bc4842508830857c8a734ef64cf6738fc43c7` | fetched at task start |
| AOS | `4439ec77ac5bea05b703964c6093a80e49617063` | comparison pointer supplied by the independent review; latest fetch is blocked because no AOS remote is configured |
| Multica | `3d37828e9265fa36c10ec443e71deff79bfdca41` | comparison pointer supplied by the independent review; latest fetch is blocked because no Multica remote is configured |

### Kernel changes

| Capability | Result | Evidence |
| --- | --- | --- |
| ADRO-owned execution ledger | implemented (local durable profile) | `internal/runtime/kernel.go` field-complete `Event`, scope, payload/envelope hashes, previous hash, status, idempotency and fencing |
| Atomic turn/checkpoint boundary | implemented | `Journal.AppendBatch` and `FinishTurn` validate before one snapshot commit |
| Tool authorization loop | implemented | `AuthorizeTool`, `StartTool`, `ApproveTool`, `FinishTool` enforce ordered durable facts |
| Exactly-once effect fence | implemented (local profile) | `FenceEffect` stores scoped receipt and converges retries to the same event |
| Lease/fencing | implemented (local profile) | `AcquireLease`, `ReleaseLease`; stale writer tokens fail closed |
| Interaction and usage facts | implemented | `AppendInteraction`, `RecordUsage` |
| Typed context envelope | implemented | `internal/harness/store.go:CompileEnvelope`, immutable selection digest and replay key; `sdk/harness/spi.go:ContextEnvelope` |
| Memory lifecycle | implemented (local profile) | `TransitionMemory` supports candidate/quarantined/confirmed/superseded/forgotten/rejected with monotonic transitions |
| Event replay scope | implemented | `internal/events/events.go:AckScoped`/`ReplayScoped` bind tenant/workspace/aggregate and reject mismatches |
| Cross-node PostgreSQL/Redis/NATS | blocked | no production adapter or external service is configured in this workspace |
| Real Multica provider conformance | blocked | no authenticated provider workspace and no `codex` executable were available |

### Verification

Passed with the Go 1.27 toolchain (`GOROOT=/usr/local/Cellar/go/1.27.0/libexec`):

```text
go test ./... -count=1 -p 1
go test ./internal/runtime ./internal/events ./internal/harness -count=1
```

The runtime suite includes restart/reload, envelope tamper detection,
idempotency conflict, stale fencing, tool authorization, atomic turn finish,
and 50 concurrent exactly-once effect attempts. Browser E2E, external AOS and
Multica fetches, and real provider conformance remain unproven and must stay
`partial`/`blocked` until the required credentials and services are supplied.

### Rollback

The runtime journal is additive and opt-in for the local provider through
`ADRO_RUNTIME_JOURNAL`; unset the variable to return to the previous provider
snapshot behavior. Existing event, harness, and provider-neutral APIs remain
source compatible.

