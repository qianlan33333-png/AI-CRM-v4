"""Resume an exact unevaluated environment failure in the existing batch."""
from contextlib import contextmanager
import copy
import hashlib
import json
from pathlib import Path
import subprocess
import sys
import unittest
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parent))
import domestic_main_release as release
from test_invoice_release_registration import InvoicePreinstallBuildRepairTests, commit


class InvoiceEnvironmentRetryTests(unittest.TestCase):
    @contextmanager
    def fixture(self):
        with InvoicePreinstallBuildRepairTests().fixture() as data:
            root, repo, config, state, front, staged, repair, ref, marker, uid = data
            source = root / "source"
            subprocess.run(["git", "-C", str(source), "checkout", "-q", staged], check=True)
            (source / "logo.txt").write_text("runtime logo candidate\n")
            pending = commit(source, "logo candidate")
            pending_ref = "refs/heads/codex/logo"
            subprocess.run(["git", f"--git-dir={repo}", "fetch", "-q", str(source), f"{pending}:{pending_ref}"], check=True)
            front.update(candidate_id=pending, head_sha=pending, base_sha=staged,
                         ref=pending_ref, status="pending", failure={"phase": "checks",
                         "kind": "environment", "candidate_verdict": "not_evaluated",
                         "check_checkpoint": "/protected/checkpoint.json"})
            front.pop("check_receipt")
            state["queue"].insert(0, {"candidate_id": staged, "head_sha": staged,
                "base_sha": state["main"]["sha"], "ref": "refs/heads/codex/one", "status": "staged"})
            batch = state["batch"]
            batch.pop("mode")
            batch["installed_app"] = {"sha": staged, "tree": batch["head_tree"], "manifest_sha256": "d" * 64}
            batch["members"][0].pop("deferred_install")
            marker["controller_files"] = ["scripts/domestic_main_release.py"]
            classification = {"runtime_changed": False, "full_build": False,
                "controller_files": marker["controller_files"], "changed_paths": [*marker["controller_files"], "docs/release-checks.json"]}
            with mock.patch.object(release, "_classify_candidate", return_value=classification):
                yield root, repo, config, state, front, staged, pending, pending_ref, repair, ref, uid

    def test_exact_environment_retry_preserves_attempt_checkpoint_and_batch(self):
        with self.fixture() as (root, repo, _, state, front, staged, pending, pending_ref, *_):
            before, batch = copy.deepcopy(front), copy.deepcopy(state["batch"])
            result = release.submit_candidate(repo, root / "state.json", pending_ref, pending, staged, root / "lock")
            self.assertEqual(result["queue_position"], 1)
            self.assertEqual(state["status"], "ready")
            self.assertEqual(front["attempt"], before["attempt"])
            self.assertEqual(front["attempt_history"], before["attempt_history"])
            self.assertEqual(front["failure"], before["failure"])
            self.assertEqual(state["batch"], batch)
            release._validate_state(json.loads((root / "state.json").read_text()))

    def test_environment_retry_rejects_other_candidate_and_install_or_verdict_change(self):
        with self.fixture() as (root, repo, _, state, front, staged, pending, pending_ref, repair, ref, _):
            original = copy.deepcopy(state)
            mutations = [lambda s: s["queue"][-1]["failure"].update(phase="stage-install"),
                lambda s: s["queue"][-1]["failure"].update(candidate_verdict="failed"),
                lambda s: s["queue"][-1]["failure"].update(required_recovery="ack-stage-reset"),
                lambda s: s.update(in_flight={"phase": "stage-install"}),
                lambda s: s.update(status="outcome_unknown", in_flight={"phase": "production-install"}),
                lambda s: s["batch"].update(status="sealed")]
            for mutate in mutations:
                state.clear(); state.update(copy.deepcopy(original)); mutate(state)
                before = copy.deepcopy(state)
                with mock.patch.object(release, "_recover_orphaned_inflight"), self.assertRaises(release.ReleaseError):
                    release.submit_candidate(repo, root / "state.json", pending_ref, pending, staged, root / "lock")
                self.assertEqual(state, before)
            state.clear(); state.update(copy.deepcopy(original))
            with self.assertRaises(release.ReleaseError):
                release.submit_candidate(repo, root / "state.json", ref, repair, staged, root / "lock")

    def test_checked_environment_repair_keeps_original_receipts_and_allows_rebased_front(self):
        with self.fixture() as (root, repo, config, state, front, staged, pending, pending_ref, repair, ref, uid):
            before = copy.deepcopy(front)
            with mock.patch.object(release.os, "geteuid", side_effect=uid):
                result = release.maintenance_check(config, repair)
            self.assertEqual(result["status"], "maintenance_checks_passed")
            self.assertEqual(result["base_sha"], staged)
            local_uid = release.os.geteuid(); first = [True]
            def repair_uid():
                if first[0]: first[0] = False; return 0
                return local_uid
            with mock.patch.object(release.os, "geteuid", side_effect=repair_uid), mock.patch.object(release, "_check_report", side_effect=AssertionError("reuse exact maintenance receipt")):
                result = release.batch_tool_repair(config, pending, repair, ref)
            self.assertFalse(result["production_written"])
            self.assertEqual(front["status"], "stale_base")
            for field in ("head_sha", "ref", "attempt", "attempt_history", "failure"):
                self.assertEqual(front[field], before[field])
            self.assertEqual(state["batch"]["installed_app"]["sha"], staged)
            self.assertEqual(state["batch"]["head_sha"], repair)
            self.assertNotIn("tool_repair_maintenance", state["batch"])
            # Rebase the original runtime delta through its same ref, keeping queue order.
            source = root / "source"
            subprocess.run(["git", "-C", str(source), "checkout", "-q", repair], check=True)
            (source / "logo.txt").write_text("runtime logo candidate\n")
            rebased = commit(source, "rebased logo")
            subprocess.run(["git", f"--git-dir={repo}", "fetch", "-q", str(source), f"+{rebased}:{pending_ref}"], check=True)
            result = release.submit_candidate(repo, root / "state.json", pending_ref, rebased, repair, root / "lock")
            self.assertEqual(result["queue_position"], 1)
            self.assertEqual(state["queue"][-1]["head_sha"], rebased)
            self.assertEqual(state["queue"][-1]["attempt_history"], before["attempt_history"])
            release._validate_state(json.loads((root / "state.json").read_text()))

    def test_environment_maintenance_rejects_changed_hosts_and_runtime_repair(self):
        with self.fixture() as (_, repo, config, state, _, _, pending, _, repair, ref, uid):
            before = copy.deepcopy(state)
            with mock.patch.object(release, "_verify_stage_app", side_effect=release.ReleaseError("install unknown")):
                with self.assertRaises(release.ReleaseError):
                    release._verify_batch_environment_repair_state(config, repo, state)
            self.assertEqual(state, before)
            with mock.patch.object(release, "_classify_candidate", return_value={"runtime_changed": True, "controller_files": ["scripts/domestic_main_release.py"]}):
                with self.assertRaisesRegex(release.ReleaseError, "source-only"):
                    release._checked_batch_controller_member(config, repo, state["batch"], repair, ref, "test", environment_repair=True)
            with self.assertRaises(release.ControllerMaintenanceRequired):
                release._checked_batch_controller_member(config, repo, state["batch"], repair, ref, "test", environment_repair=True)


if __name__ == "__main__":
    unittest.main()
