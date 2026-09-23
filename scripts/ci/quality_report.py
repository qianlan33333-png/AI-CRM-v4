#!/usr/bin/env python3
"""Conservative, read-only CI lane receipts and PR quality summaries.

OneID: not involved. Persistence: stateless. External Effects: not involved.
Only Git/GitHub Actions metadata is read; artifacts and step summaries are local.
"""
from __future__ import annotations

import argparse
from functools import cache
import json
import os
from pathlib import Path
import subprocess
from typing import Any

LANES = ("preflight", "backend", "frontend", "browser", "archive-sdk")
WORKFLOW = ".github/workflows/ci.yml"
TERMINAL = {"success", "failure"}
WORKFLOW_LIFECYCLES = {"queued", "in_progress", "completed", "requested", "waiting", "pending"}
WORKFLOW_CONCLUSIONS = {"success", "failure", "cancelled", "skipped", "timed_out", "action_required", "neutral", "stale", "startup_failure"}


def write_json(path: Path, data: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(data, ensure_ascii=False, indent=2, sort_keys=True) + "\n")


def outcome(value: str | None) -> str:
    return value if value in {"success", "failure", "cancelled", "skipped", "pending", "in_progress"} else "unknown"


def workflow_lifecycle(value: str | None) -> str:
    return value if value in WORKFLOW_LIFECYCLES else "unknown"


def workflow_conclusion(value: str | None) -> str:
    return value if value in WORKFLOW_CONCLUSIONS else "not_available"


def receipt(lane: str, setup: list[str], verification: str, job: str) -> dict[str, Any]:
    setup_values = {name: outcome(value) for name, value in (item.split("=", 1) for item in setup)}
    verification, job = outcome(verification), outcome(job)
    observations: list[str] = []
    if any(value == "failure" for value in setup_values.values()):
        observations.append("environment_setup_failure")
    if verification == "failure":
        observations.append("assertion_or_verification_failure")
    if job == "cancelled" or verification == "cancelled" or "cancelled" in setup_values.values():
        observations.append("cancelled")
    if (job in {"pending", "in_progress", "skipped"} or verification in {"pending", "in_progress", "skipped"}
            or any(value in {"pending", "in_progress", "skipped", "unknown"} for value in setup_values.values())):
        observations.append("pending_or_incomplete")
    if job == "failure" and not observations:
        observations.append("unknown_failure")
    if not observations and job == verification == "success" and all(value == "success" for value in setup_values.values()):
        observations.append("success")
    return {"schema": 1, "lane": lane, "setup": setup_values, "verification": verification, "job": job,
            "observations": observations or ["unknown_failure"]}


def gh_api(path: str) -> Any:
    result = subprocess.run(["gh", "api", path], check=True, capture_output=True, text=True, timeout=30)
    return json.loads(result.stdout)


def gh_pages(path: str) -> list[Any]:
    result = subprocess.run(["gh", "api", "--paginate", "--slurp", path], check=True, capture_output=True, text=True, timeout=90)
    data = json.loads(result.stdout)
    if not isinstance(data, list):
        raise ValueError("paginated GitHub response was not a list")
    return data


def repo_identity(run: dict[str, Any]) -> str | None:
    repository = run.get("repository")
    return repository.get("full_name") if isinstance(repository, dict) and isinstance(repository.get("full_name"), str) else None


@cache
def commit_pr_numbers(repo: str, sha: str) -> frozenset[int]:
    """Read the explicit commit-to-PR relation once per SHA for this report."""
    associated = gh_pages(f"repos/{repo}/commits/{sha}/pulls?per_page=100")
    numbers: set[int] = set()
    for page in associated:
        if not isinstance(page, list):
            raise ValueError("commit pull-request association response is invalid")
        for item in page:
            if not isinstance(item, dict) or not isinstance(item.get("number"), int):
                raise ValueError("commit pull-request association response is invalid")
            numbers.add(item["number"])
    return frozenset(numbers)


