#!/usr/bin/env python3
"""One-use, exact-candidate bridge from the observing old release to v4.

Tree equality is a content assertion, never a Git ancestry assertion. The
ordinary ancestry gate is bypassed only by callers that validate this record.
"""
from __future__ import annotations

import argparse
import fcntl
import hashlib
import json
import re
import subprocess
from datetime import datetime, timezone
from pathlib import Path
from urllib.request import Request, urlopen

from release_handoff import git, live_remote_main
from release_events import save

REPOSITORY = 'qianlan33333-png/AI-CRM-v4'
OLD_CANDIDATE = '6088e57ccf63b62dff46b4e1'
OLD_RELEASE = '4928f94e53a100ffab73132c0f096c4c4ff6e1d4'
OLD_TREE = '9c788ee796a4bfecc229675bf4e539a16c749166'
OLD_PACKAGE = 'c91db16b40ac458a8087a30b079977637bd4017f2607993529f0def4e76f1462'
OLD_RECEIPT = '89a50cf942f546293309af66ca936ed478cddcf26b1988d9067a3fbd5b83f71c'
V4_ROOT = 'ef0d4a4d25fa79fad6b1043f7efca460daf56de6'
SHA = re.compile(r'[0-9a-f]{40}\Z')
DIGEST = re.compile(r'[0-9a-f]{64}\Z')
PR_URL = re.compile(r'https://github\.com/qianlan33333-png/AI-CRM-v4/pull/([0-9]+)\Z')
PRODUCTION_STATES = {'waiting_merge', 'merged', 'production', 'observing', 'outcome_unknown'}


def same(actual, expected, label):
    if actual != expected:
        raise ValueError(f'first-v4 bridge {label} mismatch')


def evidence(ref, label):
    if not isinstance(ref, dict) or not isinstance(ref.get('path'), str) or not DIGEST.fullmatch(ref.get('sha256', '')):
        raise ValueError(f'first-v4 bridge {label} reference incomplete')
    path = Path(ref['path'])
    if not path.is_absolute() or not path.is_file():
        raise ValueError(f'first-v4 bridge {label} unavailable')
    raw = path.read_bytes()
    same(hashlib.sha256(raw).hexdigest(), ref['sha256'], f'{label} digest')
    return json.loads(raw)


def live_pr(number):
    request = Request(f'https://api.github.com/repos/{REPOSITORY}/pulls/{number}',
                      headers={'Accept': 'application/vnd.github+json', 'User-Agent': 'aicrm-first-v4-bridge'})
    with urlopen(request, timeout=20) as response:
        return json.load(response)


def live_required_checks(number):
    result = subprocess.run(['gh', 'pr', 'checks', str(number), '--repo', REPOSITORY,
                             '--required', '--json', 'name,state,bucket,link'],
                            text=True, capture_output=True, timeout=30)
    if not result.stdout.strip():
        raise ValueError('first-v4 bridge cannot read required GitHub checks')
    return json.loads(result.stdout)


def live_full_ci(run_id):
    def get(suffix):
        request = Request(f'https://api.github.com/repos/{REPOSITORY}/actions/runs/{run_id}{suffix}',
                          headers={'Accept': 'application/vnd.github+json', 'User-Agent': 'aicrm-first-v4-bridge'})
        with urlopen(request, timeout=20) as response: return json.load(response)
    run = get('')
    jobs = get('/jobs?per_page=100')
    if jobs.get('total_count', 0) > 100: raise ValueError('first-v4 bridge full CI job page overflow')
    return run, jobs.get('jobs', [])


