#!/usr/bin/env python3
"""Catch migration version collisions before a package reaches a host."""
from __future__ import annotations

import argparse
from pathlib import Path
import re
import subprocess

VERSION = re.compile(r"^(\d{4})_.+\.sql$")


def versions(paths: list[str]) -> dict[str, str]:
    result = {}
    for name in paths:
        match = VERSION.match(Path(name).name)
        if match:
            version = match.group(1)
            if version in result:
                raise SystemExit(f"duplicate migration version {version}: {result[version]}, {name}")
            result[version] = name
    return result


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--base", default="origin/main")
    parser.add_argument("--root", type=Path, default=Path("migrations"))
    args = parser.parse_args()
    current = versions([str(p) for p in args.root.glob("*.sql")])
    base_names = subprocess.check_output(["git", "ls-tree", "-r", "--name-only", args.base, "migrations"], text=True).splitlines()
    base = versions(base_names)
    added = subprocess.check_output(["git", "diff", "--name-only", f"{args.base}...HEAD", "--", "migrations"], text=True).splitlines()
    collisions = sorted(v for v in current if v in base and any(Path(p).name == Path(current[v]).name for p in added))
    if collisions:
        details = ", ".join(f"{v} ({current[v]} already exists in {base[v]})" for v in collisions)
        raise SystemExit("migration version already exists on base; assign the next unused version: " + details)
    print(f"migration sequence: {len(current)} unique versions; no base collision")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