def is_pr_run(run: dict[str, Any], pr: int, repo: str | None = None) -> bool:
    pulls = run.get("pull_requests")
    if run.get("path") != WORKFLOW or run.get("event") != "pull_request":
        return False
    if repo is not None and repo_identity(run) != repo:
        return False
    if isinstance(pulls, list) and pulls:
        return any(item.get("number") == pr for item in pulls if isinstance(item, dict))
    # Actions occasionally returns an empty pull_requests array for a genuine
    # pull_request run.  Resolve that omission through GitHub's commit-to-PR
    # relation; branch name or matching SHA alone is never sufficient.
    sha = run.get("head_sha")
    if repo is None or not isinstance(sha, str):
        raise ValueError("workflow history lacks pull-request association")
    numbers = commit_pr_numbers(repo, sha)
    if numbers == {pr}:
        return True
    if pr in numbers:
        raise ValueError("commit is ambiguously associated with multiple pull requests")
    return False


def validate_run(run: dict[str, Any], repo: str, pr: int, head: str | None = None) -> None:
    if not is_pr_run(run, pr, repo):
        raise ValueError("workflow run does not belong to requested repository/workflow/pull request")
    if head is not None and run.get("head_sha") != head:
        raise ValueError("workflow run head does not match requested pull-request head")


def check_state_from_jobs(jobs: list[dict[str, Any]]) -> str:
    check = next((job for job in jobs if job.get("name") == "check"), None)
    if not check:
        return "pending_or_incomplete"
    if check.get("status") is not None and check["status"] != "completed":
        return "pending_or_incomplete"
    conclusion = workflow_conclusion(check.get("conclusion"))
    if conclusion == "success":
        return "success"
    if conclusion == "cancelled":
        return "cancelled"
    if conclusion in {"failure", "timed_out", "action_required", "startup_failure"}:
        return "failure"
    if conclusion in {"skipped", "neutral", "stale", "not_available"}:
        return "pending_or_incomplete"
    return "unknown"


def attempt_verification(lifecycle: str, conclusion: str, jobs: list[dict[str, Any]]) -> str:
    check = check_state_from_jobs(jobs)
    if lifecycle != "completed":
        return "pending_or_incomplete"
    actual_lane_failure = any(
        job.get("name") in LANES and workflow_conclusion(job.get("conclusion"))
        in {"failure", "timed_out", "action_required", "startup_failure"}
        for job in jobs
    )
    # Cancellation can make check fail merely because it aggregates cancelled
    # dependencies.  It is a cancellation unless an actual verification lane
    # independently recorded a terminal failure.
    if conclusion == "cancelled":
        return "failure" if actual_lane_failure else "cancelled"
    if check == "pending_or_incomplete" and not any(job.get("name") == "check" for job in jobs):
        return "unknown"
    return check


def attempt_record(repo: str, run_id: int, attempt: int, *, pr: int | None = None, head: str | None = None) -> dict[str, Any]:
    run = gh_api(f"repos/{repo}/actions/runs/{run_id}/attempts/{attempt}")
    if not isinstance(run, dict):
        raise ValueError("workflow run attempt response is invalid")
    if pr is not None:
        validate_run(run, repo, pr, head)
    jobs = gh_api(f"repos/{repo}/actions/runs/{run_id}/attempts/{attempt}/jobs?per_page=100")
    if not isinstance(jobs, dict) or not isinstance(jobs.get("jobs"), list):
        raise ValueError("attempt job response is invalid")
    total = jobs.get("total_count")
    if total is not None and (not isinstance(total, int) or total > len(jobs["jobs"])):
        raise ValueError("attempt job history may be truncated")
    if len(jobs["jobs"]) == 100:
        raise ValueError("attempt job history may be truncated")
    lifecycle, conclusion = workflow_lifecycle(run.get("status")), workflow_conclusion(run.get("conclusion"))
    verification = attempt_verification(lifecycle, conclusion, jobs["jobs"])
    return {"run_id": run_id, "run_attempt": attempt, "head_sha": run.get("head_sha"), "created_at": run.get("created_at"),
            "run_url": run.get("html_url") or run.get("url"),
            "artifacts_url": f"https://github.com/{repo}/actions/runs/{run_id}/artifacts",
            "workflow_lifecycle": lifecycle, "workflow_conclusion": conclusion, "verification": verification}


