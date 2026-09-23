import importlib.util
import io
import json
import os
from pathlib import Path
import signal
from types import SimpleNamespace
import tempfile
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location('ops_config', Path(__file__).with_name('configure-ops-runtime.py'))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
SHA = 'a' * 40
OTHER_SHA = 'b' * 40
URL = b'https://open.feishu.cn/open-apis/bot/v2/hook/00000000-0000-0000-0000-000000000000\n'


class OpsRuntimeConfigurationTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        for key, value in {'ENV_FILE': self.root / 'runtime.env', 'CURRENT': self.root / 'current', 'RELEASES': self.root / 'releases', 'LOCK': self.root / 'install-release.lock', 'RECOVERY': self.root / 'recovery.json', 'PROOF': self.root / 'target.json', 'CREDENTIAL': self.root / 'webhook'}.items():
            patch = mock.patch.object(module, key, value)
            patch.start()
            self.addCleanup(patch.stop)
        module.CURRENT.symlink_to(module.RELEASES / SHA)
        source = mock.patch.object(module, 'SOURCE', module.RELEASES / SHA / 'deploy/configure-ops-runtime.py')
        source.start()
        self.addCleanup(source.stop)
        self.original = b'# exact\r\nUNRELATED=fixture-private\r\nAICRM_OPS_ENABLED=false\n'
        self.updated = module.render(self.original, True, False)
        module.ENV_FILE.write_bytes(self.original)
        module.ENV_FILE.chmod(0o600)
        self.owner = module.ENV_FILE.stat()
        # CI also runs without root; real filesystem operations/modes/durability
        # remain exercised while only the privileged chown/expected uid is mapped.
        self.real_read = module.read_private
        reader = mock.patch.object(module, 'read_private', side_effect=lambda path, uid=0, maximum=1048576: self.real_read(path, uid=os.getuid(), maximum=maximum))
        reader.start()
        self.addCleanup(reader.stop)
        chown = mock.patch.object(module.os, 'fchown')
        chown.start()
        self.addCleanup(chown.stop)
        account = mock.patch.object(module.pwd, 'getpwnam', return_value=SimpleNamespace(pw_uid=os.getuid()))
        account.start()
        self.addCleanup(account.stop)

    def apply(self, restart=lambda: None, verify=lambda sha: None, rollback_verify=lambda sha: None):
        module.apply(SHA, self.original, self.updated, self.owner, restart, verify, rollback_verify)

    def recover(self, verify=lambda sha: None):
        module.recover(SHA, lambda: None, verify)

    def test_gates_preserve_unrelated_bytes_and_reject_ambiguous_syntax(self):
        self.assertIn(b'# exact\r\nUNRELATED=fixture-private\r\n', self.updated)
        self.assertEqual(self.updated.count(b'AICRM_OPS_ENABLED='), 1)
        self.assertIn(b'AICRM_OPS_RETENTION_ENABLED=false\n', self.updated)
        for value in (self.original + b'AICRM_OPS_ENABLED=true\n', b'PRIVATE_VALUE="first\nAICRM_OPS_ENABLED=literal\nlast"\n', b'PRIVATE_VALUE=continued\\\nnext\n'):
            with self.assertRaises(ValueError):
                module.render(value, True, False)
        with self.assertRaises(ValueError):
            module.render(self.original, False, True)

    def test_success_journal_is_durable_before_environment_mutation(self):
        real_replace, real_sync = module.replace, module.sync_directory
        seen = []
        def synced(path):
            seen.append('fsync')
            real_sync(path)
        def replaced(path, content, gid=0):
            self.assertTrue(module.RECOVERY.exists())
            self.assertIn('fsync', seen)
            self.assertEqual(module.RECOVERY.stat().st_mode & 0o777, 0o600)
            self.assertEqual(module.RECOVERY.stat().st_nlink, 1)
            self.assertEqual(module.ENV_FILE.read_bytes(), self.original)
            real_replace(path, content, gid)
        with mock.patch.object(module, 'sync_directory', side_effect=synced), mock.patch.object(module, 'replace', side_effect=replaced):
            self.apply(verify=lambda sha: self.assertTrue(module.RECOVERY.exists()))
        self.assertEqual(module.ENV_FILE.read_bytes(), self.updated)
        self.assertFalse(module.RECOVERY.exists())

    def test_first_replace_committed_then_fsync_failure_restores_and_verifies(self):
        real_replace = module.replace
        calls = []
        def failed_once(path, content, gid=0):
            real_replace(path, content, gid)
            calls.append(content)
            if len(calls) == 1:
                raise OSError('fixture directory fsync failed after replacement')
        events = []
        with mock.patch.object(module, 'replace', side_effect=failed_once):
            with self.assertRaisesRegex(RuntimeError, 'original runtime verified'):
                self.apply(restart=lambda: events.append('restart'), rollback_verify=lambda sha: events.append('restored-ready'))
        self.assertEqual(calls, [self.updated, self.original])
        self.assertEqual(events, ['restart', 'restored-ready'])
        self.assertEqual(module.ENV_FILE.read_bytes(), self.original)
        self.assertFalse(module.RECOVERY.exists())

    def test_sigint_and_sigterm_compensate_after_update(self):
        for signum in (signal.SIGINT, signal.SIGTERM):
            with self.subTest(signal=signum):
                prior = signal.signal(signum, module.interrupted)
                calls = []
                def stopped():
                    calls.append('restart')
                    if len(calls) == 1:
                        os.kill(os.getpid(), signum)
                try:
                    with self.assertRaisesRegex(RuntimeError, 'original runtime verified'):
                        self.apply(restart=stopped)
                finally:
                    signal.signal(signum, prior)
                self.assertEqual(module.ENV_FILE.read_bytes(), self.original)
                self.assertEqual(len(calls), 2)
                self.assertFalse(module.RECOVERY.exists())

    def test_failed_rollback_readiness_keeps_recovery_for_retry(self):
        def unavailable(sha):
            raise RuntimeError('fixture unavailable')
        with self.assertRaisesRegex(RuntimeError, 'fixture unavailable'):
            self.apply(verify=unavailable, rollback_verify=unavailable)
        self.assertEqual(module.ENV_FILE.read_bytes(), self.original)
        self.assertTrue(module.RECOVERY.exists())
        self.recover()
        self.assertFalse(module.RECOVERY.exists())

    def test_termination_is_not_swallowed_by_readiness_retry(self):
        previous = signal.signal(signal.SIGTERM, module.interrupted)
        opener = mock.Mock()
        opener.open.side_effect = lambda *args, **kwargs: os.kill(os.getpid(), signal.SIGTERM)
        try:
            with mock.patch.object(module.urllib.request, 'build_opener', return_value=opener), mock.patch.object(module.time, 'sleep') as sleep:
                with self.assertRaisesRegex(RuntimeError, 'original runtime verified'):
                    self.apply(verify=module.verify)
                sleep.assert_not_called()
        finally:
            signal.signal(signal.SIGTERM, previous)
        self.assertEqual(module.ENV_FILE.read_bytes(), self.original)
        self.assertFalse(module.RECOVERY.exists())

    @unittest.skipUnless(hasattr(os, 'fork'), 'POSIX kill9 crash verification')
    def test_kill9_leaves_protected_journal_and_next_enable_refuses(self):
        pid = os.fork()
        if pid == 0:
            try:
                module.create_recovery(SHA, self.original, self.updated, self.owner.st_gid)
                module.replace(module.ENV_FILE, self.updated, self.owner.st_gid)
                os.kill(os.getpid(), signal.SIGKILL)
            finally:
                os._exit(99)
        _, status = os.waitpid(pid, 0)
        self.assertEqual(os.WTERMSIG(status), signal.SIGKILL)
        self.assertTrue(module.RECOVERY.exists())
        real_fstat = os.fstat
        def root_fstat(fd):
            fields = list(real_fstat(fd))
            fields[4] = 0
            return os.stat_result(fields)
        with mock.patch.object(module.os, 'geteuid', return_value=0), mock.patch.object(module.os, 'fstat', side_effect=root_fstat), mock.patch('sys.argv', ['ops', '--sha', SHA, '--mode', 'enable']), mock.patch.object(module, 'validate_target') as target:
            with self.assertRaisesRegex(ValueError, 'recovery required'):
                module.main()
            target.assert_not_called()
        self.assertEqual(module.ENV_FILE.read_bytes(), self.updated)
        self.recover()
        self.assertEqual(module.ENV_FILE.read_bytes(), self.original)
        self.assertFalse(module.RECOVERY.exists())

    @unittest.skipUnless(hasattr(os, 'fork'), 'POSIX journal publication crash verification')
    def test_kill9_at_journal_publication_keeps_single_link_recoverable_record(self):
        pid = os.fork()
        if pid == 0:
            real_rename = os.rename
            def interrupted_publish(source, destination):
                real_rename(source, destination)
                os.kill(os.getpid(), signal.SIGKILL)
            try:
                with mock.patch.object(module.os, 'rename', side_effect=interrupted_publish):
                    module.create_recovery(SHA, self.original, self.updated, self.owner.st_gid)
            finally:
                os._exit(99)
        _, status = os.waitpid(pid, 0)
        self.assertEqual(os.WTERMSIG(status), signal.SIGKILL)
        self.assertEqual(module.RECOVERY.stat().st_nlink, 1)
        self.assertEqual(module.ENV_FILE.read_bytes(), self.original)
        self.recover()
        self.assertFalse(module.RECOVERY.exists())

    def test_only_installed_matching_release_helper_is_allowed(self):
        module.assert_source(SHA)
        with self.assertRaisesRegex(RuntimeError, 'installed release helper'):
            module.assert_source(OTHER_SHA)
        with mock.patch.object(module, 'SOURCE', self.root / 'copied-helper.py'):
            with self.assertRaisesRegex(RuntimeError, 'installed release helper'):
                module.assert_source(SHA)

    def test_recover_rejects_other_release_and_third_party_env(self):
        module.create_recovery(SHA, self.original, self.updated, self.owner.st_gid)
        module.CURRENT.unlink()
        module.CURRENT.symlink_to(module.RELEASES / OTHER_SHA)
        restart = mock.Mock()
        with self.assertRaisesRegex(ValueError, 'same release'):
            module.recover(OTHER_SHA, restart, lambda sha: None)
        with self.assertRaisesRegex(RuntimeError, 'release changed'):
            module.recover(SHA, restart, lambda sha: None)
        restart.assert_not_called()
        self.assertEqual(module.ENV_FILE.read_bytes(), self.original)
        module.CURRENT.unlink()
        module.CURRENT.symlink_to(module.RELEASES / SHA)
        module.ENV_FILE.write_bytes(b'UNRELATED=changed-by-another-operator\n')
        with self.assertRaisesRegex(ValueError, 'outside recovery'):
            self.recover()
        self.assertTrue(module.RECOVERY.exists())

    def test_recovery_record_cannot_be_overwritten_or_silently_corrupted(self):
        module.create_recovery(SHA, self.original, self.updated, self.owner.st_gid)
        before = module.RECOVERY.read_bytes()
        with self.assertRaises(FileExistsError):
            module.create_recovery(SHA, b'OTHER=true\n', self.updated, self.owner.st_gid)
        self.assertEqual(module.RECOVERY.read_bytes(), before)
        record = json.loads(before)
        record['original_sha256'] = '0' * 64
        module.RECOVERY.write_text(json.dumps(record))
        with self.assertRaisesRegex(ValueError, 'digest invalid'):
            self.recover()
        self.assertEqual(module.ENV_FILE.read_bytes(), self.original)

    def test_worker_wrong_executable_or_deleted_binary_is_rejected(self):
        expected = str(module.RELEASES / SHA / 'bin/aicrm')
        def run(args, **kwargs):
            return SimpleNamespace(stdout='100' if args[2] == module.SERVICES[0] else '200')
        with mock.patch.object(module.subprocess, 'run', side_effect=run):
            for wrong in (str(module.RELEASES / OTHER_SHA / 'bin/aicrm'), expected + ' (deleted)'):
                with self.subTest(executable=wrong), mock.patch.object(module.os, 'readlink', side_effect=lambda path: expected if path == '/proc/100/exe' else wrong):
                    with self.assertRaisesRegex(RuntimeError, 'release mismatch'):
                        module.verify_processes(SHA)
            with mock.patch.object(module.os, 'readlink', return_value=expected):
                module.verify_processes(SHA)

    def set_proof(self, credential=URL, **changes):
        module.CREDENTIAL.write_bytes(credential)
        module.CREDENTIAL.chmod(0o600)
        proof = {'version': 1, 'target_ref': module.TARGET, 'credential_sha256': module.digest(credential)}
        proof.update(changes)
        module.PROOF.write_text(json.dumps(proof))
        module.PROOF.chmod(0o600)

    def test_original_target_proof_binds_exact_credential_bytes(self):
        self.set_proof()
        module.validate_target()
        for changes in ({'target_ref': 'wrong-group'}, {'credential_sha256': '0' * 64}, {'version': True}, {'unexpected': 'value'}):
            with self.subTest(proof=changes):
                self.set_proof(**changes)
                with self.assertRaises(ValueError):
                    module.validate_target()
        self.set_proof()
        module.CREDENTIAL.write_bytes(URL.rstrip())
        with self.assertRaisesRegex(ValueError, 'credential mismatch'):
            module.validate_target()

    def test_official_endpoint_allowlist_rejects_proof_with_wrong_url(self):
        for value in (URL.replace(b'open.feishu.cn', b'example.com'), URL.replace(b'https:', b'http:'), URL.replace(b'open.feishu.cn', b'open.feishu.cn:443'), URL.rstrip() + b'?extra=1', URL.rstrip() + b'#fragment', URL.replace(b'/hook/', b'/other/'), URL.replace(b'feishu', b'fei\tshu')):
            with self.subTest(endpoint=value):
                self.set_proof(value)
                with self.assertRaisesRegex(ValueError, 'endpoint invalid'):
                    module.validate_target()

    def test_private_file_refuses_symlink_hardlink_wrong_owner_and_permissions(self):
        path = self.root / 'private'
        path.write_bytes(b'fixture-private')
        path.chmod(0o600)
        self.real_read(path, uid=os.getuid())
        with self.assertRaises(ValueError):
            self.real_read(path, uid=os.getuid() + 1)
        path.chmod(0o640)
        with self.assertRaises(ValueError):
            self.real_read(path, uid=os.getuid())
        path.chmod(0o600)
        linked = self.root / 'linked'
        linked.symlink_to(path)
        with self.assertRaises(OSError):
            self.real_read(linked, uid=os.getuid())
        linked.unlink()
        os.link(path, linked)
        with self.assertRaises(ValueError):
            self.real_read(path, uid=os.getuid())

    def test_failed_new_proof_does_not_block_old_runtime_recovery(self):
        def fail_new_proof(sha):
            raise ValueError('new credential no longer matches')
        recovered = []
        with self.assertRaisesRegex(RuntimeError, 'original runtime verified'):
            self.apply(verify=fail_new_proof, rollback_verify=lambda sha: recovered.append(sha))
        self.assertEqual(recovered, [SHA])
        self.assertEqual(module.ENV_FILE.read_bytes(), self.original)
        self.assertFalse(module.RECOVERY.exists())


if __name__ == '__main__':
    unittest.main()
