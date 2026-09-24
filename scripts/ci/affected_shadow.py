#!/usr/bin/env python3
"""Validate existing CI execution logs and evaluate real paired timing evidence.

Shadow collection consumes the lane executions already required by CI. It never
starts a second test run. Separate benchmark records are required before any
speed claim can authorize enabling a narrower plan.
"""
from __future__ import annotations

import argparse
from datetime import datetime
from datetime import timezone
import hashlib
import json
from pathlib import Path
import re
import statistics
import subprocess
import sys
from typing import Any

import affected_plan

SCHEMA = 1
PLAN_BINDINGS = ("baseline_sha", "baseline_tree", "head_sha", "head_tree", "policy_fingerprint")
HEX40 = re.compile(r"^[0-9a-f]{40}$")
HEX64 = re.compile(r"^[0-9a-f]{64}$")
MIN_SAVINGS = 0.30


class ShadowEvidenceError(ValueError):
    """Raised for malformed or revision-mismatched CI evidence."""


def _json_lines(text: str) -> tuple[list[dict[str, Any]], list[str]]:
    events: list[dict[str, Any]] = []
    invalid = []
    for line_no, line in enumerate(text.splitlines(), start=1):
        if not line.strip():
            continue
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            invalid.append(f"line {line_no} is not JSON")
            continue
        if not isinstance(event, dict):
            invalid.append(f"line {line_no} is not an object")
            continue
        events.append(event)
    return events, invalid


def parse_go_json(text: str) -> dict[str, Any]:
    """Summarize package and test terminal events from ``go test -json``."""
    events, invalid = _json_lines(text)
    packages: dict[str, dict[str, Any]] = {}
    tests: dict[str, dict[str, str]] = {}
    top_level: dict[str, set[str]] = {}
    for event in events:
        package = event.get("Package")
        action = event.get("Action")
        test = event.get("Test")
        if not isinstance(package, str) or not isinstance(action, str):
            continue
        if action == "output" and isinstance(event.get("Output"), str):
            item = packages.setdefault(package, {"terminal_action": None, "elapsed_seconds": None,
                                                 "terminal_count": 0, "no_test_files": False})
            if "[no test files]" in event["Output"]:
                item["no_test_files"] = True
        elif isinstance(test, str) and test:
            if action in {"pass", "fail", "skip"}:
                tests.setdefault(package, {})[test] = action
                if "/" not in test:
                    top_level.setdefault(package, set()).add(test)
        elif action in {"pass", "fail", "skip"}:
            item = packages.setdefault(package, {"terminal_action": None, "elapsed_seconds": None,
                                                 "terminal_count": 0, "no_test_files": False})
            item["terminal_action"] = action
            item["terminal_count"] += 1
            elapsed = event.get("Elapsed")
            if isinstance(elapsed, (int, float)) and elapsed >= 0:
                item["elapsed_seconds"] = float(elapsed)
    return {
        "events": len(events),
        "invalid_lines": invalid,
        "packages": packages,
        "tests": tests,
        "top_level_tests": {name: sorted(values) for name, values in top_level.items()},
    }


def _plan_bindings(plan: dict[str, Any]) -> dict[str, str]:
    bindings = {}
    for key in PLAN_BINDINGS:
        value = plan.get(key)
        if not isinstance(value, str) or not value:
            raise ShadowEvidenceError(f"plan is missing {key}")
        bindings[key] = value
    for key in ("baseline_sha", "baseline_tree", "head_sha", "head_tree"):
        if not HEX40.fullmatch(bindings[key]):
            raise ShadowEvidenceError(f"plan {key} must be a 40-character lowercase SHA")
    if not HEX64.fullmatch(bindings["policy_fingerprint"]):
        raise ShadowEvidenceError("plan policy_fingerprint must be a SHA-256 digest")
    return bindings


def _safe_seconds(value: Any, label: str) -> float:
    if isinstance(value, bool) or not isinstance(value, (int, float)) or value <= 0:
        raise ShadowEvidenceError(f"{label} must be a positive measured duration")
    return float(value)


def _nonnegative_seconds(value: Any, label: str) -> float:
    if isinstance(value, bool) or not isinstance(value, (int, float)) or value < 0:
        raise ShadowEvidenceError(f"{label} must be a measured nonnegative duration")
    return float(value)


def _package_for_check(check: dict[str, Any], module: str) -> str | None:
    if isinstance(check.get("package"), str):
        return check["package"]
    path = check.get("path")
    if not isinstance(path, str) or not path.endswith(".go"):
        return None
    parent = Path(path).parent.as_posix()
    return module if parent == "." else module + "/" + parent


def validate_lane_run(run: dict[str, Any], bindings: dict[str, str], allow_local: bool = False) -> dict[str, Any]:
    lane = run.get("lane")
    if not isinstance(lane, str) or not lane:
        raise ShadowEvidenceError("lane run has no lane name")
    if run.get("tested_sha") != bindings["head_sha"] or run.get("tree") != bindings["head_tree"]:
        raise ShadowEvidenceError(f"{lane} execution does not match the plan's tested SHA/tree")
    if run.get("policy_fingerprint") != bindings["policy_fingerprint"]:
        raise ShadowEvidenceError(f"{lane} execution does not match the plan's policy fingerprint")
    result, exit_code = run.get("result"), run.get("exit_code")
    if result not in {"success", "failed"} or isinstance(exit_code, bool) or not isinstance(exit_code, int):
        raise ShadowEvidenceError(f"{lane} lane result or exit code is invalid")
    if (result == "success" and exit_code != 0) or (result == "failed" and exit_code == 0):
        raise ShadowEvidenceError(f"{lane} lane result does not match its exit code")
    elapsed = _safe_seconds(run.get("elapsed_seconds"), lane + " elapsed_seconds")
    run_id, run_attempt = run.get("run_id"), run.get("run_attempt")
    if not isinstance(run_id, (int, str)) or not str(run_id):
        if not allow_local:
            raise ShadowEvidenceError(f"{lane} receipt is missing run_id")
        run_id = "local"
    if isinstance(run_attempt, bool) or not isinstance(run_attempt, int) or run_attempt < 1:
        if not allow_local:
            raise ShadowEvidenceError(f"{lane} receipt is missing a valid run_attempt")
        run_attempt = 1
    commands = run.get("commands")
    if commands is None and isinstance(run.get("command"), list):
        commands = [run["command"]]
    if (not isinstance(commands, list) or any(not isinstance(command, list)
            or not command or any(not isinstance(part, str) for part in command) for command in commands)):
        raise ShadowEvidenceError(f"{lane} receipt has no executed command inventory")
    if result == "success" and not commands:
        raise ShadowEvidenceError(f"{lane} successful receipt has no executed command inventory")
    return {"lane": lane, "tested_sha": bindings["head_sha"], "tree": bindings["head_tree"],
            "elapsed_seconds": elapsed, "run_id": run_id,
            "run_attempt": run_attempt, "commands": commands,
            "result": result, "exit_code": exit_code,
            "commands_sha256": _command_digest(commands) if commands else None}