def _history_page_runs(pages: list[Any]) -> list[dict[str, Any]]:
    runs: list[dict[str, Any]] = []
    total: int | None = None
    for page in pages:
        if not isinstance(page, dict) or not isinstance(page.get("workflow_runs"), list) or not isinstance(page.get("total_count"), int):
            raise ValueError("workflow history page is invalid or incomplete")
        if total is None:
            total = page["total_count"]
        elif total != page["total_count"]:
            raise ValueError("workflow history total changed while paginating")
        for run in page["workflow_runs"]:
            if not isinstance(run, dict):
                raise ValueError("workflow history run is invalid")
            runs.append(run)
    if total is None or total != len(runs):
        raise ValueError("workflow history pagination is incomplete")
    # List workflow runs documents a 1,000-result limit; a boundary result does
    # not prove the earliest PR run is visible.
    if total >= 1000:
        raise ValueError("workflow history reached GitHub truncation boundary")
    return runs


def pr_runs(repo: str, pr: int, created_at: str, head: str | None = None) -> list[dict[str, Any]]:
    suffix = "&head_sha=" + head if head else ""
    pages = gh_pages(f"repos/{repo}/actions/workflows/ci.yml/runs?event=pull_request&per_page=100&created=%3E%3D{created_at}{suffix}")
    return [run for run in _history_page_runs(pages) if is_pr_run(run, pr, repo)]


def first_pr_attempt(repo: str, pr: int, pull: dict[str, Any] | None = None) -> dict[str, Any]:
    pull = pull or gh_api(f"repos/{repo}/pulls/{pr}")
    created_at = pull.get("created_at") if isinstance(pull, dict) else None
    if not isinstance(created_at, str):
        raise ValueError("pull request has no creation time")
    candidates = pr_runs(repo, pr, created_at)
    if not candidates:
        raise ValueError("no associated CI run found for PR history")
    first = min(candidates, key=lambda run: (run.get("created_at", ""), run.get("id", 0)))
    if not isinstance(first.get("id"), int):
        raise ValueError("first workflow run has no id")
    # Always load attempt 1; list history's latest rerun field must not replace it.
    return attempt_record(repo, first["id"], 1, pr=pr, head=first.get("head_sha"))


def _pull_head(pull: dict[str, Any]) -> tuple[str, str]:
    head = pull.get("head", {}).get("sha") if isinstance(pull.get("head"), dict) else None
    created_at = pull.get("created_at")
    if not isinstance(head, str) or not isinstance(created_at, str):
        raise ValueError("pull request current head is unavailable")
    return head, created_at


def _current_head_runs(repo: str, pr: int, pull: dict[str, Any]) -> tuple[str, list[dict[str, Any]]]:
    head, created_at = _pull_head(pull)
    candidates = [run for run in pr_runs(repo, pr, created_at, head) if run.get("head_sha") == head]
    if not candidates:
        raise ValueError("no associated CI run found for current PR head")
    return head, candidates


def final_pr_attempt(repo: str, pr: int, pull: dict[str, Any] | None = None) -> tuple[str, dict[str, Any]]:
    pull = pull or gh_api(f"repos/{repo}/pulls/{pr}")
    if not isinstance(pull, dict):
        raise ValueError("pull request response is invalid")
    head, candidates = _current_head_runs(repo, pr, pull)
    latest_time = max(run.get("created_at", "") for run in candidates)
    latest_id = max((run for run in candidates if run.get("created_at", "") == latest_time), key=lambda run: run.get("id", 0)).get("id")
    attempts = [run.get("run_attempt") for run in candidates if run.get("id") == latest_id]
    if not isinstance(latest_id, int) or not attempts or any(not isinstance(item, int) or item < 1 for item in attempts):
        raise ValueError("latest workflow run has no visible attempt number")
    return head, attempt_record(repo, latest_id, max(attempts), pr=pr, head=head)


def current_head_first_attempt(repo: str, pr: int, pull: dict[str, Any]) -> dict[str, Any]:
    head, candidates = _current_head_runs(repo, pr, pull)
    first = min(candidates, key=lambda run: (run.get("created_at", ""), run.get("id", 0)))
    if not isinstance(first.get("id"), int):
        raise ValueError("current-head first workflow run has no id")
    return attempt_record(repo, first["id"], 1, pr=pr, head=head)


