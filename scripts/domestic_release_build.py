#!/usr/bin/env python3
"""Build deterministic, complete CRM release trees on the domestic builder."""

from __future__ import annotations

import argparse
import fnmatch
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import time
from dataclasses import dataclass, field
from pathlib import Path, PurePosixPath
from typing import Any, Iterable, Iterator, Sequence


CHECKSUM_NAME = "release-files.sha256"
MANIFEST_NAME = "domestic-release.json"
RELEASE_DIR_NAME = "release"
BUILDER_SCRIPT = "scripts/run-donor-view-consumers.sh"
ARCHIVE_RUNNER_SCRIPT = "scripts/build-wecom-archive-sdk-runner-linux.sh"

# These files are executed outside the application release tree. A changed
# copy is a controller update: the release queue verifies the installed fixed
# copies before advancing, but must not pretend that packaging the app installed
# them.
FIXED_CONTROLLER_FILES = {
    "scripts/domestic_release.py",
    "scripts/domestic_release_build.py",
    "deploy/domestic-promote.py",
}
CI_ONLY_PREFIXES = (".github/", "scripts/ci/")
# Explicit validation-only entrypoints are not part of the installed release.
# Keep this list small and reviewed; unknown scripts remain full-build changes.
CI_ONLY_FILES = {
    ".gitleaksignore",
    "scripts/dev_preflight.py",
    "scripts/check-architecture.py",
    "scripts/check-config-definition-import-boundary.sh",
    "scripts/check-hxc-identity-boundaries.sh",
    "scripts/check-radar-boundaries.sh",
    "scripts/check-retention-registry.py",
    "scripts/check-migration-sequence.py",
    "scripts/check-install-release-contract.sh",
    "scripts/check-release-binaries.py",
}
DEPLOY_SAMPLE_CONFIGS = {
    "deploy/domestic-release.example.json",
    "deploy/domestic-release-role.production.example",
    "deploy/domestic-release-role.staging.example",
}

DOC_SUFFIXES = {".md", ".mdx", ".rst", ".adoc"}
TEST_DIR_NAMES = {"test", "tests", "testdata", "fixtures"}
GO_SOURCE_SUFFIXES = {".go", ".c", ".cc", ".cpp", ".h", ".hh", ".hpp", ".s", ".S"}
GO_PACKAGE_FILES = (
    "GoFiles",
    "CgoFiles",
    "CFiles",
    "CXXFiles",
    "HFiles",
    "MFiles",
    "FFiles",
    "SFiles",
    "SwigFiles",
    "SwigCXXFiles",
    "SysoFiles",
)


class BuildError(RuntimeError):
    pass


@dataclass(frozen=True)
class ReleaseCommand:
    package: str
    binary: str
    cgo: bool = False


@dataclass
class Classification:
    runtime_changed: bool
    frontend_changed: bool
    migrations_changed: bool
    full_build: bool
    graph_paths: list[str]
    frontend_build_paths: list[str]
    changed_paths: list[str]
    full_build_reason: str | None = None
    controller_files: list[str] = field(default_factory=list)
    package_overlays: list[str] = field(default_factory=list)

    @property
    def build_mode(self) -> str:
        if not self.runtime_changed:
            return "controller_only" if self.controller_files else "none"
        if self.full_build:
            return "full"
        if self.frontend_build_paths:
            return "frontend_incremental"
        if self.graph_paths:
            return "go_incremental"
        return "manifest_only"

    def as_json(self, base_sha: str, target_sha: str, target_tree: str, go_commands: list[str]) -> dict[str, Any]:
        result: dict[str, Any] = {
            "base_sha": base_sha,
            "target_sha": target_sha,
            "target_tree": target_tree,
            "runtime_changed": self.runtime_changed,
            "frontend_changed": self.frontend_changed,
            "full_build": self.full_build,
            "go_commands": go_commands,
            "migrations_changed": self.migrations_changed,
            "changed_paths": self.changed_paths,
            "build_mode": self.build_mode,
            "controller_files": self.controller_files,
            "package_overlays": self.package_overlays,
        }
        if self.full_build_reason:
            result["full_build_reason"] = self.full_build_reason
        return result


def _run(command: Sequence[str], *, cwd: Path | None = None, env: dict[str, str] | None = None,
         capture: bool = False) -> subprocess.CompletedProcess[str]:
    try:
        return subprocess.run(
            list(command), cwd=str(cwd) if cwd else None, env=env,
            text=True, check=True, stdout=subprocess.PIPE if capture else None,
            stderr=subprocess.PIPE if capture else None,
        )
    except subprocess.CalledProcessError as exc:
        detail = (exc.stderr or exc.stdout or "").strip()
        command_text = " ".join(command)
        raise BuildError(f"command failed ({command_text})" + (f": {detail}" if detail else "")) from exc
    except FileNotFoundError as exc:
        raise BuildError(f"required command not found: {command[0]}") from exc


