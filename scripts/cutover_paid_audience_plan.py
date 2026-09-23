#!/usr/bin/env python3
"""Generate only reviewed native API commands. No credential/DB/old-list access."""
import argparse
import datetime
import hashlib
import json
from pathlib import Path


def plan(targets, reference_time):
    parsed = datetime.datetime.fromisoformat(reference_time.replace("Z", "+00:00"))
    if parsed.tzinfo is None:
        raise ValueError("reference time requires timezone")
    if set(targets) != {30, 37} or len({v[0] for v in targets.values()}) != 2:
        raise ValueError("two distinct explicit target packages required")
    result = []
    fixtures = Path(__file__).resolve().parents[1] / "docs/migrations/fixtures"
    for source in (30, 37):
        target, version = targets[source]
        if target < 1 or version < 1:
            raise ValueError("positive target ID and reviewed current version required")
        definition = json.loads((fixtures / f"cutover-audience-{source}-definition.json").read_text())
        body = {"expected_package_version": version, "refresh_cron_utc": "", "definition": definition}
        path = f"/api/admin/ai-audience/packages/{target}"
        for method, action, payload in (("PUT", "configuration", body), ("POST", "refresh", {"reference_time": reference_time})):
            digest = hashlib.sha256(json.dumps([target, version, action, payload], sort_keys=True).encode()).hexdigest()
            result.append({"source_package": source, "method": method, "path": f"{path}/{action}", "headers": {"Idempotency-Key": "cutover-paid-rule:" + digest}, "body": payload})
    return result


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    for source in (30, 37):
        parser.add_argument(f"--target-{source}", type=int, required=True)
        parser.add_argument(f"--version-{source}", type=int, required=True)
    parser.add_argument("--reference-time", required=True)
    args = parser.parse_args()
    print(json.dumps(plan({30: (args.target_30, args.version_30), 37: (args.target_37, args.version_37)}, args.reference_time), indent=2))