def current_attempt(repo: str, run_id: int, run_attempt: int, head: str, needs: dict[str, Any]) -> dict[str, Any]:
    check = outcome(needs.get("check", {}).get("result"))
    verification = "success" if check == "success" else "failure" if check == "failure" else "cancelled" if check == "cancelled" else "pending_or_incomplete"
    # This self-report job is still running even if check has completed.
    return {"run_id": run_id, "run_attempt": run_attempt, "head_sha": head, "workflow_lifecycle": "in_progress",
            "workflow_conclusion": "not_available", "verification": verification, "source": "current_needs",
            "artifacts_url": f"https://github.com/{repo}/actions/runs/{run_id}/artifacts"}


def evidence_timeline(first: dict[str, Any], current_head_first: dict[str, Any], final: dict[str, Any],
                      current_run: dict[str, Any], lanes: dict[str, Any]) -> list[dict[str, Any]]:
    """Return append-only-friendly PR events without allowing later success to erase failure."""
    events: list[dict[str, Any]] = []
    for label, record in (("pr_first_attempt", first), ("current_head_first_attempt", current_head_first),
                          ("current_head_final_attempt", final), ("current_reporting_run", current_run)):
        if not record.get("run_id"):
            continue
        events.append({"event": label, "head_sha": record.get("head_sha"), "run_id": record.get("run_id"),
                       "run_attempt": record.get("run_attempt"), "created_at": record.get("created_at"),
                       "run_url": record.get("run_url"), "artifacts_url": record.get("artifacts_url"),
                       "verification": record.get("verification", "unknown"),
                       "workflow_lifecycle": record.get("workflow_lifecycle", "unknown"),
                       "workflow_conclusion": record.get("workflow_conclusion", "not_available")})
    for lane, record in lanes.items():
        for observation in record.get("observations", []):
            events.append({"event": "lane", "lane": lane, "head_sha": current_run.get("head_sha"),
                           "run_id": current_run.get("run_id"), "run_attempt": current_run.get("run_attempt"),
                           "observation": observation})
    return events


def lane_summary(needs: dict[str, Any]) -> dict[str, Any]:
    summary: dict[str, Any] = {}
    for lane in LANES:
        item = needs.get(lane, {})
        value, outputs = outcome(item.get("result")), item.get("outputs", {})
        recorded = outputs.get("classification")
        raw_observations = outputs.get("observations")
        observations: list[str] = []
        if isinstance(raw_observations, str):
            try:
                parsed = json.loads(raw_observations)
            except json.JSONDecodeError:
                parsed = None
            if isinstance(parsed, list) and all(isinstance(observation, str) for observation in parsed):
                observations = parsed
        if not observations and recorded not in {None, "", "success"}:
            observations = [recorded]
        if value == "cancelled": observations.append("cancelled")
        elif value in {"pending", "in_progress", "skipped"}: observations.append("pending_or_incomplete")
        elif value == "failure" and not observations: observations.append("unknown_failure")
        elif value == "success" and not observations: observations.append("success")
        summary[lane] = {"job": value, "observations": list(dict.fromkeys(observations or ["unknown_failure"]))}
    return summary


def _metric_state(record: dict[str, Any]) -> str:
    value = record.get("verification", "unknown")
    return value if value in {"success", "failure", "cancelled", "pending_or_incomplete"} else "unknown"


def one_pr_counts(first: dict[str, Any], final: dict[str, Any], current_head_first: dict[str, Any] | None = None) -> dict[str, int]:
    result: dict[str, int] = {"prs_observed": 1}
    records = [("first", first), ("final", final)] + ([] if current_head_first is None else [("current_head_first", current_head_first)])
    for label, record in records:
        for state in ("success", "failure", "cancelled", "pending_or_incomplete", "unknown"):
            result[f"{label}_{state}"] = 0
        state = _metric_state(record)
        result[f"{label}_{state}"] = 1
        result[f"{label}_terminal_observed"] = int(state in TERMINAL)
    return result


