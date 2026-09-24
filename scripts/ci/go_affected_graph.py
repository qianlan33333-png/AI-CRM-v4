#!/usr/bin/env python3
"""Build a conservative Go package impact graph from two committed trees.

The graph uses ``go list -deps -test -json`` so production imports, internal and
external test imports, and files selected by ``//go:embed`` all participate.
Both revisions are materialized in a temporary local clone; the source checkout
is never changed.
"""
from __future__ import annotations

import argparse
import fnmatch
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
from typing import Any


SCHEMA = 1
GO_LIST_ARGS = ("list", "-mod=readonly", "-deps", "-test", "-json", "./...")
GO_FILE_FIELDS = ("GoFiles", "CgoFiles", "CompiledGoFiles", "TestGoFiles", "XTestGoFiles")
EMBED_FILE_FIELDS = ("EmbedFiles", "TestEmbedFiles", "XTestEmbedFiles")
IMPORT_FIELDS = (("Imports", "production"), ("TestImports", "internal-test"),
                 ("XTestImports", "external-test"))


class GraphError(ValueError):
    """Raised when a trustworthy package graph cannot be produced."""


def _git(root: Path, *args: str) -> str:
    return subprocess.check_output(["git", *args], cwd=root, text=True).strip()


def _resolve_commit(root: Path, ref: str) -> str:
    sha = _git(root, "rev-parse", ref + "^{commit}")
    if not re.fullmatch(r"[0-9a-f]{40}", sha):
        raise GraphError("revision did not resolve to a full commit SHA")
    return sha


def changed_paths(root: Path, base: str, head: str) -> list[str]:
    """Return both sides of additions, deletions, renames, and copies."""
    data = subprocess.check_output(
        ["git", "diff", "--name-status", "-M", "-C", "-z", base, head],
        cwd=root,
    ).decode("utf-8", errors="strict")
    tokens = data.split("\0")
    result: set[str] = set()
    position = 0
    while position < len(tokens) and tokens[position]:
        status = tokens[position]
        position += 1
        count = 2 if status.startswith(("R", "C")) else 1
        if position + count > len(tokens):
            raise GraphError("git returned a truncated name-status diff")
        result.update(tokens[position:position + count])
        position += count
    return sorted(result)


def _json_stream(text: str) -> list[dict[str, Any]]:
    decoder = json.JSONDecoder()
    position = 0
    records: list[dict[str, Any]] = []
    while position < len(text):
        while position < len(text) and text[position].isspace():
            position += 1
        if position == len(text):
            break
        try:
            item, position = decoder.raw_decode(text, position)
        except json.JSONDecodeError as error:
            raise GraphError(f"go list returned invalid JSON at byte {error.pos}") from error
        if not isinstance(item, dict):
            raise GraphError("go list JSON stream contains a non-object record")
        records.append(item)
    if not records:
        raise GraphError("go list returned an empty package inventory")
    return records


def _relative_paths(root: Path, package_dir: str, filename: str) -> set[str]:
    candidate = Path(filename)
    if not candidate.is_absolute():
        candidate = Path(package_dir) / candidate
    result = set()
    root_lexical = Path(os.path.abspath(root))
    root_resolved = root.resolve()
    # Keep the lexical path so a changed symlink entry is attributed, and its
    # resolved path so changes to an embedded canonical target are attributed.
    for path in (Path(os.path.abspath(candidate)), candidate.resolve(strict=False)):
        try:
            relative = path.relative_to(root_lexical if path == Path(os.path.abspath(candidate))
                                        else root_resolved).as_posix()
        except (OSError, ValueError):
            continue
        if relative not in {"", "."} and not relative.startswith("../"):
            result.add(relative)
    return result


