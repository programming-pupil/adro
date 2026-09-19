# MCP protocol boundary

Design source: `ADRO-origin`, with `public-standard-derived` JSON-RPC and MCP transport framing.

`internal/mcp.Client` owns the connection boundary. It does not own approval, effect receipts, audit truth, or runtime state. A caller must complete those durable checks before invoking a tool; the client only makes a negotiated protocol call after the caller has selected the server.

## Negotiation and schema identity

Every session sends `initialize` with the supported protocol version list's newest version, the ADRO client identity, and the tool capability declaration. The server must return an explicit supported `protocolVersion`; unknown versions fail closed. `notifications/initialized` is sent before any tool operation. The HTTP transport retains `Mcp-Session-Id` and sends the negotiated `MCP-Protocol-Version` header on subsequent requests.

`tools/list` is treated as a versioned tool contract. The response is canonicalized with the runtime JSON encoding and hashed. A configured `schema_digest` must match exactly, and invocation rejects missing or duplicate tool definitions, deleted tools, malformed input schemas, and missing required arguments. A caller can run discovery to record a new digest, but the client never silently updates an approved server's digest.

## Transports and limits

The same tool contract runs over streamable HTTP, legacy SSE responses, and an explicitly enabled stdio process. HTTP accepts JSON or SSE `data` frames and bounds request and response bytes. Stdio uses direct `exec.CommandContext` (never a shell), bounded newline framing with compatibility support for `Content-Length`, strict JSON-only stdout, context cancellation, and process teardown. Stdio is disabled by default. Environment injection is separately disabled by default; enabling it is an explicit host policy decision.

A `SecretRef` is never serialized into JSON-RPC. `SecretResolver` is an injected boundary; `BrokerSecretResolver` binds material to tenant, session, effect, destination, purpose, and a short lease, then revokes the lease after the call. If a reference cannot be resolved, the connection is rejected. Recursive secret-like fields in tool arguments are rejected before transport dispatch.

## Remaining integration gates

The protocol adapter is experimental. MCP calls still need durable effect intent/receipt/unknown-outcome records, the shared capability policy and sandbox brokers, production Secret Manager wiring, persistent connection/session projection, and API/Inspector stream evidence. Those gates keep MCP below `stable`.
