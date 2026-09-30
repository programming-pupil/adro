# Release status

The v2 rebuild has not reached a release milestone. The frozen `v1-final` tag
is for reference only. Existing signature and dependency verification assets
are retained during migration; they do not establish v2 release readiness.

Release acceptance requires the implemented stage gates, real PostgreSQL and
process tests, and the task ledger to be complete. Record each command actually
run and its result. Missing or skipped checks are not successful acceptance.
