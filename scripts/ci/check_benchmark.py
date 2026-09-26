#!/usr/bin/env python3
"""Read-only release rehearsal: full comparison, then two warm affected runs.

This invokes existing preflight lanes in a disposable candidate checkout. It
never submits, installs, promotes or edits controller state/configuration.
"""
from __future__ import annotations
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import shutil
import signal
import threading
import subprocess
import sys
import tempfile
import time

import affected_plan
import check_preparation


def git(root: Path, *args: str) -> str:
    return subprocess.check_output(["git", *args], cwd=root, text=True).strip()


def terminal_events(path: Path) -> dict[tuple[str, str], str]:
    result = {}
    for line in path.read_text().splitlines():
        try:
            event = json.loads(line)
        except ValueError:
            continue
        if event.get("Action") in {"pass", "fail", "skip"}:
            result[(event.get("Package", ""), event.get("Test", ""))] = event["Action"]
    return result


def validate_comparison(full: dict, affected: dict, packages: list[str], journeys: list[str]) -> None:
    if any(action == "fail" for action in full.values()) or any(action == "fail" for action in affected.values()):
        raise ValueError("full or affected comparison has failed events")
    for package in packages:
        key = ("backend", package, "")
        if key not in affected or affected[key] != full.get(key):
            raise ValueError("affected package result differs from full baseline: " + package)
    for name in journeys:
        matches = [key for key in affected if key[0] == "browser" and key[2] == name]
        if len(matches) != 1 or affected[matches[0]] != "pass" or full.get(matches[0]) != "pass":
            raise ValueError("required journey is missing, skipped or differs from baseline: " + name)
    # Every test/subtest actually exercised in an affected lane must retain its
    # full-baseline terminal result; expected non-browser skips remain visible.
    for key, action in affected.items():
        if key not in full or full[key] != action:
            raise ValueError("affected terminal result differs from full baseline")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base", required=True)
    parser.add_argument("--report-dir", type=Path, required=True)
    parser.add_argument("--cold", action="store_true", help="also record a separate cold affected run")
    args = parser.parse_args()
    root = Path.cwd().resolve()
    report = args.report_dir.resolve()
    if platform.system() != "Linux" or platform.machine() not in {"x86_64", "amd64"}:
        raise ValueError("performance acceptance requires the actual Linux amd64 staging host")
    if report.is_relative_to(root) or report.exists():
        raise ValueError("use a fresh evidence directory outside the candidate checkout")
    head = git(root, "rev-parse", "HEAD")
    tree = git(root, "rev-parse", "HEAD^{tree}")
    plan = affected_plan.build_plan(root, args.base, head)
    if (not plan.get("evidence_eligible") or plan["candidate"]["profile"] != "affected-packages"
            or "public-commerce-v1" not in plan["candidate"]["selection_reasons"]):
        raise ValueError("benchmark requires a clean commerce-only diff against the optimizer baseline")
    report.mkdir(mode=0o700, parents=True)
    plan_path = report / "affected-plan.json"
    check_preparation.atomic_json(plan_path, plan)
    receipt = {"schema": 1, "head_sha": head, "head_tree": tree,
               "base_sha": plan["baseline_sha"], "planner_policy_fingerprint": affected_plan.planner_policy_fingerprint(),
               "host": platform.node(), "processors": os.cpu_count(), "runs": [], "acceptance": "incomplete"}
    summary = report / "benchmark.json"
    baseline = None
    try:
        for name in ["full", "warm-1", "warm-2"] + (["cold"] if args.cold else []):
            lane_report = report / name
            command = [sys.executable, "scripts/dev_preflight.py", "full" if name == "full" else "affected",
                       "--report-dir", str(lane_report)]
            if name != "full":
                command += ["--base", plan["baseline_sha"], "--head", head]
            started = time.monotonic()
            directory = Path(tempfile.mkdtemp(prefix="preparation-", dir=report))
            free_before = shutil.disk_usage(report).free
            minimum = {"free":free_before}
            stopped = threading.Event()
            def sample():
                while not stopped.wait(0.5):
                    minimum["free"] = min(minimum["free"],shutil.disk_usage(report).free)
            sampler = threading.Thread(target=sample,daemon=True)
            sampler.start()
            try:
                env = dict(os.environ, AICRM_TEST_PREP_DIR=str(directory),
                           AICRM_DEDUP_BASE_SHA=plan["baseline_sha"], AICRM_DEDUP_HEAD_SHA=head)
                if name == "cold":
                    # Only this disposable checkout's generated dependencies;
                    # canonical sources and shared host caches remain intact.
                    for path in (root / "node_modules", root / "web/v3/node_modules"):
                        shutil.rmtree(path, ignore_errors=True)
                    env.update(GOCACHE=str(Path(directory) / "go-build"),
                               npm_config_cache=str(Path(directory) / "npm-cache"))
                with (report / (name + ".log")).open("xb") as log:
                    process = subprocess.Popen(command,cwd=root,env=env,stdout=log,stderr=subprocess.STDOUT,
                                               start_new_session=True)
                    try:
                        result = subprocess.CompletedProcess(command,process.wait())
                    except BaseException:
                        try:
                            os.killpg(process.pid,signal.SIGTERM)
                            try: process.wait(timeout=5)
                            except subprocess.TimeoutExpired:
                                os.killpg(process.pid,signal.SIGKILL); process.wait(timeout=5)
                        except ProcessLookupError: pass
                        raise
            finally:
                stopped.set(); sampler.join()
                minimum["free"] = min(minimum["free"],shutil.disk_usage(report).free)
                metadata = report/(name+"-preparation")
                metadata.mkdir(mode=0o700)
                for value in [*directory.glob("*.json"),*directory.glob("*.jsonl")]:
                    if value.is_file() and not value.is_symlink(): shutil.copyfile(value,metadata/value.name)
                try:
                    check_preparation.cleanup_databases(directory, os.environ["AICRM_DATABASE_URL"])
                except BaseException:
                    # Preserve cleanup failure in the benchmark result.
                    receipt["acceptance"] = "failed_cleanup"
                    receipt["retained_preparation"] = str(directory)
                    raise
                shutil.rmtree(directory)
                elapsed = round(time.monotonic() - started, 3)
            receipt["runs"].append({"name": name, "command": command, "exit_code": result.returncode,
                                     "elapsed_seconds": elapsed,
                                     "disk": {"free_before_bytes":free_before,"minimum_free_bytes":minimum["free"],
                                              "peak_increment_bytes":max(0,free_before-minimum["free"]),
                                              "free_after_cleanup_bytes":shutil.disk_usage(report).free}, "cache": "cold" if name == "cold" else "existing-host-cache",
                                     "log_sha256": hashlib.sha256((report / (name + ".log")).read_bytes()).hexdigest()})
            check_preparation.atomic_json(summary, receipt)
            if result.returncode or git(root, "rev-parse", "HEAD") != head or git(root, "rev-parse", "HEAD^{tree}") != tree or git(root, "status", "--porcelain=v1", "--untracked-files=all"):
                raise ValueError("benchmark execution or exact-source validation failed")
            events = {}
            for path in lane_report.rglob("*-go-test.jsonl"):
                events.update({("backend", *key): value for key,value in terminal_events(path).items()})
            for path in lane_report.rglob("browser-execution.log"):
                events.update({("browser", *key): value for key,value in terminal_events(path).items()})
            if not events:
                raise ValueError("benchmark has no real Go/Chromium evidence")
            if name == "full":
                baseline = events
            else:
                validate_comparison(baseline, events, plan["candidate_go_packages"],
                    [check["test"] for check in plan["candidate"]["selected_checks"] if check["lane"] == "browser"])
        warm = [run for run in receipt["runs"] if run["name"].startswith("warm-")]
        receipt["acceptance"] = "passed" if len(warm) == 2 and all(run["elapsed_seconds"] <= 600 for run in warm) else "performance_target_unmet"
        return 0 if receipt["acceptance"] == "passed" else 1
    finally:
        check_preparation.atomic_json(summary, receipt)


if __name__ == "__main__":
    raise SystemExit(main())
