"""Real filesystem/lock and existing observer fixtures for native completion."""
import fcntl
import importlib.util
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch


def load(name, filename):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).with_name(filename))
    module = importlib.util.module_from_spec(spec); spec.loader.exec_module(module)
    return module


m = load('retention_observer_installer', 'domestic-promote.py')
fixtures = load('retention_observer_fixtures', 'test_release_success.py')


class ObservationCompletion(unittest.TestCase):
    def setUp(self):
        self.fixture = fixtures.ReleaseSuccessTests(); self.fixture.setUp()
        self.addCleanup(self.fixture.doCleanups)
        self.root = self.fixture.root; self.sha = self.fixture.sha
        for key, value in [('ROOT', self.root), ('RELEASES', self.root / 'releases'), ('LOCK', self.root / 'install-release.lock')]:
            context = patch.object(m, key, value); context.start(); self.addCleanup(context.stop)
        context = patch.object(m, '_load_success_observer', return_value=fixtures.observer)
        context.start(); self.addCleanup(context.stop)
        # Production verifiers fix uid/gid 0; this fixture uses the test uid.
        def verify_file(path):
            info = path.lstat()
            if path.is_symlink() or info.st_uid != os.getuid() or info.st_mode & 0o022:
                raise RuntimeError('unsafe synthetic observer')
            return info
        for key, value in [('_assert_root_file', verify_file), ('_assert_root_directory', lambda p, **kw: fixtures.cleanup.require_control_directory(p, private=kw.get('private', False)))]:
            context = patch.object(m, key, value); context.start(); self.addCleanup(context.stop)

    def test_actual_observer_fact_then_idempotent_reuse_preserves_app_inode(self):
        binary = self.fixture.package / 'bin/aicrm'; before = binary.stat().st_ino
        with m.LOCK.open('a+') as lock, tempfile.TemporaryDirectory(dir=self.root) as d:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            first = m._ensure_current_success(self.sha, fixtures.cleanup, lock.fileno(), Path(d))
            fact = self.fixture.receipt.read_bytes()
            second = m._ensure_current_success(self.sha, fixtures.cleanup, lock.fileno(), Path(d))
        self.assertEqual(first['status'], 'observed_current')
        self.assertEqual(second['status'], 'verified_existing')
        self.assertEqual(fact, self.fixture.receipt.read_bytes())
        self.assertEqual(before, binary.stat().st_ino)

    def test_shared_observer_copy_changes_only_current_code_inodes(self):
        original = {}
        alias_dir = self.root / 'releases' / self.fixture.fixture.names[0] / 'deploy'
        alias_dir.mkdir()
        for name in ('record-release-success.py', 'cleanup-releases.py', 'post-release-retention.py'):
            path = self.fixture.package / 'deploy' / name
            path.chmod(0o444)
            os.link(path, alias_dir / name)
            original[name] = (path.read_bytes(), path.stat().st_ino, path.stat().st_mode)
        with m.LOCK.open('a+') as lock, tempfile.TemporaryDirectory(dir=self.root) as d:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            result = m._ensure_current_success(self.sha, fixtures.cleanup, lock.fileno(), Path(d))
        self.assertEqual(result['detached_observer_files'], 3)
        for name, (data, inode, mode) in original.items():
            path = self.fixture.package / 'deploy' / name
            self.assertEqual(path.read_bytes(), data)
            self.assertEqual(path.stat().st_nlink, 1)
            self.assertNotEqual(path.stat().st_ino, inode)
            self.assertEqual(path.stat().st_mode, mode)
            self.assertEqual((alias_dir / name).stat().st_ino, inode)

    def test_failed_copy_leaves_package_exact_and_no_fact(self):
        source = self.fixture.source; os.link(source, self.fixture.package / 'deploy/observer-alias.py')
        before = (source.read_bytes(), source.stat().st_ino)
        with tempfile.TemporaryDirectory(dir=self.root) as d, patch.object(m.shutil, 'copyfileobj', side_effect=OSError('synthetic full disk')):
            with self.assertRaises(OSError): m._detach_observer_files(self.sha, Path(d))
            self.assertEqual(list(Path(d).iterdir()), [])
        self.assertEqual(before, (source.read_bytes(), source.stat().st_ino))
        self.assertFalse(self.fixture.receipt.exists())

    def test_fd9_uses_original_flock_and_restores_any_previous_fd9(self):
        saved = None
        try: saved = os.dup(9)
        except OSError: pass
        spare = os.open(self.root / 'spare', os.O_CREAT | os.O_RDWR, 0o600)
        try:
            os.dup2(spare, 9); expected = os.fstat(9)
            with m.LOCK.open('a+') as lock:
                fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
                with m._observer_install_lock(lock.fileno()):
                    self.assertEqual(os.fstat(9).st_ino, os.fstat(lock.fileno()).st_ino)
                    with m.LOCK.open('a+') as second:
                        with self.assertRaises(BlockingIOError): fcntl.flock(second, fcntl.LOCK_EX | fcntl.LOCK_NB)
                self.assertEqual(os.fstat(9).st_ino, expected.st_ino)
        finally:
            os.close(spare)
            if saved is None: os.close(9)
            else: os.dup2(saved, 9); os.close(saved)

    def test_wrong_lock_and_bad_schema_never_publish_success(self):
        with (self.root / 'wrong.lock').open('a+') as wrong:
            with self.assertRaisesRegex(RuntimeError, 'identity_mismatch'):
                with m._observer_install_lock(wrong.fileno()): pass
        value = self.fixture.post.snapshot.return_value
        self.fixture.post.snapshot.side_effect = lambda *_: {'current_sha': self.sha, 'captured_at': fixtures.cleanup.utcnow().isoformat(), 'migrations': [{'version':'0001','name':'0001_example.sql','checksum':'0'*64}]}
        with m.LOCK.open('a+') as lock, tempfile.TemporaryDirectory(dir=self.root) as d:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            with self.assertRaisesRegex(ValueError, 'schema_mismatch'):
                m._ensure_current_success(self.sha, fixtures.cleanup, lock.fileno(), Path(d))
        self.assertFalse(self.fixture.receipt.exists())

    def test_copy_work_is_sibling_on_target_filesystem_not_root_scratch(self):
        alias = self.root / 'releases' / self.fixture.fixture.names[0] / 'deploy'
        alias.mkdir(); self.fixture.source.chmod(0o444); os.link(self.fixture.source, alias / self.fixture.source.name)
        replace = os.replace
        def same_filesystem(source, destination):
            if Path(source).parent.parent != self.root / 'releases':
                raise OSError(18, 'cross-device synthetic failure')
            return replace(source, destination)
        with m.LOCK.open('a+') as lock, tempfile.TemporaryDirectory(dir=self.root) as scratch:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            # Only the copy helper uses this replace; observer atomics still use
            # the real implementation from the existing fixture's module.
            detach = m._detach_observer_files
            def check_work(sha, work):
                with patch.object(m.os, 'replace', side_effect=same_filesystem):
                    return detach(sha, work)
            with patch.object(m, '_detach_observer_files', side_effect=check_work):
                m._ensure_current_success(self.sha, fixtures.cleanup, lock.fileno(), Path(scratch))
        self.assertEqual(list((self.root / 'releases').glob('.domestic-observer-*')), [])

    def test_partial_copy_failure_can_resume_without_changing_old_aliases(self):
        alias_dir = self.root / 'releases' / self.fixture.fixture.names[0] / 'deploy'; alias_dir.mkdir()
        for name in ('record-release-success.py', 'cleanup-releases.py', 'post-release-retention.py'):
            (self.fixture.package / 'deploy' / name).chmod(0o444)
            os.link(self.fixture.package / 'deploy' / name, alias_dir / name)
        real = m.shutil.copyfileobj; calls = []
        def fail_second(source, target):
            calls.append(1)
            if len(calls) == 2: raise OSError('synthetic interrupted second copy')
            return real(source, target)
        with m.LOCK.open('a+') as lock, tempfile.TemporaryDirectory(dir=self.root) as d:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            with patch.object(m.shutil, 'copyfileobj', side_effect=fail_second):
                with self.assertRaises(OSError): m._ensure_current_success(self.sha, fixtures.cleanup, lock.fileno(), Path(d))
            self.assertFalse(self.fixture.receipt.exists())
            result = m._ensure_current_success(self.sha, fixtures.cleanup, lock.fileno(), Path(d))
            self.assertEqual(result['status'], 'observed_current')
        self.assertEqual(list((self.root / 'releases').glob('.domestic-observer-*')), [])


if __name__ == '__main__': unittest.main()
