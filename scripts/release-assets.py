#!/usr/bin/env python3
"""Verify Go dependency notices and sign traceable release manifests."""

import argparse
import hashlib
import json
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile


ROOT = Path(__file__).resolve().parent.parent
NAMESPACE = "adro-release"
MANIFEST_PATHS = ("SBOM", "THIRD_PARTY_NOTICES", "go.mod", "go.sum", "VERSION")


def run(program, *args, data=None):
    return subprocess.run(
        [program, *args], cwd=ROOT, input=data, capture_output=True, check=True
    ).stdout.decode().strip()


def digest(data):
    return hashlib.sha256(data).hexdigest()


def read_json(path):
    value = json.loads(Path(path).read_text(encoding="utf-8"))
    if not isinstance(value, dict):
        raise ValueError(f"{path} must contain a JSON object")
    return value


def write_json(path, value):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")


def version():
    value = (ROOT / "VERSION").read_text(encoding="utf-8").strip()
    if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?", value):
        raise ValueError("VERSION must contain a release version")
    return value


def dependency_key(item):
    return f"{item['ecosystem']}:{item['name']}@{item['version']}"


def dependencies():
    registry = read_json(ROOT / "release/dependencies.json")
    if set(registry) != {"go"}:
        raise ValueError("dependency registry must describe Go modules only")
    declared = sorted(
        [dict(item, ecosystem="go") for item in registry["go"]], key=dependency_key
    )
    actual = run("go", "list", "-m", "-f", "{{if not .Main}}{{.Path}}|{{.Version}}{{end}}", "all")
    actual_keys = sorted("go:" + line.replace("|", "@") for line in actual.splitlines() if line)
    if actual_keys != [dependency_key(item) for item in declared]:
        raise ValueError("dependency registry does not match go.mod dependency graph")
    for item in declared:
        if any(not item.get(key) for key in ("license", "source", "supplier", "scope")):
            raise ValueError(f"{dependency_key(item)} has incomplete license metadata")
    return declared


def spdx_id(item):
    return re.sub(r"[^A-Za-z0-9.-]", "-", "SPDXRef-" + dependency_key(item))


def license_target(item):
    safe = re.sub(r"[^A-Za-z0-9._@-]", "_", f"go-{item['name']}@{item['version']}")
    return "THIRD_PARTY_LICENSES/" + safe + ".txt"


def license_text(item):
    metadata = json.loads(run("go", "mod", "download", "-json", f"{item['name']}@{item['version']}"))
    directory = Path(metadata.get("Dir", ""))
    if not metadata.get("Dir") or not directory.is_dir():
        raise ValueError(f"module directory unavailable for {dependency_key(item)}")
    for name in ("LICENSE", "LICENSE.md", "LICENSE.txt", "License", "LICENCE", "COPYING", "NOTICE"):
        source = directory / name
        if source.is_file():
            text = source.read_text(encoding="utf-8")
            return re.sub(r"[ \t]+$", "", text, flags=re.MULTILINE).rstrip("\n") + "\n"
    raise ValueError(f"license text unavailable for {dependency_key(item)}")


def generated_files():
    items = dependencies()
    release_version = version()
    manifest_hash = digest("\n".join(
        digest((ROOT / name).read_bytes())
        for name in ("go.mod", "go.sum", "VERSION", "release/dependencies.json")
    ).encode())
    root_id = "SPDXRef-Package-ADRO"
    packages = [{
        "SPDXID": root_id, "name": "adro", "versionInfo": release_version,
        "downloadLocation": "https://github.com/programming-pupil/adro",
        "filesAnalyzed": False, "licenseConcluded": "Apache-2.0",
        "licenseDeclared": "Apache-2.0", "copyrightText": "Copyright 2026 ADRO contributors",
        "supplier": "Organization: ADRO contributors",
    }]
    for item in items:
        packages.append({
            "SPDXID": spdx_id(item), "name": item["name"], "versionInfo": item["version"],
            "downloadLocation": item["source"], "filesAnalyzed": False,
            "licenseConcluded": item["license"], "licenseDeclared": item["license"],
            "copyrightText": "NOASSERTION",
            "supplier": "NOASSERTION" if item["supplier"] == "NOASSERTION" else "Organization: " + item["supplier"],
            "externalRefs": [{"referenceCategory": "PACKAGE-MANAGER", "referenceType": "purl",
                              "referenceLocator": f"pkg:golang/{item['name']}@{item['version']}"}],
        })
    sbom = {
        "spdxVersion": "SPDX-2.3", "dataLicense": "CC0-1.0", "SPDXID": "SPDXRef-DOCUMENT",
        "name": "adro-" + release_version,
        "documentNamespace": f"https://adro.dev/spdx/adro-{release_version}-{manifest_hash}",
        "creationInfo": {"created": "1970-01-01T00:00:00Z", "creators": ["Tool: scripts/release-assets.py"]},
        "documentDescribes": [root_id], "packages": packages,
        "relationships": [{
            "spdxElementId": root_id if item["scope"] == "runtime" else spdx_id(item),
            "relationshipType": "DEPENDS_ON" if item["scope"] == "runtime" else "DEV_DEPENDENCY_OF",
            "relatedSpdxElement": spdx_id(item) if item["scope"] == "runtime" else root_id,
        } for item in items],
    }
    files = {"SBOM": json.dumps(sbom, indent=2, ensure_ascii=False) + "\n"}
    notices = ["ADRO Third-Party Notices",
               f"Generated from Go manifests and release/dependencies.json for ADRO {release_version}.",
               "The release verifier fails when a manifest dependency is absent from this list.", ""]
    for item in items:
        text = license_text(item)
        target = license_target(item)
        files[target] = text
        notices.extend([
            f"{item['name']} {item['version']}", "  ecosystem: go", f"  scope: {item['scope']}",
            f"  license: {item['license']}", f"  source: {item['source']}",
            f"  license-file: {target}", f"  license-sha256: {digest(text.encode())}", "",
        ])
    files["THIRD_PARTY_NOTICES"] = "\n".join(notices).rstrip() + "\n"
    return files


