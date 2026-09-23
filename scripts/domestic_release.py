#!/usr/bin/env python3
"""Poll protected GitHub main and promote exact, locally built commits in order.

Run as the staging release controller. Source builds execute separately as
``aicrm-build``; this controller invokes the fixed installer and performs
read-only privileged host readback.
"""
from __future__ import annotations

import argparse
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import shlex
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.request

SHA = re.compile(r"^[0-9a-f]{40}$")
FILE_SHA = re.compile(r"^[0-9a-f]{64}$")
REPO = "qianlan33333-png/AI-CRM-v4"
STAGING_RETRY_SHA = "5538d615a9abe2e25be799936866a7330b1d3af8"
BUILD_USER = "aicrm-build"
BUILD_ROOT = Path("/opt/aicrm/domestic/build-worker")
HOST_READBACK_CODE = """
import hashlib, json, pathlib, re, subprocess, sys, urllib.request
p = pathlib.Path('/opt/aicrm/current').resolve(strict=True)
target = sys.argv[1] if len(sys.argv) > 1 else p.name
if not re.fullmatch(r'[0-9a-f]{40}', target):
    raise SystemExit('invalid readback SHA')
units = {}
for name in ('aicrm.service', 'aicrm-effects-worker.service'):
    active = subprocess.run(('systemctl', 'is-active', '--quiet', name)).returncode == 0
    pid = subprocess.check_output(('systemctl', 'show', name, '-p', 'MainPID', '--value'), text=True).strip()
    executable = pathlib.Path(f'/proc/{pid}/exe').resolve(strict=True) if pid.isdigit() and int(pid) > 0 else None
    units[name] = {'active': active, 'pid': int(pid) if pid.isdigit() else 0, 'executable': str(executable) if executable else None}
receipt_path = pathlib.Path('/opt/aicrm/domestic-receipts') / f'{target}.json'
receipt_exists = receipt_path.exists() or receipt_path.is_symlink()
receipt = json.loads(receipt_path.read_text()) if receipt_exists and receipt_path.is_file() and not receipt_path.is_symlink() else None
backup_path = pathlib.Path('/opt/aicrm/database-backups') / f'pre-{target}.dump'
database_backup_exists = backup_path.is_file() and not backup_path.is_symlink()
print(json.dumps({
    'current': str(p),
    'release_env': (p / 'release.env').read_text(),
    'manifest_sha256': hashlib.sha256((p / 'release-files.sha256').read_bytes()).hexdigest(),
    'readyz': json.load(urllib.request.urlopen('http://127.0.0.1:8080/readyz', timeout=3)),
    'services': units,
    'receipt_target_sha': target,
    'receipt_exists': receipt_exists,
    'receipt': receipt,
    'database_backup_exists': database_backup_exists,
}))
"""


def command(*args: str, cwd: Path | None = None, timeout: int = 600) -> str:
    result = subprocess.run(args, cwd=cwd, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=timeout)
    if result.returncode:
        raise RuntimeError(f"command failed ({result.returncode}): {args[0]} {args[1] if len(args)>1 else ''}: {result.stderr[-500:]}")
    return result.stdout.strip()


def git(repo: Path, *args: str) -> str:
    return command("git", "-C", str(repo), *args)


def require_official_origin(repo: Path) -> None:
    origin = git(repo, "remote", "get-url", "origin")
    if origin not in {f"git@github.com:{REPO}.git", f"https://github.com/{REPO}.git"}:
        raise RuntimeError("source origin is not the official GitHub repository")


