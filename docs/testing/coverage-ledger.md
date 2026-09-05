## Coverage Ledger

`ruby scripts/coverage-ledger.rb --check` is the source-of-truth inventory
check for release coverage. It reads the current checkout rather than a hand
maintained count:

- `openapi/openapi.yaml` supplies method/path operations.
- `apps/web/enhancements.js` supplies the menu registry.
- `apps/web/index.html` and `apps/web/enhancements.js` supply actionable button
  and data-attribute inventory.

The command writes `var/test-report/coverage-ledger/<source_sha>/` with
`openapi_operations.json`, `menus.json`, `dom_actions.json`, `ledger.json`,
`summary.json`, and `report.json`. Every ledger row carries the stable
`operation_id`, `menu_id` or `action_id`, `case_id`, `test_file`,
`test_function`, `layer`, `fixture`, `last_sha`, `evidence`, and
`verification_status` fields.

The inventory gate checks uniqueness, current source SHA, and the expected 19
menu entries. A derived operation ID is a visible contract gap when the
OpenAPI entry has no `operationId`; it is not silently treated as a declared
operation. `inventory-only` rows prove registration only. API behavior,
browser behavior, recovery, and real Codex evidence must be recorded by their
respective test layers before a release can claim PASS.

Generated reports remain under `var/`, which is ignored by the repository's
effective `.gitignore`. Source code, test code, formal test plans, licenses,
and release evidence schemas are not ignored.