def _git(repo: Path, *args: str, capture: bool = True) -> str:
    result = _run(["git", "-C", str(repo), *args], capture=capture)
    return result.stdout or ""


def _resolve_commit(repo: Path, value: str) -> str:
    return _git(repo, "rev-parse", "--verify", f"{value}^{{commit}}").strip()


def _require_ancestor(repo: Path, ancestor_sha: str, target_sha: str, label: str) -> None:
    result = subprocess.run(
        ["git", "-C", str(repo), "merge-base", "--is-ancestor", ancestor_sha, target_sha],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.PIPE,
        text=True,
        check=False,
    )
    if result.returncode != 0:
        raise BuildError(f"{label} must be an ancestor of the target commit")


def _tree_sha(repo: Path, target_sha: str) -> str:
    return _git(repo, "rev-parse", f"{target_sha}^{{tree}}").strip()


def _changed_paths(repo: Path, base_sha: str, target_sha: str) -> list[str]:
    raw = _git(repo, "diff", "--no-renames", "--name-only", "-z", base_sha, target_sha)
    paths = [item.decode("utf-8", "surrogateescape") for item in raw.encode("utf-8", "surrogateescape").split(b"\0") if item]
    return sorted(set(paths))


def _deleted_paths(repo: Path, base_sha: str, target_sha: str) -> list[str]:
    raw = _git(repo, "diff", "--diff-filter=D", "--no-renames", "--name-only", "-z", base_sha, target_sha)
    paths = [item.decode("utf-8", "surrogateescape") for item in raw.encode("utf-8", "surrogateescape").split(b"\0") if item]
    return sorted(set(paths))


def _validate_payload_deletions(repo: Path, base_sha: str, target_sha: str) -> None:
    unsafe_deletions = [
        path for path in _deleted_paths(repo, base_sha, target_sha)
        if path.startswith("migrations/") or path == "components/excel-batches/batches.py"
    ]
    if unsafe_deletions:
        raise BuildError(f"release payload deletion requires an explicit removal plan: {unsafe_deletions}")


def _is_test_or_document(path: str) -> bool:
    parsed = PurePosixPath(path)
    name = parsed.name.lower()
    if path.startswith("docs/") or parsed.suffix.lower() in DOC_SUFFIXES:
        return True
    if parsed.parent.as_posix() == "cmd/aicrm" and name.endswith("_chromium_journey.mjs"):
        return True
    if any(part.lower() in TEST_DIR_NAMES for part in parsed.parts):
        return True
    if name.endswith("_test.go") or name.startswith(("test_", "test-")):
        return True
    if ".test." in name or ".spec." in name:
        return True
    return False


def _is_lockfile(path: str) -> bool:
    name = PurePosixPath(path).name.lower()
    return (
        name in {"package-lock.json", "npm-shrinkwrap.json", "yarn.lock", "pnpm-lock.yaml", "bun.lockb", "bun.lock"}
        or name.endswith(".lock")
    )


def _is_frontend_asset(path: str) -> bool:
    normalized = PurePosixPath(path)
    suffix = normalized.suffix.lower()
    if path.startswith("web/"):
        return True
    if not path.startswith("internal/"):
        return False
    if suffix in {".html", ".htm", ".css", ".js", ".mjs", ".jsx", ".ts", ".tsx", ".svg", ".png", ".jpg", ".jpeg", ".gif", ".ico", ".woff", ".woff2"}:
        return True
    return suffix == ".json" and "static" in normalized.parts


