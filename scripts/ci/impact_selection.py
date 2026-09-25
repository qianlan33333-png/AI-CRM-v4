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
    "scripts/domestic_main_release.py",
    "scripts/manual_github_sync.py",
    "scripts/test_domestic_release.py",
    "scripts/test_domestic_release_build.py",
    "scripts/test_domestic_release_controller.py",
    "scripts/test_manual_github_sync.py",
    "deploy/domestic-promote.py",
    "deploy/test_domestic_main_source.py",
}
RELEASE_TOOL_PREFIXES = ("scripts/ci/",)

# Candidate-only policy. Keep it separate from select(): PR1 records this plan
# in shadow mode while the current CI selector remains the enforced contract.
CANDIDATE_POLICY_FILES = {
    ".github/workflows/ci.yml", "docs/governance/capability-impact.json",
    "scripts/dev_preflight.py",
    "scripts/ci/affected_plan.py", "scripts/ci/impact_selection.py",
    "scripts/ci/governance_impact.py", "scripts/ci/quality_lanes.py",
    "scripts/ci/quality_report.py", "scripts/ci/local_first_gate.py",
    "scripts/ci/verification.py",
}
CANDIDATE_POLICY_PREFIXES = ("docs/governance/",)
ORDINARY_DOC_PREFIXES = ("docs/", "skills/")
ORDINARY_DOC_FILES = {
    "AGENTS.md", "README.md", "docs/development-before-start.md",
    "skills/aicrm-v3-development-frontdoor/SKILL.md",
    "skills/aicrm-v3-development/SKILL.md",
}


def _candidate_full(reason: str, reasons: list[str] | None = None) -> dict:
    why = sorted(set(reasons or [reason]))
    return {"mode": "full", "lanes": list(LANES), "checks": [], "reason": reason,
            "reasons": why, "profile": "full"}


def _ordinary_document(path: str) -> bool:
    if path in ORDINARY_DOC_FILES:
        return True
    return (path.startswith(ORDINARY_DOC_PREFIXES) and path.endswith(".md")
            and not path.startswith(CANDIDATE_POLICY_PREFIXES))


def _candidate_tooling(path: str) -> bool:
    # Repository-wide instructions and the selector itself may not classify
    # their own change as low impact. Other documented release-tool contracts
    # retain the existing narrow tooling profile as a shadow candidate.
    if path in CANDIDATE_POLICY_FILES or _ordinary_document(path):
        return False
    if path in RELEASE_TOOL_FILES:
        return True
    if path.startswith("scripts/ci/test_") and path.endswith(".py"):
        return True
    return path.startswith("deploy/test_domestic_promote") and path.endswith(".py")


def select_candidate(report: dict) -> dict:
    """Select a conservative shadow plan without changing the enforced selector.

    Documentation and release-tooling contributions are combined. Any policy,
    protected, unknown, high-risk, or malformed input keeps the full plan.
    """
    if not isinstance(report, dict):
        return _candidate_full("invalid-impact-report")
    changed = report.get("changed_paths", [])
    risk = report.get("risk", "high")
    if (not isinstance(changed, list) or not changed
            or not all(isinstance(path, str) and path and not path.startswith("/")
                       and ".." not in Path(path).parts for path in changed)):
        return _candidate_full("invalid-or-empty-changed-paths")
    if risk not in {"low", "medium", "high"}:
        return _candidate_full("invalid-risk-classification")

    if any(path in CANDIDATE_POLICY_FILES or path.startswith(CANDIDATE_POLICY_PREFIXES)
           for path in changed):
        return _candidate_full("policy-change-requires-full")

    document_paths = [path for path in changed if _ordinary_document(path)]
    tooling_paths = [path for path in changed if _candidate_tooling(path)]
    classified = set(document_paths) | set(tooling_paths)
    app_paths = [path for path in changed if path not in classified]

    # Shared, migration, provider, build, and generic deployment changes remain
    # conservative even if a caller supplies an attractive check mapping.
    protected = [path for path in app_paths
                 if path in PROTECTED_FILES or path.startswith(PROTECTED_PREFIXES)]
    if protected:
        return _candidate_full("protected-or-shared-path")

    checks = report.get("checks", [])
    if not isinstance(checks, list) or any(not valid_check(check) for check in checks):
        return _candidate_full("unknown-or-unmapped-check")

    reasons = []
    if document_paths:
        reasons.append("documentation-only")
    if tooling_paths:
        reasons.append("release-tooling-contracts")

    if app_paths:
        direct = report.get("direct", [])
        mapped = isinstance(direct, list) and bool(direct)
        if (any(not path.startswith("internal/") for path in app_paths)
                or risk not in {"low", "medium"} or not mapped or not checks):
            return _candidate_full("unknown-or-high-risk-impact")
        reasons.append("affected-capabilities")

    if not reasons:
        return _candidate_full("unknown-or-unmapped-path")

    # A tooling/doc-only combination should not inherit broad registry closure
    # checks (for example downstream Go and browser consumers of delivery
    # governance). Those two contributions both map to the preflight profile.
    if not app_paths:
        profile = "tooling" if tooling_paths else "documentation"
        reason = reasons[0] if len(reasons) == 1 else "unioned-impacts"
        return {"mode": "targeted", "lanes": ["preflight"], "checks": [],
                "reason": reason, "reasons": sorted(reasons), "profile": profile}

    lanes = {"preflight"}
    lanes.update(check["lane"] for check in checks)
    unioned_checks = {json.dumps(check, sort_keys=True): check for check in checks}
    if len(reasons) == 1:
        reason = reasons[0]
    else:
        reason = "unioned-impacts"
    profile = "affected"
    return {"mode": "targeted", "lanes": [lane for lane in LANES if lane in lanes],
            "checks": [unioned_checks[key] for key in sorted(unioned_checks)],
            "reason": reason, "reasons": sorted(reasons), "profile": profile}


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
