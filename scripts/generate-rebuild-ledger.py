#!/usr/bin/env python3
"""Regenerate the 451-item rebuild evidence ledger from its authority attachment."""

from __future__ import annotations

import argparse
import hashlib
from pathlib import Path
import re

EXPECTED_COUNT = 451
EXPECTED_SOURCE_DIGEST = "c1065cf81b5abe01266024367a74b8c58df1571e1f24e761ba26d7243d9bded6"
TODO_ID = re.compile(r"(?:(?:P[01]|UI)-[A-Z0-9-]+|SOURCE-L[0-9]{4})")


def progress_rows(path: Path) -> dict[str, tuple[str, str]]:
    rows: dict[str, tuple[str, str]] = {}
    for line in path.read_text(encoding="utf-8").splitlines():
        match = re.match(r"^\|\s*([^|]+?)\s*\|\s*([^|]+?)\s*\|\s*(.*?)\s*\|$", line)
        if not match:
            continue
        key, state, evidence = (part.strip() for part in match.groups())
        if TODO_ID.fullmatch(key):
            rows[key] = (state, evidence)
    return rows


def source_items(path: Path, evidence_rows: dict[str, tuple[str, str]]) -> list[dict[str, object]]:
    items: list[dict[str, object]] = []
    section = "Unsectioned"
    for line_number, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        heading = re.match(r"^(#{2,4})\s+(.+?)\s*$", line)
        if heading:
            section = heading.group(2)
        checkbox = re.match(r"^[-*]\s+\[[ xX]\]\s+(.+?)\s*$", line)
        if not checkbox:
            continue
        text = checkbox.group(1)
        identifier = re.match(r"^\*\*([^*]+)\*\*\s*(.*)$", text)
        if identifier:
            key, body = identifier.group(1).strip(), identifier.group(2).strip()
        else:
            key, body = f"SOURCE-L{line_number:04d}", text.strip()
        raw_state, evidence = evidence_rows.get(key, ("unverified", ""))
        if raw_state == "unverified":
            tracking = "unverified"
        elif raw_state.startswith("partial"):
            tracking = "partial"
        else:
            tracking = "evidenced"
        items.append(
            {
                "line": line_number,
                "section": section,
                "key": key,
                "body": body,
                "tracking": tracking,
                "raw_state": raw_state,
                "evidence": evidence,
            }
        )
    return items


def identity(items: list[dict[str, object]]) -> str:
    payload = "".join(
        f"{item['line']}\0{item['key']}\0{item['body']}\n" for item in items
    ).encode()
    return hashlib.sha256(payload).hexdigest()


def render(items: list[dict[str, object]], digest: str) -> str:
    counts = {
        state: sum(item["tracking"] == state for item in items)
        for state in ("evidenced", "partial", "unverified")
    }
    lines = [
        "# Rebuild Todo Evidence Ledger",
        "",
        "Updated: 2026-09-19.",
        "",
        "This ledger mirrors every top-level checkbox in the authoritative rebuild TodoList attachment. "
        "All 451 entries remain open until their implementation, conformance, fault evidence, documentation, "
        "main-branch merge, and final acceptance conditions are satisfied. `evidenced` means repository evidence "
        "has been recorded; it does not mean the final issue acceptance gate is closed.",
        "",
        f"- Authoritative item count: **{len(items)}**",
        f"- Source identity digest: `{digest}`",
        f"- Evidence tracking: **{counts['evidenced']} evidenced**, **{counts['partial']} partial**, **{counts['unverified']} unverified**",
        "- Checkbox rule: only final evidence review may change `[ ]` to `[x]`; deleting, merging, or renaming an item fails `scripts/verify-rebuild-ledger.py`.",
        "",
    ]
    current_section = None
    for item in items:
        if item["section"] != current_section:
            current_section = item["section"]
            lines.extend([f"## {current_section}", ""])
        lines.append(
            f"- [ ] `{item['key']}` [source:{item['line']}] [state:{item['tracking']}] {item['body']}"
        )
        if item["raw_state"] != "unverified":
            lines.append(
                f"  - Recorded evidence state: `{item['raw_state']}`. {item['evidence']}"
            )
        lines.append("")
    return "\n".join(lines).rstrip() + "\n"


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--source",
        type=Path,
        default=Path("attachments/adro-top-runtime-rebuild-todolist.zh-CN.md"),
    )
    parser.add_argument("--progress", type=Path, default=Path("docs/rebuild/progress.md"))
    parser.add_argument("--output", type=Path, default=Path("docs/rebuild/todo-evidence.md"))
    args = parser.parse_args()

    items = source_items(args.source, progress_rows(args.progress))
    if len(items) != EXPECTED_COUNT:
        raise SystemExit(f"expected {EXPECTED_COUNT} checklist items, found {len(items)}")
    keys = [str(item["key"]) for item in items]
    if len(keys) != len(set(keys)):
        raise SystemExit("authoritative checklist contains duplicate generated keys")
    digest = identity(items)
    if digest != EXPECTED_SOURCE_DIGEST:
        raise SystemExit(
            f"authority digest {digest} does not match pinned {EXPECTED_SOURCE_DIGEST}"
        )
    args.output.write_text(render(items, digest), encoding="utf-8")
    print(f"generated {args.output}: {len(items)} items, source digest {digest}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
