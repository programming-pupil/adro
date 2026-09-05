## Document Inventory And Cleanup

Inventory date: 2026-09-05

The inventory was built from `rg --files -g '*.md'` and an inbound-reference
scan over source, tests, workflows, Make targets, and Markdown links. The
pre-change release baseline was `b3e0c119db39aa1d32dce0a574ef49b6c4f3ab52`
(`origin/main`). No deleted file was referenced by the repository after
excluding its own historical content.

### Allowlist

The following categories remain in the repository:

- product and governance documents: `README*`, `ABOUT.md`, `ROADMAP.md`,
  `RELEASE.md`, `SECURITY.md`, `SUPPORT.md`, `CONTRIBUTING.md`,
  `GOVERNANCE.md`, `MAINTAINERS.md`, `CHANGELOG.md`, and `THREAT_MODEL.md`;
- normative architecture and compatibility documents under
  `docs/architecture/`, plus product requirements and workflow documentation;
- release and test specifications: `ADRO-release-expert-test-plan.zh-CN.md`,
  `docs/testing/`, `e2e/README.md`, `qa.md`, `develop.md`, `scenario.md`, and
  `docs/operations/`;
- evidence, licenses, notices, and generated dependency records. Historical
  evidence is retained when it is the only record of a real run.

### Confirmed Deletions

| File | Reason | Reference scan | Recovery |
|---|---|---|---|
| `CTW-15-ga-audit.md` | Obsolete CTW-25 release-readiness snapshot with stale menu and test claims; superseded by the current release plan and architecture readiness docs. | No inbound repository references. | Revert the cleanup commit. |
| `ADRO-vs-AOS-Multica-architecture-review-2026-09-02.md` | Historical comparison against an obsolete ADRO SHA; not a normative architecture specification or release evidence record. | No inbound repository references. | Revert the cleanup commit. |
| `docs/architecture/ADRO-vs-AOS-ctw27-2026-09-02.md` | Historical handoff for a deleted working branch and stale refs; implementation details are now in the current architecture and release documents. | No inbound repository references. | Revert the cleanup commit. |

No source code, test code, formal test plan, license, architecture norm,
change record, or release evidence schema was deleted. The cleanup is limited
to these three unreferenced historical duplicates.