def verify_promotion_freshness(batch_path: Path, merged_main: str, proof: dict):
    """Use the existing signed GitHub-main attestation on the offline staging node."""
    from release_freshness import verify
    if not isinstance(proof, dict) or any(not isinstance(proof.get(k), Path) for k in
                                          ('attestation', 'signature', 'allowed_signers', 'bundle')):
        raise ValueError('first-v4 bridge signed promotion freshness proof missing')
    batch = json.loads(batch_path.read_text())
    root = Path(batch['worktree'])
    receipt_ref = (batch.get('technical_acceptance') or {}).get('receipt_ref')
    if not receipt_ref:
        receipt_ref = batch['members'][0]['receipt_ref']
    receipt_path = Path(receipt_ref)
    if not receipt_path.is_absolute(): receipt_path = batch_path.parent / receipt_path
    receipt = json.loads(receipt_path.read_text())
    package = Path(receipt['package_path'])
    if not package.is_absolute(): package = receipt_path.parent / package
    expected = {'repository': REPOSITORY, 'branch': 'main', 'stage': 'promotion',
                'main_sha': merged_main, 'main_tree': git(root, 'rev-parse', merged_main + '^{tree}'),
                'pr_head_sha': batch['aggregate_head_sha'],
                'pr_head_tree': git(root, 'rev-parse', batch['aggregate_head_sha'] + '^{tree}'),
                'merge_preview_sha': batch['merge_preview_sha'],
                'candidate_tree_sha': batch['candidate_tree_sha']}
    value = verify(attestation_path=proof['attestation'], signature_path=proof['signature'],
                   allowed_signers=proof['allowed_signers'], identity='aicrm-release-command-center',
                   expected=expected, bundle=proof['bundle'], package=package, git_repository=root)
    same(value.get('package_sha256'), batch['package_sha256'], 'signed package digest')
    return value


def decision(record, kind, batch):
    value = record.get(kind)
    if not isinstance(value, dict):
        raise ValueError(f'first-v4 bridge {kind} missing')
    for key in ('decision_id', 'recorded_by_thread_id', 'authority_message_id', 'decision_text'):
        if not isinstance(value.get(key), str) or not value[key].strip():
            raise ValueError(f'first-v4 bridge {kind} lacks {key}')
    same(value.get('decision'), 'approved', f'{kind} decision')
    same(value['authority_message_id'], record['user_authorization']['user_message_id'], f'{kind} authority')
    for key in ('candidate_id', 'package_sha256', 'merge_preview_sha'):
        same(value.get(key), batch[key], f'{kind} {key}')
    proof = evidence(value.get('evidence'), f'{kind} evidence')
    for key in ('decision_id', 'recorded_by_thread_id', 'authority_message_id', 'decision_text', 'candidate_id',
                'package_sha256', 'merge_preview_sha', 'decision'):
        same(proof.get(key), value[key], f'{kind} proof {key}')
    same(proof.get('source'), 'operator_attestation', f'{kind} proof source')
    expires = datetime.fromisoformat(value['expires_at'].replace('Z', '+00:00'))
    if expires.tzinfo is None or expires <= datetime.now(timezone.utc):
        raise ValueError(f'first-v4 bridge {kind} authorization expired')


