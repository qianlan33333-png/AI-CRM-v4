#!/usr/bin/env python3
"""Inventory or remove only verified, unreferenced AI-CRM release packages.

No age-based recursive rm is used. Unknown directories and any non-package
content are protected. Run as root on the deployment host. Inventory is the
default; apply requires its saved plan and a fresh installed-schema snapshot.
"""
from __future__ import annotations

import argparse
import contextlib
import datetime as dt
import fcntl
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import stat
import subprocess
import sys
import urllib.request

SHA = re.compile(r"[0-9a-f]{40}\Z")
HEX = re.compile(r"[0-9a-f]{64}\Z")
PACKAGE_ROOTS = {"bin", "web", "migrations", "deploy", "components"}
PROTECTED_PARTS = {"backup", "backups", "secret", "secrets", "uploads", "exports", "business", "pgdata", "data", ".git"}
PROTECTED_SUFFIXES = {".pem", ".key", ".dump", ".backup", ".p12", ".pfx"}
ROOT_UID = 0
ROOT_GID = 0
SUCCESS_DIRECTORY = "release-success"
SUCCESS_PENDING = ".publication-pending.json"
SUCCESS_KEYS = {"version", "release_sha", "manifest_sha256", "package_digest", "binary_sha256",
                "schema_digest", "succeeded_at", "sequence", "run_number", "api_pid", "worker_pid"}


class Refuse(RuntimeError):
    """A stable, non-sensitive refusal reason."""

    def __init__(self, reason, deleted=None):
        super().__init__(reason)
        self.deleted = deleted or []


def utcnow():
    return dt.datetime.now(dt.timezone.utc)


def digest_file(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            h.update(block)
    return h.hexdigest()


def canonical_digest(value) -> str:
    return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(",", ":")).encode()).hexdigest()


def require_regular(path: Path):
    info = path.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1:
        raise Refuse("non_regular_or_shared_file")
    return info


def require_control_directory(path: Path, private=False):
    info = path.lstat()
    if (not stat.S_ISDIR(info.st_mode) or path.resolve() != path or info.st_uid != ROOT_UID
            or info.st_gid != ROOT_GID or stat.S_IMODE(info.st_mode) & 0o022
            or (private and stat.S_IMODE(info.st_mode) != 0o700)):
        raise Refuse("untrusted_release_control_directory")


def read_private_receipt_json(root: Path, filename: str) -> dict:
    """Only the root installer's fixed, private directory is an evidence source."""
    require_control_directory(root)
    directory = root / SUCCESS_DIRECTORY
    require_control_directory(directory, private=True)
    path = directory / filename
    descriptor = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    try:
        info = os.fstat(descriptor)
        if (not stat.S_ISREG(info.st_mode) or info.st_nlink != 1 or info.st_uid != ROOT_UID
                or info.st_gid != ROOT_GID or stat.S_IMODE(info.st_mode) != 0o600 or info.st_size > 16384):
            raise Refuse("untrusted_release_success_receipt")
        with os.fdopen(descriptor, "rb", closefd=False) as stream:
            raw = stream.read(16385)
        if len(raw) > 16384:
            raise Refuse("invalid_release_success_receipt")
        def unique_object(pairs):
            value = {}
            for key, item in pairs:
                if key in value:
                    raise Refuse("invalid_release_success_receipt")
                value[key] = item
            return value
        value = json.loads(raw, object_pairs_hook=unique_object)
        after = path.lstat()
        if (info.st_dev, info.st_ino, info.st_size, info.st_mtime_ns) != (after.st_dev, after.st_ino, after.st_size, after.st_mtime_ns):
            raise Refuse("release_success_receipt_changed")
        return value
    except (ValueError, TypeError, KeyError):
        raise Refuse("invalid_release_success_receipt") from None
    finally:
        os.close(descriptor)


def validate_success_receipt(value, name=None):
    try:
        if (not isinstance(value, dict) or set(value) != SUCCESS_KEYS or type(value["version"]) is not int
                or value["version"] != 1 or not isinstance(value["release_sha"], str)
                or not SHA.fullmatch(value["release_sha"]) or (name is not None and value["release_sha"] != name)
                or any(not isinstance(value[key], str) or not HEX.fullmatch(value[key])
                       for key in ("manifest_sha256", "package_digest", "binary_sha256", "schema_digest"))
                or any(type(value[key]) is not int or value[key] <= 0 for key in ("sequence", "api_pid", "worker_pid"))
                or value["api_pid"] == value["worker_pid"]
                or (value["run_number"] is not None and (not isinstance(value["run_number"], str)
                    or not re.fullmatch(r"[1-9][0-9]*", value["run_number"])) )):
            raise Refuse("invalid_release_success_receipt")
        succeeded = dt.datetime.fromisoformat(value["succeeded_at"])
        if succeeded.tzinfo is None or succeeded > utcnow() + dt.timedelta(seconds=60):
            raise Refuse("invalid_release_success_time")
        return value
    except (ValueError, TypeError, KeyError):
        raise Refuse("invalid_release_success_receipt") from None


