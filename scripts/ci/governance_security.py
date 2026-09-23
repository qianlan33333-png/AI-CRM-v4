#!/usr/bin/env python3
"""Free CLI security checks with explicit baselines, timeboxed debt and safe output.

No PR comments, paid SARIF upload, dependency updates or business repair occurs.
"""
from __future__ import annotations

import argparse
from datetime import date
import json
import os
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile

import governance_npm

ROOT = Path(__file__).resolve().parents[2]
TOOLS = ROOT / "scripts/ci/governance-tools.json"


def decode_stream(text: str) -> list[dict]:
    decoder, records = json.JSONDecoder(), []
    while text.strip():
        text = text.lstrip()
        record, end = decoder.raw_decode(text)
        if not isinstance(record, dict):
            raise ValueError("govulncheck record must be an object")
        records.append(record)
        text = text[end:]
    # Empty/truncated output must not masquerade as a clean scan.
    if not any("config" in record for record in records) or not any("progress" in record for record in records):
        raise ValueError("incomplete govulncheck stream")
    return records


def reachable_findings(records: list[dict]) -> dict[str, dict]:
    findings = {}
    for record in records:
        finding = record.get("finding")
        if not finding:
            continue
        trace = finding.get("trace", [])
        if not trace or not trace[0].get("function"):
            continue  # module/package presence is reported by scanner, not reachability
        frame = trace[0]
        key = "|".join((finding["osv"], frame.get("module", ""), frame.get("package", ""), frame.get("receiver", ""), frame["function"]))
        findings[key] = {"id": finding["osv"], "module": frame.get("module", ""),
                         "package": frame.get("package", ""), "symbol": frame.get("receiver", "") + "." + frame["function"],
                         "fixed_version": finding.get("fixed_version", ""), "key": key}
    return findings


def evaluate_debt(head: dict, baseline: dict, ledger: dict, today: date) -> tuple[list[dict], list[str]]:
    results, failures = [], []
    entries = ledger.get("entries", [])
    for key, finding in sorted(head.items()):
        result = dict(finding)
        if key not in baseline:
            result["status"] = "new_reachable_vulnerability"
            failures.append(key)
        else:
            debt = next((e for e in entries if e.get("id") == finding["id"] and e.get("module") == finding["module"]), None)
            valid = False
            if debt:
                try:
                    start, expiry = date.fromisoformat(debt["first_recorded"]), date.fromisoformat(debt["expires"])
                    valid = (start <= today <= expiry and 0 < (expiry - start).days <= 30
                             and debt.get("status") == "approved_timeboxed_exception"
                             and bool(debt.get("approved_by")) and bool(debt.get("approval_evidence"))
                             and bool(debt.get("owner")) and len(debt.get("reason", "")) >= 20
                             and bool(debt.get("tracking")))
                except (ValueError, KeyError, TypeError):
                    pass
            result["status"] = "approved_timeboxed_existing_debt" if valid else "unapproved_unregistered_or_expired_existing_debt"
            if debt:
                result["debt"] = debt
            if not valid:
                failures.append(key)
        results.append(result)
    return results, failures


def run(command: list[str], root: Path, output: Path, timeout: int = 600) -> int:
    output.parent.mkdir(parents=True, exist_ok=True)
    with output.open("w") as stream:
        result = subprocess.run(command, cwd=root, stdout=stream, stderr=subprocess.PIPE, text=True, timeout=timeout)
    if result.returncode not in (0, 1):
        # Do not echo scanner stderr, which may contain source context/secrets.
        raise ValueError(f"{Path(command[0]).name} did not complete (exit {result.returncode})")
    return result.returncode


def git(root: Path, *args: str) -> str:
    return subprocess.check_output(["git", *args], cwd=root, text=True).strip()


def verify_build_info(text: str, module: str, version: str) -> None:
    expected = ["mod", module, version]
    if not any(line.split()[:3] == expected for line in text.splitlines()):
        raise ValueError("scanner binary does not match the source-pinned module version")


def install(destination: Path) -> None:
    destination.mkdir(parents=True, exist_ok=True)
    tools = json.loads(TOOLS.read_text())["tools"]
    for name, tool in tools.items():
        # Build exact released source versions using the Go checksum database.
        # Explicit local toolchain prevents an unreviewed compiler download.
        subprocess.run(["go", "install", tool["module"] + "@" + tool["version"]], check=True,
                       env=dict(os.environ, GOBIN=str(destination.resolve()), GOTOOLCHAIN="local"))
        if not (destination / name).is_file():
            raise ValueError(f"missing installed tool: {name}")


