#!/usr/bin/env python3
"""Audit every active npm lock using pinned npm, without installs or lifecycle scripts."""
from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[2]
NODE_VERSION = "v24.18.0"
NPM_VERSION = "11.12.1"
SEVERITIES = ("info", "low", "moderate", "high", "critical")


def parse_audit(text: str, code: int) -> dict:
    data = json.loads(text)
    if not isinstance(data, dict) or data.get("error") or data.get("auditReportVersion") != 2:
        raise ValueError("npm audit did not return a complete version 2 report")
    vulnerabilities = data.get("vulnerabilities")
    if not isinstance(data.get("metadata"), dict):
        raise ValueError("npm audit metadata is unavailable")
    metadata = data["metadata"].get("vulnerabilities", {})
    if not isinstance(vulnerabilities, dict) or not isinstance(metadata, dict) or any(type(metadata.get(k)) is not int or metadata[k] < 0 for k in (*SEVERITIES, "total")):
        raise ValueError("npm audit metadata is unavailable")
    counts = dict.fromkeys(SEVERITIES, 0)
    result = {}
    for name, finding in vulnerabilities.items():
        if not isinstance(finding, dict) or finding.get("name") != name or finding.get("severity") not in SEVERITIES:
            raise ValueError("invalid npm finding identity or severity")
        via = finding.get("via")
        if not isinstance(via, list) or not via or not finding.get("nodes"):
            raise ValueError("incomplete npm finding")
        advisories = []
        for cause in via:
            if isinstance(cause, str) and cause in vulnerabilities:
                advisories.append("dependency:" + cause)
            elif isinstance(cause, dict) and isinstance(cause.get("source"), int) and cause.get("url", "").startswith("https://github.com/advisories/GHSA-"):
                advisories.append(cause["url"])
            else:
                raise ValueError("invalid npm advisory or missing transitive cause")
        counts[finding["severity"]] += 1
        # Advisory identity is part of the baseline: an existing package gaining
        # a newly published advisory cannot be silently classified as old debt.
        key = name + "|" + "|".join(sorted(advisories))
        result[key] = {"package": name, "severity": finding["severity"], "advisories": sorted(advisories),
                       "range": finding.get("range", ""), "fix_available": finding.get("fixAvailable", False)}
    if counts != {key: metadata[key] for key in SEVERITIES} or metadata["total"] != len(result):
        raise ValueError("npm audit findings disagree with metadata")
    if code not in (0, 1) or bool(result) != bool(code):
        raise ValueError("npm audit report and exit status disagree")
    return result


def validate_inputs(package: bytes, lock: bytes) -> None:
    manifest, locked = json.loads(package), json.loads(lock)
    if locked.get("lockfileVersion") != 3 or not isinstance(locked.get("packages"), dict):
        raise ValueError("npm requires the committed version 3 lock")
    root = locked["packages"].get("")
    if not isinstance(root, dict):
        raise ValueError("npm lock root is missing")
    for field in ("name", "version", "dependencies", "devDependencies", "optionalDependencies"):
        if manifest.get(field) != root.get(field):
            raise ValueError("npm manifest and lock root disagree")


def git(root: Path, *args: str) -> bytes:
    return subprocess.check_output(["git", *args], cwd=root)


def project_paths(paths: list[str]) -> list[str]:
    manifests = {str(Path(p).parent) for p in paths if Path(p).name == "package.json"}
    locks = {str(Path(p).parent) for p in paths if Path(p).name == "package-lock.json"}
    if not manifests or manifests != locks:
        raise ValueError("every active npm manifest must have a tracked lock and vice versa")
    return sorted(manifests)


def verify_runtime() -> None:
    for command, expected in ((["node", "--version"], NODE_VERSION), (["npm", "--version"], NPM_VERSION)):
        actual = subprocess.check_output(command, text=True, stderr=subprocess.PIPE).strip()
        if actual != expected:
            raise ValueError("npm audit runtime differs from pinned Node/npm")


