"""Resolve non-Go inputs to real source consumers in BOTH committed trees.

Unknown contracts widen their own lane; author declarations never waive tests.
"""
from __future__ import annotations
import json,re,subprocess,io,tarfile,hashlib
from pathlib import Path


def blob(root, rev, path):
    p=subprocess.run(['git','show',f'{rev}:{path}'],cwd=root,capture_output=True)
    return p.stdout.decode('utf-8',errors='replace') if p.returncode==0 else ''


def source_contents(root, rev):
    data=subprocess.check_output(['git','archive',rev],cwd=root)
    result={}
    with tarfile.open(fileobj=io.BytesIO(data)) as archive:
        for member in archive:
            p=member.name
            if member.isfile() and (p.endswith('.go') or p.startswith('web/') and p.endswith(('.ts','.js','.mjs'))):
                result[p]=archive.extractfile(member).read().decode('utf-8',errors='replace')
    return result


def document(path):
    return path in {'AGENTS.md','README.md'} or path.startswith('skills/') and path.endswith('.md') or path.startswith('docs/') and (Path(path).suffix.lower() in {'.md','.mdx','.png','.jpg','.jpeg','.svg','.webp','.pdf'} or path.startswith('docs/engineering/dedup/') and path.endswith('.json'))


def api_blocks(source):
    # OpenAPI path blocks are two-space entries; global schemas stay separate.
    parts=re.split(r'(?m)^  (/[^\n]+):\s*$',source)
    return {parts[i]:parts[i+1].split('\ncomponents:',1)[0] for i in range(1,len(parts),2)}


def component_blocks(source):
    """Canonical OpenAPI indentation; unresolved/global component changes widen."""
    body=source.split("\ncomponents:",1)[-1] if "\ncomponents:" in source else ""
    result={};section=None;name=None;lines=[]
    for line in body.splitlines():
        category=re.fullmatch(r"  ([A-Za-z0-9_]+):\s*",line)
        entry=re.match(r"    ([A-Za-z0-9_-]+):",line)
        if category or entry:
            if name:result[section+'/'+name]='\n'.join(lines)
            lines=[];name=None
            if category:section=category.group(1)
            elif section:name=entry.group(1);lines=[line]
        elif name:lines.append(line)
    if name:result[section+'/'+name]='\n'.join(lines)
    return result


def affected_api_routes(old,new):
    a,b=api_blocks(old),api_blocks(new)
    ca,cb=component_blocks(old),component_blocks(new)
    changed={key for key in ca.keys()|cb.keys() if ca.get(key)!=cb.get(key)}
    all_components={**ca,**cb}
    expanded=set(changed)
    while True:
        users={key for key,body in all_components.items() if any('#/components/'+ref in body for ref in expanded)}
        if users <= expanded:break
        expanded |= users
    routes={route for route in a.keys()|b.keys() if a.get(route)!=b.get(route) or
            any('#/components/'+ref in a.get(route,'')+'\n'+b.get(route,'') for ref in expanded)}
    header_a=re.split(r'(?m)^paths:\s*$',old)[0]
    header_b=re.split(r'(?m)^paths:\s*$',new)[0]
    unresolved = header_a!=header_b or bool(changed and not routes) or (not changed and (old.split('\ncomponents:',1)[-1] if '\ncomponents:' in old else '') != (new.split('\ncomponents:',1)[-1] if '\ncomponents:' in new else ''))
    # Authentication schemes and common parameter/response contracts are global.
    unresolved |= any(not key.startswith('schemas/') for key in changed)
    return routes,unresolved