def collect_execution(plan: dict[str, Any], graph: dict[str, Any], runs: list[dict[str, Any]],
                      required_checks: list[dict[str, Any]] | None = None,
                      allow_local: bool = False, pr_number: int | None = None,
                      original_gate_result: str = "unknown") -> dict[str, Any]:
    """Join package graph, required lane receipts, and actual JSON test logs.

    Any missing package, mapped required test, invalid output, or skipped
    unmapped test makes the shadow evidence ineligible. It does not change the
    existing stable check's outcome; the report tells the activation evaluator
    why this sample cannot count.
    """
    bindings = _plan_bindings(plan)
    if graph.get("schema") != 1:
        raise ShadowEvidenceError("unsupported Go graph schema")
    for key in ("baseline_sha", "baseline_tree", "head_sha", "head_tree"):
        if graph.get(key) != bindings[key]:
            raise ShadowEvidenceError(f"Go graph {key} does not match the plan")
    if graph.get("changed_paths") != plan.get("changed_paths"):
        raise ShadowEvidenceError("Go graph changed paths do not match the plan")
    graph_not_required = graph.get("status") == "not_required"
    if graph_not_required and not affected_plan.graph_not_required(plan.get("changed_paths", [])):
        raise ShadowEvidenceError("missing Go graph is not justified by a known docs/tooling-only change")
    if graph.get("graph_valid") is not True or graph.get("unowned_go_paths"):
        raise ShadowEvidenceError("Go graph is invalid or has unowned source paths")
    if not isinstance(graph.get("graph_fingerprint"), str) or not HEX64.fullmatch(graph["graph_fingerprint"]):
        raise ShadowEvidenceError("Go graph fingerprint is missing or malformed")

    source = plan.get("source", {})
    parent_prd = plan.get("parent_prd", {})
    parent_prd_valid = (isinstance(parent_prd, dict) and isinstance(parent_prd.get("id"), str)
                        and parent_prd["id"] and isinstance(parent_prd.get("sha256"), str)
                        and HEX64.fullmatch(parent_prd["sha256"]))
    source_binding_invalid = (plan.get("source_clean") is not True or not isinstance(source, dict)
                              or source.get("working_tree_clean") is not True
                              or source.get("head_matches") is not True)
    shadow_sample_invalid = (plan.get("evidence_eligible") is not True or not parent_prd_valid)
    if source_binding_invalid or (shadow_sample_invalid and not allow_local):
        source_problems = ["plan source is dirty or not evidence-eligible"]
    else:
        source_problems = []

    by_lane: dict[str, dict[str, Any]] = {}
    run_problems = list(source_problems)
    original_lane_failures = []
    for run in runs:
        lane_receipt = validate_lane_run(run, bindings, allow_local=allow_local)
        lane = lane_receipt["lane"]
        if lane in by_lane:
            run_problems.append("duplicate lane receipt: " + lane)
            continue
        by_lane[lane] = lane_receipt
        if lane_receipt["result"] != "success":
            original_lane_failures.append(lane)
            run_problems.append("original CI lane failed: " + lane)
        log_path = run.get("go_json_log")
        if log_path:
            try:
                log_text = Path(log_path).read_text(encoding="utf-8")
            except OSError as error:
                by_lane[lane]["parsed"] = {"invalid_lines": ["cannot read Go JSON log"]}
                run_problems.append(f"{lane} Go JSON log unavailable: {type(error).__name__}")
            else:
                by_lane[lane]["parsed"] = parse_go_json(log_text)
                if by_lane[lane]["parsed"]["invalid_lines"]:
                    run_problems.append(lane + " Go JSON output contains invalid lines")
        else:
            by_lane[lane]["parsed"] = None

    selected_packages = graph.get("selected_packages")
    if not isinstance(selected_packages, list):
        raise ShadowEvidenceError("Go graph selected_packages must be an array")
    package_files: dict[str, dict[str, Any]] = {}
    for item in selected_packages:
        if (not isinstance(item, dict) or not isinstance(item.get("import_path"), str)
                or not isinstance(item.get("test_files"), list)):
            raise ShadowEvidenceError("Go graph package inventory is incomplete")
        package_files[item["import_path"]] = item
    required_packages = sorted(package_files)
    planned_packages = plan.get("candidate_go_packages")
    if not isinstance(planned_packages, list) or sorted(planned_packages) != required_packages:
        raise ShadowEvidenceError("plan package inventory does not match the Go graph")
    candidate = plan.get("candidate", {})
    planned_lanes = plan.get("selected_lanes", candidate.get("selected_lanes"))
    if not isinstance(planned_lanes, list) or any(lane not in {"preflight", "backend", "frontend", "browser", "archive-sdk"}
                                                   for lane in planned_lanes):
        raise ShadowEvidenceError("plan selected_lanes must be a valid array")
    checks = required_checks if required_checks is not None else plan.get("selected_checks")
    if not isinstance(checks, list):
        raise ShadowEvidenceError("required checks must be a JSON array")
    problems = list(run_problems)
    # Original lane failures belong to the old gate observation. They only
    # invalidate the candidate when they also prevent a selected check from
    # being evaluated below.
    required_failures = list(source_problems)
    candidate_explicit_failure = False
    if not required_packages and not checks and not planned_lanes:
        problems.append("shadow plan contains no package, mapped-check, or selected-lane inventory")
    for lane in sorted(set(planned_lanes)):
        if lane not in by_lane:
            problem = f"selected lane did not execute: {lane}"
            problems.append(problem)
            required_failures.append(problem)
    required_tests = []
    for check in checks:
        if not isinstance(check, dict):
            raise ShadowEvidenceError("required check is not an object")
        test = check.get("test")
        lane = check.get("lane")
        package = _package_for_check(check, graph.get("module", ""))
        if lane not in by_lane:
            problem = f"selected check lane did not execute ({lane}): {test or check.get('path', '?')}"
            problems.append(problem)
            required_failures.append(problem)
        if isinstance(test, str) and package and lane in {"backend", "browser"}:
            required_tests.append({"package": package, "test": test, "lane": lane,
                                  "path": check.get("path")})

    backend = by_lane.get("backend", {}).get("parsed")
    package_execution_status: dict[str, str] = {}
    if required_packages:
        if backend is None:
            problem = "backend Go JSON log is required for affected packages"
            problems.append(problem)
            required_failures.append(problem)
        else:
            commands = by_lane["backend"]["commands"]
            flat_commands = [str(part) for command in commands for part in command]
            if not any("go" in command and "test" in command for command in commands):
                problems.append("backend receipt does not identify its Go test command")
                required_failures.append("backend receipt does not identify its Go test command")
            go_test_commands = [command for command in commands if "go" in command and "test" in command]
            full_package_suite = any("./..." in command for command in go_test_commands)
            if not full_package_suite:
                module = graph.get("module", "")
                for package in required_packages:
                    prefix = module + "/" if module else ""
                    if not prefix or not package.startswith(prefix):
                        problem = f"affected package is outside the Go module inventory: {package}"
                        problems.append(problem)
                        required_failures.append(problem)
                        continue
                    package_dir = "./" + package[len(prefix):]
                    if not any(package_dir in command for command in go_test_commands):
                        problem = f"backend Go test command omitted affected package: {package}"
                        problems.append(problem)
                        required_failures.append(problem)
            if "-json" not in flat_commands:
                problems.append("backend Go test command did not produce JSON execution evidence")
                required_failures.append("backend Go test command did not produce JSON execution evidence")
            command_flags = {part.split("=", 1)[0] for part in flat_commands}
            if command_flags & {"-run", "-skip", "-list"}:
                problems.append("backend Go test command filtered the affected package suite")
                required_failures.append("backend Go test command filtered the affected package suite")
            if "-count=1" not in flat_commands:
                problems.append("backend Go test command did not disable test caching")
                required_failures.append("backend Go test command did not disable test caching")
            if "-race" not in flat_commands:
                problems.append("backend Go test command omitted the canonical race check")
                required_failures.append("backend Go test command omitted the canonical race check")
            if not backend["events"] or backend["invalid_lines"]:
                problems.append("backend Go JSON event stream is empty or malformed")
                required_failures.append("backend Go JSON event stream is empty or malformed")
            for package in required_packages:
                package_result = backend["packages"].get(package, {})
                terminal = package_result.get("terminal_action")
                test_count = len(backend["top_level_tests"].get(package, []))
                test_files = package_files[package]["test_files"]
                if test_files:
                    if terminal != "pass":
                        if terminal == "fail":
                            candidate_explicit_failure = True
                        problem = f"affected package tests did not pass in backend run: {package}"
                        problems.append(problem)
                        required_failures.append(problem)
                        package_execution_status[package] = "failed"
                        continue
                    if test_count == 0:
                        problem = f"affected package has a test-file inventory but no executed tests: {package}"
                        problems.append(problem)
                        required_failures.append(problem)
                        package_execution_status[package] = "failed"
                    else:
                        package_execution_status[package] = "tests_passed"
                elif terminal == "skip" and package_result.get("no_test_files") is True and test_count == 0:
                    package_execution_status[package] = "compile_only_no_tests"
                else:
                    problem = f"affected package has no test files but lacks successful no-test compile evidence: {package}"
                    problems.append(problem)
                    required_failures.append(problem)
                    package_execution_status[package] = "failed"

    for required in required_tests:
        lane = required["lane"]
        parsed = by_lane.get(lane, {}).get("parsed")
        if parsed is None:
            problem = f"required {lane} test has no Go JSON execution log: {required['test']}"
            problems.append(problem)
            required_failures.append(problem)
            continue
        action = parsed["tests"].get(required["package"], {}).get(required["test"])
        if action != "pass":
            if action in {"fail", "skip"}:
                candidate_explicit_failure = True
            problem = f"required {lane} test did not pass ({action or 'unexecuted'}): {required['test']}"
            problems.append(problem)
            required_failures.append(problem)

    for check in checks:
        lane = check.get("lane")
        if lane in {"frontend", "archive-sdk"}:
            commands = by_lane.get(lane, {}).get("commands", [])
            path = check.get("path")
            command_ran = (isinstance(path, str) and any(
                any(path in part for part in command) for command in commands))
            if not command_ran:
                problem = f"required {lane} check command was not executed: {path or '?'}"
                problems.append(problem)
                required_failures.append(problem)
            elif by_lane.get(lane, {}).get("result") == "failed":
                candidate_explicit_failure = True

    required_by_lane = {(item["package"], item["test"], item["lane"]) for item in required_tests}
    unknown_skips = []
    stage_owned_skips = []
    unselected_skips = []
    stage_owned = {
        ("github.com/qianlan33333-png/AI-CRM-v3/cmd/aicrm", "TestDomesticReleaseInstalledAlipayCheckout")
    }
    for lane, receipt in by_lane.items():
        parsed = receipt.get("parsed")
        if not parsed:
            continue
        for package, test_actions in parsed["tests"].items():
            for test, action in test_actions.items():
                if action != "skip" or "/" in test:
                    continue
                if (package, test) in stage_owned:
                    stage_owned_skips.append({"lane": lane, "package": package, "test": test,
                                              "owner": "staging-installed-binary-contract"})
                    continue
                if lane == "backend" and test.endswith("ChromiumJourney"):
                    browser_receipt = by_lane.get("browser", {})
                    browser_parsed = browser_receipt.get("parsed")
                    browser_test = (browser_parsed.get("tests", {}).get(package, {}).get(test)
                                    if isinstance(browser_parsed, dict) else None)
                    same_run = (browser_receipt.get("run_id") == receipt.get("run_id")
                                and browser_receipt.get("run_attempt") == receipt.get("run_attempt"))
                    if same_run and browser_test == "pass":
                        continue
                owner = next((required_lane for required_package, required_test, required_lane in required_by_lane
                              if required_package == package and required_test == test), None)
                if package not in required_packages and not any(item[0] == package and item[1] == test
                                                                 for item in required_by_lane):
                    unselected_skips.append({"lane": lane, "package": package, "test": test})
                    continue
                if owner != lane:
                    unknown_skips.append({"lane": lane, "package": package, "test": test})
    if unknown_skips:
        problems.append("unmapped skipped tests make this shadow sample ineligible")

    package_measurements = []
    if backend:
        for package in required_packages:
            item = backend["packages"].get(package, {})
            execution_kind = package_execution_status.get(package, "not_verified")
            package_measurements.append({
                "package": package,
                "elapsed_seconds": item.get("elapsed_seconds"),
                "terminal_action": item.get("terminal_action"),
                "executed_top_level_tests": len(backend["top_level_tests"].get(package, [])),
                "test_file_count": len(package_files[package]["test_files"]),
                "execution_kind": execution_kind,
            })

    planned_lane_set = set(planned_lanes)
    selected_failed_lanes = {lane for lane in planned_lane_set
                             if lane in by_lane and by_lane[lane]["result"] != "success"}
    if "preflight" in selected_failed_lanes:
        candidate_explicit_failure = True
    if candidate_explicit_failure:
        candidate_result = "failure"
    elif required_failures or unknown_skips:
        candidate_result = "unknown"
    elif selected_failed_lanes:
        # The canonical lane receipt has only a lane-level exit status. If a
        # selected lane failed but all selected package tests appear green in
        # the full-run log, we cannot infer that the candidate lane would pass:
        # shared setup, vet, or non-Go checks may have failed. Keep it unknown.
        candidate_result = "unknown"
    else:
        candidate_result = "success"

    trial_observation = None
    if isinstance(pr_number, int) and not isinstance(pr_number, bool) and pr_number > 0:
        lane_receipts = []
        for lane in sorted(by_lane):
            item = by_lane[lane]
            lane_receipts.append({
                "lane": lane, "result": item["result"], "exit_code": item["exit_code"],
                "elapsed_seconds": item["elapsed_seconds"], "run_id": item["run_id"],
                "run_attempt": item["run_attempt"], "tested_sha": item["tested_sha"],
                "tree": item["tree"], "policy_fingerprint": bindings["policy_fingerprint"],
                "commands_sha256": item["commands_sha256"],
            })
        identities = {(str(item["run_id"]), item["run_attempt"]) for item in lane_receipts}
        run_id = lane_receipts[0]["run_id"] if len(identities) == 1 and lane_receipts else None
        run_attempt = lane_receipts[0]["run_attempt"] if len(identities) == 1 and lane_receipts else None
        parent_id = parent_prd.get("id") if parent_prd_valid else None
        parent_sha = parent_prd.get("sha256") if parent_prd_valid else None
        enforced = plan.get("enforced", {})
        required_lanes = (enforced.get("selected_lanes")
                          if isinstance(enforced, dict) else None)
        if required_lanes is None:
            # Compatibility for local fixture plans; real PR1 plans always
            # carry the trusted selector result in `enforced`.
            required_lanes = plan.get("selected_lanes", [])
        lane_map_complete = (isinstance(required_lanes, list) and bool(required_lanes)
                             and set(by_lane) >= set(required_lanes)
                             and all(by_lane[lane]["commands_sha256"] for lane in required_lanes
                                     if lane in by_lane))
        observation = {
            "schema": SCHEMA, "kind": "pr_shadow_observation",
            "pr_number": pr_number, "run_id": run_id, "run_attempt": run_attempt,
            "baseline_sha": bindings["baseline_sha"], "baseline_tree": bindings["baseline_tree"],
            "source_sha": bindings["head_sha"], "tree": bindings["head_tree"],
            "policy_fingerprint": bindings["policy_fingerprint"],
            "parent_prd_id": parent_id, "parent_prd_sha": parent_sha,
            "original_ci_result": (original_gate_result if original_gate_result in {"success", "failure"}
                                    else "unknown"),
            "candidate_result": candidate_result,
            "required_lanes": sorted(set(required_lanes)) if isinstance(required_lanes, list) else [],
            "complete": bool(lane_map_complete), "lane_receipts": lane_receipts,
        }
        observation["receipt_id"] = _json_digest(observation)
        trial_observation = observation

    return {
        "schema": SCHEMA,
        **bindings,
        "observed_mode": plan.get("observed_mode", "shadow"),
        "parent_prd": parent_prd if parent_prd_valid else None,
        "graph_fingerprint": graph.get("graph_fingerprint"),
        "required_packages": required_packages,
        "package_test_inventory": {
            package: {"test_files": package_files[package]["test_files"],
                      "executed_top_level_tests": (len(backend["top_level_tests"].get(package, []))
                                                   if backend else 0),
                      "execution_kind": package_execution_status.get(package, "not_verified")}
            for package in required_packages
        },
        "required_tests": required_tests,
        "package_measurements": package_measurements,
        "lanes": [by_lane[name] for name in sorted(by_lane)],
        "unknown_skips": unknown_skips,
        "unselected_skips": unselected_skips,
        "stage_owned_skips": stage_owned_skips,
        "stage_receipt_required": bool(stage_owned_skips),
        "required_results_passed": candidate_result == "success",
        "original_ci_lane_failures": sorted(original_lane_failures),
        "candidate_result": candidate_result,
        "trial_observation": trial_observation,
        "measurement_eligible": not problems and not allow_local,
        "evidence_kind": "local_only" if allow_local else "ci_receipts",
        "problems": sorted(set(problems)),
    }


