"""Choose checks from changed files and the base/head Go consumer graph.

The selector runs from the trusted base checkout. A candidate cannot edit the
code that chooses its own checks. Unknown relationships fall back to a package
suite or the complete lane, instead of inheriting a static directory risk tag.
"""
from __future__ import annotations

import re
import subprocess
import input_scope
import host_contracts
from pathlib import Path


TEST = re.compile(r"(?m)^func\s+(Test[A-Za-z0-9_]+)\s*\(")
HUNK = re.compile(r"^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@", re.M)


def _git(root: Path, *args: str) -> str:
    return subprocess.check_output(["git", *args], cwd=root, text=True)


def changed_go_tests(root: Path, base: str, head: str, path: str) -> list[str]:
    """Identify tests whose bodies intersect a changed hunk in the head file."""
    file = root / path
    if not file.is_file():
        return []
    lines = file.read_text(encoding="utf-8").splitlines()
    starts = [(index, match.group(1)) for index, line in enumerate(lines, 1)
              if (match := TEST.match(line))]
    if not starts:
        return []
    diff = _git(root, "diff", "--no-ext-diff", "--unified=0", base, head, "--", path)
    changed = set()
    for match in HUNK.finditer(diff):
        first, count = int(match.group(1)), int(match.group(2) or "1")
        changed.update(range(first, first + max(1, count)))
    result = []
    for index, (first, name) in enumerate(starts):
        end = starts[index + 1][0] if index + 1 < len(starts) else len(lines) + 1
        if any(first <= line < end for line in changed):
            result.append(name)
    return result


def _domain_hints(root: Path, base: str, head: str, paths: list[str]) -> set[str]:
    hints = set()
    for path in paths:
        if path.startswith("web/"):
            stem = Path(path).stem
            hints.update(re.findall(r"[a-z]+", re.sub(r"([a-z])([A-Z])", r"\1 \2", stem).lower()))
        if path.startswith("cmd/aicrm/") and path.endswith(".go"):
            diff = _git(root, "diff", "--no-ext-diff", "--unified=0", base, head, "--", path)
            for route in re.findall(r'"/(?:api/v\d+/)?([a-z][a-z0-9_-]+)', diff):
                hints.add(route.split("/")[0].replace("-", "_"))
    return {hint for hint in hints if len(hint) >= 5 and hint not in {
        "admin", "style", "center", "script", "assets", "component", "shared", "public", "index"}}


def _referencing_go_tests(root: Path, fixture: str) -> list[dict[str, str]]:
    """Find Go tests that invoke a changed non-Go journey fixture by name."""
    filename = Path(fixture).name
    parent = root / Path(fixture).parent
    checks = []
    for file in parent.glob("*_test.go"):
        lines = file.read_text(encoding="utf-8").splitlines()
        starts = [(index, match.group(1)) for index, line in enumerate(lines, 1)
                  if (match := TEST.match(line))]
        for index, (first, name) in enumerate(starts):
            end = starts[index + 1][0] if index + 1 < len(starts) else len(lines) + 1
            if filename in "\n".join(lines[first - 1:end - 1]):
                checks.append({"lane": "backend", "path": file.relative_to(root).as_posix(), "test": name})
    return checks


