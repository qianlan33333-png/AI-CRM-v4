#!/usr/bin/env python3
"""Build a deterministic, non-enforcing affected-check plan for a V4 commit pair."""
from __future__ import annotations

import hashlib
import json
from pathlib import Path
import subprocess

import governance_impact
import impact_selection
import go_affected_graph

SCHEMA = 1
POLICY_FILES = (
    "AGENTS.md",
    "README.md",
    "docs/development-before-start.md",
    "docs/prd/2026-09-24-small-step-impact-checks.md",
    "docs/governance/capability-impact.json",
    ".github/workflows/ci.yml",
    "scripts/dev_preflight.py",
    "scripts/ci/affected_plan.py",
    "scripts/ci/impact_selection.py",
    "scripts/ci/governance_impact.py",
    "scripts/ci/quality_lanes.py",
    "scripts/ci/affected_shadow.py",
    "scripts/ci/go_affected_graph.py",
    "scripts/ci/verification.py",
    "skills/aicrm-v3-development-frontdoor/SKILL.md",
    "skills/aicrm-v3-development/SKILL.md",
)
EXECUTABLE_POLICY_FILES = {
    ".github/workflows/ci.yml", "docs/governance/capability-impact.json",
    "scripts/dev_preflight.py", "scripts/ci/affected_plan.py", "scripts/ci/impact_selection.py",
    "scripts/ci/governance_impact.py", "scripts/ci/quality_lanes.py", "scripts/ci/affected_shadow.py",
    "scripts/ci/go_affected_graph.py", "scripts/ci/verification.py",
}
POLICY_PREFIXES = (".github/workflows/", "docs/governance/", "scripts/ci/")
PAYMENT_LEAF_PREFIX = "internal/payment/"
PAYMENT_BROWSER_CHECK = {
    "lane": "browser",
    "path": "cmd/aicrm/payment_actions_public_checkout_chromium_postgres_integration_test.go",
    "test": "TestPostgreSQLPaymentActionsPublicCheckoutChromiumJourney",
}
IMPACT_MAPS = {
    "sql": {
        "embedded_files": "Go EmbedFiles, TestEmbedFiles, and XTestEmbedFiles package ownership",
        "migration_prefix": "migrations/",
        "migration_change": "protected-full-selection",
    },
    "api": {
        "prefix": "api/",
        "checks": [{"lane": "frontend", "path": "scripts/check-openapi-route-parity.test.mjs"}],
        "change": "protected-full-selection",
    },
    "browser": {"payment_leaf_check": PAYMENT_BROWSER_CHECK},
}
PARENT_PRD_PATH = "docs/prd/2026-09-24-small-step-impact-checks.md"


def git(root: Path, *args: str, binary: bool = False):
    result = subprocess.run(["git", *args], cwd=root, stdout=subprocess.PIPE,
                            stderr=subprocess.PIPE, check=False)
    if result.returncode:
        detail = result.stderr.decode("utf-8", errors="replace").strip()
        raise ValueError("git " + " ".join(args) + " failed" + (": " + detail if detail else ""))
    return result.stdout if binary else result.stdout.decode("utf-8").strip()


def resolve_commit(root: Path, ref: str) -> str:
    if not isinstance(ref, str) or not ref.strip():
        raise ValueError("base and head must be nonempty Git revisions")
    return git(root, "rev-parse", "--verify", "--end-of-options", ref + "^{commit}")


def tree_for(root: Path, commit: str) -> str:
    return git(root, "rev-parse", "--verify", commit + "^{tree}")


def changed_paths(root: Path, base: str, head: str) -> list[str]:
    return go_affected_graph.changed_paths(root, base, head)


def source_status(root: Path) -> list[str]:
    return git(root, "status", "--porcelain=v1", "--untracked-files=all").splitlines()


def repository_policy_fingerprint(root: Path, head: str) -> str:
    """Hash repository policy files from the requested commit, not the worktree."""
    entries = {}
    for path in POLICY_FILES:
        result = subprocess.run(["git", "show", f"{head}:{path}"], cwd=root,
                                stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, check=False)
        entries[path] = hashlib.sha256(result.stdout).hexdigest() if result.returncode == 0 else None
    payload = json.dumps({"schema": SCHEMA, "files": entries}, sort_keys=True,
                         separators=(",", ":")).encode()
    return hashlib.sha256(payload).hexdigest()


