#!/usr/bin/env python3
"""Validate a staging receipt before it can authorize a release promotion."""
from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path
import re

SHA = re.compile(r"^[0-9a-f]{40}$")
DIGEST = re.compile(r"^[0-9a-f]{64}$")


def load(path: Path) -> dict:
    value = json.loads(path.read_text())
    if not isinstance(value, dict) or value.get("schema") != 1:
        raise ValueError("invalid staging receipt schema")
    for key in ("repository", "commit_sha", "tree_sha", "package_sha256", "environment", "status"):
        if key not in value:
            raise ValueError(f"staging receipt missing {key}")
    if value["repository"] != "AI-CRM-v3" or value["environment"] != "staging" or value["status"] != "accepted":
        raise ValueError("staging receipt is not an accepted AI-CRM-v3 staging receipt")
    if not SHA.fullmatch(value["commit_sha"]) or not SHA.fullmatch(value["tree_sha"]):
        raise ValueError("staging receipt has invalid commit/tree")
    if not DIGEST.fullmatch(value["package_sha256"]):
        raise ValueError("staging receipt has invalid package digest")
    return value


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("receipt", type=Path)
    parser.add_argument("--head", required=True)
    parser.add_argument("--tree", required=True)
    parser.add_argument("--package", type=Path)
    args = parser.parse_args()
    receipt = load(args.receipt)
    if receipt["commit_sha"] != args.head or receipt["tree_sha"] != args.tree:
        raise SystemExit("staging receipt commit/tree does not match requested release")
    if args.package:
        digest = hashlib.sha256(args.package.read_bytes()).hexdigest()
        if digest != receipt["package_sha256"]:
            raise SystemExit("staging receipt package digest mismatch")
    print(json.dumps(receipt, sort_keys=True, separators=(",", ":")))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