def classify_paths(paths: Iterable[str]) -> Classification:
    """Classify release impact without consulting local or external state."""
    changed = sorted(set(paths))
    runtime_changed = False
    frontend_changed = False
    migrations_changed = False
    full_reason: str | None = None
    graph_paths: list[str] = []
    frontend_build_paths: list[str] = []
    controller_files: list[str] = []
    package_overlays: set[str] = set()

    for path in changed:
        if _is_test_or_document(path):
            continue

        normalized = path.strip("/")
        name = PurePosixPath(normalized).name.lower()

        if normalized.startswith(CI_ONLY_PREFIXES) or normalized in CI_ONLY_FILES:
            continue

        if normalized in FIXED_CONTROLLER_FILES:
            controller_files.append(normalized)
            continue

        if normalized in DEPLOY_SAMPLE_CONFIGS:
            # This is operator documentation/config illustration, not the
            # installed host config and not part of the application package.
            continue

        runtime_changed = True

        if normalized.startswith("migrations/"):
            if PurePosixPath(normalized).suffix.lower() == ".sql":
                migrations_changed = True
                package_overlays.add("migrations")
            else:
                full_reason = full_reason or "unknown_migration_payload"
            continue

        if normalized.startswith("deploy/"):
            # Except for the fixed installer helper and example config above,
            # deploy files can change host units or install behavior. Require
            # the full package path so the installer applies and verifies them.
            full_reason = full_reason or "deploy_payload_change"
            continue

        if normalized.startswith("components/excel-batches/"):
            if name == "batches.py":
                package_overlays.add("components/excel-batches")
            elif name in {"requirements.txt", "aicrm-excel-batches.service"}:
                full_reason = full_reason or "component_dependency_or_service_change"
            else:
                full_reason = full_reason or "unknown_component_payload"
            continue

        if normalized.startswith(("internal/platform/", "internal/externaleffects/")):
            full_reason = full_reason or "shared_runtime_infrastructure"
            continue

        if (
            normalized.startswith("scripts/")
            or normalized in {"Makefile", "go.mod", "go.sum", "go.work", "go.work.sum", ".npmrc"}
            or name in {"package.json", "package-lock.json", "npm-shrinkwrap.json", "yarn.lock", "pnpm-lock.yaml", "bun.lock", "bun.lockb"}
            or _is_lockfile(normalized)
            or name.startswith(("dockerfile", "containerfile"))
        ):
            full_reason = full_reason or ("lockfile_or_dependencies" if _is_lockfile(normalized) or name == "package.json" else "build_or_deploy_infrastructure")
            if normalized.startswith("web/") or name == "package.json" or name in {"package-lock.json", "npm-shrinkwrap.json", "yarn.lock", "pnpm-lock.yaml", "bun.lock", "bun.lockb"}:
                frontend_changed = True
            continue

        if normalized.startswith("web/donor-sources/"):
            # Materialized donor inputs can feed both npm output and Go embeds;
            # until their source-to-view mapping is explicit, rebuild safely.
            frontend_changed = True
            full_reason = full_reason or "donor_source_view_change"
            continue

        if normalized.startswith("web/"):
            frontend_changed = True
            frontend_build_paths.append(path)
            continue

        if _is_frontend_asset(normalized):
            frontend_changed = True
            graph_paths.append(path)
            continue

        if PurePosixPath(normalized).suffix in GO_SOURCE_SUFFIXES:
            graph_paths.append(path)
            continue

        if normalized.startswith(("internal/", "cmd/", "pkg/")):
            # Non-Go files in Go packages may be //go:embed inputs. The package
            # graph decides whether a changed file has a safe incremental path.
            graph_paths.append(path)
            continue

        full_reason = full_reason or "unknown_runtime_path"

    return Classification(
        runtime_changed=runtime_changed,
        frontend_changed=frontend_changed,
        migrations_changed=migrations_changed,
        full_build=full_reason is not None,
        graph_paths=graph_paths,
        frontend_build_paths=frontend_build_paths,
        changed_paths=changed,
        full_build_reason=full_reason,
        controller_files=sorted(controller_files),
        package_overlays=sorted(package_overlays),
    )


def _git_file(repo: Path, target_sha: str, path: str) -> str:
    result = _run(["git", "-C", str(repo), "show", f"{target_sha}:{path}"], capture=True)
    return result.stdout or ""


def _release_commands(repo: Path, target_sha: str) -> list[ReleaseCommand]:
    builder = _git_file(repo, target_sha, BUILDER_SCRIPT)
    archive_runner = _git_file(repo, target_sha, ARCHIVE_RUNNER_SCRIPT)
    commands: list[ReleaseCommand] = []
    seen: set[str] = set()
    pattern = re.compile(r"\bgo\s+build\b.*?\s-o\s+release/bin/([^\s]+)\s+(\./cmd/[^\s]+)")
    for line in builder.splitlines():
        match = pattern.search(line)
        if not match:
            continue
        binary, package = match.groups()
        if package not in seen:
            commands.append(ReleaseCommand(package=package, binary=binary))
            seen.add(package)

    archive_package = "./cmd/wecom-archive-sdk-runner"
    if archive_package in archive_runner and archive_package not in seen:
        commands.append(ReleaseCommand(package=archive_package, binary="wecom-archive-sdk-runner", cgo=True))
    if not commands:
        raise BuildError("could not discover release Go commands from the target build scripts")
    return commands


@dataclass
class GoPackage:
    import_path: str
    rel_dir: str
    deps: list[str]
    files: set[str]
    embed_files: set[str]
    embed_patterns: set[str] = field(default_factory=set)


def _decode_json_stream(value: str) -> Iterator[dict[str, Any]]:
    decoder = json.JSONDecoder()
    index = 0
    while index < len(value):
        while index < len(value) and value[index].isspace():
            index += 1
        if index >= len(value):
            break
        item, index = decoder.raw_decode(value, index)
        yield item


