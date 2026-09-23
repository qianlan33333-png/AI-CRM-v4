import hashlib,json,subprocess,tempfile,unittest,sys
from pathlib import Path
from unittest.mock import patch
sys.path.insert(0,str(Path(__file__).parent))
from release_deferred_acceptance import validate_deferred_batch
from release_control import prepare_promote

class DeferredTests(unittest.TestCase):
 def fixture(self,root):
  repo=root/'repo'; repo.mkdir()
  def git(*args,**kw): return subprocess.check_output(['git','-C',str(repo),*args],text=True,**kw).strip()
  git('init','-q'); git('config','user.name','test'); git('config','user.email','x@example.invalid'); (repo/'base').write_text('base'); git('add','base'); git('commit','-qm','base'); base=git('rev-parse','HEAD'); git('branch','main',base); git('remote','add','origin',str(repo))
  members=[]
  for idx,(project,item) in enumerate((('orders','shipping-fix'),('payments','callback-fix'))):
   (repo/item).write_text(item); git('add',item); git('commit','-qm',item); members.append({'project_key':project,'work_item':item,'origin_thread_id':f'thread-{idx}','commit_sha':git('rev-parse','HEAD'),'tree_sha':git('rev-parse','HEAD^{tree}')})
  head=git('rev-parse','HEAD'); tree=git('rev-parse','HEAD^{tree}'); preview=git('commit-tree',tree,'-p',base,'-p',head,input='preview\n'); package=root/'aicrm.tar.gz'; package.write_bytes(b'accepted package'); package_sha=hashlib.sha256(package.read_bytes()).hexdigest(); checks=[]
  for name in ('build_provenance','readyz','migration_sequence','artifact_integrity'):
   evidence=root/(name+'.json'); evidence.write_text('{}'); checks.append({'name':name,'passed':True,'evidence':{'path':str(evidence),'sha256':hashlib.sha256(evidence.read_bytes()).hexdigest()}})
  receipt=root/'technical.json'; receipt.write_text(json.dumps({'status':'technical_accepted','environment':'staging','candidate_id':'deferred-1','merge_preview_sha':preview,'candidate_tree_sha':tree,'package_sha256':package_sha,'package_path':str(package),'checks':checks}))
  journeys=[{'project_key':m['project_key'],'work_item':m['work_item'],'journeys':[f"verify real {m['work_item']} readback"]} for m in members]
  decision={'status':'business_acceptance_deferred_to_user','exception_id':'user-post-production-check-1','decided_by':'user','decided_at':'2026-09-23T00:00:00Z','due_at':'2026-09-24T00:00:00Z','decision_text':'User will execute the listed production journeys','owner':'user','scope':['orders','payments'],'unverified_business_journeys':journeys,'post_production_owner':'user','post_production_actions':['record exact journey evidence']}
  batch={'batch_id':'generic','candidate_id':'deferred-1','worktree':str(repo),'base_main_sha':base,'aggregate_head_sha':head,'merge_preview_sha':preview,'candidate_tree_sha':tree,'package_sha256':package_sha,'members':members,'business_acceptance':decision,'technical_acceptance':{'receipt_ref':str(receipt),'receipt_sha256':hashlib.sha256(receipt.read_bytes()).hexdigest()},'production_readback':{'status':'pending_technical_deployment','business_status':'deferred_to_user','required_technical_readback':['active_sha','readyz','package_sha256']}}
  manifest=root/'batch.json'; manifest.write_text(json.dumps(batch)); return manifest,batch
 def test_generic_deferred_batch_and_same_package_promotion(self):
  with tempfile.TemporaryDirectory() as temp:
   root=Path(temp); manifest,batch=self.fixture(root); self.assertEqual(len(validate_deferred_batch(manifest)['members']),2); queue=root/'queue.json'; queue.write_text(json.dumps({'items':[{'candidate_id':batch['candidate_id'],'status':'waiting_merge','candidate_tree_sha':batch['candidate_tree_sha'],'package_sha256':batch['package_sha256']}]}))
   with patch('release_control.live_remote_main',return_value=batch['aggregate_head_sha']): self.assertEqual(prepare_promote(manifest,queue,batch['aggregate_head_sha'],batch['base_main_sha'])['package_sha256'],batch['package_sha256'])
 def test_generic_journey_or_missing_due_date_rejected(self):
  with tempfile.TemporaryDirectory() as temp:
   root=Path(temp); manifest,batch=self.fixture(root); batch['business_acceptance']['unverified_business_journeys'][0]['journeys']=['production test']; manifest.write_text(json.dumps(batch))
   with self.assertRaisesRegex(ValueError,'precise'): validate_deferred_batch(manifest)
 def test_receipt_generator_requires_explicit_complete_evidence(self):
  with tempfile.TemporaryDirectory() as temp:
   root=Path(temp); package=root/'package.tar'; package.write_bytes(b'package'); output=root/'receipt.json'; evidence=[]
   for name in ('build_provenance','readyz','migration_sequence','artifact_integrity'):
    path=root/(name+'.json'); path.write_text('{}'); evidence.extend(['--evidence',name+'='+str(path)])
   base=['python3',str(Path(__file__).with_name('create_deferred_acceptance.py')),'technical-receipt','--candidate-id','candidate','--merge-preview-sha','a'*40,'--candidate-tree-sha','b'*40,'--package',str(package),'--output',str(output),*evidence]
   self.assertNotEqual(subprocess.run(base,capture_output=True).returncode,0); self.assertEqual(subprocess.run([*base,'--confirm-technical-checks-passed'],capture_output=True).returncode,0)
if __name__=='__main__': unittest.main()
