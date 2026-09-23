#!/usr/bin/env python3
"""Reject new exact duplicate paths between two complete pinned Git commits.

The comparison reads Git objects only. It has no extension or directory allowlist:
regular files, binary blobs, empty files, names with unusual bytes, and symlinks
are considered. A symlink is grouped only with other symlinks, never with a
regular file holding the same blob bytes.

Optional exceptions are narrowly scoped JSON:
{
  "schema_version": 1,
  "exceptions": [{
    "path": "repository-relative/path",
    "content_sha256": "64 lowercase hex characters",
    "reason": "why this exact duplicate path is required"
  }]
}
Each exception applies to one exact head path/hash pair. Directories, globs and
unused exceptions are rejected.
"""
from __future__ import annotations

import argparse
from collections import defaultdict
from contextlib import contextmanager
import json
import os
from pathlib import Path, PurePosixPath
import re
import subprocess
import sys
import tempfile
from typing import Any, Iterator

from scan_exact_duplicates import scan


class DuplicateGateError(RuntimeError):
    """The exact-duplicate gate cannot prove that the head is safe."""


def _git(repo: Path, *args: str, check: bool = True) -> subprocess.CompletedProcess:
    env = dict(os.environ, GIT_NO_REPLACE_OBJECTS="1", GIT_NO_LAZY_FETCH="1", GIT_TERMINAL_PROMPT="0")
    result = subprocess.run(
        ["git", "-C", str(repo), *args],
        env=env,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        timeout=120,
    )
    if check and result.returncode:
        raise DuplicateGateError(result.stderr.decode("utf8", "replace").strip() or "Git command failed")
    return result


def _resolve_commit(repo: Path, ref: str) -> str:
    return _git(repo, "rev-parse", "--verify", "--end-of-options", f"{ref}^{{commit}}").stdout.decode("ascii").strip()


def _assert_ancestor(repo: Path, base: str, head: str) -> None:
    result = _git(repo, "merge-base", "--is-ancestor", base, head, check=False)
    if result.returncode:
        raise DuplicateGateError("base commit must be an ancestor of head commit")


def _require_complete(report: dict[str, Any], label: str) -> None:
    errors = report.get("errors")
    if not report.get("full_payload_inventory_complete") or errors:
        detail = "; ".join(str(item) for item in errors or []) or "Git tree contains a submodule or LFS pointer"
        raise DuplicateGateError(f"{label} exact scan is incomplete: {detail}")
    inventory = report.get("inventory")
    if not isinstance(inventory, list) or any("content_sha256" not in entry for entry in inventory if entry.get("object_type") == "blob"):
        raise DuplicateGateError(f"{label} exact scan did not verify every blob")


def _groups(report: dict[str, Any]) -> tuple[dict[tuple[str, str], list[dict[str, Any]]], dict[tuple[str, str], set[str]]]:
    all_paths: dict[tuple[str, str], set[str]] = defaultdict(set)
    entries_by_key: dict[tuple[str, str], list[dict[str, Any]]] = defaultdict(list)
    for entry in report["inventory"]:
        if entry.get("object_type") != "blob":
            continue
        entry_kind = entry.get("entry_kind")
        oid = entry.get("object_id")
        path = entry.get("path")
        if entry_kind not in {"regular", "symlink"} or not isinstance(oid, str) or not isinstance(path, str):
            raise DuplicateGateError("exact scan returned an invalid blob inventory entry")
        key = (entry_kind, oid)
        all_paths[key].add(path)
        entries_by_key[key].append(entry)
    duplicates = {
        key: sorted(entries_by_key[key], key=lambda entry: entry["path"])
        for key, paths in all_paths.items()
        if len(paths) >= 2
    }
    return duplicates, all_paths


def _valid_relative_path(value: str) -> bool:
    path = PurePosixPath(value)
    return (
        bool(value)
        and bool(path.parts)
        and not path.is_absolute()
        and str(path) == value
        and all(part != ".." for part in path.parts)
    )


