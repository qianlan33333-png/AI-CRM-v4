"""Read and validate the candidate-owned AI check list.

The manifest is ordinary source in the candidate Git tree. It contains paths
and test names only; it cannot introduce shell commands or executables.
"""
from __future__ import annotations

import json
from pathlib import Path, PurePosixPath
import re
import subprocess


MANIFEST_PATH = "docs/release-checks.json"
LANES = ("preflight", "backend", "frontend", "browser", "archive-sdk")
LANE_ORDER = {lane: index for index, lane in enumerate(LANES)}
GO_TEST = re.compile(rb"(?m)^\s*func\s+(Test[A-Za-z0-9_]+)\s*\(")
TEST_NAME = re.compile(r"Test[A-Za-z0-9_]+\Z")
CHROMIUM_TEST = re.compile(r"Test[A-Za-z0-9_]*ChromiumJourney\Z")
SLUG = re.compile(r"[a-z0-9]+(?:-[a-z0-9]+)*\Z")
CHECK_FIELDS = {"lane", "path", "package", "test", "postgres", "race", "fresh",
                "concurrency", "requires_postgres", "reason"}


class CheckPlanError(ValueError):
    """The candidate check list is malformed, stale, or unsafe to execute."""


def _git(root: Path, *args: str) -> bytes:
    result = subprocess.run(["git", *args], cwd=root, stdout=subprocess.PIPE,
                            stderr=subprocess.PIPE, check=False)
    if result.returncode:
        detail = result.stderr.decode("utf-8", errors="replace").strip()
        raise CheckPlanError("git " + " ".join(args) + " failed" + (": " + detail if detail else ""))
    return result.stdout


def _safe_repo_path(value: object, label: str = "path") -> str:
    if not isinstance(value, str) or not value or "\\" in value or "\x00" in value:
        raise CheckPlanError(label + " must be a nonempty repository-relative path")
    path = PurePosixPath(value)
    if (path.is_absolute() or value != path.as_posix()
            or any(part in {"", ".", ".."} for part in path.parts)):
        raise CheckPlanError(label + " is not a safe repository-relative path: " + value)
    return value


def _blob(root: Path, head_sha: str, path: str) -> bytes | None:
    """Return a regular file blob from the exact head tree, rejecting symlinks."""
    entries = _git(root, "ls-tree", "-r", "-z", head_sha, "--", path).split(b"\0")
    found = None
    for entry in entries:
        if not entry:
            continue
        try:
            metadata, encoded_path = entry.split(b"\t", 1)
            mode, kind, oid = metadata.decode("ascii").split(" ", 2)
            tree_path = encoded_path.decode("utf-8")
        except (UnicodeDecodeError, ValueError):
            raise CheckPlanError("could not parse Git tree entry for " + path)
        if tree_path == path:
            if kind != "blob" or mode not in {"100644", "100755"}:
                raise CheckPlanError("check path must be a regular file in the candidate tree: " + path)
            found = oid
            break
    if found is None:
        return None
    return _git(root, "cat-file", "blob", found)


def _require_blob(root: Path, head_sha: str, path: str, label: str = "check path") -> bytes:
    content = _blob(root, head_sha, path)
    if content is None:
        raise CheckPlanError(label + " does not exist in the candidate Git tree: " + path)
    return content


def _package_directory(value: object) -> str:
    if not isinstance(value, str) or not value.startswith("./"):
        raise CheckPlanError("backend package must use a local Go package path such as ./internal/example")
    relative = value[2:]
    if relative == ".":
        return "."
    relative = _safe_repo_path(relative, "backend package")
    if any(part in {"...", "*"} for part in PurePosixPath(relative).parts):
        raise CheckPlanError("backend package must name one package, not a pattern")
    return relative


def _package_files(root: Path, head_sha: str, directory: str) -> list[str]:
    files = _git(root, "ls-tree", "-r", "--name-only", head_sha, "--", directory).decode("utf-8").splitlines()
    prefix = "" if directory == "." else directory + "/"
    return sorted(path for path in files if path.endswith(".go")
                  and (directory == "." or path.startswith(prefix)))


