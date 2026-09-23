#!/usr/bin/env python3
"""Local/CI checks using existing gates, with exact execution evidence."""
from __future__ import annotations

import argparse
import json
import os
import platform
from pathlib import Path
import re
import shlex
import subprocess
import sys
import tempfile
import time

ROOT = Path(__file__).resolve().parents[1]
FULL_LANES = ("preflight", "backend", "frontend", "browser", "archive-sdk")
SHELL_JOURNEYS = {
    "TestPostgreSQLSidebarThumbnailChromiumJourney",
    "TestPostgreSQLAdminShellLayoutChromiumJourney",
}
# Coverage floor, not an allowlist: new compiled journeys join automatically.
REQUIRED_JOURNEYS = SHELL_JOURNEYS | {
    "TestPostgreSQLCustomerTagCommandChromiumJourney",
    "TestPostgreSQLOpenPlatformV1ChromiumJourney",
    "TestPostgreSQLProductExternalPushChromiumJourney",
    "TestPostgreSQLOwnerHandoffChromiumJourney",
    "TestPostgreSQLRuntimeReleaseChromiumJourney",
}


def select_journeys(listing: str, group: str) -> list[str]:
    names = set(re.findall(r"^Test\w*ChromiumJourney$", listing, re.MULTILINE))
    missing = REQUIRED_JOURNEYS - names
    if missing:
        raise ValueError("required Chromium tests missing: " + ", ".join(sorted(missing)))
    selected = names if group == "all" else names & SHELL_JOURNEYS if group == "shell" else names - SHELL_JOURNEYS
    if not selected:
        raise ValueError("empty Chromium selection")
    return sorted(selected)


def verify_journey_results(path: Path, expected: list[str]) -> dict:
    terminal, problems = {}, []
    package_passed = False
    for line in path.read_text().splitlines():
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue  # Compiler diagnostics; subprocess status is also checked.
        name, action = event.get("Test", ""), event.get("Action")
        if not name:
            if action == "pass":
                package_passed = True
            elif action in {"fail", "skip"}:
                problems.append("package: " + action)
        elif name.split("/")[0] in expected and action in {"pass", "fail", "skip"}:
            terminal[name] = action
            if action != "pass":
                problems.append(name + ": " + action)
    for name in expected:
        if terminal.get(name) != "pass":
            problems.append(name + ": no successful terminal event")
    if not package_passed:
        problems.append("no successful package terminal event")
    if problems:
        raise ValueError("Chromium execution incomplete: " + "; ".join(problems))
    return terminal


