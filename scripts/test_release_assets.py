#!/usr/bin/env python3
"""Exercise release metadata checks against a real temporary Git repository."""

import json
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest


class ReleaseAssetsTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="adro-release-test-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        (self.root / "scripts").mkdir()
        shutil.copyfile(Path(__file__).with_name("release-assets.py"), self.root / "scripts/release-assets.py")
        (self.root / "VERSION").write_text("2.0.0\n")
        (self.root / "go.mod").write_text("module example.test/release\n\ngo 1.25.0\n")
        (self.root / "go.sum").write_text("")
        (self.root / "release").mkdir()
        (self.root / "release/dependencies.json").write_text('{"go": []}\n')
        self.command("git", "init", "-q")
        self.tool("generate")
        self.command("git", "add", ".")
        self.command("git", "-c", "user.name=Release Test", "-c", "user.email=release@example.test",
                     "-c", "commit.gpgsign=false", "commit", "-qm", "release fixture")
        self.command("git", "tag", "v2.0.0")

    def command(self, *args):
        return subprocess.run(args, cwd=self.root, capture_output=True, check=True)

    def tool(self, *args, succeeds=True):
        result = subprocess.run([sys.executable, "scripts/release-assets.py", *args],
                                cwd=self.root, capture_output=True, text=True)
        self.assertEqual(result.returncode == 0, succeeds, result.stdout + result.stderr)
        return result

    def test_generation_is_deterministic_and_stale_data_fails(self):
        before = (self.root / "SBOM").read_bytes()
        self.tool("generate")
        self.assertEqual((self.root / "SBOM").read_bytes(), before)
        self.tool("verify")
        (self.root / "THIRD_PARTY_NOTICES").write_text("changed\n")
        self.tool("verify", succeeds=False)

    def test_manifest_inventory_and_integrity(self):
        self.tool("manifest", "--output", "manifest.json")
        self.tool("verify-manifest", "manifest.json")
        file = self.root / "manifest.json"
        original = json.loads(file.read_text())
        for corrupt in ([], original["artifacts"] + [original["artifacts"][0]],
                        [dict(original["artifacts"][0], path="../outside")] + original["artifacts"][1:]):
            file.write_text(json.dumps(dict(original, artifacts=corrupt)))
            self.tool("verify-manifest", "manifest.json", succeeds=False)
        original["artifacts"][0]["sha256"] = "0" * 64
        file.write_text(json.dumps(original))
        self.tool("verify-manifest", "manifest.json", succeeds=False)

    def test_dirty_source_and_wrong_tag_refuse_manifest(self):
        (self.root / "go.sum").write_text("dirty\n")
        self.tool("manifest", "--output", "manifest.json", succeeds=False)
        (self.root / "go.sum").write_text("")
        self.command("git", "tag", "-d", "v2.0.0")
        self.command("git", "tag", "v1.0.0")
        self.tool("manifest", "--output", "manifest.json", succeeds=False)

    def test_dependency_mismatch_refuses_generation(self):
        (self.root / "release/dependencies.json").write_text(json.dumps({"go": [{
            "name": "example.test/missing", "version": "v1.0.0", "license": "MIT",
            "source": "https://example.test", "supplier": "NOASSERTION", "scope": "runtime",
        }]}))
        self.tool("generate", succeeds=False)


if __name__ == "__main__":
    unittest.main()
