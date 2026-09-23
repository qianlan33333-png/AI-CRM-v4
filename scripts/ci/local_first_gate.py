#!/usr/bin/env python3
"""Require an immutable staging receipt for pull requests."""
from __future__ import annotations

import json
import os
import re
import subprocess
import sys


SHA = re.compile(r"^[0-9a-f]{40}$")
NON_RUNTIME_PREFIXES = (".github/", "docs/", "scripts/ci/", "scripts/staging-fixtures/", "skills/")
NON_RUNTIME_FILES = {"AGENTS.md"}
NON_RUNTIME_FILES.add("internal/adminops/retention_resources.generated.json")
NON_RUNTIME_FILES.add("deploy/README.md")
OPERATOR_ONLY_PREFIXES = (
    "scripts/deploy-release-local.sh",
    "deploy/install-release.sh",
    "deploy/run-release-as-root.sh",
)
# Release guard and deployment mechanics are validated by their own contracts;
# they do not require an application receipt when no runtime code changes.
OPERATOR_ONLY_PREFIXES += (
    "deploy/build-release-on-staging.sh",
    "deploy/build-release-on-staging-remote.sh",
    "deploy/promote-staging-release.sh",
    "scripts/check-release-binaries.py",
    "scripts/check-migration-sequence.py",
    "scripts/test_release_preflight.py",
    "scripts/check-install-release-contract.sh",
    "scripts/ci/local_first_gate.py",
    "scripts/validate-staging-receipt.py",
    "scripts/test_staging_receipt.py",
    "scripts/validate-staging-capability.py",
    "scripts/test_validate_staging_capability.py",
    "scripts/run-donor-view-consumers.sh",
    "deploy/create-merge-preview-bundle.sh",
    "deploy/promote-staging-direct.sh",
    "deploy/sync-staging-source-mirror.sh",
    "scripts/accept-staging-candidate.py",
    "scripts/release_candidate.py",
    "scripts/release_queue.py",
    "scripts/release_control.py",
    "scripts/release_coordinator.py",
    "scripts/release_events.py",
    "scripts/release_handoff.py",
    "scripts/test_release_candidate.py",
    "scripts/test_release_queue.py",
    "scripts/test_release_control.py",
    "scripts/test_release_coordinator.py",
    "scripts/test_release_events.py",
    "deploy/seed-staging-business-fixtures.sh",
    "deploy/accept-staging-candidate.sh",
    "scripts/validate-staging-fixture-readback.py",
    "scripts/test_validate_staging_fixture_readback.py",
)


def requires_staging_receipt(current: str) -> bool:
    base = os.environ.get("PR_BASE_SHA", "")
    if not SHA.fullmatch(base):
        return True
    changed = subprocess.check_output(
        ["git", "-c", "core.quotePath=false", "diff", "--name-only", f"{base}...{current}"],
        text=True,
    ).splitlines()
    return any(
        path not in NON_RUNTIME_FILES
        and not path.startswith(NON_RUNTIME_PREFIXES)
        and path not in OPERATOR_ONLY_PREFIXES
        for path in changed
    )


def main() -> int:
    if os.environ.get("GITHUB_EVENT_NAME") != "pull_request":
        return 0
    try:
        needs = json.loads(os.environ.get("CI_NEEDS", "{}"))
        mode = needs.get("plan", {}).get("outputs", {}).get("mode", "light")
    except json.JSONDecodeError:
        raise SystemExit("invalid CI_NEEDS while checking staging receipt")
    if mode != "light":
        print(json.dumps({"mode": mode, "staging": "superseded by full CI"}, separators=(",", ":")))
        return 0
    repo = os.environ["GITHUB_REPOSITORY"]
    number = os.environ["PR_NUMBER"]
    body = json.loads(subprocess.check_output(["gh", "api", f"repos/{repo}/pulls/{number}"], text=True))["body"] or ""
    current = os.environ.get("PR_HEAD_SHA") or subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip()
    if not requires_staging_receipt(current):
        print(json.dumps({"head": current, "staging": "not_required_for_non_runtime_change"}, separators=(",", ":")))
        return 0
    tree = subprocess.check_output(["git", "rev-parse", f"{current}^{{tree}}"], text=True).strip()
    values = {}
    for name in ("Staging-Head", "Staging-Tree", "Staging-Receipt"):
        match = re.search(rf"(?m)^{re.escape(name)}:\s*(\S+)\s*$", body)
        values[name] = match.group(1) if match else ""
    if not SHA.fullmatch(values["Staging-Head"]):
        raise SystemExit("staging receipt head is missing or invalid")
    staging_tree = subprocess.check_output(["git", "rev-parse", f"{values['Staging-Head']}^{{tree}}"], text=True).strip()
    if staging_tree != tree:
        raise SystemExit("staging receipt head tree does not match the current PR tree")
    if not SHA.fullmatch(values["Staging-Tree"]) or values["Staging-Tree"] != tree:
        raise SystemExit("staging receipt tree does not match the current PR tree")
    if values["Staging-Receipt"].startswith("<"):
        raise SystemExit("staging receipt link is missing")
    print(json.dumps({"head": current, "tree": tree, "receipt": values["Staging-Receipt"]}, separators=(",", ":")))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