def _materialize_source_views(root: Path) -> None:
    script = root / "scripts/prepare-donor-source-views.mjs"
    if script.exists():
        _run(["node", str(script)], cwd=root)


def _go_packages(root: Path, commands: list[ReleaseCommand]) -> tuple[dict[str, GoPackage], dict[str, set[str]]]:
    _materialize_source_views(root)
    env = os.environ.copy()
    env.update({"GOOS": "linux", "GOARCH": "amd64", "GOWORK": "off", "CGO_ENABLED": "1"})
    output = _run(["go", "list", "-deps", "-json", *[item.package for item in commands]], cwd=root, env=env, capture=True).stdout or ""
    raw_packages = list(_decode_json_stream(output))
    package_map: dict[str, GoPackage] = {}
    for item in raw_packages:
        if item.get("Error"):
            raise BuildError("go list reported a package error")
        import_path = item.get("ImportPath")
        raw_dir = item.get("Dir")
        if not import_path or not raw_dir:
            continue
        directory = Path(raw_dir).resolve()
        try:
            rel_dir = directory.relative_to(root.resolve()).as_posix()
        except ValueError:
            continue
        files: set[str] = set()
        for key in GO_PACKAGE_FILES:
            for file_name in item.get(key, []):
                files.add((PurePosixPath(rel_dir) / file_name).as_posix())
        embed_files = {
            (PurePosixPath(rel_dir) / file_name).as_posix()
            for file_name in item.get("EmbedFiles", [])
        }
        embed_patterns = set(item.get("EmbedPatterns", []))
        package_map[import_path] = GoPackage(
            import_path=import_path,
            rel_dir=rel_dir,
            deps=list(item.get("Deps", [])),
            files=files,
            embed_files=embed_files,
            embed_patterns=embed_patterns,
        )

    command_graphs: dict[str, set[str]] = {}
    for command in commands:
        command_dir = command.package.removeprefix("./")
        root_packages = [
            pkg.import_path for pkg in package_map.values()
            if pkg.rel_dir == command_dir
        ]
        if len(root_packages) != 1:
            raise BuildError(f"could not resolve release command package {command.package}")
        root_package = root_packages[0]
        reachable: set[str] = set()
        pending = [root_package]
        while pending:
            current = pending.pop()
            if current in reachable:
                continue
            reachable.add(current)
            package = package_map.get(current)
            if package:
                pending.extend(dep for dep in package.deps if dep not in reachable)
        command_graphs[command.package] = reachable

    return package_map, command_graphs


def affected_go_commands(
    changed_paths: Iterable[str],
    commands: list[ReleaseCommand],
    package_map: dict[str, GoPackage],
    command_graphs: dict[str, set[str]],
    *,
    previous_package_map: dict[str, GoPackage] | None = None,
    previous_command_graphs: dict[str, set[str]] | None = None,
) -> list[str]:
    """Return commands affected in either side of a source range.

    The head graph identifies new/changed files and current dependencies. The
    base graph is equally important for deleted files: once a source or embed
    input has been removed, the head graph cannot tell which old binaries used
    to depend on it.
    """
    package_maps = [package_map]
    if previous_package_map is not None:
        package_maps.append(previous_package_map)
    graph_versions = [command_graphs]
    if previous_command_graphs is not None:
        graph_versions.append(previous_command_graphs)

    package_by_dir: dict[str, set[str]] = {}
    for packages in package_maps:
        for import_path, package in packages.items():
            package_by_dir.setdefault(package.rel_dir, set()).add(import_path)

    changed_packages: set[str] = set()
    for path in changed_paths:
        if _is_test_or_document(path):
            continue
        normalized = PurePosixPath(path).as_posix()
        matches: set[str] = set()
        parent = PurePosixPath(normalized).parent.as_posix()
        if PurePosixPath(normalized).suffix in GO_SOURCE_SUFFIXES:
            matches.update(package_by_dir.get(parent, set()))
        for packages in package_maps:
            for import_path, package in packages.items():
                if normalized in package.embed_files or normalized in package.files:
                    matches.add(import_path)
                    continue
                package_prefix = package.rel_dir.rstrip("/") + "/"
                if normalized.startswith(package_prefix):
                    relative = normalized[len(package_prefix):]
                    for pattern in package.embed_patterns:
                        if _embed_pattern_matches(relative, pattern):
                            matches.add(import_path)
                            break
        if not matches:
            raise BuildError(f"changed Go or package asset is outside the release dependency graph: {path}")
        changed_packages.update(matches)

    result = [
        command.package
        for command in commands
        if any(graphs.get(command.package, set()) & changed_packages for graphs in graph_versions)
    ]
    return result


