#!/usr/bin/env python3
"""Choose canonical CI lanes from an independent capability-impact report."""
from __future__ import annotations

import argparse
import json
from pathlib import Path
import re

LANES = ("preflight", "backend", "frontend", "browser", "archive-sdk")
PROTECTED_PREFIXES = (
    ".github/", "scripts/", "docs/governance/", "deploy/", "migrations/",
    "api/", "cmd/aicrm/", "cmd/migrate-", "internal/platform/", "internal/identity/",
    "internal/config/", "internal/webshell/", "internal/outbound/",
    "internal/externaleffects/", "internal/configmigration/", "components/",
)
PROTECTED_FILES = {"Makefile", "AGENTS.md", "go.mod", "go.sum", "package.json", "package-lock.json", "orval.config.mjs"}


def full(reason: str) -> dict:
    return {"mode": "full", "lanes": list(LANES), "checks": [], "reason": reason}


def valid_check(check: object) -> bool:
    if not isinstance(check, dict):
        return False
    lane, path = check.get("lane"), check.get("path")
    if lane not in LANES or not isinstance(path, str) or not path or path.startswith("/"):
        return False
    if ".." in Path(path).parts:
        return False
    test = check.get("test")
    if test is not None and (not isinstance(test, str) or not test):
        return False
    if lane == "backend":
        return Path(path).suffix == ".go" and (test is None or bool(re.fullmatch(r"Test[A-Za-z0-9_]+", test)))
    if lane == "browser":
        return (path.startswith("cmd/aicrm/") and Path(path).suffix == ".go" and isinstance(test, str)
                and bool(re.fullmatch(r"Test[A-Za-z0-9_]*ChromiumJourney", test)))
    if lane == "frontend":
        return Path(path).suffix in {".mjs", ".js"}
    if lane == "archive-sdk":
        return Path(path).suffix == ".sh"
    return lane == "preflight"


def select(report: dict) -> dict:
    changed = report.get("changed_paths", [])
    risk = report.get("risk", "high")
    protected = any(path in PROTECTED_FILES or path.startswith(PROTECTED_PREFIXES) for path in changed)
    if not changed or protected or risk not in {"low", "medium"}:
        return full("protected-or-high-risk")
    if risk == "medium":
        checks = report.get("checks", [])
        if not checks or not isinstance(checks, list) or not all(valid_check(check) for check in checks):
            return full("unknown-or-unmapped-check")
        lanes = {check["lane"] for check in checks}
        lanes.add("preflight")
        return {"mode": "targeted", "lanes": [lane for lane in LANES if lane in lanes],
                "checks": checks, "reason": "affected-capabilities"}
    if not changed or any(not (path.startswith("docs/") and path.endswith(".md")) for path in changed):
        return full("unknown-or-unmapped-path")
    return {"mode": "targeted", "lanes": ["preflight"], "checks": [], "reason": "documentation-only"}


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("report", type=Path)
    args = parser.parse_args()
    result = select(json.loads(args.report.read_text()))
    print(json.dumps(result, separators=(",", ":")))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
