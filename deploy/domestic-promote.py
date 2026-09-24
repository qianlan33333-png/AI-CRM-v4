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
import socket
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
HOST_ROLE_FILE = Path("/etc/aicrm/domestic-release-role")
READY = "http://127.0.0.1:8080/readyz"
HOST_ROLE_DIRECTORY = Path("/etc/aicrm")
HOST_ROLES = {"staging", "production"}
HOST_CONTRACT_PATH = "/usr/bin:/bin"
# These identities were read back from the current domestic VMs. If a host is
# renamed or its private address changes, releases must stop until this mapping
# is reviewed and updated in a trusted helper change.
HOST_IDENTITIES = {
    "vm-4-6-ubuntu": ("staging", "10.0.4.6"),
    "vm-4-13-ubuntu": ("production", "10.0.4.13"),
}


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


def migration_inventory(release: Path) -> dict[str, str]:
    """Hash the candidate SQL independently of the release controller's flag."""
    root = release / "migrations"
    if not root.exists():
        return {}
    if root.is_symlink() or not root.is_dir():
        raise ValueError("migration path is unsafe")
    inventory: dict[str, str] = {}
    for path in root.rglob("*"):
        if path.is_symlink() or not (path.is_dir() or path.is_file()):
            raise ValueError("migration path contains a link or special file")
        if path.is_file() and path.suffix == ".sql":
            inventory[path.relative_to(release).as_posix()] = digest(path)
    return inventory


def reject_unclassified_migration_changes(metadata: dict, candidate: Path, previous: Path | None) -> None:
    """Never let a false PR/build flag suppress a production migration backup."""
    if metadata["migrations_changed"] or previous is None:
        return
    if migration_inventory(candidate) != migration_inventory(previous):
        raise ValueError("migration SQL changed but release metadata marks migrations_changed=false")


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
    """Create a verified production backup; synthetic staging databases are never dumped."""
    require_host_role("production")
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


def actual_host_role() -> str:
    """Bind marker interpretation to the known VM hostname and private IP."""
    hostname = socket.gethostname().split(".", 1)[0].lower()
    identity = HOST_IDENTITIES.get(hostname)
    if identity is None:
        raise RuntimeError("host identity is not approved for domestic release")
    role, expected_address = identity
    ip_command = next((path for path in (Path("/usr/sbin/ip"), Path("/usr/bin/ip")) if path.exists()), None)
    if ip_command is None:
        raise RuntimeError("host identity address probe is unavailable")
    _require_host_tool(str(ip_command), "ip")
    try:
        result = subprocess.run(
            [str(ip_command), "-o", "-4", "addr", "show", "scope", "global"],
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            timeout=5,
        )
    except Exception as exc:
        raise RuntimeError("host identity address probe failed") from exc
    if result.returncode != 0:
        raise RuntimeError("host identity address probe failed")
    addresses: set[str] = set()
    for line in result.stdout.splitlines():
        fields = line.split()
        if "inet" in fields:
            try:
                addresses.add(str(ipaddress.ip_interface(fields[fields.index("inet") + 1]).ip))
            except (ValueError, IndexError):
                continue
    if expected_address not in addresses:
        raise RuntimeError("host identity address does not match its approved role")
    return role


def require_host_role(expected: str | None = None) -> str:
    """Read the protected host role; package metadata can never select it."""
    try:
        directory_info = HOST_ROLE_DIRECTORY.lstat()
        info = HOST_ROLE_FILE.lstat()
        if (
            not stat.S_ISDIR(directory_info.st_mode)
            or HOST_ROLE_DIRECTORY.is_symlink()
            or directory_info.st_uid != 0
            or directory_info.st_gid != 0
            or stat.S_IMODE(directory_info.st_mode) & 0o022
            or not stat.S_ISREG(info.st_mode)
            or HOST_ROLE_FILE.is_symlink()
            or info.st_uid != 0
            or info.st_gid != 0
            or stat.S_IMODE(info.st_mode) & 0o022
        ):
            raise RuntimeError("host role configuration is missing or unsafe")
        value = HOST_ROLE_FILE.read_text(encoding="ascii")
    except (OSError, UnicodeError) as exc:
        raise RuntimeError("host role configuration is missing or unsafe") from exc
    role = value.removesuffix("\n")
    if value != f"{role}\n" or role not in HOST_ROLES:
        raise RuntimeError("host role configuration must be exactly staging or production")
    if role != actual_host_role():
        raise RuntimeError("host role configuration does not match the machine identity")
    if expected is not None and role != expected:
        raise RuntimeError(f"host role mismatch: expected {expected}, found {role}")
    return role


