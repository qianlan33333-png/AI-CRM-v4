import importlib.util
import json
import tempfile
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("staging_build_gc", Path(__file__).with_name("staging-build-gc.py"))
gc = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gc)


class StagingBuildGCTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        (self.root / "builds").mkdir()
        (self.root / "releases").mkdir()
        self.active = "f" * 40
        (self.root / "releases" / self.active).mkdir()
        (self.root / "current").symlink_to(self.root / "releases" / self.active)
        self.sha = "a" * 40
        self.candidate = "c" * 24
        self.state = self.root / "state.json"
        self.queue = self.root / "release-queue.json"
        (self.root / "staging-build.lock").touch()
        (self.root / "release-queue.lock").touch()
        self.audit = self.root / "audit"
        self.directory = self.root / "builds" / self.sha
        self.directory.mkdir()
        (self.directory / ".git").mkdir()
        (self.directory / ".git" / "HEAD").write_text(self.sha + "\n")
        manifest = {"candidate_id": self.candidate, "merge_preview_sha": self.sha, "tree_sha": "b" * 40}
        (self.directory / "candidate-manifest.json").write_text(json.dumps(manifest))
        archive = self.directory / f"aicrm-{self.sha}.tar.gz"
        archive.write_bytes(b"package")
        receipt = {**manifest, "status": "built", "package_name": archive.name, "package_sha256": gc.digest(archive)}
        (self.directory / "staging-receipt.json").write_text(json.dumps(receipt))
        self.set_status("stale_candidate")

    def set_status(self, status):
        item = {"candidate_id": self.candidate, "merge_preview_sha": self.sha, "status": status,
                "events": [{"to": status, "time": 1}]}
        for path in (self.state, self.queue):
            path.write_text(json.dumps({"schema": 1, "items": [item]}))

    def args(self, mode="inventory", plan=None):
        return SimpleNamespace(root=self.root, state=self.state, queue=self.queue, audit=self.audit,
                               mode=mode, plan=plan, readyz="http://127.0.0.1:8080/readyz")

    def test_inventory_and_apply_preserve_proof_and_readback(self):
        plan = gc.run(self.args())
        self.assertEqual([entry["sha"] for entry in plan["eligible"]], [self.sha])
        plan_path = self.root / "plan.json"
        plan_path.write_text(json.dumps(plan))
        with patch.object(gc, "readyz", side_effect=[{"status": 200, "release_sha": self.active},
                                                    {"status": 200, "release_sha": self.active}]):
            result = gc.run(self.args("apply", plan_path))
        self.assertEqual(result["deleted"], [self.sha])
        self.assertFalse(self.directory.exists())
        receipt = json.loads(Path(result["audit"]).read_text())
        self.assertEqual(receipt["status"], "completed")
        self.assertEqual(receipt["plan"]["eligible"][0]["proof"]["package_sha256"], gc.hashlib.sha256(b"package").hexdigest())

    def test_nonterminal_or_missing_registration_protected(self):
        self.set_status("observing")
        self.assertEqual(gc.run(self.args())["eligible"], [])
        self.state.write_text(json.dumps({"schema": 1, "items": []}))
        self.assertEqual(gc.run(self.args())["eligible"], [])
        self.assertTrue(self.directory.exists())

    def test_installed_release_and_unknown_directory_protected(self):
        (self.root / "releases" / self.sha).mkdir()
        (self.root / "builds" / "mystery").mkdir()
        plan = gc.run(self.args())
        self.assertEqual(plan["eligible"], [])
        self.assertEqual(len(plan["protected"]), 2)

    def test_mismatched_source_or_symlink_protected(self):
        (self.directory / "candidate-manifest.json").write_text("{}")
        self.assertEqual(gc.run(self.args())["eligible"], [])
        self.setUp_source()
        (self.directory / "escape").symlink_to(self.state)
        self.assertEqual(gc.run(self.args())["eligible"], [])

    def setUp_source(self):
        manifest = {"candidate_id": self.candidate, "merge_preview_sha": self.sha, "tree_sha": "b" * 40}
        (self.directory / "candidate-manifest.json").write_text(json.dumps(manifest))

    def test_changed_plan_fails_without_delete(self):
        plan = gc.run(self.args())
        plan_path = self.root / "plan.json"
        plan_path.write_text(json.dumps(plan))
        self.set_status("waiting_merge")
        with self.assertRaisesRegex(gc.Refuse, "plan_stale"):
            gc.run(self.args("apply", plan_path))
        self.assertTrue(self.directory.exists())

    def test_health_failure_fails_before_delete(self):
        plan_path = self.root / "plan.json"
        plan_path.write_text(json.dumps(gc.run(self.args())))
        with patch.object(gc, "readyz", side_effect=gc.Refuse("readyz_failed")):
            with self.assertRaisesRegex(gc.Refuse, "readyz_failed"):
                gc.run(self.args("apply", plan_path))
        self.assertTrue(self.directory.exists())

    def test_readyz_current_mismatch_fails_before_delete(self):
        plan_path = self.root / "plan.json"
        plan_path.write_text(json.dumps(gc.run(self.args())))
        with patch.object(gc, "readyz", return_value={"status": 200, "release_sha": "e" * 40}):
            with self.assertRaisesRegex(gc.Refuse, "readyz_current_mismatch"):
                gc.run(self.args("apply", plan_path))
        self.assertTrue(self.directory.exists())

    def test_inventory_does_not_create_audit_or_plan(self):
        gc.run(self.args())
        self.assertFalse(self.audit.exists())
        self.assertFalse((self.root / "plan.json").exists())


if __name__ == "__main__":
    unittest.main()
