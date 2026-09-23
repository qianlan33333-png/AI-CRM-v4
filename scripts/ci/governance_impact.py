#!/usr/bin/env python3
"""Validate the capability registry and report the consumers affected by a Git diff.

This is an impact/review gate, not a replacement for the canonical full lanes.
The report records author declarations separately from independent approval.
"""
from __future__ import annotations

import argparse
import fnmatch
import json
import os
from pathlib import Path
import re
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[2]
REGISTRY = "docs/governance/capability-impact.json"
MODULE = "github.com/qianlan33333-png/AI-CRM-v3/"
RISK = {"low": 0, "medium": 1, "high": 2}
LANES = {"preflight", "backend", "frontend", "browser", "archive-sdk"}
REQUIRED_ROOTS = ("internal/", "cmd/", "web/", "api/", "migrations/", "components/", "scripts/", ".github/", "deploy/")
PROTECTED_RULE_PATHS = (".github/", "scripts/ci/", "docs/governance/")


def git(root: Path, *args: str) -> str:
    return subprocess.check_output(["git", *args], cwd=root, text=True).strip()


def owners(path: str, components: list[dict]) -> set[str]:
    return {c["id"] for c in components if any(fnmatch.fnmatchcase(path, p) for p in c["paths"])}


def validate_registry(registry: dict, root: Path, tracked: list[str]) -> list[dict]:
    if registry.get("schema") != 1:
        raise ValueError("unsupported capability registry schema")
    if not set(REQUIRED_ROOTS).issubset(registry.get("required_roots", [])) or registry.get("excluded_paths"):
        raise ValueError("required production coverage cannot be disabled or excluded")
    components = registry["components"]
    ids = [c["id"] for c in components]
    if len(ids) != len(set(ids)) or not ids:
        raise ValueError("capability ids must be nonempty and unique")
    for component in components:
        if (not component.get("owner") or component.get("risk") not in RISK
                or not component.get("paths") or not component.get("checks")):
            raise ValueError(f"{component['id']}: owner, paths, risk and checks are required")
        for dependency in component.get("depends_on", []):
            if dependency not in ids:
                raise ValueError(f"{component['id']}: unknown dependency {dependency}")
        for pattern in component["paths"]:
            if pattern.startswith("internal/") and any(c in pattern.split("/")[1] for c in "*?["):
                raise ValueError("internal domains require explicit registration; no catch-all domain pattern")
            if not any(fnmatch.fnmatchcase(path, pattern) for path in tracked):
                raise ValueError(f"{component['id']}: stale path pattern {pattern}")
        for check in component["checks"]:
            if check["lane"] not in LANES:
                raise ValueError(f"{component['id']}: unknown canonical lane")
            path = root / check["path"]
            if check["path"] not in tracked or not path.is_file():
                raise ValueError(f"{component['id']}: missing test {check['path']}")
            if check.get("test"):
                if not re.search(r"^func\s+" + re.escape(check["test"]) + r"\s*\(.*\*testing\.T", path.read_text(), re.M):
                    raise ValueError(f"{component['id']}: missing test function {check['test']}")
                if check["lane"] == "browser" and (not check["path"].startswith("cmd/aicrm/") or not check["test"].endswith("ChromiumJourney")):
                    raise ValueError("browser mappings must use auto-discovered Host ChromiumJourney tests")
    # A new production domain cannot silently inherit an all-internal wildcard.
    # Other registered families (e.g. migration CLIs) intentionally share a gate.
    for path in tracked:
        if path.startswith(tuple(registry["required_roots"])) and not owners(path, components):
            if not any(fnmatch.fnmatchcase(path, p) for p in registry.get("excluded_paths", [])):
                raise ValueError(f"unregistered production path: {path}")
    return components


