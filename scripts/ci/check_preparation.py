"""Attempt-local, input-bound preparation and a single heavy-resource slot.

Only immutable migration results and frontend/dependency snapshots are shared.
Every checkout gets writable copies; every composition fixture gets a database.
"""
from __future__ import annotations

import argparse
from contextlib import contextmanager
import fcntl
from functools import wraps
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
from urllib.parse import parse_qs, unquote, urlsplit


def local_test_database_target(raw: str | None):
    """Parse only an explicit loopback synthetic database URL."""
    if not isinstance(raw, str) or not raw:
        return None
    try:
        parsed = urlsplit(raw)
        hostname = parsed.hostname
        port = parsed.port or 5432
        query = parse_qs(parsed.query, keep_blank_values=True, strict_parsing=True)
    except ValueError:
        return None
    if {name.lower() for name in query} & {
            "database", "dbname", "host", "hostaddr", "service", "servicefile"}:
        return None
    database = unquote(parsed.path.strip("/"))
    if (parsed.scheme not in {"postgres", "postgresql"}
            or hostname not in {"localhost", "127.0.0.1", "::1"}
            or not (database == "aicrm_ci" or database.startswith("aicrm_test_"))):
        return None
    return parsed, database, port, query


def postgres_test_connection_environment(raw: str) -> dict[str, str] | None:
    """Pin libpq to a checked local test target and clear ambient redirects."""
    target = local_test_database_target(raw)
    if target is None:
        return None
    parsed, database, port, query = target
    env = dict(os.environ)
    for name in ("PGHOSTADDR", "PGSERVICE", "PGSERVICEFILE"):
        env.pop(name, None)
    env.update(
        PGHOST=parsed.hostname or "",
        PGPORT=str(port),
        PGUSER=unquote(parsed.username or ""),
        PGPASSWORD=unquote(parsed.password or ""),
        PGDATABASE=database,
        PGSSLMODE=query.get("sslmode", ["prefer"])[0],
    )
    return env


def atomic_json(path: Path, value: dict) -> None:
    temporary = path.with_name(path.name + ".tmp-" + str(os.getpid()))
    temporary.write_text(json.dumps(value, sort_keys=True) + "\n")
    os.chmod(temporary, 0o600)
    temporary.replace(path)


def preparation_root() -> Path | None:
    raw = os.environ.get("AICRM_TEST_PREP_DIR")
    if not raw:
        return None
    root = Path(raw)
    if not root.is_absolute() or root.is_symlink():
        raise ValueError("preparation directory must be an absolute private directory")
    info = root.stat()
    if info.st_uid != os.getuid() or info.st_mode & 0o077:
        raise ValueError("preparation directory must belong exclusively to the check account")
    return root


@contextmanager
def lock(root: Path, name: str):
    path = root / name
    flags = os.O_CREAT | os.O_RDWR
    if hasattr(os, "O_NOFOLLOW"):
        flags |= os.O_NOFOLLOW
    descriptor = os.open(path, flags, 0o600)
    try:
        info = os.fstat(descriptor)
        if not stat.S_ISREG(info.st_mode) or info.st_uid != os.getuid():
            raise ValueError("preparation lock must be a regular file owned by the check account")
        if info.st_mode & 0o077:
            os.fchmod(descriptor, 0o600)
        with os.fdopen(descriptor, "r+") as file:
            descriptor = -1
            fcntl.flock(file, fcntl.LOCK_EX)
            yield
    finally:
        if descriptor >= 0:
            os.close(descriptor)


def snapshot_root(prep: Path) -> Path:
    """Return the persistent snapshot root, or the attempt root when disabled."""
    raw = os.environ.get("AICRM_TEST_PREP_CACHE")
    if not raw:
        return prep
    root = Path(raw)
    if not root.is_absolute() or root.is_symlink() or not root.is_dir():
        raise ValueError("preparation snapshot cache must be an existing absolute private directory")
    info = root.stat()
    if info.st_uid != os.getuid() or info.st_mode & 0o077:
        raise ValueError("preparation snapshot cache must belong exclusively to the check account")
    if prep.stat().st_dev != info.st_dev:
        raise ValueError("attempt preparation and snapshot cache must be on the same filesystem")
    for entry in root.iterdir():
        item = entry.lstat()
        if entry.is_symlink() or item.st_uid != os.getuid() or item.st_mode & 0o077:
            raise ValueError("preparation snapshot cache contains a linked or non-private entry")
        if entry.name in {"artifact.lock", "npm.lock"}:
            if not stat.S_ISREG(item.st_mode):
                raise ValueError("preparation snapshot lock is not a regular file")
        elif re.fullmatch(r"(?:artifact|npm)-[0-9a-f]{64}", entry.name):
            if not stat.S_ISDIR(item.st_mode):
                raise ValueError("preparation snapshot unit is not a directory")
        else:
            raise ValueError("preparation snapshot cache contains an unknown entry")
    return root


