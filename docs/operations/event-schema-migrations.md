# Event schema migrations

ADRO stores event envelopes as immutable bytes. A reader authenticates the stored envelope first, then applies an explicit in-memory upcaster chain before passing the derived envelope to a pure reducer. The stored payload, payload digest, envelope digest, sequence and hash chain are never rewritten during replay.

`core/event.Registry` accepts only adjacent migrations (`vN -> vN+1`) registered for the exact event type. A missing step, a future version, an invalid JSON result, or a downgrade attempt fails closed. Replay evidence retains the original envelope digest and records every version reached by the upcaster path.

Typed projections choose an unknown-field policy explicitly. `RejectUnknownFields` is the default for business decisions and catches schema drift. `PreserveUnknownFields` is reserved for metadata-only projections that do not make authorization or state-transition decisions from fields they do not understand.

A full stream is validated with `event.ValidateChain`; paginated readers use `ValidateChainFrom` with the preceding sequence and digest. `reducer.ReplayVerified` combines chain verification, upcasting, pure reduction and a deterministic state digest. It is safe to use after loading a verified snapshot because the caller can pass the snapshot sequence and preceding digest to the page validator and still rebuild from sequence zero when the snapshot is missing or corrupt.

Schema changes must ship with:

- an adjacent upcaster and a fixture for each supported historical version;
- a reducer replay test that records the original digest and upcaster path;
- a negative test for unknown future versions and missing migrations;
- an explicit unknown-field policy;
- a compatibility note describing whether downgrade is blocked before deployment.

The reference EventStore adapters continue to persist the original envelope JSON. Upcasting is a reader/projection concern and must not be used to conceal corruption or to mutate authoritative history.
