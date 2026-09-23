#!/usr/bin/env python3
"""Publish a minimal root-authenticated release-fact bridge, never original receipts.

Run only from the installed release. It reads existing success/revocation facts;
there is no observe/import/assume-success mode and no database access.
"""
import argparse
import datetime as dt
import fcntl
import importlib.util
import json
import os
from pathlib import Path
import re
import stat
import sys
import tempfile

sys.dont_write_bytecode = True
ROOT = Path('/opt/aicrm')
RESULT = Path('/var/lib/aicrm-maintenance/governance-releases.json')
SOURCE = Path(__file__).resolve()
ROOT_UID = 0
ROOT_GID = 0
MAX_FACTS = 2048


def control(path, directory=False):
    info = path.lstat()
    if (info.st_uid != ROOT_UID or info.st_gid != ROOT_GID or info.st_mode & 0o022
            or stat.S_ISLNK(info.st_mode) or path.resolve() != path
            or (directory and not stat.S_ISDIR(info.st_mode))
            or (not directory and (not stat.S_ISREG(info.st_mode) or info.st_nlink != 1))):
        raise ValueError('untrusted_governance_control')
    return info


def source_helper():
    if os.geteuid() != ROOT_UID:
        raise ValueError('root_required')
    current = ROOT / 'current'
    info = current.lstat()
    if not stat.S_ISLNK(info.st_mode) or info.st_uid != ROOT_UID:
        raise ValueError('current_release_untrusted')
    target = current.resolve(strict=True)
    sha = target.name
    if not re.fullmatch(r'[a-f0-9]{40}', sha) or target != ROOT / 'releases' / sha or SOURCE != target / 'deploy' / 'publish-governance-releases.py':
        raise ValueError('installed_current_bridge_required')
    helper = SOURCE.with_name('cleanup-releases.py')
    for path in (ROOT, ROOT / 'releases', target, SOURCE.parent):
        control(path, True)
    for path in (SOURCE, helper):
        control(path)
    spec = importlib.util.spec_from_file_location('governance_release_cleanup', helper)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return sha, module


def project(cleanup, sha):
    cleanup.require_control_directory(ROOT / cleanup.SUCCESS_DIRECTORY, private=True)
    if os.path.lexists(ROOT / cleanup.SUCCESS_DIRECTORY / cleanup.SUCCESS_PENDING):
        raise ValueError('release_publication_pending')
    facts = {}
    directory = ROOT / cleanup.SUCCESS_DIRECTORY
    # Bound the directory read as well as the exported fact count. No hidden
    # truncation: over-capacity is an explicit unavailable bridge.
    for count, path in enumerate(directory.iterdir(), 1):
        if count > MAX_FACTS:
            raise ValueError('release_evidence_capacity')
        name = path.name
        # Match the success writer: interrupted atomic temporary files are not
        # published evidence. They still count toward the directory bound.
        if name.startswith('.pending-'):
            continue
        if re.fullmatch(r'[a-f0-9]{40}\.json', name):
            receipt = cleanup.read_success_receipt(ROOT, name[:-5])
            revoked = False
        elif re.fullmatch(r'revoked-[1-9][0-9]*-[a-f0-9]{40}\.json', name):
            receipt = cleanup.read_pending_success(ROOT, name)['candidate']
            revoked = True
        else:
            raise ValueError('unknown_release_receipt_file')
        sequence = receipt['sequence']
        if sequence in facts:
            raise ValueError('duplicate_release_sequence')
        facts[sequence] = {'sequence': sequence, 'release_sha': receipt['release_sha'],
                           'succeeded_at': receipt['succeeded_at'],
                           'receipt_digest': cleanup.canonical_digest(receipt), 'revoked': revoked}
    if not any(item['release_sha'] == sha and not item['revoked'] for item in facts.values()):
        raise ValueError('current_success_receipt_missing')
    return {'version': 1, 'generated_at': dt.datetime.now(dt.timezone.utc).isoformat(),
            'current_release': sha, 'facts': [facts[k] for k in sorted(facts)]}


def publish(value):
    for parent in (RESULT.parent.parent.parent, RESULT.parent.parent):
        control(parent, True)
    RESULT.parent.mkdir(mode=0o755, exist_ok=True)
    control(RESULT.parent, True)
    if os.path.lexists(RESULT):
        control(RESULT)
    raw = json.dumps(value, sort_keys=True, separators=(',', ':')).encode() + b'\n'
    if len(raw) > 1024 * 1024:
        raise ValueError('release_evidence_capacity')
    fd, temporary = tempfile.mkstemp(prefix='.governance-releases-', dir=RESULT.parent)
    try:
        os.fchown(fd, ROOT_UID, ROOT_GID)
        os.fchmod(fd, 0o644)
        with os.fdopen(fd, 'wb') as stream:
            stream.write(raw)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, RESULT)
        directory = os.open(RESULT.parent, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
    finally:
        if os.path.lexists(temporary):
            os.unlink(temporary)


def run(inherited=None):
    sha, cleanup = source_helper()
    path = ROOT / 'install-release.lock'
    expected = control(path)
    owned = inherited is None
    descriptor = os.open(path, os.O_RDWR | os.O_NOFOLLOW | os.O_NONBLOCK) if owned else inherited
    try:
        actual = os.fstat(descriptor)
        if (expected.st_dev, expected.st_ino) != (actual.st_dev, actual.st_ino):
            raise ValueError('installer_lock_mismatch')
        fcntl.flock(descriptor, fcntl.LOCK_EX | fcntl.LOCK_NB)
        if (ROOT / 'current').resolve(strict=True) != ROOT / 'releases' / sha:
            raise ValueError('release_changed')
        value = project(cleanup, sha)
        publish(value)
        print(json.dumps({'state': 'published', 'fact_count': len(value['facts'])}))
    finally:
        if owned:
            os.close(descriptor)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--lock-fd', type=int, choices=range(3, 65))
    args = parser.parse_args()
    try:
        run(args.lock_fd)
    except Exception:
        # Never expose paths, receipt bodies, or underlying exception text.
        print('{"state":"unavailable","code":"release_evidence_not_published"}', file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