def _load_exceptions(path: Path | None) -> set[tuple[str, str]]:
    if path is None:
        return set()
    try:
        data = json.loads(path.read_text(encoding="utf8"))
    except OSError as exc:
        raise DuplicateGateError(f"cannot read exceptions file: {exc}") from exc
    except json.JSONDecodeError as exc:
        raise DuplicateGateError(f"invalid exceptions JSON: {exc.msg}") from exc
    if not isinstance(data, dict) or data.get("schema_version") != 1 or not isinstance(data.get("exceptions"), list):
        raise DuplicateGateError("exceptions must contain schema_version=1 and an exceptions array")
    allowed: set[tuple[str, str]] = set()
    for item in data["exceptions"]:
        if not isinstance(item, dict) or set(item) != {"path", "content_sha256", "reason"}:
            raise DuplicateGateError("each exception must be an object")
        path_value, digest, reason = item.get("path"), item.get("content_sha256"), item.get("reason")
        if not isinstance(path_value, str) or not _valid_relative_path(path_value):
            raise DuplicateGateError("exception path must be one exact repository-relative path")
        if not isinstance(digest, str) or not re.fullmatch(r"[0-9a-f]{64}", digest):
            raise DuplicateGateError("exception content_sha256 must be a lowercase SHA-256")
        if not isinstance(reason, str) or not reason.strip():
            raise DuplicateGateError("exception reason must be non-empty")
        key = (path_value, digest)
        if key in allowed:
            raise DuplicateGateError("duplicate exact exception")
        allowed.add(key)
    return allowed


@contextmanager
def _scan_reports(repo: Path, base: str, head: str) -> Iterator[tuple[dict[str, Any], dict[str, Any]]]:
    with tempfile.TemporaryDirectory(prefix="aicrm-exact-duplicate-gate-") as directory:
        root = Path(directory)
        yield scan(repo, base, root / "base"), scan(repo, head, root / "head")


def check(repo: Path, base_ref: str, head_ref: str, exceptions_path: Path | None = None) -> dict[str, Any]:
    repo = repo.resolve()
    base, head = _resolve_commit(repo, base_ref), _resolve_commit(repo, head_ref)
    _assert_ancestor(repo, base, head)
    exceptions = _load_exceptions(exceptions_path)
    with _scan_reports(repo, base, head) as (base_report, head_report):
        _require_complete(base_report, "base")
        _require_complete(head_report, "head")
        base_groups, base_paths = _groups(base_report)
        head_groups, _ = _groups(head_report)

    violations: list[dict[str, Any]] = []
    for key, entries in sorted(head_groups.items()):
        existing_paths = base_paths.get(key, set())
        group_previously_existed = key in base_groups
        for entry in entries:
            path = entry["path"]
            if path in existing_paths:
                continue
            violation = {
                "kind": "new_duplicate_path" if group_previously_existed else "new_duplicate_group",
                "entry_kind": key[0],
                "object_id": key[1],
                "content_sha256": entry["content_sha256"],
                "bytes_per_copy": entry["bytes"],
                "path": path,
            }
            violations.append(violation)
    violations.sort(key=lambda item: (item["path"], item["content_sha256"], item["kind"]))

    active_exceptions = {
        (entry["path"], entry["content_sha256"])
        for entries in head_groups.values()
        for entry in entries
    } & exceptions
    rejected = []
    for violation in violations:
        key = (violation["path"], violation["content_sha256"])
        if key not in active_exceptions:
            rejected.append(violation)
    unused = exceptions - active_exceptions
    if unused:
        values = ", ".join(f"{path}@{digest}" for path, digest in sorted(unused))
        raise DuplicateGateError(f"unused exact duplicate exception: {values}")
    if rejected:
        values = ", ".join(f"{item['kind']}:{item['path']}@{item['content_sha256']}" for item in rejected)
        raise DuplicateGateError(f"new exact duplicate paths: {values}")

    return {
        "schema_version": 1,
        "base_commit": base,
        "head_commit": head,
        "checked_entry_kinds": ["regular", "symlink"],
        "violations": violations,
        "exempted_paths": sorted(path for path, _ in active_exceptions),
        "status": "pass",
    }


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("repo", type=Path)
    parser.add_argument("--base", required=True, help="Pinned base commit or ref")
    parser.add_argument("--head", required=True, help="Pinned head commit or ref")
    parser.add_argument("--exceptions", type=Path, help="Optional exact path/hash/reason JSON")
    args = parser.parse_args()
    try:
        print(json.dumps(check(args.repo, args.base, args.head, args.exceptions), ensure_ascii=True, sort_keys=True))
    except (DuplicateGateError, OSError, ValueError, subprocess.SubprocessError) as exc:
        print(f"exact duplicate gate failed: {exc}", file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
