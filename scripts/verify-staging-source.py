#!/usr/bin/env python3
"""Verify the signed v4 source bundle against a merge-preview manifest."""
from __future__ import annotations

import argparse
import json
import re
import subprocess
from pathlib import Path

from release_freshness import verify

REPOSITORY = "qianlan33333-png/AI-CRM-v4"
IDENTITY = "aicrm-release-command-center"
SHA = re.compile(r"^[0-9a-f]{40}$")


def git(repo: Path, *args: str) -> str:
    return subprocess.check_output(["git", "-C", str(repo), *args], text=True).strip()


def check(manifest: Path, bundle: Path, attestation: Path, signature: Path,
          allowed_signers: Path, repository: Path, require_objects: bool = False) -> dict:
    candidate = json.loads(manifest.read_text())
    if candidate.get("schema") != 1:
        raise ValueError("invalid candidate manifest schema")
    for field in ("base_main_sha", "pr_head_sha", "merge_preview_sha", "tree_sha"):
        if not SHA.fullmatch(candidate.get(field, "")):
            raise ValueError(f"invalid candidate {field}")
    # Trees are verified against Git objects below. At this stage they are also
    # signed, so an attacker cannot substitute a different expected tree.
    signed = json.loads(attestation.read_text())
    expected = {
        "repository": REPOSITORY, "branch": "main", "stage": "source",
        "main_sha": candidate["base_main_sha"],
        "main_tree": signed.get("main_tree"),
        "pr_head_sha": candidate["pr_head_sha"],
        "pr_head_tree": signed.get("pr_head_tree"),
        "merge_preview_sha": candidate["merge_preview_sha"],
        "candidate_tree_sha": candidate["tree_sha"],
    }
    value = verify(attestation_path=attestation, signature_path=signature,
                   allowed_signers=allowed_signers, identity=IDENTITY,
                   expected=expected, bundle=bundle, package=None,
                   git_repository=repository)
    if require_objects:
        checks = (("base_main_sha", "main_tree"),
                  ("pr_head_sha", "pr_head_tree"),
                  ("merge_preview_sha", "candidate_tree_sha"))
        for sha_field, tree_field in checks:
            if git(repository, "rev-parse", candidate[sha_field] + "^{tree}") != value[tree_field]:
                raise ValueError(f"{sha_field} tree mismatch")
        parents = git(repository, "rev-list", "--parents", "-n", "1",
                      candidate["merge_preview_sha"]).split()
        if parents != [candidate["merge_preview_sha"], candidate["base_main_sha"],
                       candidate["pr_head_sha"]]:
            raise ValueError("merge preview parents mismatch")
    return value


def main() -> None:
    parser = argparse.ArgumentParser()
    for name in ("manifest", "bundle", "attestation", "signature", "allowed-signers", "git-repository"):
        parser.add_argument("--" + name, type=Path, required=True)
    parser.add_argument("--require-objects", action="store_true")
    args = parser.parse_args()
    check(args.manifest, args.bundle, args.attestation, args.signature,
          args.allowed_signers, args.git_repository, args.require_objects)
    print("verified v4 staging source freshness")


if __name__ == "__main__":
    main()
