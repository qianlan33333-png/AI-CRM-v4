#!/usr/bin/env python3
"""Apply fixed governance gates with a protected, same-release recovery record."""
import argparse
import base64
from contextlib import contextmanager
import fcntl
import hashlib
import json
import os
from pathlib import Path
import pwd
import re
import shlex
import signal
import stat
import subprocess
import tempfile
import time
import urllib.request

SOURCE = Path(__file__).resolve()
ENV_FILE = Path('/etc/aicrm/aicrm.env')
CURRENT = Path('/opt/aicrm/current')
RELEASES = Path('/opt/aicrm/releases')
LOCK = Path('/opt/aicrm/install-release.lock')
RECOVERY = Path('/etc/aicrm/.ops-runtime-recovery.json')
PROOF = Path('/etc/aicrm/ops-feishu-target.json')
CREDENTIAL = Path('/etc/aicrm/ops-feishu-webhook')
TARGET = 'original-ops-group'
SERVICES = ('aicrm.service', 'aicrm-effects-worker.service')
KEYS = ('AICRM_OPS_ENABLED', 'AICRM_OPS_NOTIFICATION_ENABLED', 'AICRM_OPS_RETENTION_ENABLED', 'AICRM_OPS_TARGET_REF', 'AICRM_OPS_WEBHOOK_FILE')
JOURNAL_KEYS = {'version', 'release_sha', 'original_env_b64', 'original_sha256', 'updated_sha256', 'original_gid'}


class ConfigurationInterrupted(BaseException):
    """Must bypass ordinary transient-readiness exception handling."""


def digest(raw):
    return hashlib.sha256(raw).hexdigest()


def strict_json(raw):
    def pairs(items):
        result = {}
        for key, value in items:
            if key in result:
                raise ValueError('duplicate protected record key')
            result[key] = value
        return result
    return json.loads(raw, object_pairs_hook=pairs)


