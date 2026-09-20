# Evaluation Runs

`internal/eval` provides a versioned evaluator boundary over immutable, redacted-capable session bundles. A bundle has a schema version, tenant/workspace scope, source event digest, monotonic events, and a canonical digest. Evaluators receive a cloned bundle and cannot mutate the session under evaluation.

`EvalRun` transitions through `queued`, `running`, `paused`, `completed`, `failed`, and `cancelled`. Every mutation uses a revision for compare-and-swap semantics. A run carries an evaluator identity/version, a unit budget, consumed cost, structured findings, evidence digest, and—when an invariant fails—a minimal reproducer bundle containing only offending event sequences. Cancellation and deadline errors pause a run for explicit resume; terminal runs cannot be executed again.

The built-in trajectory evaluator checks duplicate effect dispatch, receipts without authorization, retries after unknown outcomes, and capability-bearing events without authorization. It is a deterministic safety signal and does not replace model-based or human review.
