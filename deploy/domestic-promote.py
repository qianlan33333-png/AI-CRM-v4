#!/usr/bin/env python3
"""Install an already built CRM v4 directory on staging or production.

This fixed, root-owned helper is deliberately separate from the release payload.
Only a checksum-verified payload may become /opt/aicrm/current. It shares the
legacy install lock so an old/manual installer cannot switch current at once.
"""
from __future__ import annotations

import argparse
from contextlib import contextmanager
import fcntl
import grp
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import pwd
import re
import shutil
import signal
import socket
import stat
import subprocess
import sys
import tarfile
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
SMOKE_SOURCE_REPOSITORY = Path("/opt/aicrm/source")
SMOKE_SNAPSHOT_ROOT = ROOT / "domestic-smoke-sources"
SMOKE_DIAGNOSTIC_ROOT = ROOT / "domestic-smoke-diagnostics"
SMOKE_GO = Path("/opt/aicrm/toolchain/go-1.26.6/bin/go")
SMOKE_NODE = Path("/opt/aicrm/toolchain/node-v24.18.0-linux-x64/bin/node")
SMOKE_GOCACHE = Path("/opt/aicrm/cache/go-build")
SMOKE_GOMODCACHE = Path("/opt/aicrm/cache/go-mod")
SMOKE_CONTRACT = "alipay_checkout"
SMOKE_TEST_NAME = "TestDomesticReleaseInstalledAlipayCheckout"
SMOKE_TEST_MARKER = "domestic_release_installed_alipay_checkout: PASS"
SMOKE_TEST_TIMEOUT_SECONDS = 840
SMOKE_GOMAXPROCS = "2"
SMOKE_SOURCE_REMOTES = {
    "git@github.com:qianlan33333-png/AI-CRM-v4.git",
    "https://github.com/qianlan33333-png/AI-CRM-v4.git",
}
DOMESTIC_INCOMING = ROOT / "domestic-incoming"
SOURCE_BACKUPS = ROOT / "source-backups"
DOMESTIC_MAIN = ROOT / "domestic-main"
DOMESTIC_MAIN_STATE = DOMESTIC_MAIN / "state.json"
DOMESTIC_SOURCE_RECEIPT_SUFFIX = ".bundle.json"
DOMESTIC_SOURCE_BUNDLE_MIN_FREE_BYTES = 1024 * 1024 * 1024
DOMESTIC_SOURCE_REFS = ("refs/heads/main", "refs/domestic/candidates/{sha}")
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
    units = {Path(path).name for path in paths if path.startswith("deploy/") and Path(path).suffix in {".service", ".timer"}
             and not Path(path).name.startswith(("aicrm-domestic-release.", "aicrm-domestic-main-release."))}
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


def _staging_smoke_user_can(flag: str, path: Path) -> None:
    try:
        account = pwd.getpwnam("ubuntu")
    except KeyError as exc:
        raise RuntimeError("staging smoke account is unavailable") from exc
    result = subprocess.run(
        [RUNUSER, "--preserve-environment", "-u", "ubuntu", "--", "/usr/bin/test", flag, str(path)],
        env={"HOME": account.pw_dir, "PATH": HOST_CONTRACT_PATH},
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        timeout=10,
    )
    if result.returncode != 0:
        raise RuntimeError("staging smoke account cannot access the configured offline build path")


def _require_protected_runtime_environment(path: Path) -> None:
    """Require a root-readable, root-owned secret file without following links."""
    try:
        info = path.lstat()
    except OSError as exc:
        raise RuntimeError("host runtime environment is missing or unsafe") from exc
    mode = stat.S_IMODE(info.st_mode)
    if (
        not stat.S_ISREG(info.st_mode)
        or info.st_uid != 0
        or not mode & 0o400
        or mode & 0o022
        or mode & 0o111
        or mode & 0o007
    ):
        raise RuntimeError("host runtime environment is missing or unsafe")
    if mode & 0o040:
        try:
            service_group = grp.getgrnam("aicrm").gr_gid
        except KeyError as exc:
            raise RuntimeError("host runtime environment is missing or unsafe") from exc
        if info.st_gid != service_group:
            raise RuntimeError("host runtime environment is missing or unsafe")


def _has_required_systemd_environment_file(value: str, path: Path) -> bool:
    # systemd --show renders each environment file as
    # "/path (ignore_errors=no)". Require the secret file as mandatory; the
    # per-release environment may remain optional.
    pattern = rf"(?:^|\s){re.escape(str(path))} \(ignore_errors=no\)(?:\s|$)"
    return re.search(pattern, value) is not None


def _parse_systemd_show_fields(output: str) -> dict[str, list[str]]:
    """Preserve repeated systemctl properties such as EnvironmentFiles."""
    fields: dict[str, list[str]] = {}
    for line in output.splitlines():
        if "=" not in line:
            continue
        key, value = line.split("=", 1)
        fields.setdefault(key, []).append(value)
    return fields


def check_host_contract() -> dict:
    """Read-only host rehearsal for the fixed helper, service user, and PG16."""
    role = require_host_role()
    _require_protected_runtime_environment(ENV)
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
    for flag, path in (("-x", CURRENT), ("-x", CURRENT / "bin/aicrm")):
        _service_user_can(flag, path)
    for unit in ("aicrm.service", "aicrm-effects-worker.service", "aicrm-migrate.service"):
        result = run(systemctl, "show", unit, "-p", "User", "-p", "Group", "-p", "WorkingDirectory", "-p", "EnvironmentFiles")
        fields = _parse_systemd_show_fields(result.stdout)
        if (
            fields.get("User") != ["aicrm"]
            or fields.get("Group") != ["aicrm"]
            or fields.get("WorkingDirectory") != [str(CURRENT)]
            or not _has_required_systemd_environment_file("\n".join(fields.get("EnvironmentFiles", [])), ENV)
        ):
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


def _staging_database_url() -> str:
    _require_protected_runtime_environment(ENV)
    values = [line.partition("=")[2].strip().strip("\"'") for line in ENV.read_text().splitlines() if line.startswith("AICRM_DATABASE_URL=")]
    if len(values) != 1 or not values[0]:
        raise RuntimeError("exactly one staging database URL is required")
    parsed = database_environment(values[0])
    try:
        if not ipaddress.ip_address(parsed["PGHOST"]).is_loopback:
            raise RuntimeError("staging PostgreSQL must use an explicit loopback IP")
    except (KeyError, ValueError) as exc:
        raise RuntimeError("staging PostgreSQL must use an explicit loopback IP") from exc
    return values[0]


def staging_smoke_test_environment(
    runtime_tmp: Path,
) -> dict[str, str]:
    """Return only the fixed build-tool environment for the unprivileged runner."""
    return {
        "HOME": pwd.getpwnam("ubuntu").pw_dir,
        "PATH": f"{SMOKE_GO.parent}:{SMOKE_NODE.parent}:/usr/bin:/bin",
        "GOCACHE": str(SMOKE_GOCACHE),
        "GOMODCACHE": str(SMOKE_GOMODCACHE),
        "GOTOOLCHAIN": "local",
        "GOMAXPROCS": SMOKE_GOMAXPROCS,
        "GOPROXY": "off",
        "GOSUMDB": "off",
        "GOWORK": "off",
        "TMPDIR": str(runtime_tmp),
    }


def _write_staging_smoke_inputs(
    path: Path,
    *,
    database_url: str,
    source_sha: str,
    installed_sha: str,
    installed_binary: Path,
    installed_binary_sha256: str,
    uid: int,
    gid: int,
) -> None:
    """Write private fixture inputs outside argv/environment and transfer them to ubuntu."""
    database_environment(database_url)
    payload = {
        "stage_role": "staging",
        "source_sha": source_sha,
        "installed_sha": installed_sha,
        "installed_binary": str(installed_binary),
        "installed_binary_sha256": installed_binary_sha256,
        "database_url": database_url,
    }
    encoded = (json.dumps(payload, sort_keys=True) + "\n").encode("utf-8")
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_NOFOLLOW", 0), 0o600)
    try:
        with os.fdopen(descriptor, "wb", closefd=False) as output:
            output.write(encoded)
            output.flush()
            os.fsync(descriptor)
        os.fchown(descriptor, uid, gid)
        os.fchmod(descriptor, 0o400)
    except Exception:
        path.unlink(missing_ok=True)
        raise
    finally:
        os.close(descriptor)


