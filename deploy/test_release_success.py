import importlib.util
import contextlib
import io
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import sys
from types import SimpleNamespace
import unittest
from unittest.mock import patch


def load(name, filename):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).with_name(filename))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


fixtures = load("success_fixtures", "test_cleanup_releases.py")
cleanup = fixtures.cleanup
observer = load("success_observer", "record-release-success.py")


class ReleaseSuccessTests(unittest.TestCase):
    def setUp(self):
        self.fixture = fixtures.CleanupTests()
        self.fixture.setUp()
        self.addCleanup(self.fixture.doCleanups)
        self.root = self.fixture.root
        self.sha = self.fixture.names[-1]
        self.package = self.root / "releases" / self.sha
        (self.package / "deploy").mkdir()
        self.source = self.package / "deploy/record-release-success.py"
        self.source.write_bytes(Path(observer.__file__).read_bytes())
        manifest = self.package / "release-files.sha256"
        manifest.write_text(manifest.read_text() + cleanup.digest_file(self.source) + "  ./deploy/record-release-success.py\n")
        for name in ("cleanup-releases.py", "post-release-retention.py"):
            path = self.source.with_name(name)
            path.write_bytes(Path(observer.__file__).with_name(name).read_bytes())
            manifest.write_text(manifest.read_text() + cleanup.digest_file(path) + "  ./deploy/" + name + "\n")
        self.receipt = self.root / cleanup.SUCCESS_DIRECTORY / (self.sha + ".json")
        self.receipt.unlink()
        self.config = self.root / "app.env"
        self.config.write_text("SYNTHETIC_TEST_CONFIG=not_read\n"); self.config.chmod(0o600)
        (self.root / "install-release.lock").touch(mode=0o644)
        self.post = SimpleNamespace(require_lock=unittest.mock.Mock(),
            database_environment=unittest.mock.Mock(return_value={}),
            snapshot=unittest.mock.Mock(side_effect=lambda *_: json.loads(self.fixture.schema.read_text())))
        for target, key, value in ((observer, "ROOT", self.root), (observer, "CONFIG", self.config),
                (observer, "SOURCE", self.source), (observer, "ROOT_UID", os.getuid()), (observer, "ROOT_GID", os.getgid())):
            changed = patch.object(target, key, value); changed.start(); self.addCleanup(changed.stop)
        helpers = patch.object(observer, "helper", side_effect=lambda name, filename: cleanup if filename == "cleanup-releases.py" else self.post)
        helpers.start(); self.addCleanup(helpers.stop)
        self.processes = patch.object(observer, "verify_processes", return_value={"api_pid": 10, "worker_pid": 20})
        self.processes.start(); self.addCleanup(self.processes.stop)

    def test_real_observation_binding_atomic_private_receipt_and_sequence(self):
        receipt = observer.record(self.sha, "100")
        self.assertEqual(receipt["sequence"], 4)
        self.assertEqual(receipt["run_number"], "100")
        self.assertEqual(self.receipt.stat().st_mode & 0o777, 0o600)
        self.assertEqual(self.receipt.stat().st_nlink, 1)
        package = cleanup.verified_package(self.package)
        self.assertEqual(cleanup.verified_success(self.root, package), receipt)
        cleanup.check_ready.assert_called_with(self.sha)
        self.assertEqual(cleanup.check_ready.call_count, 2)
        self.post.require_lock.assert_called_once_with(self.root)
        # Historical package deletion does not remove sequence evidence.
        shutil.rmtree(self.root / "releases" / self.fixture.names[2])
        again = observer.record(self.sha)
        self.assertEqual(again["sequence"], 5)
        self.assertFalse(list(self.receipt.parent.glob(".pending-*")))

    def test_failed_readiness_worker_schema_or_package_does_not_publish(self):
        with patch.object(cleanup, "check_ready", side_effect=cleanup.Refuse("not_ready")):
            with self.assertRaises(cleanup.Refuse): observer.record(self.sha)
        self.assertFalse(self.receipt.exists())
        with patch.object(observer, "verify_processes", side_effect=ValueError("wrong_worker")):
            with self.assertRaises(ValueError): observer.record(self.sha)
        self.assertFalse(self.receipt.exists())
        schema = json.loads(self.fixture.schema.read_text())
        schema["migrations"][0]["checksum"] = "0" * 64
        with patch.object(self.post, "snapshot", return_value=schema):
            with self.assertRaisesRegex(ValueError, "schema"): observer.record(self.sha)
        self.assertFalse(self.receipt.exists())
        (self.package / "bin/aicrm").write_bytes(b"changed")
        with self.assertRaises(cleanup.Refuse): observer.record(self.sha)
        self.assertFalse(self.receipt.exists())

    def test_source_current_lock_config_and_privilege_are_not_optional(self):
        with patch.object(observer, "SOURCE", self.root / "copied-helper.py"):
            with self.assertRaisesRegex(ValueError, "installed"): observer.record(self.sha)
        with patch.object(observer.os, "geteuid", return_value=os.getuid() + 1):
            with self.assertRaisesRegex(ValueError, "root"): observer.record(self.sha)
        with patch.object(self.post, "require_lock", side_effect=ValueError("wrong_lock")):
            with self.assertRaisesRegex(ValueError, "wrong_lock"): observer.record(self.sha)
        self.config.chmod(0o644)
        with self.assertRaisesRegex(ValueError, "configuration"): observer.record(self.sha)
        self.config.chmod(0o600)
        (self.root / "current").unlink()
        (self.root / "current").symlink_to(self.root / "releases" / self.fixture.names[0])
        with self.assertRaisesRegex(ValueError, "not_current"): observer.record(self.sha)
        self.assertFalse(self.receipt.exists())

    def test_write_failure_and_post_observation_mutation_do_not_publish(self):
        with patch.object(observer.os, "replace", side_effect=OSError("write failed")):
            with self.assertRaises(OSError): observer.record(self.sha)
        self.assertFalse(self.receipt.exists())
        self.assertFalse(list(self.receipt.parent.glob(".pending-*")))
        def tamper(*_):
            (self.package / "bin/aicrm").write_bytes(b"tampered during readiness")
            return {"api_pid": 10, "worker_pid": 20}
        with patch.object(observer, "verify_processes", side_effect=tamper):
            with self.assertRaises(cleanup.Refuse): observer.record(self.sha)
        self.assertFalse(self.receipt.exists())

    def test_untrusted_existing_receipt_does_not_reset_permanent_sequence(self):
        existing = self.root / cleanup.SUCCESS_DIRECTORY / (self.fixture.names[2] + ".json")
        existing.write_text('{"success":true}')
        with self.assertRaises(cleanup.Refuse): observer.record(self.sha)
        self.assertFalse(self.receipt.exists())

    def test_helper_import_does_not_modify_package_with_bytecode(self):
        (self.source.parent / "isolated_helper.py").write_text("VALUE = 1\n")
        environment = dict(os.environ)
        environment.pop("PYTHONDONTWRITEBYTECODE", None)
        code = '''import runpy,sys
from pathlib import Path
assert sys.dont_write_bytecode is False
value=runpy.run_path(sys.argv[1],run_name="isolated_observer")
assert sys.dont_write_bytecode is True
value[sys.argv[2]]("isolated_helper", "isolated_helper.py")
assert not (Path(sys.argv[1]).parent / "__pycache__").exists()
'''
        for script, function in ((self.source, "helper"), (self.source.with_name("post-release-retention.py"), "load_helper")):
            subprocess.run([sys.executable, "-c", code, str(script), function], env=environment, check=True, capture_output=True)
        host = self.source.with_name("install-host-maintenance.py")
        host.write_bytes(Path(observer.__file__).with_name(host.name).read_bytes())
        host.with_name("install-runtime-retention.py").write_text('def apply():\n    raise RuntimeError("isolated-import-stop")\n')
        host_code = '''import os,runpy,sys
from pathlib import Path
from unittest.mock import patch
assert sys.dont_write_bytecode is False
value=runpy.run_path(sys.argv[1],run_name="isolated_host")
assert sys.dont_write_bytecode is True
with patch.object(sys,"platform","linux"),patch.object(os,"geteuid",return_value=0),patch.object(Path,"is_dir",return_value=True):
    try: value["install"]()
    except RuntimeError as error: assert str(error)=="isolated-import-stop"
    else: raise AssertionError("probe must stop before system changes")
assert not (Path(sys.argv[1]).parent / "__pycache__").exists()
'''
        subprocess.run([sys.executable, "-c", host_code, str(host)], env=environment, check=True, capture_output=True)

    def test_post_rename_fsync_failure_revokes_new_claim_and_preserves_sequence(self):
        real_sync = observer.sync_directory
        failed = False
        def fail_after_rename(directory):
            nonlocal failed
            if not failed and self.receipt.exists():
                failed = True
                raise OSError("directory fsync failed after rename")
            real_sync(directory)
        with patch.object(observer, "sync_directory", side_effect=fail_after_rename):
            with self.assertRaisesRegex(OSError, "after rename"): observer.record(self.sha)
        self.assertTrue(failed)
        self.assertFalse(self.receipt.exists())
        self.assertFalse((self.receipt.parent / cleanup.SUCCESS_PENDING).exists())
        revoked = list(self.receipt.parent.glob("revoked-*.json"))
        self.assertEqual(len(revoked), 1)
        self.assertEqual(cleanup.read_pending_success(self.root, revoked[0].name)["candidate"]["sequence"], 4)
        self.assertEqual(observer.record(self.sha)["sequence"], 5)

    def test_post_rename_fsync_failure_restores_previous_valid_receipt(self):
        previous = observer.record(self.sha)
        real_sync = observer.sync_directory
        failed = False
        def fail_after_rename(directory):
            nonlocal failed
            if not failed and self.receipt.exists() and json.loads(self.receipt.read_text())["sequence"] > previous["sequence"]:
                failed = True
                raise OSError("post-rename failure")
            real_sync(directory)
        with patch.object(observer, "sync_directory", side_effect=fail_after_rename):
            with self.assertRaises(OSError): observer.record(self.sha)
        self.assertEqual(json.loads(self.receipt.read_text()), previous)
        self.assertEqual(observer.record(self.sha)["sequence"], previous["sequence"] + 2)

    def test_committed_receipt_final_guard_fsync_failure_never_triggers_false_rollback(self):
        real_sync = observer.sync_directory
        pending = self.receipt.parent / cleanup.SUCCESS_PENDING
        def fail_after_guard_removed(directory):
            if self.receipt.exists() and not pending.exists():
                raise OSError("final guard fsync failed")
            real_sync(directory)
        real_write = observer.write_private_json
        def reject_guard_recreation(path, value):
            if path == pending and self.receipt.exists():
                raise OSError("cannot recreate guard")
            real_write(path, value)
        with patch.object(observer, "sync_directory", side_effect=fail_after_guard_removed), \
                patch.object(observer, "write_private_json", side_effect=reject_guard_recreation), \
                contextlib.redirect_stderr(io.StringIO()) as output:
            committed = observer.record(self.sha)
        self.assertEqual(cleanup.verified_success(self.root, cleanup.verified_package(self.package)), committed)
        self.assertIn("success committed", output.getvalue())
        self.assertFalse(pending.exists())

    def test_committed_receipt_guard_unlink_failure_returns_success_but_blocks_cleanup(self):
        pending = self.receipt.parent / cleanup.SUCCESS_PENDING
        real_unlink = Path.unlink
        def fail_guard(path, *args, **kwargs):
            if path == pending:
                raise OSError("guard removal failed")
            return real_unlink(path, *args, **kwargs)
        with patch.object(Path, "unlink", fail_guard), contextlib.redirect_stderr(io.StringIO()):
            committed = observer.record(self.sha)
        self.assertEqual(json.loads(self.receipt.read_text()), committed)
        self.assertTrue(pending.exists())
        with self.assertRaisesRegex(cleanup.Refuse, "publication_incomplete"):
            cleanup.inventory(self.root, self.fixture.schema, [])

    def test_kill9_publication_blocks_cleanup_and_revoke_never_starts_failed_release(self):
        pid = os.fork()
        if pid == 0:
            real_sync = observer.sync_directory
            def killed_after_rename(directory):
                if self.receipt.exists():
                    os.kill(os.getpid(), signal.SIGKILL)
                real_sync(directory)
            observer.sync_directory = killed_after_rename
            observer.record(self.sha)
            os._exit(99)
        _, status = os.waitpid(pid, 0)
        self.assertEqual(os.WTERMSIG(status), signal.SIGKILL)
        self.assertTrue(self.receipt.exists())
        self.assertTrue((self.receipt.parent / cleanup.SUCCESS_PENDING).is_file())
        with self.assertRaisesRegex(cleanup.Refuse, "publication_incomplete"):
            cleanup.inventory(self.root, self.fixture.schema, [])
        # Model the installer's rollback; recovery never requires running this SHA again.
        current = self.root / "current"
        current.unlink(); current.symlink_to(self.root / "releases" / self.fixture.names[0])
        self.post.snapshot.reset_mock()
        with patch.object(observer, "verify_processes", side_effect=AssertionError("must not start or observe failed version")):
            observer.record(self.sha, revoke=True)
        self.post.snapshot.assert_not_called()
        self.assertEqual(current.resolve().name, self.fixture.names[0])
        self.assertFalse(self.receipt.exists())
        self.assertFalse((self.receipt.parent / cleanup.SUCCESS_PENDING).exists())
        self.assertEqual(len(list(self.receipt.parent.glob("revoked-*.json"))), 1)

    def test_uncertain_compensation_stays_blocked_and_rejects_third_party_receipt(self):
        original_sync = observer.sync_directory
        def fail_after_rename(directory):
            if self.receipt.exists():
                raise OSError("persistent storage failure")
            original_sync(directory)
        with patch.object(observer, "sync_directory", side_effect=fail_after_rename):
            with self.assertRaises(OSError): observer.record(self.sha)
        pending = self.receipt.parent / cleanup.SUCCESS_PENDING
        self.assertTrue(pending.exists())
        with self.assertRaises(cleanup.Refuse): cleanup.inventory(self.root, self.fixture.schema, [])
        altered = json.loads(self.receipt.read_text()); altered["sequence"] += 10
        self.receipt.write_text(json.dumps(altered))
        with self.assertRaisesRegex(ValueError, "receipt_changed"): observer.record(self.sha, revoke=True)
        self.assertTrue(pending.exists())

    def test_unsealed_helper_cannot_execute_before_rejection(self):
        marker = self.root / "untrusted-code-ran"
        path = self.source.with_name("cleanup-releases.py")
        path.write_text("from pathlib import Path\nPath(" + repr(str(marker)) + ").touch()\n")
        path.chmod(0o666)
        fresh = load("success_real_import", "record-release-success.py")
        fresh.SOURCE = self.source
        with patch.object(observer, "helper", wraps=fresh.helper) as imported:
            with self.assertRaisesRegex(ValueError, "untrusted_success_observer"):
                observer.record(self.sha)
            imported.assert_not_called()
        self.assertFalse(marker.exists())

    def test_service_inactive_or_missing_mainpid_cannot_be_observed(self):
        with patch.object(observer.subprocess, "run", side_effect=observer.subprocess.CalledProcessError(1, "systemctl")):
            with self.assertRaises(observer.subprocess.CalledProcessError): observer.process_id("aicrm.service")
        for pid in ("0", "", "not-a-pid"):
            with patch.object(observer.subprocess, "run", return_value=SimpleNamespace(stdout=pid)):
                with self.assertRaisesRegex(ValueError, "unavailable"): observer.process_id("aicrm.service")

    def test_process_verification_checks_both_images_hashes_and_pid_reuse(self):
        self.processes.stop()
        expected = self.package / "bin/aicrm"
        actual_stat = Path.stat
        def file_stat(path, *args, **kwargs):
            return actual_stat(expected) if str(path).startswith("/proc/") else actual_stat(path, *args, **kwargs)
        digest = cleanup.digest_file(expected)
        with patch.object(observer, "process_id", side_effect=[10, 10, 20, 20]), \
                patch.object(observer.os, "readlink", return_value=str(expected)), \
                patch.object(Path, "stat", file_stat):
            with patch.object(cleanup, "digest_file", return_value=digest):
                self.assertEqual(observer.verify_processes(self.sha, digest, cleanup), {"api_pid": 10, "worker_pid": 20})
        for mismatch in (str(expected) + " (deleted)", str(self.root / "previous/bin/aicrm")):
            with patch.object(observer, "process_id", return_value=10), patch.object(observer.os, "readlink", return_value=mismatch):
                with self.assertRaisesRegex(ValueError, "image_mismatch"): observer.verify_processes(self.sha, digest, cleanup)
        with patch.object(observer, "process_id", side_effect=[10, 11]), \
                patch.object(observer.os, "readlink", return_value=str(expected)), patch.object(Path, "stat", file_stat), \
                patch.object(cleanup, "digest_file", return_value=digest):
            with self.assertRaisesRegex(ValueError, "changed"): observer.verify_processes(self.sha, digest, cleanup)
        with patch.object(observer, "process_id", return_value=10), \
                patch.object(observer.os, "readlink", return_value=str(expected)), patch.object(Path, "stat", file_stat), \
                patch.object(cleanup, "digest_file", return_value="0" * 64):
            with self.assertRaisesRegex(ValueError, "changed"): observer.verify_processes(self.sha, digest, cleanup)


if __name__ == "__main__":
    unittest.main()