def generate(verify=False):
    for name, content in generated_files().items():
        path = ROOT / name
        if verify:
            if not path.is_file() or path.read_bytes() != content.encode():
                raise ValueError(f"{name} is missing or stale; run scripts/release-assets.py generate")
        else:
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(content, encoding="utf-8")


def artifact_record(name):
    path = (ROOT / name).resolve()
    # A manifest only names repository artifacts, never arbitrary host files.
    relative = path.relative_to(ROOT)
    data = path.read_bytes()
    return {"path": str(relative), "sha256": digest(data), "size_bytes": len(data)}


def release_identity():
    release_version = version()
    tag = run("git", "describe", "--tags", "--exact-match", "HEAD")
    if tag != "v" + release_version:
        raise ValueError("release tag does not match VERSION")
    return {"version": release_version, "tag": tag,
            "commit": run("git", "rev-parse", "--verify", "HEAD"),
            "source_tree": run("git", "rev-parse", "HEAD^{tree}")}


def create_manifest(output):
    identity = release_identity()
    if run("git", "status", "--porcelain", "--untracked-files=no"):
        raise ValueError("tracked worktree changes prevent a traceable release manifest")
    generate(verify=True)
    write_json(output, dict(identity, schema_version=1, product="ADRO",
                           committed_at=run("git", "show", "-s", "--format=%cI", "HEAD"),
                           builder={"go": run("go", "version"), "python": sys.version.split()[0]},
                           artifacts=[artifact_record(name) for name in MANIFEST_PATHS]))


def verify_manifest(path):
    manifest = read_json(path)
    for key, expected in release_identity().items():
        if manifest.get(key) != expected:
            raise ValueError(f"manifest {key} does not match current release")
    artifacts = manifest.get("artifacts", [])
    if sorted(item["path"] for item in artifacts) != sorted(MANIFEST_PATHS):
        raise ValueError("manifest artifact inventory does not match release inputs")
    for item in artifacts:
        if item != artifact_record(item["path"]):
            raise ValueError(f"manifest artifact mismatch: {item['path']}")


def verify_signature(path, signature, signers, identity):
    read_json(path)
    run("ssh-keygen", "-Y", "verify", "-f", str(signers), "-I", identity,
        "-n", NAMESPACE, "-s", str(signature), data=Path(path).read_bytes())


def sign_manifest(path, key, signature, signers, identity):
    read_json(path)
    # Sign a private temporary copy to avoid interactive overwrite prompts.
    with tempfile.TemporaryDirectory(prefix="adro-sign-") as directory:
        copy = Path(directory) / "manifest"
        shutil.copyfile(path, copy)
        run("ssh-keygen", "-Y", "sign", "-f", str(key), "-n", NAMESPACE, str(copy))
        Path(signature).parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(str(copy) + ".sig", signature)
    public = run("ssh-keygen", "-y", "-f", str(key))
    if not public.startswith(("ssh-ed25519 ", "ssh-rsa ", "ecdsa-")):
        raise ValueError("signing key did not produce a supported SSH public key")
    Path(signers).parent.mkdir(parents=True, exist_ok=True)
    Path(signers).write_text(identity + " " + public + "\n", encoding="utf-8")
    verify_signature(path, signature, signers, identity)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    commands.add_parser("generate")
    commands.add_parser("verify")
    commands.add_parser("manifest").add_argument("--output", required=True)
    commands.add_parser("verify-manifest").add_argument("path")
    for name in ("sign-manifest", "verify-signature"):
        command = commands.add_parser(name)
        command.add_argument("path")
        command.add_argument("--signature", required=True)
        command.add_argument("--allowed-signers", required=True)
        command.add_argument("--identity", default=NAMESPACE)
        if name == "sign-manifest":
            command.add_argument("--key", required=True)
    args = parser.parse_args()
    if args.command in ("generate", "verify"):
        generate(verify=args.command == "verify")
    elif args.command == "manifest":
        create_manifest(args.output)
    elif args.command == "verify-manifest":
        verify_manifest(args.path)
    elif args.command == "sign-manifest":
        sign_manifest(args.path, args.key, args.signature, args.allowed_signers, args.identity)
    elif args.command == "verify-signature":
        verify_signature(args.path, args.signature, args.allowed_signers, args.identity)
    print("release assets: " + args.command + " passed")


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, KeyError, TypeError, subprocess.CalledProcessError) as error:
        print(f"release assets: {error}", file=sys.stderr)
        sys.exit(1)