def exact_check_success(sha: str, token: str | None = None) -> bool:
    if not SHA.fullmatch(sha):
        raise ValueError("invalid commit SHA")
    url = f"https://api.github.com/repos/{REPO}/commits/{sha}/check-runs?check_name=check&per_page=100"
    headers = {"Accept": "application/vnd.github+json", "User-Agent": "aicrm-domestic-release/1", "X-GitHub-Api-Version": "2022-11-28"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    with urllib.request.urlopen(urllib.request.Request(url, headers=headers), timeout=20) as response:
        data = json.load(response)
    # Multiple workflow reruns may exist. The newest run must succeed: an old
    # green run cannot override a later failed rerun on the same SHA.
    runs = [item for item in data.get("check_runs", []) if item.get("name") == "check" and item.get("head_sha") == sha and item.get("app", {}).get("slug") == "github-actions"]
    if not runs:
        return False
    newest = max(runs, key=lambda item: (item.get("started_at") or "", item.get("id") or 0))
    return newest.get("status") == "completed" and newest.get("conclusion") == "success"


def github_commit_tree(sha: str, token: str | None = None) -> str:
    if not SHA.fullmatch(sha):
        raise ValueError("invalid commit SHA")
    headers = {"Accept": "application/vnd.github+json", "User-Agent": "aicrm-domestic-release/1", "X-GitHub-Api-Version": "2022-11-28"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    with urllib.request.urlopen(urllib.request.Request(f"https://api.github.com/repos/{REPO}/git/commits/{sha}", headers=headers), timeout=20) as response:
        data = json.load(response)
    tree = data.get("tree", {}).get("sha")
    if data.get("sha") != sha or not isinstance(tree, str) or not SHA.fullmatch(tree):
        raise RuntimeError("GitHub commit/tree response mismatch")
    return tree


def first_parent_queue(repo: Path, base: str, head: str) -> list[str]:
    if not SHA.fullmatch(base) or not SHA.fullmatch(head):
        raise ValueError("invalid queue cursor")
    command("git", "-C", str(repo), "merge-base", "--is-ancestor", base, head)
    values = git(repo, "rev-list", "--first-parent", "--reverse", f"{base}..{head}").splitlines()
    parent = base
    for sha in values:
        if not SHA.fullmatch(sha) or git(repo, "rev-parse", f"{sha}^1") != parent:
            raise RuntimeError("first-parent chain changed")
        parent = sha
    return values


def atomic_json(path: Path, value: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, name = tempfile.mkstemp(dir=path.parent, prefix=f".{path.name}.")
    try:
        with os.fdopen(fd, "w") as stream:
            json.dump(value, stream, sort_keys=True, indent=2)
            stream.write("\n")
            stream.flush()
            os.fsync(stream.fileno())
        os.chmod(name, 0o600)
        os.replace(name, path)
    finally:
        Path(name).unlink(missing_ok=True)


def load_config(path: Path) -> dict:
    config = json.loads(path.read_text())
    required = ("repo", "work_root", "state", "stage_incoming", "stage_helper", "prod_host", "prod_user", "prod_key", "prod_known_hosts", "prod_incoming", "prod_helper")
    if any(not isinstance(config.get(key), str) or not config[key] for key in required):
        raise ValueError("missing domestic release config")
    if not isinstance(config.get("production_enabled"), bool):
        raise ValueError("production_enabled must be explicit")
    return config


def ssh_args(config: dict) -> list[str]:
    return ["ssh", "-i", config["prod_key"], "-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes", "-o", "StrictHostKeyChecking=yes", "-o", f"UserKnownHostsFile={config['prod_known_hosts']}", "-o", "ConnectTimeout=15", f"{config['prod_user']}@{config['prod_host']}"]


def prod_readback(config: dict, receipt_sha: str | None = None) -> dict:
    # Entirely read-only. Also used after a result-unknown SSH interruption.
    if receipt_sha is not None and not SHA.fullmatch(receipt_sha):
        raise ValueError("invalid receipt SHA")
    args = ["sudo python3 -c " + shlex.quote(HOST_READBACK_CODE)]
    if receipt_sha is not None:
        args.append(receipt_sha)
    return json.loads(command(*ssh_args(config), *args, timeout=30))


def stage_readback(receipt_sha: str | None = None) -> dict:
    if receipt_sha is not None and not SHA.fullmatch(receipt_sha):
        raise ValueError("invalid receipt SHA")
    args = ["sudo", "python3", "-c", HOST_READBACK_CODE]
    if receipt_sha is not None:
        args.append(receipt_sha)
    return json.loads(command(*args, timeout=30))


def bind_baseline(config: dict, prod_preview_sha: str) -> dict:
    """One-time binding: installed preview and protected main have one tree."""
    if not SHA.fullmatch(prod_preview_sha):
        raise ValueError("invalid production preview SHA")
    repo = Path(config["repo"])
    state_path = Path(config["state"])
    if state_path.exists():
        raise RuntimeError("baseline already exists; never overwrite the release ledger")
    require_official_origin(repo)
    git(repo, "fetch", "--no-tags", "origin", "main")
    main_sha = git(repo, "rev-parse", "refs/remotes/origin/main")
    main_tree = git(repo, "rev-parse", f"{main_sha}^{{tree}}")
    preview_tree = github_commit_tree(prod_preview_sha, os.environ.get("GITHUB_TOKEN"))
    if main_tree != preview_tree:
        raise RuntimeError("installed preview and current main differ in source tree")
    verify_readback(prod_readback(config), prod_preview_sha)
    manifest_sha = command(*ssh_args(config), "sha256sum", "/opt/aicrm/current/release-files.sha256", timeout=30).split()[0]
    receipt = json.loads(command(*ssh_args(config), "sudo", "cat", f"/opt/aicrm/release-success/{prod_preview_sha}.json", timeout=30))
    if receipt.get("release_sha") != prod_preview_sha or receipt.get("manifest_sha256") != manifest_sha:
        raise RuntimeError("installed production receipt or manifest mismatch")
    stage_release = Path(config["work_root"]) / "builds" / main_sha / "release"
    if not stage_release.is_dir():
        raise RuntimeError("staging baseline package is missing")
    stage_marker = Path("/opt/aicrm/current/release.env")
    if not stage_marker.is_file() or stage_marker.read_text() != f"AICRM_RELEASE_SHA={main_sha}\n":
        raise RuntimeError("staging baseline is not installed")
    state = {"schema_version": 1, "status": "ready", "processed_sha": main_sha, "deployed_source_sha": main_sha, "prod_installed_sha": prod_preview_sha, "prod_installed_manifest_sha256": manifest_sha, "baseline_tree": main_tree, "baseline_prod_manifest_sha256": manifest_sha, "blocked_sha": None, "failure": None}
    atomic_json(state_path, state)
    return state


def verify_readback(data: dict, sha: str, manifest_sha256: str | None = None) -> None:
    if data.get("release_env") != f"AICRM_RELEASE_SHA={sha}\n" or data.get("readyz", {}).get("release_sha") != sha or data.get("readyz", {}).get("status") != "ready":
        raise RuntimeError("production version or readyz mismatch")
    if data.get("current") != f"/opt/aicrm/releases/{sha}":
        raise RuntimeError("installed release directory mismatch")
    if manifest_sha256 is not None and data.get("manifest_sha256") != manifest_sha256:
        raise RuntimeError("installed manifest digest mismatch")
    services = data.get("services", {})
    for unit in ("aicrm.service", "aicrm-effects-worker.service"):
        if services.get(unit, {}).get("active") is not True or services[unit].get("pid", 0) <= 0:
            raise RuntimeError(f"installed service not active: {unit}")
        expected_executable = f"/opt/aicrm/releases/{sha}/bin/aicrm"
        if services[unit].get("executable") != expected_executable:
            raise RuntimeError(f"installed service executable mismatch: {unit}")


def verify_install_receipt(receipt: dict | None, metadata: dict, previous_sha: str) -> None:
    if not isinstance(receipt, dict):
        raise RuntimeError("production success receipt is missing or unsafe")
    expected = {
        "source_sha": metadata["source_sha"],
        "source_tree": metadata.get("source_tree"),
        "manifest_sha256": metadata["release_files_sha256"],
        "previous_sha": previous_sha,
        "technical_status": "installed_healthy",
    }
    if any(receipt.get(key) != value for key, value in expected.items()):
        raise RuntimeError("production success receipt mismatch")


def copy_payload(config: dict, sha: str, payload: Path, metadata: Path, link_sha: str) -> tuple[str, str]:
    incoming = f"{config['prod_incoming'].rstrip('/')}/{sha}"
    remote_meta = f"{config['prod_incoming'].rstrip('/')}/{sha}.json"
    # Dedicated account can only write incoming; root-owned helper revalidates
    # and moves into immutable releases under the shared host lock.
    command(*ssh_args(config), "mkdir", "-m", "0700", "--", incoming, timeout=30)
    transport = " ".join(shlex.quote(part) for part in ssh_args(config)[:-1])
    # -e is a fixed local command string; config is provisioned by root.
    # Protected Linux hardlinks forbid the unprivileged receiver from linking
    # root-owned previous releases. Receive through the fixed sudo rsync path;
    # the installer still verifies every byte before making it current.
    command("rsync", "-a", "--no-owner", "--no-group", "--no-perms", "--checksum", "--delete", "--rsync-path=sudo rsync", f"--link-dest=/opt/aicrm/releases/{link_sha}", "-e", transport, f"{payload}/", f"{config['prod_user']}@{config['prod_host']}:{incoming}/", timeout=600)
    command("rsync", "-a", "-e", transport, str(metadata), f"{config['prod_user']}@{config['prod_host']}:{remote_meta}", timeout=60)
    return incoming, remote_meta


def build_candidate(config: dict, sha: str, base: str, base_release: Path | None) -> tuple[Path, dict]:
    repo = Path(config["repo"])
    work_root = Path(config["work_root"])
    if not SHA.fullmatch(sha) or not SHA.fullmatch(base):
        raise ValueError("invalid build commit")
    worker = BUILD_ROOT / sha
    checkout = worker / "source"
    worker_out = worker / "out"
    out = work_root / "builds" / sha
    if worker.exists() or out.exists():
        raise RuntimeError("existing build path needs inspection; refusing to reuse stale artifacts")
    if not BUILD_ROOT.is_dir() or BUILD_ROOT.is_symlink():
        raise RuntimeError("isolated build account is not provisioned")
    out.parent.mkdir(parents=True, exist_ok=True)
    # Source hooks and npm/go build scripts execute as a separate account that
    # cannot read the production SSH key, sudo, state ledger or runtime env.
    build_prefix = ["sudo", "-u", BUILD_USER, "-H", "--"]
    command(*build_prefix, "mkdir", "-m", "0755", str(worker), timeout=30)
    command(*build_prefix, "git", "-c", f"safe.directory={repo / '.git'}", "clone", "-q", "--no-checkout", "--local", "--no-hardlinks", str(repo), str(checkout), timeout=120)
    command(*build_prefix, "git", "-C", str(checkout), "-c", f"safe.directory={repo / '.git'}", "fetch", "-q", "--no-tags", str(repo), sha, timeout=120)
    command(*build_prefix, "git", "-C", str(checkout), "cat-file", "-e", f"{sha}^{{commit}}", timeout=30)
    environment = [
        f"HOME={BUILD_ROOT}", f"PATH={os.environ['PATH']}",
        f"GOCACHE={BUILD_ROOT / 'cache/go-build'}",
        f"GOMODCACHE={BUILD_ROOT / 'cache/go-mod'}",
        f"npm_config_cache={BUILD_ROOT / 'cache/npm'}",
        f"TMPDIR={BUILD_ROOT / 'tmp'}", "PYTHONDONTWRITEBYTECODE=1",
    ]
    args = [*build_prefix, "/usr/bin/env", "-i", *environment, "python3", str(Path(__file__).with_name("domestic_release_build.py")), "build", "--repo", str(checkout), "--base", base, "--target", sha, "--base-release", str(base_release) if base_release is not None else "none", "--out", str(worker_out)]
    command(*args, timeout=7200)
    if worker_out.is_symlink():
        raise RuntimeError("isolated build produced a symlink")
    command(*build_prefix, "chmod", "0755", str(worker_out), timeout=30)
    if any(path.is_symlink() for path in worker_out.rglob("*")):
        raise RuntimeError("isolated build produced a symlink")
    shutil.copytree(worker_out, out)
    out.chmod(0o755)
    metadata = json.loads((out / "domestic-release.json").read_text())
    if metadata.get("source_sha") != sha or metadata.get("base_sha") != base or metadata.get("source_tree") != git(repo, "rev-parse", f"{sha}^{{tree}}"):
        raise RuntimeError("built artifact source mismatch")
    manifest = out / "release" / "release-files.sha256"
    if hashlib.sha256(manifest.read_bytes()).hexdigest() != metadata.get("release_files_sha256"):
        raise RuntimeError("built artifact manifest mismatch")
    command("sudo", "rm", "-rf", "--", str(worker), timeout=120)
    return out, metadata


def stage_install(config: dict, sha: str, out: Path, base_sha: str) -> dict:
    incoming_root = Path(config["stage_incoming"])
    incoming_root.mkdir(parents=True, exist_ok=True)
    incoming = incoming_root / sha
    if incoming.exists():
        raise RuntimeError("existing staging incoming path needs inspection")
    shutil.copytree(out / "release", incoming, copy_function=shutil.copy2)
    metadata_path = out / "domestic-release.json"
    metadata_bytes = metadata_path.read_bytes()
    metadata = json.loads(metadata_bytes)
    if metadata.get("source_sha") != sha or metadata.get("base_sha") != base_sha:
        raise RuntimeError("staging metadata source or base SHA mismatch")
    metadata_sha = hashlib.sha256(metadata_bytes).hexdigest()
    result = command("sudo", config["stage_helper"], "--incoming", str(incoming), "--metadata", str(metadata_path), "--expected-sha", sha, "--metadata-sha256", metadata_sha, "--expected-base", base_sha, timeout=300)
    receipt = json.loads(result.splitlines()[-1])
    if receipt.get("source_sha") != sha or receipt.get("technical_status") != "installed_healthy":
        raise RuntimeError("staging install receipt mismatch")
    expected_manifest = metadata["release_files_sha256"]
    verify_readback(stage_readback(), sha, expected_manifest)
    if metadata.get("frontend_changed"):
        with urllib.request.urlopen("http://127.0.0.1:8080/login", timeout=5) as response:
            if response.status != 200 or "text/html" not in response.headers.get("Content-Type", ""):
                raise RuntimeError("staging UI route is not serving HTML")
    return receipt


def verify_release_artifact(release: Path, metadata: dict) -> None:
    """Revalidate the complete cached artifact before reusing a stage orphan."""
    if release.is_symlink() or not release.is_dir():
        raise RuntimeError("cached release artifact is missing or unsafe")
    manifest = release / "release-files.sha256"
    if manifest.is_symlink() or not manifest.is_file() or hashlib.sha256(manifest.read_bytes()).hexdigest() != metadata.get("release_files_sha256"):
        raise RuntimeError("cached release manifest does not match metadata")
    entries: dict[str, str] = {}
    for line in manifest.read_text().splitlines():
        match = re.fullmatch(r"([0-9a-f]{64})  (.+)", line)
        if not match:
            raise RuntimeError("cached release manifest is invalid")
        digest, name = match.groups()
        path = Path(name)
        if path.is_absolute() or path.as_posix() != name or ".." in path.parts or "." in path.parts or "\\" in name or name in {"release-files.sha256", "release.env"} or name in entries:
            raise RuntimeError("cached release manifest contains an unsafe path")
        entries[name] = digest
    actual: set[str] = set()
    for path in release.rglob("*"):
        if path.is_symlink() or not (path.is_file() or path.is_dir()):
            raise RuntimeError("cached release contains a linked or special path")
        if path.is_file() and path != manifest:
            name = path.relative_to(release).as_posix()
            actual.add(name)
            if entries.get(name) != hashlib.sha256(path.read_bytes()).hexdigest():
                raise RuntimeError("cached release file digest mismatch")
    if actual != set(entries):
        raise RuntimeError("cached release file set mismatch")


def stage_retry_metadata_path(config: dict, sha: str, metadata_bytes: bytes) -> Path:
    incoming_root = Path(config["stage_incoming"])
    if incoming_root.is_symlink() or not incoming_root.is_dir():
        raise RuntimeError("staging incoming root is missing or unsafe")
    target = incoming_root / f"{sha}.json"
    if target.is_symlink():
        raise RuntimeError("staging retry metadata path is unsafe")
    if target.exists():
        if not target.is_file() or target.read_bytes() != metadata_bytes:
            raise RuntimeError("staging retry metadata already exists with different content")
        return target
    try:
        descriptor = os.open(target, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    except FileExistsError:
        if target.is_symlink() or not target.is_file() or target.read_bytes() != metadata_bytes:
            raise RuntimeError("staging retry metadata changed concurrently")
        return target
    try:
        with os.fdopen(descriptor, "wb") as stream:
            stream.write(metadata_bytes)
            stream.flush()
            os.fsync(stream.fileno())
    except Exception:
        target.unlink(missing_ok=True)
        raise
    return target


def call_stage_retry_inspector(config: dict, metadata_path: Path, sha: str, metadata_sha: str, base_sha: str) -> dict:
    output = command(
        "sudo", config["stage_helper"], "--inspect-staging-retry",
        "--metadata", str(metadata_path), "--expected-sha", sha,
        "--metadata-sha256", metadata_sha, "--expected-base", base_sha,
        timeout=60,
    )
    result = json.loads(output.splitlines()[-1])
    if (
        result.get("status") != "eligible"
        or result.get("source_sha") != sha
        or result.get("base_sha") != base_sha
        or result.get("orphan_verified") is not True
        or result.get("receipt_exists") is not False
        or result.get("backup_exists") is not False
        or result.get("migration_0206_applied") is not False
        or result.get("old_constraint_present") is not True
    ):
        raise RuntimeError("staging retry preflight did not prove the exact allowed state")
    return result


def promote_checked_candidate(
    config: dict,
    state_path: Path,
    state: dict,
    sha: str,
    out: Path,
    metadata: dict,
    installed: str,
    started: float,
    *,
    stage_receipt: dict,
) -> None:
    """Run the same production handoff only after stage receipt/readback is verified."""
    state.update(
        status="staging_verified",
        blocked_sha=sha,
        staging_verified_sha=sha,
        staging_verified_receipt=stage_receipt,
        staging_verified_manifest_sha256=metadata["release_files_sha256"],
    )
    atomic_json(state_path, state)
    try:
        metadata_path = out / "domestic-release.json"
        metadata_bytes = metadata_path.read_bytes()
        copied_metadata = json.loads(metadata_bytes)
        if copied_metadata != metadata or copied_metadata.get("source_sha") != sha:
            raise RuntimeError("staged metadata changed after package verification")
        metadata_sha = hashlib.sha256(metadata_bytes).hexdigest()
        incoming, remote_meta = copy_payload(config, sha, out / "release", metadata_path, installed)
    except Exception as exc:
        state.update(status="transport_failed", blocked_sha=sha, failure=f"stage verified; production handoff did not complete: {type(exc).__name__}")
        atomic_json(state_path, state)
        raise
    # Once the remote install begins, a lost reply is outcome_unknown even if
    # its transport error looks retryable.
    state.update(status="prod_installing", blocked_sha=sha)
    atomic_json(state_path, state)
    try:
        result = command(*ssh_args(config), "sudo", config["prod_helper"], "--incoming", incoming, "--metadata", remote_meta, "--expected-sha", sha, "--metadata-sha256", metadata_sha, "--expected-base", installed, timeout=300)
        helper_receipt = json.loads(result.splitlines()[-1])
        production = prod_readback(config, sha)
        verify_readback(production, sha, metadata["release_files_sha256"])
        verify_install_receipt(production.get("receipt"), metadata, installed)
        if helper_receipt != production.get("receipt"):
            raise RuntimeError("production helper receipt differs from readback")
    except Exception as exc:
        state.update(status="outcome_unknown", failure=f"production install/readback requires reconciliation: {type(exc).__name__}")
        atomic_json(state_path, state)
        raise
    commit_time = git(Path(config["repo"]), "show", "-s", "--format=%ct", sha)
    state.update(
        status="ready",
        processed_sha=sha,
        deployed_source_sha=sha,
        prod_installed_sha=sha,
        prod_installed_manifest_sha256=metadata["release_files_sha256"],
        blocked_sha=None,
        failure=None,
        last_duration_seconds=round(time.monotonic() - started, 1),
        last_merge_to_healthy_seconds=max(0, int(time.time()) - int(commit_time)),
    )
    atomic_json(state_path, state)


def retry_staging(config: dict, expected_sha: str) -> dict:
    """Retry only the exact stage backup failure, then continue one normal production handoff."""
    if not isinstance(expected_sha, str) or not SHA.fullmatch(expected_sha):
        raise ValueError("retry-staging requires an exact --sha")
    if expected_sha != STAGING_RETRY_SHA:
        raise RuntimeError("retry-staging is limited to the current 5538 migration incident")
    if not config["production_enabled"]:
        raise RuntimeError("production publishing is disabled")
    started = time.monotonic()
    repo = Path(config["repo"])
    work_root = Path(config["work_root"])
    state_path = Path(config["state"])
    if not state_path.is_file():
        raise RuntimeError("release ledger is missing")
    lock_path = state_path.with_suffix(".lock")
    with lock_path.open("a+") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        state = json.loads(state_path.read_text())
        if state.get("status") != "staging_failed" or state.get("blocked_sha") != expected_sha:
            raise RuntimeError("retry-staging requires the exact blocked staging_failed SHA")
        if state.get("staging_retry_attempted_sha") == expected_sha:
            raise RuntimeError("the one permitted staging retry was already attempted")
        processed = state.get("processed_sha")
        deployed = state.get("deployed_source_sha")
        installed = state.get("prod_installed_sha")
        if not all(isinstance(value, str) and SHA.fullmatch(value) for value in (processed, deployed, installed)):
            raise RuntimeError("invalid release ledger cursor")
        require_official_origin(repo)
        git(repo, "fetch", "--no-tags", "origin", "main")
        main_sha = git(repo, "rev-parse", "refs/remotes/origin/main")
        queue = first_parent_queue(repo, processed, main_sha)
        if (
            not queue
            or queue[0] != expected_sha
            or git(repo, "rev-parse", f"{expected_sha}^1") != processed
            or not exact_check_success(expected_sha, os.environ.get("GITHUB_TOKEN"))
        ):
            raise RuntimeError("blocked SHA is not the exact checked first-parent queue head")

        build = work_root / "builds" / expected_sha
        metadata_path = build / "domestic-release.json"
        release_path = build / "release"
        if metadata_path.is_symlink() or not metadata_path.is_file() or release_path.is_symlink() or not release_path.is_dir():
            raise RuntimeError("verified stage package is missing or unsafe")
        metadata_bytes = metadata_path.read_bytes()
        metadata = json.loads(metadata_bytes)
        migration_paths = [path for path in metadata.get("changed_paths", []) if isinstance(path, str) and path.startswith("migrations/")]
        if (
            metadata.get("source_sha") != expected_sha
            or metadata.get("base_sha") != deployed
            or metadata.get("source_tree") != git(repo, "rev-parse", f"{expected_sha}^{{tree}}")
            or metadata.get("migrations_changed") is not True
            or migration_paths != ["migrations/0206_order_native_alipay_checkout.sql"]
        ):
            raise RuntimeError("staging retry metadata does not match the exact 0206 release")
        verify_release_artifact(release_path, metadata)
        metadata_sha = hashlib.sha256(metadata_bytes).hexdigest()

        previous_build = work_root / "builds" / deployed
        previous_metadata_path = previous_build / "domestic-release.json"
        previous_release_path = previous_build / "release"
        if previous_metadata_path.is_symlink() or not previous_metadata_path.is_file() or previous_release_path.is_symlink() or not previous_release_path.is_dir():
            raise RuntimeError("verified previous stage release is missing or unsafe")
        previous_metadata = json.loads(previous_metadata_path.read_text())
        previous_manifest = previous_metadata.get("release_files_sha256")
        if previous_metadata.get("source_sha") != deployed or not isinstance(previous_manifest, str) or not FILE_SHA.fullmatch(previous_manifest):
            raise RuntimeError("previous stage manifest is missing")
        verify_release_artifact(previous_release_path, previous_metadata)
        stage_before = stage_readback(expected_sha)
        if stage_before.get("receipt_target_sha") != expected_sha or stage_before.get("receipt_exists") is not False:
            raise RuntimeError("stage target receipt already exists; inspect before retry")
        verify_readback(stage_before, deployed, previous_manifest)

        retry_metadata_path = stage_retry_metadata_path(config, expected_sha, metadata_bytes)
        call_stage_retry_inspector(config, retry_metadata_path, expected_sha, metadata_sha, deployed)
        # Persist a one-shot guard before invoking a helper that can change the
        # release or schema. A process interruption leaves the queue halted.
        state.update(
            status="staging_retrying",
            blocked_sha=expected_sha,
            staging_retry_attempted_sha=expected_sha,
            staging_retry_attempted_at_utc=time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        )
        atomic_json(state_path, state)
        try:
            output = command(
                "sudo", config["stage_helper"], "--retry-existing", "--retry-staging-migration",
                "--metadata", str(retry_metadata_path), "--expected-sha", expected_sha,
                "--metadata-sha256", metadata_sha, "--expected-base", deployed,
                timeout=300,
            )
            helper_receipt = json.loads(output.splitlines()[-1])
            stage_after = stage_readback(expected_sha)
            verify_readback(stage_after, expected_sha, metadata["release_files_sha256"])
            verify_install_receipt(stage_after.get("receipt"), metadata, deployed)
            expected_backup = f"/opt/aicrm/database-backups/pre-{expected_sha}.dump"
            if (
                helper_receipt != stage_after.get("receipt")
                or helper_receipt.get("database_backup") != expected_backup
                or stage_after.get("database_backup_exists") is not True
            ):
                raise RuntimeError("staging migration receipt or backup readback mismatch")
        except Exception as exc:
            confirmed_unchanged = False
            try:
                call_stage_retry_inspector(config, retry_metadata_path, expected_sha, metadata_sha, deployed)
                stage_after = stage_readback(expected_sha)
                verify_readback(stage_after, deployed, previous_manifest)
                confirmed_unchanged = stage_after.get("receipt_target_sha") == expected_sha and stage_after.get("receipt_exists") is False
            except Exception:
                confirmed_unchanged = False
            state.update(
                status="staging_failed" if confirmed_unchanged else "staging_retry_unknown",
                blocked_sha=expected_sha,
                failure=("stage retry failed; independent readback confirmed no stage or schema change" if confirmed_unchanged else "stage retry result requires read-only reconciliation"),
            )
            atomic_json(state_path, state)
            raise RuntimeError(state["failure"]) from exc

        promote_checked_candidate(
            config, state_path, state, expected_sha, build, metadata, installed, started,
            stage_receipt=helper_receipt,
        )
        return {"status": "ready", "processed_sha": expected_sha, "stage_retry": "readback_confirmed"}


def recover(config: dict, *, retry_blocked: bool, expected_sha: str) -> dict:
    """Explicitly reconcile one outcome-unknown release and retry its verified orphan once."""
    if not retry_blocked:
        raise ValueError("recover requires --retry-blocked")
    if not isinstance(expected_sha, str) or not SHA.fullmatch(expected_sha):
        raise ValueError("recover requires an exact --sha")
    started = time.monotonic()
    repo = Path(config["repo"])
    work_root = Path(config["work_root"])
    state_path = Path(config["state"])
    if not state_path.is_file():
        raise RuntimeError("release ledger is missing")
    lock_path = state_path.with_suffix(".lock")
    with lock_path.open("a+") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        state = json.loads(state_path.read_text())
        sha = state.get("blocked_sha")
        if state.get("status") != "outcome_unknown" or not isinstance(sha, str) or not SHA.fullmatch(sha):
            raise RuntimeError("recover requires one blocked outcome_unknown SHA")
        if sha != expected_sha:
            raise RuntimeError("explicit --sha does not match blocked outcome_unknown SHA")
        if not config["production_enabled"]:
            raise RuntimeError("production publishing is disabled")
        require_official_origin(repo)
        git(repo, "fetch", "--no-tags", "origin", "main")
        head = git(repo, "rev-parse", "refs/remotes/origin/main")
        queue = first_parent_queue(repo, state["processed_sha"], head)
        if not queue or queue[0] != sha or not exact_check_success(sha, os.environ.get("GITHUB_TOKEN")):
            raise RuntimeError("blocked SHA is not the next checked commit on protected main")

        installed = state.get("prod_installed_sha")
        deployed = state.get("deployed_source_sha")
        if not all(isinstance(value, str) and SHA.fullmatch(value) for value in (installed, deployed)):
            raise RuntimeError("invalid prior release cursor")
        build = work_root / "builds" / sha
        metadata_path = build / "domestic-release.json"
        release_path = build / "release"
        if metadata_path.is_symlink() or not metadata_path.is_file() or release_path.is_symlink() or not release_path.is_dir():
            raise RuntimeError("verified stage package is missing or unsafe")
        metadata_bytes = metadata_path.read_bytes()
        metadata = json.loads(metadata_bytes)
        metadata_sha = hashlib.sha256(metadata_bytes).hexdigest()
        if (
            metadata.get("source_sha") != sha
            or metadata.get("base_sha") != deployed
            or metadata.get("source_tree") != git(repo, "rev-parse", f"{sha}^{{tree}}")
            or type(metadata.get("migrations_changed")) is not bool
        ):
            raise RuntimeError("stage metadata does not match the blocked source")
        if metadata["migrations_changed"]:
            raise RuntimeError("automatic orphan recovery is disabled for migration releases")
        manifest_path = release_path / "release-files.sha256"
        manifest_sha = hashlib.sha256(manifest_path.read_bytes()).hexdigest()
        if manifest_sha != metadata.get("release_files_sha256"):
            raise RuntimeError("stage package manifest differs from its metadata")
        stage = stage_readback(sha)
        verify_readback(stage, sha, manifest_sha)
        verify_install_receipt(stage.get("receipt"), metadata, deployed)

        production = prod_readback(config, sha)
        target_receipt_present = production.get("receipt_exists") is True
        if target_receipt_present:
            if production.get("receipt_target_sha") != sha:
                raise RuntimeError("production receipt readback targeted another SHA")
            verify_readback(production, sha, manifest_sha)
            verify_install_receipt(production.get("receipt"), metadata, installed)
            # The install completed but the caller did not record its receipt.
            # This path is read-only on production and only repairs the ledger.
        else:
            if production.get("current") != f"/opt/aicrm/releases/{installed}":
                raise RuntimeError("production current conflicts with the prior release; refusing retry")
            previous_manifest = state.get("prod_installed_manifest_sha256")
            if not isinstance(previous_manifest, str) or not FILE_SHA.fullmatch(previous_manifest):
                previous_manifest = state.get("baseline_prod_manifest_sha256")
            if not isinstance(previous_manifest, str) or not FILE_SHA.fullmatch(previous_manifest):
                raise RuntimeError("prior production manifest digest is missing")
            verify_readback(production, installed, previous_manifest)
            if state.get("recovery_attempted_sha") == sha:
                raise RuntimeError("the one permitted orphan retry was already attempted")
            remote_metadata = f"{config['prod_incoming'].rstrip('/')}/{sha}.json"
            remote_metadata_sha = command(*ssh_args(config), "sudo", "sha256sum", remote_metadata, timeout=30).split()[0]
            if remote_metadata_sha != metadata_sha:
                raise RuntimeError("production metadata differs from the verified stage package")
            # Persist the one-time guard before the remote helper can change current.
            state["recovery_attempted_sha"] = sha
            state["recovery_attempted_at_utc"] = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
            atomic_json(state_path, state)
            try:
                result = command(*ssh_args(config), "sudo", config["prod_helper"], "--retry-existing", "--metadata", remote_metadata, "--expected-sha", sha, "--metadata-sha256", metadata_sha, "--expected-base", installed, timeout=300)
                helper_receipt = json.loads(result.splitlines()[-1])
                production = prod_readback(config, sha)
                verify_readback(production, sha, manifest_sha)
                verify_install_receipt(production.get("receipt"), metadata, installed)
                if helper_receipt != production.get("receipt"):
                    raise RuntimeError("production helper receipt differs from readback")
            except Exception as exc:
                state["failure"] = str(exc)[-500:]
                atomic_json(state_path, state)
                raise

        commit_time = git(repo, "show", "-s", "--format=%ct", sha)
        state.update(
            status="ready",
            processed_sha=sha,
            deployed_source_sha=sha,
            prod_installed_sha=sha,
            prod_installed_manifest_sha256=manifest_sha,
            blocked_sha=None,
            failure=None,
            last_recovery_sha=sha,
            last_recovery_status="readback_confirmed" if target_receipt_present else "orphan_reused_healthy",
            last_duration_seconds=round(time.monotonic() - started, 1),
            last_merge_to_healthy_seconds=max(0, int(time.time()) - int(commit_time)),
        )
        atomic_json(state_path, state)
        return {"status": "ready", "processed_sha": sha, "recovery": state["last_recovery_status"]}


def poll(config: dict) -> dict:
    repo = Path(config["repo"])
    state_path = Path(config["state"])
    if not state_path.is_file():
        raise RuntimeError("baseline state missing; run documented read-only baseline binding first")
    lock_path = state_path.with_suffix(".lock")
    with lock_path.open("a+") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        state = json.loads(state_path.read_text())
        if state.get("status") != "ready":
            raise RuntimeError(f"queue halted: {state.get('status')}")
        processed = state["processed_sha"]
        deployed = state["deployed_source_sha"]
        installed = state["prod_installed_sha"]
        if any(not SHA.fullmatch(value) for value in (processed, deployed, installed)):
            raise ValueError("invalid state cursor")
        require_official_origin(repo)
        git(repo, "fetch", "--no-tags", "origin", "main")
        head = git(repo, "rev-parse", "refs/remotes/origin/main")
        queue = first_parent_queue(repo, processed, head)
        for sha in queue:
            started = time.monotonic()
            if not exact_check_success(sha, os.environ.get("GITHUB_TOKEN")):
                return {"status": "awaiting_exact_check", "sha": sha}
            if not config["production_enabled"]:
                return {"status": "dry_run_only", "sha": sha}
            # The builder decides whether the merged change affects runtime;
            # docs-only commits still require exact check but advance cursor.
            plan = json.loads(command("python3", str(Path(__file__).with_name("domestic_release_build.py")), "classify", "--repo", str(repo), "--base", deployed, "--target", sha))
            if not plan.get("runtime_changed"):
                state["processed_sha"] = sha
                atomic_json(state_path, state)
                processed = sha
                continue
            base_release = Path(config["work_root"]) / "builds" / deployed / "release"
            if not base_release.is_dir():
                raise RuntimeError("verified staging base release missing")
            try:
                out, metadata = build_candidate(config, sha, deployed, base_release)
                stage_receipt = stage_install(config, sha, out, deployed)
            except Exception as exc:
                state.update(status="staging_failed", blocked_sha=sha, failure=f"staging install failed: {type(exc).__name__}")
                atomic_json(state_path, state)
                raise
            promote_checked_candidate(config, state_path, state, sha, out, metadata, installed, started, stage_receipt=stage_receipt)
            processed, deployed, installed = sha, sha, sha
        return {"status": "ready", "processed_sha": processed, "queued_count": len(queue)}


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--config", type=Path, required=True)
    parser.add_argument("action", choices=("poll", "readback", "bind-baseline", "recover", "retry-staging"))
    parser.add_argument("--prod-preview-sha")
    parser.add_argument("--retry-blocked", action="store_true")
    parser.add_argument("--sha", help=f"exact blocked commit SHA; staging retry only accepts {STAGING_RETRY_SHA}")
    args = parser.parse_args()
    config = load_config(args.config)
    if args.action == "poll":
        if args.retry_blocked or args.sha:
            parser.error("--retry-blocked and --sha are only valid with recover or retry-staging")
        result = poll(config)
    elif args.action == "readback":
        if args.retry_blocked or args.sha:
            parser.error("--retry-blocked and --sha are only valid with recover or retry-staging")
        result = prod_readback(config)
    elif args.action == "bind-baseline":
        if args.retry_blocked or args.sha:
            parser.error("--retry-blocked and --sha are only valid with recover or retry-staging")
        if not args.prod_preview_sha:
            parser.error("bind-baseline requires --prod-preview-sha")
        result = bind_baseline(config, args.prod_preview_sha)
    elif args.action == "retry-staging":
        if args.retry_blocked or not args.sha or args.prod_preview_sha:
            parser.error("retry-staging requires only --sha <exact-blocked-SHA>")
        result = retry_staging(config, args.sha)
    else:
        if not args.retry_blocked or not args.sha or args.prod_preview_sha:
            parser.error("recover requires --retry-blocked --sha <exact-blocked-SHA>")
        result = recover(config, retry_blocked=True, expected_sha=args.sha)
    print(json.dumps(result, ensure_ascii=False, sort_keys=True))


if __name__ == "__main__":
    main()