def batch_counts(summaries: list[dict[str, Any]]) -> dict[str, int]:
    seen, total = set(), {"prs_observed": 0}
    for summary in summaries:
        key = (summary.get("repository"), summary.get("pull_request"))
        if not isinstance(key[0], str) or not isinstance(key[1], int):
            raise ValueError("batch summary has no repository and pull request identity")
        if key in seen:
            raise ValueError("batch has duplicate PR summary: " + key[0] + "#" + str(key[1]))
        seen.add(key); total["prs_observed"] += 1
        for name, value in one_pr_counts(summary["first_attempt"], summary["final_attempt"], summary.get("current_head_first_attempt")).items():
            if name != "prs_observed": total[name] = total.get(name, 0) + value
    return total


def source_snapshot() -> dict[str, Any]:
    try:
        def git(*args: str) -> str:
            return subprocess.run(["git", *args], check=True, capture_output=True, text=True, timeout=15).stdout.strip()
        return {"available": True, "head": git("rev-parse", "HEAD"), "tree": git("rev-parse", "HEAD^{tree}"),
                "status": git("status", "--porcelain=v1", "--untracked-files=all").splitlines()}
    except (OSError, subprocess.SubprocessError):
        return {"available": False}


def unknown_history_record() -> dict[str, Any]:
    return {"verification": "unknown", "history": "unavailable_or_incomplete", "workflow_lifecycle": "unknown", "workflow_conclusion": "not_available"}


def history_records(repo: str, pr: int) -> tuple[dict[str, Any], dict[str, Any], dict[str, Any], str | None, str]:
    """Read independent PR observations without erasing confirmed evidence.

    The pull read is the only shared prerequisite.  Once it has established a
    current head, a missing new-head run must not turn a confirmed old first
    attempt into unknown, and a first-attempt API error must not hide a fresh
    final attempt.
    """
    try:
        pull = gh_api(f"repos/{repo}/pulls/{pr}")
        if not isinstance(pull, dict):
            raise ValueError("pull request response is invalid")
        current_head, _ = _pull_head(pull)
    except (OSError, subprocess.SubprocessError, ValueError, KeyError, TypeError, json.JSONDecodeError):
        unknown = unknown_history_record()
        return unknown, unknown, unknown, None, "unknown"

    first, current_first, final = unknown_history_record(), unknown_history_record(), unknown_history_record()
    confirmed = 0
    try:
        first = first_pr_attempt(repo, pr, pull)
        confirmed += 1
    except (OSError, subprocess.SubprocessError, ValueError, KeyError, TypeError, json.JSONDecodeError):
        pass
    try:
        current_first = current_head_first_attempt(repo, pr, pull)
        confirmed += 1
    except (OSError, subprocess.SubprocessError, ValueError, KeyError, TypeError, json.JSONDecodeError):
        pass
    try:
        final_head, final = final_pr_attempt(repo, pr, pull)
        if final_head != current_head:
            raise ValueError("final attempt returned a stale pull-request head")
        confirmed += 1
    except (OSError, subprocess.SubprocessError, ValueError, KeyError, TypeError, json.JSONDecodeError):
        pass
    return first, current_first, final, current_head, "available" if confirmed == 3 else "partial" if confirmed else "unknown"


def step_summary(data: dict[str, Any]) -> None:
    path = os.environ.get("GITHUB_STEP_SUMMARY")
    if not path: return
    def describe(label: str, record: dict[str, Any]) -> str:
        return f"- {label}: `{record.get('verification', 'unknown')}`; lifecycle `{record.get('workflow_lifecycle', 'unknown')}`; conclusion `{record.get('workflow_conclusion', 'not_available')}`"
    counts = data["counts"]
    lines = ["## CI evidence report", "", f"PR `{data['repository']}#{data['pull_request']}`; history `{data['history']}`.", "",
             describe("PR first attempt", data["first_attempt"]), describe("Current-head final attempt", data["final_attempt"]),
             describe("Current reporting run", data["current_run_snapshot"]) + " (separate self snapshot)", "",
             f"Terminal denominators (success + failure): first `{counts['first_terminal_observed']}`, final `{counts['final_terminal_observed']}`. Cancelled, pending/incomplete and unknown are separate, not code-error rate.", ""]
    with open(path, "a", encoding="utf-8") as handle: handle.write("\n".join(lines))