def inventory_from_records(root: Path, records: list[dict[str, Any]], module_path: str) -> dict[str, Any]:
    """Normalize ``go list`` records into repository packages, files, and edges."""
    root = Path(root).resolve()
    packages: dict[str, dict[str, Any]] = {}
    import_to_dir: dict[str, str] = {}
    package_records: list[tuple[str, dict[str, Any]]] = []

    local_records: list[tuple[str, str, str, dict[str, Any]]] = []
    import_paths_by_dir: dict[str, set[str]] = {}
    for record in records:
        if record.get("Error") or record.get("DepsErrors"):
            detail = record.get("Error") or record.get("DepsErrors")
            raise GraphError(f"go list package error for {record.get('ImportPath', '?')}: {detail}")
        raw_import = record.get("ImportPath")
        directory = record.get("Dir")
        if not isinstance(raw_import, str) or not isinstance(directory, str):
            continue
        try:
            rel_dir = Path(directory).resolve().relative_to(root).as_posix()
        except (OSError, ValueError):
            continue
        stripped_import = re.sub(r" \[[^]]+\]$", "", raw_import)
        for_test = record.get("ForTest")
        # go list includes synthetic copies of an imported package while
        # assembling another package's tests (ForTest may name that consumer).
        # They still belong to their own import path. External test packages
        # are the exception: their `_test` package belongs with ForTest.
        candidate = (for_test if isinstance(for_test, str) and for_test
                     and record.get("Name", "").endswith("_test") else stripped_import)
        if candidate == module_path or candidate.startswith(module_path + "/"):
            local_records.append((rel_dir, stripped_import, candidate, record))
            import_paths_by_dir.setdefault(rel_dir, set()).add(stripped_import)

    for rel_dir, raw_import, candidate_import, record in local_records:
        if record.get("Name") == "main" and raw_import.endswith(".test"):
            continue
        canonical_import = candidate_import
        external_test_package = bool(record.get("ForTest") and record.get("Name", "").endswith("_test"))
        if (not record.get("ForTest") and record.get("Name", "").endswith("_test")
                and raw_import.endswith("_test")):
            possible_owner = raw_import[:-5]
            if possible_owner in import_paths_by_dir.get(rel_dir, set()):
                canonical_import = possible_owner
                external_test_package = True
        record_module = record.get("Module")
        record_module_path = record_module.get("Path") if isinstance(record_module, dict) else None
        if record_module_path not in (None, module_path):
            continue
        if rel_dir == ".." or rel_dir.startswith("../"):
            continue

        entry = packages.setdefault(rel_dir, {
            "dir": rel_dir,
            "import_path": canonical_import,
            "source_files": set(),
            "embed_files": set(),
            "test_files": set(),
        })
        if entry["import_path"] != canonical_import:
            raise GraphError(f"multiple local import paths map to package directory {rel_dir}")
        import_to_dir[canonical_import] = rel_dir
        import_to_dir[raw_import] = rel_dir
        package_records.append((rel_dir, record))

        for field in GO_FILE_FIELDS:
            for filename in record.get(field, []):
                for path in _relative_paths(root, str(root / rel_dir), filename):
                    entry["source_files"].add(path)
                    if external_test_package or field in {"TestGoFiles", "XTestGoFiles"}:
                        entry["test_files"].add(path)
        for field in EMBED_FILE_FIELDS:
            for filename in record.get(field, []):
                for path in _relative_paths(root, str(root / rel_dir), filename):
                    entry["embed_files"].add(path)

    edges: set[tuple[str, str, str]] = set()
    for consumer_dir, record in package_records:
        for field, edge_kind in IMPORT_FIELDS:
            imports = record.get(field, [])
            if not isinstance(imports, list):
                raise GraphError(f"go list field {field} is not an array")
            for imported in imports:
                dependency_dir = import_to_dir.get(imported)
                if dependency_dir and dependency_dir != consumer_dir:
                    edges.add((consumer_dir, dependency_dir, edge_kind))

    normalized_packages = {}
    for rel_dir, entry in sorted(packages.items()):
        normalized_packages[rel_dir] = {
            "dir": rel_dir,
            "import_path": entry["import_path"],
            "source_files": sorted(entry["source_files"]),
            "embed_files": sorted(entry["embed_files"]),
            "test_files": sorted(entry["test_files"]),
        }
    normalized_edges = [
        {"consumer": consumer, "dependency": dependency, "kind": kind}
        for consumer, dependency, kind in sorted(edges)
    ]
    return {"module": module_path, "packages": normalized_packages, "edges": normalized_edges}


