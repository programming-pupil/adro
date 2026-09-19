# Verified identity and service credentials

ADRO carries one verified `core/identity.Actor` across an authenticated request. The API authentication boundary validates the credential, freezes tenant and workspace scope, binds the actor to `context.Context`, and overwrites legacy identity headers before a handler runs. Runtime and API code must read the context actor; request bodies and caller-supplied actor headers are display input, not authority.

The closed actor vocabulary is `human`, `service`, `agent`, `worker`, and `extension`. Every actor records its authentication method, credential ID, audience, canonical issue and expiry times, tenant, workspace, and any delegation chain. Tenant and workspace cannot change during delegation. Credential lifetime can only shrink. Impersonation, takeover, and break-glass transitions require an approval ID; all transitions retain the prior effective actor, reason, mode, and time.

Local human login sessions are random bearer credentials held only as hashes. Disabling a user or logging out revokes the corresponding session. The API maps an active session to a `human` actor and records its credential ID and lifetime in audit evidence.

Service, agent, worker, and extension callers use short-lived Ed25519 credentials. A token binds the exact actor type and ID, tenant, workspace, audience, issue time, expiry, credential ID, and optional delegation chain. The API accepts only the `adro-api` audience. It rejects malformed signatures, non-canonical payloads, future or expired tokens, overlong lifetimes, revoked credentials, revoked keys, unknown actor types, and request scope headers that differ from the verified credential.

`ADRO_API_TOKEN` is deliberately unsupported. If it is set, startup readiness fails. Configure `ADRO_SERVICE_CREDENTIAL_FILE` with a mode-`0600` authority file instead. When the native launcher finds `<ADRO_HOME>/service-credentials.json`, it selects that file automatically; an explicitly configured missing or unsafe file fails closed.

## Operator workflow

Initialize an authority once. The command refuses to overwrite an existing file.

```bash
go run ./cmd/adroctl service-credential init \
  --file .adro/service-credentials.json \
  --key-id service-key-2026-09
```

Issue a credential no longer than 15 minutes. Keep the returned bearer token in the target workload's secret channel; logs and durable state should retain only `actor.credential_id`.

```bash
go run ./cmd/adroctl service-credential issue \
  --file .adro/service-credentials.json \
  --type worker \
  --id worker-west-1 \
  --tenant tenant-1 \
  --workspace workspace-1 \
  --audience adro-api \
  --ttl 5m
```

Start the API with the same authority file:

```bash
ADRO_AUTH_MODE=required \
ADRO_SERVICE_CREDENTIAL_FILE=.adro/service-credentials.json \
./start.sh --no-open
```

Rotate by retiring the active key and creating a new key. Credentials issued by the retired key remain valid only until their existing expiry, which permits a bounded rollout. The retired private key is erased from the state file.

```bash
go run ./cmd/adroctl service-credential rotate \
  --file .adro/service-credentials.json \
  --current-key service-key-2026-09 \
  --new-key service-key-2026-10
```

Revoke one credential by the credential ID returned at issuance, or revoke a retired key to invalidate all of its outstanding credentials immediately:

```bash
go run ./cmd/adroctl service-credential revoke-credential \
  --file .adro/service-credentials.json \
  --credential-id 'credential:...'

go run ./cmd/adroctl service-credential revoke-key \
  --file .adro/service-credentials.json \
  --key-id service-key-2026-09
```

The authority file is updated with an atomic write, `fsync`, rename, and directory sync. Only one active signing key is valid. Active-key revocation is rejected until rotation establishes a replacement.

## Audit and failure behavior

Authenticated audit events record the effective actor, original actor, complete delegation chain, and credential ID. A caller cannot replace an authenticated approval reviewer, takeover owner, comment author, or automation actor with a body or header value. Optional local authentication continues to support explicit legacy headers for development, but presenting any invalid bearer credential still returns `401` instead of falling back to anonymous access.

The shipped authority is a single-node reference boundary. The private signing key is stored in the local mode-`0600` file, sessions are process-local, and revocation reload is process-start based. Production and HA profiles still require a conformance-tested workload identity provider, shared revocation, protected key storage, authenticated key rotation, and propagation evidence before their identity capability can be marked stable.
