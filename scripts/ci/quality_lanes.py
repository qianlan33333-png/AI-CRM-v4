#!/usr/bin/env python3
"""Run a canonical CI lane or its registered focused checks after setup.

GitHub Actions and ``dev_preflight.py full`` both invoke this file so their
verification command lists cannot drift.  It deliberately does not install an
operating-system runtime or start PostgreSQL: those are explicit platform
prerequisites owned by the caller.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import platform
import re
import time
from urllib.parse import parse_qs, unquote, urlparse


ROOT = Path(__file__).resolve().parents[2]
LANES = ("preflight", "backend", "frontend", "browser", "archive-sdk")
NODE_VERSION = "24.18.0"
NPM_VERSION = "11.12.1"
WORKFLOW_PATH = ".github/workflows/ci.yml"
PARENT_PRD_PATH = "docs/prd/2026-09-24-small-step-impact-checks.md"


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


def affected_package_test_command(packages: list[str]) -> list[str]:
    """Build a full-suite Go test command for exact affected packages.

    The command intentionally has no test-name filter, so new tests in each
    selected package are discovered automatically.
    """
    if not packages or any(not isinstance(item, str) or not item for item in packages):
        raise ValueError("affected package inventory must be nonempty")
    go_mod = (ROOT / "go.mod").read_text(encoding="utf-8")
    match = re.search(r"(?m)^module[ \t]+([^\s]+)", go_mod)
    if not match:
        raise ValueError("go.mod has no module declaration")
    module = match.group(1)
    paths = []
    for package in packages:
        if not package.startswith(module + "/"):
            raise ValueError("affected package is outside the repository module")
        relative = package[len(module) + 1:]
        if (not relative or relative.startswith("/") or ".." in Path(relative).parts
                or any(part in {"", ".", "..."} for part in relative.split("/"))):
            raise ValueError("affected package has an unsafe import path")
        paths.append("./" + relative)
    return ["bash", "scripts/run-go-with-donor-views.sh", "go", "test", "-json",
            "-p", "1", "-race", "-count=1", "-timeout=15m", *sorted(set(paths))]


def replace_full_backend_test_with_packages(commands_: list[list[str]], packages: list[str]) -> list[list[str]]:
    test_command = affected_package_test_command(packages)
    replaced = False
    result = []
    for command in commands_:
        if "test" in command and "go" in command and "./..." in command:
            result.append(test_command)
            replaced = True
        else:
            result.append(command)
    if not replaced:
        raise ValueError("canonical backend lane has no full Go test command to replace")
    return result


def _run_policy_fingerprint() -> str | None:
    ci_dir = str(ROOT / "scripts/ci")
    if ci_dir not in sys.path:
        sys.path.insert(0, ci_dir)
    try:
        import affected_plan
        head = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip()
        return affected_plan.combined_policy_fingerprint(
            affected_plan.planner_policy_fingerprint(),
            affected_plan.repository_policy_fingerprint(ROOT, head))
    except (OSError, ValueError, subprocess.SubprocessError):
        return None


def _sha256_json(value: dict) -> str:
    payload = json.dumps(value, sort_keys=True, separators=(",", ":")).encode()
    return hashlib.sha256(payload).hexdigest()


def _pr_number() -> int | None:
    direct = os.environ.get("GITHUB_PR_NUMBER", "")
    event_path = os.environ.get("GITHUB_EVENT_PATH", "")
    try:
        event = json.loads(Path(event_path).read_text()) if event_path else {}
    except (OSError, json.JSONDecodeError):
        event = {}
    value = direct or (event.get("pull_request", {}).get("number") if isinstance(event, dict) else None)
    try:
        number = int(value)
    except (TypeError, ValueError):
        return None
    return number if number > 0 else None


def _parent_prd_sha(head: str | None) -> str | None:
    if not head:
        return None
    try:
        content = subprocess.check_output(["git", "show", f"{head}:{PARENT_PRD_PATH}"],
                                          cwd=ROOT, stderr=subprocess.DEVNULL)
    except subprocess.SubprocessError:
        return None
    return hashlib.sha256(content).hexdigest()


def _measurement_fingerprints() -> tuple[str | None, str | None]:
    """Bind timing evidence to the Actions runner image and cache policy."""
    if os.environ.get("GITHUB_ACTIONS", "").lower() != "true":
        return None, None
    def version(command: list[str]) -> str:
        try:
            return subprocess.check_output(command, cwd=ROOT, stderr=subprocess.STDOUT,
                                           text=True, timeout=10).strip().splitlines()[0]
        except (OSError, subprocess.SubprocessError, IndexError):
            return "unavailable"

    runner = {
        "os": os.environ.get("RUNNER_OS", platform.system()),
        "arch": os.environ.get("RUNNER_ARCH", platform.machine()),
        "image_os": os.environ.get("ImageOS", ""),
        "image_version": os.environ.get("ImageVersion", ""),
        "python": platform.python_version(),
        "go": version(["go", "version"]),
        "node": version(["node", "--version"]),
        "npm": version(["npm", "--version"]),
    }
    # The generic runner OS/architecture fields do not pin the hosted image.
    # Without both immutable image labels, timings cannot be compared as
    # same-environment evidence, so keep the diagnostic environment hash but
    # omit the cache fingerprint that makes a pair eligible.
    if not runner["image_os"] or not runner["image_version"]:
        return _sha256_json(runner), None
    cache = {
        "setup_go_actions_cache": False,
        "setup_node_actions_cache": False,
        "go_test_cache": "disabled-by-count-1",
        "runner_image": runner["image_version"] or runner["image_os"],
        "actions_hosted_job": True,
        "pip_cache": "runner-local-no-actions-cache",
    }
    return _sha256_json(runner), _sha256_json(cache)


def _execution_receipt(report_dir: Path, execution: dict, elapsed: float,
                       result: str, exit_code: int, tested_sha: str | None,
                       tree: str | None, policy_fingerprint: str | None,
                       run_id: str | None, run_attempt: int | None) -> dict | None:
    pr_number = _pr_number()
    parent_sha = _parent_prd_sha(tested_sha)
    environment_fingerprint, cache_fingerprint = _measurement_fingerprints()
    commands = execution.get("commands", [])
    commands_sha = (hashlib.sha256(json.dumps(
        commands, sort_keys=True, separators=(",", ":")).encode()).hexdigest()
        if commands else None)
    log_name = execution.get("go_json_log")
    log_path = report_dir / log_name if isinstance(log_name, str) else None
    try:
        test_json_sha = hashlib.sha256(log_path.read_bytes()).hexdigest() if log_path and log_path.is_file() else None
    except OSError:
        test_json_sha = None
    go_commands = [command for command in commands
                   if isinstance(command, list) and "go" in command and "test" in command]
    has_vet = any("go" in command and "vet" in command for command in commands
                  if isinstance(command, list))
    if (not pr_number or not run_id or not run_attempt or not tested_sha or not tree
            or not policy_fingerprint or not parent_sha or not environment_fingerprint
            or not cache_fingerprint or not commands_sha or not test_json_sha):
        return None
    return {
        "schema": 1, "kind": "canonical_ci_lane_execution",
        "workflow_path": WORKFLOW_PATH,
        "conclusion": "success" if result == "success" and exit_code == 0 else "failure",
        "pr_number": pr_number, "run_id": run_id, "run_attempt": run_attempt,
        "tested_sha": tested_sha, "tree": tree, "policy_fingerprint": policy_fingerprint,
        "parent_prd_id": PARENT_PRD_PATH, "parent_prd_sha": parent_sha,
        "commands_sha256": commands_sha, "elapsed_seconds": elapsed,
        "includes_setup": bool(commands), "includes_vet": has_vet,
        "includes_test_suite": bool(go_commands), "test_json_sha256": test_json_sha,
        "environment_fingerprint": environment_fingerprint,
        "cache_fingerprint": cache_fingerprint,
    }


def _write_lane_receipt(report_dir: Path | None, lane: str, execution: dict,
                        started: float, result: str, exit_code: int) -> None:
    if report_dir is None:
        return
    report_dir.mkdir(parents=True, exist_ok=True)
    def git(ref: str) -> str | None:
        try:
            return subprocess.check_output(["git", "rev-parse", ref], cwd=ROOT, text=True).strip()
        except subprocess.SubprocessError:
            return None
    run_attempt = os.environ.get("GITHUB_RUN_ATTEMPT", "")
    try:
        run_attempt_value = int(run_attempt) if run_attempt else None
    except ValueError:
        run_attempt_value = None
    elapsed = max(0.001, time.monotonic() - started)
    tested_sha, tree = git("HEAD"), git("HEAD^{tree}")
    policy_fingerprint = _run_policy_fingerprint()
    run_id = os.environ.get("GITHUB_RUN_ID") or None
    receipt = {
        "schema": 1, "lane": lane,
        "tested_sha": tested_sha, "tree": tree,
        "policy_fingerprint": policy_fingerprint,
        "result": result, "exit_code": exit_code,
        "elapsed_seconds": elapsed,
        "run_id": run_id,
        "run_attempt": run_attempt_value,
        "commands": execution["commands"],
        "go_json_log": execution.get("go_json_log"),
    }
    measured = _execution_receipt(report_dir, execution, elapsed, result, exit_code,
                                  tested_sha, tree, policy_fingerprint, run_id, run_attempt_value)
    if measured is not None:
        receipt["execution_receipt"] = measured
    (report_dir / "run.json").write_text(json.dumps(receipt, ensure_ascii=False, indent=2) + "\n")


def _clear_lane_outputs(report_dir: Path | None, lane: str) -> None:
    """Remove only this lane's previous receipt and test log before a rerun."""
    if report_dir is None:
        return
    names = ["run.json", lane + "-go-test.jsonl"]
    if lane == "browser":
        names.append("browser-execution.log")
    for name in names:
        path = report_dir / name
        if path.is_dir() and not path.is_symlink():
            raise ValueError("lane output path is a directory: " + name)
        path.unlink(missing_ok=True)


