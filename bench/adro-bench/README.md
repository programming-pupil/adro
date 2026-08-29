# ADRO Bench

The benchmark fixture records deterministic platform behavior independently of
model quality: explicit repository recall, duplicate event handling, evidence
hashes, repair attempt limits, and cursor recovery. Each run writes JSON with
the ADRO version, provider, fixture commit and environment. Model effectiveness
must be reported over repeated runs with intervals; a single successful demo is
not a benchmark result.

The first fixture is `examples/three-repo-feign`. Run the API and execute its
script to produce the raw delivery graph that a benchmark runner can capture.