def _command_digest(commands: Any) -> str | None:
    if (not isinstance(commands, list) or not commands
            or any(not isinstance(command, list) or not command
                   or any(not isinstance(part, str) for part in command) for command in commands)):
        return None
    payload = json.dumps(commands, sort_keys=True, separators=(",", ":")).encode()
    return hashlib.sha256(payload).hexdigest()


def _json_digest(value: Any) -> str:
    payload = json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode()
    return hashlib.sha256(payload).hexdigest()


def _verified_quality_lane_receipt(run_path: Path) -> tuple[dict[str, Any], dict[str, Any]]:
    """Read one actual quality_lanes run.json and its adjacent test log."""
    run = json.loads(run_path.read_text(encoding="utf-8"))
    receipt = run.get("execution_receipt") if isinstance(run, dict) else None
    if not isinstance(receipt, dict) or receipt.get("kind") != "canonical_ci_lane_execution":
        raise ShadowEvidenceError(f"{run_path} has no canonical CI execution receipt")
    commands = run.get("commands")
    if (run.get("lane") != "backend" or run.get("result") != "success" or run.get("exit_code") != 0
            or receipt.get("conclusion") != "success"
            or receipt.get("workflow_path") != ".github/workflows/ci.yml"
            or receipt.get("commands_sha256") != _command_digest(commands)
            or receipt.get("tested_sha") != run.get("tested_sha")
            or receipt.get("tree") != run.get("tree")
            or receipt.get("policy_fingerprint") != run.get("policy_fingerprint")
            or receipt.get("run_id") != run.get("run_id")
            or receipt.get("run_attempt") != run.get("run_attempt")
            or receipt.get("elapsed_seconds") != run.get("elapsed_seconds")):
        raise ShadowEvidenceError(f"{run_path} does not match its successful backend lane receipt")
    log_name = run.get("go_json_log")
    log_path = run_path.parent / log_name if isinstance(log_name, str) else None
    if (log_path is None or not log_path.is_file()
            or hashlib.sha256(log_path.read_bytes()).hexdigest() != receipt.get("test_json_sha256")):
        raise ShadowEvidenceError(f"{run_path} Go JSON log is missing or does not match its receipt")
    return run, receipt


def _parse_ci_timestamp(value: Any, field: str) -> datetime:
    if not isinstance(value, str) or not value:
        raise ShadowEvidenceError(f"GitHub CI jobs receipt is missing {field}")
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError as error:
        raise ShadowEvidenceError(f"GitHub CI jobs receipt has invalid {field}") from error
    if parsed.tzinfo is None:
        raise ShadowEvidenceError(f"GitHub CI jobs receipt {field} has no timezone")
    return parsed


def _github_api_json(path: str) -> dict[str, Any]:
    """Read one GitHub REST resource through the user's existing gh auth."""
    result = subprocess.run(["gh", "api", path], stdout=subprocess.PIPE,
                            stderr=subprocess.DEVNULL, check=False, timeout=60)
    if result.returncode:
        raise ShadowEvidenceError("GitHub Actions history read failed")
    try:
        value = json.loads(result.stdout)
    except json.JSONDecodeError as error:
        raise ShadowEvidenceError("GitHub Actions history response is malformed") from error
    if not isinstance(value, dict):
        raise ShadowEvidenceError("GitHub Actions history response is not an object")
    return value


def _github_api_pages(path: str, key: str) -> tuple[list[dict[str, Any]], int, int]:
    """Read every page and reject a moving/truncated API result."""
    page = 1
    total = None
    values: list[dict[str, Any]] = []
    while page <= 1000:
        separator = "&" if "?" in path else "?"
        response = _github_api_json(f"{path}{separator}per_page=100&page={page}")
        count, rows = response.get("total_count"), response.get(key)
        if (isinstance(count, bool) or not isinstance(count, int) or count < 0
                or not isinstance(rows, list)
                or any(not isinstance(row, dict) for row in rows)):
            raise ShadowEvidenceError("GitHub Actions history page has an invalid inventory")
        if total is None:
            total = count
        elif count != total:
            raise ShadowEvidenceError("GitHub Actions history changed during pagination")
        values.extend(rows)
        if len(values) >= total:
            break
        if not rows:
            raise ShadowEvidenceError("GitHub Actions history pagination ended early")
        page += 1
    if total is None or page > 1000 or len(values) != total:
        raise ShadowEvidenceError("GitHub Actions history is truncated")
    return values, total, page


def export_github_pr_ci_history(github_repo: str, pr_number: int) -> dict[str, Any]:
    """Export read-only workflow run/attempt/job history for one PR."""
    if (not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", github_repo)
            or isinstance(pr_number, bool) or not isinstance(pr_number, int) or pr_number < 1):
        raise ShadowEvidenceError("GitHub repository or PR number is invalid")
    prefix = f"repos/{github_repo}"
    pull = _github_api_json(f"{prefix}/pulls/{pr_number}")
    head = pull.get("head") if isinstance(pull.get("head"), dict) else {}
    head_ref = head.get("ref")
    if not isinstance(head_ref, str) or not head_ref:
        raise ShadowEvidenceError("GitHub PR head branch is unavailable")
    listed, total, page_count = _github_api_pages(
        f"{prefix}/actions/workflows/ci.yml/runs", "workflow_runs")
    list_projection = []
    matching = []
    ambiguous = []
    for item in listed:
        refs = item.get("pull_requests")
        if not isinstance(refs, list):
            raise ShadowEvidenceError("GitHub workflow run has no PR attribution inventory")
        if any(not isinstance(ref, dict) or isinstance(ref.get("number"), bool)
               or not isinstance(ref.get("number"), int) or ref["number"] < 1 for ref in refs):
            raise ShadowEvidenceError("GitHub workflow run has invalid PR attribution")
        numbers = sorted({ref["number"] for ref in refs})
        path = item.get("path")
        if not isinstance(path, str) or path.split("@", 1)[0] != ".github/workflows/ci.yml":
            continue
        run_id, sha = item.get("id"), item.get("head_sha")
        if (isinstance(run_id, bool) or not isinstance(run_id, int) or run_id < 1
                or not isinstance(sha, str) or not HEX40.fullmatch(sha)):
            raise ShadowEvidenceError("GitHub workflow run identity is invalid")
        projection = {"run_id": run_id, "path": path, "head_sha": sha,
                      "head_branch": item.get("head_branch"), "event": item.get("event"),
                      "pull_request_numbers": numbers, "latest_attempt": item.get("run_attempt")}
        list_projection.append(projection)
        if pr_number in numbers:
            matching.append((item, projection))
        elif item.get("head_branch") == head_ref:
            ambiguous.append(run_id)
    if len({item["run_id"] for item in list_projection}) != len(list_projection):
        raise ShadowEvidenceError("GitHub workflow run list contains duplicate identities")

    runs = []
    incomplete_reasons = []
    if ambiguous:
        incomplete_reasons.append("workflow runs on the PR head branch lack PR attribution")
    for item, projection in matching:
        latest = item.get("run_attempt")
        if isinstance(latest, bool) or not isinstance(latest, int) or latest < 1:
            raise ShadowEvidenceError("GitHub workflow run has an invalid attempt count")
        attempts = []
        for attempt_number in range(1, latest + 1):
            stem = f"{prefix}/actions/runs/{projection['run_id']}/attempts/{attempt_number}"
            attempt = _github_api_json(stem)
            if (attempt.get("id") != projection["run_id"]
                    or attempt.get("run_attempt") != attempt_number
                    or attempt.get("head_sha") != projection["head_sha"]):
                raise ShadowEvidenceError("GitHub workflow attempt identity does not match its run")
            jobs, jobs_count, jobs_pages = _github_api_pages(stem + "/jobs", "jobs")
            normalized_jobs = [{key: job.get(key) for key in
                                ("name", "status", "conclusion", "started_at", "completed_at")}
                               for job in jobs]
            start = attempt.get("run_started_at") or attempt.get("created_at")
            record = {
                "run_attempt": attempt_number, "status": attempt.get("status"),
                "conclusion": attempt.get("conclusion"), "head_sha": attempt.get("head_sha"),
                "created_at": attempt.get("created_at"), "started_at": start,
                "updated_at": attempt.get("updated_at"),
                "jobs_total_count": jobs_count, "jobs_fetched_count": len(jobs),
                "jobs_page_count": jobs_pages, "jobs": normalized_jobs,
                "jobs_sha256": _json_digest(normalized_jobs),
            }
            if record["status"] != "completed" or not isinstance(record["conclusion"], str):
                incomplete_reasons.append(
                    f"workflow run {projection['run_id']} attempt {attempt_number} is not terminal")
            attempts.append(record)
        runs.append({**projection, "attempts": attempts})
    if not runs:
        incomplete_reasons.append("no CI workflow runs are attributable to this PR")
    bundle = {
        "schema": 1, "kind": "github_pr_ci_history_export",
        "github_repo": github_repo, "pr_number": pr_number,
        "workflow_path": ".github/workflows/ci.yml", "pr_head_branch": head_ref,
        "run_list_total_count": total, "run_list_fetched_count": len(listed),
        "run_list_page_count": page_count,
        "run_list_sha256": _json_digest(list_projection),
        "unattributed_same_branch_run_ids": sorted(ambiguous),
        "runs": runs, "complete": not incomplete_reasons,
        "incomplete_reasons": sorted(set(incomplete_reasons)),
        "exported_at": datetime.now(timezone.utc).isoformat(),
    }
    bundle["history_sha256"] = _json_digest(bundle)
    return bundle