def resolve(root:Path,base:str,head:str,paths:list[str]):
    mapped={}; unknown={}; checks=[]
    all_sources={rev:source_contents(root,rev) for rev in (base,head)} if any(p.startswith(('api/','migrations/')) or p in {'go.mod','go.sum'} for p in paths) else {}
    for path in sorted(paths):
        if path in {'web/donor-sources/source-index.json','web/donor-sources/source-lock.json'}:
            try:
                documents={rev:json.loads(blob(root,rev,path)) for rev in (base,head)}
                normalized={};canonical=set()
                for rev,doc in documents.items():
                    records=doc.get('contents',doc.get('entries',[]))
                    identities={record['id']:record['canonical_path'] for record in records}
                    for record in records:
                        name=record['canonical_path']
                        if name not in paths:continue
                        raw=subprocess.check_output(['git','show',rev+':'+name],cwd=root)
                        git_blob=hashlib.sha1(b'blob '+str(len(raw)).encode()+b'\0'+raw).hexdigest()
                        if record['source_git_blob_sha']!=git_blob or record['content_sha256']!=hashlib.sha256(raw).hexdigest() or record['bytes']!=len(raw):raise ValueError('source binding mismatch')
                        canonical.add(name)
                    def normalize(value):
                        if isinstance(value,list):return [normalize(x) for x in value]
                        if isinstance(value,dict):
                            linked=identities.get(value.get('id')) or identities.get(value.get('content_id'))
                            return {k:normalize(v) for k,v in value.items() if not (linked in canonical and k in {'source_git_blob_sha','content_sha256','bytes'})}
                        if isinstance(value,str) and value in identities:return identities[value]
                        return value
                    normalized[rev]=normalize(doc)
                if normalized[base]!=normalized[head] or not canonical:raise ValueError('source contract structure changed')
                mapped[path]=sorted({consumer for name in canonical for consumer in mapped.get(name,[name])})
                checks.append('source-binding')
            except (KeyError,ValueError,subprocess.SubprocessError):unknown[path]='source-binding-unmapped'
            continue
        if path=='docs/governance/retention-registry.json':
            mapped[path]=[];checks.append('retention');continue
        if path.startswith('api/'):
            old,new=blob(root,base,path),blob(root,head,path)
            routes,global_change=affected_api_routes(old,new)
            matches=set();missing=[]
            for route in routes:
                prefix=route.split('{',1)[0] if '{' in route else route
                found={p for files in all_sources.values() for p,s in files.items() if not p.endswith('_test.go') and (route in s or '{' in route and len(prefix)>5 and prefix in s)}
                if not found:missing.append(route)
                matches|=found
            checks.append('openapi')
            if global_change or missing:unknown[path]='api-global-or-unmapped-route';continue
            mapped[path]=sorted(matches);continue
        if path.startswith('migrations/') and path.endswith('.sql'):
            sql=blob(root,base,path)+'\n'+blob(root,head,path)
            tables=set(re.findall(r'(?i)\b(?:ALTER\s+TABLE|CREATE\s+TABLE|DROP\s+TABLE|REFERENCES|UPDATE|INSERT\s+INTO|DELETE\s+FROM)\s+(?:IF\s+(?:NOT\s+)?EXISTS\s+)?(?:public\.)?"?([a-zA-Z_][\w]*)',sql))
            matches=set();missing=[]
            for table in tables:
                found={p for files in all_sources.values() for p,s in files.items() if p.endswith('.go') and re.search(r'\b'+re.escape(table)+r'\b',s)}
                if not found:missing.append(table)
                matches|=found
            checks.append('migration')
            # Dynamic SQL/functions/types cannot be bounded by literal table names.
            dynamic = re.search(r'(?i)\b(?:DO\s+\$|EXECUTE\s|CREATE\s+(?:OR\s+REPLACE\s+)?(?:FUNCTION|PROCEDURE|TYPE|EXTENSION)|DROP\s+(?:FUNCTION|PROCEDURE|TYPE|EXTENSION))',sql)
            if not tables or missing or dynamic:unknown[path]='sql-table-consumer-unmapped';continue
            mapped[path]=sorted(matches);continue
        if path in {'go.mod','go.sum'}:
            old,new=blob(root,base,path),blob(root,head,path)
            # Toolchain/module/replace/exclude changes affect all Go packages.
            if path=='go.mod' and [l for l in old.splitlines() if re.match(r'^(module|go|toolchain|replace|exclude)\b',l)]!=[l for l in new.splitlines() if re.match(r'^(module|go|toolchain|replace|exclude)\b',l)]:
                unknown[path]='go-global-configuration';continue
            lines=set(old.splitlines())^set(new.splitlines());modules={l.split()[0] for l in lines if re.match(r'\s*[^\s]+\s+v[0-9]',l)}
            matches={p for files in all_sources.values() for p,s in files.items() if p.endswith('.go') and any(re.search(r'"'+re.escape(m)+r'(?:/|\")',s) for m in modules)}
            if not matches:unknown[path]='go-indirect-dependency-unmapped';continue
            mapped[path]=sorted(matches)
        elif path in {'package.json','package-lock.json'}:
            # Frontend build/test consumers only. Dynamic resolution stays broad
            # within frontend/browser, never adds unrelated backend or SDK.
            unknown[path]='npm-consumers';checks.append('npm')
    return {'mapped':mapped,'unknown':unknown,'contracts':sorted(set(checks))}