def parent_prd_identity(root: Path, head: str) -> dict:
    result = subprocess.run(["git", "show", f"{head}:{PARENT_PRD_PATH}"], cwd=root,
                            stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, check=False)
    if result.returncode:
        return {"id": None, "path": PARENT_PRD_PATH, "sha256": None}
    return {"id": PARENT_PRD_PATH, "path": PARENT_PRD_PATH,
            "sha256": hashlib.sha256(result.stdout).hexdigest()}


def planner_policy_fingerprint() -> str:
    """Hash the planner and selector code that actually produced this plan."""
    files = (Path(__file__).resolve(), Path(impact_selection.__file__).resolve(),
             Path(go_affected_graph.__file__).resolve())
    entries = {path.name: hashlib.sha256(path.read_bytes()).hexdigest() for path in files}
    payload = json.dumps({"schema": SCHEMA, "files": entries}, sort_keys=True,
                         separators=(",", ":")).encode()
    return hashlib.sha256(payload).hexdigest()


def combined_policy_fingerprint(planner: str, repository: str) -> str:
    payload = json.dumps({"schema": SCHEMA, "planner": planner, "repository_head": repository},
                         sort_keys=True, separators=(",", ":")).encode()
    return hashlib.sha256(payload).hexdigest()


def normalize_selection(selection: dict) -> dict:
    reasons = selection.get("reasons") or [selection.get("reason", "unspecified")]
    return {
        "selection_mode": selection["mode"],
        "selected_lanes": selection["lanes"],
        "selected_checks": selection.get("checks", []),
        "selection_reasons": sorted(set(reasons)),
        "profile": selection.get("profile", "full"),
    }


def full_selection(reason: str) -> dict:
    return {"mode": "full", "lanes": list(impact_selection.LANES), "checks": [],
            "reason": reason, "reasons": [reason], "profile": "full"}


def analyze_trusted_base(root: Path, base: str, head: str, paths: list[str]) -> dict:
    """Analyze with policy from base and a base/head path union.

    This prevents a PR from editing its own capability classification to make
    its candidate plan narrower. Any such edit is still forced to full below.
    """
    base_tracked = git(root, "ls-tree", "-r", "--name-only", base).splitlines()
    head_tracked = git(root, "ls-tree", "-r", "--name-only", head).splitlines()
    tracked = sorted(set(base_tracked) | set(head_tracked))
    registry = json.loads(git(root, "show", f"{base}:docs/governance/capability-impact.json"))
    return governance_impact.analyze(root, registry, tracked, paths)


def validate_graph(graph: object, base_sha: str, base_tree: str, head_sha: str,
                   head_tree: str, paths: list[str]) -> tuple[bool, str | None]:
    if not isinstance(graph, dict) or graph.get("schema") != 1:
        return False, "missing-or-unsupported-package-graph"
    expected = {"baseline_sha": base_sha, "baseline_tree": base_tree,
                "head_sha": head_sha, "head_tree": head_tree}
    if any(graph.get(key) != value for key, value in expected.items()):
        return False, "package-graph-source-binding-mismatch"
    if graph.get("changed_paths") != paths:
        return False, "package-graph-changed-paths-mismatch"
    if graph.get("graph_valid") is not True or graph.get("unowned_go_paths"):
        return False, "package-graph-has-unowned-go-paths"
    selected = graph.get("selected_packages")
    if not isinstance(selected, list):
        return False, "package-graph-has-invalid-package-inventory"
    for item in selected:
        if (not isinstance(item, dict) or not isinstance(item.get("dir"), str)
                or not isinstance(item.get("import_path"), str)):
            return False, "package-graph-has-invalid-package-inventory"
    if not isinstance(graph.get("graph_fingerprint"), str) or len(graph["graph_fingerprint"]) != 64:
        return False, "package-graph-has-invalid-fingerprint"
    return True, None


def graph_not_required(paths: list[str]) -> bool:
    """Prove that only documentation or registered CI tooling changed."""
    if not paths:
        return False
    return all(
        impact_selection.release_tooling_only([path])
        or (path in impact_selection.ORDINARY_DOC_FILES)
        or (path.startswith(("docs/", "skills/")) and path.endswith(".md")
            and not path.startswith("docs/governance/"))
        for path in paths
    )


