#!/usr/bin/env python3
"""Poll protected GitHub main and promote exact, locally built commits in order.

Run as the staging release controller. Source builds execute separately as
``aicrm-build``; this controller invokes the fixed installer and performs
read-only privileged host readback.
"""
from __future__ import annotations

import argparse
from datetime import datetime, timezone
import fcntl
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import shlex
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.request

SHA = re.compile(r"^[0-9a-f]{40}$")
FILE_SHA = re.compile(r"^[0-9a-f]{64}$")
REPO = "qianlan33333-png/AI-CRM-v4"
CI_WORKFLOW_PATH = ".github/workflows/ci.yml"
CI_FULL_RESULT_STEP = "Record exact main full regression result"
CI_REQUIRED_JOBS = ("plan", "preflight", "backend", "frontend", "browser", "archive-sdk", "governance", "check")
CI_FULL_LANES = ("preflight", "backend", "frontend", "browser", "archive-sdk")
CI_RUNS_PAGE_SIZE = 100
CI_RUNS_MAX_PAGES = 20
CI_HISTORY_RETRY_SECONDS = 300
BUILD_USER = "aicrm-build"
BUILD_ROOT = Path("/opt/aicrm/domestic/build-worker")
GITHUB_FETCH_TIMEOUT_SECONDS = 30
CONTROLLER_SOURCE_PATHS = {
    "scripts/domestic_release.py",
    "scripts/domestic_release_build.py",
    "deploy/domestic-promote.py",
}
ALIPAY_SMOKE_PATHS = (
    "cmd/aicrm/",
    "internal/externaleffects/",
    "internal/identity/",
    "internal/order/",
    "internal/payment/",
    "internal/platform/",
    "internal/product/",
    "internal/webshell/",
    "migrations/",
    "web/v3/payment/",
)
ALIPAY_SMOKE_FIXTURE = "cmd/aicrm/domestic_release_installed_smoke_test.go"
SMOKE_DOC_SUFFIXES = {".md", ".mdx", ".rst", ".adoc"}
SMOKE_TEST_DIR_NAMES = {"test", "tests", "testdata", "fixtures"}
HOST_READBACK_CODE = """
import hashlib, json, pathlib, re, subprocess, sys, urllib.request
p = pathlib.Path('/opt/aicrm/current').resolve(strict=True)
target = sys.argv[1] if len(sys.argv) > 1 else p.name
if not re.fullmatch(r'[0-9a-f]{40}', target):
    raise SystemExit('invalid readback SHA')
units = {}
for name in ('aicrm.service', 'aicrm-effects-worker.service'):
    active = subprocess.run(('systemctl', 'is-active', '--quiet', name)).returncode == 0
    pid = subprocess.check_output(('systemctl', 'show', name, '-p', 'MainPID', '--value'), text=True).strip()
    executable = pathlib.Path(f'/proc/{pid}/exe').resolve(strict=True) if pid.isdigit() and int(pid) > 0 else None
    units[name] = {'active': active, 'pid': int(pid) if pid.isdigit() else 0, 'executable': str(executable) if executable else None}
receipt_path = pathlib.Path('/opt/aicrm/domestic-receipts') / f'{target}.json'
receipt_exists = receipt_path.exists() or receipt_path.is_symlink()
receipt = json.loads(receipt_path.read_text()) if receipt_exists and receipt_path.is_file() and not receipt_path.is_symlink() else None
backup_path = pathlib.Path('/opt/aicrm/database-backups') / f'pre-{target}.dump'
database_backup_exists = backup_path.is_file() and not backup_path.is_symlink()
print(json.dumps({
    'current': str(p),
    'release_env': (p / 'release.env').read_text(),
    'manifest_sha256': hashlib.sha256((p / 'release-files.sha256').read_bytes()).hexdigest(),
    'readyz': json.load(urllib.request.urlopen('http://127.0.0.1:8080/readyz', timeout=3)),
    'services': units,
    'receipt_target_sha': target,
    'receipt_exists': receipt_exists,
    'receipt': receipt,
    'database_backup_exists': database_backup_exists,
}))
"""

# PR38's immutable source archive predates the source-view smoke fix in PR40.
# Permit exactly this checked source/helper pair to use the already merged PR40
# executor, while preserving both digests in the smoke receipt. All other
# candidates must use the helper committed in that candidate.
PR38_SMOKE_SOURCE_SHA = "32043f2ecdb814270245dbf2b3840eb868e0a33f"
PR38_SMOKE_SOURCE_TREE = "de394d4902d56337d110d2acd0a2f889f9dc0be6"
PR38_SOURCE_HELPER_SHA256 = "ca3ae4c8c8022620ba87e1d6c0c006fac222b0e63419c56adfb65b852ff3f5f9"
PR38_FORWARD_BUILDER_SHA256 = "775be12976cab4d66728b28bd877ea11e7314649c65ebc852759eb99b6e95f79"
PR38_PREVIOUS_CURSOR_SHA = "20b6e33f4a6667d76e675501ec7ab9466388dee7"
PR38_INSTALLED_BASE_SHA = "3ab9946d45b99101d483e23d8a68c2492047b748"
PR38_RESUME_MARKER = {
    "source_sha": PR38_SMOKE_SOURCE_SHA,
    "processed_sha": PR38_PREVIOUS_CURSOR_SHA,
    "deployed_source_sha": PR38_INSTALLED_BASE_SHA,
    "prod_installed_sha": PR38_INSTALLED_BASE_SHA,
    "reason": "staging_smoke_failed",
}
PR38_HELPER_TRUST_ANCHOR_SHA = "4018d27e719a4f54b279df1c33e1fe8d69882855"
PR38_HELPER_TRUST_ANCHOR_TREE = "49299006dd4db445f368351ef26f7e2173586a27"
PR38_EXECUTOR_HELPER_SHA256 = "2a6c8a222dee5d19e505083d8311f548d8effafcb3fd68f856d8a24c82fa46f7"


def command(*args: str, cwd: Path | None = None, timeout: int = 600) -> str:
    result = subprocess.run(args, cwd=cwd, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=timeout)
    if result.returncode:
        raise RuntimeError(f"command failed ({result.returncode}): {args[0]} {args[1] if len(args)>1 else ''}: {result.stderr[-500:]}")
    return result.stdout.strip()


def git(repo: Path, *args: str) -> str:
    git_args = ("git", "-C", str(repo), *args)
    if args[:1] == ("fetch",):
        return command(*git_args, timeout=GITHUB_FETCH_TIMEOUT_SECONDS)
    return command(*git_args)


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def require_official_origin(repo: Path) -> None:
    origin = git(repo, "remote", "get-url", "origin")
    if origin not in {f"git@github.com:{REPO}.git", f"https://github.com/{REPO}.git"}:
        raise RuntimeError("source origin is not the official GitHub repository")


def _parse_utc(value: str | None) -> datetime | None:
    if not isinstance(value, str) or not value:
        return None
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError:
        return None
    if parsed.tzinfo is None:
        return None
    return parsed.astimezone(timezone.utc)


def _seconds_since(value: str | None) -> int | None:
    parsed = _parse_utc(value)
    if parsed is None:
        return None
    return max(0, round((datetime.now(timezone.utc) - parsed).total_seconds()))


