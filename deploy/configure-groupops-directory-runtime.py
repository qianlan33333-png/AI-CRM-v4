#!/usr/bin/env python3
"""Activate only the explicitly requested WeCom directory read capability."""
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import time
import urllib.request


def main():
    sha = sys.argv[1]
    if not re.fullmatch(r"[0-9a-f]{40}", sha):
        raise SystemExit("invalid release SHA")
    path = Path(os.environ.get("AICRM_RUNTIME_ENV_FILE", "/etc/aicrm/aicrm.env"))
    if path.is_symlink() or not path.is_file():
        raise SystemExit("invalid runtime configuration")
    original = path.read_bytes()
    lines = original.decode().splitlines()
    values = {}
    for line in lines:
        if line and not line.startswith("#") and "=" in line:
            key, value = line.split("=", 1)
            if key in values:
                raise SystemExit("duplicate runtime key: " + key)
            values[key] = value
    if values.get("AICRM_WECOM_ENABLED") != "true" or not values.get("AICRM_WECOM_CONTACT_SECRET"):
        raise SystemExit("WeCom directory prerequisites unavailable")
    key = "AICRM_GROUP_OPS_PROVIDER_READ_ENABLED"
    updated = "\n".join(line for line in lines if not line.startswith(key + "=")) + "\n" + key + "=true\n"
    stat = path.stat()

    def replace(content):
        fd, name = tempfile.mkstemp(prefix=".groupops-directory-", dir=path.parent)
        try:
            with os.fdopen(fd, "wb") as file:
                file.write(content)
                file.flush()
                os.fsync(file.fileno())
            os.chmod(name, 0o600)
            if os.geteuid() == 0:
                os.chown(name, stat.st_uid, stat.st_gid)
            os.replace(name, path)
        finally:
            if os.path.exists(name):
                os.unlink(name)

    production = str(path) == "/etc/aicrm/aicrm.env"
    replace(updated.encode())
    try:
        if production:
            subprocess.run(["systemctl", "restart", "aicrm.service"], check=True)
            for _ in range(30):
                try:
                    with urllib.request.urlopen("http://127.0.0.1:8080/readyz", timeout=2) as response:
                        ready = json.load(response)
                    if ready.get("release_sha") == sha and ready.get("status") == "ready":
                        break
                except Exception:
                    pass
                time.sleep(1)
            else:
                raise RuntimeError("directory runtime readiness failed")
    except Exception:
        replace(original)
        if production:
            subprocess.run(["systemctl", "restart", "aicrm.service"], check=False)
        raise
    print("Group Ops directory read configuration active; dispatch configuration unchanged")
    # Report presence only. Never print credential values or identity scopes.
    print("Survey OAuth enabled:", values.get("AICRM_SURVEY_OAUTH_ENABLED") == "true")
    for required in ("AICRM_SURVEY_OAUTH_APP_ID", "AICRM_SURVEY_OAUTH_SECRET", "AICRM_SURVEY_OAUTH_OPEN_PLATFORM_ID"):
        print(required + " configured:", bool(values.get(required)))


if __name__ == "__main__":
    main()
