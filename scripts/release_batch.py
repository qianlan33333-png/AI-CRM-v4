#!/usr/bin/env python3
"""Validate a multi-origin batch without collapsing individual receipts."""
from __future__ import annotations
import hashlib
import json
import re
import subprocess
from pathlib import Path
from release_handoff import git, live_remote_main, validate_acceptance

SHA=re.compile(r'^[0-9a-f]{40}$')
DIGEST=re.compile(r'^[0-9a-f]{64}$')

def validate_batch(path: Path, *, check_live_main: bool = True) -> dict:
    batch=json.loads(path.read_text())
    required=('batch_id','candidate_id','worktree','base_main_sha','aggregate_head_sha','merge_preview_sha','candidate_tree_sha','package_sha256','members')
    if any(k not in batch for k in required): raise ValueError('batch manifest missing required fields')
    if not all(SHA.fullmatch(batch[k]) for k in ('base_main_sha','aggregate_head_sha','merge_preview_sha','candidate_tree_sha')):
        raise ValueError('batch Git SHA invalid')
    if not DIGEST.fullmatch(batch['package_sha256']): raise ValueError('batch package digest invalid')
    root=Path(batch['worktree'])
    if check_live_main and live_remote_main(root)!=batch['base_main_sha']: raise ValueError('batch base main stale')
    parents=git(root,'rev-list','--parents','-n','1',batch['merge_preview_sha']).split()[1:]
    if parents!=[batch['base_main_sha'],batch['aggregate_head_sha']]: raise ValueError('batch preview parents mismatch')
    if git(root,'rev-parse',batch['merge_preview_sha']+'^{tree}')!=batch['candidate_tree_sha']:
        raise ValueError('batch candidate tree mismatch')
    members=batch['members']
    if not isinstance(members,list) or not members or not all(isinstance(m,dict) for m in members):
        raise ValueError('batch requires at least one original work item')
    projects=[m.get('project_key') for m in members]; work_items=[m.get('work_item') for m in members]
    if any(not isinstance(v,str) or not v.strip() for v in projects+work_items):
        raise ValueError('batch project_key and work_item must be non-empty strings')
    if len(set(projects))!=len(projects) or len(set(work_items))!=len(work_items):
        raise ValueError('batch project_key and work_item must be unique')
    for member in members:
        for field in ('work_item','origin_thread_id','commit_sha','tree_sha','receipt_ref','receipt_sha256'):
            if not member.get(field): raise ValueError(f'batch member missing {field}')
        if not SHA.fullmatch(member['commit_sha']) or git(root,'rev-parse',member['commit_sha']+'^{tree}')!=member['tree_sha']:
            raise ValueError('member commit/tree mismatch')
        if subprocess.run(['git','-C',str(root),'merge-base','--is-ancestor',member['commit_sha'],batch['merge_preview_sha']]).returncode:
            raise ValueError('member commit absent from joint preview')
        receipt_path=Path(member['receipt_ref'])
        if not receipt_path.is_absolute(): receipt_path=path.parent/receipt_path
        if not receipt_path.is_file() or hashlib.sha256(receipt_path.read_bytes()).hexdigest()!=member['receipt_sha256']:
            raise ValueError('member receipt missing or digest mismatch')
        reference={'status':'accepted','receipt':str(receipt_path),'receipt_sha256':member['receipt_sha256'],
                   'candidate_id':batch['candidate_id'],'merge_preview_sha':batch['merge_preview_sha'],
                   'candidate_tree_sha':batch['candidate_tree_sha'],'package_sha256':batch['package_sha256']}
        validate_acceptance({'work_item':member['work_item'],'staging_acceptance':reference,
                             'merge_preview_sha':batch['merge_preview_sha'],'candidate_tree_sha':batch['candidate_tree_sha'],
                             'package_sha256':batch['package_sha256']},path)
    return batch
