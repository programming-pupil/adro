# Contributing

Contributions should preserve provider independence and the versioned API
contracts. Run `gofmt`, `go vet ./...`, `go test ./...`, and the race tests
before opening a change. New public endpoints require an OpenAPI update,
request-id/error-code behavior, an idempotency decision, and focused tests.
Security issues should follow `SECURITY.md` rather than a public issue.
