#!/usr/bin/env python3
"""Reject common local artifacts and high-confidence secrets in tracked files."""

from __future__ import annotations

import re
import subprocess
import sys
from pathlib import Path


FORBIDDEN_PATHS = (
    re.compile(r"(^|/)(\.env(?:\..*)?|\.secrets?|credentials?)(\..*)?$", re.I),
    re.compile(r"(^|/)(state|session|storage)\.json$", re.I),
    re.compile(r"\.(pem|key|p12|pfx|jks|keystore|sqlite|sqlite3|db|bak|orig|rej)$", re.I),
    re.compile(r"(^|/)(node_modules|target|dist|screenshots|\.goal-state|\.release-state)(/|$)"),
    re.compile(r"(^|/)(app-data|logs)(/|$)"),
)

SECRET_PATTERNS = (
    # Require a plausible payload as well as the PEM header so synthetic
    # parser fixtures such as "BEGIN ... PRIVATE KEY / zz" remain allowed.
    re.compile(r"(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----\s*[A-Za-z0-9+/=\r\n]{64,}"),
    re.compile(r"\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9_]{20,}\b"),
    re.compile(r"\bgithub_pat_[A-Za-z0-9_]{20,}\b"),
    re.compile(r"\bAKIA[0-9A-Z]{16}\b"),
    re.compile(r"\bASIA[0-9A-Z]{16}\b"),
    re.compile(r"(?i)\baws_secret_access_key\s*[:=]\s*[\"'][^\"']{16,}[\"']"),
)


def tracked_files(root: Path) -> list[str]:
    result = subprocess.run(
        ["git", "ls-files", "-z"], cwd=root, check=True, stdout=subprocess.PIPE
    )
    return [item for item in result.stdout.decode().split("\0") if item]


def main() -> int:
    root = Path(__file__).resolve().parents[2]
    files = tracked_files(root)
    problems: list[str] = []
    for relative in files:
        path = Path(relative)
        if any(pattern.search(relative) for pattern in FORBIDDEN_PATHS):
            problems.append(f"forbidden tracked path: {relative}")
            continue
        if not (root / path).is_file():
            # Git records a submodule as a gitlink, which is represented by a
            # directory in a checkout and must not be read as a regular file.
            continue
        try:
            text = (root / path).read_text(encoding="utf-8")
        except (UnicodeDecodeError, FileNotFoundError):
            continue
        for pattern in SECRET_PATTERNS:
            if pattern.search(text):
                problems.append(f"high-confidence secret pattern in: {relative}")
                break
    if problems:
        print("Repository hygiene check failed:", file=sys.stderr)
        print("\n".join(f"- {item}" for item in problems), file=sys.stderr)
        return 1
    print(f"PASS repository hygiene: {len(files)} tracked files checked")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