def import_edges(root: Path, tracked: list[str], components: list[dict]) -> list[tuple[str, str, str]]:
    """Derive cross-component Go imports; retain the source as review evidence."""
    edges = []
    for path in tracked:
        if not path.endswith(".go") or path.endswith("_test.go"):
            continue
        source = (root / path).read_text(errors="replace")
        for block in re.findall(r"(?m)^import\s*(?:\((.*?)\)|([^\n]+))", source, re.S):
            # Only import declarations, never arbitrary strings in code/test data.
            for imported in re.findall(r'"([^"\n]+)"', " ".join(block)):
                if not imported.startswith(MODULE):
                    continue
                target = imported[len(MODULE):] + "/__package__.go"
                for consumer in owners(path, components):
                    for dependency in owners(target, components) - {consumer}:
                        edges.append({"consumer": consumer, "dependency": dependency, "source": path})
    return sorted({(e["consumer"], e["dependency"], e["source"]) for e in edges})


def analyze(root: Path, registry: dict, tracked: list[str], changed: list[str]) -> dict:
    components = validate_registry(registry, root, tracked)
    edges = import_edges(root, tracked, components)
    for c in components:
        edges.extend((c["id"], d, REGISTRY) for d in c.get("depends_on", []))
    direct = set()
    unknown = []
    for path in changed:
        matched = owners(path, components)
        direct.update(matched)
        if not matched and path.startswith(tuple(registry["required_roots"])):
            if not any(fnmatch.fnmatchcase(path, p) for p in registry.get("excluded_paths", [])):
                unknown.append(path)
    if unknown:
        raise ValueError("changed paths need capability registration: " + ", ".join(unknown))
    affected = set(direct)
    while True:
        expanded = affected | {consumer for consumer, dependency, _ in edges if dependency in affected}
        if expanded == affected:
            break
        affected = expanded
    selected = [c for c in components if c["id"] in affected]
    # Risk follows directly changed owners, not downstream consumers' unrelated risk.
    risk = max((c["risk"] for c in components if c["id"] in direct), key=RISK.get, default="low")
    if any(path.startswith(PROTECTED_RULE_PATHS) for path in changed):
        risk = "high"  # A registry edit cannot downgrade review of the gate itself.
    checks = {json.dumps(check, sort_keys=True) for c in selected for check in c["checks"]}
    return {"risk": risk, "changed_paths": changed, "direct": sorted(direct),
            "affected": sorted(affected), "checks": [json.loads(c) for c in sorted(checks)],
            "edges": [{"consumer": c, "dependency": d, "source": p} for c, d, p in sorted(set(edges))
                      if c in affected and d in affected],
            "test_execution": "mapped to existing full CI lanes; this report does not execute or select a smaller test suite"}


def author_review(body: str, head: str, risk: str) -> dict:
    fields = {}
    for key in ("Governance-Head", "Governance-Preservation", "Governance-Validation"):
        matches = re.findall(r"^" + key + r":[ \t]*([^\r\n]+)$", body, re.M)
        fields[key] = matches[0].strip() if len(matches) == 1 else ""
    valid = fields["Governance-Head"] == head and all(
        len(fields[k]) >= 12 and not fields[k].startswith("<")
        for k in ("Governance-Preservation", "Governance-Validation"))
    return {"kind": "author_impact_declaration", "head": head,
            "status": "recorded" if valid else "missing_or_stale", "required": risk == "high",
            "independent_review": "unverified"}


def branch_review_policy(repo: str) -> dict:
    result = subprocess.run(["gh", "api", f"repos/{repo}/branches/main/protection"], capture_output=True, text=True, timeout=30)
    if result.returncode:
        return {"status": "unverified", "deployment_enablement_gap": "required independent review could not be verified"}
    policy = json.loads(result.stdout).get("required_pull_request_reviews") or {}
    count = policy.get("required_approving_review_count", 0)
    return {"status": "observed", "required_approving_review_count": count,
            "require_code_owner_reviews": policy.get("require_code_owner_reviews", False),
            "require_last_push_approval": policy.get("require_last_push_approval", False),
            "deployment_enablement_gap": "independent review is not required on main" if count < 1 else None}


