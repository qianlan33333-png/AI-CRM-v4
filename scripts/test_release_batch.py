import hashlib,json,subprocess,tempfile,unittest,sys
from pathlib import Path
from unittest.mock import patch
sys.path.insert(0,str(Path(__file__).parent))
from release_batch import validate_batch
from release_coordinator import return_to_origin

class BatchTests(unittest.TestCase):
 def test_generic_members_share_package_and_each_receipt(self):
  with tempfile.TemporaryDirectory() as temp:
   root=Path(temp); repo=root/'repo'; repo.mkdir()
   def git(*args,**kw): return subprocess.check_output(['git','-C',str(repo),*args],text=True,**kw).strip()
   git('init','-q'); git('config','user.name','test'); git('config','user.email','x@example.invalid'); (repo/'base').write_text('base'); git('add','base'); git('commit','-qm','base'); base=git('rev-parse','HEAD')
   members=[]
   for idx,(project,item) in enumerate((('orders','shipping-fix'),('payments','callback-fix'))):
    (repo/item).write_text(item); git('add',item); git('commit','-qm',item); members.append({'project_key':project,'work_item':item,'origin_thread_id':f'thread-{idx}','commit_sha':git('rev-parse','HEAD'),'tree_sha':git('rev-parse','HEAD^{tree}')})
   head=git('rev-parse','HEAD'); tree=git('rev-parse','HEAD^{tree}'); preview=git('commit-tree',tree,'-p',base,'-p',head,input='preview\n'); package=root/'package.tar'; package.write_bytes(b'same'); digest=hashlib.sha256(package.read_bytes()).hexdigest(); built=root/'built.json'; built.write_text(json.dumps({'status':'built','merge_preview_sha':preview,'candidate_tree_sha':tree,'package_sha256':digest}))
   for member in members:
    evidence=root/(member['work_item']+'.log'); evidence.write_text('verified journey'); receipt=root/(member['work_item']+'.json'); receipt.write_text(json.dumps({'status':'accepted','work_item':member['work_item'],'candidate_id':'joint-1','merge_preview_sha':preview,'candidate_tree_sha':tree,'package_sha256':digest,'package_path':str(package),'built_receipt':str(built),'built_receipt_sha256':hashlib.sha256(built.read_bytes()).hexdigest(),'journeys':[{'work_item':member['work_item'],'name':'business journey','expected':'verified','actual':'verified','command':'test','effect_mode':'virtual','passed':True,'evidence_path':str(evidence),'evidence_sha256':hashlib.sha256(evidence.read_bytes()).hexdigest()}]})); member.update(receipt_ref=str(receipt),receipt_sha256=hashlib.sha256(receipt.read_bytes()).hexdigest())
   batch={'batch_id':'generic','candidate_id':'joint-1','worktree':str(repo),'base_main_sha':base,'aggregate_head_sha':head,'merge_preview_sha':preview,'candidate_tree_sha':tree,'package_sha256':digest,'members':members}; manifest=root/'batch.json'; manifest.write_text(json.dumps(batch))
   with patch('release_batch.live_remote_main',return_value=base): self.assertEqual(len(validate_batch(manifest)['members']),2)
   batch['members'][1]['project_key']='orders'; manifest.write_text(json.dumps(batch))
   with patch('release_batch.live_remote_main',return_value=base),self.assertRaisesRegex(ValueError,'unique'): validate_batch(manifest)
 def test_batch_return_preserves_each_origin(self):
  members=[{'origin_thread_id':'one','commit_sha':'a'*40,'tree_sha':'b'*40},{'origin_thread_id':'two','commit_sha':'c'*40,'tree_sha':'d'*40}]; state={'schema':2,'items':[{'candidate_id':'joint','work_item':'generic-batch','members':members,'status':'integration_check','events':[]}],'returns':[],'events':[]}
  returned=return_to_origin(state,'joint','merge_conflict',['conflict'],'resolve',['new handoff'])
  self.assertEqual(len(returned['returns']),2); self.assertEqual({e['payload']['destination_thread_id'] for e in state['events']},{'one','two'})
if __name__=='__main__': unittest.main()
