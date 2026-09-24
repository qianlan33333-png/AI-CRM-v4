#!/usr/bin/env python3
"""Build a deterministic, non-enforcing affected-check plan for a V4 commit pair."""
from __future__ import annotations

import hashlib
import json
from pathlib import Path
import subprocess

import governance_impact
import impact_selection

SCHEMA = 1
POLICY_FILES = (
    "AGENTS.md",
    "README.md",
    "docs/development-before-start.md",
    "docs/governance/capability-impact.json",
    "scripts/dev_preflight.py",
    "scripts/ci/affected_plan.py",
    "scripts/ci/impact_selection.py",
    "scripts/ci/governance_impact.py",
    "scripts/ci/quality_lanes.py",
    "scripts/ci/verification.py",
    "skills/aicrm-v3-development-frontdoor/SKILL.md",
    "skills/aicrm-v3-development/SKILL.md",
)


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
    output = git(root, "diff", "--name-only", "--no-renames", "-z", base, head, binary=True)
    paths = [part.decode("utf-8", errors="replace") for part in output.split(b"\0") if part]
    return sorted(set(paths))


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


def planner_policy_fingerprint() -> str:
    """Hash the planner and selector code that actually produced this plan."""
    files = (Path(__file__).resolve(), Path(impact_selection.__file__).resolve())
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


def analyze_head(root: Path, head: str, paths: list[str]) -> dict:
    tracked = git(root, "ls-tree", "-r", "--name-only", head).splitlines()
    registry = json.loads(git(root, "show", f"{head}:docs/governance/capability-impact.json"))
    return governance_impact.analyze(root, registry, tracked, paths)


def build_plan(root: Path, base: str, head: str, graph_result: dict | None = None) -> dict:
    """Return candidate and current enforced selections bound to exact Git trees.

    ``graph_result`` is reserved for the follow-up package-graph implementation.
    PR1 deliberately selects only from the repository capability registry.
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
    if clean and exact_checkout:
        try:
            report = analyze_head(root, head_sha, paths)
        except Exception as error:
            analysis_error = type(error).__name__ + ": " + str(error)

    graph_valid = graph_result is None
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
    elif graph_result is not None:
        # PR1 reserves the field for the package-graph implementation. Until
        # that contract validates a graph result, never treat supplied graph
        # data as evidence or use it to narrow the candidate.
        graph_valid = False
        candidate = full_selection("unvalidated-graph-result")
        enforced = impact_selection.select(report)
    else:
        try:
            candidate = impact_selection.select_candidate(report)
            enforced = impact_selection.select(report)
        except Exception as error:
            report = None
            analysis_error = "selection-validation-failed: " + str(error)
            candidate = full_selection("selection-validation-failed")
            enforced = full_selection("selection-validation-failed")

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
        "source": source,
        "candidate": normalize_selection(candidate),
        "enforced": normalize_selection(enforced),
        "analysis": {"status": "complete" if report is not None else "failed",
                     "error": analysis_error},
        "evidence_eligible": bool(report is not None and clean and exact_checkout and graph_valid),
        "graph_result": graph_result,
        "selection_source": "capability-registry",
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