def non_go_graph_receipt(base_sha: str, base_tree: str, head_sha: str, head_tree: str,
                         paths: list[str]) -> dict:
    payload = json.dumps(
        {"schema": 1, "status": "not_required", "proof": "registered_docs_or_tooling_only",
         "baseline_sha": base_sha, "baseline_tree": base_tree, "head_sha": head_sha,
         "head_tree": head_tree, "changed_paths": paths},
        sort_keys=True, separators=(",", ":")).encode()
    return {
        "schema": 1, "status": "not_required", "proof": "registered_docs_or_tooling_only",
        "baseline_sha": base_sha, "baseline_tree": base_tree, "head_sha": head_sha,
        "head_tree": head_tree, "changed_paths": paths, "graph_valid": True,
        "unowned_go_paths": [], "selected_packages": [],
        "graph_fingerprint": hashlib.sha256(payload).hexdigest(),
        "module": "",
    }


def package_candidate(report: dict, graph: dict, paths: list[str], policy_changed: bool) -> dict:
    """Select a shadow-only package candidate for explicit domain changes.

    Only mapped medium-risk domains and payment leaves can use a bounded
    package candidate. Shared, policy, unknown, and other high-risk paths stay
    full. Current enforced checks are calculated separately by the legacy
    selector.
    """
    if policy_changed:
        return full_selection("policy-change-requires-full"), []
    old_candidate = impact_selection.select_candidate(report)
    selected_packages = graph.get("selected_packages", [])
    if not selected_packages:
        return old_candidate, []

    app_paths = [path for path in paths
                 if not (path.startswith("docs/") and path.endswith(".md"))]
    payment_leaf = bool(app_paths) and all(path.startswith(PAYMENT_LEAF_PREFIX) for path in app_paths)
    mapped_medium_domain = report.get("risk") in {"low", "medium"} and bool(report.get("direct"))
    if not payment_leaf and not mapped_medium_domain:
        return old_candidate, []

    # A Go package can be selected only when its changed source is owned and
    # the capability registry recognizes the direct source paths.
    if not report.get("direct") or any(path.startswith((".github/", "scripts/ci/", "docs/governance/",
                                                        "migrations/", "api/", "internal/platform/",
                                                        "internal/identity/", "internal/config/"))
                                      for path in app_paths):
        return full_selection("protected-or-unmapped-package-impact"), []

    checks = []
    for check in report.get("checks", []):
        if check.get("lane") in {"frontend", "browser", "archive-sdk"}:
            checks.append(check)
        elif check.get("lane") == "backend":
            # Backend checks are covered by running every test in each package
            # selected from the base/head Go test-import closure.
            continue
    if payment_leaf:
        checks = [check for check in checks if check.get("lane") != "browser"]
        checks.append(PAYMENT_BROWSER_CHECK)
    checks = {json.dumps(check, sort_keys=True): check for check in checks}
    checks = [checks[key] for key in sorted(checks)]

    lanes = {"preflight", "backend"}
    lanes.update(check["lane"] for check in checks)
    reasons = ["go-test-import-closure"]
    reasons.append("payment-domain-leaf" if payment_leaf else "registered-capability")
    candidate = {
        "mode": "targeted", "lanes": [lane for lane in impact_selection.LANES if lane in lanes],
        "checks": checks, "reason": "unioned-impacts", "reasons": sorted(reasons),
        "profile": "affected-packages",
    }
    packages = sorted({item["import_path"] for item in selected_packages})
    return candidate, packages


