#!/usr/bin/env python3
"""Bounded cleanup of registered CRM-only process files; never global /tmp.

These roots are disposable execution artifacts, not business exports or Media
originals. Unknown roots, symlinks, hardlinks, mounts, foreign owners, live file
descriptors, and files with backup/secret/business names are not deletable.
Output contains aggregate counts only, never file names or contents.
"""
from __future__ import annotations

import argparse
import fcntl
import json
import os
from pathlib import Path
import pwd
import stat
import sys
import time

ROOTS = (Path("/var/lib/aicrm/process-tmp"), Path("/var/lib/aicrm/process-diagnostics"))
MARKER = ".aicrm-disposable-v1"
MARKER_CONTENT = "aicrm-process-artifacts-v1\n"
AGE_SECONDS = 30 * 24 * 60 * 60
PROTECTED = {"backup", "backups", "secrets", "secret", "business", "uploads", "exports", "pgdata", ".git", "data", ".env"}
SUFFIXES = {".pem", ".key", ".p12", ".pfx", ".dump", ".backup", ".sql"}


class Refuse(RuntimeError):
    pass


def active_files(proc=Path("/proc")):
    """Fail closed if the root operator cannot inspect a still-running process."""
    if not proc.is_dir():
        raise Refuse("process_inventory_unavailable")
    result = set()
    for pid in proc.iterdir():
        if not pid.name.isdigit():
            continue
        try:
            for descriptor in (pid / "fd").iterdir():
                try:
                    info = descriptor.stat()
                    result.add((info.st_dev, info.st_ino))
                except FileNotFoundError:
                    pass
            # An mmap can remain live after its original descriptor is closed.
            # Device/inode fields suffice; never retain or emit mapped paths.
            with (pid / "maps").open() as mappings:
                for line in mappings:
                    fields = line.split(maxsplit=5)
                    if len(fields) < 5:
                        raise Refuse("process_inventory_incomplete")
                    major, minor = fields[3].split(":")
                    inode = int(fields[4])
                    if inode:
                        result.add((os.makedev(int(major, 16), int(minor, 16)), inode))
        except FileNotFoundError:
            if pid.exists():
                raise Refuse("process_inventory_incomplete") from None
        except (OSError, ValueError):
            if pid.exists():
                raise Refuse("process_inventory_incomplete") from None
    return result


def safe_name(name):
    return name != MARKER and name.lower() not in PROTECTED and Path(name).suffix.lower() not in SUFFIXES


def cleanup(roots, owner, before, limit, apply, active, refresh_active=None):
    if before > time.time() - AGE_SECONDS or not 1 <= limit <= 1000:
        raise Refuse("invalid_retention_boundary_or_batch")
    report = {"mode": "apply" if apply else "inventory", "retention_days": 30,
              "candidates": 0, "deleted": 0, "bytes": 0, "protected": 0,
              "remaining": False, "uninitialized_roots": 0}
    for root in roots:
        if not root.exists() and not root.is_symlink():
            report["uninitialized_roots"] += 1
            continue
        root_info = root.lstat()
        if root.resolve() != root or not stat.S_ISDIR(root_info.st_mode) or root_info.st_uid != owner or root_info.st_mode & 0o022:
            raise Refuse("invalid_registered_process_directory")
        marker = root / MARKER
        marker_info = marker.lstat()
        if not stat.S_ISREG(marker_info.st_mode) or marker_info.st_nlink != 1 or marker.read_text() != MARKER_CONTENT:
            raise Refuse("unclassified_process_directory")
        # fwalk pins directory descriptors; never follow symlinks while deleting.
        for directory, dirs, files, descriptor in os.fwalk(root, follow_symlinks=False):
            info = os.fstat(descriptor)
            if info.st_dev != root_info.st_dev or info.st_uid != owner or info.st_mode & 0o022:
                dirs[:] = []
                report["protected"] += len(files)
                continue
            allowed = []
            for name in dirs:
                child = os.stat(name, dir_fd=descriptor, follow_symlinks=False)
                if safe_name(name) and stat.S_ISDIR(child.st_mode) and child.st_dev == root_info.st_dev and child.st_uid == owner:
                    allowed.append(name)
                else:
                    report["protected"] += 1
            dirs[:] = allowed
            for name in sorted(files):
                try:
                    previous = os.stat(name, dir_fd=descriptor, follow_symlinks=False)
                except FileNotFoundError:
                    continue
                if (not safe_name(name) or not stat.S_ISREG(previous.st_mode) or previous.st_nlink != 1
                        or previous.st_dev != root_info.st_dev or previous.st_uid != owner
                        or (previous.st_dev, previous.st_ino) in active):
                    report["protected"] += 1
                    continue
                # A recently written or replaced execution artifact is still live.
                if max(previous.st_mtime, previous.st_ctime) >= before:
                    continue
                if report["candidates"] == limit:
                    report["remaining"] = True
                    return report
                report["candidates"] += 1
                if apply:
                    # Refresh immediately before each unlink, not just before
                    # the traversal. External actors opening after this check
                    # cannot be serialized without their cooperation.
                    if refresh_active is not None and (previous.st_dev, previous.st_ino) in refresh_active():
                        report["protected"] += 1
                        continue
                    current = os.stat(name, dir_fd=descriptor, follow_symlinks=False)
                    if (current.st_ino, current.st_size, current.st_mtime_ns, current.st_ctime_ns, current.st_nlink) != (previous.st_ino, previous.st_size, previous.st_mtime_ns, previous.st_ctime_ns, previous.st_nlink):
                        raise Refuse("process_file_changed_during_cleanup")
                    os.unlink(name, dir_fd=descriptor)
                    report["deleted"] += 1
                report["bytes"] += previous.st_size
    return report


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("inventory", "apply"), nargs="?", default="inventory")
    parser.add_argument("--limit", type=int, default=1000)
    args = parser.parse_args()
    try:
        if sys.platform != "linux" or os.geteuid() != 0:
            raise Refuse("linux_root_required_for_complete_reference_inventory")
        descriptor = os.open("/run/lock/aicrm-runtime-retention.lock", os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
        try:
            lock_info = os.fstat(descriptor)
            if not stat.S_ISREG(lock_info.st_mode) or lock_info.st_uid != 0 or lock_info.st_nlink != 1 or lock_info.st_mode & 0o022:
                raise Refuse("invalid_retention_lock")
            fcntl.flock(descriptor, fcntl.LOCK_EX | fcntl.LOCK_NB)
            result = cleanup(ROOTS, pwd.getpwnam("aicrm").pw_uid, time.time() - AGE_SECONDS, args.limit, args.mode == "apply", active_files(), refresh_active=active_files)
        finally:
            os.close(descriptor)
        print(json.dumps(result, sort_keys=True))
        return 0
    except (OSError, KeyError, Refuse) as exc:
        reason = str(exc) if isinstance(exc, Refuse) else "runtime_retention_inventory_or_io_failed"
        print(json.dumps({"ok": False, "reason": reason}), file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
