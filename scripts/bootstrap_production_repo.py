#!/usr/bin/env python3
"""Create a history-free repository snapshot from an exact production source tree."""
from __future__ import annotations

import argparse
import hashlib
import json
import shutil
import subprocess
import re
from pathlib import Path


def git(root: Path, *args: str) -> str:
    return subprocess.check_output(["git", "-C", str(root), *args], text=True).strip()


def matches_file(receipt_file: Path, raw_path: str, digest: str) -> bool:
    if not isinstance(raw_path, str) or not raw_path or not re.fullmatch(r"[0-9a-f]{64}", digest or ""):
        return False
    path = Path(raw_path)
    if not path.is_absolute(): path = receipt_file.parent / path
    if not path.is_file(): return False
    actual = hashlib.sha256()
    with path.open("rb") as handle:
        for block in iter(lambda: handle.read(1024 * 1024), b""):
            actual.update(block)
    return actual.hexdigest() == digest


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--source", type=Path, required=True)
    parser.add_argument("--receipt", type=Path, required=True)
    parser.add_argument("--destination", type=Path, required=True)
    parser.add_argument("--repo-name", default="AI-CRM-v4")
    args = parser.parse_args()
    source = args.source.resolve(); destination = args.destination.resolve()
    receipt = json.loads(args.receipt.read_text())
    deferred = receipt.get("business_acceptance", {}).get("status") == "business_acceptance_deferred_to_user"
    if receipt.get("environment") != "production" or (receipt.get("status") != "observing" if deferred else receipt.get("status") != "released"):
        raise SystemExit("production receipt must be released, or observing under the explicit user-deferred exception")
    expected_items = {"alipay-config", "welcome-group-invite", "shipping-address"}
    if deferred:
        decision = receipt["business_acceptance"]
        if decision.get("exception_id") != "current-three-project-post-production-user-acceptance-v1" or set(decision.get("scope", [])) != expected_items or decision.get("owner") != "user":
            raise SystemExit("invalid deferred business acceptance scope")
        readback = receipt.get("production_readback")
        if not isinstance(readback, dict) or readback.get("release_sha") != receipt.get("commit_sha") or readback.get("tree_sha") != receipt.get("tree_sha") or readback.get("package_sha256") != receipt.get("package_sha256") or readback.get("readyz_status") != "ready":
            raise SystemExit("deferred baseline requires exact active production technical readback")
        if not matches_file(args.receipt, readback.get("evidence_path", ""), readback.get("evidence_sha256", "")):
            raise SystemExit("production technical readback evidence missing")
    else:
        business = receipt.get("business_receipts")
        if not isinstance(business, list) or {x.get("work_item") for x in business if isinstance(x, dict)} != expected_items:
            raise SystemExit("production receipt must include all three work-item receipts")
        if any(x.get("status") != "accepted" or not matches_file(args.receipt, x.get("readback_evidence_path", ""), x.get("readback_evidence_sha256", "")) for x in business):
            raise SystemExit("each work item needs accepted production business readback digest")
        observation = receipt.get("observation")
        if not isinstance(observation, dict) or observation.get("status") != "passed" or not matches_file(args.receipt, observation.get("evidence_path", ""), observation.get("evidence_sha256", "")):
            raise SystemExit("production observation evidence missing")
    commit = receipt.get("commit_sha", ""); tree = receipt.get("tree_sha", "")
    if not re.fullmatch(r"[0-9a-f]{40}", commit) or not re.fullmatch(r"[0-9a-f]{40}", tree):
        raise SystemExit("receipt must contain full commit_sha and tree_sha")
    package = receipt.get("package_sha256", "")
    if not re.fullmatch(r"[0-9a-f]{64}", package):
        raise SystemExit("receipt must contain full package_sha256")
    if not matches_file(args.receipt, receipt.get("package_path", ""), package):
        raise SystemExit("production package file missing or digest mismatch")
    if git(source, "rev-parse", commit) != commit or git(source, f"rev-parse", f"{commit}^{{tree}}") != tree:
        raise SystemExit("source repository does not contain the exact production tree")
    if git(source, "status", "--porcelain"):
        raise SystemExit("source repository is dirty")
    if destination.exists():
        raise SystemExit(f"destination already exists: {destination}")
    destination.parent.mkdir(parents=True, exist_ok=True)
    destination.mkdir()
    archive = subprocess.Popen(["git", "-C", str(source), "archive", commit], stdout=subprocess.PIPE)
    subprocess.run(["tar", "-x", "-C", str(destination)], stdin=archive.stdout, check=True)
    if archive.wait() != 0:
        raise SystemExit("source archive failed")
    subprocess.run(["git", "-C", str(destination), "init", "-q"], check=True)
    subprocess.run(["git", "-C", str(destination), "config", "user.name", "AI-CRM Release Baseline"], check=True)
    subprocess.run(["git", "-C", str(destination), "config", "user.email", "release-baseline@localhost"], check=True)
    subprocess.run(["git", "-C", str(destination), "add", "-A"], check=True)
    subprocess.run(["git", "-C", str(destination), "commit", "-qm", f"production baseline {commit}"], check=True)
    first_tree = git(destination, "rev-parse", "HEAD^{tree}")
    if first_tree != tree:
        raise SystemExit("new repository first commit differs from production source tree")
    metadata = {
        "schema": 1, "repository_name": args.repo_name, "source_commit_sha": commit,
        "source_tree_sha": tree, "package_sha256": package,
        "production_receipt_sha256": hashlib.sha256(args.receipt.read_bytes()).hexdigest(),
        "baseline_commit_sha": git(destination, "rev-parse", "HEAD"),
        "business_acceptance_status": "business_acceptance_deferred_to_user" if deferred else "accepted",
        "release_status": receipt["status"],
    }
    sidecar = destination.parent / (destination.name + "-production-provenance.json")
    if sidecar.exists():
        raise SystemExit(f"provenance sidecar already exists: {sidecar}")
    sidecar.write_text(json.dumps(metadata, ensure_ascii=False, indent=2) + "\n")
    print(json.dumps({"destination": str(destination), "baseline_commit": metadata["baseline_commit_sha"], "source_commit_sha": commit, "source_tree_sha": tree, "provenance_sidecar": str(sidecar)}, ensure_ascii=False))


if __name__ == "__main__":
    main()
