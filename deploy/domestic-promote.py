#!/usr/bin/env python3
"""Install an already built CRM v4 directory on staging or production.

This fixed, root-owned helper is deliberately separate from the release payload.
Only a checksum-verified payload may become /opt/aicrm/current. It shares the
legacy install lock so an old/manual installer cannot switch current at once.
"""
from __future__ import annotations

import argparse
import fcntl
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import pwd
import re
import shutil
import stat
import subprocess
import sys
import tempfile
import time
import urllib.request
from urllib.parse import parse_qsl, unquote_to_bytes, urlsplit

SHA = re.compile(r"^[0-9a-f]{40}$")
FILE_SHA = re.compile(r"^[0-9a-f]{64}$")
ROOT = Path("/opt/aicrm")
RELEASES = ROOT / "releases"
CURRENT = ROOT / "current"
LOCK = ROOT / "install-release.lock"
RECEIPTS = ROOT / "domestic-receipts"
ENV = Path("/etc/aicrm/aicrm.env")
RUNUSER = "/usr/sbin/runuser"
STAGE_ROLE = Path("/etc/aicrm/domestic-release-role")
READY = "http://127.0.0.1:8080/readyz"
STAGING_MIGRATION_VERSION = "0206"
STAGING_MIGRATION_NAME = "0206_order_native_alipay_checkout.sql"
STAGING_MIGRATION_PATH = f"migrations/{STAGING_MIGRATION_NAME}"
STAGING_RETRY_SHA = "5538d615a9abe2e25be799936866a7330b1d3af8"
PRE_0206_ORDER_CHECK = """CHECK ((((record_origin = 'native'::text) AND (provider <> 'alipay'::text) AND (effect_eligible = true) AND (source_row_digest IS NULL) AND (payer_customer_id IS NOT NULL) AND (beneficiary_customer_id IS NOT NULL)) OR ((record_origin = 'history'::text) AND (effect_eligible = false) AND (octet_length(source_row_digest) = 32))))"""


def run(*args: str, check: bool = True) -> subprocess.CompletedProcess[str]:
    return subprocess.run(args, check=check, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)


