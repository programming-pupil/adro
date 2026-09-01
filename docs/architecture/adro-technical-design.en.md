# ADRO Technical Design

This document is the English companion to the Chinese technical design. It
describes the contracts that make the local profile durable, auditable, and
extensible without coupling the kernel to a particular execution vendor.

## Design goals

- Keep requirements, work items, pipeline transitions, evidence, context, and
  audit facts in the ADRO control plane.
- Preserve one logical session across design, development, testing, repair, and
  reporting. A repair may create a new attempt, but it must reuse the original
  context and provider worktree when the provider supports continuation.
- Persist before side effects. Leases, idempotency records, checkpoints, and
  outbox events make retries and process restarts deterministic.
- Fail closed when evidence, signatures, permissions, capabilities, or session
  continuity cannot be verified.

## Runtime shape

![Layered ADRO architecture](./adro-architecture.svg)

The local profile stores state in atomic mode-0600 JSON snapshots and keeps a
fsynced append-only `harness.json.transcript.jsonl` alongside a short-lived
crash-window journal. Startup verifies every transcript hash/sequence and
reconciles a missing snapshot turn from the log; mid-log tampering or reordering
fails closed. External adapters expose the same versioned interfaces and remain
explicit deployment inputs that must pass the matching conformance suite.

## Harness contract

The harness is an append-only session ledger:

1. A turn records role, prompt, response, tool events, usage, and source
   citations before it is acknowledged.
2. Checkpoints include the session sequence, phase, provider binding, workspace
   fingerprint, a hash of the previous checkpoint, and (for tool events) a
   tool-call ID whose before/after phases are validated as a pair.
3. Compaction writes an exact archive window and a replacement summary; the
   archive remains addressable for replay and audit.
4. Memory items cite transcript or artifact IDs. Replacement and supersession
   are explicit, so stale facts are not silently merged into the active context.
5. Leases, outbox events, and recovery scans make an interrupted attempt
   observable and replayable. Duplicate claims are rejected atomically.
6. Bounded sessions enable an automatic budget guard. The guard archives the
   oldest contiguous transcript window, retains a configured tail, and keeps
   source/replacement hashes so the complete transcript remains auditable.

Requirement and bug comments are immutable, workspace-scoped records with
parent/root pointers. A mention or explicit dispatch request appends a follow-up
turn to the target session and dispatches the provider only after the durable
outbox intent and before-effect checkpoint exist. Replays restore a missing
after-effect checkpoint and use provider continuity only when the original
session and work directory are proven.

Follow-up execution is itself a durable receipt, exposed through
`/api/v1/comments/{comment_id}/follow-up`. The receipt records the selected
agent, thread turn, outbox, provider run, mode, attempts, and terminal reason.
This makes status polling and retry safe after a lost HTTP response. The
follow-up prompt includes every comment in the immutable root thread in
chronological order, not only the latest reply.

Memory is split into `working`, `session`, and `project` scopes. The local
`/memory/reduce` endpoint performs a deterministic lexical claim extraction for
fact/constraint/decision/invariant/preference lines, assigns a fingerprint, and
records conflicting claims through explicit supersession. Pinned and
high-importance facts are compiled first, expired items are omitted, and
superseded facts leave an auditable ledger while disappearing from the active
frontier. Project memory is shared by sessions with the same project ID. This
deterministic tiering intentionally works without a vector index; semantic
retrieval can be added behind the same compiler boundary later.

## Seven-stage workflow

![ADRO delivery capability map](./adro-capability-map.svg)

An integration failure carries the failed evidence, context manifest, commit
baseline, and provider provenance back to the original development stage. The
state machine refuses an unproven continuation and records a durable reason
for suspension or manual escalation.

## Extension and operations

Provider, runner, Git, CI, deployment, artifact, notification, identity,
memory, compression, and scheduler integrations are versioned SPIs. Signed
plugin manifests declare capabilities and permissions; activation requires
digest and signature verification, health checks, and administrator approval.
The watchdog records `timed_out` or `suspended` instead of leaving an attempt
in an unbounded waiting state.

See `adro-technical-design.zh-CN.md` for the full field-level contract and
`../product-requirements.en.md` for the English product requirements.
