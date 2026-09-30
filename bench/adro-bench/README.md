# Benchmark asset migration

The repository-change evaluation input is preserved at
`testdata/evals/repository-change/tasks/three-repo-feign.json`. It is a task
specification, not a performance benchmark or evidence of a successful run.
V2-13 owns its conversion into the evaluation dataset and recorded graders.

The resource-selection benchmark source remains in
`internal/orchestration/resource_accounting_benchmark_test.go` until its tested
algorithms are separated from the old storage and scheduling code. V2-01 must
preserve its workloads before deleting that package; V2-11 owns the pure
selection implementation and V2-13 owns benchmark integration.

Preserve these assertions when changing the benchmark's production target:

| Workload | Required correctness assertion |
|---|---|
| 1024 requests; noisy and quiet tenants with weights 1:4 | The quiet tenant appears in the early selection window; use tenant identity, not an unrelated virtual-finish comparison as a proxy |
| 4096 requests across 32 tenants | Every request is selected exactly once and the pending queue is empty |
| One occupied concurrency slot and 256 waiting requests | All later requests wait; none reserve resources beyond quota |
| Worker jitter and load shedding | Shed plus selected equals submitted, with no overlap; persisted or recovery work is retained |
| An old low-priority request ahead of 1023 new high-priority requests | Aging brings old work into the first selection window |

Fixed inputs, allocation reporting and assertions belong in the timed benchmark
path. Timing an incorrect selection must fail the benchmark. The preserved
baseline is available under the read-only `v1-final` tag.