def read_private(path, uid=0, maximum=1048576):
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(fd, 'rb') as stream:
        info = os.fstat(stream.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_uid != uid or stat.S_IMODE(info.st_mode) != 0o600 or info.st_nlink != 1 or not 0 < info.st_size <= maximum:
            raise ValueError('protected file owner or mode invalid')
        raw = stream.read(maximum + 1)
        if len(raw) != info.st_size:
            raise ValueError('protected file changed while reading')
    return raw, info


def render(original: bytes, enabled: bool, retention: bool) -> bytes:
    if retention and not enabled:
        raise ValueError('retention requires inspections')
    # Preserve unrelated bytes. Reject multiline quotes/continuations rather
    # than interpreting a line inside a credential as a configuration key.
    kept, seen = [], set()
    for line in original.splitlines(keepends=True):
        text = line.decode().strip()
        if not text or text.startswith(('#', ';')):
            kept.append(line)
            continue
        key, separator, value = text.partition('=')
        key = key.strip()
        if not separator or not re.fullmatch(r'[A-Za-z_][A-Za-z_0-9]*', key) or '\x00' in text:
            raise ValueError('unsupported runtime configuration syntax')
        shlex.split(value, comments=False, posix=True)
        if key in seen:
            raise ValueError('duplicate runtime configuration key')
        seen.add(key)
        if key not in KEYS:
            kept.append(line)
    values = ['true' if enabled else 'false', 'true' if enabled else 'false', 'true' if retention else 'false', TARGET, str(CREDENTIAL)]
    prefix = b''.join(kept)
    if prefix and not prefix.endswith(b'\n'):
        prefix += b'\n'
    return prefix + ('\n'.join(k + '=' + v for k, v in zip(KEYS, values)) + '\n').encode()


def sync_directory(path):
    fd = os.open(path, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def replace(path, content, gid=0):
    fd, name = tempfile.mkstemp(prefix='.ops-runtime-', dir=path.parent)
    try:
        with os.fdopen(fd, 'wb') as stream:
            os.fchmod(stream.fileno(), 0o600)
            os.fchown(stream.fileno(), 0, gid)
            stream.write(content)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(name, path)
        sync_directory(path.parent)
    finally:
        if os.path.exists(name):
            os.unlink(name)


@contextmanager
def block_termination():
    previous = signal.pthread_sigmask(signal.SIG_BLOCK, {signal.SIGINT, signal.SIGTERM})
    try:
        yield
    finally:
        signal.pthread_sigmask(signal.SIG_SETMASK, previous)


def create_recovery(sha, original, updated, gid):
    # All callers hold the shared installer lock; the parent is root-owned.
    if os.path.lexists(RECOVERY):
        raise FileExistsError('protected recovery already exists')
    record = {'version': 1, 'release_sha': sha, 'original_env_b64': base64.b64encode(original).decode(), 'original_sha256': digest(original), 'updated_sha256': digest(updated), 'original_gid': gid}
    fd, name = tempfile.mkstemp(prefix='.ops-recovery-', dir=RECOVERY.parent)
    try:
        with block_termination():
            with os.fdopen(fd, 'wb') as stream:
                os.fchmod(stream.fileno(), 0o600)
                os.fchown(stream.fileno(), 0, 0)
                stream.write(json.dumps(record, separators=(',', ':')).encode())
                stream.flush()
                os.fsync(stream.fileno())
            # Rename a fully fsynced file atomically. Unlike hard-link publish,
            # SIGKILL cannot leave a two-link journal that recovery would reject.
            if os.path.lexists(RECOVERY):
                raise FileExistsError('protected recovery already exists')
            os.rename(name, RECOVERY)
            sync_directory(RECOVERY.parent)
    finally:
        if os.path.exists(name):
            os.unlink(name)


def assert_current(sha):
    if os.readlink(CURRENT) != str(RELEASES / sha):
        raise RuntimeError('installed release changed')


def assert_source(sha):
    if SOURCE != RELEASES / sha / 'deploy/configure-ops-runtime.py':
        raise RuntimeError('installed release helper required')


def restart():
    subprocess.run(['systemctl', 'restart', *SERVICES], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=60)


def verify_processes(sha):
    expected = str(RELEASES / sha / 'bin/aicrm')
    for unit in SERVICES:
        subprocess.run(['systemctl', 'is-active', '--quiet', unit], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=5)
        output = subprocess.run(['systemctl', 'show', unit, '-p', 'MainPID', '--value'], check=True, capture_output=True, text=True, timeout=5).stdout.strip()
        if not re.fullmatch('[1-9][0-9]*', output) or os.readlink('/proc/' + output + '/exe') != expected:
            raise RuntimeError('runtime process release mismatch')


def verify(sha):
    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, *args, **kwargs):
            return None
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
    for _ in range(30):
        assert_current(sha)
        try:
            with opener.open('http://127.0.0.1:8080/readyz', timeout=2) as response:
                raw = response.read(65537)
            if len(raw) <= 65536:
                data = strict_json(raw)
                if data.get('status') == 'ready' and data.get('release_sha') == sha:
                    verify_processes(sha)
                    assert_current(sha)
                    return
        except Exception:
            pass
        time.sleep(1)
    raise RuntimeError('governance readiness unavailable')


def validate_target():
    raw, _ = read_private(PROOF, maximum=4096)
    proof = strict_json(raw)
    if not isinstance(proof, dict) or set(proof) != {'version', 'target_ref', 'credential_sha256'} or type(proof['version']) is not int or proof['version'] != 1 or proof['target_ref'] != TARGET or not isinstance(proof['credential_sha256'], str) or not re.fullmatch('[0-9a-f]{64}', proof['credential_sha256']):
        raise ValueError('original notification proof invalid')
    raw, _ = read_private(CREDENTIAL, uid=pwd.getpwnam('aicrm').pw_uid, maximum=4096)
    if digest(raw) != proof['credential_sha256']:
        raise ValueError('original notification credential mismatch')
    value = raw.decode().strip()
    if not re.fullmatch(r'https://(?:open\.feishu\.cn|open\.larksuite\.com)/open-apis/bot/v2/hook/[A-Za-z0-9-]{16,128}', value):
        raise ValueError('original notification endpoint invalid')


def recover(sha, restart_fn=restart, verify_fn=verify):
    assert_current(sha)
    raw, _ = read_private(RECOVERY, maximum=1500000)
    record = strict_json(raw)
    if not isinstance(record, dict) or set(record) != JOURNAL_KEYS or type(record['version']) is not int or record['version'] != 1 or record['release_sha'] != sha or type(record['original_gid']) is not int or not 0 <= record['original_gid'] <= 2**32 - 1:
        raise ValueError('same release recovery required')
    original = base64.b64decode(record['original_env_b64'], validate=True)
    if not 0 < len(original) <= 1048576 or digest(original) != record['original_sha256'] or not isinstance(record['updated_sha256'], str) or not re.fullmatch('[0-9a-f]{64}', record['updated_sha256']):
        raise ValueError('recovery digest invalid')
    current, _ = read_private(ENV_FILE)
    if digest(current) not in (record['original_sha256'], record['updated_sha256']):
        raise ValueError('runtime configuration changed outside recovery')
    with block_termination():
        assert_current(sha)
        replace(ENV_FILE, original, record['original_gid'])
        restart_fn()
        verify_fn(sha)
        assert_current(sha)
        RECOVERY.unlink()
        sync_directory(RECOVERY.parent)


def apply(sha, original, updated, owner, restart_fn=restart, verify_fn=verify, recovery_verify_fn=verify):
    create_recovery(sha, original, updated, owner.st_gid)
    try:
        assert_current(sha)
        replace(ENV_FILE, updated, owner.st_gid)
        restart_fn()
        verify_fn(sha)
        assert_current(sha)
    except BaseException:
        # Includes a replace committed before directory fsync failed and signals.
        recover(sha, restart_fn, recovery_verify_fn)
        raise RuntimeError('governance activation failed; original runtime verified') from None
    # All runtime checks passed: the new state is committed. A crash before
    # durable removal leaves a journal requiring explicit same-SHA recovery.
    with block_termination():
        RECOVERY.unlink()
        sync_directory(RECOVERY.parent)


def interrupted(signum, frame):
    raise ConfigurationInterrupted('governance configuration interrupted')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--sha', required=True)
    parser.add_argument('--mode', choices=('inspect', 'enable', 'disable', 'recover'), default='inspect')
    parser.add_argument('--retention', choices=('off', 'on'), default='off')
    args = parser.parse_args()
    if os.geteuid() != 0 or not re.fullmatch('[0-9a-f]{40}', args.sha) or (args.retention == 'on' and args.mode != 'enable'):
        raise ValueError('root and exact installed release required')
    fd = os.open(LOCK, os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
    previous_handlers = {}
    try:
        info = os.fstat(fd)
        if not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_nlink != 1 or info.st_mode & 0o022:
            raise ValueError('unsafe installer lock')
        fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        if not os.path.samestat(info, LOCK.lstat()):
            raise ValueError('installer lock changed')
        assert_current(args.sha)
        assert_source(args.sha)
        for signum in (signal.SIGINT, signal.SIGTERM):
            previous_handlers[signum] = signal.signal(signum, interrupted)
        if args.mode == 'recover':
            recover(args.sha)
            print(json.dumps({'release_sha': args.sha, 'state': 'recovered'}))
            return
        if os.path.lexists(RECOVERY):
            raise ValueError('protected recovery required before configuration')
        original, owner = read_private(ENV_FILE)
        if args.mode == 'inspect':
            print(json.dumps({'release_sha': args.sha, 'state': 'inspection_only', 'mutation': False}))
            return
        if args.mode == 'enable':
            validate_target()
        updated = render(original, args.mode == 'enable', args.retention == 'on')
        def verified(sha):
            verify(sha)
            if args.mode == 'enable':
                validate_target()
        # Rollback checks the original runtime without requiring a failed new proof.
        apply(args.sha, original, updated, owner, verify_fn=verified)
        print(json.dumps({'release_sha': args.sha, 'state': 'enabled' if args.mode == 'enable' else 'disabled', 'retention_enabled': args.retention == 'on', 'provider_visibility': 'requires_separate_receipt_and_group_readback'}))
    finally:
        for signum, handler in previous_handlers.items():
            signal.signal(signum, handler)
        os.close(fd)


if __name__ == '__main__':
    try:
        main()
    except (Exception, KeyboardInterrupt, ConfigurationInterrupted):
        raise SystemExit('governance runtime configuration failed; protected recovery may be required; inspect locally') from None
