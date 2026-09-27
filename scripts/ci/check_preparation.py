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
import subprocess
import sys
import tempfile
import time
from urllib.parse import parse_qs, unquote, urlsplit


def atomic_json(path: Path, value: dict) -> None:
    temporary = path.with_name(path.name + ".tmp-" + str(os.getpid()))
    temporary.write_text(json.dumps(value, sort_keys=True) + "\n")
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
    with (root / name).open("a") as file:
        fcntl.flock(file, fcntl.LOCK_EX)
        yield


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
    return digest_files(root, [p.relative_to(root).as_posix() for p in root.rglob("*")
                              if p.is_file() and p.relative_to(root).as_posix() not in excluded])


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
    command = ["npm", "ci", "--no-audit", "--no-fund"]
    if prefix != ".":
        command += ["--prefix", prefix]
    if prep is None:
        run(command, root)
        return
    fingerprint = hashlib.sha256(json.dumps([digest_files(package, ["package.json", "package-lock.json"]),
                                            tool_input()], sort_keys=True).encode()).hexdigest()
    cache = prep / ("npm-" + fingerprint)
    stamp = package / "node_modules/.aicrm-preparation.json"
    with lock(prep, "npm.lock"):
        if stamp.is_file():
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
        reused = cache.is_dir()
        if reused:
            try:
                receipt = json.loads((cache / "receipt.json").read_text())
                valid = receipt.get("input") == fingerprint and receipt.get("output") == directory_digest(cache / "modules")
            except (OSError, ValueError):
                valid = False
            if not valid:
                event("npm_corruption",False,fingerprint)
                shutil.rmtree(cache)
                reused = False
        if not reused:
            run(command, root)
            temporary = Path(tempfile.mkdtemp(prefix="npm-build-", dir=prep))
            try:
                shutil.copytree(package / "node_modules", temporary / "modules", symlinks=True)
                atomic_json(temporary / "receipt.json",{"input":fingerprint,"output":directory_digest(temporary / "modules")})
                temporary.rename(cache)
            finally:
                if temporary.exists():
                    shutil.rmtree(temporary)
        else:
            shutil.rmtree(package / "node_modules", ignore_errors=True)
            shutil.copytree(cache / "modules", package / "node_modules", symlinks=True)
        marker = package / "node_modules/.package-lock.json"
        atomic_json(stamp, {"input": fingerprint, "lock": digest_files(marker.parent, [marker.name]),
                            "output":directory_digest(stamp.parent,excluded=(stamp.name,))})
        event("npm", reused, fingerprint)


def artifact_input(root: Path) -> str:
    # Hash the actual tracked bytes, including build scripts and source carriers.
    # HEAD alone would accept changed local inputs under an old commit identity.
    names = subprocess.check_output(["git", "ls-files", "-z"], cwd=root).decode().split("\0")
    return hashlib.sha256(json.dumps([digest_files(root, [name for name in names if name]),
                                     tool_input()], sort_keys=True).encode()).hexdigest()


def materialize_artifact(root: Path) -> None:
    prep = preparation_root()
    if prep is None:
        raise ValueError("artifact preparation requires an attempt directory")
    fingerprint = artifact_input(root)
    cache = prep / ("artifact-" + fingerprint)
    with lock(prep, "artifact.lock"):
        if cache.is_dir():
            restore_artifact(root, cache, fingerprint, True)
            return
    # Never wait for the heavy slot while holding a snapshot lock: a builder
    # may already hold the heavy slot and need this same artifact lock.
    materialize_uncached_artifact(root, fingerprint)


def restore_artifact(root: Path, cache: Path, fingerprint: str, reused: bool) -> None:
    record = json.loads((cache / "receipt.json").read_text())
    if record.get("input") != fingerprint or record.get("output") != directory_digest(cache / "dist"):
        raise ValueError("prepared artifact bytes or input identity changed")
    destination = root / "web/dist"
    shutil.rmtree(destination, ignore_errors=True)
    shutil.copytree(cache / "dist", destination)
    event("artifact", reused, fingerprint)


@serialized_preparation
def materialize_uncached_artifact(root: Path, fingerprint: str) -> None:
    prep = preparation_root()
    cache = prep / ("artifact-" + fingerprint)
    with lock(prep, "artifact.lock"):
        reused = cache.is_dir()
        if not reused:
            temporary = Path(tempfile.mkdtemp(prefix="artifact-build-", dir=prep))
            try:
                run(["npm", "run", "build", "--silent"], root)
                run(["node", "scripts/build-v3-host-adapters.mjs"], root)
                stage = temporary / "dist"
                for script in ("stage-pr01-effects-ui.mjs", "stage-survey-ui.mjs", "stage-new-shell-ui.mjs"):
                    run(["node", "scripts/" + script, "web/dist", str(stage)], root)
                atomic_json(temporary / "receipt.json", {"input": fingerprint, "output": directory_digest(stage)})
                temporary.rename(cache)
            except BaseException:
                # Keep failed preparation output as diagnostic evidence.
                raise
        restore_artifact(root, cache, fingerprint, reused)


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
    cache = prep / ("artifact-" + fingerprint)
    with lock(prep, "artifact.lock"):
        if cache.exists():
            record = json.loads((cache / "receipt.json").read_text())
            if record.get("input") != fingerprint or record.get("output") != directory_digest(stage):
                raise ValueError("repeated staging produced different artifact bytes")
            return
        if not (stage / "asset-manifest.json").is_file():
            raise ValueError("prepared release artifact lacks its manifest")
        temporary = Path(tempfile.mkdtemp(prefix="artifact-stage-", dir=prep))
        shutil.copytree(stage, temporary / "dist")
        atomic_json(temporary / "receipt.json", {"input": fingerprint, "output": directory_digest(stage)})
        temporary.rename(cache)
        event("artifact", False, fingerprint)


def cleanup_databases(prep: Path, raw: str) -> None:
    parsed = urlsplit(raw)
    database = unquote(parsed.path.lstrip("/"))
    if (parsed.scheme not in {"postgres", "postgresql"} or parsed.hostname not in {"localhost", "127.0.0.1", "::1"}
            or not (database == "aicrm_ci" or database.startswith("aicrm_test_"))):
        raise ValueError("cleanup requires an explicitly configured local synthetic database")
    env = dict(os.environ, PGHOST=parsed.hostname, PGPORT=str(parsed.port or 5432),
               PGUSER=unquote(parsed.username or ""), PGPASSWORD=unquote(parsed.password or ""),
               PGDATABASE=database, PGSSLMODE=parse_qs(parsed.query).get("sslmode", ["prefer"])[0])
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
