"""Select a small set of direct checks when a candidate has no AI check list."""
from __future__ import annotations

import json
from pathlib import Path, PurePosixPath
import re
import subprocess

import ai_check_plan


TEST = re.compile(rb"(?m)^\s*func\s+(Test[A-Za-z0-9_]+)\s*\(")
HUNK = re.compile(rb"^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@", re.M)


def _git(root: Path, *args: str) -> bytes:
    result = subprocess.run(["git", *args], cwd=root, stdout=subprocess.PIPE,
                            stderr=subprocess.PIPE, check=False)
    if result.returncode:
        detail = result.stderr.decode("utf-8", errors="replace").strip()
        raise ValueError("git " + " ".join(args) + " failed" + (": " + detail if detail else ""))
    return result.stdout


def _blob(root: Path, head: str, path: str) -> bytes | None:
    result = subprocess.run(["git", "show", f"{head}:{path}"], cwd=root,
                            stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, check=False)
    return result.stdout if result.returncode == 0 else None


def changed_go_tests(root: Path, base: str, head: str, path: str) -> list[str]:
    """Return tests whose body intersects a changed hunk in the candidate file."""
    content = _blob(root, head, path)
    if content is None:
        return []
    lines = content.splitlines()
    starts = [(index, match.group(1).decode("ascii"))
              for index, line in enumerate(lines, 1) if (match := TEST.match(line))]
    if not starts:
        return []
    diff = _git(root, "diff", "--no-ext-diff", "--unified=0", base, head, "--", path)
    changed = set()
    for match in HUNK.finditer(diff):
        first, count = int(match.group(1)), int(match.group(2) or b"1")
        changed.update(range(first, first + max(1, count)))
    names = []
    for index, (first, name) in enumerate(starts):
        end = starts[index + 1][0] if index + 1 < len(starts) else len(lines) + 1
        if any(first <= line < end for line in changed):
            names.append(name)
    return names


def _module_path(root: Path, head: str) -> str:
    content = _blob(root, head, "go.mod")
    if content is None:
        return ""
    match = re.search(rb"(?m)^module[ \t]+([^\s]+)", content)
    return match.group(1).decode("utf-8") if match else ""


def _direct_go_importers(root: Path, head: str, module: str, directory: str) -> list[str]:
    """Return one-hop Go importers of a changed package, with no transitive walk."""
    if not module or directory == ".":
        import_path = module
    else:
        import_path = module + "/" + directory
    if not import_path:
        return []
    result = subprocess.run(["git", "grep", "-l", "-F", '"' + import_path + '"', head, "--", "*.go"],
                            cwd=root, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False)
    if result.returncode not in {0, 1}:
        detail = result.stderr.decode("utf-8", errors="replace").strip()
        raise ValueError("could not read direct Go importers" + (": " + detail if detail else ""))
    files = []
    for line in result.stdout.decode("utf-8").splitlines():
        _, separator, path = line.partition(":")
        if separator and path.endswith(".go") and PurePosixPath(path).parent.as_posix() != directory:
            files.append(path)
    return sorted(set(files))


def _referencing_go_tests(root: Path, head: str, fixture: str) -> list[dict]:
    filename = PurePosixPath(fixture).name
    result = subprocess.run(["git", "grep", "-l", "-F", filename, head, "--", "*_test.go"],
                            cwd=root, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False)
    if result.returncode not in {0, 1}:
        detail = result.stderr.decode("utf-8", errors="replace").strip()
        raise ValueError("could not find direct Go fixture consumers" + (": " + detail if detail else ""))
    checks = []
    for line in result.stdout.decode("utf-8").splitlines():
        _, separator, path = line.partition(":")
        if not separator or not path.endswith("_test.go"):
            continue
        content = _blob(root, head, path)
        if content is None:
            continue
        lines = content.splitlines()
        starts = [(index, match.group(1).decode("ascii"))
                  for index, line in enumerate(lines, 1) if (match := TEST.match(line))]
        for index, (first, name) in enumerate(starts):
            end = starts[index + 1][0] if index + 1 < len(starts) else len(lines) + 1
            if any(filename.encode() in line for line in lines[first - 1:end - 1]):
                package_dir = PurePosixPath(path).parent.as_posix()
                checks.append({"lane": "backend", "path": path,
                               "package": "./" + ("." if package_dir == "." else package_dir),
                               "test": name})
    return checks


def _package_go_files(root: Path, head: str, directory: str) -> list[str]:
    output = _git(root, "ls-tree", "-r", "--name-only", head, "--", directory).decode("utf-8")
    prefix = "" if directory == "." else directory + "/"
    return sorted(path for path in output.splitlines()
                  if path.endswith(".go") and (directory == "." or path.startswith(prefix)))


def _add(checks: list[dict], check: dict) -> None:
    key = json.dumps(check, sort_keys=True, separators=(",", ":"))
    if all(json.dumps(existing, sort_keys=True, separators=(",", ":")) != key for existing in checks):
        checks.append(check)


def _needs_manifest(paths: list[str]) -> ValueError:
    return ValueError("docs/release-checks.json is required to select a direct check for: "
                      + ", ".join(paths))


