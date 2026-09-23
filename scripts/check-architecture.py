#!/usr/bin/env python3
"""Minimal mechanical architecture checks for the clean v3 baseline."""

from __future__ import annotations

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
MODULE_PREFIX = "github.com/qianlan33333-png/AI-CRM-v3/internal/"
DONOR_MARKERS = ("AI-CRM-production", "AI-CRM-v2", "aicrm_next")
IMPORT_RE = re.compile(r'"(' + re.escape(MODULE_PREFIX) + r'[^\"]+)"')
VERIFIED_FACT_SYMBOL_RE = re.compile(r"\b(?:NewVerifiedFact|ProviderVerifiedIdentityInput)\b")
TRUSTED_VERIFIED_FACT_DIRS = frozenset({"adapter", "provider"})


def fail(message: str) -> None:
    print(f"architecture: {message}", file=sys.stderr)
    raise SystemExit(1)


def domain_of(path: Path) -> str | None:
    parts = path.relative_to(ROOT).parts
    if len(parts) >= 2 and parts[0] == "internal":
        return parts[1]
    return None


def target_parts(import_path: str) -> tuple[str, tuple[str, ...]]:
    suffix = import_path.removeprefix(MODULE_PREFIX)
    parts = tuple(suffix.split("/"))
    return parts[0], parts[1:]


def go_code_without_comments_or_literals(source: str) -> str:
    """Return only Go code tokens relevant to a symbol-presence gate."""
    output: list[str] = []
    index = 0
    while index < len(source):
        current = source[index]
        following = source[index + 1] if index + 1 < len(source) else ""
        if current == "/" and following == "/":
            index += 2
            while index < len(source) and source[index] != "\n":
                index += 1
            output.append("\n")
            continue
        if current == "/" and following == "*":
            index += 2
            while index + 1 < len(source) and source[index : index + 2] != "*/":
                index += 1
            index = min(index + 2, len(source))
            output.append(" ")
            continue
        if current == "`":
            index += 1
            while index < len(source) and source[index] != "`":
                index += 1
            index = min(index + 1, len(source))
            output.append(" ")
            continue
        if current in ('"', "'"):
            delimiter = current
            index += 1
            while index < len(source):
                if source[index] == "\\":
                    index += 2
                    continue
                if source[index] == delimiter:
                    index += 1
                    break
                index += 1
            output.append(" ")
            continue
        output.append(current)
        index += 1
    return "".join(output)


def may_construct_verified_fact(path: Path) -> bool:
    relative = path.relative_to(ROOT)
    if path.name.endswith("_test.go"):
        return True
    if relative == Path("internal/identity/domain/fact.go"):
        return True
    parts = relative.parts
    return (
        len(parts) >= 4
        and parts[0] == "internal"
        and parts[2] in TRUSTED_VERIFIED_FACT_DIRS
    )


def main() -> None:
    go_files = sorted((ROOT / "cmd").rglob("*.go")) + sorted((ROOT / "internal").rglob("*.go"))
    if not go_files:
        fail("no Go source files found")

    for path in go_files:
        source = path.read_text(encoding="utf-8")
        for marker in DONOR_MARKERS:
            if marker in source:
                fail(f"{path.relative_to(ROOT)} references donor marker {marker}")

        owner = domain_of(path)

        # VerifiedFact is opaque at the Go type boundary, but its exported
        # constructor must be callable by provider-verified adapters. Keep that
        # trust assertion mechanically enforceable: HTTP/app/store/cmd code may
        # receive a fact but may never mint one from raw identifiers.
        if VERIFIED_FACT_SYMBOL_RE.search(go_code_without_comments_or_literals(source)) and not may_construct_verified_fact(path):
            fail(
                f"{path.relative_to(ROOT)} constructs a verified identity outside an adapter/provider boundary"
            )

        if owner != "config" and "/platform/config/" not in f"/{path.relative_to(ROOT).as_posix()}/":
            if "os.Getenv(" in source or "os.LookupEnv(" in source:
                fail(f"{path.relative_to(ROOT)} reads environment outside platform/config")

        for imported in IMPORT_RE.findall(source):
            target, remainder = target_parts(imported)
            if owner is None:  # cmd is the composition root.
                continue
            if owner == "platform":
                if target != "platform":
                    fail(f"platform imports business domain {target}: {path.relative_to(ROOT)}")
                continue
            if target in (owner, "platform"):
                continue
            if not remainder or remainder[0] not in ("port", "domain"):
                fail(
                    f"cross-domain concrete import {imported} from {path.relative_to(ROOT)}; "
                    "only port/domain contracts are allowed"
                )

    print(f"architecture: checked {len(go_files)} Go files")


if __name__ == "__main__":
    main()
