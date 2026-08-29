ADRO release acceptance is defined by
`docs/operations/release-runbook.md`. The checked-in version is 0.1.0 and is a
single-node reference profile, not GA.

Every release must retain a verified SPDX SBOM/NOTICE/license set, a clean
commit/tag release manifest, full local verification, the browser matrix, and a
real Multica conformance JSON report. Mock, configuration-only, emulated device,
or interface-only evidence cannot satisfy an external production gate.
