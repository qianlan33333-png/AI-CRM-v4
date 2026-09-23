#!/usr/bin/env python3
"""Create a portable release archive without macOS metadata entries."""
from __future__ import annotations

import argparse
from pathlib import Path
import tarfile


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("root", type=Path)
    parser.add_argument("archive", type=Path)
    args = parser.parse_args()
    root = args.root.resolve()
    archive = args.archive.resolve()
    if not root.is_dir() or archive == root:
        raise SystemExit("invalid release root/archive")
    all_paths = sorted(root.rglob("*"))
    links = [p for p in all_paths if p.is_symlink()]
    if links:
        raise SystemExit("symlink in release: " + str(links[0]))
    entries = [p for p in all_paths if p.is_file()]
    bad = [p for p in entries if p.name.startswith("._") or "".join(p.relative_to(root).parts).startswith("._")]
    if bad:
        raise SystemExit("AppleDouble entry in release: " + str(bad[0]))
    archive.parent.mkdir(parents=True, exist_ok=True)
    with tarfile.open(archive, "w:gz", format=tarfile.PAX_FORMAT) as bundle:
        for path in entries:
            bundle.add(path, arcname=path.relative_to(root).as_posix(), recursive=False)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
