#!/usr/bin/env python3
"""Create and validate immutable merge-preview release candidates."""
from __future__ import annotations
import argparse, hashlib, json, subprocess
from pathlib import Path

STATES = ('development','waiting_candidate','preview_building','staging_acceptance','frozen','waiting_merge','merged','production','observing','released','stale_candidate')
SHARED = ('cmd/aicrm/','migrations/','internal/platform/','internal/externaleffects/','.github/','deploy/','scripts/')

def git(*args: str) -> str:
    return subprocess.check_output(['git', *args], text=True).strip()

def changed(base: str, head: str) -> list[str]:
    return git('diff','--name-only',f'{base}...{head}').splitlines()

def classify(paths: list[str]) -> tuple[list[str], list[str]]:
    modules, shared = set(), set()
    for p in paths:
        if p.startswith('internal/payment/') or 'payment' in p: modules.add('payment')
        elif p.startswith('internal/segment/') or 'audience' in p: modules.add('audience')
        elif p.startswith('internal/groupops/') or 'groupops' in p: modules.add('groupops')
        elif p.startswith('internal/distribution/') or 'distribution' in p: modules.add('distribution')
        elif p.startswith('web/') or p.startswith('internal/webshell/'): modules.add('frontend')
        else: modules.add('runtime')
        for prefix in SHARED:
            if p.startswith(prefix): shared.add(prefix.rstrip('/'))
    if any(p.startswith('migrations/') for p in paths): shared.add('migrations')
    if any(p.startswith(('cmd/aicrm/','internal/platform/')) for p in paths): shared.add('composition')
    return sorted(modules), sorted(shared)

def preview(base: str, head: str, pr: str, out: Path) -> None:
    base = git('rev-parse', base); head = git('rev-parse', head)
    tree = subprocess.run(['git','merge-tree','--write-tree',base,head], text=True, capture_output=True)
    if tree.returncode != 0: raise SystemExit('merge preview has conflicts')
    tree_sha = tree.stdout.strip().splitlines()[0]
    merge_sha = head if base == head else git('commit-tree',tree_sha,'-p',base,'-p',head,'-m',f'candidate PR #{pr}: {head}')
    paths = changed(base, head)
    modules, shared = classify(paths)
    candidate_id = hashlib.sha256(f'{base}\0{head}\0{tree_sha}'.encode()).hexdigest()[:24]
    value = {'schema':1,'candidate_id':candidate_id,'pr_number':str(pr),'base_main_sha':base,'pr_head_sha':head,'merge_preview_sha':merge_sha,'tree_sha':tree_sha,'affected_modules':modules,'shared_dependencies':shared,'package_sha256':None,'status':'waiting_candidate'}
    out.write_text(json.dumps(value, ensure_ascii=False, indent=2)+'\n')

def validate(path: Path, merge_sha: str|None, main_sha: str|None) -> None:
    v=json.loads(path.read_text())
    required=('candidate_id','base_main_sha','merge_preview_sha','tree_sha','affected_modules','shared_dependencies','status')
    if v.get('schema') != 1 or any(k not in v for k in required): raise SystemExit('invalid candidate manifest')
    if git('rev-parse', v['merge_preview_sha']+'^{tree}') != v['tree_sha']: raise SystemExit('candidate tree mismatch')
    if v['status'] not in STATES: raise SystemExit('invalid candidate status')
    if merge_sha:
        actual=git('rev-parse',f'{merge_sha}^{{tree}}')
        if actual != v['tree_sha']: raise SystemExit('merged commit tree differs from candidate tree')
    if main_sha and git('rev-parse',main_sha) != v['base_main_sha']:
        raise SystemExit('candidate base main is stale')

def main():
    p=argparse.ArgumentParser(); sub=p.add_subparsers(dest='cmd',required=True)
    a=sub.add_parser('preview'); a.add_argument('--base',required=True); a.add_argument('--head',required=True); a.add_argument('--pr',required=True); a.add_argument('--out',type=Path,required=True)
    a=sub.add_parser('validate'); a.add_argument('manifest',type=Path); a.add_argument('--merge-sha'); a.add_argument('--main-sha')
    x=p.parse_args()
    if x.cmd=='preview': preview(x.base,x.head,x.pr,x.out)
    else: validate(x.manifest,x.merge_sha,x.main_sha)
if __name__=='__main__': main()
