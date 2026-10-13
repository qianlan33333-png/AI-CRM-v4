"""Mechanical V4 preparation. No business source or test assertion is edited."""
from __future__ import annotations

import hashlib
import json
from pathlib import Path, PurePosixPath
import re
import subprocess
import sys
import tempfile

INDEX = "web/donor-sources/source-index.json"
LOCK = "web/donor-sources/source-lock.json"
APPROVALS = "docs/engineering/dedup/p5-authority-change-approvals.json"
V4 = "https://github.com/qianlan33333-png/AI-CRM-v4.git"


def git(root: Path, *args: str) -> bytes:
    result = subprocess.run(["git", "-C", str(root), *args], capture_output=True)
    if result.returncode:
        raise ValueError("git_" + args[0].replace("-", "_") + "_failed")
    return result.stdout


def safe_file(root: Path, relative: str) -> Path:
    value = PurePosixPath(relative)
    if value.is_absolute() or value.as_posix() != relative or ".." in value.parts:
        raise ValueError("preparation_path_escape")
    path = root / relative
    if path.is_symlink() or not path.is_file() or root.resolve() not in path.resolve().parents:
        raise ValueError("preparation_file_unsafe:" + relative)
    return path


def write_json(path: Path, value: dict) -> None:
    raw = (json.dumps(value, ensure_ascii=False, indent=2) + "\n").encode()
    if path.read_bytes() != raw:
        path.write_bytes(raw)


def synchronize_sources(root: Path, base: str | None, authorization: str | None) -> list[str]:
    """Bind only active V4 content already committed; frozen donors stay frozen."""
    index = json.loads(safe_file(root, INDEX).read_text())
    libraries = {item["id"]: item for item in index["libraries"]}
    originals = {p: (root / p).read_bytes() for p in (INDEX, LOCK)}
    updates = []
    head = git(root, "rev-parse", "HEAD").decode().strip()
    for content in index["contents"]:
        library = libraries[content["library_id"]]
        if library.get("authority_kind") != "active_v3_contract":
            continue
        if library.get("source_repository") != V4:
            raise ValueError("active_source_not_v4")
        source = safe_file(root, content["canonical_path"])
        raw = source.read_bytes()
        digest = hashlib.sha256(raw).hexdigest()
        if content["content_sha256"] == digest and content["bytes"] == len(raw):
            continue
        # A worktree edit is not a committed source identity. Commit business
        # changes first; this command prepares the ensuing metadata commit.
        if git(root, "show", head + ":" + content["canonical_path"]) != raw:
            raise ValueError("canonical_source_not_committed:" + content["canonical_path"])
        if not base or not authorization:
            raise ValueError("source_binding_requires_existing_scope_reference_and_base")
        blob = git(root, "rev-parse", head + ":" + content["canonical_path"]).decode().strip()
        content.update(source_git_blob_sha=blob, content_sha256=digest, bytes=len(raw))
        library["source_commit"] = head
        updates.append(content["canonical_path"])
    if not updates:
        return []
    # Multiple contents can share a library; all bindings/lock rows derive
    # from the same authoritative objects, never from pasted old SHA values.
    contents = {item["id"]: item for item in index["contents"]}
    for binding in index["bindings"]:
        content = contents[binding["content_id"]]
        library = libraries[content["library_id"]]
        if library.get("source_repository") == V4:
            binding.update(source_commit=library["source_commit"],
                           source_git_blob_sha=content["source_git_blob_sha"])
    entries = []
    for content in index["contents"]:
        library = libraries[content["library_id"]]
        entries.append({**{key: content[key] for key in (
            "id", "library_id", "canonical_path", "source_path", "source_git_blob_sha",
            "content_sha256", "bytes", "mode")},
            "source_repository": library["source_repository"], "source_commit": library["source_commit"]})
    lock = {"schema_version": 1, "entries": sorted(entries, key=lambda item: item["id"])}
    # Validate the before snapshot before writing anything. The caller's
    # human scope reference is evidence, not a new grant of authority.
    sys.path.insert(0, str(root / "scripts/audit"))
    import check_source_authority_changes as authority
    base = authority.resolve_commit(root, base)
    authority.require_ancestor(root, base, head)
    before_entries, before_paths = authority.validate_snapshot(root, base)
    paths = sorted({INDEX, LOCK, *before_paths, *(c["canonical_path"] for c in index["contents"])})
    payloads = {INDEX: (json.dumps(index, ensure_ascii=False, indent=2) + "\n").encode(),
                LOCK: (json.dumps(lock, ensure_ascii=False, indent=2) + "\n").encode()}
    changes = []
    for relative in paths:
        before = authority.file_sha256(root, before_entries, relative)
        after = hashlib.sha256(payloads.get(relative, safe_file(root, relative).read_bytes())).hexdigest()
        if before != after:
            changes.append({"path": relative, "base_sha256": before, "head_sha256": after})
    ledger_path = safe_file(root, APPROVALS)
    ledger = json.loads(ledger_path.read_text())
    matching = [r for r in ledger["approvals"] if r["base_commit"] == base]
    row = {"base_commit": base, "changes": changes,
           "reason": "Mechanical binding of the already authorized current V4 contract",
           "review_reference": authorization}
    if matching and matching != [row]:
        raise ValueError("existing_authority_reference_differs")
    if not matching:
        ledger["approvals"].append(row)
    try:
        write_json(root / INDEX, index)
        write_json(root / LOCK, lock)
        write_json(ledger_path, ledger)
    except BaseException:
        for relative, raw in originals.items():
            (root / relative).write_bytes(raw)
        raise
    return [INDEX, LOCK, APPROVALS]


