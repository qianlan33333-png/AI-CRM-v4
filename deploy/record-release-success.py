#!/usr/bin/env python3
"""Record observed release success under the existing root installer lock.

There is no import/override/assume-success mode. Old packages which never ran
with this observer remain unproven; receipts cannot certify cross-schema use.
"""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys
import tempfile

# Also applies to manual observation of the current installed release.
sys.dont_write_bytecode = True

ROOT = Path("/opt/aicrm")
CONFIG = Path("/etc/aicrm/aicrm.env")
SOURCE = Path(__file__).resolve()
SERVICES = ("aicrm.service", "aicrm-effects-worker.service")
ROOT_UID = 0
ROOT_GID = 0


def helper(name, filename):
    spec = importlib.util.spec_from_file_location(name, SOURCE.with_name(filename))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def require_source(sha, current=True):
    if os.geteuid() != ROOT_UID or not re.fullmatch(r"[0-9a-f]{40}", sha):
        raise ValueError("root_and_release_required")
    expected = ROOT / "releases" / sha / "deploy" / "record-release-success.py"
    if SOURCE != expected or expected.resolve() != expected:
        raise ValueError("installed_success_observer_required")
    code_files = (expected, expected.with_name("cleanup-releases.py"), expected.with_name("post-release-retention.py"))
    for path in (ROOT, ROOT / "releases", expected.parent.parent, expected.parent, *code_files):
        info = path.lstat()
        if (info.st_uid != ROOT_UID or info.st_gid != ROOT_GID or info.st_mode & 0o022
                or stat.S_ISLNK(info.st_mode) or (path in code_files and (not stat.S_ISREG(info.st_mode) or info.st_nlink != 1))):
            raise ValueError("untrusted_success_observer")
    if current and (ROOT / "current").resolve(strict=True) != expected.parent.parent:
        raise ValueError("success_release_not_current")


