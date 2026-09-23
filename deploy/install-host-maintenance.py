#!/usr/bin/env python3
"""Install only fixed root-owned retention helpers, a unit and narrow polkit rule."""
import importlib.util
import json
import os
from pathlib import Path
import stat
import subprocess
import sys

sys.dont_write_bytecode = True

SOURCE = Path(__file__).resolve().parent
HELPERS = Path("/usr/local/libexec/aicrm-retention")
RESULTS = Path("/var/lib/aicrm-maintenance")


def root_directory(path):
    if path.exists():
        info = path.lstat()
        if not stat.S_ISDIR(info.st_mode) or path.resolve() != path or info.st_uid != 0 or info.st_mode & 0o022:
            raise ValueError("unsafe_maintenance_install_directory")
    else:
        path.mkdir(mode=0o755, parents=True)
    os.chmod(path, 0o755)


def install():
    if sys.platform != "linux" or os.geteuid() != 0:
        raise ValueError("linux_root_required")
    if not Path("/etc/polkit-1/rules.d").is_dir():
        raise ValueError("polkit_rules_support_required")
    spec = importlib.util.spec_from_file_location("runtime_policy", SOURCE / "install-runtime-retention.py")
    policy = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(policy)
    policy.apply()
    root_directory(HELPERS)
    root_directory(RESULTS)
    for name in ("runtime-maintenance.py", "cleanup-runtime-files.py"):
        policy.atomic_write(HELPERS / name, (SOURCE / name).read_text())
    policy.atomic_write(Path("/etc/systemd/system/aicrm-runtime-retention.service"), (SOURCE / "aicrm-runtime-retention.service").read_text())
    policy.atomic_write(Path("/etc/polkit-1/rules.d/60-aicrm-runtime-retention.rules"), (SOURCE / "60-aicrm-runtime-retention.rules").read_text())
    subprocess.run(["/usr/bin/systemctl", "daemon-reload"], capture_output=True, check=True)
    # No start/enable/timer here: the existing River hourly job calls the unit.
    print(json.dumps({"installed": True, "unit": "aicrm-runtime-retention.service", "scheduler": "existing_river_job", "application_restart_required": True}))


if __name__ == "__main__":
    try:
        install()
    except (OSError, ValueError, KeyError, subprocess.CalledProcessError):
        raise SystemExit("host maintenance install refused")
