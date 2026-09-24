#!/usr/bin/env python3
"""Run a canonical CI lane or its registered focused checks after setup.

GitHub Actions and ``dev_preflight.py full`` both invoke this file so their
verification command lists cannot drift.  It deliberately does not install an
operating-system runtime or start PostgreSQL: those are explicit platform
prerequisites owned by the caller.
"""
from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import platform
import re
from urllib.parse import parse_qs, unquote, urlparse


ROOT = Path(__file__).resolve().parents[2]
LANES = ("preflight", "backend", "frontend", "browser", "archive-sdk")
NODE_VERSION = "24.18.0"
NPM_VERSION = "11.12.1"


def command_available(name: str) -> bool:
    return shutil.which(name) is not None


def postgres_16_ready() -> bool:
    url = os.environ.get("AICRM_DATABASE_URL")
    if not url or not command_available("psql"):
        return False
    parsed = urlparse(url)
    database = unquote(parsed.path.strip("/"))
    if (parsed.scheme not in {"postgres", "postgresql"} or parsed.hostname not in {"127.0.0.1", "localhost", "::1"}
            or not (database == "aicrm_ci" or database.startswith("aicrm_test_"))):
        return False
    query = parse_qs(parsed.query)
    env = dict(os.environ, PGHOST=parsed.hostname, PGPORT=str(parsed.port or 5432),
               PGUSER=unquote(parsed.username or ""), PGPASSWORD=unquote(parsed.password or ""),
               PGDATABASE=database, PGSSLMODE=query.get("sslmode", ["prefer"])[0])
    result = subprocess.run(
        ["psql", "-Atqc", "SHOW server_version_num"], env=env,
        stdout=subprocess.PIPE,
        stderr=subprocess.DEVNULL,
        text=True,
        timeout=10,
        check=False,
    )
    return result.returncode == 0 and result.stdout.strip().startswith("16")


def exact_version(command: list[str], expected: str) -> bool:
    if not command_available(command[0]):
        return False
    result = subprocess.run(command, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True, check=False)
    return result.returncode == 0 and expected in result.stdout.split()


def chromium_font_ready() -> bool:
    if not command_available("fc-match"):
        return False
    result = subprocess.run(["fc-match", "Noto Sans CJK SC"], stdout=subprocess.PIPE,
                            stderr=subprocess.DEVNULL, text=True, check=False)
    return result.returncode == 0 and "Noto" in result.stdout


def dedup_base_ready() -> bool:
    base, head = os.environ.get("AICRM_DEDUP_BASE_SHA", ""), os.environ.get("AICRM_DEDUP_HEAD_SHA", "")
    if not len(head) == 40 or not all(char in "0123456789abcdef" for char in head):
        return False
    # Match check-dedup-base-diff.sh: manual dispatch has no event.before, so
    # an empty/zero base explicitly means the checked-out HEAD parent.
    if not base or all(char == "0" for char in base):
        base = head + "^"
    elif not len(base) == 40 or not all(char in "0123456789abcdef" for char in base):
        return False
    current = subprocess.run(["git", "rev-parse", "HEAD"], cwd=ROOT, stdout=subprocess.PIPE, text=True, check=False)
    present = subprocess.run(["git", "cat-file", "-e", base + "^{commit}"], cwd=ROOT, check=False)
    return current.returncode == 0 and current.stdout.strip() == head and present.returncode == 0


def missing_prerequisites(lane: str) -> list[str]:
    required = {"bash", "go", "make", "node", "python3"}
    missing = []
    if not exact_version(["go", "version"], "go1.26.6"):
        missing.append("Go 1.26.6")
    if not exact_version(["node", "--version"], "v" + NODE_VERSION):
        missing.append("Node " + NODE_VERSION)
    if lane != "preflight" and not exact_version(["npm", "--version"], NPM_VERSION):
        missing.append("npm " + NPM_VERSION)
    if lane == "preflight" and not dedup_base_ready():
        missing.append("AICRM_DEDUP_BASE_SHA/current AICRM_DEDUP_HEAD_SHA baseline")
    if lane in {"backend", "browser"} and not postgres_16_ready():
        required.add("PostgreSQL 16 reachable through AICRM_DATABASE_URL")
    if lane == "browser":
        if platform.system() != "Linux" or platform.machine() not in {"x86_64", "amd64"}:
            missing.append("Linux amd64 browser environment")
        required.add("google-chrome")
    if lane == "archive-sdk" and (platform.system() != "Linux" or platform.machine() not in {"x86_64", "amd64"}):
        missing.append("Linux amd64 archive SDK environment")
    for item in sorted(required):
        if item == "PostgreSQL 16 reachable through AICRM_DATABASE_URL":
            if not postgres_16_ready():
                missing.append(item)
        elif not command_available(item):
            missing.append(item)
    return missing


