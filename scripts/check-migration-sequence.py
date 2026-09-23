#!/usr/bin/env python3
"""Check changed migrations for sequence collisions and common destructive SQL."""
from __future__ import annotations

import argparse
from pathlib import Path
import re
import subprocess

from ci.migration_safety import dangerous_sql_reasons

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
    changed = subprocess.check_output(["git", "diff", "--name-only", f"{args.base}...HEAD", "--", "migrations"], text=True).splitlines()
    deleted = subprocess.check_output(["git", "diff", "--diff-filter=D", "--name-only", f"{args.base}...HEAD", "--", "migrations"], text=True).splitlines()
    deleted_migrations = [name for name in deleted if Path(name).suffix.lower() == ".sql"]
    if deleted_migrations:
        raise SystemExit("migration history files are immutable; add a new migration instead of deleting: "
                         + ", ".join(deleted_migrations))
    collisions = sorted(v for v in current if v in base and any(Path(p).name == Path(current[v]).name for p in changed))
    if collisions:
        details = ", ".join(f"{v} ({current[v]} already exists in {base[v]})" for v in collisions)
        raise SystemExit("migration version already exists on base; assign the next unused version: " + details)

    changed_migrations = [Path(name) for name in changed if Path(name).suffix.lower() == ".sql"]
    unsafe = []
    for migration in changed_migrations:
        if not migration.is_file():
            continue  # Deleted files are handled by repository history checks.
        for reason in dangerous_sql_reasons(migration.read_text(encoding="utf-8")):
            unsafe.append(f"{migration}: {reason}")
    if unsafe:
        raise SystemExit(
            "destructive migration SQL is rejected: " + "; ".join(unsafe)
            + ". Use a forward-compatible expand/backfill/contract sequence."
        )

    print(f"migration sequence: {len(current)} unique versions; no base collision; "
          f"{len(changed_migrations)} changed SQL file(s) passed destructive SQL checks")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