def _embed_pattern_matches(relative: str, pattern: str) -> bool:
    normalized = pattern.removeprefix("./")
    if relative == normalized or fnmatch.fnmatchcase(relative, normalized):
        return True
    if not any(character in normalized for character in "*?["):
        return relative.startswith(normalized.rstrip("/") + "/")
    return False


class TemporaryWorktree:
    def __init__(self, repo: Path, target_sha: str, temp_parent: Path | None = None):
        self.repo = repo
        self.target_sha = target_sha
        self.temp_parent = temp_parent
        self.parent: Path | None = None
        self.path: Path | None = None

    def __enter__(self) -> Path:
        self.parent = Path(tempfile.mkdtemp(prefix="domestic-release-source-", dir=self.temp_parent))
        self.path = self.parent / "source"
        try:
            _git(self.repo, "worktree", "add", "--detach", str(self.path), self.target_sha, capture=False)
        except BuildError:
            try:
                _git(self.repo, "worktree", "remove", "--force", str(self.path), capture=False)
            except BuildError:
                pass
            shutil.rmtree(self.parent, ignore_errors=True)
            raise
        return self.path

    def __exit__(self, exc_type: Any, exc: Any, traceback: Any) -> None:
        if self.path and self.path.exists():
            try:
                _git(self.repo, "worktree", "remove", "--force", str(self.path), capture=False)
            finally:
                if self.parent:
                    shutil.rmtree(self.parent, ignore_errors=True)


def _make_plan(repo: Path, base_sha: str, target_sha: str, target_root: Path | None = None) -> dict[str, Any]:
    changed = _changed_paths(repo, base_sha, target_sha)
    tree = _tree_sha(repo, target_sha)
    classification = classify_paths(changed)
    if not classification.runtime_changed:
        return classification.as_json(base_sha, target_sha, tree, [])
    try:
        commands = _release_commands(repo, target_sha)
    except BuildError:
        # A malformed or missing release builder is itself a full-build blocker.
        classification.full_build = True
        classification.runtime_changed = True
        classification.full_build_reason = classification.full_build_reason or "release_command_inventory_unavailable"
        return classification.as_json(base_sha, target_sha, tree, [])

    if classification.full_build:
        return classification.as_json(base_sha, target_sha, tree, [item.package for item in commands])

    if classification.graph_paths:
        try:
            if target_root is not None:
                package_map, command_graphs = _go_packages(target_root, commands)
                with TemporaryWorktree(repo, base_sha) as baseline_source:
                    baseline_package_map, baseline_graphs = _go_packages(baseline_source, commands)
            else:
                with TemporaryWorktree(repo, target_sha) as source:
                    package_map, command_graphs = _go_packages(source, commands)
                with TemporaryWorktree(repo, base_sha) as baseline_source:
                    baseline_package_map, baseline_graphs = _go_packages(baseline_source, commands)
            affected = affected_go_commands(
                classification.graph_paths,
                commands,
                package_map,
                command_graphs,
                previous_package_map=baseline_package_map,
                previous_command_graphs=baseline_graphs,
            )
            return classification.as_json(base_sha, target_sha, tree, affected)
        except (BuildError, json.JSONDecodeError):
            classification.full_build = True
            classification.full_build_reason = "dependency_graph_unavailable"
            return classification.as_json(base_sha, target_sha, tree, [item.package for item in commands])

    return classification.as_json(base_sha, target_sha, tree, [])


def plan(
    repo_path: str | Path,
    base_value: str,
    target_value: str,
    validation_scope_base_value: str | None = None,
) -> dict[str, Any]:
    repo = Path(repo_path).resolve()
    base_sha = _resolve_commit(repo, base_value)
    target_sha = _resolve_commit(repo, target_value)
    validation_scope_base_sha = _resolve_commit(repo, validation_scope_base_value or base_value)
    _require_ancestor(repo, validation_scope_base_sha, target_sha, "validation scope base")
    _validate_payload_deletions(repo, base_sha, target_sha)
    result = _make_plan(repo, base_sha, target_sha)
    # This range explains package and test scope. The release controller has
    # only verified the exact GitHub check; it has not fetched an independently
    # bound CI receipt, so this must not be reported as the actual CI baseline.
    result["validation_scope_base_sha"] = validation_scope_base_sha
    result["validation_scope_base_tree"] = _tree_sha(repo, validation_scope_base_sha)
    result["validation_scope_changed_paths"] = _changed_paths(repo, validation_scope_base_sha, target_sha)
    result["actual_ci_baseline_verified"] = False
    return result


