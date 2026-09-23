#!/usr/bin/env python3
"""Fail closed on unreviewed donor-source authority changes between Git commits.

The gate reads only pinned Git objects. It validates each commit's source index,
source lock, canonical payload identity and canonical blob bytes before comparing
all authority paths. An authority change needs one explicitly reviewed record
bound to the base commit and to the complete before/after SHA-256 path set.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import subprocess
import sys
from typing import Any

INDEX_PATH = "web/donor-sources/source-index.json"
LOCK_PATH = "web/donor-sources/source-lock.json"
APPROVALS_PATH = "docs/engineering/dedup/p5-authority-change-approvals.json"
HEX40 = re.compile(r"[0-9a-f]{40}\Z")
HEX64 = re.compile(r"[0-9a-f]{64}\Z")
ENV = dict(os.environ, GIT_NO_REPLACE_OBJECTS="1", GIT_NO_LAZY_FETCH="1", GIT_TERMINAL_PROMPT="0")


class AuthorityGateError(RuntimeError):
    """The gate could not prove a source authority change is reviewed."""


def git(repo: Path, *args: str, check: bool = True) -> subprocess.CompletedProcess[bytes]:
    result = subprocess.run(["git", "-C", str(repo), *args], env=ENV, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=120)
    if check and result.returncode:
        raise AuthorityGateError(result.stderr.decode("utf8", "replace").strip() or "Git command failed")
    return result


def resolve_commit(repo: Path, value: str) -> str:
    return git(repo, "rev-parse", "--verify", "--end-of-options", f"{value}^{{commit}}").stdout.decode("ascii").strip()


def require_ancestor(repo: Path, base: str, head: str) -> None:
    if git(repo, "merge-base", "--is-ancestor", base, head, check=False).returncode:
        raise AuthorityGateError("base commit must be an ancestor of head commit")


def valid_path(value: Any) -> bool:
    if not isinstance(value, str) or not value or "\\" in value or "\0" in value:
        return False
    path = PurePosixPath(value)
    return not path.is_absolute() and str(path) == value and all(piece not in {"", ".", ".."} for piece in path.parts)


def required_string(value: Any, label: str) -> str:
    if not isinstance(value, str) or not value:
        raise AuthorityGateError(f"{label} must be a nonempty string")
    return value


def required_hex(value: Any, width: int, label: str) -> str:
    if not isinstance(value, str) or not (HEX40 if width == 40 else HEX64).fullmatch(value):
        raise AuthorityGateError(f"{label} must be lowercase {width}-hex")
    return value


def tree(repo: Path, commit: str) -> dict[str, tuple[str, str]]:
    rows = git(repo, "ls-tree", "-r", "-z", "--full-tree", commit).stdout.split(b"\0")
    values: dict[str, tuple[str, str]] = {}
    for row in rows:
        if not row:
            continue
        header, raw_path = row.split(b"\t", 1)
        mode, kind, oid = header.split()
        if kind != b"blob":
            continue
        path = raw_path.decode("utf8", "surrogateescape")
        values[path] = (mode.decode("ascii"), oid.decode("ascii"))
    return values


def blob(repo: Path, oid: str) -> bytes:
    return git(repo, "cat-file", "blob", oid).stdout


def json_blob(repo: Path, commit: str, entries: dict[str, tuple[str, str]], path: str, label: str) -> tuple[dict[str, Any], str]:
    entry = entries.get(path)
    if entry is None or entry[0] != "100644":
        raise AuthorityGateError(f"{label} is missing or not a regular 100644 file at {commit}")
    try:
        value = json.loads(blob(repo, entry[1]).decode("utf8"))
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise AuthorityGateError(f"{label} is not valid UTF-8 JSON at {commit}: {exc}") from exc
    if not isinstance(value, dict):
        raise AuthorityGateError(f"{label} must contain a JSON object at {commit}")
    return value, entry[1]


def canonical_lock_entry(content: dict[str, Any], library: dict[str, Any]) -> dict[str, Any]:
    return {
        "id": content["id"],
        "library_id": content["library_id"],
        "source_repository": library["source_repository"],
        "source_commit": library["source_commit"],
        "canonical_path": content["canonical_path"],
        "source_path": content["source_path"],
        "source_git_blob_sha": content["source_git_blob_sha"],
        "content_sha256": content["content_sha256"],
        "bytes": content["bytes"],
        "mode": content["mode"],
    }


def validate_snapshot(repo: Path, commit: str) -> tuple[dict[str, tuple[str, str]], set[str]]:
    entries = tree(repo, commit)
    index, _ = json_blob(repo, commit, entries, INDEX_PATH, "source index")
    lock, _ = json_blob(repo, commit, entries, LOCK_PATH, "source lock")
    if index.get("schema_version") != 1 or index.get("lock_path") != LOCK_PATH:
        raise AuthorityGateError(f"source index schema or fixed lock path is invalid at {commit}")
    libraries = index.get("libraries")
    contents = index.get("contents")
    bindings = index.get("bindings")
    views = index.get("views")
    if not all(isinstance(value, list) for value in (libraries, contents, bindings, views)):
        raise AuthorityGateError(f"source index arrays are invalid at {commit}")
    library_by_id: dict[str, dict[str, Any]] = {}
    for library in libraries:
        if not isinstance(library, dict):
            raise AuthorityGateError(f"source library is invalid at {commit}")
        identifier = required_string(library.get("id"), "library.id")
        if identifier in library_by_id:
            raise AuthorityGateError(f"source index repeats library id {identifier}")
        if library.get("immutable") is not True or library.get("authority_kind") not in {"frozen_donor", "active_v3_contract"}:
            raise AuthorityGateError(f"source library authority is invalid: {identifier}")
        required_string(library.get("source_repository"), "library.source_repository")
        required_hex(library.get("source_commit"), 40, "library.source_commit")
        if not valid_path(library.get("root")):
            raise AuthorityGateError(f"library.root is invalid: {identifier}")
        library_by_id[identifier] = library

    content_by_id: dict[str, dict[str, Any]] = {}
    canonical_paths: set[str] = set()
    canonical_hashes: set[str] = set()
    for content in contents:
        if not isinstance(content, dict):
            raise AuthorityGateError(f"canonical content is invalid at {commit}")
        identifier = required_string(content.get("id"), "content.id")
        library = library_by_id.get(content.get("library_id"))
        if identifier in content_by_id or library is None:
            raise AuthorityGateError(f"canonical content identity is invalid: {identifier}")
        canonical_path = content.get("canonical_path")
        source_path = content.get("source_path")
        if not valid_path(canonical_path) or not valid_path(source_path) or not canonical_path.startswith(library["root"] + "/"):
            raise AuthorityGateError(f"canonical content path is invalid: {identifier}")
        content_sha = required_hex(content.get("content_sha256"), 64, "content.content_sha256")
        blob_sha = required_hex(content.get("source_git_blob_sha"), 40, "content.source_git_blob_sha")
        if not isinstance(content.get("bytes"), int) or content["bytes"] < 0 or content.get("mode") != "100644":
            raise AuthorityGateError(f"canonical content size or mode is invalid: {identifier}")
        if canonical_path in canonical_paths or content_sha in canonical_hashes:
            raise AuthorityGateError(f"canonical content duplicates a path or payload: {identifier}")
        entry = entries.get(canonical_path)
        if entry is None or entry[0] != "100644" or entry[1] != blob_sha:
            raise AuthorityGateError(f"canonical Git blob identity differs from source index: {canonical_path}")
        data = blob(repo, entry[1])
        if len(data) != content["bytes"] or hashlib.sha256(data).hexdigest() != content_sha:
            raise AuthorityGateError(f"canonical payload bytes differ from source index: {canonical_path}")
        canonical_paths.add(canonical_path)
        canonical_hashes.add(content_sha)
        content_by_id[identifier] = content

    for binding in bindings:
        if not isinstance(binding, dict) or binding.get("content_id") not in content_by_id:
            raise AuthorityGateError(f"source binding is invalid at {commit}")
        content = content_by_id[binding["content_id"]]
        library = library_by_id[content["library_id"]]
        if not valid_path(binding.get("logical_path")) or not valid_path(binding.get("source_path")):
            raise AuthorityGateError(f"source binding path is invalid at {commit}")
        if binding.get("source_repository") != library["source_repository"] or binding.get("source_commit") != library["source_commit"] or binding.get("source_path") != content["source_path"] or binding.get("source_git_blob_sha") != content["source_git_blob_sha"] or binding.get("mode") != "100644":
            raise AuthorityGateError(f"source binding identity differs from canonical source: {binding.get('logical_path')}")
    for view in views:
        if not isinstance(view, dict) or view.get("content_id") not in content_by_id or not valid_path(view.get("target_path")) or not isinstance(view.get("enabled"), bool):
            raise AuthorityGateError(f"source view is invalid at {commit}")

    expected_lock = sorted((canonical_lock_entry(content, library_by_id[content["library_id"]]) for content in content_by_id.values()), key=lambda item: item["id"])
    if lock.get("schema_version") != 1 or lock.get("entries") != expected_lock:
        raise AuthorityGateError(f"source lock differs from source index at {commit}")
    return entries, canonical_paths


def file_sha256(repo: Path, entries: dict[str, tuple[str, str]], path: str) -> str | None:
    entry = entries.get(path)
    if entry is None:
        return None
    return hashlib.sha256(blob(repo, entry[1])).hexdigest()


def authority_changes(repo: Path, base: str, head: str) -> list[dict[str, str | None]]:
    base_entries, base_paths = validate_snapshot(repo, base)
    head_entries, head_paths = validate_snapshot(repo, head)
    paths = sorted({INDEX_PATH, LOCK_PATH, *base_paths, *head_paths})
    changes = []
    for path in paths:
        before, after = file_sha256(repo, base_entries, path), file_sha256(repo, head_entries, path)
        if before != after:
            changes.append({"path": path, "base_sha256": before, "head_sha256": after})
    return changes


def load_approvals(repo: Path, head: str, entries: dict[str, tuple[str, str]]) -> list[dict[str, Any]]:
    raw, _ = json_blob(repo, head, entries, APPROVALS_PATH, "P5 authority approval ledger")
    if raw.get("schema_version") != 1 or not isinstance(raw.get("approvals"), list):
        raise AuthorityGateError("P5 authority approval ledger must contain schema_version=1 and approvals array")
    values: list[dict[str, Any]] = []
    for approval in raw["approvals"]:
        if not isinstance(approval, dict) or set(approval) != {"base_commit", "changes", "reason", "review_reference"}:
            raise AuthorityGateError("each P5 authority approval must contain base_commit, changes, reason and review_reference")
        required_hex(approval["base_commit"], 40, "approval.base_commit")
        required_string(approval["reason"], "approval.reason")
        required_string(approval["review_reference"], "approval.review_reference")
        if not isinstance(approval["changes"], list) or not approval["changes"]:
            raise AuthorityGateError("each P5 authority approval must list one or more exact changes")
        normalized = []
        seen: set[str] = set()
        for change in approval["changes"]:
            if not isinstance(change, dict) or set(change) != {"path", "base_sha256", "head_sha256"} or not valid_path(change.get("path")):
                raise AuthorityGateError("each P5 authority approval change must have exact path/base_sha256/head_sha256")
            if change["path"] in seen:
                raise AuthorityGateError("P5 authority approval repeats a path")
            seen.add(change["path"])
            for key in ("base_sha256", "head_sha256"):
                value = change[key]
                if value is not None:
                    required_hex(value, 64, f"approval.{key}")
            normalized.append({"path": change["path"], "base_sha256": change["base_sha256"], "head_sha256": change["head_sha256"]})
        values.append({**approval, "changes": sorted(normalized, key=lambda item: item["path"])})
    return values


def check(repo: Path, base_ref: str, head_ref: str) -> dict[str, Any]:
    repo = repo.resolve()
    base, head = resolve_commit(repo, base_ref), resolve_commit(repo, head_ref)
    require_ancestor(repo, base, head)
    changes = authority_changes(repo, base, head)
    head_entries, _ = validate_snapshot(repo, head)
    approvals = load_approvals(repo, head, head_entries)
    if not changes:
        return {"schema_version": 1, "base_commit": base, "head_commit": head, "authority_changes": [], "approval": None, "status": "pass"}
    matches = [approval for approval in approvals if approval["base_commit"] == base]
    if len(matches) != 1:
        raise AuthorityGateError("authority changes require exactly one approval bound to this base commit")
    approval = matches[0]
    if approval["changes"] != changes:
        raise AuthorityGateError("authority approval does not exactly match source-index, source-lock and canonical payload SHA-256 changes")
    return {"schema_version": 1, "base_commit": base, "head_commit": head, "authority_changes": changes, "approval": {"reason": approval["reason"], "review_reference": approval["review_reference"]}, "status": "pass"}


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("repo", type=Path)
    parser.add_argument("--base", required=True)
    parser.add_argument("--head", required=True)
    args = parser.parse_args()
    try:
        print(json.dumps(check(args.repo, args.base, args.head), ensure_ascii=True, sort_keys=True))
    except (AuthorityGateError, OSError, ValueError, subprocess.SubprocessError) as exc:
        print(f"source authority gate failed: {exc}", file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