def checked_unit(cache: Path) -> bool:
    """Validate a snapshot unit's boundary; return False only when it is absent."""
    try:
        info = cache.lstat()
    except FileNotFoundError:
        return False
    if cache.is_symlink() or not stat.S_ISDIR(info.st_mode) or info.st_uid != os.getuid() or info.st_mode & 0o077:
        raise ValueError("preparation snapshot unit has an unsafe type, owner, or mode")
    return True


def snapshot_receipt(cache: Path) -> dict | None:
    path = cache / "receipt.json"
    try:
        info = path.lstat()
    except FileNotFoundError:
        return None
    if path.is_symlink() or not stat.S_ISREG(info.st_mode) or info.st_uid != os.getuid() or info.st_mode & 0o077:
        raise UnsafeSnapshot("preparation snapshot receipt has an unsafe type, owner, or mode")
    try:
        value = json.loads(path.read_text())
    except (OSError, ValueError):
        return None
    return value if isinstance(value, dict) else None


def snapshot_layout(cache: Path, directory_names: set[str]) -> bool:
    """Reject unknown top-level content and report incomplete units as corrupt."""
    seen: set[str] = set()
    for entry in cache.iterdir():
        info = entry.lstat()
        if entry.is_symlink() or info.st_uid != os.getuid():
            raise UnsafeSnapshot("preparation snapshot unit contains a linked or foreign entry")
        if entry.name == "receipt.json":
            if not stat.S_ISREG(info.st_mode) or info.st_mode & 0o077:
                raise UnsafeSnapshot("preparation snapshot receipt has an unsafe type or mode")
        elif entry.name in directory_names:
            if not stat.S_ISDIR(info.st_mode):
                raise UnsafeSnapshot("preparation snapshot payload has an unsafe type")
        else:
            raise UnsafeSnapshot("preparation snapshot unit contains an unknown entry")
        seen.add(entry.name)
    return seen == directory_names | {"receipt.json"}


class UnsafeSnapshot(ValueError):
    pass


def _safe_link(root: Path, path: Path) -> str:
    target = os.readlink(path)
    try:
        resolved_root = root.resolve(strict=True)
        resolved_target = path.resolve(strict=False)
        if os.path.commonpath((str(resolved_root), str(resolved_target))) != str(resolved_root):
            raise UnsafeSnapshot("preparation snapshot contains a link outside its unit")
    except (OSError, RuntimeError, ValueError) as error:
        if isinstance(error, UnsafeSnapshot):
            raise
        raise UnsafeSnapshot("preparation snapshot contains an invalid link") from error
    return target


@contextmanager
def heavy_slot(command: list[str], env: dict | None = None):
    environment = dict(os.environ if env is None else env)
    root = preparation_root()
    managed_go = any(Path(word).name == "go" and index + 1 < len(command)
                     and command[index + 1] in {"test", "vet"} for index, word in enumerate(command))
    if root and managed_go:
        # Go keeps vet, race, package selection and fresh test execution. Its
        # native tool hook acquires the shared slot only for compilation/linking;
        # cached test execution can overlap the single browser/build task.
        tool = go_quoted_join([sys.executable, str(Path(__file__).resolve()), "go-tool"])
        flag = go_quoted_join(["-toolexec=" + tool])
        environment["GOFLAGS"] = (environment.get("GOFLAGS", "") + " " + flag).strip()
        yield environment
        return
    heavy = any(word in command for word in ("go", "npm", "npx")) or any(
        "run-donor-view-consumers" in word or "dev_preflight" in word or "build-v3" in word
        or "check_preparation.py" in word for word in command)
    if not root or not heavy or environment.get("AICRM_HEAVY_SLOT_HELD") == "1":
        yield environment
        return
    with lock(root, "heavy.lock"):
        environment["AICRM_HEAVY_SLOT_HELD"] = "1"
        yield environment