def module_path(root: Path) -> str:
    go_mod = (root / "go.mod").read_text(encoding="utf-8")
    match = re.search(r"(?m)^module[ \t]+([^\s]+)[ \t]*$", go_mod)
    if not match:
        raise GraphError("go.mod has no module declaration")
    return match.group(1)


def affected_closure(base_inventory: dict[str, Any], head_inventory: dict[str, Any], paths: list[str]) -> dict[str, Any]:
    """Compute affected packages over the union of base and head graphs."""
    inventories = (base_inventory, head_inventory)
    direct: dict[str, set[str]] = {}
    file_owners: dict[str, set[str]] = {}
    reverse: dict[str, set[str]] = {}
    for inventory in inventories:
        for package_dir, package in inventory["packages"].items():
            direct.setdefault(package_dir, set())
            for path in package["source_files"] + package["embed_files"]:
                file_owners.setdefault(path, set()).add(package_dir)
        for edge in inventory["edges"]:
            reverse.setdefault(edge["dependency"], set()).add(edge["consumer"])
            direct.setdefault(edge["consumer"], set())
            direct.setdefault(edge["dependency"], set())

    seed_packages: set[str] = set()
    direct_path_owners: dict[str, list[str]] = {}
    for path in paths:
        owners = file_owners.get(path, set())
        if owners:
            direct_path_owners[path] = sorted(owners)
            seed_packages.update(owners)
    unowned_go_paths = sorted(path for path in paths if path.endswith(".go")
                              and path not in direct_path_owners)

    affected = set(seed_packages)
    frontier = list(seed_packages)
    while frontier:
        dependency = frontier.pop()
        for consumer in reverse.get(dependency, set()):
            if consumer not in affected:
                affected.add(consumer)
                frontier.append(consumer)

    base_packages = base_inventory["packages"]
    head_packages = head_inventory["packages"]
    selected = []
    removed = []
    for package_dir in sorted(affected):
        base_package = base_packages.get(package_dir)
        head_package = head_packages.get(package_dir)
        if head_package:
            selected.append({
                "dir": package_dir,
                "import_path": head_package["import_path"],
                "baseline_present": base_package is not None,
                "head_present": True,
                "reason": "changed-or-transitive-import",
                "source_files": head_package.get("source_files", []),
                "embed_files": head_package.get("embed_files", []),
                "test_files": head_package.get("test_files", []),
            })
        elif base_package:
            removed.append({"dir": package_dir, "import_path": base_package["import_path"]})

    relevant_edges = set()
    for inventory in inventories:
        for edge in inventory["edges"]:
            if edge["consumer"] in affected and edge["dependency"] in affected:
                relevant_edges.add((edge["consumer"], edge["dependency"], edge["kind"]))

    return {
        "changed_paths": sorted(set(paths)),
        "direct_path_owners": direct_path_owners,
        "unowned_go_paths": unowned_go_paths,
        "graph_valid": not unowned_go_paths,
        "affected_package_dirs": sorted(affected),
        "selected_packages": selected,
        "removed_packages": removed,
        "edges": [
            {"consumer": consumer, "dependency": dependency, "kind": kind}
            for consumer, dependency, kind in sorted(relevant_edges)
        ],
    }


def _run_checked(command: list[str], cwd: Path, env: dict[str, str] | None = None) -> str:
    result = subprocess.run(command, cwd=cwd, env=env, capture_output=True, text=True, check=False)
    if result.returncode:
        detail = (result.stderr or result.stdout).strip()
        raise GraphError(f"command failed ({result.returncode}): {' '.join(command)}: {detail[-2000:]}")
    return result.stdout