def _host_tool(name: str) -> str:
    path = shutil.which(name, path=HOST_CONTRACT_PATH)
    if path is None:
        raise RuntimeError(f"required host tool is unavailable in restricted PATH: {name}")
    return path


def _require_root_executable(path: str, label: str) -> None:
    candidate = Path(path)
    try:
        info = candidate.lstat()
        if (
            candidate.is_symlink()
            or not stat.S_ISREG(info.st_mode)
            or info.st_uid != 0
            or info.st_gid != 0
            or stat.S_IMODE(info.st_mode) & 0o022
            or not os.access(candidate, os.X_OK)
        ):
            raise RuntimeError(f"unsafe host executable: {label}")
    except OSError as exc:
        raise RuntimeError(f"required host executable is unavailable: {label}") from exc


def _require_host_tool(path: str, label: str) -> None:
    candidate = Path(path)
    try:
        resolved = candidate.resolve(strict=True)
        info = resolved.stat()
        if (
            not resolved.is_file()
            or info.st_uid != 0
            or info.st_gid != 0
            or stat.S_IMODE(info.st_mode) & 0o022
            or not os.access(resolved, os.X_OK)
        ):
            raise RuntimeError(f"unsafe host tool: {label}")
    except OSError as exc:
        raise RuntimeError(f"required host tool is unavailable: {label}") from exc


def _service_user_can(flag: str, path: Path) -> None:
    result = subprocess.run(
        [RUNUSER, "--preserve-environment", "-u", "aicrm", "--", "/usr/bin/test", flag, str(path)],
        env={"HOME": pwd.getpwnam("aicrm").pw_dir, "PATH": HOST_CONTRACT_PATH},
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        timeout=10,
    )
    if result.returncode != 0:
        raise RuntimeError("service account cannot access the configured runtime path")