def security(root: Path, base: str, head: str, tools_dir: Path, out: Path, weekly: bool) -> dict:
    out.mkdir(parents=True, exist_ok=True)
    report = {"schema": 1, "base": base, "head": head, "mode": "weekly_report" if weekly else "change_gate",
              "tools": json.loads(TOOLS.read_text())["tools"], "failures": [], "checks": {}}
    changed = git(root, "diff", "--name-only", "--no-renames", base, head).splitlines()
    if git(root, "rev-parse", "HEAD") != head:
        raise ValueError("scan checkout must equal tested head")
    if git(root, "status", "--porcelain", "--untracked-files=no"):
        raise ValueError("scan source must be the clean committed tested checkout")

    def binary(name: str) -> str:
        path = tools_dir / name
        if not path.is_file():
            raise ValueError(f"install pinned {name} before scanning")
        tool = report["tools"][name]
        metadata = subprocess.check_output(["go", "version", "-m", str(path.resolve())], text=True)
        verify_build_info(metadata, tool.get("source_module", tool["module"]), tool["version"])
        return str(path.resolve())

    # Gitleaks' own report is redacted. Exit 1 means findings; unexpected exits
    # are setup/scan failures, never successful/empty evidence.
    command = [binary("gitleaks"), "git", "--no-banner", "--redact=100", "--report-format", "json",
               "--report-path", str((out / "gitleaks.json").resolve())]
    if not weekly:
        command += ["--log-opts", base + ".." + head]
    command.append(".")
    leaks = run(command, root, out / "gitleaks-output.txt")
    leak_report = json.loads((out / "gitleaks.json").read_text())
    if not isinstance(leak_report, list) or bool(leak_report) != bool(leaks):
        raise ValueError("gitleaks report and exit status disagree")
    report["checks"]["gitleaks"] = {"status": "findings" if leaks else "passed", "scope": "full_history" if weekly else "new_commits"}
    if leaks:
        report["failures"].append("gitleaks")

    report["checks"]["npm"] = governance_npm.scan(root, base, head, out, weekly)
    if report["checks"]["npm"]["status"] != "passed":
        report["failures"].append("npm")

    code = run([binary("govulncheck"), "-format", "json", "./..."], root, out / "govulncheck-head.json")
    if code:
        raise ValueError("govulncheck failed to analyze candidate")
    head_records = decode_stream((out / "govulncheck-head.json").read_text())
    head_findings = reachable_findings(head_records)
    go_changed = any(p.endswith(".go") or p in {"go.mod", "go.sum", "go.work", "go.work.sum"} or p.startswith("vendor/") for p in changed)
    if weekly:
        baseline = head_findings
        baseline_mode = "weekly_inventory_not_a_new_change_baseline"
    elif not go_changed:
        baseline = head_findings
        baseline_mode = "identical_go_inputs"
    else:
        # A read-only git archive gives source at base without checkout mutations,
        # hooks or credentials. It cannot execute installation scripts from base.
        with tempfile.TemporaryDirectory(prefix="aicrm-security-base-") as temporary:
            target = Path(temporary)
            archive = target / "base.tar"
            with archive.open("wb") as stream:
                subprocess.run(["git", "archive", "--format=tar", base], cwd=root, stdout=stream, check=True)
            with tarfile.open(archive) as tar:
                for member in tar.getmembers():
                    if member.issym() or member.islnk() or member.name.startswith("/") or ".." in Path(member.name).parts:
                        raise ValueError("unexpected archive path or link")
                tar.extractall(target, filter="data")
            code = run([binary("govulncheck"), "-format", "json", "./..."], target, out / "govulncheck-base.json")
            if code:
                raise ValueError("govulncheck failed to analyze base")
            baseline = reachable_findings(decode_stream((out / "govulncheck-base.json").read_text()))
        baseline_mode = "same_tool_base_source_scan"
    ledger = json.loads((root / "docs/governance/security-debt.json").read_text())
    findings, failures = evaluate_debt(head_findings, baseline, ledger, date.today())
    report["checks"]["govulncheck"] = {"status": "findings" if failures else "passed", "baseline_mode": baseline_mode,
                                         "reachable": findings, "stream_parsed": True,
                                         "scope": "known Go source-reachable vulnerabilities; not a general security audit"}
    if failures:
        report["failures"].append("govulncheck")
    if not weekly and "api/openapi.yaml" in changed:
        base_spec = out / "openapi-base.yaml"
        base_spec.write_text(git(root, "show", base + ":api/openapi.yaml") + "\n")
        result = run([binary("oasdiff"), "breaking", str(base_spec.resolve()), str(root / "api/openapi.yaml"),
                      "--format", "json", "--fail-on", "WARN"], root, out / "oasdiff.json")
        report["checks"]["oasdiff"] = {"status": "breaking_changes" if result else "passed", "base": base}
        if result:
            report["failures"].append("oasdiff")
    else:
        report["checks"]["oasdiff"] = {"status": "not_applicable", "reason": "weekly inventory" if weekly else "OpenAPI identical to base"}
    return report


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=["install", "scan"])
    parser.add_argument("--tools-dir", type=Path, required=True)
    parser.add_argument("--root", type=Path, default=ROOT)
    parser.add_argument("--base")
    parser.add_argument("--out", type=Path)
    parser.add_argument("--weekly", action="store_true")
    args = parser.parse_args()
    if args.mode == "install":
        install(args.tools_dir)
        return 0
    if not args.base or not args.out:
        parser.error("scan requires --base and --out")
    root, out = args.root.resolve(), args.out.resolve()
    out.mkdir(parents=True, exist_ok=True)
    try:
        report = security(root, git(root, "rev-parse", args.base + "^{commit}"), git(root, "rev-parse", "HEAD"), args.tools_dir, out, args.weekly)
    except (OSError, ValueError, TypeError, KeyError, subprocess.SubprocessError, tarfile.TarError) as error:
        report = {"schema": 1, "status": "unverified", "failures": [type(error).__name__],
                  "message": "scanner setup, execution or result validation failed; no clean claim is available"}
        print(report["message"], file=sys.stderr)
    (out / "security.json").write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n")
    if os.environ.get("GITHUB_STEP_SUMMARY"):
        with open(os.environ["GITHUB_STEP_SUMMARY"], "a") as stream:
            stream.write("### Security inventory\n\n" + ("Findings or unavailable checks: " + ", ".join(report["failures"]) if report["failures"] else "Pinned CLI checks completed; see artifacts for scope and existing debt.") + "\n\nNo dependencies or business state were changed.\n")
    return 1 if report["failures"] else 0


if __name__ == "__main__":
    raise SystemExit(main())
