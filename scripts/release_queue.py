#!/usr/bin/env python3
"""Atomic FIFO release coordination; the active item survives process failure."""
import argparse
import fcntl
import json
import os
import time
from pathlib import Path

ORDER = ['waiting_candidate', 'preview_building', 'staging_acceptance', 'frozen',
         'waiting_merge', 'merged', 'production', 'observing', 'released']
ACTIVE = set(ORDER[1:-1])

def reservation_file(queue_file):
    return queue_file.with_name(queue_file.name + '.promotion-reservation.json')

def guarded_reservation(queue_file, candidate_id=None, token=None, generation=None):
    path = reservation_file(queue_file)
    if not path.exists(): return None
    value = json.loads(path.read_text())
    if candidate_id != value.get('candidate_id') or token != value.get('token') or generation != value.get('generation'):
        raise ValueError('promotion reservation owns release queue')
    return value

def change(queue, command, manifest=None, candidate_id=None, status=None, main=None):
    items = queue['items']
    if command == 'enqueue':
        if any(i['candidate_id'] == manifest['candidate_id'] for i in items):
            return
        items.append({**manifest, 'status': ORDER[0], 'events': []})
        return
    item = next((i for i in items if i['candidate_id'] == candidate_id), None)
    if item is None:
        raise ValueError('candidate not found')
    old = item['status']
    if status == 'released' and item.get('bootstrap_exception_id'):
        raise ValueError('bootstrap repair requires separate real-business acceptance reconciliation')
    if status == 'stale_candidate':
        if old in {'merged', 'production', 'observing', 'released'}:
            raise ValueError('merged candidate cannot be invalidated')
    elif old not in ORDER or ORDER.index(old) + 1 >= len(ORDER) or ORDER[ORDER.index(old)+1] != status:
        raise ValueError(f'invalid transition: {old} -> {status}')
    if status in ACTIVE:
        allowed_legacy = item.get('bootstrap_exception_id') == 'first-v4-alipay-entry-repair-v1'
        if any(i['status'] in ACTIVE and i is not item and not
               (allowed_legacy and i.get('candidate_id') == '6088e57ccf63b62dff46b4e1'
                and i.get('status') == 'observing' and i.get('repair_candidate_id') == candidate_id)
               for i in items):
            raise ValueError('another candidate owns the release queue')
        if status == 'preview_building' and next(i for i in items if i['status'] == ORDER[0]) is not item:
            raise ValueError('candidate is not at queue head')
    if status in {'preview_building', 'frozen', 'waiting_merge'}:
        if main != item['base_main_sha']:
            raise ValueError('candidate base main is stale')
    item['events'].append({'from': old, 'to': status, 'time': int(time.time())})
    item['status'] = status

def main():
    p = argparse.ArgumentParser()
    p.add_argument('--file', type=Path, default=Path('/opt/aicrm/release-queue.json'))
    commands = p.add_subparsers(dest='command', required=True)
    c = commands.add_parser('enqueue'); c.add_argument('manifest', type=Path)
    c = commands.add_parser('adopt-accepted'); c.add_argument('receipt', type=Path); c.add_argument('--bootstrap',type=Path); c.add_argument('--worktree',type=Path)
    c = commands.add_parser('transition'); c.add_argument('candidate_id'); c.add_argument('status'); c.add_argument('--main')
    commands.add_parser('show')
    a = p.parse_args()
    a.file.parent.mkdir(parents=True, exist_ok=True)
    with a.file.with_suffix('.lock').open('a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        if a.command != 'show':
            guarded_reservation(a.file, getattr(a, 'candidate_id', None), os.environ.get('AICRM_PROMOTION_TOKEN'), os.environ.get('AICRM_PROMOTION_GENERATION'))
        queue = json.loads(a.file.read_text()) if a.file.exists() else {'schema': 1, 'items': []}
        if a.command == 'show':
            print(json.dumps(queue, ensure_ascii=False)); return
        if a.command == 'adopt-accepted':
            value = json.loads(a.receipt.read_text())
            if value.get('status') != 'accepted': raise ValueError('accepted receipt required')
            bridge = None
            active = [i for i in queue['items'] if i.get('status') in ACTIVE]
            if a.bootstrap:
                if not a.worktree: raise ValueError('bootstrap requires worktree')
                from release_bootstrap import validate_bootstrap
                from release_control import verify_pr
                from release_handoff import git, live_remote_main
                import hashlib
                record=json.loads(a.bootstrap.read_text()); c=record['candidate']
                if live_remote_main(a.worktree)!=c['base_main_sha'] or git(a.worktree,'rev-parse','HEAD')!=c['head_sha']:
                    raise ValueError('bootstrap main or head moved')
                bridge=validate_bootstrap(a.bootstrap,root=a.worktree,base=c['base_main_sha'],head=c['head_sha'],
                    preview=c['preview_sha'],tree=c['tree_sha'],package=c['package_sha256'],
                    candidate_id=c['candidate_id'],queue=queue,phase='admission',
                    accepted_receipt_sha256=hashlib.sha256(a.receipt.read_bytes()).hexdigest())
                verify_pr({'pr_url':c['pr_url'],'worktree':str(a.worktree),'commit_sha':c['head_sha'],
                           'base_main_sha':c['base_main_sha']})
                for key, expected in (('candidate_id',c['candidate_id']),('merge_preview_sha',c['preview_sha']),
                                      ('candidate_tree_sha',c['tree_sha']),('package_sha256',c['package_sha256'])):
                    if value.get(key)!=expected: raise ValueError(f'accepted receipt {key} mismatch')
                if any(i.get('candidate_id')!=bridge['legacy_candidate_id'] for i in active):
                    raise ValueError('another candidate owns release queue')
            elif active:
                raise ValueError('another candidate owns the release queue')
            if not any(i.get('candidate_id') == value.get('candidate_id') for i in queue['items']):
                if bridge:
                    old=next(i for i in queue['items'] if i.get('candidate_id')==bridge['legacy_candidate_id'])
                    old['repair_candidate_id']=bridge['candidate_id']
                    old.setdefault('events',[]).append({'kind':'one_time_repair_bridge','candidate_id':bridge['candidate_id'],'time':int(time.time())})
                    value['bootstrap_exception_id']=bridge['exception_id']
                    value['repairs_observation_id']=bridge['legacy_candidate_id']
                queue['items'].append({**value, 'status': 'waiting_merge', 'events': [{'from': 'accepted', 'to': 'waiting_merge', 'time': int(time.time())}]})
        else:
            change(queue, a.command, manifest=json.loads(a.manifest.read_text()) if a.command == 'enqueue' else None,
                   candidate_id=getattr(a, 'candidate_id', None), status=getattr(a, 'status', None), main=getattr(a, 'main', None))
        tmp = a.file.with_suffix('.tmp')
        with tmp.open('w') as out:
            json.dump(queue, out, ensure_ascii=False, indent=2)
            out.flush(); os.fsync(out.fileno())
        os.replace(tmp, a.file)
if __name__ == '__main__':
    main()
