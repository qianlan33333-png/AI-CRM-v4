#!/usr/bin/env python3
"""Durable release outbox; state and notifications share one atomic state file."""
from __future__ import annotations
import argparse
import fcntl
import json
import os
import time
import uuid
from pathlib import Path

EVENT_TYPES = {'handoff_ready', 'new_commit', 'blocked', 'needs_review', 'accepted_for_preview', 'returned_missing_evidence', 'returned_conflict', 'returned_test_failure', 'returned_business_failure', 'environment_blocked', 'ready_for_resubmit', 'production_released', 'state_changed'}
RETURN_TYPES = {'missing_evidence': 'returned_missing_evidence', 'merge_conflict': 'returned_conflict', 'test_failure': 'returned_test_failure', 'business_failure': 'returned_business_failure', 'environment_blocked': 'environment_blocked'}
LEASE_SECONDS = 300
MAX_ATTEMPTS = 5

def now(): return int(time.time())

def load(path: Path) -> dict:
    state = json.loads(path.read_text()) if path.exists() else {'schema': 2, 'items': [], 'returns': [], 'events': []}
    if state.get('schema') not in (1, 2): raise ValueError('invalid release state schema')
    for key in ('items', 'returns', 'events'): state.setdefault(key, [])
    return state

def save(path: Path, state: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_name(path.name + f'.{uuid.uuid4().hex}.tmp')
    try:
        with temporary.open('x') as handle:
            json.dump(state, handle, ensure_ascii=False, indent=2)
            handle.write('\n'); handle.flush(); os.fsync(handle.fileno())
        os.replace(temporary, path)
        fd = os.open(path.parent, os.O_RDONLY)
        try: os.fsync(fd)
        finally: os.close(fd)
    finally: temporary.unlink(missing_ok=True)

def event_key(value: dict) -> str:
    return ':'.join(str(value[k]) for k in ('event_type', 'origin_thread_id', 'candidate_id', 'commit_sha'))

def append(state: dict, value: dict) -> dict:
    if value.get('event_type') not in EVENT_TYPES and not str(value.get('event_type', '')).startswith('state_changed:'):
        raise ValueError('invalid event_type')
    for key in ('origin_thread_id', 'destination_thread_id', 'candidate_id', 'commit_sha', 'tree_sha'):
        if not isinstance(value.get(key), str) or not value[key].strip(): raise ValueError(f'missing {key}')
    for key in ('commit_sha', 'tree_sha'):
        if len(value[key]) != 40 or any(c not in '0123456789abcdef' for c in value[key]): raise ValueError(f'invalid {key}')
    key = event_key(value)
    payload = {k: value.get(k, '') for k in ('event_type', 'origin_thread_id', 'destination_thread_id', 'candidate_id', 'commit_sha', 'tree_sha', 'required_action')}
    payload['evidence'] = list(value.get('evidence', [])); payload['resubmit_conditions'] = list(value.get('resubmit_conditions', []))
    for optional in ('owner_thread_id', 'blocked_reason'):
        if optional in value: payload[optional] = value[optional]
    for existing in state['events']:
        if existing['payload']['idempotency_key'] == key:
            comparable = {k: existing['payload'].get(k, '') for k in payload}
            if comparable != payload: raise ValueError('idempotency key reused with different payload')
            return existing
    payload.update(event_id=str(uuid.uuid4()), idempotency_key=key, created_at=now())
    event = {'payload': payload,
             'delivery': {'status': 'pending', 'attempt': 0, 'lifetime_attempt': 0, 'history': [], 'next_attempt_at': now(), 'lease_token': None, 'lease_until': None, 'last_error': ''},
             'processing': {'status': 'unacknowledged', 'observations': []}}
    state['events'].append(event)
    return event

def find(state: dict, event_id: str) -> dict:
    for event in state['events']:
        if event['payload']['event_id'] == event_id: return event
    raise ValueError('event not found')

def claim(state: dict, event_id: str, *, at: int | None = None) -> dict:
    event = find(state, event_id); delivery = event['delivery']; moment = now() if at is None else at
    if delivery['status'] in ('sent', 'dead_letter'): raise ValueError('terminal event cannot be claimed')
    if delivery['status'] == 'sending' and delivery['lease_until'] > moment: raise ValueError('event lease is active')
    if delivery['next_attempt_at'] > moment: raise ValueError('event backoff is active')
    if delivery['status'] == 'sending':
        delivery.setdefault('history', []).append({'attempt': delivery['attempt'], 'result': 'lease_expired', 'at': moment})
    if delivery['attempt'] >= MAX_ATTEMPTS:
        delivery.update(status='dead_letter', lease_token=None, lease_until=None, last_error='delivery lease exhausted')
        delivery.setdefault('history', []).append({'attempt': delivery['attempt'], 'result': 'dead_letter', 'at': moment, 'error': 'delivery lease exhausted'})
        return event
    delivery.update(status='sending', attempt=delivery['attempt'] + 1, lease_token=str(uuid.uuid4()), lease_until=moment + LEASE_SECONDS)
    delivery['lifetime_attempt'] = delivery.get('lifetime_attempt', 0) + 1
    return event

def finish(state: dict, event_id: str, status: str, token: str, error: str = '', *, at: int | None = None, max_attempts: int = MAX_ATTEMPTS) -> dict:
    if status not in ('sent', 'failed'): raise ValueError('invalid finish status')
    event = find(state, event_id); delivery = event['delivery']
    if delivery['status'] != 'sending' or token != delivery['lease_token']: raise ValueError('delivery token does not own event')
    moment = now() if at is None else at
    delivery['status'] = 'sent' if status == 'sent' else ('dead_letter' if delivery['attempt'] >= max_attempts else 'failed')
    delivery.setdefault('history', []).append({'attempt': delivery['attempt'], 'result': delivery['status'], 'at': moment, 'error': error[:1000]})
    delivery['next_attempt_at'] = moment + min(3600, 30 * 2 ** delivery['attempt']) if status == 'failed' else None
    delivery.update(last_error=error[:1000], lease_token=None, lease_until=None)
    return event

def replay(state: dict, event_id: str) -> dict:
    event = find(state, event_id); delivery = event['delivery']
    if delivery['status'] != 'dead_letter': raise ValueError('only dead-letter events can be replayed')
    delivery.setdefault('history', []).append({'attempt': delivery['attempt'], 'result': 'manual_replay', 'at': now()})
    delivery.update(status='pending', attempt=0, next_attempt_at=now(), lease_token=None, lease_until=None, last_error='')
    return event

def acknowledge(state: dict, event_id: str, thread_id: str, observation: str) -> dict:
    event = find(state, event_id)
    if event['delivery']['status'] != 'sent': raise ValueError('event transport is not sent')
    if thread_id != event['payload']['destination_thread_id']: raise ValueError('acknowledging task mismatch')
    if not observation.strip(): raise ValueError('processing observation required')
    processing = event.setdefault('processing', {'status': 'unacknowledged', 'observations': []})
    if not any(x['thread_id'] == thread_id and x['observation'] == observation for x in processing['observations']):
        processing['observations'].append({'thread_id': thread_id, 'observation': observation, 'at': now()})
    processing['status'] = 'acknowledged'
    return event

def main():
    parser = argparse.ArgumentParser(); parser.add_argument('--state', type=Path, default=Path('release-coordinator/state.json'))
    sub = parser.add_subparsers(dest='command', required=True); sub.add_parser('show')
    p = sub.add_parser('claim'); p.add_argument('event_id')
    p = sub.add_parser('finish'); p.add_argument('event_id'); p.add_argument('status', choices=('sent', 'failed')); p.add_argument('token'); p.add_argument('--error', default='')
    p = sub.add_parser('replay'); p.add_argument('event_id')
    p = sub.add_parser('ack'); p.add_argument('event_id'); p.add_argument('thread_id'); p.add_argument('observation')
    args = parser.parse_args(); lock_path = args.state.with_name(args.state.name + '.lock'); lock_path.parent.mkdir(parents=True, exist_ok=True)
    with lock_path.open('a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX); state = load(args.state)
        if args.command == 'show': print(json.dumps(state, ensure_ascii=False, indent=2)); return
        if args.command == 'claim': result = claim(state, args.event_id)
        elif args.command == 'finish': result = finish(state, args.event_id, args.status, args.token, args.error)
        elif args.command == 'replay': result = replay(state, args.event_id)
        else: result = acknowledge(state, args.event_id, args.thread_id, args.observation)
        save(args.state, state); print(json.dumps(result, ensure_ascii=False, indent=2))

if __name__ == '__main__': main()
