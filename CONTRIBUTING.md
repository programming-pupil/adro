# Contributing

This branch is rebuilding adro v2. Read `README.md` and `docs/architecture.md`
for the current implementation boundaries. Keep changes scoped to the active
task and use `ci/paths.yaml` to identify retained migration inputs.

Run formatting, affected package tests with the race detector, and `go build ./...`
for code changes. Report the exact checks executed, failures, and checks not run.
Missing infrastructure is not passing evidence. The full milestone checks are
required before claiming stage acceptance.

Do not add compatibility wrappers for the retired runtime. Preserve third-party
license text and update generated dependency notices when dependencies change.
Follow `SECURITY.md` for private vulnerability reports.