def _source_commit_tree(source_sha: str, *, source_ref: str | None = None,
                        source_repository: Path | None = None) -> str:
    if not SHA.fullmatch(source_sha):
        raise ValueError("invalid smoke source SHA")
    source_repository = source_repository or SMOKE_SOURCE_REPOSITORY
    repository = source_repository
    if repository.is_symlink() or not repository.is_dir() or (repository / ".git").is_symlink() or not (repository / ".git").exists():
        # Bare repositories have no .git directory; accept them only when a
        # domestic immutable candidate ref is supplied explicitly.
        if source_ref is None or repository.is_symlink() or not repository.is_dir():
            raise RuntimeError("fixed source repository is missing or unsafe")
    is_bare = run("git", f"--git-dir={repository}", "rev-parse", "--is-bare-repository", check=False).stdout.strip() == "true"
    if source_ref is not None:
        expected_ref = f"refs/domestic/candidates/{source_sha}"
        if source_ref != expected_ref or not is_bare:
            raise RuntimeError("domestic smoke requires the exact immutable candidate ref in a bare repository")
        resolved = run("git", f"--git-dir={repository}", "rev-parse", "--verify", f"{source_ref}^{{commit}}").stdout.strip()
        if resolved != source_sha:
            raise RuntimeError("domestic smoke candidate ref does not resolve to the exact source SHA")
        tree = run("git", f"--git-dir={repository}", "rev-parse", "--verify", f"{source_sha}^{{tree}}").stdout.strip()
        if not re.fullmatch(r"[0-9a-f]{40}", tree):
            raise RuntimeError("smoke source tree identity is invalid")
        return tree
    if is_bare:
        raise RuntimeError("bare source repository requires an explicit domestic candidate ref")
    git_prefix = ("git", "-C", str(repository), "-c", f"safe.directory={repository}")
    origin = run(*git_prefix, "remote", "get-url", "origin").stdout.strip()
    if origin not in SMOKE_SOURCE_REMOTES:
        raise RuntimeError("fixed source repository origin is not the approved CRM v4 repository")
    run(*git_prefix, "cat-file", "-e", f"{source_sha}^{{commit}}")
    ancestry = subprocess.run((*git_prefix, "merge-base", "--is-ancestor", source_sha, "refs/remotes/origin/main"), stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    if ancestry.returncode != 0:
        raise RuntimeError("smoke source commit is not on the checked main history")
    tree = run(*git_prefix, "rev-parse", f"{source_sha}^{{tree}}").stdout.strip()
    if not re.fullmatch(r"[0-9a-f]{40}", tree):
        raise RuntimeError("smoke source tree identity is invalid")
    return tree


def _source_git_dir(repository: Path | None = None) -> str:
    repository = repository or SMOKE_SOURCE_REPOSITORY
    git_prefix = ("git", "-C", str(repository), "-c", f"safe.directory={repository}")
    value = run(*git_prefix, "rev-parse", "--absolute-git-dir").stdout.strip()
    path = Path(value)
    if not path.is_absolute() or path.is_symlink() or not path.is_dir():
        raise RuntimeError("fixed source repository Git directory is unavailable")
    return str(path.resolve(strict=True))


def _extract_source_archive(archive_path: Path, destination: Path) -> None:
    """Extract only regular files and directories from a trusted Git archive."""
    destination.mkdir(mode=0o700)
    root = destination.resolve(strict=True)
    seen: set[str] = set()
    try:
        with tarfile.open(archive_path, mode="r:") as archive:
            members = archive.getmembers()
            if not members:
                raise RuntimeError("smoke source archive is empty")
            total_bytes = 0
            for member in members:
                name = member.name[:-1] if member.isdir() and member.name.endswith("/") else member.name
                relative = Path(name)
                if (
                    not name
                    or "\\" in name
                    or relative.is_absolute()
                    or relative.as_posix() != name
                    or any(part in {"", ".", ".."} for part in relative.parts)
                    or name in seen
                    or not (member.isdir() or member.isfile())
                ):
                    raise RuntimeError("smoke source archive contains an unsafe path or entry")
                seen.add(name)
                target = root.joinpath(*relative.parts)
                if not target.resolve(strict=False).is_relative_to(root):
                    raise RuntimeError("smoke source archive escapes its fixed snapshot")
                if member.isdir():
                    target.mkdir(mode=0o755, parents=True, exist_ok=False)
                    continue
                total_bytes += member.size
                if member.size < 0 or total_bytes > 2_000_000_000:
                    raise RuntimeError("smoke source archive exceeds the size limit")
                target.parent.mkdir(mode=0o755, parents=True, exist_ok=True)
                source = archive.extractfile(member)
                if source is None:
                    raise RuntimeError("smoke source archive file data is missing")
                with source, target.open("xb") as output:
                    shutil.copyfileobj(source, output)
                target.chmod(0o444 | (member.mode & 0o111))
        for directory in sorted((path for path in root.rglob("*") if path.is_dir()), reverse=True):
            directory.chmod(0o555)
        root.chmod(0o555)
    except Exception:
        shutil.rmtree(destination, ignore_errors=True)
        raise


def _archive_smoke_source(source_sha: str, destination: Path, *, source_ref: str | None = None,
                          source_repository: Path | None = None) -> str:
    source_repository = source_repository or SMOKE_SOURCE_REPOSITORY
    tree = _source_commit_tree(source_sha, source_ref=source_ref, source_repository=source_repository)
    if source_ref is None:
        command = ("git", "-C", str(source_repository), "-c", f"safe.directory={source_repository}", "archive", "--format=tar", source_sha)
    else:
        command = ("git", f"--git-dir={source_repository}", "archive", "--format=tar", source_sha)
    result = subprocess.run(
        command,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        timeout=120,
    )
    if result.returncode != 0 or not result.stdout:
        raise RuntimeError("fixed source commit could not be archived")
    archive_path = destination / "source.tar"
    archive_path.write_bytes(result.stdout)
    _extract_source_archive(archive_path, destination / "source")
    archive_path.unlink()
    return tree


def _installed_staging_smoke_identity(expected_sha: str, expected_manifest_sha256: str) -> tuple[Path, str]:
    if not SHA.fullmatch(expected_sha) or not FILE_SHA.fullmatch(expected_manifest_sha256):
        raise ValueError("invalid installed staging smoke identity")
    if current_sha() != expected_sha:
        raise RuntimeError("staging smoke current release SHA mismatch")
    release = RELEASES / expected_sha
    if release.is_symlink() or not release.is_dir() or release.resolve(strict=True) != release:
        raise RuntimeError("staging smoke release path is unsafe")
    metadata = {"source_sha": expected_sha, "release_files_sha256": expected_manifest_sha256}
    verify_payload_without_release_env(release, metadata)
    verify_root_owned_release(release)
    manifest = release / "release-files.sha256"
    if digest(manifest) != expected_manifest_sha256:
        raise RuntimeError("staging smoke installed manifest mismatch")
    binary = release / "bin/aicrm"
    info = binary.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_gid != 0 or info.st_mode & 0o022 or not os.access(binary, os.R_OK | os.X_OK):
        raise RuntimeError("staging smoke installed binary is unsafe or unavailable")
    readiness(expected_sha)
    return binary, digest(binary)


def _chown_smoke_source_snapshot(source_root: Path, uid: int, gid: int) -> None:
    """Make only the throwaway extracted source tree writable by the test user."""
    for path in source_root.rglob("*"):
        info = path.lstat()
        if stat.S_ISLNK(info.st_mode) or not (stat.S_ISREG(info.st_mode) or stat.S_ISDIR(info.st_mode)):
            raise RuntimeError("staging smoke source snapshot contains a linked or special path")
        os.chown(path, uid, gid)
        if stat.S_ISDIR(info.st_mode):
            path.chmod(0o755)
        else:
            path.chmod(0o755 if info.st_mode & 0o111 else 0o644)
    os.chown(source_root, uid, gid)
    source_root.chmod(0o755)


def _attach_smoke_source_git_metadata(source_root: Path, index_path: Path, git_dir: str) -> Path:
    """Expose the checked, private index to source-view tooling in the archive."""
    root = Path(source_root)
    index = Path(index_path)
    repository_git_dir = Path(git_dir)
    try:
        root_real = root.resolve(strict=True)
        index_real = index.resolve(strict=True)
        index_info = index.lstat()
        git_dir_info = repository_git_dir.lstat()
    except OSError as exc:
        raise RuntimeError("staging smoke source Git metadata is unavailable") from exc
    if (
        root.is_symlink()
        or not root.is_dir()
        or not stat.S_ISREG(index_info.st_mode)
        or index_info.st_uid != os.geteuid()
        or stat.S_IMODE(index_info.st_mode) != 0o444
        or not stat.S_ISDIR(git_dir_info.st_mode)
        or repository_git_dir.is_symlink()
        or index_real.is_relative_to(root_real)
    ):
        raise RuntimeError("staging smoke source index or Git directory is unsafe")
    git_dir_real = repository_git_dir.resolve(strict=True)
    live_index = git_dir_real / "index"
    if live_index.exists() and os.path.samefile(index_real, live_index):
        raise RuntimeError("staging smoke source index must not use the live repository index")
    metadata = root / ".git"
    if metadata.exists() or metadata.is_symlink():
        raise RuntimeError("staging smoke source snapshot already contains Git metadata")
    metadata.mkdir(mode=0o700)
    metadata_index = metadata / "index"
    fd = os.open(metadata_index, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    try:
        with index_real.open("rb") as source, os.fdopen(fd, "wb") as output:
            shutil.copyfileobj(source, output)
            output.flush()
            os.fsync(output.fileno())
            os.fchmod(output.fileno(), 0o444)
    except Exception:
        metadata_index.unlink(missing_ok=True)
        raise
    if digest(metadata_index) != digest(index_real):
        raise RuntimeError("staging smoke source Git metadata differs from its checked index")
    metadata.chmod(0o555)
    _verify_smoke_source_git_metadata(root, index_real, str(git_dir_real))
    return metadata_index


def _verify_smoke_source_git_metadata(source_root: Path, index_path: Path, git_dir: str) -> None:
    root = Path(source_root)
    index = Path(index_path)
    metadata = root / ".git"
    metadata_index = metadata / "index"
    try:
        metadata_info = metadata.lstat()
        metadata_index_info = metadata_index.lstat()
        index_info = index.lstat()
        git_dir_info = Path(git_dir).lstat()
    except OSError as exc:
        raise RuntimeError("staging smoke source Git metadata is unavailable") from exc
    root_real = root.resolve(strict=True)
    index_real = index.resolve(strict=True)
    if (
        not stat.S_ISDIR(metadata_info.st_mode)
        or metadata.is_symlink()
        or metadata_info.st_uid != os.geteuid()
        or stat.S_IMODE(metadata_info.st_mode) & 0o022
        or not stat.S_ISREG(metadata_index_info.st_mode)
        or metadata_index.is_symlink()
        or metadata_index_info.st_uid != os.geteuid()
        or stat.S_IMODE(metadata_index_info.st_mode) != 0o444
        or not stat.S_ISREG(index_info.st_mode)
        or index.is_symlink()
        or index_info.st_uid != os.geteuid()
        or stat.S_IMODE(index_info.st_mode) != 0o444
        or index_real.is_relative_to(root_real)
        or not stat.S_ISDIR(git_dir_info.st_mode)
        or Path(git_dir).is_symlink()
    ):
        raise RuntimeError("staging smoke source Git metadata is unsafe")
    live_index = Path(git_dir).resolve(strict=True) / "index"
    if live_index.exists() and os.path.samefile(index, live_index):
        raise RuntimeError("staging smoke source index must not use the live repository index")


def _smoke_source_index(source_sha: str, source_root: Path, scratch: Path, git_dir: str,
                        source_repository: Path | None = None) -> Path:
    source_repository = source_repository or SMOKE_SOURCE_REPOSITORY
    index_path = scratch / "source.index"
    result = subprocess.run(
        (
            "git", "-c", f"safe.directory={source_repository}",
            f"--git-dir={git_dir}", f"--work-tree={source_root}", "read-tree",
            f"--index-output={index_path}", f"{source_sha}^{{tree}}",
        ),
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        timeout=30,
    )
    if result.returncode != 0 or index_path.is_symlink() or not index_path.is_file():
        raise RuntimeError("staging smoke source index could not be prepared")
    index_path.chmod(0o444)
    return index_path


def _verify_smoke_source_snapshot(source_root: Path, git_dir: str, index_path: Path,
                                  source_repository: Path | None = None) -> None:
    source_repository = source_repository or SMOKE_SOURCE_REPOSITORY
    environment = {
        **os.environ,
        "GIT_DIR": git_dir,
        "GIT_WORK_TREE": str(source_root),
        "GIT_INDEX_FILE": str(index_path),
        "GIT_OPTIONAL_LOCKS": "0",
        "GIT_CONFIG_COUNT": "1",
        "GIT_CONFIG_KEY_0": "safe.directory",
        "GIT_CONFIG_VALUE_0": str(source_repository),
    }
    result = subprocess.run(
        (
            "git", "-c", f"safe.directory={source_repository}",
            "--git-dir", git_dir, "--work-tree", str(source_root), "diff", "--quiet", "--",
        ),
        env=environment,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.PIPE,
        text=True,
        timeout=30,
    )
    if result.returncode != 0:
        raise RuntimeError("staging smoke source snapshot no longer matches its checked commit")
    index = Path(index_path)
    index_info = index.lstat()
    if not stat.S_ISREG(index_info.st_mode) or index.is_symlink() or index_info.st_uid != os.geteuid():
        raise RuntimeError("staging smoke source index became unsafe during snapshot verification")
    index.chmod(0o444)


def _run_unprivileged_smoke_command(
    command: Sequence[str],
    source_root: Path,
    environment: dict[str, str],
    *,
    timeout_seconds: int,
    required_marker: str | None = None,
    label: str,
) -> str:
    args = [
        RUNUSER, "-u", "ubuntu", "--", "/usr/bin/env", "-i",
        *(f"{key}={value}" for key, value in sorted(environment.items())),
        *command,
    ]
    with tempfile.TemporaryFile() as stdout, tempfile.TemporaryFile() as stderr:
        try:
            process = subprocess.Popen(
                args,
                cwd=source_root,
                stdin=subprocess.DEVNULL,
                stdout=stdout,
                stderr=stderr,
                env={"PATH": HOST_CONTRACT_PATH},
                start_new_session=True,
            )
            returncode = process.wait(timeout=timeout_seconds)
        except subprocess.TimeoutExpired as exc:
            if "process" in locals() and process.poll() is None:
                try:
                    os.killpg(process.pid, signal.SIGTERM)
                    process.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    os.killpg(process.pid, signal.SIGKILL)
                    process.wait()
                except ProcessLookupError:
                    pass
            if "process" in locals():
                try:
                    _stop_smoke_process_group(process.pid)
                except ProcessLookupError:
                    pass
            diagnostic = _write_smoke_diagnostic(
                label, source_root, command, environment, "timeout", stdout, stderr,
            )
            detail = f"; diagnostic={diagnostic}" if diagnostic else "; diagnostic unavailable"
            raise RuntimeError(f"{label} timed out; child process group was stopped{detail}") from exc
        except OSError as exc:
            diagnostic = _write_smoke_diagnostic(
                label, source_root, command, environment, "not-started", stdout, stderr,
            )
            detail = f"; diagnostic={diagnostic}" if diagnostic else "; diagnostic unavailable"
            raise RuntimeError(f"{label} could not start{detail}") from exc
        orphaned = _stop_smoke_process_group(process.pid)
        output = _read_smoke_stream(stdout, 2_000_000)
        if returncode != 0:
            diagnostic = _write_smoke_diagnostic(
                label, source_root, command, environment, returncode, stdout, stderr,
            )
            detail = f"; diagnostic={diagnostic}" if diagnostic else "; diagnostic unavailable"
            raise RuntimeError(f"{label} failed with exit status {returncode}{detail}")
        if orphaned:
            diagnostic = _write_smoke_diagnostic(
                label, source_root, command, environment, "orphan-processes-stopped", stdout, stderr,
            )
            detail = f"; diagnostic={diagnostic}" if diagnostic else "; diagnostic unavailable"
            raise RuntimeError(f"{label} left child processes; the process group was stopped{detail}")
        if required_marker is not None and required_marker not in output:
            diagnostic = _write_smoke_diagnostic(
                label, source_root, command, environment, "missing-pass-marker", stdout, stderr,
            )
            detail = f"; diagnostic={diagnostic}" if diagnostic else "; diagnostic unavailable"
            raise RuntimeError(f"{label} did not report its required pass marker{detail}")
        return output


def _read_smoke_stream(stream, limit: int) -> str:
    stream.seek(0)
    return stream.read(limit).decode("utf-8", errors="replace")


def _redact_smoke_output(value: str, environment: dict[str, str]) -> str:
    result = value
    for name, secret in environment.items():
        upper = name.upper()
        if secret and any(marker in upper for marker in ("DATABASE_URL", "PASSWORD", "SECRET", "PRIVATE", "TOKEN", "API_KEY")):
            result = result.replace(secret, "[REDACTED]")
    result = re.sub(r"(?<=://)[^/@\s:]+:[^/@\s]+@", "[REDACTED]@", result)
    return result


def _write_smoke_diagnostic(
    label: str,
    source_root: Path,
    command: Sequence[str],
    environment: dict[str, str],
    result: int | str,
    stdout,
    stderr,
) -> Path | None:
    """Keep bounded, redacted failure output in a private root-only local file."""
    if os.geteuid() != 0:
        return None
    try:
        SMOKE_DIAGNOSTIC_ROOT.mkdir(mode=0o700, parents=True, exist_ok=True)
        info = SMOKE_DIAGNOSTIC_ROOT.lstat()
        if (
            not stat.S_ISDIR(info.st_mode)
            or info.st_uid != 0
            or info.st_gid != 0
            or stat.S_IMODE(info.st_mode) != 0o700
        ):
            return None
        key = re.sub(r"[^a-z0-9-]+", "-", label.lower()).strip("-")[:48] or "smoke"
        source_sha = environment.get("AICRM_DOMESTIC_RELEASE_SOURCE_SHA", "unknown")
        if not SHA.fullmatch(source_sha):
            source_sha = "unknown"
        path = SMOKE_DIAGNOSTIC_ROOT / f"{source_sha}-{key}-{time.time_ns()}.log"
        safe_stdout = _redact_smoke_output(_read_smoke_stream(stdout, 2_000_000), environment)
        safe_stderr = _redact_smoke_output(_read_smoke_stream(stderr, 512_000), environment)
        content = (
            f"label={label}\nresult={result}\ncommand={command[0] if command else '<empty>'}\n"
            f"working_directory={source_root}\n\nstdout:\n{safe_stdout}\n\nstderr:\n{safe_stderr}\n"
        ).encode("utf-8", errors="replace")
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_NOFOLLOW", 0), 0o600)
        try:
            with os.fdopen(fd, "wb") as output:
                output.write(content)
                output.flush()
                os.fsync(output.fileno())
        except Exception:
            path.unlink(missing_ok=True)
            raise
        return path
    except (OSError, RuntimeError, ValueError):
        return None


def _run_installed_smoke_test(
    source_root: Path,
    environment: dict[str, str],
    git_dir: str,
    index_path: Path,
    node_path: str,
    smoke_input_path: Path,
    source_repository: Path | None = None,
) -> None:
    source_repository = source_repository or SMOKE_SOURCE_REPOSITORY
    _verify_smoke_source_git_metadata(source_root, index_path, git_dir)
    metadata_index = Path(source_root) / ".git/index"
    metadata_index_digest = digest(metadata_index)
    source_root_real = Path(source_root).resolve(strict=True)
    git_dir_real = Path(git_dir).resolve(strict=True)
    index_real = Path(index_path).resolve(strict=True)
    source_index_environment = {
        "HOME": environment["HOME"],
        "PATH": environment["PATH"],
        "TMPDIR": environment["TMPDIR"],
        "GIT_DIR": str(git_dir_real),
        "GIT_WORK_TREE": str(source_root_real),
        "GIT_INDEX_FILE": str(index_real),
        "GIT_OPTIONAL_LOCKS": "0",
        "GIT_CONFIG_COUNT": "1",
        "GIT_CONFIG_KEY_0": "safe.directory",
        "GIT_CONFIG_VALUE_0": str(source_repository),
    }
    _run_unprivileged_smoke_command(
        (node_path, str(source_root / "scripts/prepare-donor-source-views.mjs"), "--root", str(source_root)),
        source_root,
        source_index_environment,
        timeout_seconds=120,
        label="staging smoke source preparation",
    )
    _verify_smoke_source_snapshot(source_root, git_dir, index_path, source_repository)
    _verify_smoke_source_git_metadata(source_root, index_path, git_dir)
    if digest(metadata_index) != metadata_index_digest:
        raise RuntimeError("staging smoke source Git metadata changed during preparation")
    test_environment = {**environment, "GOFLAGS": "-buildvcs=false"}
    _run_unprivileged_smoke_command(
        (
            str(SMOKE_GO), "test", "-p=1", "-count=1", "-v", "-run",
            f"^{SMOKE_TEST_NAME}$", "./cmd/aicrm", "-args",
            f"-domestic-release-smoke-input={smoke_input_path}",
        ),
        source_root,
        test_environment,
        timeout_seconds=SMOKE_TEST_TIMEOUT_SECONDS,
        required_marker=SMOKE_TEST_MARKER,
        label="installed staging smoke",
    )
    _verify_smoke_source_snapshot(source_root, git_dir, index_path, source_repository)


def _stop_smoke_process_group(process_group: int) -> bool:
    """Return whether any process remained after the test runner exited."""
    try:
        os.killpg(process_group, 0)
    except ProcessLookupError:
        return False
    os.killpg(process_group, signal.SIGTERM)
    deadline = time.monotonic() + 3
    while time.monotonic() < deadline:
        try:
            os.killpg(process_group, 0)
        except ProcessLookupError:
            return True
        time.sleep(0.1)
    try:
        os.killpg(process_group, signal.SIGKILL)
    except ProcessLookupError:
        return True
    deadline = time.monotonic() + 3
    while time.monotonic() < deadline:
        try:
            os.killpg(process_group, 0)
        except ProcessLookupError:
            return True
        time.sleep(0.1)
    raise RuntimeError("installed staging smoke child process group could not be stopped")


def run_staging_smoke(
    source_sha: str,
    expected_sha: str,
    expected_manifest_sha256: str,
    expected_helper_sha256: str,
    *,
    source_ref: str | None = None,
    source_repository: Path | None = None,
) -> dict:
    """Run the reviewed smoke fixture as ubuntu against the immutable installed API binary."""
    helper_sha256 = verify_helper_digest(expected_helper_sha256)
    if require_host_role("staging") != "staging":
        raise RuntimeError("installed behavior smoke is staging-only")
    host_contract = check_host_contract()
    if host_contract.get("host_role") != "staging" or host_contract.get("postgres_major") != 16 or host_contract.get("database_connection") != "verified":
        raise RuntimeError("staging host contract is incomplete for installed behavior smoke")
    source_repository = source_repository or SMOKE_SOURCE_REPOSITORY
    tree_sha = _source_commit_tree(source_sha, source_ref=source_ref, source_repository=source_repository)
    database_url = _staging_database_url()
    binary, binary_sha256 = _installed_staging_smoke_identity(expected_sha, expected_manifest_sha256)
    if LOCK.is_symlink() or (LOCK.exists() and not stat.S_ISREG(LOCK.lstat().st_mode)):
        raise RuntimeError("shared install lock is unsafe")
    SMOKE_SNAPSHOT_ROOT.mkdir(mode=0o755, parents=True, exist_ok=True)
    snapshot_info = SMOKE_SNAPSHOT_ROOT.lstat()
    if not stat.S_ISDIR(snapshot_info.st_mode) or snapshot_info.st_uid != 0 or snapshot_info.st_gid != 0 or snapshot_info.st_mode & 0o022:
        raise RuntimeError("fixed staging smoke snapshot root is unsafe")
    test_started = time.monotonic()
    with LOCK.open("a+") as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError as exc:
            raise RuntimeError("another release install is holding the shared lock") from exc
        # Recheck release identity and services after the lock closes the race
        # between preflight and installing another immutable release.
        binary, binary_sha256 = _installed_staging_smoke_identity(expected_sha, expected_manifest_sha256)
        with tempfile.TemporaryDirectory(prefix=f"{source_sha}-", dir=SMOKE_SNAPSHOT_ROOT) as scratch_name:
            scratch = Path(scratch_name)
            scratch.chmod(0o755)
            source_root = scratch / "source"
            tree_sha = _archive_smoke_source(source_sha, scratch, source_ref=source_ref,
                                             source_repository=source_repository)
            if source_root.is_symlink() or not source_root.is_dir():
                raise RuntimeError("fixed staging smoke source snapshot is unavailable")
            ubuntu = pwd.getpwnam("ubuntu")
            git_dir = _source_git_dir(source_repository)
            source_index = _smoke_source_index(source_sha, source_root, scratch, git_dir, source_repository)
            _chown_smoke_source_snapshot(source_root, ubuntu.pw_uid, ubuntu.pw_gid)
            _attach_smoke_source_git_metadata(source_root, source_index, git_dir)
            runtime_tmp = scratch / "runtime-tmp"
            runtime_tmp.mkdir(mode=0o700)
            os.chown(runtime_tmp, ubuntu.pw_uid, ubuntu.pw_gid)
            for cache in (SMOKE_GOCACHE, SMOKE_GOMODCACHE):
                if cache.is_symlink() or not cache.is_dir():
                    raise RuntimeError("offline Go smoke cache is unavailable")
                _staging_smoke_user_can("-w", cache)
            _require_host_tool(str(SMOKE_GO), "fixed Go toolchain")
            _require_host_tool(str(SMOKE_NODE), "fixed Node.js runtime")
            smoke_input_path = scratch / "installed-smoke-input.json"
            _write_staging_smoke_inputs(
                smoke_input_path,
                database_url=database_url,
                source_sha=source_sha,
                installed_sha=expected_sha,
                installed_binary=binary,
                installed_binary_sha256=binary_sha256,
                uid=ubuntu.pw_uid,
                gid=ubuntu.pw_gid,
            )
            environment = staging_smoke_test_environment(runtime_tmp)
            _run_installed_smoke_test(
                source_root, environment, git_dir, source_index, str(SMOKE_NODE), smoke_input_path,
                source_repository,
            )
        final_binary, final_binary_sha256 = _installed_staging_smoke_identity(expected_sha, expected_manifest_sha256)
        if final_binary != binary or final_binary_sha256 != binary_sha256:
            raise RuntimeError("staging smoke installed binary changed during the contract")
    return {
        "status": "passed",
        "contract": SMOKE_CONTRACT,
        "test_name": SMOKE_TEST_NAME,
        "test_marker": SMOKE_TEST_MARKER,
        "stage_role": "staging",
        "source_sha": source_sha,
        "source_tree": tree_sha,
        "installed_sha": expected_sha,
        "manifest_sha256": expected_manifest_sha256,
        "installed_binary_sha256": binary_sha256,
        "helper_sha256": helper_sha256,
        "test_duration_seconds": round(time.monotonic() - test_started, 1),
        "go_toolchain": str(SMOKE_GO),
        "go_package_parallelism": 1,
        "go_max_procs": int(SMOKE_GOMAXPROCS),
        "verified_at_utc": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
    }


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
            units = sorted({path.name for path in (release / "deploy").glob("aicrm*.service") if path.name not in {"aicrm-domestic-release.service", "aicrm-domestic-main-release.service"}} | {path.name for path in (release / "deploy").glob("aicrm*.timer") if path.name not in {"aicrm-domestic-release.timer", "aicrm-domestic-main-release.timer"}})
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


def _valid_sha(value: str, label: str) -> str:
    if not isinstance(value, str) or not SHA.fullmatch(value):
        raise ValueError(f"invalid {label}")
    return value


def _valid_digest(value: str, label: str) -> str:
    if not isinstance(value, str) or not FILE_SHA.fullmatch(value):
        raise ValueError(f"invalid {label}")
    return value


def _assert_root_directory(path: Path, *, create: bool = False, mode: int = 0o700, private: bool = False) -> None:
    if path.is_symlink():
        raise RuntimeError("fixed domestic release directory is unsafe")
    if not path.exists():
        if not create:
            raise RuntimeError("fixed domestic release directory is missing")
        path.mkdir(mode=mode, parents=True, exist_ok=False)
        os.chown(path, 0, 0)
        os.chmod(path, mode)
    info = path.lstat()
    if (
        not stat.S_ISDIR(info.st_mode)
        or info.st_uid != 0
        or info.st_gid != 0
        or stat.S_IMODE(info.st_mode) & 0o022
        or (private and stat.S_IMODE(info.st_mode) != 0o700)
    ):
        raise RuntimeError("fixed domestic release directory is unsafe")


def _assert_root_file(path: Path, *, exact_mode: int | None = None) -> os.stat_result:
    try:
        info = path.lstat()
    except OSError as exc:
        raise RuntimeError("fixed domestic release file is missing or unsafe") from exc
    if (
        not stat.S_ISREG(info.st_mode)
        or info.st_uid != 0
        or info.st_gid != 0
        or (exact_mode is not None and stat.S_IMODE(info.st_mode) != exact_mode)
        or (exact_mode is None and stat.S_IMODE(info.st_mode) & 0o022)
        or path.is_symlink()
    ):
        raise RuntimeError("fixed domestic release file is unsafe")
    return info


def _git_checked(repository: Path, *args: str, timeout: int = 60) -> str:
    environment = {
        "PATH": "/usr/bin:/bin",
        "HOME": "/root",
        "GIT_CONFIG_NOSYSTEM": "1",
        "GIT_CONFIG_GLOBAL": "/dev/null",
        "GIT_TERMINAL_PROMPT": "0",
        "GIT_OPTIONAL_LOCKS": "0",
    }
    try:
        result = subprocess.run(
            ["git", "-C", str(repository), *args],
            env=environment,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            timeout=timeout,
        )
    except (OSError, subprocess.SubprocessError) as exc:
        raise RuntimeError("source bundle validation failed") from exc
    if result.returncode != 0:
        raise RuntimeError("source bundle validation failed")
    return result.stdout.strip()


def _source_bundle_receipt_path(source_sha: str) -> Path:
    return SOURCE_BACKUPS / f"{source_sha}{DOMESTIC_SOURCE_RECEIPT_SUFFIX}"


def _bundle_heads(repository: Path, bundle: Path) -> dict[str, str]:
    raw = _git_checked(repository, "bundle", "list-heads", str(bundle))
    heads: dict[str, str] = {}
    for line in raw.splitlines():
        pieces = line.split()
        if len(pieces) != 2 or not SHA.fullmatch(pieces[0]) or pieces[1] in heads:
            raise RuntimeError("source bundle refs are invalid")
        heads[pieces[1]] = pieces[0]
    return heads


def _verify_source_bundle_file(
    bundle: Path,
    *,
    source_sha: str,
    source_tree: str,
    previous_main_sha: str,
    bundle_sha256: str,
    allow_baseline_transition: bool = False,
    installed_app_sha: str | None = None,
    installed_app_tree: str | None = None,
) -> dict:
    source_sha = _valid_sha(source_sha, "source SHA")
    source_tree = _valid_sha(source_tree, "source tree")
    previous_main_sha = _valid_sha(previous_main_sha, "previous main SHA")
    bundle_sha256 = _valid_digest(bundle_sha256, "source bundle SHA256")
    info = _assert_root_file(bundle, exact_mode=0o400)
    if info.st_size <= 0 or digest(bundle) != bundle_sha256:
        raise ValueError("source bundle digest mismatch")
    if source_sha == previous_main_sha and not allow_baseline_transition:
        raise ValueError("source candidate must advance domestic main")
    _assert_root_directory(ROOT)
    _assert_root_directory(SOURCE_BACKUPS, private=True)
    with tempfile.TemporaryDirectory(prefix=f".verify-{source_sha}-", dir=SOURCE_BACKUPS) as temporary:
        repository = Path(temporary) / "repo.git"
        bundle_path = Path(temporary) / "source.bundle"
        shutil.copyfile(bundle, bundle_path)
        bundle_path.chmod(0o400)
        _git_checked(Path(temporary), "init", "--bare", "--quiet", str(repository))
        _git_checked(repository, "bundle", "verify", str(bundle_path))
        heads = _bundle_heads(repository, bundle_path)
        candidate_ref = DOMESTIC_SOURCE_REFS[1].format(sha=source_sha)
        expected_refs = {DOMESTIC_SOURCE_REFS[0], candidate_ref}
        if set(heads) != expected_refs:
            raise ValueError("source bundle must contain exactly the approved main and candidate refs")
        if allow_baseline_transition:
            if heads[DOMESTIC_SOURCE_REFS[0]] != source_sha or heads[candidate_ref] != source_sha:
                raise ValueError("baseline source bundle refs must both point to the source commit")
        elif heads[DOMESTIC_SOURCE_REFS[0]] != previous_main_sha or heads[candidate_ref] != source_sha:
            raise ValueError("source bundle refs do not match the requested identities")
        _git_checked(
            repository,
            "fetch", "--no-tags", str(bundle_path),
            f"{DOMESTIC_SOURCE_REFS[0]}:{DOMESTIC_SOURCE_REFS[0]}",
            f"{candidate_ref}:{candidate_ref}",
        )
        _git_checked(repository, "fsck", "--full", "--strict", "--no-reflogs")
        restored_tree = _git_checked(repository, "rev-parse", f"{source_sha}^{{tree}}")
        if restored_tree != source_tree:
            raise ValueError("source bundle tree mismatch")
        first_parent = _git_checked(repository, "rev-list", "--first-parent", source_sha).splitlines()
        if not first_parent or first_parent[0] != source_sha or previous_main_sha not in first_parent:
            raise ValueError("source candidate is not on the expected first-parent main history")
        if installed_app_sha is not None:
            installed_app_sha = _valid_sha(installed_app_sha, "installed app SHA")
            if installed_app_sha not in first_parent:
                raise ValueError("installed app source is not an ancestor of domestic main")
            restored_app_tree = _git_checked(repository, "rev-parse", f"{installed_app_sha}^{{tree}}")
            if installed_app_tree is None or restored_app_tree != _valid_sha(installed_app_tree, "installed app tree"):
                raise ValueError("installed app tree does not match its source commit")
        if allow_baseline_transition and previous_main_sha == source_sha and first_parent != [source_sha]:
            raise ValueError("baseline source bundle self identity is invalid")
        return {
            "source_sha": source_sha,
            "source_tree": restored_tree,
            "previous_main_sha": previous_main_sha,
            "bundle_sha256": bundle_sha256,
            "self_contained": True,
            "bundle_bytes": info.st_size,
            "first_parent": first_parent,
        }


def _copy_incoming_bundle(source_bundle: Path, source_sha: str, expected_digest: str) -> tuple[Path, int, int]:
    expected_path = DOMESTIC_INCOMING / f"{source_sha}.bundle"
    if source_bundle != expected_path:
        raise ValueError("source bundle path is outside the fixed incoming directory")
    _assert_root_directory(ROOT)
    _assert_root_directory(DOMESTIC_INCOMING, private=True)
    _assert_root_directory(SOURCE_BACKUPS, create=True, private=True)
    try:
        descriptor = os.open(source_bundle, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
    except OSError as exc:
        raise RuntimeError("incoming source bundle is missing or unsafe") from exc
    try:
        source_info = os.fstat(descriptor)
        if not stat.S_ISREG(source_info.st_mode) or source_info.st_size <= 0:
            raise RuntimeError("incoming source bundle is missing or unsafe")
        available_bytes = shutil.disk_usage(SOURCE_BACKUPS).free
        required = max(DOMESTIC_SOURCE_BUNDLE_MIN_FREE_BYTES, 2 * source_info.st_size)
        if available_bytes < required:
            raise RuntimeError("insufficient free disk for verified source backup")
        temporary_fd, temporary_name = tempfile.mkstemp(prefix=f".{source_sha}.", suffix=".bundle.tmp", dir=SOURCE_BACKUPS)
        temporary = Path(temporary_name)
        hasher = hashlib.sha256()
        copied_bytes = 0
        try:
            with os.fdopen(descriptor, "rb", closefd=False) as source, os.fdopen(temporary_fd, "wb") as target:
                for block in iter(lambda: source.read(1024 * 1024), b""):
                    target.write(block)
                    hasher.update(block)
                    copied_bytes += len(block)
                target.flush()
                os.fsync(target.fileno())
            os.chown(temporary, 0, 0)
            os.chmod(temporary, 0o400)
            if copied_bytes != source_info.st_size or hasher.hexdigest() != expected_digest:
                raise ValueError("source bundle digest mismatch")
        except Exception:
            temporary.unlink(missing_ok=True)
            raise
        return temporary, source_info.st_size, available_bytes
    finally:
        os.close(descriptor)


def _atomic_root_receipt(path: Path, payload: dict, *, create_only: bool) -> None:
    parent = path.parent
    _assert_root_directory(parent, create=True, private=True)
    encoded = (json.dumps(payload, sort_keys=True, separators=(",", ":")) + "\n").encode("utf-8")
    descriptor, name = tempfile.mkstemp(prefix=f".{path.name}.", suffix=".tmp", dir=parent)
    temporary = Path(name)
    try:
        with os.fdopen(descriptor, "wb") as output:
            output.write(encoded)
            output.flush()
            os.fchown(output.fileno(), 0, 0)
            os.fchmod(output.fileno(), 0o600)
            os.fsync(output.fileno())
        if create_only:
            os.link(temporary, path, follow_symlinks=False)
            temporary.unlink()
        else:
            if path.is_symlink() or (path.exists() and not stat.S_ISREG(path.lstat().st_mode)):
                raise RuntimeError("fixed domestic main state file is unsafe")
            if path.exists():
                _assert_root_file(path, exact_mode=0o600)
            os.replace(temporary, path)
        directory_fd = os.open(parent, os.O_RDONLY | getattr(os, "O_DIRECTORY", 0))
        try:
            os.fsync(directory_fd)
        finally:
            os.close(directory_fd)
    finally:
        temporary.unlink(missing_ok=True)


def save_domestic_source_bundle(
    source_bundle: Path,
    *,
    source_sha: str,
    source_tree: str,
    previous_main_sha: str,
    expected_bundle_sha256: str,
    allow_baseline_transition: bool = False,
) -> dict:
    require_host_role("production")
    source_sha = _valid_sha(source_sha, "source SHA")
    source_tree = _valid_sha(source_tree, "source tree")
    previous_main_sha = _valid_sha(previous_main_sha, "previous main SHA")
    expected_bundle_sha256 = _valid_digest(expected_bundle_sha256, "source bundle SHA256")
    _assert_root_directory(ROOT)
    _assert_root_directory(SOURCE_BACKUPS, create=True, private=True)
    target = SOURCE_BACKUPS / f"{source_sha}.bundle"
    receipt_path = _source_bundle_receipt_path(source_sha)
    if target.exists() and not target.is_symlink() and not receipt_path.exists() and not receipt_path.is_symlink():
        verified = _verify_source_bundle_file(
            target, source_sha=source_sha, source_tree=source_tree,
            previous_main_sha=previous_main_sha,
            bundle_sha256=expected_bundle_sha256,
            allow_baseline_transition=allow_baseline_transition,
        )
        _atomic_root_receipt(receipt_path, {
            "schema_version": 1,
            "source_sha": source_sha,
            "source_tree": source_tree,
            "previous_main_sha": previous_main_sha,
            "bundle_sha256": expected_bundle_sha256,
            "self_contained": True,
            "baseline_transition": allow_baseline_transition,
            "bundle_bytes": verified["bundle_bytes"],
            "verified_at_utc": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        }, create_only=True)
        return {
            "status": "verified",
            "source_sha": source_sha,
            "source_tree": source_tree,
            "previous_main_sha": previous_main_sha,
            "bundle_sha256": expected_bundle_sha256,
            "self_contained": True,
            "bundle_bytes": verified["bundle_bytes"],
            "available_bytes": shutil.disk_usage(SOURCE_BACKUPS).free,
        }
    if target.exists() or target.is_symlink() or receipt_path.exists() or receipt_path.is_symlink():
        return verify_domestic_source_backup(
            source_sha=source_sha, source_tree=source_tree,
            previous_main_sha=previous_main_sha,
            expected_bundle_sha256=expected_bundle_sha256,
            allow_baseline_transition=allow_baseline_transition,
        )
    temporary, bundle_bytes, available_bytes = _copy_incoming_bundle(
        source_bundle, source_sha, expected_bundle_sha256,
    )
    try:
        validated = _verify_source_bundle_file(
            temporary, source_sha=source_sha, source_tree=source_tree,
            previous_main_sha=previous_main_sha,
            bundle_sha256=expected_bundle_sha256,
            allow_baseline_transition=allow_baseline_transition,
        )
        try:
            os.link(temporary, target, follow_symlinks=False)
        except FileExistsError as exc:
            raise RuntimeError("source backup already exists; inspect before retry") from exc
        os.unlink(temporary)
        receipt = {
            "schema_version": 1,
            "source_sha": source_sha,
            "source_tree": source_tree,
            "previous_main_sha": previous_main_sha,
            "bundle_sha256": expected_bundle_sha256,
            "self_contained": True,
            "baseline_transition": allow_baseline_transition,
            "bundle_bytes": bundle_bytes,
            "verified_at_utc": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        }
        _atomic_root_receipt(receipt_path, receipt, create_only=True)
        return {
            "status": "verified",
            "source_sha": source_sha,
            "source_tree": source_tree,
            "previous_main_sha": previous_main_sha,
            "bundle_sha256": expected_bundle_sha256,
            "self_contained": True,
            "bundle_bytes": bundle_bytes,
            "available_bytes": available_bytes,
        }
    finally:
        temporary.unlink(missing_ok=True)


def verify_domestic_source_backup(
    *,
    source_sha: str,
    source_tree: str,
    previous_main_sha: str,
    expected_bundle_sha256: str,
    allow_baseline_transition: bool = False,
    installed_app_sha: str | None = None,
    installed_app_tree: str | None = None,
) -> dict:
    require_host_role("production")
    source_sha = _valid_sha(source_sha, "source SHA")
    source_tree = _valid_sha(source_tree, "source tree")
    previous_main_sha = _valid_sha(previous_main_sha, "previous main SHA")
    expected_bundle_sha256 = _valid_digest(expected_bundle_sha256, "source bundle SHA256")
    _assert_root_directory(ROOT)
    _assert_root_directory(SOURCE_BACKUPS, private=True)
    bundle = SOURCE_BACKUPS / f"{source_sha}.bundle"
    receipt_path = _source_bundle_receipt_path(source_sha)
    _assert_root_file(receipt_path, exact_mode=0o600)
    try:
        receipt = json.loads(receipt_path.read_bytes())
    except (OSError, json.JSONDecodeError) as exc:
        raise RuntimeError("source backup receipt is invalid") from exc
    if not isinstance(receipt, dict) or any(
        receipt.get(key) != value for key, value in {
            "schema_version": 1,
            "source_sha": source_sha,
            "source_tree": source_tree,
            "previous_main_sha": previous_main_sha,
            "bundle_sha256": expected_bundle_sha256,
            "self_contained": True,
            "baseline_transition": allow_baseline_transition,
        }.items()
    ):
        raise ValueError("source backup receipt identity mismatch")
    verified = _verify_source_bundle_file(
        bundle, source_sha=source_sha, source_tree=source_tree,
        previous_main_sha=previous_main_sha,
        bundle_sha256=expected_bundle_sha256,
        allow_baseline_transition=allow_baseline_transition,
        installed_app_sha=installed_app_sha,
        installed_app_tree=installed_app_tree,
    )
    if receipt.get("bundle_bytes") != verified["bundle_bytes"] or not isinstance(receipt.get("verified_at_utc"), str):
        raise ValueError("source backup receipt size or timestamp mismatch")
    return {
        "status": "verified",
        "source_sha": source_sha,
        "source_tree": source_tree,
        "previous_main_sha": previous_main_sha,
        "bundle_sha256": expected_bundle_sha256,
        "self_contained": True,
        "bundle_bytes": verified["bundle_bytes"],
        "available_bytes": shutil.disk_usage(SOURCE_BACKUPS).free,
        "_first_parent": verified["first_parent"],
    }


@contextmanager
def _production_release_lock():
    _assert_root_directory(ROOT)
    if LOCK.is_symlink() or (LOCK.exists() and not stat.S_ISREG(LOCK.lstat().st_mode)):
        raise RuntimeError("shared production install lock is unsafe")
    descriptor = os.open(LOCK, os.O_RDWR | os.O_CREAT | getattr(os, "O_NOFOLLOW", 0), 0o600)
    try:
        info = os.fstat(descriptor)
        if not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_gid != 0 or stat.S_IMODE(info.st_mode) & 0o022:
            raise RuntimeError("shared production install lock is unsafe")
        try:
            fcntl.flock(descriptor, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError as exc:
            raise RuntimeError("production release is currently locked") from exc
        yield
    finally:
        os.close(descriptor)


def _read_install_receipt(installed_app_sha: str) -> tuple[dict, bytes, str]:
    installed_app_sha = _valid_sha(installed_app_sha, "installed app SHA")
    path = RECEIPTS / f"{installed_app_sha}.json"
    _assert_root_directory(RECEIPTS, private=True)
    _assert_root_file(path, exact_mode=0o600)
    content = path.read_bytes()
    try:
        receipt = json.loads(content)
    except json.JSONDecodeError as exc:
        raise RuntimeError("production install receipt is invalid") from exc
    if not isinstance(receipt, dict):
        raise RuntimeError("production install receipt is invalid")
    return receipt, content, hashlib.sha256(content).hexdigest()


def _verify_installed_app(
    installed_app_sha: str,
    installed_app_tree: str,
    installed_manifest_sha256: str,
    expected_receipt_sha256: str | None = None,
) -> tuple[dict, str]:
    installed_app_sha = _valid_sha(installed_app_sha, "installed app SHA")
    installed_app_tree = _valid_sha(installed_app_tree, "installed app tree")
    installed_manifest_sha256 = _valid_digest(installed_manifest_sha256, "installed manifest SHA256")
    receipt, _content, receipt_sha256 = _read_install_receipt(installed_app_sha)
    if expected_receipt_sha256 is not None and receipt_sha256 != _valid_digest(expected_receipt_sha256, "install receipt SHA256"):
        raise ValueError("production install receipt digest mismatch")
    if (
        receipt.get("schema_version") != 1
        or receipt.get("source_sha") != installed_app_sha
        or receipt.get("source_tree") != installed_app_tree
        or receipt.get("manifest_sha256") != installed_manifest_sha256
        or receipt.get("technical_status") != "installed_healthy"
    ):
        raise ValueError("production install receipt identity mismatch")
    if current_sha() != installed_app_sha:
        raise ValueError("production current release SHA mismatch")
    release = RELEASES / installed_app_sha
    if release.is_symlink() or not release.is_dir() or release.resolve(strict=True) != RELEASES.resolve(strict=True) / installed_app_sha:
        raise RuntimeError("production current release path is unsafe")
    release_env = release / "release.env"
    if release_env.is_symlink() or not release_env.is_file() or release_env.read_text() != f"AICRM_RELEASE_SHA={installed_app_sha}\n":
        raise ValueError("production release marker mismatch")
    metadata = {"source_sha": installed_app_sha, "release_files_sha256": installed_manifest_sha256}
    verify_payload_without_release_env(release, metadata)
    verify_root_owned_release(release)
    if digest(release / "release-files.sha256") != installed_manifest_sha256:
        raise ValueError("production release manifest mismatch")
    readiness(installed_app_sha)
    return receipt, receipt_sha256


def _read_main_state() -> dict:
    _assert_root_directory(DOMESTIC_MAIN, private=True)
    _assert_root_file(DOMESTIC_MAIN_STATE, exact_mode=0o600)
    try:
        state = json.loads(DOMESTIC_MAIN_STATE.read_bytes())
    except (OSError, json.JSONDecodeError) as exc:
        raise RuntimeError("domestic main state is invalid") from exc
    required = {
        "schema_version", "status", "main_sha", "main_tree", "installed_app_sha",
        "installed_app_tree", "installed_manifest_sha256", "install_receipt_sha256",
        "source_bundle_sha256", "updated_at_utc",
    }
    if not isinstance(state, dict) or set(state) != required or state.get("schema_version") != 1 or state.get("status") != "ready":
        raise RuntimeError("domestic main state is invalid")
    for field in ("main_sha", "main_tree", "installed_app_sha", "installed_app_tree"):
        _valid_sha(state[field], field)
    for field in ("installed_manifest_sha256", "install_receipt_sha256", "source_bundle_sha256"):
        _valid_digest(state[field], field)
    if not isinstance(state["updated_at_utc"], str) or not state["updated_at_utc"].endswith("Z"):
        raise RuntimeError("domestic main state timestamp is invalid")
    return state


def _build_main_cursor(
    *,
    main_sha: str,
    main_tree: str,
    installed_app_sha: str,
    installed_app_tree: str,
    installed_manifest_sha256: str,
    source_bundle_sha256: str,
    previous_main_sha: str,
    allow_baseline_transition: bool = False,
) -> tuple[dict, dict]:
    main_sha = _valid_sha(main_sha, "main SHA")
    main_tree = _valid_sha(main_tree, "main tree")
    installed_app_sha = _valid_sha(installed_app_sha, "installed app SHA")
    installed_app_tree = _valid_sha(installed_app_tree, "installed app tree")
    installed_manifest_sha256 = _valid_digest(installed_manifest_sha256, "installed manifest SHA256")
    source_bundle_sha256 = _valid_digest(source_bundle_sha256, "source bundle SHA256")
    previous_main_sha = _valid_sha(previous_main_sha, "previous main SHA")
    verified_bundle = verify_domestic_source_backup(
        source_sha=main_sha,
        source_tree=main_tree,
        previous_main_sha=previous_main_sha,
        expected_bundle_sha256=source_bundle_sha256,
        allow_baseline_transition=allow_baseline_transition,
        installed_app_sha=installed_app_sha,
        installed_app_tree=installed_app_tree,
    )
    receipt, receipt_sha256 = _verify_installed_app(
        installed_app_sha, installed_app_tree, installed_manifest_sha256,
    )
    receipt_previous = receipt.get("previous_sha")
    if receipt_previous is not None:
        receipt_previous = _valid_sha(receipt_previous, "install receipt previous SHA")
        if receipt_previous not in verified_bundle["_first_parent"]:
            raise ValueError("installed app receipt base is not an ancestor of domestic main")
    cursor = {
        "schema_version": 1,
        "status": "ready",
        "main_sha": main_sha,
        "main_tree": main_tree,
        "installed_app_sha": installed_app_sha,
        "installed_app_tree": installed_app_tree,
        "installed_manifest_sha256": installed_manifest_sha256,
        "install_receipt_sha256": receipt_sha256,
        "source_bundle_sha256": source_bundle_sha256,
        "updated_at_utc": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
    }
    return cursor, receipt


def initialize_domestic_main(
    *,
    main_sha: str,
    main_tree: str,
    installed_app_sha: str,
    installed_app_tree: str,
    installed_manifest_sha256: str,
    source_bundle_sha256: str,
) -> dict:
    require_host_role("production")
    _assert_root_directory(ROOT)
    _assert_root_directory(DOMESTIC_MAIN, create=True, private=True)
    with _production_release_lock():
        main_sha = _valid_sha(main_sha, "main SHA")
        source_receipt_path = _source_bundle_receipt_path(main_sha)
        _assert_root_file(source_receipt_path, exact_mode=0o600)
        try:
            source_receipt = json.loads(source_receipt_path.read_bytes())
        except (OSError, json.JSONDecodeError) as exc:
            raise RuntimeError("source backup receipt is invalid") from exc
        if not isinstance(source_receipt, dict) or source_receipt.get("baseline_transition") is not True:
            raise RuntimeError("initial domestic main requires a baseline-transition source backup")
        previous_main_sha = source_receipt.get("previous_main_sha")
        if not isinstance(previous_main_sha, str) or not SHA.fullmatch(previous_main_sha):
            raise RuntimeError("baseline source backup receipt is invalid")
        cursor, _receipt = _build_main_cursor(
            main_sha=main_sha, main_tree=main_tree,
            installed_app_sha=installed_app_sha, installed_app_tree=installed_app_tree,
            installed_manifest_sha256=installed_manifest_sha256,
            source_bundle_sha256=source_bundle_sha256,
            previous_main_sha=previous_main_sha,
            allow_baseline_transition=True,
        )
        if DOMESTIC_MAIN_STATE.exists() or DOMESTIC_MAIN_STATE.is_symlink():
            existing = _read_main_state()
            identity_fields = set(cursor) - {"updated_at_utc"}
            if all(existing[field] == cursor[field] for field in identity_fields):
                return existing
            raise RuntimeError("domestic main baseline already exists with a different identity")
        _atomic_root_receipt(DOMESTIC_MAIN_STATE, cursor, create_only=True)
        return cursor


def record_domestic_main(
    *,
    main_sha: str,
    main_tree: str,
    installed_app_sha: str,
    installed_app_tree: str,
    installed_manifest_sha256: str,
    source_bundle_sha256: str,
    expected_previous_main_sha: str,
) -> dict:
    require_host_role("production")
    expected_previous_main_sha = _valid_sha(expected_previous_main_sha, "expected previous main SHA")
    _assert_root_directory(ROOT)
    _assert_root_directory(DOMESTIC_MAIN, private=True)
    with _production_release_lock():
        existing = _read_main_state()
        cursor, _receipt = _build_main_cursor(
            main_sha=main_sha, main_tree=main_tree,
            installed_app_sha=installed_app_sha, installed_app_tree=installed_app_tree,
            installed_manifest_sha256=installed_manifest_sha256,
            source_bundle_sha256=source_bundle_sha256,
            previous_main_sha=expected_previous_main_sha,
        )
        identity_fields = set(cursor) - {"updated_at_utc"}
        if existing["main_sha"] == cursor["main_sha"]:
            if all(existing[field] == cursor[field] for field in identity_fields):
                return existing
            raise RuntimeError("domestic main already records a different identity for this SHA")
        if existing["main_sha"] != expected_previous_main_sha:
            raise RuntimeError("domestic main compare-and-swap base mismatch")
        if cursor["main_sha"] == expected_previous_main_sha:
            raise ValueError("record-domestic-main requires a new main SHA")
        _atomic_root_receipt(DOMESTIC_MAIN_STATE, cursor, create_only=False)
        return cursor


def read_domestic_main() -> dict:
    require_host_role("production")
    _assert_root_directory(ROOT)
    with _production_release_lock():
        cursor = _read_main_state()
        source_receipt_path = _source_bundle_receipt_path(cursor["main_sha"])
        _assert_root_file(source_receipt_path, exact_mode=0o600)
        try:
            source_receipt = json.loads(source_receipt_path.read_bytes())
        except (OSError, json.JSONDecodeError) as exc:
            raise RuntimeError("source backup receipt is invalid") from exc
        if not isinstance(source_receipt, dict):
            raise RuntimeError("source backup receipt is invalid")
        verified = _build_main_cursor(
            main_sha=cursor["main_sha"], main_tree=cursor["main_tree"],
            installed_app_sha=cursor["installed_app_sha"], installed_app_tree=cursor["installed_app_tree"],
            installed_manifest_sha256=cursor["installed_manifest_sha256"],
            source_bundle_sha256=cursor["source_bundle_sha256"],
            previous_main_sha=source_receipt.get("previous_main_sha", ""),
            allow_baseline_transition=source_receipt.get("baseline_transition") is True,
        )
        verified_identity = {key: value for key, value in verified[0].items() if key != "updated_at_utc"}
        cursor_identity = {key: value for key, value in cursor.items() if key != "updated_at_utc"}
        if verified_identity != cursor_identity:
            raise ValueError("domestic main cursor does not match verified production state")
        return {
            "status": "ready",
            "cursor": cursor,
            "install_receipt": verified[1],
            "install_receipt_sha256": cursor["install_receipt_sha256"],
        }


def main() -> None:
    p = argparse.ArgumentParser()
    source = p.add_mutually_exclusive_group(required=True)
    source.add_argument("--incoming", type=Path)
    source.add_argument("--retry-existing", action="store_true", help="reuse a checksum-verified orphan under the install lock")
    source.add_argument("--check-host-contract", action="store_true", help="read-only check of role, PostgreSQL 16, service user, paths, and systemd")
    source.add_argument("--run-staging-smoke", action="store_true", help="run the fixed installed Alipay checkout contract on staging")
    source.add_argument("--save-domestic-source-bundle", action="store_true")
    source.add_argument("--verify-domestic-source-backup", action="store_true")
    source.add_argument("--initialize-domestic-main", action="store_true")
    source.add_argument("--record-domestic-main", action="store_true")
    source.add_argument("--read-domestic-main", action="store_true")
    p.add_argument("--metadata", type=Path)
    p.add_argument("--expected-sha", help="exact source SHA bound to the metadata")
    p.add_argument("--metadata-sha256", help="SHA256 of the exact metadata file bytes")
    p.add_argument("--expected-base", help="40-char installed SHA or 'none' for empty staging")
    p.add_argument("--source-sha", help="exact checked source commit that owns the fixed staging fixture")
    p.add_argument("--expected-manifest-sha256", help="SHA256 of the installed release manifest")
    p.add_argument("--expected-helper-sha256", help="SHA256 of the exact checked Git source file being rehearsed")
    p.add_argument("--source-bundle", type=Path)
    p.add_argument("--expected-source-sha")
    p.add_argument("--expected-source-tree")
    p.add_argument("--expected-previous-main-sha")
    p.add_argument("--expected-bundle-sha256")
    p.add_argument("--allow-baseline-transition", action="store_true")
    p.add_argument("--source-tree")
    p.add_argument("--previous-main-sha")
    p.add_argument("--main-sha")
    p.add_argument("--main-tree")
    p.add_argument("--installed-app-sha")
    p.add_argument("--installed-app-tree")
    p.add_argument("--installed-manifest-sha256")
    p.add_argument("--source-bundle-sha256")
    p.add_argument("--source-ref", help="exact immutable domestic candidate ref, for domestic bare repositories")
    p.add_argument("--source-repository", type=Path, help="fixed checked source repository used by the smoke fixture")
    p.add_argument("--source-bundle", type=Path)
    p.add_argument("--expected-source-sha")
    p.add_argument("--expected-source-tree")
    p.add_argument("--expected-previous-main-sha")
    p.add_argument("--expected-bundle-sha256")
    p.add_argument("--allow-baseline-transition", action="store_true")
    p.add_argument("--source-tree")
    p.add_argument("--previous-main-sha")
    p.add_argument("--main-sha")
    p.add_argument("--main-tree")
    p.add_argument("--installed-app-sha")
    p.add_argument("--installed-app-tree")
    p.add_argument("--installed-manifest-sha256")
    p.add_argument("--source-bundle-sha256")
    args = p.parse_args()
    if os.geteuid() != 0:
        raise SystemExit("root required")
    if args.check_host_contract:
        if (
            any(value is not None for value in (args.metadata, args.expected_sha, args.metadata_sha256, args.expected_base, args.source_sha, args.expected_manifest_sha256))
            or any(value is not None for value in (args.source_bundle, args.expected_source_sha, args.expected_source_tree, args.expected_previous_main_sha, args.expected_bundle_sha256, args.source_tree, args.previous_main_sha, args.source_bundle_sha256, args.main_sha, args.main_tree, args.installed_app_sha, args.installed_app_tree, args.installed_manifest_sha256))
            or args.allow_baseline_transition
        ):
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
    if args.run_staging_smoke:
        if (
            any(value is not None for value in (args.metadata, args.metadata_sha256, args.expected_base))
            or any(value is not None for value in (args.source_bundle, args.expected_source_sha, args.expected_source_tree, args.expected_previous_main_sha, args.expected_bundle_sha256, args.source_tree, args.previous_main_sha, args.source_bundle_sha256, args.main_sha, args.main_tree, args.installed_app_sha, args.installed_app_tree, args.installed_manifest_sha256))
            or args.allow_baseline_transition
        ):
            p.error("--run-staging-smoke does not accept install metadata")
        if args.expected_sha is None or args.source_sha is None or args.expected_manifest_sha256 is None or args.expected_helper_sha256 is None:
            p.error("--run-staging-smoke requires --source-sha, --expected-sha, --expected-manifest-sha256, and --expected-helper-sha256")
        try:
            result = run_staging_smoke(
                args.source_sha, args.expected_sha, args.expected_manifest_sha256, args.expected_helper_sha256,
                source_ref=args.source_ref,
                source_repository=args.source_repository or SMOKE_SOURCE_REPOSITORY,
            )
        except (OSError, ValueError, RuntimeError, subprocess.SubprocessError) as exc:
            raise SystemExit(str(exc)) from exc
        print(json.dumps(result, sort_keys=True))
        return
    domestic_fields = (
        args.source_bundle, args.expected_source_sha, args.expected_source_tree,
        args.expected_previous_main_sha, args.expected_bundle_sha256,
        args.source_tree, args.previous_main_sha, args.source_bundle_sha256,
        args.main_sha, args.main_tree, args.installed_app_sha,
        args.installed_app_tree, args.installed_manifest_sha256,
        args.source_bundle_sha256,
    )
    domestic_mode = any((
        args.save_domestic_source_bundle, args.verify_domestic_source_backup,
        args.initialize_domestic_main, args.record_domestic_main, args.read_domestic_main,
    ))
    if domestic_mode:
        if any(value is not None for value in (args.metadata, args.expected_sha, args.metadata_sha256, args.expected_base, args.expected_manifest_sha256, args.expected_helper_sha256)) or (args.source_sha is not None and not args.verify_domestic_source_backup):
            p.error("domestic source commands do not accept installer or smoke arguments")
        try:
            if args.save_domestic_source_bundle:
                if (
                    args.source_bundle is None
                    or any(value is None for value in (args.expected_source_sha, args.expected_source_tree, args.expected_previous_main_sha, args.expected_bundle_sha256))
                    or any(value is not None for value in (args.source_sha, args.source_tree, args.previous_main_sha, args.source_bundle_sha256, args.main_sha, args.main_tree, args.installed_app_sha, args.installed_app_tree, args.installed_manifest_sha256))
                ):
                    p.error("--save-domestic-source-bundle requires --source-bundle and all expected source identity fields")
                result = save_domestic_source_bundle(
                    args.source_bundle,
                    source_sha=args.expected_source_sha,
                    source_tree=args.expected_source_tree,
                    previous_main_sha=args.expected_previous_main_sha,
                    expected_bundle_sha256=args.expected_bundle_sha256,
                    allow_baseline_transition=args.allow_baseline_transition,
                )
            elif args.verify_domestic_source_backup:
                if (
                    args.source_bundle is not None
                    or any(value is not None for value in (args.expected_source_sha, args.expected_source_tree, args.expected_previous_main_sha, args.expected_bundle_sha256))
                    or any(value is None for value in (args.source_sha, args.source_tree, args.previous_main_sha, args.source_bundle_sha256))
                    or any(value is not None for value in (args.main_sha, args.main_tree, args.installed_app_sha, args.installed_app_tree, args.installed_manifest_sha256))
                ):
                    p.error("--verify-domestic-source-backup requires source identity fields and no incoming path")
                result = verify_domestic_source_backup(
                    source_sha=args.source_sha,
                    source_tree=args.source_tree,
                    previous_main_sha=args.previous_main_sha,
                    expected_bundle_sha256=args.source_bundle_sha256,
                    allow_baseline_transition=args.allow_baseline_transition,
                )
                result.pop("_first_parent", None)
            elif args.initialize_domestic_main or args.record_domestic_main:
                if (
                    args.source_bundle is not None
                    or any(value is not None for value in (args.expected_source_sha, args.expected_source_tree, args.expected_bundle_sha256, args.source_sha, args.source_tree, args.previous_main_sha))
                    or args.allow_baseline_transition
                ):
                    p.error("domestic main record commands do not accept source-bundle validation arguments")
                required = (args.main_sha, args.main_tree, args.installed_app_sha, args.installed_app_tree, args.installed_manifest_sha256, args.source_bundle_sha256)
                if any(value is None for value in required):
                    p.error("domestic main record command requires all main, app, manifest, and bundle identity fields")
                if args.initialize_domestic_main:
                    if args.expected_previous_main_sha is not None:
                        p.error("--initialize-domestic-main does not accept --expected-previous-main-sha")
                    result = initialize_domestic_main(
                        main_sha=args.main_sha, main_tree=args.main_tree,
                        installed_app_sha=args.installed_app_sha, installed_app_tree=args.installed_app_tree,
                        installed_manifest_sha256=args.installed_manifest_sha256,
                        source_bundle_sha256=args.source_bundle_sha256,
                    )
                else:
                    if args.expected_previous_main_sha is None:
                        p.error("--record-domestic-main requires --expected-previous-main-sha")
                    result = record_domestic_main(
                        main_sha=args.main_sha, main_tree=args.main_tree,
                        installed_app_sha=args.installed_app_sha, installed_app_tree=args.installed_app_tree,
                        installed_manifest_sha256=args.installed_manifest_sha256,
                        source_bundle_sha256=args.source_bundle_sha256,
                        expected_previous_main_sha=args.expected_previous_main_sha,
                    )
            else:
                if any(value is not None for value in domestic_fields) or args.allow_baseline_transition:
                    p.error("--read-domestic-main takes no additional identity arguments")
                result = read_domestic_main()
        except (OSError, ValueError, RuntimeError, subprocess.SubprocessError) as exc:
            raise SystemExit(str(exc)) from None
        print(json.dumps(result, sort_keys=True))
        return
    if any(value is not None for value in domestic_fields) or args.allow_baseline_transition:
        p.error("domestic source arguments require a domestic source command")
    if args.expected_helper_sha256 is not None or args.source_sha is not None or args.expected_manifest_sha256 is not None:
        p.error("staging smoke arguments are only valid with --run-staging-smoke")
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