def _history_attempt_cost(attempt: dict[str, Any], run_id: int) -> dict[str, Any]:
    jobs = attempt.get("jobs")
    if (attempt.get("status") != "completed" or not isinstance(attempt.get("conclusion"), str)
            or not isinstance(jobs, list)
            or isinstance(attempt.get("run_attempt"), bool)
            or not isinstance(attempt.get("run_attempt"), int) or attempt["run_attempt"] < 1
            or not isinstance(attempt.get("head_sha"), str)
            or not HEX40.fullmatch(attempt["head_sha"])
            or isinstance(attempt.get("jobs_total_count"), bool)
            or not isinstance(attempt.get("jobs_total_count"), int)
            or attempt.get("jobs_total_count") != len(jobs)
            or isinstance(attempt.get("jobs_fetched_count"), bool)
            or not isinstance(attempt.get("jobs_fetched_count"), int)
            or attempt.get("jobs_fetched_count") != len(jobs)
            or not isinstance(attempt.get("jobs_sha256"), str)
            or attempt.get("jobs_sha256") != _json_digest(jobs)):
        raise ShadowEvidenceError("GitHub workflow attempt history is incomplete")
    for job in jobs:
        if not isinstance(job, dict) or not isinstance(job.get("name"), str):
            raise ShadowEvidenceError("GitHub workflow attempt contains an invalid job")
    conclusion = attempt["conclusion"]
    if conclusion == "success":
        plan = [job for job in jobs if job["name"] == "plan"]
        check = [job for job in jobs if job["name"] == "check"]
        if (len(plan) != 1 or len(check) != 1
                or plan[0].get("conclusion") != "success"
                or check[0].get("conclusion") != "success"):
            raise ShadowEvidenceError("successful workflow attempt lacks its successful plan/check jobs")
        started, ended = plan[0].get("started_at"), check[0].get("completed_at")
        start = _parse_ci_timestamp(started, "plan.started_at")
        check_start = _parse_ci_timestamp(check[0].get("started_at"), "check.started_at")
        end = _parse_ci_timestamp(ended, "check.completed_at")
        if check_start < start or end < check_start:
            raise ShadowEvidenceError("GitHub plan/check timestamps are out of order")
        seconds = _nonnegative_seconds((end - start).total_seconds(), "successful CI wall seconds")
        measure = "plan_to_required_check"
    else:
        started, ended = attempt.get("started_at") or attempt.get("created_at"), attempt.get("updated_at")
        start = _parse_ci_timestamp(started, "attempt.started_at")
        end = _parse_ci_timestamp(ended, "attempt.updated_at")
        if end < start:
            raise ShadowEvidenceError("GitHub attempt timestamps are out of order")
        seconds = _nonnegative_seconds((end - start).total_seconds(), "failed/cancelled CI wall seconds")
        measure = "attempt_start_to_terminal"
    return {
        "run_id": run_id, "run_attempt": attempt["run_attempt"],
        "head_sha": attempt["head_sha"], "status": attempt["status"],
        "conclusion": conclusion, "measure": measure,
        "started_at": started, "ended_at": ended, "elapsed_seconds": seconds,
        "jobs_total_count": attempt["jobs_total_count"],
        "jobs_fetched_count": attempt["jobs_fetched_count"],
        "jobs_sha256": attempt["jobs_sha256"], "jobs": jobs,
        "plan_conclusion": plan[0]["conclusion"] if conclusion == "success" else None,
        "check_conclusion": check[0]["conclusion"] if conclusion == "success" else None,
        "check_started_at": check[0].get("started_at") if conclusion == "success" else None,
        "check_completed_at": check[0].get("completed_at") if conclusion == "success" else None,
    }


def _github_ci_wall_receipt(ci_jobs_path: Path,
                            execution_receipt: dict[str, Any]) -> dict[str, Any]:
    """Bind all CI workflow runs and attempts attributed to this PR."""
    try:
        document = json.loads(ci_jobs_path.read_text(encoding="utf-8"))
    except json.JSONDecodeError as error:
        raise ShadowEvidenceError("GitHub CI history JSON is malformed") from error
    if not isinstance(document, dict):
        raise ShadowEvidenceError("GitHub CI history JSON is not an object")
    history_sha = document.get("history_sha256")
    unsigned = {key: value for key, value in document.items() if key != "history_sha256"}
    if (document.get("schema") != 1 or document.get("kind") != "github_pr_ci_history_export"
            or document.get("complete") is not True or document.get("incomplete_reasons") != []
            or history_sha != _json_digest(unsigned)
            or document.get("workflow_path") != ".github/workflows/ci.yml"
            or document.get("pr_number") != execution_receipt.get("pr_number")
            or document.get("run_list_total_count") != document.get("run_list_fetched_count")
            or isinstance(document.get("run_list_total_count"), bool)
            or not isinstance(document.get("run_list_total_count"), int)
            or not isinstance(document.get("run_list_sha256"), str)
            or not HEX64.fullmatch(document["run_list_sha256"])
            or document.get("unattributed_same_branch_run_ids") != []):
        raise ShadowEvidenceError("GitHub CI PR history is incomplete or unbound")
    runs = document.get("runs")
    if not isinstance(runs, list) or not runs:
        raise ShadowEvidenceError("GitHub CI PR history contains no runs")
    summaries = []
    seen = set()
    for history_run in runs:
        path = history_run.get("path") if isinstance(history_run, dict) else None
        pr_numbers = history_run.get("pull_request_numbers") if isinstance(history_run, dict) else None
        if (not isinstance(path, str)
                or path.split("@", 1)[0] != ".github/workflows/ci.yml"
                or not isinstance(pr_numbers, list)
                or any(isinstance(number, bool) or not isinstance(number, int) for number in pr_numbers)
                or execution_receipt["pr_number"] not in pr_numbers):
            raise ShadowEvidenceError("GitHub CI history contains an unattributed workflow run")
        run_id = history_run.get("run_id")
        run_head_sha = history_run.get("head_sha")
        latest = history_run.get("latest_attempt")
        attempts = history_run.get("attempts")
        if (isinstance(run_id, bool) or not isinstance(run_id, int) or run_id < 1
                or not isinstance(run_head_sha, str) or not HEX40.fullmatch(run_head_sha)
                or isinstance(latest, bool) or not isinstance(latest, int) or latest < 1
                or not isinstance(attempts, list) or len(attempts) != latest
                or run_id in seen):
            raise ShadowEvidenceError("GitHub CI history has missing or duplicate attempts")
        seen.add(run_id)
        numbers = set()
        for attempt in attempts:
            if (not isinstance(attempt, dict) or isinstance(attempt.get("run_attempt"), bool)
                    or not isinstance(attempt.get("run_attempt"), int)):
                raise ShadowEvidenceError("GitHub CI history has an invalid attempt")
            number = attempt["run_attempt"]
            if number in numbers or number < 1 or number > latest:
                raise ShadowEvidenceError("GitHub CI history has duplicate/out-of-range attempt numbers")
            numbers.add(number)
            if attempt.get("head_sha") != run_head_sha:
                raise ShadowEvidenceError("GitHub workflow attempt source disagrees with its run")
            summaries.append(_history_attempt_cost(attempt, run_id))
        if numbers != set(range(1, latest + 1)):
            raise ShadowEvidenceError("GitHub CI history omitted a workflow run attempt")
    paired = [item for item in summaries
              if str(item["run_id"]) == str(execution_receipt["run_id"])
              and item["run_attempt"] == execution_receipt["run_attempt"]]
    if len(paired) != 1 or paired[0]["conclusion"] != "success":
        raise ShadowEvidenceError("paired successful CI attempt is absent from the PR history")
    return {
        "schema": 1, "kind": "github_pr_ci_attempt_history_wall_time",
        "pr_number": execution_receipt["pr_number"],
        "workflow_path": ".github/workflows/ci.yml", "paired_run_id": execution_receipt["run_id"],
        "paired_run_attempt": execution_receipt["run_attempt"],
        # GitHub's PR run metadata identifies the PR head; the canonical lane
        # receipt identifies the merge-preview tree actually tested by CI.
        "paired_tested_sha": execution_receipt["tested_sha"],
        "paired_tree": execution_receipt["tree"],
        "paired_policy_fingerprint": execution_receipt["policy_fingerprint"],
        "paired_run_head_sha": paired[0]["head_sha"],
        "run_list_total_count": document["run_list_total_count"],
        "run_list_fetched_count": document["run_list_fetched_count"],
        "run_list_sha256": document["run_list_sha256"],
        "history_sha256": history_sha, "complete": True,
        "run_inventory": [{key: history_run[key] for key in
                           ("run_id", "path", "head_sha", "head_branch", "event",
                            "pull_request_numbers", "latest_attempt")}
                          for history_run in runs],
        "attempt_count": len(summaries), "attempts": summaries,
        "elapsed_seconds": sum(item["elapsed_seconds"] for item in summaries),
    }


def _release_state_timing(state_path: Path, manifest_path: Path, record: dict[str, Any],
                          repo: Path) -> dict[str, Any]:
    """Adapt the release controller's existing state and build manifest timings."""
    state_bytes = state_path.read_bytes()
    manifest_bytes = manifest_path.read_bytes()
    state = json.loads(state_bytes)
    manifest = json.loads(manifest_bytes)
    if not isinstance(state, dict) or not isinstance(manifest, dict):
        raise ShadowEvidenceError("domestic release state or manifest is not an object")
    source_sha = state.get("deployed_source_sha")
    processed_sha = state.get("processed_sha")
    prod_sha = state.get("prod_installed_sha")
    installed_manifest = state.get("prod_installed_manifest_sha256")
    if (state.get("status") != "ready" or not isinstance(source_sha, str) or not HEX40.fullmatch(source_sha)
            or processed_sha != source_sha
            or prod_sha != source_sha or manifest.get("source_sha") != source_sha
            or manifest.get("source_tree") != record["tree"]
            or not HEX40.fullmatch(str(manifest.get("source_tree", "")))
            or installed_manifest != manifest.get("release_files_sha256")
            or not isinstance(installed_manifest, str) or not HEX64.fullmatch(installed_manifest)):
        raise ShadowEvidenceError("domestic release state cursor/manifest does not match the installed source tree")
    phase_timings = state.get("last_release_timings_seconds")
    if not isinstance(phase_timings, dict):
        raise ShadowEvidenceError("domestic release state has no phase_timings_seconds")
    try:
        elapsed = _safe_seconds(phase_timings.get("total"), "domestic release total seconds")
    except ShadowEvidenceError as error:
        raise ShadowEvidenceError("domestic release total timing is missing") from error
    for name, value in phase_timings.items():
        if (not isinstance(name, str) or isinstance(value, bool)
                or not isinstance(value, (int, float)) or value < 0):
            raise ShadowEvidenceError("domestic release phase timing is malformed")
    try:
        source_prd = subprocess.check_output(
            ["git", "show", f"{source_sha}:docs/prd/2026-09-24-small-step-impact-checks.md"],
            cwd=repo, stderr=subprocess.DEVNULL)
    except subprocess.SubprocessError as error:
        raise ShadowEvidenceError("domestic release parent PRD source is unavailable") from error
    if hashlib.sha256(source_prd).hexdigest() != record["parent_prd_sha"]:
        raise ShadowEvidenceError("domestic release parent PRD differs from the timing record")
    state_snapshot = {
        key: state.get(key) for key in (
            "schema_version", "status", "processed_sha", "deployed_source_sha", "prod_installed_sha",
            "prod_installed_manifest_sha256", "last_release_timings_seconds")
    }
    manifest_snapshot = {
        key: manifest.get(key) for key in (
            "schema_version", "source_sha", "source_tree", "release_files_sha256", "build_mode",
            "phase_timings_seconds")
    }
    identity = {key: record[key] for key in (
        "parent_prd_id", "parent_prd_sha", "pr_number", "source_sha", "tree",
        "policy_fingerprint")}
    identity["capability_id"] = record["scope_id"]
    deployment = {
        "schema": 1, "kind": "domestic_release_state_timing", "conclusion": "success",
        **identity, "elapsed_seconds": elapsed,
        "parent_prd_source_sha256": hashlib.sha256(source_prd).hexdigest(),
        "state_path": str(state_path.resolve()),
        "state_file_sha256": hashlib.sha256(state_bytes).hexdigest(),
        "state_snapshot": state_snapshot, "state_snapshot_sha256": _json_digest(state_snapshot),
        "manifest_path": str(manifest_path.resolve()),
        "manifest_file_sha256": hashlib.sha256(manifest_bytes).hexdigest(),
        "manifest_snapshot": manifest_snapshot,
        "manifest_snapshot_sha256": _json_digest(manifest_snapshot),
        "phase_timings_seconds": phase_timings,
        "installed_source_sha": source_sha, "installed_manifest_sha256": installed_manifest,
        "deployment_id": _json_digest({
            "state_file_sha256": hashlib.sha256(state_bytes).hexdigest(),
            "manifest_file_sha256": hashlib.sha256(manifest_bytes).hexdigest(), **identity}),
    }
    return deployment


