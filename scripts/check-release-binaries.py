#!/usr/bin/env python3
"""Reject release executables that cannot run on the Linux amd64 target."""
from __future__ import annotations

import argparse
from pathlib import Path
import stat


def is_linux_amd64_elf(path: Path) -> bool:
    data = path.read_bytes()[:20]
    # ELF magic, 64-bit class, little endian, x86-64 machine (EM_X86_64=62).
    return len(data) >= 20 and data[:4] == b"\x7fELF" and data[4] == 2 and data[5] == 1 and data[18:20] == b"\x3e\x00"


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("bin_root", type=Path)
    root = parser.parse_args().bin_root
    if not root.is_dir():
        raise SystemExit(f"missing release bin directory: {root}")
    binaries = sorted(p for p in root.rglob("*") if p.is_file() and p.stat().st_mode & stat.S_IXUSR)
    if not binaries:
        raise SystemExit("release contains no executable binaries")
    invalid = [p for p in binaries if not is_linux_amd64_elf(p)]
    if invalid:
        names = ", ".join(str(p.relative_to(root)) for p in invalid)
        raise SystemExit("release binary is not Linux amd64 ELF: " + names)
    print(f"release binaries: {len(binaries)} Linux amd64 ELF files")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
