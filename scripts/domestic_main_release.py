#!/usr/bin/env python3
"""Domestic Git authority and fail-closed serial release controller.

Developers push only ``refs/heads/codex/*`` to the staging bare repository.
The restricted submit endpoint records an exact branch/head/base tuple.  One
locked controller checks and releases that tuple, stores a self-contained
source bundle on production before installing application files, reads the
same package back from production, then advances domestic ``main`` with CAS.
This controller deliberately has no GitHub API or GitHub push path.
"""
from __future__ import annotations

import argparse
from concurrent.futures import ThreadPoolExecutor, wait, FIRST_COMPLETED
from contextlib import contextmanager
from datetime import datetime, timezone
import fcntl
import grp
import hashlib
import json
import os
import pwd
from pathlib import Path
import re
import shlex
import shutil
import stat
import subprocess
import sys
import tempfile
import time
import threading
import signal
import tarfile
from typing import Any, Iterator

SCRIPT_DIR = Path(__file__).resolve().parent
if str(SCRIPT_DIR) not in sys.path:
    sys.path.insert(0, str(SCRIPT_DIR))
import domestic_release as legacy  # Reuse the installed-package helpers, never its GitHub poller.
import domestic_release_build as builder

SHA = re.compile(r"^[0-9a-f]{40}$")
TREE = re.compile(r"^[0-9a-f]{40}$")
FILE_SHA = re.compile(r"^[0-9a-f]{64}$")
ZERO_SHA = "0" * 40
MAIN_REF = "refs/heads/main"
CANDIDATE_REF_PREFIX = "refs/domestic/candidates/"
PUSH_REF = re.compile(r"^refs/heads/codex/[A-Za-z0-9][A-Za-z0-9._/-]{0,119}$")
SCHEMA_VERSION = 1
CURSOR_PATH = "/opt/aicrm/domestic/source-cursor/state.json"
DEFAULT_STATE = "/opt/aicrm/domestic/control/state.json"
DEFAULT_LOCK = "/opt/aicrm/domestic/control/controller.lock"
SOURCE_BACKUP_ROOT = "/opt/aicrm/domestic/source-backups"
SOURCE_BUNDLE_INCOMING_ROOT = "/opt/aicrm/domestic-incoming"
DEFAULT_REPO = "/opt/aicrm/domestic/source.git"
DEFAULT_CONTROLLER = "/usr/local/libexec/aicrm/domestic_main_release.py"
DEFAULT_CONFIG = "/etc/aicrm/domestic-main-release.json"
BASELINE_OVERLAY_BASE_SHA = "291baa2d13864c3a60f3ed93e08382c3e598db33"
BASELINE_OVERLAY_BASE_TREE = "3c17b8a86e2e69ed4f6942304300c609300fb077"
BASELINE_OVERLAY_APP_SHA = "960b30e9406fae2045aeb7ef5dce863976407727"
BASELINE_OVERLAY_PR = 46
BASELINE_OVERLAY_SEED_ROOT = Path("/var/tmp")
OLD_RELEASE_TIMER = "aicrm-domestic-release.timer"
NEW_RELEASE_TIMER = "aicrm-domestic-main-release.timer"
HOST_UNIT_FILES = {
    "deploy/aicrm-domestic-main-release.service": Path("/etc/systemd/system/aicrm-domestic-main-release.service"),
    "deploy/aicrm-domestic-main-release.timer": Path("/etc/systemd/system/aicrm-domestic-main-release.timer"),
}
BUILD_TOOLCHAIN = ("go", "node", "npm", "git", "bash")
_LOCK_CONTEXT = threading.local()


class ReleaseError(RuntimeError):
    pass


class CheckEnvironmentError(ReleaseError):
    """Checks cannot evaluate the candidate because the test environment is unavailable."""

    checkpoint: str | None = None


class ControllerMaintenanceRequired(ReleaseError):
    """A fixed controller must be installed through the reviewed maintenance path."""


class CheckIncompleteError(ReleaseError):
    """Unknown failure or incomplete evidence; no verdict and no continuation."""


class CheckCandidateError(ReleaseError):
    """An executed candidate test or validation explicitly failed."""


def _run(args: list[str], *, cwd: Path | None = None, input_text: str | None = None,
         input_path: Path | None = None, timeout: int = 600, check: bool = True,
         umask: int = -1, terminate_group: bool = False) -> subprocess.CompletedProcess[str]:
    if input_text is not None and input_path is not None:
        raise ValueError("command input must have one source")
    if terminate_group:
        if input_path is not None:
            raise ValueError("isolated command input must use text")
        process = subprocess.Popen(args, cwd=cwd, stdin=subprocess.PIPE, text=True,
                                   stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                   umask=umask, start_new_session=True)
        try:
            stdout, stderr = process.communicate(input_text, timeout=timeout)
        except BaseException as error:
            # Kill the command's group, including compilers and browser
            # children, before an attempt's generated directories are removed.
            try:
                os.killpg(process.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            process.communicate()
            if isinstance(error, subprocess.TimeoutExpired):
                raise subprocess.TimeoutExpired(["isolated-build-command"], timeout) from None
            raise
        result = subprocess.CompletedProcess(args, process.returncode, stdout, stderr)
    elif input_path is not None:
        with input_path.open("rb") as source:
            raw = subprocess.run(args, cwd=cwd, input=source.read(), stdout=subprocess.PIPE,
                                 stderr=subprocess.PIPE, timeout=timeout, umask=umask)
        result = subprocess.CompletedProcess(args, raw.returncode,
                                             raw.stdout.decode("utf-8", errors="replace"),
                                             raw.stderr.decode("utf-8", errors="replace"))
    else:
        result = subprocess.run(args, cwd=cwd, input=input_text, text=True,
                                stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=timeout,
                                umask=umask)
    if check and result.returncode:
        # Child output can contain test credentials and database diagnostics.
        # Preserve only the command basename and exit code in the durable log.
        label = Path(args[0]).name if args else "unknown-command"
        action = args[1] if len(args) > 1 and not args[1].startswith("-") else "operation"
        raise ReleaseError(f"command failed: {label} {action} (exit={result.returncode})")
    return result


def _git(repo: Path, *args: str, timeout: int = 600, check: bool = True) -> str:
    return _run(["git", "-c", f"safe.directory={repo}", f"--git-dir={repo}", *args], timeout=timeout, check=check).stdout.strip()


def _source_blob(repo: Path, sha: str, path: str) -> bytes:
    source = repo.resolve()
    commit = _sha(sha, "source commit")
    result = subprocess.run(
        ["git", "-c", f"safe.directory={source}", "-C", str(source), "show", f"{commit}:{path}"],
        stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False, timeout=600,
    )
    if result.returncode:
        raise ReleaseError(f"command failed: git operation (exit={result.returncode})")
    return result.stdout


def _worktree_git(path: Path, *args: str, timeout: int = 600, check: bool = True) -> str:
    return _run(["git", "--no-optional-locks", "-C", str(path), *args],
                timeout=timeout, check=check).stdout.strip()


def _sha(value: Any, label: str) -> str:
    if not isinstance(value, str) or not SHA.fullmatch(value):
        raise ValueError(f"invalid {label}")
    return value


def _digest(value: Any, label: str) -> str:
    if not isinstance(value, str) or not FILE_SHA.fullmatch(value):
        raise ValueError(f"invalid {label}")
    return value


def _utc_now() -> str:
    return datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")


def _utc_string(value: Any, label: str) -> str:
    if not isinstance(value, str):
        raise ReleaseError(f"invalid {label}")
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError as exc:
        raise ReleaseError(f"invalid {label}") from exc
    if parsed.tzinfo is None or parsed.utcoffset() is None:
        raise ReleaseError(f"invalid {label}")
    return value


def _file_sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def _tree(repo: Path, sha: str) -> str:
    value = _git(repo, "rev-parse", "--verify", f"{_sha(sha, 'commit')}^{{tree}}")
    return _sha(value, "tree")


def _resolve_ref(repo: Path, ref: str) -> str:
    return _sha(_git(repo, "rev-parse", "--verify", "--end-of-options", f"{ref}^{{commit}}"), "ref commit")


def _first_parent_chain(repo: Path, base: str, head: str) -> list[str]:
    base, head = _sha(base, "base SHA"), _sha(head, "head SHA")
    result = _run(["git", f"--git-dir={repo}", "merge-base", "--is-ancestor", base, head], check=False)
    if result.returncode != 0:
        raise ReleaseError("candidate does not descend from its declared base")
    raw = _git(repo, "rev-list", "--first-parent", "--reverse", f"{base}..{head}")
    values = raw.splitlines() if raw else []
    parent = base
    for commit in values:
        _sha(commit, "first-parent commit")
        parents = _git(repo, "show", "-s", "--format=%P", commit).split()
        if not parents or parents[0] != parent:
            raise ReleaseError("candidate first-parent chain is not linear from declared base")
        if len(parents) != 1:
            raise ReleaseError("merge commits are not accepted as release candidates; update the development branch")
        parent = commit
    if parent != head:
        raise ReleaseError("candidate is not on the declared first-parent chain")
    return values


def _classify_candidate(repo: Path, base_sha: str, head_sha: str) -> dict[str, Any]:
    """Run source/graph classification with the repository's intended file mask.

    The classifier materializes source and Go graph snapshots. Keep only that
    work under 022 so its generated tracked-source modes remain readable, and
    restore the controller's (normally 0077) mask even if analysis fails.
    """
    previous_umask = os.umask(0o022)
    try:
        return builder.classify(repo, base_sha, head_sha)
    finally:
        os.umask(previous_umask)


def _is_bare_repo(repo: Path) -> bool:
    return not repo.is_symlink() and repo.is_dir() and _git(repo, "rev-parse", "--is-bare-repository") == "true"


def _assert_ref_name(ref: str) -> str:
    if not isinstance(ref, str) or not PUSH_REF.fullmatch(ref) or ".." in ref or "//" in ref or ref.endswith((".", "/", ".lock")):
        raise ValueError("only refs/heads/codex/<task> branches may be submitted")
    _run(["git", "check-ref-format", ref])
    return ref


def _safe_directory(path: Path, *, create: bool = False, mode: int = 0o700) -> None:
    if path.is_symlink():
        raise ReleaseError(f"protected directory is a symlink: {path}")
    if create:
        path.mkdir(mode=mode, parents=True, exist_ok=True)
    info = path.lstat()
    if (not stat.S_ISDIR(info.st_mode) or stat.S_IMODE(info.st_mode) & 0o022
            or info.st_uid != os.geteuid()):
        raise ReleaseError(f"protected directory has unsafe type or permissions: {path}")


def _prepare_new_bare_hooks_directory(path: Path) -> None:
    """Remove only shared-repository write bits from a just-created hooks dir."""
    try:
        info = path.lstat()
    except OSError as exc:
        raise ReleaseError("new bare repository hooks directory is missing or unreadable") from exc
    if stat.S_ISLNK(info.st_mode) or not stat.S_ISDIR(info.st_mode) or info.st_uid != os.geteuid():
        raise ReleaseError("new bare repository hooks directory has unsafe type or owner")
    mode = stat.S_IMODE(info.st_mode)
    safe_mode = mode & ~0o022
    if safe_mode != mode:
        try:
            os.chmod(path, safe_mode)
        except OSError as exc:
            raise ReleaseError("cannot secure new bare repository hooks directory") from exc
    _safe_directory(path)


def _install_new_pre_receive_hook(repo: Path, controller_path: str) -> Path:
    hook = repo / "hooks/pre-receive"
    if hook.exists() or hook.is_symlink():
        raise ReleaseError("unexpected pre-receive hook already exists")
    content = (
        "#!/bin/sh\n"
        f"exec /usr/bin/python3 {shlex.quote(controller_path)} hook-pre-receive "
        f"--repo {shlex.quote(str(repo))}\n"
    )
    try:
        descriptor = os.open(hook, os.O_WRONLY | os.O_CREAT | os.O_EXCL |
                             getattr(os, "O_NOFOLLOW", 0), 0o755)
        with os.fdopen(descriptor, "w", encoding="utf-8") as stream:
            stream.write(content)
            stream.flush()
            os.fsync(stream.fileno())
            os.fchmod(stream.fileno(), 0o755)
    except OSError as exc:
        raise ReleaseError("cannot install the new protected receive hook") from exc
    return hook


def _assert_partial_bootstrap_repository(repo: Path, state_path: Path, lock_path: Path,
                                        expected_sha: str, expected_tree: str,
                                        installed_app_sha: str) -> None:
    if state_path.exists() or state_path.is_symlink():
        raise ReleaseError("partial bootstrap recovery requires the domestic ledger to be absent")
    if lock_path.is_symlink():
        raise ReleaseError("partial bootstrap recovery controller lock is a symlink")
    if lock_path.exists():
        lock_info = lock_path.lstat()
        lock_owner = 0 if repo == Path(DEFAULT_REPO) else os.geteuid()
        if (not stat.S_ISREG(lock_info.st_mode) or lock_info.st_uid != lock_owner
                or stat.S_IMODE(lock_info.st_mode) != 0o600):
            raise ReleaseError("partial bootstrap recovery controller lock is unsafe")
    if repo.is_symlink() or not repo.is_dir():
        raise ReleaseError("partial bootstrap repository is missing or unsafe")
    owner = 0 if repo == Path(DEFAULT_REPO) else os.geteuid()
    repo_info = repo.lstat()
    group = 0 if repo == Path(DEFAULT_REPO) else repo_info.st_gid
    if repo_info.st_uid != owner:
        raise ReleaseError("partial bootstrap repository has an unexpected owner")
    for current, dirs, files in os.walk(repo, topdown=True, followlinks=False):
        directory = Path(current)
        info = directory.lstat()
        if (not stat.S_ISDIR(info.st_mode) or info.st_uid != owner or info.st_gid != group
                or info.st_mode & 0o002):
            raise ReleaseError("partial bootstrap repository contains an unsafe directory")
        for name in dirs:
            child = directory / name
            child_info = child.lstat()
            if (stat.S_ISLNK(child_info.st_mode) or not stat.S_ISDIR(child_info.st_mode)
                    or child_info.st_uid != owner or child_info.st_gid != group
                    or child_info.st_mode & 0o002):
                raise ReleaseError("partial bootstrap repository contains a linked or unowned directory")
        for name in files:
            child = directory / name
            child_info = child.lstat()
            if (stat.S_ISLNK(child_info.st_mode) or not stat.S_ISREG(child_info.st_mode)
                    or child_info.st_uid != owner or child_info.st_gid != group
                    or child_info.st_mode & 0o002):
                raise ReleaseError("partial bootstrap repository contains a linked or unowned file")
    _verify_bare_repository_parent(repo, require_root_owner=(repo == Path(DEFAULT_REPO)))
    if not _is_bare_repo(repo) or _git(repo, "symbolic-ref", "HEAD") != MAIN_REF:
        raise ReleaseError("partial bootstrap repository is not bound to bare main")
    if _git(repo, "config", "--bool", "receive.denyDeletes") != "true":
        raise ReleaseError("partial bootstrap repository does not deny ref deletion")
    if _git(repo, "config", "--bool", "receive.denyNonFastForwards") != "true":
        raise ReleaseError("partial bootstrap repository does not deny non-fast-forward updates")
    if _git(repo, "config", "--get", "core.sharedRepository") != "1":
        raise ReleaseError("partial bootstrap repository is not the expected shared-init repository")
    refs = _git(repo, "for-each-ref", "--format=%(refname)").splitlines()
    if refs != [MAIN_REF] or _resolve_ref(repo, MAIN_REF) != expected_sha:
        raise ReleaseError("partial bootstrap repository refs differ from the reviewed main-only state")
    if _tree(repo, expected_sha) != expected_tree:
        raise ReleaseError("partial bootstrap main tree differs from the reviewed tree")
    if not _is_ancestor(repo, installed_app_sha, expected_sha):
        raise ReleaseError("partial bootstrap main does not descend from the installed application")
    first_parent = _git(repo, "rev-list", "--first-parent", expected_sha).splitlines()
    if installed_app_sha not in first_parent:
        raise ReleaseError("installed application is not on the partial bootstrap first-parent chain")
    if _classify_candidate(repo, installed_app_sha, expected_sha).get("runtime_changed") is not False:
        raise ReleaseError("partial bootstrap main contains application changes beyond the installed app")
    hook = repo / "hooks/pre-receive"
    if hook.exists() or hook.is_symlink():
        raise ReleaseError("partial bootstrap recovery requires the receive hook to be absent")
    checked = _run(["git", f"--git-dir={repo}", "fsck", "--full", "--strict", "--no-reflogs"], check=False)
    if checked.returncode != 0:
        raise ReleaseError("partial bootstrap repository object integrity check failed")


def recover_partial_bootstrap(config: dict[str, Any], expected_sha: str,
                              expected_tree: str, installed_app_sha: str) -> dict[str, Any]:
    """Complete only the reviewed shared-init partial repository; never reseed it."""
    config = _check_config(config)
    expected_sha = _sha(expected_sha, "partial bootstrap main SHA")
    expected_tree = _sha(expected_tree, "partial bootstrap main tree")
    installed_app_sha = _sha(installed_app_sha, "installed application SHA")
    repo, state_path, lock_path = (Path(config[key]) for key in ("repo", "state", "lock"))
    if str(lock_path) != DEFAULT_LOCK:
        raise ReleaseError("partial bootstrap recovery requires the fixed controller lock")
    push_group = grp.getgrnam(config["push_group"])
    push_user = pwd.getpwnam(config["push_user"])
    root_group = grp.getgrgid(0)
    if (push_group.gr_gid == 0 or push_user.pw_uid == 0 or push_user.pw_gid == 0
            or config["push_user"] in root_group.gr_mem):
        raise ReleaseError("partial bootstrap recovery requires a non-root push identity outside root group")
    _assert_partial_bootstrap_repository(repo, state_path, lock_path, expected_sha,
                                        expected_tree, installed_app_sha)
    with _locked(lock_path, nonblocking=True):
        _assert_partial_bootstrap_repository(repo, state_path, lock_path, expected_sha,
                                            expected_tree, installed_app_sha)
        _prepare_new_bare_hooks_directory(repo / "hooks")
        hook = _install_new_pre_receive_hook(repo, config["controller_path"])
        _secure_bare_repository_permissions(repo, grp.getgrnam(config["push_group"]).gr_gid)
        verified = verify_bare_repository(repo, controller_path=config["controller_path"],
                                          push_group=config["push_group"])
        if (verified.get("main_sha") != expected_sha or verified.get("main_tree") != expected_tree
                or hook.is_symlink() or not hook.is_file()):
            raise ReleaseError("partial bootstrap recovery readback differs from the reviewed main")
        checked = _run(["git", f"--git-dir={repo}", "fsck", "--full", "--strict", "--no-reflogs"], check=False)
        if checked.returncode != 0 or state_path.exists() or state_path.is_symlink():
            raise ReleaseError("partial bootstrap recovery did not preserve verified objects and absent ledger")
        return {"status": "partial_bootstrap_recovered", "main_sha": verified["main_sha"],
                "main_tree": verified["main_tree"], "hook": str(hook),
                "ledger_created": False}


def atomic_json(path: Path, value: dict[str, Any], *, mode: int = 0o600) -> None:
    _safe_directory(path.parent, create=True)
    if path.is_symlink() or (path.exists() and not path.is_file()):
        raise ReleaseError(f"ledger path is unsafe: {path}")
    fd, name = tempfile.mkstemp(dir=path.parent, prefix=f".{path.name}.")
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as stream:
            json.dump(value, stream, ensure_ascii=False, sort_keys=True, indent=2)
            stream.write("\n")
            stream.flush()
            os.fsync(stream.fileno())
        os.chmod(name, mode)
        os.replace(name, path)
        directory_fd = os.open(path.parent, os.O_RDONLY)
        try:
            os.fsync(directory_fd)
        finally:
            os.close(directory_fd)
    finally:
        Path(name).unlink(missing_ok=True)


@contextmanager
def _locked(lock_path: Path, *, nonblocking: bool = False) -> Iterator[None]:
    _safe_directory(lock_path.parent, create=True)
    if lock_path.is_symlink() or (lock_path.exists() and not lock_path.is_file()):
        raise ReleaseError("controller lock path is unsafe")
    descriptor = os.open(lock_path, os.O_RDWR | os.O_CREAT | getattr(os, "O_NOFOLLOW", 0), 0o600)
    try:
        with os.fdopen(descriptor, "a+") as stream:
            try:
                fcntl.flock(stream.fileno(), fcntl.LOCK_EX | (fcntl.LOCK_NB if nonblocking else 0))
            except BlockingIOError as exc:
                raise ReleaseError("another domestic release operation holds the serial lock") from exc
            held = getattr(_LOCK_CONTEXT, "paths", [])
            _LOCK_CONTEXT.paths = [*held, str(lock_path.resolve())]
            try:
                yield
            finally:
                _LOCK_CONTEXT.paths = held
    finally:
        # fdopen owns and closes the descriptor on normal exit.
        pass


def _check_config(config: dict[str, Any]) -> dict[str, Any]:
    required = (
        "repo", "state", "lock", "work_root", "source_worktree", "stage_incoming", "stage_helper",
        "prod_host", "prod_user", "prod_key", "prod_known_hosts", "prod_incoming", "prod_helper",
        "push_user", "push_group", "controller_path", "config_path",
    )
    if not isinstance(config, dict) or any(not isinstance(config.get(key), str) or not config[key] for key in required):
        raise ValueError("domestic main release config is incomplete")
    if not Path(config["repo"]).is_absolute() or not Path(config["state"]).is_absolute():
        raise ValueError("repository and state paths must be absolute")
    if config["state"] != DEFAULT_STATE:
        raise ValueError("domestic release state path must match the systemd unit contract")
    if Path(config["prod_known_hosts"]).is_relative_to(Path("/tmp")):
        raise ValueError("production Host Key pin must use a persistent protected known_hosts file")
    if not Path(config["prod_key"]).is_absolute() or not Path(config["prod_known_hosts"]).is_absolute():
        raise ValueError("production SSH identity paths must be absolute")
    if config["repo"] != DEFAULT_REPO or config["controller_path"] != DEFAULT_CONTROLLER or config["config_path"] != DEFAULT_CONFIG:
        raise ValueError("domestic controller paths must match the fixed restricted-command contract")
    if not isinstance(config.get("production_enabled"), bool):
        raise ValueError("production_enabled must be an explicit boolean")
    return config


def load_config(path: Path) -> dict[str, Any]:
    if path.is_symlink() or not path.is_file():
        raise ReleaseError("domestic main release config is missing or unsafe")
    info = path.stat()
    if info.st_uid != 0 or stat.S_IMODE(info.st_mode) & 0o077:
        raise ReleaseError("domestic main release config must be root-owned and mode 0600")
    return _check_config(json.loads(path.read_text(encoding="utf-8")))


def _verify_bare_repository_parent(repo: Path, *, require_root_owner: bool) -> None:
    """Ensure the fixed repository cannot be replaced through its parent."""
    parent = repo.parent
    if parent.is_symlink() or not parent.is_dir():
        raise ReleaseError("domestic bare repository parent is missing or unsafe")
    info = parent.lstat()
    if stat.S_ISLNK(info.st_mode) or not stat.S_ISDIR(info.st_mode):
        raise ReleaseError("domestic bare repository parent is missing or unsafe")
    if info.st_mode & 0o022:
        raise ReleaseError("domestic bare repository parent must not be group/world writable")
    if require_root_owner and info.st_uid != 0:
        raise ReleaseError("domestic bare repository parent must be root-owned before bootstrap/activation")


def bootstrap_bare_repository(repo: Path, seed_repo: Path, baseline_sha: str,
                              *, controller_path: str, push_group: str) -> dict[str, str]:
    """Create a new protected bare repo from one verified baseline commit."""
    baseline_sha = _sha(baseline_sha, "baseline SHA")
    if repo.exists() or repo.is_symlink():
        raise ReleaseError("bare repository path already exists; inspect rather than overwrite")
    if not seed_repo.exists() or seed_repo.is_symlink() or not seed_repo.is_dir():
        raise ReleaseError("seed repository is missing or unsafe")
    _verify_bare_repository_parent(repo, require_root_owner=(repo == Path(DEFAULT_REPO)))
    repo.parent.mkdir(parents=True, exist_ok=True)
    result = _run(["git", "init", "--bare", "--shared=group", str(repo)])
    del result
    _git(repo, "config", "receive.denyDeletes", "true")
    _git(repo, "config", "receive.denyNonFastForwards", "true")
    _git(repo, "config", "core.logAllRefUpdates", "true")
    _run(["git", f"--git-dir={repo}", "fetch", "--no-tags", str(seed_repo), f"{baseline_sha}:{MAIN_REF}"])
    if _resolve_ref(repo, MAIN_REF) != baseline_sha:
        raise ReleaseError("seed baseline does not resolve to the requested domestic main SHA")
    _git(repo, "symbolic-ref", "HEAD", MAIN_REF)
    hooks = repo / "hooks"
    _prepare_new_bare_hooks_directory(hooks)
    _install_new_pre_receive_hook(repo, controller_path)
    # The fixed hook and repository config remain root-owned. The push group
    # can write Git objects and codex refs/reflogs only; receive-pack's hook
    # protects main and hooks from the forced SSH account.
    _secure_bare_repository_permissions(repo, grp.getgrnam(push_group).gr_gid)
    return {"repo": str(repo), "main_sha": baseline_sha, "main_tree": _tree(repo, baseline_sha)}


def _secure_bare_repository_permissions(repo: Path, push_gid: int) -> None:
    if os.geteuid() != 0:
        raise ReleaseError("bare repository ownership setup must run as root")
    # Git stores objects, refs, and reflogs separately. The push account gets
    # group write on objects and codex refs/reflogs; config, main refs/reflogs,
    # packed refs, and hooks remain root-owned and unavailable for modification.
    os.chown(repo, 0, push_gid)
    # Candidate checks run as a separate, untrusted build identity. Git
    # objects are world-readable because this source is public, but only the
    # forced push account's group may write them.
    os.chmod(repo, 0o2755)
    codex_refs = repo / "refs/heads/codex"
    codex_refs.mkdir(mode=0o2775, parents=True, exist_ok=True)
    codex_reflogs = repo / "logs/refs/heads/codex"
    codex_reflogs.mkdir(mode=0o2775, parents=True, exist_ok=True)
    for current, dirs, files in os.walk(repo, topdown=True, followlinks=False):
        current_path = Path(current)
        for name in dirs:
            path = current_path / name
            if stat.S_ISLNK(path.lstat().st_mode) or not stat.S_ISDIR(path.lstat().st_mode):
                raise ReleaseError("bare repository contains a linked or special directory")
            relative = path.relative_to(repo)
            if _push_writable_bare_path(relative):
                os.chown(path, 0, push_gid); os.chmod(path, 0o2775)
            elif relative.parts[:1] in {("hooks",)}:
                os.chown(path, 0, 0); os.chmod(path, 0o755)
            else:
                # In particular refs/heads and refs/domestic remain root-only
                # for writes. The push account cannot move main or candidate pins.
                os.chown(path, 0, 0); os.chmod(path, 0o755)
        for name in files:
            path = current_path / name
            if stat.S_ISLNK(path.lstat().st_mode) or not stat.S_ISREG(path.lstat().st_mode):
                raise ReleaseError("bare repository contains a linked or special file")
            relative = path.relative_to(repo)
            if _push_writable_bare_path(relative):
                os.chown(path, 0, push_gid); os.chmod(path, 0o664)
            elif relative.parts[:1] == ("hooks",):
                os.chown(path, 0, 0); os.chmod(path, 0o755 if path == repo / "hooks/pre-receive" else 0o644)
            else:
                os.chown(path, 0, 0); os.chmod(path, 0o644)
    os.chown(repo / "refs/heads", 0, 0)
    os.chmod(repo / "refs/heads", 0o755)
    os.chown(repo / "refs", 0, 0)
    os.chmod(repo / "refs", 0o755)
    os.chown(codex_refs, 0, push_gid)
    os.chmod(codex_refs, 0o2775)
    os.chown(repo / "config", 0, 0); os.chmod(repo / "config", 0o644)
    os.chown(repo / "hooks", 0, 0); os.chmod(repo / "hooks", 0o755)


def _push_writable_bare_path(relative: Path) -> bool:
    """Only object storage and task refs/reflogs accept push-account writes."""
    parts = relative.parts
    return (parts[:1] == ("objects",)
            or parts[:3] == ("refs", "heads", "codex")
            or parts[:4] == ("logs", "refs", "heads", "codex"))


def verify_bare_repository(repo: Path, *, controller_path: str | None = None,
                          push_group: str | None = None) -> dict[str, Any]:
    _verify_bare_repository_parent(repo, require_root_owner=(repo == Path(DEFAULT_REPO)))
    if not _is_bare_repo(repo):
        raise ReleaseError("domestic source repository is missing, unsafe or not bare")
    if _git(repo, "symbolic-ref", "HEAD") != MAIN_REF:
        raise ReleaseError("domestic bare HEAD must be refs/heads/main")
    if _git(repo, "config", "--bool", "receive.denyDeletes") != "true":
        raise ReleaseError("bare repository must deny ref deletion")
    if _git(repo, "config", "--bool", "receive.denyNonFastForwards") != "true":
        raise ReleaseError("bare repository must deny non-fast-forward updates")
    root_info = repo.lstat()
    config_path = repo / "config"
    config_info = config_path.lstat()
    hooks_info = (repo / "hooks").lstat()
    if (root_info.st_uid != 0 or stat.S_ISLNK(config_info.st_mode) or not stat.S_ISREG(config_info.st_mode)
            or config_info.st_uid != 0 or config_info.st_mode & 0o022
            or stat.S_ISLNK(hooks_info.st_mode) or not stat.S_ISDIR(hooks_info.st_mode)
            or hooks_info.st_uid != 0 or hooks_info.st_mode & 0o022):
        raise ReleaseError("bare repository config and hooks must be root-owned and protected from push users")
    if push_group:
        group = grp.getgrnam(push_group)
        if root_info.st_gid != group.gr_gid:
            raise ReleaseError("bare repository group does not match the configured push group")
        try:
            builder_user = pwd.getpwnam(legacy.BUILD_USER)
            build_groups = {builder_user.pw_gid, *(item.gr_gid for item in grp.getgrall() if legacy.BUILD_USER in item.gr_mem)}
            if group.gr_gid in build_groups:
                raise ReleaseError("isolated build account must not belong to the writable push group")
            readable = _run(["/usr/bin/sudo", "-n", "-u", legacy.BUILD_USER, "--",
                             "/usr/bin/git", "-c", f"safe.directory={repo}", f"--git-dir={repo}",
                             "cat-file", "-e", f"{_resolve_ref(repo, MAIN_REF)}^{{commit}}"],
                            timeout=30, check=False)
            if readable.returncode != 0:
                raise ReleaseError("isolated build account cannot read the protected bare repository")
        except KeyError as exc:
            raise ReleaseError("isolated build account or push group is not provisioned") from exc
    hook = repo / "hooks/pre-receive"
    if hook.is_symlink() or not hook.is_file() or not os.access(hook, os.X_OK):
        raise ReleaseError("protected receive hook is missing")
    info = hook.stat()
    if info.st_uid != 0 or info.st_gid != 0 or info.st_mode & 0o022:
        raise ReleaseError("pre-receive hook must not be group or other writable")
    if controller_path and controller_path not in hook.read_text(encoding="utf-8"):
        raise ReleaseError("pre-receive hook does not invoke the configured controller")
    codex_refs = repo / "refs/heads/codex"
    if push_group:
        group = grp.getgrnam(push_group)
        heads = repo / "refs/heads"
        if heads.lstat().st_uid != 0 or heads.lstat().st_mode & 0o022:
            raise ReleaseError("main ref namespace must be root-owned and not push-writable")
        if codex_refs.is_symlink() or not codex_refs.is_dir():
            raise ReleaseError("protected codex refs directory is missing")
        codex_info = codex_refs.lstat()
        if codex_info.st_uid != 0 or codex_info.st_gid != group.gr_gid or not codex_info.st_mode & 0o020:
            raise ReleaseError("codex ref namespace is not writable by the configured push group")
        main_ref = repo / "refs/heads/main"
        if main_ref.exists() and (main_ref.lstat().st_uid != 0 or main_ref.lstat().st_mode & 0o022):
            raise ReleaseError("domestic main ref is writable by the push account")
    main_sha = _resolve_ref(repo, MAIN_REF)
    return {"repo": str(repo), "bare": True, "main_sha": main_sha, "main_tree": _tree(repo, main_sha),
            "hook": str(hook), "hook_mode": stat.S_IMODE(info.st_mode)}


def validate_receive_updates(repo: Path, input_text: str) -> None:
    """Allow the push-only SSH account to write only codex task branches."""
    if not isinstance(input_text, str) or not input_text.strip():
        raise ReleaseError("receive-pack supplied no ref updates")
    for line in input_text.splitlines():
        parts = line.split()
        if len(parts) != 3:
            raise ReleaseError("malformed receive-pack ref update")
        old, new, ref = parts
        if not SHA.fullmatch(old) or not SHA.fullmatch(new):
            raise ReleaseError("malformed receive-pack SHA")
        if new == ZERO_SHA:
            raise ReleaseError("deleting refs is not allowed through developer push")
        if not PUSH_REF.fullmatch(ref) or ".." in ref or "//" in ref or ref.endswith((".", "/", ".lock")):
            raise ReleaseError("developers may update only refs/heads/codex/<task>")
        _run(["git", "check-ref-format", ref])
        _sha(_git(repo, "rev-parse", "--verify", f"{new}^{{commit}}"), "pushed commit")
        if old != ZERO_SHA:
            _sha(_git(repo, "rev-parse", "--verify", f"{old}^{{commit}}"), "old pushed commit")
            fast_forward = _run(["git", "-c", f"safe.directory={repo}", f"--git-dir={repo}",
                                 "merge-base", "--is-ancestor", old, new], check=False)
            if fast_forward.returncode != 0:
                raise ReleaseError("non-fast-forward developer updates are rejected")


def _load_state(path: Path, *, owner_uid: int = 0) -> dict[str, Any]:
    if path.is_symlink() or not path.is_file():
        raise ReleaseError("domestic main ledger is missing; do not create an empty ledger")
    info = path.stat()
    if info.st_uid != owner_uid or stat.S_IMODE(info.st_mode) & 0o077:
        raise ReleaseError("domestic main ledger must be root-owned and private")
    try:
        state = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise ReleaseError("domestic main ledger is unreadable or corrupt") from exc
    _validate_state(state)
    return state


def _validate_controller_maintenance_marker(state: dict[str, Any]) -> None:
    marker = state.get("controller_maintenance")
    if marker is None:
        return
    required = {"candidate_sha", "candidate_tree", "base_sha", "controller_files",
                "fixed_file_sha256", "check_receipt_sha256", "checked_at_utc"}
    if not isinstance(marker, dict) or set(marker) != required:
        raise ReleaseError("controller maintenance marker is incomplete")
    _sha(marker.get("candidate_sha"), "maintenance candidate SHA")
    _sha(marker.get("candidate_tree"), "maintenance candidate tree")
    _sha(marker.get("base_sha"), "maintenance base SHA")
    _digest(marker.get("check_receipt_sha256"), "maintenance check receipt digest")
    _utc_string(marker.get("checked_at_utc"), "maintenance check timestamp")
    changed = marker.get("controller_files")
    fixed_hashes = marker.get("fixed_file_sha256")
    fixed_files = set(builder.FIXED_CONTROLLER_FILES)
    if (not isinstance(changed, list) or not changed
            or any(not isinstance(path, str) or path not in fixed_files for path in changed)
            or changed != sorted(set(changed))
            or not isinstance(fixed_hashes, dict) or set(fixed_hashes) != fixed_files):
        raise ReleaseError("controller maintenance marker file inventory is invalid")
    for path, digest in fixed_hashes.items():
        if not isinstance(path, str):
            raise ReleaseError("controller maintenance marker file inventory is invalid")
        _digest(digest, "maintenance fixed file digest")
    main = state.get("main")
    if not isinstance(main, dict) or marker["base_sha"] != main.get("sha"):
        raise ReleaseError("controller maintenance marker base differs from domestic main")
    active = _active_queue_item(state)
    if (active is None or active.get("status") not in {"pending", "failed"}
            or active.get("head_sha") != marker["candidate_sha"]
            or active.get("base_sha") != marker["base_sha"]
            or state.get("in_flight") is not None
            or state.get("status") not in {"ready", "blocked"}):
        raise ReleaseError("controller maintenance marker no longer identifies the queue front")


def _validate_state(state: Any) -> None:
    if not isinstance(state, dict) or state.get("schema_version") != SCHEMA_VERSION:
        raise ReleaseError("domestic main ledger has an unsupported schema")
    if state.get("status") not in {"ready", "blocked", "outcome_unknown"}:
        raise ReleaseError("domestic main ledger has an unknown status")
    main = state.get("main")
    app = state.get("installed_app")
    queue = state.get("queue")
    if not isinstance(main, dict) or not isinstance(app, dict) or not isinstance(queue, list):
        raise ReleaseError("domestic main ledger is incomplete")
    _sha(main.get("sha"), "ledger main SHA")
    _sha(main.get("tree"), "ledger main tree")
    _sha(app.get("sha"), "ledger installed app SHA")
    _sha(app.get("tree"), "ledger installed app tree")
    _digest(app.get("manifest_sha256"), "ledger installed manifest digest")
    if state.get("in_flight") is not None and not isinstance(state["in_flight"], dict):
        raise ReleaseError("domestic main ledger in-flight record is invalid")
    if "staging_out_of_sync" in state and not isinstance(state["staging_out_of_sync"], bool):
        raise ReleaseError("staging synchronization marker is invalid")
    if state.get("batch") is not None:
        _batch_tip(state)
        batch = state["batch"]
        app = batch.get("installed_app")
        if not isinstance(app, dict):
            raise ReleaseError("cumulative staging application is missing")
        _sha(app.get("sha"), "cumulative staging application SHA")
        _sha(app.get("tree"), "cumulative staging application tree")
        _digest(app.get("manifest_sha256"), "cumulative staging application manifest")
        if state.get("staging_out_of_sync") is not True:
            raise ReleaseError("cumulative staging must be recorded as ahead of production")
    ack = state.get("archive_ack")
    if ack is not None:
        if not isinstance(ack, dict) or ack.get("observed_by") != "manual_cli":
            raise ReleaseError("manual GitHub archive acknowledgement is invalid")
        _sha(ack.get("sha"), "last manually confirmed GitHub SHA")
        _utc_string(ack.get("confirmed_at_utc"), "archive acknowledgement timestamp")
    ids: set[str] = set()
    for item in queue:
        if not isinstance(item, dict):
            raise ReleaseError("domestic candidate queue is corrupt")
        for key in ("candidate_id", "ref", "head_sha", "base_sha", "status"):
            if not isinstance(item.get(key), str) or not item[key]:
                raise ReleaseError("domestic candidate queue entry is incomplete")
        _assert_ref_name(item["ref"])
        _sha(item["head_sha"], "queued candidate head")
        _sha(item["base_sha"], "queued candidate base")
        if item["candidate_id"] in ids:
            raise ReleaseError("domestic candidate queue repeats an id")
        ids.add(item["candidate_id"])
    inflight = state.get("in_flight")
    if state["status"] == "outcome_unknown" and inflight is None:
        raise ReleaseError("unknown outcome has no durable in-flight identity")
    _validate_controller_maintenance_marker(state)


def _active_queue_item(state: dict[str, Any]) -> dict[str, Any] | None:
    for item in state["queue"]:
        if item.get("status") in {"pending", "stale_base", "failed", "outcome_unknown",
                                  "stage_validation_pending", "source_approval_pending"}:
            return item
    return None


def _batch_tip(state: dict[str, Any]) -> str:
    batch = state.get("batch")
    if batch is None:
        return _sha(state["main"]["sha"], "domestic main SHA")
    if not isinstance(batch, dict) or batch.get("status") not in {"open", "sealed", "promoting"}:
        raise ReleaseError("cumulative staging batch is invalid")
    if batch["status"] == "promoting" and (state.get("status") != "outcome_unknown"
                                             or not isinstance(state.get("in_flight"), dict)):
        raise ReleaseError("promoting batch lost its durable in-flight attempt")
    if batch.get("base_sha") != state["main"]["sha"]:
        raise ReleaseError("cumulative staging base differs from domestic main")
    members = batch.get("members")
    if not isinstance(members, list) or not members:
        raise ReleaseError("cumulative staging batch has no members")
    previous = batch["base_sha"]
    for member in members:
        if not isinstance(member, dict) or member.get("base_sha") != previous:
            raise ReleaseError("cumulative staging member chain is broken")
        previous = _sha(member.get("head_sha"), "staged member head")
    if batch.get("head_sha") != previous:
        raise ReleaseError("cumulative staging tip differs from its members")
    return previous


def _active_queue_position(state: dict[str, Any], item: dict[str, Any]) -> int:
    active = [entry for entry in state["queue"] if entry.get("status") in {
        "pending", "stale_base", "failed", "outcome_unknown",
        "stage_validation_pending", "source_approval_pending",
    }]
    if item not in active:
        raise ReleaseError("candidate is not in the active queue")
    return active.index(item) + 1


def _new_state(main_sha: str, main_tree: str, installed_app: dict[str, str]) -> dict[str, Any]:
    return {
        "schema_version": SCHEMA_VERSION,
        "status": "ready",
        "main": {"sha": _sha(main_sha, "baseline main SHA"), "tree": _sha(main_tree, "baseline main tree")},
        "installed_app": {
            "sha": _sha(installed_app.get("sha"), "baseline app SHA"),
            "tree": _sha(installed_app.get("tree"), "baseline app tree"),
            "manifest_sha256": _digest(installed_app.get("manifest_sha256"), "baseline app manifest"),
        },
        "queue": [],
        "batch": None,
        "in_flight": None,
        "controller_maintenance": None,
        "staging_out_of_sync": False,
        "last_release": None,
        "archive_ack": None,
        "updated_at_utc": _utc_now(),
    }


def _recover_orphaned_inflight(state_path: Path, state: dict[str, Any]) -> str | None:
    """Durably classify a transaction left behind by SIGKILL, timeout, or reboot."""
    inflight = state.get("in_flight")
    if not isinstance(inflight, dict) or state.get("status") == "outcome_unknown":
        return None
    if state.get("status") == "blocked" and inflight.get("phase") == "source-approval-pending":
        item = next((candidate for candidate in state.get("queue", [])
                     if candidate.get("candidate_id") == inflight.get("candidate_id")
                     and candidate.get("head_sha") == inflight.get("head_sha")), None)
        if item is None or item.get("status") != "source_approval_pending":
            raise ReleaseError("source approval pause has no exact queued candidate")
        return None
    if state.get("status") == "blocked" and inflight.get("phase") == "stage-validation-pending":
        item = next((candidate for candidate in state.get("queue", [])
                     if candidate.get("candidate_id") == inflight.get("candidate_id")
                     and candidate.get("head_sha") == inflight.get("head_sha")), None)
        if (inflight.get("stage_install_completed") is not True or item is None
                or item.get("status") != "stage_validation_pending"):
            raise ReleaseError("staged pause has no exact completed installation attempt")
        return None  # Intentional pause. The next poll must supply exact journey evidence.
    item = next((candidate for candidate in state.get("queue", [])
                 if candidate.get("candidate_id") == inflight.get("candidate_id")
                 and candidate.get("head_sha") == inflight.get("head_sha")), None)
    if item is None:
        raise ReleaseError("orphaned in-flight release has no matching queue candidate; remain stopped")
    now = _utc_now()
    if inflight.get("production_install_started") is True or inflight.get("commit_started") is True:
        inflight.update({"phase": "interrupted-production-outcome", "error_type": "InterruptedProcess",
                         "updated_at_utc": now})
        item.update({"status": "outcome_unknown", "failure": {
            "phase": "interrupted-production-outcome", "error_type": "InterruptedProcess",
            "required_recovery": "run read-only reconcile; never reinstall blindly", "recorded_at_utc": now}})
        state.update({"status": "outcome_unknown", "in_flight": inflight, "updated_at_utc": now})
        outcome = "outcome_unknown"
    elif inflight.get("stage_install_started") is True:
        state["staging_out_of_sync"] = True
        item.update({"status": "failed", "failure": {
            "phase": "interrupted-staging-install", "error_type": "InterruptedProcess",
            "required_recovery": "restore the prior verified application and rebuild the synthetic staging database, then run ack-stage-reset",
            "recorded_at_utc": now}})
        inflight.update({"phase": "interrupted-staging-install", "error_type": "InterruptedProcess",
                         "updated_at_utc": now})
        state.update({"status": "blocked", "in_flight": inflight, "updated_at_utc": now})
        outcome = "staging_reset_required"
    else:
        item.update({"status": "failed", "failure": {
            "phase": "interrupted-before-side-effect", "error_type": "InterruptedProcess",
            "required_recovery": "resubmit the exact candidate after reviewing the failed attempt", "recorded_at_utc": now}})
        state.update({"status": "blocked", "in_flight": None, "updated_at_utc": now})
        outcome = "retry_required"
    _validate_state(state)
    atomic_json(state_path, state)
    return outcome


def _candidate_ref(head_sha: str) -> str:
    return CANDIDATE_REF_PREFIX + _sha(head_sha, "candidate SHA")


def _pin_candidate(repo: Path, head_sha: str) -> None:
    ref = _candidate_ref(head_sha)
    existing = _run(["git", f"--git-dir={repo}", "show-ref", "--verify", "--hash", ref], check=False).stdout.strip()
    if existing:
        if existing != head_sha:
            raise ReleaseError("immutable domestic candidate ref points elsewhere")
        return
    _run(["git", "-c", "core.sharedRepository=0", f"--git-dir={repo}",
          "update-ref", ref, head_sha, ZERO_SHA], umask=0o022)


def submit_candidate(repo: Path, state_path: Path, ref: str, head_sha: str, base_sha: str,
                     lock_path: Path | None = None,
                     supersedes_candidate_id: str | None = None) -> dict[str, Any]:
    ref = _assert_ref_name(ref)
    head_sha, base_sha = _sha(head_sha, "candidate head"), _sha(base_sha, "candidate base")
    if lock_path is None:
        raise ReleaseError("submit requires the shared serial lock path")
    with _locked(lock_path, nonblocking=True):
        _is_bare_repo(repo) or (_ for _ in ()).throw(ReleaseError("domestic source repository is unavailable"))
        state = _load_state(state_path)
        _recover_orphaned_inflight(state_path, state)
        inflight = state.get("in_flight")
        if (state.get("status") == "blocked" and isinstance(inflight, dict)
                and inflight.get("phase") in {"source-approval-pending", "stage-validation-pending"}):
            raise ReleaseError("an exact candidate is awaiting human approval; finish it before submitting another")
        if state["status"] == "outcome_unknown":
            raise ReleaseError("production outcome is unknown; reconcile before submitting another candidate")
        batch = state.get("batch")
        if state.get("staging_out_of_sync") is True and batch is None:
            raise ReleaseError("staging differs from the last production version; restore the verified application and synthetic database, then run ack-stage-reset")
        actual_main = _resolve_ref(repo, MAIN_REF)
        expected_base = _batch_tip(state)
        if actual_main != state["main"]["sha"] or base_sha != expected_base:
            raise ReleaseError("candidate base is stale; update and recheck from the current cumulative staging head")
        retry_front = _active_queue_item(state)
        retryable_block = (state["status"] == "blocked" and retry_front is not None
                           and retry_front.get("status") == "failed"
                           and (retry_front.get("ref") == ref
                                or retry_front.get("candidate_id") == supersedes_candidate_id))
        if retryable_block and retry_front.get("failure", {}).get("required_recovery"):
            retryable_block = (state.get("stage_reset_acknowledged_at_utc", "")
                               >= retry_front["failure"]["recorded_at_utc"])
        if batch is not None and (batch["status"] != "open"
                                  or (state["status"] != "ready" and not retryable_block)
                                  or state.get("in_flight") is not None):
            raise ReleaseError("cumulative staging batch is not open for another candidate")
        actual_head = _resolve_ref(repo, ref)
        if actual_head != head_sha:
            raise ReleaseError("submitted head does not match the exact pushed branch head")
        _first_parent_chain(repo, expected_base, head_sha)
        if batch is not None and any(member["ref"] == ref for member in batch["members"]):
            raise ReleaseError("a staged member ref cannot be reused in the same batch")
        _pin_candidate(repo, head_sha)
        entries = state["queue"]
        queue_identity_before = [tuple(item.get(key) for key in ("candidate_id", "ref", "head_sha", "base_sha"))
                                 for item in entries]
        duplicate = next((record for record in entries if record.get("head_sha") == head_sha), None)
        same_ref = next((item for item in entries if item.get("ref") == ref
                         and item.get("status") in {"pending", "stale_base", "failed"}), None)
        same = same_ref
        if duplicate is not None and duplicate is not same:
            raise ReleaseError("candidate SHA is already present in the queue")
        if same is not None:
            previous = same["head_sha"]
            if previous == head_sha and same["base_sha"] == base_sha:
                environment_retry = same.get("failure", {}).get("kind") == "environment"
                if same["status"] in {"pending", "stale_base"} and not environment_retry:
                    return {"status": same["status"], "candidate_id": same["candidate_id"], "head_sha": head_sha,
                            "base_sha": base_sha, "queue_position": _active_queue_position(state, same)}
                # Keep attempts monotonic so every retry gets a fresh evidence directory.
                same.update({"status": "pending", "submitted_at_utc": _utc_now()})
                if not environment_retry:
                    same.pop("failure", None)
                state["in_flight"] = None
                state["status"] = "ready"
                state["updated_at_utc"] = _utc_now()
                atomic_json(state_path, state)
                return {"status": "pending", "candidate_id": same["candidate_id"], "head_sha": head_sha,
                        "base_sha": base_sha, "queue_position": _active_queue_position(state, same)}
            same.update({
                "candidate_id": head_sha,
                "head_sha": head_sha,
                "base_sha": base_sha,
                "status": "pending",
                "submitted_at_utc": _utc_now(),
                "attempt": 0,
                "supersedes_head_sha": previous,
                "last_failure": None,
            })
            same.pop("failure", None)
            item = same
        else:
            blocked = _active_queue_item(state)
            if (state.get("status") == "blocked" and blocked is not None
                    and blocked.get("status") in {"stale_base", "failed"}):
                if supersedes_candidate_id != blocked.get("candidate_id"):
                    raise ReleaseError("replacing a blocked candidate requires its exact --supersedes candidate ID")
                old_ref, old_head = blocked["ref"], blocked["head_sha"]
                blocked.update({"candidate_id": head_sha, "ref": ref, "head_sha": head_sha,
                                "base_sha": base_sha, "status": "pending", "submitted_at_utc": _utc_now(),
                                "attempt": 0, "supersedes_ref": old_ref, "supersedes_head_sha": old_head})
                blocked.pop("failure", None)
                item = blocked
            else:
                if supersedes_candidate_id is not None:
                    raise ReleaseError("--supersedes may name only the first blocked stale or failed candidate")
                item = {"candidate_id": head_sha, "ref": ref, "head_sha": head_sha, "base_sha": base_sha,
                        "status": "pending", "submitted_at_utc": _utc_now(), "attempt": 0}
                entries.append(item)
        queue_identity_after = [tuple(record.get(key) for key in ("candidate_id", "ref", "head_sha", "base_sha"))
                                for record in entries]
        if queue_identity_after != queue_identity_before:
            state["controller_maintenance"] = None
        marker = state.get("controller_maintenance")
        if marker is not None and (marker.get("candidate_sha") != head_sha or marker.get("base_sha") != base_sha):
            state["controller_maintenance"] = None
        state["status"] = "ready"
        state["in_flight"] = None
        state["updated_at_utc"] = _utc_now()
        atomic_json(state_path, state)
        return {"status": item["status"], "candidate_id": item["candidate_id"], "head_sha": head_sha,
                "base_sha": base_sha, "queue_position": _active_queue_position(state, item)}


def _policy_worktree(repo: Path, work_root: Path, base_sha: str, push_group: str) -> Path:
    """Materialize the trusted current-main check policy outside candidate code."""
    if work_root.is_symlink() or not work_root.is_dir():
        raise ReleaseError("trusted policy parent is missing or unsafe")
    parent_info = work_root.lstat()
    if parent_info.st_uid != 0 or parent_info.st_mode & 0o022:
        raise ReleaseError("trusted policy parent must be protected by root ownership")
    root = work_root / "trusted-policy"
    if root.is_symlink():
        raise ReleaseError("trusted policy worktree root is unsafe")
    root.mkdir(mode=0o755, parents=True, exist_ok=True)
    root_info = root.lstat()
    if root_info.st_uid != 0 or root_info.st_mode & 0o022:
        raise ReleaseError("trusted policy worktree root must be protected by root ownership")
    os.chmod(root, 0o711)
    path = root / base_sha
    if path.exists() or path.is_symlink():
        registered = _run(["git", f"--git-dir={repo}", "worktree", "list", "--porcelain"], check=False).stdout
        if path.is_symlink() or not path.is_dir() or str(path.resolve()) not in registered:
            raise ReleaseError("unregistered trusted policy worktree path exists")
        if _worktree_git(path, "rev-parse", "HEAD") != base_sha or _worktree_git(path, "status", "--porcelain"):
            raise ReleaseError("trusted policy worktree differs from its exact base")
        _make_worktree_metadata_readable(repo, path)
        return path
    _run(["git", f"--git-dir={repo}", "worktree", "add", "--detach", str(path), base_sha],
         timeout=120, umask=0o022)
    os.chmod(path, 0o755)
    _make_worktree_metadata_readable(repo, path)
    if _worktree_git(path, "rev-parse", "HEAD") != base_sha or _worktree_git(path, "status", "--porcelain"):
        raise ReleaseError("trusted policy worktree failed its base binding")
    return path


def _make_worktree_metadata_readable(repo: Path, worktree: Path) -> None:
    """Give the isolated builder read access to Git metadata without write access."""
    if os.geteuid() != 0:
        raise ReleaseError("worktree Git metadata permissions must be set by root")
    build_gid = pwd.getpwnam(legacy.BUILD_USER).pw_gid
    metadata = Path(_worktree_git(worktree, "rev-parse", "--absolute-git-dir")).resolve(strict=True)
    expected_root = (repo / "worktrees").resolve(strict=True)
    try:
        metadata.relative_to(expected_root)
    except ValueError as exc:
        raise ReleaseError("candidate worktree Git metadata is outside the protected bare repository") from exc
    os.chown(expected_root, 0, build_gid)
    os.chmod(expected_root, 0o750)
    for current, dirs, files in os.walk(metadata, topdown=True, followlinks=False):
        directory = Path(current)
        if directory.is_symlink() or not stat.S_ISDIR(directory.lstat().st_mode):
            raise ReleaseError("candidate Git metadata contains an unsafe directory")
        os.chown(directory, 0, build_gid)
        os.chmod(directory, 0o750)
        for name in dirs:
            target = directory / name
            if target.is_symlink() or not stat.S_ISDIR(target.lstat().st_mode):
                raise ReleaseError("candidate Git metadata contains an unsafe directory")
        for name in files:
            target = directory / name
            if target.is_symlink() or not stat.S_ISREG(target.lstat().st_mode):
                raise ReleaseError("candidate Git metadata contains an unsafe file")
            os.chown(target, 0, build_gid)
            os.chmod(target, 0o640)


def _check_env(config: dict[str, Any], safe_repository: Path | None = None, *,
               base_sha: str | None = None, head_sha: str | None = None) -> dict[str, str]:
    database_url = config.get("check_database_url")
    if not isinstance(database_url, str) or not database_url:
        raise ReleaseError("isolated synthetic check database URL is not configured")
    from urllib.parse import unquote, urlsplit
    parsed = urlsplit(database_url)
    database = unquote(parsed.path.lstrip("/"))
    if (parsed.scheme not in {"postgres", "postgresql"}
            or parsed.hostname not in {"127.0.0.1", "localhost", "::1"}
            or not (database == "aicrm_ci" or database.startswith("aicrm_test_"))):
        raise ReleaseError("check database must be a local synthetic aicrm_ci/aicrm_test database")
    attempt_database = config.get("_check_attempt_database")
    if attempt_database is not None:
        if not isinstance(attempt_database, str) or not re.fullmatch(
                r"aicrm_test_clone_[0-9a-f]{16}_acceptance_test", attempt_database):
            raise ReleaseError("check attempt database is not a registered synthetic name")
        # Keep the canonical host, role, credentials and options. Only the
        # disposable execution database changes; resume identity stays bound
        # to the configured anchor, never a random attempt name.
        database_url = parsed._replace(path="/" + attempt_database).geturl()
    path_value = config.get("build_path")
    if (not isinstance(path_value, str) or not path_value or "\n" in path_value or "\x00" in path_value
            or any(not part or not Path(part).is_absolute() for part in path_value.split(":"))):
        raise ReleaseError("isolated check PATH is invalid")
    if "/opt/aicrm/toolchain/npm/bin" not in path_value.split(":"):
        raise ReleaseError("isolated check PATH omits the fixed npm toolchain directory")
    build_root = Path(legacy.BUILD_ROOT)
    environment = {
        "PATH": path_value,
        "HOME": str(build_root),
        "TMPDIR": str(build_root / "tmp"),
        "GOCACHE": str(build_root / "cache/go-build"),
        "GOMODCACHE": str(build_root / "cache/go-mod"),
        "npm_config_cache": str(build_root / "cache/npm"),
        "AICRM_DATABASE_URL": database_url,
        "PYTHONDONTWRITEBYTECODE": "1",
        "GIT_CONFIG_COUNT": "1" if safe_repository else "0",
        "GIT_CONFIG_KEY_0": "safe.directory",
        "GIT_CONFIG_VALUE_0": str(safe_repository.resolve(strict=True)) if safe_repository else "",
    }
    verified_profile = config.get("verified_commerce_policy_fingerprint")
    if verified_profile is not None:
        if not isinstance(verified_profile, str) or not FILE_SHA.fullmatch(verified_profile):
            raise ReleaseError("verified commerce policy fingerprint is invalid")
        environment["AICRM_VERIFIED_COMMERCE_POLICY"] = verified_profile
    if config.get("_check_preparation_dir"):
        environment["AICRM_TEST_PREP_DIR"] = str(config["_check_preparation_dir"])
    if config.get("_check_preparation_cache"):
        environment["AICRM_TEST_PREP_CACHE"] = str(config["_check_preparation_cache"])
    if config.get("_check_tmpdir"):
        environment["TMPDIR"] = str(config["_check_tmpdir"])
    if (base_sha is None) != (head_sha is None):
        raise ReleaseError("check environment requires both exact base and head SHAs")
    if base_sha is not None:
        environment.update(AICRM_DEDUP_BASE_SHA=_sha(base_sha, "check base SHA"),
                           AICRM_DEDUP_HEAD_SHA=_sha(head_sha, "check head SHA"))
    return environment


def _verify_build_toolchain(config: dict[str, Any]) -> dict[str, dict[str, str]]:
    """Prove required build tools resolve under the real unprivileged build identity."""
    code = r'''import json, os, pathlib, shutil, stat, subprocess, sys
names = ("go", "node", "npm", "git", "bash")
versions = {"go": ("version",), "node": ("--version",), "npm": ("--version",),
            "git": ("--version",), "bash": ("--version",)}
result = {}
for name in names:
    found = shutil.which(name)
    if not found:
        raise SystemExit(20)
    path = pathlib.Path(found).resolve(strict=True)
    info = path.stat()
    if not stat.S_ISREG(info.st_mode) or not os.access(path, os.X_OK) or info.st_uid != 0 or info.st_mode & 0o022:
        raise SystemExit(21)
    parent = path.parent
    while True:
        parent_info = parent.stat()
        if parent_info.st_uid != 0 or parent_info.st_mode & 0o022:
            raise SystemExit(22)
        if parent == parent.parent:
            break
        parent = parent.parent
    completed = subprocess.run([str(path), *versions[name]], stdout=subprocess.PIPE,
                               stderr=subprocess.STDOUT, text=True, timeout=20, check=False)
    if completed.returncode:
        raise SystemExit(23)
    version = completed.stdout.strip().splitlines()
    if not version:
        raise SystemExit(24)
    result[name] = {"path": str(path), "version": version[0][:160]}
print(json.dumps(result, sort_keys=True, separators=(",", ":")))
'''
    result = _build_command(config, ["/usr/bin/python3", "-c", code], cwd=Path("/"), timeout=120, check=False)
    if result.returncode != 0:
        raise CheckEnvironmentError("required build tools are unavailable or not protected for the isolated build account")
    try:
        payload = json.loads(result.stdout.splitlines()[-1])
    except (IndexError, json.JSONDecodeError) as exc:
        raise ReleaseError("isolated build toolchain probe did not return valid evidence") from exc
    if not isinstance(payload, dict) or set(payload) != set(BUILD_TOOLCHAIN):
        raise ReleaseError("isolated build toolchain evidence is incomplete")
    for name in BUILD_TOOLCHAIN:
        item = payload.get(name)
        if (not isinstance(item, dict) or not isinstance(item.get("path"), str)
                or not Path(item["path"]).is_absolute() or not isinstance(item.get("version"), str)
                or not item["version"]):
            raise ReleaseError("isolated build toolchain evidence is invalid")
    return payload


def _build_command(config: dict[str, Any], command: list[str], *, cwd: Path,
                   input_text: str | None = None, timeout: int = 60 * 60,
                   check: bool = True, safe_repository: Path | None = None,
                   umask: int = -1, base_sha: str | None = None,
                   head_sha: str | None = None) -> subprocess.CompletedProcess[str]:
    _check_storage_mount(config)
    env = _check_env(config, safe_repository, base_sha=base_sha, head_sha=head_sha)
    args = ["/usr/bin/sudo", "-n", "-u", legacy.BUILD_USER, "-H", "--", "/usr/bin/env", "-i"]
    args.extend([f"{key}={value}" for key, value in env.items()])
    args.extend(command)
    return _run(args, cwd=cwd, input_text=input_text, timeout=timeout, check=check,
                umask=umask, terminate_group=True)


def _trusted_runner_code() -> str:
    # Import both the lane runner and dev_preflight from the exact base
    # worktree. Override only their repository root so they execute checks on
    # candidate files while the check-selection and orchestration code stay
    # fixed to current domestic main.
    return """import sys
from pathlib import Path
policy = Path(sys.argv.pop(1))
candidate = Path(sys.argv.pop(1))
sys.path.insert(0, str(policy / 'scripts/ci'))
sys.path.insert(0, str(policy / 'scripts'))
import quality_lanes
quality_lanes.ROOT = candidate
original_commands = quality_lanes.commands
original_focused = quality_lanes.focused_commands
original_run_recorded = quality_lanes.run_recorded

def rewrite(commands):
    result = []
    for command in commands:
        if len(command) > 1 and command[1] == 'scripts/dev_preflight.py':
            wrapper = '''import sys
from pathlib import Path
policy = Path(sys.argv.pop(1))
root = Path(sys.argv.pop(1))
sys.path.insert(0, str(policy / 'scripts'))
import dev_preflight as module
module.ROOT = root
sys.argv = ['dev_preflight', *sys.argv[1:]]
raise SystemExit(module.main())
'''
            result.append([sys.executable, '-c', wrapper, str(policy), str(candidate), *command[2:]])
        else:
            result.append(command)
    return result

def trusted_commands(lane, report_dir):
    return rewrite(original_commands(lane, report_dir))

def trusted_focused(lane, report_dir, checks):
    return rewrite(original_focused(lane, report_dir, checks))

def trusted_run_recorded(command, env, lane, report_dir, execution):
    actual = list(command)
    # Focused Chromium journeys selected in the backend lane require the same
    # explicit opt-in as the dedicated browser lane. Without it, go test exits
    # successfully after skipping the named journey, leaving incomplete checks.
    if (lane == 'backend' and actual[:4] ==
            ['bash', 'scripts/run-go-with-donor-views.sh', 'go', 'test']
            and '-run' in actual and
            'ChromiumJourney' in actual[actual.index('-run') + 1]):
        env = dict(env, AICRM_REQUIRE_CHROMIUM_JOURNEY='1')
    prefix = ['bash', 'scripts/run-go-with-donor-views.sh', 'go', 'test', '-json',
              '-p', '1', '-race', '-count=1']
    if (lane == 'backend' and actual[:len(prefix)] == prefix
            and len(actual) > len(prefix) + 1
            and actual[len(prefix)] == '-timeout=15m'):
        packages = actual[len(prefix) + 1:]
        if all(package == './...' or
               (package.startswith('./') and all(part not in {'', '.', '..', '...'}
                                                   for part in package[2:].split('/')))
               for package in packages):
            actual[len(prefix)] = '-timeout=30m'
    return original_run_recorded(actual, env, lane, report_dir, execution)

quality_lanes.commands = trusted_commands
quality_lanes.focused_commands = trusted_focused
quality_lanes.run_recorded = trusted_run_recorded
sys.argv = ['quality_lanes', *sys.argv[1:]]
raise SystemExit(quality_lanes.main())
"""


def _trusted_preflight_plan(config: dict[str, Any], policy: Path, candidate: Path,
                            base_sha: str, head_sha: str) -> dict[str, Any]:
    code = (
        "import sys,json; from pathlib import Path; "
        "policy=Path(sys.argv[1]); root=Path(sys.argv[2]); base=sys.argv[3]; head=sys.argv[4]; "
        "sys.path.insert(0,str(policy/'scripts/ci')); import affected_plan; "
        "print(json.dumps(affected_plan.build_plan(root,base,head),sort_keys=True,separators=(',',':')))"
    )
    # Graph snapshots and generated donor views must retain tracked 100644
    # source modes. Limit the permissive umask to this isolated read-only plan.
    result = _build_command(config, ["/usr/bin/python3", "-c", code, str(policy), str(candidate), base_sha, head_sha],
                            cwd=policy, timeout=900, check=False, safe_repository=candidate,
                            umask=0o022)
    if result.returncode != 0:
        raise ReleaseError("trusted impact analysis failed; candidate is held for full review")
    try:
        plan = json.loads(result.stdout.splitlines()[-1])
    except (IndexError, json.JSONDecodeError) as exc:
        raise ReleaseError("trusted impact analysis did not return a valid plan") from exc
    enforced = plan.get("enforced") if isinstance(plan, dict) else None
    lanes = enforced.get("selected_lanes") if isinstance(enforced, dict) else None
    source = plan.get("selection_source")
    legacy_transition = (source == "trusted-baseline-registry-and-go-test-graph"
                         and plan.get("policy_changed") is True
                         and isinstance(enforced, dict)
                         and enforced.get("selection_mode") == "full"
                         and lanes == list(builder_ci_lanes())
                         and plan.get("graph_result", {}).get("graph_valid") is True)
    if (source not in {"trusted-base-behavior-and-go-graph", "trusted-baseline-registry-and-go-test-graph"}
            or (source != "trusted-base-behavior-and-go-graph" and not legacy_transition)
            or plan.get("baseline_sha") != base_sha or plan.get("head_sha") != head_sha
            or plan.get("head_tree") != _worktree_git(candidate, "rev-parse", "HEAD^{tree}")
            or plan.get("source_clean") is not True or plan.get("source", {}).get("head_matches") is not True
            or plan.get("source", {}).get("status") != []
            or (plan.get("evidence_eligible") is not True and not legacy_transition)
            or not isinstance(lanes, list) or not lanes
            or any(lane not in builder_ci_lanes() for lane in lanes)):
        raise ReleaseError("trusted impact plan failed exact source or lane validation")
    return plan


def _check_policy_changes(repo: Path, base_sha: str, head_sha: str) -> list[str]:
    """Do not let a candidate edit the checker that certifies that candidate."""
    result = _run(["git", f"--git-dir={repo}", "diff", "--no-renames", "--name-only", base_sha, head_sha])
    paths = [line for line in result.stdout.splitlines() if line]
    fixed_policy_files = {
        ".github/workflows/ci.yml", "scripts/dev_preflight.py",
        "scripts/ci/affected_plan.py", "scripts/ci/impact_selection.py",
        "scripts/ci/governance_impact.py", "scripts/ci/quality_lanes.py",
        "scripts/ci/affected_shadow.py", "scripts/ci/go_affected_graph.py",
        "scripts/ci/verification.py", "docs/governance/capability-impact.json",
    }
    return sorted(path for path in paths if path in fixed_policy_files or path.startswith((".github/workflows/", "scripts/ci/")))


def builder_ci_lanes() -> tuple[str, ...]:
    # Kept in the installed controller so a candidate cannot edit its own lane
    # allow-list through scripts/ci/quality_lanes.py.
    return ("preflight", "backend", "frontend", "browser", "archive-sdk")


def _enforced_lanes(plan: dict[str, Any], changed_policy: list[str]) -> tuple[dict[str, Any], list[str], list[dict[str, Any]], list[str], str]:
    enforced = plan.get("enforced") if isinstance(plan, dict) else None
    if not isinstance(enforced, dict):
        raise ReleaseError("trusted impact plan has no enforced selection")
    lanes = enforced.get("selected_lanes")
    checks = enforced.get("selected_checks", [])
    packages = [] if enforced.get("profile") == "behavior" else plan.get("candidate_go_packages", [])
    profile = enforced.get("profile", "full")
    if not isinstance(lanes, list) or not lanes or any(lane not in builder_ci_lanes() for lane in lanes):
        raise ReleaseError("trusted impact plan selected an invalid lane set")
    if changed_policy and not ((profile == "tooling" and lanes == ["preflight"])
                               or (plan.get("selection_source") == "trusted-base-behavior-and-go-graph"
                                   and profile == "full" and enforced.get("selection_mode") == "full"
                                   and "policy-and-runtime-changed" in enforced.get("selection_reasons", []))
                               or (plan.get("selection_source") == "trusted-baseline-registry-and-go-test-graph"
                                   and profile == "full" and tuple(lanes) == builder_ci_lanes())):
        raise ReleaseError("trusted base policy did not select the release-tool contracts")
    if not isinstance(checks, list) or not isinstance(packages, list):
        raise ReleaseError("trusted impact plan contains an invalid focused test set")
    return enforced, lanes, checks, packages, profile


def _private_check_checkout(config: dict[str, Any], repo: Path, checkout: Path,
                            head_sha: str) -> Path:
    """Clone the exact candidate into the unprivileged, disposable check workspace."""
    build_user = legacy.BUILD_USER
    build_info = pwd.getpwnam(build_user)
    parent = checkout.parent
    parent_info = parent.lstat()
    if (parent.is_symlink() or not stat.S_ISDIR(parent_info.st_mode)
            or parent_info.st_uid != build_info.pw_uid
            or stat.S_IMODE(parent_info.st_mode) != 0o700):
        raise ReleaseError("private check workspace parent is not a build-user-only directory")
    if checkout.exists() or checkout.is_symlink():
        raise ReleaseError("private check checkout path already exists")
    source = repo.resolve(strict=True)
    commands = [
        (["git", "-c", f"safe.directory={source}", "clone", "-q", "--no-checkout",
          "--local", "--no-hardlinks", str(source), str(checkout)], source),
        (["git", "-C", str(checkout), "-c", f"safe.directory={source}", "fetch",
          "-q", "--no-tags", str(source), head_sha], checkout),
        (["git", "-C", str(checkout), "checkout", "--detach", "--force", head_sha], checkout),
    ]
    for command, safe_repository in commands:
        result = _build_command(config, command, cwd=Path("/"), timeout=600,
                                check=False, safe_repository=safe_repository, umask=0o022)
        if result.returncode:
            raise ReleaseError("isolated check candidate checkout failed")
    try:
        info = checkout.lstat()
    except OSError as exc:
        raise ReleaseError("isolated check candidate checkout is missing") from exc
    if (checkout.is_symlink() or not stat.S_ISDIR(info.st_mode)
            or info.st_uid != build_info.pw_uid):
        raise ReleaseError("isolated check candidate checkout has an unexpected owner")
    os.chmod(checkout, 0o700)
    _verify_check_checkout_tree(repo, head_sha, checkout)
    return checkout


def _verify_check_checkout_tree(repo: Path, head_sha: str, checkout: Path) -> int:
    """Compare tracked files to protected bare-tree blobs without trusting checkout Git metadata."""
    head_sha = _sha(head_sha, "check checkout SHA")
    try:
        root_info = checkout.lstat()
        build_uid = pwd.getpwnam(legacy.BUILD_USER).pw_uid
    except (OSError, KeyError) as exc:
        raise ReleaseError("isolated check candidate checkout is unavailable") from exc
    if (checkout.is_symlink() or not stat.S_ISDIR(root_info.st_mode)
            or root_info.st_uid != build_uid):
        raise ReleaseError("isolated check candidate checkout has an unsafe root")
    tree_result = subprocess.run(
        ["git", f"--git-dir={repo}", "ls-tree", "-rz", "--full-tree", "-r", head_sha],
        stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False,
    )
    if tree_result.returncode:
        raise ReleaseError("protected candidate tree could not be enumerated")
    try:
        root_fd = os.open(os.fsencode(checkout), os.O_RDONLY | os.O_DIRECTORY | getattr(os, "O_NOFOLLOW", 0))
    except OSError as exc:
        raise ReleaseError("isolated check candidate checkout root is not safely accessible") from exc
    count = 0
    try:
        for record in tree_result.stdout.split(b"\0"):
            if not record:
                continue
            try:
                header, raw_path = record.split(b"\t", 1)
                raw_mode, object_type, expected_oid = header.split(b" ")
            except ValueError as exc:
                raise ReleaseError("protected candidate tree entry is malformed") from exc
            parts = raw_path.split(b"/")
            if (not raw_path or raw_path.startswith(b"/") or b".git" in parts
                    or any(part in {b"", b".", b".."} for part in parts)):
                raise ReleaseError("protected candidate tree contains an unsafe path")
            if object_type != b"blob" or raw_mode not in {b"100644", b"100755", b"120000"}:
                raise ReleaseError("protected candidate tree contains an unsupported file type")
            parent_fd = os.dup(root_fd)
            try:
                for part in parts[:-1]:
                    next_fd = os.open(part, os.O_RDONLY | os.O_DIRECTORY |
                                      getattr(os, "O_NOFOLLOW", 0), dir_fd=parent_fd)
                    os.close(parent_fd)
                    parent_fd = next_fd
                leaf = parts[-1]
                if raw_mode == b"120000":
                    info = os.stat(leaf, dir_fd=parent_fd, follow_symlinks=False)
                    if not stat.S_ISLNK(info.st_mode) or info.st_uid != build_uid:
                        raise ReleaseError("tracked source symlink changed type")
                    target = os.readlink(leaf, dir_fd=parent_fd)
                    target_bytes = target if isinstance(target, bytes) else os.fsencode(target)
                    actual_oid = hashlib.sha1(f"blob {len(target_bytes)}\0".encode() + target_bytes).hexdigest().encode()
                else:
                    descriptor = os.open(leaf, os.O_RDONLY | os.O_NONBLOCK |
                                         getattr(os, "O_NOFOLLOW", 0),
                                         dir_fd=parent_fd)
                    try:
                        info = os.fstat(descriptor)
                        if (not stat.S_ISREG(info.st_mode) or info.st_nlink != 1
                                or info.st_uid != build_uid):
                            raise ReleaseError("tracked source file changed type or link count")
                        executable = bool(info.st_mode & 0o111)
                        if executable != (raw_mode == b"100755"):
                            raise ReleaseError("tracked source file mode changed")
                        digest = hashlib.sha1(f"blob {info.st_size}\0".encode())
                        while chunk := os.read(descriptor, 1024 * 1024):
                            digest.update(chunk)
                        actual_oid = digest.hexdigest().encode()
                    finally:
                        os.close(descriptor)
                if actual_oid != expected_oid:
                    raise ReleaseError("tracked source bytes differ from the protected candidate tree")
                count += 1
            except OSError as exc:
                raise ReleaseError("tracked source path is missing or traverses an unsafe parent") from exc
            finally:
                os.close(parent_fd)
    finally:
        os.close(root_fd)
    return count


def _lane_test_log_name(receipt: dict[str, Any]) -> str | None:
    name = receipt.get("go_json_log")
    if name is None and receipt.get("lane") == "browser":
        # The old trusted runner writes this fixed log without naming it in
        # run.json. Keep its receipt intact and validate the actual evidence.
        name = "browser-execution.log"
    return name if isinstance(name, str) and Path(name).name == name else None


def _verify_lane_evidence(lane_dir: Path, lane: str, head_sha: str, tree: str,
                          checks: list[dict[str, Any]], packages: list[str]) -> dict[str, Any]:
    try:
        receipt = json.loads((lane_dir / "run.json").read_text())
    except (OSError, ValueError) as exc:
        raise CheckIncompleteError("trusted lane evidence is missing: " + lane) from exc
    if (receipt.get("lane") != lane or receipt.get("result") != "success"
            or receipt.get("exit_code") != 0 or receipt.get("tested_sha") != head_sha
            or receipt.get("tree") != tree or not receipt.get("commands")):
        raise CheckIncompleteError("trusted lane evidence failed exact identity or result validation: " + lane)
    if "required_commands" in receipt:
        results = receipt.get("command_results", [])
        if (receipt["required_commands"] != len(receipt["commands"])
                or len(results) != receipt["required_commands"]
                or any(result.get("exit_code") != 0 or result.get("command") != command
                       for result, command in zip(results, receipt["commands"]))):
            raise CheckIncompleteError("required check command evidence is missing or failed: " + lane)
    required_names = {check["test"] for check in checks if check.get("lane") == "browser" and check.get("test")}
    if lane in {"backend", "browser"}:
        name = _lane_test_log_name(receipt)
        if name is None:
            raise CheckIncompleteError("trusted lane test evidence is missing: " + lane)
        try:
            events = []
            for line in (lane_dir / name).read_text().splitlines():
                try:
                    value = json.loads(line)
                except ValueError:
                    continue
                if isinstance(value, dict):
                    events.append(value)
        except OSError as exc:
            raise CheckIncompleteError("trusted lane test log is absent: " + lane) from exc
        if not events or any(event.get("Action") == "fail" for event in events):
            raise CheckIncompleteError("trusted lane test evidence is empty or failed: " + lane)
        if lane == "backend" and packages:
            if "required_go_packages" in receipt and receipt["required_go_packages"] != sorted(set(packages)):
                raise CheckIncompleteError("required backend package inventory differs from trusted discovery")
            terminal = {event.get("Package"): event.get("Action") for event in events
                        if not event.get("Test") and event.get("Action") in {"pass", "skip", "fail"}}
            no_tests = {event.get("Package") for event in events
                        if event.get("Action") == "output" and "[no test files]" in event.get("Output", "")}
            if any(terminal.get(package) != "pass" and not
                   (terminal.get(package) == "skip" and package in no_tests) for package in packages):
                raise CheckIncompleteError("required affected package was missing or skipped")
        if lane == "backend":
            required_backend = {(str(Path(check["path"]).parent), check["test"])
                                for check in checks if check.get("lane") == "backend" and check.get("test")}
            terminal_tests = {(event.get("Package", ""), event.get("Test")): event.get("Action")
                              for event in events if event.get("Test") and event.get("Action") in {"pass", "skip", "fail"}}
            if any(not any(package.endswith("/" + directory) and test == name and action == "pass"
                           for (package, test), action in terminal_tests.items())
                   for directory, name in required_backend):
                raise CheckIncompleteError("required named Go test was missing, skipped or failed")
        if lane == "browser":
            try:
                summary = json.loads((lane_dir / "summary.json").read_text())
                discovered = summary.get("required_browser_tests")
            except (OSError, ValueError) as exc:
                raise CheckIncompleteError("browser discovery evidence is absent") from exc
            if not isinstance(discovered, list) or not discovered or any(not isinstance(name, str) for name in discovered):
                raise CheckIncompleteError("browser discovery evidence is empty")
            required_names.update(discovered)
            terminal = {event.get("Test"): event.get("Action") for event in events
                        if event.get("Action") in {"pass", "skip", "fail"} and event.get("Test")}
            if required_names and any(terminal.get(name) != "pass" for name in required_names):
                raise CheckIncompleteError("required Chromium journey was missing or skipped")
            if any(value == "skip" for value in terminal.values()):
                raise CheckIncompleteError("required browser lane contains a skipped journey")
    return receipt


def _lane_test_events(lane_dir: Path, receipt: dict[str, Any]) -> list[dict[str, Any]]:
    name = _lane_test_log_name(receipt)
    if name is None:
        return []
    try:
        values = []
        for line in (lane_dir / name).read_text().splitlines():
            try:
                event = json.loads(line)
            except ValueError:
                continue
            if isinstance(event, dict):
                values.append(event)
        return values
    except OSError:
        return []


def _environment_lane_failure(receipt: dict[str, Any], events: list[dict[str, Any]]) -> bool:
    """Only explicit prerequisite failures or known infrastructure errors qualify.

    An assertion failure, HTTP permission failure, timeout, or unknown error
    must never turn into a resumable environment failure by keyword alone.
    """
    if receipt.get("failure_kind") == "environment" and not events:
        return True
    failed = {event["Test"] for event in events if event.get("Test")
              and "/" not in event["Test"] and event.get("Action") == "fail"}
    if not failed:
        return False
    classified = set()
    for event in events:
        name, output = event.get("Test", ""), event.get("Output", "")
        if name not in failed or not isinstance(output, str):
            continue
        if (name == "TestPostgreSQLMediaRefreshChromiumJourney"
                and 'permission denied to set parameter "session_replication_role"' in output
                and "42501" in output):
            classified.add(name)
        if (name.endswith("ChromiumJourney") and
                (re.search(r"spawn (?:chromium|chromium-browser|google-chrome(?:-stable)?) ENOENT", output)
                 or "Chromium binary is unavailable" in output)):
            classified.add(name)
    return classified == failed


def _sanitized_test_events(events: list[dict[str, Any]]) -> list[dict[str, Any]]:
    # Preserve test evidence without copying fixture logs or credentials into
    # the durable controller ledger. No-test package markers are significant.
    result = []
    for event in events:
        if event.get("Action") in {"pass", "skip", "fail"}:
            result.append({key: event[key] for key in ("Action", "Package", "Test", "Elapsed") if key in event})
        elif event.get("Action") == "output" and "[no test files]" in event.get("Output", ""):
            result.append({"Action": "output", "Package": event.get("Package"), "Output": "[no test files]"})
    return result


def _passed_go_packages(events: list[dict[str, Any]], required: list[str]) -> list[str]:
    terminal = {event.get("Package"): event.get("Action") for event in events
                if not event.get("Test") and event.get("Action") in {"pass", "skip", "fail"}}
    no_tests = {event.get("Package") for event in events
                if event.get("Action") == "output" and "[no test files]" in event.get("Output", "")}
    failed = {event.get("Package") for event in events if event.get("Action") == "fail"}
    return sorted(package for package in required if package not in failed and
                  (terminal.get(package) == "pass" or
                   (terminal.get(package) == "skip" and package in no_tests)))


def _candidate_lane_failure(receipt: dict, events: list[dict]) -> bool:
    if any(re.search(r"test timed out|signal: (?:killed|terminated)|panic:.*timeout", event.get("Output", ""))
           for event in events):
        return False
    if any(event.get("Test") and event.get("Action") == "fail" for event in events):
        return True
    if any("[build failed]" in event.get("Output", "") for event in events):
        return True
    # Setup exits and missing receipts alone do not prove a code regression.
    for result in receipt.get("command_results", []):
        command = result.get("command", [])
        if result.get("exit_code") == 1 and ("unittest" in command or
                (command and command[0] == "node" and any("test" in Path(value).name for value in command[1:]))):
            return True
    return False


def _merge_backend_continuation(lane_dir: Path, snapshot: dict, receipt: dict,
                                required: list[str]) -> None:
    if receipt.get("required_go_packages") != required or snapshot.get("required_go_packages") != required:
        raise CheckIncompleteError("continued backend package inventory changed")
    passed = _passed_go_packages(snapshot["events"], required)
    if receipt.get("reused_go_packages") != passed:
        raise CheckIncompleteError("continued backend package reuse evidence differs")
    prior = [event for event in snapshot["events"] if event.get("Package") in passed]
    events = _sanitized_test_events(_lane_test_events(lane_dir, receipt))
    if any(event.get("Package") in passed for event in events):
        raise CheckIncompleteError("continued backend reran a package recorded as reused")
    receipt.update(go_json_log="continued-tests.jsonl", reused_from=snapshot["result"]["log_path"])
    (lane_dir / "continued-tests.jsonl").write_text("".join(json.dumps(event) + "\n" for event in [*prior, *events]))
    (lane_dir / "run.json").write_text(json.dumps(receipt))


def _check_database_identity(config: dict) -> dict:
    from urllib.parse import urlsplit, unquote
    database = urlsplit(config["check_database_url"])
    return {"host":database.hostname,"port":database.port or 5432,
            "role":unquote(database.username or ""),"name":unquote(database.path),
            "options_sha256":hashlib.sha256(database.query.encode()).hexdigest()}


def _check_resume_identity(config: dict[str, Any], plan: dict[str, Any], toolchain: dict,
                           enforced: dict, lanes: list[str], checks: list, packages: list,
                           profile: str) -> dict[str, Any]:
    return {"baseline_sha": plan["baseline_sha"], "head_sha": plan["head_sha"],
            "head_tree": plan["head_tree"], "policy_fingerprint": plan["policy_fingerprint"],
            "controller_sha256": _file_sha256(Path(__file__)),
            "lanes": lanes, "checks": checks, "packages": packages, "profile": profile,
            "required_go_packages": config.get("_required_go_packages", []),
            "selection_mode": enforced["selection_mode"],
            "host": list(os.uname()), "build_user": legacy.BUILD_USER,
            "database":_check_database_identity(config),
            "tools": {name: value if name != "chromium" else {"version": value["version"]}
                      for name, value in toolchain.items()}}


def _load_check_checkpoint(path: Path, diagnostic_root: Path, identity: dict) -> dict:
    # Only the controller's protected capsule can certify an earlier attempt.
    # Build-owned run.json files alone cannot authorize reuse on a later run.
    if path.parent != diagnostic_root or not path.name.endswith("-checkpoint.json"):
        raise ReleaseError("check continuation checkpoint is outside protected diagnostics")
    _safe_directory(diagnostic_root)
    descriptor = os.open(path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
    try:
        info = os.fstat(descriptor)
        if (not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid()
                or info.st_nlink != 1 or stat.S_IMODE(info.st_mode) != 0o600):
            raise ReleaseError("check continuation checkpoint is not protected")
        with os.fdopen(descriptor, "r", closefd=False) as stream:
            value = json.load(stream)
    finally:
        os.close(descriptor)
    if (value.get("schema") != 1 or value.get("identity") != identity or
            not (value.get("failure_kind") == "environment" or
                 (value.get("result") == "passed" and value.get("failure_kind") is None))):
        return {}  # Different code, policy, scope, or runtime gets a fresh run.
    for snapshot in value.get("lanes", {}).values():
        result = snapshot["result"]
        log = Path(result["log_path"])
        if log.parent != diagnostic_root or log.is_symlink():
            raise ReleaseError("continued check diagnostic is outside protected diagnostics")
        info = log.lstat()
        if (info.st_uid != os.geteuid() or not stat.S_ISREG(info.st_mode)
                or info.st_nlink != 1 or stat.S_IMODE(info.st_mode) != 0o600
                or _file_sha256(log) != result["log_sha256"]):
            raise ReleaseError("continued check diagnostic hash or ownership changed")
    return value


def _restore_check_snapshot(lane_dir: Path, snapshot: dict) -> None:
    receipt = dict(snapshot["receipt"])
    receipt.update(reused_from=snapshot["result"]["log_path"], go_json_log="continued-tests.jsonl")
    (lane_dir / "continued-tests.jsonl").write_text("".join(json.dumps(event) + "\n" for event in snapshot["events"]))
    if snapshot.get("required_browser_tests"):
        (lane_dir / "summary.json").write_text(json.dumps({"required_browser_tests": snapshot["required_browser_tests"]}))
    (lane_dir / "run.json").write_text(json.dumps(receipt))
    (lane_dir / "progress.json").write_text(json.dumps({"lane": receipt["lane"], "status": "reused",
                                                       "reused_from": receipt["reused_from"]}))


def _merge_browser_continuation(lane_dir: Path, snapshot: dict, receipt: dict) -> None:
    prior = [event for event in snapshot["events"] if event.get("Test")
             and event.get("Action") == "pass"]
    events = _sanitized_test_events(_lane_test_events(lane_dir, receipt))
    receipt.update(go_json_log="continued-tests.jsonl", reused_from=snapshot["result"]["log_path"],
                   reused_browser_tests=sorted({event["Test"] for event in prior if "/" not in event["Test"]}))
    (lane_dir / "continued-tests.jsonl").write_text("".join(json.dumps(event) + "\n" for event in [*prior, *events]))
    (lane_dir / "summary.json").write_text(json.dumps({"required_browser_tests": snapshot["required_browser_tests"]}))
    (lane_dir / "run.json").write_text(json.dumps(receipt))


def _run_check_process(args: list[str], *, cwd: Path, output, timeout: int) -> subprocess.CompletedProcess:
    process = subprocess.Popen(args, cwd=cwd, stdout=output, stderr=subprocess.STDOUT, start_new_session=True)
    try:
        code = process.wait(timeout=timeout)
        return subprocess.CompletedProcess(args, code)
    except BaseException:
        try:
            os.killpg(process.pid, signal.SIGTERM)
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                os.killpg(process.pid, signal.SIGKILL)
                process.wait(timeout=5)
        except ProcessLookupError:
            pass
        raise


def _lane_check_profile(enforced: dict[str, Any], profile: str) -> str:
    if (enforced.get("selection_mode") == "targeted" and profile == "affected-packages"
            and "public-commerce-v1" in enforced.get("selection_reasons", [])):
        return "public-commerce-v1"
    return "tooling" if profile == "tooling" else "full"


def _browser_needs_npm(config: dict[str, Any], policy: Path, checkout: Path,
                       backend_checkout: Path, checks: list[dict[str, Any]]) -> bool:
    # Import only protected policy, never candidate Python. Missing/old policy
    # and uncertain dependency inputs retain the existing complete preparation.
    config["_browser_dependency_evidence"] = {"needs_npm": True, "reason": "dependency inspection unavailable"}
    code = ("import sys,json; from pathlib import Path; "
            "sys.path.insert(0,str(Path(sys.argv[1])/'scripts/ci')); "
            "import commerce_checks; print(json.dumps(commerce_checks.browser_npm_inputs(json.loads(sys.argv[3]))))")
    result = _run(["/usr/bin/python3", "-c", code, str(policy), str(checkout),
                   json.dumps(checks)], cwd=policy, check=False)
    try:
        inputs = json.loads(result.stdout)
    except (ValueError, TypeError):
        return True
    if result.returncode or not isinstance(inputs, list) or not inputs:
        return True
    parser = policy / "scripts/ci/browser_npm_dependencies.mjs"
    if not parser.is_file() or parser.is_symlink():
        return True
    try:
        result = _build_command(config, ["node", str(parser), "--root", str(checkout),
            "--compiler-root", str(backend_checkout), "--entries-json", json.dumps(inputs)],
            cwd=checkout, safe_repository=checkout, timeout=60, check=False)
    except (OSError, subprocess.SubprocessError):
        return True
    try:
        proof = json.loads(result.stdout)
    except (ValueError, TypeError):
        return True
    needs_npm = result.returncode != 0 or not isinstance(proof, dict) or proof.get("needs_npm") is not False
    if isinstance(proof, dict):
        config["_browser_dependency_evidence"] = {**proof, "needs_npm": needs_npm,
            "parser_sha256": _file_sha256(parser), "exit_code": result.returncode}
    return needs_npm


def _run_check_lanes(config: dict[str, Any], repo: Path, policy: Path,
                     execution_worktree: Path, report_dir: Path,
                     base_sha: str, head_sha: str, enforced: dict[str, Any],
                     lanes: list[str], checks: list[dict[str, Any]], packages: list[str],
                     profile: str, diagnostic_root: Path) -> list[dict[str, Any]]:
    tree = _tree(repo, head_sha)  # Protected authority; never trust private clone Git metadata.
    prior = config.get("_check_checkpoint", {}).get("lanes", {})
    snapshots = config.setdefault("_check_snapshots", {})
    guard = threading.Lock()
    backend_running = threading.Event()
    if "backend" not in lanes:
        backend_running.set()

    def execute_lane(lane: str) -> dict[str, Any]:
        # t.Chdir and generated artifacts may mutate a checkout. Never share it
        # between concurrently executing lanes, even when source SHA is equal.
        snapshot = prior.get(lane, {})
        lane_dir = report_dir / lane
        _build_command(config, ["/usr/bin/mkdir", "-m", "0700", str(lane_dir)], cwd=execution_worktree,
                       timeout=30, safe_repository=execution_worktree)
        lane_checks = [check for check in checks if check.get("lane") == lane]
        lane_packages = (config.get("_required_go_packages") or
                         (packages if enforced.get("selection_mode") == "targeted" else [])) if lane == "backend" else []
        if snapshot.get("status") == "passed":
            _restore_check_snapshot(lane_dir, snapshot)
            _verify_lane_evidence(lane_dir, lane, head_sha, tree, lane_checks, lane_packages)
            with guard:
                snapshots[lane] = snapshot
            return dict(snapshot["result"], reused=True, duration_seconds=0,
                        reused_duration_seconds=snapshot["result"].get("duration_seconds"))
        checkout = (execution_worktree if lane == "preflight" else
                    _private_check_checkout(config, repo, execution_worktree.parent / ("candidate-" + lane), head_sha))
        # Package discovery and each lane use separate clean checkouts. Embed
        # views prepared for discovery are therefore absent from the lane copy.
        _prepare_exact_source_views(config, policy, checkout)
        preparation_helper = (policy / "scripts/ci/check_preparation.py").is_file()
        # The canonical backend stage prepares both dependency trees itself.
        # Pre-installing them here makes stage rehash the same writable copies.
        backend_stages_dependencies = preparation_helper and (
            enforced.get("selection_mode") == "full" or bool(packages))
        # Let backend compile/link its first test binary before Chromium takes
        # the shared heavy slot. Browser dependency preparation uses that slot
        # too, so it must also wait rather than block the first backend link.
        # This schedules work only: all mandatory browser assertions still run
        # if backend finishes without starting a test (including failure).
        waiting = time.monotonic()
        if lane == "browser":
            backend_running.wait()
        scheduler_wait = time.monotonic() - waiting
        dependency_preparation = "not-applicable"
        if lane in {"frontend", "browser"} or (lane == "backend" and not backend_stages_dependencies):
            needs_dependencies = not (lane == "browser" and preparation_helper
                # Continued backend commands may reuse stage evidence while
                # its old disposable artifact has already been reclaimed.
                and "backend" in lanes and not prior.get("backend")
                and any(path.is_file() and not path.is_symlink() for path in
                        (report_dir / ".preparation").glob("artifact-*/receipt.json"))
                and _lane_check_profile(enforced, profile) == "public-commerce-v1"
                and not _browser_needs_npm(config, policy, checkout,
                    execution_worktree.parent / "candidate-backend", lane_checks))
            dependency_preparation = "prepared" if needs_dependencies else "not-required-by-inspected-dependencies"
        else:
            needs_dependencies = False
        if needs_dependencies:
            _prepare_check_dependencies(config, checkout, [lane],
                diagnostic_root / f"{head_sha}-{report_dir.name}-{lane}-npm-ci.log", policy=policy)
        args = [lane, "--report-dir", str(lane_dir)]
        if profile == "behavior":
            args.extend(["--profile", "behavior"])
        elif _lane_check_profile(enforced, profile) == "public-commerce-v1":
            args.extend(["--profile", "public-commerce-v1"])
        elif lane == "preflight":
            args.extend(["--profile", profile if profile in {"tooling", "documentation"} else "full"])
        continued_names = []
        if lane == "browser" and snapshot.get("required_browser_tests"):
            passed = {event["Test"] for event in snapshot["events"] if event.get("Action") == "pass" and event.get("Test")}
            continued_names = [name for name in snapshot["required_browser_tests"] if name not in passed]
            if continued_names:
                # dev_preflight still discovers and validates every requested
                # name against this exact candidate before it executes them.
                lane_checks = [{"lane": "browser", "path": "cmd/aicrm/continuation_test.go", "test": name}
                               for name in continued_names]
        if enforced.get("selection_mode") == "targeted" and lane == "backend" and packages:
            args.extend(["--focus-packages-json", json.dumps(sorted(set(packages)), separators=(",", ":"))])
        elif (enforced.get("selection_mode") == "targeted" or continued_names) and lane != "preflight" and lane_checks:
            args.extend(["--focus-checks-json", json.dumps(lane_checks, separators=(",", ":"))])
        if snapshot and (policy / "scripts/ci/check_preparation.py").is_file():
            args.extend(["--resume-commands-json", json.dumps(snapshot.get("passed_commands", []), separators=(",", ":"))])
            if lane == "backend" and snapshot.get("required_go_packages"):
                if snapshot["required_go_packages"] != lane_packages:
                    raise CheckIncompleteError("continued Go packages differ from trusted discovery")
                args.extend(["--resume-go-packages-json", json.dumps(
                    _passed_go_packages(snapshot["events"], lane_packages), separators=(",", ":"))])
        command = ["/usr/bin/python3", "-c", _trusted_runner_code(), str(policy), str(checkout), *args]
        log = diagnostic_root / f"{head_sha}-{report_dir.name}-{lane}.log"
        if log.exists() or log.is_symlink():
            raise ReleaseError("trusted check diagnostic path already exists; inspect before retry")
        started = time.monotonic()
        free_before = shutil.disk_usage(report_dir).free
        with log.open("xb") as output:
            os.chmod(log, 0o600)
            run_args = ["/usr/bin/sudo", "-n", "-u", legacy.BUILD_USER, "-H", "--", "/usr/bin/env", "-i"]
            run_args.extend([f"{key}={value}" for key, value in
                             _check_env(config, checkout, base_sha=base_sha, head_sha=head_sha).items()])
            run_args.extend(command)
            try:
                completed = _run_check_process(run_args, cwd=checkout, output=output, timeout=6 * 60 * 60)
            except subprocess.TimeoutExpired as exc:
                raise CheckIncompleteError("trusted check process timed out: " + lane) from exc
        result = {"lane": lane, "exit_code": completed.returncode,
                  "dependency_preparation": dependency_preparation,
                  "duration_seconds": round(time.monotonic() - started, 1),
                  "scheduler_wait_seconds": round(scheduler_wait, 3),
                  "free_before_bytes":free_before, "free_after_bytes":shutil.disk_usage(report_dir).free,
                  "disk_increment_bytes":free_before-shutil.disk_usage(report_dir).free,
                  "log_sha256": _file_sha256(log), "log_path": str(log)}
        if lane == "browser" and config.get("_browser_dependency_evidence"):
            result["dependency_inspection"] = config["_browser_dependency_evidence"]
        # Failed attempts need the same immutable-source validation as passed
        # ones before any partial result is eligible for future continuation.
        _verify_check_checkout_tree(repo, head_sha, checkout)
        try:
            receipt = json.loads((lane_dir / "run.json").read_text())
        except (OSError, ValueError):
            receipt = {}
        events = _lane_test_events(lane_dir, receipt)
        identity_valid = (receipt.get("lane") == lane and receipt.get("tested_sha") == head_sha
                          and receipt.get("tree") == tree)
        environment = identity_valid and _environment_lane_failure(receipt, events)
        if completed.returncode != 0 and not environment:
            error_type = CheckCandidateError if identity_valid and _candidate_lane_failure(receipt, events) else CheckIncompleteError
            raise error_type(f"trusted impact lane failed: {lane} (exit={completed.returncode})")
        if lane == "backend" and snapshot.get("required_go_packages") and (receipt.get("required_go_packages") or completed.returncode == 0):
            _merge_backend_continuation(lane_dir, snapshot, receipt, lane_packages)
            events = _lane_test_events(lane_dir, receipt)
        elif lane == "backend" and environment and snapshot.get("required_go_packages"):
            passed_packages = _passed_go_packages(snapshot["events"], lane_packages)
            events = [*[event for event in snapshot["events"] if event.get("Package") in passed_packages], *events]
        if continued_names and completed.returncode == 0:
            _verify_lane_evidence(lane_dir, lane, head_sha, tree, lane_checks, [])
            _merge_browser_continuation(lane_dir, snapshot, receipt)
            events = _lane_test_events(lane_dir, receipt)
        required_names = []
        if lane == "browser":
            try:
                required_names = json.loads((lane_dir / "summary.json").read_text()).get("required_browser_tests", [])
            except (OSError, ValueError):
                pass
            # Keep earlier passed journeys even when another environment
            # issue stops the continuation before all remaining names pass.
            if snapshot.get("required_browser_tests") and completed.returncode:
                events = [*[event for event in snapshot["events"] if event.get("Test") and event.get("Action") == "pass"], *events]
                required_names = snapshot["required_browser_tests"]
        with guard:
            snapshots[lane] = {"status": "environment" if environment else "passed", "result": result,
                "receipt": receipt, "events": _sanitized_test_events(events),
                "required_browser_tests": required_names,
                "required_go_packages": lane_packages,
                "passed_commands": receipt.get("passed_commands", [])}
        if environment:
            raise CheckEnvironmentError("required check environment is unavailable: " + lane)
        receipt = _verify_lane_evidence(lane_dir, lane, head_sha, tree, lane_checks,
                                       lane_packages)
        result.update(command_results=receipt.get("command_results", []),
                      preparation=receipt.get("preparation", {}), slow_tests=receipt.get("slow_tests", []))
        if snapshot:
            result.update(reused_browser_tests=receipt.get("reused_browser_tests", []),
                          reused_go_packages=receipt.get("reused_go_packages", []),
                          resumed_from=snapshot["result"]["log_path"])
        return result

    failure_stop = threading.Event()
    def run_lane(lane: str) -> dict[str, Any]:
        if failure_stop.is_set():
            raise CheckIncompleteError('previous lane failed; this lane was not evaluated')
        try:
            return execute_lane(lane)
        except BaseException as error:
            if isinstance(error, ReleaseError) and not isinstance(error, (CheckEnvironmentError, CheckIncompleteError)):
                failure_stop.set()
            raise
        finally:
            if lane == "backend":
                backend_running.set()

    # Preflight remains a prerequisite. Full checks include timing-sensitive
    # database assertions, so run their lanes serially on the 2 GB host.
    # Focused checks retain the existing overlap and shared heavy.lock.
    lane_results = []
    callback = config.get("_check_progress_callback")
    pending_lanes = list(lanes)
    if "preflight" in pending_lanes:
        if callback:
            callback({"stages": [{"lane": "preflight", "status": "running"}],
                      "completed_lanes": 0, "required_lanes": len(lanes)})
        lane_results.append(run_lane("preflight"))
        pending_lanes.remove("preflight")
    if not (policy / "scripts/ci/check_preparation.py").is_file():
        # The installed old policy has no shared heavy-resource lock. Its own
        # maintenance validation must retain serial execution on this host.
        lane_results.extend(run_lane(lane) for lane in pending_lanes)
        return lane_results
    with ThreadPoolExecutor(max_workers=1 if profile == "full" else 2) as executor:
        # Backend must own a worker even if the caller lists browser first;
        # otherwise both workers could wait for a backend still in the queue.
        if "backend" in pending_lanes:
            pending_lanes.remove("backend")
            pending_lanes.insert(0, "backend")
        pending = {executor.submit(run_lane, lane): lane for lane in pending_lanes}
        failures = []
        previous = None
        browser_offset = 0
        browser_terminal = {}
        while pending:
            completed, _ = wait(pending, timeout=0.5, return_when=FIRST_COMPLETED)
            if not backend_running.is_set():
                try:
                    backend_progress = json.loads((report_dir / "backend" / "progress.json").read_text())
                    if backend_progress.get("started_tests", 0) > 0:
                        backend_running.set()
                except (OSError, ValueError):
                    pass
            for future in completed:
                pending.pop(future)
                try:
                    lane_results.append(future.result())
                except ReleaseError as error:
                    failures.append(error)
                    if not isinstance(error, (CheckEnvironmentError, CheckIncompleteError)):
                        for queued in pending: queued.cancel()
                    # Stop submitting later expensive lanes after a concrete failure.
                    if any(f.cancelled() for f in pending):
                        pending = {f:l for f,l in pending.items() if not f.cancelled()}
            if callback:
                stages = []
                for lane in lanes:
                    path = report_dir / lane / "progress.json"
                    try:
                        value = json.loads(path.read_text())
                    except (OSError, ValueError):
                        value = {"lane": lane, "status": "preparing" if lane in pending.values() else "pending"}
                    if lane == "browser":
                        lane_path = report_dir / lane
                        try:
                            required = json.loads((lane_path / "summary.json").read_text()).get("required_browser_tests", [])
                            log_path = lane_path / "browser-execution.log"
                            if log_path.is_file():
                                with log_path.open() as events:
                                    events.seek(browser_offset)
                                    for line in events:
                                        try:
                                            event = json.loads(line)
                                        except ValueError:
                                            continue
                                        if event.get("Test") in required and event.get("Action") in {"pass", "skip", "fail"}:
                                            browser_terminal[event["Test"]] = event["Action"]
                                    browser_offset = events.tell()
                            value.update(required_tests=len(required), completed_tests=len(browser_terminal),
                                         failed_tests=[name for name, status in browser_terminal.items() if status != "pass"])
                        except (OSError, ValueError):
                            pass
                    stages.append(value)
                progress = {"stages": stages, "completed_lanes": len(lane_results), "required_lanes": len(lanes)}
                encoded = json.dumps(progress, sort_keys=True)
                if encoded != previous:
                    callback(progress)
                    previous = encoded
        if failures:
            # An environment error cannot mask an independent code failure.
            raise next((error for error in failures if not isinstance(error, CheckEnvironmentError)), failures[0])
    return sorted(lane_results, key=lambda result: lanes.index(result["lane"]))


def _prepare_check_dependencies(config: dict[str, Any], checkout: Path, lanes: list[str],
                                diagnostic_log: Path, *, policy: Path | None = None) -> None:
    if not ({"backend", "frontend", "browser"} if policy else {"frontend", "browser"}).intersection(lanes):
        return
    if diagnostic_log.exists() or diagnostic_log.is_symlink():
        raise ReleaseError("trusted check diagnostic path already exists; inspect before retry")
    commands = [
        ["env", "-u", "AICRM_DATABASE_URL", "npm", "ci", "--no-audit", "--no-fund"],
        ["env", "-u", "AICRM_DATABASE_URL", "npm", "ci", "--prefix", "web/v3",
         "--no-audit", "--no-fund"],
    ]
    if policy and (policy / "scripts/ci/check_preparation.py").is_file():
        commands = [["/usr/bin/python3", str(policy / "scripts/ci/check_preparation.py"), "npm"],
                    ["/usr/bin/python3", str(policy / "scripts/ci/check_preparation.py"), "npm", "--prefix", "web/v3"]]
    with diagnostic_log.open("xb") as output:
        os.chmod(diagnostic_log, 0o600)
        for command in commands:
            output.write(("$ " + shlex.join(command) + "\n").encode())
            output.flush()
            result = _build_command(config, command, cwd=checkout, timeout=60 * 60,
                                    check=False, safe_repository=checkout)
            output.write(result.stdout.encode())
            output.write(result.stderr.encode())
            output.flush()
            if result.returncode:
                raise CheckIncompleteError("isolated check npm dependency setup failed; diagnose environment or lock inputs")


def _require_cleanup_lock(config: dict) -> None:
    if str(Path(config["lock"]).resolve()) not in getattr(_LOCK_CONTEXT, "paths", []):
        raise ReleaseError("check artifact cleanup requires the existing serial controller lock")


def _allocated_bytes(path: Path) -> int:
    if not path.exists():
        return 0
    if path.is_symlink():
        raise ReleaseError("artifact lifecycle root cannot be a symlink")
    return sum(item.lstat().st_blocks * 512 for item in path.rglob("*") if not item.is_symlink())


def _lifecycle_directory(config: dict) -> Path:
    _require_cleanup_lock(config)
    path = Path(config["work_root"]) / "check-lifecycle"
    _safe_directory(path, create=True)
    return path


def _process_alive(pid: int) -> bool:
    try:
        os.kill(pid, 0)
        return True
    except ProcessLookupError:
        return False
    except PermissionError:
        return True


def _cleanup_attempt_record(config: dict, path: Path, record: dict) -> dict:
    """Only registered generated objects; evidence and Git authority stay put."""
    _require_cleanup_lock(config)
    _check_storage_mount(config)
    if record.get("guard_build_processes"):
        _assert_build_account_idle()
    temporary = Path(record["temporary"])
    report = Path(record["report"])
    reports_root = Path(legacy.BUILD_ROOT) / "domestic-main-checks"
    if (temporary.parent != Path(config["work_root"]) or not temporary.name.startswith("domestic-main-check-")
            or report.parent != reports_root or not report.name.startswith(record["head_sha"] + "-")
            or not SHA.fullmatch(record["head_sha"])):
        raise ReleaseError("attempt lifecycle paths are outside registered generated roots")
    for value in (temporary, report):
        if value.is_symlink():
            raise ReleaseError("attempt lifecycle path changed to a symlink")
    prep = report / ".preparation"
    before = shutil.disk_usage(Path(config["work_root"])).free
    scratch = Path(record["scratch"]) if record.get("scratch") else None
    if scratch:
        roots = {Path(config.get("check_execution_root", "/tmp")).resolve(), Path("/tmp").resolve()}
        if (scratch.parent not in roots or not re.fullmatch(r"ac-[0-9a-f]{16}", scratch.name)
                or scratch.is_symlink()):
            raise ReleaseError("attempt scratch path is outside registered execution roots")
    generated = [temporary, prep, *[report / lane / ".venv" for lane in builder_ci_lanes()]]
    if scratch:
        generated.append(scratch)
    for value in generated:
        parent = value
        boundary = scratch.parent if scratch and value == scratch else (temporary.parent if value == temporary else report.parent)
        while parent != boundary:
            if parent.is_symlink():
                raise ReleaseError("generated lifecycle object traverses a symlink")
            parent = parent.parent
    reclaim = sum(_allocated_bytes(value) for value in generated)
    if prep.is_dir():
        # Small inventories and preparation counters are evidence; snapshots,
        # dependency copies and profiles are disposable generated objects.
        evidence = path.parent / (path.stem + "-preparation")
        _safe_directory(evidence, create=True)
        for value in [*prep.glob("*.json"), *prep.glob("*.jsonl")]:
            if not value.is_symlink() and value.is_file():
                shutil.copyfile(value, evidence / value.name)
                os.chmod(evidence / value.name, 0o600)
        for snapshot in prep.iterdir():
            if (snapshot.is_symlink() or not snapshot.is_dir()
                    or not re.fullmatch(r"(?:artifact|npm)-[0-9a-f]{64}", snapshot.name)):
                continue
            receipt = snapshot / "receipt.json"
            if receipt.is_file() and not receipt.is_symlink():
                preserved = evidence / (snapshot.name + "-receipt.json")
                shutil.copyfile(receipt, preserved)
                os.chmod(preserved, 0o600)
        if list(prep.glob("database-*.json")):
            if _check_database_identity(config) != record["database_identity"]:
                raise CheckEnvironmentError("stale synthetic database cleanup identity is unavailable; retained attempt")
            _cleanup_attempt_databases(config,prep)
    for value in generated:
        if value.is_symlink():
            raise ReleaseError("generated lifecycle object is a symlink")
        if value.exists():
            shutil.rmtree(value)
    preparation_cache = _trim_preparation_cache(config,
        config.get("check_capacity", {}).get("cache_limits_bytes", {}).get("preparation"))
    if preparation_cache is not None:
        record["preparation_cache"] = preparation_cache
    record.update(status="reclaimed", finished_at_utc=_utc_now(), reclaimed_allocated_bytes=reclaim,
                  free_after_bytes=shutil.disk_usage(Path(config["work_root"])).free,
                  released_free_bytes=shutil.disk_usage(Path(config["work_root"])).free - before)
    atomic_json(path, record)
    return record


def _cleanup_attempt_databases(config: dict, prep: Path) -> None:
    _require_cleanup_lock(config)
    if not list(prep.glob("database-*.json")):
        return
    # Root controls this code even when the old trusted policy predates test
    # preparation. The same non-superuser account drops only inventoried DBs.
    code = """import json,os,re,subprocess,sys
from pathlib import Path
from urllib.parse import urlsplit,unquote,parse_qs
p=urlsplit(os.environ['AICRM_DATABASE_URL'])
name=unquote(p.path.strip('/'))
if p.hostname not in ('localhost','127.0.0.1','::1') or not (name=='aicrm_ci' or name.startswith('aicrm_test_')):
 raise SystemExit(21)
env=dict(os.environ,PGHOST=p.hostname,PGPORT=str(p.port or 5432),PGUSER=unquote(p.username or ''),
 PGPASSWORD=unquote(p.password or ''),PGDATABASE=name,PGSSLMODE=parse_qs(p.query).get('sslmode',['prefer'])[0])
for f in sorted(Path(sys.argv[1]).glob('database-*.json')):
 if f.is_symlink(): raise SystemExit(22)
 d=json.loads(f.read_text()).get('database','')
 if not re.fullmatch(r'aicrm_test_(?:tpl|clone)_[0-9a-f]{16}_acceptance_test',d) or f.name!='database-'+d+'.json':
  raise SystemExit(23)
 q='SELECT pg_get_userbyid(datdba)=current_user FROM pg_database WHERE datname=\\\''+d+'\\\''
 v=subprocess.run(['psql','-X','-Atqc',q],env=env,capture_output=True,text=True,timeout=10)
 if v.returncode or v.stdout.strip() not in ('','t'): raise SystemExit(24)
 if v.stdout.strip()=='t':
  result=subprocess.run(['psql','-X','-v','ON_ERROR_STOP=1','-Atqc','DROP DATABASE "'+d+'"'],env=env,
   stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=20)
  if result.returncode: raise SystemExit(25)
 f.unlink()
"""
    result = _build_command(config,["/usr/bin/python3","-c",code,str(prep)],cwd=Path("/"),timeout=120,check=False)
    if result.returncode:
        raise CheckEnvironmentError("synthetic database cleanup failed; retained attempt inventory")


def _assert_build_account_idle() -> None:
    # A killed controller may leave its build-account children alive. Defer
    # crash recovery while any check process still uses that account.
    processes = subprocess.run(["ps", "-u", legacy.BUILD_USER, "-o", "pid="],
                               stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, check=False)
    # ps returns 1 for an empty account, not for a missing/invalid account.
    if processes.returncode not in (0, 1) or (processes.returncode == 1 and
            (processes.stdout.strip() or (processes.stderr or "").strip())):
        raise CheckEnvironmentError("build account process inventory is unavailable; retained attempts")
    try:
        children = any(int(pid) != os.getpid() for pid in processes.stdout.split())
    except ValueError as exc:
        raise CheckEnvironmentError("build account process inventory is incomplete; retained attempts") from exc
    if children:
        raise CheckEnvironmentError("build account still has live processes; retained attempts and blocked new checks")


def _recover_check_attempts(config: dict) -> list[dict]:
    directory = _lifecycle_directory(config)
    recovered = []
    _assert_build_account_idle()
    pending = []
    for path in directory.glob("*-attempt.json"):
        if path.is_symlink():
            raise ReleaseError("lifecycle record is a symlink")
        info = path.stat()
        if info.st_uid != os.geteuid() or info.st_mode & 0o077 or info.st_nlink != 1:
            raise ReleaseError("lifecycle record is not controller-protected")
        record = json.loads(path.read_text())
        if record.get("status") == "reclaimed":
            continue
        if (_process_alive(record["pid"]) and
                        not (record["pid"] == os.getpid() and record.get("status") == "evidence_saved_cleanup_pending")):
            raise CheckEnvironmentError("registered check controller is still alive; blocked new checks")
        pending.append((path, record))
    # Validate the entire inventory before deleting anything. A live
    # controller discovered later must protect all shared attempt resources.
    for path, record in pending:
        recovered.append(_cleanup_attempt_record(config, path, record))
    return recovered


def _reclaim_policy_worktrees(config: dict, repo: Path, base_sha: str) -> dict:
    """Retire reconstructible rule checkouts, never authoritative Git refs."""
    _require_cleanup_lock(config)
    _assert_build_account_idle()
    root = Path(config["work_root"]) / "trusted-policy"
    if not root.exists() and not root.is_symlink():
        return {"protected": [], "removed": [], "reclaimed_bytes": 0}
    _safe_directory(root)
    root = root.resolve()
    keep = {_sha(base_sha, "check base SHA"), _resolve_ref(repo, "refs/heads/main")}
    pending = set()
    module = Path(__file__).resolve()
    if module.is_relative_to(root) and SHA.fullmatch(module.relative_to(root).parts[0]):
        keep.add(module.relative_to(root).parts[0])
    directory = _lifecycle_directory(config)
    for path in directory.glob("*-attempt.json"):
        info = path.lstat()
        if path.is_symlink() or info.st_uid != os.geteuid() or info.st_mode & 0o077 or info.st_nlink != 1:
            raise CheckEnvironmentError("unsafe lifecycle evidence; retained policy checkouts")
        record = json.loads(path.read_text())
        policy = Path(record.get("policy", "")).resolve()
        if record.get("status") != "reclaimed" and policy.parent == root:
            keep.add(policy.name)
            pending.add(policy.name)
    registered = {}
    for block in _git(repo, "worktree", "list", "--porcelain").split("\n\n"):
        fields = dict(line.split(" ", 1) for line in block.splitlines() if " " in line)
        if "worktree" in fields:
            registered[Path(fields["worktree"]).resolve()] = fields.get("HEAD")
    planned = []
    retained = []
    archive_indexes = []
    # Validate the entire inventory before removing any checkout. Unknown or
    # changed source stays available for diagnosis; a Git ref must preserve
    # each retired commit so historical rules can be reconstructed verbatim.
    paths = set(root.iterdir()) | {p for p in registered if p.parent == root and not p.exists()}
    for path in sorted(paths):
        archived = re.fullmatch(r"([0-9a-f]{40})\.archived\.json", path.name)
        if archived:
            info = path.lstat()
            if (not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid()
                    or info.st_mode & 0o077 or info.st_nlink != 1):
                raise CheckEnvironmentError("unsafe policy archive index; retained source inventory")
            try:
                record = json.loads(path.read_text())
                valid = (Path(record.get("original_path", "")).resolve() == root / archived.group(1)
                         and record.get("raw_logs_preserved") is True
                         and record.get("ended_source_and_artifact_archive") is True)
            except (OSError, ValueError, TypeError, AttributeError):
                valid = False
            if not valid:
                raise CheckEnvironmentError("invalid policy archive index; retained source inventory")
            # This is retained evidence from the existing archival flow, not
            # a source checkout or a new claim that archive bytes were checked.
            archive_indexes.append(str(path))
            continue
        if not SHA.fullmatch(path.name) or path.is_symlink() or (path.exists() and not path.is_dir()):
            raise CheckEnvironmentError("unrecognized policy checkout; retained source inventory")
        if path.name in pending or (path.name in keep and path.exists()):
            continue
        if path.exists():
            _safe_directory(path)
        if (registered.get(path) != path.name or (path.exists() and (
                _worktree_git(path, "rev-parse", "HEAD") != path.name
                or _worktree_git(path, "status", "--porcelain", "--untracked-files=all")))):
            raise CheckEnvironmentError("unregistered or changed policy checkout; retained source inventory")
        refs = _git(repo, "for-each-ref", "--contains=" + path.name, "--format=%(refname)").splitlines()
        if not refs:
            retained.append({"path": str(path), "reason": "commit lacks an authoritative retention ref"})
            continue
        planned.append({"path": str(path), "sha": path.name, "tree": _tree(repo, path.name),
                        "retained_refs": refs, "allocated_bytes": _allocated_bytes(path)})
    proof = directory / ("policy-reclaim-" + str(time.time_ns()) + ".json")
    result = {"protected": sorted(keep), "retained": retained, "archive_indexes": archive_indexes,
              "removed": [], "planned": planned,
              "reclaimed_bytes": 0, "proof": str(proof) if planned else None}
    for item in planned:
        atomic_json(proof, result)
        _run(["git", f"--git-dir={repo}", "worktree", "remove", "--force", item["path"]], timeout=120)
        result["removed"].append(item)
        result["reclaimed_bytes"] += item["allocated_bytes"]
        atomic_json(proof, result)
    return result


def _check_storage_mount(config: dict) -> dict:
    raw = config.get("check_storage_mount")
    if raw is None:
        return {"mode":"root_filesystem"}
    mount = Path(raw)
    expected = config.get("check_storage_uuid")
    if (not mount.is_absolute() or mount.is_symlink() or not os.path.ismount(mount)
            or not isinstance(expected,str) or not expected):
        raise CheckEnvironmentError("configured check data disk is not mounted; blocked root-disk fallback")
    found = subprocess.run(["findmnt","-n","-o","UUID","--target",str(mount)],
                           capture_output=True,text=True,check=False,timeout=15)
    if found.returncode or found.stdout.strip() != expected:
        raise CheckEnvironmentError("check data disk UUID differs from the configured disk")
    device = mount.stat().st_dev
    paths = config.get("check_storage_paths", [str(legacy.BUILD_ROOT)])
    if not isinstance(paths,list) or not paths:
        raise CheckEnvironmentError("check data disk requires its managed paths")
    for raw_path in paths:
        path=Path(raw_path)
        if not path.is_absolute() or not path.exists() or path.stat().st_dev != device:
            raise CheckEnvironmentError("managed check storage path is not on the configured data disk")
    return {"mode":"data_disk","mount":str(mount),"uuid":expected,"managed_paths":paths,
            "free_bytes":shutil.disk_usage(mount).free}


def _check_capacity(config: dict, filesystem: Path, profile_key: str = "check_capacity") -> dict:
    free = shutil.disk_usage(filesystem).free
    measured = config.get(profile_key)
    if not measured:
        if config.get("_check_capacity_calibration") is True:
            return {"mode":"calibration", "free_before_bytes":free}
        raise CheckEnvironmentError("check capacity is not calibrated on this host; measure workspace, database and evidence peaks first")
    fields = ("workspace_peak_bytes", "database_reserve_bytes", "evidence_reserve_bytes")
    if not measured.get("measured_at_utc") or any(type(measured.get(name)) is not int or measured[name] <= 0 for name in fields):
        raise CheckEnvironmentError("check capacity profile lacks measured positive workspace/database/evidence budgets")
    required = sum(measured[name] for name in fields)
    if free < required:
        raise CheckEnvironmentError(f"insufficient staging disk: free={free} bytes, measured check requirement={required} bytes")
    return {"mode":"measured", "free_before_bytes":free, "required_bytes":required,
            "measurement":measured["measured_at_utc"]}


def _merge_legacy_check_caches(config: dict) -> dict:
    _require_cleanup_lock(config)
    if config.get("check_cache_migration_enabled") is not True:
        return {"status":"pending_validation"}
    source = Path(legacy.BUILD_ROOT).parents[1] / "cache"
    target = Path(legacy.BUILD_ROOT) / "cache"
    if not source.exists():
        return {"status":"already_consolidated"}
    if source.is_symlink() or target.is_symlink():
        raise ReleaseError("cache consolidation root is a symlink")
    account = pwd.getpwnam(legacy.BUILD_USER)
    migrated = 0
    duplicate = 0
    conflicts = []
    proof = _lifecycle_directory(config) / ("cache-migration-"+str(time.time_ns())+".json")
    journal = proof.with_suffix(".jsonl")
    record = {"source":str(source),"target":str(target),"verified_log":str(journal),"status":"in_progress"}
    atomic_json(proof,record)
    for name in ("go-build","go-mod","npm"):
        origin = source/name
        if not origin.is_dir() or origin.is_symlink():
            continue
        for value in origin.rglob("*"):
            if not value.is_file() or value.is_symlink():
                continue
            relative = value.relative_to(source)
            destination = target/relative
            parent = destination.parent
            while parent != target:
                if parent.is_symlink():
                    raise ReleaseError("canonical cache path traverses a symlink")
                parent = parent.parent
            digest = _file_sha256(value)
            if destination.exists():
                if destination.is_symlink() or _file_sha256(destination) != digest:
                    conflicts.append(relative.as_posix())
                    continue  # Keep canonical input and mismatched old evidence.
                duplicate += value.stat().st_blocks*512
            else:
                destination.parent.mkdir(parents=True,exist_ok=True)
                parent = destination.parent
                while parent != target:
                    os.chown(parent,account.pw_uid,account.pw_gid)
                    parent = parent.parent
                shutil.copyfile(value,destination)
                os.chown(destination,account.pw_uid,account.pw_gid)
                os.chmod(destination,0o600)
                if _file_sha256(destination) != digest:
                    raise ReleaseError("canonical cache copy failed byte verification")
                migrated += value.stat().st_blocks*512
            # Persist proof before unlink, so an interruption can safely rerun.
            descriptor = os.open(journal,os.O_WRONLY|os.O_APPEND|os.O_CREAT|getattr(os,"O_NOFOLLOW",0),0o600)
            with os.fdopen(descriptor,"a") as stream:
                stream.write(json.dumps({"path":relative.as_posix(),"sha256":digest})+"\n")
            if _file_sha256(value) != digest:
                raise ReleaseError("legacy cache changed during consolidation")
            value.unlink()
        for path in sorted(origin.rglob("*"),key=lambda item:len(item.parts),reverse=True):
            if path.is_dir() and not path.is_symlink():
                try: path.rmdir()
                except OSError: pass
    record.update(status="completed" if not conflicts else "retained_conflicts",migrated_bytes=migrated,
                  duplicate_reclaimed_bytes=duplicate,conflicts=conflicts)
    atomic_json(proof,record)
    return record


def _check_preparation_cache(config: dict) -> Path | None:
    limit = config.get("check_capacity", {}).get("cache_limits_bytes", {}).get("preparation")
    if limit is None:
        return None
    if type(limit) is not int or limit <= 0:
        raise CheckEnvironmentError("preparation cache capacity must be positive and measured")
    _require_cleanup_lock(config)
    root = Path(legacy.BUILD_ROOT) / "cache/preparation"
    if root.is_symlink():
        raise CheckEnvironmentError("preparation cache root is a symlink")
    _build_command(config, ["/usr/bin/mkdir", "-m", "0700", "-p", str(root)],
                   cwd=Path("/"), timeout=30)
    info = root.lstat()
    if not stat.S_ISDIR(info.st_mode) or info.st_uid != pwd.getpwnam(legacy.BUILD_USER).pw_uid:
        raise CheckEnvironmentError("preparation cache must belong to the actual build account")
    if stat.S_IMODE(info.st_mode) != 0o700:
        _build_command(config, ["/usr/bin/chmod", "0700", str(root)], cwd=Path("/"), timeout=30)
    _verify_preparation_cache_root(root)
    return root


def _verify_preparation_cache_root(root: Path) -> None:
    info = root.lstat()
    if (not stat.S_ISDIR(info.st_mode) or info.st_uid != pwd.getpwnam(legacy.BUILD_USER).pw_uid
            or stat.S_IMODE(info.st_mode) != 0o700):
        raise CheckEnvironmentError("preparation cache must be private to the actual build account")


def _trim_preparation_cache(config: dict, limit: int | None) -> dict | None:
    _require_cleanup_lock(config)
    root = Path(legacy.BUILD_ROOT) / "cache/preparation"
    if not root.exists() and not root.is_symlink():
        return None
    _assert_build_account_idle()  # Preserve snapshots while any task uses them.
    _verify_preparation_cache_root(root)
    units = []
    for value in root.iterdir():
        if value.name in {"artifact.lock", "npm.lock"} and value.is_file() and not value.is_symlink():
            continue
        if not value.is_symlink() and value.is_dir() and re.fullmatch(r"(?:artifact|npm)-[0-9a-f]{64}", value.name):
            units.append(value)
        else:
            raise CheckEnvironmentError("unrecognized preparation cache entry; retained for diagnosis")
    if limit is not None and (type(limit) is not int or limit <= 0):
        raise CheckEnvironmentError("preparation cache capacity must be positive and measured")
    before = _allocated_bytes(root)
    if limit is None or before <= limit:
        # No cache writer is active under the existing controller lock. With
        # no eviction, the measured inventory is also the final inventory.
        return {"before_bytes": before, "reclaimed_bytes": 0, "after_bytes": before,
                "limit_bytes": limit, "removed": [], "reused": limit is not None}
    reclaimed = 0
    removed = []
    for unit in sorted(units, key=lambda item: item.stat().st_mtime_ns):
        if limit is None or before - reclaimed <= limit:
            break
        reclaimed += unit.lstat().st_blocks * 512 + _allocated_bytes(unit)
        removed.append(unit.name)
        shutil.rmtree(unit)  # Whole snapshots only; writable task copies are elsewhere.
    after = _allocated_bytes(root)
    if limit is not None and after > limit:
        raise CheckEnvironmentError("preparation cache metadata exceeds measured budget")
    return {"before_bytes": before, "reclaimed_bytes": reclaimed, "after_bytes": after,
            "limit_bytes": limit, "removed": removed, "reused": limit is not None}


def _trim_check_caches(config: dict) -> dict:
    _require_cleanup_lock(config)
    limits = config.get("check_capacity", {}).get("cache_limits_bytes", {})
    result = {}
    for name in ("go-build", "npm"):
        root = Path(legacy.BUILD_ROOT) / "cache" / name
        if not root.exists():
            continue
        limit = limits.get(name)
        before = _allocated_bytes(root)
        reclaimed = 0
        if limit is not None:
            if type(limit) is not int or limit <= 0:
                raise CheckEnvironmentError("cache capacity must be positive and based on the host measurement")
        if limit is not None and before > limit:
            # Content-addressed build/download entries are disposable; source,
            # toolchains and module trees are never evicted file by file.
            files = [value for value in root.rglob("*") if value.is_file() and not value.is_symlink()]
            for value in sorted(files, key=lambda item: max(item.stat().st_atime_ns, item.stat().st_mtime_ns)):
                if before - reclaimed <= limit:
                    break
                reclaimed += value.stat().st_blocks * 512
                value.unlink()
        result[name] = {"before_bytes":before, "reclaimed_bytes":reclaimed,
                        "after_bytes":_allocated_bytes(root) if limit is not None and before > limit else before,
                        "limit_bytes":limit, "reused":True}
    modules = Path(legacy.BUILD_ROOT) / "cache/go-mod"
    if modules.exists():
        limit = limits.get("go-mod")
        before = _allocated_bytes(modules)
        reclaimed = 0
        if limit is not None:
            if type(limit) is not int or limit <= 0:
                raise CheckEnvironmentError("module cache capacity must be positive and measured")
        if limit is not None and before > limit:
            protected = set()
            def escape(value):
                return "".join("!" + char.lower() if char.isupper() else char for char in value)
            refs = _git(Path(config["repo"]), "for-each-ref", "--format=%(refname)", "refs/heads/", "refs/domestic/").splitlines()
            for ref in refs:
                source = _run(["git", "--git-dir=" + config["repo"], "show", ref + ":go.sum"], check=False)
                if source.returncode:
                    continue
                for line in source.stdout.splitlines():
                    values = line.split()
                    if len(values) == 3:
                        protected.add(escape(values[0]) + "@" + escape(values[1].removesuffix("/go.mod")))
            units = [value for value in modules.rglob("*@*") if value.is_dir() and not value.is_symlink()
                     and "@v" not in value.relative_to(modules).parts and value.relative_to(modules).as_posix() not in protected]
            for unit in sorted(units, key=lambda item: max(item.stat().st_atime_ns, item.stat().st_mtime_ns)):
                if before - reclaimed <= limit:
                    break
                relative = unit.relative_to(modules).as_posix()
                module, version = relative.rsplit("@", 1)
                reclaimed += _allocated_bytes(unit)
                shutil.rmtree(unit)
                downloads = modules / "cache/download" / module / "@v"
                if downloads.is_dir() and not downloads.is_symlink():
                    for value in downloads.iterdir():
                        if value.is_file() and not value.is_symlink() and value.name.startswith(version + "."):
                            reclaimed += value.stat().st_blocks * 512
                            value.unlink()
        after = _allocated_bytes(modules) if limit is not None and before > limit else before
        result["go-mod"] = {"before_bytes":before, "after_bytes":after, "reclaimed_bytes":reclaimed,
                            "limit_bytes":limit, "reused":True, "protected_by":"all authoritative source refs go.sum"}
        if limit is not None and after > limit:
            raise CheckEnvironmentError("referenced module cache exceeds measured budget; keep source dependencies and review capacity")
    preparation = _trim_preparation_cache(config, limits.get("preparation"))
    if preparation is not None:
        result["preparation"] = preparation
    return result


def _reclaim_unreferenced_packages(config: dict) -> dict:
    _require_cleanup_lock(config)
    state = _load_state(Path(config["state"]))
    batch = state.get("batch")
    if ((state.get("staging_out_of_sync") and
         (not isinstance(batch, dict) or batch.get("status") != "open"))
            or (state.get("in_flight") and state["in_flight"].get("phase") != "checks")):
        return {"status":"protected_unresolved_install", "removed":[]}
    releases = Path(config.get("stage_releases", "/opt/aicrm/releases"))
    current = (batch["installed_app"] if isinstance(batch, dict)
               else state["installed_app"])["sha"]
    if (releases.parent / "current").resolve() != (releases / current).resolve():
        return {"status":"current_identity_unavailable", "removed":[]}
    receipt_path = releases.parent / "domestic-receipts" / (current + ".json")
    if receipt_path.is_symlink() or not receipt_path.is_file():
        return {"status":"rollback_identity_unavailable", "removed":[]}
    current_receipt = json.loads(receipt_path.read_text())
    if current_receipt.get("source_sha") != current:
        return {"status":"current_receipt_identity_unavailable", "removed":[]}
    if isinstance(batch, dict) and (current_receipt != batch.get("stage_receipt")
                                    or current_receipt.get("manifest_sha256") != batch["installed_app"]["manifest_sha256"]):
        return {"status":"cumulative_stage_receipt_changed", "removed":[]}
    previous = current_receipt.get("previous_sha")
    if not isinstance(previous, str) or not SHA.fullmatch(previous):
        return {"status":"rollback_identity_unavailable", "removed":[]}
    refs = set(_git(Path(config["repo"]), "for-each-ref", "--format=%(objectname)").splitlines())
    pending = {item["head_sha"] for item in state.get("queue", []) if item.get("status") != "completed"}
    protected = refs | pending | {current, previous, state["main"]["sha"],
                                  state["installed_app"]["sha"]}
    removed = []
    proof = _lifecycle_directory(config) / ("packages-" + str(time.time_ns()) + ".json")
    snapshots = {"protected":sorted(protected), "removed":removed}
    if releases.is_symlink() or not releases.is_dir():
        return {"status":"package_root_unavailable", "removed":[]}
    for path in sorted(releases.iterdir()):
        if not SHA.fullmatch(path.name) or path.is_symlink() or not path.is_dir():
            continue
        if path.name in pending:
            continue
        duplicate = releases.parent / "domestic/builds" / path.name / "release"
        if path.name in protected and not duplicate.is_dir():
            # This package is retained and has no duplicate eligible for
            # reclamation. Its installation verification is separate from
            # cleanup; do not reread every retained package byte per attempt.
            continue
        installed_receipt = releases.parent / "domestic-receipts" / (path.name + ".json")
        # A read-back installation receipt proves this package is no longer
        # being built/accepted. Never infer completion merely from directory age.
        if not installed_receipt.is_file() or installed_receipt.is_symlink():
            continue
        installed = json.loads(installed_receipt.read_text())
        if installed.get("source_sha") != path.name:
            continue
        manifest = builder.verify_release_inventory(path, allow_release_env=True)
        ancestors = [duplicate, duplicate.parent, duplicate.parent.parent, duplicate.parent.parent.parent]
        if (duplicate.is_dir() and not any(value.is_symlink() for value in ancestors)
                and builder.verify_release_inventory(duplicate) == manifest):
            record = {"path":str(duplicate), "manifest_sha256":manifest,
                      "allocated_bytes":_allocated_bytes(duplicate), "reason":"verified duplicate of retained package"}
            removed.append(record)
            atomic_json(proof, snapshots)
            shutil.rmtree(duplicate)
        if path.name not in protected:
            record = {"path":str(path), "manifest_sha256":manifest,
                      "allocated_bytes":_allocated_bytes(path), "reason":"installed history released from all references"}
            removed.append(record)
            atomic_json(proof, snapshots)
            shutil.rmtree(path)
    return {"status":"completed", "protected":sorted(protected), "removed":removed,
            "reclaimed_bytes":sum(item["allocated_bytes"] for item in removed)}


def _archive_closed_check_evidence(config: dict) -> list[dict]:
    """Archive only resolved attempts; verify remote bytes before local removal."""
    _require_cleanup_lock(config)
    if config.get("check_evidence_archive_enabled") is not True:
        return []  # Retain evidence until the desk validates archive storage.
    state = _load_state(Path(config["state"]))
    closed = {item["head_sha"] for item in state.get("queue", []) if item.get("status") == "completed"}
    pending = {item["head_sha"] for item in state.get("queue", []) if item.get("status") != "completed"}
    directory = _lifecycle_directory(config)
    archived = []
    def finish_local_removal(path, record, mapping, capsule):
        if _file_sha256(mapping) != record["archive"]["mapping_sha256"]:
            raise ReleaseError("verified archive mapping changed; retained local evidence")
        members = json.loads(mapping.read_text())["members"]
        report = Path(record["report"])
        diagnostics = Path(config["work_root"]) / "diagnostics"
        if report.parent != Path(legacy.BUILD_ROOT) / "domestic-main-checks":
            raise ReleaseError("archived report is outside registered roots")
        for member in members:
            value = Path(member["original_path"])
            if not value.is_absolute() or Path(os.path.abspath(value)) != value:
                raise ReleaseError("archived evidence path is not normalized")
            if not (value.is_relative_to(report) or
                    (value.parent == diagnostics and value.name.startswith(record["head_sha"]+"-"+report.name+"-"))):
                raise ReleaseError("archived member is outside registered evidence roots")
            parent = value.parent
            boundary = report.parent if value.is_relative_to(report) else diagnostics.parent
            while parent != boundary:
                if parent.is_symlink():
                    raise ReleaseError("archived evidence traverses a symlink")
                parent = parent.parent
            if value.is_symlink() or (value.exists() and _file_sha256(value) != member["sha256"]):
                raise ReleaseError("closed evidence changed after archiving; retained local files")
        for member in members:
            Path(member["original_path"]).unlink(missing_ok=True)
        capsule.unlink(missing_ok=True)
        record["archive"]["local_cleanup_complete"] = True
        atomic_json(path,record)
    for path in directory.glob("*-attempt.json"):
        if (path.is_symlink() or path.stat().st_uid != os.geteuid() or
                path.stat().st_mode & 0o077 or path.stat().st_nlink != 1):
            raise ReleaseError("evidence lifecycle record is unsafe")
        record = json.loads(path.read_text())
        if record.get("archive"):
            if not record["archive"].get("local_cleanup_complete"):
                report_name = Path(record["report"]).name
                finish_local_removal(path,record,directory/(report_name+"-archive.json"),directory/(report_name+".tar.gz"))
            continue
        if (record.get("status") != "reclaimed" or
                record["head_sha"] not in closed or record["head_sha"] in pending):
            continue
        report = Path(record["report"])
        if report.parent != Path(legacy.BUILD_ROOT)/"domestic-main-checks" or report.is_symlink():
            raise ReleaseError("closed evidence report is outside registered roots")
        files = [value for value in report.rglob("*") if value.is_file() and not value.is_symlink()]
        diagnostic_root = Path(config["work_root"])/"diagnostics"
        files += [value for value in diagnostic_root.glob(record["head_sha"]+"-"+report.name+"-*")
                  if value.is_file() and not value.is_symlink()]
        if not files:
            continue
        original = {str(value):_file_sha256(value) for value in files}
        capsule = directory/(report.name+".tar.gz")
        if capsule.is_symlink():
            raise ReleaseError("archive capsule is a symlink")
        with tarfile.open(capsule, "w:gz") as archive:
            for index, value in enumerate(files):
                archive.add(value, arcname=str(index), recursive=False)
        os.chmod(capsule,0o600)
        with tarfile.open(capsule,"r:gz") as archive:
            for index,value in enumerate(files):
                content = archive.extractfile(str(index))
                if content is None or hashlib.sha256(content.read()).hexdigest() != original[str(value)]:
                    raise ReleaseError("compressed evidence failed original-byte verification")
        # Mapping lives in the protected ledger on both sides of the archive.
        mapping = directory/(report.name+"-archive.json")
        atomic_json(mapping,{"schema":1,"members":[{"member":str(index),"original_path":str(value),
                             "sha256":original[str(value)]} for index,value in enumerate(files)],"head_sha":record["head_sha"],
                             "capsule_sha256":_file_sha256(capsule)})
        remote_root = "/opt/aicrm/domestic/check-evidence/" + record["head_sha"]
        _production_ssh(config,"sudo","-n","/usr/bin/mkdir","-p","-m","0700",remote_root,timeout=30)
        transport = " ".join(shlex.quote(part) for part in legacy.ssh_args(config)[:-1])
        for value in (capsule,mapping):
            legacy.command("rsync","-a","--no-owner","--no-group","--no-perms","--checksum",
                           "--rsync-path=sudo -n /usr/bin/rsync","-e",transport,str(value),
                           f"{config['prod_user']}@{config['prod_host']}:{remote_root}/{value.name}",timeout=600)
            observed = _production_ssh(config,"sudo","-n","/usr/bin/sha256sum",remote_root+"/"+value.name,timeout=30)
            if observed.split()[0] != _file_sha256(value):
                raise ReleaseError("remote evidence archive failed independent hash verification")
        archive_receipt = {"root":remote_root,"capsule_sha256":_file_sha256(capsule),
                           "mapping_sha256":_file_sha256(mapping),"verified_at_utc":_utc_now(),
                           "original_bytes":sum(value.stat().st_size for value in files),"local_cleanup_complete":False}
        record["archive"] = archive_receipt
        atomic_json(path,record)
        finish_local_removal(path,record,mapping,capsule)
        archived.append(archive_receipt)
    return archived


@contextmanager
def _check_attempt_directory(config: dict, report: Path, policy: Path, head_sha: str):
    directory = _lifecycle_directory(config)
    execution_root = Path(config.get("check_execution_root", "/tmp"))
    if not execution_root.is_absolute():
        raise CheckEnvironmentError("check execution root must be absolute")
    try:
        execution_root = execution_root.resolve(strict=True)
    except OSError as exc:
        raise CheckEnvironmentError("check execution root is unavailable") from exc
    if config.get("check_execution_root"):
        _safe_directory(execution_root)
    token = os.urandom(8).hex()
    # Go's compile action includes the absolute package directory. The serial
    # controller owns this fixed workspace; every attempt still creates a new
    # checkout and removes it afterwards. Lane checkouts remain independent.
    temporary = Path(config["work_root"]) / "domestic-main-check-current"
    scratch = execution_root / ("ac-" + token)
    if any(value.exists() or value.is_symlink() for value in (temporary, scratch)):
        raise CheckEnvironmentError("unreclaimed check workspace exists; retained for registered recovery or diagnosis")
    record_path = directory / (report.name + "-attempt.json")
    helper = policy / "scripts/ci/check_preparation.py"
    record = {"schema":1, "pid":os.getpid(), "status":"active", "head_sha":head_sha,
              "guard_build_processes":bool(config.get("_check_process_guard")),
              "temporary":str(temporary), "scratch":str(scratch), "report":str(report), "policy":str(policy),
              "cleanup_helper_sha256":_file_sha256(helper) if helper.is_file() else None,
              "database_identity":_check_database_identity(config),
              "started_at_utc":_utc_now(), "retention":"generated objects until evidence saved; unresolved evidence retained"}
    atomic_json(record_path, record)
    # Register names before creation so SIGKILL cannot leave untracked objects.
    try:
        temporary.mkdir(mode=0o700)
        scratch.mkdir(mode=0o700)
    except BaseException:
        record["status"]="evidence_saved_cleanup_pending"
        atomic_json(record_path,record)
        _cleanup_attempt_record(config,record_path,record)
        raise
    config["_check_tmpdir"] = str(scratch)
    free_before = shutil.disk_usage(temporary).free
    scratch_free_before = shutil.disk_usage(scratch).free
    report_free_before = shutil.disk_usage(report).free
    minimum = {"free":free_before}
    scratch_minimum = {"free":scratch_free_before}
    report_minimum = {"free":report_free_before}
    stopped = threading.Event()
    def sample():
        while not stopped.wait(0.5):
            minimum["free"] = min(minimum["free"], shutil.disk_usage(temporary.parent).free)
            scratch_minimum["free"] = min(scratch_minimum["free"], shutil.disk_usage(scratch.parent).free)
            report_minimum["free"] = min(report_minimum["free"], shutil.disk_usage(report.parent).free)
    sampler = threading.Thread(target=sample, daemon=True)
    sampler.start()
    try:
        yield str(temporary)
    finally:
        original_error = sys.exc_info()[1]
        stopped.set()
        sampler.join()
        minimum["free"] = min(minimum["free"], shutil.disk_usage(temporary.parent).free)
        scratch_minimum["free"] = min(scratch_minimum["free"], shutil.disk_usage(scratch.parent).free)
        report_minimum["free"] = min(report_minimum["free"], shutil.disk_usage(report.parent).free)
        record["status"] = "evidence_saved_cleanup_pending"
        record.update(free_before_bytes=free_before, minimum_free_bytes=minimum["free"],
                      peak_disk_increment_bytes=max(0, free_before-minimum["free"]),
                      execution_free_before_bytes=scratch_free_before,
                      execution_minimum_free_bytes=scratch_minimum["free"],
                      execution_peak_disk_increment_bytes=max(0,scratch_free_before-scratch_minimum["free"]),
                      report_storage_free_before_bytes=report_free_before,
                      report_storage_minimum_free_bytes=report_minimum["free"],
                      report_storage_peak_disk_increment_bytes=max(0,report_free_before-report_minimum["free"]))
        atomic_json(record_path, record)
        try:
            _cleanup_attempt_record(config, record_path, record)
        except BaseException as exc:
            record["cleanup_error_type"] = type(exc).__name__
            atomic_json(record_path, record)
            if original_error is not None:
                # Cleanup blockage cannot relabel a product assertion or an
                # unknown interrupted execution as a resumable environment fault.
                raise original_error from exc
            raise
        finally:
            config.pop("_check_tmpdir", None)


def _prepare_exact_source_views(config: dict, policy: Path, checkout: Path) -> None:
    """Materialize generated embed inputs in each disposable exact-head checkout."""
    if (checkout / "web/donor-sources/source-index.json").is_file():
        prepared = _build_command(config,["node",str(policy / "scripts/prepare-donor-source-views.mjs"),
                                  "--root",str(checkout)],cwd=checkout,timeout=180,
                                  check=False,safe_repository=checkout)
        if prepared.returncode:
            raise CheckIncompleteError("exact-source embed preparation did not complete")
        if ((checkout / "internal/config/http/openapi.go").is_file()
                and not (checkout / "internal/config/http/openapi.yaml").is_file()):
            raise CheckIncompleteError("exact-source OpenAPI embed view was not materialized")


def _discover_check_packages(config: dict, policy: Path, checkout: Path,
                             plan: dict, enforced: dict) -> list[str]:
    # The graph and actual tests materialize these same exact-source embed
    # inputs. Never generate them in the protected authority worktree.
    _prepare_exact_source_views(config, policy, checkout)
    expressions = (["./"+item["dir"] for item in plan["graph_result"]["selected_packages"]]
                   if enforced["selection_mode"] == "targeted" else ["./..."])
    inventory = _build_command(config,["go","list","-f","{{.ImportPath}}",*expressions],
                               cwd=checkout,timeout=300,check=False,safe_repository=checkout)
    if inventory.returncode or not inventory.stdout.strip():
        raise CheckIncompleteError("trusted backend package discovery did not complete")
    return sorted(set(inventory.stdout.splitlines()))


def _check_execution_preflight(config: dict, policy: Path, checkout: Path,
                               report: Path, browser_required: bool) -> dict:
    code = """import json,os,stat
from pathlib import Path
p=Path(os.environ['TMPDIR']);fd=os.open('/',os.O_RDONLY|os.O_DIRECTORY)
try:
 for part in p.parts[1:]:
  nxt=os.open(part,os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW,dir_fd=fd);os.close(fd);fd=nxt
 s=os.fstat(fd)
 if s.st_uid!=os.geteuid() or stat.S_IMODE(s.st_mode)!=0o700: raise SystemExit(2)
 print(json.dumps({'uid':s.st_uid,'mode':stat.S_IMODE(s.st_mode),'root':str(p.parent),'device':s.st_dev}))
finally: os.close(fd)
"""
    result = _build_command(config,["/usr/bin/python3","-c",code],cwd=checkout,timeout=30,check=False)
    if result.returncode:
        raise CheckEnvironmentError("private TMPDIR cannot be opened under the actual build account")
    try:
        identity = json.loads(result.stdout.strip())
    except ValueError as exc:
        raise CheckIncompleteError("private TMPDIR capability evidence is missing") from exc
    if not browser_required:
        return identity
    # Real DevTools startup catches socket-length, permission and launch
    # failures that an executable --version check cannot detect.
    browser_code = """import {resolveChromiumBinary} from %s;
import {spawn,execFileSync} from 'node:child_process';
import fs from 'node:fs/promises';import path from 'node:path';import os from 'node:os';
const env={...process.env};delete env.AICRM_DATABASE_URL;
let binary=null,version=null,profile,child,first='',tail='',error;
const evidence={devtools_ready:false,tmpdir_bytes:Buffer.byteLength(os.tmpdir())};
try {
 binary=resolveChromiumBinary();version=execFileSync(binary,['--version'],{encoding:'utf8',timeout:10000,env}).trim();
 evidence.binary=binary;evidence.version=version;
 profile=await fs.mkdtemp(path.join(os.tmpdir(),'p-'));
 child=spawn(binary,['--headless=new','--no-sandbox','--remote-debugging-port=0','--user-data-dir='+profile,'--ignore-certificate-errors','--allow-insecure-localhost','about:blank'],{env,detached:true,stdio:['ignore','ignore','pipe']});
 child.on('error',e=>error=e.code||e.name);
 child.stderr.on('data',c=>{const text=String(c);first=(first+text).slice(0,16384);tail=(tail+text).slice(-16384)});
 const end=Date.now()+30000;
 while(Date.now()<end&&!error&&child.exitCode===null&&!child.signalCode){
  try {
   const port=(await fs.readFile(path.join(profile,'DevToolsActivePort'),'utf8')).split('\\n')[0];
   if(/^\\d+$/.test(port)){
    const response=await fetch('http://127.0.0.1:'+port+'/json/version',{signal:AbortSignal.timeout(2000)});
    if(response.ok&&(await response.json()).webSocketDebuggerUrl){evidence.devtools_ready=true;break}
   }
  }catch{}
  await new Promise(r=>setTimeout(r,50));
 }
 evidence.exit_code=child.exitCode;evidence.signal=child.signalCode;evidence.launch_error=error||null;
}catch(e){evidence.launch_error=e.code||e.name||'unknown_error'}finally{
 if(child?.pid){
  const closed=new Promise(r=>child.exitCode!==null||child.signalCode?r():child.once('close',r));
  const waitForClose=async timeout=>{
   let timer;
   try{await Promise.race([closed,new Promise(r=>timer=setTimeout(r,timeout))])}
   finally{clearTimeout(timer)}
  };
  try{process.kill(-child.pid,'SIGTERM')}catch{}
  await waitForClose(3000);
  try{process.kill(-child.pid,'SIGKILL')}catch{}
  await waitForClose(2000);
 }
 const redact=s=>(profile?s.replaceAll(profile,'<profile>'):s).replaceAll(os.tmpdir(),'<tmp>').replace(/https?:\\/\\/\\S+/g,'<url>');
 evidence.stderr_first=redact(first);evidence.stderr_tail=redact(tail);
 await fs.writeFile(process.argv[1],JSON.stringify(evidence)+'\\n',{mode:0o600});
 if(profile)await fs.rm(profile,{recursive:true,force:true});
}
console.log(JSON.stringify({binary,version,devtools_ready:evidence.devtools_ready}));
process.exitCode=evidence.devtools_ready?0:2;
""" % json.dumps((policy / "internal/webshell/chromium_binary.mjs").as_uri())
    result = _build_command(config,["node","--input-type=module","-e",browser_code,
                                   str(report / "browser-precheck.json")],cwd=checkout,timeout=90,check=False)
    if result.returncode:
        raise CheckEnvironmentError("real Chromium DevTools startup failed; see browser-precheck.json")
    try:
        identity["chromium"] = json.loads(result.stdout.splitlines()[-1])
    except (ValueError, IndexError) as exc:
        raise CheckIncompleteError("real Chromium startup evidence is missing") from exc
    return identity


def _check_database_capability(config: dict, checkout: Path, prep: Path) -> None:
    # Bootstrap against an old trusted baseline without importing candidate
    # policy. Every CREATE is registered before execution for crash recovery.
    code = """import json,os,subprocess,sys,uuid
from pathlib import Path
from urllib.parse import urlsplit,unquote,parse_qs
p=urlsplit(os.environ['AICRM_DATABASE_URL']);name=unquote(p.path.strip('/'))
if p.hostname not in ('localhost','127.0.0.1','::1') or not (name=='aicrm_ci' or name.startswith('aicrm_test_')):raise SystemExit(21)
env=dict(os.environ,PGHOST=p.hostname,PGPORT=str(p.port or 5432),PGUSER=unquote(p.username or ''),PGPASSWORD=unquote(p.password or ''),PGDATABASE=name,PGSSLMODE=parse_qs(p.query).get('sslmode',['prefer'])[0])
def sql(q):
 return subprocess.run(['psql','-X','-v','ON_ERROR_STOP=1','-Atqc',q],env=env,capture_output=True,text=True,timeout=15)
version=sql('SHOW server_version_num')
if version.returncode or not version.stdout.strip().startswith('16'):raise SystemExit(22)
prep=Path(sys.argv[1]);names=['aicrm_test_'+kind+'_'+uuid.uuid4().hex[:16]+'_acceptance_test' for kind in ('tpl','clone')]
for name in names:
 target=prep/('database-'+name+'.json');temporary=target.with_suffix('.tmp')
 with temporary.open('x') as out:
  os.chmod(temporary,0o600);json.dump({'database':name,'purpose':'capability_probe'},out);out.flush();os.fsync(out.fileno())
 os.replace(temporary,target)
ready=False
try:
 ready=(sql('CREATE DATABASE "'+names[0]+'" TEMPLATE template0').returncode==0 and
        sql('ALTER DATABASE "'+names[0]+'" ALLOW_CONNECTIONS false').returncode==0 and
        sql('CREATE DATABASE "'+names[1]+'" TEMPLATE "'+names[0]+'"').returncode==0)
finally:
 for name in reversed(names):
  try:dropped=sql('DROP DATABASE IF EXISTS "'+name+'"').returncode==0
  except (OSError,subprocess.SubprocessError):dropped=False
  if dropped:(prep/('database-'+name+'.json')).unlink()
  ready=ready and dropped
print(json.dumps({'ready':ready}))
raise SystemExit(0 if ready else 23)
"""
    result = _build_command(config, ["/usr/bin/python3", "-c", code, str(prep)],
                            cwd=checkout, timeout=120, check=False)
    if result.returncode:
        raise CheckEnvironmentError("PostgreSQL 16 synthetic CREATE/TEMPLATE/DROP failed under the actual build account")


def _create_check_attempt_database(config: dict, checkout: Path, prep: Path) -> str:
    # Legacy fixtures create schemas and rely on defer to remove them. Putting
    # their anchor inside the existing DB registry also covers SIGKILL, without
    # guessing which schemas in the shared configured database are disposable.
    code = """import json,os,subprocess,sys,uuid
from pathlib import Path
from urllib.parse import urlsplit,unquote,parse_qs
p=urlsplit(os.environ['AICRM_DATABASE_URL'])
env=dict(os.environ,PGHOST=p.hostname,PGPORT=str(p.port or 5432),PGUSER=unquote(p.username or ''),
 PGPASSWORD=unquote(p.password or ''),PGDATABASE=unquote(p.path.strip('/')),
 PGSSLMODE=parse_qs(p.query).get('sslmode',['prefer'])[0])
name='aicrm_test_clone_'+uuid.uuid4().hex[:16]+'_acceptance_test'
target=Path(sys.argv[1])/('database-'+name+'.json');temporary=target.with_suffix('.tmp')
with temporary.open('x') as out:
 os.chmod(temporary,0o600);json.dump({'database':name,'purpose':'attempt_anchor'},out);out.flush();os.fsync(out.fileno())
os.replace(temporary,target)
result=subprocess.run(['psql','-X','-v','ON_ERROR_STOP=1','-Atqc',
 'CREATE DATABASE "'+name+'" TEMPLATE template0'],env=env,capture_output=True,text=True,timeout=30)
if result.returncode:raise SystemExit(21)
print(json.dumps({'database':name}))
"""
    result = _build_command(config, ["/usr/bin/python3", "-c", code, str(prep)],
                            cwd=checkout, timeout=60, check=False)
    if result.returncode:
        raise CheckEnvironmentError("registered synthetic attempt database creation failed; retained inventory")
    try:
        name = json.loads(result.stdout.splitlines()[-1])["database"]
    except (ValueError, KeyError, TypeError, IndexError) as exc:
        raise CheckIncompleteError("synthetic attempt database evidence is missing") from exc
    if not isinstance(name, str) or not re.fullmatch(r"aicrm_test_clone_[0-9a-f]{16}_acceptance_test", name):
        raise CheckIncompleteError("synthetic attempt database evidence is invalid")
    return name


def _check_report(config: dict[str, Any], repo: Path, worktree: Path, report_dir: Path,
                  base_sha: str, head_sha: str, *, diagnostic_root: Path | None = None) -> dict[str, Any]:
    storage = _check_storage_mount(config)  # Before recovery or any filesystem mutation.
    if report_dir.exists() or report_dir.is_symlink():
        raise ReleaseError("candidate check evidence path already exists; inspect before retry")
    recovered = _recover_check_attempts(config)
    source_recovery = _reclaim_policy_worktrees(config, repo, base_sha)
    package_recovery = _reclaim_unreferenced_packages(config)
    archived_evidence = _archive_closed_check_evidence(config)
    cache_migration = _merge_legacy_check_caches(config)
    cache_usage = _trim_check_caches(config)
    capacity = _check_capacity(config, Path(config["work_root"]))
    if storage["mode"] == "data_disk":
        storage["capacity"] = _check_capacity(config, Path(storage["mount"]), "check_storage_capacity")
    _build_command(config, ["/usr/bin/mkdir", "-m", "0700", "-p", str(report_dir)], cwd=Path("/"), timeout=30)
    report_info = report_dir.lstat()
    if not stat.S_ISDIR(report_info.st_mode) or report_info.st_uid != pwd.getpwnam(legacy.BUILD_USER).pw_uid or stat.S_IMODE(report_info.st_mode) != 0o700:
        raise ReleaseError("check evidence directory is not owned by the isolated build account")
    policy = _policy_worktree(repo, Path(config["work_root"]), base_sha, config["push_group"])
    changed_policy = _check_policy_changes(repo, base_sha, head_sha)
    toolchain = _verify_build_toolchain(config)
    plan = _trusted_preflight_plan(config, policy, worktree, base_sha, head_sha)
    enforced, lanes, checks, packages, profile = _enforced_lanes(plan, changed_policy)
    config = dict(config, _check_process_guard=True)

    diagnostic_root = diagnostic_root or (Path(config["work_root"]) / "diagnostics")
    _safe_directory(diagnostic_root, create=True)
    _safe_directory(Path(config["work_root"]))
    previous_path = config.get("_check_resume_checkpoint")
    prior = {}
    with _check_attempt_directory(config, report_dir, policy, head_sha) as temporary:
        check_root = Path(temporary)
        os.chmod(check_root, 0o711)
        build_info = pwd.getpwnam(legacy.BUILD_USER)
        execution_parent = check_root / "execution"
        execution_parent.mkdir(mode=0o700)
        os.chown(execution_parent, build_info.pw_uid, build_info.pw_gid)
        os.chmod(execution_parent, 0o700)
        attempt_tmp = Path(config["_check_tmpdir"])
        os.chown(attempt_tmp, build_info.pw_uid, build_info.pw_gid)
        execution_worktree = _private_check_checkout(
            config, repo, execution_parent / "candidate", head_sha)
        prep = report_dir / ".preparation"
        _build_command(config, ["/usr/bin/mkdir", "-m", "0700", str(prep)], cwd=execution_worktree, timeout=30)
        check_config = dict(config, _check_snapshots={}, _check_tmpdir=str(attempt_tmp))
        check_config["_check_preparation_dir"] = str(prep)
        preparation_cache = _check_preparation_cache(check_config)
        if preparation_cache is not None:
            check_config["_check_preparation_cache"] = str(preparation_cache)
        toolchain["execution"] = _check_execution_preflight(check_config, policy, execution_worktree, report_dir, "browser" in lanes)
        if (policy / "scripts/ci/check_preparation.py").is_file():
            probe_code = """import sys,json
from pathlib import Path
sys.path.insert(0, str(Path(sys.argv[1]) / 'scripts/ci'))
import quality_lanes
quality_lanes.ROOT = Path(sys.argv[2])
missing = {lane: quality_lanes.missing_prerequisites(lane) for lane in json.loads(sys.argv[3])}
print(json.dumps({lane: items for lane, items in missing.items() if items}))
"""
            probe = _build_command(check_config, ["/usr/bin/python3", "-c", probe_code, str(policy),
                                   str(worktree), json.dumps(lanes)], cwd=worktree, timeout=180,
                                   check=False, safe_repository=worktree,
                                   base_sha=base_sha, head_sha=head_sha)
            if probe.returncode:
                raise CheckIncompleteError("actual build account prerequisite probe did not complete")
            try:
                missing = json.loads(probe.stdout.splitlines()[-1])
            except (ValueError, IndexError) as exc:
                raise CheckIncompleteError("actual build account prerequisite evidence is missing") from exc
            if missing:
                raise CheckEnvironmentError("required check prerequisites missing: " + json.dumps(missing, sort_keys=True))
        elif set(lanes) & {"backend", "browser"}:
            _check_database_capability(check_config, execution_worktree, prep)
        if "backend" in lanes and (enforced["selection_mode"] == "full" or profile == "affected-packages"):
            check_config["_required_go_packages"] = _discover_check_packages(
                check_config,policy,execution_worktree,plan,enforced)
        continuation_identity = _check_resume_identity(
            check_config,plan,toolchain,enforced,lanes,checks,packages,profile)
        if previous_path:
            prior = _load_check_checkpoint(Path(previous_path),diagnostic_root,continuation_identity)
        check_config["_check_checkpoint"] = prior
        if set(lanes) & {"backend", "browser"}:
            check_config["_check_attempt_database"] = _create_check_attempt_database(
                check_config, execution_worktree, prep)
        try:
            lane_results = _run_check_lanes(
                check_config, repo, policy, execution_worktree, report_dir, base_sha, head_sha,
                enforced, lanes, checks, packages, profile, diagnostic_root)
        except CheckEnvironmentError as error:
            if (_worktree_git(worktree, "rev-parse", "HEAD") != head_sha
                    or _worktree_git(worktree, "status", "--porcelain=v1", "--untracked-files=all")):
                raise ReleaseError("candidate source changed during failed isolated checks") from error
            checkpoint = diagnostic_root / f"{head_sha}-{report_dir.name}-checkpoint.json"
            atomic_json(checkpoint, {"schema": 1, "failure_kind": "environment",
                "identity": continuation_identity, "recorded_at_utc": _utc_now(),
                "lanes": check_config["_check_snapshots"], "continued_from": previous_path if prior else None})
            error.checkpoint = str(checkpoint)
            raise
        if _verify_check_checkout_tree(repo, head_sha, execution_worktree) == 0:
            raise ReleaseError("isolated check candidate checkout has no tracked source files")
    if _worktree_git(worktree, "rev-parse", "HEAD") != head_sha or _worktree_git(worktree, "status", "--porcelain=v1", "--untracked-files=all"):
        raise ReleaseError("candidate source changed or became dirty during isolated checks")
    completed_checkpoint = diagnostic_root / f"{head_sha}-{report_dir.name}-checkpoint.json"
    atomic_json(completed_checkpoint, {"schema":1,"result":"passed","failure_kind":None,
        "identity":continuation_identity,"recorded_at_utc":_utc_now(),
        "lanes":check_config["_check_snapshots"],"continued_from":previous_path if prior else None})
    receipt = {
        "schema_version": 1, "status": "passed", "baseline_sha": base_sha,
        "baseline_tree": plan["baseline_tree"], "head_sha": head_sha, "head_tree": plan["head_tree"],
        "policy_fingerprint": plan["policy_fingerprint"],
        "trusted_policy_sha": base_sha,
        "selection_mode": enforced["selection_mode"],
        "profile": profile, "selected_lanes": lanes,
        "selection_reasons": enforced.get("selection_reasons"),
        "changed_paths": plan.get("changed_paths"), "toolchain": toolchain, "lane_results": lane_results,
        "business_assessment": plan.get("business_assessment"),
        "capacity":capacity, "storage":storage, "cache_usage":cache_usage, "recovered_attempts":recovered,
        "cache_migration":cache_migration,
        "source_recovery":source_recovery,
        "package_recovery":package_recovery,
        "archived_evidence":archived_evidence,
        "artifact_lifecycle":json.loads((_lifecycle_directory(config) / (report_dir.name + "-attempt.json")).read_text()),
        "execution_receipt_sha256": None, "verified_at_utc": _utc_now(),
        "continued_from": previous_path if prior else None,
        "check_checkpoint":str(completed_checkpoint),
    }
    canonical = json.dumps(receipt, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode()
    receipt["execution_receipt_sha256"] = hashlib.sha256(canonical).hexdigest()
    atomic_json(diagnostic_root / f"{head_sha}-{report_dir.name}-receipt.json", receipt)
    return receipt


def _verify_package(out: Path, head_sha: str, base_sha: str, head_tree: str) -> dict[str, Any]:
    metadata_path = out / "domestic-release.json"
    release = out / "release"
    if metadata_path.is_symlink() or not metadata_path.is_file() or release.is_symlink() or not release.is_dir():
        raise ReleaseError("release build output is incomplete")
    metadata = json.loads(metadata_path.read_text(encoding="utf-8"))
    if metadata.get("source_sha") != head_sha or metadata.get("source_tree") != head_tree:
        raise ReleaseError("release package does not bind to the exact candidate source")
    if metadata.get("base_sha") != base_sha or metadata.get("actual_ci_baseline_verified") is not False:
        raise ReleaseError("release build metadata has an unexpected base or gate claim")
    legacy.verify_release_artifact(release, metadata)
    return metadata


def _create_full_bundle(repo: Path, work_root: Path, head_sha: str, head_tree: str,
                        *, previous_main_sha: str | None = None,
                        baseline_transition: bool = False) -> tuple[Path, dict[str, Any]]:
    root = work_root / "source-bundles"
    _safe_directory(root, create=True)
    bundle = root / f"{head_sha}.bundle"
    metadata_path = root / f"{head_sha}.json"
    candidate_ref = _candidate_ref(head_sha)
    main_sha = _resolve_ref(repo, MAIN_REF)
    if head_sha != main_sha:
        _first_parent_chain(repo, main_sha, head_sha)
    if _tree(repo, head_sha) != head_tree:
        raise ReleaseError("source bundle candidate tree does not match its exact commit")
    previous_main_sha = _sha(previous_main_sha or main_sha, "previous domestic main SHA")
    if not _is_ancestor(repo, previous_main_sha, main_sha):
        raise ReleaseError("source bundle previous main is not an ancestor of its main ref")
    if baseline_transition and (head_sha != main_sha or previous_main_sha == main_sha):
        raise ReleaseError("baseline source bundle must advance domestic main beyond the installed application cursor")
    bundle_id = _candidate_ref(head_sha)

    def verify_bundle(digest: str) -> None:
        if bundle.is_symlink() or not bundle.is_file() or _file_sha256(bundle) != digest:
            raise ReleaseError("source bundle is missing or its digest does not match")
        empty_root = Path(tempfile.mkdtemp(prefix="aicrm-bundle-check-"))
        try:
            empty = empty_root / "verify.git"
            _run(["git", "init", "--bare", "--shared=0600", str(empty)])
            _run(["git", f"--git-dir={empty}", "bundle", "verify", str(bundle)], timeout=600)
            refs = _run(["git", f"--git-dir={empty}", "bundle", "list-heads", str(bundle)], timeout=600).stdout.splitlines()
            head_refs = {line.split(maxsplit=1)[1]: line.split(maxsplit=1)[0] for line in refs if len(line.split(maxsplit=1)) == 2}
            if set(head_refs) != {MAIN_REF, bundle_id} or head_refs.get(MAIN_REF) != main_sha or head_refs.get(bundle_id) != head_sha:
                raise ReleaseError("source bundle must contain only domestic main and the exact candidate ref")
            _run(["git", f"--git-dir={empty}", "fetch", "--no-tags", str(bundle), f"{bundle_id}:refs/heads/recovery"], timeout=600)
            restored = _git(empty, "rev-parse", "refs/heads/recovery")
            restored_tree = _tree(empty, restored)
            if restored != head_sha or restored_tree != head_tree:
                raise ReleaseError("self-contained source bundle recovery SHA/tree mismatch")
            _run(["git", f"--git-dir={empty}", "fsck", "--full", "--strict"], timeout=600)
        finally:
            shutil.rmtree(empty_root, ignore_errors=True)

    if bundle.exists() or bundle.is_symlink() or metadata_path.exists() or metadata_path.is_symlink():
        if (bundle.is_symlink() or metadata_path.is_symlink() or not bundle.is_file() or not metadata_path.is_file()):
            raise ReleaseError("existing source bundle pair is unsafe or incomplete")
        try:
            previous = json.loads(metadata_path.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError) as exc:
            raise ReleaseError("existing source bundle metadata is unreadable") from exc
        if not isinstance(previous, dict):
            raise ReleaseError("existing source bundle metadata has an invalid shape")
        digest = _file_sha256(bundle)
        expected_previous = {"source_sha": head_sha, "source_tree": head_tree,
                             "previous_main_sha": previous_main_sha,
                             "baseline_transition": baseline_transition,
                             "bundle_sha256": digest, "self_contained": True,
                             "verified_from_empty_repository": True}
        if any(previous.get(key) != value for key, value in expected_previous.items()):
            raise ReleaseError("existing source bundle metadata belongs to another transaction")
        verify_bundle(digest)
        return bundle, previous

    _run(["git", f"--git-dir={repo}", "bundle", "create", str(bundle), MAIN_REF, candidate_ref], timeout=600)
    os.chmod(bundle, 0o600)
    digest = _file_sha256(bundle)
    verify_bundle(digest)
    metadata = {"schema_version": 1, "source_sha": head_sha, "source_tree": head_tree,
                "previous_main_sha": previous_main_sha,
                "bundle_sha256": digest, "bundle_bytes": bundle.stat().st_size,
                "self_contained": True, "verified_from_empty_repository": True,
                "baseline_transition": baseline_transition,
                "verified_at_utc": _utc_now()}
    atomic_json(metadata_path, metadata)
    return bundle, metadata


def _production_ssh(config: dict[str, Any], *remote_args: str, timeout: int = 600) -> str:
    base = legacy.ssh_args(config)
    return legacy.command(*base, *remote_args, timeout=timeout)


def _upload_and_store_bundle(config: dict[str, Any], bundle: Path, metadata: dict[str, Any],
                             *, baseline_transition: bool = False) -> dict[str, Any]:
    sha, tree = metadata["source_sha"], metadata["source_tree"]
    if bool(metadata.get("baseline_transition")) != baseline_transition:
        raise ReleaseError("source bundle baseline mode does not match its upload operation")
    incoming_root = Path(config.get("prod_source_bundle_incoming", SOURCE_BUNDLE_INCOMING_ROOT))
    if str(incoming_root) != SOURCE_BUNDLE_INCOMING_ROOT:
        raise ReleaseError("production source bundle incoming path must use the fixed protected helper directory")
    remote_bundle = str(incoming_root / f"{sha}.bundle")
    transport = " ".join(shlex.quote(part) for part in legacy.ssh_args(config)[:-1])
    _production_ssh(config, "sudo", "-n", "/usr/bin/mkdir", "-p", "-m", "0700", SOURCE_BUNDLE_INCOMING_ROOT, timeout=30)
    legacy.command("rsync", "-a", "--no-owner", "--no-group", "--no-perms", "--checksum",
                   "--rsync-path=sudo -n /usr/bin/rsync", "-e", transport, str(bundle),
                   f"{config['prod_user']}@{config['prod_host']}:{remote_bundle}", timeout=1800)
    args = ["sudo", "-n", config["prod_helper"], "--save-domestic-source-bundle",
            "--source-bundle", remote_bundle, "--expected-source-sha", sha,
            "--expected-source-tree", tree,
            "--expected-previous-main-sha", metadata["previous_main_sha"],
            "--expected-bundle-sha256", metadata["bundle_sha256"]]
    if baseline_transition:
        args.append("--allow-baseline-transition")
    receipt = json.loads(_production_ssh(config, *args, timeout=1800).splitlines()[-1])
    expected = {"status": "verified", "source_sha": sha, "source_tree": tree,
                "previous_main_sha": metadata["previous_main_sha"],
                "bundle_sha256": metadata["bundle_sha256"], "self_contained": True}
    if not isinstance(receipt, dict) or any(receipt.get(key) != value for key, value in expected.items()):
        raise ReleaseError("production source backup receipt does not match the candidate bundle")
    if baseline_transition:
        # The installed 291 helper persists and verifies this flag in its
        # root-owned receipt, but its save/verify JSON responses intentionally
        # omit it. Verify the durable object through that compatible contract.
        return _verify_saved_production_bundle(config, metadata, baseline_transition=True)
    return receipt


def _verify_saved_production_bundle(config: dict[str, Any], metadata: dict[str, Any],
                                    *, baseline_transition: bool = False) -> dict[str, Any]:
    """Read and verify an already-persisted production bundle without upload."""
    args = ["sudo", "-n", config["prod_helper"], "--verify-domestic-source-backup",
            "--source-sha", metadata["source_sha"],
            "--source-tree", metadata["source_tree"],
            "--previous-main-sha", metadata["previous_main_sha"],
            "--source-bundle-sha256", metadata["bundle_sha256"]]
    if baseline_transition:
        args.append("--allow-baseline-transition")
    raw = _production_ssh(config, *args, timeout=600)
    try:
        receipt = json.loads(raw.splitlines()[-1])
    except (IndexError, json.JSONDecodeError) as exc:
        raise ReleaseError("production source backup verification returned invalid JSON") from exc
    expected = {"status": "verified", "source_sha": metadata["source_sha"],
                "source_tree": metadata["source_tree"],
                "previous_main_sha": metadata["previous_main_sha"],
                "bundle_sha256": metadata["bundle_sha256"], "self_contained": True}
    if not isinstance(receipt, dict) or any(receipt.get(key) != value for key, value in expected.items()):
        raise ReleaseError("production source backup readback does not match the saved bundle")
    if "bundle_bytes" in metadata and receipt.get("bundle_bytes") != metadata["bundle_bytes"]:
        raise ReleaseError("production source backup readback has a different bundle size")
    # The old helper validates baseline_transition against the durable receipt
    # before returning this response; only the response omits the field.
    if baseline_transition:
        receipt["baseline_transition"] = True
    return receipt


def _verify_production_cursor(config: dict[str, Any]) -> dict[str, Any]:
    raw = _production_ssh(config, "sudo", "-n", config["prod_helper"], "--read-domestic-main", timeout=60)
    payload = json.loads(raw.splitlines()[-1])
    if not isinstance(payload, dict) or payload.get("status") != "ready" or not isinstance(payload.get("cursor"), dict):
        raise ReleaseError("production domestic source cursor is not ready")
    cursor = payload["cursor"]
    _sha(cursor.get("main_sha"), "production main cursor")
    _sha(cursor.get("main_tree"), "production main tree")
    _sha(cursor.get("installed_app_sha"), "production installed application SHA")
    _sha(cursor.get("installed_app_tree"), "production installed application tree")
    _digest(cursor.get("installed_manifest_sha256"), "production manifest digest")
    _digest(cursor.get("install_receipt_sha256"), "production install receipt digest")
    _digest(cursor.get("source_bundle_sha256"), "production source bundle digest")
    receipt_digest = _digest(payload.get("install_receipt_sha256"), "raw production receipt digest")
    if receipt_digest != cursor["install_receipt_sha256"]:
        raise ReleaseError("production helper receipt digest does not match its domestic source cursor")
    receipt = payload.get("install_receipt")
    if not isinstance(receipt, dict) or receipt.get("source_sha") != cursor["installed_app_sha"]:
        raise ReleaseError("production helper returned an invalid installed application receipt")
    if receipt.get("source_tree") != cursor["installed_app_tree"] or receipt.get("manifest_sha256") != cursor["installed_manifest_sha256"]:
        raise ReleaseError("production installed application receipt differs from the source cursor")
    return payload


def _record_production_cursor(config: dict[str, Any], *, candidate_sha: str, candidate_tree: str,
                              previous_main_sha: str, installed_app: dict[str, str],
                              bundle_sha256: str, initialize: bool = False) -> dict[str, Any]:
    action = "--initialize-domestic-main" if initialize else "--record-domestic-main"
    args = ["sudo", "-n", config["prod_helper"], action,
            "--main-sha", candidate_sha, "--main-tree", candidate_tree,
            "--installed-app-sha", installed_app["sha"], "--installed-app-tree", installed_app["tree"],
            "--installed-manifest-sha256", installed_app["manifest_sha256"],
            "--source-bundle-sha256", bundle_sha256]
    if not initialize:
        args.extend(["--expected-previous-main-sha", previous_main_sha])
    output = json.loads(_production_ssh(config, *args, timeout=120).splitlines()[-1])
    if output.get("status") != "ready" or output.get("main_sha") != candidate_sha or output.get("main_tree") != candidate_tree:
        raise ReleaseError("production did not commit the exact domestic main cursor")
    return output


def _verify_stage_app(sha: str, manifest_sha: str) -> dict[str, Any]:
    data = legacy.stage_readback(sha)
    legacy.verify_readback(data, sha, manifest_sha)
    return data


def _verify_controller_files(config: dict[str, Any], repo: Path, head_sha: str,
                             paths: list[str]) -> dict[str, Any] | None:
    if not paths:
        return None
    if not isinstance(paths, list) or not all(isinstance(path, str) for path in paths):
        raise ReleaseError("fixed controller file inventory is invalid")
    supported = set(builder.FIXED_CONTROLLER_FILES) | {"scripts/domestic_main_release.py"}
    if any(path not in supported for path in paths):
        raise ReleaseError("candidate changes an unregistered fixed controller")
    legacy_paths = [path for path in paths if path != "scripts/domestic_main_release.py" and path not in HOST_UNIT_FILES]
    try:
        legacy_result = legacy.verify_controller_installation(config, repo, head_sha, legacy_paths) if legacy_paths else None
    except (RuntimeError, OSError) as exc:
        raise ControllerMaintenanceRequired("fixed deployment helper differs from the candidate; run the reviewed maintenance-check and installation path") from exc
    own_path = "scripts/domestic_main_release.py"
    own_result = None
    if own_path in paths:
        expected = hashlib.sha256(_source_blob(repo, head_sha, own_path)).hexdigest()
        installed = Path(config["controller_path"])
        if installed.is_symlink() or not installed.is_file():
            raise ReleaseError("fixed domestic controller is missing or unsafe")
        info = installed.stat()
        actual = _file_sha256(installed)
        if info.st_uid != 0 or info.st_mode & 0o022 or actual != expected:
            raise ControllerMaintenanceRequired("fixed domestic controller differs from the candidate; run the reviewed maintenance-check and installation path")
        own_result = {"expected_sha256": expected, "staging_sha256": actual}
    units = _verify_host_units(repo, head_sha, [path for path in paths if path in HOST_UNIT_FILES])
    return {"status": "matched", "files": legacy_result.get("files", {}) if legacy_result else {},
            **({own_path: own_result} if own_result else {}), "host_units": units,
            "verified_at_utc": _utc_now()}


def _verify_host_units(repo: Path, head_sha: str, paths: list[str]) -> dict[str, str]:
    """Bind the installed, root-protected new poller to the exact source tree."""
    result: dict[str, str] = {}
    for source in paths:
        if source not in HOST_UNIT_FILES:
            raise ReleaseError("unregistered domestic release unit")
        target = HOST_UNIT_FILES[source]
        if target.is_symlink() or not target.is_file():
            raise ControllerMaintenanceRequired("reviewed domestic release unit is not installed")
        info, parent = target.lstat(), target.parent.lstat()
        if (info.st_uid != 0 or info.st_mode & 0o022 or parent.st_uid != 0
                or parent.st_mode & 0o022 or not stat.S_ISDIR(parent.st_mode)):
            raise ReleaseError("domestic release unit is not protected by root ownership")
        expected = hashlib.sha256(_source_blob(repo, head_sha, source)).hexdigest()
        actual = _file_sha256(target)
        if actual != expected:
            raise ControllerMaintenanceRequired("installed domestic release unit differs from exact source")
        result[source] = actual
    return result


def _protected_exact_helper(path: Path, expected_sha256: str) -> bool:
    if path.is_symlink() or not path.is_file() or _file_sha256(path) != expected_sha256:
        return False
    info = path.lstat()
    parent = path.parent.lstat()
    if (not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_gid != 0 or info.st_mode & 0o022
            or not stat.S_ISDIR(parent.st_mode) or parent.st_uid != 0 or parent.st_gid != 0 or parent.st_mode & 0o022):
        raise ReleaseError("fixed staging helper is not protected by root ownership and permissions")
    return True


def _run_stage_helper_host_contract(config: dict[str, Any], repo: Path,
                                    candidate_sha: str) -> dict[str, Any] | None:
    expected = hashlib.sha256(_source_blob(repo, candidate_sha, "deploy/domestic-promote.py")).hexdigest()
    helper = Path(legacy._helper_fixed_path(config["stage_helper"]))
    if not _protected_exact_helper(helper, expected):
        return None
    probe = _run(["/usr/bin/sudo", "-n", "/usr/bin/python3", str(helper),
                  "--check-host-contract", "--expected-helper-sha256", expected],
                 timeout=180, check=False)
    if probe.returncode != 0:
        raise ReleaseError("installed candidate helper failed its read-only staging host contract")
    try:
        receipt = json.loads(probe.stdout.splitlines()[-1])
    except (IndexError, json.JSONDecodeError) as exc:
        raise ReleaseError("candidate staging helper returned invalid host-contract evidence") from exc
    if (not isinstance(receipt, dict) or receipt.get("host_role") != "staging"
            or receipt.get("postgres_major") != 16
            or receipt.get("database_connection") != "verified"
            or receipt.get("helper_sha256") != expected):
        raise ReleaseError("candidate staging helper host-contract evidence is not exact")
    return receipt


def _pending_archive_count(repo: Path, confirmed_sha: str | None, main_sha: str) -> int | None:
    if confirmed_sha is None:
        return None
    if not _is_ancestor(repo, confirmed_sha, main_sha):
        raise ReleaseError("last manually confirmed GitHub SHA is not domestic main ancestry")
    return len(_git(repo, "rev-list", "--first-parent", f"{confirmed_sha}..{main_sha}").splitlines())


def _is_ancestor(repo: Path, older: str, newer: str) -> bool:
    result = _run(["git", f"--git-dir={repo}", "merge-base", "--is-ancestor", older, newer], check=False)
    return result.returncode == 0


def _validate_baseline_identity(repo: Path, main_sha: str, main_tree: str,
                                installed_app: dict[str, str]) -> None:
    """Allow a source-only baseline tip while proving the installed app is its ancestor."""
    main_sha = _sha(main_sha, "baseline main SHA")
    main_tree = _sha(main_tree, "baseline main tree")
    app_sha = _sha(installed_app.get("sha"), "baseline installed app SHA")
    app_tree = _sha(installed_app.get("tree"), "baseline installed app tree")
    _git(repo, "cat-file", "-e", f"{app_sha}^{{commit}}")
    if not _is_ancestor(repo, app_sha, main_sha):
        raise ReleaseError("installed production app is not an ancestor of the domestic main baseline")
    if _tree(repo, app_sha) != app_tree or _tree(repo, main_sha) != main_tree:
        raise ReleaseError("installed app or domestic main tree differs from its recorded source")
    first_parent = _git(repo, "rev-list", "--first-parent", main_sha).splitlines()
    if app_sha not in first_parent:
        raise ReleaseError("installed production app is not on the first-parent chain of domestic main")
    if _classify_candidate(repo, app_sha, main_sha)["runtime_changed"]:
        raise ReleaseError("domestic baseline contains application changes newer than the installed production app")


def archive_ack(config: dict[str, Any], github_sha: str) -> dict[str, Any]:
    """Record a manual Mac-side GitHub push readback; this controller never contacts GitHub."""
    if os.geteuid() != 0:
        raise ReleaseError("archive acknowledgement must run through the restricted root endpoint")
    config = _check_config(config)
    repo, state_path = Path(config["repo"]), Path(config["state"])
    github_sha = _sha(github_sha, "manually confirmed GitHub SHA")
    with _locked(Path(config["lock"]), nonblocking=True):
        state = _load_state(state_path)
        if state.get("status") != "ready":
            raise ReleaseError("cannot record GitHub archive acknowledgement while a candidate outcome is unresolved")
        _git(repo, "cat-file", "-e", f"{github_sha}^{{commit}}")
        main_sha = _resolve_ref(repo, MAIN_REF)
        if main_sha != state["main"]["sha"] or not _is_ancestor(repo, github_sha, main_sha):
            raise ReleaseError("manual GitHub SHA must be an ancestor of current domestic main")
        previous = state.get("archive_ack")
        if previous and not _is_ancestor(repo, previous["sha"], github_sha):
            raise ReleaseError("manual archive acknowledgement cannot move backward")
        cursor = _verify_production_cursor(config)["cursor"]
        if cursor.get("main_sha") != main_sha or cursor.get("main_tree") != _tree(repo, main_sha):
            raise ReleaseError("production source cursor does not match domestic main")
        confirmed_at = _utc_now()
        state["archive_ack"] = {"sha": github_sha, "confirmed_at_utc": confirmed_at, "observed_by": "manual_cli"}
        _update_state(state_path, state, status="ready")
        return {"status": "recorded", "github_synced_sha": github_sha,
                "domestic_main_sha": main_sha,
                "pending_first_parent_count": _pending_archive_count(repo, github_sha, main_sha),
                "confirmed_at_utc": confirmed_at}


def _verify_prod_app(config: dict[str, Any], sha: str, manifest_sha: str) -> dict[str, Any]:
    data = legacy.prod_readback(config, sha)
    legacy.verify_readback(data, sha, manifest_sha)
    receipt = data.get("receipt")
    if not isinstance(receipt, dict) or receipt.get("source_sha") != sha or receipt.get("manifest_sha256") != manifest_sha or receipt.get("technical_status") != "installed_healthy":
        raise ReleaseError("production installed application receipt does not match exact version")
    return data


def _validate_stage_reset(state: dict[str, Any], stage_app: dict[str, str],
                          prod_app: dict[str, str]) -> None:
    if state.get("status") != "blocked" or state.get("staging_out_of_sync") is not True:
        raise ReleaseError("no failed staging transaction is awaiting reset acknowledgement")
    expected_prod = state.get("installed_app")
    batch = state.get("batch")
    expected_stage = batch.get("installed_app") if isinstance(batch, dict) else expected_prod
    if (not isinstance(expected_prod, dict) or not isinstance(expected_stage, dict)
            or stage_app != expected_stage or prod_app != expected_prod):
        raise ReleaseError("stage and production must read back their last independently verified applications")


def acknowledge_stage_reset(config: dict[str, Any]) -> dict[str, Any]:
    """Clear a staging-only block after an operator rebuilt synthetic data and restored the prior app."""
    if os.geteuid() != 0:
        raise ReleaseError("stage reset acknowledgement must run as root")
    config = _check_config(config)
    state_path = Path(config["state"])
    with _locked(Path(config["lock"]), nonblocking=True):
        state = _load_state(state_path)
        stage_app, _ = _installed_app_identity(config, production=False)
        prod_app, _ = _installed_app_identity(config, production=True)
        _validate_stage_reset(state, stage_app, prod_app)
        inflight = state.get("in_flight")
        if inflight is not None:
            if (not isinstance(inflight, dict) or inflight.get("stage_install_started") is not True
                    or inflight.get("production_install_started") is True or inflight.get("commit_started") is True):
                raise ReleaseError("staging reset cannot clear a transaction that may have reached production")
            item = next((entry for entry in state["queue"]
                         if entry.get("candidate_id") == inflight.get("candidate_id")
                         and entry.get("head_sha") == inflight.get("head_sha")), None)
            if item is None or item.get("status") != "failed":
                raise ReleaseError("staging reset cannot clear an unrecorded interrupted candidate")
            state["in_flight"] = None
        state["staging_out_of_sync"] = state.get("batch") is not None
        state["stage_reset_acknowledged_at_utc"] = _utc_now()
        # Preserve blocked state and failed queue item. The developer must
        # explicitly resubmit a corrected or rebased branch after this check.
        _update_state(state_path, state, status="blocked")
        return {"status": "stage_reset_verified", "installed_app_sha": prod_app["sha"],
                "staged_app_sha": stage_app["sha"], "main_sha": state["main"]["sha"]}


def _reconcile_identity(repo: Path, inflight: dict[str, Any]) -> tuple[str, str, str, dict[str, Any], dict[str, str], dict[str, str], bool]:
    """Validate all durable identities before reconcile makes any remote call."""
    sha = _sha(inflight.get("candidate_sha", inflight.get("head_sha")), "in-flight candidate SHA")
    tree = _sha(inflight.get("candidate_tree", inflight.get("head_tree")), "in-flight candidate tree")
    old_main = _sha(inflight.get("previous_main_sha", inflight.get("base_sha")), "in-flight previous main")
    _first_parent_chain(repo, old_main, sha)
    if _tree(repo, sha) != tree:
        raise ReleaseError("in-flight candidate tree no longer matches its commit")
    bundle_meta = inflight.get("bundle_meta")
    if (not isinstance(bundle_meta, dict) or bundle_meta.get("source_sha") != sha
            or bundle_meta.get("source_tree") != tree or bundle_meta.get("previous_main_sha") != old_main
            or bundle_meta.get("self_contained") is not True
            or bundle_meta.get("verified_from_empty_repository") is not True):
        raise ReleaseError("in-flight source bundle proof is missing; remain stopped")
    _digest(bundle_meta.get("bundle_sha256"), "in-flight source bundle digest")

    def app_identity(value: Any, label: str) -> dict[str, str]:
        if not isinstance(value, dict):
            raise ReleaseError(f"{label} identity is missing")
        identity = {
            "sha": _sha(value.get("sha"), f"{label} SHA"),
            "tree": _sha(value.get("tree"), f"{label} tree"),
            "manifest_sha256": _digest(value.get("manifest_sha256"), f"{label} manifest digest"),
        }
        if _tree(repo, identity["sha"]) != identity["tree"]:
            raise ReleaseError(f"{label} tree does not match its commit")
        return identity

    runtime_changed = inflight.get("runtime_changed")
    batch_mode = isinstance(inflight.get("batch_member_heads"), list)
    if batch_mode:
        runtime_changed = True
    if not isinstance(runtime_changed, bool):
        raise ReleaseError("in-flight runtime change classification is missing")
    installed_app = app_identity(inflight.get("installed_app"), "in-flight installed app")
    previous_app = app_identity(inflight.get("previous_installed_app"), "in-flight previous app")
    if runtime_changed:
        metadata = inflight.get("release_metadata")
        if ((not batch_mode and (installed_app["sha"] != sha or installed_app["tree"] != tree))
                or (batch_mode and not _is_ancestor(repo, installed_app["sha"], sha))
                or not isinstance(metadata, dict) or metadata.get("source_sha") != installed_app["sha"]
                or metadata.get("source_tree") != installed_app["tree"]
                or metadata.get("release_files_sha256") != installed_app["manifest_sha256"]):
            raise ReleaseError("in-flight application package identity is incomplete")
    elif installed_app != previous_app:
        raise ReleaseError("source-only candidate changed the installed application identity")
    return sha, tree, old_main, bundle_meta, installed_app, previous_app, runtime_changed


def _active_worktree(config: dict[str, Any], repo: Path, head_sha: str) -> Path:
    worktree = Path(config["source_worktree"])
    expected_parent = Path(config["work_root"]).resolve()
    if not worktree.is_absolute() or worktree.resolve(strict=False).parent != expected_parent:
        raise ReleaseError("source worktree must be a direct child of the protected work root")
    if worktree.exists() or worktree.is_symlink():
        # The controller owns this exact path. A registered prior checkout may
        # be replaced only after it has been proven to be a Git worktree.
        if worktree.is_symlink() or not worktree.is_dir():
            raise ReleaseError("stale candidate source worktree is unsafe")
        registered = _run(["git", f"--git-dir={repo}", "worktree", "list", "--porcelain"], check=False).stdout
        if str(worktree.resolve()) not in registered:
            raise ReleaseError("unregistered source worktree occupies the fixed candidate path")
        _run(["git", f"--git-dir={repo}", "worktree", "remove", "--force", str(worktree)])
    _run(["git", f"--git-dir={repo}", "worktree", "add", "--detach", str(worktree), head_sha],
         timeout=120, umask=0o022)
    _make_worktree_metadata_readable(repo, worktree)
    if _worktree_git(worktree, "rev-parse", "HEAD") != head_sha:
        raise ReleaseError("candidate checkout is not at the exact submitted head")
    if _worktree_git(worktree, "status", "--porcelain=v1", "--untracked-files=all"):
        raise ReleaseError("candidate checkout is not clean before checks")
    return worktree


def _needs_installed_alipay_smoke(changed_paths: list[str]) -> bool:
    return legacy._alipay_smoke_required({"changed_paths": changed_paths})


def _run_installed_smoke(config: dict[str, Any], worktree: Path, sha: str,
                         manifest_sha: str, helper_sha: str, tree_sha: str, *,
                         validation_scope_base_sha: str) -> dict[str, Any] | None:
    validation_scope_base_sha = _sha(validation_scope_base_sha, "installed smoke validation base SHA")
    plan_paths = _worktree_git(
        worktree, "diff", "--name-only", "--no-renames", validation_scope_base_sha, sha,
    ).splitlines()
    if not _needs_installed_alipay_smoke(plan_paths):
        return None
    source_ref = _candidate_ref(sha)
    output = legacy.command(
        "sudo", config["stage_helper"], "--run-staging-smoke",
        "--source-sha", sha, "--source-ref", source_ref,
        "--source-repository", config["repo"],
        "--expected-sha", sha, "--expected-manifest-sha256", manifest_sha,
        "--expected-helper-sha256", helper_sha,
        timeout=900,
    )
    receipt = json.loads(output.splitlines()[-1])
    expected = {"status": "passed", "source_sha": sha, "source_tree": tree_sha,
                "source_ref": source_ref, "installed_sha": sha,
                "manifest_sha256": manifest_sha, "helper_sha256": helper_sha,
                "stage_role": "staging"}
    if not isinstance(receipt, dict) or any(receipt.get(key) != value for key, value in expected.items()):
        raise ReleaseError("installed staging behavior receipt does not match the exact domestic candidate")
    return receipt


def _update_state(path: Path, state: dict[str, Any], **changes: Any) -> None:
    state.update(changes)
    state["updated_at_utc"] = _utc_now()
    _validate_state(state)
    atomic_json(path, state)


def _maintenance_check_checkpoint(config: dict[str, Any], marker: dict[str, Any]) -> str | None:
    """Resolve the completed capsule through the existing receipt digest.

    Keep the maintenance marker compatible with installed older controllers;
    only protected exact-receipt evidence can supply a continuation pointer.
    """
    directory = Path(config["work_root"]) / "diagnostics"
    if not directory.exists() and not directory.is_symlink():
        return None  # Older markers without a protected receipt need fresh checks.
    _safe_directory(directory)
    for path in sorted(directory.glob(f"{marker['candidate_sha']}-*-receipt.json")):
        descriptor = os.open(path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
        try:
            info = os.fstat(descriptor)
            if (not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid()
                    or info.st_nlink != 1 or stat.S_IMODE(info.st_mode) != 0o600):
                raise ReleaseError("maintenance check receipt is not protected")
            with os.fdopen(descriptor, "r", closefd=False) as stream:
                receipt = json.load(stream)
        finally:
            os.close(descriptor)
        if not isinstance(receipt, dict):
            raise ReleaseError("maintenance check receipt is malformed")
        canonical = json.dumps(receipt, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode()
        if hashlib.sha256(canonical).hexdigest() != marker["check_receipt_sha256"]:
            continue
        if (receipt.get("status") != "passed" or receipt.get("baseline_sha") != marker["base_sha"]
                or receipt.get("head_sha") != marker["candidate_sha"]
                or receipt.get("head_tree") != marker["candidate_tree"]):
            raise ReleaseError("maintenance check receipt identity differs from marker")
        checkpoint = receipt.get("check_checkpoint")
        if checkpoint is None:
            return None
        if (not isinstance(checkpoint, str) or Path(checkpoint).parent != directory
                or not Path(checkpoint).name.endswith("-checkpoint.json")):
            raise ReleaseError("maintenance check checkpoint is outside protected diagnostics")
        return checkpoint
    return None


def _advance_main_cas(repo: Path, old_sha: str, new_sha: str) -> None:
    old_sha, new_sha = _sha(old_sha, "expected old main SHA"), _sha(new_sha, "candidate main SHA")
    current = _resolve_ref(repo, MAIN_REF)
    if current == new_sha:
        return
    if current != old_sha:
        raise ReleaseError("domestic main changed during the release; CAS stopped")
    _first_parent_chain(repo, old_sha, new_sha)
    _run(["git", "-c", "core.sharedRepository=0", f"--git-dir={repo}",
          "update-ref", MAIN_REF, new_sha, old_sha], umask=0o022)


def _finalize_success(config: dict[str, Any], state_path: Path, state: dict[str, Any], item: dict[str, Any],
                      bundle_meta: dict[str, Any], previous_main_sha: str, installed_app: dict[str, str],
                      check_receipt: dict[str, Any], stage_receipt: dict[str, Any] | None,
                      production_receipt: dict[str, Any] | None, started: float) -> dict[str, Any]:
    repo = Path(config["repo"])
    candidate_sha = item["head_sha"]
    candidate_tree = _tree(repo, candidate_sha)
    cursor = _record_production_cursor(
        config, candidate_sha=candidate_sha, candidate_tree=candidate_tree,
        previous_main_sha=previous_main_sha, installed_app=installed_app,
        bundle_sha256=bundle_meta["bundle_sha256"],
    )
    # A successful cursor write is independently read back before the queue
    # entry becomes terminal. A lost response is reconciled without reinstall.
    observed = _verify_production_cursor(config)
    if observed["cursor"].get("main_sha") != candidate_sha or observed["cursor"].get("source_bundle_sha256") != bundle_meta["bundle_sha256"]:
        raise ReleaseError("production source cursor readback differs from the exact candidate")
    _advance_main_cas(repo, previous_main_sha, candidate_sha)
    state["main"] = {"sha": candidate_sha, "tree": candidate_tree}
    item["status"] = "completed"
    item["completed_at_utc"] = _utc_now()
    item["check_receipt"] = check_receipt
    item["stage_receipt"] = stage_receipt
    item["production_receipt"] = production_receipt
    item["source_bundle"] = bundle_meta
    item["duration_seconds"] = round(time.monotonic() - started, 1)
    batch = state.get("batch")
    batch_heads = None
    if isinstance(batch, dict):
        if batch.get("status") not in {"sealed", "promoting"} or batch.get("head_sha") != candidate_sha:
            raise ReleaseError("completed batch differs from the production source cursor")
        batch_heads = [member["head_sha"] for member in batch["members"]]
        for member in batch["members"]:
            queued = next((entry for entry in state["queue"]
                           if entry.get("candidate_id") == member["candidate_id"]
                           and entry.get("head_sha") == member["head_sha"]), None)
            if queued is None:
                raise ReleaseError("completed batch member is absent from the serial queue")
            queued["status"] = "completed"
            queued["completed_at_utc"] = item["completed_at_utc"]
            queued["batch_final_head_sha"] = candidate_sha
            queued["production_receipt"] = production_receipt
        state["batch"] = None
    state["installed_app"] = installed_app
    state["staging_out_of_sync"] = False
    state["in_flight"] = None
    state["controller_maintenance"] = None
    ack = state.get("archive_ack")
    try:
        pending = _pending_archive_count(repo, ack.get("sha") if ack else None, candidate_sha)
    except ReleaseError:
        pending = None
    state["last_release"] = {"main_sha": candidate_sha, "main_tree": candidate_tree,
                             "installed_app_sha": installed_app["sha"],
                             "manifest_sha256": installed_app["manifest_sha256"],
                             "source_bundle_sha256": bundle_meta["bundle_sha256"],
                             "last_manual_confirmed_github_sha": ack.get("sha") if ack else "unknown",
                             "github_sync_observed_by": "manual_cli" if ack else "unknown",
                             "github_pending_first_parent_count": pending if pending is not None else "unknown",
                             "status": "ready", "completed_at_utc": item["completed_at_utc"],
                             "duration_seconds": item["duration_seconds"]}
    if batch_heads is not None:
        state["last_release"]["batch_member_heads"] = batch_heads
    _update_state(state_path, state, status="ready")
    cleanup = None
    try:
        cleanup = _production_ssh(config, "sudo", "-n", config["prod_helper"],
                                  "--reclaim-obsolete-releases", timeout=120)
    except (ReleaseError, OSError, subprocess.SubprocessError):
        cleanup = {"status": "maintenance-pending"}
    state["last_release"]["package_cleanup"] = cleanup
    _update_state(state_path, state, status="ready")
    return {"status": "completed", "main_sha": candidate_sha, "main_tree": candidate_tree,
            "installed_app_sha": installed_app["sha"], "source_bundle_sha256": bundle_meta["bundle_sha256"],
            "duration_seconds": item["duration_seconds"], "cursor": cursor}


def _mark_unknown(state_path: Path, state: dict[str, Any], phase: str, error: BaseException) -> None:
    inflight = state.get("in_flight") or {}
    inflight.update({"phase": phase, "error_type": type(error).__name__, "updated_at_utc": _utc_now()})
    state["in_flight"] = inflight
    state["status"] = "outcome_unknown"
    state["updated_at_utc"] = _utc_now()
    atomic_json(state_path, state)


def _mark_failed(state_path: Path, state: dict[str, Any], item: dict[str, Any], error: BaseException, phase: str) -> None:
    environment = isinstance(error, (CheckEnvironmentError, ControllerMaintenanceRequired))
    incomplete = isinstance(error, CheckIncompleteError) or (phase == "checks" and
        not isinstance(error,(CheckCandidateError,CheckEnvironmentError,ControllerMaintenanceRequired)))
    item["status"] = "pending" if environment else "failed"
    item["failure"] = {"phase": phase, "error_type": type(error).__name__, "recorded_at_utc": _utc_now(),
                       "kind": "environment" if environment else "unknown" if incomplete else "candidate_check",
                       "candidate_verdict": "not_evaluated" if environment or incomplete else "failed"}
    if incomplete:
        item["failure"]["required_action"] = "diagnose incomplete or unknown execution; no automatic continuation or passing verdict"
    if environment:
        item["failure"]["required_action"] = "repair the check environment, then resubmit the same exact candidate; keep queue order"
        if getattr(error, "checkpoint", None):
            item["failure"]["check_checkpoint"] = error.checkpoint
    if isinstance(error, ControllerMaintenanceRequired):
        item["failure"]["required_action"] = "run maintenance-check for this exact SHA; review and install the checked fixed controller bytes with rollback; resubmit the same candidate"
    if (state.get("in_flight") or {}).get("stage_install_started") is True:
        state["staging_out_of_sync"] = True
        item["failure"]["required_recovery"] = "restore the prior verified application and rebuild the synthetic staging database, then run ack-stage-reset"
    item.setdefault("attempt_history", []).append({"attempt": item.get("attempt"), **item["failure"]})
    state["in_flight"] = None
    state["controller_maintenance"] = None
    state["status"] = "blocked"
    state["updated_at_utc"] = _utc_now()
    atomic_json(state_path, state)


def _build_candidate(config: dict[str, Any], sha: str, base: str,
                     base_release: Path | None, *, validation_scope_base: str) -> tuple[Path, dict[str, Any]]:
    old_umask = os.umask(0o022)
    try:
        return legacy.build_candidate(config, sha, base, base_release,
                                      validation_scope_base=validation_scope_base)
    finally:
        os.umask(old_umask)


def process_candidate(config: dict[str, Any], state_path: Path, state: dict[str, Any], item: dict[str, Any]) -> dict[str, Any]:
    repo = Path(config["repo"])
    domestic_main_sha = _resolve_ref(repo, MAIN_REF)
    if domestic_main_sha != state["main"]["sha"]:
        raise ReleaseError("domestic main differs from the durable release ledger")
    old_main_sha = _batch_tip(state)
    if item["base_sha"] != old_main_sha:
        item["status"] = "stale_base"
        item["failure"] = {"phase": "base-check", "expected_base": old_main_sha,
                           "candidate_base": item["base_sha"], "recorded_at_utc": _utc_now()}
        state["controller_maintenance"] = None
        state["status"] = "blocked"
        _update_state(state_path, state)
        return {"status": "stale_base", "candidate_sha": item["head_sha"], "main_sha": old_main_sha}
    if _resolve_ref(repo, item["ref"]) != item["head_sha"]:
        item["status"] = "stale_base"
        item["failure"] = {"phase": "branch-head-check", "reason": "submitted branch has changed; resubmit exact updated head",
                           "recorded_at_utc": _utc_now()}
        state["controller_maintenance"] = None
        state["status"] = "blocked"
        _update_state(state_path, state)
        return {"status": "stale_base", "candidate_sha": item["head_sha"], "main_sha": old_main_sha}
    chain = _first_parent_chain(repo, old_main_sha, item["head_sha"])
    if not chain:
        raise ReleaseError("empty candidate cannot be released")
    _pin_candidate(repo, item["head_sha"])
    head_tree = _tree(repo, item["head_sha"])
    source_worktree = _active_worktree(config, repo, item["head_sha"])
    report_dir = Path(legacy.BUILD_ROOT) / "domestic-main-checks" / f"{item['head_sha']}-attempt-{int(item.get('attempt', 0)) + 1}"
    started = time.monotonic()
    phase = "checks"
    item["attempt"] = int(item.get("attempt", 0)) + 1
    item["status"] = "checking"
    item["started_at_utc"] = _utc_now()
    state["in_flight"] = {"candidate_id": item["candidate_id"], "ref": item["ref"],
                           "head_sha": item["head_sha"], "head_tree": head_tree,
                           "base_sha": old_main_sha, "base_tree": _tree(repo, old_main_sha),
                           "phase": phase, "production_install_started": False,
                           "updated_at_utc": _utc_now()}
    # Consume the one-shot maintenance authorization in the same durable
    # write that claims this exact candidate. Any earlier validation error
    # leaves the marker available for a safe retry.
    maintenance_marker = state.get("controller_maintenance")
    state["controller_maintenance"] = None
    _update_state(state_path, state, status="blocked")
    try:
        classification = _classify_candidate(source_worktree, old_main_sha, item["head_sha"])
        controller_files = classification.get("controller_files", [])
        controller_receipt = _verify_controller_files(config, source_worktree, item["head_sha"], controller_files)
        check_config = dict(config, _check_progress_callback=lambda progress: _update_state(
            state_path, state, in_flight={**state["in_flight"], "check_progress": progress}))
        if maintenance_marker and maintenance_marker.get("candidate_sha") == item["head_sha"]:
            check_config["_check_resume_checkpoint"] = _maintenance_check_checkpoint(config, maintenance_marker)
        if item.get("failure", {}).get("kind") == "environment" and item["failure"].get("check_checkpoint"):
            check_config["_check_resume_checkpoint"] = item["failure"].get("check_checkpoint")
        check_receipt = _check_report(check_config, repo, source_worktree, report_dir, old_main_sha, item["head_sha"])
        paths = check_receipt["changed_paths"]
        runtime_changed = bool(classification.get("runtime_changed"))
        batch = state.get("batch")
        installed_app = dict(batch["installed_app"] if batch else state["installed_app"])
        metadata = None
        stage_receipt = None
        smoke_receipt = None
        production_receipt = None
        bundle, bundle_meta = _create_full_bundle(repo, Path(config["work_root"]), item["head_sha"], head_tree)

        if runtime_changed:
            phase = "build"
            build_base = installed_app["sha"]
            base_release = Path(config.get("stage_releases", "/opt/aicrm/releases")) / build_base
            if (not base_release.is_dir() or base_release.is_symlink()
                    or builder.verify_release_inventory(base_release, allow_release_env=True)
                    != installed_app["manifest_sha256"]):
                raise ReleaseError("incremental base package differs from the installed application receipt")
            _verify_stage_app(build_base, installed_app["manifest_sha256"])
            production_base_app = state["installed_app"]
            _verify_prod_app(config, production_base_app["sha"], production_base_app["manifest_sha256"])
            build_config = dict(config, repo=str(source_worktree))
            out, metadata = _build_candidate(build_config, item["head_sha"], build_base,
                                             base_release, validation_scope_base=old_main_sha)
            if metadata.get("source_tree") != head_tree:
                raise ReleaseError("builder returned a package for a different source tree")
            if item.get("head_sha") != item["candidate_id"]:
                raise ReleaseError("candidate identity changed while the package was building")
            phase = "stage-install"
            _update_state(state_path, state, in_flight={**state["in_flight"], "phase": phase})
            state["in_flight"].update({"stage_install_started": True,
                                       "previous_stage_app": dict(installed_app),
                                       "prospective_stage_app": {"sha": item["head_sha"], "tree": head_tree,
                                                                  "manifest_sha256": metadata["release_files_sha256"]}})
            _update_state(state_path, state)
            state["staging_out_of_sync"] = True
            _update_state(state_path, state)
            stage_receipt = legacy.stage_install(build_config, item["head_sha"], out, build_base)
            if stage_receipt.get("source_sha") != item["head_sha"] or stage_receipt.get("technical_status") != "installed_healthy":
                raise ReleaseError("staging installation receipt does not match exact candidate")
            _verify_stage_app(item["head_sha"], metadata["release_files_sha256"])
            state["in_flight"].update({"stage_install_completed": True, "stage_receipt": stage_receipt})
            _update_state(state_path, state)
            smoke_required = _needs_installed_alipay_smoke(paths)
            if smoke_required:
                phase = "stage-smoke"
                _update_state(state_path, state, in_flight={**state["in_flight"], "phase": phase})
                helper_source = _source_blob(source_worktree, item["head_sha"], "deploy/domestic-promote.py")
                helper_sha = hashlib.sha256(helper_source).hexdigest()
                smoke_receipt = _run_installed_smoke(
                    config, source_worktree, item["head_sha"],
                    metadata["release_files_sha256"], helper_sha, head_tree,
                    validation_scope_base_sha=old_main_sha,
                )
                if smoke_receipt is None:
                    raise ReleaseError("required installed behavior check was not run")
            # Keep the existing attempt and queue position while the release
            # desk checks the exact installed package's affected business flow.
            # A later poll resumes this attempt; it never rebuilds or reinstalls.
            phase = "stage-validation-pending"
            _safe_directory(Path(config["work_root"]) / "stage-evidence", create=True)
            state["in_flight"].update({"phase": phase, "runtime_changed": True,
                                       "release_manifest_sha256": metadata["release_files_sha256"],
                                       "release_metadata": metadata,
                                       "package_metadata_sha256": hashlib.sha256((out / "domestic-release.json").read_bytes()).hexdigest(),
                                       "previous_installed_app": dict(installed_app),
                                       "installed_app": {"sha": item["head_sha"], "tree": head_tree,
                                                         "manifest_sha256": metadata["release_files_sha256"]},
                                       "stage_receipt": stage_receipt,
                                       "stage_smoke_receipt": smoke_receipt,
                                       "check_receipt": check_receipt,
                                       "controller_receipt": controller_receipt,
                                       "bundle_meta": bundle_meta,
                                       "production_install_started": False,
                                       "commit_started": False})
            item["status"] = "stage_validation_pending"
            _update_state(state_path, state, status="blocked")
            return {"status": "stage_validation_pending", "candidate_sha": item["head_sha"],
                    "manifest_sha256": metadata["release_files_sha256"],
                    "stage_receipt": stage_receipt,
                    "stage_evidence_path": str(Path(config["work_root"]) / "stage-evidence" / f"{item['head_sha']}.json")}
        else:
            if batch is not None:
                # A source-only member advances the cumulative source tip but
                # leaves the exact staged application bytes in place.
                _verify_stage_app(installed_app["sha"], installed_app["manifest_sha256"])
                state["in_flight"].update({"phase": "source-approval-pending", "runtime_changed": False,
                                           "check_receipt": check_receipt,
                                           "controller_receipt": controller_receipt,
                                           "bundle_meta": bundle_meta,
                                           "installed_app": installed_app})
                member = _batch_member_record(state["in_flight"], None)
                batch["members"].append(member)
                batch["head_sha"] = item["head_sha"]
                batch["head_tree"] = head_tree
                if controller_files:
                    batch["controller_source_sha"] = item["head_sha"]
                item["status"] = "staged"
                item["check_receipt"] = check_receipt
                item["source_bundle"] = bundle_meta
                state["in_flight"] = None
                _update_state(state_path, state, status="ready")
                return {"status": "batch_open", "staged_head_sha": item["head_sha"],
                        "staged_app_sha": installed_app["sha"], "member_count": len(batch["members"]),
                        "production_written": False}
            # Source-only changes also wait for human promotion before any
            # production source or cursor write.
            phase = "source-approval-pending"
            state["in_flight"].update({"phase": phase, "runtime_changed": False,
                                       "check_receipt": check_receipt,
                                       "controller_receipt": controller_receipt,
                                       "bundle_meta": bundle_meta,
                                       "previous_installed_app": dict(installed_app),
                                       "installed_app": dict(installed_app),
                                       "production_install_started": False,
                                       "commit_started": False,
                                       "candidate_sha": item["head_sha"],
                                       "candidate_tree": head_tree,
                                       "previous_main_sha": old_main_sha})
            item["status"] = "source_approval_pending"
            _verify_stage_app(installed_app["sha"], installed_app["manifest_sha256"])
            _update_state(state_path, state, status="blocked")
            return _approval_wait_result(state["in_flight"], None)

        # A source-only commit advances the recovery cursor, but must leave the
        # exact application package independently healthy on both hosts.
        _verify_stage_app(installed_app["sha"], installed_app["manifest_sha256"])

        phase = "main-cas-and-cursor"
        state["in_flight"].update({"phase": phase, "candidate_sha": item["head_sha"],
                                   "candidate_tree": head_tree, "previous_main_sha": old_main_sha,
                                   "previous_installed_app": state["installed_app"],
                                   "installed_app": installed_app, "bundle_meta": bundle_meta,
                                   "check_receipt": check_receipt, "stage_receipt": stage_receipt,
                                   "controller_receipt": controller_receipt,
                                   "production_receipt": production_receipt,
                                   "release_metadata": metadata,
                                   "package_metadata_sha256": (hashlib.sha256((Path(config["work_root"]) / "builds" / item["head_sha"] / "domestic-release.json").read_bytes()).hexdigest() if metadata else None),
                                   "runtime_changed": runtime_changed,
                                   "commit_started": True,
                                   "production_install_started": bool(runtime_changed and state["in_flight"].get("production_install_started"))})
        _update_state(state_path, state, status="outcome_unknown")
        return _finalize_success(config, state_path, state, item, bundle_meta, old_main_sha,
                                 installed_app, check_receipt, stage_receipt, production_receipt, started)
    except BaseException as exc:
        if state.get("in_flight", {}).get("commit_started") or state.get("in_flight", {}).get("production_install_started"):
            _mark_unknown(state_path, state, phase, exc)
        else:
            _mark_failed(state_path, state, item, exc, phase)
        raise


def _stage_evidence_path(config: dict[str, Any], sha: str) -> Path:
    return Path(config["work_root"]) / "stage-evidence" / f"{_sha(sha, 'stage candidate SHA')}.json"


def _read_stage_evidence(config: dict[str, Any], inflight: dict[str, Any]) -> dict[str, Any] | None:
    sha = _sha(inflight.get("head_sha"), "staged candidate SHA")
    path = _stage_evidence_path(config, sha)
    _safe_directory(path.parent)
    if not path.exists() and not path.is_symlink():
        return None
    if path.is_symlink() or not path.is_file():
        raise ReleaseError("stage journey evidence path is unsafe")
    info = path.lstat()
    if info.st_uid != os.geteuid() or info.st_nlink != 1 or stat.S_IMODE(info.st_mode) != 0o600:
        raise ReleaseError("stage journey evidence must be a protected controller receipt")
    try:
        receipt = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise ReleaseError("stage journey evidence is unreadable") from exc
    stage_receipt = inflight.get("stage_receipt")
    if not isinstance(stage_receipt, dict):
        raise ReleaseError("staged attempt has no installation receipt")
    stage_digest = hashlib.sha256(json.dumps(
        stage_receipt, sort_keys=True, separators=(",", ":")).encode()).hexdigest()
    expected = {
        "schema_version": 1, "status": "passed", "source_sha": sha,
        "source_tree": inflight["head_tree"],
        "manifest_sha256": inflight["release_manifest_sha256"],
        "stage_receipt_sha256": stage_digest,
    }
    if not isinstance(receipt, dict) or any(receipt.get(key) != value for key, value in expected.items()):
        raise ReleaseError("stage journey evidence differs from the exact installed attempt")
    verified_at = _utc_string(receipt.get("verified_at_utc"), "stage journey time")
    installed_at = _utc_string(stage_receipt.get("installed_at_utc"), "stage installation time")
    if datetime.fromisoformat(verified_at.replace("Z", "+00:00")) < datetime.fromisoformat(installed_at.replace("Z", "+00:00")):
        raise ReleaseError("stage journey predates the installed package")
    checks = receipt.get("checks")
    if not isinstance(checks, list) or not checks:
        raise ReleaseError("stage journey evidence has no business checks")
    names: set[str] = set()
    for check in checks:
        if not isinstance(check, dict):
            raise ReleaseError("stage journey check is malformed")
        name = check.get("name")
        if (not isinstance(name, str) or not re.fullmatch(r"[a-z0-9][a-z0-9_-]{1,79}", name)
                or name in names or check.get("status") != "passed"):
            raise ReleaseError("stage journey check is missing, repeated or unsuccessful")
        names.add(name)
        log_value = check.get("log")
        if not isinstance(log_value, str) or not log_value:
            raise ReleaseError("stage journey log path is missing")
        log = Path(log_value)
        if log.parent != path.parent or not log.name.startswith(f"{sha}-"):
            raise ReleaseError("stage journey log is outside the exact protected evidence directory")
        if log.is_symlink() or not log.is_file():
            raise ReleaseError("stage journey log is missing or unsafe")
        log_info = log.lstat()
        if log_info.st_uid != os.geteuid() or log_info.st_nlink != 1 or stat.S_IMODE(log_info.st_mode) != 0o600:
            raise ReleaseError("stage journey log is not protected")
        if _file_sha256(log) != _digest(check.get("log_sha256"), "stage journey log digest"):
            raise ReleaseError("stage journey log differs from its receipt")
    return receipt


def _batch_member_record(inflight: dict[str, Any], evidence: dict[str, Any] | None) -> dict[str, Any]:
    check = inflight.get("check_receipt")
    bundle = inflight.get("bundle_meta")
    app = inflight.get("installed_app")
    if not isinstance(check, dict) or check.get("status") != "passed" or not isinstance(bundle, dict) or not isinstance(app, dict):
        raise ReleaseError("staged member lacks its checks, source bundle or application identity")
    stage_receipt = inflight.get("stage_receipt")
    runtime = inflight.get("runtime_changed") is True
    if runtime and (not isinstance(stage_receipt, dict) or evidence is None):
        raise ReleaseError("runtime member lacks exact installed journey evidence")
    digest = lambda value: hashlib.sha256(json.dumps(
        value, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode()).hexdigest()
    return {
        "candidate_id": inflight["candidate_id"], "ref": inflight["ref"],
        "base_sha": _sha(inflight["base_sha"], "member base SHA"),
        "head_sha": _sha(inflight["head_sha"], "member head SHA"),
        "head_tree": _sha(inflight["head_tree"], "member tree"),
        "runtime_changed": runtime,
        "installed_app": dict(app),
        "check_receipt_sha256": digest(check),
        "stage_receipt_sha256": digest(stage_receipt) if runtime else None,
        "stage_journey_sha256": digest(evidence) if runtime else None,
        "stage_check_names": [entry["name"] for entry in evidence["checks"]] if runtime else [],
        "source_bundle_sha256": _digest(bundle["bundle_sha256"], "member bundle SHA"),
    }


def _verify_staged_member(config: dict[str, Any], repo: Path,
                          inflight: dict[str, Any], evidence: dict[str, Any]) -> None:
    sha = _sha(inflight["head_sha"], "staged member SHA")
    if (_resolve_ref(repo, inflight["ref"]) != sha or _tree(repo, sha) != inflight["head_tree"]
            or not _first_parent_chain(repo, inflight["base_sha"], sha)):
        raise ReleaseError("staged member source identity changed")
    app = inflight.get("installed_app")
    if not isinstance(app, dict) or app.get("sha") != sha:
        raise ReleaseError("staged runtime member has no exact application")
    _verify_stage_app(sha, app["manifest_sha256"])
    bundle = Path(config["work_root"]) / "source-bundles" / f"{sha}.bundle"
    if bundle.is_symlink() or not bundle.is_file() or _file_sha256(bundle) != inflight["bundle_meta"]["bundle_sha256"]:
        raise ReleaseError("staged member source bundle changed")
    if evidence.get("source_sha") != sha:
        raise ReleaseError("staged member journey source changed")


def _accept_staged_batch_member(config: dict[str, Any], state_path: Path,
                                state: dict[str, Any], item: dict[str, Any],
                                evidence: dict[str, Any]) -> dict[str, Any]:
    batch, inflight = state.get("batch"), state.get("in_flight")
    if (not isinstance(batch, dict) or batch.get("status") != "open"
            or not isinstance(inflight, dict) or inflight.get("base_sha") != _batch_tip(state)
            or item.get("head_sha") != inflight.get("head_sha")):
        raise ReleaseError("staged candidate is not the next member of the open batch")
    repo = Path(config["repo"])
    _verify_staged_member(config, repo, inflight, evidence)
    baseline = state["installed_app"]
    _verify_prod_app(config, baseline["sha"], baseline["manifest_sha256"])
    member = _batch_member_record(inflight, evidence)
    batch["members"].append(member)
    batch["head_sha"] = member["head_sha"]
    batch["head_tree"] = member["head_tree"]
    batch["installed_app"] = dict(inflight["installed_app"])
    batch["stage_receipt"] = dict(inflight["stage_receipt"])
    item["status"] = "staged"
    item["check_receipt"] = inflight["check_receipt"]
    item["stage_receipt"] = inflight["stage_receipt"]
    item["stage_journey_receipt"] = evidence
    item["source_bundle"] = inflight["bundle_meta"]
    state["in_flight"] = None
    state["staging_out_of_sync"] = True
    _update_state(state_path, state, status="ready")
    return {"status": "batch_open", "staged_head_sha": member["head_sha"],
            "staged_app_sha": batch["installed_app"]["sha"],
            "member_count": len(batch["members"]), "production_written": False}


def batch_open(config: dict[str, Any], *, controller_sha: str | None = None,
               controller_ref: str | None = None) -> dict[str, Any]:
    """Adopt a verified single staged attempt without touching production."""
    if os.geteuid() != 0:
        raise ReleaseError("opening a cumulative batch requires root")
    config = _check_config(config)
    repo, state_path = Path(config["repo"]), Path(config["state"])
    with _locked(Path(config["lock"]), nonblocking=True):
        state = _load_state(state_path)
        _recover_orphaned_inflight(state_path, state)
        if state.get("batch") is not None or state.get("status") != "blocked":
            raise ReleaseError("no single staged attempt is available to open a batch")
        inflight, item = state.get("in_flight"), _active_queue_item(state)
        if (not isinstance(inflight, dict) or inflight.get("phase") != "stage-validation-pending"
                or inflight.get("stage_install_completed") is not True
                or item is None or item.get("head_sha") != inflight.get("head_sha")):
            raise ReleaseError("batch opening requires the exact installed queue-front attempt")
        evidence = _read_stage_evidence(config, inflight)
        if evidence is None:
            raise ReleaseError("batch opening requires completed staging journeys")
        if _resolve_ref(repo, MAIN_REF) != state["main"]["sha"]:
            raise ReleaseError("domestic main changed before batch opening")
        checked_controller = state["main"]["sha"]
        controller_check = None
        if controller_sha is not None:
            checked_controller = _sha(controller_sha, "batch controller source SHA")
            if controller_ref is None or _resolve_ref(repo, _assert_ref_name(controller_ref)) != checked_controller:
                raise ReleaseError("batch controller source ref differs from its exact SHA")
            _first_parent_chain(repo, inflight["head_sha"], checked_controller)
            worktree = _active_worktree(config, repo, checked_controller)
            if _classify_candidate(worktree, inflight["head_sha"], checked_controller)["runtime_changed"]:
                raise ReleaseError("batch controller bootstrap may not include another runtime change")
            _verify_controller_files(config, repo, checked_controller,
                                     sorted(builder.FIXED_CONTROLLER_FILES))
            report = Path(legacy.BUILD_ROOT) / "domestic-main-checks" / f"{checked_controller}-batch-bootstrap-{time.time_ns()}"
            controller_check = _check_report(config, repo, worktree, report,
                                             inflight["head_sha"], checked_controller)
        else:
            _verify_controller_files(config, repo, checked_controller,
                                     sorted(builder.FIXED_CONTROLLER_FILES))
        _verify_staged_member(config, repo, inflight, evidence)
        baseline = state["installed_app"]
        _verify_prod_app(config, baseline["sha"], baseline["manifest_sha256"])
        member = _batch_member_record(inflight, evidence)
        state["batch"] = {"status": "open", "base_sha": state["main"]["sha"],
                          "base_tree": state["main"]["tree"],
                          "head_sha": member["head_sha"], "head_tree": member["head_tree"],
                          "installed_app": dict(inflight["installed_app"]),
                          "stage_receipt": dict(inflight["stage_receipt"]),
                          "members": [member], "opened_at_utc": _utc_now(),
                          "controller_source_sha": checked_controller,
                          "controller_check_receipt": controller_check}
        item["status"] = "staged"
        item["check_receipt"] = inflight["check_receipt"]
        item["stage_receipt"] = inflight["stage_receipt"]
        item["stage_journey_receipt"] = evidence
        item["source_bundle"] = inflight["bundle_meta"]
        state["in_flight"] = None
        state["staging_out_of_sync"] = True
        _update_state(state_path, state, status="ready")
        return {"status": "batch_open", "base_sha": state["batch"]["base_sha"],
                "staged_head_sha": member["head_sha"], "member_count": 1,
                "production_written": False}


def _batch_final_evidence(config: dict[str, Any], batch: dict[str, Any]) -> dict[str, Any] | None:
    app = batch["installed_app"]
    evidence = _read_stage_evidence(config, {
        "head_sha": batch["head_sha"], "head_tree": batch["head_tree"],
        "release_manifest_sha256": app["manifest_sha256"],
        "stage_receipt": batch["stage_receipt"],
    })
    if evidence is None:
        return None
    required = {name for member in batch["members"] for name in member["stage_check_names"]}
    observed = {check["name"] for check in evidence["checks"]}
    if not required <= observed:
        raise ReleaseError("final cumulative staging journeys omit an earlier member check")
    return evidence


def _batch_approval_result(batch: dict[str, Any], evidence: dict[str, Any]) -> dict[str, Any]:
    def digest(value: dict[str, Any]) -> str:
        return hashlib.sha256(json.dumps(value, ensure_ascii=False, sort_keys=True,
                                         separators=(",", ":")).encode()).hexdigest()
    identity = {
        "base_sha": batch["base_sha"], "head_sha": batch["head_sha"],
        "head_tree": batch["head_tree"],
        "members": [{key: member[key] for key in (
            "base_sha", "head_sha", "head_tree", "runtime_changed",
            "check_receipt_sha256", "stage_receipt_sha256",
            "stage_journey_sha256", "source_bundle_sha256")}
                    for member in batch["members"]],
        "installed_app": dict(batch["installed_app"]),
        "stage_receipt_sha256": digest(batch["stage_receipt"]),
        "final_journey_sha256": digest(evidence),
        "source_bundle_sha256": batch["members"][-1]["source_bundle_sha256"],
        "production_metadata_sha256": batch["production_metadata_sha256"],
    }
    return {"status": "awaiting_human_approval", "approval": identity,
            "approval_digest": digest(identity), "production_written": False}


def batch_seal(config: dict[str, Any], *, expected_head: str | None = None) -> dict[str, Any]:
    """Freeze the current package and bind its internal authorization digest."""
    if os.geteuid() != 0:
        raise ReleaseError("sealing a cumulative batch requires root")
    config = _check_config(config)
    repo, state_path = Path(config["repo"]), Path(config["state"])
    with _locked(Path(config["lock"]), nonblocking=True):
        state = _load_state(state_path)
        batch = state.get("batch")
        if (not isinstance(batch, dict) or batch.get("status") != "open"
                or state.get("status") != "ready" or state.get("in_flight") is not None
                or _active_queue_item(state) is not None):
            raise ReleaseError("cumulative batch is not ready to seal")
        if _resolve_ref(repo, MAIN_REF) != state["main"]["sha"]:
            raise ReleaseError("production source base changed before batch sealing")
        tip = _batch_tip(state)
        if expected_head is not None and tip != _sha(expected_head, "authorized batch head"):
            raise ReleaseError("batch scope changed after production authorization")
        last = batch["members"][-1]
        if _resolve_ref(repo, last["ref"]) != tip or _tree(repo, tip) != batch["head_tree"]:
            raise ReleaseError("final staged source identity changed")
        app = batch["installed_app"]
        _verify_stage_app(app["sha"], app["manifest_sha256"])
        baseline = state["installed_app"]
        _verify_prod_app(config, baseline["sha"], baseline["manifest_sha256"])
        stage_release = Path(config.get("stage_releases", "/opt/aicrm/releases")) / app["sha"]
        if builder.verify_release_inventory(stage_release, allow_release_env=True) != app["manifest_sha256"]:
            raise ReleaseError("final staged package inventory changed")
        bundle = Path(config["work_root"]) / "source-bundles" / f"{tip}.bundle"
        if bundle.is_symlink() or not bundle.is_file() or _file_sha256(bundle) != last["source_bundle_sha256"]:
            raise ReleaseError("final cumulative source bundle changed")
        evidence = _batch_final_evidence(config, batch)
        if evidence is None:
            raise ReleaseError("final cumulative staging journey is missing")
        out = Path(config["work_root"]) / "builds" / app["sha"]
        source_metadata = json.loads((out / "domestic-release.json").read_text())
        if (source_metadata.get("source_sha") != app["sha"]
                or source_metadata.get("release_files_sha256") != app["manifest_sha256"]):
            raise ReleaseError("final staged build metadata differs from its installed package")
        legacy.verify_release_artifact(out / "release", source_metadata)
        baseline_app = state["installed_app"]["sha"]
        changed = _git(repo, "diff", "--name-only", "--no-renames", baseline_app, app["sha"]).splitlines()
        promotion_metadata = dict(source_metadata)
        promotion_metadata.update({"base_sha": baseline_app, "changed_paths": changed,
                                   "migrations_changed": any(path.startswith("migrations/") for path in changed)})
        metadata_dir = Path(config["work_root"]) / "batch-evidence"
        _safe_directory(metadata_dir, create=True)
        metadata_path = metadata_dir / f"{tip}.production-metadata.json"
        if metadata_path.exists() or metadata_path.is_symlink():
            raise ReleaseError("existing batch promotion metadata requires inspection")
        atomic_json(metadata_path, promotion_metadata)
        batch["production_metadata_path"] = str(metadata_path)
        batch["production_metadata_sha256"] = _file_sha256(metadata_path)
        batch["status"] = "sealed"
        batch["final_journey_receipt"] = evidence
        batch["sealed_at_utc"] = _utc_now()
        result = _batch_approval_result(batch, evidence)
        batch["approval_digest"] = result["approval_digest"]
        _update_state(state_path, state, status="blocked")
        return result


def _checked_batch_controller_member(config: dict[str, Any], repo: Path,
                                     batch: dict[str, Any], candidate: str,
                                     ref: str, label: str) -> tuple[dict[str, Any], dict[str, Any], dict[str, Any]]:
    candidate = _sha(candidate, "batch controller SHA")
    ref = _assert_ref_name(ref)
    if _resolve_ref(repo, ref) != candidate:
        raise ReleaseError("batch controller ref differs from its exact SHA")
    _first_parent_chain(repo, batch["head_sha"], candidate)
    worktree = _active_worktree(config, repo, candidate)
    classification = _classify_candidate(worktree, batch["head_sha"], candidate)
    if (classification["runtime_changed"]
            or "scripts/domestic_main_release.py" not in classification["controller_files"]):
        raise ReleaseError("batch controller candidate must be source-only and change the controller")
    _verify_controller_files(config, repo, candidate, sorted(builder.FIXED_CONTROLLER_FILES))
    report = Path(legacy.BUILD_ROOT) / "domestic-main-checks" / f"{candidate}-{label}-{time.time_ns()}"
    check = _check_report(config, repo, worktree, report, batch["head_sha"], candidate)
    _pin_candidate(repo, candidate)
    _, bundle = _create_full_bundle(repo, Path(config["work_root"]), candidate,
                                     _tree(repo, candidate))
    member = _batch_member_record({
        "candidate_id": candidate, "ref": ref,
        "base_sha": batch["head_sha"], "head_sha": candidate,
        "head_tree": _tree(repo, candidate), "runtime_changed": False,
        "installed_app": batch["installed_app"], "check_receipt": check,
        "bundle_meta": bundle,
    }, None)
    return member, check, bundle


def _append_batch_controller_member(state: dict[str, Any], member: dict[str, Any],
                                    check: dict[str, Any], bundle: dict[str, Any],
                                    *, before: dict[str, Any] | None = None) -> None:
    batch = state["batch"]
    batch["members"].append(member)
    batch["head_sha"] = member["head_sha"]
    batch["head_tree"] = member["head_tree"]
    batch["controller_source_sha"] = member["head_sha"]
    queued = {"candidate_id": member["candidate_id"], "ref": member["ref"],
              "base_sha": member["base_sha"], "head_sha": member["head_sha"],
              "status": "staged", "check_receipt": check, "source_bundle": bundle,
              "submitted_at_utc": _utc_now()}
    if before is None:
        state["queue"].append(queued)
    else:
        state["queue"].insert(state["queue"].index(before), queued)


def batch_reopen(config: dict[str, Any], approval_digest: str, *,
                 controller_sha: str | None = None,
                 controller_ref: str | None = None) -> dict[str, Any]:
    """Invalidate an unapproved seal so more candidates can enter staging."""
    if os.geteuid() != 0:
        raise ReleaseError("reopening a cumulative batch requires root")
    config = _check_config(config)
    expected = _digest(approval_digest, "sealed batch digest")
    repo, state_path = Path(config["repo"]), Path(config["state"])
    with _locked(Path(config["lock"]), nonblocking=True):
        state = _load_state(state_path)
        _recover_orphaned_inflight(state_path, state)
        batch = state.get("batch")
        if (not isinstance(batch, dict) or batch.get("status") != "sealed"
                or state.get("status") != "blocked" or state.get("in_flight") is not None
                or _active_queue_item(state) is not None):
            raise ReleaseError("only an idle sealed batch can be reopened")
        evidence = _batch_final_evidence(config, batch)
        if evidence is None:
            raise ReleaseError("sealed batch staging journey is missing")
        current = _batch_approval_result(batch, evidence)["approval_digest"]
        if current != expected or batch.get("approval_digest") != expected:
            raise ReleaseError("sealed batch identity differs from the requested digest")
        if (_resolve_ref(repo, MAIN_REF) != batch["base_sha"]
                or state["main"]["tree"] != _tree(repo, batch["base_sha"])):
            raise ReleaseError("production source base changed before batch reopening")
        previous = batch["base_sha"]
        for member in batch["members"]:
            if (member["base_sha"] != previous
                    or _resolve_ref(repo, member["ref"]) != member["head_sha"]
                    or _tree(repo, member["head_sha"]) != member["head_tree"]):
                raise ReleaseError("sealed batch member source identity changed")
            _first_parent_chain(repo, previous, member["head_sha"])
            previous = member["head_sha"]
        app, baseline = batch["installed_app"], state["installed_app"]
        _verify_stage_app(app["sha"], app["manifest_sha256"])
        _verify_prod_app(config, baseline["sha"], baseline["manifest_sha256"])
        cursor = _verify_production_cursor(config)["cursor"]
        if (cursor.get("main_sha") != batch["base_sha"]
                or cursor.get("installed_app_sha") != baseline["sha"]
                or cursor.get("installed_manifest_sha256") != baseline["manifest_sha256"]):
            raise ReleaseError("production cursor changed before batch reopening")
        last = batch["members"][-1]
        bundle = Path(config["work_root"]) / "source-bundles" / f"{batch['head_sha']}.bundle"
        if (bundle.is_symlink() or not bundle.is_file()
                or _file_sha256(bundle) != last["source_bundle_sha256"]):
            raise ReleaseError("sealed batch source bundle changed")
        metadata = Path(batch["production_metadata_path"])
        if (metadata.is_symlink() or not metadata.is_file()
                or _file_sha256(metadata) != batch["production_metadata_sha256"]):
            raise ReleaseError("sealed batch production metadata changed")
        controller_member = None
        controller_check = None
        if controller_sha is not None:
            if controller_ref is None:
                raise ReleaseError("reopen controller SHA requires its exact ref")
            controller_member, controller_check, bundle_meta = _checked_batch_controller_member(
                config, repo, batch, controller_sha, controller_ref, "batch-reopen")
        elif controller_ref is not None:
            raise ReleaseError("reopen controller ref requires its exact SHA")
        else:
            _verify_controller_files(config, repo, batch.get("controller_source_sha", batch["base_sha"]),
                                     sorted(builder.FIXED_CONTROLLER_FILES))
        batch.setdefault("seal_history", []).append({
            "approval_digest": expected,
            "sealed_at_utc": batch["sealed_at_utc"],
            "reopened_at_utc": _utc_now(),
            "head_sha": batch["head_sha"],
            "production_metadata_sha256": batch["production_metadata_sha256"],
        })
        for key in ("approval_digest", "sealed_at_utc", "final_journey_receipt",
                    "production_metadata_path", "production_metadata_sha256"):
            batch.pop(key, None)
        if controller_member is not None:
            _append_batch_controller_member(state, controller_member, controller_check, bundle_meta)
            batch["reopen_controller_check_receipt"] = controller_check
        batch["status"] = "open"
        _update_state(state_path, state, status="ready")
        return {"status": "batch_open", "staged_head_sha": batch["head_sha"],
                "member_count": len(batch["members"]), "invalidated_approval_digest": expected,
                "production_written": False}


def batch_tool_repair(config: dict[str, Any], failed_candidate: str,
                      controller_sha: str, controller_ref: str) -> dict[str, Any]:
    """Stage a checked source-only controller fix ahead of an unevaluated queue front."""
    if os.geteuid() != 0:
        raise ReleaseError("batch tool repair requires root")
    config = _check_config(config)
    failed_candidate = _sha(failed_candidate, "failed candidate SHA")
    repo, state_path = Path(config["repo"]), Path(config["state"])
    with _locked(Path(config["lock"]), nonblocking=True):
        state = _load_state(state_path)
        _recover_orphaned_inflight(state_path, state)
        batch, front = state.get("batch"), _active_queue_item(state)
        if (not isinstance(batch, dict) or batch.get("status") != "open"
                or state.get("status") != "blocked" or state.get("in_flight") is not None
                or front is None or front.get("status") != "failed"
                or front.get("head_sha") != failed_candidate):
            raise ReleaseError("batch tool repair requires the exact unevaluated queue front")
        failure = front.get("failure") or {}
        if (failure.get("phase") != "checks"
                or failure.get("candidate_verdict") != "not_evaluated"
                or failure.get("kind") not in {"unknown", "environment"}):
            raise ReleaseError("batch tool repair cannot bypass a candidate failure")
        if (_resolve_ref(repo, MAIN_REF) != batch["base_sha"]
                or _tree(repo, batch["base_sha"]) != state["main"]["tree"]):
            raise ReleaseError("production source base changed before batch tool repair")
        last = batch["members"][-1]
        if (_resolve_ref(repo, last["ref"]) != batch["head_sha"]
                or _tree(repo, batch["head_sha"]) != batch["head_tree"]):
            raise ReleaseError("current cumulative source head changed before tool repair")
        app, baseline = batch["installed_app"], state["installed_app"]
        _verify_stage_app(app["sha"], app["manifest_sha256"])
        _verify_prod_app(config, baseline["sha"], baseline["manifest_sha256"])
        cursor = _verify_production_cursor(config)["cursor"]
        if (cursor.get("main_sha") != batch["base_sha"]
                or cursor.get("installed_app_sha") != baseline["sha"]
                or cursor.get("installed_manifest_sha256") != baseline["manifest_sha256"]):
            raise ReleaseError("production cursor changed before batch tool repair")
        member, check, bundle = _checked_batch_controller_member(
            config, repo, batch, controller_sha, controller_ref, "batch-tool-repair")
        _append_batch_controller_member(state, member, check, bundle, before=front)
        batch.setdefault("tool_repairs", []).append({
            "head_sha": member["head_sha"], "failed_candidate": failed_candidate,
            "check_receipt_sha256": member["check_receipt_sha256"],
            "recorded_at_utc": _utc_now(),
        })
        _update_state(state_path, state, status="blocked")
        return {"status": "batch_tool_repaired", "staged_head_sha": member["head_sha"],
                "blocked_candidate_sha": failed_candidate, "member_count": len(batch["members"]),
                "production_written": False}


def _approval_wait_result(inflight: dict[str, Any], evidence: dict[str, Any] | None) -> dict[str, Any]:
    """Show the exact object a human must approve; this function never writes production."""
    stage_receipt = inflight.get("stage_receipt")
    identity = {
        "base_sha": _sha(inflight["base_sha"], "approval base"),
        "head_sha": _sha(inflight["head_sha"], "approval head"),
        "head_tree": _sha(inflight["head_tree"], "approval tree"),
        "manifest_sha256": _digest(
            (inflight["release_manifest_sha256"] if evidence is not None
             else inflight["installed_app"]["manifest_sha256"]), "approval manifest"),
        "source_bundle_sha256": _digest(inflight["bundle_meta"]["bundle_sha256"], "approval bundle"),
        "stage_receipt_sha256": (hashlib.sha256(json.dumps(
            stage_receipt, sort_keys=True, separators=(",", ":")).encode()).hexdigest()
            if evidence is not None else None),
        "stage_journey_sha256": (hashlib.sha256(json.dumps(
            evidence, sort_keys=True, separators=(",", ":")).encode()).hexdigest()
            if evidence is not None else None),
    }
    digest = hashlib.sha256(json.dumps(identity, sort_keys=True, separators=(",", ":")).encode()).hexdigest()
    return {"status": "awaiting_human_approval", "approval": identity,
            "approval_digest": digest, "production_written": False}


def _promote_source_candidate(config: dict[str, Any], state_path: Path, state: dict[str, Any],
                              item: dict[str, Any]) -> dict[str, Any]:
    """Advance a source-only candidate after its exact approval, without installing an app."""
    repo, inflight = Path(config["repo"]), state["in_flight"]
    sha, tree, base = inflight["head_sha"], inflight["head_tree"], inflight["base_sha"]
    if (_resolve_ref(repo, MAIN_REF) != base or state["main"]["sha"] != base
            or _resolve_ref(repo, item["ref"]) != sha or _tree(repo, sha) != tree):
        raise ReleaseError("approved source candidate no longer matches its base and ref")
    bundle = Path(config["work_root"]) / "source-bundles" / f"{sha}.bundle"
    bundle_meta = inflight["bundle_meta"]
    if bundle.is_symlink() or not bundle.is_file() or _file_sha256(bundle) != bundle_meta["bundle_sha256"]:
        raise ReleaseError("approved source bundle changed")
    app = inflight["installed_app"]
    _verify_stage_app(app["sha"], app["manifest_sha256"])
    _verify_prod_app(config, app["sha"], app["manifest_sha256"])
    started_at = _utc_string(item.get("started_at_utc"), "source attempt start time")
    elapsed = max(0.0, (datetime.now(timezone.utc) - datetime.fromisoformat(
        started_at.replace("Z", "+00:00"))).total_seconds())
    phase = "source-backup"
    try:
        item["status"] = "checking"
        inflight.update({"phase": phase, "commit_started": True})
        _update_state(state_path, state, status="outcome_unknown")
        _upload_and_store_bundle(config, bundle, bundle_meta)
        _verify_prod_app(config, app["sha"], app["manifest_sha256"])
        return _finalize_success(config, state_path, state, item, bundle_meta, base,
                                 app, inflight["check_receipt"], None, None,
                                 time.monotonic() - elapsed)
    except BaseException as exc:
        _mark_unknown(state_path, state, phase, exc)
        raise


def _promote_staged_candidate(config: dict[str, Any], state_path: Path, state: dict[str, Any],
                              item: dict[str, Any], evidence: dict[str, Any]) -> dict[str, Any]:
    """Continue the original staged attempt after its exact installed journeys pass."""
    repo = Path(config["repo"])
    inflight = state.get("in_flight")
    if (not isinstance(inflight, dict) or inflight.get("phase") != "stage-validation-pending"
            or inflight.get("stage_install_completed") is not True
            or item.get("status") != "stage_validation_pending"
            or item.get("candidate_id") != inflight.get("candidate_id")
            or item.get("head_sha") != inflight.get("head_sha")
            or state.get("status") != "blocked"):
        raise ReleaseError("no exact staged attempt is awaiting journey evidence")
    sha = _sha(item["head_sha"], "staged candidate SHA")
    tree = _sha(inflight["head_tree"], "staged candidate tree")
    old_main = _sha(inflight["base_sha"], "staged base SHA")
    if (_resolve_ref(repo, MAIN_REF) != old_main or state["main"]["sha"] != old_main
            or _resolve_ref(repo, item["ref"]) != sha or _tree(repo, sha) != tree):
        raise ReleaseError("staged candidate no longer matches domestic main or its exact ref")
    metadata = inflight["release_metadata"]
    previous_app = inflight["previous_installed_app"]
    installed_app = inflight["installed_app"]
    bundle_meta = inflight["bundle_meta"]
    out = Path(config["work_root"]) / "builds" / sha
    metadata_path = out / "domestic-release.json"
    bundle = Path(config["work_root"]) / "source-bundles" / f"{sha}.bundle"
    if (metadata_path.is_symlink() or not metadata_path.is_file()
            or _file_sha256(metadata_path) != inflight["package_metadata_sha256"]
            or json.loads(metadata_path.read_text()) != metadata):
        raise ReleaseError("staged package metadata changed during journey validation")
    legacy.verify_release_artifact(out / "release", metadata)
    if (bundle.is_symlink() or not bundle.is_file()
            or _file_sha256(bundle) != bundle_meta["bundle_sha256"]):
        raise ReleaseError("staged source bundle changed during journey validation")
    legacy.verify_install_receipt(inflight["stage_receipt"], metadata, previous_app["sha"])
    _verify_stage_app(sha, metadata["release_files_sha256"])
    phase = "source-backup"
    started_at = _utc_string(item.get("started_at_utc"), "staged attempt start time")
    elapsed = max(0.0, (datetime.now(timezone.utc) - datetime.fromisoformat(
        started_at.replace("Z", "+00:00"))).total_seconds())
    started = time.monotonic() - elapsed
    try:
        item["status"] = "checking"
        inflight.update({"phase": phase, "stage_journey_receipt": evidence,
                         "candidate_sha": sha, "candidate_tree": tree,
                         "previous_main_sha": old_main})
        _update_state(state_path, state, status="blocked")
        _upload_and_store_bundle(config, bundle, bundle_meta)
        incoming, remote_metadata = legacy.copy_payload(
            config, sha, out / "release", metadata_path, previous_app["sha"])
        phase = "production-install"
        inflight.update({"phase": phase, "production_install_started": True})
        _update_state(state_path, state)
        result = _production_ssh(
            config, "sudo", "-n", config["prod_helper"], "--incoming", incoming,
            "--metadata", remote_metadata, "--expected-sha", sha,
            "--metadata-sha256", inflight["package_metadata_sha256"],
            "--expected-base", previous_app["sha"], timeout=1800,
        )
        production_receipt = json.loads(result.splitlines()[-1])
        production = _verify_prod_app(config, sha, metadata["release_files_sha256"])
        legacy.verify_install_receipt(production.get("receipt"), metadata, previous_app["sha"])
        if production_receipt != production.get("receipt"):
            raise ReleaseError("production installer response differs from independent readback")
        _verify_stage_app(sha, metadata["release_files_sha256"])
        item["stage_journey_receipt"] = evidence
        if inflight.get("stage_smoke_receipt") is not None:
            item["stage_smoke_receipt"] = inflight["stage_smoke_receipt"]
        phase = "main-cas-and-cursor"
        inflight.update({"phase": phase, "production_receipt": production_receipt,
                         "commit_started": True})
        _update_state(state_path, state, status="outcome_unknown")
        return _finalize_success(
            config, state_path, state, item, bundle_meta, old_main, installed_app,
            inflight["check_receipt"], inflight["stage_receipt"], production_receipt, started)
    except BaseException as exc:
        if inflight.get("commit_started") or inflight.get("production_install_started"):
            _mark_unknown(state_path, state, phase, exc)
        else:
            _mark_failed(state_path, state, item, exc, phase)
        raise


def maintenance_check(config: dict[str, Any], candidate_sha: str) -> dict[str, Any]:
    """Test a fixed-controller candidate with the currently installed trusted policy.

    This never installs candidate code or promotes an application. The operator
    reviews this receipt, installs the exact controller bytes with rollback,
    then explicitly resubmits the same source candidate.
    """
    if os.geteuid() != 0:
        raise ReleaseError("controller maintenance check must run as root")
    config = _check_config(config)
    candidate_sha = _sha(candidate_sha, "controller maintenance candidate SHA")
    repo, state_path = Path(config["repo"]), Path(config["state"])
    with _locked(Path(config["lock"]), nonblocking=True):
        verify_bare_repository(repo, controller_path=config["controller_path"], push_group=config["push_group"])
        state = _load_state(state_path)
        if state.get("status") == "outcome_unknown" or state.get("staging_out_of_sync") is True:
            raise ReleaseError("controller maintenance check is blocked by an unresolved release state")
        if state.get("controller_maintenance") is not None:
            state["controller_maintenance"] = None
            _update_state(state_path, state)
        item = _active_queue_item(state)
        if (item is None or item.get("status") not in {"pending", "failed"}
                or item.get("head_sha") != candidate_sha):
            raise ReleaseError("maintenance check requires the exact head candidate at the serial queue front")
        main_sha = _resolve_ref(repo, MAIN_REF)
        if (main_sha != state["main"]["sha"] or item.get("base_sha") != main_sha
                or _resolve_ref(repo, item["ref"]) != candidate_sha):
            raise ReleaseError("maintenance candidate is stale; update and check it from current domestic main")
        _first_parent_chain(repo, main_sha, candidate_sha)
        expected_controller = hashlib.sha256(_source_blob(repo, candidate_sha, "scripts/domestic_main_release.py")).hexdigest()
        running_controller = Path(__file__).resolve(strict=True)
        if _file_sha256(running_controller) != expected_controller:
            raise ControllerMaintenanceRequired("maintenance-check must run from the exact candidate controller bytes")
        _pin_candidate(repo, candidate_sha)
        worktree = _active_worktree(config, repo, candidate_sha)
        classification = _classify_candidate(worktree, main_sha, candidate_sha)
        if classification.get("runtime_changed") is not False:
            raise ReleaseError("controller maintenance path accepts source-only candidates only")
        controller_files = classification.get("controller_files", [])
        if not controller_files:
            raise ReleaseError("candidate does not contain a registered fixed-controller change")
        report_dir = Path(legacy.BUILD_ROOT) / "domestic-main-checks" / f"{candidate_sha}-maintenance-{time.time_ns()}"
        check_receipt = _check_report(config, repo, worktree, report_dir, main_sha, candidate_sha)
        python_controller_files = [path for path in controller_files if path.endswith(".py")]
        compile_code = (
            "from pathlib import Path; import sys; root=Path(sys.argv[1]); "
            "[compile((root / name).read_bytes(), name, 'exec') for name in sys.argv[2:]]; "
            "print('controller-syntax-ok')"
        )
        if python_controller_files:
            _build_command(config, ["/usr/bin/python3", "-c", compile_code, str(worktree), *python_controller_files],
                           cwd=worktree, timeout=120, safe_repository=worktree)
        changed_units = [str(worktree / path) for path in controller_files if path in HOST_UNIT_FILES]
        if changed_units:
            _build_command(config, ["/usr/bin/systemd-analyze", "verify", *changed_units],
                           cwd=worktree, timeout=120, safe_repository=worktree)
        if "scripts/domestic_main_release.py" in controller_files:
            _build_command(config, ["/usr/bin/python3", str(worktree / "scripts/domestic_main_release.py"), "--help"],
                           cwd=worktree, timeout=60, safe_repository=worktree)
            dry_run = (
                "import os,sys,tempfile; from pathlib import Path; "
                "root=Path(sys.argv[1]); sys.path.insert(0,str(root/'scripts')); "
                "import domestic_main_release as m; "
                "c={'repo':'/opt/aicrm/domestic/source.git','state':'/opt/aicrm/domestic/control/state.json',"
                "'lock':'/opt/aicrm/domestic/control/controller.lock','work_root':'/opt/aicrm/domestic/control/work',"
                "'source_worktree':'/opt/aicrm/domestic/control/work/candidate','stage_incoming':'/opt/aicrm/domestic-incoming',"
                "'stage_helper':'/usr/local/libexec/aicrm/domestic-promote.py','prod_host':'unused','prod_user':'unused',"
                "'prod_key':'/var/lib/aicrm/probe-key','prod_known_hosts':'/etc/ssh/ssh_known_hosts',"
                "'prod_incoming':'/opt/aicrm/domestic-incoming','prod_helper':'/usr/local/libexec/aicrm/domestic-promote.py',"
                "'push_user':'aicrm-release-push','push_group':'aicrm-release-push',"
                "'controller_path':'/usr/local/libexec/aicrm/domestic_main_release.py',"
                "'config_path':'/etc/aicrm/domestic-main-release.json','production_enabled':False,"
                "'check_database_url':'postgresql://127.0.0.1/aicrm_ci','build_path':'/opt/aicrm/toolchain/go-1.26.6/bin:/opt/aicrm/toolchain/npm/bin:/opt/aicrm/toolchain/node-v24.18.0-linux-x64/bin:/usr/bin:/bin'}; "
                "m._check_config(c); d=Path(tempfile.mkdtemp()); lock=d/'lock'; "
                "ctx=m._locked(lock,nonblocking=True); ctx.__enter__();\n"
                "try:\n try:\n  with m._locked(lock,nonblocking=True): raise SystemExit(31)\n except m.ReleaseError: print('locked-config-dry-run-ok')\n"
                "finally: ctx.__exit__(None,None,None)"
            )
            dry_result = _build_command(config, ["/usr/bin/python3", "-c", dry_run, str(worktree)],
                                        cwd=worktree, timeout=60, safe_repository=worktree)
            if "locked-config-dry-run-ok" not in dry_result.stdout:
                raise ReleaseError("candidate controller did not pass the isolated config and lock dry-run")
        stage_host_contract = None
        stage_helper_install_required = False
        if "deploy/domestic-promote.py" in controller_files:
            stage_host_contract = _run_stage_helper_host_contract(config, repo, candidate_sha)
            stage_helper_install_required = stage_host_contract is None
        canonical = json.dumps(check_receipt, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode()
        status = "tests_passed_stage_helper_install_required" if stage_helper_install_required else "maintenance_checks_passed"
        if status == "maintenance_checks_passed":
            fixed_hashes = {path: hashlib.sha256(_source_blob(repo, candidate_sha, path)).hexdigest()
                            for path in sorted(builder.FIXED_CONTROLLER_FILES)}
            state["controller_maintenance"] = {
                "candidate_sha": candidate_sha,
                "candidate_tree": _tree(repo, candidate_sha),
                "base_sha": main_sha,
                "controller_files": sorted(controller_files),
                "fixed_file_sha256": fixed_hashes,
                "check_receipt_sha256": hashlib.sha256(canonical).hexdigest(),
                "checked_at_utc": _utc_now(),
            }
            _update_state(state_path, state)
        return {"status": status, "candidate_sha": candidate_sha,
                "candidate_tree": _tree(repo, candidate_sha), "base_sha": main_sha,
                "controller_files": sorted(controller_files),
                "check_receipt_sha256": hashlib.sha256(canonical).hexdigest(),
                "toolchain": check_receipt["toolchain"],
                "stage_host_contract": stage_host_contract,
                "next_action": ("install the exact staging helper bytes with rollback, rerun maintenance-check, then install all remaining exact fixed-controller bytes before resubmitting this SHA"
                                if stage_helper_install_required else
                                "review this result, install any remaining exact fixed-controller bytes with rollback, then resubmit this SHA")}


def _verify_controller_maintenance_candidate(config: dict[str, Any], repo: Path,
                                              state: dict[str, Any]) -> dict[str, Any]:
    marker = state.get("controller_maintenance")
    item = _active_queue_item(state)
    if (not isinstance(marker, dict) or item is None or item.get("status") != "pending"
            or state.get("status") != "ready" or state.get("in_flight") is not None):
        raise ControllerMaintenanceRequired("controller maintenance marker is missing or candidate is not ready")
    candidate_sha, base_sha = marker["candidate_sha"], marker["base_sha"]
    main_sha = _resolve_ref(repo, MAIN_REF)
    if (main_sha != state["main"]["sha"] or base_sha != main_sha
            or item.get("head_sha") != candidate_sha or item.get("base_sha") != base_sha
            or _resolve_ref(repo, item["ref"]) != candidate_sha
            or _tree(repo, candidate_sha) != marker["candidate_tree"]):
        raise ControllerMaintenanceRequired("controller maintenance marker no longer matches main or queue candidate")
    _first_parent_chain(repo, base_sha, candidate_sha)
    classification = _classify_candidate(repo, base_sha, candidate_sha)
    changed = sorted(classification.get("controller_files", []))
    if (classification.get("runtime_changed") is not False
            or not changed or changed != marker["controller_files"]):
        raise ControllerMaintenanceRequired("controller maintenance marker file set differs from candidate")
    actual_hashes = {path: hashlib.sha256(_source_blob(repo, candidate_sha, path)).hexdigest()
                     for path in sorted(builder.FIXED_CONTROLLER_FILES)}
    if actual_hashes != marker["fixed_file_sha256"]:
        raise ControllerMaintenanceRequired("controller maintenance marker fixed-file hashes differ from candidate")
    _verify_controller_files(config, repo, candidate_sha, sorted(builder.FIXED_CONTROLLER_FILES))
    return marker


def poll(config: dict[str, Any], *, expected_candidate_sha: str | None = None) -> dict[str, Any]:
    if os.geteuid() != 0:
        raise ReleaseError("domestic serial release poll must run as root")
    config = _check_config(config)
    if config.get("production_enabled") is not True:
        raise ReleaseError("production release is disabled until the verified cutover is activated")
    repo, state_path = Path(config["repo"]), Path(config["state"])
    if not _is_bare_repo(repo):
        raise ReleaseError("domestic source repository is unavailable")
    with _locked(Path(config["lock"]), nonblocking=True):
        _assert_legacy_release_path_stopped()
        verify_bare_repository(repo, controller_path=config["controller_path"], push_group=config["push_group"])
        state = _load_state(state_path)
        _recover_orphaned_inflight(state_path, state)
        inflight = state.get("in_flight")
        batch = state.get("batch")
        if isinstance(batch, dict) and batch.get("status") == "sealed":
            evidence = _batch_final_evidence(config, batch)
            if evidence is None:
                raise ReleaseError("sealed batch lost its final staging journey")
            result = _batch_approval_result(batch, evidence)
            if result["approval_digest"] != batch.get("approval_digest"):
                raise ReleaseError("sealed batch approval identity changed")
            return result
        if (state["status"] == "blocked" and isinstance(inflight, dict)
                and inflight.get("phase") == "stage-validation-pending"):
            front = _active_queue_item(state)
            if front is None or front.get("head_sha") != inflight.get("head_sha"):
                raise ReleaseError("staged attempt is not at the serial queue front")
            if expected_candidate_sha is not None and expected_candidate_sha != front["head_sha"]:
                return {"status": "candidate_not_at_queue_front",
                        "requested_candidate_sha": expected_candidate_sha,
                        "queue_head_sha": front["head_sha"], "main_sha": state["main"]["sha"]}
            controller_sha = (state["batch"].get("controller_source_sha", state["main"]["sha"])
                              if state.get("batch") else state["main"]["sha"])
            _verify_controller_files(config, repo, controller_sha,
                                     sorted(builder.FIXED_CONTROLLER_FILES))
            evidence = _read_stage_evidence(config, inflight)
            if evidence is None:
                return {"status": "stage_validation_pending",
                        "candidate_sha": front["head_sha"],
                        "manifest_sha256": inflight["release_manifest_sha256"],
                        "stage_evidence_path": str(_stage_evidence_path(config, front["head_sha"]))}
            if state.get("batch") is not None:
                return _accept_staged_batch_member(config, state_path, state, front, evidence)
            return _approval_wait_result(inflight, evidence)
        if (state["status"] == "blocked" and isinstance(inflight, dict)
                and inflight.get("phase") == "source-approval-pending"):
            front = _active_queue_item(state)
            if front is None or front.get("head_sha") != inflight.get("head_sha"):
                raise ReleaseError("source approval attempt is not at the queue front")
            if expected_candidate_sha is not None and expected_candidate_sha != front["head_sha"]:
                return {"status": "candidate_not_at_queue_front",
                        "requested_candidate_sha": expected_candidate_sha,
                        "queue_head_sha": front["head_sha"], "main_sha": state["main"]["sha"]}
            return _approval_wait_result(inflight, None)
        if state["status"] == "outcome_unknown":
            raise ReleaseError("production outcome is unknown; use reconcile, never reinstall blindly")
        if state["status"] == "blocked":
            raise ReleaseError("domestic release queue is blocked; inspect or resubmit its head candidate")
        if expected_candidate_sha is not None:
            expected_candidate_sha = _sha(expected_candidate_sha, "expected queue-front candidate SHA")
            front = _active_queue_item(state)
            if front is None:
                return {"status": "ready", "main_sha": state["main"]["sha"], "queue_depth": 0}
            if front.get("head_sha") != expected_candidate_sha:
                return {"status": "candidate_not_at_queue_front",
                        "requested_candidate_sha": expected_candidate_sha,
                        "queue_head_sha": front.get("head_sha"),
                        "main_sha": state["main"]["sha"]}
        batch = state.get("batch")
        maintenance = state.get("controller_maintenance")
        if maintenance is None:
            controller_sha = batch.get("controller_source_sha", state["main"]["sha"]) if batch else state["main"]["sha"]
            _verify_controller_files(config, repo, controller_sha, sorted(builder.FIXED_CONTROLLER_FILES))
        else:
            _verify_controller_maintenance_candidate(config, repo, state)
        if batch is not None:
            baseline = state["installed_app"]
            _verify_prod_app(config, baseline["sha"], baseline["manifest_sha256"])
            stage_app = batch["installed_app"]
            _verify_stage_app(stage_app["sha"], stage_app["manifest_sha256"])
        item = _active_queue_item(state)
        if item is None:
            if batch is not None:
                return {"status": "batch_open", "base_sha": batch["base_sha"],
                        "staged_head_sha": batch["head_sha"],
                        "staged_app_sha": batch["installed_app"]["sha"],
                        "member_count": len(batch["members"]), "production_written": False}
            return {"status": "ready", "main_sha": state["main"]["sha"], "queue_depth": 0}
        result = process_candidate(config, state_path, state, item)
        if result.get("status") != "completed":
            return result
        # Continue in first-in order. The next candidate must be re-submitted
        # against the newly advanced main and can never be rebased here.
        state = _load_state(state_path)
        next_item = _active_queue_item(state)
        if next_item is None:
            return result
        if next_item.get("base_sha") != state["main"]["sha"]:
            next_item["status"] = "stale_base"
            next_item["failure"] = {"phase": "base-check", "expected_base": state["main"]["sha"],
                                    "candidate_base": next_item.get("base_sha"), "recorded_at_utc": _utc_now()}
            _update_state(state_path, state, status="blocked")
            result["queue_status"] = "stale_base"
            result["blocked_candidate_sha"] = next_item["head_sha"]
            return result
        # A poll releases one candidate. A second invocation acquires the same
        # lock after the first success, keeping each source/binary transaction
        # and receipt independently auditable.
        result["queue_status"] = "pending"
        result["next_candidate_sha"] = next_item["head_sha"]
        return result


def status_snapshot(config: dict[str, Any]) -> dict[str, Any]:
    """Atomic, read-only queue snapshot; no hashing payloads or polling hosts."""
    state = _load_state(Path(config['state']))
    item = _active_queue_item(state)
    return {'status':state['status'],'main':state['main'],'installed_app':state['installed_app'],
            'in_flight': {k:state['in_flight'].get(k) for k in ('head_sha','phase','check_progress')} if state.get('in_flight') else None,
            'queue_head': {k:item.get(k) for k in ('head_sha','status','attempt','failure')} if item else None,
            'batch':{k:state['batch'].get(k) for k in ('head_sha','status','approval_digest')} if state.get('batch') else None,
            'updated_at_utc':state.get('updated_at_utc')}


def promote_authorized(config: dict[str, Any], head_sha: str, authorization_reference: str) -> dict[str, Any]:
    """Desk supplies the already received human command; digest stays internal.

    Bind the authorized scope to the exact staged head. Keep existing digest,
    one writer, health, baseline and outcome_unknown protections in promote.
    """
    if os.geteuid() != 0:
        raise ReleaseError('production promotion must run as root')
    if not isinstance(authorization_reference,str) or not authorization_reference.strip():
        raise ReleaseError('original human production authorization reference required')
    head_sha = _sha(head_sha,'authorized production head')
    config = _check_config(config)
    with _locked(Path(config['lock']),nonblocking=True):
        state = _load_state(Path(config['state']))
        batch = state.get('batch')
        target = batch or state.get('in_flight') or {}
        if target.get('head_sha') != head_sha:
            raise ReleaseError('production authorization scope differs from staged head')
        if state.get('status') == 'outcome_unknown':
            raise ReleaseError('unknown installation requires read-only reconciliation')
        target['production_authorization']={'reference':authorization_reference,'head_sha':head_sha,'recorded_at_utc':_utc_now()}
        _update_state(Path(config['state']),state)
        if batch:
            if batch.get('status')=='sealed':
                evidence=_batch_final_evidence(config,batch)
                if evidence is None:raise ReleaseError('final staging journey missing')
                digest=_batch_approval_result(batch,evidence)['approval_digest']
            elif batch.get('status')=='open':digest=None
            else:raise ReleaseError('batch is not ready for authorized promotion')
        else:
            inflight=state.get('in_flight') or {}
            if inflight.get('phase') not in {'source-approval-pending','stage-validation-pending'}:
                raise ReleaseError('candidate is not ready for authorized promotion')
            evidence=_read_stage_evidence(config,inflight) if inflight.get('phase')=='stage-validation-pending' else None
            if inflight.get('phase')=='stage-validation-pending' and evidence is None:raise ReleaseError('required staging journey missing')
            digest=_approval_wait_result(inflight,evidence)['approval_digest']
    if digest is None: digest=batch_seal(config,expected_head=head_sha)['approval_digest']
    return promote(config,digest)


def promote(config: dict[str, Any], approval_digest: str) -> dict[str, Any]:
    """The sole production entry, called with an internally bound digest after human production authorization."""
    if os.geteuid() != 0:
        raise ReleaseError("production promotion must run as root")
    config = _check_config(config)
    if config.get("production_enabled") is not True:
        raise ReleaseError("production promotion is disabled")
    expected = _digest(approval_digest, "human approval digest")
    repo, state_path = Path(config["repo"]), Path(config["state"])
    with _locked(Path(config["lock"]), nonblocking=True):
        _assert_legacy_release_path_stopped()
        verify_bare_repository(repo, controller_path=config["controller_path"], push_group=config["push_group"])
        state = _load_state(state_path)
        _recover_orphaned_inflight(state_path, state)
        batch = state.get("batch")
        if isinstance(batch, dict) and batch.get("status") == "sealed":
            evidence = _batch_final_evidence(config, batch)
            if evidence is None:
                raise ReleaseError("final batch staging journey is missing")
            approval = _batch_approval_result(batch, evidence)
            if (approval["approval_digest"] != expected
                    or batch.get("approval_digest") != expected):
                raise ReleaseError("human approval does not match the sealed cumulative batch")
            return _promote_batch(config, state_path, state, evidence)
        inflight = state.get("in_flight")
        item = _active_queue_item(state)
        if (state.get("status") != "blocked" or not isinstance(inflight, dict)
                or item is None or item.get("candidate_id") != inflight.get("candidate_id")):
            raise ReleaseError("no exact candidate is waiting for human approval")
        phase = inflight.get("phase")
        evidence = None
        if phase == "stage-validation-pending":
            evidence = _read_stage_evidence(config, inflight)
            if evidence is None:
                raise ReleaseError("required staging journey has not completed")
        elif phase != "source-approval-pending":
            raise ReleaseError("candidate is not ready for production promotion")
        approval = _approval_wait_result(inflight, evidence)
        if approval["approval_digest"] != expected:
            raise ReleaseError("human approval does not match this candidate, artifact and staging receipt")
        # A controller-maintenance candidate has installed its exact checked
        # bytes on staging before this pause, while domestic main still points
        # at the prior source. Bind promotion to the approved candidate.
        _verify_controller_files(config, repo, inflight["head_sha"],
                                 sorted(builder.FIXED_CONTROLLER_FILES))
        if phase == "stage-validation-pending":
            return _promote_staged_candidate(config, state_path, state, item, evidence)
        return _promote_source_candidate(config, state_path, state, item)


def _promote_batch(config: dict[str, Any], state_path: Path, state: dict[str, Any],
                   evidence: dict[str, Any]) -> dict[str, Any]:
    """Install the final staged package once, then advance one source cursor."""
    repo = Path(config["repo"])
    batch = state["batch"]
    tip, base = _batch_tip(state), batch["base_sha"]
    app, baseline = batch["installed_app"], state["installed_app"]
    last = batch["members"][-1]
    item = next((entry for entry in state["queue"]
                 if entry.get("candidate_id") == last["candidate_id"]
                 and entry.get("head_sha") == tip), None)
    if (item is None or state.get("in_flight") is not None
            or _resolve_ref(repo, MAIN_REF) != base
            or _resolve_ref(repo, last["ref"]) != tip
            or _tree(repo, tip) != batch["head_tree"]):
        raise ReleaseError("sealed batch source identity changed before promotion")
    for member in batch["members"]:
        if (_resolve_ref(repo, member["ref"]) != member["head_sha"]
                or _tree(repo, member["head_sha"]) != member["head_tree"]
                or not _first_parent_chain(repo, member["base_sha"], member["head_sha"])):
            raise ReleaseError("sealed batch member source identity changed")
    _verify_controller_files(config, repo, batch.get("controller_source_sha", base),
                             sorted(builder.FIXED_CONTROLLER_FILES))
    _verify_stage_app(app["sha"], app["manifest_sha256"])
    _verify_prod_app(config, baseline["sha"], baseline["manifest_sha256"])
    cursor = _verify_production_cursor(config)["cursor"]
    if (cursor.get("main_sha") != base or cursor.get("main_tree") != batch["base_tree"]
            or cursor.get("installed_app_sha") != baseline["sha"]
            or cursor.get("installed_manifest_sha256") != baseline["manifest_sha256"]):
        raise ReleaseError("production cursor changed before batch promotion")
    metadata_path = Path(batch["production_metadata_path"])
    if (metadata_path.is_symlink() or not metadata_path.is_file()
            or _file_sha256(metadata_path) != batch["production_metadata_sha256"]):
        raise ReleaseError("approved production transfer metadata changed")
    metadata = json.loads(metadata_path.read_text())
    if metadata.get("source_sha") != app["sha"] or metadata.get("release_files_sha256") != app["manifest_sha256"]:
        raise ReleaseError("approved metadata does not describe the final staged package")
    out = Path(config["work_root"]) / "builds" / app["sha"]
    legacy.verify_release_artifact(out / "release", metadata)
    bundle = Path(config["work_root"]) / "source-bundles" / f"{tip}.bundle"
    if bundle.is_symlink() or not bundle.is_file() or _file_sha256(bundle) != last["source_bundle_sha256"]:
        raise ReleaseError("approved final source bundle changed")
    bundle_meta = item.get("source_bundle")
    if not isinstance(bundle_meta, dict) or bundle_meta.get("bundle_sha256") != last["source_bundle_sha256"]:
        raise ReleaseError("final batch member lost its source bundle receipt")
    started = time.monotonic()
    state["in_flight"] = {"candidate_id": last["candidate_id"], "head_sha": tip,
                          "head_tree": batch["head_tree"], "base_sha": base,
                          "phase": "batch-source-backup", "production_install_started": False,
                          "commit_started": False, "batch_member_heads": [m["head_sha"] for m in batch["members"]],
                          "installed_app": dict(app), "previous_installed_app": dict(baseline),
                          "runtime_changed": True,
                          "bundle_meta": bundle_meta, "stage_journey_receipt": evidence,
                          "release_metadata": metadata,
                          "package_metadata_sha256": batch["production_metadata_sha256"]}
    batch["status"] = "promoting"
    _update_state(state_path, state, status="outcome_unknown")
    phase = "batch-source-backup"
    try:
        _upload_and_store_bundle(config, bundle, bundle_meta)
        incoming, remote_metadata = legacy.copy_payload(
            config, app["sha"], out / "release", metadata_path, baseline["sha"])
        phase = "batch-production-install"
        state["in_flight"].update({"phase": phase, "production_install_started": True})
        _update_state(state_path, state, status="outcome_unknown")
        result = _production_ssh(config, "sudo", "-n", config["prod_helper"],
                                 "--incoming", incoming, "--metadata", remote_metadata,
                                 "--expected-sha", app["sha"],
                                 "--metadata-sha256", batch["production_metadata_sha256"],
                                 "--expected-base", baseline["sha"], timeout=1800)
        production_receipt = json.loads(result.splitlines()[-1])
        production = _verify_prod_app(config, app["sha"], app["manifest_sha256"])
        legacy.verify_install_receipt(production.get("receipt"), metadata, baseline["sha"])
        if production_receipt != production.get("receipt"):
            raise ReleaseError("batch production installer response differs from readback")
        _verify_stage_app(app["sha"], app["manifest_sha256"])
        phase = "batch-main-cas-and-cursor"
        state["in_flight"].update({"phase": phase, "production_receipt": production_receipt,
                                   "commit_started": True})
        _update_state(state_path, state, status="outcome_unknown")
        return _finalize_success(config, state_path, state, item, bundle_meta, base,
                                 app, item["check_receipt"], batch["stage_receipt"],
                                 production_receipt, started)
    except BaseException as exc:
        _mark_unknown(state_path, state, phase, exc)
        raise


def release_candidate(config: dict[str, Any], ref: str, head_sha: str, base_sha: str,
                      supersedes_candidate_id: str | None = None) -> dict[str, Any]:
    """Submit an exact candidate, then process it only if it is queue front."""
    if os.geteuid() != 0:
        raise ReleaseError("release must run through the root release controller")
    config = _check_config(config)
    if config.get("production_enabled") is not True:
        raise ReleaseError("production release is disabled until the verified cutover is activated")
    head_sha = _sha(head_sha, "release candidate head")
    submitted = submit_candidate(
        Path(config["repo"]), Path(config["state"]), ref, head_sha, base_sha,
        Path(config["lock"]), supersedes_candidate_id,
    )
    queue_position = submitted.get("queue_position")
    if type(queue_position) is not int or queue_position < 1:
        raise ReleaseError("submitted candidate has no valid serial queue position")
    if queue_position > 1:
        return {"status": "queued_behind_prior_candidate", "candidate_sha": head_sha,
                "queue_position": queue_position, "submission": submitted}

    result = poll(config, expected_candidate_sha=head_sha)
    if result.get("status") in {"stage_validation_pending", "awaiting_human_approval", "batch_open"}:
        return {"status": result["status"], "candidate_sha": head_sha,
                "queue_position": queue_position, "submission": submitted,
                "release": result}
    if (result.get("main_sha") == head_sha
            and result.get("status") in {"completed", "ready", "candidate_not_at_queue_front"}):
        return {"status": "completed", "candidate_sha": head_sha, "main_sha": head_sha,
                "submission": submitted, "release": result}
    return {"status": "not_released", "candidate_sha": head_sha,
            "queue_position": queue_position, "submission": submitted,
            "release": result}


def reconcile(config: dict[str, Any]) -> dict[str, Any]:
    """Read production only; complete CAS/cursor tail when the install is proven."""
    if os.geteuid() != 0:
        raise ReleaseError("reconcile must run as root")
    config = _check_config(config)
    repo, state_path = Path(config["repo"]), Path(config["state"])
    with _locked(Path(config["lock"]), nonblocking=True):
        _assert_legacy_release_path_stopped()
        state = _load_state(state_path)
        interrupted = _recover_orphaned_inflight(state_path, state)
        inflight = state.get("in_flight")
        if interrupted == "staging_reset_required":
            raise ReleaseError("interrupted staging install requires restoration and synthetic database rebuild before retry")
        if interrupted == "retry_required":
            raise ReleaseError("candidate was interrupted before side effects; explicitly resubmit it to retry")
        if state.get("status") != "outcome_unknown" or not isinstance(inflight, dict):
            raise ReleaseError("there is no durable unknown production outcome to reconcile")
        sha, tree, old_main, bundle_meta, installed_app, previous_installed_app, runtime_changed = _reconcile_identity(repo, inflight)
        bundle = Path(config["work_root"]) / "source-bundles" / f"{sha}.bundle"
        if bundle.is_symlink() or not bundle.is_file() or _file_sha256(bundle) != bundle_meta.get("bundle_sha256"):
            raise ReleaseError("local full source backup is missing or has changed; remain stopped")
        stored = _production_ssh(config, "sudo", "-n", config["prod_helper"],
                                 "--verify-domestic-source-backup", "--source-sha", sha,
                                 "--source-tree", tree, "--source-bundle-sha256", bundle_meta["bundle_sha256"],
                                 "--previous-main-sha", old_main,
                                 timeout=600)
        backup = json.loads(stored.splitlines()[-1])
        if (backup.get("status") != "verified" or backup.get("source_sha") != sha
                or backup.get("source_tree") != tree
                or backup.get("previous_main_sha") != old_main
                or backup.get("bundle_sha256") != bundle_meta["bundle_sha256"]):
            raise ReleaseError("production source backup cannot be proven; remain stopped")
        item = next((entry for entry in state["queue"] if entry.get("head_sha") == sha), None)
        if item is None:
            raise ReleaseError("in-flight candidate is not in the durable queue")
        if runtime_changed:
            manifest = installed_app["manifest_sha256"]
            production = _verify_prod_app(config, installed_app["sha"], manifest)
            metadata = inflight.get("release_metadata")
            legacy.verify_install_receipt(production.get("receipt"), metadata,
                                          previous_installed_app["sha"])
            receipt_body = production["receipt"]
        else:
            prior_app = previous_installed_app
            production = _verify_prod_app(config, prior_app["sha"], prior_app["manifest_sha256"])
            receipt_body = production["receipt"]
        _verify_stage_app(installed_app["sha"], installed_app["manifest_sha256"])
        receipt_path = Path(config.get("prod_cursor_path", CURSOR_PATH))
        if str(receipt_path) != CURSOR_PATH:
            raise ReleaseError("production cursor path must use the fixed helper contract")
        # First make the production source cursor exact and read it back.
        # Only then CAS domestic main; a crash between them is idempotently
        # finishable without reinstalling the application.
        _record_production_cursor(config, candidate_sha=sha, candidate_tree=tree,
                                  previous_main_sha=old_main, installed_app=installed_app,
                                  bundle_sha256=bundle_meta["bundle_sha256"])
        cursor = _verify_production_cursor(config)["cursor"]
        if cursor.get("main_sha") != sha or cursor.get("installed_app_sha") != installed_app["sha"]:
            raise ReleaseError("production cursor readback remains inconsistent; remain stopped")
        current_main = _resolve_ref(repo, MAIN_REF)
        if current_main == old_main:
            _advance_main_cas(repo, old_main, sha)
        elif current_main != sha:
            raise ReleaseError("domestic main has moved to a different SHA; remain stopped")
        state["main"] = {"sha": sha, "tree": tree}
        state["installed_app"] = installed_app
        state["staging_out_of_sync"] = False
        item.update({"status": "completed", "completed_at_utc": _utc_now(), "reconciled": True,
                     "production_receipt": receipt_body})
        batch = state.get("batch")
        if isinstance(batch, dict):
            if batch.get("head_sha") != sha or inflight.get("batch_member_heads") != [
                    member["head_sha"] for member in batch["members"]]:
                raise ReleaseError("unknown batch outcome differs from durable member order")
            for member in batch["members"]:
                queued = next((entry for entry in state["queue"] if entry.get("head_sha") == member["head_sha"]), None)
                if queued is None:
                    raise ReleaseError("reconciled batch member is absent from queue")
                queued.update({"status": "completed", "completed_at_utc": item["completed_at_utc"],
                               "reconciled": True, "batch_final_head_sha": sha,
                               "production_receipt": receipt_body})
            state["batch"] = None
        state["in_flight"] = None
        state["last_release"] = {"main_sha": sha, "main_tree": tree,
                                 "installed_app_sha": installed_app["sha"],
                                 "manifest_sha256": installed_app["manifest_sha256"],
                                 "source_bundle_sha256": bundle_meta["bundle_sha256"],
                                 "last_manual_confirmed_github_sha": (state.get("archive_ack") or {}).get("sha", "unknown"),
                                 "github_sync_observed_by": "manual_cli" if state.get("archive_ack") else "unknown",
                                 "github_pending_first_parent_count": _pending_archive_count(repo, (state.get("archive_ack") or {}).get("sha"), sha) if state.get("archive_ack") else "unknown",
                                 "status": "ready", "reconciled_at_utc": _utc_now()}
        if isinstance(batch, dict):
            state["last_release"]["batch_member_heads"] = inflight["batch_member_heads"]
        _update_state(state_path, state, status="ready")
        return {"status": "reconciled", "main_sha": sha, "main_tree": tree,
                "installed_app_sha": installed_app["sha"], "cursor": cursor}


def _assert_release_timers_stopped() -> None:
    for unit in (OLD_RELEASE_TIMER, NEW_RELEASE_TIMER):
        enabled = _run(["/usr/bin/systemctl", "is-enabled", unit], check=False)
        active = _run(["/usr/bin/systemctl", "is-active", unit], check=False)
        if enabled.stdout.strip() in {"enabled", "enabled-runtime", "linked", "linked-runtime"}:
            raise ReleaseError(f"release timer must be disabled before domestic authority cutover: {unit}")
        if active.stdout.strip() not in {"inactive", "failed", "unknown", "not-found", ""}:
            raise ReleaseError(f"release timer must be stopped before domestic authority cutover: {unit}")
    for unit in ("aicrm-domestic-release.service", "aicrm-domestic-main-release.service"):
        active = _run(["/usr/bin/systemctl", "is-active", unit], check=False)
        if active.stdout.strip() not in {"inactive", "failed", "unknown", "not-found", ""}:
            raise ReleaseError(f"release service must be inactive before domestic authority cutover: {unit}")


def _assert_legacy_release_path_stopped() -> None:
    """Keep old timer/oneshot from racing the new controller after activation."""
    timer_enabled = _run(["/usr/bin/systemctl", "is-enabled", OLD_RELEASE_TIMER], check=False)
    if timer_enabled.stdout.strip() in {"enabled", "enabled-runtime", "linked", "linked-runtime"}:
        raise ReleaseError("legacy release timer is enabled; domestic promotion stopped")
    timer_active = _run(["/usr/bin/systemctl", "is-active", OLD_RELEASE_TIMER], check=False)
    if timer_active.stdout.strip() not in {"inactive", "failed", "unknown", "not-found", ""}:
        raise ReleaseError("legacy release timer is active; domestic promotion stopped")
    service_active = _run(["/usr/bin/systemctl", "is-active", "aicrm-domestic-release.service"], check=False)
    if service_active.stdout.strip() not in {"inactive", "failed", "unknown", "not-found", ""}:
        raise ReleaseError("legacy release service is active; domestic promotion stopped")


def _installed_app_identity(config: dict[str, Any], *, production: bool) -> tuple[dict[str, str], dict[str, Any]]:
    data = legacy.prod_readback(config) if production else legacy.stage_readback()
    marker = data.get("release_env")
    match = re.fullmatch(r"AICRM_RELEASE_SHA=([0-9a-f]{40})\n", marker if isinstance(marker, str) else "")
    if not match:
        raise ReleaseError("installed application release marker is missing")
    sha = match.group(1)
    manifest = _digest(data.get("manifest_sha256"), "installed manifest digest")
    legacy.verify_readback(data, sha, manifest)
    receipt = data.get("receipt")
    if (not isinstance(receipt, dict) or receipt.get("source_sha") != sha
            or receipt.get("manifest_sha256") != manifest
            or receipt.get("technical_status") != "installed_healthy"
            or not isinstance(receipt.get("source_tree"), str)
            or not SHA.fullmatch(receipt["source_tree"])):
        raise ReleaseError("installed application receipt does not match exact host readback")
    identity = {"sha": sha, "tree": receipt["source_tree"], "manifest_sha256": manifest}
    return identity, data


def activate(config: dict[str, Any], *, candidate_sha: str | None = None) -> dict[str, Any]:
    """Create the stage ledger only after the production source cursor is ready."""
    config = _check_config(config)
    with _locked(Path(config["lock"]), nonblocking=True):
        _assert_release_timers_stopped()
        return _activate_locked_no_lock(config, candidate_sha=candidate_sha)


def _activate_locked_no_lock(config: dict[str, Any], *, candidate_sha: str | None = None) -> dict[str, Any]:
    """Activate without reacquiring the lock (used by prepare-baseline)."""
    repo, state_path = Path(config["repo"]), Path(config["state"])
    if state_path.exists() or state_path.is_symlink():
        raise ReleaseError("domestic ledger already exists; never overwrite or reset it")
    repository = verify_bare_repository(repo, controller_path=config["controller_path"], push_group=config["push_group"])
    stage_app, _ = _installed_app_identity(config, production=False)
    if candidate_sha is not None and (repository["main_sha"] != BASELINE_OVERLAY_BASE_SHA
                                      or repository["main_tree"] != BASELINE_OVERLAY_BASE_TREE):
        raise ReleaseError("baseline overlay candidate is valid only while domestic main remains at 291")
    overlay = None
    if candidate_sha is None:
        _verify_controller_files(config, repo, repository["main_sha"], sorted(builder.FIXED_CONTROLLER_FILES))
    else:
        overlay = _verify_existing_baseline_helper_overlay(
            config, repo, candidate_sha=candidate_sha,
            base_sha=repository["main_sha"], base_tree=repository["main_tree"],
            installed_app=stage_app,
        )
    cursor = _verify_production_cursor(config)["cursor"]
    if repository["main_sha"] != cursor.get("main_sha") or repository["main_tree"] != cursor.get("main_tree"):
        raise ReleaseError("domestic main does not match the independently read production source cursor")
    if overlay is not None and cursor.get("source_bundle_sha256") != overlay["source_bundle_sha256"]:
        raise ReleaseError("production cursor source bundle differs from the helper overlay")
    if stage_app != {"sha": cursor["installed_app_sha"], "tree": cursor["installed_app_tree"],
                     "manifest_sha256": cursor["installed_manifest_sha256"]}:
        raise ReleaseError("preproduction installed application does not match production source cursor")
    state = _new_state(repository["main_sha"], repository["main_tree"], stage_app)
    atomic_json(state_path, state)
    return {"status": "activated", "main_sha": repository["main_sha"],
            "installed_app_sha": stage_app["sha"], "ledger": str(state_path)}


def prepare_baseline(config: dict[str, Any]) -> dict[str, Any]:
    """Persist the one-time production recovery bundle and initialize both cursors."""
    if os.geteuid() != 0:
        raise ReleaseError("prepare-baseline must run as root")
    config = _check_config(config)
    if config.get("production_enabled") is not True:
        raise ReleaseError("baseline preparation requires production_enabled=true in the protected config")
    repo, state_path = Path(config["repo"]), Path(config["state"])
    with _locked(Path(config["lock"]), nonblocking=True):
        _assert_release_timers_stopped()
        toolchain = _verify_build_toolchain(config)
        if state_path.exists() or state_path.is_symlink():
            raise ReleaseError("domestic ledger already exists; baseline preparation never overwrites it")
        repository = verify_bare_repository(repo, controller_path=config["controller_path"], push_group=config["push_group"])
        main_sha, main_tree = repository["main_sha"], repository["main_tree"]
        prod_app, _ = _installed_app_identity(config, production=True)
        stage_app, _ = _installed_app_identity(config, production=False)
        if prod_app != stage_app:
            raise ReleaseError("staging and production installed application identities differ")
        _validate_baseline_identity(repo, main_sha, main_tree, prod_app)
        _verify_controller_files(config, repo, main_sha, sorted(builder.FIXED_CONTROLLER_FILES))
        _pin_candidate(repo, main_sha)
        bundle, bundle_meta = _create_full_bundle(repo, Path(config["work_root"]), main_sha, main_tree,
                                                  previous_main_sha=prod_app["sha"], baseline_transition=True)
        saved = _upload_and_store_bundle(config, bundle, bundle_meta, baseline_transition=True)
        _record_production_cursor(config, candidate_sha=main_sha, candidate_tree=main_tree,
                                  previous_main_sha=main_sha, installed_app=prod_app,
                                  bundle_sha256=bundle_meta["bundle_sha256"], initialize=True)
        cursor = _verify_production_cursor(config)["cursor"]
        if cursor.get("main_sha") != main_sha or cursor.get("installed_app_sha") != prod_app["sha"]:
            raise ReleaseError("production baseline cursor did not read back exactly")
        return {"status": "baseline_prepared", "main_sha": main_sha, "main_tree": main_tree,
                "installed_app_sha": prod_app["sha"], "source_bundle_sha256": bundle_meta["bundle_sha256"],
                "production_backup": saved, "build_toolchain": toolchain, "activation_required": True}


def _load_existing_baseline_bundle(repo: Path, work_root: Path, *, source_sha: str,
                                   source_tree: str, previous_main_sha: str) -> tuple[Path, dict[str, Any]]:
    """Load, never create, the exact local bundle for baseline recovery."""
    bundle_root = work_root / "source-bundles"
    bundle = bundle_root / f"{source_sha}.bundle"
    metadata_path = bundle_root / f"{source_sha}.json"
    if (bundle_root.is_symlink() or not bundle_root.is_dir()
            or bundle.is_symlink() or not bundle.is_file()
            or metadata_path.is_symlink() or not metadata_path.is_file()):
        raise ReleaseError("baseline recovery requires the existing local source bundle and metadata")
    try:
        metadata = json.loads(metadata_path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise ReleaseError("existing baseline source bundle metadata is invalid") from exc
    actual_digest = _file_sha256(bundle)
    actual_size = bundle.stat().st_size
    expected = {"schema_version": 1, "source_sha": source_sha, "source_tree": source_tree,
                "previous_main_sha": previous_main_sha, "bundle_sha256": actual_digest,
                "bundle_bytes": actual_size, "self_contained": True,
                "verified_from_empty_repository": True, "baseline_transition": True}
    if not isinstance(metadata, dict) or any(metadata.get(key) != value for key, value in expected.items()):
        raise ReleaseError("existing baseline bundle identity or digest does not match recovery inputs")
    # This re-verifies bundle refs, ancestry, object completeness, and the tree;
    # the existence check above prevents this helper from creating a fresh bundle.
    verified_bundle, verified_metadata = _create_full_bundle(
        repo, work_root, source_sha, source_tree,
        previous_main_sha=previous_main_sha, baseline_transition=True,
    )
    if verified_bundle != bundle or verified_metadata.get("bundle_sha256") != actual_digest:
        raise ReleaseError("existing baseline source bundle changed during verification")
    return bundle, verified_metadata


def _unique_json_object(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    value: dict[str, Any] = {}
    for key, item in pairs:
        if key in value:
            raise ValueError("duplicate JSON key")
        value[key] = item
    return value


@contextmanager
def _verified_overlay_seed_bundle(repo: Path, work_root: Path, seed_bundle: Path,
                                  candidate_sha: str, base_sha: str) -> Iterator[tuple[Path, str, str]]:
    """Yield a verified empty-repository import of the exact one-time seed bundle."""
    expected_path = BASELINE_OVERLAY_SEED_ROOT / f"domestic-main-seed-{candidate_sha}.bundle"
    if seed_bundle != expected_path:
        raise ReleaseError("baseline overlay seed bundle must use its fixed SHA-bound path")
    _safe_directory(work_root)
    if seed_bundle.is_symlink():
        raise ReleaseError("baseline overlay seed bundle is a symlink")
    try:
        source_fd = os.open(seed_bundle, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
    except OSError as exc:
        raise ReleaseError("baseline overlay seed bundle is missing or unsafe") from exc
    with os.fdopen(source_fd, "rb") as source, tempfile.TemporaryDirectory(
            prefix="baseline-overlay-seed-", dir=work_root) as temporary:
        # Git worktree metadata resolves through this parent when the isolated
        # build account checks the candidate. Permit traversal, not listing.
        os.chmod(temporary, 0o711)
        source_info = os.fstat(source.fileno())
        if not stat.S_ISREG(source_info.st_mode) or source_info.st_size <= 0:
            raise ReleaseError("baseline overlay seed bundle is not a non-empty regular file")
        bundle = Path(temporary) / "seed.bundle"
        digest = hashlib.sha256()
        with bundle.open("xb") as target:
            for block in iter(lambda: source.read(1024 * 1024), b""):
                digest.update(block)
                target.write(block)
            target.flush()
            os.fsync(target.fileno())
        seed_repo = Path(temporary) / "seed.git"
        _run(["git", "init", "--bare", "--quiet", str(seed_repo)], umask=0o022)
        verification = _run(["git", f"--git-dir={seed_repo}", "bundle", "verify", str(bundle)]).stdout
        if "The bundle records a complete history." not in verification:
            raise ReleaseError("baseline overlay seed bundle is not self-contained")
        ref = "refs/heads/main"
        heads = _run(["git", "bundle", "list-heads", str(bundle)]).stdout.splitlines()
        if heads != [f"{candidate_sha} {ref}"]:
            raise ReleaseError("baseline overlay seed bundle has unexpected refs")
        _run(["git", f"--git-dir={seed_repo}", "fetch", "--no-tags", str(bundle), f"{ref}:refs/heads/main"],
             umask=0o022)
        _run(["git", f"--git-dir={seed_repo}", "fsck", "--full", "--strict", "--no-reflogs"])
        candidate_tree = _tree(seed_repo, candidate_sha)
        if (not _is_ancestor(seed_repo, base_sha, candidate_sha)
                or base_sha not in _git(seed_repo, "rev-list", "--first-parent", candidate_sha).splitlines()):
            raise ReleaseError("baseline overlay seed bundle does not prove candidate ancestry and tree")
        if _tree(seed_repo, base_sha) != _tree(repo, base_sha):
            raise ReleaseError("baseline overlay seed base tree differs from domestic main")
        yield seed_repo, digest.hexdigest(), candidate_tree


def _run_baseline_overlay_preflight(config: dict[str, Any], repo: Path, *,
                                    base_sha: str, candidate_sha: str) -> dict[str, Any]:
    """Run the trusted tool lanes and staging helper contract on the exact 291-based seed."""
    work_root = Path(config["work_root"])
    _safe_directory(work_root)
    with tempfile.TemporaryDirectory(prefix="baseline-overlay-check-", dir=work_root) as temporary:
        check_root = Path(temporary)
        # The isolated build account must traverse this root-owned scratch
        # parent to read the candidate and trusted-policy worktrees.
        os.chmod(check_root, 0o755)
        worktree = check_root / "candidate"
        _run(["git", f"--git-dir={repo}", "worktree", "add", "--detach", str(worktree), candidate_sha],
             timeout=120, umask=0o022)
        _make_worktree_metadata_readable(repo, worktree)
        check_config = {**config, "work_root": temporary}
        report_dir = Path(legacy.BUILD_ROOT) / "domestic-main-checks" / f"{candidate_sha}-baseline-{time.time_ns()}"
        checks = _check_report(check_config, repo, worktree, report_dir, base_sha, candidate_sha,
                               diagnostic_root=work_root / "diagnostics")
        if (checks.get("status") != "passed" or checks.get("baseline_sha") != base_sha
                or checks.get("head_sha") != candidate_sha or "preflight" not in checks.get("selected_lanes", [])):
            raise ReleaseError("exact seed candidate did not pass trusted base-291 tool preflight")
        python_files = ["scripts/domestic_main_release.py", "deploy/domestic-promote.py"]
        compile_code = (
            "from pathlib import Path; import sys; root=Path(sys.argv[1]); "
            "[compile((root / name).read_bytes(), name, 'exec') for name in sys.argv[2:]]; "
            "print('overlay-tool-syntax-ok')"
        )
        _build_command(check_config, ["/usr/bin/python3", "-c", compile_code, str(worktree), *python_files],
                       cwd=worktree, timeout=120, safe_repository=worktree)
        _build_command(check_config, ["/usr/bin/python3", str(worktree / "scripts/domestic_main_release.py"), "--help"],
                       cwd=worktree, timeout=60, safe_repository=worktree)
        _build_command(check_config, ["/usr/bin/python3", "-m", "unittest",
                                      "scripts.test_domestic_release_controller"],
                       cwd=worktree, timeout=900, safe_repository=worktree)
        host = _run_stage_helper_host_contract(config, repo, candidate_sha)
        if not isinstance(host, dict):
            raise ControllerMaintenanceRequired("exact candidate staging helper host contract did not run")
        _run(["git", f"--git-dir={repo}", "worktree", "remove", "--force", str(worktree)], timeout=120)
        return {"baseline_sha": base_sha, "head_sha": candidate_sha,
                "candidate_tree": _tree(repo, candidate_sha),
                "check_receipt_sha256": checks.get("execution_receipt_sha256"),
                "selected_lanes": checks.get("selected_lanes"),
                "stage_host_contract": host}


def _overlay_evidence_digest(evidence: dict[str, Any]) -> str:
    canonical = json.dumps(evidence, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode()
    return hashlib.sha256(canonical).hexdigest()


def _overlay_evidence_path(config: dict[str, Any], candidate_sha: str) -> Path:
    candidate_sha = _sha(candidate_sha, "baseline overlay candidate SHA")
    return Path(config["work_root"]) / f"baseline-helper-overlay-{candidate_sha}.json"


def _active_overlay_candidate_path(config: dict[str, Any]) -> Path:
    return Path(config["work_root"]) / "baseline-helper-overlay-active.json"


def _read_active_overlay_candidate(config: dict[str, Any]) -> dict[str, Any] | None:
    path = _active_overlay_candidate_path(config)
    if not path.exists() and not path.is_symlink():
        return None
    _safe_directory(path.parent)
    try:
        descriptor = os.open(path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
    except OSError as exc:
        raise ReleaseError("active baseline overlay pointer is missing or unsafe") from exc
    with os.fdopen(descriptor, "rb") as stream:
        info = os.fstat(stream.fileno())
        if (not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_gid != 0
                or stat.S_IMODE(info.st_mode) != 0o600 or info.st_size > 16 * 1024):
            raise ReleaseError("active baseline overlay pointer is not root-only")
        raw = stream.read(16 * 1024 + 1)
    try:
        value = json.loads(raw.decode("utf-8"), object_pairs_hook=_unique_json_object)
    except (UnicodeDecodeError, json.JSONDecodeError, ValueError) as exc:
        raise ReleaseError("active baseline overlay pointer is invalid") from exc
    if (not isinstance(value, dict) or set(value) != {"schema_version", "candidate_sha", "superseded_candidate_shas"}
            or value.get("schema_version") != 1 or not isinstance(value.get("superseded_candidate_shas"), list)):
        raise ReleaseError("active baseline overlay pointer has an invalid schema")
    active = _sha(value.get("candidate_sha"), "active overlay candidate SHA")
    history = [_sha(sha, "superseded overlay candidate SHA") for sha in value["superseded_candidate_shas"]]
    if len(history) != len(set(history)) or active in history:
        raise ReleaseError("active baseline overlay pointer history is invalid")
    return value


def _select_active_overlay_candidate(config: dict[str, Any], candidate_sha: str, *,
                                     cursor_exists: bool, ledger_exists: bool,
                                     allow_supersede: bool = False) -> dict[str, Any]:
    """Bind the one overlay SHA; replacement is allowed only before cursor/ledger creation."""
    if os.geteuid() != 0:
        raise ReleaseError("active baseline overlay pointer must be managed by root")
    candidate_sha = _sha(candidate_sha, "active overlay candidate SHA")
    path = _active_overlay_candidate_path(config)
    _safe_directory(path.parent)
    active = _read_active_overlay_candidate(config)
    if active is None:
        if cursor_exists or ledger_exists:
            raise ReleaseError("active overlay candidate pointer is missing after cursor or ledger creation")
        active = {"schema_version": 1, "candidate_sha": candidate_sha,
                  "superseded_candidate_shas": []}
        atomic_json(path, active, mode=0o600)
        return active
    if active["candidate_sha"] == candidate_sha:
        return active
    if not allow_supersede or cursor_exists or ledger_exists:
        raise ReleaseError("baseline overlay candidate is already fixed by an existing cursor or ledger")
    history = list(active["superseded_candidate_shas"])
    if active["candidate_sha"] not in history:
        history.append(active["candidate_sha"])
    if candidate_sha in history:
        raise ReleaseError("a superseded baseline overlay candidate cannot be reactivated")
    replacement = {"schema_version": 1, "candidate_sha": candidate_sha,
                   "superseded_candidate_shas": history}
    atomic_json(path, replacement, mode=0o600)
    return replacement


def _production_cursor_exists(config: dict[str, Any]) -> bool:
    """Read only whether the fixed production cursor path exists; SSH errors fail closed."""
    code = "import os,sys; print('present' if os.path.lexists(sys.argv[1]) else 'absent')"
    path = str(config.get("prod_cursor_path", CURSOR_PATH))
    if path != CURSOR_PATH:
        raise ReleaseError("production cursor path must use the fixed protected path")
    command = "sudo -n /usr/bin/python3 -c " + shlex.quote(code) + " " + shlex.quote(path)
    result = _production_ssh(config, command, timeout=30)
    if result not in {"present", "absent"}:
        raise ReleaseError("production cursor presence readback is invalid")
    return result == "present"


def _stable_overlay_evidence(value: dict[str, Any]) -> dict[str, Any]:
    normalized = json.loads(json.dumps(value))
    normalized.pop("created_at_utc", None)
    preflight = normalized.get("local_preflight")
    if isinstance(preflight, dict):
        # Keep the original run receipt in the immutable marker for audit,
        # but do not make same-candidate recovery depend on run-local details.
        preflight.pop("check_receipt_sha256", None)
    return normalized


def _read_baseline_overlay_evidence(config: dict[str, Any], candidate_sha: str) -> dict[str, Any]:
    path = _overlay_evidence_path(config, candidate_sha)
    _safe_directory(path.parent)
    try:
        descriptor = os.open(path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
    except OSError as exc:
        raise ReleaseError("baseline helper overlay evidence is missing or unsafe") from exc
    with os.fdopen(descriptor, "rb") as stream:
        info = os.fstat(stream.fileno())
        if (not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_gid != 0
                or stat.S_IMODE(info.st_mode) != 0o600 or info.st_size > 128 * 1024):
            raise ReleaseError("existing baseline overlay evidence file is unsafe")
        raw = stream.read(128 * 1024 + 1)
    try:
        stored = json.loads(raw.decode("utf-8"), object_pairs_hook=_unique_json_object)
    except (UnicodeDecodeError, json.JSONDecodeError, ValueError) as exc:
        raise ReleaseError("existing baseline overlay evidence is invalid") from exc
    if (not isinstance(stored, dict) or set(stored) != {"schema_version", "evidence", "evidence_sha256"}
            or stored.get("schema_version") != 1 or not isinstance(stored.get("evidence"), dict)
            or stored.get("evidence_sha256") != _overlay_evidence_digest(stored["evidence"])):
        raise ReleaseError("existing baseline overlay evidence digest or schema is invalid")
    return stored


def _store_baseline_overlay_evidence(config: dict[str, Any], evidence: dict[str, Any]) -> dict[str, Any]:
    """Create the root-only overlay receipt once; re-entry accepts only same identity."""
    if os.geteuid() != 0:
        raise ReleaseError("baseline helper overlay evidence must be stored by root")
    candidate = evidence.get("candidate")
    if not isinstance(candidate, dict) or not isinstance(candidate.get("sha"), str):
        raise ReleaseError("baseline overlay evidence lacks a candidate SHA")
    path = _overlay_evidence_path(config, candidate["sha"])
    _safe_directory(path.parent)
    expected = {"schema_version": 1, "evidence": evidence,
                "evidence_sha256": _overlay_evidence_digest(evidence)}
    try:
        descriptor = os.open(path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
    except FileNotFoundError:
        try:
            descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL |
                                 getattr(os, "O_NOFOLLOW", 0), 0o600)
        except FileExistsError:
            return _store_baseline_overlay_evidence(config, evidence)
        try:
            info = os.fstat(descriptor)
            if (not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_gid != 0
                    or stat.S_IMODE(info.st_mode) != 0o600):
                raise ReleaseError("created baseline overlay evidence file is not root-only")
            raw = (json.dumps(expected, ensure_ascii=False, sort_keys=True, separators=(",", ":")) + "\n").encode()
            with os.fdopen(descriptor, "wb") as stream:
                stream.write(raw)
                stream.flush()
                os.fsync(stream.fileno())
            directory_fd = os.open(path.parent, os.O_RDONLY | getattr(os, "O_DIRECTORY", 0))
            try:
                os.fsync(directory_fd)
            finally:
                os.close(directory_fd)
        except Exception:
            try:
                os.close(descriptor)
            except OSError:
                pass
            raise
        return expected
    os.close(descriptor)
    stored = _read_baseline_overlay_evidence(config, candidate["sha"])
    if _stable_overlay_evidence(stored["evidence"]) != _stable_overlay_evidence(evidence):
        raise ReleaseError("baseline overlay evidence already exists for a different identity; do not replace it")
    return stored


def _verify_baseline_helper_overlay_candidate(config: dict[str, Any], repo: Path, *,
                                              base_sha: str, base_tree: str,
                                              installed_app: dict[str, str],
                                              baseline_bundle: dict[str, Any],
                                              candidate_sha: str, seed_bundle: Path) -> dict[str, Any]:
    """Verify the exact PR46 helper before invoking its production read-only commands."""
    if (base_sha != BASELINE_OVERLAY_BASE_SHA or base_tree != BASELINE_OVERLAY_BASE_TREE
            or installed_app.get("sha") != BASELINE_OVERLAY_APP_SHA
            or baseline_bundle.get("source_sha") != base_sha
            or baseline_bundle.get("source_tree") != base_tree
            or baseline_bundle.get("previous_main_sha") != installed_app.get("sha")
            or baseline_bundle.get("baseline_transition") is not True):
        raise ReleaseError("baseline helper overlay is restricted to the saved 291/app960 transition")
    candidate_sha = _sha(candidate_sha, "baseline overlay candidate SHA")
    if candidate_sha == base_sha:
        raise ReleaseError("baseline overlay candidate must advance baseline main")
    with _verified_overlay_seed_bundle(repo, Path(config["work_root"]), seed_bundle,
                                       candidate_sha, base_sha) as (seed_repo, seed_digest, candidate_tree):
        _first_parent_chain(seed_repo, base_sha, candidate_sha)
        classification = _classify_candidate(seed_repo, base_sha, candidate_sha)
        changed_fixed = sorted(classification.get("controller_files", []))
        allowed_fixed = sorted({"deploy/domestic-promote.py", "scripts/domestic_main_release.py"})
        if classification.get("runtime_changed") is not False or changed_fixed != allowed_fixed:
            raise ReleaseError("baseline helper overlay accepts only the reviewed PR46 controller-only change")
        helper_sha = hashlib.sha256(_source_blob(seed_repo, candidate_sha, "deploy/domestic-promote.py")).hexdigest()
        controller_sha = hashlib.sha256(_source_blob(seed_repo, candidate_sha, "scripts/domestic_main_release.py")).hexdigest()
        if _file_sha256(Path(__file__).resolve(strict=True)) != controller_sha:
            raise ControllerMaintenanceRequired("resume-baseline must run from the exact PR46 candidate controller bytes")
        # The normal fixed-file verifier remains strict for every other file.
        unchanged_fixed = sorted(set(builder.FIXED_CONTROLLER_FILES) - {"deploy/domestic-promote.py"})
        _verify_controller_files(config, repo, base_sha, unchanged_fixed)
        stage_helper = Path(legacy._helper_fixed_path(config["stage_helper"]))
        if not _protected_exact_helper(stage_helper, helper_sha):
            raise ControllerMaintenanceRequired("staging helper is not the exact PR46 overlay blob")
        prod_helper_path = legacy._helper_fixed_path(config["prod_helper"])
        prod_helper_sha = legacy._remote_file_sha256(config, prod_helper_path)
        if prod_helper_sha != helper_sha:
            raise ControllerMaintenanceRequired("production helper is not the exact PR46 overlay blob")
        preflight = _run_baseline_overlay_preflight(
            config, seed_repo, base_sha=base_sha, candidate_sha=candidate_sha,
        )
        return {"candidate_sha": candidate_sha, "candidate_tree": candidate_tree,
                "helper_sha256": helper_sha, "controller_sha256": controller_sha,
                "seed_bundle_sha256": seed_digest, "local_preflight": preflight}


def _record_baseline_helper_overlay(config: dict[str, Any], *,
                                    base_sha: str, base_tree: str,
                                    installed_app: dict[str, str],
                                    baseline_bundle: dict[str, Any],
                                    saved_backup: dict[str, Any],
                                    verified_candidate: dict[str, Any]) -> dict[str, Any]:
    source_receipt_sha = _digest(saved_backup.get("source_receipt_sha256"),
                                 "production baseline source receipt digest")
    candidate_sha = verified_candidate["candidate_sha"]
    evidence = {
            "schema_version": 1,
            "operation": "resume_baseline_helper_overlay",
            "baseline": {"sha": base_sha, "tree": base_tree},
            "installed_app": {key: installed_app[key] for key in ("sha", "tree", "manifest_sha256")},
            "production_source_backup": {
                "source_sha": base_sha,
                "source_tree": base_tree,
                "previous_main_sha": installed_app["sha"],
                "bundle_sha256": baseline_bundle["bundle_sha256"],
                "source_receipt_sha256": source_receipt_sha,
            },
            "candidate": {"pr_number": BASELINE_OVERLAY_PR, "sha": candidate_sha,
                          "tree": verified_candidate["candidate_tree"],
                          "helper_blob_sha256": verified_candidate["helper_sha256"],
                          "controller_blob_sha256": verified_candidate["controller_sha256"]},
            "local_preflight": verified_candidate["local_preflight"],
            "seed_bundle_sha256": verified_candidate["seed_bundle_sha256"],
            "created_at_utc": _utc_now(),
    }
    stored = _store_baseline_overlay_evidence(config, evidence)
    return {"path": str(_overlay_evidence_path(config, candidate_sha)),
            "evidence_sha256": stored["evidence_sha256"],
            "seed_bundle_sha256": verified_candidate["seed_bundle_sha256"],
            "helper_blob_sha256": verified_candidate["helper_sha256"],
            "source_receipt_sha256": source_receipt_sha}


def _verify_existing_baseline_helper_overlay(config: dict[str, Any], repo: Path, *,
                                            candidate_sha: str, base_sha: str, base_tree: str,
                                            installed_app: dict[str, str],
                                            production_cursor: dict[str, Any] | None = None) -> dict[str, Any]:
    """Read the immutable proof and verify only the live baseline transition identities."""
    candidate_sha = _sha(candidate_sha, "baseline overlay candidate SHA")
    stored = _read_baseline_overlay_evidence(config, candidate_sha)
    evidence = stored["evidence"]
    candidate, backup, preflight = (evidence.get("candidate"), evidence.get("production_source_backup"),
                                    evidence.get("local_preflight"))
    if (evidence.get("operation") != "resume_baseline_helper_overlay"
            or evidence.get("baseline") != {"sha": BASELINE_OVERLAY_BASE_SHA, "tree": BASELINE_OVERLAY_BASE_TREE}
            or (base_sha, base_tree) != (BASELINE_OVERLAY_BASE_SHA, BASELINE_OVERLAY_BASE_TREE)
            or evidence.get("installed_app") != {key: installed_app.get(key)
                                                  for key in ("sha", "tree", "manifest_sha256")}
            or installed_app.get("sha") != BASELINE_OVERLAY_APP_SHA
            or not isinstance(candidate, dict) or candidate.get("pr_number") != BASELINE_OVERLAY_PR
            or candidate.get("sha") != candidate_sha
            or not isinstance(backup, dict) or not isinstance(preflight, dict)):
        raise ReleaseError("baseline overlay does not match the selected 291/app960 transition")
    helper_sha = _digest(candidate.get("helper_blob_sha256"), "overlay helper blob digest")
    controller_sha = _digest(candidate.get("controller_blob_sha256"), "overlay controller blob digest")
    _sha(candidate.get("tree"), "overlay candidate tree")
    active = _read_active_overlay_candidate(config)
    if not isinstance(active, dict) or active.get("candidate_sha") != candidate_sha:
        raise ReleaseError("baseline overlay candidate does not match the root-only active candidate pointer")
    seed_sha = _digest(evidence.get("seed_bundle_sha256"), "overlay seed bundle digest")
    if (preflight.get("baseline_sha") != base_sha or preflight.get("head_sha") != candidate_sha
            or preflight.get("candidate_tree") != candidate.get("tree")
            or not isinstance(preflight.get("selected_lanes"), list)
            or "preflight" not in preflight["selected_lanes"]):
        raise ReleaseError("stored local candidate preflight is not bound to base 291 and the exact seed head")
    _digest(preflight.get("check_receipt_sha256"), "local candidate check receipt digest")
    host = preflight.get("stage_host_contract")
    if (not isinstance(host, dict) or host.get("host_role") != "staging"
            or host.get("postgres_major") != 16 or host.get("database_connection") != "verified"
            or host.get("helper_sha256") != helper_sha):
        raise ReleaseError("stored staging helper host-contract evidence is not exact")
    _digest(backup.get("source_receipt_sha256"), "production source receipt digest")
    if (backup.get("source_sha") != base_sha or backup.get("source_tree") != base_tree
            or backup.get("previous_main_sha") != installed_app["sha"]):
        raise ReleaseError("baseline overlay source receipt identity is invalid")
    metadata = _load_existing_baseline_bundle(
        repo, Path(config["work_root"]), source_sha=base_sha, source_tree=base_tree,
        previous_main_sha=installed_app["sha"],
    )[1]
    if backup.get("bundle_sha256") != metadata.get("bundle_sha256"):
        raise ReleaseError("baseline overlay source bundle digest changed")
    if (not _protected_exact_helper(Path(legacy._helper_fixed_path(config["stage_helper"])), helper_sha)
            or legacy._remote_file_sha256(config, legacy._helper_fixed_path(config["prod_helper"])) != helper_sha
            or _file_sha256(Path(__file__).resolve(strict=True)) != controller_sha):
        raise ControllerMaintenanceRequired("stage, production and running controller must match the overlay candidate")
    saved = _verify_saved_production_bundle(config, metadata, baseline_transition=True)
    if _digest(saved.get("source_receipt_sha256"), "production source receipt digest") != backup["source_receipt_sha256"]:
        raise ReleaseError("production baseline source receipt changed")
    if production_cursor is not None:
        expected = {"main_sha": base_sha, "main_tree": base_tree,
                    "installed_app_sha": installed_app["sha"], "installed_app_tree": installed_app["tree"],
                    "installed_manifest_sha256": installed_app["manifest_sha256"],
                    "source_bundle_sha256": backup["bundle_sha256"]}
        if any(production_cursor.get(key) != value for key, value in expected.items()):
            raise ReleaseError("production cursor is not the exact baseline bound by the helper overlay")
    return {"path": str(_overlay_evidence_path(config, candidate_sha)),
            "evidence_sha256": stored["evidence_sha256"], "candidate_sha": candidate_sha,
            "helper_blob_sha256": helper_sha, "seed_bundle_sha256": seed_sha,
            "source_bundle_sha256": backup["bundle_sha256"]}


def resume_baseline(config: dict[str, Any], *, candidate_sha: str | None = None,
                    seed_bundle: Path | None = None) -> dict[str, Any]:
    """Finish a baseline whose exact source bundle is already durable on production.

    This path is deliberately read-only with respect to the bundle: it never
    uploads, saves, deletes, or rebuilds it. The production helper validates the
    saved receipt and bytes before the idempotent initial cursor write.
    """
    if os.geteuid() != 0:
        raise ReleaseError("resume-baseline must run as root")
    config = _check_config(config)
    if config.get("production_enabled") is not True:
        raise ReleaseError("baseline resumption requires production_enabled=true in the protected config")
    if (candidate_sha is None) != (seed_bundle is None):
        raise ReleaseError("baseline helper overlay requires both --candidate-sha and --seed-bundle")
    repo, state_path = Path(config["repo"]), Path(config["state"])
    with _locked(Path(config["lock"]), nonblocking=True):
        _assert_release_timers_stopped()
        if state_path.exists() or state_path.is_symlink():
            raise ReleaseError("domestic ledger already exists; baseline resumption never overwrites it")
        repository = verify_bare_repository(repo, controller_path=config["controller_path"],
                                            push_group=config["push_group"])
        main_sha, main_tree = repository["main_sha"], repository["main_tree"]
        prod_app, _ = _installed_app_identity(config, production=True)
        stage_app, _ = _installed_app_identity(config, production=False)
        if prod_app != stage_app:
            raise ReleaseError("staging and production installed application identities differ")
        _validate_baseline_identity(repo, main_sha, main_tree, prod_app)
        _pin_candidate(repo, main_sha)
        _bundle, bundle_meta = _load_existing_baseline_bundle(
            repo, Path(config["work_root"]), source_sha=main_sha,
            source_tree=main_tree, previous_main_sha=prod_app["sha"],
        )
        overlay = None
        if candidate_sha is None:
            _verify_controller_files(config, repo, main_sha, sorted(builder.FIXED_CONTROLLER_FILES))
            saved = _verify_saved_production_bundle(config, bundle_meta, baseline_transition=True)
        else:
            verified_candidate = _verify_baseline_helper_overlay_candidate(
                config, repo, base_sha=main_sha, base_tree=main_tree,
                installed_app=prod_app, baseline_bundle=bundle_meta,
                candidate_sha=candidate_sha,
                seed_bundle=seed_bundle,
            )
            # This read-only path-existence probe runs only after both installed
            # helper bytes have been checked against the exact seed candidate.
            cursor_exists = _production_cursor_exists(config)
            _select_active_overlay_candidate(
                config, candidate_sha, cursor_exists=cursor_exists,
                ledger_exists=state_path.exists() or state_path.is_symlink(),
                allow_supersede=True,
            )
            # Only after verifying the installed production helper's exact candidate
            # digest above may its read-only backup verifier be executed.
            saved = _verify_saved_production_bundle(config, bundle_meta, baseline_transition=True)
            overlay = _record_baseline_helper_overlay(
                config, base_sha=main_sha, base_tree=main_tree,
                installed_app=prod_app, baseline_bundle=bundle_meta,
                saved_backup=saved, verified_candidate=verified_candidate,
            )
        _record_production_cursor(config, candidate_sha=main_sha, candidate_tree=main_tree,
                                  previous_main_sha=main_sha, installed_app=prod_app,
                                  bundle_sha256=bundle_meta["bundle_sha256"], initialize=True)
        cursor = _verify_production_cursor(config)["cursor"]
        expected_cursor = {"main_sha": main_sha, "main_tree": main_tree,
                           "installed_app_sha": prod_app["sha"],
                           "installed_app_tree": prod_app["tree"],
                           "installed_manifest_sha256": prod_app["manifest_sha256"],
                           "source_bundle_sha256": bundle_meta["bundle_sha256"]}
        if any(cursor.get(key) != value for key, value in expected_cursor.items()):
            raise ReleaseError("resumed production baseline cursor does not match the saved bundle")
        return {"status": "baseline_resumed", "main_sha": main_sha, "main_tree": main_tree,
                "installed_app_sha": prod_app["sha"], "source_bundle_sha256": bundle_meta["bundle_sha256"],
                "production_backup": saved, "activation_required": True,
                "bundle_uploaded": False, "application_deployed": False,
                **({"baseline_helper_overlay": overlay} if overlay is not None else {})}


def verify(config: dict[str, Any], *, candidate_sha: str | None = None) -> dict[str, Any]:
    """Read-only check of stage bare main, durable ledger, and production cursor."""
    config = _check_config(config)
    repo, state_path = Path(config["repo"]), Path(config["state"])
    with _locked(Path(config["lock"]), nonblocking=True):
        repository = verify_bare_repository(repo, controller_path=config["controller_path"], push_group=config["push_group"])
        state = _load_state(state_path)
        if state.get("staging_out_of_sync") is True and state.get("batch") is None:
            raise ReleaseError("staging reset has not been acknowledged after an interrupted candidate")
        if repository["main_sha"] != state["main"]["sha"] or repository["main_tree"] != state["main"]["tree"]:
            raise ReleaseError("domestic bare main does not match its durable ledger")
        if candidate_sha is not None:
            if (repository["main_sha"] != BASELINE_OVERLAY_BASE_SHA
                    or repository["main_tree"] != BASELINE_OVERLAY_BASE_TREE):
                raise ReleaseError("baseline overlay candidate is valid only while domestic main remains at 291")
            installed_app, _ = _installed_app_identity(config, production=False)
            overlay = _verify_existing_baseline_helper_overlay(
                config, repo, candidate_sha=candidate_sha,
                base_sha=repository["main_sha"], base_tree=repository["main_tree"],
                installed_app=installed_app,
            )
        else:
            batch = state.get("batch")
            controller_sha = batch.get("controller_source_sha", repository["main_sha"]) if batch else repository["main_sha"]
            _verify_controller_files(config, repo, controller_sha, sorted(builder.FIXED_CONTROLLER_FILES))
        cursor = _verify_production_cursor(config)["cursor"]
        if candidate_sha is not None and cursor.get("source_bundle_sha256") != overlay["source_bundle_sha256"]:
            raise ReleaseError("production cursor source bundle differs from the helper overlay")
        if cursor.get("main_sha") != state["main"]["sha"] or cursor.get("main_tree") != state["main"]["tree"]:
            raise ReleaseError("production source cursor does not match domestic main")
        if cursor.get("installed_app_sha") != state["installed_app"]["sha"] or cursor.get("installed_manifest_sha256") != state["installed_app"]["manifest_sha256"]:
            raise ReleaseError("production installed app does not match domestic ledger")
        stage_identity = state["batch"]["installed_app"] if state.get("batch") else state["installed_app"]
        stage = legacy.stage_readback(stage_identity["sha"])
        legacy.verify_readback(stage, stage_identity["sha"], stage_identity["manifest_sha256"])
        return {"status": "verified", "main_sha": repository["main_sha"], "main_tree": repository["main_tree"],
                "installed_app_sha": state["installed_app"]["sha"], "queue_depth": len(state["queue"]),
                "pending_candidate_sha": (_active_queue_item(state) or {}).get("head_sha"),
                "staged_head_sha": state["batch"]["head_sha"] if state.get("batch") else None,
                "staged_app_sha": stage_identity["sha"]}


def _submit_stdin(config: dict[str, Any], payload: dict[str, Any] | None = None) -> dict[str, Any]:
    if os.geteuid() != 0:
        raise ReleaseError("submit-stdin is a fixed root-mediated endpoint")
    if payload is None:
        try:
            payload = json.load(sys.stdin)
        except (json.JSONDecodeError, OSError) as exc:
            raise ReleaseError("invalid submit request") from exc
    if (not isinstance(payload, dict)
            or not {"ref", "head_sha", "base_sha"} <= set(payload)
            or set(payload) - {"ref", "head_sha", "base_sha", "supersedes_candidate_id"}):
        raise ReleaseError("submit request fields are invalid")
    config = _check_config(config)
    return submit_candidate(Path(config["repo"]), Path(config["state"]),
                            payload["ref"], payload["head_sha"], payload["base_sha"],
                            Path(config["lock"]), payload.get("supersedes_candidate_id"))


def _archive_ack_stdin(config: dict[str, Any]) -> dict[str, Any]:
    """Accept one exact SHA over stdin for the fixed sudo-rs endpoint."""
    if os.geteuid() != 0:
        raise ReleaseError("archive-ack-stdin is a fixed root-mediated endpoint")

    def unique_object(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
        value: dict[str, Any] = {}
        for key, item in pairs:
            if key in value:
                raise ValueError("duplicate JSON key")
            value[key] = item
        return value

    try:
        raw = sys.stdin.buffer.read(513)
        if len(raw) > 512:
            raise ValueError("archive acknowledgement is too large")
        payload = json.loads(raw.decode("utf-8"), object_pairs_hook=unique_object)
    except (OSError, ValueError, AttributeError, TypeError) as exc:
        raise ReleaseError("invalid archive acknowledgement request") from exc
    if not isinstance(payload, dict) or set(payload) != {"sha"}:
        raise ReleaseError("archive acknowledgement fields are invalid")
    sha = payload["sha"]
    if not isinstance(sha, str) or not SHA.fullmatch(sha):
        raise ReleaseError("archive acknowledgement SHA is invalid")
    return archive_ack(_check_config(config), sha)


def restricted_ssh() -> None:
    """Forced SSH command for the push-only developer account."""
    original = os.environ.get("SSH_ORIGINAL_COMMAND", "")
    if len(original) > 2048:
        raise ReleaseError("restricted SSH command is too long")
    repo = DEFAULT_REPO
    git_environment = {"PATH": "/usr/bin:/bin", "HOME": pwd.getpwuid(os.geteuid()).pw_dir,
                       "LANG": "C", "GIT_CONFIG_NOSYSTEM": "1"}
    if original in {f"git-receive-pack '{repo}'", f"git-receive-pack {repo}",
                    f"git receive-pack '{repo}'", f"git receive-pack {repo}"}:
        os.umask(0o002)
        os.execve("/usr/bin/git", ["git", "-c", f"safe.directory={repo}", "receive-pack", repo], git_environment)
    if original in {f"git-upload-pack '{repo}'", f"git-upload-pack {repo}",
                    f"git upload-pack '{repo}'", f"git upload-pack {repo}"}:
        os.execve("/usr/bin/git", ["git", "-c", f"safe.directory={repo}", "upload-pack", repo], git_environment)
    submit = re.fullmatch(r"domestic-submit --ref (refs/heads/codex/[A-Za-z0-9][A-Za-z0-9._/-]{0,119}) --head ([0-9a-f]{40}) --base ([0-9a-f]{40})(?: --supersedes ([0-9a-f]{40}))?", original)
    if submit:
        payload_value = {"ref": submit.group(1), "head_sha": submit.group(2), "base_sha": submit.group(3)}
        if submit.group(4):
            payload_value["supersedes_candidate_id"] = submit.group(4)
        payload = json.dumps(payload_value) + "\n"
        result = _run(["/usr/bin/sudo", "-n", "/usr/bin/python3", DEFAULT_CONTROLLER,
                       "submit-stdin", "--config", DEFAULT_CONFIG], input_text=payload, timeout=30)
        print(json.dumps(json.loads(result.stdout), ensure_ascii=False, sort_keys=True))
        return
    ack = re.fullmatch(r"domestic-archive-ack --sha ([0-9a-f]{40})", original)
    if ack:
        payload = json.dumps({"sha": ack.group(1)}) + "\n"
        result = _run(["/usr/bin/sudo", "-n", "/usr/bin/python3", DEFAULT_CONTROLLER,
                       "archive-ack-stdin", "--config", DEFAULT_CONFIG], input_text=payload, timeout=60)
        print(json.dumps(json.loads(result.stdout), ensure_ascii=False, sort_keys=True))
        return
    raise ReleaseError("SSH account permits only fixed bare-repository fetch/push, domestic-submit and domestic-archive-ack")


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("bootstrap", "recover-partial-bootstrap", "prepare-baseline", "resume-baseline", "activate", "verify", "submit", "release", "submit-stdin", "ack-stage-reset", "maintenance-check", "batch-open", "batch-seal", "batch-reopen", "batch-tool-repair",
                                            "poll", "status", "promote", "promote-authorized", "reconcile", "archive-ack", "archive-ack-stdin", "restricted-ssh", "hook-pre-receive"))
    parser.add_argument("--config", type=Path, default=Path(DEFAULT_CONFIG))
    parser.add_argument("--repo", type=Path)
    parser.add_argument("--seed-repo", type=Path)
    parser.add_argument("--baseline-sha")
    parser.add_argument("--ref")
    parser.add_argument("--head")
    parser.add_argument("--base")
    parser.add_argument("--supersedes-candidate")
    parser.add_argument("--sha")
    parser.add_argument("--authorization-reference", help="reference to the already received human production command")
    parser.add_argument("--approval-digest", help="digest of the exact candidate, artifact and staging readback approved by a person")
    parser.add_argument("--failed-candidate", help="exact unevaluated queue-front candidate for a batch tool repair")
    parser.add_argument("--calibrate-check-capacity", action="store_true",
                        help="maintenance-only measurement before setting same-host disk/cache budgets")
    parser.add_argument("--tree")
    parser.add_argument("--installed-app-sha")
    parser.add_argument("--candidate-sha", help="exact PR #46 head for the one-time baseline helper overlay")
    parser.add_argument("--seed-bundle", type=Path, help="verified full seed bundle for the one-time baseline helper overlay")
    return parser


def main(argv: list[str] | None = None) -> int:
    parser = _parser()
    args = parser.parse_args(argv)
    if args.calibrate_check_capacity and args.action != "maintenance-check":
        parser.error("--calibrate-check-capacity is only valid with maintenance-check")
    allowed_candidate_actions = {"resume-baseline", "activate", "verify"}
    if args.action not in allowed_candidate_actions and (args.candidate_sha is not None or args.seed_bundle is not None):
        parser.error("--candidate-sha is valid only with resume-baseline, activate or verify")
    if args.action != "resume-baseline" and args.seed_bundle is not None:
        parser.error("--seed-bundle is valid only with resume-baseline")
    if args.action == "resume-baseline" and ((args.candidate_sha is None) != (args.seed_bundle is None)):
        parser.error("resume-baseline overlay requires both --candidate-sha and --seed-bundle")
    try:
        if args.action == "restricted-ssh":
            restricted_ssh()
            return 0
        if args.action == "hook-pre-receive":
            if os.geteuid() == 0:
                raise ReleaseError("receive hook must run as the push-only account")
            if args.repo is None or str(args.repo) != DEFAULT_REPO:
                parser.error("hook-pre-receive requires the fixed domestic bare repository")
            validate_receive_updates(args.repo, sys.stdin.read())
            result = {"status": "accepted"}
            print(json.dumps(result, ensure_ascii=False, sort_keys=True))
            return 0
        config = load_config(args.config)
        if args.calibrate_check_capacity:
            config["_check_capacity_calibration"] = True
        if args.action == "bootstrap":
            if os.geteuid() != 0:
                raise ReleaseError("bare repository bootstrap must run as root")
            repo = args.repo or Path(config["repo"])
            seed = args.seed_repo
            sha = args.baseline_sha
            if seed is None or sha is None:
                parser.error("bootstrap requires --seed-repo and --baseline-sha")
            result = bootstrap_bare_repository(repo, seed, sha, controller_path=config["controller_path"], push_group=config["push_group"])
        elif args.action == "recover-partial-bootstrap":
            if os.geteuid() != 0:
                raise ReleaseError("partial bootstrap recovery must run as root")
            if not (args.sha and args.tree and args.installed_app_sha):
                parser.error("recover-partial-bootstrap requires --sha, --tree and --installed-app-sha")
            result = recover_partial_bootstrap(config, args.sha, args.tree, args.installed_app_sha)
        elif args.action == "prepare-baseline":
            result = prepare_baseline(config)
        elif args.action == "resume-baseline":
            result = resume_baseline(config, candidate_sha=args.candidate_sha, seed_bundle=args.seed_bundle)
        elif args.action == "activate":
            if os.geteuid() != 0:
                raise ReleaseError("activate must run as root")
            result = activate(config, candidate_sha=args.candidate_sha)
        elif args.action == "verify":
            result = verify(config, candidate_sha=args.candidate_sha)
        elif args.action == "submit-stdin":
            result = _submit_stdin(config)
        elif args.action == "ack-stage-reset":
            result = acknowledge_stage_reset(config)
        elif args.action == "maintenance-check":
            if not args.sha:
                parser.error("maintenance-check requires --sha <candidate SHA>")
            result = maintenance_check(config, args.sha)
        elif args.action == "batch-open":
            result = batch_open(config, controller_sha=args.sha, controller_ref=args.ref)
        elif args.action == "batch-seal":
            result = batch_seal(config)
        elif args.action == "batch-reopen":
            if not args.approval_digest:
                parser.error("batch-reopen requires --approval-digest")
            result = batch_reopen(config, args.approval_digest,
                                  controller_sha=args.sha, controller_ref=args.ref)
        elif args.action == "batch-tool-repair":
            if not (args.failed_candidate and args.sha and args.ref):
                parser.error("batch-tool-repair requires --failed-candidate, --sha and --ref")
            result = batch_tool_repair(config, args.failed_candidate, args.sha, args.ref)
        elif args.action == "submit":
            if os.geteuid() != 0:
                raise ReleaseError("submit must run through the restricted root endpoint")
            if not (args.ref and args.head and args.base):
                parser.error("submit requires --ref, --head and --base")
            result = submit_candidate(Path(config["repo"]), Path(config["state"]), args.ref, args.head, args.base,
                                      Path(config["lock"]), args.supersedes_candidate)
        elif args.action == "release":
            if not (args.ref and args.head and args.base):
                parser.error("release requires --ref, --head and --base")
            result = release_candidate(config, args.ref, args.head, args.base,
                                       args.supersedes_candidate)
        elif args.action == "archive-ack":
            if os.geteuid() != 0:
                raise ReleaseError("archive acknowledgement must run as root")
            if not args.sha:
                parser.error("archive-ack requires --sha <SHA>")
            result = archive_ack(config, args.sha)
        elif args.action == "archive-ack-stdin":
            result = _archive_ack_stdin(config)
        elif args.action == "poll":
            result = poll(config)
        elif args.action == 'status':
            result = status_snapshot(config)
        elif args.action == 'promote-authorized':
            if not args.head or not args.authorization_reference:parser.error('promote-authorized requires --head and --authorization-reference')
            result = promote_authorized(config,args.head,args.authorization_reference)
        elif args.action == "promote":
            if not args.approval_digest:
                parser.error("promote requires --approval-digest from the staged review result")
            result = promote(config, args.approval_digest)
        elif args.action == "reconcile":
            result = reconcile(config)
        else:
            raise AssertionError(args.action)
    except (OSError, ValueError, ReleaseError, subprocess.SubprocessError, json.JSONDecodeError) as exc:
        # Avoid including child-process stderr or URLs that may contain secrets.
        print(f"domestic main release stopped: {type(exc).__name__}", file=sys.stderr)
        return 1
    print(json.dumps(result, ensure_ascii=False, sort_keys=True))
    if args.action == "release" and result.get("status") not in {
            "completed", "stage_validation_pending", "awaiting_human_approval", "batch_open"}:
        return 2
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