def classify(repo_path: str | Path, base_value: str, target_value: str) -> dict[str, Any]:
    """Cheap impact check with no checkout or execution of target source."""
    repo = Path(repo_path).resolve()
    base_sha = _resolve_commit(repo, base_value)
    target_sha = _resolve_commit(repo, target_value)
    result = classify_paths(_changed_paths(repo, base_sha, target_sha))
    return {
        "runtime_changed": result.runtime_changed,
        "full_build": result.full_build,
        "build_mode": result.build_mode,
        "migrations_changed": result.migrations_changed,
        "controller_files": result.controller_files,
        "package_overlays": result.package_overlays,
        "changed_paths": result.changed_paths,
    }


def _iter_payload_files(release: Path) -> Iterator[Path]:
    for root, dirs, files in os.walk(release, followlinks=False):
        root_path = Path(root)
        for name in list(dirs):
            path = root_path / name
            if path.is_symlink():
                raise BuildError(f"symlink is not allowed in release payload: {path.relative_to(release)}")
        for name in files:
            path = root_path / name
            relative = path.relative_to(release).as_posix()
            if name == "release.env" or relative == CHECKSUM_NAME:
                continue
            if path.is_symlink() or not path.is_file():
                raise BuildError(f"release payload contains a non-regular file: {relative}")
            if "\n" in relative or "\r" in relative or "\\" in relative:
                raise BuildError(f"release payload path cannot be represented safely in sha256sum: {relative!r}")
            yield path


def _file_sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def write_release_inventory(release: Path) -> str:
    checksum_path = release / CHECKSUM_NAME
    entries: list[str] = []
    for path in sorted(_iter_payload_files(release), key=lambda item: item.relative_to(release).as_posix()):
        relative = path.relative_to(release).as_posix()
        entries.append(f"{_file_sha256(path)}  {relative}\n")
    content = "".join(entries).encode("utf-8")
    checksum_path.write_bytes(content)
    return hashlib.sha256(content).hexdigest()


def verify_release_inventory(release: Path, *, allow_release_env: bool = False) -> str:
    checksum_path = release / CHECKSUM_NAME
    if not checksum_path.is_file() or checksum_path.is_symlink():
        raise BuildError(f"base release lacks {CHECKSUM_NAME}")
    content = checksum_path.read_bytes()
    try:
        lines = content.decode("utf-8").splitlines()
    except UnicodeDecodeError as exc:
        raise BuildError("release checksum inventory is not UTF-8") from exc
    listed: dict[str, str] = {}
    for line in lines:
        match = re.fullmatch(r"([0-9a-f]{64})  (.+)", line)
        if not match:
            raise BuildError("release checksum inventory has an invalid line")
        digest, relative = match.groups()
        if relative in listed:
            raise BuildError(f"release checksum inventory repeats {relative}")
        if relative == CHECKSUM_NAME:
            raise BuildError("release checksum inventory must exclude itself")
        if PurePosixPath(relative).name == "release.env":
            if allow_release_env:
                continue
            raise BuildError("release checksum inventory must exclude release.env")
        listed[relative] = digest

    actual: dict[str, str] = {}
    for path in _iter_payload_files(release):
        relative = path.relative_to(release).as_posix()
        actual[relative] = _file_sha256(path)
    if set(listed) != set(actual):
        missing = sorted(set(actual) - set(listed))
        extra = sorted(set(listed) - set(actual))
        raise BuildError(f"release checksum inventory file set differs (missing={missing}, extra={extra})")
    mismatches = sorted(path for path, digest in listed.items() if actual[path] != digest)
    if mismatches:
        raise BuildError(f"release checksum mismatch: {mismatches}")
    return hashlib.sha256(content).hexdigest()


def _remove_release_env(release: Path) -> None:
    for root, dirs, files in os.walk(release, followlinks=False):
        root_path = Path(root)
        for name in list(dirs):
            candidate = root_path / name
            if candidate.is_symlink():
                raise BuildError(f"symlink is not allowed in release payload: {candidate.relative_to(release)}")
        for name in files:
            if name == "release.env":
                (root_path / name).unlink()


def _copy_release(source: Path, destination: Path) -> None:
    source = source.resolve(strict=True)
    verify_release_inventory(source, allow_release_env=True)
    shutil.copytree(source, destination, symlinks=False, copy_function=shutil.copy2,
                    ignore=shutil.ignore_patterns("release.env"))


def _overlay_source_directory(source_root: Path, release_root: Path, relative: str) -> None:
    """Replace one packaged source directory without following links/special files."""
    source = source_root / relative
    destination = release_root / relative
    if destination.is_symlink():
        raise BuildError(f"release overlay destination is a symlink: {relative}")
    if destination.exists():
        if destination.is_dir():
            shutil.rmtree(destination)
        else:
            destination.unlink()
    if not source.exists():
        return
    if source.is_symlink() or not source.is_dir():
        raise BuildError(f"release overlay source is not a regular directory: {relative}")
    for path in source.rglob("*"):
        if path.is_symlink() or not (path.is_file() or path.is_dir()):
            raise BuildError(f"release overlay contains an unsafe path: {path.relative_to(source_root)}")
    destination.parent.mkdir(parents=True, exist_ok=True)
    shutil.copytree(source, destination, symlinks=False, copy_function=shutil.copy2)


