## ADRO release acceptance

The checked-in release is a single-node reference profile. A release candidate
must pass `go test ./...`, `go test -race ./...`, `go vet ./...`, `go build
./...`, `make contracts`, and the browser matrix. The real end-to-end report
must use an installed coding client and record session, worktree, git, check,
repair, attachment, and report evidence. Test doubles may cover unit contracts,
but cannot be used as runtime proof.

Run the model-backed acceptance path with `make real-e2e`. It is intentionally
outside the default `verify` target because it requires an authenticated local
Claude Code/Codex installation and can take several minutes. The script starts
the native profile only; Docker is neither required nor started.
