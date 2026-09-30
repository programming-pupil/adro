# Repository-change evaluation input

`tasks/three-repo-feign.json` retains the baseline's three repository actions,
four delivery gates and four metric names byte for byte. It is preserved input,
not an executable v2 suite. No baseline or successful score has been invented.

V2-13 owns the real consumer (`internal/evals`) and dataset conversion described
in section 17.7. It must add complete initial workspace fixtures, recorded
trajectories, grader definitions and bounded budgets before activating this
suite. Required-repository recall and optional-repository precision use actual
repository diffs; deduplication uses recorded event/effect identities; evidence
completeness uses verified execution evidence. Human acceptance and review gates
must retain their original meaning.

V2-01 item 006 remains pending until the benchmark workload is migrated and the
evaluation asset's consumer is registered. This input must not be counted as a
passing evaluation or silently included in an active gate before then.