def select(root: Path, base: str, head: str, paths: list[str], graph: dict,
           *, policy_changed: bool = False) -> tuple[dict, list[str]]:
    if not paths:
        raise ValueError("empty candidate has no behavior to check")
    inputs = graph.get('input_scope') or input_scope.resolve(root, base, head, paths)
    owned = {p for package in graph.get('selected_packages',[]) for p in package.get('source_files',[])+package.get('embed_files',[])}
    app = [path for path in paths if not input_scope.document(path) or path in owned]
    app = [p for p in app if p not in inputs['mapped']]
    app += [p for values in inputs['mapped'].values() for p in values if not p.endswith('_test.go')]
    app = sorted(set(app))
    if not app:
        return {"mode": "targeted", "lanes": ["preflight"], "checks": [],
                "reason": "documentation", "profile": "behavior" if inputs["contracts"] else "documentation"}, []
    tooling_only = all(path.startswith(("scripts/", "deploy/", ".github/")) for path in app)
    if tooling_only:
        return {"mode": "targeted", "lanes": ["preflight"], "checks": [],
                "reason": "release-tool-contracts", "profile": "tooling"}, []
    if policy_changed:
        # The candidate may not narrow checks for its own runtime changes.
        return {"mode": "full", "lanes": ["preflight", "backend", "frontend", "browser", "archive-sdk"],
                "checks": [], "reason": "policy-and-runtime-changed", "profile": "full"}, []
    if inputs['unknown']:
        reasons=sorted(set(inputs['unknown'].values()))
        lanes=['preflight']
        if graph.get('selected_packages') or any(p.endswith('.go') for p in paths) or any(not r.startswith('npm') for r in reasons):lanes.append('backend')
        if any(r.startswith(('npm','api')) for r in reasons):lanes.append('frontend')
        lanes.append('browser')
        return {'mode':'full','lanes':lanes,'checks':[], 'reason':','.join(reasons),'profile':'full'}, []
    if any(not path.startswith(("web/", "internal/", "cmd/", "pkg/")) for path in app):
        return {"mode": "full", "lanes": ["preflight", "backend", "frontend", "browser", "archive-sdk"],
                "checks": [], "reason": "unknown-runtime-input", "profile": "full"}, []
    if any(path.startswith("web/v3/shared/") or
           (path.endswith((".css", ".scss")) and Path(path).stem.lower() in
            {"global", "base", "tokens", "theme", "styles", "common", "shared"})
           for path in app):
        # Shared UI and global style consumers are not proven by adjacent tests.
        return {"mode": "full", "lanes": ["preflight", "frontend", "browser"],
                "checks": [], "reason": "shared-web-consumers-unknown", "profile": "full"}, []

    checks: list[dict[str, str]] = []
    selected = graph.get("selected_packages", [])
    for package in selected:
        directory = package["dir"]
        changed_tests = [path for path in paths if path.startswith(directory + "/")
                         and path.endswith("_test.go")]
        named = [(path, name) for path in changed_tests
                 for name in changed_go_tests(root, base, head, path)]
        grouped = host_contracts.select(root, paths, graph) if directory == 'cmd/aicrm' else None
        if grouped is not None:
            checks.extend(grouped)
        else:
            source = next((path for path in package.get('source_files', []) if path.endswith('.go')), None)
            if source is None:raise ValueError('affected Go package has no source path')
            checks.append({'lane':'backend','path':source})
            # A complete Host backend excludes Chromium, so retain its complete
            # discovered browser class whenever shared Host code changes.
            if directory == 'cmd/aicrm':
                for carrier in sorted((root/'cmd/aicrm').glob('*_test.go')):
                    checks.extend({'lane':'browser','path':carrier.relative_to(root).as_posix(),'test':name}
                                  for name in TEST.findall(carrier.read_text()) if name.endswith('ChromiumJourney'))

    for path in paths:
        if path.endswith((".test.mjs", ".test.js")):
            checks.append({"lane": "frontend", "path": path})
        elif path.startswith(("internal/", "cmd/")) and path.endswith((".mjs", ".js")):
            referring = _referencing_go_tests(root, path)
            if not referring and not selected:
                return {"mode": "full", "lanes": ["preflight", "backend", "browser"],
                        "checks": [], "reason": "unmapped-go-journey-consumer", "profile": "full"}, []
            checks.extend(referring)
    for path in app:
        if not path.startswith("web/") or path.endswith((".test.mjs", ".test.js")):
            continue
        stem = Path(path).stem
        adjacent = Path(path).with_name(stem + ".test.mjs")
        if (root / adjacent).is_file():
            checks.append({"lane": "frontend", "path": adjacent.as_posix()})

    hints = _domain_hints(root, base, head, paths)
    if hints:
        for file in sorted((root / "web/v3").glob("*.test.mjs")):
            if any(hint in file.stem.lower() for hint in hints):
                checks.append({"lane": "frontend", "path": file.relative_to(root).as_posix()})
        for file in sorted((root / "cmd/aicrm").glob("*_chromium*_test.go")):
            relative = file.relative_to(root).as_posix()
            if not any(hint in file.stem for hint in hints):
                continue
            checks.extend({"lane": "browser", "path": relative, "test": name}
                          for name in TEST.findall(file.read_text(encoding="utf-8"))
                          if name.endswith("ChromiumJourney"))

    unique = {(item["lane"], item["path"], item.get("test", "")): item for item in checks}
    checks = [unique[key] for key in sorted(unique)]
    lanes = [lane for lane in ("preflight", "backend", "frontend", "browser", "archive-sdk")
             if lane == "preflight" or any(check["lane"] == lane for check in checks)]
    if any(path.startswith(("internal/", "cmd/")) and path.endswith(".go") for path in app) and "backend" not in lanes:
        raise ValueError("changed Go source has no selected package check")
    if any(path.startswith("web/") for path in app) and not any(
            check["lane"] in {"frontend", "browser"} for check in checks):
        # Shared or unmapped Web entries have no proven consumer scope yet.
        return {"mode": "full", "lanes": ["preflight", "frontend", "browser"],
                "checks": [], "reason": "unmapped-web-consumer", "profile": "full"}, []
    return {"mode": "targeted", "lanes": lanes, "checks": checks,
            "reason": "direct-tests-and-consumers", "profile": "behavior"}, []