def process_id(unit):
    subprocess.run(["systemctl", "is-active", "--quiet", unit], check=True, timeout=5,
                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    value = subprocess.run(["systemctl", "show", unit, "-p", "MainPID", "--value"], check=True,
                           capture_output=True, text=True, timeout=5).stdout.strip()
    if not re.fullmatch(r"[1-9][0-9]*", value):
        raise ValueError("release_process_unavailable")
    return int(value)


def verify_processes(sha, binary_digest, cleanup):
    expected = ROOT / "releases" / sha / "bin" / "aicrm"
    actual = expected.stat()
    pids = []
    for unit in SERVICES:
        pid = process_id(unit)
        executable = Path("/proc") / str(pid) / "exe"
        if os.readlink(executable) != str(expected):
            raise ValueError("release_process_image_mismatch")
        image = executable.stat()
        if ((actual.st_dev, actual.st_ino) != (image.st_dev, image.st_ino)
                or cleanup.digest_file(executable) != binary_digest
                or process_id(unit) != pid or os.readlink(executable) != str(expected)):
            raise ValueError("release_process_changed")
        pids.append(pid)
    if pids[0] == pids[1]:
        raise ValueError("release_roles_share_process")
    return {"api_pid": pids[0], "worker_pid": pids[1]}


def sync_directory(directory):
    descriptor = os.open(directory, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


def write_private_json(path, value):
    descriptor, temporary = tempfile.mkstemp(prefix=".pending-", dir=path.parent)
    try:
        os.fchmod(descriptor, 0o600)
        os.fchown(descriptor, ROOT_UID, ROOT_GID)
        with os.fdopen(descriptor, "w") as stream:
            json.dump(value, stream, sort_keys=True, separators=(",", ":"))
            stream.write("\n")
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
        sync_directory(path.parent)
    finally:
        if os.path.lexists(temporary):
            os.unlink(temporary)


def atomic_receipt(cleanup, package, processes, run_number):
    cleanup.require_control_directory(ROOT)
    directory = ROOT / cleanup.SUCCESS_DIRECTORY
    if not directory.exists() and not directory.is_symlink():
        directory.mkdir(mode=0o700)
        # The process is root; record permissions explicitly, independent of umask.
        os.chown(directory, ROOT_UID, ROOT_GID)
        directory.chmod(0o700)
        sync_directory(ROOT)
    cleanup.require_control_directory(directory, private=True)
    sequence = 0
    pending_path = directory / cleanup.SUCCESS_PENDING
    if os.path.lexists(pending_path):
        raise ValueError("success_publication_recovery_required")
    previous = None
    seen = set()
    for path in directory.iterdir():
        if path == pending_path:
            continue
        if path.name.startswith("revoked-"):
            revoked = cleanup.read_pending_success(ROOT, path.name)
            sequence = max(sequence, revoked["candidate"]["sequence"])
            continue
        if not re.fullmatch(r"[0-9a-f]{40}\.json", path.name):
            # Interrupted atomic writes are never success records or sequence sources.
            if path.name.startswith(".pending-"):
                continue
            raise ValueError("unknown_success_record")
        old = cleanup.read_success_receipt(ROOT, path.stem)
        if old["sequence"] in seen:
            raise ValueError("duplicate_success_sequence")
        seen.add(old["sequence"])
        sequence = max(sequence, old["sequence"])
        if path.stem == package["name"]:
            if any(old[key] != package[key] for key in
                   ("manifest_sha256", "package_digest", "binary_sha256", "schema_digest")):
                raise ValueError("previous_success_package_changed")
            previous = old
    receipt = {"version": 1, "release_sha": package["name"], "sequence": sequence + 1,
               "succeeded_at": cleanup.utcnow().isoformat(), "run_number": run_number or None,
               **processes, **{key: package[key] for key in
                    ("manifest_sha256", "package_digest", "binary_sha256", "schema_digest")}}
    destination = directory / (package["name"] + ".json")
    # The durable guard prevents a killed writer or uncertain directory fsync
    # from making the new canonical file usable as rollback evidence.
    journal = {"version": 1, "candidate": receipt, "previous": previous}
    write_private_json(pending_path, journal)
    try:
        write_private_json(destination, receipt)
    except BaseException:
        # Canonical directory fsync has not confirmed commit. Its durable
        # guard remains in place even if revocation cannot complete.
        revoke_pending(cleanup)
        raise
    # Canonical file AND its containing directory are durable: successful
    # observation is committed. Guard cleanup is no longer grounds to roll
    # back a healthy installation or claim that success never happened.
    try:
        pending_path.unlink()
        sync_directory(directory)
    except OSError:
        print("release success committed; publication guard cleanup pending", file=sys.stderr)
    return receipt


def revoke_pending(cleanup):
    """Only revoke uncertain metadata; never start a release or assert success."""
    directory = ROOT / cleanup.SUCCESS_DIRECTORY
    pending_path = directory / cleanup.SUCCESS_PENDING
    if not os.path.lexists(pending_path):
        return
    journal = cleanup.read_pending_success(ROOT)
    candidate, previous = journal["candidate"], journal["previous"]
    package_path = ROOT / "releases" / candidate["release_sha"]
    cleanup.require_sealed_package(package_path)
    package = cleanup.verified_package(package_path)
    if any(candidate[key] != package[key] for key in ("manifest_sha256", "package_digest", "binary_sha256", "schema_digest")):
        raise ValueError("pending_success_package_changed")
    destination = directory / (candidate["release_sha"] + ".json")
    if os.path.lexists(destination):
        observed = cleanup.read_success_receipt(ROOT, candidate["release_sha"])
        if observed != candidate and observed != previous:
            raise ValueError("pending_success_receipt_changed")
    elif previous is not None:
        raise ValueError("previous_success_receipt_missing")
    archive = directory / f"revoked-{candidate['sequence']}-{candidate['release_sha']}.json"
    if os.path.lexists(archive):
        if cleanup.read_pending_success(ROOT, archive.name) != journal:
            raise ValueError("revocation_history_changed")
    else:
        write_private_json(archive, journal)
    if previous is None:
        destination.unlink(missing_ok=True)
        sync_directory(directory)
    else:
        write_private_json(destination, previous)
    pending_path.unlink()
    # Revocation/archive and restored canonical state are already durable.
    # A failed final directory fsync can only resurrect the blocking guard on
    # crash; it cannot resurrect the revoked success receipt.
    try:
        sync_directory(directory)
    except OSError:
        print("release success revocation committed; guard cleanup durability uncertain", file=sys.stderr)


def record(sha, run_number=None, revoke=False):
    require_source(sha, current=not revoke)
    if run_number and not re.fullmatch(r"[1-9][0-9]*", run_number):
        raise ValueError("invalid_release_run_number")
    cleanup = helper("success_cleanup", "cleanup-releases.py")
    post = helper("success_schema", "post-release-retention.py")
    lock = (ROOT / "install-release.lock").lstat()
    if (not stat.S_ISREG(lock.st_mode) or lock.st_nlink != 1 or lock.st_uid != ROOT_UID
            or lock.st_gid != ROOT_GID or lock.st_mode & 0o022):
        raise ValueError("untrusted_release_lock")
    post.require_lock(ROOT)
    package_path = ROOT / "releases" / sha
    cleanup.require_sealed_package(package_path)
    package = cleanup.verified_package(package_path)
    if revoke:
        revoke_pending(cleanup)
        return
    if os.path.lexists(ROOT / cleanup.SUCCESS_DIRECTORY / cleanup.SUCCESS_PENDING):
        raise ValueError("success_publication_recovery_required")
    config = CONFIG.lstat()
    if (not stat.S_ISREG(config.st_mode) or config.st_nlink != 1 or config.st_uid != ROOT_UID
            or config.st_gid != ROOT_GID or stat.S_IMODE(config.st_mode) != 0o600):
        raise ValueError("untrusted_database_configuration")
    schema_value, _ = cleanup.validated_schema_snapshot(post.snapshot(sha, post.database_environment(CONFIG)), sha)
    if not cleanup.schema_is_compatible(package, schema_value):
        raise ValueError("success_installed_schema_mismatch")
    cleanup.check_ready(sha)
    processes = verify_processes(sha, package["binary_sha256"], cleanup)
    # Recheck current and all package hashes after observing readiness/processes.
    if (ROOT / "current").resolve(strict=True) != package_path or cleanup.verified_package(package_path) != package:
        raise ValueError("success_release_changed")
    cleanup.check_ready(sha)
    return atomic_receipt(cleanup, package, processes, run_number)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--sha", required=True)
    parser.add_argument("--run-number")
    parser.add_argument("--revoke-incomplete", action="store_true", help="revoke a fixed incomplete receipt publication; never claim success")
    arguments = parser.parse_args()
    try:
        if arguments.revoke_incomplete and arguments.run_number:
            parser.error("revocation cannot accept a run number")
        record(arguments.sha, arguments.run_number, revoke=arguments.revoke_incomplete)
    except Exception:
        print("release success could not be verified; no new success claim", file=sys.stderr)
        raise SystemExit(1)