def _checkout_snapshot(repo: Path, sha: str, destination: Path) -> None:
    """Check out a revision in a temporary shared clone with its own Git index."""
    if not destination.exists():
        result = subprocess.run(["git", "clone", "--quiet", "--shared", "--no-checkout",
                                 str(repo), str(destination)], cwd=repo.parent,
                                capture_output=True, text=True, check=False)
        if result.returncode:
            detail = (result.stderr or result.stdout).strip()
            raise GraphError(f"could not create isolated graph clone: {detail[-2000:]}")
    _run_checked(["git", "clean", "-ffdx"], cwd=destination)
    _run_checked(["git", "checkout", "--detach", "--force", sha], cwd=destination)


def _prepare_go_source(root: Path, node: str = "node") -> None:
    index = root / "web/donor-sources/source-index.json"
    if not index.is_file():
        return
    _run_checked([node, "scripts/prepare-donor-source-views.mjs", "--root", str(root)], cwd=root)


def go_list_inventory(root: Path, go: str = "go", node: str = "node") -> dict[str, Any]:
    """Run the canonical Linux/amd64 Go package and test graph inventory."""
    _prepare_go_source(root, node)
    env = dict(os.environ)
    env.update({"GOOS": "linux", "GOARCH": "amd64", "CGO_ENABLED": "1", "GOWORK": "off", "GOFLAGS": ""})
    output = _run_checked([go, *GO_LIST_ARGS], cwd=root, env=env)
    return inventory_from_records(root, _json_stream(output), module_path(root))


def build_graph(repo: Path, base: str, head: str, go: str = "go", node: str = "node") -> dict[str, Any]:
    """Create a base/head graph using isolated snapshots and union their edges."""
    repo = repo.resolve()
    base_sha = _resolve_commit(repo, base)
    head_sha = _resolve_commit(repo, head)
    base_tree = _git(repo, "rev-parse", base_sha + "^{tree}")
    head_tree = _git(repo, "rev-parse", head_sha + "^{tree}")
    paths = changed_paths(repo, base_sha, head_sha)
    with tempfile.TemporaryDirectory(prefix="aicrm-go-graph-") as temp:
        root = Path(temp)
        base_root, head_root = root / "base", root / "head"
        _checkout_snapshot(repo, base_sha, base_root)
        _checkout_snapshot(repo, head_sha, head_root)
        base_inventory = go_list_inventory(base_root, go, node)
        head_inventory = go_list_inventory(head_root, go, node)
    closure = affected_closure(base_inventory, head_inventory, paths)
    graph_digest_payload = {
        "base_tree": base_tree, "head_tree": head_tree,
        "base_edges": base_inventory["edges"], "head_edges": head_inventory["edges"],
        "base_packages": base_inventory["packages"], "head_packages": head_inventory["packages"],
        "changed_paths": paths,
        "direct_path_owners": closure["direct_path_owners"],
        "affected_package_dirs": closure["affected_package_dirs"],
        "selected_packages": closure["selected_packages"],
        "removed_packages": closure["removed_packages"],
    }
    graph_digest = hashlib.sha256(json.dumps(graph_digest_payload, sort_keys=True, separators=(",", ":")).encode()).hexdigest()
    return {
        "schema": SCHEMA,
        "baseline_sha": base_sha,
        "baseline_tree": base_tree,
        "head_sha": head_sha,
        "head_tree": head_tree,
        "module": head_inventory["module"],
        "graph_fingerprint": graph_digest,
        "go_list_args": list(GO_LIST_ARGS),
        **closure,
    }


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=Path(__file__).resolve().parents[2])
    parser.add_argument("--base", required=True)
    parser.add_argument("--head", default="HEAD")
    parser.add_argument("--out", type=Path)
    parser.add_argument("--go", default="go")
    parser.add_argument("--node", default="node")
    args = parser.parse_args()
    result = build_graph(args.root, args.base, args.head, args.go, args.node)
    content = json.dumps(result, ensure_ascii=False, indent=2) + "\n"
    if args.out:
        args.out.parent.mkdir(parents=True, exist_ok=True)
        args.out.write_text(content, encoding="utf-8")
    else:
        print(content, end="")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (GraphError, OSError, subprocess.SubprocessError, ValueError) as error:
        print(f"Go impact graph failed closed: {type(error).__name__}: {error}", file=sys.stderr)
        raise SystemExit(2)
