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
    if status == 'stale_candidate':
        if old in {'merged', 'production', 'observing', 'released'}:
            raise ValueError('merged candidate cannot be invalidated')
    elif old not in ORDER or ORDER.index(old) + 1 >= len(ORDER) or ORDER[ORDER.index(old)+1] != status:
        raise ValueError(f'invalid transition: {old} -> {status}')
    if status in ACTIVE:
        if any(i['status'] in ACTIVE and i is not item for i in items):
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
    c = commands.add_parser('adopt-accepted'); c.add_argument('receipt', type=Path)
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
            if not any(i.get('candidate_id') == value.get('candidate_id') for i in queue['items']):
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