def read_success_receipt(root: Path, name: str) -> dict:
    if not SHA.fullmatch(name):
        raise Refuse("invalid_success_release")
    return validate_success_receipt(read_private_receipt_json(root, name + ".json"), name)


def read_pending_success(root: Path, filename=SUCCESS_PENDING) -> dict:
    if filename != SUCCESS_PENDING and not re.fullmatch(r"revoked-[1-9][0-9]*-[0-9a-f]{40}\.json", filename):
        raise Refuse("invalid_success_journal_name")
    value = read_private_receipt_json(root, filename)
    if not isinstance(value, dict) or set(value) != {"version", "candidate", "previous"} or type(value["version"]) is not int or value["version"] != 1:
        raise Refuse("invalid_success_publication_journal")
    candidate = validate_success_receipt(value["candidate"])
    if filename != SUCCESS_PENDING and filename != f"revoked-{candidate['sequence']}-{candidate['release_sha']}.json":
        raise Refuse("invalid_success_revocation_binding")
    if value["previous"] is not None:
        previous = validate_success_receipt(value["previous"], candidate["release_sha"])
        if previous["sequence"] >= candidate["sequence"] or any(previous[key] != candidate[key] for key in ("manifest_sha256", "package_digest", "binary_sha256", "schema_digest")):
            raise Refuse("invalid_previous_success_binding")
    return value


def verified_success(root: Path, package: dict) -> dict:
    if os.path.lexists(root / SUCCESS_DIRECTORY / SUCCESS_PENDING):
        raise Refuse("release_success_publication_incomplete")
    require_control_directory(root / "releases")
    require_sealed_package(root / "releases" / package["name"])
    value = read_success_receipt(root, package["name"])
    if any(value[key] != package[key] for key in ("manifest_sha256", "package_digest", "binary_sha256", "schema_digest")):
        raise Refuse("release_success_package_mismatch")
    return value


def require_sealed_package(path: Path):
    require_control_directory(path)
    for directory, dirs, files in os.walk(path, followlinks=False):
        for name in dirs:
            require_control_directory(Path(directory) / name)
        for name in files:
            info = require_regular(Path(directory) / name)
            if info.st_uid != ROOT_UID or info.st_gid != ROOT_GID or stat.S_IMODE(info.st_mode) & 0o022:
                raise Refuse("unsealed_release_package")


