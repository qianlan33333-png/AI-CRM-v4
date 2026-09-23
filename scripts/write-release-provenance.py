#!/usr/bin/env python3
"""Write immutable provenance for an AI-CRM-v3 release archive."""
from __future__ import annotations

import hashlib
import json
import os
from pathlib import Path
import platform
import subprocess
import sys


def git(*args: str) -> str:
    return subprocess.check_output(["git", *args], text=True).strip()


def main() -> int:
    archive = Path(sys.argv[1]).resolve()
    output = Path(sys.argv[2]).resolve()
    commit = os.environ.get("GITHUB_SHA") or git("rev-parse", "HEAD")
    tree = git("rev-parse", f"{commit}^{{tree}}")
    digest = hashlib.sha256(archive.read_bytes()).hexdigest()
    payload = {
        "repository": "AI-CRM-v3",
        "commit_sha": commit,
        "tree_sha": tree,
        "package_sha256": digest,
        "build_environment": {
            "os": platform.platform(),
            "arch": platform.machine(),
            "go": subprocess.check_output(["go", "version"], text=True).strip(),
            "node": subprocess.check_output(["node", "--version"], text=True).strip(),
            "npm": subprocess.check_output(["npm", "--version"], text=True).strip(),
        },
        "build_command": "scripts/run-donor-view-consumers.sh release-fast",
    }
    output.write_text(json.dumps(payload, ensure_ascii=False, indent=2) + "\n")
    print(output)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
