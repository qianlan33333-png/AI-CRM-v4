#!/usr/bin/env python3
"""Keep GitHub code checks exact-head and separate from staging acceptance.

Unknown/shared application changes run full lanes; registered capabilities and
the release-tool contract profile run only their selected checks. Staging
evidence is a separate post-merge domestic installation fact.
"""
import argparse
import io
import json
import os
from pathlib import Path
import re
import subprocess
import sys
from types import ModuleType
import zipfile

PHASES = ("preflight", "backend", "frontend", "browser", "archive-sdk")
WORKFLOW = ".github/workflows/ci.yml"
HEX40 = re.compile(r"^[0-9a-f]{40}$")
HEX64 = re.compile(r"^[0-9a-f]{64}$")


def git_text(root: Path, *args: str) -> str:
    return subprocess.check_output(["git", *args], cwd=root, text=True).strip()


def resolve_baseline(event, event_name, ref, sha):
    """Resolve the exact base used by the PR or main verification run."""
    if event_name == "pull_request":
        value = event.get("pull_request", {}).get("base", {}).get("sha", "")
        if HEX40.fullmatch(value):
            return value
        return ""
    before = event.get("before", "") if isinstance(event, dict) else ""
    if HEX40.fullmatch(before):
        return before
    if ref == "refs/heads/main":
        try:
            return git("HEAD^")
        except subprocess.SubprocessError:
            return ""
    return ""


def _module_from_ref(root: Path, ref: str, path: str, name: str) -> ModuleType:
    source = subprocess.check_output(["git", "show", f"{ref}:{path}"], cwd=root)
    module = ModuleType(name)
    module.__file__ = str(root / path)
    exec(compile(source, module.__file__, "exec"), module.__dict__)
    return module


def _changed_paths(root: Path, base: str, head: str) -> list[str]:
    lines = git_text(root, "diff", "--name-status", "--find-renames", base, head).splitlines()
    paths = set()
    for line in lines:
        fields = line.split("\t")
        if not fields:
            continue
        if fields[0].startswith(("R", "C")) and len(fields) >= 3:
            paths.update(fields[1:3])
        elif len(fields) >= 2:
            paths.add(fields[-1])
    return sorted(paths)


def trusted_impact_selection(base: str, head: str, root: Path | None = None) -> dict:
    """Select actual PR lanes using only the selector and registry in base.

    A failed or invalid analysis returns the conservative full lane set. This
    keeps a PR from narrowing its own gate by changing the policy it is under.
    """
    root = (root or Path(__file__).resolve().parents[2]).resolve()
    try:
        base = git_text(root, "rev-parse", "--verify", "--end-of-options", base + "^{commit}")
        head = git_text(root, "rev-parse", "--verify", "--end-of-options", head + "^{commit}")
        if not HEX40.fullmatch(base) or not HEX40.fullmatch(head):
            raise ValueError("impact base/head is not a commit SHA")
        changed = _changed_paths(root, base, head)
        if not changed:
            raise ValueError("impact diff is empty")
        tracked = sorted(set(git_text(root, "ls-tree", "-r", "--name-only", base).splitlines())
                         | set(git_text(root, "ls-tree", "-r", "--name-only", head).splitlines()))
        registry = json.loads(subprocess.check_output(
            ["git", "show", f"{base}:docs/governance/capability-impact.json"], cwd=root, text=True))
        governance = _module_from_ref(root, base, "scripts/ci/governance_impact.py", "trusted_governance_impact")
        selector = _module_from_ref(root, base, "scripts/ci/impact_selection.py", "trusted_impact_selection")
        report = governance.analyze(root, registry, tracked, changed)
        selected = selector.select(report)
        lanes = selected.get("lanes")
        if (not isinstance(lanes, list) or not lanes or any(lane not in PHASES for lane in lanes)
                or selected.get("mode") not in {"full", "targeted"}):
            raise ValueError("trusted selector returned an invalid lane set")
        return {"mode": selected["mode"], "lanes": lanes, "checks": selected.get("checks", []),
                "profile": selected.get("profile", "full"),
                "reason": selected.get("reason", "trusted-base-selector"),
                "selection_source": "trusted-base"}
    except (ValueError, KeyError, OSError, subprocess.SubprocessError, json.JSONDecodeError) as error:
        return {"mode": "full", "lanes": list(PHASES), "checks": [], "profile": "full",
                "reason": "trusted-base-impact-failed: " + type(error).__name__,
                "selection_source": "fail-closed"}


def exact_source_binding(baseline: str, head: str, tree: str,
                         root: Path | None = None) -> dict | None:
    """Bind required-check policy to this clean checkout without graph eligibility."""
    root = (root or Path(__file__).resolve().parents[2]).resolve()
    try:
        if git_text(root, "rev-parse", "HEAD") != head:
            return None
        if git_text(root, "status", "--porcelain=v1", "--untracked-files=all"):
            return None
        baseline_tree = git_text(root, "rev-parse", baseline + "^{tree}")
        actual_head_tree = git_text(root, "rev-parse", head + "^{tree}")
        if (actual_head_tree != tree or not HEX40.fullmatch(baseline_tree)
                or not HEX40.fullmatch(actual_head_tree)):
            return None
        sys.path.insert(0, str(root / "scripts/ci"))
        try:
            import affected_plan
        finally:
            try:
                sys.path.remove(str(root / "scripts/ci"))
            except ValueError:
                pass
        planner = affected_plan.planner_policy_fingerprint()
        repository = affected_plan.repository_policy_fingerprint(root, head)
        fingerprint = affected_plan.combined_policy_fingerprint(planner, repository)
        if not HEX64.fullmatch(fingerprint):
            return None
    except (ImportError, AttributeError, OSError, ValueError, subprocess.SubprocessError):
        return None
    return {"baseline_sha": baseline, "baseline_tree": baseline_tree,
            "head_sha": head, "head_tree": tree, "policy_fingerprint": fingerprint,
            "source_clean": True, "shadow_eligible": False}


