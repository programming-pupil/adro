# Durable timer operations

ADRO timers are persisted through `internal/runtime.TimerStore`. A timer carries
an immutable command digest and explicit tenant, workspace, session, and run
scope. Worker claims carry a lease, fencing token, and occurrence key; late
workers cannot acknowledge or release a claim after the lease is lost.

Operators inspect timers through the scoped Runtime Inspector API:

- `GET /api/v1/timers?include_terminal=true` lists the timer read model for the
  authenticated tenant and workspace. Optional `session_id` and `run_id`
  filters narrow the projection.
- `GET /api/v1/timers/{timer_id}` reads one timer. A timer outside the request
  scope is reported as `404` so the endpoint does not become a cross-tenant
  existence oracle.
- `GET /api/v1/timers/{timer_id}/explain` returns the persisted state, reason,
  occurrence key, and next safe action.
- `POST /api/v1/timers/{timer_id}/cancel` performs an operator cancellation
  through the TimerStore state transition. The route requires orchestration
  management permission and accepts an optional JSON `reason`. Reusing an
  `Idempotency-Key` replays the original response; repeating the same terminal
  cancellation is convergent, while a different terminal reason is rejected.

The equivalent operator commands are `adroctl timer list`, `adroctl timer get`,
`adroctl timer explain`, and `adroctl timer cancel --id <id> --reason <text>`.
The CLI uses the same API and identity headers; it never opens or edits the
state file directly.

`ADRO_TIMER_STATE_FILE` selects the durable snapshot path. The API process sets
it to `<ADRO_HOME>/timers.json` for the local profile and to the mounted state
volume in Compose and Helm deployments. The file is written with an atomic
rename and an inter-process lock. If the store cannot be loaded, the process
reports a startup error and the inspector returns `503` rather than silently
falling back to an in-memory timer list.

The WebUI Timer Inspector is a read-only projection with explicit cancel and
explain actions. It renders lease/state metadata and does not expose the JSON
snapshot path or a direct row update control. Browser coverage exercises the
API contract, explanation panel, cancellation confirmation, responsive layout,
and refresh behavior; API tests exercise real persistence and scope isolation.
