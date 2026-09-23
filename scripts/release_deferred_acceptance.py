#!/usr/bin/env python3
"""Validate an explicit, generic user-deferred business acceptance batch."""
from __future__ import annotations
import re, subprocess
from pathlib import Path
from release_handoff import git, live_remote_main, file_sha256

SHA=re.compile(r'^[0-9a-f]{40}$'); DIGEST=re.compile(r'^[0-9a-f]{64}$')
EXCEPTION=re.compile(r'^[a-z0-9][a-z0-9-]{7,127}$')
CHECKS={'build_provenance','readyz','migration_sequence','artifact_integrity'}
GENERIC={'real production business journey','真实生产业务旅程','业务验收','production test'}

def resolve(parent,raw):
 p=Path(raw); return p if p.is_absolute() else parent/p

def evidence(path,value):
 p=resolve(path.parent,value.get('path',''))
 return p.is_file() and DIGEST.fullmatch(value.get('sha256','')) and file_sha256(p)==value['sha256']

def precise_journeys(values):
 return isinstance(values,list) and values and all(isinstance(v,str) and len(v.strip())>=8 and v.strip().lower() not in GENERIC for v in values)

def validate_deferred_batch(path:Path,*,check_live_main=True):
 import json
 batch=json.loads(path.read_text()); parent=path.parent
 for field in ('batch_id','candidate_id','worktree','base_main_sha','aggregate_head_sha','merge_preview_sha','candidate_tree_sha','package_sha256','members','business_acceptance','technical_acceptance','production_readback'):
  if field not in batch: raise ValueError('deferred batch missing '+field)
 if not all(SHA.fullmatch(batch[k]) for k in ('base_main_sha','aggregate_head_sha','merge_preview_sha','candidate_tree_sha')) or not DIGEST.fullmatch(batch['package_sha256']): raise ValueError('deferred batch SHA invalid')
 root=Path(batch['worktree'])
 if check_live_main and live_remote_main(root)!=batch['base_main_sha']: raise ValueError('deferred batch base main stale')
 if git(root,'rev-list','--parents','-n','1',batch['merge_preview_sha']).split()[1:]!=[batch['base_main_sha'],batch['aggregate_head_sha']]: raise ValueError('deferred preview parents mismatch')
 if git(root,'rev-parse',batch['merge_preview_sha']+'^{tree}')!=batch['candidate_tree_sha']: raise ValueError('deferred candidate tree mismatch')
 members=batch['members']
 if not isinstance(members,list) or not members or not all(isinstance(m,dict) for m in members): raise ValueError('deferred batch requires members')
 projects={m.get('project_key') for m in members}; pairs={(m.get('project_key'),m.get('work_item')) for m in members}
 if None in projects or len(projects)!=len(members) or len(pairs)!=len(members): raise ValueError('deferred members require unique project/work item identities')
 for member in members:
  for field in ('work_item','origin_thread_id','commit_sha','tree_sha'):
   if not member.get(field): raise ValueError('deferred member missing '+field)
  if git(root,'rev-parse',member['commit_sha']+'^{tree}')!=member['tree_sha'] or subprocess.run(['git','-C',str(root),'merge-base','--is-ancestor',member['commit_sha'],batch['merge_preview_sha']]).returncode: raise ValueError('deferred member absent from preview')
 decision=batch['business_acceptance']
 if decision.get('status')!='business_acceptance_deferred_to_user' or not EXCEPTION.fullmatch(decision.get('exception_id','')) or decision.get('decided_by')!='user': raise ValueError('invalid deferred user decision')
 if set(decision.get('scope',[]))!=projects or decision.get('owner')!='user' or not decision.get('decision_text') or not decision.get('decided_at') or not decision.get('due_at'): raise ValueError('incomplete deferred user decision')
 unverified=decision.get('unverified_business_journeys')
 if not isinstance(unverified,list) or {(x.get('project_key'),x.get('work_item')) for x in unverified if isinstance(x,dict)}!=pairs or any(not precise_journeys(x.get('journeys')) for x in unverified if isinstance(x,dict)): raise ValueError('all deferred journeys must remain precise and explicit')
 if decision.get('post_production_owner')!='user' or not decision.get('post_production_actions'): raise ValueError('user post-production actions missing')
 technical=batch['technical_acceptance']; receipt_path=resolve(parent,technical.get('receipt_ref',''))
 if not receipt_path.is_file() or file_sha256(receipt_path)!=technical.get('receipt_sha256'): raise ValueError('technical receipt missing or changed')
 receipt=json.loads(receipt_path.read_text())
 for field in ('candidate_id','merge_preview_sha','candidate_tree_sha','package_sha256'):
  if receipt.get(field)!=batch[field]: raise ValueError('technical receipt '+field+' mismatch')
 if receipt.get('status')!='technical_accepted' or receipt.get('environment')!='staging': raise ValueError('technical staging acceptance required')
 package=resolve(receipt_path.parent,receipt.get('package_path',''))
 if not package.is_file() or file_sha256(package)!=batch['package_sha256']: raise ValueError('technical accepted package mismatch')
 checks=receipt.get('checks')
 if not isinstance(checks,list) or {c.get('name') for c in checks if isinstance(c,dict)}!=CHECKS: raise ValueError('complete technical checks required')
 if any(c.get('passed') is not True or not evidence(receipt_path,c.get('evidence',{})) for c in checks): raise ValueError('technical check failed or evidence changed')
 production=batch['production_readback']
 if production.get('status')!='pending_technical_deployment' or production.get('business_status')!='deferred_to_user' or set(production.get('required_technical_readback',[]))!={'active_sha','readyz','package_sha256'}: raise ValueError('production readback contract missing')
 batch['_technical_receipt_path']=str(receipt_path); batch['_package_path']=str(package)
 return batch