def build_plan(root: Path, base: str, head: str, graph_result: dict | None = None) -> dict:
    """Return candidate and current enforced selections bound to exact Git trees.

    The current candidate and exact enforced selector are both retained. A
    supplied graph is accepted only when every source binding and changed path
    matches; ordinary CLI use builds the graph from isolated base/head trees.
    """
    root = root.resolve()
    base_sha = resolve_commit(root, base)
    head_sha = resolve_commit(root, head)
    base_tree, head_tree = tree_for(root, base_sha), tree_for(root, head_sha)
    paths = changed_paths(root, base_sha, head_sha)
    status = source_status(root)
    checked_out_head = git(root, "rev-parse", "HEAD")
    clean = not status
    exact_checkout = checked_out_head == head_sha

    report = None
    analysis_error = None
    # POLICY_FILES contribute to the trace fingerprint, while only executable
    # gate/selector policy changes force the trusted full fallback. Human-facing
    # instructions, PRDs, and skill descriptions remain ordinary documentation.
    policy_changed = any(path in EXECUTABLE_POLICY_FILES or path.startswith(POLICY_PREFIXES)
                         for path in paths)
    if clean and exact_checkout:
        try:
            report = analyze_trusted_base(root, base_sha, head_sha, paths)
        except Exception as error:
            analysis_error = type(error).__name__ + ": " + str(error)

    graph_problem = None
    graph_valid = False
    if report is not None:
        try:
            if graph_result is None and graph_not_required(paths):
                graph_result = non_go_graph_receipt(base_sha, base_tree, head_sha, head_tree, paths)
            elif graph_result is None:
                graph_result = go_affected_graph.build_graph(root, base_sha, head_sha)
            graph_valid, graph_problem = validate_graph(
                graph_result, base_sha, base_tree, head_sha, head_tree, paths)
        except Exception as error:
            graph_problem = "package-graph-failed: " + type(error).__name__ + ": " + str(error)
            graph_result = None
    if report is None:
        if not clean:
            reason = "dirty-working-tree"
        elif not exact_checkout:
            reason = "head-is-not-checked-out"
        else:
            reason = "impact-analysis-failed"
        analysis_error = analysis_error or reason
        candidate = full_selection(reason)
        enforced = full_selection(reason)
        candidate_packages = []
    elif not graph_valid:
        candidate = full_selection(graph_problem or "invalid-package-graph")
        enforced = impact_selection.select(report)
        candidate_packages = []
    else:
        try:
            candidate, candidate_packages = package_candidate(report, graph_result, paths, policy_changed)
            # A policy author cannot use the policy under review to describe
            # the gate as narrower. This is a shadow record; the stable CI
            # workflow remains the authority for the currently enforced gate.
            enforced = (full_selection("trusted-policy-change-fallback") if policy_changed
                        else impact_selection.select(report))
        except Exception as error:
            report = None
            analysis_error = "selection-validation-failed: " + str(error)
            candidate = full_selection("selection-validation-failed")
            enforced = full_selection("selection-validation-failed")
            candidate_packages = []

    source = {"checked_out_head": checked_out_head, "head_matches": exact_checkout,
              "working_tree_clean": clean, "status": status}
    planner_fingerprint = planner_policy_fingerprint()
    repository_fingerprint = repository_policy_fingerprint(root, head_sha)
    plan = {
        "schema": SCHEMA,
        "mode": "shadow",
        "observed_mode": "shadow",
        "baseline_sha": base_sha,
        "baseline_tree": base_tree,
        "head_sha": head_sha,
        "head_tree": head_tree,
        "policy_fingerprint": combined_policy_fingerprint(planner_fingerprint, repository_fingerprint),
        "policy_fingerprints": {"planner": planner_fingerprint,
                                "repository_head": repository_fingerprint},
        "changed_paths": paths,
        "source_clean": clean,
        "source": source,
        "candidate": normalize_selection(candidate),
        "selected_lanes": normalize_selection(candidate)["selected_lanes"],
        "selected_checks": normalize_selection(candidate)["selected_checks"],
        "candidate_go_packages": (sorted({item["import_path"] for item in graph_result.get("selected_packages", [])
                                          if isinstance(item, dict) and isinstance(item.get("import_path"), str)})
                                  if graph_valid and isinstance(graph_result, dict) else candidate_packages),
        "enforced": normalize_selection(enforced),
        "impact_maps": IMPACT_MAPS,
        "parent_prd": parent_prd_identity(root, head_sha),
        "analysis": {"status": "complete" if report is not None else "failed",
                     "error": analysis_error},
        "evidence_eligible": bool(report is not None and clean and exact_checkout and graph_valid
                                  and not policy_changed),
        "graph_result": graph_result,
        "selection_source": "trusted-baseline-registry-and-go-test-graph",
    }
    return plan


def main() -> int:
    import argparse

    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=Path(__file__).resolve().parents[2])
    parser.add_argument("--base", required=True)
    parser.add_argument("--head", required=True)
    args = parser.parse_args()
    plan = build_plan(args.root, args.base, args.head)
    print(json.dumps(plan, ensure_ascii=False, sort_keys=True, separators=(",", ":")))
    return 0 if plan["evidence_eligible"] else 2


if __name__ == "__main__":
    raise SystemExit(main())
