#!/usr/bin/env python3
"""Exercise the lexical guard's fail-closed and redacted diagnostic contract."""

from __future__ import annotations

import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile

SCRIPT = Path(__file__).with_name("verify-public-identity.py")


def run(*args: str, cwd: Path, env: dict[str, str] | None = None) -> subprocess.CompletedProcess[str]:
    return subprocess.run(args, cwd=cwd, env=env, text=True, capture_output=True, check=False)


def main() -> int:
    with tempfile.TemporaryDirectory() as temporary:
        root = Path(temporary)
        initialized = run("git", "init", "-q", cwd=root)
        if initialized.returncode:
            raise RuntimeError(initialized.stderr)
        builtin = "".join(("multi", "ca"))
        private = "PRIVATE-CANARY-DO-NOT-PRINT"
        (root / "source.txt").write_text(f"{builtin}\n{private}\n", encoding="utf-8")
        if run("git", "add", "source.txt", cwd=root).returncode:
            raise RuntimeError("could not stage test fixture")
        denylist = root / "denylist.json"
        denylist.write_text(json.dumps([private]), encoding="utf-8")
        environment = dict(os.environ, ADRO_PRIVATE_DENYLIST_PATH=str(denylist))
        result = run(sys.executable, str(SCRIPT), "--root", str(root), cwd=root, env=environment)
        if result.returncode != 1 or "LEX-IDENTITY-001 source.txt:1" not in result.stderr or "LEX-PRIVATE-001 source.txt:2" not in result.stderr:
            raise AssertionError(f"unexpected scan result: {result.returncode} {result.stderr!r}")
        if builtin in result.stderr.lower() or private in result.stderr:
            raise AssertionError("scanner revealed a forbidden value")

        # A clean working copy cannot hide a forbidden value already staged for
        # publication. Conversely the scanner must not treat unstaged edits as
        # content that the next commit would contain.
        (root / "source.txt").write_text("clean worktree\n", encoding="utf-8")
        staged = run(sys.executable, str(SCRIPT), "--root", str(root), cwd=root, env=environment)
        if staged.returncode != 1 or "LEX-PRIVATE-001 source.txt:2" not in staged.stderr:
            raise AssertionError(f"staged content was not checked: {staged.returncode} {staged.stderr!r}")
        if run("git", "add", "source.txt", cwd=root).returncode:
            raise RuntimeError("could not stage clean fixture")
        (root / "source.txt").write_text(private + "\n", encoding="utf-8")
        unstaged = run(sys.executable, str(SCRIPT), "--root", str(root), cwd=root, env=environment)
        if unstaged.returncode != 0:
            raise AssertionError(f"scanner read unstaged content: {unstaged.returncode} {unstaged.stderr!r}")
        environment["ADRO_PRIVATE_DENYLIST_PATH"] = str(root / "missing-denylist.json")
        unavailable = run(sys.executable, str(SCRIPT), "--root", str(root), cwd=root, env=environment)
        if unavailable.returncode != 1 or "public identity scan unavailable: ValueError" not in unavailable.stderr or private in unavailable.stderr:
            raise AssertionError(f"missing policy did not fail closed: {unavailable.returncode} {unavailable.stderr!r}")
    print("public identity negative tests passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