def _build_frontend(root: Path) -> None:
    _run(["npm", "ci", "--no-audit", "--no-fund"], cwd=root)
    _run(["npm", "ci", "--prefix", "web/v3", "--no-audit", "--no-fund"], cwd=root)
    _materialize_source_views(root)
    _run(["node", "scripts/generate-ai-assistant-client.mjs"], cwd=root)
    _run(["npm", "run", "build"], cwd=root)
    _run(["node", "scripts/build-v3-host-adapters.mjs"], cwd=root)

    release_dist = root / "release/web/dist"
    if release_dist.exists():
        shutil.rmtree(release_dist)
    release_dist.mkdir(parents=True, exist_ok=True)
    stages = [
        ["node", "scripts/stage-pr01-effects-ui.mjs", "web/dist", "release/web/dist"],
        ["node", "scripts/test-stage-pr01-effects-ui.mjs"],
        ["node", "scripts/test-groupops-history-release.mjs", "web/dist", "release/web/dist"],
        ["node", "scripts/stage-survey-ui.mjs", "web/dist", "release/web/dist"],
        ["node", "scripts/test-stage-survey-ui.mjs", "web/dist", "release/web/dist"],
        ["node", "scripts/stage-new-shell-ui.mjs", "web/dist", "release/web/dist"],
        ["node", "scripts/test-stage-new-shell-ui.mjs", "web/dist", "release/web/dist"],
    ]
    for command in stages:
        _run(command, cwd=root)


def _build_go_commands(root: Path, package_names: list[str], commands: list[ReleaseCommand]) -> None:
    by_package = {item.package: item for item in commands}
    env = os.environ.copy()
    env.update({"GOOS": "linux", "GOARCH": "amd64", "GOWORK": "off"})
    for package in package_names:
        command = by_package[package]
        output = root / "release/bin" / command.binary
        output.parent.mkdir(parents=True, exist_ok=True)
        if command.cgo:
            _run(["bash", ARCHIVE_RUNNER_SCRIPT, str(output)], cwd=root, env=env)
        else:
            _run(["go", "build", "-trimpath", "-ldflags", "-s -w", "-o", str(output), package], cwd=root, env=env)


