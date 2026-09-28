#!/usr/bin/env python3
"""Discover, group, run and audit cmd/aicrm top-level tests without omissions."""
from __future__ import annotations

import argparse
import json
from pathlib import Path
import re
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[2]
PACKAGE = "./cmd/aicrm"
TEST_NAME = re.compile(r"^Test[A-Za-z0-9_]+$")
DECLARATION = re.compile(r"(?m)^func[ \t]+(Test[A-Za-z0-9_]+)[ \t]*\(")
INSTALLED_CONTRACT = "TestDomesticReleaseInstalledAlipayCheckout"
MAX_TESTS_PER_GROUP = 30


def command_output(command: list[str]) -> str:
    result = subprocess.run(command, cwd=ROOT, capture_output=True, text=True, check=False)
    if result.returncode:
        raise ValueError(f"discovery failed ({result.returncode}): {' '.join(command)}: {result.stderr[-1200:]}")
    return result.stdout


def compiled_tests() -> dict[str, str]:
    package = json.loads(command_output(["go", "list", "-json", PACKAGE]))
    files = package.get("TestGoFiles", []) + package.get("XTestGoFiles", [])
    owners: dict[str, str] = {}
    for filename in files:
        path = ROOT / "cmd/aicrm" / filename
        for name in DECLARATION.findall(path.read_text()):
            if name == "TestMain":
                continue
            if name in owners:
                raise ValueError(f"duplicate Go test declaration: {name}")
            owners[name] = path.relative_to(ROOT).as_posix()
    return owners


def manifest() -> dict:
    listing = command_output(["bash", "scripts/run-go-with-donor-views.sh", "go", "test", "-p", "1", "-list", "^Test", PACKAGE])
    discovered = [line.strip() for line in listing.splitlines() if TEST_NAME.fullmatch(line.strip())]
    if len(discovered) != len(set(discovered)):
        raise ValueError("compiled test listing has duplicate names")
    owners = compiled_tests()
    if set(discovered) != set(owners):
        raise ValueError("compiled test listing differs from source owners: " +
                         json.dumps({"unowned": sorted(set(discovered) - set(owners)),
                                     "unlisted": sorted(set(owners) - set(discovered))}))
    browser = sorted(name for name in discovered if name.endswith("ChromiumJourney"))
    installed = [INSTALLED_CONTRACT] if INSTALLED_CONTRACT in owners else []
    backend = sorted(set(discovered) - set(browser) - set(installed))
    if not backend or not browser or not installed:
        raise ValueError("empty required cmd/aicrm test class")
    groups: list[dict] = []
    by_file: dict[str, list[str]] = {}
    for name in backend:
        by_file.setdefault(owners[name], []).append(name)
    current_files: list[str] = []
    current_tests: list[str] = []
    for filename, names in sorted(by_file.items()):
        if current_tests and len(current_tests) + len(names) > MAX_TESTS_PER_GROUP:
            groups.append({"id": f"group-{len(groups)+1:03d}", "files": current_files, "tests": current_tests})
            current_files, current_tests = [], []
        current_files.append(filename)
        current_tests.extend(names)
    if current_tests:
        groups.append({"id": f"group-{len(groups)+1:03d}", "files": current_files, "tests": current_tests})
    assigned = [name for group in groups for name in group["tests"]] + browser + installed
    if len(assigned) != len(discovered) or set(assigned) != set(discovered):
        raise ValueError("cmd/aicrm tests are not assigned exactly once")
    return {"schema": 1, "package": PACKAGE, "discovered": sorted(discovered),
            "groups": groups, "browser": browser, "installed_contract": installed,
            "owners": owners}


def audit_events(lines: list[str], expected: list[str]) -> dict:
    wanted = set(expected)
    terminal: dict[str, list[str]] = {}
    runs: dict[str, int] = {}
    package_pass = False
    unexpected = []
    skips = []
    for line in lines:
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue
        name, action = event.get("Test", ""), event.get("Action", "")
        if not name:
            if action == "pass":
                package_pass = True
            elif action in {"fail", "skip"}:
                skips.append(f"package:{action}")
            continue
        top = name.split("/")[0]
        if action in {"run", "pass", "fail", "skip"} and top not in wanted:
            unexpected.append(name + ":" + action)
        if action == "skip":
            skips.append(name)
        if name in wanted:
            if action == "run":
                runs[name] = runs.get(name, 0) + 1
            elif action in {"pass", "fail", "skip"}:
                terminal.setdefault(name, []).append(action)
    bad = {name: {"runs": runs.get(name, 0), "terminal": terminal.get(name, [])}
           for name in expected if runs.get(name) != 1 or terminal.get(name) != ["pass"]}
    if bad or unexpected or skips or not package_pass:
        raise ValueError("group execution incomplete: " + json.dumps(
            {"bad": bad, "unexpected": unexpected, "skips": skips, "package_pass": package_pass}))
    return {"expected": len(expected), "passed": len(expected), "skipped": 0,
            "package_passed": True}


def run_groups(plan: dict, output: Path) -> dict:
    output.mkdir(parents=True, exist_ok=True)
    (output / "manifest.json").write_text(json.dumps(plan, ensure_ascii=False, indent=2) + "\n")
    completed = []
    for group in plan["groups"]:
        pattern = "^(" + "|".join(re.escape(name) for name in group["tests"]) + ")$"
        command = ["bash", "scripts/run-go-with-donor-views.sh", "go", "test", "-json", "-p", "1",
                   "-race", "-count=1", "-timeout=15m", "-run", pattern, PACKAGE]
        result = subprocess.run(command, cwd=ROOT, capture_output=True, text=True, check=False)
        log = output / (group["id"] + ".jsonl")
        log.write_text(result.stdout)
        if result.returncode:
            raise ValueError(f"{group['id']} exited {result.returncode}; {log}: {result.stderr[-1200:]}")
        receipt = audit_events(result.stdout.splitlines(), group["tests"])
        completed.append({"id": group["id"], "files": group["files"], "log": str(log), **receipt})
        print(f"{group['id']}: {receipt['passed']} passed; 0 skipped", flush=True)
    summary = {"schema": 1, "status": "passed", "discovered": len(plan["discovered"]),
               "backend_tests": sum(item["passed"] for item in completed),
               "browser_tests": len(plan["browser"]), "installed_contracts": len(plan["installed_contract"]),
               "groups": completed}
    (output / "summary.json").write_text(json.dumps(summary, ensure_ascii=False, indent=2) + "\n")
    return summary


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--mode", choices=("manifest", "run"), default="manifest")
    parser.add_argument("--report-dir", type=Path)
    args = parser.parse_args()
    plan = manifest()
    if args.mode == "run":
        if args.report_dir is None:
            parser.error("--report-dir is required for run")
        run_groups(plan, args.report_dir)
    else:
        print(json.dumps(plan, ensure_ascii=False, sort_keys=True))
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (OSError, ValueError) as error:
        print(error, file=sys.stderr)
        raise SystemExit(2)
