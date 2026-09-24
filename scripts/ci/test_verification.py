import copy
import io
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch
import zipfile

import verification as policy
import verify_deployment


class VerificationTests(unittest.TestCase):
    def setUp(self):
        self.repo = "owner/repo"
        self.head, self.sha, self.tree = "a" * 40, "b" * 40, "c" * 40
        self.base, self.base_tree, self.policy_fingerprint = "d" * 40, "e" * 40, "f" * 64
        self.run = dict(id=10, run_attempt=2, event="pull_request", status="completed",
                        conclusion="success", head_sha=self.head, path=policy.WORKFLOW,
                        head_repository={"full_name": self.repo})
        self.pr = {"number": 1, "head": {"sha": self.head}, "merged_at": "2026-09-08",
                   "merge_commit_sha": self.sha, "base": {"ref": "main", "repo": {"full_name": self.repo}}}
        self.commit = {"sha": self.sha, "commit": {"tree": {"sha": self.tree}},
                       "parents": [{"sha": self.base}, {"sha": self.head}]}
        self.selection = {"mode": "full", "selected_lanes": list(policy.PHASES),
                          "selected_checks": [], "profile": "full"}
        self.proof = dict(schema=3, mode="full", repository=self.repo, event="pull_request",
                          run_id=10, run_attempt=2, pr=1, head=self.head, tested_sha=self.sha,
                          baseline_sha=self.base, baseline_tree=self.base_tree,
                          policy_fingerprint=self.policy_fingerprint, selection=self.selection,
                          tree=self.tree, phases={name: "success" for name in policy.PHASES})

    def valid(self, proof=None, commit=None, tree=None, selection=None):
        return policy.valid_proof(proof or self.proof, self.run, self.pr,
                                  commit or self.commit, tree or self.tree, self.repo,
                                  self.base, self.base_tree, self.policy_fingerprint,
                                  selection or self.selection)

    def test_identical_tree_can_reuse_squash_merge_with_different_commit_id(self):
        self.pr["merge_commit_sha"] = "e" * 40
        self.assertTrue(self.valid())

    def test_drift_or_unrelated_merge_is_rejected(self):
        self.assertFalse(self.valid(tree="f" * 40))
        commit = copy.deepcopy(self.commit)
        commit["parents"][1]["sha"] = "f" * 40
        self.assertFalse(self.valid(commit=commit))

    def test_partial_stale_or_wrong_source_evidence_is_rejected(self):
        for key, value in [("mode", "reused"), ("schema", 0), ("repository", "other/repo"),
                           ("run_id", 11), ("run_attempt", 1), ("head", "f" * 40),
                           ("event", "push"), ("pr", 2), ("tree", "f" * 40),
                           ("baseline_sha", "1" * 40), ("baseline_tree", "2" * 40),
                           ("policy_fingerprint", "0" * 64), ("selection", {"mode": "light"})]:
            with self.subTest(key=key):
                proof = dict(self.proof, **{key: value})
                self.assertFalse(self.valid(proof=proof))
        for result in ["skipped", "failure", "cancelled"]:
            proof = copy.deepcopy(self.proof)
            proof["phases"]["browser"] = result
            self.assertFalse(self.valid(proof=proof))

    def test_foreign_or_non_pr_runs_are_rejected(self):
        self.assertTrue(policy.eligible_run(self.run, self.repo, self.head))
        report_failure = dict(self.run, conclusion="failure")
        self.assertTrue(policy.eligible_run(report_failure, self.repo, self.head))
        for field, value in [("event", "push"), ("status", "in_progress"),
                             ("path", ".github/workflows/other.yml"), ("head_repository", None)]:
            self.assertFalse(policy.eligible_run(dict(self.run, **{field: value}), self.repo, self.head))

    def archive(self, name="verification.json", content=None):
        data = io.BytesIO()
        with zipfile.ZipFile(data, "w") as archive:
            archive.writestr(name, json.dumps(self.proof) if content is None else content)
        return data.getvalue()

    def test_evidence_zip_is_read_without_extracting_paths_or_executables(self):
        self.assertEqual(policy.read_proof(self.archive()), self.proof)
        for name, content in [("../verification.json", "{}"), ("run.sh", "exit 0"),
                              ("verification.json", "x" * 17000)]:
            with self.assertRaises(ValueError):
                policy.read_proof(self.archive(name, content))

    def test_api_lookup_requires_successful_jobs_from_same_attempt(self):
        def response(path, raw=False):
            if path.endswith("/commits/" + self.sha):
                return self.commit
            if "/pulls?" in path:
                return [self.pr]
            if "/workflows/" in path:
                return {"workflow_runs": [self.run]}
            if "/artifacts?" in path:
                return {"artifacts": [{"name": "ci-verification-10-2", "expired": False,
                                       "size_in_bytes": 1000, "id": 3}]}
            if path.endswith("/zip"):
                return self.archive()
            if "/commits/" in path:
                return self.commit
            if "/attempts/2/jobs?" in path:
                return {"jobs": [{"name": name,
                                   "conclusion": getattr(self, "job_results", {}).get(name, self.job_result)}
                                 for name in (*policy.PHASES, "governance", "check")]}
            self.fail(path)
        with patch.object(policy, "api", side_effect=response):
            self.job_result = "success"
            self.assertEqual(policy.find_verified_run(self.repo, self.sha, self.tree,
                                                      self.base, self.base_tree,
                                                      self.policy_fingerprint,
                                                      self.selection), 10)
            self.job_result = "failure"
            self.assertIsNone(policy.find_verified_run(self.repo, self.sha, self.tree,
                                                        self.base, self.base_tree,
                                                        self.policy_fingerprint,
                                                        self.selection))
            self.job_result = "success"
            self.assertIsNone(policy.find_verified_run(self.repo, self.sha, "1" * 40,
                                                       self.base, self.base_tree,
                                                       self.policy_fingerprint,
                                                       self.selection))

    def test_targeted_and_light_proof_reuse_uses_only_exact_successful_jobs(self):
        targeted = {"mode": "targeted", "selected_lanes": ["preflight", "backend"],
                    "selected_checks": [{"lane": "backend", "path": "internal/x/x_test.go", "test": "TestX"}],
                    "profile": "affected"}
        light = {"mode": "light", "selected_lanes": [], "selected_checks": [], "profile": "none"}

        def response(path, raw=False):
            if path.endswith("/commits/" + self.sha):
                return self.commit
            if "/pulls?" in path:
                return [self.pr]
            if "/workflows/" in path:
                return {"workflow_runs": [self.run]}
            if "/artifacts?" in path:
                return {"artifacts": [{"name": "ci-verification-10-2", "expired": False,
                                       "size_in_bytes": 1000, "id": 3}]}
            if path.endswith("/zip"):
                return self.archive()
            if "/attempts/2/jobs?" in path:
                return {"jobs": [{"name": name, "conclusion": self.job_results.get(name)}
                                 for name in (*policy.PHASES, "governance", "check")]}
            self.fail(path)

        with patch.object(policy, "api", side_effect=response):
            for selection in (targeted, light):
                with self.subTest(mode=selection["mode"]):
                    self.run["conclusion"] = "failure"  # Optional report jobs may fail.
                    self.selection = selection
                    self.proof = copy.deepcopy(self.proof)
                    self.proof.update(mode=selection["mode"], selection=selection,
                                      phases={name: ("success" if name in selection["selected_lanes"]
                                                     else "skipped") for name in policy.PHASES})
                    self.job_results = {name: ("success" if name in selection["selected_lanes"]
                                               else "skipped") for name in policy.PHASES}
                    self.job_results.update(governance="success", check="success")
                    self.assertEqual(policy.find_verified_run(
                        self.repo, self.sha, self.tree, self.base, self.base_tree,
                        self.policy_fingerprint, selection), 10)
                    self.job_results["governance"] = "failure"
                    self.assertIsNone(policy.find_verified_run(
                        self.repo, self.sha, self.tree, self.base, self.base_tree,
                        self.policy_fingerprint, selection))
                    self.job_results["governance"] = "success"
                    self.job_results["check"] = "failure"
                    self.assertIsNone(policy.find_verified_run(
                        self.repo, self.sha, self.tree, self.base, self.base_tree,
                        self.policy_fingerprint, selection))

    def test_invalid_proof_shapes_fail_closed(self):
        for selection in (
                {"mode": "targeted", "selected_lanes": [{"bad": "lane"}],
                 "selected_checks": [], "profile": "affected"},
                {"mode": "full", "selected_lanes": list(policy.PHASES[:-1]),
                 "selected_checks": [], "profile": "full"},
                {"mode": "light", "selected_lanes": [], "selected_checks": ["unexpected"],
                 "profile": "none"}):
            with self.subTest(selection=selection):
                self.assertFalse(policy.valid_proof(
                    self.proof, self.run, self.pr, self.commit, self.tree, self.repo,
                    self.base, self.base_tree, self.policy_fingerprint, selection))

    def test_daily_status_excludes_current_attempt_and_bootstraps_without_old_schedule(self):
        current = {"id": 90, "run_attempt": 1, "event": "schedule", "path": policy.WORKFLOW,
                   "head_branch": "main", "head_sha": self.sha, "status": "in_progress",
                   "conclusion": None, "run_started_at": "2026-09-24T03:17:00Z"}

        def response(path, raw=False):
            for event in ("schedule", "push", "workflow_dispatch"):
                if f"event={event}" in path:
                    return {"workflow_runs": [current] if event == "schedule" else []}
            self.fail(path)

        with patch.object(policy, "api", side_effect=response), \
             patch.object(policy.subprocess, "check_output", return_value=self.sha + "\n"):
            self.assertEqual(policy.daily_regression_status(self.repo, self.sha, 90, 1), "no_history")
            self.assertEqual(policy.daily_regression_status(self.repo, self.sha), "in_progress")

    def test_legacy_failed_push_without_regression_receipt_is_not_daily_history(self):
        old_push = {"id": 89, "run_attempt": 1, "event": "push", "path": policy.WORKFLOW,
                    "head_branch": "main", "head_sha": self.sha, "status": "completed",
                    "conclusion": "failure", "created_at": "2026-09-01T00:00:00Z"}

        def response(path, raw=False):
            for event in ("schedule", "push", "workflow_dispatch"):
                if f"event={event}" in path:
                    return {"workflow_runs": [old_push] if event == "push" else []}
            self.fail(path)

        with patch.object(policy, "api", side_effect=response), \
             patch.object(policy, "_regression_artifact", return_value=None), \
             patch.object(policy.subprocess, "check_output", return_value=self.sha + "\n"):
            self.assertEqual(policy.daily_regression_status(self.repo, self.sha), "no_history")

    def test_schedule_reuse_persists_and_same_sha_failure_forces_another_full_run(self):
        history = []
        records = {}
        outcomes = {}

        def make_run(run_id, event, status, minute):
            return {"id": run_id, "run_attempt": 1, "event": event, "path": policy.WORKFLOW,
                    "head_branch": "main", "head_sha": self.sha, "status": status,
                    "conclusion": "success" if status == "completed" else None,
                    "created_at": f"2026-09-24T03:{minute:02d}:00Z",
                    "run_started_at": f"2026-09-24T03:{minute:02d}:00Z"}

        def api_response(path, raw=False):
            for event in ("schedule", "push", "workflow_dispatch"):
                if f"event={event}" in path:
                    return {"workflow_runs": [run for run in history if run["event"] == event]}
            self.fail(path)

        def run_schedule(run_id, history_outcomes):
            current = make_run(run_id, "schedule", "in_progress", run_id - 100)
            history.append(current)  # Actions exposes the active attempt during planning.
            outcomes.update(history_outcomes)
            with patch.dict("os.environ", {"GITHUB_RUN_ID": str(run_id), "GITHUB_RUN_ATTEMPT": "1"}), \
                 patch.object(policy, "git", side_effect=lambda ref: self.tree if ref == "HEAD^{tree}" else self.base_tree), \
                 patch.object(policy, "affected_plan_binding", return_value={
                     "baseline_sha": self.base, "baseline_tree": self.base_tree,
                     "head_sha": self.sha, "head_tree": self.tree,
                     "policy_fingerprint": self.policy_fingerprint, "source_clean": True}), \
                 patch.object(policy, "api", side_effect=api_response), \
                 patch.object(policy.subprocess, "check_output", return_value=self.sha + "\n"), \
                 patch.object(policy, "_regression_artifact", side_effect=lambda _repo, run: records.get(run["id"])), \
                 patch.object(policy, "_validate_full_regression",
                              side_effect=lambda _repo, run, _record: outcomes[run["id"]]), \
                 patch.object(policy, "find_full_regression_proof",
                              side_effect=lambda *_args: next((
                                  {"run_id": run["id"], "run_attempt": 1}
                                  for run in reversed(history)
                                  if run["status"] == "completed"
                                  and records.get(run["id"], {}).get("mode") == "full"
                                  and outcomes.get(run["id"]) == "success"), None)):
                return policy.plan_decision("schedule", "refs/heads/main", self.repo,
                                            self.sha, False, self.base)

        # First schedule bootstraps with a full run even though its own active
        # run is already present in the workflow-runs API response.
        first = run_schedule(101, {})
        self.assertEqual((first["mode"], first["daily_status"]), ("full", "no_history"))

        history[0]["status"] = "completed"
        records[101] = {"mode": "full"}
        outcomes[101] = "success"
        second = run_schedule(102, {})
        self.assertEqual((second["mode"], second["verified_run"]), ("verified", 101))

        history[1]["status"] = "completed"
        records[102] = {"mode": "verified"}
        outcomes[102] = "reused"
        third = run_schedule(103, {})
        self.assertEqual((third["mode"], third["verified_run"]), ("verified", 101))
        history[2]["status"] = "completed"
        records[103] = {"mode": "verified"}
        outcomes[103] = "reused"

        # A later failed explicit full attempt on the same tree is not erased
        # by the previous success or any intervening verified-reference run.
        failed = make_run(104, "workflow_dispatch", "completed", 4)
        failed["name"] = "ci [force_full]"
        history.append(failed)
        records[104] = {"mode": "full"}
        outcomes[104] = "failure"
        retry = run_schedule(105, {})
        self.assertEqual((retry["mode"], retry["daily_status"]), ("full", "failure"))

        # A new exact full pass clears that failure, allowing unchanged future
        # schedules to refer to the successful run without another full test.
        history[4]["status"] = "completed"
        records[105] = {"mode": "full"}
        outcomes[105] = "success"
        recovered = make_run(106, "workflow_dispatch", "completed", 6)
        recovered["name"] = "ci [force_full]"
        history.append(recovered)
        records[106] = {"mode": "full"}
        outcomes[106] = "success"
        last = run_schedule(107, {})
        self.assertEqual((last["mode"], last["verified_run"]), ("verified", 106))

    def test_main_push_and_schedule_reruns_force_fresh_full_lanes(self):
        binding = {"baseline_sha": self.base, "baseline_tree": self.base_tree,
                   "head_sha": self.sha, "head_tree": self.tree,
                   "policy_fingerprint": self.policy_fingerprint, "source_clean": True}
        with patch.object(policy, "git", side_effect=lambda ref: self.tree if ref == "HEAD^{tree}" else self.base_tree), \
             patch.object(policy, "affected_plan_binding", return_value=binding), \
             patch.object(policy, "daily_regression_status", return_value="success") as daily_status, \
             patch.object(policy, "find_full_regression_proof", return_value={"run_id": 101}), \
             patch.object(policy, "find_verified_run", return_value=101):
            for event in ("push", "schedule"):
                with self.subTest(event=event):
                    decision = policy.plan_decision(
                        event, "refs/heads/main", self.repo, self.sha, False,
                        self.base, run_attempt="2")
                    self.assertEqual(decision["mode"], "full")
                    self.assertTrue(decision["full"])
                    self.assertTrue(decision["conservative_full"])
                    self.assertEqual(decision["reason"], "main-rerun-forces-full")
                    self.assertIsNone(decision["verified_run"])
            daily_status.assert_not_called()

    def test_full_proof_ignores_non_gate_workflow_conclusion_when_required_jobs_pass(self):
        run = {"id": 55, "run_attempt": 2, "event": "schedule", "status": "completed",
               "conclusion": "failure"}
        record = {"mode": "full", "status": "success", "classification": "none",
                  "lanes": {name: {"result": "success", "classification": "none"}
                            for name in policy.PHASES},
                  "gate_outcome": "success", "governance_result": "success", "plan_result": "success",
                  "required_check_status": "completed", "required_check_conclusion": "success"}
        with patch.object(policy, "_record_binding", return_value=True), \
             patch.object(policy, "_jobs_pass", return_value=True):
            self.assertEqual(policy._validate_full_regression(self.repo, run, record), "success")

    def test_targeted_and_light_proofs_reuse_only_exact_selection_and_lanes(self):
        targeted = {"mode": "targeted", "selected_lanes": ["preflight", "backend"],
                    "selected_checks": [{"lane": "backend", "path": "internal/x/x_test.go", "test": "TestX"}],
                    "profile": "affected"}
        expected_targeted = copy.deepcopy(targeted)
        proof = copy.deepcopy(self.proof)
        proof.update(mode="targeted", selection=targeted, shadow_eligible=False,
                     phases={name: ("success" if name in targeted["selected_lanes"] else "skipped")
                             for name in policy.PHASES})
        self.assertTrue(self.valid(proof=proof, selection=targeted))
        proof["selection"]["selected_checks"] = []
        self.assertFalse(self.valid(proof=proof, selection=expected_targeted))
        proof = copy.deepcopy(self.proof)
        light = {"mode": "light", "selected_lanes": [], "selected_checks": [], "profile": "none"}
        proof.update(mode="light", selection=light,
                     phases={name: "skipped" for name in policy.PHASES})
        self.assertTrue(self.valid(proof=proof, selection=light))
        proof["phases"]["backend"] = "success"
        self.assertFalse(self.valid(proof=proof, selection=light))

    def test_shadow_ineligible_policy_change_does_not_invalidate_exact_actual_proof(self):
        proof = copy.deepcopy(self.proof)
        proof["shadow_eligible"] = False
        self.assertTrue(self.valid(proof=proof))
        plan = {"schema": 1, "observed_mode": "shadow", "source_clean": True,
                "baseline_sha": self.base, "baseline_tree": self.base_tree,
                "head_sha": self.sha, "head_tree": self.tree,
                "policy_fingerprint": self.policy_fingerprint,
                "source": {"working_tree_clean": True, "head_matches": True},
                "evidence_eligible": False}
        with tempfile.TemporaryDirectory() as directory, \
             patch.object(policy, "exact_source_binding", return_value={
                 "baseline_sha": self.base, "baseline_tree": self.base_tree,
                 "head_sha": self.sha, "head_tree": self.tree,
                 "policy_fingerprint": self.policy_fingerprint, "source_clean": True}):
            path = Path(directory) / "plan.json"
            path.write_text(json.dumps(plan))
            binding = policy.affected_plan_binding(str(path), self.base, self.sha, self.tree)
        self.assertIsNotNone(binding)
        self.assertFalse(binding["shadow_eligible"])

    def test_aggregate_never_allows_pr_skips_or_missing_main_proof(self):
        needs = {name: {"result": "success"} for name in policy.PHASES}
        needs["plan"] = {"result": "success", "outputs": {}}
        policy.require_results(needs, True, "pull_request", "refs/pull/1/merge")
        needs["browser"]["result"] = "skipped"
        with self.assertRaises(ValueError):
            policy.require_results(needs, True, "pull_request", "refs/pull/1/merge")
        for name in policy.PHASES:
            needs[name]["result"] = "skipped"
        with self.assertRaises(ValueError):
            policy.require_results(needs, False, "push", "refs/heads/main")
        needs["plan"]["outputs"].update(mode="verified", verified_run="10",
                                         verified_source="pull_request", lanes="[]")
        policy.require_results(needs, False, "push", "refs/heads/main")
        with self.assertRaises(ValueError):
            policy.require_results(needs, False, "pull_request", "refs/pull/1/merge")

    def test_targeted_gate_rejects_failure_cancel_and_unplanned_success(self):
        needs = {name: {"result": "skipped"} for name in policy.PHASES}
        needs["preflight"]["result"] = "success"
        needs["plan"] = {"result": "success", "outputs": {
            "mode": "targeted", "lanes": '["preflight"]', "checks": "[]", "profile": "affected",
            "baseline_sha": self.base}}
        expected = {"mode": "targeted", "selected_lanes": ["preflight"],
                    "selected_checks": [], "profile": "affected"}
        with patch.object(policy, "expected_enforced_selection", return_value=expected):
            policy.require_results(needs, False, "pull_request", "refs/pull/1/merge")
            for result in ["failure", "cancelled", "success", None]:
                needs["browser"]["result"] = result
                with self.assertRaises(ValueError):
                    policy.require_results(needs, False, "pull_request", "refs/pull/1/merge")
            needs["browser"]["result"] = "skipped"
            with self.assertRaises(ValueError):
                policy.require_results(needs, False, "push", "refs/heads/main")

    def test_code_and_unknown_paths_require_impact_selection(self):
        cases = (
            ("scripts/test-install-release-ordering.sh\n", True),
            ("cmd/aicrm/core_operations_chromium_journey.mjs\n", True),
            ("internal/payment/app/service_test.go\n", True),
            (".github/workflows/ci.yml\n", True),
            ("migrations/0205_config_alipay_runtime_setting_keys.sql\n", True),
            ("internal/platform/jobqueue/queue.go\n", True),
            ("scripts/release_control.py\n", True),
            ("scripts/release_events.py\n", True),
            ("docs/guide.md\n", False),
            ("new-root-file.txt\n", True),
            ("web/v3/productAdapter.ts\n", True),
        )
        for changed, expected in cases:
            with self.subTest(changed=changed), patch.dict("os.environ", {"GITHUB_BASE_SHA": "a" * 40}):
                with patch.object(policy.subprocess, "check_output", return_value=changed):
                    self.assertEqual(policy.requires_impact_selection(), expected)

    def test_missing_impact_base_fails_closed_to_impact_selection(self):
        with patch.dict("os.environ", {"GITHUB_BASE_SHA": ""}), patch.object(policy.subprocess, "check_output") as diff:
            self.assertTrue(policy.requires_impact_selection())
            diff.assert_not_called()

    def test_light_pr_gate_accepts_only_skipped_long_lanes(self):
        needs = {name: {"result": "skipped"} for name in policy.PHASES}
        needs["plan"] = {"result": "success", "outputs": {"mode": "light", "lanes": "[]",
                                                                 "checks": "[]", "profile": "none",
                                                                 "baseline_sha": self.base}}
        with patch.object(policy, "expected_enforced_selection", return_value={
                "mode": "light", "selected_lanes": [], "selected_checks": [], "profile": "none"}):
            policy.require_results(needs, False, "pull_request", "refs/pull/1/merge")
            needs["backend"]["result"] = "success"
            with self.assertRaises(ValueError):
                policy.require_results(needs, False, "pull_request", "refs/pull/1/merge")

    def test_delivery_infrastructure_changes_enter_full_risk_selection(self):
        base = "d" * 40
        with patch.dict("os.environ", {"GITHUB_BASE_SHA": base}, clear=False):
            for path in (".github/workflows/ci.yml", "scripts/release_queue.py",
                         "scripts/release_coordinator.py", "scripts/release_control.py",
                         "scripts/release_events.py", "scripts/release_handoff.py"):
                with patch.object(policy.subprocess, "check_output", return_value=path + "\n"):
                    self.assertTrue(policy.requires_impact_selection(), path)

    def test_release_metadata_pr_plan_runs_full_lanes(self):
        import sys
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "output"
            summary = Path(directory) / "summary"
            event = Path(directory) / "event.json"
            event.write_text(json.dumps({"pull_request": {"base": {"sha": "a" * 40}}}))
            env = {"GITHUB_EVENT_NAME": "pull_request", "GITHUB_REF": "refs/pull/17/merge",
                   "GITHUB_REPOSITORY": "owner/repo", "GITHUB_SHA": "b" * 40,
                   "GITHUB_BASE_SHA": "a" * 40, "GITHUB_OUTPUT": str(output),
                   "GITHUB_STEP_SUMMARY": str(summary), "GITHUB_EVENT_PATH": str(event),
                   "FORCE_FULL": "false"}
            with patch.dict("os.environ", env), patch.object(sys, "argv", ["verification.py", "plan"]), \
                 patch.object(policy, "git", return_value="c" * 40), \
                 patch.object(policy, "exact_source_binding", return_value=None), \
                 patch.object(policy, "daily_regression_status", return_value="no_history"), \
                 patch.object(policy, "requires_impact_selection", return_value=True):
                policy.main()
            self.assertIn("mode=full", output.read_text())
            self.assertIn("full=true", output.read_text())

    def test_obsolete_release_mode_cannot_skip_main_verification(self):
        needs = {name: {"result": "skipped"} for name in policy.PHASES}
        needs["plan"] = {"result": "success", "outputs": {"mode": "release", "lanes": "[]"}}
        with self.assertRaises(ValueError):
            policy.require_results(needs, False, "push", "refs/heads/main")

    def test_http_200_with_wrong_sha_or_not_ready_is_not_deployed(self):
        for body in [{"status": "ready", "release_sha": "f" * 40}, {"status": "not_ready", "release_sha": self.sha}]:
            with patch.object(verify_deployment, "urlopen", return_value=io.BytesIO(json.dumps(body).encode())):
                with self.assertRaises(RuntimeError):
                    verify_deployment.verify("https://fixture.invalid", self.sha, attempts=1)
        body = {"status": "ready", "release_sha": self.sha}
        with patch.object(verify_deployment, "urlopen", return_value=io.BytesIO(json.dumps(body).encode())):
            verify_deployment.verify("https://fixture.invalid", self.sha, attempts=1)

    def test_hxc_reuse_requires_all_effective_state_checks(self):
        script = (Path(__file__).resolve().parents[2] / "deploy/rollout-hxc-identity-v2.sh").read_text()
        function = script.split("verify_existing_rollout() {", 1)[1].split("\n}\n", 1)[0]
        with tempfile.TemporaryDirectory() as directory:
            config = Path(directory) / "runtime.env"
            for failed in ["", "disabled", "projection", "worker", "timer", "enabled", "sha", "http"]:
                with self.subTest(failed=failed):
                    config.write_text("AICRM_HXC_IDENTITY_WRITE_ENABLED=" + ("false" if failed == "disabled" else "true") + "\n")
                    harness = '''set -euo pipefail
runtime_env="$1"
failure="$2"
release_sha=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
verify_current_published_projection() { [[ "$failure" != projection ]]; }
systemctl() {
  [[ "$failure" != worker || "$*" != *effects-worker* ]] &&
  [[ "$failure" != timer || "$*" != *refresh.timer* ]] &&
  [[ "$failure" != enabled || "$1" != is-enabled ]]
}
curl() {
  [[ "$failure" != http ]] || return 1
  local sha="$release_sha"
  [[ "$failure" != sha ]] || sha=old
  printf '{"status":"ready","release_sha":"%s"}' "$sha"
}
verify_existing_rollout() {'''+function+'''
}
verify_existing_rollout
'''
                    result = subprocess.run(["bash", "-c", harness, "fixture", str(config), failed], capture_output=True)
                    self.assertEqual(result.returncode == 0, failed == "", result.stderr.decode())


if __name__ == "__main__":
    unittest.main()
