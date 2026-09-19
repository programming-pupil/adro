# Extension security and supervision

ADRO treats an extension installation and a running extension process as two separate boundaries. The control plane owns publisher trust, signatures, activation, compatibility, rollback, and quarantine. The runtime consumes only the immutable execution grant defined in `ports/extensions`; it does not import registry storage or accept caller-supplied permissions as authority.

## Signed installation lifecycle

`internal/plugins.Registry` persists a versioned registry document with these facts:

- Ed25519 publisher keys and their `active`, `retired`, or `revoked` state.
- A canonical manifest digest and publisher signature for every installation.
- Tenant and workspace ownership.
- The active version and activation history for each workspace/plugin pair.
- Health state, quarantine reason, key replacement chain, and compatible rollback history.

A manifest declares protocol and adapter versions, schema and artifact digests, execution mode, message bound, capabilities, generic permissions, filesystem operations, network destinations, secret scopes, and data-egress sensitivity. Canonical sorting makes the signature independent of set ordering. Install, activation, execution authorization, rotation, revocation, and rollback all revalidate the stored signed record. The registry-issued runtime grant preserves every signed permission class, including data-egress obligations, instead of reconstructing policy from caller input.

Rotation requires the current private key to sign the replacement key transition. A retired key cannot sign a new installation but existing installations remain verifiable. Revocation quarantines every installation signed by that key and removes active routing. Rollback selects only a previous signed installation whose protocol and schema compatibility are explicit.

Administrative APIs are exposed under `/api/v1/plugins`, including the trust-store, rotation, revocation, activation, quarantine, health, and rollback routes. The request identity supplies tenant and workspace scope; JSON bodies cannot move installations across that boundary.

## Runtime boundary

`runtime/extensions.Supervisor` accepts an installation identity and asks a `ports/extensions.Authorizer` for the canonical grant. The authorizer must return the same tenant, workspace, plugin, version, digest, signature, and key identity. The supervisor then uses only the returned manifest.

Untrusted adapters run through `ports/sandbox.SandboxBroker` as independent processes. The process receives:

- An explicitly constructed environment rather than the host environment.
- Workspace-relative file grants with separate read, write, create, delete, and execute permissions.
- Network grants containing a selector, ports, protocol, purpose, and finite expiry.
- Opaque secret references only when the signed manifest declares secret access.
- A finite wall timeout, output budget, and negotiated protocol frame bound.

The runtime exposes newline-delimited JSON-RPC 2.0 over the bounded execution stream. It does not expose EventStore, database, lease, scheduler, or raw secret handles.

## Handshake and calls

Before dispatch, the adapter must complete a bounded handshake containing:

- protocol version;
- adapter version;
- schema digest;
- supported capabilities;
- required permissions;
- maximum message size.

The adapter may return a subset of signed capabilities and permissions. It cannot add privileges, enlarge the message bound, change protocol/adapter identity, or select an incompatible schema. Runtime calls are serialized, must name a signed capability, carry a correlation ID, and return valid JSON within the negotiated bound. A caller cannot invoke the handshake method after startup.

Trusted in-process adapters require both a signing key explicitly approved for in-process execution and an explicit factory. Panics are recovered at factory, handshake, call, and close boundaries. A panic, invalid JSON response, or oversized response quarantines the instance. This containment protects the runtime process from the adapter's Go panic, but an in-process adapter still shares the host address space; approval must therefore be limited to audited, version-locked code.

WASI manifests are recognized by the signed contract, but the supervisor rejects them until a dedicated WASI runtime is configured. It never routes WASI declarations through an unrestricted process runner.

## Crash recovery and quarantine

The supervisor owns process input, output, cancellation, and disposal. It applies one cumulative restart budget across the instance lifetime, exponential backoff with a configured cap, and a bounded handshake timeout. A clean unexpected exit is treated as a crash. Once the restart budget is exhausted, the supervisor:

1. marks the instance quarantined;
2. persists quarantine through the configured port;
3. emits bounded audit data without copying arbitrary adapter output;
4. rejects subsequent calls.

Extension process recovery does not decide the outcome of a dispatched write effect. Effect recovery remains in the durable effect state machine: an absent receipt becomes outcome-unknown and requires policy-driven query, compensation, or human resolution.

## Threat evidence

`docs/rebuild/threat-test-map.json` is the source of threat-to-test traceability. `scripts/verify-threat-test-map.py` checks both directions: every mapped Go test must carry the threat ID next to its test function, and every annotated test must appear in the map. The contracts workflow runs this verifier.

The current malicious-extension suite covers protocol mismatch, capability overclaim, changed authorization identity, undeclared calls, silent handshake timeout, forged response IDs, protocol flood, host-environment injection, missing secret references, repeated crash/exit, bounded restart/quarantine, in-process panic, oversized response, and invalid JSON.

## Current limitations

The local sandbox is a development/reference backend and does not claim secure tenant isolation. macOS Seatbelt has real deny-default tests; production OCI/remote isolation, controlled network proxying, Linux and Windows OS isolation, hard-link/mount/TOCTOU protection, CPU/memory/disk enforcement, destination secret injection, and payload-sensitivity enforcement against the signed data-egress grant remain required. The supervisor is a tested runtime component but is not yet wired into every production adapter call path or Runtime Inspector page. WASI execution and malicious receipt verification at a remote adapter boundary also remain pending.
