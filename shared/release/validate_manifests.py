#!/usr/bin/env python3
"""Validate DBX plugin release identity without third-party dependencies."""

from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path


SEMVER = re.compile(
    r"^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)"
    r"(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$"
)
IDENTIFIER = re.compile(r"^[a-z0-9][a-z0-9._-]*$")


def fail(message: str) -> None:
    raise ValueError(message)


def validate(root: Path, plugin: str, tag: str | None) -> None:
    plugin_root = root / plugin
    manifest_path = plugin_root / "manifest.json"
    if not manifest_path.is_file():
        fail(f"{plugin}: missing {manifest_path}")
    try:
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    except json.JSONDecodeError as exc:
        fail(f"{plugin}: invalid manifest JSON: {exc}")

    required = ("manifest_version", "id", "name", "version", "publisher", "engines")
    missing = [key for key in required if not manifest.get(key)]
    if missing:
        fail(f"{plugin}: missing required fields: {', '.join(missing)}")
    if manifest["manifest_version"] != 1:
        fail(f"{plugin}: manifest_version must be 1")
    if not IDENTIFIER.fullmatch(manifest["id"]):
        fail(f"{plugin}: invalid plugin id {manifest['id']!r}")
    if not SEMVER.fullmatch(manifest["version"]):
        fail(f"{plugin}: invalid semver {manifest['version']!r}")
    for field in ("source", "homepage"):
        value = manifest.get(field)
        if not isinstance(value, str) or not value.startswith(("https://", "http://")):
            fail(f"{plugin}: {field} must be an HTTP(S) URL for marketplace review")
    if tag and tag != f"{plugin}-v{manifest['version']}":
        fail(f"{plugin}: release tag {tag!r} does not match {plugin}-v{manifest['version']}")

    backend = manifest.get("entrypoints", {}).get("backend", {})
    executable = backend.get("executable", "")
    if not executable.startswith("bin/"):
        fail(f"{plugin}: backend executable must be package-relative under bin/")
    for relative in ("dbx-plugin.toml", "frontend", "scripts/test.sh"):
        if not (plugin_root / relative).exists():
            fail(f"{plugin}: missing {relative}")

    print(f"PASS {plugin}: {manifest['id']} {manifest['version']} publisher={manifest['publisher']}")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("plugin", choices=("ssh", "ldap", "files", "kafka"))
    parser.add_argument("--tag", help="GitHub Release tag, e.g. ssh-v0.4.71")
    args = parser.parse_args()
    try:
        validate(Path(__file__).resolve().parents[2], args.plugin, args.tag)
    except ValueError as exc:
        print(f"FAIL: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