def benchmark_records_from_lane_runs(full_run_path: Path, targeted_run_path: Path,
                                     scope_type: str, scope_id: str,
                                     release_state_path: Path | None = None,
                                     release_manifest_path: Path | None = None,
                                     repo: Path | None = None,
                                     ci_jobs_path: Path | None = None) -> list[dict[str, Any]]:
    """Build paired records from the actual quality_lanes receipts and logs.

    Capability records additionally adapt the complete plan-to-required-check
    wall time from a same-run GitHub jobs export and an existing ready release
    state. Backend lane time remains the class-level command measurement only.
    """
    if scope_type not in {"class", "capability"} or not isinstance(scope_id, str) or not scope_id:
        raise ShadowEvidenceError("benchmark scope is invalid")
    full_run, full_receipt = _verified_quality_lane_receipt(full_run_path)
    target_run, target_receipt = _verified_quality_lane_receipt(targeted_run_path)
    identity_fields = ("pr_number", "run_id", "run_attempt", "tested_sha", "tree",
                       "policy_fingerprint", "parent_prd_id", "parent_prd_sha",
                       "environment_fingerprint", "cache_fingerprint")
    if any(full_receipt.get(key) != target_receipt.get(key) for key in identity_fields):
        raise ShadowEvidenceError("full and targeted lane receipts do not share one PR/run/source/environment/cache")
    if full_receipt.get("includes_setup") is not True or target_receipt.get("includes_setup") is not True:
        raise ShadowEvidenceError("paired quality lane receipt omits lane setup commands")
    test_commands = lambda run: [command for command in run["commands"]
                                 if "go" in command and "test" in command]
    full_tests, target_tests = test_commands(full_run), test_commands(target_run)
    if not full_tests or not any("./..." in command for command in full_tests):
        raise ShadowEvidenceError("full backend receipt did not execute go test ./...")
    target_dirs = sorted({part for command in target_tests for part in command
                          if isinstance(part, str) and part.startswith("./") and part != "./..."})
    if (not target_tests or any("./..." in command for command in target_tests)
            or not target_dirs):
        raise ShadowEvidenceError("targeted backend receipt did not execute bounded package tests")
    parent_id = target_receipt.get("parent_prd_id")
    parent_sha = target_receipt.get("parent_prd_sha")
    if (not isinstance(parent_id, str) or not parent_id
            or not isinstance(parent_sha, str) or not HEX64.fullmatch(parent_sha)):
        raise ShadowEvidenceError("quality lane receipt has no parent PRD binding")
    pair_id = _json_digest({"full": full_receipt, "targeted": target_receipt,
                            "scope_type": scope_type, "scope_id": scope_id})
    common = {
        "evidence_kind": "real_command", "pair_id": pair_id,
        "scope_type": scope_type, "scope_id": scope_id,
        "source_sha": target_receipt["tested_sha"], "tree": target_receipt["tree"],
        "environment_fingerprint": target_receipt["environment_fingerprint"],
        "cache_fingerprint": target_receipt["cache_fingerprint"],
        "policy_fingerprint": target_receipt["policy_fingerprint"],
        "parent_prd_id": parent_id, "parent_prd_sha": parent_sha,
        "pr_number": target_receipt["pr_number"], "run_id": target_receipt["run_id"],
        "run_attempt": target_receipt["run_attempt"], "exit_code": 0,
    }
    full_record = {**common, "mode": "full", "commands": full_run["commands"],
                   "elapsed_seconds": full_receipt["elapsed_seconds"],
                   "execution_receipt": full_receipt}
    targeted_record = {**common, "mode": "targeted", "commands": target_run["commands"],
                       "elapsed_seconds": target_receipt["elapsed_seconds"],
                       "execution_receipt": target_receipt,
                       "selected_package_dirs": target_dirs}
    if scope_type == "capability":
        if release_state_path is None or release_manifest_path is None:
            raise ShadowEvidenceError("capability benchmark requires --release-state and --release-manifest")
        if ci_jobs_path is None:
            raise ShadowEvidenceError("capability benchmark requires --ci-jobs for complete CI wall time")
        ci_wall_receipt = _github_ci_wall_receipt(ci_jobs_path, target_receipt)
        target_record_identity = {**targeted_record}
        target_record_identity["scope_id"] = scope_id
        deployment = _release_state_timing(release_state_path, release_manifest_path,
                                           target_record_identity, repo or Path.cwd())
        deploy_seconds = float(deployment["elapsed_seconds"])
        ci_seconds = float(ci_wall_receipt["elapsed_seconds"])
        targeted_record["ci_wall_receipt"] = ci_wall_receipt
        targeted_record["ci_wall_seconds"] = ci_seconds
        targeted_record["capability_cost_receipt"] = {
            "schema": 1, "metric": "ci_plus_deploy_total_seconds",
            "capability_id": scope_id,
            "parent_prd_id": parent_id, "parent_prd_sha": parent_sha,
            "pr_number": targeted_record["pr_number"], "source_sha": targeted_record["source_sha"],
            "tree": targeted_record["tree"], "policy_fingerprint": targeted_record["policy_fingerprint"],
            "ci_run_id": targeted_record["run_id"], "ci_run_attempt": targeted_record["run_attempt"],
            "ci_seconds": ci_seconds, "backend_lane_seconds": targeted_record["elapsed_seconds"],
            "deployment_seconds": deploy_seconds,
            "total_seconds": ci_seconds + deploy_seconds,
            "ci_receipt_sha256": _json_digest({
                "backend_execution_receipt": target_receipt,
                "ci_wall_receipt": ci_wall_receipt,
            }),
            "deployment_receipt": deployment,
        }
    return [full_record, targeted_record]


def _valid_ci_wall_receipt(record: dict[str, Any]) -> float | None:
    receipt = record.get("ci_wall_receipt")
    if (not isinstance(receipt, dict) or receipt.get("schema") != 1
            or receipt.get("kind") != "github_pr_ci_attempt_history_wall_time"
            or receipt.get("pr_number") != record.get("pr_number")
            or str(receipt.get("paired_run_id")) != str(record.get("run_id"))
            or receipt.get("paired_run_attempt") != record.get("run_attempt")
            or receipt.get("paired_tested_sha") != record.get("source_sha")
            or receipt.get("paired_tree") != record.get("tree")
            or receipt.get("paired_policy_fingerprint") != record.get("policy_fingerprint")
            or receipt.get("complete") is not True
            or isinstance(receipt.get("run_list_total_count"), bool)
            or not isinstance(receipt.get("run_list_total_count"), int)
            or receipt.get("run_list_total_count") != receipt.get("run_list_fetched_count")
            or not isinstance(receipt.get("run_list_sha256"), str)
            or not HEX64.fullmatch(receipt["run_list_sha256"])
            or not isinstance(receipt.get("history_sha256"), str)
            or not HEX64.fullmatch(receipt["history_sha256"])):
        return None
    try:
        attempts = receipt.get("attempts")
        run_inventory = receipt.get("run_inventory")
        if (not isinstance(attempts, list) or not attempts
                or isinstance(receipt.get("attempt_count"), bool)
                or receipt.get("attempt_count") != len(attempts)
                or not isinstance(run_inventory, list) or not run_inventory):
            return None
        inventory_by_id = {}
        for row in run_inventory:
            row_path = row.get("path") if isinstance(row, dict) else None
            pr_numbers = row.get("pull_request_numbers") if isinstance(row, dict) else None
            if (not isinstance(row_path, str)
                    or row_path.split("@", 1)[0] != ".github/workflows/ci.yml"
                    or not isinstance(row.get("head_sha"), str)
                    or not HEX40.fullmatch(row["head_sha"])
                    or not isinstance(pr_numbers, list)
                    or any(isinstance(number, bool) or not isinstance(number, int) for number in pr_numbers)
                    or record["pr_number"] not in pr_numbers
                    or isinstance(row.get("run_id"), bool)
                    or not isinstance(row.get("run_id"), int) or row["run_id"] < 1
                    or isinstance(row.get("latest_attempt"), bool)
                    or not isinstance(row.get("latest_attempt"), int) or row["latest_attempt"] < 1
                    or row["run_id"] in inventory_by_id):
                return None
            inventory_by_id[row["run_id"]] = row
        computed, seen, paired = [], set(), []
        attempts_by_run: dict[int, set[int]] = {run_id: set() for run_id in inventory_by_id}
        for item in attempts:
            if not isinstance(item, dict):
                return None
            attempt = {
                "run_attempt": item.get("run_attempt"), "status": item.get("status"),
                "conclusion": item.get("conclusion"), "head_sha": item.get("head_sha"),
                "started_at": item.get("started_at"), "created_at": item.get("started_at"),
                "updated_at": item.get("ended_at"), "jobs": item.get("jobs"),
                "jobs_total_count": item.get("jobs_total_count"),
                "jobs_fetched_count": item.get("jobs_fetched_count"),
                "jobs_sha256": item.get("jobs_sha256"),
            }
            run_id = item.get("run_id")
            if isinstance(run_id, bool) or not isinstance(run_id, int) or run_id < 1:
                return None
            key = (run_id, attempt["run_attempt"])
            inventory = inventory_by_id.get(run_id)
            if (key in seen or inventory is None
                    or attempt["head_sha"] != inventory["head_sha"]):
                return None
            seen.add(key)
            attempts_by_run[run_id].add(attempt["run_attempt"])
            recalculated = _history_attempt_cost(attempt, run_id)
            if recalculated != item:
                return None
            computed.append(recalculated["elapsed_seconds"])
            if (str(key[0]) == str(receipt["paired_run_id"])
                    and key[1] == receipt["paired_run_attempt"]):
                paired.append(recalculated)
        if any(numbers != set(range(1, inventory_by_id[run_id]["latest_attempt"] + 1))
               for run_id, numbers in attempts_by_run.items()):
            return None
        elapsed = _nonnegative_seconds(sum(computed), "complete PR CI history seconds")
    except ShadowEvidenceError:
        return None
    paired_row = next((row for run_id, row in inventory_by_id.items()
                       if str(run_id) == str(receipt.get("paired_run_id"))), None)
    if (len(paired) != 1 or paired[0]["conclusion"] != "success"
            or paired_row is None or paired[0]["head_sha"] != receipt.get("paired_run_head_sha")
            or paired[0]["head_sha"] != paired_row["head_sha"]):
        return None
    try:
        recorded_seconds = _nonnegative_seconds(record.get("ci_wall_seconds"), "recorded CI wall seconds")
    except ShadowEvidenceError:
        return None
    return elapsed if abs(recorded_seconds - elapsed) <= 0.001 else None