class Preflight:
    def __init__(self, report_dir: Path):
        self.report_dir = report_dir
        report_dir.mkdir(parents=True, exist_ok=True)
        start = self.source_snapshot()
        self.report = {
            "schema": 2,
            # These two legacy fields stay for tools already reading the report.
            "head": start["head"], "working_tree": start["status"],
            "source": {"start": start, "end": None, "unchanged_during_execution": None},
            "environment": self.environment_fingerprint(),
            "steps": [], "lanes": [], "result": "running",
        }

    @staticmethod
    def source_snapshot() -> dict:
        def git(ref: str) -> str:
            return subprocess.check_output(["git", "rev-parse", ref], cwd=ROOT, text=True).strip()
        return {
            "head": git("HEAD"), "tree": git("HEAD^{tree}"),
            "status": subprocess.check_output(
                ["git", "status", "--porcelain=v1", "--untracked-files=all"], cwd=ROOT, text=True
            ).splitlines(),
        }

    @staticmethod
    def environment_fingerprint() -> dict:
        def version(command: list[str]) -> str | None:
            try:
                return subprocess.check_output(command, text=True, stderr=subprocess.DEVNULL, timeout=5).splitlines()[0]
            except (OSError, subprocess.SubprocessError, IndexError):
                return None
        return {"os": platform.system(), "arch": platform.machine(), "go": version(["go", "version"]),
                "node": version(["node", "--version"]), "python": version([sys.executable, "--version"])}

    def save(self):
        (self.report_dir / "summary.json").write_text(json.dumps(self.report, ensure_ascii=False, indent=2) + "\n")

    def run(self, name: str, command: list[str], env: dict | None = None) -> Path:
        print(f"\n[{name}] {shlex.join(command)}", flush=True)
        log = self.report_dir / (name + ".log")
        start = time.monotonic()
        with log.open("w") as output:
            process = subprocess.Popen(command, cwd=ROOT, env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
            assert process.stdout is not None
            with process.stdout:
                for line in process.stdout:
                    output.write(line)
                    print(line, end="", flush=True)
            code = process.wait()
        self.report["steps"].append({"name": name, "command": command, "exit_code": code, "seconds": round(time.monotonic() - start, 2), "log": str(log)})
        self.save()
        if code:
            raise RuntimeError(f"{name} failed ({code}); reproduce: {shlex.join(command)}")
        return log

    def fast(self):
        # No npm install, database or browser setup is performed in this phase.
        self.run("format", ["make", "fmt-check"])
        self.run("boundaries", ["bash", "scripts/check-hxc-identity-boundaries.sh"])
        self.run("whitespace", ["git", "diff", "--check", "HEAD"])
        self.run("current-v3-source", ["node", "scripts/prepare-donor-source-views.mjs"])
        self.run("retention-registry", [sys.executable, "scripts/check-retention-registry.py"])
        self.run("preflight-tests", [sys.executable, "scripts/test_dev_preflight.py"])

    def compile(self):
        self.run("compile-all-tests", ["bash", "scripts/run-go-with-donor-views.sh", "go", "test", "-p", "1", "-run", "^$", "./..."])

    def browser(self, group: str):
        if not os.environ.get("AICRM_DATABASE_URL"):
            raise ValueError("AICRM_DATABASE_URL is required; use an isolated PostgreSQL 16 test database")
        self.run("current-v3-source", ["node", "scripts/prepare-donor-source-views.mjs"])
        env = dict(os.environ, AICRM_REQUIRE_CHROMIUM_JOURNEY="1")
        listing = self.run("browser-discovery", ["bash", "scripts/run-go-with-donor-views.sh", "go", "test", "-p", "1", "-list", "ChromiumJourney$", "./cmd/aicrm"], env)
        names = select_journeys(listing.read_text(), group)
        self.report["required_browser_tests"] = names
        self.save()
        pattern = "^(" + "|".join(names) + ")$"
        # The complete discovered suite exceeds Go's default ten-minute package
        # budget; individual journey and browser assertion deadlines remain bounded.
        log = self.run("browser-execution", ["bash", "scripts/run-go-with-donor-views.sh", "go", "test", "-json", "-p", "1", "-count=1", "-timeout=15m", "-run", pattern, "./cmd/aicrm"], env)
        self.report["browser_results"] = verify_journey_results(log, names)

    def full(self):
        start = self.report["source"]["start"]
        if start["status"]:
            raise ValueError("local full verification requires a clean committed working tree")
        try:
            dedup_base = subprocess.check_output(["git", "merge-base", "origin/main", start["head"]], cwd=ROOT, text=True).strip()
            origin_main = subprocess.check_output(["git", "rev-parse", "origin/main"], cwd=ROOT, text=True).strip()
        except subprocess.SubprocessError as error:
            raise ValueError("local full verification requires an origin/main merge-base") from error
        self.report["dedup"] = {"origin_main": origin_main, "merge_base": dedup_base}
        lane_env = dict(os.environ, PYTHONDONTWRITEBYTECODE="1", AICRM_DEDUP_BASE_SHA=dedup_base,
                        AICRM_DEDUP_HEAD_SHA=start["head"])
        for lane in FULL_LANES:
            before = self.source_snapshot()
            if before != start:
                raise RuntimeError("source changed before local full lane " + lane)
            lane_dir = self.report_dir / "lanes" / lane
            command = [sys.executable, "scripts/ci/quality_lanes.py", lane, "--report-dir", str(lane_dir)]
            try:
                self.run("lane-" + lane, command, lane_env)
                after = self.source_snapshot()
                lane_result = "success" if after == start else "source_changed"
                self.report["lanes"].append({"name": lane, "result": lane_result, "command": command,
                                             "exit_code": 0, "source": {"before": before, "after": after}})
                if after != start:
                    raise RuntimeError("source changed during local full lane " + lane)
            except RuntimeError:
                if not self.report["lanes"] or self.report["lanes"][-1].get("name") != lane:
                    self.report["lanes"].append({"name": lane, "result": "failed", "command": command,
                                                 "source": {"before": before, "after": self.source_snapshot()}})
                raise
        if set(FULL_LANES) & {"backend", "frontend", "browser"}:
            self.report["repository"] = "AI-CRM-v3"
            self.report["commit_sha"] = subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip()
            self.report["tree_sha"] = subprocess.check_output(["git", "rev-parse", "HEAD^{tree}"], text=True).strip()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("phase", choices=["fast", "compile", "browser", "full"])
    parser.add_argument("--group", choices=["all", "shell", "business"], default="all")
    parser.add_argument("--report-dir", type=Path)
    args = parser.parse_args()
    report_dir = (args.report_dir or Path(tempfile.mkdtemp(prefix="aicrm-preflight-"))).resolve()
    if args.phase == "full" and (report_dir == ROOT or ROOT in report_dir.parents):
        parser.error("local full evidence directory must be outside the Git worktree")
    check = Preflight(report_dir)
    check.report["phase"] = args.phase
    check.report["claim"] = args.phase
    check.report["eligible_for_delivery"] = False
    print("Evidence: " + str(check.report_dir), flush=True)
    try:
        if args.phase == "browser":
            check.browser(args.group)
        else:
            getattr(check, args.phase)()
        check.report["result"] = "passed"
    except (OSError, RuntimeError, ValueError) as error:
        check.report.update(result="failed", error=str(error))
        print(str(error), file=sys.stderr)
    finally:
        end = check.source_snapshot()
        check.report["source"]["end"] = end
        check.report["source"]["unchanged_during_execution"] = end == check.report["source"]["start"]
        if args.phase == "full":
            if check.report["result"] == "passed" and check.report["source"]["unchanged_during_execution"]:
                check.report["claim"] = "local_full"
                check.report["eligible_for_delivery"] = True
            else:
                if check.report["result"] == "passed":
                    check.report.update(result="failed", error="source changed during local full verification")
                check.report["claim"] = "not_verified"
        check.save()
    return 0 if check.report["result"] == "passed" else 1


if __name__ == "__main__":
    sys.exit(main())
