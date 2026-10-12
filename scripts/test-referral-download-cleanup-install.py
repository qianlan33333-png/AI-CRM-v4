#!/usr/bin/env python3
"""Install a per-user macOS cleanup agent for owned referral test downloads."""
import argparse
import json
import os
from pathlib import Path
import plistlib
import shutil
import subprocess
import sys

LABEL = "com.aicrm.referral-test-download-cleanup"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--uninstall", action="store_true")
    args = parser.parse_args()
    if sys.platform != "darwin":
        raise SystemExit("This installer is for a local macOS user only")
    user_home = Path.home()
    plist = user_home / "Library/LaunchAgents" / (LABEL + ".plist")
    installed = user_home / "Library/Application Support/AI-CRM-v4/test-download-cleanup/referral_test_downloads.mjs"
    target = "gui/" + str(os.getuid()) + "/" + LABEL
    present = subprocess.run(["launchctl", "print", target], capture_output=True).returncode == 0
    if args.uninstall:
        if present:
            subprocess.run(["launchctl", "bootout", target], check=True)
        plist.unlink(missing_ok=True)
        installed.unlink(missing_ok=True)
        print(json.dumps({"uninstalled": True, "label": LABEL}))
        return
    node = shutil.which("node")
    if not node:
        raise SystemExit("node is required")
    source = Path(__file__).resolve().parents[1] / "internal/webshell/test_referral_downloads.mjs"
    root = subprocess.check_output([
        node, "--input-type=module", "-e",
        "import os from 'node:os';import path from 'node:path';console.log(path.join(os.tmpdir(),'aicrm-referral-test-downloads'));",
    ], text=True).strip()
    installed.parent.mkdir(parents=True, exist_ok=True)
    temporary = installed.with_suffix('.tmp')
    shutil.copyfile(source, temporary)
    temporary.replace(installed)
    plist.parent.mkdir(parents=True, exist_ok=True)
    log = user_home / "Library/Logs/AI-CRM-v4/referral-test-download-cleanup.log"
    log.parent.mkdir(parents=True, exist_ok=True)
    contents = {
        "Label": LABEL,
        "ProgramArguments": [node, str(installed), "--cleanup", "--root", root],
        "RunAtLoad": True,
        "StartInterval": 3600,
        "StandardOutPath": str(log),
        "StandardErrorPath": str(log),
    }
    plist.write_bytes(plistlib.dumps(contents))
    subprocess.run(["plutil", "-lint", str(plist)], check=True, capture_output=True)
    subprocess.run([node, str(installed), "--cleanup", "--root", root], check=True)
    if present:
        subprocess.run(["launchctl", "bootout", target], check=True)
    subprocess.run(["launchctl", "bootstrap", "gui/" + str(os.getuid()), str(plist)], check=True)
    subprocess.run(["launchctl", "print", target], check=True, capture_output=True)
    print(json.dumps({"installed": True, "label": LABEL, "plist": str(plist),
                      "root": root, "retention_hours": 72, "scan_interval_seconds": 3600}))


if __name__ == "__main__":
    main()