def run(command: list[str], env: dict[str, str] | None = None) -> None:
    print("+ " + " ".join(command), flush=True)
    subprocess.run(command, cwd=ROOT, env=env, check=True)


def venv_path(lane: str, report_dir: Path | None) -> str:
    if report_dir is None:
        raise ValueError(lane + " requires --report-dir")
    return str(report_dir / ".venv")


def commands(lane: str, report_dir: Path | None) -> list[list[str]]:
    if lane == "preflight":
        if report_dir is None:
            raise ValueError("preflight requires --report-dir")
        return [[sys.executable, "scripts/dev_preflight.py", "fast", "--report-dir", str(report_dir)], [
            sys.executable, "-m", "unittest", "discover", "-s", "scripts/ci", "-p", "test_*.py"
        ], [sys.executable, "scripts/audit/test_scan_exact_duplicates.py"], [
            sys.executable, "scripts/audit/test_check_new_exact_duplicates.py"
        ], [sys.executable, "scripts/audit/test_check_source_authority_changes.py"], [
            "bash", "scripts/audit/check-dedup-base-diff.sh", "."
        ], ["bash", "scripts/test-configure-wecom-tag-catalog-mutation-runtime.sh"], [sys.executable, "scripts/test_retention_registry.py"], [
            sys.executable, "-m", "unittest", "scripts/test_domestic_release.py", "scripts/test_domestic_release_build.py"
        ], [sys.executable, "-m", "unittest", "discover", "-s", "deploy", "-p", "test_*.py"]]
    if lane == "backend":
        venv = venv_path(lane, report_dir)
        return [["bash", "scripts/run-donor-view-consumers.sh", "stage"], [
            sys.executable, "-m", "venv", venv
        ], [venv + "/bin/pip", "install", "-r", "components/excel-batches/requirements.txt"], [
            venv + "/bin/python", "-m", "unittest", "discover", "-s", "components/excel-batches", "-v"
        ], ["bash", "scripts/run-go-with-donor-views.sh", "go", "vet", "./..."], [
            "bash", "scripts/run-go-with-donor-views.sh", "go", "test", "-p", "1", "-race", "-count=1", "-timeout=15m", "./..."
        ]]
    if lane == "frontend":
        return [["npm", "run", "orval:check"], ["node", "scripts/ci/generated_clients_contract.mjs"], ["node", "scripts/excel-batches-dom-test.mjs"], ["node", "scripts/excel-batches-pagination-dom-test.mjs"], ["node", "scripts/validate-openapi.mjs"], [
            "bash", "scripts/run-donor-view-consumers.sh", "check"
        ], ["node", "scripts/media-shell-interactions-e2e.mjs"], [
            "node", "scripts/tags-shell-interactions-e2e.mjs"
        ], ["node", "scripts/customer-directory-shell-e2e.mjs"]]
    if lane == "browser":
        venv = venv_path(lane, report_dir)
        return [[sys.executable, "-m", "venv", venv], [
            venv + "/bin/pip", "install", "-r", "components/excel-batches/requirements.txt"
        ], [venv + "/bin/python", "-c", "import openpyxl"], [
            "node", "scripts/generate-ai-assistant-client.mjs"
        ], [sys.executable, "scripts/dev_preflight.py", "browser", "--group", "all", "--report-dir", str(report_dir)]]
    if lane == "archive-sdk":
        return [["bash", "scripts/check-wecom-message-archive-sdk.sh"]]
    raise ValueError("unknown lane: " + lane)


def tooling_contract_commands() -> list[list[str]]:
    """Contracts for the release controller, CI selector, and installer.

    This suite deliberately avoids building the application or running UI and
    Chromium journeys. Host-only rehearsals remain a separate pre-deployment
    check on the preparation machine with its real service account and paths.
    """
    return [
        [sys.executable, "-m", "unittest", "discover", "-s", "scripts/ci", "-p", "test_*.py"],
        [sys.executable, "-m", "unittest", "discover", "-s", "scripts", "-p", "test_domestic_release*.py"],
        [sys.executable, "-m", "unittest", "discover", "-s", "deploy", "-p", "test_domestic_promote*.py"],
    ]