def context(root: Path, event: dict, event_name: str) -> tuple[str, str, str]:
    head = git(root, "rev-parse", "HEAD")
    if event_name == "pull_request":
        pr = event["pull_request"]
        return pr["base"]["sha"], head, pr["head"]["sha"]
    before = event.get("before", "")
    base = before if re.fullmatch(r"[0-9a-f]{40}", before) and set(before) != {"0"} else git(root, "rev-parse", "HEAD^")
    return base, head, head


def gate(needs: dict) -> None:
    if needs.get("governance", {}).get("result") != "success":
        raise ValueError("governance must execute successfully; skipped/cancelled/neutral is not evidence")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=ROOT)
    parser.add_argument("--base")
    parser.add_argument("--head", default="HEAD")
    parser.add_argument("--out", type=Path)
    parser.add_argument("--gate", action="store_true")
    args = parser.parse_args()
    if args.gate:
        gate(json.loads(os.environ["CI_NEEDS"]))
        return 0
    root = args.root.resolve()
    event = json.loads(Path(os.environ["GITHUB_EVENT_PATH"]).read_text()) if os.environ.get("GITHUB_EVENT_PATH") else {}
    event_name = os.environ.get("GITHUB_EVENT_NAME", "local")
    base, tested, head = context(root, event, event_name)
    if args.base:
        base = git(root, "rev-parse", args.base + "^{commit}")
        tested = git(root, "rev-parse", args.head + "^{commit}")
        head = tested
    if git(root, "rev-parse", "HEAD") != tested or git(root, "status", "--porcelain", "--untracked-files=no"):
        raise ValueError("impact report requires the clean committed tested checkout")
    tracked = git(root, "ls-files").splitlines()
    changed = git(root, "diff", "--name-only", "--no-renames", base, tested).splitlines()
    registry = json.loads((root / REGISTRY).read_text())
    report = analyze(root, registry, tracked, changed)
    report.update({"schema": 1, "base": base, "tested_sha": tested, "head": head,
                   "tree": git(root, "rev-parse", tested + "^{tree}"), "event": event_name})
    if event_name == "pull_request":
        # Fetch the current body so rerunning a failed job after editing the
        # declaration works, while refusing declarations for a newer PR head.
        pr = json.loads(subprocess.check_output(["gh", "api", f"repos/{os.environ['GITHUB_REPOSITORY']}/pulls/{event['number']}"], text=True, timeout=30))
        if pr["head"]["sha"] != head:
            raise ValueError("PR advanced; rerun CI for the new head")
        report["review"] = author_review(pr.get("body") or "", head, report["risk"])
        report["branch_review_policy"] = branch_review_policy(os.environ["GITHUB_REPOSITORY"])
    else:
        report["review"] = {"status": "not_a_pull_request", "independent_review": "unverified"}
    content = json.dumps(report, ensure_ascii=False, indent=2) + "\n"
    if args.out:
        args.out.parent.mkdir(parents=True, exist_ok=True)
        args.out.write_text(content)
    else:
        print(content)
    if os.environ.get("GITHUB_STEP_SUMMARY"):
        with open(os.environ["GITHUB_STEP_SUMMARY"], "a") as stream:
            stream.write(f"### Governance impact\n\nHead: `{head}`; tested tree: `{report['tree']}`; risk: **{report['risk']}**.\n\n")
            stream.write("Affected capabilities: " + ", ".join(report["affected"]) + ".\n\n")
            stream.write("Author declaration: " + report["review"]["status"] + ". Independent human approval is not asserted by this report.\n")
            if report.get("branch_review_policy", {}).get("deployment_enablement_gap"):
                stream.write("\nDeployment governance gap: " + report["branch_review_policy"]["deployment_enablement_gap"] + ".\n")
    # Author declarations remain useful context in the report, but are not a
    # release gate. The meaningful gates are exact-head/tree, staging receipt,
    # registry/API/security checks and the actual deployment readback.
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (ValueError, KeyError, OSError, subprocess.SubprocessError) as error:
        print(f"governance impact failed: {type(error).__name__}: {error}", file=sys.stderr)
        raise SystemExit(2)