def go_quoted_join(words: list[str]) -> str:
    # Match Go's cmd/internal/quoted.Join: these fields have no shell escapes.
    result = []
    for word in words:
        if not any(char in word for char in " \t\r\n'\""):
            result.append(word)
        elif "'" not in word:
            result.append("'" + word + "'")
        elif '"' not in word:
            result.append('"' + word + '"')
        else:
            raise ValueError("tool path cannot be represented in Go quoted flags")
    return " ".join(result)


def go_tool(command: list[str]) -> int:
    if not command or not Path(command[0]).is_absolute():
        raise ValueError("Go tool hook requires the actual absolute tool path")
    heavy = Path(command[0]).name in {"compile", "asm", "link", "cgo", "cover", "preprofile"}
    # Cache identity queries and vet analysis are light. Waiting for Chromium
    # here would serialize even a warm, otherwise cached Go invocation.
    if heavy and "-V=full" not in command[1:]:
        with heavy_slot(["go", "build"]) as env:
            return subprocess.run(command, env=env).returncode
    return subprocess.run(command).returncode


def serialized_preparation(function):
    @wraps(function)
    def wrapped(*args, **kwargs):
        # Lock order is always heavy slot -> snapshot lock. Children inherit
        # ownership so a nested build cannot deadlock on the parent's slot.
        with heavy_slot(["npm", "prepare"]) as env:
            previous = os.environ.get("AICRM_HEAVY_SLOT_HELD")
            if env.get("AICRM_HEAVY_SLOT_HELD") == "1":
                os.environ["AICRM_HEAVY_SLOT_HELD"] = "1"
            try:
                return function(*args, **kwargs)
            finally:
                if previous is None:
                    os.environ.pop("AICRM_HEAVY_SLOT_HELD", None)
                else:
                    os.environ["AICRM_HEAVY_SLOT_HELD"] = previous
    return wrapped


def event(kind: str, reused: bool, fingerprint: str) -> None:
    root = preparation_root()
    if root:
        record = json.dumps({"kind": kind, "reused": reused, "fingerprint": fingerprint,
                             "time": time.time()}, separators=(",", ":")) + "\n"
        with lock(root, "events.lock"):
            with (root / "preparation.jsonl").open("a") as output:
                output.write(record)


def digest_files(root: Path, paths: list[str]) -> str:
    digest = hashlib.sha256()
    for name in sorted(paths):
        path = root / name
        digest.update(name.encode() + b"\0")
        digest.update(path.read_bytes())
        digest.update(b"\0")
    return digest.hexdigest()


def directory_digest(root: Path, *, excluded: tuple[str, ...] = ()) -> str:
    if root.is_symlink():
        raise UnsafeSnapshot("preparation snapshot tree is not a real directory")
    if not root.is_dir():
        raise FileNotFoundError(root)
    entries: list[tuple[str, Path, str]] = []
    for current, directories, files in os.walk(root, topdown=True, followlinks=False):
        current_path = Path(current)
        kept_directories = []
        for name in directories:
            path = current_path / name
            relative = path.relative_to(root).as_posix()
            if path.is_symlink():
                entries.append((relative, path, "link"))
            else:
                kept_directories.append(name)
        directories[:] = kept_directories
        for name in files:
            path = current_path / name
            relative = path.relative_to(root).as_posix()
            entries.append((relative, path, "link" if path.is_symlink() else "file"))
    digest = hashlib.sha256()
    for name, path, kind in sorted(entries, key=lambda entry: entry[0]):
        if name in excluded:
            continue
        digest.update(name.encode() + b"\0")
        if kind == "link":
            digest.update(b"L\0" + _safe_link(root, path).encode())
        else:
            info = path.lstat()
            if not stat.S_ISREG(info.st_mode):
                raise UnsafeSnapshot("preparation snapshot contains a non-regular file")
            digest.update(f"F{stat.S_IMODE(info.st_mode):04o}\0".encode() + path.read_bytes())
        digest.update(b"\0")
    return digest.hexdigest()


def tool_input() -> list[str]:
    return [subprocess.check_output([tool, "--version"], text=True).strip() for tool in ("node", "npm")]