def select_light(root: Path, base: str, head: str, paths: list[str]) -> dict:
    """Choose changed packages and direct/adjacent checks without a dependency graph."""
    if not paths:
        raise ValueError("empty candidate has no behavior to check")
    root = root.resolve()
    docs = [path for path in paths if path.endswith(".md") and path.startswith(("docs/", "skills/"))]
    app = [path for path in paths if path not in docs]
    if not app:
        return {"mode": "targeted", "lanes": ["preflight"], "checks": [],
                "reason": "documentation", "reasons": ["documentation"], "profile": "documentation",
                "selection_source": "lightweight-changed-paths", "installed_checks": []}

    checks: list[dict] = []
    go_paths = [path for path in app if path.endswith(".go")]
    module = _module_path(root, head)
    host_needs_named_check = []
    for path in go_paths:
        directory = PurePosixPath(path).parent.as_posix()
        package = "./" + ("." if directory == "." else directory)
        exists = _blob(root, head, path) is not None
        if path.endswith("_test.go"):
            names = changed_go_tests(root, base, head, path) if exists else []
            if names:
                for name in names:
                    lane = "browser" if path.startswith("cmd/aicrm/") and name.endswith("ChromiumJourney") else "backend"
                    check = {"lane": lane, "path": path, "test": name}
                    if lane == "backend":
                        check["package"] = package
                    _add(checks, check)
            elif exists and directory != "cmd/aicrm":
                _add(checks, {"lane": "backend", "path": path, "package": package})
        else:
            if directory == "cmd/aicrm":
                host_needs_named_check.append(path)
                continue
            anchor = path if exists else next(iter(_package_go_files(root, head, directory)), None)
            if anchor is not None:
                _add(checks, {"lane": "backend", "path": anchor, "package": package})
            for importer in _direct_go_importers(root, head, module, directory):
                consumer_package = PurePosixPath(importer).parent.as_posix()
                if consumer_package == "cmd/aicrm":
                    host_needs_named_check.append(path)
                    continue
                _add(checks, {"lane": "backend", "path": importer,
                              "package": "./" + ("." if consumer_package == "." else consumer_package)})

    for path in app:
        if path.endswith(".go"):
            continue
        suffix = PurePosixPath(path).suffix
        name = PurePosixPath(path).name
        go_consumers = _referencing_go_tests(root, head, path) if suffix in {".mjs", ".js"} else []
        if go_consumers:
            for check in go_consumers:
                _add(checks, check)
            continue
        if suffix in {".mjs", ".js", ".ts", ".tsx", ".jsx"}:
            if name.endswith((".test.mjs", ".test.js")):
                _add(checks, {"lane": "frontend", "path": path})
                continue
            stem = name.rsplit(".", 1)[0]
            neighbors = [str(PurePosixPath(path).with_name(stem + extension))
                         for extension in (".test.mjs", ".test.js")]
            found = [candidate for candidate in neighbors if _blob(root, head, candidate) is not None]
            if not found:
                raise _needs_manifest([path])
            for candidate in found:
                _add(checks, {"lane": "frontend", "path": candidate})
            continue
        if suffix in {".css", ".scss"}:
            parent = PurePosixPath(path).parent.as_posix()
            files = _git(root, "ls-tree", "-r", "--name-only", head, "--", parent).decode("utf-8").splitlines()
            tests = [item for item in files if PurePosixPath(item).parent.as_posix() == parent
                     and PurePosixPath(item).name.endswith((".test.mjs", ".test.js"))]
            if len(tests) != 1:
                raise _needs_manifest([path])
            _add(checks, {"lane": "frontend", "path": tests[0]})
            continue
        if suffix == ".py":
            if name.startswith("test_") or name.endswith("_test.py"):
                _add(checks, {"lane": "preflight", "path": path})
                continue
            stem = PurePosixPath(path).stem
            parent = PurePosixPath(path).parent
            candidates = [str(parent / ("test_" + name)), str(parent / (stem + "_test.py"))]
            found = [candidate for candidate in candidates if _blob(root, head, candidate) is not None]
            if not found:
                raise _needs_manifest([path])
            for candidate in found:
                _add(checks, {"lane": "preflight", "path": candidate})
            continue
        if suffix == ".sh":
            if name.startswith(("test-", "test_")) or name.endswith("_test.sh"):
                _add(checks, {"lane": "preflight", "path": path})
                continue
            stem = PurePosixPath(path).stem
            parent = PurePosixPath(path).parent
            candidates = [str(parent / ("test-" + stem + ".sh")),
                          str(parent / ("test_" + stem + ".sh")),
                          str(parent / (stem + "_test.sh"))]
            found = [candidate for candidate in candidates if _blob(root, head, candidate) is not None]
            if not found:
                raise _needs_manifest([path])
            for candidate in found:
                _add(checks, {"lane": "preflight", "path": candidate})
            continue
        raise _needs_manifest([path])

    if not checks:
        raise _needs_manifest(app)
    host_checks = [check for check in checks
                   if check.get("path", "").startswith("cmd/aicrm/") and check.get("test")]
    if host_needs_named_check and not host_checks:
        raise _needs_manifest(host_needs_named_check)
    checks = [ai_check_plan.validate_check(root, head, check) for check in checks]
    lane_order = ai_check_plan.LANE_ORDER
    lanes = sorted({check["lane"] for check in checks}, key=lane_order.__getitem__)
    return {"mode": "targeted", "lanes": lanes, "checks": checks,
            "reason": "changed-packages-and-direct-checks",
            "reasons": ["changed-packages-and-direct-checks"], "profile": "ai-selected",
            "selection_source": "adjacent-behavior-checks", "installed_checks": []}


def select(root: Path, base: str, head: str, paths: list[str], graph: dict | None = None,
           *, policy_changed: bool = False) -> tuple[dict, list[str]]:
    """Compatibility API; default selection deliberately ignores dependency graphs."""
    return select_light(root, base, head, paths), []