def verified_package(path: Path) -> dict:
    if not SHA.fullmatch(path.name) or path.is_symlink() or not path.is_dir():
        raise Refuse("unknown_release_directory")
    root_stat = path.stat()
    require_regular(path / "release.env")
    manifest = path / "release-files.sha256"
    require_regular(manifest)
    expected = {}
    for line in manifest.read_text().splitlines():
        match = re.fullmatch(r"([0-9a-f]{64}) [ *](?:\./)?(.+)", line)
        if not match:
            raise Refuse("invalid_file_manifest")
        checksum, relative = match.groups()
        parts = PurePosixPath(relative).parts
        if not parts or relative.startswith("/") or ".." in parts or parts[0] not in PACKAGE_ROOTS or "\\" in relative:
            raise Refuse("manifest_path_not_package_owned")
        if any(part.lower() in PROTECTED_PARTS for part in parts) or Path(relative).suffix.lower() in PROTECTED_SUFFIXES or Path(relative).name == ".env":
            raise Refuse("protected_data_in_package")
        if relative in expected:
            raise Refuse("duplicate_manifest_file")
        expected[relative] = checksum
    if "bin/aicrm" not in expected or not any(name.startswith("migrations/") and name.endswith(".sql") for name in expected):
        raise Refuse("incomplete_release_package")
    observed = set()
    bytes_total = 0
    newest_file_ns = 0
    migrations = []
    for directory, dirs, files in os.walk(path, followlinks=False):
        for name in dirs:
            child = Path(directory) / name
            info = child.lstat()
            if not stat.S_ISDIR(info.st_mode) or info.st_dev != root_stat.st_dev:
                raise Refuse("symlink_or_mounted_package_directory")
            parts = child.relative_to(path).parts
            if parts[0] not in PACKAGE_ROOTS or any(part.lower() in PROTECTED_PARTS for part in parts):
                raise Refuse("unregistered_package_directory")
        for name in files:
            child = Path(directory) / name
            relative = str(child.relative_to(path))
            before = require_regular(child)
            if before.st_dev != root_stat.st_dev:
                raise Refuse("mounted_package_file")
            bytes_total += before.st_size
            newest_file_ns = max(newest_file_ns, before.st_mtime_ns)
            if relative == "release-files.sha256":
                continue
            if relative == "release.env":
                if child.read_text() != f"AICRM_RELEASE_SHA={path.name}\n":
                    raise Refuse("unexpected_release_environment")
                continue
            if relative not in expected:
                raise Refuse("unregistered_package_content")
            if digest_file(child) != expected[relative]:
                raise Refuse("package_checksum_mismatch")
            after = child.stat()
            if (before.st_ino, before.st_size, before.st_mtime_ns) != (after.st_ino, after.st_size, after.st_mtime_ns):
                raise Refuse("package_changed_during_verification")
            observed.add(relative)
            if relative.startswith("migrations/") and relative.endswith(".sql"):
                basename = Path(relative).name
                if not re.fullmatch(r"[0-9]{4}_.+\.sql", basename):
                    raise Refuse("unexpected_migration_name")
                migrations.append({"version": basename[:4], "name": basename, "checksum": expected[relative]})
    if observed != set(expected):
        raise Refuse("missing_package_content")
    return {"name": path.name, "bytes": bytes_total, "manifest_sha256": digest_file(manifest),
            "inode": root_stat.st_ino, "device": root_stat.st_dev, "newest_file_ns": newest_file_ns,
            "binary_sha256": expected["bin/aicrm"],
            "package_digest": canonical_digest({"release_sha": path.name, "files": expected}),
            "schema_digest": canonical_digest(sorted(migrations, key=lambda x: x["version"])),
            "migrations": sorted(migrations, key=lambda x: x["version"])}


# Explicitly reviewed v3 additive migration from PR #419. It only expands
# payment/provider CHECK enums; it does not rename/drop columns or data. Unknown
# later migrations must never inherit this exception merely by being later.
COMPATIBLE_ADDITIONAL_MIGRATIONS = {
    "0202": {"version": "0202", "name": "0202_alipay_web_payment.sql",
             "checksum": "73e2142dbd857e856dedf31bff28981d9bb5cb607b65260d7ffb770951e19eba"},
}


def schema_is_compatible(package: dict, installed: dict) -> bool:
    expected = package.get("migrations")
    actual = installed.get("migrations")
    if not isinstance(expected, list) or not expected or not isinstance(actual, list):
        return False
    if any(not isinstance(item, dict) for item in actual):
        return False
    by_version = {item.get("version"): item for item in actual}
    if len(by_version) != len(actual):
        return False
    if any(by_version.get(item["version"]) != item for item in expected):
        return False
    expected_versions = {item["version"] for item in expected}
    for version, item in by_version.items():
        if version not in expected_versions:
            if (not isinstance(version, str) or version <= max(expected_versions)
                    or COMPATIBLE_ADDITIONAL_MIGRATIONS.get(version) != item):
                return False
    return True


def schema_snapshot(path: Path, current: str) -> tuple[dict, str]:
    require_regular(path)
    value = json.loads(path.read_text())
    return validated_schema_snapshot(value, current)


def validated_schema_snapshot(value: dict, current: str) -> tuple[dict, str]:
    if value.get("current_sha") != current:
        raise Refuse("schema_current_release_mismatch")
    try:
        captured = dt.datetime.fromisoformat(value["captured_at"].replace("Z", "+00:00"))
        age = (utcnow() - captured).total_seconds()
    except (ValueError, TypeError, KeyError):
        raise Refuse("invalid_schema_capture_time") from None
    if age < -60 or age > 3600:
        raise Refuse("schema_evidence_not_fresh")
    migrations = value.get("migrations")
    if not isinstance(migrations, list) or not migrations:
        raise Refuse("schema_evidence_empty")
    versions = set()
    normalized = []
    for item in migrations:
        if set(item) != {"version", "name", "checksum"} or not re.fullmatch(r"[0-9]{4}", item["version"]) or not HEX.fullmatch(item["checksum"]) or not item["name"].startswith(item["version"] + "_") or not item["name"].endswith(".sql") or item["version"] in versions:
            raise Refuse("invalid_schema_evidence")
        versions.add(item["version"])
        normalized.append(item)
    return value, canonical_digest(sorted(normalized, key=lambda x: x["version"]))


