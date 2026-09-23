import hashlib
import fcntl
import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch
sys.path.insert(0,str(Path(__file__).parent))
from release_promote import promote, require_staging_node
from release_queue import guarded_reservation

class PromoteTests(unittest.TestCase):
    def fixture(self, root):
        sha='a'*40; tree='b'*40; digest=hashlib.sha256(b'package').hexdigest(); cid='c'*24
        archive=root/f'aicrm-{sha}.tar.gz'; archive.write_bytes(b'package')
        receipt=root/'accepted.json'; receipt.write_text(json.dumps({'package_path':str(archive)}))
        handoff=root/'handoff.json'; handoff.write_text(json.dumps({'worktree':str(root),'staging_acceptance':{'candidate_id':cid,'receipt':str(receipt)},'merge_preview_sha':sha,'candidate_tree_sha':tree,'package_sha256':digest}))
        queue=root/'queue.json'; queue.write_text(json.dumps({'schema':1,'items':[{'candidate_id':cid,'status':'waiting_merge','tree_sha':tree,'package_sha256':digest,'base_main_sha':'d'*40,'events':[]}]}))
        key=root/'key'; key.write_text('private'); kh=root/'known_hosts'; kh.write_text('pinned')
        return dict(handoff_path=handoff,queue_file=queue,merged_main='e'*40,production_sha='f'*40,host='example.invalid',user='ubuntu',key_file=key,known_hosts_file=kh,attempt_file=root/'attempt.json',deploy_script=root/'deploy.sh'),cid,sha,tree,digest
    def test_missing_registration_fails_before_transport(self):
        with tempfile.TemporaryDirectory() as temp:
            root=Path(temp); kwargs,cid,sha,tree,digest=self.fixture(root)
            kwargs['queue_file'].write_text(json.dumps({'items':[]}))
            with patch('release_promote.prepare_promote',side_effect=ValueError('missing registration')):
                with self.assertRaisesRegex(ValueError,'missing registration'): promote(**kwargs,readback=lambda *a:None,deploy=lambda *a:None)
            self.assertFalse(kwargs['attempt_file'].exists()); self.assertEqual(kwargs['key_file'].read_text(),'private')
    def test_mocked_deploy_preserves_credentials_and_blocks_replay(self):
        with tempfile.TemporaryDirectory() as temp:
            root=Path(temp); kwargs,cid,sha,tree,digest=self.fixture(root)
            calls=[]
            def readback(*args): return {'release_sha':kwargs['production_sha'] if not calls else sha,'status':'ready'}
            def deploy(cmd,env):
                calls.append(cmd)
                Path(env['DEPLOY_RECEIPT']).write_text(json.dumps({'release_sha':sha,'tree_sha':tree,'package_sha256':digest}))
            prepared={'candidate_tree_sha':tree,'package_sha256':digest}
            with patch('release_promote.prepare_promote',return_value=prepared),patch('release_promote.live_remote_main',return_value=kwargs['merged_main']):
                result=promote(**kwargs,readback=readback,deploy=deploy)
                self.assertEqual(result['status'],'observing')
                self.assertEqual(len(calls),1)
                self.assertFalse(kwargs['queue_file'].with_name('queue.json.promotion-reservation.json').exists())
                again=promote(**kwargs,readback=lambda *a:{'release_sha':sha,'status':'ready'},deploy=deploy)
                self.assertEqual(len(calls),1)
            self.assertEqual(kwargs['key_file'].read_text(),'private');self.assertEqual(kwargs['known_hosts_file'].read_text(),'pinned')
    def test_unknown_result_never_redeploys_and_reservation_blocks_other(self):
        with tempfile.TemporaryDirectory() as temp:
            root=Path(temp); kwargs,cid,sha,tree,digest=self.fixture(root)
            attempts=[]; prepared={'candidate_tree_sha':tree,'package_sha256':digest}
            with patch('release_promote.prepare_promote',return_value=prepared),patch('release_promote.live_remote_main',return_value=kwargs['merged_main']):
                def fail(cmd,env): attempts.append(cmd); raise TimeoutError('uncertain')
                with self.assertRaises(TimeoutError): promote(**kwargs,readback=lambda *a:{'release_sha':kwargs['production_sha'],'status':'ready'},deploy=fail)
                self.assertEqual(json.loads(kwargs['attempt_file'].read_text())['status'],'outcome_unknown')
                with self.assertRaises(ValueError): guarded_reservation(kwargs['queue_file'],'other',None)
                promote(**kwargs,readback=lambda *a:{'release_sha':kwargs['production_sha'],'status':'ready'},deploy=fail)
                self.assertEqual(len(attempts),1)
    def test_competing_promotion_lock_fails_before_network(self):
        with tempfile.TemporaryDirectory() as temp:
            root=Path(temp); kwargs,*_=self.fixture(root)
            lock_path=kwargs['queue_file'].with_name('queue.json.promotion.lock')
            with lock_path.open('a') as lock:
                fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
                with self.assertRaises(BlockingIOError):
                    promote(**kwargs,readback=lambda *a:None,deploy=lambda *a:None)
            self.assertFalse(kwargs['attempt_file'].exists())
    def test_package_digest_mismatch_fails_before_network(self):
        with tempfile.TemporaryDirectory() as temp:
            root=Path(temp); kwargs,cid,sha,tree,digest=self.fixture(root)
            (root/f'aicrm-{sha}.tar.gz').write_bytes(b'changed')
            with patch('release_promote.prepare_promote',return_value={'candidate_tree_sha':tree,'package_sha256':digest}):
                with self.assertRaisesRegex(ValueError,'archive digest changed'):
                    promote(**kwargs,readback=lambda *a:None,deploy=lambda *a:None)
            self.assertFalse(kwargs['attempt_file'].exists())
    def test_queue_cli_mutation_blocked_by_reservation_generation(self):
        with tempfile.TemporaryDirectory() as temp:
            root=Path(temp); kwargs,cid,*_=self.fixture(root)
            reservation=kwargs['queue_file'].with_name('queue.json.promotion-reservation.json')
            reservation.write_text(json.dumps({'candidate_id':cid,'token':'owner','generation':'g1'}))
            script=Path(__file__).with_name('release_queue.py')
            result=subprocess.run(['python3',str(script),'--file',str(kwargs['queue_file']),'transition',cid,'merged'],capture_output=True,text=True)
            self.assertNotEqual(result.returncode,0)
            self.assertIn('promotion reservation',result.stderr)
            self.assertEqual(json.loads(kwargs['queue_file'].read_text())['items'][0]['status'],'waiting_merge')
    def test_execute_rejects_non_linux_node(self):
        with patch('release_promote.platform.system',return_value='Darwin'):
            with self.assertRaisesRegex(ValueError,'Linux staging'):require_staging_node(Path('/missing'))
if __name__=='__main__':unittest.main()
