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
OLD_RELEASE_TIMER = "aicrm-domestic-release.timer"
NEW_RELEASE_TIMER = "aicrm-domestic-main-release.timer"
HOST_UNIT_FILES = {
    "deploy/aicrm-domestic-main-release.service": Path("/etc/systemd/system/aicrm-domestic-main-release.service"),
    "deploy/aicrm-domestic-main-release.timer": Path("/etc/systemd/system/aicrm-domestic-main-release.timer"),
}
BUILD_TOOLCHAIN = ("go", "node", "npm", "git", "bash")


class ReleaseError(RuntimeError):
    pass


class ControllerMaintenanceRequired(ReleaseError):
    """A fixed controller must be installed through the reviewed maintenance path."""


def _run(args: list[str], *, cwd: Path | None = None, input_text: str | None = None,
         input_path: Path | None = None, timeout: int = 600, check: bool = True) -> subprocess.CompletedProcess[str]:
    if input_text is not None and input_path is not None:
        raise ValueError("command input must have one source")
    if input_path is not None:
        with input_path.open("rb") as source:
            raw = subprocess.run(args, cwd=cwd, input=source.read(), stdout=subprocess.PIPE,
                                 stderr=subprocess.PIPE, timeout=timeout)
        result = subprocess.CompletedProcess(args, raw.returncode,
                                             raw.stdout.decode("utf-8", errors="replace"),
                                             raw.stderr.decode("utf-8", errors="replace"))
    else:
        result = subprocess.run(args, cwd=cwd, input=input_text, text=True,
                                stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=timeout)
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
    if _is_bare_repo(repo):
        result = _run(["git", f"--git-dir={repo}", "show", f"{_sha(sha, 'source commit')}:{path}"])
    else:
        result = _run(["git", "-C", str(repo), "show", f"{_sha(sha, 'source commit')}:{path}"])
    return result.stdout.encode()


def _worktree_git(path: Path, *args: str, timeout: int = 600, check: bool = True) -> str:
    return _run(["git", "-C", str(path), *args], timeout=timeout, check=check).stdout.strip()


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
    if builder.classify(repo, installed_app_sha, expected_sha).get("runtime_changed") is not False:
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
            yield
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
    # can write Git objects and candidate refs only; receive-pack's hook is
    # the boundary that protects main and hooks from the forced SSH account.
    _secure_bare_repository_permissions(repo, grp.getgrnam(push_group).gr_gid)
    return {"repo": str(repo), "main_sha": baseline_sha, "main_tree": _tree(repo, baseline_sha)}