def emit_receipt(args: argparse.Namespace) -> int:
    data = receipt(args.lane, args.setup, args.verification, args.job); write_json(args.out, data)
    if os.environ.get("GITHUB_OUTPUT"):
        primary = next((item for item in data["observations"] if item == "assertion_or_verification_failure"), None)
        primary = primary or next((item for item in data["observations"] if item != "cancelled"), data["observations"][0])
        compact = json.dumps(data["observations"], ensure_ascii=False, separators=(",", ":"))
        with open(os.environ["GITHUB_OUTPUT"], "a", encoding="utf-8") as output:
            output.write("classification=" + primary + "\n")
            output.write("observations=" + compact + "\n")
    return 0


def emit_summary(args: argparse.Namespace) -> int:
    needs, current = json.loads(args.needs), current_attempt(args.repo, args.run_id, args.run_attempt, args.head, json.loads(args.needs))
    first, current_first, final, current_head, history = history_records(args.repo, args.pr)
    lanes = lane_summary(needs)
    data = {"schema": 2, "repository": args.repo, "pull_request": args.pr, "reported_head_sha": args.head, "current_head_sha": current_head,
            "history": history, "source": source_snapshot(), "first_attempt": first, "current_head_first_attempt": current_first,
            "final_attempt": final, "current_run_snapshot": current, "lanes": lanes,
            "artifact_name": f"ci-quality-summary-{args.run_id}-{args.run_attempt}",
            "artifact_url": f"https://github.com/{args.repo}/actions/runs/{args.run_id}/artifacts",
            "timeline": evidence_timeline(first, current_first, final, current, lanes)}
    data["counts"] = one_pr_counts(first, final, current_first); write_json(args.out, data); step_summary(data)
    return 0


def emit_inspect(args: argparse.Namespace) -> int:
    first, current_first, final, current_head, history = history_records(args.repo, args.pr)
    data = {"schema": 2, "repository": args.repo, "pull_request": args.pr, "history": history, "current_head_sha": current_head,
            "first_attempt": first, "current_head_first_attempt": current_first, "final_attempt": final,
            "timeline": evidence_timeline(first, current_first, final, {}, {})}
    data["counts"] = one_pr_counts(first, final, current_first)
    print(json.dumps(data, ensure_ascii=False, indent=2, sort_keys=True)); return 0


def emit_batch(args: argparse.Namespace) -> int:
    summaries = json.loads(args.input.read_text())
    if not isinstance(summaries, list): raise ValueError("batch input must be a JSON list of PR summaries")
    write_json(args.out, {"schema": 1, "counts": batch_counts(summaries), "note": "terminal_observed is success plus failure; cancelled, pending_or_incomplete and unknown are separate."})
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__); sub = parser.add_subparsers(dest="command", required=True)
    receipt_parser = sub.add_parser("receipt"); receipt_parser.add_argument("--lane", choices=LANES, required=True); receipt_parser.add_argument("--setup", action="append", default=[]); receipt_parser.add_argument("--verification", required=True); receipt_parser.add_argument("--job", required=True); receipt_parser.add_argument("--out", type=Path, required=True); receipt_parser.set_defaults(func=emit_receipt)
    summary_parser = sub.add_parser("summary"); summary_parser.add_argument("--repo", required=True); summary_parser.add_argument("--pr", type=int, required=True); summary_parser.add_argument("--head", required=True); summary_parser.add_argument("--run-id", type=int, required=True); summary_parser.add_argument("--run-attempt", type=int, required=True); summary_parser.add_argument("--needs", required=True); summary_parser.add_argument("--out", type=Path, required=True); summary_parser.set_defaults(func=emit_summary)
    inspect_parser = sub.add_parser("inspect", help="read-only Actions history inspection"); inspect_parser.add_argument("--repo", required=True); inspect_parser.add_argument("--pr", type=int, required=True); inspect_parser.set_defaults(func=emit_inspect)
    batch_parser = sub.add_parser("batch"); batch_parser.add_argument("--input", type=Path, required=True); batch_parser.add_argument("--out", type=Path, required=True); batch_parser.set_defaults(func=emit_batch)
    args = parser.parse_args()
    return args.func(args)


if __name__ == "__main__": raise SystemExit(main())
