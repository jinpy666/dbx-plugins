#!/usr/bin/env python3
"""Validate a merged set of unsigned DBX plugin candidate artifacts."""

from __future__ import annotations

import argparse
import hashlib
import io
import json
import struct
import sys
import zipfile
from pathlib import Path

EXPECTED_TARGETS = {
    "darwin-arm64",
    "darwin-x64",
    "windows-x64",
    "linux-x64",
    "linux-arm64",
}

# The official DBX web docker image runtime is debian:bookworm-slim (glibc
# 2.36). Linux sidecars must link a comfortably lower baseline (cargo-zigbuild
# lands at 2.30 today); a native ubuntu-24.04 build pins GLIBC_2.38/2.39 and
# the sidecar dies in the loader with exit status 1 before main() — surfacing
# as "Plugin initialization failed" on web/docker installs
# (dbx-plugin-ssh#8, dbx-plugin-ssh#58). Bump only as a deliberate decision.
LINUX_MAX_GLIBC = (2, 31)


def fail(message: str) -> None:
    print(f"ERROR: {message}", file=sys.stderr)
    raise SystemExit(1)


def elf_required_glibc(data: bytes) -> tuple[int, int] | None:
    """Return the highest GLIBC symbol version an ELF64 binary requires."""
    if len(data) < 64 or data[:4] != b"\x7fELF" or data[4] != 2:
        return None
    e_shoff, = struct.unpack_from("<Q", data, 0x28)
    e_shentsize, e_shnum, e_shstrndx = struct.unpack_from("<HHH", data, 0x3A)
    sections = []
    for i in range(e_shnum):
        off = e_shoff + i * e_shentsize
        fields = struct.unpack_from("<IIQQQQIIQQ", data, off)
        sections.append(dict(name=fields[0], offset=fields[4], size=fields[5], entsize=fields[9]))
    if e_shstrndx >= len(sections):
        return None
    shstr = sections[e_shstrndx]

    def section_name(index: int) -> str:
        start = shstr["offset"] + index
        end = data.index(b"\0", start)
        return data[start:end].decode("latin-1")

    for section in sections:
        section["sname"] = section_name(section["name"])
    names = {section["sname"] for section in sections}
    if ".gnu.version_r" not in names or ".dynstr" not in names:
        return None
    dynstr = next(s for s in sections if s["sname"] == ".dynstr")
    verneed = next(s for s in sections if s["sname"] == ".gnu.version_r")
    versions: dict[int, str] = {}
    offset = verneed["offset"]
    while True:
        _, count, _, aux, next_entry = struct.unpack_from("<HHIII", data, offset)
        cursor = offset + aux
        for _ in range(count):
            _, _, other, name_off, next_aux = struct.unpack_from("<IHHII", data, cursor)
            start = dynstr["offset"] + name_off
            versions[other] = data[start:data.index(b"\0", start)].decode("latin-1")
            if not next_aux:
                break
            cursor += next_aux
        if not next_entry:
            break
        offset += next_entry
    if ".gnu.version" not in names or ".dynsym" not in names:
        return None
    dynsym = next(s for s in sections if s["sname"] == ".dynsym")
    versym = next(s for s in sections if s["sname"] == ".gnu.version")
    best: tuple[int, int] | None = None
    for i in range(dynsym["size"] // dynsym["entsize"]):
        version_index, = struct.unpack_from("<H", data, versym["offset"] + i * 2)
        version = versions.get(version_index & 0x7FFF, "")
        if not version.startswith("GLIBC_"):
            continue
        try:
            number = tuple(int(part) for part in version.split("_")[1].split("."))
        except ValueError:
            continue
        number = (number + (0, 0))[:2]
        if best is None or number > best:
            best = number
    return best


def check_linux_glibc(package_path: Path, package_bytes: bytes) -> None:
    """Fail when a linux package's ELF sidecars require too-new glibc."""
    highest: tuple[int, int] | None = None
    with zipfile.ZipFile(io.BytesIO(package_bytes)) as archive:
        for entry in archive.namelist():
            if not entry.startswith("bin/linux-"):
                continue
            payload = archive.read(entry)
            required = elf_required_glibc(payload)
            if required is not None and (highest is None or required > highest):
                highest = required
    if highest is not None and highest > LINUX_MAX_GLIBC:
        fail(
            f"{package_path.name}: linux sidecar requires GLIBC_"
            f"{highest[0]}.{highest[1]}, above the supported baseline "
            f"{LINUX_MAX_GLIBC[0]}.{LINUX_MAX_GLIBC[1]} (official docker image "
            "runs debian:bookworm, glibc 2.36) — build Linux targets through "
            "cargo-zigbuild, not the runner's native toolchain"
        )
    if highest is not None:
        print(
            f"PASS {package_path.name}: linux sidecar glibc baseline "
            f"{highest[0]}.{highest[1]}"
        )


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
        if target.startswith("linux"):
            check_linux_glibc(package_path, package_bytes)

    for plugin_id, targets in sorted(seen.items()):
        missing = EXPECTED_TARGETS - targets
        if missing:
            fail(f"{plugin_id} is missing targets: {', '.join(sorted(missing))}")
        print(f"PASS {plugin_id}: {len(targets)} targets")
    print(f"PASS artifact set: {len(metadata_files)} metadata files, {len(package_files)} packages")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