def _secure_bare_repository_permissions(repo: Path, push_gid: int) -> None:
    if os.geteuid() != 0:
        raise ReleaseError("bare repository ownership setup must run as root")
    # Git stores immutable objects separately from mutable refs. The push
    # account gets group write on those two data trees, while config, HEAD,
    # packed refs and hooks remain root-owned and unavailable for modification.
    os.chown(repo, 0, push_gid)
    # Candidate checks run as a separate, untrusted build identity. Git
    # objects are world-readable because this source is public, but only the
    # forced push account's group may write them.
    os.chmod(repo, 0o2755)
    codex_refs = repo / "refs/heads/codex"
    codex_refs.mkdir(mode=0o2775, parents=True, exist_ok=True)
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
    """Only loose/packed object storage and task refs accept push-account writes."""
    parts = relative.parts
    return parts[:1] == ("objects",) or parts[:3] == ("refs", "heads", "codex")


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
        if item.get("status") in {"pending", "stale_base", "failed", "outcome_unknown"}:
            return item
    return None


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
    _run(["git", f"--git-dir={repo}", "update-ref", ref, head_sha, ZERO_SHA])


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
        if state["status"] == "outcome_unknown":
            raise ReleaseError("production outcome is unknown; reconcile before submitting another candidate")
        if state.get("staging_out_of_sync") is True:
            raise ReleaseError("staging differs from the last production version; restore the verified application and synthetic database, then run ack-stage-reset")
        actual_main = _resolve_ref(repo, MAIN_REF)
        if actual_main != state["main"]["sha"] or base_sha != actual_main:
            raise ReleaseError("candidate base is stale; update and recheck the development branch from current domestic main")
        actual_head = _resolve_ref(repo, ref)
        if actual_head != head_sha:
            raise ReleaseError("submitted head does not match the exact pushed branch head")
        _first_parent_chain(repo, base_sha, head_sha)
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
                if same["status"] in {"pending", "stale_base"}:
                    return {"status": same["status"], "candidate_id": same["candidate_id"], "head_sha": head_sha,
                            "base_sha": base_sha, "queue_position": entries.index(same) + 1}
                # Keep attempts monotonic so every retry gets a fresh evidence directory.
                same.update({"status": "pending", "submitted_at_utc": _utc_now()})
                same.pop("failure", None)
                state["in_flight"] = None
                state["status"] = "ready"
                state["updated_at_utc"] = _utc_now()
                atomic_json(state_path, state)
                return {"status": "pending", "candidate_id": same["candidate_id"], "head_sha": head_sha,
                        "base_sha": base_sha, "queue_position": entries.index(same) + 1}
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
                "base_sha": base_sha, "queue_position": entries.index(item) + 1}


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
    path = root / base_sha
    if path.exists() or path.is_symlink():
        registered = _run(["git", f"--git-dir={repo}", "worktree", "list", "--porcelain"], check=False).stdout
        if path.is_symlink() or not path.is_dir() or str(path.resolve()) not in registered:
            raise ReleaseError("unregistered trusted policy worktree path exists")
        if _worktree_git(path, "rev-parse", "HEAD") != base_sha or _worktree_git(path, "status", "--porcelain"):
            raise ReleaseError("trusted policy worktree differs from its exact base")
        _make_worktree_metadata_readable(repo, path)
        return path
    _run(["git", f"--git-dir={repo}", "worktree", "add", "--detach", str(path), base_sha], timeout=120)
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


def _check_env(config: dict[str, Any], safe_repository: Path | None = None) -> dict[str, str]:
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
    path_value = config.get("build_path")
    if (not isinstance(path_value, str) or not path_value or "\n" in path_value or "\x00" in path_value
            or any(not part or not Path(part).is_absolute() for part in path_value.split(":"))):
        raise ReleaseError("isolated check PATH is invalid")
    if "/opt/aicrm/toolchain/npm/bin" not in path_value.split(":"):
        raise ReleaseError("isolated check PATH omits the fixed npm toolchain directory")
    build_root = Path(legacy.BUILD_ROOT)
    return {
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
        raise ReleaseError("required build tools are unavailable or not protected for the isolated build account")
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
                   check: bool = True, safe_repository: Path | None = None) -> subprocess.CompletedProcess[str]:
    env = _check_env(config, safe_repository)
    args = ["/usr/bin/sudo", "-n", "-u", legacy.BUILD_USER, "-H", "--", "/usr/bin/env", "-i"]
    args.extend([f"{key}={value}" for key, value in env.items()])
    args.extend(command)
    return _run(args, cwd=cwd, input_text=input_text, timeout=timeout, check=check)


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
sys.argv = ['dev_preflight', *sys.argv]
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

