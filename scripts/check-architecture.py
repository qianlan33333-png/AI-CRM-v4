#!/usr/bin/env python3
"""Runtime ownership checks, with an explicit Go test-composition boundary."""

from __future__ import annotations

import re
import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
MODULE_PREFIX = "github.com/qianlan33333-png/AI-CRM-v3/internal/"
DONOR_MARKERS = ("AI-CRM-production", "AI-CRM-v2", "aicrm_next")
GO_NONCODE_RE = re.compile(r'//[^\n]*|/\*[\s\S]*?\*/|"(?:\\.|[^"\\])*"|\'(?:\\.|[^\'\\])*\'|`[^`]*`')
IMPORT_LITERAL_RE = re.compile(r'(?:(\w+|[._])\s+)?("[^"\n]+"|`[^`]+`)')
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


def mask_go(source: str, keep_strings: bool = False) -> str:
    # Preserve offsets so import declarations can be located in code, without
    # interpreting comments or strings containing fake Go as imports.
    def replace(match: re.Match[str]) -> str:
        value = match.group()
        if keep_strings and value[0] in ('"', '`'):
            return value
        return "".join("\n" if char == "\n" else " " for char in value)
    return GO_NONCODE_RE.sub(replace, source)


def imports_of(source: str) -> list[tuple[str, str]]:
    code = mask_go(source)
    literals = mask_go(source, keep_strings=True)
    result: list[tuple[str, str]] = []
    for declaration in re.finditer(r"\bimport\b", code):
        start = declaration.end()
        next_token = re.search(r"\S", code[start:])
        if next_token and code[start + next_token.start()] == "(":
            end = code.find(")", start + next_token.start())
        else:
            end = code.find("\n", start)
        if end < 0:
            end = len(code)
        for match in IMPORT_LITERAL_RE.finditer(literals[start:end]):
            value = match[2]
            imported = value[1:-1] if value[0] == '`' else json.loads(value)
            result.append((match[1] or imported.rsplit("/", 1)[-1], imported))
    return result


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
        is_test = path.name.endswith("_test.go")
        code = mask_go(source)
        imports = imports_of(source)

        # VerifiedFact is opaque at the Go type boundary, but its exported
        # constructor must be callable by provider-verified adapters. Keep that
        # trust assertion mechanically enforceable: HTTP/app/store/cmd code may
        # receive a fact but may never mint one from raw identifiers.
        if VERIFIED_FACT_SYMBOL_RE.search(go_code_without_comments_or_literals(source)) and not may_construct_verified_fact(path):
            fail(
                f"{path.relative_to(ROOT)} constructs a verified identity outside an adapter/provider boundary"
            )

        if not is_test and "/platform/config/" not in f"/{path.relative_to(ROOT).as_posix()}/":
            for alias, imported in imports:
                if imported == "os":
                    receiver = "" if alias == "." else re.escape(alias) + r"\s*\.\s*"
                    if re.search(r"\b" + receiver + r"(?:Getenv|LookupEnv)\s*\(", code):
                        fail(f"{path.relative_to(ROOT)} reads environment outside platform/config")

        for _, imported in imports:
            if not imported.startswith(MODULE_PREFIX):
                continue
            if not is_test and any(part in ("tests", "testutil", "testkit") for part in imported.removeprefix(MODULE_PREFIX).split("/")):
                fail(f"runtime imports a test-only helper: {path.relative_to(ROOT)}")
            if is_test:
                # Go excludes these files from the runtime binary. Integration
                # fixtures may compose stores/adapters and synthetic envs.
                continue
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
