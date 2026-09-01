## Workflow modes

ADRO keeps the historical seven-stage pipeline as the compatibility default.
Requirements may point at a `workflow_template_id` and select either
`automatic` or `design_approval`. A template is an ordered, immutable-at-run
time list of validated `WorkflowStep` records. Every step names an agent,
optional role/configuration, and its retry limit. Design, unit test,
integration test, arbitration, and revalidation are selectable; the report
step is mandatory so every successful run has an auditable terminal artifact.

When a design-approval run emits its design result, the pipeline durably moves
to `waiting_approval` and creates an approval record. The approval decision is
the only operation that advances the run. Rejection is terminal for that run;
approval resumes at the next selected step with the same durable harness
session.

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
