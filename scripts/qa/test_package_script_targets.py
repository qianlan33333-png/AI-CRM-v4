#!/usr/bin/env python3
"""Guard literal repository-local file targets exposed by npm scripts."""

from __future__ import annotations

import json
import shlex
import unittest
from pathlib import Path
from typing import Mapping


ROOT = Path(__file__).resolve().parents[2]
LOCAL_PREFIXES = ("scripts/", "web/scripts/", "./scripts/", "./web/scripts/")


def missing_package_script_targets(
    scripts: Mapping[str, str], repository_root: Path
) -> list[tuple[str, str]]:
    """Return npm scripts that name missing or out-of-tree local files."""
    root = repository_root.resolve()
    missing: list[tuple[str, str]] = []
    for script_name, command in sorted(scripts.items()):
        for token in shlex.split(command):
            target = token.rstrip(",;)")
            if target.startswith(LOCAL_PREFIXES):
                path = (root / target.removeprefix("./")).resolve()
                if not path.is_relative_to(root) or not path.is_file():
                    missing.append((script_name, token))
    return missing


def package_script_targets() -> list[tuple[str, str]]:
    package = json.loads((ROOT / "package.json").read_text(encoding="utf-8"))
    scripts = package.get("scripts")
    if not isinstance(scripts, dict) or not all(
        isinstance(name, str) and isinstance(command, str)
        for name, command in scripts.items()
    ):
        raise ValueError("package.json scripts must be a string-to-string object")
    return missing_package_script_targets(scripts, ROOT)


class PackageScriptTargetsTest(unittest.TestCase):
    def test_valid_targets_and_external_commands_are_accepted(self):
        import tempfile

        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / "scripts").mkdir()
            (root / "web/scripts").mkdir(parents=True)
            (root / "scripts/check.sh").write_text("exit 0\n", encoding="utf-8")
            (root / "web/scripts/check.mjs").write_text("", encoding="utf-8")
            missing = missing_package_script_targets(
                {
                    "shell": "bash scripts/check.sh",
                    "relative-shell": "./scripts/check.sh",
                    "node": "node web/scripts/check.mjs",
                    "external": "npm run build && node -e 'process.exit(0)'",
                },
                root,
            )
        self.assertEqual(missing, [])

    def test_missing_targets_include_npm_script_name_and_path(self):
        import tempfile

        with tempfile.TemporaryDirectory() as temporary:
            missing = missing_package_script_targets(
                {"edge:contract": "scripts/missing.sh"}, Path(temporary)
            )
        self.assertEqual(missing, [("edge:contract", "scripts/missing.sh")])

    def test_paths_cannot_escape_the_repository_root(self):
        import tempfile

        with tempfile.TemporaryDirectory() as temporary:
            parent = Path(temporary)
            root = parent / "repo"
            root.mkdir()
            (parent / "outside.sh").write_text("exit 0\n", encoding="utf-8")
            missing = missing_package_script_targets(
                {"unsafe": "scripts/../../outside.sh"}, root
            )
        self.assertEqual(missing, [("unsafe", "scripts/../../outside.sh")])

    def test_current_package_has_no_missing_literal_local_targets(self):
        self.assertEqual(package_script_targets(), [])


if __name__ == "__main__":
    unittest.main()
