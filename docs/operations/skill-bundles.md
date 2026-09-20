# SkillBundle Contract

`internal/skills` defines the versioned `SkillBundle` boundary. Instructions, resource digests, tool schemas, examples, declared capabilities, dependencies, license, source, trust, sensitivity, and revision are canonicalized before a `sha256:` digest is assigned. Presentation order cannot change identity.

Registry installation records `skill.installed`; activation records `skill.activated`; disable, quarantine, revocation, and upgrade boundaries are also durable lifecycle events. An active step receives a frozen revision copy and digest. Activation requires an explicit capability grant, a bounded token budget, a permitted source, and a satisfiable acyclic dependency graph. A bundle never carries a secret value or an already-granted tool handle.

Signed non-built-in bundles require an Ed25519 signature and key identity. Key revocation moves every affected installation to `revoked`, and a reload preserves that state. `SkillBundle.ContextBlock` carries source, revision, digest, license, tenant scope, trust, sensitivity, and selection reason into the context manifest; policy still decides which declared capability is actually granted.