def validate(path: Path, *, batch_path: Path, queue: dict, merged_main: str | None = None,
             phase: str = 'admission', pr_reader=live_pr, check_reader=live_required_checks,
             ci_reader=live_full_ci, production_readback: dict | None = None) -> dict:
    if phase not in {'admission', 'promotion'}:
        raise ValueError('first-v4 bridge phase invalid')
    raw_bridge = path.read_bytes()
    record = json.loads(raw_bridge)
    bridge_digest = hashlib.sha256(raw_bridge).hexdigest()
    same(record.get('schema'), 1, 'schema')
    same(record.get('exception_id'), 'first-v4-joint-batch-v1', 'exception ID')
    same(record.get('repository'), REPOSITORY, 'repository')
    batch_bytes = batch_path.read_bytes()
    batch = json.loads(batch_bytes)
    same(record.get('batch_manifest_sha256'), hashlib.sha256(batch_bytes).hexdigest(), 'batch manifest digest')
    root = Path(batch['worktree'])
    base, preview, tree, package = (batch[k] for k in ('base_main_sha', 'merge_preview_sha', 'candidate_tree_sha', 'package_sha256'))
    for name, value in (('base', base), ('preview', preview), ('tree', tree)):
        if not SHA.fullmatch(value): raise ValueError(f'first-v4 bridge {name} invalid')
    if not DIGEST.fullmatch(package): raise ValueError('first-v4 bridge package invalid')
    for key in ('candidate_id', 'base_main_sha', 'merge_preview_sha', 'candidate_tree_sha', 'package_sha256'):
        same(record.get('batch', {}).get(key), batch[key], f'batch {key}')
    same(record.get('batch', {}).get('aggregate_head_sha'), batch['aggregate_head_sha'], 'batch aggregate head')
    aggregate = record.get('aggregate_pr') or {}
    aggregate_match = PR_URL.fullmatch(aggregate.get('pr_url', ''))
    if not aggregate_match: raise ValueError('first-v4 bridge aggregate PR URL invalid')
    aggregate_number = int(aggregate_match.group(1))
    same(aggregate.get('head_sha'), batch['aggregate_head_sha'], 'aggregate PR head')
    same(aggregate.get('tree_sha'), git(root, 'rev-parse', batch['aggregate_head_sha'] + '^{tree}'), 'aggregate PR tree')
    if phase == 'admission':
        aggregate_live = pr_reader(aggregate_number)
        same(aggregate_live.get('head', {}).get('sha'), batch['aggregate_head_sha'], 'live aggregate PR head')
        same(aggregate_live.get('base', {}).get('repo', {}).get('full_name'), REPOSITORY, 'live aggregate PR repository')
        same(aggregate_live.get('state'), 'open', 'aggregate PR state')
        same(aggregate_live.get('draft'), False, 'aggregate PR draft state')
        same(aggregate_live.get('base', {}).get('sha'), base, 'live aggregate PR base')
    governance = record.get('governance_pr') or {}
    governance_match = PR_URL.fullmatch(governance.get('pr_url', ''))
    if not governance_match or int(governance_match.group(1)) != 19:
        raise ValueError('first-v4 bridge governance PR URL invalid')
    governance_head = governance.get('head_sha', '')
    if not SHA.fullmatch(governance_head): raise ValueError('first-v4 bridge governance head invalid')
    same(governance.get('tree_sha'), git(root, 'rev-parse', governance_head + '^{tree}'), 'governance PR tree')
    if phase == 'admission':
        same(pr_reader(19).get('head', {}).get('sha'), governance_head, 'live governance PR head')
    subprocess.run(['git', '-C', str(root), 'merge-base', '--is-ancestor', governance_head,
                    batch['aggregate_head_sha']], check=True)
    run_id = record.get('full_ci_run_id')
    if not isinstance(run_id, int) or run_id <= 0: raise ValueError('first-v4 bridge full CI run ID missing')
    if phase == 'admission':
        required = check_reader(aggregate_number)
        if not isinstance(required, list) or not required or not any(c.get('name') == 'check' for c in required) or \
           any(c.get('bucket') != 'pass' or c.get('state') != 'SUCCESS' for c in required):
            raise ValueError('first-v4 bridge aggregate protected required check not green')
        # A check result may race a head update; re-read the PR before accepting it.
        same(pr_reader(aggregate_number).get('head', {}).get('sha'), batch['aggregate_head_sha'], 'aggregate PR head after checks')
        run, jobs = ci_reader(run_id)
        same(run.get('head_sha'), batch['aggregate_head_sha'], 'full CI head')
        same(run.get('event'), 'workflow_dispatch', 'full CI event')
        same(run.get('conclusion'), 'success', 'full CI conclusion')
        passed_jobs = {j.get('name') for j in jobs if j.get('conclusion') == 'success'}
        if not {'plan', 'governance', 'preflight', 'backend', 'frontend', 'browser', 'archive-sdk', 'check'}.issubset(passed_jobs):
            raise ValueError('first-v4 bridge full CI lanes incomplete')
    same(git(root, 'rev-list', '--parents', '-n', '1', preview).split()[1:],
         [base, batch['aggregate_head_sha']], 'preview parents')
    same(git(root, 'rev-parse', preview + '^{tree}'), tree, 'preview tree')
    same(git(root, 'rev-parse', V4_ROOT + '^{tree}'), OLD_TREE, 'actual v4 root tree')
    same(record.get('v4_root'), {'commit_sha': V4_ROOT, 'tree_sha': OLD_TREE}, 'v4 root')
    subprocess.run(['git', '-C', str(root), 'merge-base', '--is-ancestor', V4_ROOT, preview], check=True)
    current_main = merged_main if phase == 'promotion' else base
    same(git(root, 'rev-parse', 'refs/remotes/origin/main'), current_main, 'tracked main')
    if phase == 'admission': same(live_remote_main(root), current_main, 'live main')
    if phase == 'promotion': same(git(root, 'rev-parse', merged_main + '^{tree}'), tree, 'merged main tree')
    legacy = record.get('legacy', {})
    for key, expected in (('candidate_id', OLD_CANDIDATE), ('release_sha', OLD_RELEASE),
                          ('tree_sha', OLD_TREE), ('package_sha256', OLD_PACKAGE)):
        same(legacy.get(key), expected, f'legacy {key}')
    same(legacy.get('receipt', {}).get('sha256'), OLD_RECEIPT, 'legacy receipt digest')
    old_receipt = evidence(legacy.get('receipt'), 'legacy receipt')
    for key, expected in (('candidate_id', OLD_CANDIDATE), ('commit_sha', OLD_RELEASE),
                          ('tree_sha', OLD_TREE), ('package_sha256', OLD_PACKAGE), ('status', 'observing')):
        same(old_receipt.get(key), expected, f'legacy receipt {key}')
    same(old_receipt.get('business_acceptance', {}).get('status'), 'business_acceptance_deferred_to_user', 'legacy business status')
    old_readback = old_receipt.get('production_readback', {})
    for key, expected in (('release_sha', OLD_RELEASE), ('tree_sha', OLD_TREE), ('package_sha256', OLD_PACKAGE)):
        same(old_readback.get(key), expected, f'legacy receipt readback {key}')
    historic = evidence({'path': old_readback.get('evidence_path'), 'sha256': old_readback.get('evidence_sha256')}, 'legacy historic readback')
    same(historic.get('release_sha'), OLD_RELEASE, 'historic release')
    same(historic.get('tree_sha'), OLD_TREE, 'historic tree')
    same(historic.get('package_sha256'), OLD_PACKAGE, 'historic package')
    # The batch validator checks each member's commit/tree, preview ancestry,
    # accepted receipt and the actual package bytes.
    if batch.get('business_acceptance', {}).get('status') == 'business_acceptance_deferred_to_user':
        from release_deferred_acceptance import validate_deferred_batch
        validate_deferred_batch(batch_path, check_live_main=phase == 'admission')
        accepted_digest = batch['technical_acceptance']['receipt_sha256']
    else:
        from release_batch import validate_batch
        validate_batch(batch_path, check_live_main=phase == 'admission')
        accepted_digest = batch['members'][0]['receipt_sha256']
    same(record.get('batch', {}).get('accepted_receipt_sha256'), accepted_digest, 'accepted receipt')
    members = record.get('members')
    if not isinstance(members, list) or len(members) != len(batch['members']):
        raise ValueError('first-v4 bridge member count mismatch')
    numbers = []
    for bound, member in zip(members, batch['members']):
        match = PR_URL.fullmatch(bound.get('pr_url', '')) if isinstance(bound, dict) else None
        if not match: raise ValueError('first-v4 bridge PR URL invalid')
        number = int(match.group(1)); numbers.append(number)
        same(bound.get('commit_sha'), member['commit_sha'], 'member head')
        same(bound.get('tree_sha'), member['tree_sha'], 'member tree')
        same(git(root, 'rev-parse', member['commit_sha'] + '^{tree}'), member['tree_sha'], 'actual member tree')
        if phase == 'admission':
            pr = pr_reader(number)
            same(pr.get('head', {}).get('sha'), member['commit_sha'], 'live PR head')
            same(pr.get('base', {}).get('repo', {}).get('full_name'), REPOSITORY, 'live PR repository')
            same(pr.get('state'), 'open', 'live PR state')
            same(pr.get('base', {}).get('sha'), base, 'live PR base')
    if aggregate_number in numbers or aggregate_number == 19 or len(set(numbers)) != len(numbers) or \
       set(numbers) != {3, 13, 15}:
        raise ValueError('first-v4 bridge member set invalid')
    old = [i for i in queue.get('items', []) if i.get('candidate_id') == OLD_CANDIDATE]
    if len(old) != 1 or old[0].get('status') != 'observing' or old[0].get('candidate_tree_sha') != OLD_TREE or old[0].get('package_sha256') != OLD_PACKAGE:
        raise ValueError('first-v4 bridge old observing item changed')
    marker = old[0].get('first_v4_batch_bridge_candidate_id')
    if marker not in (None, batch['candidate_id']): raise ValueError('first-v4 bridge already consumed')
    if phase == 'promotion' and marker != batch['candidate_id']:
        raise ValueError('first-v4 bridge admission not consumed')
    if marker is not None:
        same(old[0].get('first_v4_batch_bridge_sha256'), bridge_digest, 'consumed bridge digest')
    owned = [i for i in queue.get('items', []) if i.get('candidate_id') == batch['candidate_id']]
    if len(owned) != 1 or owned[0].get('candidate_tree_sha', owned[0].get('tree_sha')) != tree or owned[0].get('package_sha256') != package:
        raise ValueError('first-v4 bridge candidate not queued exactly')
    if any(i.get('candidate_id') not in {OLD_CANDIDATE, batch['candidate_id']} and i.get('status') in PRODUCTION_STATES for i in queue.get('items', [])):
        raise ValueError('first-v4 bridge another production candidate active')
    if phase == 'promotion' and owned[0].get('status') != 'waiting_merge':
        raise ValueError('first-v4 bridge candidate not waiting_merge')
    authority = record.get('user_authorization')
    if not isinstance(authority, dict) or any(not isinstance(authority.get(k), str) or not authority[k].strip()
                                               for k in ('user_thread_id', 'user_message_id', 'decision_text')):
        raise ValueError('first-v4 bridge user authorization source missing')
    source = evidence(authority.get('evidence'), 'user authorization source')
    same(source.get('source'), 'user_message', 'user authorization source kind')
    for key in ('user_thread_id', 'user_message_id', 'decision_text'):
        same(source.get(key), authority[key], f'user authorization {key}')
    decision(record, 'queue_authorization', batch)
    decision(record, 'production_authorization', batch)
    queue_decision = record['queue_authorization']
    production_decision = record['production_authorization']
    if queue_decision['decision_id'] == production_decision['decision_id']:
        raise ValueError('first-v4 bridge production needs a separate operator decision')
    initial = evidence(record.get('production_readback'), 'admission production readback')
    if phase == 'promotion' and production_readback is None:
        raise ValueError('first-v4 bridge fresh promotion readback required')
    current = production_readback if phase == 'promotion' else initial
    for snapshot in (initial, current):
        same(snapshot.get('release_sha'), OLD_RELEASE, 'active release')
        same(snapshot.get('tree_sha'), OLD_TREE, 'active tree')
        same(snapshot.get('package_sha256'), OLD_PACKAGE, 'active package')
        same(snapshot.get('readyz', {}).get('release_sha'), OLD_RELEASE, 'readyz release')
        same(snapshot.get('readyz', {}).get('status'), 'ready', 'readyz health')
    observed = datetime.fromisoformat(current['observed_at'].replace('Z', '+00:00'))
    if observed.tzinfo is None or not 0 <= (datetime.now(timezone.utc) - observed).total_seconds() <= 600:
        raise ValueError('first-v4 bridge production readback stale')
    return {'candidate_id': batch['candidate_id'], 'preview_sha': preview, 'tree_sha': tree,
            'package_sha256': package, 'bridge_id': record['exception_id'], 'bridge_sha256': bridge_digest,
            'old_candidate_id': OLD_CANDIDATE}


