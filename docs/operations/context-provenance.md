# Context provenance and prompt trust zones

Context integrity and context authority are different properties. A correctly hashed user message, retrieved document, tool result, remote response, or model summary is still untrusted content. ADRO records that distinction in every compiled context block and prompt segment.

## Provenance contract

`internal/security.Provenance` carries:

- source identity;
- trust level;
- sensitivity;
- tenant scope;
- purpose;
- canonical taint labels.

Runtime-defined trust zones assign the maximum permitted trust. System and workspace policy can be trusted, signed artifacts can be verified, and user/retrieved/tool/remote/model/unknown content is untrusted with a zone-specific taint. A caller may lower trust or add taint; it cannot elevate a zone. Derived content preserves the weakest input trust, highest sensitivity, complete taint union, and exact tenant scope.

`internal/context` normalizes provenance before selection, compaction, hashing, and rendering. A manifest containing mixed tenant scopes is rejected. Semantic summaries derive provenance from every summarized input, so model output cannot turn untrusted memory or tool content into trusted policy.

## Prompt manifest v2

Prompt manifest v2 binds provenance to the exact selected block set. Each segment includes its hash, source, semantic kind, token budget, trust, sensitivity, tenant scope, purpose, taints, and content. Manifest validation checks:

- canonical semantic ordering;
- unique segment and block IDs;
- full block coverage;
- exact content/hash/source/provenance binding;
- required block preservation;
- token totals and immutable digests.

The provider-neutral renderer emits one JSON object per segment. Untrusted newlines, closing markers, or JSON-shaped text remain escaped inside the content field and cannot forge a sibling structural segment. Legacy prompt manifest v1 remains readable only when paired with the legacy compiler version; new compilation emits v2.

## Compatibility and limitations

Durable harness manifests copy the typed provenance fields so retries and replay use the same context security metadata. Existing callers that still provide the legacy `trust` string are normalized at compilation.

The current implementation protects the shared context compiler and harness path. Remaining work includes migrating every real provider/tool/memory adapter to the same typed boundary, storing large content in classified blobs, enforcing data-egress obligations, and exposing provenance and taint explanations in the Runtime Inspector.
