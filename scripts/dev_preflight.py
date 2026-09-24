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


def select_journeys(listing: str, group: str, requested: list[str] | None = None) -> list[str]:
    names = set(re.findall(r"^Test\w*ChromiumJourney$", listing, re.MULTILINE))
    if requested:
        selected = set(requested)
        if len(selected) != len(requested) or not all(re.fullmatch(r"Test\w*ChromiumJourney", name) for name in requested):
            raise ValueError("focused Chromium selection contains an invalid or duplicate journey")
        missing = selected - names
        if missing:
            raise ValueError("selected Chromium tests missing: " + ", ".join(sorted(missing)))
        return sorted(selected)
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


def build_affected_plan(base: str, head: str) -> dict:
    """Load the shadow planner without changing the existing local phases."""
    ci_dir = str(ROOT / "scripts/ci")
    if ci_dir not in sys.path:
        sys.path.insert(0, ci_dir)
    import affected_plan
    return affected_plan.build_plan(ROOT, base, head)


def exact_affected_plan_source_binding(plan: dict, current: dict, root: Path = ROOT) -> bool:
    """Validate the clean SHA/tree/policy binding without requiring sample eligibility.

    `evidence_eligible` controls whether a shadow run can count toward the PR
    trial. A clean plan that conservatively selected all lanes (for example,
    because policy changed) is still valid for local execution.
    """
    if not isinstance(plan, dict) or not isinstance(current, dict):
        return False
    source = plan.get("source")
    if (not isinstance(source, dict) or plan.get("schema") != 1
            or plan.get("observed_mode") != "shadow" or plan.get("source_clean") is not True
            or source.get("working_tree_clean") is not True or source.get("head_matches") is not True
            or source.get("status") != [] or current.get("status") != []):
        return False
    baseline, head, head_tree = plan.get("baseline_sha"), plan.get("head_sha"), plan.get("head_tree")
    policy = plan.get("policy_fingerprint")
    if (not isinstance(baseline, str) or not re.fullmatch(r"[0-9a-f]{40}", baseline)
            or not isinstance(head, str) or not re.fullmatch(r"[0-9a-f]{40}", head)
            or not isinstance(plan.get("baseline_tree"), str)
            or not re.fullmatch(r"[0-9a-f]{40}", plan["baseline_tree"])
            or not isinstance(head_tree, str) or not re.fullmatch(r"[0-9a-f]{40}", head_tree)
            or not isinstance(policy, str) or not re.fullmatch(r"[0-9a-f]{64}", policy)
            or source.get("checked_out_head") != head
            or current.get("head") != head or current.get("tree") != head_tree):
        return False

    ci_dir = str(root / "scripts/ci")
    added_ci_path = ci_dir not in sys.path
    if added_ci_path:
        sys.path.insert(0, ci_dir)
    try:
        import verification
        binding = verification.exact_source_binding(baseline, head, head_tree, root=root)
    except (ImportError, OSError, ValueError, subprocess.SubprocessError):
        return False
    finally:
        if added_ci_path:
            try:
                sys.path.remove(ci_dir)
            except ValueError:
                pass
    return (isinstance(binding, dict)
            and all(plan.get(key) == binding.get(key) for key in
                    ("baseline_sha", "baseline_tree", "head_sha", "head_tree", "policy_fingerprint")))


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

    def browser(self, group: str, requested: list[str] | None = None):
        if not os.environ.get("AICRM_DATABASE_URL"):
            raise ValueError("AICRM_DATABASE_URL is required; use an isolated PostgreSQL 16 test database")
        self.run("current-v3-source", ["node", "scripts/prepare-donor-source-views.mjs"])
        env = dict(os.environ, AICRM_REQUIRE_CHROMIUM_JOURNEY="1")
        listing = self.run("browser-discovery", ["bash", "scripts/run-go-with-donor-views.sh", "go", "test", "-p", "1", "-list", "ChromiumJourney$", "./cmd/aicrm"], env)
        names = select_journeys(listing.read_text(), group, requested)
        self.report["required_browser_tests"] = names
        self.save()
        pattern = "^(" + "|".join(names) + ")$"
        # The complete discovered suite exceeds Go's default ten-minute package
        # budget; individual journey and browser assertion deadlines remain bounded.
        log = self.run("browser-execution", ["bash", "scripts/run-go-with-donor-views.sh", "go", "test", "-json", "-p", "1", "-count=1", "-timeout=15m", "-run", pattern, "./cmd/aicrm"], env)
        self.report["browser_results"] = verify_journey_results(log, names)

    def affected(self, plan: dict) -> str:
        start = self.report["source"]["start"]
        if not exact_affected_plan_source_binding(plan, start):
            raise ValueError("affected plan does not bind the clean checkout, exact source tree, and policy")
        candidate = plan.get("candidate")
        if not isinstance(candidate, dict):
            raise ValueError("affected plan has no candidate selection")
        lanes = candidate.get("selected_lanes")
        checks = candidate.get("selected_checks", [])
        packages = plan.get("candidate_go_packages", [])
        mode = candidate.get("selection_mode")
        candidate_profile = candidate.get("profile", "full")
        if (not isinstance(lanes, list) or not lanes
                or any(lane not in FULL_LANES for lane in lanes)
                or not isinstance(checks, list) or any(not isinstance(check, dict) for check in checks)
                or not isinstance(packages, list) or any(not isinstance(item, str) or not item for item in packages)
                or mode not in {"full", "targeted"}
                or candidate_profile not in {"full", "tooling", "documentation", "affected", "affected-packages"}
                or candidate_profile == "tooling" and (lanes != ["preflight"] or mode != "targeted")):
            raise ValueError("affected candidate selection is malformed")

        self.report["affected_plan"] = plan
        self.report["local_scope"] = list(lanes)
        self.report["claim"] = "local_affected_plan"
        self.report["eligible_for_delivery"] = False
        self.report["existing_ci_gates_retained"] = True
        self.report["affected_execution"] = {
            "mode": mode, "planned_lanes": list(lanes), "completed_lanes": [],
            "incomplete_lanes": [], "failed_lanes": [], "not_executed_lanes": [],
            "packages": sorted(set(packages)), "checks": checks,
        }

        env = dict(os.environ, PYTHONDONTWRITEBYTECODE="1",
                   AICRM_DEDUP_BASE_SHA=plan["baseline_sha"],
                   AICRM_DEDUP_HEAD_SHA=plan["head_sha"])
        affected_dir = self.report_dir / "affected-lanes"
        for lane in lanes:
            if self.source_snapshot() != start:
                self.report["affected_execution"]["not_executed_lanes"].extend(lanes[lanes.index(lane):])
                self.report["affected_execution"]["source_changed_before_lane"] = lane
                return "incomplete"

            lane_dir = affected_dir / lane
            command = [sys.executable, "scripts/ci/quality_lanes.py", lane,
                       "--report-dir", str(lane_dir)]
            if lane == "preflight":
                # quality_lanes has two concrete preflight profiles. Candidate
                # documentation/affected profiles use the ordinary full
                # preflight command set; only an explicit tooling candidate
                # uses the dedicated tooling contract commands.
                quality_profile = "tooling" if candidate_profile == "tooling" else "full"
                command.extend(["--profile", quality_profile])
            lane_checks = [check for check in checks if check.get("lane") == lane]
            if mode == "targeted" and lane == "backend" and packages:
                command.extend(["--focus-packages-json", json.dumps(sorted(set(packages)), separators=(",", ":"))])
                execution_scope = {"kind": "full-package-suites", "packages": sorted(set(packages))}
            elif mode == "targeted" and lane != "preflight" and lane != "backend" and lane_checks:
                command.extend(["--focus-checks-json", json.dumps(lane_checks, separators=(",", ":"))])
                execution_scope = {"kind": "mapped-checks", "checks": lane_checks}
            elif mode == "targeted" and lane == "backend" and not packages:
                # Backend test-name filtering would miss new tests in an affected
                # package, so an absent package inventory widens to the full lane.
                execution_scope = {"kind": "full-lane-fallback", "reason": "no affected package inventory"}
            elif mode == "targeted" and lane not in {"preflight", "backend"} and not lane_checks:
                execution_scope = {"kind": "full-lane-fallback", "reason": "no mapped checks for selected lane"}
            else:
                execution_scope = {"kind": "full-lane"}

            step_count = len(self.report["steps"])
            lane_result = "success"
            error_text = None
            try:
                self.run("affected-lane-" + lane, command, env)
            except (OSError, RuntimeError) as error:
                lane_result = "failed"
                error_text = str(error)

            step = self.report["steps"][step_count] if len(self.report["steps"]) > step_count else {}
            log_path = Path(step["log"]) if isinstance(step.get("log"), str) else None
            missing_environment = []
            if log_path and log_path.is_file():
                for line in log_path.read_text(encoding="utf-8", errors="replace").splitlines():
                    prefix = "missing required local environment: "
                    if line.startswith(prefix):
                        missing_environment.extend(item.strip() for item in line[len(prefix):].split(",") if item.strip())
            receipt_path = lane_dir / "run.json"
            receipt = None
            if receipt_path.is_file():
                try:
                    receipt = json.loads(receipt_path.read_text(encoding="utf-8"))
                except (OSError, json.JSONDecodeError):
                    receipt = None
            if lane_result == "success" and (
                    not isinstance(receipt, dict) or receipt.get("lane") != lane
                    or receipt.get("result") != "success" or receipt.get("exit_code") != 0
                    or receipt.get("tested_sha") != plan["head_sha"]
                    or receipt.get("tree") != plan["head_tree"]
                    or receipt.get("policy_fingerprint") != plan["policy_fingerprint"]
                    or not isinstance(receipt.get("commands"), list)
                    or not receipt.get("commands")
                    or not isinstance(receipt.get("elapsed_seconds"), (int, float))
                    or receipt.get("elapsed_seconds", 0) <= 0):
                lane_result = "incomplete"
                error_text = "lane command returned success without a matching successful run receipt"
            elif lane_result == "failed" and missing_environment:
                lane_result = "incomplete"

            lane_record = {
                "name": lane, "result": lane_result, "command": command,
                "execution_scope": execution_scope,
                "exit_code": step.get("exit_code"), "log": str(log_path) if log_path else None,
                "receipt": receipt, "missing_environment": sorted(set(missing_environment)),
                "error": error_text,
            }
            self.report["lanes"].append(lane_record)
            if lane_result == "success":
                self.report["affected_execution"]["completed_lanes"].append(lane)
            elif lane_result == "incomplete":
                self.report["affected_execution"]["incomplete_lanes"].append(lane)
            else:
                self.report["affected_execution"]["failed_lanes"].append(lane)

            if self.source_snapshot() != start:
                self.report["affected_execution"]["not_executed_lanes"].extend(lanes[lanes.index(lane) + 1:])
                self.report["affected_execution"]["source_changed_during_lane"] = lane
                return "incomplete"

        execution = self.report["affected_execution"]
        if execution["incomplete_lanes"] or execution["not_executed_lanes"]:
            return "incomplete"
        if execution["failed_lanes"]:
            return "failed"
        graph = plan.get("graph_result")
        if not isinstance(graph, dict):
            self.report["affected_collection"] = {"result": "incomplete",
                                                   "error": "plan has no bound package graph receipt"}
            return "incomplete"
        ci_dir = str(ROOT / "scripts/ci")
        if ci_dir not in sys.path:
            sys.path.insert(0, ci_dir)
        try:
            import affected_shadow
            local_runs = []
            for lane in lanes:
                lane_receipt = dict(next(item["receipt"] for item in self.report["lanes"]
                                         if item["name"] == lane))
                go_log = lane_receipt.get("go_json_log")
                if go_log:
                    lane_receipt["go_json_log"] = str((affected_dir / lane / go_log).resolve())
                local_runs.append(lane_receipt)
            collection = affected_shadow.collect_execution(plan, graph, local_runs, allow_local=True)
            self.report["affected_collection"] = collection
            if not collection["required_results_passed"]:
                return "failed"
        except (OSError, ValueError, KeyError, StopIteration) as error:
            self.report["affected_collection"] = {"result": "incomplete", "error": str(error)}
            return "incomplete"
        return "passed"

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
    parser.add_argument("phase", choices=["fast", "compile", "browser", "full", "affected"])
    parser.add_argument("--group", choices=["all", "shell", "business"], default="all")
    parser.add_argument("--journey", action="append", default=[], help="run only the named Chromium journey; repeat for several")
    parser.add_argument("--report-dir", type=Path)
    parser.add_argument("--base", help="baseline commit for the affected shadow plan")
    parser.add_argument("--head", default="HEAD", help="candidate commit for the affected shadow plan")
    parser.add_argument("--dry-run", action="store_true", help="print the affected plan without local checks")
    args = parser.parse_args()
    if args.phase == "affected" and not args.base:
        parser.error("affected requires --base SHA")
    if args.phase != "affected" and (args.base or args.head != "HEAD" or args.dry_run):
        parser.error("--base, --head and --dry-run are only valid with affected")
    if args.phase == "affected":
        if args.report_dir:
            requested_report = args.report_dir.resolve()
            if requested_report == ROOT or ROOT in requested_report.parents:
                parser.error("affected evidence directory must be outside the Git worktree")
        try:
            plan = build_affected_plan(args.base, args.head)
        except Exception as error:
            print(str(error), file=sys.stderr)
            return 2
        print(json.dumps(plan, ensure_ascii=False, sort_keys=True, separators=(",", ":")))
        if args.dry_run:
            return 0 if plan.get("evidence_eligible") else 2
        report_dir = (args.report_dir or Path(tempfile.mkdtemp(prefix="aicrm-affected-"))).resolve()
        check = Preflight(report_dir)
        check.report["phase"] = "affected"
        affected_result = "failed"
        try:
            affected_result = check.affected(plan)
            check.report["result"] = affected_result
            if affected_result != "passed":
                check.report["claim"] = "not_verified"
        except (OSError, RuntimeError, ValueError) as error:
            check.report.update(result="failed", error=str(error), claim="not_verified")
            print(str(error), file=sys.stderr)
        finally:
            end = check.source_snapshot()
            check.report["source"]["end"] = end
            check.report["source"]["unchanged_during_execution"] = end == check.report["source"]["start"]
            if not check.report["source"]["unchanged_during_execution"]:
                check.report.update(result="failed", error="source changed during local affected checks",
                                    claim="not_verified")
            check.save()
        print("Evidence: " + str(check.report_dir), flush=True)
        return {"passed": 0, "failed": 1, "incomplete": 2}.get(check.report["result"], 1)
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
            check.browser(args.group, args.journey)
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