def check_host_contract() -> dict:
    """Read-only host rehearsal for the fixed helper, service user, and PG16."""
    role = require_host_role()
    if not ENV.is_file() or ENV.is_symlink():
        raise RuntimeError("host runtime environment is missing or unsafe")
    _require_root_executable(RUNUSER, "runuser")
    _require_host_tool(_host_tool("psql"), "psql")
    systemctl = _host_tool("systemctl")
    _require_host_tool(systemctl, "systemctl")
    if role == "production":
        _require_host_tool(_host_tool("pg_dump"), "pg_dump")
        _require_host_tool(_host_tool("pg_restore"), "pg_restore")
    try:
        service = pwd.getpwnam("aicrm")
    except KeyError as exc:
        raise RuntimeError("database service account is unavailable") from exc
    if not service.pw_dir:
        raise RuntimeError("database service account has no home directory")
    for flag, path in (("-r", ENV), ("-x", CURRENT), ("-x", CURRENT / "bin/aicrm")):
        _service_user_can(flag, path)
    for unit in ("aicrm.service", "aicrm-effects-worker.service", "aicrm-migrate.service"):
        result = run(systemctl, "show", unit, "-p", "User", "-p", "Group", "-p", "WorkingDirectory")
        fields = dict(line.split("=", 1) for line in result.stdout.splitlines() if "=" in line)
        if fields != {"User": "aicrm", "Group": "aicrm", "WorkingDirectory": str(CURRENT)}:
            raise RuntimeError(f"systemd service contract is invalid: {unit}")
    environment = _database_environment_from_host_config()
    if role == "staging":
        try:
            configured_host_is_loopback = ipaddress.ip_address(environment["PGHOST"]).is_loopback
        except (KeyError, ValueError):
            configured_host_is_loopback = False
        if not configured_host_is_loopback:
            raise RuntimeError("staging PostgreSQL must use an explicit loopback IP")
    environment["PGOPTIONS"] = "-c default_transaction_read_only=on -c statement_timeout=5000"
    query = "SELECT json_build_array(current_setting('server_version_num')::int, current_database(), current_user, COALESCE(inet_server_addr()::text, ''))::text;"
    try:
        result = subprocess.run(
            [RUNUSER, "--preserve-environment", "-u", "aicrm", "--", "psql", "-X", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-c", query],
            env={**environment, "PATH": HOST_CONTRACT_PATH},
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            timeout=10,
        )
    except Exception as exc:
        raise RuntimeError("PostgreSQL host contract probe failed") from exc
    if result.returncode != 0:
        raise RuntimeError("PostgreSQL host contract probe failed")
    try:
        values = json.loads(result.stdout.strip())
        if not isinstance(values, list) or len(values) != 4:
            raise ValueError
        version_major = int(values[0]) // 10000
        server_address_is_loopback = ipaddress.ip_interface(values[3]).ip.is_loopback
    except (ValueError, TypeError, IndexError):
        raise RuntimeError("PostgreSQL host contract returned invalid identity") from None
    if (
        len(values) != 4
        or version_major != 16
        or values[1] != environment.get("PGDATABASE")
        or values[2] != environment.get("PGUSER")
        or not values[3]
        or (role == "staging" and not server_address_is_loopback)
    ):
        raise RuntimeError("PostgreSQL host contract identity or version mismatch")
    return {"host_role": role, "postgres_major": version_major, "database_connection": "verified", "systemd_services": 3}


def verify_helper_digest(expected_sha256: str) -> str:
    if not isinstance(expected_sha256, str) or not FILE_SHA.fullmatch(expected_sha256):
        raise ValueError("expected helper SHA256 is invalid")
    helper = Path(__file__)
    try:
        helper_stat = helper.lstat()
        parent_stat = helper.parent.lstat()
    except OSError as exc:
        raise RuntimeError("fixed helper location cannot be verified safely") from exc
    if (
        not stat.S_ISREG(helper_stat.st_mode)
        or helper_stat.st_uid != 0
        or helper_stat.st_gid != 0
        or helper_stat.st_mode & 0o022
        or not stat.S_ISDIR(parent_stat.st_mode)
        or parent_stat.st_uid != 0
        or parent_stat.st_gid != 0
        or parent_stat.st_mode & 0o022
    ):
        raise RuntimeError("fixed helper must be a root-owned file in a protected root-owned directory")
    actual = digest(helper)
    if actual != expected_sha256:
        raise RuntimeError("fixed helper SHA256 does not match the checked Git source file")
    return actual


def install(
    incoming: Path | None,
    metadata_bytes: bytes,
    expected_base: str | None,
    *,
    expected_sha: str,
    metadata_sha256: str,
    retry_existing: bool = False,
) -> dict:
    metadata = verify_metadata_identity(metadata_bytes, expected_sha, metadata_sha256)
    sha = expected_sha
    host_role = require_host_role()
    if expected_base is not None and not SHA.fullmatch(expected_base):
        raise ValueError("invalid base SHA")
    if type(metadata.get("migrations_changed")) is not bool:
        raise ValueError("migration classification is required")
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
        if require_host_role() != host_role:
            raise RuntimeError("host role changed during installation preflight")
        old_sha = current_sha()
        if old_sha != expected_base:
            raise RuntimeError(f"base mismatch: current={old_sha} expected={expected_base}")
        bootstrap = old_sha is None
        old_path = CURRENT.resolve(strict=True) if old_sha else None
        release = RELEASES / sha
        receipt_path = RECEIPTS / f"{sha}.json"
        temp_receipt = RECEIPTS / f".{sha}.json.tmp"
        if any(path.exists() or path.is_symlink() for path in (receipt_path, temp_receipt)):
            raise RuntimeError("target release has receipt state; inspect before retry")
        if retry_existing:
            if metadata["migrations_changed"]:
                raise RuntimeError("orphan retry is disabled for migration releases")
            if old_sha is None or old_sha == sha:
                raise RuntimeError("orphan retry requires the prior release to be current")
            if release.is_symlink() or not release.is_dir():
                raise RuntimeError("verified orphan release is missing or unsafe")
            verify_payload_without_release_env(release, metadata)
            verify_root_owned_release(release)
            reject_unclassified_migration_changes(metadata, release, old_path)
            readiness(old_sha)
            make_release_directories_traversable(release)
        else:
            if incoming is None:
                raise ValueError("incoming package is required for a new release")
            verify_payload(incoming, metadata)
            reject_unclassified_migration_changes(metadata, incoming, old_path)
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
        units = changed_units(metadata)
        if bootstrap:
            units = sorted({path.name for path in (release / "deploy").glob("aicrm*.service") if path.name != "aicrm-domestic-release.service"} | {path.name for path in (release / "deploy").glob("aicrm*.timer") if path.name != "aicrm-domestic-release.timer"})
        unit_snapshot = {name: (Path("/etc/systemd/system") / name).read_bytes() if (Path("/etc/systemd/system") / name).is_file() else None for name in units}
        extra_active = tuple(unit for unit in ("aicrm-excel-batches.service",) if unit in units and run("systemctl", "is-active", "--quiet", unit, check=False).returncode == 0)
        backup = None
        needs_migration = metadata["migrations_changed"] or bootstrap
        if needs_migration and host_role == "production":
            backup = backup_database(sha)
        switched = False
        try:
            switch_to(release)
            switched = True
            if units:
                install_units(release, units)
            if needs_migration:
                try:
                    run("systemctl", "start", "aicrm-migrate.service")
                except Exception as exc:
                    if needs_migration and host_role == "staging":
                        raise RuntimeError(
                            "staging migration failed; candidate stopped and live runtime returned to the previous release where one existed. "
                            "Rebuild the disposable synthetic database from the current migration set "
                            "and synthetic fixtures before creating a fresh candidate; do not restore a dump."
                        ) from exc
                    raise
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
    source.add_argument("--check-host-contract", action="store_true", help="read-only check of role, PostgreSQL 16, service user, paths, and systemd")
    p.add_argument("--metadata", type=Path)
    p.add_argument("--expected-sha", help="exact source SHA bound to the metadata")
    p.add_argument("--metadata-sha256", help="SHA256 of the exact metadata file bytes")
    p.add_argument("--expected-base", help="40-char installed SHA or 'none' for empty staging")
    p.add_argument("--expected-helper-sha256", help="SHA256 of the exact checked Git source file being rehearsed")
    args = p.parse_args()
    if os.geteuid() != 0:
        raise SystemExit("root required")
    if args.check_host_contract:
        if any(value is not None for value in (args.metadata, args.expected_sha, args.metadata_sha256, args.expected_base)):
            p.error("--check-host-contract does not accept release metadata")
        if args.expected_helper_sha256 is None:
            p.error("--check-host-contract requires --expected-helper-sha256 for the exact checked Git source file")
        try:
            helper_sha256 = verify_helper_digest(args.expected_helper_sha256)
            result = check_host_contract()
        except (ValueError, RuntimeError) as exc:
            raise SystemExit(str(exc)) from exc
        result["helper_sha256"] = helper_sha256
        print(json.dumps(result, sort_keys=True))
        return
    if args.expected_helper_sha256 is not None:
        p.error("--expected-helper-sha256 is only valid with --check-host-contract")
    if args.metadata is None or args.expected_sha is None or args.metadata_sha256 is None or args.expected_base is None:
        p.error("installation requires --metadata, --expected-sha, --metadata-sha256, and --expected-base")
    metadata_root = ROOT / "domestic-incoming"
    if args.incoming is not None and not args.incoming.resolve().is_relative_to(metadata_root):
        raise SystemExit("incoming path outside domestic-incoming")
    if args.retry_existing and not args.metadata.resolve().is_relative_to(metadata_root):
        raise SystemExit("retry metadata path outside domestic-incoming")
    if args.metadata.is_symlink() or not args.metadata.is_file():
        raise SystemExit("metadata path must be a regular file")
    metadata_bytes = args.metadata.read_bytes()
    expected_base = None if args.expected_base == "none" else args.expected_base
    result = install(
        args.incoming,
        metadata_bytes,
        expected_base,
        expected_sha=args.expected_sha,
        metadata_sha256=args.metadata_sha256,
        retry_existing=args.retry_existing,
    )
    print(json.dumps(result, sort_keys=True))


if __name__ == "__main__":
    main()
