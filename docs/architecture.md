# Current implementation boundaries

Stage zero is incomplete. This branch preserves selected libraries and regression
inputs while removing the previous runtime. It currently has no server executable.
The `v1-final` tag is a read-only source reference, not a supported deployment.

| Area | Present code | Acceptance still required |
|---|---|---|
| Core | Error classification, event and identity contracts, provenance and budget values | New IDs, injected clock, and kernel integration |
| Orchestration | Retained graph rules, route parser and mention parser | Removal of old execution/storage dependencies and new scheduling behavior |
| Execution assets | Atomic file publication, directory synchronization, child-process protocol fixtures | Production process supervision and recovery |
| Checks | Strict hashed naming policy; release metadata and signature verification | Complete static gates, production assembly checks and milestone tests |

`ci/paths.yaml` records retained porting inputs and their owning tasks. A listed
package is not proof of production readiness. Storage code that remains is under
migration; v2 storage acceptance will use PostgreSQL. No browser application or
Node toolchain is required.

The naming check still fails on retained content and unresolved policy conflicts.
Run the checks documented in the root README for the already implemented assets;
record database, integration, and milestone checks separately when they are run.
