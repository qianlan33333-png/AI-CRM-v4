import importlib.util
import os
from pathlib import Path
import tempfile
import time
from types import SimpleNamespace
import unittest
from unittest.mock import patch


def load(name, file):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).with_name(file))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


cleanup = load("runtime_cleanup", "cleanup-runtime-files.py")
installer = load("runtime_installer", "install-runtime-retention.py")


class RuntimeRetentionTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name).resolve()
        (self.root / cleanup.MARKER).write_text(cleanup.MARKER_CONTENT)
        # Advancing the test clock ages ctime as well as mtime; production cannot
        # backdate ctime, so newly copied old files are intentionally protected.
        self.clock = time.time() + 32 * 86400
        self.before = self.clock - cleanup.AGE_SECONDS

    def run_cleanup(self, apply=False, limit=1000, active=None):
        with patch.object(cleanup.time, "time", return_value=self.clock):
            return cleanup.cleanup([self.root], os.getuid(), self.before, limit, apply, active or set())

    def test_cutoff_batch_dry_run_and_replay(self):
        for name in ("first", "second", "boundary", "recent"):
            (self.root / name).write_text("disposable")
        os.utime(self.root / "boundary", (self.before, self.before))
        os.utime(self.root / "recent", (self.clock, self.clock))
        preview = self.run_cleanup(limit=1)
        self.assertEqual((preview["candidates"], preview["deleted"], preview["remaining"]), (1, 0, True))
        self.assertTrue((self.root / "first").exists())
        first = self.run_cleanup(apply=True, limit=1)
        self.assertEqual((first["deleted"], first["remaining"]), (1, True))
        self.assertEqual(self.run_cleanup(apply=True)["deleted"], 1)
        self.assertEqual(self.run_cleanup(apply=True)["deleted"], 0)
        self.assertTrue((self.root / "boundary").exists())
        self.assertTrue((self.root / "recent").exists())

    def test_business_secret_hardlink_symlink_and_open_files_survive(self):
        for name in ("secrets", "business", "backups", "exports", "uploads"):
            (self.root / name).mkdir()
            (self.root / name / "content").write_text("protected")
        for name in ("database.sql", "secret.pem", ".env", "opened", "hardlink"):
            (self.root / name).write_text("protected")
        os.link(self.root / "hardlink", self.root / "hardlink-copy")
        (self.root / "alias").symlink_to(self.root / "opened")
        info = (self.root / "opened").stat()
        result = self.run_cleanup(apply=True, active={(info.st_dev, info.st_ino)})
        self.assertEqual(result["deleted"], 0)
        self.assertTrue((self.root / "alias").is_symlink())

    def test_unknown_directory_not_adopted_and_shorter_policy_rejected(self):
        (self.root / cleanup.MARKER).write_text("unknown")
        with self.assertRaisesRegex(cleanup.Refuse, "unclassified"):
            self.run_cleanup(apply=True)
        with patch.object(cleanup.time, "time", return_value=self.clock):
            with self.assertRaises(cleanup.Refuse):
                cleanup.cleanup([self.root], os.getuid(), self.clock - 29 * 86400, 1000, True, set())

    def test_proc_descriptor_inventory_identifies_live_inode(self):
        proc = self.root / "proc"
        descriptors = proc / "101" / "fd"
        descriptors.mkdir(parents=True)
        data = self.root / "execution"
        data.write_text("active")
        (descriptors / "7").symlink_to(data)
        (descriptors.parent / "maps").write_text("")
        info = data.stat()
        self.assertEqual(cleanup.active_files(proc), {(info.st_dev, info.st_ino)})

    def test_opened_after_inventory_and_mmap_without_descriptor_are_protected(self):
        artifact = self.root / "execution"
        artifact.write_text("active")
        info = artifact.stat()
        identity = (info.st_dev, info.st_ino)
        with patch.object(cleanup.time, "time", return_value=self.clock):
            report = cleanup.cleanup([self.root], os.getuid(), self.before, 1000, True, set(), refresh_active=lambda: {identity})
        self.assertEqual(report["deleted"], 0)
        self.assertTrue(artifact.exists())
        proc = self.root / "proc"
        (proc / "102" / "fd").mkdir(parents=True)
        (proc / "102" / "maps").write_text(f"1000-2000 r--p 0 {os.major(info.st_dev):x}:{os.minor(info.st_dev):x} {info.st_ino} /redacted\n")
        self.assertEqual(cleanup.active_files(proc), {identity})

    def test_incomplete_live_process_inventory_refuses_deletion(self):
        proc = self.root / "proc"
        (proc / "102" / "fd").mkdir(parents=True)
        artifact = self.root / "execution"
        artifact.write_text("do not delete without complete evidence")
        with patch.object(cleanup.time, "time", return_value=self.clock):
            with self.assertRaisesRegex(cleanup.Refuse, "process_inventory_incomplete"):
                cleanup.cleanup([self.root], os.getuid(), self.before, 1000, True, set(), refresh_active=lambda: cleanup.active_files(proc))
        self.assertTrue(artifact.exists())

    def test_namespace_commands_never_target_global_journal_or_tmp(self):
        commands = installer.commands()
        self.assertEqual(commands["journal_cleanup"], ["journalctl", "--namespace=aicrm", "--rotate", "--vacuum-time=30d"])
        self.assertEqual(commands["process_file_roots"], [str(p) for p in cleanup.ROOTS])
        self.assertTrue(all(name.startswith("aicrm") for name in commands["application_restart_required"]))
        self.assertIn("LogNamespace=aicrm", installer.DROPIN)
        config = Path(__file__).with_name("journald-aicrm-retention.conf").read_text()
        self.assertIn("journald@aicrm.conf.d", config)
        self.assertNotIn("/etc/systemd/journald.conf.d", config)

    def test_installer_mutations_are_scoped_and_never_restart_application(self):
        roots = (self.root / "temp", self.root / "diagnostics")
        writes, invoked = [], []
        def run(command, **kwargs):
            invoked.append(command)
            return SimpleNamespace(stdout="systemd 249\n")
        with patch.object(installer, "ROOTS", roots), patch.object(installer.sys, "platform", "linux"), \
                patch.object(installer.os, "geteuid", return_value=0), patch.object(installer.os, "chown"), \
                patch.object(installer.pwd, "getpwnam", return_value=SimpleNamespace(pw_uid=os.getuid(), pw_gid=os.getgid())), \
                patch.object(installer.subprocess, "run", side_effect=run), \
                patch.object(installer, "atomic_write", side_effect=lambda path, content: writes.append((path, content))):
            installer.apply()
        configs = [str(path) for path, _ in writes if str(path).startswith("/etc/")]
        self.assertEqual(len(configs), len(installer.UNITS) + 1)
        self.assertIn("/etc/systemd/journald@aicrm.conf.d/60-retention.conf", configs)
        self.assertEqual(invoked, [["systemctl", "--version"], ["systemctl", "daemon-reload"], ["systemctl", "try-restart", "systemd-journald@aicrm.service"]])

    def test_installer_refuses_existing_unclassified_directory_before_writes(self):
        roots = (self.root / "existing",)
        roots[0].mkdir()
        (roots[0] / "business.csv").write_text("do not adopt")
        with patch.object(installer, "ROOTS", roots), patch.object(installer.sys, "platform", "linux"), \
                patch.object(installer.os, "geteuid", return_value=0), \
                patch.object(installer.pwd, "getpwnam", return_value=SimpleNamespace(pw_uid=os.getuid(), pw_gid=os.getgid())), \
                patch.object(installer.subprocess, "run", return_value=SimpleNamespace(stdout="systemd 249\n")), \
                patch.object(installer, "atomic_write") as write:
            with self.assertRaisesRegex(ValueError, "unclassified"):
                installer.apply()
        write.assert_not_called()


if __name__ == "__main__":
    unittest.main()
