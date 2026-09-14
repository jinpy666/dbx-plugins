#!/usr/bin/env python3
"""Validate a merged set of unsigned DBX plugin candidate artifacts."""

from __future__ import annotations

import argparse
import hashlib
import json
import sys
from pathlib import Path

EXPECTED_TARGETS = {
    "darwin-arm64",
    "darwin-x64",
    "windows-x64",
    "linux-x64",
    "linux-arm64",
}


def fail(message: str) -> None:
    print(f"ERROR: {message}", file=sys.stderr)
    raise SystemExit(1)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("root", type=Path, help="Directory containing downloaded .dbxp and .artifact.json files")
    args = parser.parse_args()
    root = args.root.resolve()
    metadata_files = sorted(root.rglob("*.artifact.json"))
    package_files = {path.name: path for path in root.rglob("*.dbxp")}
    if not metadata_files or not package_files:
        fail("artifact directory contains no .dbxp and .artifact.json files")

    seen: dict[str, set[str]] = {}
    for metadata_path in metadata_files:
        try:
            artifact = json.loads(metadata_path.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError) as exc:
            fail(f"cannot read {metadata_path}: {exc}")
        target = artifact.get("target")
        url = artifact.get("url")
        package_name = Path(url).name if isinstance(url, str) else ""
        if not all(isinstance(value, str) and value for value in (target, package_name)):
            fail(f"{metadata_path} has incomplete identity metadata")
        if target not in EXPECTED_TARGETS:
            fail(f"{metadata_path} has unsupported target {target!r}")
        package_path = package_files.get(package_name)
        if package_path is None:
            fail(f"{metadata_path} references missing package {package_name}")
        suffix = f"-{target}.dbxp"
        if not package_name.endswith(suffix):
            fail(f"{metadata_path} target {target!r} does not match package name {package_name}")
        plugin_identity = package_name[: -len(suffix)]
        if not plugin_identity:
            fail(f"{metadata_path} has no plugin identity in package name")
        package_bytes = package_path.read_bytes()
        digest = hashlib.sha256(package_bytes).hexdigest()
        if artifact.get("sha256") != digest:
            fail(f"SHA-256 mismatch for {package_path.name}")
        if artifact.get("size") != len(package_bytes):
            fail(f"size mismatch for {package_path.name}")
        if artifact.get("signingKeyId") is not None:
            fail(f"unsigned candidate {metadata_path} unexpectedly declares signingKeyId")
        if target in seen.setdefault(plugin_identity, set()):
            fail(f"duplicate target {plugin_identity}/{target}")
        seen[plugin_identity].add(target)

    for plugin_id, targets in sorted(seen.items()):
        missing = EXPECTED_TARGETS - targets
        if missing:
            fail(f"{plugin_id} is missing targets: {', '.join(sorted(missing))}")
        print(f"PASS {plugin_id}: {len(targets)} targets")
    print(f"PASS artifact set: {len(metadata_files)} metadata files, {len(package_files)} packages")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
