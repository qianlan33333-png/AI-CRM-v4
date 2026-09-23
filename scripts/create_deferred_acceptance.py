#!/usr/bin/env python3
"""Create technical receipts and explicit user-deferred acceptance manifests."""
import argparse,json
from pathlib import Path
from release_handoff import file_sha256
from release_deferred_acceptance import CHECKS,validate_deferred_batch

def write_new(path,value):
 path.parent.mkdir(parents=True,exist_ok=True)
 with path.open('x') as f: json.dump(value,f,ensure_ascii=False,indent=2); f.write('\n')

def main():
 p=argparse.ArgumentParser(); sub=p.add_subparsers(dest='command',required=True)
 t=sub.add_parser('technical-receipt')
 for name in ('candidate-id','merge-preview-sha','candidate-tree-sha'): t.add_argument('--'+name,required=True)
 t.add_argument('--package',type=Path,required=True); t.add_argument('--evidence',action='append',required=True); t.add_argument('--output',type=Path,required=True); t.add_argument('--confirm-technical-checks-passed',action='store_true')
 b=sub.add_parser('batch'); b.add_argument('--members',type=Path,required=True); b.add_argument('--journeys',type=Path,required=True); b.add_argument('--technical-receipt',type=Path,required=True); b.add_argument('--worktree',type=Path,required=True)
 for name in ('batch-id','candidate-id','base-main-sha','aggregate-head-sha','merge-preview-sha','candidate-tree-sha','exception-id','decided-at','due-at','decision-text'): b.add_argument('--'+name,required=True)
 b.add_argument('--post-production-action',action='append',required=True); b.add_argument('--output',type=Path,required=True)
 a=p.parse_args()
 if a.command=='technical-receipt':
  if not a.confirm_technical_checks_passed: raise SystemExit('explicit --confirm-technical-checks-passed required')
  evidence={}
  for raw in a.evidence:
   name,sep,path=raw.partition('='); e=Path(path)
   if not sep or name in evidence or not e.is_absolute() or not e.is_file(): raise SystemExit('evidence must be unique name=/absolute/file')
   evidence[name]=e
  if set(evidence)!=CHECKS: raise SystemExit('exact four technical checks required: '+','.join(sorted(CHECKS)))
  value={'status':'technical_accepted','environment':'staging','candidate_id':a.candidate_id,'merge_preview_sha':a.merge_preview_sha,'candidate_tree_sha':a.candidate_tree_sha,'package_sha256':file_sha256(a.package),'package_path':str(a.package.resolve()),'checks':[{'name':name,'passed':True,'evidence':{'path':str(evidence[name].resolve()),'sha256':file_sha256(evidence[name])}} for name in sorted(CHECKS)]}
  write_new(a.output,value)
 else:
  members=json.loads(a.members.read_text()); journeys=json.loads(a.journeys.read_text())
  if not isinstance(members,list) or not members: raise SystemExit('members file must contain at least one task')
  if not isinstance(journeys,list): raise SystemExit('journeys file must be a list')
  receipt=json.loads(a.technical_receipt.read_text()); scope=sorted({m.get('project_key') for m in members})
  value={'batch_id':a.batch_id,'candidate_id':a.candidate_id,'worktree':str(a.worktree.resolve()),'base_main_sha':a.base_main_sha,'aggregate_head_sha':a.aggregate_head_sha,'merge_preview_sha':a.merge_preview_sha,'candidate_tree_sha':a.candidate_tree_sha,'package_sha256':receipt.get('package_sha256'),'members':members,'business_acceptance':{'status':'business_acceptance_deferred_to_user','exception_id':a.exception_id,'decided_by':'user','decided_at':a.decided_at,'due_at':a.due_at,'decision_text':a.decision_text,'owner':'user','scope':scope,'unverified_business_journeys':journeys,'post_production_owner':'user','post_production_actions':a.post_production_action},'technical_acceptance':{'receipt_ref':str(a.technical_receipt.resolve()),'receipt_sha256':file_sha256(a.technical_receipt)},'production_readback':{'status':'pending_technical_deployment','business_status':'deferred_to_user','required_technical_readback':['active_sha','readyz','package_sha256']}}
  write_new(a.output,value); validate_deferred_batch(a.output)
 print(json.dumps({'created':str(a.output),'sha256':file_sha256(a.output)},ensure_ascii=False))
if __name__=='__main__': main()