def referenced_names(text: str, root: Path, names: set[str]) -> set[str]:
    # Never return raw process arguments, file paths outside the release root,
    # environment values, or service definitions in an inventory report.
    return {name for name in names if re.search(re.escape(str(root / "releases" / name)) + r"(?:[/\s\x00\"';)]|$)", text)
            or f"install-release-{name}.sh" in text or f"aicrm-{name}.tar.gz" in text}


def process_references(root: Path, names: set[str], proc: Path = Path("/proc")) -> set[str]:
    if not proc.is_dir():
        raise Refuse("proc_inventory_unavailable")
    protected = set()
    for pid in proc.iterdir():
        if not pid.name.isdigit():
            continue
        try:
            for key in ("exe", "cwd", "root"):
                try:
                    protected |= referenced_names(os.readlink(pid / key), root, names)
                except FileNotFoundError:
                    pass  # Exited process or kernel thread without a userspace executable.
            for key in ("maps", "cmdline"):
                try:
                    protected |= referenced_names((pid / key).read_bytes().decode("utf-8", "replace"), root, names)
                except FileNotFoundError:
                    pass
            try:
                for fd in (pid / "fd").iterdir():
                    try:
                        protected |= referenced_names(os.readlink(fd), root, names)
                    except FileNotFoundError:
                        pass
            except FileNotFoundError:
                pass
        except (PermissionError, OSError):
            if pid.exists():
                raise Refuse("process_reference_inventory_incomplete") from None
    return protected


def run_systemctl(args: list[str]) -> str:
    result = subprocess.run(["systemctl", *args], capture_output=True, text=True, timeout=60, check=False)
    if result.returncode:
        raise Refuse("service_reference_inventory_failed")
    return result.stdout


def service_references(root: Path, names: set[str]) -> set[str]:
    text = run_systemctl(["list-unit-files", "--type=service", "--no-legend", "--no-pager"])
    text += "\n" + run_systemctl(["list-units", "--all", "--type=service", "--plain", "--no-legend", "--no-pager"])
    units = sorted({line.split()[0] for line in text.splitlines() if line.strip()})
    if not units:
        raise Refuse("service_reference_inventory_empty")
    protected = set()
    def protect_text(observed: str, template: bool = False):
        nonlocal protected
        protected |= referenced_names(observed, root, names)
        for absolute in re.findall(r"/[A-Za-z0-9_./:+@%=-]+", observed):
            # A template can select a release via an instance specifier. Its
            # future instance cannot be resolved from the uninstantiated unit;
            # conservatively protect every package instead of guessing a SHA.
            if template and "%" in absolute and str(root / "releases") in absolute:
                protected |= names
            # A disabled service or a template/drop-in can reference an alias
            # outside /opt/aicrm. Resolve it without reading environment data.
            protected |= referenced_names(str(Path(absolute).resolve(strict=False)), root, names)

    templates = [unit for unit in units if unit.endswith("@.service")]
    instances = [unit for unit in units if unit not in templates]
    for template in templates:
        # systemctl show rejects uninstantiated templates on production's
        # systemd. cat reads the template fragment AND applicable drop-ins,
        # including disabled templates; failure still refuses the inventory.
        # Keep the captured unit text private: it may include environment data.
        protect_text(run_systemctl(["cat", template, "--no-pager"]), template=True)
    properties = "FragmentPath,DropInPaths,ExecStart,ExecStartPre,ExecStartPost,ExecReload,ExecStop,ExecStopPost,WorkingDirectory,RootDirectory,EnvironmentFiles"
    for i in range(0, len(instances), 50):
        observed = run_systemctl(["show", *instances[i:i + 50], "--no-pager", f"--property={properties}"])
        protect_text(observed)
    return protected


def symlink_references(root: Path, names: set[str]) -> set[str]:
    protected = set()
    for item in root.iterdir():
        if item.is_symlink():
            protected |= referenced_names(str(item.resolve(strict=False)), root, names)
    return protected


def live_references(root: Path, names: set[str]) -> set[str]:
    return process_references(root, names) | service_references(root, names) | symlink_references(root, names)