def _valid_capability_cost_receipt(record: dict[str, Any], elapsed_seconds: float,
                                   execution_receipt: dict[str, Any]) -> dict[str, float] | None:
    """Require bound CI and deployment measurements for candidate capability cost."""
    cost = record.get("capability_cost_receipt")
    if not isinstance(cost, dict) or cost.get("schema") != 1 \
            or cost.get("metric") != "ci_plus_deploy_total_seconds":
        return None
    identity = {
        "capability_id": record["scope_id"],
        "parent_prd_id": record["parent_prd_id"], "parent_prd_sha": record["parent_prd_sha"],
        "pr_number": record["pr_number"], "source_sha": record["source_sha"], "tree": record["tree"],
        "policy_fingerprint": record["policy_fingerprint"],
    }
    if any(cost.get(key) != value for key, value in identity.items()):
        return None
    try:
        ci_seconds = _nonnegative_seconds(cost.get("ci_seconds"), "capability CI seconds")
        backend_seconds = _safe_seconds(cost.get("backend_lane_seconds"), "capability backend lane seconds")
        deploy_seconds = _safe_seconds(cost.get("deployment_seconds"), "capability deployment seconds")
        total_seconds = _safe_seconds(cost.get("total_seconds"), "capability total seconds")
    except ShadowEvidenceError:
        return None
    ci_wall_seconds = _valid_ci_wall_receipt(record)
    if (cost.get("ci_run_id") != record["run_id"]
            or cost.get("ci_run_attempt") != record["run_attempt"]
            or ci_wall_seconds is None or abs(ci_seconds - ci_wall_seconds) > 0.001
            or abs(backend_seconds - elapsed_seconds) > 0.001
            or abs(total_seconds - (ci_seconds + deploy_seconds)) > 0.001
            or cost.get("ci_receipt_sha256") != _json_digest({
                "backend_execution_receipt": execution_receipt,
                "ci_wall_receipt": record["ci_wall_receipt"],
            })):
        return None
    deployment = cost.get("deployment_receipt")
    if not isinstance(deployment, dict):
        return None
    deployment_identity = {
        "capability_id": record["scope_id"], "parent_prd_id": record["parent_prd_id"],
        "parent_prd_sha": record["parent_prd_sha"], "pr_number": record["pr_number"],
        "source_sha": record["source_sha"], "tree": record["tree"],
        "policy_fingerprint": record["policy_fingerprint"],
    }
    state_snapshot = deployment.get("state_snapshot")
    manifest_snapshot = deployment.get("manifest_snapshot")
    if (deployment.get("schema") != 1
            or deployment.get("kind") != "domestic_release_state_timing"
            or deployment.get("conclusion") != "success"
            or any(deployment.get(key) != value for key, value in deployment_identity.items())
            or deployment.get("elapsed_seconds") != deploy_seconds
            or deployment.get("parent_prd_source_sha256") != record["parent_prd_sha"]
            or not isinstance(state_snapshot, dict) or not isinstance(manifest_snapshot, dict)
            or deployment.get("state_snapshot_sha256") != _json_digest(state_snapshot)
            or deployment.get("manifest_snapshot_sha256") != _json_digest(manifest_snapshot)):
        return None
    state_timings = state_snapshot.get("last_release_timings_seconds")
    if (state_snapshot.get("status") != "ready"
            or state_snapshot.get("processed_sha") != deployment.get("installed_source_sha")
            or state_snapshot.get("deployed_source_sha") != deployment.get("installed_source_sha")
            or state_snapshot.get("prod_installed_sha") != deployment.get("installed_source_sha")
            or state_snapshot.get("prod_installed_manifest_sha256") != deployment.get("installed_manifest_sha256")
            or manifest_snapshot.get("source_sha") != deployment.get("installed_source_sha")
            or manifest_snapshot.get("source_tree") != record["tree"]
            or manifest_snapshot.get("release_files_sha256") != deployment.get("installed_manifest_sha256")
            or state_timings != deployment.get("phase_timings_seconds")
            or not isinstance(state_timings, dict) or state_timings.get("total") != deploy_seconds
            or not isinstance(deployment.get("state_path"), str)
            or not isinstance(deployment.get("manifest_path"), str)
            or not HEX64.fullmatch(str(deployment.get("state_file_sha256", "")))
            or not HEX64.fullmatch(str(deployment.get("manifest_file_sha256", "")))
            or not HEX64.fullmatch(str(deployment.get("installed_manifest_sha256", "")))):
        return None
    expected_deployment_id = _json_digest({
        "state_file_sha256": deployment["state_file_sha256"],
        "manifest_file_sha256": deployment["manifest_file_sha256"],
        **deployment_identity,
    })
    if deployment.get("deployment_id") != expected_deployment_id:
        return None
    return {"ci_seconds": ci_seconds, "deployment_seconds": deploy_seconds,
            "total_seconds": total_seconds}


def _valid_execution_record(record: dict[str, Any]) -> bool:
    if record.get("evidence_kind") != "real_command" or record.get("exit_code") != 0:
        return False
    if record.get("scope_type") not in {"class", "capability"} or record.get("mode") not in {"full", "targeted"}:
        return False
    if not all(isinstance(record.get(key), str) and record[key] for key in (
            "pair_id", "scope_id", "source_sha", "tree", "environment_fingerprint",
            "cache_fingerprint", "policy_fingerprint", "parent_prd_id", "parent_prd_sha")):
        return False
    if (not HEX40.fullmatch(record["source_sha"]) or not HEX40.fullmatch(record["tree"])
            or not HEX64.fullmatch(record["environment_fingerprint"])
            or not HEX64.fullmatch(record["cache_fingerprint"])
            or not HEX64.fullmatch(record["policy_fingerprint"])
            or not HEX64.fullmatch(record["parent_prd_sha"])):
        return False
    if (isinstance(record.get("pr_number"), bool) or not isinstance(record.get("pr_number"), int)
            or record["pr_number"] < 1 or isinstance(record.get("run_id"), bool)
            or not isinstance(record.get("run_id"), (str, int)) or not str(record["run_id"])
            or isinstance(record.get("run_attempt"), bool) or not isinstance(record.get("run_attempt"), int)
            or record["run_attempt"] < 1):
        return False
    try:
        seconds = _safe_seconds(record.get("elapsed_seconds"), "benchmark elapsed_seconds")
    except ShadowEvidenceError:
        return False
    commands = record.get("commands")
    digest = _command_digest(commands)
    if digest is None:
        return False
    receipt = record.get("execution_receipt")
    if not isinstance(receipt, dict):
        return False
    expected = {
        "schema": 1, "kind": "canonical_ci_lane_execution",
        "workflow_path": ".github/workflows/ci.yml", "conclusion": "success",
        "pr_number": record["pr_number"], "run_id": record["run_id"],
        "run_attempt": record["run_attempt"], "tested_sha": record["source_sha"],
        "tree": record["tree"], "policy_fingerprint": record["policy_fingerprint"],
        "parent_prd_id": record["parent_prd_id"], "parent_prd_sha": record["parent_prd_sha"],
        "commands_sha256": digest, "elapsed_seconds": seconds,
        "includes_setup": True, "includes_vet": True, "includes_test_suite": True,
    }
    if any(receipt.get(key) != value for key, value in expected.items()):
        return False
    if not isinstance(receipt.get("test_json_sha256"), str) or not HEX64.fullmatch(receipt["test_json_sha256"]):
        return False
    if record["scope_type"] == "capability" and record["mode"] == "targeted":
        if _valid_capability_cost_receipt(record, seconds, receipt) is None:
            return False
    go_test_commands = [command for command in commands
                        if "go" in command and "test" in command]
    if not go_test_commands:
        return False
    if any({part.split("=", 1)[0] for part in command} & {"-run", "-skip", "-list"}
           for command in go_test_commands):
        return False
    if not any("-race" in command and "-count=1" in command for command in go_test_commands):
        return False
    if record["mode"] == "full" and not any("./..." in command for command in go_test_commands):
        return False
    if record["mode"] == "targeted":
        package_dirs = record.get("selected_package_dirs")
        if (not isinstance(package_dirs, list) or not package_dirs
                or any(not isinstance(path, str) or not path or ".." in Path(path).parts
                       for path in package_dirs)):
            return False
        targeted = [command for command in go_test_commands if not any("./..." == part for part in command)]
        if not targeted or any(not any(path in command for path in package_dirs) for command in targeted):
            return False
    return True


def _valid_defect_replay(record: dict[str, Any], parent_prd_id: str, parent_prd_sha: str) -> bool:
    if not isinstance(record, dict) or record.get("parent_prd_id") != parent_prd_id \
            or record.get("parent_prd_sha") != parent_prd_sha:
        return False
    if (not isinstance(record.get("defect_id"), str) or not record["defect_id"]
            or not isinstance(record.get("package"), str) or not isinstance(record.get("test"), str)
            or not isinstance(record.get("selected_packages"), list)
            or record["package"] not in record["selected_packages"]):
        return False
    vulnerable, fixed = record.get("vulnerable"), record.get("fixed")
    if not isinstance(vulnerable, dict) or not isinstance(fixed, dict):
        return False
    for side, expected in ((vulnerable, "fail"), (fixed, "pass")):
        if (side.get("test_action") != expected or not HEX40.fullmatch(str(side.get("source_sha", "")))
                or not HEX40.fullmatch(str(side.get("tree", "")))
                or not isinstance(side.get("run_id"), (int, str)) or not str(side.get("run_id"))
                or isinstance(side.get("run_attempt"), bool) or not isinstance(side.get("run_attempt"), int)
                or side.get("run_attempt", 0) < 1):
            return False
        receipt = side.get("execution_receipt")
        if (not isinstance(receipt, dict) or receipt.get("conclusion") != expected
                or receipt.get("tested_sha") != side.get("source_sha")
                or receipt.get("tree") != side.get("tree")
                or receipt.get("run_id") != side.get("run_id")
                or receipt.get("run_attempt") != side.get("run_attempt")
                or not isinstance(receipt.get("command_id"), str) or not receipt.get("command_id")):
            return False
    return (vulnerable["source_sha"] != fixed["source_sha"]
            and vulnerable["tree"] != fixed["tree"]
            and vulnerable["execution_receipt"]["command_id"]
            != fixed["execution_receipt"]["command_id"])


