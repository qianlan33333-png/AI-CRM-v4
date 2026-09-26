"""Exercise writable-copy isolation, invalidation, cleanup and resource locking."""
import json
import multiprocessing
import os
from pathlib import Path
import tempfile
import time
import unittest
from unittest.mock import patch

import check_preparation as prep


def slot_worker(directory, queue):
    os.environ["AICRM_TEST_PREP_DIR"] = directory
    with prep.heavy_slot(["go", "test"]):
        queue.put(("enter", time.monotonic()))
        time.sleep(0.1)
        queue.put(("leave", time.monotonic()))


class CheckPreparationTest(unittest.TestCase):
    def fixture(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        root = Path(temp.name)
        cache = root / "cache"; cache.mkdir(mode=0o700)
        repo = root / "repo"; repo.mkdir()
        self.enterContext(patch.dict(os.environ, {"AICRM_TEST_PREP_DIR": str(cache)}))
        return cache, repo

    def test_artifact_copies_are_writable_isolated_and_input_changes_rebuild(self):
        cache, root = self.fixture()
        fingerprints = ["a" * 64, "a" * 64, "b" * 64]
        def build(command, cwd):
            if "stage-pr01-effects-ui.mjs" in command[1]:
                target = Path(command[-1]); target.mkdir()
                (target / "asset-manifest.json").write_text('{"original":true}')
                (target / "page.js").write_text("original")
        with patch.object(prep, "artifact_input", side_effect=fingerprints), patch.object(prep, "run", side_effect=build) as run:
            prep.materialize_artifact(root)
            (root / "web/dist/page.js").write_text("fixture changed")
            prep.materialize_artifact(root)
            self.assertEqual((root / "web/dist/page.js").read_text(), "original")
            self.assertEqual(run.call_count, 5)
            prep.materialize_artifact(root)
            self.assertEqual(run.call_count, 10)
        (cache / ("artifact-" + "b" * 64) / "dist/page.js").write_text("corrupt")
        with patch.object(prep, "artifact_input", return_value="b" * 64):
            with self.assertRaisesRegex(ValueError, "artifact bytes"):
                prep.materialize_artifact(root)

    def test_dependency_inputs_install_once_and_lock_change_invalidates(self):
        cache, root = self.fixture()
        (root / "package.json").write_text("{}")
        (root / "package-lock.json").write_text("first")
        def install(command, cwd):
            modules = cwd / "node_modules"; modules.mkdir(exist_ok=True)
            (modules / ".package-lock.json").write_text("installed")
        with patch.object(prep, "tool_input", return_value=["node", "npm"]), patch.object(prep, "run", side_effect=install) as run:
            prep.npm_dependencies(root, ".")
            prep.npm_dependencies(root, ".")
            self.assertEqual(run.call_count, 1)
            (root / "package-lock.json").write_text("second")
            prep.npm_dependencies(root, ".")
            self.assertEqual(run.call_count, 2)

    def test_corrupt_dependency_copy_restores_and_corrupt_snapshot_reinstalls(self):
        cache,root=self.fixture()
        (root/"package.json").write_text("{}")
        (root/"package-lock.json").write_text("lock")
        def install(command,cwd):
            modules=cwd/"node_modules"; modules.mkdir(exist_ok=True)
            (modules/".package-lock.json").write_text("installed")
            (modules/"library.js").write_text("verified")
        with patch.object(prep,"tool_input",return_value=["node","npm"]),patch.object(prep,"run",side_effect=install) as run:
            prep.npm_dependencies(root,".")
            (root/"node_modules/library.js").write_text("corrupt writable copy")
            prep.npm_dependencies(root,".")
            self.assertEqual((root/"node_modules/library.js").read_text(),"verified")
            self.assertEqual(run.call_count,1)
            snapshot=next(cache.glob("npm-*/modules"))
            (snapshot/"library.js").write_text("corrupt snapshot")
            (root/"node_modules/library.js").write_text("corrupt copy again")
            prep.npm_dependencies(root,".")
            self.assertEqual(run.call_count,2)
            self.assertEqual((root/"node_modules/library.js").read_text(),"verified")
            self.assertIn("npm_corruption",(cache/"preparation.jsonl").read_text())

    def test_cleanup_rejects_non_synthetic_server_and_unowned_database(self):
        cache, _ = self.fixture()
        (cache / "database-bad.json").write_text(json.dumps({"database": "production"}))
        for raw in ("postgres://role@10.0.4.1/aicrm_test_x", "postgres://role@localhost/production", "postgres://role@localhost/aicrm_test_x"):
            with self.subTest(raw=raw), patch.object(prep.subprocess, "run") as run:
                with self.assertRaises(ValueError):
                    prep.cleanup_databases(cache, raw)
                run.assert_not_called()

    def test_heavy_commands_serialize_across_processes(self):
        cache, _ = self.fixture()
        context = multiprocessing.get_context("fork")
        queue = context.Queue()
        children = [context.Process(target=slot_worker, args=(str(cache), queue)) for _ in range(2)]
        for child in children: child.start()
        events = [queue.get(timeout=5) for _ in range(4)]
        for child in children:
            child.join(5)
            self.assertEqual(child.exitcode, 0)
        self.assertEqual([event[0] for event in events], ["enter", "leave", "enter", "leave"])