def focused_commands(lane: str, report_dir: Path, checks: list[dict]) -> list[list[str]]:
    """Run registered checks for a lane, falling back to its full lane when a
    check cannot be expressed safely as a focused command.
    """
    selected = [check for check in checks if check.get("lane") == lane]
    if not selected:
        raise ValueError("focused lane has no registered checks: " + lane)
    if lane == "backend":
        packages: dict[str, set[str]] = {}
        for check in selected:
            path = Path(check.get("path", ""))
            if path.suffix != ".go" or not path.parts or ".." in path.parts:
                return commands(lane, report_dir)
            test = check.get("test")
            if test is not None and not re.fullmatch(r"Test[A-Za-z0-9_]+", test):
                return commands(lane, report_dir)
            packages.setdefault(path.parent.as_posix(), set())
            if test:
                packages[path.parent.as_posix()].add(test)
        result = []
        for package, names in sorted(packages.items()):
            command = ["bash", "scripts/run-go-with-donor-views.sh", "go", "test", "-p", "1",
                       "-race", "-count=1", "-timeout=15m"]
            if names:
                pattern = "^(" + "|".join(re.escape(name) for name in sorted(names)) + ")$"
                command.extend(["-run", pattern])
            command.append("./" + package)
            result.append(command)
        return result
    if lane == "frontend":
        paths = []
        for check in selected:
            path = Path(check.get("path", ""))
            if path.suffix not in {".mjs", ".js"} or not path.parts or ".." in path.parts:
                return commands(lane, report_dir)
            paths.append(path.as_posix())
        return [["node", path] for path in sorted(set(paths))]
    if lane == "browser":
        names = []
        for check in selected:
            name = check.get("test")
            path = Path(check.get("path", ""))
            if (not isinstance(name, str) or not re.fullmatch(r"Test[A-Za-z0-9_]*ChromiumJourney", name)
                    or not path.as_posix().startswith("cmd/aicrm/") or path.suffix != ".go"):
                return commands(lane, report_dir)
            names.append(name)
        command = [sys.executable, "scripts/dev_preflight.py", "browser"]
        for name in sorted(set(names)):
            command.extend(["--journey", name])
        command.extend(["--report-dir", str(report_dir)])
        return [command]
    if lane == "archive-sdk":
        paths = []
        for check in selected:
            path = Path(check.get("path", ""))
            if path.suffix != ".sh" or not path.parts or ".." in path.parts:
                return commands(lane, report_dir)
            paths.append(path.as_posix())
        return [["bash", path] for path in sorted(set(paths))]
    return commands(lane, report_dir)


def lane_environment(lane: str, report_dir: Path | None) -> dict[str, str]:
    env = dict(os.environ)
    if lane == "browser":
        env["PATH"] = venv_path(lane, report_dir) + "/bin:" + env.get("PATH", "")
    return env


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("lane", choices=LANES)
    parser.add_argument("--report-dir", type=Path)
    parser.add_argument("--profile", choices=("full", "tooling"), default="full")
    parser.add_argument("--focus-checks-json", default=os.environ.get("AICRM_CI_FOCUS_CHECKS", ""))
    parser.add_argument("--check-prerequisites", action="store_true")
    args = parser.parse_args()
    missing = missing_prerequisites(args.lane)
    if missing:
        print("missing required local environment: " + ", ".join(missing), file=sys.stderr)
        return 2
    if args.check_prerequisites:
        return 0
    try:
        checks = json.loads(args.focus_checks_json) if args.focus_checks_json else []
    except json.JSONDecodeError as error:
        raise ValueError("invalid focused check list") from error
    if not isinstance(checks, list) or any(not isinstance(check, dict) for check in checks):
        raise ValueError("focused check list must be a JSON array of objects")
    if args.profile == "tooling":
        if args.lane != "preflight":
            raise ValueError("tooling profile is only valid for the preflight receipt lane")
        lane_commands = tooling_contract_commands()
    else:
        lane_commands = (focused_commands(args.lane, args.report_dir, checks)
                         if checks and args.lane != "preflight" else commands(args.lane, args.report_dir))
    for command in lane_commands:
        run(command, lane_environment(args.lane, args.report_dir))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