def affected_plan_binding(path: str | None, baseline: str, head: str, tree: str) -> dict | None:
    """Validate the optional shadow plan's source binding, never its sample eligibility."""
    if not path:
        return exact_source_binding(baseline, head, tree)
    try:
        plan = json.loads(Path(path).read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        return exact_source_binding(baseline, head, tree)
    if (not isinstance(plan, dict) or plan.get("schema") != 1
            or plan.get("observed_mode") != "shadow" or plan.get("source_clean") is not True
            or plan.get("baseline_sha") != baseline or plan.get("head_sha") != head
            or plan.get("head_tree") != tree
            or not isinstance(plan.get("policy_fingerprint"), str)
            or not HEX64.fullmatch(plan["policy_fingerprint"])):
        return None
    source = plan.get("source", {})
    if (not isinstance(source, dict) or source.get("working_tree_clean") is not True
            or source.get("head_matches") is not True):
        return None
    bound = exact_source_binding(baseline, head, tree)
    if bound is None or bound["policy_fingerprint"] != plan["policy_fingerprint"]:
        return None
    bound["shadow_eligible"] = plan.get("evidence_eligible") is True
    return bound


def read_regression_record(data: bytes) -> dict:
    with zipfile.ZipFile(io.BytesIO(data)) as archive:
        entries = archive.infolist()
        if (len(entries) != 1 or entries[0].filename != "main-regression.json"
                or entries[0].file_size > 65536):
            raise ValueError("unexpected main regression artifact")
        record = json.loads(archive.read(entries[0]))
        if not isinstance(record, dict):
            raise ValueError("main regression artifact is not an object")
        return record


def _first_parent_ancestor(ancestor: str, descendant: str) -> bool | None:
    if not HEX40.fullmatch(ancestor or "") or not HEX40.fullmatch(descendant or ""):
        return None
    try:
        entries = subprocess.check_output(["git", "rev-list", "--first-parent", descendant], text=True).splitlines()
    except subprocess.SubprocessError:
        return None
    return ancestor in entries


def _jobs_pass(repo: str, run: dict, full: bool) -> bool | None:
    attempt = run.get("run_attempt")
    run_id = run.get("id")
    if (isinstance(attempt, bool) or not isinstance(attempt, int) or attempt < 1
            or isinstance(run_id, bool) or not isinstance(run_id, int)):
        return None
    try:
        response = api(f"repos/{repo}/actions/runs/{run_id}/attempts/{attempt}/jobs?per_page=100")
    except Exception:
        return None
    jobs = response.get("jobs") if isinstance(response, dict) else None
    if not isinstance(jobs, list):
        return None
    results = {job.get("name"): job.get("conclusion") for job in jobs if isinstance(job, dict)}
    required = ("plan", "governance", "check") + (PHASES if full else ())
    if any(results.get(name) in {"failure", "timed_out", "action_required", "startup_failure", "cancelled"}
           for name in required):
        return False
    return True if all(results.get(name) == "success" for name in required) else None


def _regression_artifact(repo: str, run: dict) -> dict | None:
    run_id, attempt = run.get("id"), run.get("run_attempt")
    if isinstance(run_id, bool) or not isinstance(run_id, int) or isinstance(attempt, bool) or not isinstance(attempt, int):
        return None
    expected_name = f"ci-main-regression-{run_id}-{attempt}"
    try:
        response = api(f"repos/{repo}/actions/runs/{run_id}/artifacts?per_page=100")
        artifacts = response.get("artifacts") if isinstance(response, dict) else None
        if not isinstance(artifacts, list):
            return None
        matching = [item for item in artifacts if isinstance(item, dict) and item.get("name") == expected_name
                    and item.get("expired") is not True and item.get("size_in_bytes", 0) <= 131072]
        if len(matching) != 1:
            return None
        payload = api(f"repos/{repo}/actions/artifacts/{matching[0]['id']}/zip", raw=True)
        if not isinstance(payload, bytes):
            return None
        return read_regression_record(payload)
    except Exception:
        return None


def _record_binding(record: dict, run: dict, repo: str) -> bool:
    run_id, attempt = run.get("id"), run.get("run_attempt")
    sha = run.get("head_sha")
    if (record.get("schema") != 1 or record.get("kind") != "main_full_regression"
            or record.get("repository") != repo or record.get("workflow_path") != WORKFLOW
            or record.get("branch") != "main" or record.get("event") != run.get("event")
            or record.get("run_id") != run_id or record.get("run_attempt") != attempt
            or record.get("tested_head_sha") != sha or record.get("required_check") != "check"
            or not HEX40.fullmatch(sha or "") or not HEX40.fullmatch(record.get("tested_tree") or "")
            or not HEX40.fullmatch(record.get("baseline_sha") or "")
            or not HEX64.fullmatch(record.get("policy_fingerprint") or "")):
        return False
    try:
        tree = git(sha + "^{tree}")
        parents = git_text(Path(__file__).resolve().parents[2], "rev-list", "--parents", "-n", "1", sha).split()[1:]
    except (OSError, subprocess.SubprocessError):
        return False
    return tree == record["tested_tree"] and bool(parents) and parents[0] == record["baseline_sha"]


def _validate_full_regression(repo: str, run: dict, record: dict) -> str:
    """Validate a full/reused artifact against its completed run and exact jobs."""
    if not _record_binding(record, run, repo):
        return "unknown"
    event = run.get("event")
    allowed_event = (event == "schedule" or event == "push"
                     or event == "workflow_dispatch" and record.get("force_full") is True)
    if not allowed_event or record.get("mode") not in {"full", "verified"}:
        return "unknown"
    if run.get("status") != "completed":
        return "in_progress" if run.get("status") in {"queued", "in_progress", "waiting", "requested"} else "unknown"
    if record.get("mode") == "full":
        lane_map = record.get("lanes")
        full_shape = (isinstance(lane_map, dict) and set(lane_map) == set(PHASES)
                      and all(isinstance(lane_map.get(name), dict)
                              and lane_map[name].get("result") == "success"
                              and lane_map[name].get("classification") == "none" for name in PHASES))
        if record.get("status") != "success" or record.get("classification") != "none" or not full_shape:
            return "failure" if record.get("status") == "failure" else "unknown"
        if (record.get("gate_outcome") != "success" or record.get("governance_result") != "success"
                or record.get("plan_result") != "success" or record.get("required_check_status") != "completed"
                or record.get("required_check_conclusion") != "success"):
            return "unknown"
        # Optional reporting jobs can fail after all required checks passed.
        # The exact required jobs and stable gate determine the proof.
        jobs = _jobs_pass(repo, run, full=True)
        return "success" if jobs is True else "failure" if jobs is False else "unknown"
    if record.get("status") != "reused" or record.get("classification") != "none":
        return "failure" if record.get("status") == "failure" else "unknown"
    source_id, source_attempt = record.get("reused_full_run_id"), record.get("reused_full_run_attempt")
    if isinstance(source_id, bool) or not isinstance(source_id, int) or isinstance(source_attempt, bool) or not isinstance(source_attempt, int):
        return "unknown"
    jobs = _jobs_pass(repo, run, full=False)
    if (jobs is not True or record.get("required_check_conclusion") != "success"
            or record.get("governance_result") != "success" or record.get("plan_result") != "success"):
        return "failure" if jobs is False else "unknown"
    try:
        source = api(f"repos/{repo}/actions/runs/{source_id}/attempts/{source_attempt}")
    except Exception:
        return "unknown"
    if (not isinstance(source, dict) or source.get("id") != source_id
            or source.get("run_attempt") != source_attempt or source.get("event") not in {"schedule", "push", "workflow_dispatch"}
            or source.get("head_sha") != run.get("head_sha")):
        return "unknown"
    source_record = _regression_artifact(repo, source)
    if (not isinstance(source_record, dict) or source_record.get("mode") != "full"
            or source_record.get("tested_tree") != record.get("tested_tree")
            or source_record.get("baseline_sha") != record.get("baseline_sha")
            or source_record.get("policy_fingerprint") != record.get("policy_fingerprint")):
        return "unknown"
    return "reused" if _validate_full_regression(repo, source, source_record) == "success" else "unknown"


def daily_regression_status(repo: str, head: str, exclude_run_id: str | int | None = None,
                            exclude_attempt: str | int | None = None) -> str:
    """Return latest validated main regression state along first-parent history.

    The current run is excluded so a scheduled check cannot block on its own
    in-progress Actions record. Reused schedules preserve prior full success,
    while a later failure/unknown state overrides that older success.
    """
    if not HEX40.fullmatch(head or ""):
        return "unknown"
    try:
        all_runs = []
        for event in ("schedule", "push", "workflow_dispatch"):
            response = api(f"repos/{repo}/actions/workflows/ci.yml/runs?event={event}&branch=main&per_page=100")
            runs = response.get("workflow_runs") if isinstance(response, dict) else None
            if not isinstance(runs, list):
                return "unknown"
            all_runs.extend(run for run in runs if isinstance(run, dict) and run.get("event") == event
                            and run.get("path") == WORKFLOW and run.get("head_branch") == "main")
        excluded = (str(exclude_run_id or ""), str(exclude_attempt or ""))
        try:
            first_parent_history = set(subprocess.check_output(
                ["git", "rev-list", "--first-parent", head],
                cwd=Path(__file__).resolve().parents[2], text=True).splitlines())
        except subprocess.SubprocessError:
            return "unknown"
        matching = []
        for run in all_runs:
            if (str(run.get("id", "")), str(run.get("run_attempt", ""))) == excluded:
                continue
            sha = run.get("head_sha", "")
            if not HEX40.fullmatch(sha or ""):
                return "unknown"
            if sha in first_parent_history:
                matching.append(run)
        if not matching:
            # A scheduled run is visible in Actions before planning finishes.
            # Excluding the current attempt must preserve the no-history bootstrap.
            prior_runs = [run for run in all_runs
                          if (str(run.get("id", "")), str(run.get("run_attempt", ""))) != excluded]
            return "unknown" if any(run.get("event") == "schedule" for run in prior_runs) else "no_history"
        matching.sort(key=lambda run: (run.get("run_started_at") or run.get("created_at") or "",
                                       run.get("id", 0), run.get("run_attempt", 0)))
        had_full_success = False
        unresolved = None
        for run in matching:
            if run.get("status") != "completed":
                outcome = ("in_progress" if run.get("status") in {"queued", "in_progress", "waiting", "requested"}
                           else "unknown")
                if run.get("event") in {"schedule", "workflow_dispatch"} or run.get("conclusion") != "success":
                    unresolved = outcome
                continue
            record = _regression_artifact(repo, run)
            if record is None:
                if run.get("event") in {"schedule", "workflow_dispatch"}:
                    unresolved = "unknown"
                # A regular main push predates this regression receipt path
                # unless it actually published a full-regression artifact.
                # Its exact required check remains the release gate; legacy
                # push failures without a regression receipt are not daily
                # regression observations.
                continue
            outcome = _validate_full_regression(repo, run, record)
            if outcome == "success":
                had_full_success = True
                unresolved = None
            elif outcome == "reused":
                # A same-tree reference can support unchanged scheduled runs,
                # but it is not a new full result and cannot clear a failure.
                if unresolved is None:
                    had_full_success = True
                continue
            elif outcome == "failure":
                unresolved = "failure"
            elif outcome == "unknown":
                unresolved = "unknown"
            # `reused` is validated as success above, but is not a new full
            # result. It cannot clear a failure in the release controller.
        if unresolved:
            return unresolved
        return "success" if had_full_success else "no_history"
    except (KeyError, TypeError, ValueError, OSError, subprocess.SubprocessError):
        return "unknown"


def git(ref):
    return subprocess.check_output(["git", "rev-parse", ref], text=True).strip()


def requires_impact_selection() -> bool:
    """Use the capability impact report for every non-documentation PR change."""
    base = os.environ.get("GITHUB_BASE_SHA", "")
    if not re.fullmatch(r"[0-9a-f]{40}", base):
        return True
    try:
        changed = subprocess.check_output(["git", "diff", "--name-only", f"{base}...HEAD"], text=True).splitlines()
    except subprocess.SubprocessError:
        return True
    return not changed or any(not (path.startswith("docs/") and path.endswith(".md")
                                  and not path.startswith("docs/governance/")) for path in changed)


def api(path, raw=False):
    result = subprocess.run(["gh", "api", path], check=True, capture_output=True, timeout=30)
    return result.stdout if raw else json.loads(result.stdout)


def eligible_run(run, repo, head):
    return (run.get("event") == "pull_request" and run.get("status") == "completed"
            and run.get("head_sha") == head
            and run.get("path") == WORKFLOW
            and (run.get("head_repository") or {}).get("full_name") == repo)


def valid_proof(proof, run, pr, commit, tree, repo, baseline_sha, baseline_tree,
                policy_fingerprint, expected_selection=None):
    if (not isinstance(proof, dict) or not isinstance(run, dict) or not isinstance(pr, dict)
            or not isinstance(commit, dict) or not isinstance(expected_selection, dict)):
        return False
    mode = expected_selection.get("mode")
    lanes = expected_selection.get("selected_lanes")
    checks = expected_selection.get("selected_checks")
    profile = expected_selection.get("profile")
    if (mode not in {"full", "targeted", "light"} or not isinstance(lanes, list)
            or not isinstance(checks, list) or not isinstance(profile, str)
            or any(not isinstance(lane, str) or lane not in PHASES for lane in lanes)
            or len(lanes) != len(set(lanes))):
        return False
    if ((mode == "full" and (set(lanes) != set(PHASES) or checks or profile != "full"))
            or (mode == "targeted" and (not lanes or "preflight" not in lanes))
            or (mode == "light" and (lanes or checks or profile != "none"))):
        return False
    phases = proof.get("phases")
    if not isinstance(phases, dict):
        return False
    expected_phases = {name: ("success" if mode == "full" or name in lanes else "skipped")
                       for name in PHASES}
    return (proof.get("schema") == 3 and proof.get("mode") == mode
            and proof.get("repository") == repo
            and proof.get("event") == "pull_request"
            and proof.get("run_id") == run["id"]
            and proof.get("run_attempt") == run["run_attempt"]
            and proof.get("pr") == pr["number"]
            and proof.get("head") == pr["head"]["sha"]
            and proof.get("baseline_sha") == baseline_sha
            and proof.get("baseline_tree") == baseline_tree
            and proof.get("policy_fingerprint") == policy_fingerprint
            and proof.get("selection") == {"mode": mode, "selected_lanes": lanes,
                                           "selected_checks": checks, "profile": profile}
            and proof.get("tested_sha") == commit.get("sha")
            and proof.get("tree") == tree == commit.get("commit", {}).get("tree", {}).get("sha")
            and len(commit.get("parents", [])) == 2
            and commit["parents"][0]["sha"] == baseline_sha
            and commit["parents"][1]["sha"] == pr["head"]["sha"]
            and phases == expected_phases)


def expected_enforced_selection(baseline: str, head: str,
                                root: Path | None = None) -> dict | None:
    """Recompute the PR's enforced selection with only rules from its base."""
    root = (root or Path(__file__).resolve().parents[2]).resolve()
    try:
        changed = _changed_paths(root, baseline, head)
        if not changed:
            return None
        docs_only = all(path.startswith("docs/") and path.endswith(".md")
                        and not path.startswith("docs/governance/") for path in changed)
        if docs_only:
            return {"mode": "light", "selected_lanes": [], "selected_checks": [],
                    "profile": "none"}
        selected = trusted_impact_selection(baseline, head, root)
        if selected.get("mode") == "targeted":
            lanes = selected.get("lanes")
            checks = selected.get("checks")
            profile = selected.get("profile")
            if (not isinstance(lanes, list) or not lanes or any(lane not in PHASES for lane in lanes)
                    or not isinstance(checks, list) or not isinstance(profile, str)):
                return None
            return {"mode": "targeted", "selected_lanes": lanes,
                    "selected_checks": checks, "profile": profile}
        return {"mode": "full", "selected_lanes": list(PHASES),
                "selected_checks": [], "profile": "full"}
    except (OSError, ValueError, subprocess.SubprocessError):
        return None


def read_proof(data):
    with zipfile.ZipFile(io.BytesIO(data)) as archive:
        entries = archive.infolist()
        if len(entries) != 1 or entries[0].filename != "verification.json" or entries[0].file_size > 16384:
            raise ValueError("unexpected verification artifact")
        return json.loads(archive.read(entries[0]))


def find_verified_run(repo, sha, tree, baseline_sha, baseline_tree, policy_fingerprint,
                      expected_selection):
    """Reuse only a successful PR proof bound to the exact main merge pair."""
    if (not all(HEX40.fullmatch(value or "") for value in (sha, tree, baseline_sha, baseline_tree))
            or not HEX64.fullmatch(policy_fingerprint or "")):
        return None
    prefix = f"repos/{repo}"
    try:
        merged_commit = api(f"{prefix}/commits/{sha}")
        if (merged_commit.get("sha") != sha
                or merged_commit.get("commit", {}).get("tree", {}).get("sha") != tree
                or not merged_commit.get("parents")
                or merged_commit["parents"][0].get("sha") != baseline_sha):
            return None
        pulls = api(f"{prefix}/commits/{sha}/pulls?per_page=100")
    except Exception:
        return None
    if not isinstance(pulls, list):
        return None
    for pr in pulls:
        if (not isinstance(pr, dict) or not pr.get("merged_at") or pr.get("merge_commit_sha") != sha
                or pr.get("base", {}).get("ref") != "main"
                or pr.get("base", {}).get("repo", {}).get("full_name") != repo):
            continue
        head = pr.get("head", {}).get("sha")
        if not isinstance(head, str) or not HEX40.fullmatch(head):
            continue
        try:
            runs_response = api(f"{prefix}/actions/workflows/ci.yml/runs?event=pull_request&head_sha={head}&status=completed&per_page=20")
            runs = runs_response.get("workflow_runs") if isinstance(runs_response, dict) else None
        except Exception:
            continue
        if not isinstance(runs, list):
            continue
        for run in runs:
            if not isinstance(run, dict) or not eligible_run(run, repo, head):
                continue
            expected_name = f"ci-verification-{run['id']}-{run['run_attempt']}"
            try:
                artifact_response = api(f"{prefix}/actions/runs/{run['id']}/artifacts?per_page=100")
                artifacts = artifact_response.get("artifacts") if isinstance(artifact_response, dict) else None
            except Exception:
                continue
            if not isinstance(artifacts, list):
                continue
            for artifact in artifacts:
                if (not isinstance(artifact, dict) or artifact.get("name") != expected_name
                        or artifact.get("expired") or artifact.get("size_in_bytes", 0) > 65536):
                    continue
                try:
                    proof = read_proof(api(f"{prefix}/actions/artifacts/{artifact['id']}/zip", raw=True))
                    tested_sha = proof.get("tested_sha", "")
                    if not HEX40.fullmatch(tested_sha):
                        continue
                    commit = api(f"{prefix}/commits/{tested_sha}")
                    if not valid_proof(proof, run, pr, commit, tree, repo,
                                       baseline_sha, baseline_tree, policy_fingerprint,
                                       expected_selection):
                        continue
                    jobs_response = api(f"{prefix}/actions/runs/{run['id']}/attempts/{run['run_attempt']}/jobs?per_page=100")
                    jobs = jobs_response.get("jobs") if isinstance(jobs_response, dict) else None
                    if not isinstance(jobs, list):
                        continue
                    results = {job["name"]: job["conclusion"] for job in jobs if isinstance(job, dict)}
                    expected_phases = {
                        name: ("success" if expected_selection.get("mode") == "full"
                               or name in expected_selection.get("selected_lanes", []) else "skipped")
                        for name in PHASES
                    }
                    if (all(results.get(name) == outcome for name, outcome in expected_phases.items())
                            and results.get("governance") == "success"
                            and results.get("check") == "success"):
                        return run["id"]
                except Exception:
                    continue
    return None


def find_full_regression_proof(repo: str, sha: str, tree: str, baseline_sha: str,
                              policy_fingerprint: str,
                              exclude_run_id: str | int | None = None,
                              exclude_attempt: str | int | None = None) -> dict | None:
    """Find an exact, completed full main run usable for unchanged schedules."""
    if (not all(HEX40.fullmatch(value or "") for value in (sha, tree, baseline_sha))
            or not HEX64.fullmatch(policy_fingerprint or "")):
        return None
    excluded = (str(exclude_run_id or ""), str(exclude_attempt or ""))
    found = []
    for event in ("schedule", "push", "workflow_dispatch"):
        try:
            response = api(f"repos/{repo}/actions/workflows/ci.yml/runs?event={event}&branch=main&head_sha={sha}&per_page=100")
        except Exception:
            continue
        runs = response.get("workflow_runs") if isinstance(response, dict) else None
        if not isinstance(runs, list):
            continue
        for run in runs:
            if (not isinstance(run, dict) or run.get("path") != WORKFLOW
                    or run.get("event") != event or run.get("head_branch") != "main"
                    or run.get("head_sha") != sha
                    or (str(run.get("id", "")), str(run.get("run_attempt", ""))) == excluded
                    or run.get("status") != "completed"):
                continue
            record = _regression_artifact(repo, run)
            if (not isinstance(record, dict) or record.get("mode") != "full"
                    or record.get("tested_tree") != tree or record.get("baseline_sha") != baseline_sha
                    or record.get("policy_fingerprint") != policy_fingerprint
                    or _validate_full_regression(repo, run, record) != "success"):
                continue
            found.append(run)
    if not found:
        return None
    best = max(found, key=lambda run: (run.get("run_started_at") or run.get("created_at") or "", run.get("id", 0)))
    return {"run_id": best["id"], "run_attempt": best["run_attempt"],
            "tested_sha": sha, "tested_tree": tree, "baseline_sha": baseline_sha,
            "policy_fingerprint": policy_fingerprint}


def require_results(needs, full, event, ref):
    if needs.get("plan", {}).get("result") != "success":
        raise ValueError("verification plan did not succeed")
    plan_outputs = needs["plan"].get("outputs", {})
    mode = plan_outputs.get("mode", "full" if full else "verified")
    try:
        lanes = set(json.loads(plan_outputs.get("lanes", "[]")))
    except (TypeError, json.JSONDecodeError):
        raise ValueError("invalid verification lane plan")
    if not lanes.issubset(PHASES):
        raise ValueError("unknown verification lane")
    if mode == "verified":
        if full or lanes:
            raise ValueError("reused verification must not schedule lanes")
        run_id = plan_outputs.get("verified_run", "")
        source = plan_outputs.get("verified_source", "")
        allowed_source = ((event == "schedule" and source == "main_full_regression")
                          or (event == "push" and source == "pull_request"))
        if (not allowed_source or ref != "refs/heads/main"
                or not isinstance(run_id, str) or not run_id.isdigit()):
            raise ValueError("main reuse must identify a verified PR or full regression proof")
        if event == "schedule":
            attempt = plan_outputs.get("verified_run_attempt", "")
            if not isinstance(attempt, str) or not attempt.isdigit():
                raise ValueError("scheduled reuse requires the source full run attempt")
    elif mode == "targeted":
        if event != "pull_request" or full or "preflight" not in lanes:
            raise ValueError("targeted verification is only valid for pull requests with preflight")
    elif mode not in {"full", "light"}:
        raise ValueError("unknown verification mode")
    if event == "pull_request" and mode in {"targeted", "light"}:
        baseline = plan_outputs.get("baseline_sha") or os.environ.get("PR_BASE_SHA", "")
        expected = expected_enforced_selection(baseline, os.environ.get("GITHUB_SHA", ""))
        actual = {"mode": mode, "selected_lanes": list(json.loads(plan_outputs.get("lanes", "[]"))),
                  "selected_checks": json.loads(plan_outputs.get("checks", "[]")),
                  "profile": plan_outputs.get("profile", "none")}
        if expected != actual:
            raise ValueError("targeted/light selection does not match the trusted base policy")
    if mode == "light":
        if event != "pull_request" or full or lanes:
            raise ValueError("light verification is only valid for pull requests without scheduled lanes")
        for name in PHASES:
            if needs.get(name, {}).get("result") != "skipped":
                raise ValueError(f"{name}: expected skipped")
        return
    if mode == "full" and not full:
        raise ValueError("full verification requires full=true")
    for name in PHASES:
        lane_expected = "success" if mode == "full" or name in lanes else "skipped"
        if needs.get(name, {}).get("result") != lane_expected:
            raise ValueError(f"{name}: expected {lane_expected}")


def _write_plan_outputs(full: bool, mode: str, verified_run: int | None = None,
                        verified_source: str = "", verified_run_attempt: int | None = None,
                        conservative_full: bool = False, baseline_sha: str = "",
                        policy_fingerprint: str = "",
                        daily_status: str = "unknown") -> None:
    with open(os.environ["GITHUB_OUTPUT"], "a") as output:
        output.write(f"full={str(full).lower()}\n")
        output.write(f"verified_run={verified_run or ''}\n")
        output.write(f"verified_source={verified_source}\n")
        output.write(f"verified_run_attempt={verified_run_attempt or ''}\n")
        output.write(f"mode={mode}\n")
        output.write(f"conservative_full={str(conservative_full).lower()}\n")
        output.write(f"baseline_sha={baseline_sha}\n")
        output.write(f"policy_fingerprint={policy_fingerprint}\n")
        output.write(f"daily_status={daily_status}\n")


def plan_decision(event: str, ref: str, repo: str, sha: str, force_full: bool,
                  baseline_sha: str, plan_path: str | None = None,
                  run_attempt: str | None = None) -> dict:
    """Decide whether checks can be reused or must run, failing closed."""
    try:
        tree = git("HEAD^{tree}")
    except subprocess.SubprocessError:
        tree = ""
    binding = affected_plan_binding(plan_path, baseline_sha, sha, tree)
    baseline_tree = ""
    if HEX40.fullmatch(baseline_sha):
        try:
            baseline_tree = git(baseline_sha + "^{tree}")
        except subprocess.SubprocessError:
            baseline_tree = ""

    run_id = os.environ.get("GITHUB_RUN_ID", "")
    run_attempt = os.environ.get("GITHUB_RUN_ATTEMPT", "") if run_attempt is None else str(run_attempt)
    if (ref == "refs/heads/main" and event in {"schedule", "push", "workflow_dispatch"}
            and run_attempt != "1"):
        # The public Actions workflow-runs API exposes only the latest attempt.
        # Once a run is retried, a previous failed attempt can disappear from
        # history. A rerun must therefore execute a fresh full result instead
        # of reusing an older PR or same-tree schedule proof.
        reason = ("main-rerun-forces-full" if run_attempt.isdigit()
                  else "main-run-attempt-unknown-forces-full")
        return {"full": True, "mode": "full", "verified_run": None,
                "verified_run_attempt": None, "verified_source": "",
                "conservative_full": True, "reason": reason,
                "daily_status": "not-checked", "binding": binding}
    if event == "schedule" and ref == "refs/heads/main":
        daily_status = daily_regression_status(repo, sha, run_id, run_attempt)
        source = None
        if daily_status in {"success", "no_history"} and binding:
            source = find_full_regression_proof(repo, sha, tree, baseline_sha,
                                                binding["policy_fingerprint"], run_id, run_attempt)
        if source:
            return {"full": False, "mode": "verified", "verified_run": source["run_id"],
                    "verified_run_attempt": source["run_attempt"],
                    "verified_source": "main_full_regression", "conservative_full": False,
                    "reason": "unchanged-tree-full-regression-reused", "daily_status": daily_status,
                    "binding": binding}
        return {"full": True, "mode": "full", "verified_run": None,
                "verified_source": "", "conservative_full": True,
                "reason": "scheduled-full-regression" if daily_status in {"success", "no_history"}
                else "main-daily-regression-" + daily_status,
                "daily_status": daily_status, "binding": binding}
    if event == "pull_request":
        if force_full:
            return {"full": True, "mode": "full", "verified_run": None,
                    "conservative_full": True, "reason": "explicit-full-request",
                    "daily_status": "not-checked", "binding": binding}
        daily_status = daily_regression_status(repo, baseline_sha, run_id, run_attempt)
        if daily_status not in {"success", "no_history"}:
            return {"full": True, "mode": "full", "verified_run": None,
                    "conservative_full": True,
                    "reason": "main-daily-regression-" + daily_status,
                    "daily_status": daily_status, "binding": binding}
        if not requires_impact_selection():
            return {"full": False, "mode": "light", "verified_run": None,
                    "conservative_full": False, "reason": "documentation-only",
                    "daily_status": daily_status, "binding": binding}
        return {"full": True, "mode": "full", "verified_run": None,
                "conservative_full": False, "reason": "trusted-base-impact-selection",
                "daily_status": daily_status, "binding": binding}
    if event == "workflow_dispatch" and ref == "refs/heads/main":
        daily_status = daily_regression_status(repo, sha, run_id, run_attempt)
        return {"full": True, "mode": "full", "verified_run": None,
                "verified_source": "", "verified_run_attempt": None, "conservative_full": True,
                "reason": "explicit-full-request" if force_full else "manual-main-full-run",
                "daily_status": daily_status, "binding": binding}
    if event == "push" and ref == "refs/heads/main":
        daily_status = daily_regression_status(repo, sha, run_id, run_attempt)
        verified_run = None
        if (not force_full and daily_status in {"success", "no_history"} and binding
                and binding.get("baseline_tree") == baseline_tree):
            try:
                expected = expected_enforced_selection(baseline_sha, sha)
                verified_run = find_verified_run(repo, sha, tree, baseline_sha, baseline_tree,
                                                 binding["policy_fingerprint"], expected)
            except Exception:
                verified_run = None
        if verified_run is not None:
            return {"full": False, "mode": "verified", "verified_run": verified_run,
                    "verified_run_attempt": None,
                    "verified_source": "pull_request",
                    "conservative_full": False, "reason": "exact-tree-base-policy-proof",
                    "daily_status": daily_status, "binding": binding}
        reason = "explicit-full-request" if force_full else (
            "main-daily-regression-" + daily_status if daily_status not in {"success", "no_history"}
            else "exact-main-tree-proof-unavailable")
        return {"full": True, "mode": "full", "verified_run": None,
                "verified_source": "",
                "conservative_full": True, "reason": reason,
                "daily_status": daily_status, "binding": binding}
    return {"full": True, "mode": "full", "verified_run": None,
            "conservative_full": True, "reason": "unsupported-or-unknown-event",
            "daily_status": "unknown", "binding": binding}


def _write_selection_output(selection: dict) -> None:
    print(json.dumps(selection, ensure_ascii=False, separators=(",", ":")))


def scheduled_regression_record(needs: dict, event: str, ref: str, repo: str, sha: str,
                               gate_outcome: str, baseline_sha: str,
                               policy_fingerprint: str, run_id: str,
                               run_attempt: str, mode: str, force_full: bool,
                               run_metadata: dict | None = None,
                               verified_source: str = "", verified_run: str | int | None = None,
                               verified_run_attempt: str | int | None = None) -> dict:
    """Describe a main full run or an exact reuse of an earlier full proof."""
    try:
        tree = git("HEAD^{tree}")
    except subprocess.SubprocessError:
        tree = ""
    lane_states = {}
    classifications = []
    for lane in PHASES:
        item = needs.get(lane, {}) if isinstance(needs, dict) else {}
        outputs = item.get("outputs", {}) if isinstance(item, dict) else {}
        observations = outputs.get("observations", []) if isinstance(outputs, dict) else []
        if isinstance(observations, str):
            try:
                observations = json.loads(observations)
            except json.JSONDecodeError:
                observations = ["unknown_failure"]
        if not isinstance(observations, list) or any(not isinstance(value, str) for value in observations):
            observations = ["unknown_failure"]
        if "assertion_or_verification_failure" in observations:
            classification = "business_regression"
        elif "environment_setup_failure" in observations:
            classification = "environment_failure"
        elif item.get("result") == "success" or mode == "verified" and item.get("result") == "skipped":
            classification = "none"
        else:
            classification = "unknown"
        lane_states[lane] = {"result": item.get("result", "unknown"),
                             "classification": classification,
                             "observations": observations}
        classifications.extend(observations)
    governance = needs.get("governance", {}).get("result", "unknown") if isinstance(needs, dict) else "unknown"
    plan_result = needs.get("plan", {}).get("result", "unknown") if isinstance(needs, dict) else "unknown"
    all_lanes_pass = all(lane_states[name]["result"] == "success" for name in PHASES)
    all_lanes_skipped = all(lane_states[name]["result"] == "skipped" for name in PHASES)
    identifiers_valid = (HEX40.fullmatch(sha or "") and HEX40.fullmatch(tree or "")
                         and HEX40.fullmatch(baseline_sha or "") and HEX64.fullmatch(policy_fingerprint or "")
                         and str(run_id).isdigit() and str(run_attempt).isdigit()
                         and isinstance(run_metadata, dict)
                         and run_metadata.get("id") == int(run_id)
                         and run_metadata.get("run_attempt") == int(run_attempt)
                         and isinstance(run_metadata.get("created_at"), str)
                         and isinstance(run_metadata.get("run_started_at"), str))
    allowed_main_event = (event in {"schedule", "push", "workflow_dispatch"}
                          and ref == "refs/heads/main")
    full_status = ("success" if allowed_main_event and mode == "full" and gate_outcome == "success"
                   and all_lanes_pass and governance == "success" and plan_result == "success" and identifiers_valid
                   else "failure" if any(lane_states[name]["result"] == "failure" for name in PHASES)
                   or governance == "failure" or plan_result == "failure" or gate_outcome == "failure"
                   else "unknown")
    reuse_valid = (allowed_main_event and mode == "verified" and verified_source == "main_full_regression"
                   and isinstance(verified_run, (str, int)) and str(verified_run).isdigit()
                   and gate_outcome == "success" and all_lanes_skipped
                   and governance == "success" and plan_result == "success" and identifiers_valid)
    status = "reused" if reuse_valid else full_status if mode == "full" else (
        "failure" if gate_outcome == "failure" or governance == "failure" or plan_result == "failure"
        else "unknown")
    if "assertion_or_verification_failure" in classifications:
        failure_classification = "business_regression"
    elif "environment_setup_failure" in classifications:
        failure_classification = "environment_failure"
    elif governance == "failure":
        failure_classification = "governance_failure"
    elif plan_result != "success":
        failure_classification = "planning_failure_or_incomplete"
    elif (gate_outcome != "success" or mode == "full" and not all_lanes_pass
          or mode == "verified" and not all_lanes_skipped):
        failure_classification = "verification_failure_or_incomplete"
    elif status in {"success", "reused"}:
        failure_classification = "none"
    else:
        failure_classification = "unknown_evidence"
    return {
        "schema": 1,
        "kind": "main_full_regression",
        "repository": repo,
        "workflow_path": WORKFLOW,
        "event": event,
        "mode": mode,
        "force_full": force_full,
        "branch": "main" if ref == "refs/heads/main" else ref,
        "required_check": "check",
        "run_id": int(run_id) if str(run_id).isdigit() else run_id,
        "run_attempt": int(run_attempt) if str(run_attempt).isdigit() else run_attempt,
        "run_created_at": (run_metadata or {}).get("created_at"),
        "run_started_at": (run_metadata or {}).get("run_started_at"),
        "run_status": (run_metadata or {}).get("status", "unknown"),
        "run_conclusion": (run_metadata or {}).get("conclusion"),
        "required_check_status": "completed" if gate_outcome in {"success", "failure"} else "unknown",
        "required_check_conclusion": gate_outcome,
        "tested_head_sha": sha,
        "tested_tree": tree,
        "baseline_sha": baseline_sha,
        "policy_fingerprint": policy_fingerprint,
        "status": status,
        "classification": ("business_regression" if failure_classification == "business_regression"
                            else "environment_failure" if failure_classification == "environment_failure"
                            else "none" if status in {"success", "reused"} else "unknown"),
        "failure_classification": failure_classification,
        "gate_outcome": gate_outcome,
        "governance_result": governance,
        "plan_result": plan_result,
        "lanes": lane_states,
        "verified_source": verified_source or None,
        "reused_full_run_id": int(verified_run) if reuse_valid else None,
        "reused_full_run_attempt": int(verified_run_attempt) if reuse_valid and str(verified_run_attempt).isdigit() else None,
    }


def current_run_metadata(repo: str, run_id: str, run_attempt: str) -> dict:
    if not str(run_id).isdigit() or not str(run_attempt).isdigit():
        return {}
    try:
        record = api(f"repos/{repo}/actions/runs/{run_id}/attempts/{run_attempt}")
        if (not isinstance(record, dict) or record.get("id") != int(run_id)
                or record.get("run_attempt") != int(run_attempt)):
            return {}
        return {key: record.get(key) for key in
                ("id", "run_attempt", "created_at", "run_started_at", "status", "conclusion")}
    except Exception:
        return {}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("mode", choices=["plan", "gate", "select", "regression"])
    parser.add_argument("--base")
    parser.add_argument("--head", default="HEAD")
    parser.add_argument("--out", type=Path)
    parser.add_argument("--run-attempt", default=os.environ.get("GITHUB_RUN_ATTEMPT", ""))
    args = parser.parse_args()
    event, ref = os.environ["GITHUB_EVENT_NAME"], os.environ["GITHUB_REF"]
    repo, sha = os.environ["GITHUB_REPOSITORY"], os.environ["GITHUB_SHA"]
    if args.mode == "select":
        if not args.base:
            parser.error("select requires --base")
        print(json.dumps(trusted_impact_selection(args.base, args.head),
                         ensure_ascii=False, separators=(",", ":")))
        return
    if args.mode == "regression":
        try:
            needs = json.loads(os.environ.get("CI_NEEDS", "{}"))
        except json.JSONDecodeError:
            needs = {}
        run_id = os.environ.get("GITHUB_RUN_ID", "")
        run_attempt = os.environ.get("GITHUB_RUN_ATTEMPT", "")
        mode = needs.get("plan", {}).get("outputs", {}).get("mode", "unknown")
        metadata = current_run_metadata(repo, run_id, run_attempt)
        record = scheduled_regression_record(
            needs, event, ref, repo, sha, os.environ.get("GATE_OUTCOME", "unknown"),
            needs.get("plan", {}).get("outputs", {}).get("baseline_sha", ""),
            needs.get("plan", {}).get("outputs", {}).get("policy_fingerprint", ""),
            run_id, run_attempt, mode, os.environ.get("FORCE_FULL") == "true", metadata,
            needs.get("plan", {}).get("outputs", {}).get("verified_source", ""),
            needs.get("plan", {}).get("outputs", {}).get("verified_run"),
            needs.get("plan", {}).get("outputs", {}).get("verified_run_attempt"))
        out = args.out or Path("main-regression.json")
        out.parent.mkdir(parents=True, exist_ok=True)
        out.write_text(json.dumps(record, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        with open(os.environ["GITHUB_STEP_SUMMARY"], "a") as summary:
            summary.write(f"Main full regression evidence: **{record['status']}**; "
                          f"classification: `{record['classification']}`.\n")
        return 0 if record["status"] in {"success", "reused"} else 1
    if args.mode == "plan":
        try:
            payload = json.loads(Path(os.environ["GITHUB_EVENT_PATH"]).read_text())
        except (OSError, json.JSONDecodeError):
            payload = {}
        baseline = resolve_baseline(payload, event, ref, sha)
        decision = plan_decision(event, ref, repo, sha, os.environ.get("FORCE_FULL") == "true",
                                 baseline, os.environ.get("AICRM_AFFECTED_PLAN"),
                                 args.run_attempt)
        binding = decision.get("binding") or {}
        _write_plan_outputs(
            decision["full"], decision["mode"], decision.get("verified_run"),
            decision.get("verified_source", ""), decision.get("verified_run_attempt"),
            decision.get("conservative_full", False), baseline,
            binding.get("policy_fingerprint", ""),
            decision.get("daily_status", "unknown"))
        message = decision["reason"]
        if decision.get("verified_run") is not None:
            message = f"Reusing PR run {decision['verified_run']}: exact tree/base/policy proof validated"
        print(message)
        with open(os.environ["GITHUB_STEP_SUMMARY"], "a") as summary:
            summary.write(f"Verification plan: **{decision['mode']}**; reason: `{decision['reason']}`; "
                          f"main daily status: `{decision.get('daily_status', 'unknown')}`.\n")
    else:
        needs = json.loads(os.environ["CI_NEEDS"])
        full_text = needs.get("plan", {}).get("outputs", {}).get("full")
        if full_text not in {"true", "false"}:
            raise ValueError("missing verification mode")
        full = full_text == "true"
        require_results(needs, full, event, ref)
        mode = needs["plan"]["outputs"].get("mode", "full" if full else "verified")
        try:
            payload = json.loads(Path(os.environ["GITHUB_EVENT_PATH"]).read_text())
        except (OSError, json.JSONDecodeError):
            payload = {}
        outputs = needs["plan"].get("outputs", {})
        baseline = outputs.get("baseline_sha", "")
        if not HEX40.fullmatch(baseline):
            baseline = resolve_baseline(payload, event, ref, sha)
        try:
            baseline_tree = git(baseline + "^{tree}") if HEX40.fullmatch(baseline) else ""
            tested_sha, tested_tree = git("HEAD"), git("HEAD^{tree}")
        except subprocess.SubprocessError:
            baseline_tree, tested_sha, tested_tree = "", "", ""
        try:
            selected_lanes = json.loads(outputs.get("lanes", "[]"))
            selected_checks = json.loads(outputs.get("checks", "[]"))
        except (TypeError, json.JSONDecodeError):
            selected_lanes, selected_checks = [], []
        proof = {
            "schema": 3, "mode": mode, "event": event, "repository": repo,
            "run_id": int(os.environ["GITHUB_RUN_ID"]),
            "run_attempt": int(os.environ["GITHUB_RUN_ATTEMPT"]),
            "pr": payload.get("number"),
            "head": payload.get("pull_request", {}).get("head", {}).get("sha"),
            "baseline_sha": baseline, "baseline_tree": baseline_tree,
            "policy_fingerprint": outputs.get("policy_fingerprint", ""),
            "selection": {"mode": mode, "selected_lanes": selected_lanes,
                          "selected_checks": selected_checks,
                          "profile": outputs.get("profile", "none")},
            "verified_source": outputs.get("verified_source") or None,
            "verified_run": int(outputs["verified_run"]) if str(outputs.get("verified_run", "")).isdigit() else None,
            "verified_run_attempt": int(outputs["verified_run_attempt"])
            if str(outputs.get("verified_run_attempt", "")).isdigit() else None,
            "tested_sha": tested_sha, "tree": tested_tree,
            "phases": {name: needs[name].get("result", "unknown") for name in PHASES},
        }
        Path("verification.json").write_text(json.dumps(proof, indent=2) + "\n")
        print({"full": "All required verification passed",
               "targeted": "Selected PR verification lanes passed",
               "light": "Local-first PR consistency gate passed",
               "verified": "Identical merged tree: PR verification reused"}[mode])


if __name__ == "__main__":
    main()
