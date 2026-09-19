# Secret Broker and Boundary Redaction

Design source: `ADRO-origin` and public least-privilege security practice.

`ports/secretstore` separates opaque secret identity, lease metadata, and plaintext material. Events, logs, traces, prompts, and durable records may retain a valid `SecretRef`; they must not retain material returned by a broker.

## Reference and lease contract

A `SecretRef` is a bounded opaque identifier beginning with `secret:`. JSON decoding rejects malformed or plaintext-shaped references.

`SecretBroker.Resolve` accepts a reference together with tenant, session, effect, destination, purpose, and TTL. It returns a `SecretLease` containing metadata only. There is no plaintext field in the serializable lease.

A caller obtains material through `SecretBroker.Material` and must present the full lease scope again. A mismatch in tenant, session, effect, destination, or purpose fails closed. Expired and revoked leases cannot return material. `Revoke` erases the lease-owned copy held by the reference adapter.

Callers must minimize the lifetime of the returned byte slice, clear owned buffers when practical, and never convert material into an event, log, trace attribute, prompt field, global environment variable, or diagnostic response. Production adapters should prefer a short-lived file descriptor, controlled file, or proxy channel at the destination boundary.

## In-memory reference adapter

`adapters/secret/memory` exists only for development and deterministic tests. `Put` copies material and binds it to one tenant plus explicit destination and purpose allowlists. Empty allowlists are rejected rather than treated as wildcards. Each lease receives a separate material copy, and each `Material` response is copied so a caller cannot mutate broker state.

The adapter is not encrypted storage, is not durable, has no external key manager, and retains the registered source secret for the process lifetime. It is not a production secret manager and must not be used to claim production secret isolation.

## Sensitivity classification and redaction

`internal/security` defines the sensitivity classes `public`, `internal`, `confidential`, `restricted`, and `secret`, and the surfaces `prompt`, `tool_input`, `tool_output`, `trace_attribute`, `event`, and `log`.

The redactor applies three fail-closed controls:

- recursive credential-key detection for passwords, tokens, authorization, cookies, keys, credentials, and related names;
- explicit `sensitivity` metadata and `ClassifiedValue` labels;
- depth bounds and malformed-value fallback to `[redacted]`.

A syntactically valid opaque `secret:` reference is preserved for audit even under a sensitive key. Its material is never preserved.

Current integrations replace the former orchestration diagnostic redactor and sanitize OpenTelemetry attributes before export. Trace attributes remain capped at 32 entries and 256 bytes per value, with high-cardinality identifiers excluded separately.

## Evidence

The contract and canary tests are in:

- `ports/secretstore/secretstore_test.go`
- `adapters/secret/memory/memory_test.go`
- `internal/security/redaction_test.go`
- `internal/api/orchestration_diagnostics_security_test.go`
- `internal/telemetry/trace_test.go`

They verify copied input/output material, tenant/destination/purpose enforcement, complete lease-scope binding, expiry, revocation, metadata-only JSON, recursive redaction, malformed input, opaque-reference preservation, and absence of canary plaintext from serialized diagnostics and trace attributes.

## Production gaps

This capability is not stable. A production secret-manager adapter, durable revocation audit, key rotation, destination-side file-descriptor or proxy injection, sandbox integration, adapter-boundary prompt/tool redaction across every real execution path, log bridge coverage, end-to-end canary leak tests, Runtime Inspector/API surfaces, and production fault/conformance evidence remain required.
