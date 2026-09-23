#!/usr/bin/env python3
"""Conservative, manually invoked GC for staging build directories."""
from __future__ import annotations

import argparse
import fcntl
import hashlib
import json
import os
import re
import shutil
import stat
import sys
import time
import urllib.request
import urllib.error
import uuid
from pathlib import Path

SHA = re.compile(r"[0-9a-f]{40}\Z")
HEX = re.compile(r"[0-9a-f]{64}\Z")
TERMINAL = {"released", "stale_candidate"}


class Refuse(RuntimeError):
    pass


def digest(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as source:
        for block in iter(lambda: source.read(1024 * 1024), b""):
            h.update(block)
    return h.hexdigest()


def read_json(path: Path) -> dict:
    if path.is_symlink() or not path.is_file():
        raise Refuse(f"missing_or_unsafe_input:{path.name}")
    value = json.loads(path.read_text())
    if not isinstance(value, dict) or not isinstance(value.get("items"), list):
        raise Refuse(f"invalid_input:{path.name}")
    return value


def item_for(items: list, sha: str) -> tuple[dict | None, str]:
    matches = [i for i in items if isinstance(i, dict) and i.get("merge_preview_sha") == sha]
    if len(matches) != 1:
        return None, "missing_or_ambiguous_registration"
    item = matches[0]
    if item.get("status") not in TERMINAL:
        return None, "nonterminal_registration"
    if not isinstance(item.get("candidate_id"), str) or not item["candidate_id"]:
        return None, "invalid_candidate_id"
    events = item.get("events")
    if not isinstance(events, list) or not any(isinstance(e, dict) and e.get("to") == item["status"] for e in events):
        return None, "terminal_event_missing"
    return item, ""


def tree_fingerprint(path: Path) -> tuple[str, int]:
    """No symlink, mount, or hardlink may be traversed by later removal."""
    root = path.lstat()
    if not stat.S_ISDIR(root.st_mode) or path.is_symlink():
        raise Refuse("unsafe_build_directory")
    rows, size = [], 0
    for base, directories, files in os.walk(path, followlinks=False):
        for name in sorted(directories + files):
            member = Path(base) / name
            info = member.lstat()
            if info.st_dev != root.st_dev or info.st_nlink != 1 and not stat.S_ISDIR(info.st_mode):
                raise Refuse("unsafe_build_member")
            if not (stat.S_ISDIR(info.st_mode) or stat.S_ISREG(info.st_mode)):
                raise Refuse("unsafe_build_member")
            rows.append((str(member.relative_to(path)), info.st_mode, info.st_ino, info.st_size, info.st_mtime_ns))
            if stat.S_ISREG(info.st_mode):
                size += info.st_size
    return hashlib.sha256(json.dumps(rows, separators=(",", ":")).encode()).hexdigest(), size


def source_proof(directory: Path, sha: str, candidate_id: str) -> dict:
    manifest_path = directory / "candidate-manifest.json"
    receipt_path = directory / "staging-receipt.json"
    for path in (manifest_path, receipt_path):
        if path.is_symlink() or not path.is_file():
            raise Refuse("source_proof_missing")
    manifest, receipt = json.loads(manifest_path.read_text()), json.loads(receipt_path.read_text())
    for value in (manifest, receipt):
        if value.get("merge_preview_sha") != sha or value.get("candidate_id") != candidate_id or not SHA.fullmatch(str(value.get("tree_sha", ""))):
            raise Refuse("source_proof_mismatch")
    if manifest["tree_sha"] != receipt["tree_sha"] or receipt.get("status") not in {"built", "accepted"}:
        raise Refuse("source_proof_mismatch")
    package = receipt.get("package_name")
    package_sha = receipt.get("package_sha256")
    if package != f"aicrm-{sha}.tar.gz" or not HEX.fullmatch(str(package_sha)):
        raise Refuse("source_package_invalid")
    archive = directory / package
    if archive.is_symlink() or not archive.is_file() or digest(archive) != package_sha:
        raise Refuse("source_package_mismatch")
    git_head = directory / ".git" / "HEAD"
    if git_head.is_symlink() or not git_head.is_file() or git_head.read_text().strip() != sha:
        raise Refuse("source_git_head_mismatch")
    return {"manifest_sha256": digest(manifest_path), "receipt_sha256": digest(receipt_path), "package_sha256": package_sha, "tree_sha": manifest["tree_sha"]}


def references(root: Path, state: dict, queue: dict) -> set[str]:
    protected = set()
    current = root / "current"
    if not current.is_symlink():
        raise Refuse("current_release_unavailable")
    active = current.resolve(strict=True)
    if active.parent != (root / "releases").resolve() or not SHA.fullmatch(active.name):
        raise Refuse("current_release_unmanaged")
    protected.add(active.name)
    releases = root / "releases"
    if not releases.is_dir() or releases.is_symlink():
        raise Refuse("installed_releases_unavailable")
    for entry in releases.iterdir():
        if SHA.fullmatch(entry.name):
            protected.add(entry.name)
    for control in (state, queue):
        for item in control["items"]:
            if not isinstance(item, dict):
                raise Refuse("invalid_control_item")
            if item.get("status") not in TERMINAL:
                for key in ("merge_preview_sha", "commit_sha", "release_sha"):
                    value = item.get(key)
                    if isinstance(value, str) and SHA.fullmatch(value):
                        protected.add(value)
    return protected


def inventory(root: Path, state_path: Path, queue_path: Path) -> dict:
    builds = root / "builds"
    if not builds.is_dir() or builds.is_symlink():
        raise Refuse("build_root_unavailable")
    state, queue = read_json(state_path), read_json(queue_path)
    protected_shas = references(root, state, queue)
    eligible, protected = [], []
    for directory in sorted(builds.iterdir()):
        sha = directory.name
        reason = None
        if not SHA.fullmatch(sha):
            reason = "unregistered_or_unknown_directory"
        elif sha in protected_shas:
            reason = "current_installed_or_live_reference"
        else:
            state_item, state_reason = item_for(state["items"], sha)
            queue_item, queue_reason = item_for(queue["items"], sha)
            if state_reason or queue_reason:
                reason = "state:" + state_reason if state_reason else "queue:" + queue_reason
            elif state_item["candidate_id"] != queue_item["candidate_id"] or state_item["status"] != queue_item["status"]:
                reason = "registration_conflict"
            else:
                try:
                    proof = source_proof(directory, sha, state_item["candidate_id"])
                    fingerprint, size = tree_fingerprint(directory)
                    eligible.append({"sha": sha, "candidate_id": state_item["candidate_id"], "status": state_item["status"], "bytes": size, "fingerprint": fingerprint, "proof": proof})
                except (OSError, ValueError, Refuse) as failure:
                    reason = str(failure) if isinstance(failure, Refuse) else "source_proof_invalid"
        if reason:
            protected.append({"name": sha, "reason": reason})
    return {"schema": 1, "mode": "inventory", "root": str(root), "inputs": {"state_sha256": digest(state_path), "queue_sha256": digest(queue_path)}, "eligible": eligible, "protected": protected}


def readyz(url: str) -> dict:
    try:
        with urllib.request.urlopen(url, timeout=5) as response:
            body = response.read(65536)
            if response.status != 200:
                raise Refuse("readyz_failed")
        value = json.loads(body)
        if not isinstance(value, dict):
            raise ValueError("not an object")
        release_sha = value.get("release_sha")
        if not isinstance(release_sha, str) or not SHA.fullmatch(release_sha):
            raise ValueError("release SHA unavailable")
        return {"status": 200, "release_sha": release_sha, "sha256": hashlib.sha256(body).hexdigest()}
    except (OSError, ValueError, urllib.error.URLError) as failure:
        raise Refuse("readyz_failed") from failure


def atomic_json(path: Path, value: dict) -> None:
    temporary = path.with_name(path.name + "." + uuid.uuid4().hex + ".tmp")
    try:
        with temporary.open("x") as output:
            json.dump(value, output, indent=2, sort_keys=True)
            output.write("\n")
            output.flush()
            os.fsync(output.fileno())
        os.replace(temporary, path)
        fd = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(fd)
        finally:
            os.close(fd)
    finally:
        temporary.unlink(missing_ok=True)


def run(args) -> dict:
    root = args.root.resolve()
    if not all(path.is_absolute() for path in (args.root, args.state, args.queue, args.audit)):
        raise Refuse("absolute_paths_required")
    if args.audit == root / "builds" or root / "builds" in args.audit.parents:
        raise Refuse("audit_must_survive_build_cleanup")
    lock = root / "staging-build.lock"
    with lock.open("rb") as build_lock, args.queue.with_suffix(".lock").open("rb") as queue_lock:
        lock_mode = fcntl.LOCK_SH if args.mode == "inventory" else fcntl.LOCK_EX
        fcntl.flock(build_lock, lock_mode)
        fcntl.flock(queue_lock, lock_mode)
        fresh = inventory(root, args.state, args.queue)
        if args.mode == "inventory":
            return fresh
        if args.plan is None or args.plan.is_symlink():
            raise Refuse("apply_requires_plan")
        plan = json.loads(args.plan.read_text())
        if plan != fresh:
            raise Refuse("plan_stale")
        args.audit.mkdir(mode=0o700, parents=True, exist_ok=True)
        before = os.statvfs(root)
        health_before = readyz(args.readyz)
        if health_before["release_sha"] != (root / "current").resolve(strict=True).name:
            raise Refuse("readyz_current_mismatch")
        receipt = {"schema": 1, "plan": fresh, "started_at": int(time.time()), "disk_before_available": before.f_bavail * before.f_frsize, "readyz_before": health_before, "deleted": [], "status": "in_progress"}
        output = args.audit / ("staging-build-gc-" + uuid.uuid4().hex + ".json")
        atomic_json(output, receipt)
        try:
            for group in fresh["eligible"]:
                directory = root / "builds" / group["sha"]
                if tree_fingerprint(directory)[0] != group["fingerprint"]:
                    raise Refuse("build_changed_before_delete")
                shutil.rmtree(directory)
                receipt["deleted"].append(group["sha"])
                atomic_json(output, receipt)
            after = os.statvfs(root)
            health_after = readyz(args.readyz)
            if health_after["release_sha"] != health_before["release_sha"]:
                raise Refuse("readyz_release_changed")
            receipt.update(status="completed", disk_after_available=after.f_bavail * after.f_frsize, readyz_after=health_after, completed_at=int(time.time()))
        except Exception as failure:
            receipt.update(status="failed", error=str(failure), completed_at=int(time.time()))
            atomic_json(output, receipt)
            raise
        atomic_json(output, receipt)
        return {"schema": 1, "mode": "apply", "audit": str(output), "deleted": receipt["deleted"], "disk_before_available": receipt["disk_before_available"], "disk_after_available": receipt["disk_after_available"], "readyz_before": health_before, "readyz_after": health_after}


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", nargs="?", choices=("inventory", "apply"), default="inventory")
    parser.add_argument("--root", type=Path, default=Path("/opt/aicrm"))
    parser.add_argument("--state", type=Path, required=True, help="verified copy of authoritative release-control state")
    parser.add_argument("--queue", type=Path, default=Path("/opt/aicrm/release-queue.json"))
    parser.add_argument("--audit", type=Path, default=Path("/opt/aicrm/build-gc-audit"))
    parser.add_argument("--plan", type=Path)
    parser.add_argument("--readyz", default="http://127.0.0.1:8080/readyz")
    args = parser.parse_args()
    try:
        print(json.dumps(run(args), sort_keys=True))
        return 0
    except (OSError, ValueError, Refuse) as failure:
        print(json.dumps({"ok": False, "reason": str(failure) if isinstance(failure, Refuse) else "gc_io_or_format_failed"}), file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
