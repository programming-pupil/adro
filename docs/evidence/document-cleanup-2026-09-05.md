## Document Inventory And Cleanup

Inventory dates: 2026-09-05, 2026-09-10

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
  `docs/testing/`, `e2e/README.md`, and `docs/operations/`;
- evidence, licenses, notices, and generated dependency records. Historical
  evidence is retained when it is the only record of a real run.

### Confirmed Deletions

| File | Reason | Reference scan | Recovery |
|---|---|---|---|
| `CTW-15-ga-audit.md` | Obsolete CTW-25 release-readiness snapshot with stale menu and test claims; superseded by the current release plan and architecture readiness docs. | No inbound repository references. | Revert the cleanup commit. |
| Historical comparison document | Historical comparison against an obsolete ADRO SHA; not a normative architecture specification or release evidence record. | No inbound repository references. | Revert the cleanup commit. |
| Historical architecture handoff | Historical handoff for a deleted working branch and stale refs; implementation details are now in the current architecture and release documents. | No inbound repository references. | Revert the cleanup commit. |
| `design.md` | Unreferenced draft that duplicates the normative technical design. | No inbound repository references. | Revert the 2026-09-10 cleanup commit. |
| `develop.md` | Internal execution prompt, not contributor documentation. | Referenced only by this inventory before deletion. | Revert the 2026-09-10 cleanup commit. |
| `qa.md` | Internal QA prompt, superseded by the release test matrix and expert test plan. | Referenced only by this inventory before deletion. | Revert the 2026-09-10 cleanup commit. |
| `scenario.md` | Internal real-run prompt, superseded by executable E2E scripts and stable evidence. | Referenced only by this inventory before deletion. | Revert the 2026-09-10 cleanup commit. |

No source code, test code, formal test plan, license, architecture norm,
change record, or release evidence schema was deleted. Root-level prompt files
removed in the second pass are explicitly ignored to prevent generated working
notes from re-entering the published tree.
