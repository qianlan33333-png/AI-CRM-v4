import copy
import hashlib
import json
import subprocess
import tempfile
import unittest
from datetime import datetime, timezone
from pathlib import Path
from unittest.mock import patch
import sys
sys.path.insert(0, str(Path(__file__).parent))

import release_bootstrap as b
from release_queue import change


def write_json(path, value):
    path.write_text(json.dumps(value))
    return {'path': str(path), 'sha256': hashlib.sha256(path.read_bytes()).hexdigest()}


class BootstrapTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name)
        self.repo = self.directory / 'repo'
        self.repo.mkdir()
        def git(*args, **kwargs):
            return subprocess.check_output(['git', '-C', str(self.repo), *args], text=True, **kwargs).strip()
        self.git = git
        git('init', '-q')
        git('config', 'user.name', 'test')
        git('config', 'user.email', 'test@example.invalid')
        (self.repo / 'base').write_text('old production content')
        git('add', 'base')
        git('commit', '-qm', 'v4 root')
        self.base = git('rev-parse', 'HEAD')
        self.old_tree = git('rev-parse', 'HEAD^{tree}')
        git('update-ref', 'refs/remotes/origin/main', self.base)
        (self.repo / 'repair').write_text('Alipay entry')
        git('add', 'repair')
        git('commit', '-qm', 'repair')
        self.head = git('rev-parse', 'HEAD')
        self.tree = git('rev-parse', 'HEAD^{tree}')
        self.preview = git('commit-tree', self.tree, '-p', self.base, '-p', self.head, input='preview\n')
        self.candidate_id = 'repair-1'
        self.package = 'a' * 64
        self.accepted = 'b' * 64
        historic_ref = write_json(self.directory / 'old-readback.json',
            {'release_sha': b.OLD_RELEASE, 'tree_sha': self.old_tree,
             'package_sha256': b.OLD_PACKAGE, 'readyz_status': 'ready'})
        old_receipt = {
            'candidate_id': b.OLD_CANDIDATE, 'commit_sha': b.OLD_RELEASE,
            'tree_sha': self.old_tree, 'package_sha256': b.OLD_PACKAGE,
            'status': 'observing', 'business_acceptance': {'status': 'business_acceptance_deferred_to_user'},
            'production_readback': {'release_sha': b.OLD_RELEASE, 'tree_sha': self.old_tree,
                                    'package_sha256': b.OLD_PACKAGE,
                                    'evidence_path': historic_ref['path'], 'evidence_sha256': historic_ref['sha256']},
        }
        old_ref = write_json(self.directory / 'old-receipt.json', old_receipt)
        readback = {'observed_at': datetime.now(timezone.utc).isoformat(), 'release_sha': b.OLD_RELEASE,
                    'tree_sha': self.old_tree, 'package_sha256': b.OLD_PACKAGE,
                    'readyz': {'release_sha': b.OLD_RELEASE, 'status': 'ready'}}
        rb_ref = write_json(self.directory / 'readback.json', readback)
        candidate = {'pr_url': b.PR_URL, 'candidate_id': self.candidate_id, 'base_main_sha': self.base,
                     'head_sha': self.head, 'preview_sha': self.preview, 'tree_sha': self.tree,
                     'package_sha256': self.package, 'accepted_receipt_sha256': self.accepted,
                     'queue_owner_thread_id': 'coordinator', 'origin_thread_id': 'payment-task'}
        self.record = {'schema': 1, 'exception_id': 'first-v4-alipay-entry-repair-v1', 'repository': b.REPOSITORY,
                       'legacy': {'candidate_id': b.OLD_CANDIDATE, 'release_sha': b.OLD_RELEASE,
                                  'tree_sha': self.old_tree, 'package_sha256': b.OLD_PACKAGE, 'receipt': old_ref},
                       'v4_root': {'commit_sha': self.base, 'tree_sha': self.old_tree},
                       'candidate': candidate, 'production_readback': rb_ref}
        self.record['queue_authorization'] = self.decision('queue_authorization')
        self.queue = {'items': [{'candidate_id': b.OLD_CANDIDATE, 'status': 'observing',
                                 'package_sha256': b.OLD_PACKAGE, 'candidate_tree_sha': self.old_tree,
                                 'events': []}]}
        self.path = self.directory / 'bridge.json'
        self.patches = [patch.object(b, 'V4_ROOT', self.base), patch.object(b, 'OLD_TREE', self.old_tree),
                        patch.object(b, 'OLD_RECEIPT', old_ref['sha256']),
                        patch.object(b, 'live_remote_main', return_value=self.base)]
        for item in self.patches:
            item.start()
            self.addCleanup(item.stop)

    def decision(self, kind):
        evidence = {'source': 'user_message', 'user_thread_id': 'user-thread', 'user_message_id': kind + '-message',
                    'candidate_id': self.candidate_id, 'package_sha256': self.package,
                    'preview_sha': self.preview, 'decision': 'approved', 'decision_text': kind + ' exact approval'}
        ref = write_json(self.directory / (kind + '.json'), evidence)
        return {**evidence, 'decision_id': kind + '-decision', 'recorded_by_thread_id': 'coordinator', 'evidence': ref}

    def validate(self, phase='admission', queue=None, record=None):
        write_json(self.path, self.record if record is None else record)
        return b.validate_bootstrap(self.path, root=self.repo, base=self.base, head=self.head,
                                    preview=self.preview, tree=self.tree, package=self.package,
                                    candidate_id=self.candidate_id, queue=self.queue if queue is None else queue,
                                    phase=phase, accepted_receipt_sha256=self.accepted,
                                    queue_owner_thread_id='coordinator',
                                    merged_main_sha=self.preview if phase == 'promotion' else None)

    def test_exact_bridge_accepts_admission_and_promote_only_after_second_decision(self):
        self.assertEqual(self.validate()['legacy_candidate_id'], b.OLD_CANDIDATE)
        queue = copy.deepcopy(self.queue)
        queue['items'][0]['repair_candidate_id'] = self.candidate_id
        queue['items'].append({'candidate_id': self.candidate_id, 'status': 'waiting_merge',
                               'package_sha256': self.package, 'candidate_tree_sha': self.tree,
                               'origin_thread_id': 'payment-task'})
        self.git('update-ref', 'refs/remotes/origin/main', self.preview)
        with patch.object(b, 'live_remote_main', return_value=self.preview):
            with self.assertRaisesRegex(ValueError, 'production_authorization missing'):
                self.validate('promotion', queue)
            self.record['production_authorization'] = self.decision('production_authorization')
            self.record['production_authorization']['old_release_sha'] = b.OLD_RELEASE
            self.assertEqual(self.validate('promotion', queue)['candidate_id'], self.candidate_id)

    def test_mutated_identity_receipt_package_or_approval_fails_closed(self):
        mutations = [lambda r: r['legacy'].__setitem__('release_sha', 'f' * 40),
                     lambda r: r['legacy'].__setitem__('tree_sha', 'f' * 40),
                     lambda r: r['legacy'].__setitem__('package_sha256', 'f' * 64),
                     lambda r: r['legacy']['receipt'].__setitem__('sha256', 'f' * 64),
                     lambda r: r['v4_root'].__setitem__('tree_sha', 'f' * 40),
                     lambda r: r['candidate'].__setitem__('pr_url', 'https://github.com/x/y/pull/3'),
                     lambda r: r['candidate'].__setitem__('head_sha', 'f' * 40),
                     lambda r: r['candidate'].__setitem__('preview_sha', 'f' * 40),
                     lambda r: r['candidate'].__setitem__('package_sha256', 'f' * 64),
                     lambda r: r['candidate'].__setitem__('queue_owner_thread_id', 'another'),
                     lambda r: r['queue_authorization'].__setitem__('decision', 'pending'),
                     lambda r: r['queue_authorization']['evidence'].__setitem__('sha256', 'f' * 64)]
        for mutation in mutations:
            record = copy.deepcopy(self.record)
            mutation(record)
            with self.subTest(mutation=mutation), self.assertRaises(ValueError):
                self.validate(record=record)

    def test_stale_readback_or_other_candidate_fails_closed(self):
        old = json.loads(Path(self.record['production_readback']['path']).read_text())
        old['observed_at'] = '2020-01-01T00:00:00+00:00'
        self.record['production_readback'] = write_json(self.directory / 'stale.json', old)
        with self.assertRaisesRegex(ValueError, 'stale'):
            self.validate()
        self.record['production_readback'] = write_json(self.directory / 'fresh.json',
            {**old, 'observed_at': datetime.now(timezone.utc).isoformat()})
        queue = copy.deepcopy(self.queue)
        queue['items'][0]['repair_candidate_id'] = 'other'
        with self.assertRaisesRegex(ValueError, 'already consumed'):
            self.validate(queue=queue)
        queue['items'][0].pop('repair_candidate_id')
        queue['items'].append({'candidate_id': 'other', 'status': 'observing'})
        with self.assertRaisesRegex(ValueError, 'another runtime candidate'):
            self.validate(queue=queue)

    def test_repair_queue_never_releases_without_separate_business_reconciliation(self):
        q = {'items': [{'candidate_id': b.OLD_CANDIDATE, 'status': 'observing',
                        'repair_candidate_id': self.candidate_id},
                       {'candidate_id': self.candidate_id, 'status': 'observing',
                        'bootstrap_exception_id': 'first-v4-alipay-entry-repair-v1'}]}
        with self.assertRaisesRegex(ValueError, 'real-business acceptance'):
            change(q, 'transition', candidate_id=self.candidate_id, status='released')


if __name__ == '__main__':
    unittest.main()