def run(command: list[str], root: Path) -> None:
    with heavy_slot(command) as env:
        # Installation/build subprocesses never need a database credential.
        env.pop("AICRM_DATABASE_URL", None)
        subprocess.run(command, cwd=root, env=env, check=True)


@serialized_preparation
def npm_dependencies(root: Path, prefix: str) -> None:
    prep = preparation_root()
    package = root / prefix
    modules = package / "node_modules"
    if modules.is_symlink():
        raise ValueError("candidate node_modules must not be a symlink")
    command = ["npm", "ci", "--prefer-offline", "--no-audit", "--no-fund"]
    if prefix != ".":
        command += ["--prefix", prefix]
    if prep is None:
        run(command, root)
        return
    fingerprint = hashlib.sha256(json.dumps([digest_files(package, ["package.json", "package-lock.json"]),
                                            tool_input()], sort_keys=True).encode()).hexdigest()
    snapshots = snapshot_root(prep)
    cache = snapshots / ("npm-" + fingerprint)
    stamp = package / "node_modules/.aicrm-preparation.json"
    with lock(snapshots, "npm.lock"):
        # Recheck the namespace under the snapshot lock before inspecting or
        # publishing a complete unit.
        if os.environ.get("AICRM_TEST_PREP_CACHE"):
            snapshot_root(prep)
        if stamp.is_file():
            if stamp.is_symlink():
                raise ValueError("npm preparation stamp must not be a symlink")
            try:
                data = json.loads(stamp.read_text())
            except (OSError, ValueError):
                data = {}
            marker = package / "node_modules/.package-lock.json"
            if (data.get("input") == fingerprint and marker.is_file()
                    and data.get("lock") == digest_files(marker.parent, [marker.name])
                    and data.get("output") == directory_digest(stamp.parent, excluded=(stamp.name,))):
                event("npm", True, fingerprint)
                return
        reused = checked_unit(cache)
        if reused:
            try:
                complete = snapshot_layout(cache, {"modules"})
                receipt = snapshot_receipt(cache)
                if not complete or receipt is None:
                    raise OSError("npm snapshot receipt is missing or malformed")
                valid = receipt.get("input") == fingerprint and receipt.get("output") == directory_digest(cache / "modules")
            except UnsafeSnapshot:
                raise
            except (OSError, ValueError):
                valid = False
            if not valid:
                event("npm_corruption",False,fingerprint)
                shutil.rmtree(cache)
                reused = False
        if not reused:
            run(command, root)
            temporary = Path(tempfile.mkdtemp(prefix="npm-build-", dir=prep))
            shutil.copytree(package / "node_modules", temporary / "modules", symlinks=True)
            atomic_json(temporary / "receipt.json",{"input":fingerprint,"output":directory_digest(temporary / "modules")})
            if temporary.stat().st_dev != snapshots.stat().st_dev:
                raise ValueError("npm preparation snapshot must be atomically published on the same filesystem")
            temporary.rename(cache)
        else:
            shutil.rmtree(modules, ignore_errors=True)
            shutil.copytree(cache / "modules", package / "node_modules", symlinks=True)
        marker = package / "node_modules/.package-lock.json"
        atomic_json(stamp, {"input": fingerprint, "lock": digest_files(marker.parent, [marker.name]),
                            "output":directory_digest(stamp.parent,excluded=(stamp.name,))})
        event("npm", reused, fingerprint)


def artifact_input(root: Path) -> str:
    # Bind generated assets to both the exact checked-out identity and current
    # tracked bytes. The manifest honors AICRM_SOURCE_SHA when supplied.
    head = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip()
    tree = subprocess.check_output(["git", "rev-parse", "HEAD^{tree}"], cwd=root, text=True).strip()
    names = subprocess.check_output(["git", "ls-files", "-z"], cwd=root).decode().split("\0")
    inputs = {
        "head": head,
        "tree": tree,
        "source_sha": os.environ.get("AICRM_SOURCE_SHA"),
        "tracked": digest_files(root, [name for name in names if name]),
        "tools": tool_input(),
    }
    return hashlib.sha256(json.dumps(inputs, sort_keys=True).encode()).hexdigest()


