import importlib.util,json,os,tempfile,time,unittest
from pathlib import Path
spec=importlib.util.spec_from_file_location('cleanup',Path(__file__).with_name('cleanup-source-bundles.py'));m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
class CleanupTests(unittest.TestCase):
 def setUp(self):m.ROOT_UID=os.getuid()
 def fixture(self,root,status='released',complete=True,proof=True):
  root=root.resolve();bundles=root/'source-bundles';builds=root/'builds';bundles.mkdir();builds.mkdir();os.chmod(bundles,0o700);sha='a'*40;cid='c'*24
  suffixes=('.bundle','.json','.sh') if complete else ('.bundle','.json')
  files={}
  for suffix in suffixes:
   path=bundles/(sha+suffix);path.write_text(suffix);os.chmod(path,0o600);files[suffix]={'sha256':m.digest(path)}
  queue={'items':[{'candidate_id':cid,'merge_preview_sha':sha,'status':status,'events':[{'to':status,'time':100}]}]}
  if proof:
   (builds/sha).mkdir();receipt=builds/sha/'source-bundle-retention.json';receipt.write_text(json.dumps({'schema':1,'release_sha':sha,'candidate_id':cid,'terminal_status':status,'expired_at':100,'files':{k:v['sha256'] for k,v in files.items()}}));os.chmod(receipt,0o600)
  return bundles,builds,queue,sha
 def test_inventory_and_apply_exact_group(self):
  with tempfile.TemporaryDirectory() as temp:
   bundles,builds,queue,sha=self.fixture(Path(temp));plan=m.inventory(bundles,builds,queue,200,'f'*40)
   self.assertEqual([x['sha'] for x in plan['eligible']],[sha]);result=m.apply(bundles,builds,queue,200,'f'*40,plan)
   self.assertEqual(result['deleted'],[sha]);self.assertEqual(list(bundles.iterdir()),[])
 def test_active_unknown_missing_proof_and_incomplete_are_protected(self):
  for kwargs,reason in (({'status':'waiting_merge'},'active_unknown_or_recent'),({'proof':False},'retirement_proof_missing'),({'complete':False},'incomplete_group')):
   with self.subTest(reason=reason),tempfile.TemporaryDirectory() as temp:
    bundles,builds,queue,sha=self.fixture(Path(temp),**kwargs);plan=m.inventory(bundles,builds,queue,200,'f'*40)
    self.assertEqual(plan['eligible'],[]);self.assertEqual(plan['protected'][0]['reason'],reason)
 def test_symlink_unknown_and_changed_plan_refuse(self):
  with tempfile.TemporaryDirectory() as temp:
   root=Path(temp);bundles,builds,queue,sha=self.fixture(root);(bundles/'foreign').write_text('x')
   with self.assertRaisesRegex(m.Refuse,'unknown'):m.inventory(bundles,builds,queue,200,'f'*40)
  with tempfile.TemporaryDirectory() as temp:
   root=Path(temp);bundles,builds,queue,sha=self.fixture(root);target=bundles/(sha+'.bundle');target.unlink();target.symlink_to('/etc/passwd')
   with self.assertRaisesRegex(m.Refuse,'unsafe'):m.inventory(bundles,builds,queue,200,'f'*40)
  with tempfile.TemporaryDirectory() as temp:
   root=Path(temp);bundles,builds,queue,sha=self.fixture(root);plan=m.inventory(bundles,builds,queue,200,'f'*40);(bundles/(sha+'.bundle')).write_text('changed')
   with self.assertRaisesRegex(m.Refuse,'stale'):m.apply(bundles,builds,queue,200,'f'*40,plan)
 def test_duplicate_sha_with_any_active_record_is_protected(self):
  with tempfile.TemporaryDirectory() as temp:
   bundles,builds,queue,sha=self.fixture(Path(temp))
   queue['items'].append({'candidate_id':'d'*24,'merge_preview_sha':sha,'status':'waiting_merge','events':[{'to':'waiting_merge','time':100}]})
   plan=m.inventory(bundles,builds,queue,200,'f'*40)
   self.assertEqual(plan['eligible'],[]);self.assertEqual(plan['protected'][0]['reason'],'active_unknown_or_recent')
 def test_current_active_release_is_protected_even_when_released(self):
  with tempfile.TemporaryDirectory() as temp:
   bundles,builds,queue,sha=self.fixture(Path(temp));plan=m.inventory(bundles,builds,queue,200,sha)
   self.assertEqual(plan['eligible'],[]);self.assertEqual(plan['protected'][0]['reason'],'active_release')
 def test_retirement_receipt_must_be_root_owned(self):
  with tempfile.TemporaryDirectory() as temp:
   bundles,builds,queue,sha=self.fixture(Path(temp));m.ROOT_UID=os.getuid()+1
   plan=m.inventory(bundles,builds,queue,200,'f'*40)
   self.assertEqual(plan['eligible'],[]);self.assertEqual(plan['protected'][0]['reason'],'retirement_proof_missing')
 def test_active_release_requires_current_and_success_receipt(self):
  with tempfile.TemporaryDirectory() as temp:
   root=Path(temp).resolve();sha='e'*40;(root/'releases'/sha).mkdir(parents=True);(root/'current').symlink_to(root/'releases'/sha)
   success=root/'release-success';success.mkdir();(success/(sha+'.json')).write_text(json.dumps({'version':1,'release_sha':sha,'sequence':1}))
   self.assertEqual(m.trusted_active(root/'current',success),sha)
   (success/(sha+'.json')).unlink()
   with self.assertRaisesRegex(m.Refuse,'untrusted|unavailable'):m.trusted_active(root/'current',success)
if __name__=='__main__':unittest.main()
