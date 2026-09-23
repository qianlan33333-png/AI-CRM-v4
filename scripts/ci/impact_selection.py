#!/usr/bin/env python3
"""Choose canonical CI lanes from an independent capability-impact report."""
from __future__ import annotations

import argparse
import json
from pathlib import Path

LANES = ("preflight", "backend", "frontend", "browser", "archive-sdk")
PROTECTED_PREFIXES = (".github/", "scripts/ci/", "docs/governance/", "deploy/")
PROTECTED_FILES = {"Makefile", "AGENTS.md"}


def select(report: dict) -> dict:
    changed = report.get("changed_paths", [])
    risk = report.get("risk", "high")
    protected = any(path in PROTECTED_FILES or path.startswith(PROTECTED_PREFIXES) for path in changed)
    if not changed or protected or risk not in {"low", "medium"}:
        return {"mode": "full", "lanes": list(LANES), "reason": "protected-or-high-risk"}
    if risk == "medium":
        lanes = {check["lane"] for check in report.get("checks", [])}
        if not lanes or not lanes.issubset(LANES):
            return {"mode": "full", "lanes": list(LANES), "reason": "unknown-or-unmapped-lane"}
        lanes.update({"preflight", "backend", "frontend", "browser"})
        return {"mode": "targeted", "lanes": [lane for lane in LANES if lane in lanes], "reason": "affected-capabilities"}
    if not changed or any(not (path.startswith("docs/") and path.endswith(".md")) for path in changed):
        return {"mode": "full", "lanes": list(LANES), "reason": "unknown-or-unmapped-path"}
    return {"mode": "targeted", "lanes": ["preflight"], "reason": "documentation-only"}


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("report", type=Path)
    args = parser.parse_args()
    result = select(json.loads(args.report.read_text()))
    print(json.dumps(result, separators=(",", ":")))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