def exact_check_success(sha: str, token: str | None = None, *, observation: dict | None = None) -> bool:
    if not SHA.fullmatch(sha):
        raise ValueError("invalid commit SHA")
    url = f"https://api.github.com/repos/{REPO}/commits/{sha}/check-runs?check_name=check&per_page=100"
    headers = {"Accept": "application/vnd.github+json", "User-Agent": "aicrm-domestic-release/1", "X-GitHub-Api-Version": "2022-11-28"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    with urllib.request.urlopen(urllib.request.Request(url, headers=headers), timeout=20) as response:
        data = json.load(response)
    # Multiple workflow reruns may exist. The newest run must succeed: an old
    # green run cannot override a later failed rerun on the same SHA.
    runs = [item for item in data.get("check_runs", []) if item.get("name") == "check" and item.get("head_sha") == sha and item.get("app", {}).get("slug") == "github-actions"]
    if not runs:
        if observation is not None:
            observation.clear()
            observation.update({"head_sha": sha, "success": False, "status": "missing", "conclusion": None})
        return False
    newest = max(runs, key=lambda item: (item.get("started_at") or "", item.get("id") or 0))
    success = newest.get("status") == "completed" and newest.get("conclusion") == "success"
    if observation is not None:
        started = _parse_utc(newest.get("started_at"))
        completed = _parse_utc(newest.get("completed_at"))
        observation.clear()
        observation.update({
            "head_sha": sha,
            "run_id": newest.get("id"),
            "started_at": newest.get("started_at"),
            "completed_at": newest.get("completed_at"),
            "duration_seconds": max(0, round((completed - started).total_seconds(), 1)) if started and completed else None,
            "success": success,
            "status": newest.get("status"),
            "conclusion": newest.get("conclusion"),
        })
    return success


class CIHistoryReadError(RuntimeError):
    """A temporary public GitHub API/transport failure while reading CI history."""


def _check_signature(observation: dict) -> dict:
    return {
        key: observation.get(key)
        for key in ("head_sha", "run_id", "started_at", "completed_at", "status", "conclusion")
    }


def _github_public_json(url: str) -> dict:
    request = urllib.request.Request(
        url,
        headers={
            "Accept": "application/vnd.github+json",
            "User-Agent": "aicrm-domestic-release/1",
            "X-GitHub-Api-Version": "2022-11-28",
        },
    )
    try:
        with urllib.request.urlopen(request, timeout=20) as response:
            payload = json.load(response)
    except Exception as exc:
        # The controller has no private GitHub token. Network errors and public
        # API throttling must pause promotion briefly, then be retried without
        # requiring a new application commit or another full CI run.
        raise CIHistoryReadError("GitHub public CI metadata is temporarily unavailable") from exc
    if not isinstance(payload, dict):
        raise RuntimeError("GitHub public CI response is not an object")
    return payload


def _main_ci_run_pages(start_page: int = 1):
    """Yield public main-branch CI pages without a token or artifact ZIP."""
    if type(start_page) is not int or start_page < 1 or start_page > CI_RUNS_MAX_PAGES:
        return
    for page in range(start_page, CI_RUNS_MAX_PAGES + 1):
        url = (
            f"https://api.github.com/repos/{REPO}/actions/workflows/ci.yml/runs"
            f"?branch=main&per_page={CI_RUNS_PAGE_SIZE}&page={page}"
        )
        payload = _github_public_json(url)
        batch = payload.get("workflow_runs")
        if not isinstance(batch, list):
            raise RuntimeError("GitHub main CI run list is missing")
        yield [item for item in batch if isinstance(item, dict)]
        if len(batch) < CI_RUNS_PAGE_SIZE:
            return
    raise RuntimeError("GitHub main CI history exceeds the bounded public scan")


def _main_ci_run_jobs(run: dict) -> dict[str, dict]:
    run_id = run.get("id")
    attempt = run.get("run_attempt")
    sha = run.get("head_sha")
    if type(run_id) is not int or run_id <= 0 or type(attempt) is not int or attempt <= 0 or not isinstance(sha, str) or not SHA.fullmatch(sha):
        raise RuntimeError("GitHub main CI run identity is invalid")
    url = f"https://api.github.com/repos/{REPO}/actions/runs/{run_id}/attempts/{attempt}/jobs?per_page=100"
    payload = _github_public_json(url)
    raw_jobs = payload.get("jobs")
    if not isinstance(raw_jobs, list):
        raise RuntimeError("GitHub exact CI attempt jobs are missing")
    jobs: dict[str, dict] = {}
    for item in raw_jobs:
        if not isinstance(item, dict):
            continue
        name = item.get("name")
        if not isinstance(name, str):
            continue
        # A job from another SHA or rerun attempt is never proof for this run.
        if item.get("head_sha") != sha or item.get("run_attempt") != attempt:
            continue
        if name in jobs:
            raise RuntimeError(f"GitHub exact CI attempt repeats job {name}")
        jobs[name] = item
    return jobs


def _main_ci_run_classification(run: dict, jobs: dict[str, dict]) -> tuple[str, str]:
    """Classify a run from its own exact attempt jobs; no PR-controlled flags are read."""
    event = run.get("event")
    force_full = event == "workflow_dispatch" and "[force_full]" in str(run.get("display_title", ""))
    if run.get("path") != CI_WORKFLOW_PATH or run.get("head_branch") != "main":
        return "not_full", "run is outside protected main CI"
    if event not in {"schedule", "push", "workflow_dispatch"} or (event == "workflow_dispatch" and not force_full):
        return "not_full", "run is not an eligible main full-check trigger"

    sha = run.get("head_sha")
    attempt = run.get("run_attempt")
    expected = {name for name in CI_REQUIRED_JOBS}
    if not isinstance(sha, str) or not SHA.fullmatch(sha) or type(attempt) is not int or attempt <= 0:
        return "unknown", "eligible run identity is invalid"
    if any(name not in jobs for name in expected):
        return "unknown", "eligible full run is missing a required job"
    for name in expected:
        if jobs[name].get("head_sha") != sha or jobs[name].get("run_attempt") != attempt:
            return "unknown", "required job does not match the exact run attempt"

    auxiliaries = ("plan", "governance", "check")
    check_steps = jobs["check"].get("steps")
    marker = next(
        (step for step in check_steps if isinstance(step, dict) and step.get("name") == CI_FULL_RESULT_STEP),
        None,
    ) if isinstance(check_steps, list) else None
    if marker is None:
        if event == "push":
            # Pre-PR2 main pushes can have old CI failures or skipped lanes.
            # They are not regression records and must not bootstrap a pause.
            return "not_full", "legacy main push has no full-regression record step"
        return "unknown", "eligible full run is missing its full-regression record step"
    all_aux_success = all(
        jobs[name].get("status") == "completed" and jobs[name].get("conclusion") == "success"
        for name in auxiliaries
    )
    lane_conclusions = [jobs[name].get("conclusion") for name in CI_FULL_LANES]
    if all(value == "skipped" for value in lane_conclusions) and all_aux_success:
        if attempt > 1:
            return "unknown", "rerun attempt skipped every lane, so an earlier attempt may remain unresolved"
        return "verified", "all lanes were skipped; this cannot clear a full-run failure"
    if any(value == "skipped" for value in lane_conclusions):
        return "unknown", "eligible full run skipped a required lane"
    if not all_aux_success:
        return "unknown", "eligible full run plan, governance, or required check did not succeed"
    if any(
        jobs[name].get("status") != "completed" or jobs[name].get("conclusion") != "success"
        for name in CI_FULL_LANES
    ):
        return "unknown", "eligible full run did not complete every verification lane successfully"
    return "success", "all full verification lanes, governance, and check succeeded"


def _main_first_parent_positions(repo: Path, head_sha: str) -> dict[str, int]:
    if not SHA.fullmatch(head_sha):
        raise ValueError("invalid protected main head")
    chain = git(repo, "rev-list", "--first-parent", head_sha).splitlines()
    if not chain or chain[0] != head_sha or any(not SHA.fullmatch(item) for item in chain):
        raise RuntimeError("protected main first-parent chain is invalid")
    return {sha: len(chain) - index - 1 for index, sha in enumerate(chain)}


def _main_full_regression_blocker(
    repo: Path,
    candidate_sha: str,
    main_head_sha: str,
    *,
    cache: dict | None = None,
) -> dict | None:
    """Stop a candidate behind an unresolved full-main regression or unknown run."""
    if not SHA.fullmatch(candidate_sha) or not SHA.fullmatch(main_head_sha):
        raise ValueError("invalid main regression candidate identity")
    positions = _main_first_parent_positions(repo, main_head_sha)
    if candidate_sha not in positions:
        raise RuntimeError("release candidate is not on protected main first-parent history")
    candidate_position = positions[candidate_sha]
    cache = cache if cache is not None else {}
    if cache.get("head_sha") != main_head_sha:
        cache.clear()
        cache["head_sha"] = main_head_sha
        cache["pages"] = []
        cache["complete"] = False
    pages: list[list[dict]] = cache.setdefault("pages", [])
    later_success_positions: list[int] = []
    def scan_page(page: list[dict]) -> dict | None:
        runs = [
            run for run in page
            if run.get("path") == CI_WORKFLOW_PATH
            and run.get("head_branch") == "main"
            and run.get("event") in {"schedule", "push", "workflow_dispatch"}
            and (run.get("event") != "workflow_dispatch" or "[force_full]" in str(run.get("display_title", "")))
        ]
        runs.sort(
            key=lambda run: (
                str(run.get("run_started_at") or run.get("updated_at") or run.get("created_at") or ""),
                int(run.get("id") or 0),
                int(run.get("run_attempt") or 0),
            ),
            reverse=True,
        )
        for run in runs:
            sha = run.get("head_sha")
            if not isinstance(sha, str) or not SHA.fullmatch(sha):
                return {"run_id": run.get("id"), "run_attempt": run.get("run_attempt"), "sha": None, "classification": "unknown", "reason": "eligible main run SHA is invalid"}
            run_position = positions.get(sha)
            if run_position is None:
                return {"run_id": run.get("id"), "run_attempt": run.get("run_attempt"), "sha": sha, "classification": "unknown", "reason": "eligible main run is outside the current first-parent chain"}
            try:
                job_key = (run.get("id"), run.get("run_attempt"))
                if job_key not in cache:
                    cache[job_key] = _main_ci_run_jobs(run)
                jobs = cache[job_key]
                outcome, reason = _main_ci_run_classification(run, jobs)
            except CIHistoryReadError as exc:
                return {
                    "run_id": run.get("id"),
                    "run_attempt": run.get("run_attempt"),
                    "sha": sha,
                    "classification": "unknown",
                    "transient_read_error": True,
                    "reason": str(exc),
                }
            except Exception as exc:
                outcome, reason = "unknown", f"exact run jobs could not be verified: {type(exc).__name__}"
            if outcome == "not_full" or outcome == "verified":
                continue
            if outcome == "success":
                later_success_positions.append(run_position)
                # A full run on the current tip covers every older first-parent SHA.
                if run_position == positions[main_head_sha]:
                    return {"_stop": True}
                continue
            if run_position > candidate_position:
                # A future regression does not prevent earlier commits from being released.
                continue
            cleared = any(success_position >= run_position for success_position in later_success_positions)
            if not cleared:
                return {
                    "run_id": run.get("id"),
                    "run_attempt": run.get("run_attempt"),
                    "sha": sha,
                    "classification": "unknown",
                    "reason": reason,
                }
        return {"_continue": True}

    for page in pages:
        result = scan_page(page)
        if result is not None and result.get("_stop"):
            return None
        if result is not None and not result.get("_continue"):
            return result
    if cache.get("complete"):
        return None
    start_page = len(pages) + 1
    try:
        for page in _main_ci_run_pages(start_page):
            pages.append(page)
            result = scan_page(page)
            if result is not None and result.get("_stop"):
                return None
            if result is not None and not result.get("_continue"):
                return result
        cache["complete"] = True
    except CIHistoryReadError as exc:
        return {
            "run_id": None,
            "run_attempt": None,
            "sha": main_head_sha,
            "classification": "unknown",
            "transient_read_error": True,
            "reason": str(exc),
        }
    return None


def _ci_retry_after_utc() -> str:
    retry_at = datetime.fromtimestamp(time.time() + CI_HISTORY_RETRY_SECONDS, timezone.utc)
    return retry_at.isoformat(timespec="seconds").replace("+00:00", "Z")


def _ci_retry_is_due(pause: dict) -> bool:
    retry_at = _parse_utc(pause.get("retry_after_utc"))
    return retry_at is not None and retry_at <= datetime.now(timezone.utc)


def _record_ci_pause_retry(pause: dict, *, transient: bool = True) -> None:
    if transient:
        pause["retry_after_utc"] = _ci_retry_after_utc()
    else:
        pause.pop("retry_after_utc", None)


def _watch_main_check(sha: str) -> dict:
    observation: dict = {}
    try:
        exact_check_success(sha, observation=observation)
    except Exception:
        return {"head_sha": sha, "run_id": None, "status": "unavailable", "conclusion": None}
    return _check_signature(observation)


def _enter_ci_regression_pause(
    state_path: Path,
    state: dict,
    repo: Path,
    candidate_sha: str,
    main_head_sha: str,
    blocker: dict,
    candidate_check_observation: dict | None = None,
) -> dict:
    if candidate_sha == main_head_sha and candidate_check_observation is not None:
        watched_check = _check_signature(candidate_check_observation)
    else:
        watched_check = _watch_main_check(main_head_sha)
    pause = {
        "candidate_sha": candidate_sha,
        "main_head_sha": main_head_sha,
        "classification": "unknown",
        "blocker": blocker,
        "watched_check": watched_check,
        "blocked_at_utc": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
    }
    if blocker.get("transient_read_error") is True:
        _record_ci_pause_retry(pause)
    state.update(
        status="ci_regression_blocked",
        failure="main full CI evidence is failing or incomplete; release queue is paused",
        ci_regression_pause=pause,
    )
    atomic_json(state_path, state)
    return {"status": "ci_regression_blocked", "sha": candidate_sha, "blocker": blocker}


def _retry_ci_regression_pause(state_path: Path, state: dict, repo: Path, main_head_sha: str) -> bool:
    """Retry transient history reads after backoff; retry CI evidence on a new green check."""
    pause = state.get("ci_regression_pause")
    if not isinstance(pause, dict) or not isinstance(pause.get("blocker"), dict):
        raise RuntimeError("CI regression pause ledger is invalid")
    retry_is_due = _ci_retry_is_due(pause)
    head_changed = main_head_sha != pause.get("main_head_sha")
    if pause.get("retry_after_utc") and not retry_is_due and not head_changed:
        return False
    observation: dict = {}
    try:
        success = exact_check_success(main_head_sha, observation=observation)
    except Exception:
        pause["main_head_sha"] = main_head_sha
        _record_ci_pause_retry(pause)
        state["ci_regression_pause"] = pause
        atomic_json(state_path, state)
        return False
    signature = _check_signature(observation)
    prior_signature = pause.get("watched_check")
    changed = main_head_sha != pause.get("main_head_sha") or signature != prior_signature
    retry_transient_history = (
        pause.get("blocker", {}).get("transient_read_error") is True
        and _ci_retry_is_due(pause)
    )
    if not changed and not retry_transient_history:
        if retry_is_due:
            pause.pop("retry_after_utc", None)
            state["ci_regression_pause"] = pause
            atomic_json(state_path, state)
        return False
    pause["main_head_sha"] = main_head_sha
    pause["watched_check"] = signature
    if not success:
        if retry_transient_history:
            _record_ci_pause_retry(pause)
        elif retry_is_due:
            pause.pop("retry_after_utc", None)
        state["ci_regression_pause"] = pause
        atomic_json(state_path, state)
        return False
    try:
        blocker = _main_full_regression_blocker(repo, main_head_sha, main_head_sha)
    except Exception as exc:
        blocker = {
            "run_id": None,
            "run_attempt": None,
            "sha": main_head_sha,
            "classification": "unknown",
            "transient_read_error": isinstance(exc, CIHistoryReadError),
            "reason": f"full CI history could not be reverified: {type(exc).__name__}",
        }
    if blocker is not None:
        pause["blocker"] = blocker
        _record_ci_pause_retry(pause, transient=blocker.get("transient_read_error") is True)
        state["ci_regression_pause"] = pause
        atomic_json(state_path, state)
        return False
    state.pop("ci_regression_pause", None)
    state.update(status="ready", failure=None)
    atomic_json(state_path, state)
    return True


def _git_file_sha256(repo: Path, sha: str, path: str) -> str:
    result = subprocess.run(
        ["git", "-C", str(repo), "show", f"{sha}:{path}"],
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    if result.returncode:
        raise RuntimeError(f"checked controller source is missing: {path}")
    return hashlib.sha256(result.stdout).hexdigest()


def _smoke_helper_selection(repo: Path, source_sha: str, checked_main_sha: str | None = None) -> dict[str, str]:
    """Bind a smoke source helper to the exact executor helper used for the test."""
    source_tree = git(repo, "rev-parse", f"{source_sha}^{{tree}}")
    source_helper_sha = _git_file_sha256(repo, source_sha, "deploy/domestic-promote.py")
    if source_sha != PR38_SMOKE_SOURCE_SHA:
        return {
            "source_tree": source_tree,
            "source_helper_sha256": source_helper_sha,
            "executor_helper_sha256": source_helper_sha,
            "executor_source_sha": source_sha,
            "executor_source_tree": source_tree,
            "compatibility": "exact_source",
        }

    if source_tree != PR38_SMOKE_SOURCE_TREE or source_helper_sha != PR38_SOURCE_HELPER_SHA256:
        raise RuntimeError("PR38 smoke source identity differs from the reviewed compatibility pair")
    if not isinstance(checked_main_sha, str) or not SHA.fullmatch(checked_main_sha):
        raise RuntimeError("PR38 smoke compatibility requires the exact checked main SHA")

    anchor_tree = git(repo, "rev-parse", f"{PR38_HELPER_TRUST_ANCHOR_SHA}^{{tree}}")
    if anchor_tree != PR38_HELPER_TRUST_ANCHOR_TREE:
        raise RuntimeError("PR38 smoke helper trust anchor tree changed")
    git(repo, "merge-base", "--is-ancestor", source_sha, PR38_HELPER_TRUST_ANCHOR_SHA)
    git(repo, "merge-base", "--is-ancestor", PR38_HELPER_TRUST_ANCHOR_SHA, checked_main_sha)
    anchor_helper_sha = _git_file_sha256(repo, PR38_HELPER_TRUST_ANCHOR_SHA, "deploy/domestic-promote.py")
    checked_main_helper_sha = _git_file_sha256(repo, checked_main_sha, "deploy/domestic-promote.py")
    if anchor_helper_sha != PR38_EXECUTOR_HELPER_SHA256 or checked_main_helper_sha != PR38_EXECUTOR_HELPER_SHA256:
        raise RuntimeError("PR38 smoke executor is not the exact reviewed main helper")

    return {
        "source_tree": source_tree,
        "source_helper_sha256": source_helper_sha,
        "executor_helper_sha256": PR38_EXECUTOR_HELPER_SHA256,
        "executor_source_sha": PR38_HELPER_TRUST_ANCHOR_SHA,
        "executor_source_tree": PR38_HELPER_TRUST_ANCHOR_TREE,
        "compatibility": "pr38_source_to_reviewed_main_executor",
    }


def _validate_pr38_staging_failed_ledger(state: dict, queue: list[str]) -> None:
    """Allow only the observed PR38 smoke-only failure to resume under poll's lock."""
    smoke = state.get("last_stage_smoke")
    marker = state.get("pr38_staging_failed_resume")
    marker_present = marker == PR38_RESUME_MARKER and state.get("status") in {
        "staging_failed", "ci_regression_blocked", "controller_update_required", "ready",
    }
    status = state.get("status")
    failure = state.get("failure")
    controller_failure = isinstance(failure, str) and failure.startswith("fixed controller files are not installed and verified:")
    if status == "staging_failed":
        failure_matches = failure == "staging installed behavior smoke failed: RuntimeError" or (marker_present and controller_failure)
    elif status == "ci_regression_blocked":
        pause = state.get("ci_regression_pause")
        failure_matches = (
            marker_present
            and failure == "main full CI evidence is failing or incomplete; release queue is paused"
            and isinstance(pause, dict)
            and pause.get("candidate_sha") == PR38_SMOKE_SOURCE_SHA
        )
    elif status == "controller_update_required":
        failure_matches = marker_present and controller_failure
    else:
        failure_matches = marker_present and failure is None
    if (
        not (state.get("status") == "staging_failed" or marker_present)
        or state.get("blocked_sha") != PR38_SMOKE_SOURCE_SHA
        or state.get("processed_sha") != PR38_PREVIOUS_CURSOR_SHA
        or state.get("deployed_source_sha") != PR38_INSTALLED_BASE_SHA
        or state.get("prod_installed_sha") != PR38_INSTALLED_BASE_SHA
        or not failure_matches
        or not isinstance(smoke, dict)
        or smoke.get("source_sha") != PR38_SMOKE_SOURCE_SHA
        or smoke.get("installed_sha") != PR38_INSTALLED_BASE_SHA
        or smoke.get("status") != "failed"
        or state.get("staging_verified_sha") == PR38_SMOKE_SOURCE_SHA
        or ("staging_verified_receipt" in state and state.get("staging_verified_sha") is None)
        or state.get("pr38_staging_failed_smoke_attempted") is not None
        or not queue
        or queue[0] != PR38_SMOKE_SOURCE_SHA
    ):
        raise RuntimeError("staging_failed resume is restricted to the exact unpromoted PR38 smoke failure")


def _verify_pr38_staging_failed_readbacks(config: dict, state: dict) -> dict:
    """Prove PR38 has no installed receipt and both hosts remain healthy on PR36."""
    installed = state.get("prod_installed_sha")
    manifest = state.get("prod_installed_manifest_sha256")
    if installed != PR38_INSTALLED_BASE_SHA or not isinstance(manifest, str) or not FILE_SHA.fullmatch(manifest):
        raise RuntimeError("PR38 staging_failed resume has no exact installed baseline")
    stage = stage_readback(PR38_SMOKE_SOURCE_SHA)
    production = prod_readback(config, PR38_SMOKE_SOURCE_SHA)
    for role, result in (("staging", stage), ("production", production)):
        verify_readback(result, installed, manifest)
        if result.get("receipt_target_sha") != PR38_SMOKE_SOURCE_SHA or result.get("receipt_exists") is not False:
            raise RuntimeError(f"PR38 {role} success receipt already exists; reconcile before resuming")
        if result.get("database_backup_exists") is not False:
            raise RuntimeError(f"PR38 {role} backup state is unexpected; reconcile before resuming")
    return {
        "staging_readyz": stage["readyz"],
        "staging_current": stage["current"],
        "production_readyz": production["readyz"],
        "production_current": production["current"],
        "manifest_sha256": manifest,
    }


def _local_file_sha256(path: Path) -> str:
    if path.is_symlink() or not path.is_file():
        raise RuntimeError("fixed controller file is missing or unsafe")
    return sha256_file(path)


def _helper_fixed_path(value: str) -> str:
    parts = shlex.split(value)
    if len(parts) != 1 or not parts[0].startswith("/") or not re.fullmatch(r"/[A-Za-z0-9_./-]+", parts[0]):
        raise RuntimeError("fixed installer helper path is missing or unsafe")
    return parts[0]


def _remote_file_sha256(config: dict, path: str) -> str:
    quoted = shlex.quote(path)
    script = f"test ! -L {quoted} && test -f {quoted} && sha256sum -- {quoted}"
    output = command(*ssh_args(config), script, timeout=30)
    match = re.fullmatch(r"([0-9a-f]{64})\s+.*", output)
    if not match:
        raise RuntimeError("remote fixed controller readback is invalid")
    return match.group(1)


def verify_controller_installation(
    config: dict,
    repo: Path,
    sha: str,
    paths: list[str],
    *,
    checked_main_sha: str | None = None,
) -> dict:
    """Verify fixed tools against the candidate or the exact checked main source."""
    started = time.monotonic()
    local_release = Path(__file__)
    local_builder = local_release.with_name("domestic_release_build.py")
    results: dict[str, dict[str, str]] = {}
    helper_selection = _smoke_helper_selection(repo, sha, checked_main_sha)
    checked_main_tree = None
    if checked_main_sha is not None:
        if not SHA.fullmatch(checked_main_sha):
            raise RuntimeError("checked main controller source SHA is invalid")
        checked_main_tree = git(repo, "rev-parse", f"{checked_main_sha}^{{tree}}")
    if helper_selection["compatibility"] == "pr38_source_to_reviewed_main_executor":
        candidate_builder_sha = _git_file_sha256(repo, sha, "scripts/domestic_release_build.py")
        checked_main_builder_sha = _git_file_sha256(repo, checked_main_sha, "scripts/domestic_release_build.py")
        if candidate_builder_sha != PR38_FORWARD_BUILDER_SHA256 or checked_main_builder_sha != PR38_FORWARD_BUILDER_SHA256:
            raise RuntimeError("PR38 forward controller builder is not the exact reviewed source")
    for source_path in paths:
        if source_path not in CONTROLLER_SOURCE_PATHS:
            raise RuntimeError(f"unsupported fixed controller path: {source_path}")
        candidate_expected = _git_file_sha256(repo, sha, source_path)
        if source_path == "scripts/domestic_release.py":
            actual = _local_file_sha256(local_release)
            locations = {"staging": actual}
        elif source_path == "scripts/domestic_release_build.py":
            actual = _local_file_sha256(local_builder)
            locations = {"staging": actual}
        else:
            stage_path = _helper_fixed_path(config["stage_helper"])
            prod_path = _helper_fixed_path(config["prod_helper"])
            locations = {
                "staging": _local_file_sha256(Path(stage_path)),
                "production": _remote_file_sha256(config, prod_path),
            }

        allowed_expected = candidate_expected
        compatibility = "exact_candidate"
        checked_main_expected = None
        if source_path in {"scripts/domestic_release.py", "scripts/domestic_release_build.py"} and checked_main_sha:
            checked_main_expected = _git_file_sha256(repo, checked_main_sha, source_path)
            if checked_main_expected != candidate_expected:
                if (
                    sha != PR38_SMOKE_SOURCE_SHA
                    or helper_selection["compatibility"] != "pr38_source_to_reviewed_main_executor"
                ):
                    raise RuntimeError("forward controller compatibility is restricted to the reviewed PR38 source")
                allowed_expected = checked_main_expected
                compatibility = "exact_checked_main_controller"
        elif source_path == "deploy/domestic-promote.py" and helper_selection["compatibility"] == "pr38_source_to_reviewed_main_executor":
            allowed_expected = helper_selection["executor_helper_sha256"]
            compatibility = helper_selection["compatibility"]

        results[source_path] = {
            "candidate_sha256": candidate_expected,
            "allowed_installed_sha256": allowed_expected,
            "checked_main_sha256": checked_main_expected or "",
            "compatibility": compatibility,
            **{f"{name}_sha256": value for name, value in locations.items()},
        }
        if any(value != allowed_expected for value in locations.values()):
            raise RuntimeError(f"fixed controller digest mismatch: {source_path}")
    return {
        "source_sha": sha,
        "source_tree": git(repo, "rev-parse", f"{sha}^{{tree}}"),
        "checked_main_sha": checked_main_sha,
        "checked_main_tree": checked_main_tree,
        "files": results,
        "verified_at_utc": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        "duration_seconds": round(time.monotonic() - started, 1),
        "status": "matched",
    }


def github_commit_tree(sha: str, token: str | None = None) -> str:
    if not SHA.fullmatch(sha):
        raise ValueError("invalid commit SHA")
    headers = {"Accept": "application/vnd.github+json", "User-Agent": "aicrm-domestic-release/1", "X-GitHub-Api-Version": "2022-11-28"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    with urllib.request.urlopen(urllib.request.Request(f"https://api.github.com/repos/{REPO}/git/commits/{sha}", headers=headers), timeout=20) as response:
        data = json.load(response)
    tree = data.get("tree", {}).get("sha")
    if data.get("sha") != sha or not isinstance(tree, str) or not SHA.fullmatch(tree):
        raise RuntimeError("GitHub commit/tree response mismatch")
    return tree


def first_parent_queue(repo: Path, base: str, head: str) -> list[str]:
    if not SHA.fullmatch(base) or not SHA.fullmatch(head):
        raise ValueError("invalid queue cursor")
    command("git", "-C", str(repo), "merge-base", "--is-ancestor", base, head)
    values = git(repo, "rev-list", "--first-parent", "--reverse", f"{base}..{head}").splitlines()
    parent = base
    for sha in values:
        if not SHA.fullmatch(sha) or git(repo, "rev-parse", f"{sha}^1") != parent:
            raise RuntimeError("first-parent chain changed")
        parent = sha
    return values


def atomic_json(path: Path, value: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, name = tempfile.mkstemp(dir=path.parent, prefix=f".{path.name}.")
    try:
        with os.fdopen(fd, "w") as stream:
            json.dump(value, stream, sort_keys=True, indent=2)
            stream.write("\n")
            stream.flush()
            os.fsync(stream.fileno())
        os.chmod(name, 0o600)
        os.replace(name, path)
    finally:
        Path(name).unlink(missing_ok=True)


def load_config(path: Path) -> dict:
    config = json.loads(path.read_text())
    required = ("repo", "work_root", "state", "stage_incoming", "stage_helper", "prod_host", "prod_user", "prod_key", "prod_known_hosts", "prod_incoming", "prod_helper")
    if any(not isinstance(config.get(key), str) or not config[key] for key in required):
        raise ValueError("missing domestic release config")
    if not isinstance(config.get("production_enabled"), bool):
        raise ValueError("production_enabled must be explicit")
    return config


def ssh_args(config: dict) -> list[str]:
    return ["ssh", "-i", config["prod_key"], "-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes", "-o", "StrictHostKeyChecking=yes", "-o", f"UserKnownHostsFile={config['prod_known_hosts']}", "-o", "ConnectTimeout=15", f"{config['prod_user']}@{config['prod_host']}"]


def prod_readback(config: dict, receipt_sha: str | None = None) -> dict:
    # Entirely read-only. Also used after a result-unknown SSH interruption.
    if receipt_sha is not None and not SHA.fullmatch(receipt_sha):
        raise ValueError("invalid receipt SHA")
    args = ["sudo python3 -c " + shlex.quote(HOST_READBACK_CODE)]
    if receipt_sha is not None:
        args.append(receipt_sha)
    return json.loads(command(*ssh_args(config), *args, timeout=30))


def stage_readback(receipt_sha: str | None = None) -> dict:
    if receipt_sha is not None and not SHA.fullmatch(receipt_sha):
        raise ValueError("invalid receipt SHA")
    args = ["sudo", "python3", "-c", HOST_READBACK_CODE]
    if receipt_sha is not None:
        args.append(receipt_sha)
    return json.loads(command(*args, timeout=30))


def bind_baseline(config: dict, prod_preview_sha: str) -> dict:
    """One-time binding: installed preview and protected main have one tree."""
    if not SHA.fullmatch(prod_preview_sha):
        raise ValueError("invalid production preview SHA")
    repo = Path(config["repo"])
    state_path = Path(config["state"])
    if state_path.exists():
        raise RuntimeError("baseline already exists; never overwrite the release ledger")
    require_official_origin(repo)
    git(repo, "fetch", "--no-tags", "origin", "main")
    main_sha = git(repo, "rev-parse", "refs/remotes/origin/main")
    main_tree = git(repo, "rev-parse", f"{main_sha}^{{tree}}")
    preview_tree = github_commit_tree(prod_preview_sha, os.environ.get("GITHUB_TOKEN"))
    if main_tree != preview_tree:
        raise RuntimeError("installed preview and current main differ in source tree")
    verify_readback(prod_readback(config), prod_preview_sha)
    manifest_sha = command(*ssh_args(config), "sha256sum", "/opt/aicrm/current/release-files.sha256", timeout=30).split()[0]
    receipt = json.loads(command(*ssh_args(config), "sudo", "cat", f"/opt/aicrm/release-success/{prod_preview_sha}.json", timeout=30))
    if receipt.get("release_sha") != prod_preview_sha or receipt.get("manifest_sha256") != manifest_sha:
        raise RuntimeError("installed production receipt or manifest mismatch")
    stage_release = Path(config["work_root"]) / "builds" / main_sha / "release"
    if not stage_release.is_dir():
        raise RuntimeError("staging baseline package is missing")
    stage_marker = Path("/opt/aicrm/current/release.env")
    if not stage_marker.is_file() or stage_marker.read_text() != f"AICRM_RELEASE_SHA={main_sha}\n":
        raise RuntimeError("staging baseline is not installed")
    state = {"schema_version": 1, "status": "ready", "processed_sha": main_sha, "deployed_source_sha": main_sha, "prod_installed_sha": prod_preview_sha, "prod_installed_manifest_sha256": manifest_sha, "baseline_tree": main_tree, "baseline_prod_manifest_sha256": manifest_sha, "blocked_sha": None, "failure": None}
    atomic_json(state_path, state)
    return state


def verify_readback(data: dict, sha: str, manifest_sha256: str | None = None) -> None:
    if data.get("release_env") != f"AICRM_RELEASE_SHA={sha}\n" or data.get("readyz", {}).get("release_sha") != sha or data.get("readyz", {}).get("status") != "ready":
        raise RuntimeError("production version or readyz mismatch")
    if data.get("current") != f"/opt/aicrm/releases/{sha}":
        raise RuntimeError("installed release directory mismatch")
    if manifest_sha256 is not None and data.get("manifest_sha256") != manifest_sha256:
        raise RuntimeError("installed manifest digest mismatch")
    services = data.get("services", {})
    for unit in ("aicrm.service", "aicrm-effects-worker.service"):
        if services.get(unit, {}).get("active") is not True or services[unit].get("pid", 0) <= 0:
            raise RuntimeError(f"installed service not active: {unit}")
        expected_executable = f"/opt/aicrm/releases/{sha}/bin/aicrm"
        if services[unit].get("executable") != expected_executable:
            raise RuntimeError(f"installed service executable mismatch: {unit}")


def verify_install_receipt(receipt: dict | None, metadata: dict, previous_sha: str) -> None:
    if not isinstance(receipt, dict):
        raise RuntimeError("production success receipt is missing or unsafe")
    expected = {
        "source_sha": metadata["source_sha"],
        "source_tree": metadata.get("source_tree"),
        "manifest_sha256": metadata["release_files_sha256"],
        "previous_sha": previous_sha,
        "technical_status": "installed_healthy",
    }
    if any(receipt.get(key) != value for key, value in expected.items()):
        raise RuntimeError("production success receipt mismatch")


def copy_payload(config: dict, sha: str, payload: Path, metadata: Path, link_sha: str) -> tuple[str, str]:
    incoming = f"{config['prod_incoming'].rstrip('/')}/{sha}"
    remote_meta = f"{config['prod_incoming'].rstrip('/')}/{sha}.json"
    # Dedicated account can only write incoming; root-owned helper revalidates
    # and moves into immutable releases under the shared host lock.
    command(*ssh_args(config), "mkdir", "-m", "0700", "--", incoming, timeout=30)
    transport = " ".join(shlex.quote(part) for part in ssh_args(config)[:-1])
    # -e is a fixed local command string; config is provisioned by root.
    # Protected Linux hardlinks forbid the unprivileged receiver from linking
    # root-owned previous releases. Receive through the fixed sudo rsync path;
    # the installer still verifies every byte before making it current.
    command("rsync", "-a", "--no-owner", "--no-group", "--no-perms", "--checksum", "--delete", "--rsync-path=sudo rsync", f"--link-dest=/opt/aicrm/releases/{link_sha}", "-e", transport, f"{payload}/", f"{config['prod_user']}@{config['prod_host']}:{incoming}/", timeout=600)
    command("rsync", "-a", "-e", transport, str(metadata), f"{config['prod_user']}@{config['prod_host']}:{remote_meta}", timeout=60)
    return incoming, remote_meta


def build_candidate(
    config: dict,
    sha: str,
    base: str,
    base_release: Path | None,
    validation_scope_base: str | None = None,
) -> tuple[Path, dict]:
    repo = Path(config["repo"])
    work_root = Path(config["work_root"])
    if not SHA.fullmatch(sha) or not SHA.fullmatch(base):
        raise ValueError("invalid build commit")
    validation_scope_base = validation_scope_base or base
    if not SHA.fullmatch(validation_scope_base):
        raise ValueError("invalid validation scope base commit")
    worker = BUILD_ROOT / sha
    checkout = worker / "source"
    worker_out = worker / "out"
    out = work_root / "builds" / sha
    if worker.exists() or out.exists():
        raise RuntimeError("existing build path needs inspection; refusing to reuse stale artifacts")
    if not BUILD_ROOT.is_dir() or BUILD_ROOT.is_symlink():
        raise RuntimeError("isolated build account is not provisioned")
    out.parent.mkdir(parents=True, exist_ok=True)
    # Source hooks and npm/go build scripts execute as a separate account that
    # cannot read the production SSH key, sudo, state ledger or runtime env.
    build_prefix = ["sudo", "-u", BUILD_USER, "-H", "--"]
    command(*build_prefix, "mkdir", "-m", "0755", str(worker), timeout=30)
    command(*build_prefix, "git", "-c", f"safe.directory={repo.resolve()}", "clone", "-q", "--no-checkout", "--local", "--no-hardlinks", str(repo), str(checkout), timeout=120)
    command(*build_prefix, "git", "-C", str(checkout), "-c", f"safe.directory={repo.resolve()}", "fetch", "-q", "--no-tags", str(repo), sha, timeout=120)
    command(*build_prefix, "git", "-C", str(checkout), "cat-file", "-e", f"{sha}^{{commit}}", timeout=30)
    environment = [
        f"HOME={BUILD_ROOT}", f"PATH={config.get('build_path', os.environ['PATH'])}",
        f"GOCACHE={BUILD_ROOT / 'cache/go-build'}",
        f"GOMODCACHE={BUILD_ROOT / 'cache/go-mod'}",
        f"npm_config_cache={BUILD_ROOT / 'cache/npm'}",
        f"TMPDIR={BUILD_ROOT / 'tmp'}", "PYTHONDONTWRITEBYTECODE=1",
    ]
    args = [*build_prefix, "/usr/bin/env", "-i", *environment, "python3", str(Path(__file__).with_name("domestic_release_build.py")), "build", "--repo", str(checkout), "--base", base, "--validation-scope-base", validation_scope_base, "--target", sha, "--base-release", str(base_release) if base_release is not None else "none", "--out", str(worker_out)]
    command(*args, timeout=7200)
    if worker_out.is_symlink():
        raise RuntimeError("isolated build produced a symlink")
    command(*build_prefix, "chmod", "0755", str(worker_out), timeout=30)
    if any(path.is_symlink() for path in worker_out.rglob("*")):
        raise RuntimeError("isolated build produced a symlink")
    shutil.copytree(worker_out, out)
    out.chmod(0o755)
    metadata = json.loads((out / "domestic-release.json").read_text())
    if (
        metadata.get("source_sha") != sha
        or metadata.get("base_sha") != base
        or metadata.get("source_tree") != git(repo, "rev-parse", f"{sha}^{{tree}}")
        or metadata.get("validation_scope_base_sha") != validation_scope_base
        or metadata.get("validation_scope_base_tree") != git(repo, "rev-parse", f"{validation_scope_base}^{{tree}}")
        or metadata.get("validation_scope_changed_paths") != git(repo, "diff", "--name-only", "--no-renames", validation_scope_base, sha).splitlines()
        or metadata.get("actual_ci_baseline_verified") is not False
    ):
        raise RuntimeError("built artifact source mismatch")
    manifest = out / "release" / "release-files.sha256"
    if hashlib.sha256(manifest.read_bytes()).hexdigest() != metadata.get("release_files_sha256"):
        raise RuntimeError("built artifact manifest mismatch")
    command("sudo", "rm", "-rf", "--", str(worker), timeout=120)
    return out, metadata


def stage_install(config: dict, sha: str, out: Path, base_sha: str, *, timing_sink: dict | None = None) -> dict:
    incoming_root = Path(config["stage_incoming"])
    incoming_root.mkdir(parents=True, exist_ok=True)
    incoming = incoming_root / sha
    if incoming.exists():
        raise RuntimeError("existing staging incoming path needs inspection")
    shutil.copytree(out / "release", incoming, copy_function=shutil.copy2)
    metadata_path = out / "domestic-release.json"
    metadata_bytes = metadata_path.read_bytes()
    metadata = json.loads(metadata_bytes)
    if metadata.get("source_sha") != sha or metadata.get("base_sha") != base_sha:
        raise RuntimeError("staging metadata source or base SHA mismatch")
    metadata_sha = hashlib.sha256(metadata_bytes).hexdigest()
    install_started = time.monotonic()
    try:
        result = command("sudo", config["stage_helper"], "--incoming", str(incoming), "--metadata", str(metadata_path), "--expected-sha", sha, "--metadata-sha256", metadata_sha, "--expected-base", base_sha, timeout=300)
    finally:
        if timing_sink is not None:
            timing_sink["stage_install"] = round(time.monotonic() - install_started, 1)
    receipt = json.loads(result.splitlines()[-1])
    if receipt.get("source_sha") != sha or receipt.get("technical_status") != "installed_healthy":
        raise RuntimeError("staging install receipt mismatch")
    expected_manifest = metadata["release_files_sha256"]
    readback_started = time.monotonic()
    try:
        verify_readback(stage_readback(), sha, expected_manifest)
        if metadata.get("frontend_changed"):
            with urllib.request.urlopen("http://127.0.0.1:8080/login", timeout=5) as response:
                if response.status != 200 or "text/html" not in response.headers.get("Content-Type", ""):
                    raise RuntimeError("staging UI route is not serving HTML")
    finally:
        if timing_sink is not None:
            timing_sink["stage_readback"] = round(time.monotonic() - readback_started, 1)
    return receipt


def _alipay_smoke_required(plan: dict) -> bool:
    """Return whether trusted controller policy requires the installed checkout contract."""
    paths = plan.get("changed_paths", [])
    if not isinstance(paths, list) or not all(isinstance(path, str) for path in paths):
        raise RuntimeError("invalid release impact paths for staging smoke policy")
    return any(
        path == ALIPAY_SMOKE_FIXTURE
        or (path.startswith(ALIPAY_SMOKE_PATHS) and not _alipay_smoke_path_is_test_or_document(path))
        for path in paths
    )


def _alipay_smoke_path_is_test_or_document(path: str) -> bool:
    parsed = PurePosixPath(path)
    name = parsed.name.lower()
    if path.startswith("docs/") or parsed.suffix.lower() in SMOKE_DOC_SUFFIXES:
        return True
    if any(part.lower() in SMOKE_TEST_DIR_NAMES for part in parsed.parts):
        return True
    if name.endswith("_test.go") or name.startswith(("test_", "test-")):
        return True
    if ".test." in name or ".spec." in name:
        return True
    return parsed.parent.as_posix() == "cmd/aicrm" and name.endswith("_chromium_journey.mjs")


def _trusted_changed_paths(repo: Path, base_sha: str, target_sha: str) -> list[str]:
    if not SHA.fullmatch(base_sha) or not SHA.fullmatch(target_sha):
        raise ValueError("invalid release path range")
    return git(repo, "diff", "--name-only", "--no-renames", base_sha, target_sha).splitlines()


def verify_stage_smoke_receipt(
    receipt: dict,
    source_sha: str,
    installed_sha: str,
    manifest_sha256: str,
    helper_selection: dict[str, str],
) -> dict:
    expected = {
        "status": "passed",
        "contract": "alipay_checkout",
        "test_name": "TestDomesticReleaseInstalledAlipayCheckout",
        "test_marker": "domestic_release_installed_alipay_checkout: PASS",
        "stage_role": "staging",
        "source_sha": source_sha,
        "source_tree": helper_selection["source_tree"],
        "installed_sha": installed_sha,
        "manifest_sha256": manifest_sha256,
        "helper_sha256": helper_selection["executor_helper_sha256"],
        "source_helper_sha256": helper_selection["source_helper_sha256"],
        "executor_helper_sha256": helper_selection["executor_helper_sha256"],
        "executor_source_sha": helper_selection["executor_source_sha"],
        "executor_source_tree": helper_selection["executor_source_tree"],
        "helper_compatibility": helper_selection["compatibility"],
    }
    if not isinstance(receipt, dict) or any(receipt.get(key) != value for key, value in expected.items()):
        raise RuntimeError("staging smoke receipt identity mismatch")
    if not FILE_SHA.fullmatch(str(receipt.get("installed_binary_sha256", ""))):
        raise RuntimeError("staging smoke receipt has no installed binary digest")
    timestamp = receipt.get("verified_at_utc")
    if _parse_utc(timestamp) is None:
        raise RuntimeError("staging smoke receipt has no verification timestamp")
    return receipt


def _installed_stage_manifest_sha(config: dict, installed_sha: str) -> str:
    readback = stage_readback(installed_sha)
    if readback.get("current") != f"/opt/aicrm/releases/{installed_sha}":
        raise RuntimeError("staging smoke current release mismatch")
    manifest_sha = readback.get("manifest_sha256")
    if not isinstance(manifest_sha, str) or not FILE_SHA.fullmatch(manifest_sha):
        raise RuntimeError("staging smoke manifest digest is missing")
    artifact = Path(config["work_root"]) / "builds" / installed_sha
    metadata_path = artifact / "domestic-release.json"
    release_path = artifact / "release"
    if metadata_path.is_symlink() or not metadata_path.is_file() or release_path.is_symlink() or not release_path.is_dir():
        raise RuntimeError("staging smoke verified package is missing")
    metadata = json.loads(metadata_path.read_text())
    if metadata.get("source_sha") != installed_sha or metadata.get("release_files_sha256") != manifest_sha:
        raise RuntimeError("staging smoke package metadata does not match the installed release")
    verify_release_artifact(release_path, metadata)
    return manifest_sha


def run_stage_smoke(
    config: dict,
    repo: Path,
    source_sha: str,
    installed_sha: str,
    manifest_sha: str,
    *,
    checked_main_sha: str | None = None,
    timing_sink: dict | None = None,
) -> dict:
    """Run a fixed source fixture against the exact installed staging executable."""
    if not all(SHA.fullmatch(value) for value in (source_sha, installed_sha)) or not FILE_SHA.fullmatch(manifest_sha):
        raise ValueError("invalid staging smoke identity")
    started = time.monotonic()
    helper = config["stage_helper"]
    helper_selection = _smoke_helper_selection(repo, source_sha, checked_main_sha)
    if _local_file_sha256(Path(helper)) != helper_selection["executor_helper_sha256"]:
        raise RuntimeError("fixed staging smoke helper does not match the checked source")
    try:
        output = command(
            "sudo", helper, "--run-staging-smoke",
            "--source-sha", source_sha,
            "--expected-sha", installed_sha,
            "--expected-manifest-sha256", manifest_sha,
            "--expected-helper-sha256", helper_selection["executor_helper_sha256"],
            timeout=900,
        )
        receipt = json.loads(output.splitlines()[-1])
        if not isinstance(receipt, dict):
            raise RuntimeError("staging smoke helper returned an invalid receipt")
        provenance = {
            "source_helper_sha256": helper_selection["source_helper_sha256"],
            "executor_helper_sha256": helper_selection["executor_helper_sha256"],
            "executor_source_sha": helper_selection["executor_source_sha"],
            "executor_source_tree": helper_selection["executor_source_tree"],
            "helper_compatibility": helper_selection["compatibility"],
        }
        if any(key in receipt and receipt[key] != value for key, value in provenance.items()):
            raise RuntimeError("staging smoke helper returned conflicting executor provenance")
        receipt.update(provenance)
        return verify_stage_smoke_receipt(receipt, source_sha, installed_sha, manifest_sha, helper_selection)
    finally:
        if timing_sink is not None:
            timing_sink["stage_smoke"] = round(time.monotonic() - started, 1)


def verify_release_artifact(release: Path, metadata: dict) -> None:
    """Revalidate the complete cached artifact before reusing a stage orphan."""
    if release.is_symlink() or not release.is_dir():
        raise RuntimeError("cached release artifact is missing or unsafe")
    manifest = release / "release-files.sha256"
    if manifest.is_symlink() or not manifest.is_file() or sha256_file(manifest) != metadata.get("release_files_sha256"):
        raise RuntimeError("cached release manifest does not match metadata")
    entries: dict[str, str] = {}
    for line in manifest.read_text().splitlines():
        match = re.fullmatch(r"([0-9a-f]{64})  (.+)", line)
        if not match:
            raise RuntimeError("cached release manifest is invalid")
        digest, name = match.groups()
        path = Path(name)
        if path.is_absolute() or path.as_posix() != name or ".." in path.parts or "." in path.parts or "\\" in name or name in {"release-files.sha256", "release.env"} or name in entries:
            raise RuntimeError("cached release manifest contains an unsafe path")
        entries[name] = digest
    actual: set[str] = set()
    for path in release.rglob("*"):
        if path.is_symlink() or not (path.is_file() or path.is_dir()):
            raise RuntimeError("cached release contains a linked or special path")
        if path.is_file() and path != manifest:
            name = path.relative_to(release).as_posix()
            actual.add(name)
            if entries.get(name) != sha256_file(path):
                raise RuntimeError("cached release file digest mismatch")
    if actual != set(entries):
        raise RuntimeError("cached release file set mismatch")


def promote_checked_candidate(
    config: dict,
    state_path: Path,
    state: dict,
    sha: str,
    out: Path,
    metadata: dict,
    installed: str,
    started: float,
    *,
    stage_receipt: dict,
    changed_paths: list[str],
    build_base_sha: str,
    validation_scope_base_sha: str,
    stage_smoke_receipt: dict | None = None,
    phase_timings: dict | None = None,
    check_observation: dict | None = None,
    checked_main_sha: str | None = None,
) -> None:
    """Run the same production handoff only after stage receipt/readback is verified."""
    phase_timings = dict(phase_timings or {})
    check_observation = dict(check_observation or {})
    build_timings = metadata.get("phase_timings_seconds", {})
    if isinstance(build_timings, dict) and isinstance(build_timings.get("build"), (int, float)):
        phase_timings.setdefault("build", build_timings["build"])
    if not SHA.fullmatch(validation_scope_base_sha) or git(Path(config["repo"]), "rev-parse", f"{sha}^1") != validation_scope_base_sha:
        raise RuntimeError("promotion validation scope is not the exact first parent")
    if metadata.get("validation_scope_base_sha") != validation_scope_base_sha:
        raise RuntimeError("promotion validation scope does not match the built candidate")
    repo = Path(config["repo"])
    if not SHA.fullmatch(build_base_sha) or metadata.get("base_sha") != build_base_sha:
        raise RuntimeError("promotion build base does not match the deployed source cursor")
    trusted_build_paths = _trusted_changed_paths(repo, build_base_sha, sha)
    if changed_paths != trusted_build_paths:
        raise RuntimeError("promotion package paths do not match the deployed build base")
    smoke_paths = _trusted_changed_paths(repo, validation_scope_base_sha, sha)
    if (
        metadata.get("validation_scope_base_tree") != git(repo, "rev-parse", f"{validation_scope_base_sha}^{{tree}}")
        or metadata.get("validation_scope_changed_paths") != smoke_paths
        or metadata.get("actual_ci_baseline_verified") is not False
    ):
        raise RuntimeError("promotion validation range does not match the checked source")
    smoke_required = _alipay_smoke_required({"changed_paths": smoke_paths})
    if smoke_required:
        source_tree = metadata.get("source_tree")
        # The checked controller derives its own exact helper source digest;
        # candidate metadata cannot make the behavior contract optional.
        if not isinstance(source_tree, str) or not SHA.fullmatch(source_tree):
            raise RuntimeError("staging smoke candidate source tree is invalid")
        if stage_smoke_receipt is None:
            raise RuntimeError("required installed staging smoke receipt is missing")
        helper_selection = _smoke_helper_selection(repo, sha, checked_main_sha)
        verify_stage_smoke_receipt(
            stage_smoke_receipt, sha, sha, metadata["release_files_sha256"], helper_selection,
        )
    state.update(
        status="staging_verified",
        blocked_sha=sha,
        staging_verified_sha=sha,
        staging_verified_receipt=stage_receipt,
        staging_verified_manifest_sha256=metadata["release_files_sha256"],
        validation_scope_base_sha=validation_scope_base_sha,
        validation_scope_base_tree=metadata["validation_scope_base_tree"],
        validation_scope_changed_paths=metadata["validation_scope_changed_paths"],
        last_release_timings_seconds=phase_timings,
    )
    if smoke_required and stage_smoke_receipt is not None:
        state["staging_verified_smoke"] = stage_smoke_receipt
    else:
        state.pop("staging_verified_smoke", None)
    atomic_json(state_path, state)
    try:
        metadata_path = out / "domestic-release.json"
        metadata_bytes = metadata_path.read_bytes()
        copied_metadata = json.loads(metadata_bytes)
        if copied_metadata != metadata or copied_metadata.get("source_sha") != sha:
            raise RuntimeError("staged metadata changed after package verification")
        metadata_sha = hashlib.sha256(metadata_bytes).hexdigest()
        transfer_started = time.monotonic()
        try:
            incoming, remote_meta = copy_payload(config, sha, out / "release", metadata_path, installed)
        finally:
            phase_timings["transfer"] = round(time.monotonic() - transfer_started, 1)
            state["last_release_timings_seconds"] = phase_timings
            atomic_json(state_path, state)
    except Exception as exc:
        phase_timings["total"] = round(time.monotonic() - started, 1)
        state.update(status="transport_failed", blocked_sha=sha, failure=f"stage verified; production handoff did not complete: {type(exc).__name__}", last_release_timings_seconds=phase_timings)
        atomic_json(state_path, state)
        raise
    # Once the remote install begins, a lost reply is outcome_unknown even if
    # its transport error looks retryable.
    state.update(status="prod_installing", blocked_sha=sha)
    state["last_release_timings_seconds"] = phase_timings
    atomic_json(state_path, state)
    try:
        install_started = time.monotonic()
        result = command(*ssh_args(config), "sudo", config["prod_helper"], "--incoming", incoming, "--metadata", remote_meta, "--expected-sha", sha, "--metadata-sha256", metadata_sha, "--expected-base", installed, timeout=300)
        phase_timings["production_install"] = round(time.monotonic() - install_started, 1)
        readback_started = time.monotonic()
        helper_receipt = json.loads(result.splitlines()[-1])
        production = prod_readback(config, sha)
        verify_readback(production, sha, metadata["release_files_sha256"])
        verify_install_receipt(production.get("receipt"), metadata, installed)
        if helper_receipt != production.get("receipt"):
            raise RuntimeError("production helper receipt differs from readback")
        phase_timings["production_readback"] = round(time.monotonic() - readback_started, 1)
    except Exception as exc:
        if "production_install" not in phase_timings:
            phase_timings["production_install"] = round(time.monotonic() - install_started, 1)
        elif "production_readback" not in phase_timings:
            phase_timings["production_readback"] = round(time.monotonic() - readback_started, 1)
        phase_timings["total"] = round(time.monotonic() - started, 1)
        state.update(status="outcome_unknown", failure=f"production install/readback requires reconciliation: {type(exc).__name__}", last_release_timings_seconds=phase_timings)
        atomic_json(state_path, state)
        raise
    commit_time = git(Path(config["repo"]), "show", "-s", "--format=%ct", sha)
    state.update(
        status="ready",
        processed_sha=sha,
        deployed_source_sha=sha,
        prod_installed_sha=sha,
        prod_installed_manifest_sha256=metadata["release_files_sha256"],
        blocked_sha=None,
        failure=None,
        last_duration_seconds=round(time.monotonic() - started, 1),
        last_merge_to_healthy_seconds=max(0, int(time.time()) - int(commit_time)),
        last_check_to_healthy_seconds=_seconds_since(check_observation.get("completed_at")),
        last_release_timings_seconds={**phase_timings, "total": round(time.monotonic() - started, 1)},
    )
    atomic_json(state_path, state)


def recover(config: dict, *, retry_blocked: bool, expected_sha: str) -> dict:
    """Explicitly reconcile one outcome-unknown release and retry its verified orphan once."""
    if not retry_blocked:
        raise ValueError("recover requires --retry-blocked")
    if not isinstance(expected_sha, str) or not SHA.fullmatch(expected_sha):
        raise ValueError("recover requires an exact --sha")
    started = time.monotonic()
    repo = Path(config["repo"])
    work_root = Path(config["work_root"])
    state_path = Path(config["state"])
    if not state_path.is_file():
        raise RuntimeError("release ledger is missing")
    lock_path = state_path.with_suffix(".lock")
    with lock_path.open("a+") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        state = json.loads(state_path.read_text())
        sha = state.get("blocked_sha")
        if state.get("status") != "outcome_unknown" or not isinstance(sha, str) or not SHA.fullmatch(sha):
            raise RuntimeError("recover requires one blocked outcome_unknown SHA")
        if sha != expected_sha:
            raise RuntimeError("explicit --sha does not match blocked outcome_unknown SHA")
        if not config["production_enabled"]:
            raise RuntimeError("production publishing is disabled")
        require_official_origin(repo)
        git(repo, "fetch", "--no-tags", "origin", "main")
        head = git(repo, "rev-parse", "refs/remotes/origin/main")
        queue = first_parent_queue(repo, state["processed_sha"], head)
        check_observation: dict = {}
        if not queue or queue[0] != sha or not exact_check_success(sha, os.environ.get("GITHUB_TOKEN"), observation=check_observation):
            raise RuntimeError("blocked SHA is not the next checked commit on protected main")
        try:
            regression_blocker = _main_full_regression_blocker(repo, sha, head)
        except Exception as exc:
            regression_blocker = {
                "run_id": None,
                "run_attempt": None,
                "sha": head,
                "classification": "unknown",
                "reason": f"main full CI history could not be verified: {type(exc).__name__}",
            }
        if regression_blocker is not None:
            state["ci_regression_pause"] = {
                "candidate_sha": sha,
                "main_head_sha": head,
                "classification": "unknown",
                "blocker": regression_blocker,
                "watched_check": _check_signature(check_observation),
                "blocked_at_utc": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
            }
            atomic_json(state_path, state)
            raise RuntimeError("main full CI evidence is failing or incomplete; orphan recovery is blocked")

        installed = state.get("prod_installed_sha")
        deployed = state.get("deployed_source_sha")
        if not all(isinstance(value, str) and SHA.fullmatch(value) for value in (installed, deployed)):
            raise RuntimeError("invalid prior release cursor")
        build = work_root / "builds" / sha
        metadata_path = build / "domestic-release.json"
        release_path = build / "release"
        if metadata_path.is_symlink() or not metadata_path.is_file() or release_path.is_symlink() or not release_path.is_dir():
            raise RuntimeError("verified stage package is missing or unsafe")
        metadata_bytes = metadata_path.read_bytes()
        metadata = json.loads(metadata_bytes)
        metadata_sha = hashlib.sha256(metadata_bytes).hexdigest()
        if (
            metadata.get("source_sha") != sha
            or metadata.get("base_sha") != deployed
            or metadata.get("source_tree") != git(repo, "rev-parse", f"{sha}^{{tree}}")
            or type(metadata.get("migrations_changed")) is not bool
        ):
            raise RuntimeError("stage metadata does not match the blocked source")
        if metadata["migrations_changed"]:
            raise RuntimeError("automatic orphan recovery is disabled for migration releases")
        manifest_path = release_path / "release-files.sha256"
        manifest_sha = sha256_file(manifest_path)
        if manifest_sha != metadata.get("release_files_sha256"):
            raise RuntimeError("stage package manifest differs from its metadata")
        stage = stage_readback(sha)
        verify_readback(stage, sha, manifest_sha)
        verify_install_receipt(stage.get("receipt"), metadata, deployed)

        validation_scope_base_sha = state.get("processed_sha")
        if (
            not isinstance(validation_scope_base_sha, str)
            or not SHA.fullmatch(validation_scope_base_sha)
            or git(repo, "rev-parse", f"{sha}^1") != validation_scope_base_sha
            or metadata.get("validation_scope_base_sha") != validation_scope_base_sha
        ):
            raise RuntimeError("stage validation scope does not match the blocked first-parent commit")
        changed_paths = _trusted_changed_paths(repo, deployed, sha)
        validation_paths = _trusted_changed_paths(repo, validation_scope_base_sha, sha)
        smoke_required = _alipay_smoke_required({"changed_paths": validation_paths})
        stage_smoke_receipt = None
        if smoke_required:
            try:
                stage_smoke_receipt = run_stage_smoke(config, repo, sha, sha, manifest_sha, checked_main_sha=head)
            except Exception as exc:
                state.pop("staging_verified_smoke", None)
                state.update(
                    status="outcome_unknown",
                    failure=f"required staging installed behavior smoke failed during recovery: {type(exc).__name__}",
                    last_stage_smoke={"source_sha": sha, "installed_sha": sha, "manifest_sha256": manifest_sha, "status": "failed"},
                )
                atomic_json(state_path, state)
                raise
            verify_stage_smoke_receipt(
                stage_smoke_receipt,
                sha,
                sha,
                manifest_sha,
                _smoke_helper_selection(repo, sha, head),
            )
            state["staging_verified_smoke"] = stage_smoke_receipt
            atomic_json(state_path, state)

        production = prod_readback(config, sha)
        target_receipt_present = production.get("receipt_exists") is True
        if target_receipt_present:
            if production.get("receipt_target_sha") != sha:
                raise RuntimeError("production receipt readback targeted another SHA")
            verify_readback(production, sha, manifest_sha)
            verify_install_receipt(production.get("receipt"), metadata, installed)
            # The install completed but the caller did not record its receipt.
            # This path is read-only on production and only repairs the ledger.
        else:
            if production.get("current") != f"/opt/aicrm/releases/{installed}":
                raise RuntimeError("production current conflicts with the prior release; refusing retry")
            previous_manifest = state.get("prod_installed_manifest_sha256")
            if not isinstance(previous_manifest, str) or not FILE_SHA.fullmatch(previous_manifest):
                previous_manifest = state.get("baseline_prod_manifest_sha256")
            if not isinstance(previous_manifest, str) or not FILE_SHA.fullmatch(previous_manifest):
                raise RuntimeError("prior production manifest digest is missing")
            verify_readback(production, installed, previous_manifest)
            if state.get("recovery_attempted_sha") == sha:
                raise RuntimeError("the one permitted orphan retry was already attempted")
            remote_metadata = f"{config['prod_incoming'].rstrip('/')}/{sha}.json"
            remote_metadata_sha = command(*ssh_args(config), "sudo", "sha256sum", remote_metadata, timeout=30).split()[0]
            if remote_metadata_sha != metadata_sha:
                raise RuntimeError("production metadata differs from the verified stage package")
            # Persist the one-time guard before the remote helper can change current.
            state["recovery_attempted_sha"] = sha
            state["recovery_attempted_at_utc"] = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
            atomic_json(state_path, state)
            try:
                result = command(*ssh_args(config), "sudo", config["prod_helper"], "--retry-existing", "--metadata", remote_metadata, "--expected-sha", sha, "--metadata-sha256", metadata_sha, "--expected-base", installed, timeout=300)
                helper_receipt = json.loads(result.splitlines()[-1])
                production = prod_readback(config, sha)
                verify_readback(production, sha, manifest_sha)
                verify_install_receipt(production.get("receipt"), metadata, installed)
                if helper_receipt != production.get("receipt"):
                    raise RuntimeError("production helper receipt differs from readback")
            except Exception as exc:
                state["failure"] = str(exc)[-500:]
                atomic_json(state_path, state)
                raise

        commit_time = git(repo, "show", "-s", "--format=%ct", sha)
        state.update(
            status="ready",
            processed_sha=sha,
            deployed_source_sha=sha,
            prod_installed_sha=sha,
            prod_installed_manifest_sha256=manifest_sha,
            blocked_sha=None,
            failure=None,
            last_recovery_sha=sha,
            last_recovery_status="readback_confirmed" if target_receipt_present else "orphan_reused_healthy",
            last_duration_seconds=round(time.monotonic() - started, 1),
            last_merge_to_healthy_seconds=max(0, int(time.time()) - int(commit_time)),
        )
        if stage_smoke_receipt is not None:
            state["staging_verified_smoke"] = stage_smoke_receipt
        else:
            state.pop("staging_verified_smoke", None)
        atomic_json(state_path, state)
        return {"status": "ready", "processed_sha": sha, "recovery": state["last_recovery_status"]}


def poll(config: dict) -> dict:
    repo = Path(config["repo"])
    state_path = Path(config["state"])
    if not state_path.is_file():
        raise RuntimeError("baseline state missing; run documented read-only baseline binding first")
    lock_path = state_path.with_suffix(".lock")
    with lock_path.open("a+") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        state = json.loads(state_path.read_text())
        initial_status = state.get("status")
        if initial_status not in {"ready", "controller_update_required", "ci_regression_blocked", "staging_failed"}:
            raise RuntimeError(f"queue halted: {state.get('status')}")
        resuming_pr38_staging_failure = (
            initial_status == "staging_failed"
            or state.get("pr38_staging_failed_resume") == PR38_RESUME_MARKER
        )
        blocked_controller_sha = state.get("blocked_sha") if initial_status == "controller_update_required" else None
        if blocked_controller_sha is not None and not SHA.fullmatch(blocked_controller_sha):
            raise RuntimeError("controller update ledger cursor is invalid")
        processed = state["processed_sha"]
        deployed = state["deployed_source_sha"]
        installed = state["prod_installed_sha"]
        if any(not SHA.fullmatch(value) for value in (processed, deployed, installed)):
            raise ValueError("invalid state cursor")
        require_official_origin(repo)
        git(repo, "fetch", "--no-tags", "origin", "main")
        head = git(repo, "rev-parse", "refs/remotes/origin/main")
        if initial_status == "ci_regression_blocked":
            if not _retry_ci_regression_pause(state_path, state, repo, head):
                return {
                    "status": "ci_regression_blocked",
                    "sha": state.get("ci_regression_pause", {}).get("candidate_sha"),
                    "blocker": state.get("ci_regression_pause", {}).get("blocker"),
                }
            initial_status = "ready"
            blocked_controller_sha = None
        queue = first_parent_queue(repo, processed, head)
        if blocked_controller_sha is not None and (not queue or queue[0] != blocked_controller_sha):
            raise RuntimeError("controller update is no longer the next checked first-parent commit")
        if resuming_pr38_staging_failure:
            _validate_pr38_staging_failed_ledger(state, queue)
            if state.get("pr38_staging_failed_resume") != PR38_RESUME_MARKER:
                state["pr38_staging_failed_resume"] = dict(PR38_RESUME_MARKER)
                atomic_json(state_path, state)
        regression_history_cache: dict = {}
        for sha in queue:
            started = time.monotonic()
            pr38_resume_readback = None
            check_observation: dict = {}
            if not exact_check_success(sha, os.environ.get("GITHUB_TOKEN"), observation=check_observation):
                return {"status": "awaiting_exact_check", "sha": sha}
            if not config["production_enabled"]:
                return {"status": "dry_run_only", "sha": sha}
            try:
                regression_blocker = _main_full_regression_blocker(
                    repo, sha, head, cache=regression_history_cache,
                )
            except Exception as exc:
                regression_blocker = {
                    "run_id": None,
                    "run_attempt": None,
                    "sha": head,
                    "classification": "unknown",
                    "reason": f"main full CI history could not be verified: {type(exc).__name__}",
                }
            if regression_blocker is not None:
                return _enter_ci_regression_pause(
                    state_path, state, repo, sha, head, regression_blocker,
                    candidate_check_observation=check_observation,
                )
            plan = json.loads(command("python3", str(Path(__file__).with_name("domestic_release_build.py")), "classify", "--repo", str(repo), "--base", deployed, "--target", sha))
            changed_paths = _trusted_changed_paths(repo, deployed, sha)
            validation_paths = _trusted_changed_paths(repo, processed, sha)
            if plan.get("changed_paths") != changed_paths:
                raise RuntimeError("release classifier output does not match the trusted commit path range")
            smoke_required = _alipay_smoke_required({"changed_paths": validation_paths})
            phase_timings: dict[str, float | None] = {
                "check": check_observation.get("duration_seconds"),
            }
            controller_files = plan.get("controller_files", [])
            if resuming_pr38_staging_failure and sha == PR38_SMOKE_SOURCE_SHA:
                if not controller_files or plan.get("runtime_changed") is not False or not smoke_required:
                    raise RuntimeError("PR38 staging_failed resume no longer matches the reviewed controller-only smoke plan")
                pr38_resume_readback = _verify_pr38_staging_failed_readbacks(config, state)
            if controller_files:
                controller_started = time.monotonic()
                try:
                    verification = verify_controller_installation(config, repo, sha, controller_files, checked_main_sha=head)
                except Exception as exc:
                    phase_timings["controller_readback"] = round(time.monotonic() - controller_started, 1)
                    if resuming_pr38_staging_failure:
                        state.update(
                            failure=f"fixed controller files are not installed and verified: {type(exc).__name__}",
                            last_release_timings_seconds={**phase_timings, "total": round(time.monotonic() - started, 1)},
                        )
                        atomic_json(state_path, state)
                    else:
                        state.update(
                            status="controller_update_required",
                            blocked_sha=sha,
                            failure=f"fixed controller files are not installed and verified: {type(exc).__name__}",
                            last_release_timings_seconds={**phase_timings, "total": round(time.monotonic() - started, 1)},
                        )
                        atomic_json(state_path, state)
                    raise RuntimeError("fixed controller files do not match the exact checked source; install them under the maintenance lock and rerun") from exc
                phase_timings["controller_readback"] = verification["duration_seconds"]
                state["last_controller_verification"] = verification
                if pr38_resume_readback is not None:
                    state["last_pr38_resume_readback"] = {
                        **pr38_resume_readback,
                        "checked_main_sha": head,
                        "checked_main_tree": git(repo, "rev-parse", f"{head}^{{tree}}"),
                        "source_sha": sha,
                    }
                atomic_json(state_path, state)
            if blocked_controller_sha == sha and not controller_files:
                raise RuntimeError("blocked controller update is absent from the source impact plan")
            if not plan.get("runtime_changed"):
                stage_smoke_receipt = None
                if smoke_required:
                    try:
                        smoke_manifest = _installed_stage_manifest_sha(config, deployed)
                        if resuming_pr38_staging_failure:
                            state["pr38_staging_failed_smoke_attempted"] = {
                                "source_sha": PR38_SMOKE_SOURCE_SHA,
                                "checked_main_sha": head,
                                "started_at_utc": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
                            }
                            atomic_json(state_path, state)
                        stage_smoke_receipt = run_stage_smoke(
                            config,
                            repo,
                            sha,
                            deployed,
                            smoke_manifest,
                            checked_main_sha=head,
                            timing_sink=phase_timings,
                        )
                    except Exception as exc:
                        phase_timings["total"] = round(time.monotonic() - started, 1)
                        state.update(
                            status="staging_failed",
                            blocked_sha=sha,
                            validation_scope_base_sha=processed,
                            validation_scope_base_tree=git(repo, "rev-parse", f"{processed}^{{tree}}"),
                            validation_scope_changed_paths=validation_paths,
                            failure=f"staging installed behavior smoke failed: {type(exc).__name__}",
                            last_stage_smoke={"source_sha": sha, "installed_sha": deployed, "status": "failed"},
                            last_release_timings_seconds=phase_timings,
                        )
                        atomic_json(state_path, state)
                        raise
                state.update(
                    status="ready",
                    processed_sha=sha,
                    blocked_sha=None,
                    failure=None,
                    last_release_timings_seconds={**phase_timings, "total": round(time.monotonic() - started, 1)},
                )
                if stage_smoke_receipt is not None:
                    state["last_stage_smoke"] = stage_smoke_receipt
                else:
                    state.pop("last_stage_smoke", None)
                if controller_files:
                    state["last_check_to_controller_verified_seconds"] = _seconds_since(check_observation.get("completed_at"))
                state.pop("pr38_staging_failed_resume", None)
                state.pop("pr38_staging_failed_smoke_attempted", None)
                atomic_json(state_path, state)
                processed = sha
                blocked_controller_sha = None
                resuming_pr38_staging_failure = False
                continue
            base_release = Path(config["work_root"]) / "builds" / deployed / "release"
            if not base_release.is_dir():
                raise RuntimeError("verified staging base release missing")
            build_started = time.monotonic()
            stage_smoke_receipt = None
            try:
                out, metadata = build_candidate(config, sha, deployed, base_release, processed)
                build_timings = metadata.get("phase_timings_seconds", {})
                if isinstance(build_timings, dict) and isinstance(build_timings.get("build"), (int, float)):
                    phase_timings["build"] = build_timings["build"]
                stage_receipt = stage_install(config, sha, out, deployed, timing_sink=phase_timings)
                if smoke_required:
                    stage_smoke_receipt = run_stage_smoke(
                        config,
                        repo,
                        sha,
                        sha,
                        metadata["release_files_sha256"],
                        checked_main_sha=head,
                        timing_sink=phase_timings,
                    )
            except Exception as exc:
                phase_timings.setdefault("build", round(time.monotonic() - build_started, 1))
                state.update(
                    status="staging_failed",
                    blocked_sha=sha,
                    validation_scope_base_sha=processed,
                    validation_scope_base_tree=git(repo, "rev-parse", f"{processed}^{{tree}}"),
                    validation_scope_changed_paths=validation_paths,
                    failure=f"staging install or behavior smoke failed: {type(exc).__name__}",
                    last_release_timings_seconds={**phase_timings, "total": round(time.monotonic() - started, 1)},
                )
                if smoke_required:
                    state["last_stage_smoke"] = {
                        "source_sha": sha,
                        "installed_sha": sha,
                        "status": "failed",
                    }
                atomic_json(state_path, state)
                raise
            promote_checked_candidate(
                config, state_path, state, sha, out, metadata, installed, started,
                stage_receipt=stage_receipt,
                changed_paths=changed_paths,
                build_base_sha=deployed,
                validation_scope_base_sha=processed,
                stage_smoke_receipt=stage_smoke_receipt,
                phase_timings=phase_timings,
                check_observation=check_observation,
                checked_main_sha=head,
            )
            processed, deployed, installed = sha, sha, sha
            blocked_controller_sha = None
        return {"status": "ready", "processed_sha": processed, "queued_count": len(queue)}


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--config", type=Path, required=True)
    parser.add_argument("action", choices=("poll", "readback", "bind-baseline", "recover"))
    parser.add_argument("--prod-preview-sha")
    parser.add_argument("--retry-blocked", action="store_true")
    parser.add_argument("--sha", help="exact blocked commit SHA")
    args = parser.parse_args()
    config = load_config(args.config)
    if args.action == "poll":
        if args.retry_blocked or args.sha:
            parser.error("--retry-blocked and --sha are only valid with recover")
        result = poll(config)
    elif args.action == "readback":
        if args.retry_blocked or args.sha:
            parser.error("--retry-blocked and --sha are only valid with recover")
        result = prod_readback(config)
    elif args.action == "bind-baseline":
        if args.retry_blocked or args.sha:
            parser.error("--retry-blocked and --sha are only valid with recover")
        if not args.prod_preview_sha:
            parser.error("bind-baseline requires --prod-preview-sha")
        result = bind_baseline(config, args.prod_preview_sha)
    else:
        if not args.retry_blocked or not args.sha or args.prod_preview_sha:
            parser.error("recover requires --retry-blocked --sha <exact-blocked-SHA>")
        result = recover(config, retry_blocked=True, expected_sha=args.sha)
    print(json.dumps(result, ensure_ascii=False, sort_keys=True))


if __name__ == "__main__":
    main()
