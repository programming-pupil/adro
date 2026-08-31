# Delivery status

The repository ships a complete, self-contained local profile: domain state,
workflow gates, provider and artifact extension points, HTTP and WebSocket
control APIs, browser workbench, runner registration, repository graph,
capability-center governance, artifact upload/migration controls, audit chain,
migrations, container packaging and contract tests. It is suitable for offline
evaluation and as the reference implementation for adapters.

Production deployment still requires configuring the optional adapter boundaries
with an organization’s Git, CI, deployment, identity, secrets and observability
systems. The local profile never claims that an external system is connected
when it is not.