def _validate_package(root: Path, head_sha: str, value: object, path: str) -> str:
    package_dir = (PurePosixPath(path).parent.as_posix() if value is None
                   else _package_directory(value))
    package = "./" + ("." if package_dir == "." else package_dir)
    if package_dir != PurePosixPath(path).parent.as_posix():
        raise CheckPlanError("backend package must match the directory containing path: " + path)
    if not _package_files(root, head_sha, package_dir):
        raise CheckPlanError("backend package has no Go source in the candidate tree: " + package)
    return package


def validate_check(root: Path, head_sha: str, raw: object) -> dict:
    """Validate one manifest check and return its canonical, executable form."""
    if not isinstance(raw, dict):
        raise CheckPlanError("each check must be an object")
    extra = set(raw) - CHECK_FIELDS
    if extra:
        raise CheckPlanError("unsupported check fields: " + ", ".join(sorted(extra)))
    if "reason" in raw and (not isinstance(raw["reason"], str) or not raw["reason"].strip()):
        raise CheckPlanError("check reason must explain the direct consumer")
    lane = raw.get("lane")
    if lane not in LANES:
        raise CheckPlanError("check lane must be one of: " + ", ".join(LANES))
    if lane == "backend" and raw.get("path") is None and raw.get("package") is not None:
        package_dir = _package_directory(raw["package"])
        candidates = _package_files(root, head_sha, package_dir)
        requested_test = raw.get("test")
        if requested_test is not None:
            if not isinstance(requested_test, str) or not TEST_NAME.fullmatch(requested_test):
                raise CheckPlanError("test must be a Go Test function name")
            candidates = [candidate for candidate in candidates if candidate.endswith("_test.go")
                          and requested_test.encode("ascii") in GO_TEST.findall(
                              _require_blob(root, head_sha, candidate))]
        if not candidates:
            raise CheckPlanError("backend package has no matching Go source or named test: "
                                 + str(raw["package"]))
        raw = {**raw, "path": candidates[0]}
    path = _safe_repo_path(raw.get("path"))
    content = _require_blob(root, head_sha, path)
    test = raw.get("test")
    if test is not None and (not isinstance(test, str) or not TEST_NAME.fullmatch(test)):
        raise CheckPlanError("test must be a Go Test function name")
    concurrency = raw.get("race", raw.get("concurrency", False))
    requires_postgres = raw.get("postgres", raw.get("requires_postgres", False))
    fresh = raw.get("fresh", False)
    if not isinstance(concurrency, bool) or not isinstance(requires_postgres, bool) or not isinstance(fresh, bool):
        raise CheckPlanError("concurrency, requires_postgres, and fresh must be booleans")
    if lane == "backend":
        if not path.endswith(".go"):
            raise CheckPlanError("backend path must be a Go source or test file")
        package = _validate_package(root, head_sha, raw.get("package"), path)
        if test is not None:
            if not path.endswith("_test.go"):
                raise CheckPlanError("a named backend test must point to its _test.go file")
            defined = {name.decode("ascii") for name in GO_TEST.findall(content)}
            if test not in defined:
                raise CheckPlanError("named backend test is not defined in " + path + ": " + test)
        check = {"lane": lane, "path": path, "package": package}
        if "reason" in raw:
            check["reason"] = raw["reason"]
        if test is not None:
            check["test"] = test
        if concurrency:
            check["race"] = True
        if requires_postgres:
            check["postgres"] = True
        if fresh or requires_postgres:
            check["fresh"] = True
        elif "fresh" in raw:
            check["fresh"] = False
        return check
    if "package" in raw:
        raise CheckPlanError("package is only valid for backend checks")
    if concurrency or requires_postgres or fresh:
        raise CheckPlanError("Go execution flags are only valid for backend checks")
    if lane == "frontend":
        if PurePosixPath(path).suffix not in {".mjs", ".js"}:
            raise CheckPlanError("frontend checks must name a .mjs or .js file")
        if test is not None:
            raise CheckPlanError("frontend checks use a path and do not accept a Go test name")
    elif lane == "browser":
        if not isinstance(test, str) or not CHROMIUM_TEST.fullmatch(test):
            raise CheckPlanError("browser checks require a named ChromiumJourney test")
        if not path.startswith("cmd/aicrm/") or not path.endswith("_test.go"):
            raise CheckPlanError("browser checks must point to a cmd/aicrm Go test file")
        if test.encode("ascii") not in {name for name in GO_TEST.findall(content)}:
            raise CheckPlanError("named browser journey is not defined in " + path + ": " + test)
    elif lane == "preflight":
        if PurePosixPath(path).suffix not in {".py", ".sh"}:
            raise CheckPlanError("preflight checks must name a Python or shell contract")
        if test is not None:
            raise CheckPlanError("preflight checks use a path and do not accept a Go test name")
    elif lane == "archive-sdk":
        if PurePosixPath(path).suffix != ".sh":
            raise CheckPlanError("archive-sdk checks must name a shell script")
        if test is not None:
            raise CheckPlanError("archive-sdk checks use a path and do not accept a Go test name")
    check = {"lane": lane, "path": path}
    if "reason" in raw:
        check["reason"] = raw["reason"]
    if lane == "browser":
        check["test"] = test
    return check


