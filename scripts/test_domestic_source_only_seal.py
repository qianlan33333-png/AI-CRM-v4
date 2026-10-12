"""Actual app evidence, source-only seal and cumulative maintenance contracts."""
from contextlib import ExitStack, nullcontext
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).parent))
import domestic_main_release as m
import test_domestic_release_controller as original

A, B, C, D = (letter * 40 for letter in 'abcd')
TREE, HASH = 'e' * 40, 'f' * 64


class SourceOnlySeal(unittest.TestCase):
    def fixture(self, root):
        member = lambda base, head, runtime: {
            'base_sha': base, 'head_sha': head, 'head_tree': TREE,
            'ref': 'refs/heads/codex/' + head[0], 'runtime_changed': runtime,
            'check_receipt_sha256': HASH, 'stage_receipt_sha256': HASH if runtime else None,
            'stage_journey_sha256': HASH if runtime else None, 'source_bundle_sha256': HASH,
            'stage_check_names': ['original-journey'] if runtime else [],
        }
        stage = {'source_sha': B, 'source_tree': TREE, 'manifest_sha256': HASH,
                 'technical_status': 'installed_healthy', 'installed_at_utc': '2026-10-12T01:00:00Z'}
        batch = {'status': 'open', 'mode': 'source-accumulated', 'base_sha': A,
                 'head_sha': C, 'head_tree': TREE, 'members': [member(A, B, True), member(B, C, False)],
                 'final_stage_head': B, 'installed_app': {'sha': B, 'tree': TREE, 'manifest_sha256': HASH},
                 'stage_receipt': stage}
        directory = root / 'stage-evidence'; directory.mkdir(mode=0o700)
        log = directory / (B + '-original.log'); log.write_text('actual journey pass\n'); log.chmod(0o600)
        evidence = {'schema_version': 1, 'status': 'passed', 'source_sha': B, 'source_tree': TREE,
                    'manifest_sha256': HASH, 'stage_receipt_sha256': hashlib.sha256(json.dumps(stage, sort_keys=True, separators=(',', ':')).encode()).hexdigest(),
                    'verified_at_utc': '2026-10-12T02:00:00Z', 'checks': [{'name': 'original-journey', 'status': 'passed', 'log': str(log), 'log_sha256': m._file_sha256(log)}]}
        m.atomic_json(directory / (B + '.json'), evidence)
        return {'repo': str(root / 'repo'), 'work_root': str(root)}, batch, evidence

    def bindings(self, stack, runtime=False):
        stack.enter_context(patch.object(m, '_tree', return_value=TREE))
        stack.enter_context(patch.object(m, '_first_parent_chain', return_value=[C]))
        stack.enter_context(patch.object(m, '_classify_candidate', return_value={'runtime_changed': runtime}))

    def test_source_tail_uses_original_app_receipt_and_log_without_rewriting(self):
        with tempfile.TemporaryDirectory() as d, ExitStack() as stack:
            root = Path(d); config, batch, evidence = self.fixture(root)
            self.bindings(stack)
            before = (root / 'stage-evidence' / (B + '.json')).read_bytes()
            self.assertEqual(m._batch_final_evidence(config, batch), evidence)
            self.assertEqual((root / 'stage-evidence' / (B + '.json')).read_bytes(), before)
            self.assertFalse((root / 'stage-evidence' / (C + '.json')).exists())

    def test_runtime_tail_cannot_reuse_earlier_app_evidence(self):
        with tempfile.TemporaryDirectory() as d, ExitStack() as stack:
            config, batch, _ = self.fixture(Path(d)); self.bindings(stack, True)
            with self.assertRaisesRegex(m.ReleaseError, 'runtime changes'):
                m._batch_final_evidence(config, batch)

    def test_foreign_stage_sha_and_tree_are_rejected(self):
        with tempfile.TemporaryDirectory() as d, ExitStack() as stack:
            config, batch, _ = self.fixture(Path(d)); self.bindings(stack)
            batch['final_stage_head'] = D
            with self.assertRaisesRegex(m.ReleaseError, 'installed application'):
                m._batch_final_evidence(config, batch)
            batch['final_stage_head'] = B; batch['installed_app']['tree'] = D
            with self.assertRaisesRegex(m.ReleaseError, 'tree changed'):
                m._batch_final_evidence(config, batch)

    def test_source_tail_cannot_drop_an_earlier_selected_journey(self):
        with tempfile.TemporaryDirectory() as d, ExitStack() as stack:
            config, batch, _ = self.fixture(Path(d)); self.bindings(stack)
            batch['members'][0]['stage_check_names'].append('missing-journey')
            with self.assertRaisesRegex(m.ReleaseError, 'omit an earlier'):
                m._batch_final_evidence(config, batch)

    def test_seal_source_tail_binds_original_package_without_rebuild(self):
        with tempfile.TemporaryDirectory() as d, ExitStack() as stack:
            root = Path(d); config, batch, evidence = self.fixture(root); self.bindings(stack)
            config.update(state=str(root/'state'), lock=str(root/'lock'), stage_releases=str(root/'releases'))
            state = {'status':'ready','in_flight':None,'queue':[],'main':{'sha':A},
                     'installed_app':{'sha':A,'manifest_sha256':HASH},'batch':batch}
            bundles = root/'source-bundles'; bundles.mkdir(); bundle = bundles/(C+'.bundle'); bundle.write_bytes(b'exact source bundle')
            batch['members'][-1]['source_bundle_sha256'] = m._file_sha256(bundle)
            out = root/'builds'/B; out.mkdir(parents=True)
            (root/'batch-evidence').mkdir(mode=0o700)
            (out/'domestic-release.json').write_text(json.dumps({'source_sha':B,'release_files_sha256':HASH}))
            stack.enter_context(patch.object(m.os,'geteuid',return_value=0))
            stack.enter_context(patch.object(m,'_safe_directory'))
            stack.enter_context(patch.object(m,'_check_config',side_effect=lambda c:c))
            stack.enter_context(patch.object(m,'_locked',return_value=nullcontext()))
            stack.enter_context(patch.object(m,'_load_state',return_value=state))
            stack.enter_context(patch.object(m,'_resolve_ref',side_effect=lambda r,ref:A if ref==m.MAIN_REF else C))
            stack.enter_context(patch.object(m,'_verify_stage_app'))
            stack.enter_context(patch.object(m,'_verify_prod_app'))
            stack.enter_context(patch.object(m.builder,'verify_release_inventory',return_value=HASH))
            stack.enter_context(patch.object(m.legacy,'verify_release_artifact'))
            stack.enter_context(patch.object(m,'_git',return_value='migrations/0237.sql'))
            stack.enter_context(patch.object(m,'_update_state'))
            # Protected evidence remains real; only the root identity guard is adapted for this user test.
            stack.enter_context(patch.object(m,'_read_stage_evidence',return_value=evidence))
            build = stack.enter_context(patch.object(m,'_build_candidate'))
            install = stack.enter_context(patch.object(m.legacy,'stage_install'))
            result = m.batch_seal(config, expected_head=C)
            self.assertEqual(result['approval']['head_sha'], C)
            self.assertEqual(result['approval']['installed_app']['sha'], B)
            self.assertEqual(batch['final_journey_receipt'], evidence)
            build.assert_not_called(); install.assert_not_called()

    def test_cumulative_maintenance_marker_uses_current_source_not_production(self):
        state = m._new_state(A,TREE,{'sha':A,'tree':TREE,'manifest_sha256':HASH})
        state.update(staging_out_of_sync=True,batch={'status':'open','base_sha':A,'head_sha':B,
                     'members':[{'base_sha':A,'head_sha':B}],'installed_app':{'sha':B,'tree':TREE,'manifest_sha256':HASH}})
        state['queue']=[{'candidate_id':C,'ref':'refs/heads/codex/c','head_sha':C,'base_sha':B,'status':'pending'}]
        marker={'candidate_sha':C,'candidate_tree':TREE,'base_sha':B,'controller_files':['scripts/domestic_main_release.py'],
                'fixed_file_sha256':{p:HASH for p in m.builder.FIXED_CONTROLLER_FILES},'check_receipt_sha256':HASH,'checked_at_utc':m._utc_now()}
        state['controller_maintenance']=marker
        m._validate_state(state)
        with patch.object(m,'_resolve_ref',side_effect=lambda r,ref:A if ref==m.MAIN_REF else C), patch.object(m,'_tree',return_value=TREE), patch.object(m,'_first_parent_chain'), patch.object(m,'_classify_candidate',return_value={'runtime_changed':False,'controller_files':['scripts/domestic_main_release.py']}), patch.object(m,'_source_blob',return_value=b'fixed'), patch.object(m,'_verify_controller_files'):
            marker['fixed_file_sha256']={p:hashlib.sha256(b'fixed').hexdigest() for p in m.builder.FIXED_CONTROLLER_FILES}
            self.assertEqual(m._verify_controller_maintenance_candidate({},Path('/repo'),state),marker)
            marker['base_sha']=A
            with self.assertRaisesRegex(m.ReleaseError,'marker base'):
                m._validate_state(state)

    def test_maintenance_writer_checks_current_cumulative_source(self):
        with tempfile.TemporaryDirectory() as d, ExitStack() as stack:
            root=Path(d)
            state=m._new_state(A,TREE,{'sha':A,'tree':TREE,'manifest_sha256':HASH})
            state.update(staging_out_of_sync=True,batch={'status':'open','base_sha':A,'head_sha':B,
                         'members':[{'base_sha':A,'head_sha':B}],'installed_app':{'sha':B,'tree':TREE,'manifest_sha256':HASH}})
            state['queue']=[{'candidate_id':C,'ref':'refs/heads/codex/c','head_sha':C,'base_sha':B,'status':'pending'}]
            config={'repo':str(root),'state':str(root/'state'),'lock':str(root/'lock'),'work_root':str(root),'push_group':'fixture','controller_path':m.DEFAULT_CONTROLLER}
            for name, value in [('geteuid',0)]: stack.enter_context(patch.object(m.os,name,return_value=value))
            stack.enter_context(patch.object(m,'_check_config',side_effect=lambda c:c))
            stack.enter_context(patch.object(m,'_locked',return_value=nullcontext()))
            stack.enter_context(patch.object(m,'verify_bare_repository'))
            stack.enter_context(patch.object(m,'_load_state',return_value=state))
            stack.enter_context(patch.object(m,'_resolve_ref',side_effect=lambda r,ref:A if ref==m.MAIN_REF else C))
            stack.enter_context(patch.object(m,'_source_blob',side_effect=lambda r,s,p:Path(m.__file__).read_bytes() if p=='scripts/domestic_main_release.py' else b'fixed'))
            stack.enter_context(patch.object(m,'_first_parent_chain'))
            stack.enter_context(patch.object(m,'_pin_candidate'))
            stack.enter_context(patch.object(m,'_active_worktree',return_value=root))
            stack.enter_context(patch.object(m,'_classify_candidate',return_value={'runtime_changed':False,'controller_files':['scripts/domestic_main_release.py']}))
            check=stack.enter_context(patch.object(m,'_check_report',return_value={'status':'passed','toolchain':{}}))
            stack.enter_context(patch.object(m,'_build_command',return_value=subprocess.CompletedProcess([],0,'locked-config-dry-run-ok','')))
            stack.enter_context(patch.object(m,'_tree',return_value=TREE))
            stack.enter_context(patch.object(m,'_update_state'))
            self.assertEqual(m.maintenance_check(config,C)['status'],'maintenance_checks_passed')
            self.assertEqual(check.call_args.args[-2:],(B,C))
            self.assertEqual(state['controller_maintenance']['base_sha'],B)
            state['status']='outcome_unknown'
            with self.assertRaisesRegex(m.ReleaseError,'unresolved release state'):
                m.maintenance_check(config,C)


def load_tests(loader, tests, pattern):
    tests = loader.loadTestsFromTestCase(SourceOnlySeal)
    for name in ('test_controller_maintenance_marker_binds_base_candidate_and_all_fixed_bytes',
                 'test_maintenance_writer_load_install_and_resubmit_preserve_legacy_marker_protocol'):
        tests.addTest(original.DomesticMainReleaseTests(name))
    tests.addTest(original.CumulativeBatchTests('test_final_journey_must_cover_earlier_member'))
    return tests


if __name__ == '__main__':
    unittest.main()
