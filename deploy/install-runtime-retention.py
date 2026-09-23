#!/usr/bin/env python3
"""Install namespace-only CRM log/process-file policy; inventory by default.

No global journal vacuum, global tmp cleanup, timer or application restart.
The release operator restarts the listed application units during deployment.
The shared durable scheduler may invoke cleanup-runtime-files.py apply, and
journalctl --namespace=aicrm --rotate --vacuum-time=30d through host maintenance.
"""
import argparse
import json
import os
from pathlib import Path
import pwd
import re
import subprocess
import sys
import tempfile

UNITS = ("aicrm.service", "aicrm-effects-worker.service", "aicrm-wecom-worker.service",
         "aicrm-migrate.service", "aicrm-customer-sync-daily.service",
         "aicrm-hxc-dashboard-refresh.service", "aicrm-hxc-dashboard-rollout.service",
         "aicrm-automation-bootstrap.service")
ROOTS = (Path("/var/lib/aicrm/process-tmp"), Path("/var/lib/aicrm/process-diagnostics"))
MARKER = ".aicrm-disposable-v1"
MARKER_CONTENT = "aicrm-process-artifacts-v1\n"
DROPIN = """# CRM-only disposable process data; business files must use their Owner store.
[Service]
LogNamespace=aicrm
Environment=TMPDIR=/var/lib/aicrm/process-tmp
ReadWritePaths=/var/lib/aicrm/process-tmp /var/lib/aicrm/process-diagnostics
"""


def commands():
    return {"namespace": "aicrm", "journal_max_age_days": 30,
            "process_file_roots": [str(path) for path in ROOTS],
            "application_restart_required": list(UNITS),
            "runtime_cleanup": ["python3", "/opt/aicrm/current/deploy/cleanup-runtime-files.py", "apply", "--limit", "1000"],
            "journal_cleanup": ["journalctl", "--namespace=aicrm", "--rotate", "--vacuum-time=30d"]}


def atomic_write(path, content):
    path.parent.mkdir(parents=True, exist_ok=True)
    if path.parent.resolve() != path.parent or path.is_symlink():
        raise ValueError("configuration_symlink_refused")
    with tempfile.NamedTemporaryFile(mode="w", dir=path.parent, delete=False) as stream:
        pending = Path(stream.name)
        try:
            stream.write(content)
            stream.flush()
            os.fsync(stream.fileno())
            os.chmod(pending, 0o644)
            os.replace(pending, path)
        finally:
            pending.unlink(missing_ok=True)


def apply():
    if sys.platform != "linux" or os.geteuid() != 0:
        raise ValueError("linux_root_required")
    result = subprocess.run(["systemctl", "--version"], capture_output=True, text=True, check=True)
    match = re.match(r"systemd (\d+)", result.stdout)
    if not match or int(match.group(1)) < 245:
        raise ValueError("systemd_log_namespace_support_required")
    account = pwd.getpwnam("aicrm")
    # Do not adopt a pre-existing nonempty directory of unknown purpose.
    for root in ROOTS:
        if root.is_symlink() or (root.exists() and root.resolve() != root):
            raise ValueError("process_directory_symlink_refused")
        if root.exists() and any(root.iterdir()):
            marker = root / MARKER
            if marker.is_symlink() or not marker.is_file() or marker.read_text() != MARKER_CONTENT:
                raise ValueError("unclassified_existing_process_directory")
    for root in ROOTS:
        root.mkdir(mode=0o700, parents=True, exist_ok=True)
        os.chown(root, account.pw_uid, account.pw_gid)
        os.chmod(root, 0o700)
        atomic_write(root / MARKER, MARKER_CONTENT)
    source = Path(__file__).with_name("journald-aicrm-retention.conf")
    atomic_write(Path("/etc/systemd/journald@aicrm.conf.d/60-retention.conf"), source.read_text())
    for unit in UNITS:
        atomic_write(Path("/etc/systemd/system") / (unit + ".d") / "60-retention.conf", DROPIN)
    subprocess.run(["systemctl", "daemon-reload"], check=True, capture_output=True)
    subprocess.run(["systemctl", "try-restart", "systemd-journald@aicrm.service"], check=True, capture_output=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("inventory", "apply"), nargs="?", default="inventory")
    args = parser.parse_args()
    try:
        if args.mode == "apply":
            apply()
        print(json.dumps({"mode": args.mode, **commands()}, indent=2))
        return 0
    except (OSError, ValueError, KeyError, subprocess.CalledProcessError):
        print(json.dumps({"ok": False, "reason": "runtime_retention_install_refused"}), file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