def _trial_observation_problem(record: Any, parent_prd_id: str | None,
                               parent_prd_sha: str | None) -> str | None:
    """Validate a collected PR observation without requiring paired timings."""
    if not isinstance(record, dict) or record.get("schema") != SCHEMA \
            or record.get("kind") != "pr_shadow_observation":
        return "malformed PR shadow observation"
    if (isinstance(record.get("pr_number"), bool) or not isinstance(record.get("pr_number"), int)
            or record["pr_number"] < 1 or isinstance(record.get("run_id"), bool)
            or not isinstance(record.get("run_id"), (str, int)) or not str(record["run_id"])
            or isinstance(record.get("run_attempt"), bool)
            or not isinstance(record.get("run_attempt"), int) or record["run_attempt"] < 1):
        return "observation is missing a PR/run/attempt identity"
    for key in ("baseline_sha", "baseline_tree", "source_sha", "tree"):
        if not isinstance(record.get(key), str) or not HEX40.fullmatch(record[key]):
            return "observation has an invalid source binding"
    if (not isinstance(record.get("policy_fingerprint"), str)
            or not HEX64.fullmatch(record["policy_fingerprint"])):
        return "observation has an invalid policy fingerprint"
    if (record.get("parent_prd_id") != parent_prd_id or record.get("parent_prd_sha") != parent_prd_sha
            or not isinstance(parent_prd_id, str) or not parent_prd_id
            or not isinstance(parent_prd_sha, str) or not HEX64.fullmatch(parent_prd_sha)):
        return "observation belongs to an unknown or different parent PRD"
    if record.get("original_ci_result") not in {"success", "failure"}:
        return "observation has an unknown original CI result"
    if record.get("candidate_result") not in {"success", "failure", "unknown"}:
        return "observation has an invalid candidate result"
    required_lanes = record.get("required_lanes")
    lane_receipts = record.get("lane_receipts")
    if (not isinstance(required_lanes, list) or not required_lanes
            or any(not isinstance(lane, str) or not lane for lane in required_lanes)
            or len(set(required_lanes)) != len(required_lanes)
            or not isinstance(lane_receipts, list)):
        return "observation has no trusted enforced lane inventory"
    by_lane = {}
    for receipt in lane_receipts:
        if not isinstance(receipt, dict):
            return "observation contains a malformed lane receipt"
        lane = receipt.get("lane")
        if lane not in required_lanes or lane in by_lane:
            return "observation lane receipts do not match enforced selection"
        if (receipt.get("run_id") != record["run_id"]
                or receipt.get("run_attempt") != record["run_attempt"]
                or receipt.get("tested_sha") != record["source_sha"]
                or receipt.get("tree") != record["tree"]
                or receipt.get("policy_fingerprint") != record["policy_fingerprint"]
                or receipt.get("result") not in {"success", "failed"}):
            return "observation lane receipt binding mismatch"
        code = receipt.get("exit_code")
        if (isinstance(code, bool) or not isinstance(code, int)
                or (receipt["result"] == "success" and code != 0)
                or (receipt["result"] == "failed" and code == 0)):
            return "observation lane result does not match its exit code"
        try:
            _safe_seconds(receipt.get("elapsed_seconds"), "observation lane elapsed_seconds")
        except ShadowEvidenceError:
            return "observation lane timing is missing"
        digest = receipt.get("commands_sha256")
        if not isinstance(digest, str) or not HEX64.fullmatch(digest):
            return "observation lane has no executed command receipt"
        by_lane[lane] = receipt
    if set(by_lane) != set(required_lanes) or record.get("complete") is not True:
        return "observation is missing an enforced lane receipt"
    unsigned = {key: value for key, value in record.items() if key != "receipt_id"}
    if record.get("receipt_id") != _json_digest(unsigned):
        return "observation receipt digest mismatch"
    return None


def _capability_pr_inventory(value: Any, parent_prd_id: str | None,
                             parent_prd_sha: str | None) -> tuple[list[str], dict[str, set[int]], list[str]]:
    """Validate business-capability PR membership separately from code owners."""
    if value == []:
        return [], {}, []
    if (not isinstance(value, dict) or value.get("schema") != 1
            or value.get("parent_prd_id") != parent_prd_id
            or value.get("parent_prd_sha") != parent_prd_sha
            or not isinstance(value.get("capabilities"), list)):
        return [], {}, ["capability cost inventory is missing or not bound to the parent PRD"]
    ids: list[str] = []
    pr_map: dict[str, set[int]] = {}
    errors = []
    for entry in value["capabilities"]:
        if not isinstance(entry, dict) or not isinstance(entry.get("id"), str) or not entry["id"]:
            errors.append("capability cost inventory has an invalid business capability")
            continue
        capability = entry["id"]
        if capability in pr_map:
            errors.append("capability cost inventory has duplicate business capability ids")
            continue
        ids.append(capability)
        numbers = entry.get("pr_numbers")
        if (not isinstance(numbers, list) or not numbers
                or any(isinstance(number, bool) or not isinstance(number, int) or number < 1
                       for number in numbers)
                or len(set(numbers)) != len(numbers)):
            errors.append("business capability has a missing or invalid PR inventory: " + capability)
            pr_map[capability] = set()
        else:
            pr_map[capability] = set(numbers)
    return sorted(ids), pr_map, errors


