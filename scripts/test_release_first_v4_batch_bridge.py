import hashlib
import json
import sys
import tempfile
import unittest
from datetime import datetime, timezone
from pathlib import Path
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).parent))
from release_first_v4_batch_bridge import (OLD_CANDIDATE, OLD_PACKAGE, OLD_RECEIPT,
    OLD_RELEASE, OLD_TREE, V4_ROOT, admit, bridge_transition, evidence, validate)


class FirstV4BatchBridgeTests(unittest.TestCase):
    def test_validator_binds_live_heads_old_tree_and_exact_package(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            base, aggregate, preview, tree = ('a'*40, 'b'*40, 'c'*40, 'd'*40)
            package, accepted = 'e'*64, 'f'*64
            heads = ['1'*40, '2'*40, '3'*40]
            trees = ['4'*40, '5'*40, '6'*40]
            cid = 'joint-first-batch'
            members = [{'work_item': f'item-{i}', 'commit_sha': head, 'tree_sha': member_tree,
                        'receipt_sha256': accepted}
                       for i, (head, member_tree) in enumerate(zip(heads, trees))]
            batch = {'worktree': str(root), 'candidate_id': cid, 'base_main_sha': base,
                     'aggregate_head_sha': aggregate, 'merge_preview_sha': preview,
                     'candidate_tree_sha': tree, 'package_sha256': package, 'members': members}
            batch_path = root/'batch.json'; batch_path.write_text(json.dumps(batch))
            record = {'schema': 1, 'exception_id': 'first-v4-joint-batch-v1',
                      'repository': 'qianlan33333-png/AI-CRM-v4',
                      'batch_manifest_sha256': hashlib.sha256(batch_path.read_bytes()).hexdigest(),
                      'batch': {**{key: batch[key] for key in ('candidate_id', 'base_main_sha',
                          'aggregate_head_sha', 'merge_preview_sha', 'candidate_tree_sha', 'package_sha256')},
                          'accepted_receipt_sha256': accepted},
                      'v4_root': {'commit_sha': V4_ROOT, 'tree_sha': OLD_TREE},
                      'legacy': {'candidate_id': OLD_CANDIDATE, 'release_sha': OLD_RELEASE,
                                 'tree_sha': OLD_TREE, 'package_sha256': OLD_PACKAGE,
                                 'receipt': {'path': '/receipt', 'sha256': OLD_RECEIPT}},
                      'members': [{'pr_url': f'https://github.com/qianlan33333-png/AI-CRM-v4/pull/{number}',
                                   'commit_sha': head, 'tree_sha': member_tree}
                                  for number, head, member_tree in zip((15, 3, 13), heads, trees)],
                      'production_readback': {'path': '/current', 'sha256': '7'*64}}
            bridge_path = root/'bridge.json'; bridge_path.write_text(json.dumps(record))
            queue = {'items': [{'candidate_id': OLD_CANDIDATE, 'status': 'observing',
                                'candidate_tree_sha': OLD_TREE, 'package_sha256': OLD_PACKAGE},
                               {'candidate_id': cid, 'status': 'frozen',
                                'candidate_tree_sha': tree, 'package_sha256': package}]}
            readback = {'release_sha': OLD_RELEASE, 'tree_sha': OLD_TREE,
                        'package_sha256': OLD_PACKAGE, 'readyz': {'release_sha': OLD_RELEASE, 'status': 'ready'},
                        'observed_at': datetime.now(timezone.utc).isoformat()}
            old_receipt = {'candidate_id': OLD_CANDIDATE, 'commit_sha': OLD_RELEASE,
                           'tree_sha': OLD_TREE, 'package_sha256': OLD_PACKAGE, 'status': 'observing',
                           'business_acceptance': {'status': 'business_acceptance_deferred_to_user'},
                           'production_readback': {'release_sha': OLD_RELEASE, 'tree_sha': OLD_TREE,
                               'package_sha256': OLD_PACKAGE, 'evidence_path': '/historic',
                               'evidence_sha256': '8'*64}}
            git_values = {('rev-list', '--parents', '-n', '1', preview): f'{preview} {base} {aggregate}',
                          ('rev-parse', preview+'^{tree}'): tree,
                          ('rev-parse', V4_ROOT+'^{tree}'): OLD_TREE,
                          ('rev-parse', 'refs/remotes/origin/main'): base}
            git_values.update({('rev-parse', head+'^{tree}'): member_tree for head, member_tree in zip(heads, trees)})
            def fake_evidence(_, label):
                return old_receipt if label == 'legacy receipt' else readback
            def pr_reader(number):
                return {'state': 'open', 'head': {'sha': heads[(15, 3, 13).index(number)]},
                        'base': {'sha': base, 'repo': {'full_name': 'qianlan33333-png/AI-CRM-v4'}}}
            with patch('release_first_v4_batch_bridge.git', side_effect=lambda _, *a: git_values[a]), \
                 patch('release_first_v4_batch_bridge.live_remote_main', return_value=base), \
                 patch('release_first_v4_batch_bridge.subprocess.run'), \
                 patch('release_first_v4_batch_bridge.evidence', side_effect=fake_evidence), \
                 patch('release_first_v4_batch_bridge.decision'), \
                 patch('release_batch.validate_batch'):
                self.assertEqual(validate(bridge_path, batch_path=batch_path, queue=queue,
                                          pr_reader=pr_reader)['candidate_id'], cid)
                with self.assertRaisesRegex(ValueError, 'live PR head'):
                    validate(bridge_path, batch_path=batch_path, queue=queue,
                             pr_reader=lambda n: {**pr_reader(n), 'head': {'sha': '9'*40}})
                queue['items'][0]['candidate_tree_sha'] = '0'*40
                with self.assertRaisesRegex(ValueError, 'old observing'):
                    validate(bridge_path, batch_path=batch_path, queue=queue, pr_reader=pr_reader)
                queue['items'][0]['candidate_tree_sha'] = OLD_TREE
                queue['items'][1]['package_sha256'] = '0'*64
                with self.assertRaisesRegex(ValueError, 'queued exactly'):
                    validate(bridge_path, batch_path=batch_path, queue=queue, pr_reader=pr_reader)

    def test_evidence_is_content_bound(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'receipt.json'
            path.write_text('{"status":"observing"}')
            ref = {'path': str(path), 'sha256': hashlib.sha256(path.read_bytes()).hexdigest()}
            self.assertEqual(evidence(ref, 'receipt')['status'], 'observing')
            path.write_text('{"status":"released"}')
            with self.assertRaisesRegex(ValueError, 'digest'):
                evidence(ref, 'receipt')

    def test_admission_consumes_exactly_once_and_preserves_old_observation(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            queue_path = root / 'queue.json'
            queue_path.write_text(json.dumps({'items': [
                {'candidate_id': OLD_CANDIDATE, 'status': 'observing', 'events': [{'kind': 'original'}]},
                {'candidate_id': 'new-batch', 'status': 'frozen', 'events': []}]}))
            bridge = root / 'bridge.json'; bridge.write_text('{}')
            batch = root / 'batch.json'; batch.write_text('{}')
            result = {'candidate_id': 'new-batch', 'bridge_id': 'first-v4-joint-batch-v1',
                      'bridge_sha256': hashlib.sha256(bridge.read_bytes()).hexdigest()}
            with patch('release_first_v4_batch_bridge.validate', return_value=result):
                admit(bridge, batch_path=batch, queue_path=queue_path)
                with self.assertRaisesRegex(ValueError, 'twice'):
                    admit(bridge, batch_path=batch, queue_path=queue_path)
            queue = json.loads(queue_path.read_text())
            self.assertEqual(queue['items'][0]['status'], 'observing')
            self.assertEqual(queue['items'][0]['events'], [{'kind': 'original'}])
            self.assertEqual(queue['items'][0]['first_v4_batch_bridge_candidate_id'], 'new-batch')
            self.assertEqual(queue['items'][1]['status'], 'waiting_merge')

    def test_transition_is_narrow_and_cannot_be_rebound(self):
        digest = 'a' * 64
        queue = {'items': [
            {'candidate_id': OLD_CANDIDATE, 'status': 'observing',
             'first_v4_batch_bridge_candidate_id': 'new-batch', 'first_v4_batch_bridge_sha256': digest},
            {'candidate_id': 'new-batch', 'status': 'waiting_merge',
             'first_v4_batch_bridge_sha256': digest},
        ]}
        with self.assertRaisesRegex(ValueError, 'digest'):
            bridge_transition(queue, 'new-batch', 'merged', 'b' * 64)
        with self.assertRaisesRegex(ValueError, 'consumption'):
            bridge_transition(queue, 'other', 'merged', digest)
        bridge_transition(queue, 'new-batch', 'merged', digest)
        bridge_transition(queue, 'new-batch', 'production', digest)
        bridge_transition(queue, 'new-batch', 'observing', digest)
        self.assertEqual([i['status'] for i in queue['items']], ['observing', 'observing'])
        with self.assertRaisesRegex(ValueError, 'invalid'):
            bridge_transition(queue, 'new-batch', 'production', digest)
        queue['items'].append({'candidate_id': 'intruder', 'status': 'production'})
        with self.assertRaisesRegex(ValueError, 'slot occupied'):
            # Restore a valid transition source to test competing occupancy.
            queue['items'][1]['status'] = 'production'
            bridge_transition(queue, 'new-batch', 'observing', digest)


if __name__ == '__main__': unittest.main()
