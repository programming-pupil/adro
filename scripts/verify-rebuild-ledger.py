#!/usr/bin/env python3
"""Verify that the authoritative 451-item rebuild ledger was not narrowed."""

from pathlib import Path
import hashlib
import re
import sys

EXPECTED_COUNT = 451
EXPECTED_SOURCE_DIGEST = "c1065cf81b5abe01266024367a74b8c58df1571e1f24e761ba26d7243d9bded6"
ITEM = re.compile(r"^- \[([ xX])\] `([^`]+)` \[source:(\d+)\] \[state:(evidenced|partial|unverified)\] (.*)$")


def main() -> int:
    path = Path(__file__).resolve().parents[1] / "docs/rebuild/todo-evidence.md"
    records = []
    for line in path.read_text(encoding="utf-8").splitlines():
        match = ITEM.match(line)
        if match:
            _, key, source_line, _, body = match.groups()
            records.append((int(source_line), key, body))
    if len(records) != EXPECTED_COUNT:
        print(f"rebuild ledger item count {len(records)} != {EXPECTED_COUNT}", file=sys.stderr)
        return 1
    keys = [record[1] for record in records]
    if len(keys) != len(set(keys)):
        print("rebuild ledger contains duplicate keys", file=sys.stderr)
        return 1
    payload = "".join(f"{source_line}\0{key}\0{body}\n" for source_line, key, body in records).encode()
    actual = hashlib.sha256(payload).hexdigest()
    if actual != EXPECTED_SOURCE_DIGEST:
        print(f"rebuild ledger source digest {actual} != {EXPECTED_SOURCE_DIGEST}", file=sys.stderr)
        return 1
    print(f"rebuild ledger verified: {len(records)} items, source digest {actual}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
