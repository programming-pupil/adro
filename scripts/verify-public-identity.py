#!/usr/bin/env python3
"""Check tracked public files without printing forbidden names or source text.

An optional organization-private JSON array of literal names may be supplied in
ADRO_PRIVATE_DENYLIST_PATH. The built-in checks protect the repository even
when no private policy file is available to a public CI runner.
"""

from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
BUILTIN_NAME = "".join(("multi", "ca"))
BUILTIN_ACRONYM = "".join(("a", "os"))
ACRONYM_PATTERN = re.compile(r"(?<![a-z0-9_])" + BUILTIN_ACRONYM + r"(?![a-z0-9_])", re.IGNORECASE)


def private_rules() -> list[tuple[str, str]]:
    location = os.environ.get("ADRO_PRIVATE_DENYLIST_PATH", "").strip()
    if not location:
        return []
    try:
        values = json.loads(Path(location).read_text(encoding="utf-8"))
    except (OSError, ValueError) as error:
        raise ValueError("private lexical policy could not be loaded") from error
    if not isinstance(values, list) or not values or any(
        not isinstance(value, str) or not value.strip() or len(value) > 256 for value in values
    ):
        raise ValueError("private lexical policy must contain nonempty literal strings")
    return [(f"LEX-PRIVATE-{index:03d}", value.casefold()) for index, value in enumerate(values, 1)]


def scan(root: Path, private: list[tuple[str, str]]) -> list[str]:
    paths = subprocess.check_output(["git", "ls-files", "-z"], cwd=root, stderr=subprocess.DEVNULL).split(b"\0")
    violations: list[str] = []
    for raw_path in paths:
        if not raw_path:
            continue
        relative = os.fsdecode(raw_path)
        if relative.startswith("THIRD_PARTY_LICENSES/"):
            continue
        try:
            # The index is what `git commit` will publish. Reading the working
            # tree alone can silently pass a staged forbidden value when the
            # file was edited again after staging.
            content = subprocess.check_output(["git", "show", f":{relative}"], cwd=root, stderr=subprocess.DEVNULL)
        except subprocess.CalledProcessError:
            violations.append(f"LEX-READ {relative}:0")
            continue
        if b"\0" in content:
            continue
        for number, raw_line in enumerate(content.splitlines(), 1):
            line = raw_line.decode("utf-8", "replace").casefold()
            rules = []
            if BUILTIN_NAME in line:
                rules.append("LEX-IDENTITY-001")
            if ACRONYM_PATTERN.search(line):
                rules.append("LEX-IDENTITY-002")
            rules.extend(rule_id for rule_id, needle in private if needle in line)
            violations.extend(f"{rule_id} {relative}:{number}" for rule_id in rules)
    return violations


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", type=Path, default=ROOT)
    args = parser.parse_args()
    try:
        violations = scan(args.root.resolve(), private_rules())
    except (ValueError, subprocess.CalledProcessError) as error:
        print(f"public identity scan unavailable: {type(error).__name__}", file=sys.stderr)
        return 1
    if violations:
        for violation in violations:
            print(violation, file=sys.stderr)
        return 1
    print("public identity scan passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
