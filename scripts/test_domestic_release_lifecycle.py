"""Artifact lifecycle uses the serial lock and preserves recoverable evidence."""
import json
import os
import pwd
import shutil
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

from scripts import domestic_main_release as release
from scripts.test_domestic_release_controller import make_repository


class CheckLifecycleTest(unittest.TestCase):
    def fixture(self, root):
        work = root / 'control/work'; work.mkdir(parents=True)
        build = root / 'build-worker'; (build/'domestic-main-checks').mkdir(parents=True)
        policy = work / 'trusted-policy'; policy.mkdir()
        config = {'work_root':str(work), 'lock':str(root/'control/controller.lock'),
                  'check_database_url':'postgres://synthetic@localhost/aicrm_test_lifecycle_acceptance_test'}
        return work, build, policy, config

    def test_policy_recovery_preserves_main_current_pending_and_authoritative_history(self):
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw); work, _, policies, config = self.fixture(root)
            repo, base, candidate, other = make_repository(root)
            for sha in (base, candidate, other):
                subprocess.run(['git', '--git-dir='+str(repo), 'worktree', 'add', '--detach',
                                str(policies/sha), sha], capture_output=True, check=True)
            with patch.object(release, '_assert_build_account_idle'), release._locked(Path(config['lock'])):
                ledger = release._lifecycle_directory(config)
                pending = ledger/'pending-attempt.json'
                release.atomic_json(pending, {'status':'evidence_saved_cleanup_pending','policy':str(policies/other)})
                result = release._reclaim_policy_worktrees(config, repo, base)
                self.assertEqual([r['sha'] for r in result['removed']], [candidate])
                self.assertTrue((policies/base).is_dir())
                self.assertTrue((policies/other).is_dir())
                self.assertFalse((policies/candidate).exists())
                self.assertEqual(json.loads(Path(result['proof']).read_text())['removed'], result['removed'])
                release.atomic_json(pending, {'status':'reclaimed','policy':str(policies/other)})
                self.assertEqual([r['sha'] for r in release._reclaim_policy_worktrees(config, repo, base)['removed']], [other])
            for sha in (base, candidate, other):
                subprocess.run(['git', '--git-dir='+str(repo), 'cat-file', '-e', sha+'^{commit}'], check=True)
            self.assertEqual(release._resolve_ref(repo, 'refs/heads/main'), base)
            self.assertEqual(release._resolve_ref(repo, 'refs/heads/codex/one'), candidate)
            with self.assertRaisesRegex(release.ReleaseError, 'serial'):
                release._reclaim_policy_worktrees(config, repo, base)

    def test_policy_recovery_validates_all_sources_before_deletion(self):
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw); _, _, policies, config = self.fixture(root)
            repo, base, candidate, other = make_repository(root)
            for sha in (candidate, other):
                subprocess.run(['git', '--git-dir='+str(repo), 'worktree', 'add', '--detach',
                                str(policies/sha), sha], capture_output=True, check=True)
            with patch.object(release, '_assert_build_account_idle'), release._locked(Path(config['lock'])):
                dirty = policies/other/'main.txt'; original = dirty.read_bytes(); dirty.write_text('unaccounted edit')
                with self.assertRaisesRegex(release.CheckEnvironmentError, 'changed policy'):
                    release._reclaim_policy_worktrees(config, repo, base)
                self.assertTrue((policies/candidate).exists())
                self.assertEqual(dirty.read_text(), 'unaccounted edit')
                dirty.write_bytes(original)
                unknown = policies/('a'*40); unknown.mkdir(); (unknown/'main.txt').write_text('unregistered source')
                with self.assertRaisesRegex(release.CheckEnvironmentError, 'unregistered'):
                    release._reclaim_policy_worktrees(config, repo, base)
                self.assertTrue((policies/candidate).exists())
                shutil.rmtree(unknown); unknown.symlink_to(policies/candidate, target_is_directory=True)
                with self.assertRaisesRegex(release.CheckEnvironmentError, 'unrecognized'):
                    release._reclaim_policy_worktrees(config, repo, base)
                self.assertTrue((policies/candidate).exists())

    def test_policy_recovery_retains_existing_archive_index_and_rejects_unsafe_metadata(self):
        with tempfile.TemporaryDirectory() as raw:
            root=Path(raw); _, _, policies, config=self.fixture(root)
            repo,base,candidate,other=make_repository(root)
            def checkout():
                subprocess.run(['git','--git-dir='+str(repo),'worktree','add','--detach',
                                str(policies/candidate),candidate],capture_output=True,check=True)
            checkout(); marker=policies/(other+'.archived.json')
            record={'original_path':str(policies/other),'archive':str(root/'evidence.tar.zst'),
                    'raw_logs_preserved':True,'ended_source_and_artifact_archive':True}
            release.atomic_json(marker,record); original=marker.read_bytes()
            with patch.object(release,'_assert_build_account_idle'),release._locked(Path(config['lock'])):
                result=release._reclaim_policy_worktrees(config,repo,base)
                self.assertEqual(result['archive_indexes'],[str(marker.resolve())])
                self.assertEqual(marker.read_bytes(),original)
                self.assertFalse((policies/candidate).exists())
                checkout()
                for invalid in ({**record,'original_path':str(policies/candidate)},
                                {**record,'raw_logs_preserved':False}, []):
                    release.atomic_json(marker,invalid)
                    with self.assertRaisesRegex(release.CheckEnvironmentError,'archive index'):
                        release._reclaim_policy_worktrees(config,repo,base)
                    self.assertTrue((policies/candidate).exists())
                release.atomic_json(marker,record);marker.chmod(0o644)
                with self.assertRaisesRegex(release.CheckEnvironmentError,'unsafe policy archive'):
                    release._reclaim_policy_worktrees(config,repo,base)
                self.assertTrue((policies/candidate).exists())
                marker.unlink(); marker.symlink_to(root/'unknown.json')
                with self.assertRaisesRegex(release.CheckEnvironmentError,'unsafe policy archive'):
                    release._reclaim_policy_worktrees(config,repo,base)
                self.assertTrue((policies/candidate).exists())

    def test_ten_distinct_policy_baselines_leave_only_main_and_current_checkout(self):
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw); _, _, policies, config = self.fixture(root)
            repo, base, _, _ = make_repository(root); source=root/'source'
            subprocess.run(['git', '--git-dir='+str(repo), 'worktree', 'add', '--detach',
                            str(policies/base), base], capture_output=True, check=True)
            with patch.object(release, '_assert_build_account_idle'), release._locked(Path(config['lock'])):
                history=[]
                for number in range(10):
                    (source/'round.txt').write_text(str(number))
                    subprocess.run(['git', '-C', str(source), 'add', 'round.txt'], check=True)
                    subprocess.run(['git', '-C', str(source), 'commit', '-m', 'round '+str(number)], capture_output=True, check=True)
                    sha=release._worktree_git(source, 'rev-parse', 'HEAD');history.append(sha)
                    subprocess.run(['git', '--git-dir='+str(repo), 'fetch', str(source),
                                    'HEAD:refs/heads/codex/policy-history'], capture_output=True, check=True)
                    subprocess.run(['git', '--git-dir='+str(repo), 'worktree', 'add', '--detach',
                                    str(policies/sha), sha], capture_output=True, check=True)
                    release._reclaim_policy_worktrees(config, repo, sha)
                    self.assertEqual({p.name for p in policies.iterdir()}, {base,sha})
                for sha in history:
                    subprocess.run(['git', '--git-dir='+str(repo), 'cat-file', '-e', sha+'^{commit}'], check=True)

    def test_interrupted_policy_removal_recovers_metadata_without_losing_unreferenced_source(self):
        with tempfile.TemporaryDirectory() as raw:
            root=Path(raw); _, _, policies, config=self.fixture(root)
            repo, base, candidate, other=make_repository(root)
            for sha in (candidate,other):
                subprocess.run(['git','--git-dir='+str(repo),'worktree','add','--detach',
                                str(policies/sha),sha],capture_output=True,check=True)
            # Model interruption after files disappear but before Git's
            # worktree registry is removed; the next start must repair it.
            shutil.rmtree(policies/candidate)
            subprocess.run(['git','--git-dir='+str(repo),'update-ref','-d','refs/heads/codex/two'],check=True)
            with release._locked(Path(config['lock'])):
                with patch.object(release,'_assert_build_account_idle',side_effect=release.CheckEnvironmentError('live process')):
                    with self.assertRaisesRegex(release.CheckEnvironmentError,'live process'):
                        release._reclaim_policy_worktrees(config,repo,base)
                with patch.object(release,'_assert_build_account_idle'):
                    result=release._reclaim_policy_worktrees(config,repo,base)
            self.assertEqual([r['sha'] for r in result['removed']],[candidate])
            self.assertTrue((policies/other).exists())
            self.assertEqual(len(result['retained']),1)
            registry=release._git(repo,'worktree','list','--porcelain')
            self.assertNotIn(str((policies/candidate).resolve()),registry)
            subprocess.run(['git','--git-dir='+str(repo),'worktree','add','--detach',
                            str(policies/candidate),candidate],capture_output=True,check=True)

    def test_ten_rounds_success_failure_timeout_and_interrupt_leave_no_generated_growth(self):
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw); work,build,policy,config = self.fixture(root)
            with patch.object(release.legacy, 'BUILD_ROOT', build), release._locked(Path(config['lock'])):
                for number in range(10):
                    report = build/'domestic-main-checks'/('b'*40+'-attempt-'+str(number)); report.mkdir()
                    (report/'run.json').write_text(json.dumps({'round':number}))
                    error = [None, RuntimeError('assertion'), subprocess.TimeoutExpired(['go'],1), KeyboardInterrupt()][number%4]
                    try:
                        with release._check_attempt_directory(config,report,policy,'b'*40) as directory:
                            temporary = Path(directory)
                            (temporary/'checkout').mkdir(); (temporary/'checkout/generated').write_bytes(b'x'*65536)
                            (report/'.preparation').mkdir(mode=0o700)
                            (report/'.preparation/preparation.jsonl').write_text('{"kind":"npm"}\n')
                            snapshot = report / '.preparation' / ('artifact-' + 'a' * 64)
                            snapshot.mkdir(); (snapshot / 'receipt.json').write_text(json.dumps({'round': number}))
                            (report/'.preparation/snapshot').write_bytes(b'y'*65536)
                            (report/'backend/.venv').mkdir(parents=True); (report/'backend/.venv/runtime').write_bytes(b'z'*65536)
                            if error: raise error
                    except (RuntimeError, subprocess.TimeoutExpired, KeyboardInterrupt):
                        pass
                    self.assertFalse(temporary.exists())
                    self.assertFalse((report/'.preparation').exists())
                    self.assertFalse((report/'backend/.venv').exists())
                    self.assertTrue((report/'run.json').exists())
                    receipt = work / 'check-lifecycle' / (report.name + '-attempt-preparation') / ('artifact-' + 'a' * 64 + '-receipt.json')
                    self.assertEqual(json.loads(receipt.read_text()), {'round': number})
                    self.assertEqual(receipt.stat().st_mode & 0o777, 0o600)
                    self.assertEqual(list(work.glob('domestic-main-check-*')), [])
                records = list((work/'check-lifecycle').glob('*-attempt.json'))
                self.assertEqual(len(records),10)
                self.assertTrue(all(json.loads(path.read_text())['status']=='reclaimed' for path in records))

    def test_cleanup_requires_serial_lock_and_rejects_authority_or_symlink_paths(self):
        with tempfile.TemporaryDirectory() as raw:
            root=Path(raw); work,build,policy,config=self.fixture(root)
            with self.assertRaisesRegex(release.ReleaseError,'serial'):
                release._lifecycle_directory(config)
            with patch.object(release.legacy,'BUILD_ROOT',build), release._locked(Path(config['lock'])):
                directory=release._lifecycle_directory(config)
                report=build/'domestic-main-checks'/('b'*40+'-attempt-1'); report.mkdir()
                authority=work/'authority.git'; authority.mkdir()
                record={'temporary':str(authority), 'report':str(report),'head_sha':'b'*40}
                with self.assertRaisesRegex(release.ReleaseError,'registered'):
                    release._cleanup_attempt_record(config,directory/'record.json',record)
                linked=work/'domestic-main-check-link'; linked.symlink_to(authority,target_is_directory=True)
                record['temporary']=str(linked)
                with self.assertRaisesRegex(release.ReleaseError,'symlink'):
                    release._cleanup_attempt_record(config,directory/'record.json',record)
                self.assertTrue(authority.exists())

    def test_next_start_reclaims_only_dead_registered_attempt_and_keeps_live_one(self):
        with tempfile.TemporaryDirectory() as raw:
            root=Path(raw); work,build,policy,config=self.fixture(root)
            with patch.object(release.legacy,'BUILD_ROOT',build), release._locked(Path(config['lock'])):
                directory=release._lifecycle_directory(config)
                for pid, suffix in [(10001,'dead'),(10002,'live')]:
                    temporary=work/('domestic-main-check-'+suffix); temporary.mkdir()
                    report=build/'domestic-main-checks'/('b'*40+'-'+suffix); report.mkdir()
                    release.atomic_json(directory/(suffix+'-attempt.json'), {'pid':pid,'status':'active',
                        'temporary':str(temporary),'report':str(report),'head_sha':'b'*40})
                with patch.object(release,'_process_alive',return_value=False), \
                     patch.object(release.subprocess,'run',return_value=subprocess.CompletedProcess([],0,'')):
                    recovered=release._recover_check_attempts(config)
                self.assertEqual(len(recovered),2)
                self.assertFalse((work/'domestic-main-check-dead').exists())
                (work/'domestic-main-check-live').mkdir()
                with patch.object(release,'_process_alive',return_value=False), \
                     patch.object(release.subprocess,'run',return_value=subprocess.CompletedProcess([],0,'20001\n')):
                    with self.assertRaisesRegex(release.CheckEnvironmentError,'live processes'):
                        release._recover_check_attempts(config)
                self.assertTrue((work/'domestic-main-check-live').exists())

    def test_real_python_survivor_blocks_cleanup_and_next_check(self):
        import sys
        with tempfile.TemporaryDirectory() as raw:
            root=Path(raw);work,build,policy,config=self.fixture(root)
            directory=work/'domestic-main-check-orphan';directory.mkdir()
            report=build/'domestic-main-checks'/('b'*40+'-orphan');report.mkdir()
            worker=subprocess.Popen([sys.executable,'-c','import time;time.sleep(120)'],cwd=directory)
            try:
                with patch.object(release.legacy,'BUILD_ROOT',build),release._locked(Path(config['lock'])):
                    registry=release._lifecycle_directory(config)
                    release.atomic_json(registry/'orphan-attempt.json',{'pid':99999999,'status':'active',
                        'temporary':str(directory),'report':str(report),'head_sha':'b'*40})
                    with patch.object(release.subprocess,'run',return_value=subprocess.CompletedProcess([],0,str(worker.pid)+'\n')):
                        with self.assertRaises(release.CheckEnvironmentError):release._recover_check_attempts(config)
                    self.assertTrue(directory.exists());self.assertIsNone(worker.poll())
                    worker.terminate();worker.wait(timeout=10)
                    with patch.object(release.subprocess,'run',return_value=subprocess.CompletedProcess([],1,'','')):
                        self.assertEqual(len(release._recover_check_attempts(config)),1)
                    self.assertFalse(directory.exists())
            finally:
                if worker.poll() is None:worker.terminate();worker.wait(timeout=10)

    def test_live_controller_protects_all_attempts_before_any_reclamation(self):
        with tempfile.TemporaryDirectory() as raw:
            root=Path(raw);work,build,policy,config=self.fixture(root)
            with patch.object(release.legacy,'BUILD_ROOT',build),release._locked(Path(config['lock'])):
                registry=release._lifecycle_directory(config)
                for suffix,pid in [('first-dead',99999998),('last-live',os.getpid())]:
                    temporary=work/('domestic-main-check-'+suffix);temporary.mkdir()
                    report=build/'domestic-main-checks'/('b'*40+'-'+suffix);report.mkdir()
                    release.atomic_json(registry/(suffix+'-attempt.json'),{'pid':pid,'status':'active',
                        'temporary':str(temporary),'report':str(report),'head_sha':'b'*40})
                with patch.object(release.subprocess,'run',return_value=subprocess.CompletedProcess([],1,'','')):
                    with self.assertRaisesRegex(release.CheckEnvironmentError,'controller'):
                        release._recover_check_attempts(config)
                self.assertTrue((work/'domestic-main-check-first-dead').is_dir())

    def test_short_scratch_registered_before_creation_and_reclaimed_on_interrupt(self):
        with tempfile.TemporaryDirectory() as raw:
            root=Path(raw);work,build,policy,config=self.fixture(root)
            report=build/'domestic-main-checks'/('b'*40+'-scratch');report.mkdir()
            scratch=None
            with patch.object(release.legacy,'BUILD_ROOT',build),release._locked(Path(config['lock'])):
                with self.assertRaises(KeyboardInterrupt):
                    with release._check_attempt_directory(config,report,policy,'b'*40):
                        scratch=Path(config['_check_tmpdir'])
                        record=json.loads(next((work/'check-lifecycle').glob('*-attempt.json')).read_text())
                        self.assertEqual(record['scratch'],str(scratch));self.assertTrue(scratch.is_dir())
                        self.assertLess(len(str(scratch).encode()),59)
                        (scratch/'profile').mkdir();raise KeyboardInterrupt()
                self.assertFalse(scratch.exists());self.assertNotIn('_check_tmpdir',config)

    def test_cleanup_retains_registered_resources_while_build_children_survive(self):
        with tempfile.TemporaryDirectory() as raw:
            root=Path(raw);work,build,policy,config=self.fixture(root)
            temporary=work/'domestic-main-check-survivor';temporary.mkdir()
            report=build/'domestic-main-checks'/('b'*40+'-survivor');report.mkdir()
            record={'guard_build_processes':True,'temporary':str(temporary),'report':str(report),'head_sha':'b'*40}
            with patch.object(release.legacy,'BUILD_ROOT',build),release._locked(Path(config['lock'])), \
                 patch.object(release.subprocess,'run',return_value=subprocess.CompletedProcess([],0,'20001\n')):
                with self.assertRaisesRegex(release.CheckEnvironmentError,'live processes'):
                    release._cleanup_attempt_record(config,work/'attempt.json',record)
            self.assertTrue(temporary.is_dir())

    def test_recreated_checkout_uses_same_compile_path_and_retains_unknown_workspaces(self):
        with tempfile.TemporaryDirectory() as raw:
            root=Path(raw);work,build,policy,config=self.fixture(root)
            paths=[]
            with patch.object(release.legacy,'BUILD_ROOT',build),release._locked(Path(config['lock'])):
                for number in range(2):
                    report=build/'domestic-main-checks'/('b'*40+'-stable-'+str(number));report.mkdir()
                    with release._check_attempt_directory(config,report,policy,'b'*40) as temporary:
                        checkout=Path(temporary)/'execution/candidate';checkout.mkdir(parents=True)
                        self.assertFalse((checkout/'generated').exists())
                        (checkout/'generated').write_text('attempt-'+str(number));paths.append(str(checkout))
                    self.assertFalse(Path(temporary).exists())
                self.assertEqual(paths[0],paths[1])
                unknown=work/'domestic-main-check-current';unknown.mkdir();(unknown/'retain').write_text('diagnose')
                report=build/'domestic-main-checks'/('b'*40+'-stable-unknown');report.mkdir()
                with self.assertRaisesRegex(release.CheckEnvironmentError,'unreclaimed'):
                    with release._check_attempt_directory(config,report,policy,'b'*40):self.fail('unexpected execution')
                self.assertEqual((unknown/'retain').read_text(),'diagnose')

    def test_cleanup_blockage_preserves_original_failure_classification(self):
        with tempfile.TemporaryDirectory() as raw:
            root=Path(raw);work,build,policy,config=self.fixture(root)
            report=build/'domestic-main-checks'/('b'*40+'-mixed');report.mkdir()
            with patch.object(release.legacy,'BUILD_ROOT',build),release._locked(Path(config['lock'])), \
                 patch.object(release,'_cleanup_attempt_record',side_effect=release.CheckEnvironmentError('live child')):
                with self.assertRaisesRegex(release.CheckCandidateError,'business assertion'):
                    with release._check_attempt_directory(config,report,policy,'b'*40):
                        raise release.CheckCandidateError('business assertion')
                record=json.loads(next((work/'check-lifecycle').glob('*-attempt.json')).read_text())
                self.assertEqual(record['status'],'evidence_saved_cleanup_pending')
                self.assertTrue(Path(record['temporary']).exists())
                shutil.rmtree(Path(record['scratch']))  # fixture owns the retained mock resource

    def test_capacity_blocks_before_work_and_lru_cache_keeps_recent_content(self):
        with tempfile.TemporaryDirectory() as raw:
            root=Path(raw); work,build,policy,config=self.fixture(root)
            with self.assertRaisesRegex(release.CheckEnvironmentError,'not calibrated'):
                release._check_capacity(config,work)
            config['check_capacity']={'measured_at_utc':'fixture','workspace_peak_bytes':100,
                'database_reserve_bytes':100,'evidence_reserve_bytes':100,'cache_limits_bytes':{'go-build':8192}}
            with patch.object(release.shutil,'disk_usage',return_value=release.shutil._ntuple_diskusage(1000,900,100)):
                with self.assertRaisesRegex(release.CheckEnvironmentError,'insufficient staging disk'):
                    release._check_capacity(config,work)
            config['check_storage_capacity']=dict(config['check_capacity'],workspace_peak_bytes=1000)
            with patch.object(release.shutil,'disk_usage',return_value=release.shutil._ntuple_diskusage(1000,900,100)):
                with self.assertRaisesRegex(release.CheckEnvironmentError,'insufficient staging disk'):
                    release._check_capacity(config,build,'check_storage_capacity')
            cache=build/'cache/go-build'; cache.mkdir(parents=True)
            old=cache/'old-d'; old.write_bytes(b'x'*8192); os.utime(old,(1,1))
            new=cache/'new-d'; new.write_bytes(b'y'*8192)
            with patch.object(release.legacy,'BUILD_ROOT',build), release._locked(Path(config['lock'])):
                usage=release._trim_check_caches(config)
            self.assertFalse(old.exists()); self.assertTrue(new.exists())
            self.assertTrue(usage['go-build']['reused'])
            self.assertGreater(usage['go-build']['reclaimed_bytes'],0)

    def test_cache_no_eviction_measures_once_and_does_not_read_source_refs(self):
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw); _, build, _, config = self.fixture(root)
            roots = {name: build / 'cache' / name for name in ('go-build', 'go-mod', 'npm', 'preparation')}
            for name, directory in roots.items():
                directory.mkdir(parents=True, mode=0o700)
                destination = directory / ('artifact-' + 'a' * 64) if name == 'preparation' else directory / 'kept'
                if name == 'preparation':
                    destination.mkdir(); destination = destination / 'payload'
                destination.write_bytes(b'x' * 8192)
            measured = {name: release._allocated_bytes(directory) for name, directory in roots.items()}
            original_scan = release._allocated_bytes
            for limits in ({}, measured):
                with self.subTest(limits=limits), patch.object(release.legacy, 'BUILD_ROOT', build), \
                     patch.object(release.pwd, 'getpwnam', return_value=pwd.getpwuid(os.getuid())), \
                     patch.object(release, '_assert_build_account_idle'), \
                     patch.object(release, '_git', side_effect=AssertionError('no eviction needs no refs')), \
                     patch.object(release, '_allocated_bytes', wraps=original_scan) as scans, \
                     release._locked(Path(config['lock'])):
                    config['check_capacity'] = {'cache_limits_bytes': limits}
                    result = release._trim_check_caches(config)
                    self.assertEqual(scans.call_count, 4)
                    self.assertEqual({call.args[0] for call in scans.call_args_list}, set(roots.values()))
                    for name, usage in result.items():
                        self.assertEqual(usage['before_bytes'], measured[name])
                        self.assertEqual(usage['after_bytes'], measured[name])
                        self.assertEqual(usage['reclaimed_bytes'], 0)
            unknown = roots['preparation'] / 'unknown'; unknown.mkdir()
            with patch.object(release.legacy, 'BUILD_ROOT', build), \
                 patch.object(release.pwd, 'getpwnam', return_value=pwd.getpwuid(os.getuid())), \
                 patch.object(release, '_assert_build_account_idle'), release._locked(Path(config['lock'])):
                for limit in (None, measured['preparation'] * 10):
                    with self.assertRaisesRegex(release.CheckEnvironmentError, 'unrecognized'):
                        release._trim_preparation_cache(config, limit)

    def test_cache_no_eviction_still_rejects_invalid_limits(self):
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw); _, build, _, config = self.fixture(root)
            for name in ('go-build', 'go-mod', 'npm', 'preparation'):
                (build / 'cache' / name).mkdir(parents=True, mode=0o700)
            with patch.object(release.legacy, 'BUILD_ROOT', build), \
                 patch.object(release.pwd, 'getpwnam', return_value=pwd.getpwuid(os.getuid())), \
                 patch.object(release, '_assert_build_account_idle'), release._locked(Path(config['lock'])):
                for name in ('go-build', 'go-mod', 'npm', 'preparation'):
                    for bad in (0, -1, True, '8192'):
                        config['check_capacity'] = {'cache_limits_bytes': {name: bad}}
                        with self.subTest(name=name, bad=bad), self.assertRaises(release.CheckEnvironmentError):
                            release._trim_check_caches(config)

    def test_module_cache_over_budget_retains_referenced_units_and_checks_after_deletion(self):
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw); _, build, _, config = self.fixture(root)
            modules = build / 'cache/go-mod'
            protected = modules / 'example.org/keep@v1.0.0'
            retired = modules / 'example.org/old@v1.0.0'
            downloads = modules / 'cache/download/example.org/old/@v'
            for directory in (protected, retired, downloads):
                directory.mkdir(parents=True)
            (protected / 'source.go').write_bytes(b'p' * 8192)
            (retired / 'source.go').write_bytes(b'r' * 8192)
            (downloads / 'v1.0.0.zip').write_bytes(b'z' * 8192)
            before = release._allocated_bytes(modules)
            config.update(repo=str(root / 'authority.git'),
                          check_capacity={'cache_limits_bytes': {'go-mod': before - 8192}})
            original_scan = release._allocated_bytes
            with patch.object(release.legacy, 'BUILD_ROOT', build), release._locked(Path(config['lock'])), \
                 patch.object(release, '_git', return_value='refs/heads/main'), \
                 patch.object(release, '_run', return_value=subprocess.CompletedProcess([], 0, 'example.org/keep v1.0.0 hash\n')), \
                 patch.object(release, '_allocated_bytes', wraps=original_scan) as scans:
                usage = release._trim_check_caches(config)['go-mod']
                self.assertEqual(sum(call.args[0] == modules for call in scans.call_args_list), 2)
            self.assertTrue((protected / 'source.go').is_file())
            self.assertFalse(retired.exists())
            self.assertFalse((downloads / 'v1.0.0.zip').exists())
            self.assertEqual(usage['after_bytes'], release._allocated_bytes(modules))
            self.assertLessEqual(usage['after_bytes'], usage['limit_bytes'])

    def test_preparation_cache_requires_opt_in_and_uses_actual_private_build_account(self):
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw); _, build, _, config = self.fixture(root)
            account = pwd.getpwuid(os.getuid())
            def mkdir(cfg, command, **kwargs):
                if command[0] == '/usr/bin/chmod':
                    os.chmod(command[-1], 0o700)
                else:
                    Path(command[-1]).mkdir(mode=0o700, parents=True, exist_ok=True)
                return subprocess.CompletedProcess(command, 0, '', '')
            with patch.object(release.legacy, 'BUILD_ROOT', build), \
                 patch.object(release.pwd, 'getpwnam', return_value=account), \
                 patch.object(release, '_build_command', side_effect=mkdir) as execute:
                self.assertIsNone(release._check_preparation_cache(config))
                execute.assert_not_called()
                config['check_capacity'] = {'cache_limits_bytes': {'preparation': 1024 * 1024}}
                with self.assertRaisesRegex(release.ReleaseError, 'serial'):
                    release._check_preparation_cache(config)
                with release._locked(Path(config['lock'])):
                    cache = release._check_preparation_cache(config)
                    self.assertEqual(cache, build / 'cache/preparation')
                    cfg = dict(config, _check_preparation_cache=str(cache),
                               build_path='/opt/aicrm/toolchain/npm/bin:/usr/bin')
                    self.assertEqual(release._check_env(cfg)['AICRM_TEST_PREP_CACHE'], str(cache))
                    os.chmod(cache, 0o755)
                    self.assertEqual(release._check_preparation_cache(config), cache)
                    self.assertEqual(cache.stat().st_mode & 0o777, 0o700)
                    for bad in (0, True, -1):
                        config['check_capacity']['cache_limits_bytes']['preparation'] = bad
                        with self.assertRaisesRegex(release.CheckEnvironmentError, 'positive'):
                            release._check_preparation_cache(config)

    def test_preparation_cache_reclaims_whole_old_snapshots_and_preserves_writable_copy(self):
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw); _, build, _, config = self.fixture(root)
            cache = build / 'cache/preparation'; cache.mkdir(parents=True, mode=0o700)
            old = cache / ('npm-' + 'a' * 64); (old / 'modules').mkdir(parents=True)
            (old / 'modules/library.js').write_bytes(b'a' * 65536)
            copy = root / 'active-copy.js'; copy.write_text('private copy')
            (old / 'modules/link').symlink_to(copy)
            new = cache / ('artifact-' + 'b' * 64); new.mkdir(); (new / 'page.js').write_bytes(b'b' * 8192)
            (new / 'receipt.json').write_text('{"input":"verified"}')
            os.utime(old, (1, 1))
            config['check_capacity'] = {'cache_limits_bytes': {'preparation': 24576}}
            with patch.object(release.legacy, 'BUILD_ROOT', build), \
                 patch.object(release.pwd, 'getpwnam', return_value=pwd.getpwuid(os.getuid())), \
                 patch.object(release, '_assert_build_account_idle'), release._locked(Path(config['lock'])):
                result = release._trim_check_caches(config)['preparation']
            self.assertEqual(result['removed'], [old.name])
            self.assertLessEqual(result['after_bytes'], 24576)
            self.assertFalse(old.exists()); self.assertTrue((new / 'receipt.json').is_file())
            self.assertEqual(copy.read_text(), 'private copy')

    def test_preparation_cache_retains_all_entries_if_running_unknown_or_linked(self):
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw); _, build, _, config = self.fixture(root)
            cache = build / 'cache/preparation'; cache.mkdir(parents=True, mode=0o700)
            known = cache / ('artifact-' + 'a' * 64); known.mkdir(); (known / 'page.js').write_bytes(b'a' * 8192)
            protected = root / 'source.txt'; protected.write_text('protected')
            with patch.object(release.legacy, 'BUILD_ROOT', build), \
                 patch.object(release.pwd, 'getpwnam', return_value=pwd.getpwuid(os.getuid())):
                with self.assertRaisesRegex(release.ReleaseError, 'serial'):
                    release._trim_preparation_cache(config, 1)
                with release._locked(Path(config['lock'])), \
                     patch.object(release, '_assert_build_account_idle', side_effect=release.CheckEnvironmentError('running task')):
                    with self.assertRaisesRegex(release.CheckEnvironmentError, 'running'):
                        release._trim_preparation_cache(config, 1)
                with release._locked(Path(config['lock'])), patch.object(release, '_assert_build_account_idle'):
                    unknown = cache / 'unaccounted'; unknown.mkdir()
                    with self.assertRaisesRegex(release.CheckEnvironmentError, 'unrecognized'):
                        release._trim_preparation_cache(config, 1)
                    self.assertTrue(known.exists()); unknown.rmdir()
                    linked = cache / 'artifact.lock'; linked.symlink_to(protected)
                    with self.assertRaisesRegex(release.CheckEnvironmentError, 'unrecognized'):
                        release._trim_preparation_cache(config, 1)
                    self.assertTrue(known.exists()); self.assertEqual(protected.read_text(), 'protected')

    def test_missing_or_wrong_disk_blocks_before_root_disk_fallback(self):
        with tempfile.TemporaryDirectory() as raw:
            root=Path(raw)
            config={'check_storage_mount':raw,'check_storage_uuid':'expected','check_storage_paths':[raw]}
            with patch.object(release.os.path,'ismount',return_value=False):
                with self.assertRaisesRegex(release.CheckEnvironmentError,'fallback'):
                    release._check_storage_mount(config)
            with patch.object(release.os.path,'ismount',return_value=True), \
                 patch.object(release.subprocess,'run',return_value=subprocess.CompletedProcess([],0,'wrong\n')):
                with self.assertRaisesRegex(release.CheckEnvironmentError,'UUID'):
                    release._check_storage_mount(config)
            with patch.object(release.os.path,'ismount',return_value=True), \
                 patch.object(release.subprocess,'run',return_value=subprocess.CompletedProcess([],0,'expected\n')):
                self.assertEqual(release._check_storage_mount(config)['mode'],'data_disk')

    def test_cache_consolidation_verifies_bytes_and_retains_conflicting_old_entries(self):
        with tempfile.TemporaryDirectory() as raw:
            root=Path(raw); work,unused,policy,config=self.fixture(root)
            build=root/'domestic/build-worker'; build.mkdir(parents=True)
            origin=root/'cache/go-build'; origin.mkdir(parents=True)
            canonical=build/'cache/go-build'; canonical.mkdir(parents=True)
            (origin/'same-d').write_text('same'); (canonical/'same-d').write_text('same')
            (origin/'new-d').write_text('new')
            (origin/'conflict-a').write_text('old'); (canonical/'conflict-a').write_text('canonical')
            config['check_cache_migration_enabled']=True
            with patch.object(release.legacy,'BUILD_ROOT',build), \
                 patch.object(release.pwd,'getpwnam',return_value=pwd.getpwuid(os.getuid())), \
                 release._locked(Path(config['lock'])):
                result=release._merge_legacy_check_caches(config)
            self.assertFalse((origin/'same-d').exists()); self.assertFalse((origin/'new-d').exists())
            self.assertEqual((canonical/'new-d').read_text(),'new')
            self.assertTrue((origin/'conflict-a').exists())
            self.assertEqual((canonical/'conflict-a').read_text(),'canonical')
            self.assertEqual(result['status'],'retained_conflicts')
            self.assertTrue(Path(result['verified_log']).exists())

    def test_closed_evidence_is_removed_only_after_remote_archive_readback(self):
        with tempfile.TemporaryDirectory() as raw:
            root=Path(raw); work,build,policy,config=self.fixture(root)
            config.update(state=str(root/'state.json'),check_evidence_archive_enabled=True,
                          prod_user='fixture',prod_host='fixture')
            report=build/'domestic-main-checks'/('b'*40+'-attempt-1'); report.mkdir()
            (report/'first-failure.log').write_text('original failure evidence')
            (work/'diagnostics').mkdir()
            with patch.object(release.legacy,'BUILD_ROOT',build), release._locked(Path(config['lock'])):
                directory=release._lifecycle_directory(config)
                record=directory/(report.name+'-attempt.json')
                release.atomic_json(record,{'status':'reclaimed','head_sha':'b'*40,'report':str(report)})
                state={'queue':[{'head_sha':'b'*40,'status':'completed'}]}
                with patch.object(release,'_load_state',return_value=state), \
                     patch.object(release.legacy,'ssh_args',return_value=['ssh','fixture']), \
                     patch.object(release.legacy,'command') as upload, \
                     patch.object(release,'_production_ssh',return_value='0'*64+' file'):
                    with self.assertRaisesRegex(release.ReleaseError,'independent hash'):
                        release._archive_closed_check_evidence(config)
                self.assertTrue((report/'first-failure.log').exists())
                def remote(config,*args,**kwargs):
                    return release._file_sha256(directory/Path(args[-1]).name)+' file' if 'sha256sum' in args[2] else ''
                with patch.object(release,'_load_state',return_value=state), \
                     patch.object(release.legacy,'ssh_args',return_value=['ssh','fixture']), \
                     patch.object(release.legacy,'command'), \
                     patch.object(release,'_production_ssh',side_effect=remote):
                    archived=release._archive_closed_check_evidence(config)
                self.assertEqual(len(archived),1)
                self.assertFalse((report/'first-failure.log').exists())
                self.assertTrue(json.loads(record.read_text())['archive']['local_cleanup_complete'])
                mapping=json.loads((directory/(report.name+'-archive.json')).read_text())
                self.assertEqual(mapping['members'][0]['original_path'],str(report/'first-failure.log'))

    def test_current_rollback_pending_and_authority_references_survive_package_gc(self):
        with tempfile.TemporaryDirectory() as raw:
            root=Path(raw); work,build,policy,config=self.fixture(root)
            releases=root/'releases'; releases.mkdir()
            receipts=root/'domestic-receipts'; receipts.mkdir()
            names=['a'*40,'b'*40,'c'*40,'d'*40]
            for name in names:
                path=releases/name; path.mkdir(); (path/'binary').write_text(name)
                release.builder.write_release_inventory(path)
                (receipts/(name+'.json')).write_text(json.dumps({'source_sha':name,'previous_sha':names[1]}))
            (root/'current').symlink_to(releases/names[0],target_is_directory=True)
            duplicate=root/'domestic/builds'/names[0]/'release'
            duplicate.parent.mkdir(parents=True); shutil.copytree(releases/names[0],duplicate)
            (duplicate.parent/'domestic-release.json').write_text('retained metadata')
            config.update(state=str(root/'state.json'),repo=str(root/'authority.git'),stage_releases=str(releases))
            state={'installed_app':{'sha':names[0]},'main':{'sha':names[0]},'queue':[{'head_sha':names[2],'status':'pending'}]}
            with patch.object(release,'_load_state',return_value=state), \
                 patch.object(release,'_git',return_value=names[0]), \
                 patch.object(release.builder,'verify_release_inventory',wraps=release.builder.verify_release_inventory) as verify, \
                 release._locked(Path(config['lock'])):
                result=release._reclaim_unreferenced_packages(config)
                self.assertEqual(verify.call_count,3)  # current + duplicate + unreferenced history
            self.assertFalse(duplicate.exists())
            self.assertTrue((duplicate.parent/'domestic-release.json').exists())
            for name in names[:3]: self.assertTrue((releases/name).exists())
            self.assertFalse((releases/names[3]).exists())
            self.assertEqual(len(result['removed']),2)
            # A build ancestor link cannot redirect duplicate GC outside its root.
            protected=root/'protected-task'; (protected/'release').mkdir(parents=True)
            shutil.copytree(releases/names[0],protected/'release',dirs_exist_ok=True)
            (duplicate.parent/'domestic-release.json').unlink()
            duplicate.parent.rmdir()
            duplicate.parent.symlink_to(protected,target_is_directory=True)
            with patch.object(release,'_load_state',return_value=state), \
                 patch.object(release,'_git',return_value=names[0]), release._locked(Path(config['lock'])):
                release._reclaim_unreferenced_packages(config)
            self.assertTrue((protected/'release/binary').exists())
            duplicate.parent.unlink()
            with patch.object(release,'_load_state',return_value=state), \
                 patch.object(release,'_git',return_value=names[0]), \
                 patch.object(release.builder,'verify_release_inventory',side_effect=AssertionError('nothing eligible for cleanup')), \
                 release._locked(Path(config['lock'])):
                result=release._reclaim_unreferenced_packages(config)
                self.assertEqual(result['removed'],[])
                for name in names[:3]: self.assertTrue((releases/name).exists())

    def test_timeout_terminates_only_the_attempt_process_group_before_cleanup(self):
        process=unittest.mock.Mock(pid=424242)
        process.wait.side_effect=[subprocess.TimeoutExpired(['synthetic'],1),0]
        with patch.object(release.subprocess,'Popen',return_value=process) as spawn, \
             patch.object(release.os,'killpg') as kill:
            with self.assertRaises(subprocess.TimeoutExpired):
                release._run_check_process(['synthetic'],cwd=Path('/'),output=None,timeout=1)
        self.assertTrue(spawn.call_args.kwargs['start_new_session'])
        kill.assert_called_once_with(process.pid,release.signal.SIGTERM)
