# Durable Runtime Lifecycle

Design source: `ADRO-origin`.

`runtime/lifecycle.Manager` owns the component graph and derives process
health from component health. It validates unique names, missing dependencies,
duplicate dependencies and cycles before startup, then starts components in a
stable topological order and stops successfully started components in reverse
order. A failed startup cancels the root context and rolls back only components
that completed startup.

`NewManagerWithTimeouts` applies an explicit bound to each component startup
and shutdown call. Components own their listeners, workers and connections;
when startup returns an error or a timeout, the component must release any
resources it acquired before returning. `Stop` is idempotent and aggregates
all stop errors while continuing reverse-order cleanup.

`Snapshot` keeps component health as the authoritative input and exposes a
derived process view with `ready`, `live`, aggregate status, the latest change
time and the first actionable reason. Readiness and liveness are independent:
a degraded component may remain live while failing readiness. The manager does
not write business events or mutate component state while sampling health.

The reference tests cover deterministic topology, rollback, repeated stop,
error aggregation, startup timeout, status aggregation and readiness/liveness
separation. Production integration still must connect this contract to every
runtime component and expose authenticated probes and structured health events.

Shutdown timeout and owned-worker tests additionally prove cooperative context
cancellation and bounded cleanup for the reference contract. Startup and stop
bounds require every component implementation to honor its context; a component
that ignores cancellation violates the `Component` contract and cannot be made
safe by detaching an unowned goroutine.

If a component stop returns an error, the manager retains that component in its
owned set and rejects restart until a later `Stop` call completes cleanup. This
prevents a transient shutdown failure from being forgotten and prevents a
second component instance from starting over leaked resources.
