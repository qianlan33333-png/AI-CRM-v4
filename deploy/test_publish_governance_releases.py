#!/usr/bin/env python3
import contextlib
import fcntl
import io
import datetime as dt
import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest

BASE = Path(__file__).parent

def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module

bridge = load('bridge_under_test', BASE / 'publish-governance-releases.py')
cleanup = load('cleanup_under_test', BASE / 'cleanup-releases.py')

class BridgeTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name).resolve()
        bridge.ROOT = self.root
        bridge.RESULT = self.root / "state" / "maintenance" / "governance-releases.json"
        (self.root / "state").mkdir(mode=0o755)
        bridge.ROOT_UID = cleanup.ROOT_UID = os.getuid()
        bridge.ROOT_GID = cleanup.ROOT_GID = os.getgid()
        self.directory = self.root / 'release-success'
        self.directory.mkdir(mode=0o700)
        self.sha = 'a' * 40
        self.receipt = {'version': 1, 'release_sha': self.sha, 'manifest_sha256': 'b' * 64,
                        'package_digest': 'c' * 64, 'binary_sha256': 'd' * 64, 'schema_digest': 'e' * 64,
                        'succeeded_at': dt.datetime.now(dt.timezone.utc).isoformat(), 'sequence': 1,
                        'run_number': '123', 'api_pid': 12, 'worker_pid': 13}
        self.write(self.sha + '.json', self.receipt)

    def write(self, name, value):
        path = self.directory / name
        path.write_text(json.dumps(value))
        path.chmod(0o600)
        return path

    def test_projects_only_whitelisted_minimal_facts_and_preserves_gap(self):
        later = dict(self.receipt, sequence=3, release_sha='f' * 40)
        self.write(later['release_sha'] + '.json', later)
        report = bridge.project(cleanup, self.sha)
        self.assertEqual([f['sequence'] for f in report['facts']], [1, 3])
        self.assertEqual(set(report['facts'][0]), {'sequence','release_sha','succeeded_at','receipt_digest','revoked'})
        for key in ('api_pid', 'worker_pid', 'package_digest', 'schema_digest', 'run_number'):
            self.assertNotIn(key, json.dumps(report))

    def test_revocation_is_not_a_success_or_failed_deployment_claim(self):
        revoked = dict(self.receipt, sequence=2, release_sha='f' * 40)
        self.write('revoked-2-' + revoked['release_sha'] + '.json', {'version':1,'candidate':revoked,'previous':None})
        report = bridge.project(cleanup, self.sha)
        self.assertTrue(report['facts'][1]['revoked'])
        self.assertNotIn('failure_rate', report)

    def test_pending_unknown_files_and_duplicate_sequences_fail_closed(self):
        for name, value in [('.publication-pending.json', {}), ('unexpected.json', {}), ('f' * 40 + '.json', dict(self.receipt, release_sha='f' * 40))]:
            path = self.write(name, value)
            with self.assertRaises((ValueError, cleanup.Refuse)):
                bridge.project(cleanup, self.sha)
            path.unlink()

    def test_interrupted_atomic_temporary_is_not_a_fact_but_counts_toward_capacity(self):
        temporary = self.write('.pending-abcd1234', {'not': 'a published success receipt'})
        report = bridge.project(cleanup, self.sha)
        self.assertEqual(len(report['facts']), 1)
        self.assertEqual(report['facts'][0]['sequence'], 1)
        self.assertTrue(temporary.exists(), 'read-only bridge must not delete another writer artifact')
        maximum = bridge.MAX_FACTS
        bridge.MAX_FACTS = 1
        try:
            with self.assertRaisesRegex(ValueError, 'capacity'):
                bridge.project(cleanup, self.sha)
        finally:
            bridge.MAX_FACTS = maximum
        self.write('.publication-pending.json', {})
        with self.assertRaisesRegex(ValueError, 'publication_pending'):
            bridge.project(cleanup, self.sha)

    def test_symlink_hardlink_and_loose_receipt_permissions_rejected(self):
        path = self.directory / (self.sha + '.json')
        path.chmod(0o644)
        with self.assertRaises(cleanup.Refuse): bridge.project(cleanup, self.sha)
        path.chmod(0o600)
        other = self.root / 'other.json'
        os.link(path, other)
        with self.assertRaises(cleanup.Refuse): bridge.project(cleanup, self.sha)
        other.unlink()
        path.rename(other)
        path.symlink_to(other)
        with self.assertRaises((OSError, cleanup.Refuse)): bridge.project(cleanup, self.sha)

    def test_current_receipt_and_directory_authority_required(self):
        with self.assertRaises(ValueError): bridge.project(cleanup, 'f' * 40)
        self.directory.chmod(0o755)
        with self.assertRaises(cleanup.Refuse): bridge.project(cleanup, self.sha)

    def test_bounded_capacity_is_explicit_failure_not_truncated_success(self):
        old = bridge.MAX_FACTS
        bridge.MAX_FACTS = 0
        try:
            with self.assertRaises(ValueError): bridge.project(cleanup, self.sha)
        finally:
            bridge.MAX_FACTS = old

    def test_atomic_publication_is_public_minimal_not_private_original(self):
        value = bridge.project(cleanup, self.sha)
        bridge.publish(value)
        self.assertEqual(bridge.RESULT.stat().st_mode & 0o777, 0o644)
        self.assertEqual(json.loads(bridge.RESULT.read_text()), value)
        original = self.directory / (self.sha + '.json')
        self.assertEqual(original.stat().st_mode & 0o777, 0o600)
        bridge.RESULT.unlink()
        bridge.RESULT.symlink_to(original)
        with self.assertRaises(ValueError): bridge.publish(value)
        self.assertEqual(json.loads(original.read_text()), self.receipt)

    def test_shared_installer_lock_and_inherited_descriptor(self):
        (self.root / 'releases' / self.sha).mkdir(parents=True)
        (self.root / 'current').symlink_to(self.root / 'releases' / self.sha)
        lock = self.root / 'install-release.lock'
        lock.touch(mode=0o600)
        old = bridge.source_helper
        bridge.source_helper = lambda: (self.sha, cleanup)
        try:
            with lock.open('r+') as owner:
                fcntl.flock(owner.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
                with self.assertRaises(BlockingIOError): bridge.run()
                with contextlib.redirect_stdout(io.StringIO()): bridge.run(owner.fileno())
                self.assertTrue(bridge.RESULT.is_file())
                with lock.open('r+') as contender:
                    with self.assertRaises(BlockingIOError): fcntl.flock(contender.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
            with contextlib.redirect_stdout(io.StringIO()): bridge.run()
        finally:
            bridge.source_helper = old

if __name__ == '__main__': unittest.main()