def scan_inputs(package: bytes, lock: bytes, output: Path) -> dict:
    validate_inputs(package, lock)
    # Only the two JSON inputs are copied. Repository .npmrc, install hooks,
    # source code and node_modules cannot execute or change the audit scope.
    with tempfile.TemporaryDirectory(prefix="aicrm-npm-audit-") as temporary:
        directory = Path(temporary)
        (directory / "package.json").write_bytes(package)
        (directory / "package-lock.json").write_bytes(lock)
        (directory / "user.npmrc").write_text("")
        (directory / "global.npmrc").write_text("")
        env = {k: v for k, v in os.environ.items() if not k.lower().startswith("npm_config_")}
        command = ["npm", "audit", "--json", "--package-lock-only", "--ignore-scripts", "--audit-level=info",
                   "--include=dev", "--include=optional", "--include=peer", "--registry=https://registry.npmjs.org",
                   "--userconfig=" + str(directory / "user.npmrc"), "--globalconfig=" + str(directory / "global.npmrc")]
        process = subprocess.run(command, cwd=directory, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, timeout=180)
        # npm registry errors can contain arbitrary response bodies. Retain only
        # validated audit JSON, never print stderr or persist an error payload.
        findings = parse_audit(process.stdout, process.returncode)
        output.write_text(process.stdout)
        return findings


def scan(root: Path, base: str, head: str, out: Path, weekly: bool) -> dict:
    verify_runtime()
    out.mkdir(parents=True, exist_ok=True)
    head_paths = git(root, "ls-tree", "-r", "--name-only", head).decode().splitlines()
    base_paths = git(root, "ls-tree", "-r", "--name-only", base).decode().splitlines()
    projects = project_paths(head_paths)
    # A malformed base is a setup failure, not an empty clean baseline.
    base_projects = project_paths(base_paths)
    reports = []
    for index, project in enumerate(projects):
        prefix = "" if project == "." else project + "/"
        package = git(root, "show", head + ":" + prefix + "package.json")
        lock = git(root, "show", head + ":" + prefix + "package-lock.json")
        current = scan_inputs(package, lock, out / f"npm-{index}-head.json")
        if weekly:
            previous, mode = current, "weekly_inventory_not_a_new_change_baseline"
        elif project not in base_projects:
            previous, mode = {}, "new_npm_project"
        else:
            old_package = git(root, "show", base + ":" + prefix + "package.json")
            old_lock = git(root, "show", base + ":" + prefix + "package-lock.json")
            if (package, lock) == (old_package, old_lock):
                previous, mode = current, "identical_npm_inputs_rescanned"
            else:
                previous = scan_inputs(old_package, old_lock, out / f"npm-{index}-base.json")
                mode = "same_tool_base_lock_scan"
        findings = [{**v, "status": "existing_unapproved_vulnerability" if k in previous else "new_vulnerability"}
                    for k, v in sorted(current.items())]
        reports.append({"project": project, "package_sha256": hashlib.sha256(package).hexdigest(),
                        "lock_sha256": hashlib.sha256(lock).hexdigest(), "baseline_mode": mode, "findings": findings,
                        "resolved_count": len(set(previous) - set(current))})
    return {"status": "findings" if any(p["findings"] for p in reports) else "passed",
            "node": NODE_VERSION, "npm": NPM_VERSION, "projects": reports,
            "policy": "all known severities block, including unapproved existing findings; no npm exceptions",
            "scope": "all tracked active npm locks including development tools; registry known-vulnerability data, not reachability"}


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=ROOT)
    parser.add_argument("--base", required=True)
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--weekly", action="store_true")
    args = parser.parse_args()
    head = git(args.root, "rev-parse", "HEAD").decode().strip()
    base = git(args.root, "rev-parse", args.base + "^{commit}").decode().strip()
    args.out.mkdir(parents=True, exist_ok=True)
    try:
        if git(args.root, "status", "--porcelain", "--untracked-files=no").strip():
            raise ValueError("npm scan requires clean committed source")
        report = scan(args.root, base, head, args.out, args.weekly)
    except (OSError, ValueError, TypeError, subprocess.SubprocessError):
        report = {"status": "unverified", "message": "npm setup, audit or report validation failed; no clean claim"}
    (args.out / "npm-security.json").write_text(json.dumps({"base": base, "head": head, **report}, indent=2) + "\n")
    return 0 if report["status"] == "passed" else 1


if __name__ == "__main__":
    raise SystemExit(main())
