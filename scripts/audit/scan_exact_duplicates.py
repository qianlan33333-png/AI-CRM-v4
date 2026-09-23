#!/usr/bin/env python3
"""Read-only, all-extension duplicate inventory of a pinned Git commit.

Python 3.10+, Git, standard library only. No deletion or repository execution.
Reads Git objects, NOT working-tree content. Output must be outside the repo.
This tool detects byte-identical files; it does not prove semantic equivalence,
unused code, safe removability, or browser/runtime dependency closure.
"""
from __future__ import annotations
import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import subprocess
import sys
from collections import defaultdict

ENV = dict(os.environ, GIT_NO_REPLACE_OBJECTS='1', GIT_NO_LAZY_FETCH='1', GIT_TERMINAL_PROMPT='0')

def git(repo: Path, *args: str, check: bool = True) -> subprocess.CompletedProcess:
    p = subprocess.run(['git', '-C', str(repo), *args], env=ENV,
                       stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=120)
    if check and p.returncode:
        raise RuntimeError(p.stderr.decode('utf8', 'replace').strip() or 'Git command failed')
    return p

def decode(b: bytes) -> str:
    return b.decode('utf8', 'surrogateescape')

def scan(repo: Path, ref: str, out: Path) -> dict:
    root = Path(decode(git(repo, 'rev-parse', '--show-toplevel').stdout).strip()).resolve()
    # scan() is a library entry point as well as the CLI backend. Normalize
    # both operands here: callers may use a spelling under /var that resolves
    # to /private/var on macOS, or a relative output path. Comparing a
    # normalized root with an unnormalized output can otherwise let an audit
    # write inside the checkout, violating this tool's read-only contract.
    out = Path(out).resolve()
    if out == root or root in out.parents:
        raise ValueError('Output must be outside the repository to keep the scan read-only.')
    if out.exists():
        raise ValueError('Output directory already exists; choose a new path.')
    # Reject promisor clones rather than risk lazy network downloads.
    partial = git(root, 'config', '--get', 'extensions.partialclone', check=False)
    promisor = git(root, 'config', '--get-regexp', r'^remote\..*\.promisor$', check=False)
    if partial.returncode == 0 or any(line.lower().endswith(b' true') for line in promisor.stdout.splitlines()):
        raise ValueError('Use a complete checkout (not a partial/promisor clone).')
    commit = decode(git(root, 'rev-parse', '--verify', '--end-of-options', ref+'^{commit}').stdout).strip()
    tree = decode(git(root, 'rev-parse', '--verify', commit+'^{tree}').stdout).strip()
    records = git(root, 'ls-tree', '-r', '-l', '-z', '--full-tree', commit).stdout.split(b'\0')
    inventory = []
    for record in records:
        if not record:
            continue
        header, path_b = record.split(b'\t', 1)
        mode, kind, oid, size_b = header.split()
        item = dict(path=decode(path_b), mode=mode.decode(), object_type=kind.decode(),
                    object_id=oid.decode(), bytes=None if size_b == b'-' else int(size_b))
        item['entry_kind'] = 'submodule' if kind == b'commit' else ('symlink' if mode == b'120000' else 'regular')
        inventory.append(item)
    inventory.sort(key=lambda r: r['path'])
    if len({r['path'] for r in inventory}) != len(inventory):
        raise RuntimeError('Duplicate paths in Git tree inventory.')
    sizes = {}
    for item in inventory:
        if item['object_type'] == 'blob':
            oid = item['object_id']
            if oid in sizes and sizes[oid] != item['bytes']:
                raise RuntimeError('Conflicting metadata for the same Git object.')
            sizes[oid] = item['bytes']
    object_details, errors = {}, []
    proc = subprocess.Popen(['git', '-C', str(root), 'cat-file', '--batch'], env=ENV,
                            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
    assert proc.stdin and proc.stdout
    try:
        for oid in sorted(sizes):
            proc.stdin.write((oid+'\n').encode('ascii')); proc.stdin.flush()
            header = proc.stdout.readline().strip().split()
            if len(header) != 3 or header[1] != b'blob':
                raise RuntimeError(f'Cannot read blob {oid}: {header!r}')
            n = int(header[2])
            if n != sizes[oid]:
                raise RuntimeError(f'Blob size mismatch: {oid}')
            h = hashlib.sha256(); remaining = n; prefix = b''
            while remaining:
                chunk = proc.stdout.read(min(1024*1024, remaining))
                if not chunk:
                    raise RuntimeError(f'Unexpected EOF in blob {oid}')
                h.update(chunk)
                if len(prefix) < 2048:
                    prefix += chunk[:2048-len(prefix)]
                remaining -= len(chunk)
            if proc.stdout.read(1) != b'\n':
                raise RuntimeError(f'Invalid batch framing for {oid}')
            object_details[oid] = dict(content_sha256=h.hexdigest(),
                is_lfs_pointer=prefix.startswith(b'version https://git-lfs.github.com/spec/v1\n'))
    except Exception as exc:
        errors.append(str(exc))
    finally:
        proc.stdin.close(); proc.stdout.close()
        try:
            proc.wait(timeout=10)
        except subprocess.TimeoutExpired:
            proc.kill(); proc.wait()
        if proc.returncode and not errors:
            errors.append(f'git cat-file exit code {proc.returncode}')
    for r in inventory:
        r.update(object_details.get(r['object_id'], {}))
    buckets = defaultdict(list)
    for r in inventory:
        if r['entry_kind'] == 'regular' and r['object_id'] in object_details:
            buckets[r['object_id']].append(r)
    duplicates = []
    for oid, items in buckets.items():
        if len(items) < 2:
            continue
        n = items[0]['bytes']
        duplicates.append(dict(group_id='blob:'+oid, content_sha256=items[0]['content_sha256'],
            file_count=len(items), bytes_per_copy=n, path_bytes=n*len(items),
            excess_path_bytes=n*(len(items)-1), mode_variants=sorted({r['mode'] for r in items}),
            extensions=sorted({PurePosixPath(r['path']).suffix.lower() or '(none)' for r in items}),
            paths=[r['path'] for r in items], review_status='unreviewed'))
    duplicates.sort(key=lambda g: (-g['excess_path_bytes'], g['group_id']))
    blobs = [r for r in inventory if r['object_type'] == 'blob']
    submodules = [r for r in inventory if r['entry_kind'] == 'submodule']
    lfs = [r['path'] for r in inventory if r.get('is_lfs_pointer')]
    root_rollup = defaultdict(lambda: dict(entries=0, blobs=0, verified_blobs=0))
    for r in inventory:
        top = r['path'].split('/')[0] if '/' in r['path'] else '(root files)'
        root_rollup[top]['entries'] += 1
        if r['object_type'] == 'blob':
            root_rollup[top]['blobs'] += 1
            root_rollup[top]['verified_blobs'] += int('content_sha256' in r)
    verified_paths = sum('content_sha256' in r for r in blobs)
    blob_complete = not errors and verified_paths == len(blobs)
    report = dict(schema_version=1, commit=commit, tree=tree,
        scope='All entries in the pinned superproject Git tree; no extension or directory exclusion.',
        exclusions='No untracked files. Symlinks are inventoried and hashed as links, not dereferenced. Submodule/LFS payloads require separate audit.',
        inventory_complete=True, git_blob_verification_complete=blob_complete,
        full_payload_inventory_complete=blob_complete and not submodules and not lfs,
        semantic_duplicate_audit_complete=False, dependency_audit_complete=False,
        total_entries=len(inventory), total_blob_paths=len(blobs), verified_blob_paths=verified_paths,
        unique_blob_objects=len(sizes), verified_unique_blob_objects=len(object_details),
        regular_duplicate_groups=len(duplicates), duplicate_file_paths=sum(g['file_count'] for g in duplicates),
        excess_path_bytes=sum(g['excess_path_bytes'] for g in duplicates),
        errors=errors, submodules=submodules, lfs_pointer_paths=lfs,
        root_coverage=dict(sorted(root_rollup.items())), duplicates=duplicates, inventory=inventory)
    out.mkdir(parents=True)
    # ASCII-escaped JSON preserves filenames containing invalid UTF-8 safely.
    (out/'duplicate-audit.json').write_text(json.dumps(report, ensure_ascii=True, indent=2)+'\n', encoding='utf8')
    lines=['# Pinned-commit exact-duplicate inventory', '', f'Commit: `{commit}`', f'Tree: `{tree}`', '',
        f'Git blob paths verified: **{verified_paths}/{len(blobs)}**.',
        f'Exact duplicate groups: **{len(duplicates)}**.',
        f'Excess path-content bytes: **{report["excess_path_bytes"]:,}**.', '',
        '**Not a safe-deletion or semantic-equivalence report.**',
        'Full payload inventory complete: '+str(report['full_payload_inventory_complete']), '',
        'See JSON for complete filenames, hashes, modes, LFS/submodule limitations and coverage.', '',
        '| Object | Copies | Bytes/copy | Excess path bytes |', '|---|---:|---:|---:|']
    for g in duplicates:
        lines.append(f'| `{g["group_id"]}` | {g["file_count"]} | {g["bytes_per_copy"]} | {g["excess_path_bytes"]} |')
    (out/'duplicate-audit.md').write_text('\n'.join(lines)+'\n',encoding='utf8')
    return report

def main() -> int:
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('repo', type=Path); p.add_argument('--ref',required=True)
    p.add_argument('--out',type=Path,required=True)
    a=p.parse_args()
    try:
        r=scan(a.repo.resolve(), a.ref, a.out.resolve())
        print(json.dumps({k:r[k] for k in ['commit','total_entries','verified_blob_paths','regular_duplicate_groups','excess_path_bytes','full_payload_inventory_complete']},indent=2))
        return 0 if r['full_payload_inventory_complete'] else 2
    except (OSError,ValueError,RuntimeError,subprocess.SubprocessError) as exc:
        print('Audit failed: '+str(exc),file=sys.stderr); return 2
if __name__=='__main__':
    raise SystemExit(main())