def validate_installed_checks(raw: object) -> list[str]:
    if raw is None:
        return []
    if not isinstance(raw, list):
        raise CheckPlanError("installed_checks must be an array")
    names = []
    for item in raw:
        if isinstance(item, dict) and set(item) == {"name"}:
            item = item["name"]
        if not isinstance(item, str) or not SLUG.fullmatch(item):
            raise CheckPlanError("installed_checks entries must be lowercase short names")
        if item not in names:
            names.append(item)
    return names


def load_manifest(root: Path, base_sha: str, head_sha: str) -> dict | None:
    """Load the candidate's manifest; return None only when it is absent."""
    raw = _blob(root, head_sha, MANIFEST_PATH)
    if raw is None:
        return None
    try:
        manifest = json.loads(raw.decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError) as error:
        raise CheckPlanError("invalid " + MANIFEST_PATH + ": " + str(error))
    if not isinstance(manifest, dict):
        raise CheckPlanError(MANIFEST_PATH + " must contain a JSON object")
    extra = set(manifest) - {"schema", "baseline_sha", "checks", "installed_checks", "build_commands"}
    if extra:
        raise CheckPlanError("unsupported " + MANIFEST_PATH + " fields: " + ", ".join(sorted(extra)))
    if "schema" in manifest and manifest["schema"] != 1:
        raise CheckPlanError("unsupported " + MANIFEST_PATH + " schema")
    baseline = manifest.get("baseline_sha")
    if baseline is not None:
        if not isinstance(baseline, str) or not re.fullmatch(r"[0-9a-f]{40}", baseline):
            raise CheckPlanError("baseline_sha must be a full lowercase commit SHA")
        if baseline != base_sha:
            raise CheckPlanError("stale AI check list: baseline_sha does not match the requested base")
    raw_checks = manifest.get("checks")
    if not isinstance(raw_checks, list) or not raw_checks:
        raise CheckPlanError("checks must be a nonempty array")
    checks = [validate_check(root, head_sha, item) for item in raw_checks]
    deduplicated = []
    seen = set()
    for check in checks:
        key = json.dumps(check, sort_keys=True, separators=(",", ":"))
        if key not in seen:
            seen.add(key)
            deduplicated.append(check)
    build_commands = manifest.get("build_commands", [])
    if not isinstance(build_commands, list) or any(not isinstance(item, str) for item in build_commands):
        raise CheckPlanError("build_commands must be an array of package path strings")
    return {
        "schema": 1,
        "baseline_sha": baseline,
        "checks": deduplicated,
        "installed_checks": validate_installed_checks(manifest.get("installed_checks")),
        "build_commands": build_commands,
    }


def selection(manifest: dict) -> dict:
    checks = manifest["checks"]
    lanes = sorted({check["lane"] for check in checks}, key=LANE_ORDER.__getitem__)
    return {
        "mode": "targeted",
        "lanes": lanes,
        "checks": checks,
        "reason": "local-ai-check-list",
        "reasons": ["local-ai-check-list"],
        "profile": "ai-selected",
        "selection_source": "local-ai-check-list",
        "installed_checks": manifest["installed_checks"],
        "build_commands": manifest["build_commands"],
    }
