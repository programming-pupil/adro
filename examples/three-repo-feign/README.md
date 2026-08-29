# Three-repository contract example

This fixture is intentionally small and deterministic. `feign-contract` owns
the `InviteResponse` contract, `provider-service` exposes `POST /invite`, and
`caller-service` consumes it. The scenario demonstrates explicit repository
scope, a required provider change, a caller migration, and a test-only
repository. It is data for `adro-bench`, not a claim that ADRO can silently
clone or modify arbitrary repositories.

Run the local API first, then execute `./run.sh`. The script creates a
requirement with all three repositories, starts it, and verifies the resulting
WorkItems and event cursor.