def prepare(root: Path, *, base: str | None = None, authorization: str | None = None,
            verify: bool = False, generate_catalog: bool = True) -> dict:
    root = root.resolve()
    before = git(root, "status", "--porcelain=v1").decode().splitlines()
    if verify and before:
        raise ValueError("prepare_verify_requires_clean_committed_source")
    head = git(root, "rev-parse", "HEAD").decode().strip()
    tree = git(root, "rev-parse", "HEAD^{tree}").decode().strip()
    changed = [] if verify else synchronize_sources(root, base, authorization)
    for command in (["node", "scripts/prepare-donor-source-views.mjs"],
                    *([["go", "run", "./cmd/generate-cli-catalog"]] if generate_catalog else [])):
        result = subprocess.run(command, cwd=root, capture_output=True)
        if result.returncode:
            raise ValueError("preparation_command_failed:" + command[-1])
    # Additional binaries must be declared by the existing release builder.
    sys.path.insert(0, str(root / "scripts"))
    import domestic_release_build as builder
    commands = {c.package for c in builder._release_commands(root, head)}
    manifest = json.loads((root / "docs/release-checks.json").read_text())
    if not verify and "./cmd/aicrm-cli" in commands and any(
            "internal/clicatalog/port/catalog.json" in line for line in
            git(root, "status", "--porcelain=v1").decode().splitlines()):
        declared = manifest.setdefault("build_commands", [])
        if "./cmd/aicrm-cli" not in declared:
            declared.append("./cmd/aicrm-cli")
            write_json(root / "docs/release-checks.json", manifest)
            changed.append("docs/release-checks.json")
    required = set(manifest.get("build_commands", []))
    if not required <= commands:
        raise ValueError("unregistered_release_program:" + ",".join(sorted(required - commands)))
    after = git(root, "status", "--porcelain=v1").decode().splitlines()
    if verify and after:
        raise ValueError("generated_outputs_not_committed")
    return {"status": "prepared", "source_sha": head, "source_tree": tree,
            "mechanical_changes": changed, "working_tree": after,
            "verification": "clean_exact_checkout" if verify else "commit_changes_then_verify",
            "declared_build_commands": sorted(required), "tests_claimed": False}


def handoff(root: Path, base: str, ref: str, task: str, authorization: str,
            receipts: list[Path], impact: Path, unverified: list[str]) -> dict:
    if git(root, "status", "--porcelain=v1").strip():
        raise ValueError("handoff_requires_clean_committed_source")
    head = git(root, "rev-parse", "HEAD").decode().strip()
    base = git(root, "rev-parse", base + "^{commit}").decode().strip()
    if not re.fullmatch(r"refs/heads/codex/[A-Za-z0-9._/-]+", ref):
        raise ValueError("handoff_ref_invalid")
    remote = git(root, "ls-remote", "domestic", ref).decode().split()
    if len(remote) != 2 or remote != [head, ref]:
        raise ValueError("domestic_candidate_not_pushed_or_ref_changed")
    values = []
    for path in receipts:
        raw = path.read_bytes(); row = json.loads(raw)
        source = row.get("source", {})
        actual = row.get("head_sha", row.get("source_sha", row.get("head", source.get("start", {}).get("head"))))
        if actual != head or row.get("result", row.get("status")) != "passed":
            raise ValueError("handoff_test_receipt_not_exact_pass")
        if source and source.get("unchanged_during_execution") is not True:
            raise ValueError("handoff_test_source_changed")
        steps = row.get("steps")
        if not isinstance(steps, list) or not steps:
            raise ValueError("handoff_receipt_has_no_executed_checks")
        for step in steps:
            if (not isinstance(step, dict) or step.get("exit_code") != 0
                    or not isinstance(step.get("command"), list) or not step["command"]
                    or not Path(step.get("log", "")).is_file()):
                raise ValueError("handoff_check_log_missing_or_unsuccessful")
            if "-json" in step["command"] and "-run" in step["command"]:
                from dev_preflight import verify_journey_results
                pattern = step["command"][step["command"].index("-run") + 1]
                expected = re.findall(r"Test[A-Za-z0-9_]+", pattern)
                if not expected:
                    raise ValueError("handoff_named_test_selection_empty")
                verify_journey_results(Path(step["log"]), expected)
        values.append({"path": str(path.resolve()), "sha256": hashlib.sha256(raw).hexdigest(), "source_sha": head})
    impacts = json.loads(impact.read_text())
    if set(impacts) != {"contract", "mechanism", "consumers", "pages", "evidence"}:
        raise ValueError("handoff_requires_five_impacts")
    route = Path("/Users/qianlan/Downloads/新CRM/release-control/workstation.json")
    thread = json.loads(route.read_text())["thread_id"]
    return {"schema_version": 1, "origin_task_id": task, "workstation_thread_id": thread,
            "base": base, "head": head, "tree": git(root, "rev-parse", "HEAD^{tree}").decode().strip(),
            "ref": ref, "impact": impacts, "test_receipts": values,
            "unverified": unverified, "authorization_reference": authorization,
            "delivery": "send this exact payload with send_message_to_thread after rereading route"}
