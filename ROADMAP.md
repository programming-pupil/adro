# Roadmap

1. Local delivery profile: control API, domain state, seven-stage pipeline,
   same-session repair, filesystem artifacts, verified uploads, cursor events,
   WebSocket streams, repository graph, runner registration, capability
   governance, audit chain, and multilingual local workbench.
2. Production adapters: PostgreSQL persistence with RLS, OIDC/RBAC, NATS /
   Temporal, Git/CI/deployment integrations, isolated runners and cloud
   ArtifactStore drivers. Every adapter is optional and installed through the
   provider-neutral SPI.
3. Hardening: local workspace observer, HA, online migration workers,
   plugin conformance, upgrade/rollback matrix, and public ADRO Bench results.