def digest(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as f:
        for block in iter(lambda: f.read(1024 * 1024), b""):
            h.update(block)
    return h.hexdigest()


def verify_metadata_identity(content: bytes, expected_sha: str, metadata_sha256: str) -> dict:
    """Bind the exact metadata bytes and their source to the caller's release identity."""
    if not isinstance(expected_sha, str) or not SHA.fullmatch(expected_sha):
        raise ValueError("invalid expected source SHA")
    if not isinstance(metadata_sha256, str) or not FILE_SHA.fullmatch(metadata_sha256):
        raise ValueError("invalid metadata SHA256")
    if not isinstance(content, bytes) or hashlib.sha256(content).hexdigest() != metadata_sha256:
        raise ValueError("metadata SHA256 mismatch")
    metadata = json.loads(content)
    if not isinstance(metadata, dict) or metadata.get("source_sha") != expected_sha:
        raise ValueError("metadata source SHA mismatch")
    return metadata


def manifest_entries(payload: Path, metadata: dict) -> dict[str, str]:
    """Parse one canonical manifest for both incoming packages and orphans."""
    if payload.is_symlink() or not payload.is_dir():
        raise ValueError("payload must be a real directory")
    manifest = payload / "release-files.sha256"
    if manifest.is_symlink() or not manifest.is_file():
        raise ValueError("manifest missing or unsafe")
    if not FILE_SHA.fullmatch(str(metadata.get("release_files_sha256", ""))):
        raise ValueError("missing manifest digest")
    if digest(manifest) != metadata["release_files_sha256"]:
        raise ValueError("manifest digest mismatch")
    listed: dict[str, str] = {}
    for line in manifest.read_text().splitlines():
        match = re.fullmatch(r"([0-9a-f]{64})  (.+)", line)
        if not match:
            raise ValueError("invalid manifest line")
        value, name = match.groups()
        rel = Path(name)
        if (
            rel.is_absolute()
            or rel.as_posix() != name
            or ".." in rel.parts
            or "." in rel.parts
            or "\\" in name
            or name in {"release-files.sha256", "release.env"}
            or name in listed
        ):
            raise ValueError("unsafe manifest path")
        listed[name] = value
    return listed


def verify_payload(payload: Path, metadata: dict) -> None:
    """Reject missing, extra, linked and altered files before any live action."""
    listed = manifest_entries(payload, metadata)
    manifest = payload / "release-files.sha256"
    actual: set[str] = set()
    for path in payload.rglob("*"):
        if path.is_symlink() or not (path.is_file() or path.is_dir()):
            raise ValueError("linked or special payload path")
        if path.is_file() and path != manifest:
            name = path.relative_to(payload).as_posix()
            if name == "release.env":
                raise ValueError("release.env must be written by installer")
            actual.add(name)
            if listed.get(name) != digest(path):
                raise ValueError(f"payload digest mismatch: {name}")
    if actual != set(listed):
        raise ValueError("manifest does not exactly cover payload")
    if not (payload / "bin/aicrm").is_file():
        raise ValueError("missing API executable")
    if not (payload / "web/dist").is_dir():
        raise ValueError("missing web assets")


def current_sha() -> str | None:
    if not CURRENT.is_symlink():
        return None
    target = CURRENT.resolve(strict=True)
    if target.parent != RELEASES.resolve():
        raise ValueError("current points outside release root")
    release_env = target / "release.env"
    value = release_env.read_text().strip() if release_env.exists() else ""
    if not re.fullmatch(r"AICRM_RELEASE_SHA=[0-9a-f]{40}", value):
        raise ValueError("current release has no exact SHA")
    return value.split("=", 1)[1]


def switch_to(path: Path) -> None:
    temp = ROOT / f".current.{os.getpid()}"
    temp.symlink_to(path)
    os.replace(temp, CURRENT)


def readiness(sha: str, extra_units: tuple[str, ...] = ()) -> None:
    last = "unavailable"
    for _ in range(30):
        try:
            with urllib.request.urlopen(READY, timeout=2) as response:
                body = json.load(response)
            if body.get("status") == "ready" and body.get("release_sha") == sha:
                services = ("aicrm.service", "aicrm-effects-worker.service")
                active = all(run("systemctl", "is-active", "--quiet", unit, check=False).returncode == 0 for unit in services)
                expected_binary = CURRENT.resolve(strict=True) / "bin/aicrm"
                matching_images = True
                for unit in services:
                    pid_text = run("systemctl", "show", unit, "-p", "MainPID", "--value").stdout.strip()
                    if not pid_text.isdigit() or int(pid_text) <= 0 or Path(f"/proc/{pid_text}/exe").resolve() != expected_binary:
                        matching_images = False
                        break
                extra_active = True
                for unit in extra_units:
                    if run("systemctl", "is-active", "--quiet", unit, check=False).returncode != 0:
                        extra_active = False
                        break
                    pid = run("systemctl", "show", unit, "-p", "MainPID", "--value").stdout.strip()
                    if not pid.isdigit() or int(pid) <= 0:
                        extra_active = False
                        break
                if active and matching_images and extra_active:
                    return
            last = f"status={body.get('status')} sha={body.get('release_sha')}"
        except Exception as exc:
            last = type(exc).__name__
        time.sleep(1)
    raise RuntimeError(f"release health failed: {last}")


def restart_services(extra_units: tuple[str, ...] = ()) -> None:
    run("systemctl", "restart", "aicrm.service")
    run("systemctl", "restart", "aicrm-effects-worker.service")
    for unit in extra_units:
        run("systemctl", "restart", unit)


def changed_units(metadata: dict) -> list[str]:
    paths = metadata.get("changed_paths", [])
    if not isinstance(paths, list) or not all(isinstance(path, str) for path in paths):
        raise ValueError("invalid changed paths")
    units = {Path(path).name for path in paths if path.startswith("deploy/") and Path(path).suffix in {".service", ".timer"} and not Path(path).name.startswith("aicrm-domestic-release.")}
    if any(path.startswith("components/excel-batches/") for path in paths):
        units.add("aicrm-excel-batches.service")
    return sorted(units)


def install_units(release: Path, names: list[str]) -> None:
    for name in names:
        source = release / ("components/excel-batches" if name == "aicrm-excel-batches.service" else "deploy") / name
        if not source.is_file() or source.is_symlink():
            raise RuntimeError(f"systemd unit missing: {name}")
        target = Path("/etc/systemd/system") / name
        fd, temp_name = tempfile.mkstemp(dir=target.parent, prefix=f".{name}.")
        try:
            with os.fdopen(fd, "wb") as out, source.open("rb") as stream:
                shutil.copyfileobj(stream, out)
                out.flush()
                os.fsync(out.fileno())
            os.chmod(temp_name, 0o644)
            os.replace(temp_name, target)
        finally:
            Path(temp_name).unlink(missing_ok=True)
    if names:
        run("systemctl", "daemon-reload")


def restore_units(previous: dict[str, bytes | None]) -> None:
    for name, content in previous.items():
        target = Path("/etc/systemd/system") / name
        if content is None:
            target.unlink(missing_ok=True)
            continue
        fd, temp_name = tempfile.mkstemp(dir=target.parent, prefix=f".{name}.")
        try:
            with os.fdopen(fd, "wb") as out:
                out.write(content)
                out.flush()
                os.fsync(out.fileno())
            os.chmod(temp_name, 0o644)
            os.replace(temp_name, target)
        finally:
            Path(temp_name).unlink(missing_ok=True)
    if previous:
        run("systemctl", "daemon-reload")


def backup_database(sha: str) -> Path:
    """Only migration releases call this; no production data goes to staging."""
    values = [line.partition("=")[2].strip().strip('"\'') for line in ENV.read_text().splitlines() if line.startswith("AICRM_DATABASE_URL=")]
    if len(values) != 1 or not values[0]:
        raise RuntimeError("exactly one database URL is required for migration backup")
    database_env = _database_client_environment(values[0])
    root = database_backup_directory(create=True)
    if root is None:
        raise RuntimeError("database backup directory is missing")
    target = root / f"pre-{sha}.dump"
    if database_backup_artifacts(root, sha):
        raise RuntimeError("backup state for SHA already exists; inspect before retry")
    with tempfile.NamedTemporaryFile(dir=root, prefix=f".pre-{sha}.", delete=False) as out:
        temp = Path(out.name)
        try:
            process = subprocess.run(
                [RUNUSER, "--preserve-environment", "-u", "aicrm", "--", "pg_dump", "-Fc"],
                env={**database_env, "PATH": "/usr/bin:/bin"},
                stdout=out,
                stderr=subprocess.PIPE,
            )
            if process.returncode != 0 or out.tell() < 100:
                # libpq client diagnostics can include connection details. Keep
                # credentials and DSN material out of the release ledger/logs.
                raise RuntimeError("pg_dump failed or returned an incomplete archive")
            out.flush()
            os.fsync(out.fileno())
            run("pg_restore", "--list", str(temp))
            os.chmod(temp, 0o600)
            os.replace(temp, target)
        finally:
            temp.unlink(missing_ok=True)
    return target


def database_backup_directory(*, create: bool) -> Path | None:
    """Return the private backup directory, rejecting symlinks and unsafe modes."""
    root = ROOT / "database-backups"
    if create:
        root.mkdir(mode=0o700, exist_ok=True)
    try:
        info = root.lstat()
    except FileNotFoundError:
        return None
    if (
        not stat.S_ISDIR(info.st_mode)
        or info.st_uid != os.geteuid()
        or info.st_gid != os.getegid()
        or stat.S_IMODE(info.st_mode) != 0o700
    ):
        raise RuntimeError("database backup directory is unsafe")
    return root


def database_backup_artifacts(root: Path | None, sha: str) -> list[Path]:
    """Find a completed backup or a leftover in-progress dump for this SHA."""
    if root is None:
        return []
    target = root / f"pre-{sha}.dump"
    return [path for path in (target, *root.glob(f".pre-{sha}.*")) if path.exists() or path.is_symlink()]


def _strict_unquote(value: str) -> str:
    if re.search(r"%(?![0-9a-fA-F]{2})", value):
        raise ValueError("database URL contains invalid percent encoding")
    try:
        decoded = unquote_to_bytes(value).decode("utf-8", errors="strict")
    except (UnicodeDecodeError, ValueError) as exc:
        raise ValueError("database URL contains invalid percent encoding") from exc
    if any(ord(char) < 0x20 or ord(char) == 0x7f for char in decoded):
        raise ValueError("database URL contains unsupported characters")
    return decoded


def database_environment(database_url: str) -> dict[str, str]:
    """Translate the supported PostgreSQL URI subset to libpq PG* settings.

    The URI itself must never be passed as PGDATABASE or placed in argv. Keep
    the accepted option set narrow so TLS or connection behavior cannot be
    silently discarded.
    """
    try:
        parsed = urlsplit(database_url)
        if re.search(r"%(?![0-9a-fA-F]{2})", database_url) or parsed.scheme not in {"postgres", "postgresql"} or parsed.fragment:
            raise ValueError
        host = parsed.hostname
        if not host or not re.fullmatch(r"[A-Za-z0-9._:-]+", host) or parsed.username is None or not parsed.path.startswith("/"):
            raise ValueError
        parsed_port = parsed.port
        port = 5432 if parsed_port is None else parsed_port
        if not 1 <= port <= 65535:
            raise ValueError
        username = _strict_unquote(parsed.username)
        password = _strict_unquote(parsed.password) if parsed.password is not None else None
        database = _strict_unquote(parsed.path[1:])
        if not username or not database or "/" in parsed.path[1:]:
            raise ValueError
        query = parse_qsl(parsed.query, keep_blank_values=True, strict_parsing=True, max_num_fields=2)
        if len(query) > 1 or any(key != "sslmode" for key, _ in query):
            raise ValueError
        environment = {
            "PGHOST": host,
            "PGPORT": str(port),
            "PGUSER": username,
            "PGDATABASE": database,
        }
        if password is not None:
            environment["PGPASSWORD"] = password
        if query:
            sslmode = query[0][1]
            if sslmode not in {"disable", "allow", "prefer", "require", "verify-ca", "verify-full"}:
                raise ValueError
            environment["PGSSLMODE"] = sslmode
        return environment
    except (ValueError, UnicodeError) as exc:
        raise ValueError("database URL is outside the supported PostgreSQL URI subset") from exc


def _database_environment_from_host_config() -> dict[str, str]:
    values = [line.partition("=")[2].strip().strip('"\'') for line in ENV.read_text().splitlines() if line.startswith("AICRM_DATABASE_URL=")]
    if len(values) != 1 or not values[0]:
        raise RuntimeError("exactly one database URL is required")
    return _database_client_environment(values[0])


def _database_client_environment(database_url: str) -> dict[str, str]:
    environment = database_environment(database_url)
    try:
        environment["HOME"] = pwd.getpwnam("aicrm").pw_dir
    except KeyError as exc:
        raise RuntimeError("database service account is unavailable") from exc
    return environment


def require_staging_role() -> None:
    try:
        info = STAGE_ROLE.lstat()
        if (
            not stat.S_ISREG(info.st_mode)
            or STAGE_ROLE.is_symlink()
            or info.st_uid != 0
            or info.st_gid != 0
            or stat.S_IMODE(info.st_mode) & 0o022
            or STAGE_ROLE.read_text() != "staging\n"
        ):
            raise RuntimeError("staging-only retry is not enabled on this host")
    except OSError as exc:
        raise RuntimeError("staging-only retry is not enabled on this host") from exc


def staging_migration_baseline() -> tuple[bool, bool]:
    """Return whether 0206 is unapplied and its old CHECK is still installed."""
    environment = _database_environment_from_host_config()
    if any(environment.get(key) != value for key, value in {
        "PGHOST": "127.0.0.1",
        "PGUSER": "aicrm_test",
        "PGDATABASE": "aicrm_test_baseline_5d15",
    }.items()):
        raise RuntimeError("staging retry database configuration is not the approved synthetic database")
    environment["PGOPTIONS"] = "-c default_transaction_read_only=on -c statement_timeout=5000 -c lock_timeout=500"
    query = f"""
SELECT
  current_database(),
  current_user,
  COALESCE(inet_server_addr()::text, ''),
  CASE WHEN NOT EXISTS (
    SELECT 1 FROM public.platform_schema_migrations WHERE version >= '{STAGING_MIGRATION_VERSION}'
  ) THEN 'yes' ELSE 'no' END,
  COALESCE((
    SELECT 1 FROM pg_catalog.pg_constraint
    WHERE conrelid = 'public.orders'::regclass
      AND conname = 'orders_origin_effect_shape'
      AND contype = 'c'
      AND convalidated
  ), 0),
  COALESCE((
    SELECT pg_get_constraintdef(oid)
    FROM pg_catalog.pg_constraint
    WHERE conrelid = 'public.orders'::regclass
      AND conname = 'orders_origin_effect_shape'
      AND contype = 'c'
      AND convalidated
  ), '')
"""
    try:
        result = subprocess.run(
            [RUNUSER, "--preserve-environment", "-u", "aicrm", "--", "psql", "-X", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-c", query],
            env={**environment, "PATH": "/usr/bin:/bin"},
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            timeout=10,
        )
    except Exception as exc:
        raise RuntimeError("staging migration baseline could not be inspected") from exc
    if result.returncode != 0:
        raise RuntimeError("staging migration baseline could not be inspected")
    values = result.stdout.strip().split("|", 5)
    try:
        # PostgreSQL renders inet addresses with their mask (e.g. 127.0.0.1/32).
        server_is_loopback = ipaddress.ip_interface(values[2]).ip.is_loopback
    except (ValueError, IndexError):
        server_is_loopback = False
    old_constraint_present = len(values) == 6 and values[4] == "1" and is_pre_0206_order_constraint(values[5])
    if (
        len(values) != 6
        or values[0] != "aicrm_test_baseline_5d15"
        or values[1] != "aicrm_test"
        or not server_is_loopback
        or values[3] != "yes"
        or not old_constraint_present
    ):
        raise RuntimeError("staging migration baseline is not the expected pre-0206 schema")
    return True, old_constraint_present


def is_pre_0206_order_constraint(definition: str) -> bool:
    """Match the exact PostgreSQL 16 definition of the old validated CHECK."""
    return re.sub(r"\s+", "", definition).lower() == re.sub(r"\s+", "", PRE_0206_ORDER_CHECK).lower()


def is_only_staging_migration(metadata: dict) -> bool:
    paths = metadata.get("changed_paths")
    if not isinstance(paths, list) or not all(isinstance(path, str) for path in paths):
        return False
    return [path for path in paths if path.startswith("migrations/")] == [STAGING_MIGRATION_PATH]


def inspect_staging_retry(
    metadata_bytes: bytes,
    expected_base: str,
    *,
    expected_sha: str,
    metadata_sha256: str,
) -> dict:
    """Read-only eligibility check for the one approved staging migration retry."""
    require_staging_role()
    if expected_sha != STAGING_RETRY_SHA:
        raise RuntimeError("staging migration retry is limited to the current 5538 incident")
    metadata = verify_metadata_identity(metadata_bytes, expected_sha, metadata_sha256)
    if expected_base is None or not SHA.fullmatch(expected_base):
        raise ValueError("staging retry requires the exact prior SHA")
    if metadata.get("migrations_changed") is not True or not is_only_staging_migration(metadata):
        raise RuntimeError("staging retry is limited to migration 0206")
    for control in (ROOT, RELEASES, RECEIPTS):
        if control.is_symlink() or (control.exists() and not control.is_dir()):
            raise RuntimeError("unsafe release control directory")
    if LOCK.is_symlink() or not LOCK.exists() or not stat.S_ISREG(LOCK.lstat().st_mode):
        raise RuntimeError("unsafe shared install lock")
    backup_root = database_backup_directory(create=False)
    with LOCK.open("r+") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        if current_sha() != expected_base:
            raise RuntimeError("staging retry base has changed")
        release = RELEASES / expected_sha
        receipt = RECEIPTS / f"{expected_sha}.json"
        temp_receipt = RECEIPTS / f".{expected_sha}.json.tmp"
        backup_artifacts = database_backup_artifacts(backup_root, expected_sha)
        if any(path.exists() or path.is_symlink() for path in (receipt, temp_receipt)) or backup_artifacts:
            raise RuntimeError("staging retry target already has receipt or backup state")
        if release.is_symlink() or not release.is_dir():
            raise RuntimeError("staging retry orphan is missing or unsafe")
        verify_payload_without_release_env(release, metadata)
        verify_root_owned_release(release)
        readiness(expected_base)
        migration_pending, old_constraint_present = staging_migration_baseline()
        if not migration_pending or not old_constraint_present:
            raise RuntimeError("staging retry schema baseline is not the expected pre-0206 state")
        return {
            "status": "eligible",
            "source_sha": expected_sha,
            "base_sha": expected_base,
            "orphan_verified": True,
            "receipt_exists": False,
            "backup_exists": False,
            "migration_0206_applied": False,
            "old_constraint_present": True,
        }


def install(
    incoming: Path | None,
    metadata_bytes: bytes,
    expected_base: str | None,
    *,
    expected_sha: str,
    metadata_sha256: str,
    retry_existing: bool = False,
    retry_staging_migration: bool = False,
) -> dict:
    metadata = verify_metadata_identity(metadata_bytes, expected_sha, metadata_sha256)
    sha = expected_sha
    if expected_base is not None and not SHA.fullmatch(expected_base):
        raise ValueError("invalid base SHA")
    if type(metadata.get("migrations_changed")) is not bool:
        raise ValueError("migration classification is required")
    if retry_staging_migration and not retry_existing:
        raise ValueError("staging migration retry requires a verified orphan")
    if not ENV.is_file() or not shutil.which("systemctl"):
        raise RuntimeError("host runtime is not provisioned")
    for control in (ROOT, RELEASES, RECEIPTS):
        if control.is_symlink() or (control.exists() and not control.is_dir()):
            raise RuntimeError("unsafe release control directory")
    if LOCK.is_symlink() or (LOCK.exists() and not stat.S_ISREG(LOCK.stat().st_mode)):
        raise RuntimeError("unsafe shared install lock")
    RELEASES.mkdir(mode=0o755, exist_ok=True)
    with LOCK.open("a+") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        old_sha = current_sha()
        if old_sha != expected_base:
            raise RuntimeError(f"base mismatch: current={old_sha} expected={expected_base}")
        release = RELEASES / sha
        receipt_path = RECEIPTS / f"{sha}.json"
        temp_receipt = RECEIPTS / f".{sha}.json.tmp"
        if any(path.exists() or path.is_symlink() for path in (receipt_path, temp_receipt)):
            raise RuntimeError("target release has receipt state; inspect before retry")
        if retry_existing:
            if metadata["migrations_changed"] and not retry_staging_migration:
                raise RuntimeError("orphan retry is disabled for migration releases")
            if retry_staging_migration:
                require_staging_role()
                if sha != STAGING_RETRY_SHA or metadata["migrations_changed"] is not True or not is_only_staging_migration(metadata):
                    raise RuntimeError("staging migration retry is limited to migration 0206")
            if old_sha is None or old_sha == sha:
                raise RuntimeError("orphan retry requires the prior release to be current")
            if release.is_symlink() or not release.is_dir():
                raise RuntimeError("verified orphan release is missing or unsafe")
            verify_payload_without_release_env(release, metadata)
            verify_root_owned_release(release)
            readiness(old_sha)
            if retry_staging_migration:
                backup_root = database_backup_directory(create=False)
                if database_backup_artifacts(backup_root, sha):
                    raise RuntimeError("backup state for SHA already exists; inspect before retry")
                migration_pending, old_constraint_present = staging_migration_baseline()
                if not migration_pending or not old_constraint_present:
                    raise RuntimeError("staging retry schema baseline is not the expected pre-0206 state")
            make_release_directories_traversable(release)
        else:
            if incoming is None:
                raise ValueError("incoming package is required for a new release")
            verify_payload(incoming, metadata)
            if release.is_symlink() or release.exists():
                raise RuntimeError("target release already exists; inspect before retry")
            # Validate before move and then seal; the receiving account loses
            # write access before the live symlink can point at this directory.
            shutil.move(str(incoming), str(release))
            run("chown", "-R", "root:root", str(release))
            (release / "release.env").write_text(f"AICRM_RELEASE_SHA={sha}\n")
            os.chmod(release / "release.env", 0o644)
            verify_payload_without_release_env(release, metadata)
            verify_root_owned_release(release)
            make_release_directories_traversable(release)
        old_path = CURRENT.resolve(strict=True) if old_sha else None
        bootstrap = old_sha is None
        units = changed_units(metadata)
        if bootstrap:
            units = sorted({path.name for path in (release / "deploy").glob("aicrm*.service") if path.name != "aicrm-domestic-release.service"} | {path.name for path in (release / "deploy").glob("aicrm*.timer") if path.name != "aicrm-domestic-release.timer"})
        unit_snapshot = {name: (Path("/etc/systemd/system") / name).read_bytes() if (Path("/etc/systemd/system") / name).is_file() else None for name in units}
        extra_active = tuple(unit for unit in ("aicrm-excel-batches.service",) if unit in units and run("systemctl", "is-active", "--quiet", unit, check=False).returncode == 0)
        backup = None
        if metadata["migrations_changed"]:
            backup = backup_database(sha)
        switched = False
        try:
            switch_to(release)
            switched = True
            if units:
                install_units(release, units)
            if metadata["migrations_changed"] or bootstrap:
                run("systemctl", "start", "aicrm-migrate.service")
            restart_services(extra_active)
            for unit in units:
                if unit not in {"aicrm.service", "aicrm-effects-worker.service", "aicrm-migrate.service", *extra_active}:
                    run("systemctl", "try-restart", unit)
            readiness(sha, extra_active)
            verify_payload_without_release_env(release, metadata)
        except Exception:
            if switched:
                if old_path:
                    switch_to(old_path)
                    try:
                        if units:
                            restore_units(unit_snapshot)
                        restart_services(extra_active)
                        readiness(old_sha, extra_active)
                    except Exception as rollback_error:
                        raise RuntimeError(f"rollback outcome unknown: {rollback_error}")
                else:
                    CURRENT.unlink(missing_ok=True)
                    run("systemctl", "stop", "aicrm.service", check=False)
                    run("systemctl", "stop", "aicrm-effects-worker.service", check=False)
                    if units:
                        restore_units(unit_snapshot)
            raise
        receipt = {"schema_version": 1, "source_sha": sha, "source_tree": metadata.get("source_tree"), "manifest_sha256": metadata["release_files_sha256"], "previous_sha": old_sha, "database_backup": str(backup) if backup else None, "technical_status": "installed_healthy", "installed_at_utc": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())}
        RECEIPTS.mkdir(mode=0o700, exist_ok=True)
        RECEIPTS.chmod(0o700)
        if temp_receipt.exists() or temp_receipt.is_symlink() or receipt_path.exists() or receipt_path.is_symlink():
            raise RuntimeError("receipt path changed during installation")
        temp_receipt.write_text(json.dumps(receipt, sort_keys=True) + "\n")
        os.chmod(temp_receipt, 0o600)
        os.replace(temp_receipt, receipt_path)
        return receipt


def verify_payload_without_release_env(payload: Path, metadata: dict) -> None:
    release_env = payload / "release.env"
    if not release_env.is_file():
        raise ValueError("release.env missing")
    # The per-host release marker is generated after package verification.
    # Check the immutable package portion without editing any hard-linked file.
    expected = f"AICRM_RELEASE_SHA={metadata['source_sha']}\n"
    if release_env.read_text() != expected:
        raise ValueError("release marker mismatch")
    verify_payload_with_marker(payload, metadata)


def verify_payload_with_marker(payload: Path, metadata: dict) -> None:
    # Installed releases use the same canonical path and duplicate checks.
    entries = manifest_entries(payload, metadata)
    marker = payload / "release.env"
    if marker.is_symlink() or not marker.is_file():
        raise ValueError("release marker missing or unsafe")
    manifest = payload / "release-files.sha256"
    actual = set()
    for path in payload.rglob("*"):
        if path.is_symlink() or not (path.is_file() or path.is_dir()):
            raise ValueError("linked or special release path")
        if path.is_file() and path != payload / "release.env" and path != manifest:
            name = path.relative_to(payload).as_posix()
            actual.add(name)
            if entries.get(name) != digest(path):
                raise ValueError("installed file digest mismatch")
    if actual != set(entries):
        raise ValueError("installed file set mismatch")


def verify_root_owned_release(release: Path) -> None:
    """Require every verified release path to be a root-owned file or directory."""
    entries = [(path, path.lstat()) for path in (release, *release.rglob("*"))]
    for _path, info in entries:
        if stat.S_ISLNK(info.st_mode) or not (stat.S_ISREG(info.st_mode) or stat.S_ISDIR(info.st_mode)):
            raise ValueError("linked or special release path")
    for _path, info in entries:
        if stat.S_IMODE(info.st_mode) & 0o022:
            raise ValueError("release path is group or other writable")
    for _path, info in entries:
        if info.st_uid != 0 or info.st_gid != 0:
            raise ValueError("release path is not root-owned")


def make_release_directories_traversable(release: Path) -> None:
    """Make verified root-owned directories traversable without chmodding files."""
    directories = [release, *(path for path in release.rglob("*") if path.is_dir())]
    modes = []
    for directory in directories:
        info = directory.lstat()
        if stat.S_ISLNK(info.st_mode) or not stat.S_ISDIR(info.st_mode):
            raise ValueError("release path is not a directory")
        mode = stat.S_IMODE(info.st_mode)
        if mode & 0o022:
            raise ValueError("release directory is group or other writable")
        safe_mode = (mode | 0o555) & ~0o022
        modes.append((directory, mode, safe_mode))
    for directory, mode, safe_mode in modes:
        if mode != safe_mode:
            directory.chmod(safe_mode)


def main() -> None:
    p = argparse.ArgumentParser()
    source = p.add_mutually_exclusive_group(required=True)
    source.add_argument("--incoming", type=Path)
    source.add_argument("--retry-existing", action="store_true", help="reuse a checksum-verified orphan under the install lock")
    source.add_argument("--inspect-staging-retry", action="store_true", help="read-only preflight for the one staging migration retry")
    p.add_argument("--retry-staging-migration", action="store_true", help="allow only the explicitly gated staging orphan retry for migration 0206")
    p.add_argument("--metadata", type=Path, required=True)
    p.add_argument("--expected-sha", required=True, help="exact source SHA bound to the metadata")
    p.add_argument("--metadata-sha256", required=True, help="SHA256 of the exact metadata file bytes")
    p.add_argument("--expected-base", required=True, help="40-char installed SHA or 'none' for empty staging")
    args = p.parse_args()
    if os.geteuid() != 0:
        raise SystemExit("root required")
    if args.retry_staging_migration and not args.retry_existing:
        p.error("--retry-staging-migration requires --retry-existing")
    metadata_root = ROOT / "domestic-incoming"
    if args.incoming is not None and not args.incoming.resolve().is_relative_to(metadata_root):
        raise SystemExit("incoming path outside domestic-incoming")
    if (args.retry_existing or args.inspect_staging_retry) and not args.metadata.resolve().is_relative_to(metadata_root):
        raise SystemExit("retry metadata path outside domestic-incoming")
    if args.metadata.is_symlink() or not args.metadata.is_file():
        raise SystemExit("metadata path must be a regular file")
    metadata_bytes = args.metadata.read_bytes()
    expected_base = None if args.expected_base == "none" else args.expected_base
    if args.inspect_staging_retry:
        result = inspect_staging_retry(
            metadata_bytes,
            expected_base,
            expected_sha=args.expected_sha,
            metadata_sha256=args.metadata_sha256,
        )
        print(json.dumps(result, sort_keys=True))
        return
    result = install(
        args.incoming,
        metadata_bytes,
        expected_base,
        expected_sha=args.expected_sha,
        metadata_sha256=args.metadata_sha256,
        retry_existing=args.retry_existing,
        retry_staging_migration=args.retry_staging_migration,
    )
    print(json.dumps(result, sort_keys=True))


if __name__ == "__main__":
    main()