def evaluate_benchmarks(
    records: list[dict[str, Any]],
    trial_classes: list[str],
    capabilities: Any,
    capability_baselines: dict[str, Any] | None = None,
    known_defect_replays: list[dict[str, Any]] | None = None,
    expected_defect_ids: list[str] | None = None,
    parent_prd_id: str | None = None,
    parent_prd_sha: str | None = None,
    minimum_trial_prs: int = 10,
    minimum_class_prs: int = 3,
    required_savings: float = MIN_SAVINGS,
    observations: list[dict[str, Any]] | None = None,
) -> dict[str, Any]:
    """Evaluate PR observations, verified timing pairs, capability cost, and defect replay.

    A package duration, boolean claim, repeated pair from one PR, or hand-built
    record without a matching canonical CI execution receipt cannot enable a
    fast path. Ten observations are counted independently from the smaller set
    of PRs with real paired timing commands.
    """
    grouped: dict[tuple[str, str, str], dict[str, dict[str, Any]]] = {}
    rejected: list[str] = []
    invalid_capability_evidence: set[str] = set()
    capability_ids, capability_pr_numbers, inventory_errors = _capability_pr_inventory(
        capabilities, parent_prd_id, parent_prd_sha)
    rejected.extend(inventory_errors)

    def invalidate_capability(record: Any) -> None:
        if (isinstance(record, dict) and record.get("scope_type") == "capability"
                and isinstance(record.get("scope_id"), str) and record["scope_id"]):
            invalid_capability_evidence.add(record["scope_id"])

    valid_observations: dict[tuple[str, str, int], dict[str, Any]] = {}
    observation_by_pr: dict[int, dict[str, Any]] = {}
    conflicting_observation_prs: set[int] = set()
    unknown_observation_count = 0
    confirmed_misses = []
    for observation in observations or []:
        problem = _trial_observation_problem(observation, parent_prd_id, parent_prd_sha)
        if problem:
            rejected.append(problem)
            continue
        identity = (observation["pr_number"], str(observation["run_id"]), observation["run_attempt"])
        previous = valid_observations.get(identity)
        if previous is not None:
            if previous["receipt_id"] == observation["receipt_id"]:
                continue
            rejected.append("conflicting duplicate PR/run observation")
            conflicting_observation_prs.add(observation["pr_number"])
            continue
        valid_observations[identity] = observation
        prior_pr_observation = observation_by_pr.get(observation["pr_number"])
        if prior_pr_observation is None:
            observation_by_pr[observation["pr_number"]] = observation
        elif any(prior_pr_observation.get(key) != observation.get(key)
                 for key in ("source_sha", "tree", "policy_fingerprint", "parent_prd_id", "parent_prd_sha")):
            rejected.append("one PR has conflicting source-bound trial observations")
            conflicting_observation_prs.add(observation["pr_number"])
        if observation["candidate_result"] == "unknown":
            unknown_observation_count += 1
        elif (observation["original_ci_result"] == "failure"
              and observation["candidate_result"] == "success"):
            confirmed_misses.append({"pr_number": observation["pr_number"],
                                     "source_sha": observation["source_sha"],
                                     "run_id": observation["run_id"],
                                     "run_attempt": observation["run_attempt"]})

    pair_by_scope_pr: dict[tuple[str, str, int], str] = {}
    for record in records:
        if not isinstance(record, dict) or not _valid_execution_record(record):
            rejected.append("malformed, synthetic, failed, or unbound benchmark execution receipt")
            invalidate_capability(record)
            continue
        if (parent_prd_id and record["parent_prd_id"] != parent_prd_id) \
                or (parent_prd_sha and record["parent_prd_sha"] != parent_prd_sha):
            rejected.append("benchmark record belongs to a different parent PRD")
            invalidate_capability(record)
            continue
        observation = observation_by_pr.get(record["pr_number"])
        if (observation is None or observation["pr_number"] in conflicting_observation_prs
                or any(observation.get(key) != record.get(record_key) for key, record_key in (
                    ("source_sha", "source_sha"), ("tree", "tree"),
                    ("policy_fingerprint", "policy_fingerprint"),
                    ("parent_prd_id", "parent_prd_id"), ("parent_prd_sha", "parent_prd_sha")))):
            rejected.append("benchmark pair has no matching source-bound PR observation")
            invalidate_capability(record)
            continue
        scope_type, scope_id, pr_number = record["scope_type"], record["scope_id"], record["pr_number"]
        if scope_type == "class" and scope_id not in trial_classes:
            rejected.append("benchmark record belongs to an unregistered change class")
            continue
        if scope_type == "capability" and scope_id not in capability_ids:
            rejected.append("benchmark record belongs to an unregistered capability")
            invalidate_capability(record)
            continue
        if (scope_type == "capability"
                and pr_number not in capability_pr_numbers.get(scope_id, set())):
            rejected.append("capability benchmark PR is outside its declared business-capability inventory")
            invalidate_capability(record)
            continue
        pr_key = (scope_type, scope_id, pr_number)
        prior_pair = pair_by_scope_pr.get(pr_key)
        if prior_pair is not None and prior_pair != record["pair_id"]:
            rejected.append(f"duplicate benchmark pair for {scope_type} {scope_id} on PR {pr_number}")
            invalidate_capability(record)
            continue
        pair_by_scope_pr[pr_key] = record["pair_id"]
        key = (scope_type, scope_id, record["pair_id"])
        pair = grouped.setdefault(key, {})
        side = record["mode"]
        if side in pair:
            rejected.append("duplicate paired benchmark side: " + record["pair_id"])
            invalidate_capability(record)
            continue
        pair[side] = record

    measurements: dict[tuple[str, str], list[dict[str, Any]]] = {}
    for (scope_type, scope_id, pair_id), pair in grouped.items():
        full, targeted = pair.get("full"), pair.get("targeted")
        if full is None or targeted is None:
            rejected.append("incomplete paired command evidence: " + pair_id)
            if scope_type == "capability":
                invalid_capability_evidence.add(scope_id)
            continue
        identity_fields = ("source_sha", "tree", "environment_fingerprint", "cache_fingerprint",
                           "policy_fingerprint", "parent_prd_id", "parent_prd_sha", "pr_number",
                           "run_id", "run_attempt")
        if any(full[key] != targeted[key] for key in identity_fields):
            rejected.append("paired run identity mismatch: " + pair_id)
            if scope_type == "capability":
                invalid_capability_evidence.add(scope_id)
            continue
        if full["commands"] == targeted["commands"]:
            rejected.append("full and targeted benchmark commands are identical: " + pair_id)
            if scope_type == "capability":
                invalid_capability_evidence.add(scope_id)
            continue
        full_time, target_time = float(full["elapsed_seconds"]), float(targeted["elapsed_seconds"])
        savings = (full_time - target_time) / full_time
        cost = (_valid_capability_cost_receipt(targeted, target_time, targeted["execution_receipt"])
                if scope_type == "capability" else None)
        measurements.setdefault((scope_type, scope_id), []).append({
            "pair_id": pair_id, "pr_number": full["pr_number"], "source_sha": full["source_sha"],
            "tree": full["tree"], "environment_fingerprint": full["environment_fingerprint"],
            "cache_fingerprint": full["cache_fingerprint"], "policy_fingerprint": full["policy_fingerprint"],
            "full_seconds": full_time, "targeted_seconds": target_time, "savings": savings,
            "capability_ci_seconds": cost["ci_seconds"] if cost else None,
            "capability_deployment_seconds": cost["deployment_seconds"] if cost else None,
            "capability_total_seconds": cost["total_seconds"] if cost else None,
        })

    by_class = {}
    for scope_id in sorted(set(trial_classes)):
        samples = measurements.get(("class", scope_id), [])
        distinct_prs = {item["pr_number"] for item in samples}
        p50 = statistics.median(item["savings"] for item in samples) if samples else None
        passes = len(distinct_prs) >= minimum_class_prs and p50 is not None and p50 >= required_savings
        by_class[scope_id] = {
            "sample_count": len(samples), "distinct_pr_count": len(distinct_prs),
            "p50_savings": p50, "required_savings": required_savings,
            "minimum_distinct_prs": minimum_class_prs, "passes": passes,
            "total_full_seconds": round(sum(item["full_seconds"] for item in samples), 3),
            "total_targeted_seconds": round(sum(item["targeted_seconds"] for item in samples), 3),
        }

    capability_baselines = capability_baselines or {}
    by_capability = {}
    capability_ready = bool(capability_ids)
    for capability in capability_ids:
        samples = measurements.get(("capability", capability), [])
        evidence_complete = capability not in invalid_capability_evidence
        expected_prs = capability_pr_numbers.get(capability, set())
        observed_prs = set(observation_by_pr)
        measured_prs = {item["pr_number"] for item in samples}
        coverage_complete = bool(expected_prs) and expected_prs <= observed_prs and measured_prs == expected_prs
        evidence_complete = evidence_complete and coverage_complete
        baseline = capability_baselines.get(capability)
        baseline_valid = (
            isinstance(baseline, dict)
            and baseline.get("metric") == "ci_plus_deploy_total_seconds"
            and baseline.get("parent_prd_id") == parent_prd_id
            and baseline.get("parent_prd_sha") == parent_prd_sha
            and isinstance(baseline.get("baseline_id"), str) and bool(baseline["baseline_id"])
            and isinstance(baseline.get("receipt_ids"), list) and bool(baseline["receipt_ids"])
            and all(isinstance(item, str) and item for item in baseline["receipt_ids"])
        )
        try:
            baseline_seconds = _safe_seconds(
                baseline.get("total_seconds") if isinstance(baseline, dict) else None,
                "capability baseline total_seconds")
        except ShadowEvidenceError:
            baseline_valid = False
            baseline_seconds = None
        targeted_total = sum(item["capability_total_seconds"] for item in samples
                             if isinstance(item.get("capability_total_seconds"), (int, float)))
        candidate_measured = (evidence_complete and bool(samples)
                              and all(isinstance(item.get("capability_total_seconds"), (int, float))
                                      for item in samples))
        cost_change = ((targeted_total - baseline_seconds) / baseline_seconds
                       if candidate_measured and baseline_valid and baseline_seconds else None)
        passes = candidate_measured and baseline_valid and cost_change is not None and cost_change <= 0
        by_capability[capability] = {
            "sample_count": len(samples),
            "distinct_pr_count": len({item["pr_number"] for item in samples}),
            "evidence_complete": evidence_complete,
            "expected_pr_numbers": sorted(expected_prs),
            "observed_pr_numbers": sorted(expected_prs & observed_prs),
            "cost_pr_numbers": sorted(measured_prs),
            "coverage_complete": coverage_complete,
            "full_ci_seconds": round(sum(item["full_seconds"] for item in samples), 3),
            "targeted_ci_seconds": round(sum(item["capability_ci_seconds"] or 0 for item in samples), 3),
            "targeted_deployment_seconds": round(sum(item["capability_deployment_seconds"] or 0 for item in samples), 3),
            "total_targeted_ci_deploy_seconds": round(targeted_total, 3),
            "baseline_id": baseline.get("baseline_id") if isinstance(baseline, dict) else None,
            "baseline_ci_deploy_seconds": round(baseline_seconds, 3) if baseline_seconds is not None else None,
            "cost_change": cost_change,
            "passes": passes,
        }
        capability_ready &= passes

    expected_defect_ids = sorted(set(expected_defect_ids or []))
    replays = known_defect_replays or []
    replay_by_id = {}
    for replay in replays:
        if not isinstance(replay, dict) or not _valid_defect_replay(
                replay, parent_prd_id or "", parent_prd_sha or ""):
            rejected.append("malformed or unbound known-defect replay evidence")
            continue
        defect_id = replay["defect_id"]
        if defect_id in replay_by_id:
            rejected.append("duplicate known-defect replay: " + defect_id)
            continue
        replay_by_id[defect_id] = replay
    missing_defects = sorted(set(expected_defect_ids) - set(replay_by_id))

    countable_observation_prs = {
        item["pr_number"] for item in observation_by_pr.values()
        if item["candidate_result"] in {"success", "failure"}
        and item["pr_number"] not in conflicting_observation_prs
    }
    trial_pr_count = len(countable_observation_prs)
    unmet_reasons = []
    if not parent_prd_id or not parent_prd_sha or not HEX64.fullmatch(parent_prd_sha):
        unmet_reasons.append("parent PRD identity is unknown or unbound")
    if trial_pr_count < minimum_trial_prs:
        unmet_reasons.append(f"trial has {trial_pr_count} distinct PRs; requires {minimum_trial_prs}")
    if unknown_observation_count:
        unmet_reasons.append(f"trial has {unknown_observation_count} unknown candidate observations")
    if confirmed_misses:
        unmet_reasons.append("candidate passed while the original CI gate failed; confirmed omission blocks activation")
    unmet_reasons.extend("class lacks 30% p50 savings or three distinct PR pairs: " + scope_id
                         for scope_id, result in by_class.items() if not result["passes"])
    unmet_reasons.extend("capability total does not beat its measured CI+deployment baseline: " + scope_id
                         for scope_id, result in by_capability.items() if not result["passes"])
    if not capability_ids:
        unmet_reasons.append("capability cost evidence inventory is empty")
    if not expected_defect_ids:
        unmet_reasons.append("known-defect replay inventory is empty")
    if missing_defects:
        unmet_reasons.append("known-defect replay missing: " + ", ".join(missing_defects))
    if rejected:
        unmet_reasons.append("rejected or inconsistent evidence records are present")

    valid_parent_prd = bool(parent_prd_id and isinstance(parent_prd_sha, str) and HEX64.fullmatch(parent_prd_sha))
    global_ready = (valid_parent_prd and trial_pr_count >= minimum_trial_prs and capability_ready
                    and bool(expected_defect_ids) and not missing_defects and not rejected
                    and unknown_observation_count == 0 and not confirmed_misses)
    eligible_classes = sorted(scope_id for scope_id, result in by_class.items()
                              if global_ready and result["passes"])
    return {
        "schema": SCHEMA,
        "evidence_kind": "paired_canonical_ci_commands_with_run_receipts",
        "parent_prd_id": parent_prd_id,
        "parent_prd_sha": parent_prd_sha,
        "distinct_trial_pr_count": trial_pr_count,
        "distinct_observation_receipt_count": len(valid_observations),
        "unknown_observation_count": unknown_observation_count,
        "confirmed_misses": confirmed_misses,
        "minimum_trial_pr_count": minimum_trial_prs,
        "minimum_distinct_prs_per_class": minimum_class_prs,
        "by_class": by_class,
        "eligible_classes": eligible_classes,
        "by_capability": by_capability,
        "known_defect_replays": sorted(replay_by_id),
        "missing_known_defect_replays": missing_defects,
        "rejected_records": sorted(set(rejected)),
        "unmet_reasons": sorted(set(unmet_reasons)),
        "activation_ready": bool(eligible_classes),
    }


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    collect = sub.add_parser("collect", help="join plan, graph, lane receipts, and Go JSON logs")
    collect.add_argument("--plan", type=Path, required=True)
    collect.add_argument("--graph", type=Path, required=True)
    collect.add_argument("--runs", type=Path, required=True)
    collect.add_argument("--pr-number", type=int, default=0)
    collect.add_argument("--original-gate-result", choices=("success", "failure", "unknown"), default="unknown")
    collect.add_argument("--out", type=Path, required=True)
    pair = sub.add_parser("pair", help="adapt actual quality lane and release timing receipts")
    pair.add_argument("--full-run", type=Path, required=True)
    pair.add_argument("--targeted-run", type=Path, required=True)
    pair.add_argument("--scope-type", choices=("class", "capability"), required=True)
    pair.add_argument("--scope-id", required=True)
    pair.add_argument("--release-state", type=Path)
    pair.add_argument("--release-manifest", type=Path)
    pair.add_argument("--ci-jobs", type=Path,
                      help="complete read-only `export-ci-history` bundle for this PR; required for capability scope")
    pair.add_argument("--repo", type=Path)
    pair.add_argument("--out", type=Path, required=True)
    export = sub.add_parser("export-ci-history", help="export all GitHub CI runs, attempts, and jobs for a PR")
    export.add_argument("--github-repo", required=True, help="owner/repository")
    export.add_argument("--pr-number", type=int, required=True)
    export.add_argument("--out", type=Path, required=True)
    benchmark = sub.add_parser("benchmarks", help="evaluate actual paired full/targeted timings")
    benchmark.add_argument("--records", type=Path, required=True)
    benchmark.add_argument("--trial-classes", type=Path, required=True)
    benchmark.add_argument("--capabilities", type=Path, required=True)
    benchmark.add_argument("--capability-baselines", type=Path)
    benchmark.add_argument("--known-defect-replays", type=Path)
    benchmark.add_argument("--known-defects", type=Path)
    benchmark.add_argument("--parent-prd", type=Path)
    benchmark.add_argument("--observations", type=Path)
    benchmark.add_argument("--out", type=Path, required=True)
    args = parser.parse_args()
    if args.command == "export-ci-history":
        result = export_github_pr_ci_history(args.github_repo, args.pr_number)
    elif args.command == "collect":
        result = collect_execution(json.loads(args.plan.read_text()), json.loads(args.graph.read_text()),
                                   json.loads(args.runs.read_text()), pr_number=args.pr_number,
                                   original_gate_result=args.original_gate_result)
    elif args.command == "pair":
        result = benchmark_records_from_lane_runs(
            args.full_run, args.targeted_run, args.scope_type, args.scope_id,
            args.release_state, args.release_manifest, args.repo, args.ci_jobs)
    else:
        parent_prd = json.loads(args.parent_prd.read_text()) if args.parent_prd else {}
        result = evaluate_benchmarks(json.loads(args.records.read_text()),
                                     json.loads(args.trial_classes.read_text()),
                                     json.loads(args.capabilities.read_text()),
                                     json.loads(args.capability_baselines.read_text())
                                     if args.capability_baselines else {},
                                     json.loads(args.known_defect_replays.read_text())
                                     if args.known_defect_replays else [],
                                     json.loads(args.known_defects.read_text())
                                     if args.known_defects else [],
                                     parent_prd.get("id"), parent_prd.get("sha256"),
                                     observations=(json.loads(args.observations.read_text())
                                                   if args.observations else []))
    args.out.parent.mkdir(parents=True, exist_ok=True)
    args.out.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    if args.command == "export-ci-history" and not result["complete"]:
        return 2
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (OSError, json.JSONDecodeError, ShadowEvidenceError, subprocess.SubprocessError) as error:
        print(f"affected CI shadow evidence failed closed: {type(error).__name__}: {error}", file=sys.stderr)
        raise SystemExit(2)
