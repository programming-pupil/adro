# Rebuild Progress

Updated: 2026-09-21.

The authoritative issue TodoList remains the complete scope. This file records evidence for landed increments; absence from this table means not completed.

| Todo ID | State | Evidence |
|---|---|---|
| P0-ARCH-013 | partial | `docs/rebuild/capability-evidence.md` records capability maturity and missing gates; `docs/rebuild/todo-evidence.md` mirrors all 451 authoritative checklist items with deletion-resistant source identity verification |
| P0-BASE-001 | complete locally | Annotated tag `legacy-delivery-control-plane-20260917` at `2d480a87f0ecdf974ab09cdece2729f209a8e813` |
| P0-BASE-002 | partial | Git tag freezes database migrations, workspace fixtures, OpenAPI and tracked browser assets; generated screenshot fixture still pending |
| P0-BASE-003 | complete | `docs/rebuild/baseline-report.md` records exact-tag serial test, race, fault, benchmark discovery and isolated browser reruns |
| P0-BASE-004 | complete | `docs/rebuild/decision-log.md` |
| P0-CORE-001 | complete | `core/ids` typed IDs and validation |
| P0-CORE-002 | partial | Generic reducer/replay contract exists; legacy handlers are not migrated |
| P0-CORE-003 | partial | Canonical envelope v1 exists; the legacy runtime journal now maps into it in shadow mode with durable pending work and restart recovery, while other producers remain pending |
| P0-CORE-004 | partial | Runtime journal mapping, retry/backoff, shutdown cancellation, restart backfill, stale-writer queue reconciliation, and no-outbox shadow tests exist in `internal/runtime/shadow.go`; Harness, Orchestration and Audit mappings remain pending |
| P0-CORE-006 | partial | Canonical JSON v1, golden digest and fuzz smoke |
| P0-CORE-007 | partial | `core/event/registry.go` provides adjacent explicit upcasters, reject/preserve unknown-field policy, future-version rejection and downgrade blocking; N-2 fixtures and producer migrations remain pending |
| P0-CORE-008 | partial | Session/Turn/Step transition tables and illegal-transition tests exist in `internal/runtime/lifecycle_state.go`; ModelCall, Approval, Timer and Delegation transition tables remain pending |
| P0-CORE-009 | partial | `docs/rebuild/event-source-inventory.md`; the runtime journal has a legacy-authoritative EventStore shadow with durable queue/recovery evidence, while source cutover and the other producers remain pending |
| P0-CORE-010 | partial | `core/event.ValidateChain` and `core/reducer.ReplayVerified` produce verified replay/state-digest evidence; `internal/projection.Worker` replays one tenant/stream partition from sequence zero with a canonical digest-checked offset, and configured `projection.AtomicStore` backends commit each mutation page with its offset atomically; other views and runtime composition remain pending |
| P0-CORE-013 | partial | Dependency interfaces and deterministic testkit exist; legacy reducers still call system sources |
| P0-CORE-014 | partial | Manual clock, sequence IDs/random and recording sleeper tests |
| P0-CORE-015 | partial | Canonical map/number/Unicode encoding tests; cross-process fixtures pending |
| P0-CORE-016 | partial | Event envelope carries encoding and hash identities; snapshot/blob/manifest migration pending |
| P0-CORE-017 | partial | Reducer byte-determinism test exists; runtime state machines pending |
| P0-CORE-018 | partial | Turn and Step reference lifecycle freeze canonical config, adapter/protocol, policy, tokenizer, context/tool/policy digests with tamper tests; ExecutionPlan and production engine binding remain pending |
| P0-CORE-019 | partial | Restart replay retains historical Turn/Step snapshot digests; hot-update boundary integration and authoritative EventStore cutover remain pending |
| P0-MODEL-004 | partial | `internal/runtime/model_retry.go` defines stable failure classes and explicit dispatch acceptance rules; provider adapter emission remains pending |
| P0-MODEL-005 | partial | Bounded deterministic backoff, Retry-After handling, authoritative `model.retry_scheduled` plus `timer.schedule_requested` journal intents, and idempotent TimerStore projection exist; provider query/reconcile wiring remains pending |
| P0-MODEL-006 | partial | Deterministic route evidence, historical route reuse, circuit breaker and unknown-dispatch fallback guard exist; live adapter pool integration remains pending |
| P0-MODEL-012 | partial | `ModelRouteDecision` records candidate health, rate limit, capabilities, score and reason; API/event persistence remains pending |
| P0-MODEL-013 | partial | `ModelCircuitBreaker` implements closed/open/half-open with one probe; provider health/admission composition remains pending |
| P0-MODEL-014 | partial | Retry and route replay refuse fallback after an unproven dispatch; provider query/reconcile wiring remains pending |
| P0-LIFE-001 | complete | Lifecycle component contract |
| P0-LIFE-002 | complete | Missing dependency, duplicate and cycle validation with stable order |
| P0-LIFE-003 | complete | Topological start, reverse stop and partial-start rollback |
| P0-LIFE-005 | partial | Root cancellation propagation is implemented; model/tool/sub-agent integration pending |
| P0-LIFE-006 | complete locally for reference manager | `Snapshot`, `Readiness` and `Liveness` derive independent probe state, reason and change time from component health in `runtime/lifecycle/manager.go`; production API wiring remains pending |
| P0-LIFE-009 | partial | Core lifecycle conformance covers startup/shutdown timeout, rollback, repeated stop, aggregated errors and owned-worker cancellation; socket/file-descriptor leak tests and production integration remain pending |
| P0-EFFECT-001 | partial | Intent, prepare, dispatch, receipt, unknown-outcome and policy-valid reconciled facts in `internal/runtime/kernel.go`; positive write timeouts now commit a digest-bound timer intent before dispatch, while external adapter reconcile implementations remain pending |
| P0-EFFECT-002 | complete for legacy ToolLoop | Intent commits before `tool.started` and callback dispatch; transition APIs require positive lease fencing |
| P0-EFFECT-003 | partial | Effect ID, input digest, class, explicit reconcile policy and fence are durable; adapter idempotency key contract pending |
| P0-EFFECT-006 | partial | Journal enforces query/compensate/human/unrecoverable decisions and idempotent resolution; concrete external reconcile adapters remain pending |
| P0-EFFECT-005 | complete for legacy ToolLoop | Dispatched writes without receipts return `ErrEffectOutcomeUnknown` and are not replayed |
| P0-EFFECT-007 | partial | Effect receipt, tool terminal event, and timeout-to-unknown transition are journaled with fencing and idempotent timer claims; final checkpoint integration pending |
| P0-TOOL-001 | complete locally for reference contract | Versioned `ToolContract` freezes name, schemas, capabilities, effect class, limits and concurrency mode in `internal/runtime/tool_contract.go` and `internal/runtime/kernel.go` |
| P0-TOOL-002 | complete locally for reference contract | Effect classes and explicit reconciliation policy validation in `FreezeToolContract`; write retries fail closed |
| P0-TOOL-003 | complete locally for reference contract | Canonical frozen contract digest is stored in `tool.authorized`; digest changes conflict during a call |
| P0-TOOL-004 | complete locally for reference contract | Bounded JSON Schema subset, byte limits, field classifications and canonical payload digests with negative tests |
| P0-TOOL-005 | complete locally for reference executor | `ToolLoop.RunBatch` uses a positive fixed worker bound with `parallel_safe` groups and exclusive barriers |
| P0-TOOL-006 | complete locally for reference executor | Batch results are returned by model request index regardless of callback completion order |
| P0-TOOL-007 | complete locally for reference executor | Cancellation persists `tool.not_started` for calls that never dispatch; dispatched calls retain receipt/unknown semantics |
| P0-CTX-001 | partial | `internal/context/manifest.go` is the immutable model-visible manifest and provider boundary; API/provider-wide cutover remains pending |
| P0-MCP-001 | partial | `internal/mcp` performs explicit `initialize` capability negotiation and rejects omitted/unknown protocol versions; provider-wide negotiation persistence remains pending |
| P0-MCP-002 | partial | Shared transport contract supports streamable HTTP/SSE and explicitly enabled bounded stdio; production remote connection/session lifecycle remains pending |
| P0-MCP-003 | partial | `internal/runtime/MCPToolExecutor` routes MCP through the durable ToolLoop for approval, timeout, effect intent/dispatch/receipt and unknown-outcome semantics; shared policy/sandbox and Inspector integration remain pending |
| P0-MCP-004 | partial | `SecretResolver` and `BrokerSecretResolver` keep references out of JSON-RPC and bind short-lived broker leases to an explicit scope; production broker wiring remains pending |
| P1-MCP-005 | partial | Canonical tool schema digest, duplicate/deleted tool rejection, required-argument checks and protocol-version fail-closed tests exist; live catalog persistence and Inspector evidence remain pending |
| P0-STORE-001 | partial | EventStore, Snapshot, Lease, Blob, Secret, Scope, and Projection ports are independent; shared projection contract plus memory, SQLite, and PostgreSQL adapters enforce digest/CAS/tenant rules, `AtomicStore` commits ordered mutation pages with offsets in one backend transaction, and `internal/projection.Worker` uses the atomic path when configured; full runtime composition and production SecretStore wiring remain pending |
| P0-STORE-002 | complete for EventStore | `adapters/eventstore/sqlite` is explicitly single-node and does not claim HA |
| P0-STORE-003 | complete for EventStore | `adapters/eventstore/postgres`; migrations 001-015 apply together; real PostgreSQL 17 lock-wait, conformance and restore evidence |
| P0-STORE-004 | complete for EventStore/LeaseStore | SQLite and PostgreSQL run `conformance/eventstore` |
| P0-STORE-005 | complete for both backends | Expected-sequence CAS, concurrent single-winner and concurrent same-key replay evidence |
| P0-STORE-006 | complete for both EventStores | Event, outbox and terminal snapshot share one rollback-tested transaction |
| P0-STORE-009 | partial | `internal/artifact.Lifecycle` and `BlobLifecycle` persist roots, legal holds, two-phase tombstone/purge results, and deletion proofs; artifact/blob inventory and workspace rollback now enforce matching tenant scope; event/snapshot/blob projection roots remain to be wired |
| P0-STORE-015 | partial | Composite tenant/stream foreign keys reject cross-tenant EventStore rows; projection rows and offsets carry tenant boundaries and scoped reads/writes; authenticated scoped read/RLS and full cache/queue/blob/trace composition remain pending |
| P0-EVAL-023 | partial | `internal/eval` defines versioned Evaluator, immutable SessionBundle and EvalRun state machine |
| P0-EVAL-004 | partial | Shared suite covers CAS, idempotency, atomicity, lease fencing, concurrency, restart, subscription, tenant isolation and corruption; runtime journal write/rename/directory-sync faults, stale cross-instance fence/idempotency, release/restart monotonic fencing and shadow queue restart tests exist, while generic migration crash/resume and tail repair remain pending |
| P0-TIMER-001 | partial | Human deadlines, model retries, positive write effect timeouts, sleep, and scheduled resume use versioned durable command intents; provider-wide call-site and database backend scheduling remain pending |
| P0-TIMER-002 | complete locally | `Timer` persists UTC due time, command digest, stream sequence, generation, owner, state, lease expiry and fencing token in `internal/runtime/timer.go` |
| P0-TIMER-003 | complete locally | Atomic `ClaimDue`, fencing-token takeover, occurrence-key idempotency and release/ack tests in `internal/runtime/timer_test.go` |
| P0-TIMER-004 | partial | Manual-clock ordering, clock jump, DST normalization and leap-second-shaped input tests exist; broader cross-process and calendar compatibility fixtures remain pending |
| P0-TIMER-005 | complete locally for reference backend | Bounded catch-up, coalesce, suppression and expiry are persisted and tested; integration with every runtime retry/deadline path remains pending |
| P0-TIMER-006 | partial | Scoped `GET /api/v1/timers`, per-timer read/explain, permission-checked cancel, `adroctl timer` commands, and WebUI Timer Inspector use the durable TimerStore; effect/model/human timer intents now have projector tests, while API/browser and production worker composition remain pending |
| P1-GRAPH-009 | complete locally | Backpressure, weighted fairness, priority aging/inversion, workspace quota and non-sheddable recovery tests in `internal/orchestration/resource_accounting_test.go` |
| P0-GRAPH-010 | complete locally for local scheduler | `Scheduler.Tick` reserves tenant/workspace/agent capacity before provider dispatch and reports explicit admitted/waiting/rejected states; distributed SQL quota adapter remains pending |
| P0-GRAPH-011 | complete locally for reference queue | Integer weighted fair queuing exposes virtual finish and a conservative starvation deadline with deterministic tests |
| P0-GRAPH-012 | complete locally for reference queue | Priority aging raises waiting work to a configured maximum; tenant emergency ceilings cap priority with an auditable decision |
| P0-GRAPH-013 | complete locally for reference queue | Overload shedding only removes low-priority unpersisted work; persisted and recovery requests are protected |
| P0-GRAPH-014 | complete locally for reference queue | Claim order uses effective priority, integer virtual finish and a stable tenant/workspace/agent/time/ID key |
| P0-GRAPH-015 | complete locally | `internal/orchestration/resource_accounting_benchmark_test.go` and `scripts/run-resource-scheduler-benchmarks.sh` benchmark noisy-neighbor fairness, bursts, quota exhaustion, worker jitter/recovery and priority inversion; sustained multi-process soak remains a release gate |
| P0-RES-001 | complete locally | `ResourceVector` unifies CPU time, memory peak, disk/output/network bytes, tokens, tool calls, wall time and concurrency slots |
| P0-RES-002 | complete locally | Every reservation preserves requested, reserved, consumed, released and overage vectors |
| P0-RES-003 | complete locally | Child reservations are carved from the parent and child consumption rolls up, preventing recursive delegation oversell |
| P0-RES-004 | complete locally for reference backend | Scheduler reserve-before-dispatch, worker settlement, failed-dispatch release, atomic persistence rollback and deepest-first orphan recovery are tested |
| P0-RES-005 | complete locally | Usage attribution binds tenant, workspace, agent, session, step, model/tool effect and cost center |
| P0-RES-006 | complete locally | Soft limits return warning/compaction/degradation actions; hard limits produce explicit waiting/rejected decisions and terminal overage reason |
| P0-RES-007 | complete locally | Usage retains raw provider JSON, normalized usage, independent estimate, signed discrepancy and explicit missing-provider fallback |
| P0-RES-008 | complete locally | `/api/v1/resources` drives the Cost Center burn bars, reservation table, anomaly evidence and child-Agent attribution; `e2e/resource-accounting.spec.js` covers desktop refresh and narrow-screen overflow |
| P0-RES-009 | complete locally for reference backend | Conformance covers duplicate/conflicting usage, delayed billing, missing usage, negative values and integer overflow rollback |
| P0-LOOP-001 | partial | Reference Session/Turn/Step hierarchy, snapshots, checkpoint boundaries, parent/child settlement and restart replay exist in `internal/runtime/lifecycle_state.go`; pure reducer/EventStore/RuntimeEngine integration remains pending |
| P0-LOOP-002 | partial | Reference Step rejects model request commit before durable `step.context_frozen` and replay verifies the frozen digest; production model dispatch path is not yet cut over |
| P0-LOOP-003 | partial | `ModelRequest` canonical prompt/config/context/policy digest and Journal replay tests exist; all provider adapters are not migrated |
| P0-LOOP-004 | partial | Closed `StopReason` vocabulary and terminal-category validation cover reference Session/Turn/Step transitions; full engine error taxonomy and UI/API mapping remain pending |
| P0-LOOP-009 | partial | `ModelRequest` freezes request digest, adapter identity, attempt and idempotency key before dispatch; config snapshot persistence is still a contract-level digest |
| P0-LOOP-010 | partial | `model.outcome_unknown` blocks late stream frames and redispatch; provider query/human recovery implementation remains pending |
| P0-LOOP-011 | partial | Model event validation distinguishes stream frames, finish and provider error; full step reducer and cancellation taxonomy remain pending |
| P0-LOOP-012 | partial | Reference lifecycle persists stable stop reason codes and rejects unknown/category-invalid values; remaining runtime call sites still need migration from error-string branching |
| P0-LOOP-013 | partial | Turn freezes config and Step freezes config/context/tool/policy identities with canonical digests; hot-update and production adapter integration remain pending |
| P0-MODEL-001 | partial | Versioned `ModelEvent` types and cursor validation in `internal/runtime/model.go`; adapter migration and API wire compatibility remain pending |
| P0-MODEL-003 | complete locally for reference contract | `ProviderCapabilities` validates protocol/model/features fail-closed with tests |
| P0-MODEL-010 | complete locally for reference contract | `ModelRequest`, `ModelEvent`, `ProviderCapabilities`, `ContinuationToken` and `Usage` are defined and tested |
| P0-MODEL-011 | complete locally for reference contract | Canonical request digest and idempotency conflict tests |
| P0-MODEL-015 | complete locally for reference contract | Incompatible protocol/model/capability negotiation is rejected without adapter-name inference |
| P0-OBS-001 | complete locally | `internal/telemetry` uses the official OpenTelemetry Go SDK and OTLP/HTTP protobuf exporter; the API owns one provider, injects it into orchestration, fails closed on invalid compatibility configuration and flushes on shutdown; see `docs/operations/opentelemetry.md` |
| P0-SBX-001 | complete locally for contract | Seven cumulative enforcement levels and capability validation in `ports/sandbox`; unknown or inconsistent capabilities fail closed |
| P0-SBX-002 | partial | Local `Prepare` rejects insufficient enforcement and unsupported CPU/memory/disk/secret/output requirements; production policy wiring remains pending |
| P0-SBX-003 | partial | macOS Seatbelt deny-default implementation and real negative probes exist; Linux Landlock/bwrap and Windows restricted-token/ACL remain pending |
| P0-SBX-004 | partial | Local capability metadata explicitly reports no secure tenant isolation and the limitation is documented; Inspector/API wiring remains pending |
| P0-SBX-006 | partial | Strict network grant contract plus real Seatbelt default-deny evidence; controlled proxy, grant enforcement and DNS-rebinding tests remain pending |
| P0-SBX-007 | partial | Separate file operations, containment and symlink/ancestor checks with Seatbelt allow/deny evidence; hard-link/mount/TOCTOU controls remain pending |
| P0-SBX-008 | partial | Bounded streams, output termination, timeout, cancellation and Unix descendant cleanup are tested; Windows process trees and CPU/memory/disk enforcement remain pending |
| P0-SEC-001 | partial | Metadata-only scope-bound secret leases and development memory broker with expiry/revocation/copy/canary tests; production storage and injection remain pending |
| P0-SEC-002 | partial | Central sensitivity/redaction package now protects orchestration diagnostics and trace attributes; complete prompt/tool/log adapter integration remains pending |
| P0-SEC-003 | partial | `internal/security/provenance.go` and prompt manifest v2 derive trust from runtime zones, preserve taint/sensitivity, reject cross-tenant input and structurally escape untrusted content; provider-wide migration remains pending |
| P0-SEC-004 | partial | Plugin manifests canonically sign generic, file, network, secret and data-egress permissions; registry/startup verify network-to-egress coverage and the extension supervisor enforces classified calls; adapter-wide integration remains pending |
| P0-SEC-005 | partial | Durable Ed25519 trust store supports authenticated rotation, retirement, revocation-driven quarantine, compatible rollback and persistence rollback tests; artifact distribution/signing remains pending |
| P0-SEC-006 | partial | `docs/rebuild/threat-test-map.json` and `scripts/verify-threat-test-map.py` enforce 30 mapped threats and bidirectional test annotations; full threat coverage remains pending |
| P0-IDENT-001 | partial | `core/identity.Actor` is bound to request context only after human-session or service-credential verification; authenticated handlers use immutable actor/tenant/workspace scope and spoofing tests fail closed, while non-HTTP runtime boundaries still need full migration |
| P0-IDENT-002 | complete locally for reference boundary | Human, service, agent, worker and extension actor types, human-session revocation, short-lived service credentials, key retirement/revocation and per-credential revocation are implemented and tested |
| P0-IDENT-003 | partial | Membership remains an API/menu boundary and `core/policy` remains the capability boundary; complete enforcement across every tool/model/MCP dispatch is still pending |
| P0-IDENT-004 | complete locally for reference boundary | Ed25519 service credentials bind audience, actor, tenant, workspace and a maximum 15-minute lifetime; `ADRO_API_TOKEN` fails readiness and `adroctl service-credential` manages init/issue/rotation/revocation |
| P0-IDENT-005 | partial | Delegation, impersonation, takeover and break-glass transitions preserve original/effective actors, require approval for privileged modes and emit audit-chain evidence; authoritative event propagation across all async work remains pending |
| P0-IDENT-006 | partial | Authenticated API, artifact object operations, runner, comments, audit and orchestration request paths use verified tenant/workspace context; unscoped or mismatched artifact access fails closed; full store/cache/queue/blob/trace/projection conformance remains pending |
| P0-EXT-001 | partial | Registry authorization plus explicit in-process factory and panic containment exist; production adapter wiring and isolation evidence remain pending |
| P0-EXT-002 | partial | `runtime/extensions` runs reference external adapters over bounded JSON-RPC on `SandboxBroker` without EventStore/database handles; production isolation remains pending |
| P0-EXT-003 | partial | Bounded reference handshake verifies protocol, adapter, schema, capability/permission subsets and message size; production adapter conformance remains pending |
| P0-EXT-004 | partial | Reference supervisor covers start/health/stop, cumulative restart budget, capped backoff, quarantine and bounded audit in crash/exit tests; production lifecycle integration remains pending |
| P0-EXT-005 | partial | Extension crashes are isolated and durable write effects already retain unknown-outcome semantics; production adapter/effect integration remains pending |
| P0-EXT-006 | partial | WASI is represented in the signed contract and explicitly rejected without a dedicated runtime; a production WASI transform runner is absent |
| P0-EXT-007 | partial | Host environment is not inherited and manifest file/network/secret grants map to broker requests; production secret injection and network proxy remain pending |
| P0-EXT-008 | partial | Registry compatibility matrix, signed activation/rollback and rolling handshake rejection exist; live rolling replacement orchestration remains pending |
| P0-EXT-009 | partial | Malicious suite covers timeout, protocol flood, forged IDs, overclaim, panic, invalid/oversized output and exit storms; memory/fork bomb and forged receipt integration remain pending |
| P0-POLICY-001 | partial | `core/policy` evaluates declared capabilities rather than adapter/tool names; migration of every dispatch path remains pending |
| P0-POLICY-004 | partial | `core/policy.ValidateChild` rejects tenant/workspace changes, added capabilities/destinations and higher sensitivity; orchestration delegation wiring remains pending |
| P0-POLICY-007 | partial | Canonical decision records bind scope, normalized input digest, outcome/reason, bundle digest, engine version and timestamp; `adapters/policy/eventstore` persists and idempotently reads them; production composition remains pending |
| P0-POLICY-008 | partial | Historical `Replay` avoids current-policy evaluation and `Audit` reports recomputation divergence; persisted audit APIs remain pending |
| P0-POLICY-009 | partial | Typed context provenance separates trusted instructions from tainted user/retrieved/tool/model data; provider/tool migration remains pending |
| P0-POLICY-010 | partial | Extension dispatch evaluates tenant/workspace, capability, exact destination, purpose and sensitivity and persists the decision before dispatch; model/tool/MCP/HTTP integration remains pending |
| P0-POLICY-011 | partial | Evaluator errors, timeouts, malformed results and engine-version mismatch produce durable deny outcomes; external engine conformance remains pending |
| P0-ARCH-012 | partial | `scripts/verify-public-identity.py` checks tracked public text without printing forbidden values, with negative tests and optional private denylist; full historical scan, private policy and naming cleanup remain pending |
| P0-TRIM-010 | partial | SPDX SBOM, dependency notices, license copies and supply-chain verification are reproducible from the manifests; full security, governance and release gates remain pending |
| P1-SEC-007 | partial | `SBOM`, `THIRD_PARTY_NOTICES` and license copies now cover the current dependency graph and `make supply-chain` verifies them; SLSA provenance, signed release artifacts and reproducible binary evidence remain pending |
| P0-GOV-011 | partial | `.github/PULL_REQUEST_TEMPLATE.md` asks authors to record clean-room provenance, license/SBOM impact, capability evidence and naming scan; independent review enforcement remains pending |
| SOURCE-L0652 | complete locally | The private JSON envelope exporter was removed; no custom OTLP wire implementation remains in the runtime |
| SOURCE-L0703 | complete locally | Official OpenTelemetry Go SDK dependencies, provider lifecycle and standard OTLP/HTTP protobuf export are implemented and tested |
| P0-STREAM-001 | partial | Monotonic event sequence and cursor with replay/gap tests; persistent stream adapter and all transport mappings remain pending |
| P0-STREAM-002 | complete locally for reference stream | Bounded capacity and explicit block/drop/disconnect policies are implemented and tested |
| P0-STREAM-003 | complete locally for reference stream | Retention and dropped-optional-delta conditions return structured gap errors |
| P0-STREAM-004 | partial | UTF-8 and complete canonical tool argument validation exists; provider chunk assembler integration remains pending |
| P0-STREAM-005 | complete locally for reference contract | Distinct text/reasoning/tool/usage/finish/error frame types and terminal validation |
| P0-STREAM-007 | partial | Context cancellation is available at gateway boundary; adapter socket/goroutine cleanup evidence remains pending |
| P0-STREAM-009 | partial | Reference tests cover retention gap, optional drop, overflow disconnect, duplicate/out-of-order rejection; provider matrix remains pending |

