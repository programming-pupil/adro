# ADR-0002: Durable Effects And Unknown Outcomes

- Status: accepted
- Date: 2026-09-17
- Design source: `ADRO-origin`

## Context

A local transaction cannot atomically include a non-transactional external system. Committing an intent before a callback proves authorization and planned execution, not that the remote effect completed.

## Decision

Write tools use explicit `intent -> dispatch prepared -> dispatched -> receipt` facts. A crash, timeout, or lost response after durable dispatch and before receipt produces `outcome unknown`.

Read-only work may retry under a bounded policy. Idempotent writes require the same externally enforced key. Reconcilable writes query external state before deciding. Non-retriable writes require human resolution. No write is replayed solely because its local receipt is absent.

Receipt and the corresponding model-visible tool completion commit atomically. Stale fencing tokens cannot commit either record.

## Consequences

- ADRO does not claim exactly-once execution for non-transactional external effects.
- Recovery and UI must expose unresolved outcomes instead of presenting them as failures or successes.
- Tool contracts must declare effect class and reconciliation behavior before write retries can be enabled.
