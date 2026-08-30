# Release runbook

ADRO 0.1.0 is a local reference release until every production and real
Provider gate is reproducibly green. A passing unit or browser suite cannot
promote it to GA.

## Reproducible inputs

Install Go 1.24 and Node 22, then use locked dependency manifests:

```bash
go mod download
npm ci
node scripts/release-assets.mjs generate
node scripts/release-assets.mjs verify
```

The generator compares `go list -m` and every `package-lock.json` package with
`release/dependencies.json`, writes the SPDX 2.3 `SBOM`, writes
`THIRD_PARTY_NOTICES`, and copies the exact upstream license texts into
`THIRD_PARTY_LICENSES/`. An unknown or changed dependency fails verification.

## Source provenance

Release manifests can only be created from a clean Git checkout whose HEAD has
the exact `v<package.json version>` tag:

```bash
git status --short
git tag -s v0.1.0
node scripts/release-assets.mjs manifest --output dist/release-manifest.json
node scripts/release-assets.mjs verify-manifest dist/release-manifest.json
```

The manifest records commit, Git tree, signed-tag name, commit time, toolchain
versions, and SHA-256/size for SBOM, notices, OpenAPI and dependency locks. The
command fails when `.git` is absent, the tree is dirty, or tag/version differ.
The current in-place Multica work directory has no Git metadata, so it cannot
produce a trustworthy manifest; perform this step only after the changes are
committed in a real checkout.

## Verification

```bash
make verify
```

This runs Go unit/race/vet/build, startup credential-mode regression, script and
JavaScript syntax checks, OpenAPI/YAML/JSON contracts, supply-chain verification,
the complete Chromium workflow, and the five-project browser smoke matrix.

For a real Multica release candidate, start ADRO with a real Provider and run:

```bash
ADRO_CONFORMANCE_BASE_URL='https://adro.example.test' \
ADRO_CONFORMANCE_USERNAME='release-verifier' \
ADRO_CONFORMANCE_PASSWORD='<secret>' \
ADRO_CONFORMANCE_WORKSPACE_ID='<adro-workspace>' \
ADRO_CONFORMANCE_PROVIDER_WORKSPACE_ID='<multica-workspace-uuid>' \
ADRO_CONFORMANCE_REPOSITORY_ID='<adro-repository>' \
ADRO_CONFORMANCE_MEMBER_ID='<adro-member>' \
ADRO_MULTICA_TOKEN='<multica-pat>' \
ADRO_MULTICA_WS_URL='wss://multica.example.test/api/events' \
ADRO_CONFORMANCE_REPORT='dist/multica-conformance.json' \
node scripts/multica-conformance.mjs
```

Exit 0 means every required real run/session/workdir, commit, test, submission,
attachment, daemon WebSocket and same-session repair check passed. Exit 2 means
`blocked` and the JSON report identifies the missing input or upstream
capability. Exit 1 means a claimed capability failed. Never convert either
non-zero result into a pass by substituting MockProvider evidence.

`ADRO_CONFORMANCE_WORKSPACE_ID` is the local ADRO workspace identifier. Native
Multica task cleanup uses the provider workspace UUID, supplied separately as
`ADRO_CONFORMANCE_PROVIDER_WORKSPACE_ID` (or inherited from
`ADRO_MULTICA_WORKSPACE_ID`); the two values are not interchangeable.

## Rollback

Do not publish GA until the production adapter migration and recovery sequence
in `docs/architecture/production-deployment.md` has measured RTO/RPO evidence.
For the shipped single-node profile, stop the API, retain a copy of the full
volume, deploy the prior image, and restore the matching snapshot set together;
mixing control, event, auth, audit, runner, or artifact versions is unsupported.
