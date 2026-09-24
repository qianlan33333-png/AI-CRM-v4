#!/usr/bin/env python3
"""Keep PR code verification separate from staging handoff acceptance."""
from __future__ import annotations

import json
import os
import subprocess
import re


SHA = re.compile(r"^[0-9a-f]{40}$")
NON_RUNTIME_PREFIXES = (".github/", "docs/", "scripts/ci/", "scripts/staging-fixtures/", "skills/")
NON_RUNTIME_FILES = {"AGENTS.md"}
NON_RUNTIME_FILES.add("internal/adminops/retention_resources.generated.json")
NON_RUNTIME_FILES.add("deploy/README.md")
TEST_ONLY_PREFIXES = ("scripts/test-", "scripts/test_", "scripts/ci/test_", "deploy/test_")


def is_test_only(path: str) -> bool:
    return (path.startswith(TEST_ONLY_PREFIXES)
            or path.endswith(("_test.go", ".test.mjs", ".spec.mjs", "_chromium_journey.mjs")))
OPERATOR_ONLY_PREFIXES = (
    "scripts/deploy-release-local.sh",
    "deploy/install-release.sh",
    "deploy/run-release-as-root.sh",
)
# Release guard and deployment mechanics are validated by their own contracts;
# they do not require an application receipt when no runtime code changes.
OPERATOR_ONLY_PREFIXES += (
    "scripts/domestic_release.py",
    "scripts/domestic_release_build.py",
    "scripts/test_domestic_release.py",
    "scripts/test_domestic_release_build.py",
    "deploy/domestic-promote.py",
    "deploy/domestic-release-role.production.example",
    "deploy/domestic-release-role.staging.example",
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
    "scripts/verify-staging-source.py",
    "scripts/test_staging_source_gate.py",
    "scripts/release_freshness.py",
    "scripts/write-release-provenance.py",
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


def is_runtime_path(path: str) -> bool:
    return (path not in NON_RUNTIME_FILES
            and not path.startswith(NON_RUNTIME_PREFIXES)
            and not is_test_only(path)
            and path not in OPERATOR_ONLY_PREFIXES)


def requires_staging_receipt(current: str) -> bool:
    base = os.environ.get("PR_BASE_SHA", "")
    if not SHA.fullmatch(base):
        return True
    changed = subprocess.check_output(
        ["git", "-c", "core.quotePath=false", "diff", "--name-only", f"{base}...{current}"],
        text=True,
    ).splitlines()
    return any(is_runtime_path(path) for path in changed)


def main() -> int:
    if os.environ.get("GITHUB_EVENT_NAME") != "pull_request":
        return 0
    try:
        needs = json.loads(os.environ.get("CI_NEEDS", "{}"))
        mode = needs.get("plan", {}).get("outputs", {}).get("mode", "light")
    except json.JSONDecodeError:
        raise SystemExit("invalid CI_NEEDS while checking staging receipt")
    if mode not in {"light", "targeted", "full"}:
        raise SystemExit("invalid PR verification mode while checking staging receipt")
    current = os.environ.get("PR_HEAD_SHA") or subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip()
    if not requires_staging_receipt(current):
        print(json.dumps({"head": current, "staging": "not_applicable_to_non_runtime_change"}, separators=(",", ":")))
        return 0
    if mode not in {"full", "targeted"}:
        raise SystemExit("runtime PR requires selected code CI; staging is checked at release handoff")
    tree = subprocess.check_output(["git", "rev-parse", f"{current}^{{tree}}"], text=True).strip()
    # PR body links are author-controlled and never count as staging proof.
    # release_control handoff checks local preview/package/journey consistency;
    # the coordinator must separately confirm trusted staging-node origin.
    print(json.dumps({"head": current, "tree": tree,
                      "staging": "pending_release_handoff_validation"}, separators=(",", ":")))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
