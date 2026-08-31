# Release runbook

ADRO releases the control plane and its native local profile independently of
any external orchestration product. A passing unit suite is necessary but not
sufficient for an enterprise GA claim.

## Reproducible inputs

Use the locked Go and Node manifests from a clean Git checkout:

```bash
go mod download
npm ci
node scripts/release-assets.mjs generate
node scripts/release-assets.mjs verify
```

## Local verification

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./...
make contracts
ADRO_EXECUTOR=/path/to/claude ./start.sh --no-open
./start.sh --status
./start.sh --stop
```

The smoke run must use a real installed coding client. Record the client
version, session ID, worktree, git baseline/head, process exit, test results,
repair continuity, artifacts, attachments, and final report. Missing model
credentials are an explicit prerequisite, not a reason to substitute a fake
runtime result.

## Production gate

Before publishing GA, install and contract-test every selected external plugin
for Git, CI, deployment, identity, secrets, event transport, persistence and
notifications. Measure restart recovery, concurrency limits, audit retention,
RTO/RPO, and security isolation. Plugins are versioned and can be rolled back
without changing ADRO domain records.

## Rollback

Stop the API and WebUI, retain a copy of the complete state/artifact volume,
deploy the previous tagged binaries, and restore matching snapshots together.
Never mix control, event, auth, audit, runner, or artifact versions.