| P0-POLICY-002 | partial | New runtime approvals persist paired `approval.asked` and `approval.decided` events; legacy approval APIs and tool authorization paths still need migration |
| P0-POLICY-003 | partial | Missing responders converge on durable timeout, actor/schema/version errors fail closed, and restart preserves pending requests; production deadline worker composition remains pending |
| P0-HUMAN-001 | complete locally for reference state machine | `HumanInteractionRequest` is separate from `ApprovalRequest` and supports question, choice, freeform, artifact review and takeover kinds |
| P0-HUMAN-002 | complete locally for reference state machine | Request events freeze schema, deadline, eligible actors, claim policy, context digest, sensitivity, version and definition digest |
| P0-HUMAN-003 | complete locally for reference state machine | Responses bind a verified actor, request version, idempotency key, canonical payload digest and bounded schema validation |
| P0-HUMAN-004 | complete locally for reference state machine | Deterministic tests cover timeout, withdrawal, duplicate/conflicting answers, concurrent claim, approved takeover and displaced claimants |
| P0-HUMAN-005 | complete locally for reference state machine | A response can be applied only to a pending step while its turn waits for the matching input class; active model/effect steps fail closed |
| P0-CTX-002 | partial | Prompt manifest ranks system/policy/agent/task/memory/history/tool layers; steering and production assembly integration remain pending |
| P0-CTX-003 | partial | Context blocks carry source, hash, token estimate, mandatory, provenance, sensitivity, atomic metadata and selection reason |
| P0-CTX-004 | partial | Tokenizer identity is frozen and validated at provider boundary; every adapter migration remains pending |
| P0-CTX-005 | partial | Compiler promotes paired tool transaction blocks into an atomic mandatory group; all harness producers remain pending |
| P0-CTX-006 | partial | Compiler rejects overflow instead of truncating mandatory/atomic/Unicode blocks; provider stream integration remains pending |
| P0-COMP-001 | partial | `internal/context/compaction.go` records source window, summary, archive, quality and replay lineage; production archive wiring remains pending |
| P0-CTX-007 | partial | `internal/context/diff.go` emits deterministic digest-only added/removed/changed block projections with token and reason accounting |
| P0-COMP-002 | partial | Compaction lineage rejects non-reducing summaries and unverified mandatory recall; provider-specific summarizer gates remain pending |
| P0-COMP-003 | partial | Compaction requires an immutable archive reference and never embeds source bytes in lineage; object lifecycle integration remains pending |
| P0-COMP-004 | partial | Deterministic recall probe checks required facts and mandatory block identity with an evidence digest |
| P0-SKILL-001 | partial | `internal/skills` defines canonical versioned SkillBundle with instructions, resources, schemas, tools, examples, capabilities, source and digest |
| P1-COMP-005 | partial | Manifest digest and tokenizer identity provide a stable cache key contract; provider-native cache adapters remain pending |
| P0-SKILL-002 | partial | Registry persists install/activate/disable/quarantine/revoke events and freezes active revisions; authoritative EventStore integration remains pending |
| P0-SKILL-003 | partial | `SkillBundle.ContextBlock` preserves source, trust, sensitivity, license, selection reason, revision and digest in context provenance |
| P0-SKILL-004 | partial | Activation checks explicit capability grants and bundles carry no secret/tool handles; all dispatch policy composition remains pending |
| P0-SKILL-005 | partial | Registry rejects duplicate schemas/capabilities, invalid dependencies and cycles, and enforces token ceilings |
| P0-SKILL-006 | partial | Ed25519 signatures, key revocation, quarantine, durable reload and lifecycle evidence are covered; distribution compatibility scan remains pending |
| P0-STORE-013 | partial | Lifecycle roots distinguish retain-until and legal hold, preserve protected objects in every proof, and perform scoped deletion only after verified inventory |
| P0-STORE-014 | partial | GC reports are content-addressed, durable and revalidated after restart; corrupt/missing artifact metadata blocks overwrite and backup/object-store proof adapters remain pending |
| P0-EVAL-024 | partial | Evaluators receive cloned bundles and results bind bundle/evaluator digests; external fixture catalog remains pending |
| P0-EVAL-025 | partial | Rule evaluator is separate from model/human evaluators at the interface boundary; model/human adapter implementations remain pending |
| P0-EVAL-026 | partial | SessionBundle records schema/source digest, tenant scope and redaction state; dataset version/license/split registry remains pending |
| P0-EVAL-027 | partial | EvalRun supports revision CAS, cancellation pause/resume, unit budget and minimal failing bundle evidence |