def run_recorded(command: list[str], env: dict[str, str] | None, lane: str,
                 report_dir: Path | None, execution: dict) -> None:
    print("+ " + " ".join(command), flush=True)
    execution["commands"].append(command)
    if "-json" in command and report_dir is not None:
        report_dir.mkdir(parents=True, exist_ok=True)
        log_path = report_dir / (lane + "-go-test.jsonl")
        mode = "a" if log_path.exists() else "w"
        process = subprocess.Popen(command, cwd=ROOT, env=env, stdout=subprocess.PIPE,
                                   stderr=subprocess.STDOUT, text=True)
        assert process.stdout is not None
        with log_path.open(mode, encoding="utf-8") as log:
            with process.stdout:
                for line in process.stdout:
                    log.write(line)
                    print(line, end="", flush=True)
        code = process.wait()
        execution["go_json_log"] = log_path.name
        if code:
            raise subprocess.CalledProcessError(code, command)
    else:
        subprocess.run(command, cwd=ROOT, env=env, check=True)
        if "dev_preflight.py" in command and "browser" in command and report_dir is not None:
            browser_log = report_dir / "browser-execution.log"
            if browser_log.is_file():
                execution["go_json_log"] = browser_log.name


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
            "bash", "scripts/run-go-with-donor-views.sh", "go", "test", "-json", "-p", "1", "-race", "-count=1", "-timeout=15m", "./..."
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
        [sys.executable, "-m", "unittest", "deploy.test_domestic_main_source"],
        [sys.executable, "-m", "unittest", "scripts.test_manual_github_sync"],
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
            command = ["bash", "scripts/run-go-with-donor-views.sh", "go", "test", "-json", "-p", "1",
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
    parser.add_argument("--focus-packages-json", default=os.environ.get("AICRM_CI_FOCUS_PACKAGES", ""))
    parser.add_argument("--check-prerequisites", action="store_true")
    args = parser.parse_args()
    started = time.monotonic()
    execution = {"commands": [], "go_json_log": None}
    result, exit_code = "failed", 2
    try:
        _clear_lane_outputs(args.report_dir, args.lane)
        missing = missing_prerequisites(args.lane)
        if missing:
            print("missing required local environment: " + ", ".join(missing), file=sys.stderr)
            exit_code = 2
        elif args.check_prerequisites:
            result, exit_code = "success", 0
        else:
            checks = json.loads(args.focus_checks_json) if args.focus_checks_json else []
            packages = json.loads(args.focus_packages_json) if args.focus_packages_json else []
            if (not isinstance(checks, list) or any(not isinstance(check, dict) for check in checks)
                    or not isinstance(packages, list)
                    or any(not isinstance(package, str) for package in packages)):
                raise ValueError("focused check and package lists must be JSON arrays")
            if packages and checks:
                raise ValueError("package focus and named-check focus cannot be combined")
            if packages and (args.lane != "backend" or args.profile != "full"):
                raise ValueError("affected package focus is only valid for the full backend lane")
            if args.profile == "tooling":
                if args.lane != "preflight":
                    raise ValueError("tooling profile is only valid for the preflight receipt lane")
                lane_commands = tooling_contract_commands()
            else:
                lane_commands = (focused_commands(args.lane, args.report_dir, checks)
                                 if checks and args.lane != "preflight" else commands(args.lane, args.report_dir))
            if packages:
                lane_commands = replace_full_backend_test_with_packages(lane_commands, packages)
            for command in lane_commands:
                run_recorded(command, lane_environment(args.lane, args.report_dir),
                             args.lane, args.report_dir, execution)
            result, exit_code = "success", 0
    except subprocess.CalledProcessError as error:
        exit_code = error.returncode or 1
        print(str(error), file=sys.stderr)
    except (OSError, ValueError) as error:
        print(str(error), file=sys.stderr)
        exit_code = 2
    finally:
        _write_lane_receipt(args.report_dir, args.lane, execution, started, result, exit_code)
    return exit_code


if __name__ == "__main__":
    raise SystemExit(main())
