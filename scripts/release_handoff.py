#!/usr/bin/env python3
"""Validate an immutable developer-to-release handoff.

This command never edits the Git worktree. It only reads Git metadata and
validates a JSON handoff document supplied by the originating task.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import re
import subprocess
from pathlib import Path

SHA = re.compile(r"^[0-9a-f]{40}$")
REQUIRED = (
    "change_class", "work_item", "origin_thread_id", "branch", "worktree", "commit_sha",
    "tree_sha", "scope", "affected_modules", "dependencies", "local_tests",
    "known_risks", "release_ready", "pr_url", "oneid_decision",
    "persistence_decision", "external_effects_decision", "rollback_point",
    "base_main_sha", "merge_preview_sha", "candidate_tree_sha",
)
SHA256 = re.compile(r"^[0-9a-f]{64}$")


def file_sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for block in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def validate_acceptance(value: dict, handoff_path: Path) -> None:
    acceptance = value["staging_acceptance"]
    if not isinstance(acceptance, dict) or acceptance.get("status") != "accepted":
        raise ValueError("staging acceptance must be an accepted receipt reference")
    raw_path = acceptance.get("receipt")
    if not isinstance(raw_path, str) or not raw_path:
        raise ValueError("staging receipt path missing")
    receipt_path = Path(raw_path)
    if not receipt_path.is_absolute():
        receipt_path = handoff_path.parent / receipt_path
    data = receipt_path.read_bytes()
    if not SHA256.fullmatch(acceptance.get("receipt_sha256", "")) or hashlib.sha256(data).hexdigest() != acceptance["receipt_sha256"]:
        raise ValueError("staging receipt digest mismatch")
    receipt = json.loads(data)
    for field in ("candidate_id", "merge_preview_sha", "candidate_tree_sha", "package_sha256"):
        if receipt.get(field) != acceptance.get(field):
            raise ValueError(f"staging receipt {field} differs from handoff reference")
    for field in ("merge_preview_sha", "candidate_tree_sha", "package_sha256"):
        if receipt.get(field) != value.get(field):
            raise ValueError(f"staging receipt {field} differs from handoff")
    if receipt.get("status") != "accepted" or receipt.get("work_item") != value["work_item"]:
        raise ValueError("staging receipt status or work item mismatch")
    package_path = Path(receipt.get("package_path", ""))
    if not package_path.is_absolute(): package_path = receipt_path.parent / package_path
    if not package_path.is_file() or file_sha256(package_path) != value["package_sha256"]:
        raise ValueError("accepted package file missing or SHA mismatch")
    built_path = Path(receipt.get("built_receipt", ""))
    if not built_path.is_absolute(): built_path = receipt_path.parent / built_path
    if not built_path.is_file() or not SHA256.fullmatch(receipt.get("built_receipt_sha256", "")) or hashlib.sha256(built_path.read_bytes()).hexdigest() != receipt["built_receipt_sha256"]:
        raise ValueError("built receipt missing or SHA mismatch")
    built = json.loads(built_path.read_bytes())
    if built.get("status") != "built" or any(built.get(field) != value[field] for field in ("merge_preview_sha", "candidate_tree_sha", "package_sha256")):
        raise ValueError("built receipt differs from accepted candidate")
    journeys = receipt.get("journeys")
    if not isinstance(journeys, list) or not journeys:
        raise ValueError("staging receipt requires work-item journeys")
    for journey in journeys:
        if not isinstance(journey, dict) or journey.get("work_item") != value["work_item"]:
            raise ValueError("journey work item mismatch")
        for field in ("name", "expected", "actual", "command", "effect_mode", "evidence_sha256"):
            if not journey.get(field):
                raise ValueError(f"journey missing {field}")
        if journey.get("passed") is not True or not SHA256.fullmatch(journey["evidence_sha256"]):
            raise ValueError("journey failed or has invalid evidence digest")
        evidence_path = Path(journey.get("evidence_path", ""))
        if not evidence_path.is_absolute():
            evidence_path = receipt_path.parent / evidence_path
        if not evidence_path.is_file() or hashlib.sha256(evidence_path.read_bytes()).hexdigest() != journey["evidence_sha256"]:
            raise ValueError("journey evidence is missing or changed")


def validate_governance_acceptance(value: dict, handoff_path: Path) -> None:
    acceptance = value.get("governance_acceptance")
    if not isinstance(acceptance, dict) or acceptance.get("status") != "accepted":
        raise ValueError("governance-only handoff requires accepted governance evidence")
    checks = acceptance.get("checks")
    if not isinstance(checks, list) or not checks:
        raise ValueError("governance acceptance requires checks")
    for check in checks:
        if not isinstance(check, dict) or not check.get("name") or not check.get("command") or check.get("passed") is not True:
            raise ValueError("governance check contract incomplete")
        evidence_path = Path(check.get("evidence_path", ""))
        if not evidence_path.is_absolute(): evidence_path = handoff_path.parent / evidence_path
        digest = check.get("evidence_sha256", "")
        if not evidence_path.is_file() or not SHA256.fullmatch(digest) or file_sha256(evidence_path) != digest:
            raise ValueError("governance check evidence missing or changed")


def git(root: Path, *args: str) -> str:
    return subprocess.check_output(["git", "-C", str(root), *args], text=True).strip()


def live_remote_main(root: Path) -> str:
    try:
        line = subprocess.check_output(["git", "-C", str(root), "ls-remote", "--exit-code", "origin", "refs/heads/main"],
                                       text=True, timeout=30).strip()
    except (subprocess.CalledProcessError, subprocess.TimeoutExpired) as exc:
        raise ValueError("cannot verify live origin/main") from exc
    sha = line.split()[0] if line else ""
    if not SHA.fullmatch(sha): raise ValueError("live origin/main did not return a commit SHA")
    return sha


def validate(path: Path) -> dict:
    value = json.loads(path.read_text())
    if "supersedes_checkpoint_id" in value and (not isinstance(value["supersedes_checkpoint_id"], str)
                                               or not value["supersedes_checkpoint_id"].strip()):
        raise ValueError("supersedes_checkpoint_id must be a nonempty checkpoint ID")
    missing = [key for key in REQUIRED if key not in value]
    if missing:
        raise ValueError("handoff missing: " + ", ".join(missing))
    for key in ("commit_sha", "tree_sha", "candidate_tree_sha"):
        if not isinstance(value[key], str) or not SHA.fullmatch(value[key]):
            raise ValueError(f"handoff {key} must be a full commit/tree SHA")
    for key in ("base_main_sha", "merge_preview_sha"):
        if not isinstance(value[key], str) or not SHA.fullmatch(value[key]):
            raise ValueError(f"handoff {key} must be full SHA")
    if value["change_class"] not in {"runtime", "governance_only"}:
        raise ValueError("handoff change_class must be runtime or governance_only")
    if not isinstance(value["release_ready"], bool):
        raise ValueError("handoff release_ready must be boolean")
    for key in ("affected_modules", "dependencies", "local_tests", "known_risks"):
        if not isinstance(value[key], list):
            raise ValueError(f"handoff {key} must be an array")
    root = Path(value["worktree"]).resolve()
    if not root.is_dir():
        raise ValueError(f"handoff worktree does not exist: {root}")
    actual_commit = git(root, "rev-parse", "HEAD")
    actual_tree = git(root, "rev-parse", "HEAD^{tree}")
    actual_branch = git(root, "symbolic-ref", "--short", "HEAD")
    if actual_commit != value["commit_sha"]:
        raise ValueError("handoff commit_sha does not match worktree HEAD")
    if actual_tree != value["tree_sha"]:
        raise ValueError("handoff tree_sha does not match worktree HEAD")
    if actual_branch != value["branch"]:
        raise ValueError("handoff branch does not match worktree branch")
    if git(root, "status", "--porcelain"):
        raise ValueError("handoff worktree is dirty")
    try:
        live_main = git(root, "rev-parse", "refs/remotes/origin/main")
        merge_tree = git(root, "rev-parse", value["merge_preview_sha"] + "^{tree}")
        parents = git(root, "rev-list", "--parents", "-n", "1", value["merge_preview_sha"]).split()[1:]
    except subprocess.CalledProcessError as exc:
        raise ValueError("origin/main or merge preview commit unavailable locally") from exc
    if live_main != value["base_main_sha"]:
        raise ValueError("candidate is stale: origin/main moved")
    if live_remote_main(root) != value["base_main_sha"]:
        raise ValueError("candidate is stale: remote main moved")
    if parents != [value["base_main_sha"], value["commit_sha"]]:
        raise ValueError("merge preview must have current main and PR head as parents")
    if merge_tree != value["candidate_tree_sha"]:
        raise ValueError("candidate tree does not match merge preview")
    if value["release_ready"] is not True:
        raise ValueError("handoff release_ready must be true")
    if not value["pr_url"].startswith("https://github.com/") or "/pull/" not in value["pr_url"]:
        raise ValueError("handoff needs a GitHub PR")
    if value["change_class"] == "runtime":
        if not isinstance(value.get("package_sha256"), str) or not SHA256.fullmatch(value["package_sha256"]):
            raise ValueError("runtime handoff package_sha256 must be full digest")
        if "staging_acceptance" not in value: raise ValueError("runtime handoff missing staging_acceptance")
        validate_acceptance(value, path)
    else:
        if value.get("package_sha256") not in {None, ""} or "staging_acceptance" in value:
            raise ValueError("governance-only handoff cannot claim a runtime package")
        validate_governance_acceptance(value, path)
    return value


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("handoff", type=Path)
    args = parser.parse_args()
    value = validate(args.handoff)
    print(json.dumps({"valid": True, "handoff": value}, ensure_ascii=False))


if __name__ == "__main__":
    main()
