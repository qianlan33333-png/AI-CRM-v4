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

# These files define the domestic publisher and its CI gate. They are high
# consequence tools, but their contract suite is independent of application
# behavior and should not force unrelated Go, frontend, and browser suites.
# Keep this allow-list narrow: migrations, build inputs, service units, action
# setup, and unknown deployment paths must retain the conservative full lanes.
RELEASE_TOOL_FILES = {
    ".github/workflows/ci.yml",
    "AGENTS.md",
    "README.md",
    "deploy/README.md",
    "docs/operations/domestic-release.md",
    "docs/operations/archive/2026-09-23-5538-0206-staging-retry.md",
    "docs/operations/archive/2026-09-24-staging-migration-backup-retry-plan.superseded.md",
    "docs/plans/2026-09-24-staging-migration-backup-retry.md",
    "docs/prd/2026-09-24-release-test-loop.md",
    "deploy/domestic-release.example.json",
    "deploy/domestic-release-role.production.example",
    "deploy/domestic-release-role.staging.example",
    "scripts/domestic_release.py",
    "scripts/domestic_release_build.py",
    "scripts/test_domestic_release.py",
    "scripts/test_domestic_release_build.py",
    "deploy/domestic-promote.py",
}
RELEASE_TOOL_PREFIXES = ("scripts/ci/",)


def release_tooling_only(changed: list[str]) -> bool:
    if not changed:
        return False
    for path in changed:
        if path in RELEASE_TOOL_FILES or path.startswith(RELEASE_TOOL_PREFIXES):
            continue
        if path.startswith("deploy/test_domestic_promote") and path.endswith(".py"):
            continue
        return False
    return True


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
    if not isinstance(changed, list) or not all(isinstance(path, str) for path in changed):
        return full("invalid-changed-paths")
    if release_tooling_only(changed):
        return {"mode": "targeted", "lanes": ["preflight"], "checks": [],
                "reason": "release-tooling-contracts", "profile": "tooling"}
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
