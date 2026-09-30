# adro

adro is undergoing a v2 rebuild. This branch contains retained Go libraries,
asset migrations, and the first static checks. Stage zero is incomplete.
The new single executable and its server command are not available yet.

The frozen `v1-final` tag is a read-only reference. This branch does not maintain
v1 compatibility. Historical deployment and launch instructions have been removed.

## Local verification

Use the Go version declared in `go.mod`:

```sh
go build ./...
go test -race ./core/errs ./internal/upstream/scan ./internal/upstream/supervisor
go test -race ./tools/namingdeny/...
```

These commands cover existing components; they do not constitute stage-zero or
storage acceptance. The repository-wide naming check currently fails on retained
legacy content and unresolved naming-policy conflicts. The remaining gates,
production assembly, and PostgreSQL integration are still under development.
