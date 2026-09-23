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
import json
import os
from pathlib import Path
import re
import shutil
import stat
import subprocess
import sys
import tempfile
import time
import urllib.request

SHA = re.compile(r"^[0-9a-f]{40}$")
FILE_SHA = re.compile(r"^[0-9a-f]{64}$")
ROOT = Path("/opt/aicrm")
RELEASES = ROOT / "releases"
CURRENT = ROOT / "current"
LOCK = ROOT / "install-release.lock"
ENV = Path("/etc/aicrm/aicrm.env")
READY = "http://127.0.0.1:8080/readyz"


def run(*args: str, check: bool = True) -> subprocess.CompletedProcess[str]:
    return subprocess.run(args, check=check, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)


def digest(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as f:
        for block in iter(lambda: f.read(1024 * 1024), b""):
            h.update(block)
    return h.hexdigest()


def verify_payload(payload: Path, metadata: dict) -> None:
    """Reject missing, extra, linked and altered files before any live action."""
    if payload.is_symlink() or not payload.is_dir():
        raise ValueError("payload must be a real directory")
    manifest = payload / "release-files.sha256"
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
        if rel.is_absolute() or ".." in rel.parts or name in {"release-files.sha256", "release.env"} or name in listed:
            raise ValueError("unsafe manifest path")
        listed[name] = value
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
    root = ROOT / "database-backups"
    root.mkdir(mode=0o700, exist_ok=True)
    root.chmod(0o700)
    target = root / f"pre-{sha}.dump"
    if target.exists():
        raise RuntimeError("backup for SHA already exists; inspect before retry")
    with tempfile.NamedTemporaryFile(dir=root, prefix=f".pre-{sha}.", delete=False) as out:
        temp = Path(out.name)
        try:
            process = subprocess.run(["runuser", "-u", "aicrm", "--", "env", f"PGDATABASE={values[0]}", "pg_dump", "-Fc"], stdout=out, stderr=subprocess.PIPE)
            if process.returncode != 0 or out.tell() < 100:
                raise RuntimeError(f"pg_dump failed: {process.stderr.decode(errors='replace')[-300:]}")
            out.flush()
            os.fsync(out.fileno())
            run("pg_restore", "--list", str(temp))
            os.chmod(temp, 0o600)
            os.replace(temp, target)
        finally:
            temp.unlink(missing_ok=True)
    return target


def install(incoming: Path, metadata: dict, expected_base: str | None) -> dict:
    sha = metadata.get("source_sha")
    if not isinstance(sha, str) or not SHA.fullmatch(sha):
        raise ValueError("invalid source SHA")
    if expected_base is not None and not SHA.fullmatch(expected_base):
        raise ValueError("invalid base SHA")
    if type(metadata.get("migrations_changed")) is not bool:
        raise ValueError("migration classification is required")
    if not ENV.is_file() or not shutil.which("systemctl"):
        raise RuntimeError("host runtime is not provisioned")
    for control in (ROOT, RELEASES):
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
        verify_payload(incoming, metadata)
        release = RELEASES / sha
        if release.exists():
            raise RuntimeError("target release already exists; inspect before retry")
        # Validate before move and then seal; the receiving user must lose write
        # access before the live symlink can point at this directory.
        shutil.move(str(incoming), str(release))
        run("chown", "-R", "root:root", str(release))
        run("chmod", "-R", "go-w", str(release))
        (release / "release.env").write_text(f"AICRM_RELEASE_SHA={sha}\n")
        os.chmod(release / "release.env", 0o644)
        verify_payload_without_release_env(release, metadata)
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
        receipt_path = ROOT / "domestic-receipts"
        receipt_path.mkdir(mode=0o700, exist_ok=True)
        temp_receipt = receipt_path / f".{sha}.json.tmp"
        temp_receipt.write_text(json.dumps(receipt, sort_keys=True) + "\n")
        os.chmod(temp_receipt, 0o600)
        os.replace(temp_receipt, receipt_path / f"{sha}.json")
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
    # verify_payload disallows a marker in incoming. Temporarily compare the
    # manifest's exact file set here instead of altering the release tree.
    manifest = payload / "release-files.sha256"
    if digest(manifest) != metadata["release_files_sha256"]:
        raise ValueError("manifest digest mismatch")
    entries = {}
    for line in manifest.read_text().splitlines():
        match = re.fullmatch(r"([0-9a-f]{64})  (.+)", line)
        if not match:
            raise ValueError("invalid manifest")
        entries[match[2]] = match[1]
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


def main() -> None:
    p = argparse.ArgumentParser()
    p.add_argument("--incoming", type=Path, required=True)
    p.add_argument("--metadata", type=Path, required=True)
    p.add_argument("--expected-base", required=True, help="40-char installed SHA or 'none' for empty staging")
    args = p.parse_args()
    if os.geteuid() != 0:
        raise SystemExit("root required")
    if not args.incoming.resolve().is_relative_to(ROOT / "domestic-incoming"):
        raise SystemExit("incoming path outside domestic-incoming")
    metadata = json.loads(args.metadata.read_text())
    print(json.dumps(install(args.incoming, metadata, None if args.expected_base == "none" else args.expected_base), sort_keys=True))


if __name__ == "__main__":
    main()
