#!/usr/bin/env python3
"""Stable, fail-closed entrypoints for release metadata. Never edits business source."""
from __future__ import annotations
import argparse
import fcntl
import json
import os
import re
import subprocess
from pathlib import Path
from release_handoff import validate, validate_acceptance, git, live_remote_main
from release_coordinator import register, return_to_origin
from release_events import load, save, replay, append

def locked_state(path: Path, operation):
    lock_path = path.with_name(path.name + '.lock'); lock_path.parent.mkdir(parents=True, exist_ok=True)
    with lock_path.open('a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        state = load(path)
        result = operation(state)
        save(path, state)
        return result

def verify_pr(value: dict) -> None:
    match = re.fullmatch(r'https://github\.com/([^/]+)/([^/]+)/pull/([0-9]+)', value['pr_url'].rstrip('/'))
    if not match: raise ValueError('PR URL must identify an exact GitHub repository and number')
    owner,repo,number=match.groups(); selected=f'{owner}/{repo}'
    remote=git(Path(value['worktree']),'remote','get-url','origin')
    if not any(remote.endswith(prefix+selected+suffix) for prefix in ('/',':') for suffix in ('','.git')):
        raise ValueError('PR repository differs from selected origin')
    try:
        raw=subprocess.check_output(['gh','api',f'repos/{selected}/pulls/{number}'],text=True,timeout=30)
    except (subprocess.CalledProcessError,subprocess.TimeoutExpired,FileNotFoundError) as exc:
        raise ValueError('cannot verify live PR head/base') from exc
    pr=json.loads(raw)
    if pr.get('head',{}).get('sha')!=value['commit_sha'] or pr.get('base',{}).get('sha')!=value['base_main_sha']:
        raise ValueError('live PR head or base moved')
    if pr.get('base',{}).get('repo',{}).get('full_name')!=selected:
        raise ValueError('PR targets another repository')

def preview(root: Path, branch: str, base: str, output: Path) -> dict:
    if output.exists(): raise ValueError('candidate manifest already exists')
    if git(root, 'status', '--porcelain'): raise ValueError('worktree is dirty')
    if git(root, 'symbolic-ref', '--short', 'HEAD') != branch: raise ValueError('branch mismatch')
    if git(root, 'rev-parse', 'refs/remotes/origin/main') != base: raise ValueError('origin/main moved')
    if live_remote_main(root) != base: raise ValueError('remote main moved')
    head = git(root, 'rev-parse', 'HEAD')
    proc = subprocess.run(['git', '-C', str(root), 'merge-tree', '--write-tree', base, head], capture_output=True, text=True)
    if proc.returncode != 0: raise ValueError('merge conflict; return to origin: ' + proc.stdout[-3000:])
    tree = proc.stdout.strip().splitlines()[0]
    if len(tree) != 40: raise ValueError('merge-tree did not return a tree SHA')
    merge_sha = subprocess.check_output(['git', '-C', str(root), 'commit-tree', tree, '-p', base, '-p', head], input='release merge preview\n', text=True).strip()
    manifest = {'schema': 1, 'base_main_sha': base, 'pr_head_sha': head, 'merge_preview_sha': merge_sha, 'candidate_tree_sha': tree, 'branch': branch}
    output.parent.mkdir(parents=True, exist_ok=True)
    with output.open('x') as handle: json.dump(manifest, handle, indent=2); handle.write('\n')
    return manifest

def verify_lineage(root: Path, base: str, production_sha: str, preview_sha: str, head: str | None = None) -> dict:
    live_main = git(root, 'rev-parse', 'refs/remotes/origin/main')
    if live_main != base: raise ValueError('stale candidate: origin/main moved')
    if live_remote_main(root) != base: raise ValueError('stale candidate: remote main moved')
    parents = git(root, 'rev-list', '--parents', '-n', '1', preview_sha).split()[1:]
    if len(parents) != 2 or parents[0] != base: raise ValueError('preview does not merge current main')
    if head and parents[1] != head: raise ValueError('preview PR head mismatch')
    proc = subprocess.run(['git', '-C', str(root), 'merge-base', '--is-ancestor', production_sha, preview_sha])
    if proc.returncode != 0: raise ValueError('candidate excludes active production SHA')
    if head and git(root, 'rev-parse', 'HEAD') != head: raise ValueError('PR head moved')
    return {'valid': True, 'base_main_sha': base, 'production_sha': production_sha, 'merge_preview_sha': preview_sha, 'pr_head_sha': head}

def prepare_promote(handoff_path: Path, queue_path: Path, merged_main: str, production_sha: str,
                    bridge_readback_path: Path | None = None) -> dict:
    value = json.loads(handoff_path.read_text()); root = Path(value['worktree'])
    bridge_ref = value.get('first_v4_batch_bridge')
    if 'members' in value:
        if value.get('business_acceptance',{}).get('status')=='business_acceptance_deferred_to_user':
            from release_deferred_acceptance import validate_deferred_batch
            value=validate_deferred_batch(handoff_path,check_live_main=False);receipt_path=Path(value['_technical_receipt_path'])
        else:
            from release_batch import validate_batch
            value=validate_batch(handoff_path,check_live_main=False)
            first=value['members'][0]; receipt_path=Path(first['receipt_ref'])
            if not receipt_path.is_absolute(): receipt_path=handoff_path.parent/receipt_path
        value['commit_sha']=value['aggregate_head_sha']
        value['staging_acceptance']={'candidate_id':value['candidate_id'],'receipt':str(receipt_path)}
    else:
        validate_acceptance(value, handoff_path)
    if live_remote_main(root) != merged_main: raise ValueError('merged main is not live origin/main')
    if git(root, 'rev-parse', merged_main + '^{tree}') != value['candidate_tree_sha']:
        raise ValueError('merged main tree differs from accepted package tree')
    parents = git(root, 'rev-list', '--parents', '-n', '1', value['merge_preview_sha']).split()[1:]
    if parents != [value['base_main_sha'], value['commit_sha']]: raise ValueError('invalid merge preview parents')
    queue = json.loads(queue_path.read_text())
    if bridge_ref:
        from release_first_v4_batch_bridge import OLD_RELEASE, validate as validate_first_v4_bridge
        if production_sha != OLD_RELEASE or 'members' not in value:
            raise ValueError('first-v4 bridge only applies to the exact old release and a batch')
        if bridge_readback_path is None:
            raise ValueError('first-v4 bridge requires fresh promotion readback')
        bridge_path = Path(bridge_ref)
        if not bridge_path.is_absolute(): bridge_path = handoff_path.parent / bridge_path
        validate_first_v4_bridge(bridge_path, batch_path=handoff_path, queue=queue,
                                 merged_main=merged_main, phase='promotion',
                                 production_readback=json.loads(bridge_readback_path.read_text()))
    elif subprocess.run(['git', '-C', str(root), 'merge-base', '--is-ancestor', production_sha, value['merge_preview_sha']]).returncode:
        raise ValueError('candidate excludes active production SHA')
    cid = value['staging_acceptance']['candidate_id']
    owned = [i for i in queue.get('items', []) if i.get('candidate_id') == cid]
    active = [i for i in queue.get('items', []) if i.get('status') in {'preview_building', 'staging_acceptance', 'frozen', 'waiting_merge', 'merged', 'production', 'observing'}]
    permitted = owned
    if bridge_ref:
        permitted = owned + [i for i in queue.get('items', []) if i.get('candidate_id') == OLD_CANDIDATE]
    if len(owned) != 1 or owned[0].get('status') != 'waiting_merge' or len(active) != len(permitted) or any(i not in permitted for i in active):
        raise ValueError('candidate does not exclusively own waiting_merge queue slot')
    if owned[0].get('tree_sha', owned[0].get('candidate_tree_sha')) != value['candidate_tree_sha'] or owned[0].get('package_sha256') != value['package_sha256']:
        raise ValueError('queue candidate tree/package differs from accepted receipt')
    return {'prepared': True, 'candidate_id': cid, 'merged_main_sha': merged_main,
            'candidate_tree_sha': value['candidate_tree_sha'], 'package_sha256': value['package_sha256'],
            'business_acceptance_status': value.get('business_acceptance',{}).get('status','accepted'),
            'deployment_adapter': 'guarded_staging_same_package_v1'}

def main():
    parser = argparse.ArgumentParser(); parser.add_argument('--state', type=Path, default=Path('release-coordinator/state.json')); parser.add_argument('--coordinator-thread-id', default=os.environ.get('AICRM_RELEASE_COORDINATOR_THREAD_ID','release-command-center'))
    sub = parser.add_subparsers(dest='command', required=True)
    p = sub.add_parser('handoff'); handoff = p.add_subparsers(dest='action', required=True)
    p = handoff.add_parser('validate'); p.add_argument('path', type=Path)
    for action in ('submit', 'resubmit'):
        p = handoff.add_parser(action); p.add_argument('path', type=Path); p.add_argument('candidate_id')
    p = handoff.add_parser('submit-batch'); p.add_argument('path', type=Path)
    p = handoff.add_parser('submit-deferred-batch'); p.add_argument('path', type=Path)
    p = sub.add_parser('release'); release = p.add_subparsers(dest='action', required=True)
    p = release.add_parser('candidate-preview'); p.add_argument('worktree', type=Path); p.add_argument('branch'); p.add_argument('base_main_sha'); p.add_argument('output', type=Path)
    p = release.add_parser('accept-staging'); p.add_argument('handoff', type=Path)
    p = release.add_parser('verify-lineage'); p.add_argument('worktree', type=Path); p.add_argument('base_main_sha'); p.add_argument('production_sha'); p.add_argument('merge_preview_sha'); p.add_argument('--head-sha')
    p = release.add_parser('return'); p.add_argument('candidate_id'); p.add_argument('failure_class'); p.add_argument('--evidence', action='append', required=True); p.add_argument('--action', dest='required_action', required=True); p.add_argument('--condition', action='append', required=True)
    p = release.add_parser('replay-event'); p.add_argument('event_id')
    p = release.add_parser('promote'); p.add_argument('--prepare', action='store_true'); p.add_argument('--execute', action='store_true'); p.add_argument('--handoff', type=Path); p.add_argument('--queue', type=Path); p.add_argument('--merged-main-sha'); p.add_argument('--production-sha'); p.add_argument('--bridge-readback', type=Path); p.add_argument('--host'); p.add_argument('--user',default='ubuntu'); p.add_argument('--key',type=Path); p.add_argument('--known-hosts',type=Path); p.add_argument('--attempt-file',type=Path); p.add_argument('--deploy-script',type=Path); p.add_argument('--staging-node-id',type=Path,default=Path('/opt/aicrm/staging-node-id.json'))
    args = parser.parse_args()
    if args.command == 'handoff':
        if args.action in {'submit-batch','submit-deferred-batch'}:
            if args.action=='submit-deferred-batch':
                from release_deferred_acceptance import validate_deferred_batch
                batch=validate_deferred_batch(args.path)
            else:
                from release_batch import validate_batch
                batch=validate_batch(args.path)
            def submit_batch(state):
                cid=batch['candidate_id']
                prior=next((i for i in state['items'] if i['candidate_id']==cid),None)
                if prior:
                    if prior.get('merge_preview_sha')!=batch['merge_preview_sha']: raise ValueError('batch candidate id reused')
                    return
                state['items'].append({'candidate_id':cid,'work_item':batch['batch_id'],'members':batch['members'],
                                       'merge_preview_sha':batch['merge_preview_sha'],'candidate_tree_sha':batch['candidate_tree_sha'],
                                       'base_main_sha':batch['base_main_sha'],'package_sha256':batch['package_sha256'],
                                       'business_acceptance_status':batch.get('business_acceptance',{}).get('status','accepted'),
                                       'status':'handoff_ready','events':[]})
                for member in batch['members']:
                    append(state,{'event_type':'handoff_ready','origin_thread_id':member['origin_thread_id'],
                                  'destination_thread_id':args.coordinator_thread_id,'candidate_id':cid,
                                  'commit_sha':member['commit_sha'],'tree_sha':member['tree_sha'],
                                  'evidence':[member.get('receipt_ref') or batch['technical_acceptance']['receipt_ref']]})
            locked_state(args.state,submit_batch)
            print(json.dumps({'registered':True,'candidate_id':batch['candidate_id'],'members':[m['work_item'] for m in batch['members']]}))
            return
        value = validate(args.path)
        if args.action == 'validate': result = {'valid': True, 'commit_sha': value['commit_sha'], 'candidate_tree_sha': value['candidate_tree_sha']}
        else:
            verify_pr(value)
            def submit(state):
                if args.action == 'resubmit':
                    if any(i['candidate_id'] == args.candidate_id for i in state['items']): raise ValueError('resubmit requires new candidate ID')
                    if any(i['work_item'] == value['work_item'] and i['commit_sha'] == value['commit_sha'] and i.get('staging_receipt_sha256') == value['staging_acceptance']['receipt_sha256'] for i in state['items']): raise ValueError('resubmit requires new commit or new accepted evidence')
                return register(state, value, args.candidate_id, args.coordinator_thread_id)
            locked_state(args.state, submit)
            result = {'registered': True, 'candidate_id': args.candidate_id, 'event_status': 'pending'}
    elif args.action == 'candidate-preview': result = preview(args.worktree, args.branch, args.base_main_sha, args.output)
    elif args.action == 'accept-staging':
        value = validate(args.handoff)
        result = {'accepted': True, 'work_item': value['work_item'], 'candidate_tree_sha': value['candidate_tree_sha'], 'package_sha256': value['package_sha256'], 'receipt_sha256': value['staging_acceptance']['receipt_sha256']}
    elif args.action == 'verify-lineage': result = verify_lineage(args.worktree, args.base_main_sha, args.production_sha, args.merge_preview_sha, args.head_sha)
    elif args.action == 'return': result = locked_state(args.state, lambda state: return_to_origin(state, args.candidate_id, args.failure_class, args.evidence, args.required_action, args.condition))
    elif args.action == 'replay-event': result = locked_state(args.state, lambda state: replay(state, args.event_id))
    elif args.action == 'promote':
        if args.prepare == args.execute: raise SystemExit('choose exactly one of --prepare or --execute')
        if not all((args.handoff, args.queue, args.merged_main_sha, args.production_sha)):
            raise SystemExit('prepare requires --handoff --queue --merged-main-sha --production-sha')
        if args.prepare: result = prepare_promote(args.handoff, args.queue, args.merged_main_sha, args.production_sha,
                                                  bridge_readback_path=args.bridge_readback)
        else:
            if not all((args.host, args.key, args.known_hosts, args.attempt_file, args.deploy_script)):
                raise SystemExit('execute requires --host --key --known-hosts --attempt-file --deploy-script')
            from release_promote import promote
            result = promote(handoff_path=args.handoff,queue_file=args.queue,merged_main=args.merged_main_sha,
                             production_sha=args.production_sha,host=args.host,user=args.user,key_file=args.key,
                             known_hosts_file=args.known_hosts,attempt_file=args.attempt_file,deploy_script=args.deploy_script,
                             staging_node_id=args.staging_node_id,bridge_readback_path=args.bridge_readback)
    else: raise ValueError('unsupported command')
    print(json.dumps(result, ensure_ascii=False, indent=2))

if __name__ == '__main__': main()
