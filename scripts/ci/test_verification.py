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
        self.run = dict(id=10, run_attempt=2, event="pull_request", status="completed",
                        conclusion="success", head_sha=self.head, path=policy.WORKFLOW,
                        head_repository={"full_name": self.repo})
        self.pr = {"number": 1, "head": {"sha": self.head}, "merged_at": "2026-09-08",
                   "merge_commit_sha": self.sha, "base": {"ref": "main", "repo": {"full_name": self.repo}}}
        self.commit = {"sha": self.sha, "commit": {"tree": {"sha": self.tree}},
                       "parents": [{"sha": "d" * 40}, {"sha": self.head}]}
        self.proof = dict(schema=1, mode="full", repository=self.repo, event="pull_request",
                          run_id=10, run_attempt=2, pr=1, head=self.head, tested_sha=self.sha,
                          tree=self.tree, phases={name: "success" for name in policy.PHASES})

    def valid(self, proof=None, commit=None, tree=None):
        return policy.valid_proof(proof or self.proof, self.run, self.pr,
                                  commit or self.commit, tree or self.tree, self.repo)

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
                           ("event", "push"), ("pr", 2), ("tree", "f" * 40)]:
            with self.subTest(key=key):
                proof = dict(self.proof, **{key: value})
                self.assertFalse(self.valid(proof=proof))
        for result in ["skipped", "failure", "cancelled"]:
            proof = copy.deepcopy(self.proof)
            proof["phases"]["browser"] = result
            self.assertFalse(self.valid(proof=proof))

    def test_foreign_or_non_pr_runs_are_rejected(self):
        self.assertTrue(policy.eligible_run(self.run, self.repo, self.head))
        for field, value in [("event", "push"), ("conclusion", "failure"), ("status", "in_progress"),
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
                return {"jobs": [{"name": name, "conclusion": self.job_result}
                                 for name in (*policy.PHASES, "check")]}
            self.fail(path)
        with patch.object(policy, "api", side_effect=response):
            self.job_result = "success"
            self.assertEqual(policy.find_verified_run(self.repo, self.sha, self.tree), 10)
            self.job_result = "failure"
            self.assertIsNone(policy.find_verified_run(self.repo, self.sha, self.tree))
            self.job_result = "success"
            self.assertIsNone(policy.find_verified_run(self.repo, self.sha, "f" * 40))

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
        needs["plan"]["outputs"]["verified_run"] = "10"
        policy.require_results(needs, False, "push", "refs/heads/main")
        with self.assertRaises(ValueError):
            policy.require_results(needs, False, "pull_request", "refs/pull/1/merge")

    def test_targeted_gate_rejects_failure_cancel_and_unplanned_success(self):
        needs = {name: {"result": "skipped"} for name in policy.PHASES}
        needs["preflight"]["result"] = "success"
        needs["plan"] = {"result": "success", "outputs": {"mode": "targeted", "lanes": '["preflight"]'}}
        policy.require_results(needs, False, "pull_request", "refs/pull/1/merge")
        for result in ["failure", "cancelled", "success", None]:
            needs["browser"]["result"] = result
            with self.assertRaises(ValueError):
                policy.require_results(needs, False, "pull_request", "refs/pull/1/merge")
        needs["browser"]["result"] = "skipped"
        with self.assertRaises(ValueError):
            policy.require_results(needs, False, "push", "refs/heads/main")

    def test_test_fixtures_and_high_risk_paths_require_full_pr_ci(self):
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
            ("web/v3/productAdapter.ts\n", True),
        )
        for changed, expected in cases:
            with self.subTest(changed=changed), patch.dict("os.environ", {"GITHUB_BASE_SHA": "a" * 40}):
                with patch.object(policy.subprocess, "check_output", return_value=changed):
                    self.assertEqual(policy.requires_full_pr_verification(), expected)

    def test_light_pr_gate_accepts_only_skipped_long_lanes(self):
        needs = {name: {"result": "skipped"} for name in policy.PHASES}
        needs["plan"] = {"result": "success", "outputs": {"mode": "light", "lanes": "[]"}}
        policy.require_results(needs, False, "pull_request", "refs/pull/1/merge")
        needs["backend"]["result"] = "success"
        with self.assertRaises(ValueError):
            policy.require_results(needs, False, "pull_request", "refs/pull/1/merge")

    def test_delivery_infrastructure_changes_require_full_pr_verification(self):
        base = "d" * 40
        with patch.dict("os.environ", {"GITHUB_BASE_SHA": base}, clear=False), patch.object(
            policy.subprocess, "check_output", return_value=".github/workflows/ci.yml\n"
        ):
            self.assertTrue(policy.requires_full_pr_verification())

    def test_release_consistency_gate_accepts_main_without_long_lanes(self):
        needs = {name: {"result": "skipped"} for name in policy.PHASES}
        needs["plan"] = {"result": "success", "outputs": {"mode": "release", "lanes": "[]"}}
        policy.require_results(needs, False, "push", "refs/heads/main")
        with self.assertRaises(ValueError):
            policy.require_results(needs, False, "pull_request", "refs/pull/1/merge")

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
