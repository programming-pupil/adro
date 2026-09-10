## Workflow modes

ADRO's graph-native path freezes an Agent, a published Squad revision, or a
request-specific `WorkflowGraph` into an immutable `RequirementExecutionPlan`.
Agent, nested Squad, Gate, Human, Merge, and Repair nodes can be connected by
typed success/failure/timeout/approval/bug/cancel edges. Structured predicates,
fan-out/join policies, evidence requirements, budgets, retries, deadlines, and
bounded loop traversals are validated before execution. Every node attempt,
feedback decision, repair lifecycle, lease/fencing token, and selected edge is
available through the plan timeline and replay APIs.

The Web workbench exposes the same graph contract through Agent and Squad
editors, validation, dry-run, requirement quick-Squad creation, execution-plan
selection, and timeline/replay views. Plans pin definition revisions, so later
Agent or Squad edits affect only new executions.

ADRO also keeps the historical seven-stage pipeline as a compatibility
default. Requirements may point at a `workflow_template_id` and select either
`automatic` or `design_approval`. A template is an ordered, immutable-at-run
time list of validated `WorkflowStep` records. Every step names an agent,
optional role/configuration, and its retry limit. The mandatory report step
keeps successful compatibility runs auditable.

When a design-approval run emits its design result, the pipeline durably moves
to `waiting_approval` and creates an approval record. The approval decision is
the only operation that advances the run. Rejection is terminal for that run;
approval resumes at the next selected step with the same durable harness
session.

## Comment handoff

Requirement and Bug threads use structured Agent and Squad mention URIs. The
picker resolves only authorized targets from the current workspace roster;
plain display names never dispatch work. Each target receives a durable
queued/coalesced/deferred/blocked receipt tied to the comment revision and
originator lineage. Member and issue mentions are render-only, while `@all`
is a broadcast outcome and never expands into Agent fan-out. Editing or
retrying a comment recomputes only that revision's pending targets.

## Ordinary chat

`POST /api/v1/chats` creates an ADRO-owned conversation that is independent of
requirements. It can carry a project ID, and files are uploaded through the
existing ArtifactStore attachment API using `owner_type=chat_session`.
`POST /api/v1/chats/{id}/messages` appends a user message to both the chat
projection and the harness transcript. The response includes the turn hash and
context status, so clients can render compaction, archive, memory, and
checkpoint health without provider-specific state.

The chat projection is persisted in the same versioned control-plane snapshot
as requirements and workflow templates. Production adapters should map the
included migration to PostgreSQL and keep the harness on a transactional
append-only store; the local profile remains deterministic and crash-safe.

## Provider continuity

The logical ADRO session always survives API/provider restarts. For native
continuation, ADRO reuses the provider issue only when provenance proves the
same native session and normalized work directory. A missing local child
process cannot be resurrected in place; recovery submits a continuation through
the provider's documented resume contract and fail-closes on a session/workdir
mismatch. This distinction is intentional: it prevents a silent fresh run from
being presented as a repair with preserved context.