def artifact_valid(cache: Path, fingerprint: str, *, require_web_dist: bool = False) -> bool:
    try:
        if not snapshot_layout(cache, {"dist", "web-dist"} if require_web_dist else {"dist"}):
            return False
        receipt = snapshot_receipt(cache)
        if receipt is None:
            return False
        if receipt.get("input") != fingerprint or receipt.get("output") != directory_digest(cache / "dist"):
            return False
        if require_web_dist and receipt.get("web_dist_output") != directory_digest(cache / "web-dist"):
            return False
        return True
    except UnsafeSnapshot:
        raise
    except (OSError, ValueError):
        return False


def touch_snapshot(cache: Path) -> None:
    os.utime(cache, None)


def write_attempt_artifact_receipt(prep: Path, snapshots: Path, cache: Path, fingerprint: str) -> None:
    if snapshots == prep:
        return
    local = prep / ("artifact-" + fingerprint)
    if local.is_symlink():
        raise ValueError("attempt artifact receipt directory must not be a symlink")
    local.mkdir(mode=0o700, exist_ok=True)
    info = local.stat()
    if info.st_uid != os.getuid() or info.st_mode & 0o077:
        raise ValueError("attempt artifact receipt directory must be private")
    receipt = json.loads((cache / "receipt.json").read_text())
    atomic_json(local / "receipt.json", receipt)


def restore_frontend_artifact(root: Path, cache: Path, fingerprint: str) -> None:
    if not artifact_valid(cache, fingerprint, require_web_dist=True):
        raise ValueError("prepared frontend artifact bytes or input identity changed")
    for source, destination in ((cache / "web-dist", root / "web/dist"),
                                (cache / "dist", root / "release/web/dist")):
        if destination.is_symlink():
            raise ValueError("frontend artifact destination must not be a symlink")
        shutil.rmtree(destination, ignore_errors=True)
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copytree(source, destination, symlinks=True)


def reusable_stage_tests(stage_function: str) -> list[list[str]] | None:
    """Recognize the canonical stage pipeline, returning only its test commands."""
    tests: list[list[str]] = []
    for raw in stage_function.splitlines()[1:-1]:
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        if line in {"mkdir -p release", "rm -rf -- release/web/dist"}:
            continue
        if re.fullmatch(r"node[ \t]+scripts/test-[A-Za-z0-9_.-]+\.mjs(?:[ \t]+[A-Za-z0-9_./-]+)*", line):
            tests.append(re.split(r"[ \t]+", line))
        elif line == "node scripts/stage-pr01-effects-ui.mjs web/dist release/web/dist":
            continue
        elif line == "node scripts/stage-survey-ui.mjs web/dist release/web/dist":
            continue
        elif line == "node scripts/stage-new-shell-ui.mjs web/dist release/web/dist":
            continue
        else:
            return None
    return tests or None


def reusable_build_frontend(build_function: str) -> bool:
    """Only reuse a build when its entire canonical command body is known."""
    commands = []
    for raw in build_function.splitlines()[1:-1]:
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        commands.append(line)
    return commands == [
        "node scripts/generate-ai-assistant-client.mjs",
        "npm run build",
        "node scripts/build-v3-host-adapters.mjs",
    ]


def materialize_artifact(root: Path) -> None:
    prep = preparation_root()
    if prep is None:
        raise ValueError("artifact preparation requires an attempt directory")
    fingerprint = artifact_input(root)
    snapshots = snapshot_root(prep)
    cache = snapshots / ("artifact-" + fingerprint)
    with lock(snapshots, "artifact.lock"):
        if snapshots != prep:
            snapshot_root(prep)
        if checked_unit(cache):
            if artifact_valid(cache, fingerprint, require_web_dist=True):
                restore_artifact(root, cache, fingerprint, True)
                touch_snapshot(cache)
                write_attempt_artifact_receipt(prep, snapshots, cache, fingerprint)
                return
            event("artifact_corruption", False, fingerprint)
            shutil.rmtree(cache)
    # Never wait for the heavy slot while holding a snapshot lock: a builder
    # may already hold the heavy slot and need this same artifact lock.
    materialize_uncached_artifact(root, fingerprint)


def restore_artifact(root: Path, cache: Path, fingerprint: str, reused: bool) -> None:
    if not artifact_valid(cache, fingerprint, require_web_dist=True):
        raise ValueError("prepared artifact bytes or input identity changed")
    destination = root / "web/dist"
    shutil.rmtree(destination, ignore_errors=True)
    shutil.copytree(cache / "dist", destination)
    event("artifact", reused, fingerprint)


