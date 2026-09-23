#!/usr/bin/env python3
"""Fixed root helper called only by the singleton systemd oneshot.

River owns scheduling/retries. This file is a last-execution observation, not
a second queue. It contains aggregates and stable error codes only.
"""
import datetime as dt
import json
import os
from pathlib import Path
import stat
import subprocess
import tempfile

RESULT = Path("/var/lib/aicrm-maintenance/runtime-retention.json")
FILE_COMMAND = ["/usr/bin/python3", "-I", "/usr/local/libexec/aicrm-retention/cleanup-runtime-files.py", "apply", "--limit", "1000"]
JOURNAL_COMMAND = ["/usr/bin/journalctl", "--namespace=aicrm", "--rotate", "--vacuum-time=30d"]
SPACE_RESOURCES = {"process_storage": Path("/var/lib/aicrm/process-tmp"),
                   "journal_persistent_storage": Path("/var/log/journal"),
                   "journal_runtime_storage": Path("/run/log/journal")}


def sample_space(resources):
    """Host filesystem observations, not bytes attributable to this cleanup."""
    result = {}
    for name, path in resources.items():
        try:
            info = path.stat()
            usage = os.statvfs(path)
            result[name] = (info.st_dev, usage.f_bavail * usage.f_frsize)
        except OSError:
            result[name] = None
    return result


def space_observations(before, after):
    result = []
    for name, first in before.items():
        last = after.get(name)
        valid = first is not None and last is not None and first[0] == last[0]
        result.append({"resource": name, "state": "sampled" if valid else "unavailable",
                       "available_bytes_before": first[1] if first else None,
                       "available_bytes_after": last[1] if last else None,
                       "available_bytes_net_change": last[1] - first[1] if valid else None})
    return result


def now():
    return dt.datetime.now(dt.timezone.utc).isoformat()


def write_result(path, value):
    parent = path.parent
    info = parent.lstat()
    if not stat.S_ISDIR(info.st_mode) or parent.resolve() != parent or info.st_uid != 0 or info.st_mode & 0o022:
        raise ValueError("unsafe_host_maintenance_result_directory")
    if path.is_symlink():
        raise ValueError("unsafe_host_maintenance_result_file")
    with tempfile.NamedTemporaryFile(mode="w", dir=parent, delete=False) as stream:
        pending = Path(stream.name)
        try:
            json.dump(value, stream, sort_keys=True)
            stream.write("\n")
            stream.flush()
            os.fsync(stream.fileno())
            os.chmod(pending, 0o644)
            os.replace(pending, path)
        finally:
            pending.unlink(missing_ok=True)


def run(result_path=RESULT):
    report = {"version": 1, "started_at": now(), "finished_at": None, "state": "running",
              "runtime": None, "journal": "not_attempted", "failure_codes": [], "space_observations": []}
    space_before = sample_space(SPACE_RESOURCES)
    # A kill or host restart leaves explicit running evidence; readers reject it.
    write_result(result_path, report)
    try:
        result = subprocess.run(FILE_COMMAND, capture_output=True, text=True, timeout=60, check=False)
        value = json.loads(result.stdout) if result.returncode == 0 else None
        keys = ("candidates", "deleted", "bytes", "protected", "uninitialized_roots")
        if type(value) is not dict or any(type(value.get(key)) is not int or value[key] < 0 for key in keys) or type(value.get("remaining")) is not bool or not value["deleted"] <= value["candidates"] <= 1000:
            raise ValueError("runtime_cleanup_failed")
        report["runtime"] = {key: value[key] for key in (*keys, "remaining")}
        if value["uninitialized_roots"]:
            report["failure_codes"].append("runtime_roots_uninitialized")
    except (OSError, ValueError, subprocess.TimeoutExpired):
        report["failure_codes"].append("runtime_cleanup_failed")
    try:
        result = subprocess.run(JOURNAL_COMMAND, capture_output=True, timeout=20, check=False)
        report["journal"] = "completed" if result.returncode == 0 else "failed"
    except (OSError, subprocess.TimeoutExpired):
        report["journal"] = "failed"
    if report["journal"] != "completed":
        report["failure_codes"].append("journal_cleanup_failed")
    report["finished_at"] = now()
    report["space_observations"] = space_observations(space_before, sample_space(SPACE_RESOURCES))
    report["state"] = "completed" if not report["failure_codes"] else "partial_failed"
    write_result(result_path, report)
    print(json.dumps(report, sort_keys=True))
    return 0 if report["state"] == "completed" else 1


if __name__ == "__main__":
    if os.geteuid() != 0:
        raise SystemExit("root helper required")
    raise SystemExit(run())
