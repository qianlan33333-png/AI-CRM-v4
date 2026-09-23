#!/usr/bin/env python3
"""Readiness and exact release identity; never accept an older server's HTTP 200."""
import json
import re
import sys
import time
from urllib.error import URLError
from urllib.request import urlopen


def verify(base_url, sha, attempts=12):
    if not re.fullmatch(r"[0-9a-f]{40}", sha):
        raise ValueError("invalid release SHA")
    for attempt in range(attempts):
        try:
            with urlopen(base_url.rstrip("/") + "/readyz", timeout=10) as response:
                body = json.load(response)
            if body.get("status") == "ready" and body.get("release_sha") == sha:
                print(f"Verified public release {sha}")
                return
        except (URLError, TimeoutError, ValueError):
            pass
        if attempt + 1 < attempts:
            time.sleep(5)
    raise RuntimeError("public readiness did not confirm the expected release SHA")


if __name__ == "__main__":
    verify(sys.argv[1], sys.argv[2])