@serialized_preparation
def materialize_uncached_artifact(root: Path, fingerprint: str) -> None:
    prep = preparation_root()
    if prep is None:
        raise ValueError("artifact preparation requires an attempt directory")
    snapshots = snapshot_root(prep)
    cache = snapshots / ("artifact-" + fingerprint)
    with lock(snapshots, "artifact.lock"):
        if snapshots != prep:
            snapshot_root(prep)
        reused = checked_unit(cache)
        if reused and not artifact_valid(cache, fingerprint, require_web_dist=True):
            event("artifact_corruption", False, fingerprint)
            shutil.rmtree(cache)
            reused = False
        if not reused:
            temporary = Path(tempfile.mkdtemp(prefix="artifact-build-", dir=prep))
            run(["npm", "run", "build", "--silent"], root)
            run(["node", "scripts/build-v3-host-adapters.mjs"], root)
            web_dist = temporary / "web-dist"
            shutil.copytree(root / "web/dist", web_dist, symlinks=True)
            stage = temporary / "dist"
            for script in ("stage-pr01-effects-ui.mjs", "stage-survey-ui.mjs", "stage-new-shell-ui.mjs"):
                run(["node", "scripts/" + script, "web/dist", str(stage)], root)
            atomic_json(temporary / "receipt.json", {
                "input": fingerprint,
                "output": directory_digest(stage),
                "web_dist_output": directory_digest(web_dist),
            })
            if temporary.stat().st_dev != snapshots.stat().st_dev:
                raise ValueError("artifact preparation snapshot must be atomically published on the same filesystem")
            temporary.rename(cache)
            reused = False
        restore_artifact(root, cache, fingerprint, reused)
        touch_snapshot(cache)
        write_attempt_artifact_receipt(prep, snapshots, cache, fingerprint)


@serialized_preparation
def frontend_checks(root: Path, mode: str) -> None:
    # Keep the existing build/check command bodies as the single authority.
    # The release script itself remains unchanged; checks omit only its repeated
    # unconditional npm ci setup, which is input-bound above.
    npm_dependencies(root, ".")
    npm_dependencies(root, "web/v3")
    run(["node", "scripts/prepare-donor-source-views.mjs"], root)
    source = (root / "scripts/run-donor-view-consumers.sh").read_text()
    functions = []
    for name in ("run_frontend_and_stage_checks", "build_frontend", "stage_frontend"):
        match = re.search(r"(?ms)^" + name + r"\(\) \{\n.*?^\}", source)
        if not match:
            raise ValueError("canonical frontend function is missing: " + name)
        functions.append(match.group())
    stage_function = functions[2]
    prep = preparation_root()
    if mode == "stage" and prep is not None and os.environ.get("AICRM_TEST_PREP_CACHE"):
        tests = reusable_stage_tests(stage_function)
        if tests and reusable_build_frontend(functions[1]):
            # This generated input can be tracked by the checkout. Refresh it
            # before computing the identity, just as build_frontend does.
            run(["node", "scripts/generate-ai-assistant-client.mjs"], root)
            fingerprint = artifact_input(root)
            snapshots = snapshot_root(prep)
            cache = snapshots / ("artifact-" + fingerprint)
            reused = False
            with lock(snapshots, "artifact.lock"):
                snapshot_root(prep)
                if checked_unit(cache):
                    if artifact_valid(cache, fingerprint, require_web_dist=True):
                        restore_frontend_artifact(root, cache, fingerprint)
                        touch_snapshot(cache)
                        write_attempt_artifact_receipt(prep, snapshots, cache, fingerprint)
                        reused = True
                    else:
                        event("artifact_corruption", False, fingerprint)
                        shutil.rmtree(cache)
            if reused:
                # All four canonical stage tests are fresh on every attempt;
                # only expensive build/transforms consume the snapshot.
                for command in tests:
                    run(command, root)
                publish_artifact(root, root / "release/web/dist")
                return
    entry = "build_frontend" if mode == "stage" else "run_frontend_and_stage_checks"
    script = "set -euo pipefail\nmode=" + mode + "\n" + "\n".join(functions) + "\n" + entry + "\nstage_frontend\n"
    # Serialize the complete build/check shell, including its npm/npx/Node
    # children, rather than opening a second heavyweight build on 2GB.
    with heavy_slot(["npm", "run", mode]) as env:
        env.pop("AICRM_DATABASE_URL", None)
        subprocess.run(["bash", "-c", script], cwd=root, env=env, check=True)
    publish_artifact(root, root / "release/web/dist")


