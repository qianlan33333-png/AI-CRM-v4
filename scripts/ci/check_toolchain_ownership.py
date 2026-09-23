#!/usr/bin/env python3
"""Keep the original donor npm evidence while allowing only V3 tooling upgrades."""
import argparse
import hashlib
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
HISTORICAL = {
    "package.json": "ab5eaf7d1c014619f2d3ef8eeebd4f7a0336f0384df4aaa5e96dc0cad245b19e",
    "package-lock.json": "bbcf2ecd7a3eaf9c5fe0b8dc594047e7cf733d86b6eb89c601391b6059d34408",
}


def check(root: Path) -> None:
    record = json.loads((root / "docs/donor-manifests/v3-toolchain-ownership.json").read_text())
    snapshots = {p: "docs/donor-manifests/toolchain-v2/" + p.removesuffix(".json") + ".frozen.json" for p in HISTORICAL}
    if (record.get("schema") != 1 or record.get("active_paths") != list(HISTORICAL)
            or record.get("historical_snapshots") != snapshots
            or record.get("historical_manifest") != "docs/donor-manifests/pr01-web.sha256"
            or record.get("source_commit") != "6bfbe5816bb89913c70adaca87d6a486260e016e"
            or record.get("owner") != "V3 build and security maintainers"):
        raise ValueError("toolchain ownership cannot expand or erase historical donor identity")
    manifest = (root / record["historical_manifest"]).read_text().splitlines()
    for path, expected in HISTORICAL.items():
        if not any(line.split() == [expected, path] for line in manifest):
            raise ValueError("original donor manifest was rewritten")
        snapshot = root / snapshots[path]
        if snapshot.is_symlink() or hashlib.sha256(snapshot.read_bytes()).hexdigest() != expected:
            raise ValueError("original donor toolchain snapshot changed")
    active = json.loads((root / "package.json").read_text())
    if active.get("engines") != {"node": "24.18.0", "npm": "11.12.1"} or active.get("packageManager") != "npm@11.12.1":
        raise ValueError("active V3 runtime must remain explicitly pinned")
    for group in ("dependencies", "devDependencies", "overrides"):
        for value in active.get(group, {}).values():
            parts = value.split(".") if isinstance(value, str) else []
            if len(parts) != 3 or not all(part.isdigit() for part in parts):
                raise ValueError("active V3 dependency versions must be exact releases")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=ROOT)
    check(parser.parse_args().root)
    print("V3 toolchain ownership and original donor evidence: PASS")