def check_ready(current: str):
    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, *args, **kwargs):
            raise Refuse("readyz_redirect_refused")
    try:
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
        with opener.open("http://127.0.0.1:8080/readyz", timeout=5) as response:
            value = json.loads(response.read(65536))
        if value.get("status") != "ready" or value.get("release_sha") != current:
            raise Refuse("readyz_release_mismatch")
    except Refuse:
        raise
    except Exception:
        raise Refuse("readyz_verification_failed") from None


@contextlib.contextmanager
def release_lock(root: Path):
    lock = root / "install-release.lock"
    # Opening the same inode as the installer is essential; never replace it.
    flags = os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW
    descriptor = os.open(lock, flags, 0o600)
    try:
        if not stat.S_ISREG(os.fstat(descriptor).st_mode):
            raise Refuse("invalid_release_lock")
        try:
            fcntl.flock(descriptor, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise Refuse("release_install_or_cleanup_in_progress") from None
        yield
    finally:
        os.close(descriptor)


def inventory(root: Path, schema_file: Path, explicit_protected: list[str]) -> dict:
    releases = root / "releases"
    if root.is_symlink() or not root.is_dir() or releases.is_symlink() or not releases.is_dir():
        raise Refuse("invalid_release_root")
    if os.path.lexists(root / SUCCESS_DIRECTORY / SUCCESS_PENDING):
        raise Refuse("release_success_publication_incomplete")
    current_path = (root / "current").resolve(strict=True)
    if current_path.parent != releases or not SHA.fullmatch(current_path.name):
        raise Refuse("current_is_not_a_managed_release")
    current = current_path.name
    schema, installed_digest = schema_snapshot(schema_file, current)
    check_ready(current)
    names = {path.name for path in releases.iterdir() if SHA.fullmatch(path.name)}
    protected = live_references(root, names) | set(explicit_protected) | {current}
    entries, verified = [], {}
    for path in sorted(releases.iterdir()):
        if not SHA.fullmatch(path.name):
            entries.append({"name": path.name, "action": "protect", "reason": "unknown_directory"})
            continue
        try:
            value = verified_package(path)
            verified[path.name] = value
        except (OSError, ValueError, Refuse) as exc:
            reason = str(exc) if isinstance(exc, Refuse) else "package_verification_failed"
            entries.append({"name": path.name, "action": "protect", "reason": reason})
    if current not in verified or verified[current]["schema_digest"] != installed_digest:
        raise Refuse("current_package_or_installed_schema_unverified")
    successful = {}
    for name, package in verified.items():
        if name == current or package["schema_digest"] != installed_digest:
            continue
        try:
            successful[name] = verified_success(root, package)
        except (OSError, ValueError, Refuse):
            pass  # No historical success evidence means no rollback claim.
    # A duplicate monotonic sequence cannot be produced by the locked writer.
    sequences = [value["sequence"] for value in successful.values()]
    if len(sequences) != len(set(sequences)):
        raise Refuse("duplicate_release_success_sequence")
    rollback = [verified[name] for name in sorted(successful, key=lambda name: successful[name]["sequence"], reverse=True)[:2]]
    blockers = []
    if len(rollback) != 2:
        blockers.append("two_successful_schema_compatible_verified_rollback_releases_required")
    protected |= {v["name"] for v in rollback}
    for name, value in sorted(verified.items()):
        value = dict(value)
        value.update(action="protect" if name in protected or blockers else "delete", reason="referenced_or_rollback" if name in protected else "missing_rollback_evidence" if blockers else "verified_unreferenced_release")
        entries.append(value)
    candidates = [entry for entry in entries if entry["action"] == "delete"]
    return {"version": 1, "mode": "inventory", "root": str(root), "current_sha": current,
            "schema_digest": installed_digest, "captured_at": utcnow().isoformat(),
            "rollback_releases": [v["name"] for v in rollback], "blockers": blockers,
            "candidate_count": len(candidates), "candidate_bytes": sum(v["bytes"] for v in candidates),
            "entries": sorted(entries, key=lambda v: v["name"])}


def apply_plan(root: Path, schema_file: Path, plan: dict, explicit_protected: list[str]) -> dict:
    if plan.get("version") != 1 or plan.get("mode") != "inventory" or plan.get("root") != str(root) or plan.get("blockers"):
        raise Refuse("invalid_or_blocked_cleanup_plan")
    fresh = inventory(root, schema_file, explicit_protected)
    if fresh["blockers"] or fresh["current_sha"] != plan.get("current_sha") or fresh["schema_digest"] != plan.get("schema_digest"):
        raise Refuse("release_or_schema_changed_since_inventory")
    eligible = {item["name"]: item for item in fresh["entries"] if item["action"] == "delete"}
    candidates = [item for item in plan.get("entries", []) if item.get("action") == "delete"]
    selected = set()
    absent = []
    for old in candidates:
        name = old.get("name", "")
        if not SHA.fullmatch(name) or name in selected or name in absent:
            raise Refuse("invalid_or_duplicate_plan_candidate")
        path = root / "releases" / name
        if not path.exists() and not path.is_symlink():
            absent.append(name)  # Replaying a completed cleanup changes nothing.
            continue
        if name not in eligible:
            raise Refuse("candidate_now_protected_or_missing")
        selected.add(name)
        if any(old.get(key) != eligible[name].get(key) for key in ("manifest_sha256", "inode", "device", "bytes")):
            raise Refuse("candidate_changed_since_inventory")
    # Never partially execute a plan whose initial protection validation failed.
    deleted = []
    for name in sorted(selected):
        try:
            if name in live_references(root, selected):
                raise Refuse("candidate_became_referenced")
            path = root / "releases" / name
            value = verified_package(path)
            if any(value[key] != eligible[name][key] for key in ("manifest_sha256", "inode", "device", "bytes")):
                raise Refuse("candidate_changed_before_delete")
            if not shutil.rmtree.avoids_symlink_attacks:
                raise Refuse("fd_safe_recursive_removal_unavailable")
            receipt = {"name": name, "bytes": value["bytes"], "manifest_sha256": value["manifest_sha256"]}
            audit(root, {"event": "delete_started", **receipt})
            shutil.rmtree(path)
            deleted.append(receipt)
            audit(root, {"event": "delete_completed", **receipt})
        except (Refuse, OSError) as exc:
            reason = str(exc) if isinstance(exc, Refuse) else "release_cleanup_io_failed"
            raise Refuse(reason, deleted=deleted) from None
    return {"version": 1, "mode": "apply", "current_sha": fresh["current_sha"], "schema_digest": fresh["schema_digest"],
            "rollback_releases": fresh["rollback_releases"], "deleted": deleted,
            "already_absent": absent, "deleted_bytes": sum(item["bytes"] for item in deleted), "completed_at": utcnow().isoformat()}


def audit(root: Path, event: dict):
    # Outside releases and excluded from all process-file retention. A crash
    # between started/completed remains visible instead of appearing atomic.
    descriptor = os.open(root / "release-cleanup-audit.jsonl", os.O_WRONLY | os.O_APPEND | os.O_CREAT | os.O_NOFOLLOW, 0o600)
    try:
        info = os.fstat(descriptor)
        if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1:
            raise Refuse("invalid_release_cleanup_audit")
        payload = (json.dumps({"at": utcnow().isoformat(), **event}, sort_keys=True) + "\n").encode()
        if os.write(descriptor, payload) != len(payload):
            raise Refuse("release_cleanup_audit_write_incomplete")
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("inventory", "apply"), nargs="?", default="inventory")
    parser.add_argument("--root", type=Path, default=Path("/opt/aicrm"))
    parser.add_argument("--schema-evidence", type=Path, required=True)
    parser.add_argument("--plan", type=Path, help="saved inventory JSON; required for apply")
    parser.add_argument("--protect-release", action="append", default=[], help="extra protected release SHA")
    args = parser.parse_args()
    try:
        if sys.platform != "linux" or os.geteuid() != 0:
            raise Refuse("linux_root_required_for_complete_reference_inventory")
        if any(not SHA.fullmatch(name) for name in args.protect_release) or not args.root.is_absolute():
            raise Refuse("invalid_cleanup_argument")
        if args.mode == "apply" and args.plan is None:
            raise Refuse("apply_requires_inventory_plan")
        with release_lock(args.root):
            if args.mode == "inventory":
                result = inventory(args.root, args.schema_evidence, args.protect_release)
            else:
                result = apply_plan(args.root, args.schema_evidence, json.loads(args.plan.read_text()), args.protect_release)
        print(json.dumps(result, ensure_ascii=False, indent=2))
        return 1 if result.get("blockers") else 0
    except (Refuse, OSError, ValueError, subprocess.TimeoutExpired) as exc:
        reason = str(exc) if isinstance(exc, Refuse) else "cleanup_evidence_or_io_failed"
        print(json.dumps({"ok": False, "reason": reason, "mode": args.mode,
                          "deleted": getattr(exc, "deleted", [])}), file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
