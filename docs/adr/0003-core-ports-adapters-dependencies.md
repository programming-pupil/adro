# ADR-0003: Core, Ports, And Adapters Dependencies

- Status: accepted
- Date: 2026-09-17
- Design source: `ADRO-origin`

## Decision

The target dependency direction is:

```text
core <- ports <- runtime <- controlplane
                 ^              ^
                 +--- adapters -+
core/ports/runtime/controlplane <- api
```

`core` uses the standard library and approved pure-data dependencies. `ports` contains narrow interfaces and DTOs. `runtime` owns state-machine policy. `adapters` implement ports without changing reducer semantics. `controlplane` schedules runtime work and cannot update session state directly. `api` submits commands and reads projections.

`internal/architecture/dependencies_test.go` parses production imports in these roots and rejects reverse or undeclared layer dependencies.

## Consequences

- Legacy `internal/*` packages are migrated incrementally and are not grandfathered into the new core.
- Composition belongs in commands, not package-level registries or `init` hooks.
- Example packs may depend on public contracts; core packages cannot depend on examples.
