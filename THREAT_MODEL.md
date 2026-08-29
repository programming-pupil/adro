# Threat model

ADRO treats repository files, issue descriptions, comments, MCP responses,
logs, and web pages as untrusted input. The primary boundaries are the control
API, provider adapter, runner security domain, ArtifactStore, and extension
gateway. Production deployments must enforce OIDC claims, tenant-scoped RLS,
short-lived secrets, egress allowlists, rootless isolation, redaction before
artifact upload, signed extension manifests, webhook HMAC/timestamp windows,
and immutable audit retention. A local MockProvider does not provide those
production controls.
