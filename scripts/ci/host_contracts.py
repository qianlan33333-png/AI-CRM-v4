"""Trusted whole-file Host contracts: imports, routes and SQL, never test-name hints.

Source hash mismatch/new carrier is always included. Shared Host production
changes retain its complete suite. Test declarations are discovered at runtime.
"""
import hashlib,json,re
from pathlib import Path


def select(root, paths, graph):
    if any(p.startswith('cmd/aicrm/') and not p.endswith('_test.go') for p in paths):
        return None
    scope={p['dir'].split('/')[1] for p in graph.get('selected_packages',[]) if p['dir'].startswith('internal/')}
    if not scope:return None
    registry=json.loads(Path(__file__).with_name('host-contracts.json').read_text())
    checks=[]
    for file in sorted((root/'cmd/aicrm').glob('*_test.go')):
        source=file.read_bytes();path=file.relative_to(root).as_posix();entry=registry.get('files',{}).get(path)
        if entry and entry['sha256']==hashlib.sha256(source).hexdigest() and (set(entry['domains']) - {'access','platform'}) and not (scope & set(entry['domains'])):
            continue
        for name in re.findall(r'(?m)^func\s+(Test[A-Za-z0-9_]+)\s*\(',source.decode()):
            if name in {'TestMain','TestDomesticReleaseInstalledAlipayCheckout'}:continue
            checks.append({'lane':'browser' if name.endswith('ChromiumJourney') else 'backend','path':path,'test':name})
    return checks