def admit(path: Path, *, batch_path: Path, queue_path: Path, pr_reader=live_pr):
    """Consume the bridge for one accepted batch; preserve old observation."""
    batch = json.loads(batch_path.read_text())
    code_root = Path(__file__).resolve().parents[1]
    if git(code_root, 'rev-parse', 'HEAD') != batch.get('aggregate_head_sha') or \
       code_root.resolve() != Path(batch.get('worktree', '')).resolve():
        raise ValueError('first-v4 bridge admission must run from exact aggregate checkout')
    lock_path = queue_path.with_suffix('.lock')
    with lock_path.open('a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        queue = json.loads(queue_path.read_text())
        result = validate(path, batch_path=batch_path, queue=queue, pr_reader=pr_reader)
        old = next(i for i in queue['items'] if i['candidate_id'] == OLD_CANDIDATE)
        candidate = next(i for i in queue['items'] if i['candidate_id'] == result['candidate_id'])
        if old.get('first_v4_batch_bridge_candidate_id') is not None:
            raise ValueError('first-v4 bridge cannot be admitted twice')
        if candidate['status'] not in {'staging_acceptance', 'frozen'}:
            raise ValueError('first-v4 bridge requires a staging accepted candidate')
        previous_status = candidate['status']
        old['first_v4_batch_bridge_candidate_id'] = result['candidate_id']
        old['first_v4_batch_bridge_sha256'] = result['bridge_sha256']
        candidate['first_v4_batch_bridge_id'] = result['bridge_id']
        candidate['first_v4_batch_bridge_sha256'] = result['bridge_sha256']
        candidate['status'] = 'waiting_merge'
        candidate.setdefault('events', []).append({'from': previous_status, 'to': 'waiting_merge',
                                                   'kind': 'first_v4_batch_bridge', 'bridge_id': result['bridge_id']})
        save(queue_path, queue)
        return result


def bridge_transition(queue, candidate_id, status, bridge_sha256):
    old = next((i for i in queue['items'] if i.get('candidate_id') == OLD_CANDIDATE), None)
    owned = [i for i in queue['items'] if i.get('candidate_id') == candidate_id]
    if old is None or old.get('status') != 'observing' or old.get('first_v4_batch_bridge_candidate_id') != candidate_id or len(owned) != 1:
        raise ValueError('first-v4 bridge consumption changed')
    item = owned[0]
    if old.get('candidate_tree_sha') != OLD_TREE or old.get('package_sha256') != OLD_PACKAGE:
        raise ValueError('first-v4 bridge old production evidence changed')
    same(old.get('first_v4_batch_bridge_sha256'), bridge_sha256, 'old bridge digest')
    same(item.get('first_v4_batch_bridge_sha256'), bridge_sha256, 'candidate bridge digest')
    expected = {'merged': 'waiting_merge', 'production': 'merged', 'observing': 'production'}
    if item.get('status') != expected.get(status): raise ValueError('first-v4 bridge transition invalid')
    if any(i is not old and i is not item and i.get('status') in PRODUCTION_STATES for i in queue['items']):
        raise ValueError('first-v4 bridge production slot occupied')
    item.setdefault('events', []).append({'from': item['status'], 'to': status,
                                          'kind': 'first_v4_batch_bridge'})
    item['status'] = status


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['validate', 'admit'])
    parser.add_argument('--bridge', required=True, type=Path)
    parser.add_argument('--batch', required=True, type=Path)
    parser.add_argument('--queue', required=True, type=Path)
    args = parser.parse_args()
    if args.action == 'admit':
        result = admit(args.bridge, batch_path=args.batch, queue_path=args.queue)
    else:
        result = validate(args.bridge, batch_path=args.batch, queue=json.loads(args.queue.read_text()))
    print(json.dumps(result, ensure_ascii=False, indent=2))


if __name__ == '__main__': main()
