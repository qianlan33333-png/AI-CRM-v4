"""Contracts for focused checks, source accumulation, and final-package staging."""
import hashlib
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from contextlib import nullcontext, ExitStack
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).parent))
import domestic_main_release as m
import domestic_release_build as b

A, B, C = 'a'*40, 'b'*40, 'c'*40
TREE = 'd'*40
DIGEST = 'e'*64


class ExtremeRelease(unittest.TestCase):
    def state(self):
        return m._new_state(A, TREE, {'sha': A, 'tree': TREE, 'manifest_sha256': DIGEST})

    def item(self, head, base):
        return {'candidate_id': head, 'head_sha': head, 'base_sha': base,
                'ref': 'refs/heads/codex/'+head[:8], 'status': 'pending'}

    def receipt(self, base, head):
        return {'status': 'passed', 'profile': 'ai-selected', 'baseline_sha': base,
                'head_sha': head, 'head_tree': TREE, 'changed_paths': ['internal/product/app/settings.go'],
                'installed_checks': ['contact-'+head[0]], 'selected_lanes': ['preflight']}

    def test_policy_change_does_not_expand_explicit_ai_list(self):
        checks = [{'lane': 'preflight', 'path': 'scripts/test_domestic_release_extreme.py'}]
        selection = {'selection_mode': 'targeted', 'selected_lanes': ['preflight'],
                     'selected_checks': checks, 'profile': 'ai-selected'}
        plan = {'enforced': selection, 'selection_source': 'local-ai-check-list'}
        result = m._enforced_lanes(plan, ['scripts/ci/quality_lanes.py'])
        self.assertEqual(result[1:], (['preflight'], checks, [], 'ai-selected'))

    def test_ai_source_is_bound_to_exact_checkout(self):
        plan = {'selection_source': 'local-ai-check-list', 'baseline_sha': A, 'head_sha': B,
                'head_tree': TREE, 'source_clean': True, 'source': {'head_matches': True, 'status': []},
                'evidence_eligible': True, 'enforced': {'selected_lanes': ['preflight']}}
        with patch.object(m, '_build_command', return_value=subprocess.CompletedProcess([], 0, json.dumps(plan))), patch.object(m, '_worktree_git', return_value=TREE):
            self.assertEqual(m._trusted_preflight_plan({}, Path('/policy'), Path('/candidate'), A, B), plan)
            with self.assertRaises(m.ReleaseError):
                m._trusted_preflight_plan({}, Path('/policy'), Path('/candidate'), A, C)

    def test_two_runtime_members_accumulate_without_build_or_install_then_stage_once(self):
        state = self.state()
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            out = root/'out'; out.mkdir(); (out/'domestic-release.json').write_text('{}')
            config = {'repo': str(root), 'work_root': str(root), 'stage_releases': str(root),
                      'lock': str(root/'lock'), 'state': str(root/'state'), 'controller_path': str(root/'controller'),
                      'push_group': 'unused', 'production_enabled': True}
            (root/A).mkdir()
            refs = {'refs/heads/main': A}
            first, second = self.item(B, A), self.item(C, B)
            refs[first['ref']], refs[second['ref']] = B, C
            state['queue'] = [first]
            metadata = {'source_tree': TREE, 'release_files_sha256': DIGEST, 'runtime_config_check_supported': True}
            with ExitStack() as stack:
                stack.enter_context(patch.object(m, '_resolve_ref', side_effect=lambda repo, ref: refs[ref]))
                stack.enter_context(patch.object(m, '_first_parent_chain', return_value=[B]))
                stack.enter_context(patch.object(m, '_pin_candidate'))
                stack.enter_context(patch.object(m, '_tree', return_value=TREE))
                stack.enter_context(patch.object(m, '_active_worktree', return_value=root))
                stack.enter_context(patch.object(m, '_classify_candidate', return_value={'runtime_changed': True, 'controller_files': []}))
                stack.enter_context(patch.object(m, '_verify_controller_files', return_value={}))
                checks = stack.enter_context(patch.object(m, '_check_report', side_effect=lambda cfg, repo, wt, report, base, head: self.receipt(base, head)))
                stack.enter_context(patch.object(m, '_create_full_bundle', return_value=(root/'bundle', {'bundle_sha256': DIGEST})))
                stack.enter_context(patch.object(m, '_update_state', side_effect=lambda path, st, **kw: st.update(kw)))
                stack.enter_context(patch.object(m, '_verify_stage_app'))
                stack.enter_context(patch.object(m, '_verify_prod_app'))
                stack.enter_context(patch.object(m, '_safe_directory'))
                stack.enter_context(patch.object(m.builder, 'verify_release_inventory', return_value=DIGEST))
                build = stack.enter_context(patch.object(m, '_build_candidate', return_value=(out, metadata)))
                install = stack.enter_context(patch.object(m.legacy, 'stage_install', return_value={'source_sha': C, 'technical_status': 'installed_healthy'}))
                stack.enter_context(patch.object(m, '_worktree_git', return_value='internal/product/app/settings.go'))
                stack.enter_context(patch.object(m, '_git', return_value='internal/product/app/settings.go'))
                stack.enter_context(patch.object(m.os, 'geteuid', return_value=0))
                stack.enter_context(patch.object(m, '_check_config', side_effect=lambda c: c))
                stack.enter_context(patch.object(m, '_locked', return_value=nullcontext()))
                stack.enter_context(patch.object(m, '_load_state', return_value=state))
                stack.enter_context(patch.object(m, '_assert_legacy_release_path_stopped'))
                self.assertEqual(m.process_candidate(config, root/'state', state, first)['status'], 'batch_open')
                state['queue'].append(second)
                self.assertEqual(m.process_candidate(config, root/'state', state, second)['status'], 'batch_open')
                build.assert_not_called(); install.assert_not_called()
                self.assertEqual(state['batch']['installed_app']['sha'], A)
                self.assertEqual([x['head_sha'] for x in state['batch']['members']], [B, C])
                m._validate_state(state)
                staged = m.batch_stage(config)
                self.assertEqual(staged['status'], 'stage_validation_pending')
                self.assertEqual(checks.call_count, 2)
                build.assert_called_once(); install.assert_called_once()
                self.assertEqual(state['in_flight']['check_receipt']['installed_checks'], ['contact-b', 'contact-c'])
                with patch.object(m, '_verify_staged_member'):
                    with self.assertRaisesRegex(m.ReleaseError, 'omits'):
                        m._accept_final_batch_stage(config, root/'state', state, second, {'checks': [{'name': 'contact-c'}]})
                    m._accept_final_batch_stage(config, root/'state', state, second, {'checks': [{'name': 'contact-b'}, {'name': 'contact-c'}]})
                self.assertTrue(m.batch_stage(config)['already_staged'])
                self.assertEqual(build.call_count, 1); self.assertEqual(install.call_count, 1)

    def test_unknown_install_cannot_enter_final_staging(self):
        state = self.state(); state.update(status='outcome_unknown', in_flight={'head_sha': B})
        with patch.object(m.os, 'geteuid', return_value=0), patch.object(m, '_check_config', side_effect=lambda c:c), patch.object(m, '_locked', return_value=nullcontext()), patch.object(m, '_load_state', return_value=state), patch.object(m, '_assert_legacy_release_path_stopped'), patch.object(m, 'process_candidate') as process:
            with self.assertRaisesRegex(m.ReleaseError, 'idle resolved'):
                m.batch_stage({'repo':'/unused', 'state':'/unused/state', 'lock':'/unused/lock', 'production_enabled':True})
            process.assert_not_called()

    def test_ai_smoke_does_not_add_unrequested_payment_journey(self):
        self.assertFalse(m.profile_requires_legacy_smoke({'profile':'ai-selected'}))
        self.assertTrue(m.profile_requires_legacy_smoke({'profile':'full'}))

    def test_maintenance_reuses_only_the_exact_protected_passed_receipt(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            diagnostics = root / 'diagnostics'
            diagnostics.mkdir()
            receipt = self.receipt(A, B)
            canonical = json.dumps(receipt, ensure_ascii=False, sort_keys=True, separators=(',', ':')).encode()
            marker = {'candidate_sha': B, 'candidate_tree': TREE, 'base_sha': A,
                      'check_receipt_sha256': hashlib.sha256(canonical).hexdigest()}
            m.atomic_json(diagnostics / (B + '-maintenance-receipt.json'), receipt)
            config = {'work_root': directory}
            self.assertEqual(m._maintenance_check_checkpoint(config, marker, return_receipt=True), receipt)
            marker['check_receipt_sha256'] = DIGEST
            self.assertIsNone(m._maintenance_check_checkpoint(config, marker, return_receipt=True))

    def test_application_build_does_not_compute_a_repository_graph(self):
        inventory = [b.ReleaseCommand('./cmd/aicrm', 'aicrm'), b.ReleaseCommand('./cmd/migrate-example', 'migrate-example')]
        with patch.object(b, '_changed_paths', return_value=['internal/product/app/settings.go']), patch.object(b, '_tree_sha', return_value=TREE), patch.object(b, '_maintenance_application_guard'), patch.object(b, '_release_commands', return_value=inventory), patch.object(b, '_git_file', return_value='{}'), patch.object(b, '_go_packages', side_effect=AssertionError('whole graph must not run')):
            self.assertEqual(b._make_plan(Path('/repo'), A, B)['go_commands'], ['./cmd/aicrm'])


if __name__ == '__main__':
    unittest.main()