quality_lanes.commands = trusted_commands
quality_lanes.focused_commands = trusted_focused
sys.argv = ['quality_lanes', *sys.argv]
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
    result = _build_command(config, ["/usr/bin/python3", "-c", code, str(policy), str(candidate), base_sha, head_sha],
                            cwd=policy, timeout=900, check=False, safe_repository=candidate)
    if result.returncode != 0:
        raise ReleaseError("trusted impact analysis failed; candidate is held for full review")
    try:
        plan = json.loads(result.stdout.splitlines()[-1])
    except (IndexError, json.JSONDecodeError) as exc:
        raise ReleaseError("trusted impact analysis did not return a valid plan") from exc
    enforced = plan.get("enforced") if isinstance(plan, dict) else None
    lanes = enforced.get("selected_lanes") if isinstance(enforced, dict) else None
    if (plan.get("selection_source") != "trusted-baseline-registry-and-go-test-graph"
            or plan.get("baseline_sha") != base_sha or plan.get("head_sha") != head_sha
            or plan.get("head_tree") != _worktree_git(candidate, "rev-parse", "HEAD^{tree}")
            or plan.get("source_clean") is not True or plan.get("source", {}).get("head_matches") is not True
            or plan.get("source", {}).get("status") != []
            or plan.get("evidence_eligible") is not True
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
    return sorted(path for path in paths if path in fixed_policy_files or path.startswith((".github/workflows/", "scripts/ci/", "docs/governance/")))


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
    packages = plan.get("candidate_go_packages", [])
    profile = enforced.get("profile", "full")
    if not isinstance(lanes, list) or not lanes or any(lane not in builder_ci_lanes() for lane in lanes):
        raise ReleaseError("trusted impact plan selected an invalid lane set")
    if changed_policy and (enforced.get("selection_mode") != "full" or tuple(lanes) != builder_ci_lanes()):
        raise ReleaseError("trusted base policy did not force every lane for a policy change")
    if not isinstance(checks, list) or not isinstance(packages, list):
        raise ReleaseError("trusted impact plan contains an invalid focused test set")
    if changed_policy:
        return enforced, list(builder_ci_lanes()), [], [], "full"
    return enforced, lanes, checks, packages, profile


def _check_report(config: dict[str, Any], repo: Path, worktree: Path, report_dir: Path,
                  base_sha: str, head_sha: str) -> dict[str, Any]:
    if report_dir.exists() or report_dir.is_symlink():
        raise ReleaseError("candidate check evidence path already exists; inspect before retry")
    _build_command(config, ["/usr/bin/mkdir", "-m", "0700", "-p", str(report_dir)], cwd=Path("/"), timeout=30)
    report_info = report_dir.lstat()
    if not stat.S_ISDIR(report_info.st_mode) or report_info.st_uid != pwd.getpwnam(legacy.BUILD_USER).pw_uid or stat.S_IMODE(report_info.st_mode) != 0o700:
        raise ReleaseError("check evidence directory is not owned by the isolated build account")
    policy = _policy_worktree(repo, Path(config["work_root"]), base_sha, config["push_group"])
    changed_policy = _check_policy_changes(repo, base_sha, head_sha)
    toolchain = _verify_build_toolchain(config)
    plan = _trusted_preflight_plan(config, policy, worktree, base_sha, head_sha)
    enforced, lanes, checks, packages, profile = _enforced_lanes(plan, changed_policy)
    lane_results = []
    diagnostic_root = Path(config["work_root"]) / "diagnostics"
    _safe_directory(diagnostic_root, create=True)
    for lane in lanes:
        lane_dir = report_dir / lane
        _build_command(config, ["/usr/bin/mkdir", "-m", "0700", str(lane_dir)], cwd=worktree,
                       timeout=30, safe_repository=worktree)
        args = [lane, "--report-dir", str(lane_dir)]
        if lane == "preflight":
            args.extend(["--profile", "tooling" if profile == "tooling" else "full"])
        lane_checks = [check for check in checks if check.get("lane") == lane]
        if enforced.get("selection_mode") == "targeted" and lane != "preflight" and lane_checks:
            args.extend(["--focus-checks-json", json.dumps(lane_checks, separators=(",", ":"))])
        if enforced.get("selection_mode") == "targeted" and lane == "backend" and packages:
            args.extend(["--focus-packages-json", json.dumps(sorted(set(packages)), separators=(",", ":"))])
        command = ["/usr/bin/python3", "-c", _trusted_runner_code(), str(policy), str(worktree), *args]
        log = diagnostic_root / f"{head_sha}-{report_dir.name}-{lane}.log"
        if log.exists() or log.is_symlink():
            raise ReleaseError("trusted check diagnostic path already exists; inspect before retry")
        started = time.monotonic()
        with log.open("xb") as output:
            os.chmod(log, 0o600)
            run_args = ["/usr/bin/sudo", "-n", "-u", legacy.BUILD_USER, "-H", "--", "/usr/bin/env", "-i"]
            run_args.extend([f"{key}={value}" for key, value in _check_env(config, worktree).items()])
            run_args.extend(command)
            completed = subprocess.run(run_args, cwd=worktree, stdout=output, stderr=subprocess.STDOUT,
                                        timeout=6 * 60 * 60, check=False)
        lane_results.append({"lane": lane, "exit_code": completed.returncode,
                             "duration_seconds": round(time.monotonic() - started, 1),
                             "log_sha256": _file_sha256(log), "log_path": str(log)})
        if completed.returncode != 0:
            raise ReleaseError(f"trusted impact lane failed: {lane} (exit={completed.returncode})")
    if _worktree_git(worktree, "rev-parse", "HEAD") != head_sha or _worktree_git(worktree, "status", "--porcelain=v1", "--untracked-files=all"):
        raise ReleaseError("candidate source changed or became dirty during isolated checks")
    receipt = {
        "schema_version": 1, "status": "passed", "baseline_sha": base_sha,
        "baseline_tree": plan["baseline_tree"], "head_sha": head_sha, "head_tree": plan["head_tree"],
        "policy_fingerprint": plan["policy_fingerprint"],
        "trusted_policy_sha": base_sha,
        "selection_mode": "full" if changed_policy else enforced["selection_mode"],
        "profile": profile, "selected_lanes": lanes,
        "selection_reasons": (sorted(set((enforced.get("selection_reasons") or []) + ["trusted-base-policy-change-forced-full"]))
                              if changed_policy else enforced.get("selection_reasons")),
        "changed_paths": plan.get("changed_paths"), "toolchain": toolchain, "lane_results": lane_results,
        "execution_receipt_sha256": None, "verified_at_utc": _utc_now(),
    }
    canonical = json.dumps(receipt, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode()
    receipt["execution_receipt_sha256"] = hashlib.sha256(canonical).hexdigest()
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
    if baseline_transition:
        expected["baseline_transition"] = True
    if not isinstance(receipt, dict) or any(receipt.get(key) != value for key, value in expected.items()):
        raise ReleaseError("production source backup receipt does not match the candidate bundle")
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
    if builder.classify(repo, app_sha, main_sha)["runtime_changed"]:
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
    expected = state.get("installed_app")
    if not isinstance(expected, dict) or stage_app != expected or prod_app != expected:
        raise ReleaseError("stage and production must both read back the last verified application before clearing the staging block")


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
        state["staging_out_of_sync"] = False
        state["stage_reset_acknowledged_at_utc"] = _utc_now()
        # Preserve blocked state and failed queue item. The developer must
        # explicitly resubmit a corrected or rebased branch after this check.
        _update_state(state_path, state, status="blocked")
        return {"status": "stage_reset_verified", "installed_app_sha": prod_app["sha"],
                "main_sha": state["main"]["sha"]}


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
    if not isinstance(runtime_changed, bool):
        raise ReleaseError("in-flight runtime change classification is missing")
    installed_app = app_identity(inflight.get("installed_app"), "in-flight installed app")
    previous_app = app_identity(inflight.get("previous_installed_app"), "in-flight previous app")
    if runtime_changed:
        metadata = inflight.get("release_metadata")
        if (installed_app["sha"] != sha or installed_app["tree"] != tree
                or not isinstance(metadata, dict) or metadata.get("source_sha") != sha
                or metadata.get("source_tree") != tree
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
    _run(["git", f"--git-dir={repo}", "worktree", "add", "--detach", str(worktree), head_sha], timeout=120)
    _make_worktree_metadata_readable(repo, worktree)
    if _worktree_git(worktree, "rev-parse", "HEAD") != head_sha:
        raise ReleaseError("candidate checkout is not at the exact submitted head")
    if _worktree_git(worktree, "status", "--porcelain=v1", "--untracked-files=all"):
        raise ReleaseError("candidate checkout is not clean before checks")
    return worktree


def _needs_installed_alipay_smoke(changed_paths: list[str]) -> bool:
    return legacy._alipay_smoke_required({"changed_paths": changed_paths})


def _run_installed_smoke(config: dict[str, Any], worktree: Path, sha: str,
                         manifest_sha: str, helper_sha: str, tree_sha: str) -> dict[str, Any] | None:
    plan_paths = _worktree_git(worktree, "diff", "--name-only", "--no-renames", f"{sha}^1", sha).splitlines()
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


def _advance_main_cas(repo: Path, old_sha: str, new_sha: str) -> None:
    old_sha, new_sha = _sha(old_sha, "expected old main SHA"), _sha(new_sha, "candidate main SHA")
    current = _resolve_ref(repo, MAIN_REF)
    if current == new_sha:
        return
    if current != old_sha:
        raise ReleaseError("domestic main changed during the release; CAS stopped")
    _first_parent_chain(repo, old_sha, new_sha)
    _run(["git", f"--git-dir={repo}", "update-ref", MAIN_REF, new_sha, old_sha])


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
    item["status"] = "failed"
    item["failure"] = {"phase": phase, "error_type": type(error).__name__, "recorded_at_utc": _utc_now()}
    if isinstance(error, ControllerMaintenanceRequired):
        item["failure"]["required_action"] = "run maintenance-check for this exact SHA; review and install the checked fixed controller bytes with rollback; resubmit the same candidate"
    if state.get("in_flight", {}).get("stage_install_started") is True:
        state["staging_out_of_sync"] = True
        item["failure"]["required_recovery"] = "restore the prior verified application and rebuild the synthetic staging database, then run ack-stage-reset"
    state["in_flight"] = None
    state["controller_maintenance"] = None
    state["status"] = "blocked"
    state["updated_at_utc"] = _utc_now()
    atomic_json(state_path, state)


def process_candidate(config: dict[str, Any], state_path: Path, state: dict[str, Any], item: dict[str, Any]) -> dict[str, Any]:
    repo = Path(config["repo"])
    old_main_sha = _resolve_ref(repo, MAIN_REF)
    if old_main_sha != state["main"]["sha"]:
        raise ReleaseError("domestic main differs from the durable release ledger")
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
    state["controller_maintenance"] = None
    _update_state(state_path, state, status="blocked")
    try:
        classification = builder.classify(source_worktree, old_main_sha, item["head_sha"])
        controller_files = classification.get("controller_files", [])
        controller_receipt = _verify_controller_files(config, source_worktree, item["head_sha"], controller_files)
        check_receipt = _check_report(config, repo, source_worktree, report_dir, old_main_sha, item["head_sha"])
        paths = check_receipt["changed_paths"]
        runtime_changed = bool(classification.get("runtime_changed"))
        installed_app = dict(state["installed_app"])
        metadata = None
        stage_receipt = None
        smoke_receipt = None
        production_receipt = None
        bundle, bundle_meta = _create_full_bundle(repo, Path(config["work_root"]), item["head_sha"], head_tree)

        if runtime_changed:
            phase = "build"
            build_base = installed_app["sha"]
            base_release = Path(config.get("stage_releases", "/opt/aicrm/releases")) / build_base
            build_config = dict(config, repo=str(source_worktree))
            out, metadata = legacy.build_candidate(build_config, item["head_sha"], build_base,
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
                helper_source = _worktree_git(source_worktree, "show", f"{item['head_sha']}:deploy/domestic-promote.py")
                helper_sha = hashlib.sha256(helper_source.encode()).hexdigest()
                smoke_receipt = _run_installed_smoke(config, source_worktree, item["head_sha"],
                                                     metadata["release_files_sha256"], helper_sha, head_tree)
                if smoke_receipt is None:
                    raise ReleaseError("required installed behavior check was not run")
            phase = "source-backup"
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
            _update_state(state_path, state)
            _upload_and_store_bundle(config, bundle, bundle_meta)
            incoming, remote_metadata = legacy.copy_payload(config, item["head_sha"], out / "release",
                                                             out / "domestic-release.json", installed_app["sha"])
            metadata_bytes = (out / "domestic-release.json").read_bytes()
            metadata_sha = hashlib.sha256(metadata_bytes).hexdigest()
            phase = "production-install"
            state["in_flight"].update({"phase": phase, "production_install_started": True,
                                       "candidate_sha": item["head_sha"], "candidate_tree": head_tree,
                                       "previous_main_sha": old_main_sha,
                                       "previous_installed_app": dict(installed_app),
                                       "installed_app": {"sha": item["head_sha"], "tree": head_tree,
                                                         "manifest_sha256": metadata["release_files_sha256"]},
                                       "bundle_meta": bundle_meta,
                                       "package_metadata_sha256": metadata_sha,
                                       "release_metadata": metadata})
            _update_state(state_path, state)
            install_started = time.monotonic()
            result = _production_ssh(
                config, "sudo", "-n", config["prod_helper"], "--incoming", incoming,
                "--metadata", remote_metadata, "--expected-sha", item["head_sha"],
                "--metadata-sha256", metadata_sha, "--expected-base", installed_app["sha"], timeout=1800,
            )
            production_receipt = json.loads(result.splitlines()[-1])
            production = _verify_prod_app(config, item["head_sha"], metadata["release_files_sha256"])
            legacy.verify_install_receipt(production.get("receipt"), metadata, installed_app["sha"])
            if production_receipt != production.get("receipt"):
                raise ReleaseError("production installer response differs from independent readback")
            installed_app = dict(state["in_flight"]["installed_app"])
        else:
            # Source-only commits still get a full production recovery bundle
            # and a verified production health readback, but never install the
            # application package or execute migrations.
            phase = "source-backup"
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
            _update_state(state_path, state)
            _upload_and_store_bundle(config, bundle, bundle_meta)
            _verify_prod_app(config, installed_app["sha"], installed_app["manifest_sha256"])

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
        classification = builder.classify(worktree, main_sha, candidate_sha)
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
                "ctx=m._locked(lock,nonblocking=True); ctx.__enter__(); "
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
    classification = builder.classify(repo, base_sha, candidate_sha)
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


def poll(config: dict[str, Any]) -> dict[str, Any]:
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
        if state["status"] == "outcome_unknown":
            raise ReleaseError("production outcome is unknown; use reconcile, never reinstall blindly")
        if state["status"] == "blocked":
            raise ReleaseError("domestic release queue is blocked; inspect or resubmit its head candidate")
        maintenance = state.get("controller_maintenance")
        if maintenance is None:
            _verify_controller_files(config, repo, state["main"]["sha"], sorted(builder.FIXED_CONTROLLER_FILES))
        else:
            _verify_controller_maintenance_candidate(config, repo, state)
        item = _active_queue_item(state)
        if item is None:
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
            production = _verify_prod_app(config, sha, manifest)
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
        state["in_flight"] = None
        state["last_release"] = {"main_sha": sha, "main_tree": tree,
                                 "installed_app_sha": installed_app["sha"],
                                 "manifest_sha256": installed_app["manifest_sha256"],
                                 "source_bundle_sha256": bundle_meta["bundle_sha256"],
                                 "last_manual_confirmed_github_sha": (state.get("archive_ack") or {}).get("sha", "unknown"),
                                 "github_sync_observed_by": "manual_cli" if state.get("archive_ack") else "unknown",
                                 "github_pending_first_parent_count": _pending_archive_count(repo, (state.get("archive_ack") or {}).get("sha"), sha) if state.get("archive_ack") else "unknown",
                                 "status": "ready", "reconciled_at_utc": _utc_now()}
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


def activate(config: dict[str, Any]) -> dict[str, Any]:
    """Create the stage ledger only after the production source cursor is ready."""
    config = _check_config(config)
    with _locked(Path(config["lock"]), nonblocking=True):
        _assert_release_timers_stopped()
        return _activate_locked_no_lock(config)


def _activate_locked_no_lock(config: dict[str, Any]) -> dict[str, Any]:
    """Activate without reacquiring the lock (used by prepare-baseline)."""
    repo, state_path = Path(config["repo"]), Path(config["state"])
    if state_path.exists() or state_path.is_symlink():
        raise ReleaseError("domestic ledger already exists; never overwrite or reset it")
    repository = verify_bare_repository(repo, controller_path=config["controller_path"], push_group=config["push_group"])
    _verify_controller_files(config, repo, repository["main_sha"], sorted(builder.FIXED_CONTROLLER_FILES))
    cursor = _verify_production_cursor(config)["cursor"]
    if repository["main_sha"] != cursor.get("main_sha") or repository["main_tree"] != cursor.get("main_tree"):
        raise ReleaseError("domestic main does not match the independently read production source cursor")
    stage_app, _ = _installed_app_identity(config, production=False)
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


def verify(config: dict[str, Any]) -> dict[str, Any]:
    """Read-only check of stage bare main, durable ledger, and production cursor."""
    config = _check_config(config)
    repo, state_path = Path(config["repo"]), Path(config["state"])
    with _locked(Path(config["lock"]), nonblocking=True):
        repository = verify_bare_repository(repo, controller_path=config["controller_path"], push_group=config["push_group"])
        _verify_controller_files(config, repo, repository["main_sha"], sorted(builder.FIXED_CONTROLLER_FILES))
        state = _load_state(state_path)
        if state.get("staging_out_of_sync") is True:
            raise ReleaseError("staging reset has not been acknowledged after an interrupted candidate")
        if repository["main_sha"] != state["main"]["sha"] or repository["main_tree"] != state["main"]["tree"]:
            raise ReleaseError("domestic bare main does not match its durable ledger")
        cursor = _verify_production_cursor(config)["cursor"]
        if cursor.get("main_sha") != state["main"]["sha"] or cursor.get("main_tree") != state["main"]["tree"]:
            raise ReleaseError("production source cursor does not match domestic main")
        if cursor.get("installed_app_sha") != state["installed_app"]["sha"] or cursor.get("installed_manifest_sha256") != state["installed_app"]["manifest_sha256"]:
            raise ReleaseError("production installed app does not match domestic ledger")
        stage = legacy.stage_readback(state["installed_app"]["sha"])
        legacy.verify_readback(stage, state["installed_app"]["sha"], state["installed_app"]["manifest_sha256"])
        return {"status": "verified", "main_sha": repository["main_sha"], "main_tree": repository["main_tree"],
                "installed_app_sha": state["installed_app"]["sha"], "queue_depth": len(state["queue"]),
                "pending_candidate_sha": (_active_queue_item(state) or {}).get("head_sha")}


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
    parser.add_argument("action", choices=("bootstrap", "recover-partial-bootstrap", "prepare-baseline", "activate", "verify", "submit", "submit-stdin", "ack-stage-reset", "maintenance-check",
                                            "poll", "reconcile", "archive-ack", "archive-ack-stdin", "restricted-ssh", "hook-pre-receive"))
    parser.add_argument("--config", type=Path, default=Path(DEFAULT_CONFIG))
    parser.add_argument("--repo", type=Path)
    parser.add_argument("--seed-repo", type=Path)
    parser.add_argument("--baseline-sha")
    parser.add_argument("--ref")
    parser.add_argument("--head")
    parser.add_argument("--base")
    parser.add_argument("--supersedes-candidate")
    parser.add_argument("--sha")
    parser.add_argument("--tree")
    parser.add_argument("--installed-app-sha")
    return parser


def main(argv: list[str] | None = None) -> int:
    parser = _parser()
    args = parser.parse_args(argv)
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
        elif args.action == "submit-stdin":
            result = _submit_stdin(config)
        elif args.action == "ack-stage-reset":
            result = acknowledge_stage_reset(config)
        elif args.action == "maintenance-check":
            if not args.sha:
                parser.error("maintenance-check requires --sha <candidate SHA>")
            result = maintenance_check(config, args.sha)
        elif args.action == "submit":
            if os.geteuid() != 0:
                raise ReleaseError("submit must run through the restricted root endpoint")
            if not (args.ref and args.head and args.base):
                parser.error("submit requires --ref, --head and --base")
            result = submit_candidate(Path(config["repo"]), Path(config["state"]), args.ref, args.head, args.base,
                                      Path(config["lock"]), args.supersedes_candidate)
        elif args.action == "archive-ack":
            if os.geteuid() != 0:
                raise ReleaseError("archive acknowledgement must run as root")
            if not args.sha:
                parser.error("archive-ack requires --sha <SHA>")
            result = archive_ack(config, args.sha)
        elif args.action == "archive-ack-stdin":
            result = _archive_ack_stdin(config)
        elif args.action == "activate":
            if os.geteuid() != 0:
                raise ReleaseError("activate must run as root")
            result = activate(config)
        elif args.action == "verify":
            result = verify(config)
        elif args.action == "poll":
            result = poll(config)
        elif args.action == "reconcile":
            result = reconcile(config)
        else:
            raise AssertionError(args.action)
    except (OSError, ValueError, ReleaseError, subprocess.SubprocessError, json.JSONDecodeError) as exc:
        # Avoid including child-process stderr or URLs that may contain secrets.
        print(f"domestic main release stopped: {type(exc).__name__}", file=sys.stderr)
        return 1
    print(json.dumps(result, ensure_ascii=False, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