@serialized_preparation
def publish_artifact(root: Path, stage: Path) -> None:
    prep = preparation_root()
    if prep is None:
        return
    fingerprint = artifact_input(root)
    snapshots = snapshot_root(prep)
    cache = snapshots / ("artifact-" + fingerprint)
    with lock(snapshots, "artifact.lock"):
        if snapshots != prep:
            snapshot_root(prep)
        if checked_unit(cache):
            if not artifact_valid(cache, fingerprint, require_web_dist=True):
                event("artifact_corruption", False, fingerprint)
                shutil.rmtree(cache)
            else:
                record = json.loads((cache / "receipt.json").read_text())
                if (record.get("output") != directory_digest(stage)
                        or record.get("web_dist_output") != directory_digest(root / "web/dist")):
                    raise ValueError("repeated staging produced different artifact bytes")
                touch_snapshot(cache)
                write_attempt_artifact_receipt(prep, snapshots, cache, fingerprint)
                event("artifact", True, fingerprint)
                return
        if not (stage / "asset-manifest.json").is_file():
            raise ValueError("prepared release artifact lacks its manifest")
        temporary = Path(tempfile.mkdtemp(prefix="artifact-stage-", dir=prep))
        shutil.copytree(stage, temporary / "dist")
        shutil.copytree(root / "web/dist", temporary / "web-dist", symlinks=True)
        atomic_json(temporary / "receipt.json", {
            "input": fingerprint,
            "output": directory_digest(stage),
            "web_dist_output": directory_digest(temporary / "web-dist"),
        })
        if temporary.stat().st_dev != snapshots.stat().st_dev:
            raise ValueError("artifact snapshot must be atomically published on the same filesystem")
        temporary.rename(cache)
        write_attempt_artifact_receipt(prep, snapshots, cache, fingerprint)
        event("artifact", False, fingerprint)


def cleanup_databases(prep: Path, raw: str) -> None:
    env = postgres_test_connection_environment(raw)
    if env is None:
        raise ValueError("cleanup requires an explicitly configured local synthetic database")
    for file in sorted(prep.glob("database-*.json")):
        if file.is_symlink():
            raise ValueError("database inventory is a symlink")
        value = json.loads(file.read_text())
        name = value.get("database", "")
        if (not re.fullmatch(r"aicrm_test_(tpl|clone)_[0-9a-f]{16}_acceptance_test", name)
                or file.name != "database-" + name + ".json"):
            raise ValueError("unexpected database in preparation cleanup inventory")
        owner = subprocess.run(["psql", "-X", "-Atqc",
            "SELECT pg_get_userbyid(datdba)=current_user FROM pg_database WHERE datname='"+name+"'"],
            env=env, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True, check=True, timeout=10)
        if owner.stdout.strip() not in ("", "t"):
            raise ValueError("inventoried database is not owned by the test account")
        subprocess.run(["psql", "-X", "-v", "ON_ERROR_STOP=1", "-qc", 'DROP DATABASE IF EXISTS "' + name + '"'],
                       env=env, stdout=subprocess.DEVNULL, check=True, timeout=30)
        file.unlink()


def main() -> None:
    if sys.argv[1:2] == ["go-tool"]:
        raise SystemExit(go_tool(sys.argv[2:]))
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("npm", "artifact", "stage", "check", "publish", "cleanup"))
    parser.add_argument("--prefix", default=".")
    args = parser.parse_args()
    root = Path.cwd()
    if args.action == "npm":
        npm_dependencies(root, args.prefix)
    elif args.action == "artifact":
        materialize_artifact(root)
    elif args.action in {"stage", "check"}:
        frontend_checks(root, args.action)
    elif args.action == "publish":
        publish_artifact(root, root / "release/web/dist")
    else:
        prep = preparation_root()
        if prep and list(prep.glob("database-*.json")):
            cleanup_databases(prep, os.environ["AICRM_DATABASE_URL"])


if __name__ == "__main__":
    main()
