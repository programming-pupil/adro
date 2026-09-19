#!/usr/bin/env python3
"""Verify bidirectional threat-model to Go test traceability."""

from __future__ import annotations

import json
from pathlib import Path
import re
import sys

ROOT = Path(__file__).resolve().parents[1]
MAP_PATH = ROOT / "docs/rebuild/threat-test-map.json"
THREAT_ID = re.compile(r"^TM-[A-Z0-9]+(?:-[A-Z0-9]+)+$")
ANNOTATED_TEST = re.compile(
    r"// Threat IDs?:\s*([^\n]+)\nfunc\s+(Test[A-Za-z0-9_]+)\s*\(", re.MULTILINE
)
TEST_FUNCTION = re.compile(r"\bfunc\s+(Test[A-Za-z0-9_]+)\s*\(")


def fail(message: str) -> int:
    print(f"threat-test map: {message}", file=sys.stderr)
    return 1


def main() -> int:
    try:
        document = json.loads(MAP_PATH.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        return fail(f"cannot read {MAP_PATH.relative_to(ROOT)}: {error}")
    if document.get("schema_version") != 1 or not isinstance(document.get("threats"), list):
        return fail("schema_version=1 and a threats array are required")

    mapped: dict[tuple[str, str], set[str]] = {}
    threat_ids: set[str] = set()
    mapping_count = 0
    for threat in document["threats"]:
        threat_id = threat.get("id")
        tests = threat.get("tests")
        if not isinstance(threat_id, str) or not THREAT_ID.fullmatch(threat_id):
            return fail(f"invalid threat id {threat_id!r}")
        if threat_id in threat_ids:
            return fail(f"duplicate threat id {threat_id}")
        threat_ids.add(threat_id)
        if not str(threat.get("description", "")).strip() or not str(threat.get("mitigation", "")).strip():
            return fail(f"{threat_id} needs description and mitigation")
        if not isinstance(tests, list) or not tests:
            return fail(f"{threat_id} has no tests")
        if len(tests) != len(set(tests)):
            return fail(f"{threat_id} contains duplicate test references")
        for reference in tests:
            if not isinstance(reference, str) or reference.count("#") != 1:
                return fail(f"{threat_id} has invalid test reference {reference!r}")
            relative, test_name = reference.split("#")
            path = (ROOT / relative).resolve()
            try:
                path.relative_to(ROOT)
            except ValueError:
                return fail(f"{threat_id} escapes repository root: {relative}")
            if path.suffix != ".go" or not path.name.endswith("_test.go") or not path.is_file():
                return fail(f"{threat_id} references missing Go test file {relative}")
            functions = set(TEST_FUNCTION.findall(path.read_text(encoding="utf-8")))
            if test_name not in functions:
                return fail(f"{threat_id} references missing test {reference}")
            mapped.setdefault((relative, test_name), set()).add(threat_id)
            mapping_count += 1

    annotated: dict[tuple[str, str], set[str]] = {}
    for path in ROOT.rglob("*_test.go"):
        if any(part.startswith(".") or part in {"node_modules", "attachments", "var"} for part in path.parts):
            continue
        relative = path.relative_to(ROOT).as_posix()
        text = path.read_text(encoding="utf-8")
        for raw_ids, test_name in ANNOTATED_TEST.findall(text):
            ids = {value.strip() for value in raw_ids.split(",") if value.strip()}
            if not ids:
                return fail(f"empty threat annotation on {relative}#{test_name}")
            unknown = ids - threat_ids
            if unknown:
                return fail(f"{relative}#{test_name} references unknown threats {sorted(unknown)}")
            annotated[(relative, test_name)] = ids

    for target, ids in mapped.items():
        missing = ids - annotated.get(target, set())
        if missing:
            return fail(f"{target[0]}#{target[1]} lacks annotations for {sorted(missing)}")
    for target, ids in annotated.items():
        missing = ids - mapped.get(target, set())
        if missing:
            return fail(f"{target[0]}#{target[1]} annotations are absent from the map: {sorted(missing)}")

    print(
        f"threat-test map verified: {len(threat_ids)} threats, "
        f"{len(mapped)} tests, {mapping_count} bidirectional links"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