def _write_manifest(path: Path, value: dict[str, Any]) -> None:
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def build(repo_path: str | Path, base_value: str, target_value: str,
          base_release_value: str, out_value: str | Path,
          validation_scope_base_value: str | None = None) -> dict[str, Any]:
    build_started = time.monotonic()
    repo = Path(repo_path).resolve()
    base_sha = _resolve_commit(repo, base_value)
    target_sha = _resolve_commit(repo, target_value)
    validation_scope_base_sha = _resolve_commit(repo, validation_scope_base_value or base_value)
    _require_ancestor(repo, validation_scope_base_sha, target_sha, "validation scope base")
    _validate_payload_deletions(repo, base_sha, target_sha)
    output = Path(out_value).resolve()
    try:
        output.relative_to(repo)
    except ValueError:
        pass
    else:
        raise BuildError("--out must be outside the source repository")

    base_release: Path | None
    if base_release_value == "none":
        base_release = None
    else:
        base_release = Path(base_release_value).resolve(strict=True)
        if not base_release.is_dir():
            raise BuildError("--base-release must be a release directory or 'none'")
        verify_release_inventory(base_release, allow_release_env=True)

    output.parent.mkdir(parents=True, exist_ok=True)
    if output.is_symlink():
        raise BuildError(f"--out must not be a symlink: {output}")
    if output.exists():
        if not output.is_dir() or any(output.iterdir()):
            raise BuildError(f"--out already exists and is not empty: {output}")
        output.rmdir()
    staged_output = Path(tempfile.mkdtemp(prefix=f".{output.name}.building-", dir=output.parent))
    completed = False
    try:
        # A complete release can exceed a small /tmp tmpfs. Keep the linker
        # output on the persistent disk that holds --out.
        with TemporaryWorktree(repo, target_sha, temp_parent=output.parent) as source:
            plan_value = _make_plan(repo, base_sha, target_sha, target_root=source)
            command_inventory = _release_commands(repo, target_sha)
            command_by_package = {item.package: item for item in command_inventory}
            actual_full_build = bool(plan_value["full_build"] or base_release is None)
            go_commands = list(plan_value["go_commands"])
            if actual_full_build:
                go_commands = [item.package for item in command_inventory]

            release_in_source = source / RELEASE_DIR_NAME
            if release_in_source.exists():
                shutil.rmtree(release_in_source)

            if actual_full_build:
                env = os.environ.copy()
                env.update({"GOOS": "linux", "GOARCH": "amd64", "GITHUB_SHA": target_sha})
                _run(["bash", BUILDER_SCRIPT, "release-fast"], cwd=source, env=env)
            else:
                assert base_release is not None
                _copy_release(base_release, release_in_source)
                if plan_value["frontend_changed"] and any(
                    path.startswith("web/") for path in plan_value["changed_paths"]
                    if not _is_test_or_document(path)
                ):
                    _build_frontend(source)
                if go_commands:
                    _build_go_commands(source, go_commands, command_inventory)

                for relative in plan_value.get("package_overlays", []):
                    _overlay_source_directory(source, release_in_source, relative)

            if not release_in_source.is_dir():
                raise BuildError("release builder completed without creating release/")
            missing_commands = [
                item.binary for item in command_inventory
                if not (release_in_source / "bin" / item.binary).is_file()
                or (release_in_source / "bin" / item.binary).is_symlink()
            ]
            if missing_commands:
                raise BuildError(f"release is missing expected programs: {missing_commands}")
            _remove_release_env(release_in_source)
            release_out = staged_output / RELEASE_DIR_NAME
            shutil.copytree(release_in_source, release_out, symlinks=True, copy_function=shutil.copy2)
            release_files_sha = write_release_inventory(release_out)
            verify_release_inventory(release_out)

            manifest = {
                "schema_version": 1,
                "source_sha": target_sha,
                "base_sha": base_sha,
                "source_tree": _tree_sha(repo, target_sha),
                # Package reuse binds to the last actually deployed source.
                # This controller-derived source range is only a scope hint;
                # it is not an exact CI baseline receipt.
                "validation_scope_base_sha": validation_scope_base_sha,
                "validation_scope_base_tree": _tree_sha(repo, validation_scope_base_sha),
                "validation_scope_changed_paths": _changed_paths(repo, validation_scope_base_sha, target_sha),
                "actual_ci_baseline_verified": False,
                "runtime_changed": bool(plan_value["runtime_changed"]),
                "frontend_changed": bool(plan_value["frontend_changed"]),
                "full_build": actual_full_build,
                "build_mode": "full" if actual_full_build else plan_value.get("build_mode", "manifest_only"),
                "changed_paths": plan_value["changed_paths"],
                "go_commands": go_commands,
                "migrations_changed": bool(plan_value["migrations_changed"]),
                "controller_files": plan_value.get("controller_files", []),
                "package_overlays": plan_value.get("package_overlays", []),
                "release_files_sha256": release_files_sha,
                "release_path": RELEASE_DIR_NAME,
                "phase_timings_seconds": {"build": round(time.monotonic() - build_started, 1)},
            }
            _write_manifest(staged_output / MANIFEST_NAME, manifest)
        if output.exists():
            raise BuildError(f"--out appeared while building: {output}")
        os.replace(staged_output, output)
        completed = True
        return manifest
    finally:
        if not completed:
            shutil.rmtree(staged_output, ignore_errors=True)


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    subparsers = parser.add_subparsers(dest="action", required=True)
    classify_parser = subparsers.add_parser("classify", help="detect runtime changes without executing target source")
    classify_parser.add_argument("--repo", required=True)
    classify_parser.add_argument("--base", required=True)
    classify_parser.add_argument("--target", required=True)
    plan_parser = subparsers.add_parser("plan", help="classify a source commit range")
    plan_parser.add_argument("--repo", required=True)
    plan_parser.add_argument("--base", required=True)
    plan_parser.add_argument("--validation-scope-base")
    plan_parser.add_argument("--target", required=True)
    build_parser = subparsers.add_parser("build", help="create a complete release tree")
    build_parser.add_argument("--repo", required=True)
    build_parser.add_argument("--base", required=True)
    build_parser.add_argument("--validation-scope-base")
    build_parser.add_argument("--target", required=True)
    build_parser.add_argument("--base-release", required=True, help="existing release directory, or 'none' for the first build")
    build_parser.add_argument("--out", required=True)
    return parser


def main(argv: Sequence[str] | None = None) -> int:
    args = _parser().parse_args(argv)
    try:
        if args.action == "classify":
            result = classify(args.repo, args.base, args.target)
        elif args.action == "plan":
            result = plan(args.repo, args.base, args.target, args.validation_scope_base)
        else:
            result = build(args.repo, args.base, args.target, args.base_release, args.out, args.validation_scope_base)
        print(json.dumps(result, ensure_ascii=False, sort_keys=True))
        return 0
    except (BuildError, OSError, ValueError) as exc:
        print(f"domestic_release_build: {exc}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
