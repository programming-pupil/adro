# Durable Timer Command Contracts

Timeout, sleep, and scheduled-resume commands use the same durable `TimerStore` claim and fencing protocol as approval deadlines and model retries. `EffectTimeoutTimerSpec` persists effect identity, input digest, generation, and reconcile policy. `SleepTimerSpec` and `ScheduledResumeTimerSpec` persist only a continuation identity, generation, and reason; the continuation is rebuilt from committed events after firing.

A worker must validate the claimed command, compare the generation and digest with the current aggregate, and then apply an idempotent state transition. A late receipt or a duplicate delivery cannot turn a stale timer into a new dispatch. The timer records are durable; production composition still needs every model/effect call site to schedule these commands before dispatch.
