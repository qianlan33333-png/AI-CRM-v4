"""Domestic GC reuses actual package, success, schema and reference contracts."""
import importlib.util
import json
import os
import unittest
from contextlib import nullcontext
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch


def load(name, filename):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).with_name(filename))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


m = load('package_gc', 'domestic-promote.py')
contracts = load('gc_evidence_contracts', 'test_cleanup_releases.py')
cleanup = contracts.cleanup


class PackageGC(unittest.TestCase):
    def setUp(self):
        self.fixture = contracts.CleanupTests()
        self.fixture.setUp()
        self.addCleanup(self.fixture.doCleanups)
        self.root = self.fixture.root
        self.names = self.fixture.names
        self.current = self.names[-1]
        cursor = {'installed_app_sha': self.current}
        schema_reader = SimpleNamespace(snapshot=lambda *args: json.loads(self.fixture.schema.read_text()),
                                        database_environment=lambda *args: {})
        for context in (
            patch.object(m, 'ROOT', self.root), patch.object(m, 'RELEASES', self.root / 'releases'),
            patch.object(m, 'require_host_role'), patch.object(m, '_production_release_lock', return_value=nullcontext()),
            patch.object(m, '_read_main_state', return_value=cursor),
            patch.object(m, '_reverify_main_cursor', return_value=(cursor, {'previous_sha': self.names[-2]})),
            patch.object(m, 'current_sha', return_value=self.current), patch.object(m, 'readiness'),
            patch.object(m, '_release_retention_helpers', return_value=(cleanup, schema_reader)),
        ):
            context.start()
            self.addCleanup(context.stop)

    def test_two_real_compatible_rollbacks_and_unknown_contents_are_retained(self):
        releases = self.root / 'releases'
        unknown = releases / ('f' * 40)
        unknown.mkdir(); (unknown / 'uploads').mkdir()
        (releases / 'linked').symlink_to(self.root)
        result = m.reclaim_obsolete_releases()
        self.assertEqual(result['status'], 'completed')
        self.assertEqual(result['rollback_releases'], [self.names[-2], self.names[-3]])
        self.assertEqual(result['removed'], [self.names[0]])
        for name in self.names[1:]:
            self.assertTrue((releases / name).is_dir())
        self.assertTrue(unknown.is_dir())
        self.assertTrue((releases / 'linked').is_symlink())
        self.assertTrue((self.root / 'release-cleanup-audit.jsonl').is_file())

    def test_one_successful_rollback_never_deletes_any_package(self):
        for name in self.names[:2]:
            (self.root / cleanup.SUCCESS_DIRECTORY / (name + '.json')).unlink()
        result = m.reclaim_obsolete_releases()
        self.assertEqual(result['status'], 'maintenance-pending')
        self.assertEqual(result['rollback_releases'], [self.names[-2]])
        self.assertEqual(result['removed'], [])
        self.assertTrue(all((self.root / 'releases' / n).is_dir() for n in self.names))

    def test_changed_receipt_cannot_count_as_second_rollback(self):
        (self.root / cleanup.SUCCESS_DIRECTORY / (self.names[0] + '.json')).unlink()
        receipt = self.root / cleanup.SUCCESS_DIRECTORY / (self.names[1] + '.json')
        value = json.loads(receipt.read_text()); value['binary_sha256'] = '0' * 64
        receipt.write_text(json.dumps(value))
        result = m.reclaim_obsolete_releases()
        self.assertEqual(result['status'], 'maintenance-pending')
        self.assertEqual(result['rollback_releases'], [self.names[-2]])
        self.assertEqual(result['removed'], [])

    def test_successful_incompatible_schema_cannot_count_as_rollback(self):
        (self.root / cleanup.SUCCESS_DIRECTORY / (self.names[0] + '.json')).unlink()
        package = self.root / 'releases' / self.names[1]
        migration = package / 'migrations/0001_example.sql'
        migration.write_bytes(b'CREATE TABLE incompatible(id text);\n')
        manifest = package / 'release-files.sha256'
        old = cleanup.digest_file(self.root / 'releases' / self.current / 'migrations/0001_example.sql')
        manifest.write_text(manifest.read_text().replace(old, cleanup.digest_file(migration)))
        self.fixture.receipt(self.names[1], 2)
        result = m.reclaim_obsolete_releases()
        self.assertEqual(result['status'], 'maintenance-pending')
        self.assertEqual(result['rollback_releases'], [self.names[-2]])
        self.assertEqual(result['removed'], [])

    def test_live_reference_is_protected_beyond_two_rollbacks(self):
        with patch.object(cleanup, 'live_references', return_value={self.names[0]}):
            result = m.reclaim_obsolete_releases()
        self.assertEqual(result['status'], 'completed')
        self.assertEqual(result['removed'], [])
        self.assertTrue((self.root / 'releases' / self.names[0]).is_dir())

    def test_shared_current_file_defers_to_normal_success_observer(self):
        binary = self.root / 'releases' / self.current / 'bin/aicrm'
        os.link(binary, self.root / 'shared-current-image')
        result = m.reclaim_obsolete_releases()
        self.assertEqual(result['status'], 'maintenance-pending')
        self.assertEqual(result['reason'], 'release_retention_evidence_unavailable')
        self.assertEqual(result['removed'], [])
        self.assertTrue(all((self.root / 'releases' / n).is_dir() for n in self.names))

    def test_cursor_mismatch_never_calls_retention(self):
        with patch.object(m, 'current_sha', return_value='b' * 40), patch.object(m, '_release_retention_helpers') as helpers:
            with self.assertRaisesRegex(RuntimeError, 'differs'):
                m.reclaim_obsolete_releases()
            helpers.assert_not_called()


if __name__ == '__main__':
    unittest.main()
