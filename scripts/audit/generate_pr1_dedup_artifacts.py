#!/usr/bin/env python3
"""Materialize PR-1 duplicate-governance audit artifacts from pinned Git scans.

This is audit-only tooling. It reads pinned Git objects and pre-generated exact
scan reports, creates review records, and never mutates business source,
invokes a repository build, or executes a provider integration.

Layer A is the exact object inventory supplied by scan_exact_duplicates.py.
Layer B detects only byte-preserving mechanical normalization candidates.
Layer C is a bounded lexical shared-block heuristic, deliberately not an AST
or semantic equivalence decision. Layer D records static references with each
candidate explicitly blocked on runtime/build-owner review.
"""
from __future__ import annotations

import argparse
from collections import Counter, defaultdict
from dataclasses import dataclass
import difflib
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import posixpath
import re
import subprocess
import sys
from typing import Any, Iterable

ENV = dict(os.environ, GIT_NO_REPLACE_OBJECTS="1", GIT_NO_LAZY_FETCH="1", GIT_TERMINAL_PROMPT="0")
SCHEMA_VERSION = 1
TEXT_SUFFIXES = {
    ".c", ".cc", ".cpp", ".css", ".cjs", ".go", ".h", ".html", ".java", ".js",
    ".json", ".md", ".mjs", ".py", ".sh", ".sql", ".toml", ".ts", ".tsx", ".txt",
    ".yaml", ".yml", ".zsh",
}
CODEBLOCK_SUFFIXES = {
    ".c", ".cc", ".cpp", ".css", ".cjs", ".go", ".h", ".html", ".java", ".js",
    ".json", ".mjs", ".py", ".sh", ".sql", ".toml", ".ts", ".tsx", ".yaml", ".yml", ".zsh",
}
SOURCE_EXTENSIONS = (".ts", ".tsx", ".js", ".mjs", ".cjs", ".json", ".html", ".css", ".go", ".py", ".sh", ".sql", ".yaml", ".yml")
MAX_CODEBLOCK_BYTES = 256 * 1024
CODEBLOCK_WINDOW = 12
MAX_SHINGLE_OCCURRENCES = 12
IMPORT_RE = re.compile(
    r"(?:\bfrom\s*|\bimport\s*\(|\brequire\s*\(|\bimport\s+)(['\"])([^'\"\\]*(?:\\.[^'\"\\]*)*)\1"
)
NODE_FILE_RE = re.compile(
    r"\b(?:readFileSync|readFile|copyFileSync|copyFile|globSync|glob)\s*\(\s*(['\"])([^'\"\\]*(?:\\.[^'\"\\]*)*)\1"
)
HTML_ASSET_RE = re.compile(r"\b(?:src|href)\s*=\s*(['\"])([^'\"]+)\1", re.IGNORECASE)
GO_EMBED_RE = re.compile(r"^\s*//go:embed\s+(.+?)\s*$", re.MULTILINE)
GO_FILE_RE = re.compile(r"\bos\.(?:ReadFile|Open|Stat)\s*\(\s*(['\"])([^'\"\\]*(?:\\.[^'\"\\]*)*)\1")
GO_PATH_JOIN_RE = re.compile(r"\bfilepath\.Join\s*\(([^\n)]*)\)")
NODE_PATH_JOIN_RE = re.compile(r"\bpath\.(?:join|resolve)\s*\(([^\n)]*)\)")
SHELL_REPO_ROOT_RE = re.compile(r"\$(?:REPO_ROOT|ROOT|repo_root)/([A-Za-z0-9_./-]+)")
PUBLIC_STATIC_RE = re.compile(r"(['\"])(/static/[A-Za-z0-9_./-]+)\1")
QUOTED_SEGMENT_RE = re.compile(r"(['\"])([^'\"\\]*(?:\\.[^'\"\\]*)*)\1")


def git(repo: Path, *args: str, check: bool = True) -> subprocess.CompletedProcess[bytes]:
    result = subprocess.run(
        ["git", "-C", str(repo), *args], env=ENV, stdout=subprocess.PIPE,
        stderr=subprocess.PIPE, check=False, timeout=120,
    )
    if check and result.returncode:
        detail = result.stderr.decode("utf8", "replace").strip() or "Git command failed"
        raise RuntimeError(detail)
    return result


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def json_dump(path: Path, value: Any) -> None:
    path.write_text(json.dumps(value, ensure_ascii=True, indent=2, sort_keys=True) + "\n", encoding="utf8")


def load_json(path: Path) -> dict[str, Any]:
    value = json.loads(path.read_text(encoding="utf8"))
    if not isinstance(value, dict):
        raise ValueError(f"Expected object JSON: {path}")
    return value


def top_level(path: str) -> str:
    return path.split("/", 1)[0] if "/" in path else "(root files)"


def suffix(path: str) -> str:
    return PurePosixPath(path).suffix.lower()


def safe_text(data: bytes) -> str | None:
    if b"\0" in data:
        return None
    try:
        return data.decode("utf8")
    except UnicodeDecodeError:
        return None


def cat_blobs(repo: Path, object_ids: Iterable[str]) -> dict[str, bytes]:
    object_ids = sorted(set(object_ids))
    proc = subprocess.Popen(
        ["git", "-C", str(repo), "cat-file", "--batch"], env=ENV,
        stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
    )
    assert proc.stdin and proc.stdout
    values: dict[str, bytes] = {}
    try:
        for object_id in object_ids:
            proc.stdin.write((object_id + "\n").encode("ascii"))
        proc.stdin.close()
        for object_id in object_ids:
            header = proc.stdout.readline().strip().split()
            if len(header) != 3 or header[0].decode("ascii", "replace") != object_id or header[1] != b"blob":
                raise RuntimeError(f"Cannot read blob {object_id}: {header!r}")
            size = int(header[2])
            data = proc.stdout.read(size)
            if len(data) != size or proc.stdout.read(1) != b"\n":
                raise RuntimeError(f"Invalid cat-file framing for {object_id}")
            values[object_id] = data
    finally:
        if proc.stdin and not proc.stdin.closed:
            proc.stdin.close()
        if proc.stdout:
            proc.stdout.close()
        stderr = proc.stderr.read().decode("utf8", "replace") if proc.stderr else ""
        code = proc.wait(timeout=15)
        if code:
            raise RuntimeError(stderr.strip() or f"git cat-file exited {code}")
    return values


def normalized_mechanical(data: bytes) -> bytes:
    """Deliberately narrow, content-preserving candidate normalization only."""
    data = data.replace(b"\r\n", b"\n").replace(b"\r", b"\n")
    lines = data.split(b"\n")
    return b"\n".join(line.rstrip(b" \t") for line in lines)


def compact_diff(left: str, right: str, left_name: str, right_name: str) -> list[str]:
    diff = list(
        difflib.unified_diff(
            left.splitlines(), right.splitlines(), fromfile=left_name, tofile=right_name,
            lineterm="", n=2,
        )
    )
    if len(diff) > 80:
        return diff[:80] + ["... diff truncated after 80 lines ..."]
    return diff


def make_mechanical_candidates(records: list[dict[str, Any]], contents: dict[str, bytes]) -> dict[str, Any]:
    unique: dict[str, dict[str, Any]] = {}
    for record in records:
        unique.setdefault(record["object_id"], record)
    buckets: dict[str, list[str]] = defaultdict(list)
    skipped = Counter()
    for object_id, record in unique.items():
        data = contents[object_id]
        text = safe_text(data)
        if text is None:
            skipped["binary_or_non_utf8"] += 1
            continue
        if suffix(record["path"]) not in TEXT_SUFFIXES and not text.startswith("#!"):
            skipped["unsupported_text_suffix"] += 1
            continue
        buckets[sha256(normalized_mechanical(data))].append(object_id)
    candidates = []
    for norm, object_ids in sorted(buckets.items()):
        if len(object_ids) < 2:
            continue
        if len({contents[oid] for oid in object_ids}) < 2:
            continue
        entries = []
        for object_id in sorted(object_ids):
            paths = sorted(record["path"] for record in records if record["object_id"] == object_id)
            entries.append({
                "object_id": object_id,
                "content_sha256": sha256(contents[object_id]),
                "bytes": len(contents[object_id]),
                "paths": paths,
            })
        first, second = entries[0], entries[1]
        candidates.append({
            "candidate_id": "mechanical:" + norm,
            "layer": "B",
            "comparison": "utf8 text after CRLF/CR-to-LF and trailing-space/tab normalization",
            "normalization_sha256": norm,
            "entries": entries,
            "difference_summary": compact_diff(
                safe_text(contents[first["object_id"]]) or "",
                safe_text(contents[second["object_id"]]) or "",
                first["paths"][0], second["paths"][0],
            ),
            "human_decision": "unreviewed_candidate_no_merge_or_delete",
        })
    return {
        "method": "Narrow mechanical normalization; comments, string literals, generated headers, encoding and executable-mode semantics are not removed or interpreted.",
        "text_suffixes": sorted(TEXT_SUFFIXES),
        "skipped_unique_objects": dict(sorted(skipped.items())),
        "candidates": candidates,
    }


def normalized_code_line(line: str) -> str:
    return line.strip().replace("\t", " ")


def make_codeblock_candidates(records: list[dict[str, Any]], contents: dict[str, bytes]) -> dict[str, Any]:
    """Bounded lexical shingle scan. It intentionally does not claim AST equivalence."""
    representative: dict[str, dict[str, Any]] = {}
    for record in records:
        representative.setdefault(record["object_id"], record)
    fingerprints: dict[tuple[str, str], list[tuple[str, int]]] = defaultdict(list)
    pair_samples: dict[tuple[str, str], dict[str, Any]] = {}
    skipped = Counter()
    scanned = 0
    occurrence_limit_reached = False
    for object_id, record in sorted(representative.items()):
        path = record["path"]
        data = contents[object_id]
        text = safe_text(data)
        ext = suffix(path)
        if text is None:
            skipped["binary_or_non_utf8"] += 1
            continue
        if ext not in CODEBLOCK_SUFFIXES:
            skipped["unsupported_suffix"] += 1
            continue
        if len(data) > MAX_CODEBLOCK_BYTES:
            skipped["over_bounded_bytes"] += 1
            continue
        lines = text.splitlines()
        if len(lines) < CODEBLOCK_WINDOW:
            skipped["fewer_than_window_lines"] += 1
            continue
        scanned += 1
        canonical_lines = [normalized_code_line(line) for line in lines]
        for start in range(0, len(canonical_lines) - CODEBLOCK_WINDOW + 1):
            sample = canonical_lines[start:start + CODEBLOCK_WINDOW]
            if not any(sample):
                continue
            fingerprint = sha256("\x1e".join(sample).encode("utf8"))
            key = (ext, fingerprint)
            prior = fingerprints[key]
            for prior_id, prior_start in prior:
                if prior_id == object_id:
                    continue
                pair = tuple(sorted((prior_id, object_id)))
                existing = pair_samples.get(pair)
                if existing is None:
                    pair_samples[pair] = {
                        "left_object_id": pair[0],
                        "right_object_id": pair[1],
                        "extension": ext,
                        "minimum_shared_block_lines": CODEBLOCK_WINDOW,
                        "left_start_line": prior_start + 1 if pair[0] == prior_id else start + 1,
                        "right_start_line": start + 1 if pair[1] == object_id else prior_start + 1,
                        "matching_window_count": 1,
                    }
                else:
                    existing["matching_window_count"] += 1
            if len(prior) < MAX_SHINGLE_OCCURRENCES:
                prior.append((object_id, start))
            else:
                occurrence_limit_reached = True
    by_id_paths: dict[str, list[str]] = defaultdict(list)
    for record in records:
        by_id_paths[record["object_id"]].append(record["path"])
    candidates = []
    for pair, sample in sorted(pair_samples.items(), key=lambda item: (-item[1]["matching_window_count"], item[0])):
        left, right = pair
        candidates.append({
            "candidate_id": "codeblock:" + left + ":" + right,
            "layer": "C",
            **sample,
            "left_paths": sorted(by_id_paths[left]),
            "right_paths": sorted(by_id_paths[right]),
            "human_decision": "unreviewed_lexical_overlap_no_merge_or_delete",
        })
    return {
        "method": "Lexical normalized-line windows, not AST parsing, token equivalence, data-flow or semantic analysis.",
        "supported_suffixes": sorted(CODEBLOCK_SUFFIXES),
        "window_lines": CODEBLOCK_WINDOW,
        "maximum_bytes_per_unique_blob": MAX_CODEBLOCK_BYTES,
        "maximum_indexed_occurrences_per_fingerprint": MAX_SHINGLE_OCCURRENCES,
        "scanned_unique_objects": scanned,
        "skipped_unique_objects": dict(sorted(skipped.items())),
        "occurrence_limit_reached": occurrence_limit_reached,
        "candidates": candidates,
    }


def annotate_known_near_candidates(near: dict[str, Any]) -> None:
    known_pair = {
        "2c9ad56268a570e7668db36a86645fedcde47fc7",
        "4f01e23fb6d3d4461702a115b7aaf320627e2ab2",
    }
    for candidate in near["codeblock"]["candidates"]:
        if {candidate["left_object_id"], candidate["right_object_id"]} == known_pair:
            candidate["human_decision"] = "retain_separate_test_contexts_not_safe_to_merge"
            candidate["review_evidence"] = [
                "web/src/api/admin.test.ts line ~1112 asserts questionnaire external-push metadata/configuration_version are undefined; the frozen GroupOps copy lacks that newer projection contract.",
                "web/src/api/admin.test.ts line ~1221 tests the versioned /api/admin/hxc-dashboard/summary full projection, while the frozen GroupOps copy retains the narrower /api/admin/hxc-current?limit=100 contract and matching assertions.",
                "The two copies therefore share lexical blocks but have distinct test intent and endpoint/DTO coverage. Keep both execution contexts; no extraction or overwrite is approved.",
            ]
            return
    raise RuntimeError("Expected admin.test.ts counterexample was not found by the lexical candidate scan.")


def candidate_target_paths(duplicates: list[dict[str, Any]], near: dict[str, Any]) -> set[str]:
    paths = {path for group in duplicates for path in group["paths"]}
    for candidate in near["mechanical"]["candidates"]:
        for entry in candidate["entries"]:
            paths.update(entry["paths"])
    for candidate in near["codeblock"]["candidates"]:
        paths.update(candidate["left_paths"])
        paths.update(candidate["right_paths"])
    return paths


def resolve_local_reference(consumer: str, raw: str, all_paths: set[str], *, allow_bare_local: bool = False) -> str | None:
    raw = raw.strip()
    if not raw or raw.startswith(("http://", "https://", "data:", "#")):
        return None
    raw = raw.split("?", 1)[0].split("#", 1)[0]
    if raw.startswith("/"):
        base = raw.lstrip("/")
    elif raw.startswith(("./", "../")):
        base = posixpath.normpath(posixpath.join(posixpath.dirname(consumer), raw))
    elif allow_bare_local:
        base = posixpath.normpath(posixpath.join(posixpath.dirname(consumer), raw))
    else:
        return None
    if base.startswith("../") or base == "..":
        return None
    possibilities = [base]
    if not PurePosixPath(base).suffix:
        possibilities.extend(base + ext for ext in SOURCE_EXTENSIONS)
        possibilities.extend(posixpath.join(base, "index" + ext) for ext in SOURCE_EXTENSIONS)
    for possibility in possibilities:
        if possibility in all_paths:
            return possibility
    return None


def extract_static_references(path: str, text: str, all_paths: set[str]) -> list[dict[str, Any]]:
    refs: list[dict[str, Any]] = []
    seen: set[tuple[str, str, int]] = set()

    def add(kind: str, raw: str, index: int, *, allow_bare_local: bool = False) -> None:
        target = resolve_local_reference(path, raw, all_paths, allow_bare_local=allow_bare_local)
        static_path = raw.split("?", 1)[0].split("#", 1)[0]
        if target is None and static_path.startswith("/static/"):
            public_target = "internal/webshell/static/" + static_path.removeprefix("/static/")
            if public_target in all_paths:
                target = public_target
        if target is None or target == path:
            return
        line = text.count("\n", 0, index) + 1
        key = (kind, target, line)
        if key not in seen:
            seen.add(key)
            refs.append({"kind": kind, "target": target, "line": line, "raw": raw})

    def add_directory(kind: str, raw: str, index: int) -> None:
        base = posixpath.normpath(posixpath.join(posixpath.dirname(path), raw))
        if base.startswith("../") or base == "..":
            return
        prefix = base.rstrip("/") + "/"
        line = text.count("\n", 0, index) + 1
        for target in sorted(candidate for candidate in all_paths if candidate.startswith(prefix) and candidate != path):
            key = (kind, target, line)
            if key not in seen:
                seen.add(key)
                refs.append({"kind": kind, "target": target, "line": line, "raw": raw + "/**"})

    for match in IMPORT_RE.finditer(text):
        add("static_or_dynamic_module_reference", match.group(2), match.start())
    for match in NODE_FILE_RE.finditer(text):
        add("node_file_or_glob_reference", match.group(2), match.start(), allow_bare_local=True)
    for match in HTML_ASSET_RE.finditer(text):
        add("html_asset_reference", match.group(2), match.start(), allow_bare_local=True)
    for match in GO_EMBED_RE.finditer(text):
        for raw in match.group(1).split():
            if not raw.startswith("-"):
                add("go_embed_reference", raw, match.start(), allow_bare_local=True)
                add_directory("go_embed_directory_reference", raw, match.start())
    for match in GO_FILE_RE.finditer(text):
        add("go_file_reference", match.group(2), match.start(), allow_bare_local=True)
    for match in GO_PATH_JOIN_RE.finditer(text):
        quoted = [item[1] for item in QUOTED_SEGMENT_RE.findall(match.group(1))]
        if quoted:
            add("go_filepath_join_reference", "/".join(quoted), match.start(), allow_bare_local=True)
    for match in NODE_PATH_JOIN_RE.finditer(text):
        quoted = [item[1] for item in QUOTED_SEGMENT_RE.findall(match.group(1))]
        if quoted:
            raw = "/".join(quoted)
            target = raw if raw in all_paths else None
            if target and target != path:
                refs.append({"kind": "node_repository_root_path_join", "target": target, "line": text.count("\n", 0, match.start()) + 1, "raw": raw})
    for match in SHELL_REPO_ROOT_RE.finditer(text):
        target = match.group(1)
        if target in all_paths and target != path:
            refs.append({"kind": "shell_repository_root_path", "target": target, "line": text.count("\n", 0, match.start()) + 1, "raw": "$REPO_ROOT/" + target})
    for match in PUBLIC_STATIC_RE.finditer(text):
        add("webshell_public_static_reference", match.group(2), match.start())
    return refs


def make_dependency_map(records: list[dict[str, Any]], contents: dict[str, bytes], targets: set[str]) -> dict[str, Any]:
    all_paths = {record["path"] for record in records}
    target_consumers: dict[str, list[dict[str, Any]]] = {path: [] for path in sorted(targets)}
    target_basenames: dict[str, set[str]] = defaultdict(set)
    for target in targets:
        target_basenames[PurePosixPath(target).name].add(target)
    basename_match = re.compile("|".join(re.escape(name) for name in sorted(target_basenames, key=lambda value: (-len(value), value)))) if target_basenames else None
    text_files = 0
    unresolved_dynamic_files = []
    for record in records:
        text = safe_text(contents[record["object_id"]])
        if text is None:
            continue
        text_files += 1
        consumer = record["path"]
        for ref in extract_static_references(consumer, text, all_paths):
            if ref["target"] in targets:
                target_consumers[ref["target"]].append({
                    "consumer_path": consumer,
                    "method": ref["kind"],
                    "line": ref["line"],
                    "evidence": ref["raw"],
                    "confidence": "static_resolved",
                })
        if basename_match:
            for match in basename_match.finditer(text):
                name = match.group(0)
                for target in sorted(target_basenames[name]):
                    if target == consumer:
                        continue
                    if target in text:
                        method, confidence = "literal_repository_path", "static_literal"
                    else:
                        method, confidence = "basename_mention", "ambiguous_needs_owner_review"
                    target_consumers[target].append({
                        "consumer_path": consumer,
                        "method": method,
                        "line": text.count("\n", 0, match.start()) + 1,
                        "evidence": name,
                        "confidence": confidence,
                    })
        if "import(" in text or "readFile" in text or "copyFile" in text or "glob" in text:
            unresolved_dynamic_files.append(consumer)
    normalized: dict[str, dict[str, Any]] = {}
    for target, consumers in target_consumers.items():
        unique = {(entry["consumer_path"], entry["method"], entry["line"], entry["evidence"]): entry for entry in consumers}
        ordered = sorted(unique.values(), key=lambda entry: (entry["consumer_path"], entry["line"], entry["method"], entry["evidence"]))
        normalized[target] = {
            "static_consumers": ordered,
            "static_consumer_count": len(ordered),
            "dependency_status": "static_inventory_recorded; runtime_and_staging_proof_deferred_to_named_P2_entrypoints",
            "reason": "This static inventory cannot prove computed imports, shell expansion, runtime route selection, generated asset inclusion or release staging. Each exact group therefore names the P2 entrypoints that must provide the remaining proof; no removal is authorized in PR-1.",
        }
    return {
        "schema_version": SCHEMA_VERSION,
        "scope": "Static Git-tree analysis of every readable UTF-8 blob at the target commit; never an assertion that no dynamic consumer exists.",
        "methods": [
            "relative TypeScript/JavaScript module import, require and dynamic-import literals",
            "literal Node read/copy/glob calls and repository-root path.join/path.resolve segments",
            "literal HTML src/href attributes",
            "Go //go:embed files/directories, os.ReadFile/Open/Stat and filepath.Join literals",
            "shell $REPO_ROOT literals",
            "full-path and basename lexical references across all readable text blobs",
        ],
        "target_path_count": len(targets),
        "scanned_text_file_count": text_files,
        "files_with_dynamic_or_file_loading_tokens": sorted(set(unresolved_dynamic_files)),
        "targets": normalized,
    }


V2_FROZEN_COMMIT = "6bfbe5816bb89913c70adaca87d6a486260e016e"
AI_PRODUCTION_FROZEN_COMMIT = "dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f"
AI_PRODUCTION_SOURCE_PATHS = {
    "web/donors/ai-assistant-production/static/send_content_readonly_detail.css": (
        "aicrm_next/app/admin_console/static/admin_console/send_content_readonly_detail.css"
    ),
}
V2_MODULES = {
    "products-v2": {
        "module": "products", "manifest": "docs/migration/product/pr04-donor-manifest.yaml",
        "gate": "scripts/check-pr04-donor-manifest.sh",
    },
    "coupons-v2": {
        "module": "coupons", "manifest": "docs/migration/coupon/pr05-donor-manifest.yaml",
        "gate": "scripts/check-pr05-closure.sh",
    },
    "automation-v2": {
        "module": "automation", "manifest": "docs/migration/automation/pr07-donor-manifest.yaml",
        "gate": "scripts/check-pr07-frontend-freeze.sh",
    },
    "automation-operations-v2": {
        "module": "automation_operations", "manifest": "docs/migration/automationops/donor-sha256.txt",
        "gate": "scripts/check-automationops-donor-manifest.sh",
    },
    "media-v2": {
        "module": "media", "manifest": "docs/migration/media/pr02-donor-manifest.yaml",
        "gate": "scripts/check-pr02-donor-manifest.sh",
    },
    "groupops-v2": {
        "module": "group_operations", "manifest": "docs/migration/groupops/pr06-donor-manifest.yaml",
        "gate": "scripts/check-pr06-donor-manifest.sh",
    },
    "adminops-v2": {
        "module": "admin_operations", "manifest": "docs/migration/adminops/pr09-donor-manifest.yaml",
        "gate": "scripts/check-pr09-frontend-freeze.sh",
    },
    "operation-cycles-v2": {
        "module": "operation_cycles", "manifest": "docs/donor-manifests/pr08-operation-cycles.yaml",
        "gate": "scripts/check-pr08-frontend-donor-manifest.sh",
    },
}


def logical_source_path(path: str) -> str | None:
    if path.startswith("web/src/"):
        return path
    match = re.match(r"^web/donors/[^/]+/src/(.+)$", path)
    if match:
        return "web/src/" + match.group(1)
    return None


def source_binding_for_path(path: str, record: dict[str, Any], target_commit: str, content_sha256: str) -> dict[str, Any]:
    binding: dict[str, Any] = {
        "logical_path": path,
        "mode": record["mode"],
        "content_sha256": content_sha256,
        "bytes": record["bytes"],
    }
    if path.startswith("web/donors/"):
        donor = path.split("/")[2]
        logical = logical_source_path(path)
        if donor == "ai-assistant-production":
            source_path = AI_PRODUCTION_SOURCE_PATHS.get(path)
            if source_path is None:
                raise ValueError(f"No exact production donor source path recorded for {path}")
            binding.update({
                "module": "ai_assistant", "source_repository": "AI-CRM production donor",
                "source_commit": AI_PRODUCTION_FROZEN_COMMIT, "source_path": source_path,
                "source_git_blob_sha": record["object_id"],
                "usage": "frozen_production_asset_compatibility_view",
                "freeze_gate": "scripts/check-ai-assistant-donor-manifest.sh",
                "freeze_ledger": "docs/migration/ai-assistant/donor-sha256.txt",
            })
            return binding
        module = V2_MODULES.get(donor)
        if module is None:
            binding.update({
                "module": donor, "source_repository": "AI-CRM-v2",
                "source_commit": V2_FROZEN_COMMIT, "source_path": logical or path,
                "usage": "frozen_donor_compatibility_view",
                "freeze_gate": None, "freeze_ledger": None,
            })
            return binding
        binding.update({
            "module": module["module"], "source_repository": "AI-CRM-v2",
            "source_commit": V2_FROZEN_COMMIT, "source_path": logical or path,
            "usage": "frozen_donor_compatibility_view", "freeze_gate": module["gate"],
            "freeze_ledger": module["manifest"],
        })
        return binding
    if path.startswith("web/src/"):
        binding.update({
            "module": "v3_web_frozen_view", "source_repository": "AI-CRM-v3",
            "source_commit": target_commit, "source_path": path,
            "usage": "currently_tracked_exact_copy_to_become_materialized_view_only_if_still_byte_identical",
            "freeze_gate": "scripts/check-pr01-donor-manifest.sh",
            "freeze_ledger": "docs/donor-manifests/pr01-web.sha256",
        })
        return binding
    if path == "api/openapi.yaml":
        binding.update({
            "module": "v3_openapi_contract", "source_repository": "AI-CRM-v3",
            "source_commit": target_commit, "source_path": path,
            "usage": "active_canonical_contract_source", "freeze_gate": "scripts/validate-openapi.mjs",
            "freeze_ledger": None,
        })
        return binding
    if path == "internal/config/http/openapi.yaml":
        binding.update({
            "module": "config_http", "source_repository": "AI-CRM-v3",
            "source_commit": target_commit, "source_path": path,
            "usage": "go_embed_compatibility_view", "freeze_gate": "internal/config/http/handler_test.go",
            "freeze_ledger": None,
        })
        return binding
    if path.startswith("internal/webshell/static/"):
        binding.update({
            "module": "webshell", "source_repository": "AI-CRM-v3",
            "source_commit": target_commit, "source_path": path,
            "usage": "webshell_embedded_compatibility_view", "freeze_gate": "internal/webshell/handler_test.go",
            "freeze_ledger": None,
        })
        return binding
    binding.update({
        "module": "repository_source", "source_repository": "AI-CRM-v3",
        "source_commit": target_commit, "source_path": path,
        "usage": "tracked_exact_copy", "freeze_gate": None, "freeze_ledger": None,
    })
    return binding


def canonical_plan(paths: list[str], content_sha256: str) -> dict[str, Any]:
    if set(paths) == {"api/openapi.yaml", "internal/config/http/openapi.yaml"}:
        return {
            "canonical_path": "api/openapi.yaml",
            "canonical_lifecycle": "active_v3_api_contract_source",
            "p1_binding": "Declare api/openapi.yaml and its SHA-256 as the sole source; do not move or regenerate it in PR-1.",
            "p2_materialization": "Materialize internal/config/http/openapi.yaml from api/openapi.yaml atomically before every Go vet/test/race/build/run/release entrypoint. The Go embed package cannot read a parent path, so the materialized package-local view is required.",
            "p3_removal": "After clean-checkout Go and release evidence proves the view is recreated, remove only the tracked internal/config/http/openapi.yaml copy.",
        }
    if (
        "internal/webshell/static/admin_console/send_content_readonly_detail.css" in paths
        and "web/donors/ai-assistant-production/static/send_content_readonly_detail.css" in paths
    ):
        return {
            "canonical_path": "web/donor-sources/production-" + AI_PRODUCTION_FROZEN_COMMIT + "/static/send_content_readonly_detail.css",
            "canonical_lifecycle": "immutable_production_donor_source",
            "p1_binding": "Store the existing bytes once in the immutable production donor source library and bind both source identities to the same SHA-256.",
            "p2_materialization": "Materialize the frozen AI donor path and internal/webshell/static/admin_console path before build-v3-host-adapters and every Go entrypoint that embeds webshell/static; preserve the public URL and MIME checks.",
            "p3_removal": "After clean-checkout Host asset, renderer, MIME and release-stage evidence, remove only the two tracked copies and retain generated untracked views.",
        }
    logicals = sorted({logical for path in paths if (logical := logical_source_path(path))})
    relative = logicals[0].removeprefix("web/src/") if logicals else PurePosixPath(paths[0]).name
    return {
        "canonical_path": "web/donor-sources/v2-" + V2_FROZEN_COMMIT + "/web/src/" + relative,
        "canonical_lifecycle": "immutable_v2_frozen_source",
        "p1_binding": "Copy this exact SHA-256 once into the immutable v2 source library and record every current source identity and logical path in the source index; no active adapter may edit that file.",
        "p2_materialization": "Materialize each declared web/src or web/donors compatibility view by whitelist before its named freeze gate, frontend build, Host adapter build, CI artifact stage and release staging. A later active change must be a separate V3 adapter/derived source, never an edit to the view.",
        "p3_removal": "After declared consumers pass from a clean checkout with only materialized untracked views, remove only the tracked paths in this exact group.",
    }


def shared_web_entrypoints() -> list[dict[str, str]]:
    return [
        {"id": "pr01_full_source_set", "path": "scripts/check-pr01-donor-manifest.sh", "phase": "P2", "reason": "Current full web source-set and checksum gate must be changed to verify source bindings and materialized logical views without reducing its set."},
        {"id": "web_build", "path": "web/scripts/build.mjs", "phase": "P2", "reason": "Reads registry, navigation, templates and web/src entries; materialization must run before npm build."},
        {"id": "host_adapter_build", "path": "scripts/build-v3-host-adapters.mjs", "phase": "P2", "reason": "Consumes web build manifest plus frozen adapter inputs; it must receive only declared materialized views."},
        {"id": "ci_release_artifact", "path": ".github/workflows/ci.yml", "phase": "P2", "reason": "The release-artifact job currently runs frontend, Host-adapter, staging and Go steps; it must invoke the single preparation step before all consumers."},
        {"id": "release_stage", "path": "deploy/install-release.sh", "phase": "P2", "reason": "Release staging must validate declared source/view inputs rather than rely on tracked duplicate files."},
    ]


def cross_build_entrypoints(kind: str) -> list[dict[str, str]]:
    common = [
        {"id": "make_direct_go", "path": "Makefile", "phase": "P2", "reason": "make vet/test/build/run/radar-check invoke Go directly; a prerequisite must materialize and verify package-local embed views."},
        {"id": "ci_direct_go", "path": ".github/workflows/ci.yml", "phase": "P2", "reason": "CI has direct go vet/test/race/build calls; preparation cannot be limited to npm build."},
        {"id": "release_direct_go", "path": ".github/workflows/ci.yml", "phase": "P2", "reason": "Release artifact builds all Go binaries directly and must prepare views before every build."},
        {"id": "documented_bare_go", "path": "docs/engineering/dedup/", "phase": "P2", "reason": "Document and test the explicit prepare command for a developer who invokes bare go test/build; go:generate is not implicit in go test."},
    ]
    if kind == "openapi":
        return common + [
            {"id": "openapi_generator", "path": "scripts/generate-ai-assistant-client.mjs", "phase": "P2", "reason": "Reads api/openapi.yaml, which remains canonical; generated-client checks must not read the package-local embed view as a source."},
            {"id": "config_http_embed_and_test", "path": "internal/config/http/openapi.go", "phase": "P2", "reason": "//go:embed requires the package-local materialized openapi.yaml; handler_test must retain its byte comparison with api/openapi.yaml."},
        ]
    return common + [
        {"id": "webshell_embed_and_route", "path": "internal/webshell/renderer.go", "phase": "P2", "reason": "The embedded static directory must contain the materialized CSS before Go compilation; renderer/template/MIME contract remains unchanged."},
        {"id": "ai_adapter_static_copy", "path": "scripts/build-v3-host-adapters.mjs", "phase": "P2", "reason": "The AI frozen donor CSS is copied into Host staging; source index/view materialization must precede that copy."},
        {"id": "webshell_public_asset_test", "path": "internal/webshell/handler_test.go", "phase": "P2", "reason": "Public static URL and text/css assertions must pass from the materialized view."},
    ]


def relevant_static_consumers(paths: list[str], dependency_map: dict[str, Any]) -> list[dict[str, Any]]:
    records: dict[tuple[str, str, int, str], dict[str, Any]] = {}
    for path in paths:
        for item in dependency_map["targets"][path]["static_consumers"]:
            consumer = item["consumer_path"]
            if not (consumer.startswith(("scripts/", ".github/", "deploy/", "internal/", "cmd/", "web/")) or consumer in {"Makefile", "package.json"}):
                continue
            key = (consumer, item["method"], item["line"], item["evidence"])
            records[key] = item
    return sorted(records.values(), key=lambda item: (item["consumer_path"], item["line"], item["method"], item["evidence"]))


def owner_role(paths: list[str]) -> str:
    if set(paths) == {"api/openapi.yaml", "internal/config/http/openapi.yaml"}:
        return "source-governance:openapi-and-config-http"
    if any(path.startswith("internal/webshell/static/") for path in paths):
        return "source-governance:webshell-and-ai-frozen-assets"
    modules = sorted({path.split("/")[2] for path in paths if path.startswith("web/donors/")})
    if modules:
        return "source-governance:" + ",".join(modules)
    return "source-governance:v3-web-source"


def classify_exception(paths: list[str]) -> str | None:
    if any(path.startswith("migrations/") for path in paths):
        return "historical_migration_preserved"
    if set(paths) == {"api/openapi.yaml", "internal/config/http/openapi.yaml"}:
        return "cross_build_openapi_source_go_embed_and_authenticated_download_contract"
    if (
        "internal/webshell/static/admin_console/send_content_readonly_detail.css" in paths
        and "web/donors/ai-assistant-production/static/send_content_readonly_detail.css" in paths
    ):
        return "cross_host_static_asset_and_frozen_ai_donor_contract"
    if any(path.startswith("web/donors/") for path in paths):
        return "frozen_donor_lifecycle_requires_manifest_and_freeze_review"
    if any("/generated/" in path for path in paths):
        return "generated_contract_requires_generator_and_schema_provenance_review"
    if any(path.endswith(("_test.go", ".test.ts", ".test.js", ".spec.ts", ".spec.js")) for path in paths):
        return "test_execution_context_must_be_preserved"
    if any(path.startswith("docs/") for path in paths):
        return "documentation_identity_and_link_targets_require_review"
    return None


def acceptance_for_paths(paths: list[str]) -> list[str]:
    common = [
        "PR-1 source-governance binding is retained; a future exception requires an explicit named change owner",
        "all static and dynamic/build/release consumers are traced",
        "relevant characterization/build/Host checks are selected and pass",
    ]
    if set(paths) == {"api/openapi.yaml", "internal/config/http/openapi.yaml"}:
        return common + [
            "api/openapi.yaml remains the declared source for client generation and contract tests",
            "the Go embed/download path is generated or otherwise proven byte-identical before go build and release staging",
        ]
    if (
        "internal/webshell/static/admin_console/send_content_readonly_detail.css" in paths
        and "web/donors/ai-assistant-production/static/send_content_readonly_detail.css" in paths
    ):
        return common + [
            "AI frozen donor checksum and Host static asset URL/MIME checks remain exact",
        ]
    if any(path.startswith("web/donors/") for path in paths):
        return common + [
            "frozen manifest and all 20 logical PR07 files retain exact coverage",
        ]
    return common


def proposed_canonical(paths: list[str]) -> str:
    def rank(path: str) -> tuple[int, str]:
        if path.startswith("api/"):
            return (0, path)
        if path.startswith("internal/"):
            return (1, path)
        if path.startswith("web/src/"):
            return (2, path)
        if path.startswith("web/donors/"):
            return (4, path)
        return (3, path)
    return min(paths, key=rank)


def consumer_closure_id(paths: list[str]) -> str:
    if set(paths) == {"api/openapi.yaml", "internal/config/http/openapi.yaml"}:
        return "openapi_go_embed_view"
    if any(path.startswith("internal/webshell/static/") for path in paths):
        return "ai_webshell_static_view"
    return "v2_frozen_web_views"


def consumer_closures() -> dict[str, dict[str, Any]]:
    return {
        "v2_frozen_web_views": {
            "scope": "Every exact group whose copies are web/src and/or web/donors/<module>-v2/src paths.",
            "canonical_lifecycle": "immutable v2 source library with source-identity index and whitelist-only untracked views.",
            "entrypoints": shared_web_entrypoints(),
            "proof": "P2 clean checkout must run each affected module freeze gate plus web build, Host adapter build, CI artifact stage and release staging without tracked compatibility copies.",
        },
        "openapi_go_embed_view": {
            "scope": "api/openapi.yaml and internal/config/http/openapi.yaml only.",
            "canonical_lifecycle": "api source plus atomically materialized package-local Go embed view.",
            "entrypoints": cross_build_entrypoints("openapi"),
            "proof": "P2 must show byte identity, authenticated download, generator input and all direct Go entrypoints after preparation from a clean checkout.",
        },
        "ai_webshell_static_view": {
            "scope": "The frozen AI CSS and its webshell static/public asset copy only.",
            "canonical_lifecycle": "immutable production donor source plus materialized AI and Go-embedded webshell views.",
            "entrypoints": cross_build_entrypoints("webshell_css"),
            "proof": "P2 must show donor hash, adapter/stage result, Go embed, public URL/MIME and release artifact from a clean checkout.",
        },
    }


def make_decisions(duplicates: list[dict[str, Any]], dependency_map: dict[str, Any], seed_blob_ids: set[str], path_records: dict[str, dict[str, Any]], target_commit: str) -> dict[str, Any]:
    decisions = []
    for group in duplicates:
        paths = sorted(group["paths"])
        group_id = group["group_id"]
        exception = classify_exception(paths)
        consumers = sum(dependency_map["targets"][path]["static_consumer_count"] for path in paths)
        bindings = [source_binding_for_path(path, path_records[path], target_commit, group["content_sha256"]) for path in paths]
        plan = canonical_plan(paths, group["content_sha256"])
        if set(paths) == {"api/openapi.yaml", "internal/config/http/openapi.yaml"}:
            entrypoints = cross_build_entrypoints("openapi")
        elif any(path.startswith("internal/webshell/static/") for path in paths):
            entrypoints = cross_build_entrypoints("webshell_css")
        else:
            entrypoints = shared_web_entrypoints()
            for binding in bindings:
                if binding["freeze_gate"]:
                    entrypoints.append({
                        "id": "freeze_gate:" + binding["logical_path"], "path": binding["freeze_gate"], "phase": "P2",
                        "reason": "Preserve this exact source identity, checksum and logical path through the declared compatibility-view binding.",
                    })
        static_consumers = relevant_static_consumers(paths, dependency_map)
        ambiguous = sum(1 for item in static_consumers if item["confidence"] == "ambiguous_needs_owner_review")
        decisions.append({
            "group_id": group_id,
            "content_sha256": group["content_sha256"],
            "bytes_per_copy": group["bytes_per_copy"],
            "paths": paths,
            "canonical": plan,
            "owner_role": owner_role(paths),
            "owner_assignment": "PR-1 source-governance owner; module boundaries remain in each binding and gate.",
            "seed_group_05045": group_id.removeprefix("blob:") in seed_blob_ids,
            "source_version_bindings": bindings,
            "static_consumer_closure": static_consumers,
            "consumer_closure_id": consumer_closure_id(paths),
            "pipeline_entrypoints": entrypoints,
            "p0_disposition": "concrete_single_source_and_compatibility_view_plan_recorded",
            "action_in_pr1": "retain_all_paths_no_delete_no_symlink_no_import_rewrite",
            "deletion_authorized": False,
            "permanent_exception": None,
            "temporary_transition_constraint": exception,
            "static_consumer_observations": consumers,
            "ambiguous_static_mentions": ambiguous,
            "dependency_status": "static_consumer_closure_recorded; future materialization gates are enumerated",
            "remaining_nonstatic_proof": "P2 must prove the exact listed materialization order from a clean checkout; no path is exempt from that proof.",
            "acceptance_before_any_future_change": acceptance_for_paths(paths),
        })
    return {
        "schema_version": SCHEMA_VERSION,
        "policy": "PR-1 records the concrete source binding and consumer-closure route for every group. Files remain until later materialization and clean-checkout proof; no group is a deletion authorization in this PR.",
        "consumer_closures": consumer_closures(),
        "decisions": decisions,
    }


def validate_seed(seed: dict[str, Any], baseline: dict[str, Any], target: dict[str, Any]) -> list[dict[str, Any]]:
    base_by_blob = {group["group_id"].removeprefix("blob:"): group for group in baseline["duplicates"]}
    target_by_blob = {group["group_id"].removeprefix("blob:"): group for group in target["duplicates"]}
    validation = []
    for group in seed["groups"]:
        blob = group["git_blob_sha"]
        baseline_group = base_by_blob.get(blob)
        expected_paths = sorted(group["paths"])
        result = {
            "seed_group_id": group["group_id"],
            "blob_id": blob,
            "expected_bytes_per_copy": group["bytes_per_copy"],
            "expected_paths": expected_paths,
        }
        if baseline_group is None:
            result.update({"baseline_status": "missing_exact_group", "target_status": "not_compared"})
        else:
            baseline_matches = (
                baseline_group["bytes_per_copy"] == group["bytes_per_copy"]
                and sorted(baseline_group["paths"]) == expected_paths
            )
            result["baseline_status"] = "verified_exact" if baseline_matches else "different"
            result["baseline_observed_paths"] = sorted(baseline_group["paths"])
            target_group = target_by_blob.get(blob)
            if target_group is None:
                result["target_status"] = "absent_from_target_commit"
            elif target_group["bytes_per_copy"] == group["bytes_per_copy"] and sorted(target_group["paths"]) == expected_paths:
                result["target_status"] = "still_exact_same_paths"
            else:
                result["target_status"] = "present_with_path_or_size_difference"
                result["target_observed_paths"] = sorted(target_group["paths"])
        validation.append(result)
    return validation


def command_text(repo: Path, *args: str) -> str:
    return git(repo, *args).stdout.decode("utf8", "replace").strip()


def make_provenance(repo: Path, baseline: dict[str, Any], target: dict[str, Any], seed_path: Path, baseline_scan: Path, target_scan: Path) -> dict[str, Any]:
    scanner_path = Path(__file__).resolve().parent / "scan_exact_duplicates.py"
    fsck = git(repo, "fsck", "--full", "--no-reflogs", "--connectivity-only", check=False)
    partial = git(repo, "config", "--get", "extensions.partialclone", check=False)
    promisor = git(repo, "config", "--get-regexp", r"^remote\..*\.promisor$", check=False)
    if fsck.returncode:
        raise RuntimeError("git fsck failed: " + fsck.stderr.decode("utf8", "replace").strip())
    if partial.returncode == 0 or promisor.returncode == 0:
        raise RuntimeError("Refusing provenance for partial/promisor clone.")
    return {
        "schema_version": SCHEMA_VERSION,
        "audit_kind": "PR-1 all-tracked-source duplicate baseline; read-only Git-object analysis",
        "remote_origin": command_text(repo, "remote", "get-url", "origin"),
        "git_version": subprocess.check_output(["git", "--version"], text=True).strip(),
        "audit_checkout_head": command_text(repo, "rev-parse", "HEAD"),
        "audit_checkout_status_before_artifacts": command_text(repo, "status", "--porcelain"),
        "partialclone_config": None,
        "promisor_config": None,
        "git_fsck_full_connectivity": "passed",
        "baseline_commit": baseline["commit"],
        "baseline_tree": baseline["tree"],
        "target_commit": target["commit"],
        "target_tree": target["tree"],
        "seed_evidence_path": str(seed_path),
        "seed_evidence_sha256": sha256(seed_path.read_bytes()),
        "baseline_exact_scan_path": str(baseline_scan / "duplicate-audit.json"),
        "baseline_exact_scan_sha256": sha256((baseline_scan / "duplicate-audit.json").read_bytes()),
        "target_exact_scan_path": str(target_scan / "duplicate-audit.json"),
        "target_exact_scan_sha256": sha256((target_scan / "duplicate-audit.json").read_bytes()),
        "exact_scanner_path": str(scanner_path),
        "exact_scanner_sha256": sha256(scanner_path.read_bytes()),
        "artifact_generator_path": str(Path(__file__).resolve()),
        "artifact_generator_sha256": sha256(Path(__file__).read_bytes()),
    }


def make_inventory(target: dict[str, Any], baseline: dict[str, Any]) -> dict[str, Any]:
    fields = [
        "commit", "tree", "scope", "exclusions", "inventory_complete", "git_blob_verification_complete",
        "full_payload_inventory_complete", "total_entries", "total_blob_paths", "verified_blob_paths",
        "unique_blob_objects", "verified_unique_blob_objects", "errors", "submodules", "lfs_pointer_paths", "root_coverage",
    ]
    return {
        "schema_version": SCHEMA_VERSION,
        "target": {field: target[field] for field in fields},
        "baseline_reference": {field: baseline[field] for field in fields},
        "inventory": target["inventory"],
    }


def write_coverage(
    path: Path, baseline: dict[str, Any], target: dict[str, Any], seed_validation: list[dict[str, Any]],
    near: dict[str, Any], dependency_map: dict[str, Any], decisions: dict[str, Any],
) -> None:
    seed_ok = sum(item["baseline_status"] == "verified_exact" for item in seed_validation)
    target_seed_ok = sum(item.get("target_status") == "still_exact_same_paths" for item in seed_validation)
    lines = [
        "# PR-1 duplicate-source baseline coverage",
        "",
        f"Target commit: `{target['commit']}`",
        f"Target tree: `{target['tree']}`",
        f"Historical seed baseline: `{baseline['commit']}`",
        f"Historical tree: `{baseline['tree']}`",
        "",
        "## Classification",
        "",
        "- OneID / external identity: not involved; this audit reads only Git objects.",
        "- Persistence / internal tasks / provider effects: not involved; no database, queue, provider or build command ran.",
        "- PR-1 action: audit only. No tracked source was removed, linked, imported, generated or rewritten.",
        "",
        "## Required PR sequence",
        "",
        "| Stage | Status in this branch | Gate before the next stage |",
        "|---|---|---|",
        "| P0 / PR-1 baseline audit | This branch only | Full object coverage, seed verification, candidate/consumer/owner records; no unknown deletion target |",
        "| P1 / PR-2 source mechanism and pilot | Not started | Owner-approved immutable source, bindings and materialization negative cases |",
        "| P2 / PR-3 consumer/build wiring | Not started | Clean-checkout relevant entrypoints and frozen logical-file verification |",
        "| P3 / PR-4 approved payload removal | Not started | Explicit per-group approval plus pre/post behavior and freeze evidence |",
        "| P4 / PR-5 prevention/final audit | Not started | Injection gate, exception review and final before/after ledger |",
        "",
        "## Layer A: exact Git-object inventory",
        "",
        f"- Target tracked entries: **{target['total_entries']}**; blob paths verified: **{target['verified_blob_paths']}/{target['total_blob_paths']}**.",
        f"- Target unique blob objects: **{target['verified_unique_blob_objects']}/{target['unique_blob_objects']}**.",
        f"- Target exact groups: **{target['regular_duplicate_groups']}**; paths in groups: **{target['duplicate_file_paths']}**; additional logical path bytes: **{target['excess_path_bytes']:,}**.",
        f"- Baseline tracked entries: **{baseline['total_entries']}**; blob paths verified: **{baseline['verified_blob_paths']}/{baseline['total_blob_paths']}**.",
        f"- Baseline exact groups: **{baseline['regular_duplicate_groups']}**; paths in groups: **{baseline['duplicate_file_paths']}**; additional logical path bytes: **{baseline['excess_path_bytes']:,}**.",
        f"- Read errors: target `{len(target['errors'])}`, baseline `{len(baseline['errors'])}`. Target LFS pointers `{len(target['lfs_pointer_paths'])}`, submodules `{len(target['submodules'])}`.",
        "",
        "### Target top-level coverage",
        "",
        "| Root | Entries | Blobs | Verified blobs |",
        "|---|---:|---:|---:|",
    ]
    for root, data in target["root_coverage"].items():
        lines.append(f"| `{root}` | {data['entries']} | {data['blobs']} | {data['verified_blobs']} |")
    lines.extend([
        "",
        "## Seed evidence",
        "",
        f"- 05045 seed groups verified exactly: **{seed_ok}/{len(seed_validation)}**.",
        f"- Those seed groups unchanged at target: **{target_seed_ok}/{len(seed_validation)}**.",
        "- Each group/path/size result is in `exact-duplicates.json`; this is a revalidation, not deletion authorization.",
        "",
        "## Layers B and C: candidate-only analysis",
        "",
        f"- Mechanical candidates: **{len(near['mechanical']['candidates'])}**. Only line-ending and trailing-space/tab normalization were applied; comments, headers, encoding, modes and behavior remain meaningful.",
        f"- Lexical shared-block candidates: **{len(near['codeblock']['candidates'])}** across **{near['codeblock']['scanned_unique_objects']}** bounded unique UTF-8 source objects.",
        f"- C-layer exclusions: {json.dumps(near['codeblock']['skipped_unique_objects'], ensure_ascii=False, sort_keys=True)}. This is not parser, AST, type, data-flow or semantic analysis.",
        "",
        "## Consumer and decision closure",
        "",
        f"- Static target paths mapped: **{dependency_map['target_path_count']}**; readable text blobs scanned: **{dependency_map['scanned_text_file_count']}**.",
        f"- Exact decisions: **{len(decisions['decisions'])}**, all retain every path in PR-1. Each has an immutable-source or active-contract canonical plan, a source/version/hash binding per current path, a reusable consumer closure and explicit P2 entrypoints; no deletion is approved.",
        f"- Consumer closures: **{len(decisions['consumer_closures'])}** reusable families. The static map retains both resolved literals and ambiguous lexical evidence; dynamic imports, shell expansion, runtime routing, generated assets and release staging are specifically deferred to the named P2 clean-checkout entrypoints, never treated as absent consumers.",
        "",
        "## Artifacts",
        "",
        "- `inventory.json`: all target tracked entries, including binary, empty files, modes and special-entry status.",
        "- `exact-duplicates.json`: all exact groups and 39-seed verification across both pinned commits.",
        "- `near-duplicate-candidates.json`: B/C candidate methods, results and explicit limits.",
        "- `dependency-map.json`: per-target resolved and ambiguous static consumer observations, plus the bounded scanner boundary.",
        "- `dedup-decisions.json`: every exact group has a source-governance owner, concrete canonical/source-version binding, reusable consumer closure, P2 entrypoints, temporary transition constraint and retain-only action. No group has a permanent exception in PR-1.",
        "- `provenance.json`: direct GitHub clone, pinned commits/trees, object-scan inputs, tool hashes and pre-output checkout state.",
        "",
        "**Closure state: PR-1 records a concrete non-destructive source and consumer route for every exact group. It does not materialize a view, rewire a consumer, run a repository build or approve deletion. P1/P2 must prove those declared gates from a clean checkout before P3 can remove any tracked payload.**",
        "",
    ])
    path.write_text("\n".join(lines), encoding="utf8")


def make_sha_sums(out: Path) -> None:
    entries = []
    for path in sorted(out.iterdir()):
        if path.name == "SHA256SUMS" or not path.is_file():
            continue
        entries.append(f"{sha256(path.read_bytes())}  {path.name}")
    (out / "SHA256SUMS").write_text("\n".join(entries) + "\n", encoding="utf8")


def verify_artifacts(out: Path) -> None:
    required = {
        "inventory.json", "exact-duplicates.json", "near-duplicate-candidates.json", "dependency-map.json",
        "dedup-decisions.json", "coverage.md", "provenance.json", "SHA256SUMS",
    }
    present = {path.name for path in out.iterdir() if path.is_file()}
    if required - present:
        raise ValueError(f"Missing artifact(s): {sorted(required - present)}")
    inventory = load_json(out / "inventory.json")
    exact = load_json(out / "exact-duplicates.json")
    dependency = load_json(out / "dependency-map.json")
    near = load_json(out / "near-duplicate-candidates.json")
    decisions = load_json(out / "dedup-decisions.json")
    if inventory["target"]["total_entries"] != len(inventory["inventory"]):
        raise ValueError("Inventory denominator does not equal listed tracked entries.")
    groups = exact["target_exact_groups"]
    if len(groups) != len(decisions["decisions"]):
        raise ValueError("Every exact group must have one decision.")
    if {group["group_id"] for group in groups} != {entry["group_id"] for entry in decisions["decisions"]}:
        raise ValueError("Decision group IDs differ from exact group IDs.")
    closures = decisions.get("consumer_closures")
    if not isinstance(closures, dict) or not closures:
        raise ValueError("Consumer closures are missing.")
    for entry in decisions["decisions"]:
        if not entry.get("source_version_bindings"):
            raise ValueError(f"Decision lacks source/version bindings: {entry['group_id']}")
        if entry.get("consumer_closure_id") not in closures:
            raise ValueError(f"Decision has unknown consumer closure: {entry['group_id']}")
        if entry.get("deletion_authorized"):
            raise ValueError(f"PR-1 may not authorize deletion: {entry['group_id']}")
    paths = candidate_target_paths(groups, near)
    if paths != set(dependency["targets"]):
        raise ValueError("Dependency targets do not equal exact and near-candidate paths.")
    sums = {}
    for line in (out / "SHA256SUMS").read_text(encoding="utf8").splitlines():
        digest, name = line.split("  ", 1)
        sums[name] = digest
    for name, digest in sums.items():
        if sha256((out / name).read_bytes()) != digest:
            raise ValueError(f"SHA256SUMS mismatch: {name}")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("repo", type=Path, nargs="?")
    parser.add_argument("--baseline-scan", type=Path)
    parser.add_argument("--target-scan", type=Path)
    parser.add_argument("--seed", type=Path)
    parser.add_argument("--out", type=Path)
    parser.add_argument("--verify", type=Path)
    args = parser.parse_args()
    if args.verify:
        verify_artifacts(args.verify.resolve())
        print(json.dumps({"verified": str(args.verify.resolve())}, sort_keys=True))
        return 0
    if not all((args.repo, args.baseline_scan, args.target_scan, args.seed, args.out)):
        raise SystemExit("repo, --baseline-scan, --target-scan, --seed and --out are required unless --verify is used.")
    repo = args.repo.resolve()
    out = args.out.resolve()
    if out.exists():
        raise SystemExit(f"Refusing to overwrite existing output directory: {out}")
    baseline = load_json(args.baseline_scan / "duplicate-audit.json")
    target = load_json(args.target_scan / "duplicate-audit.json")
    seed = load_json(args.seed)
    if not baseline["full_payload_inventory_complete"] or not target["full_payload_inventory_complete"]:
        raise SystemExit("Exact scan is incomplete; refusing to create a baseline audit.")
    if baseline["commit"] != seed["base_commit"]:
        raise SystemExit("Seed evidence baseline does not match the supplied baseline scan.")
    target_records = target["inventory"]
    object_ids = [record["object_id"] for record in target_records if record["object_type"] == "blob"]
    contents = cat_blobs(repo, object_ids)
    mechanical = make_mechanical_candidates(target_records, contents)
    codeblock = make_codeblock_candidates(target_records, contents)
    near = {
        "schema_version": SCHEMA_VERSION,
        "target_commit": target["commit"],
        "mechanical": mechanical,
        "codeblock": codeblock,
        "limitations": [
            "No parser/AST/type/data-flow/semantic clone engine is bundled or executed in PR-1.",
            "Layer B candidates are not behavior or license equivalence and cannot be merged automatically.",
            "Layer C is bounded by source size and shingle occurrence limits; excluded objects remain explicitly unreviewed.",
            "The known same-name but different-blob admin.test.ts pair is retained as a review candidate, never treated as an exact duplicate.",
        ],
    }
    annotate_known_near_candidates(near)
    targets = candidate_target_paths(target["duplicates"], near)
    dependency = make_dependency_map(target_records, contents, targets)
    seed_validation = validate_seed(seed, baseline, target)
    seed_blob_ids = {entry["git_blob_sha"] for entry in seed["groups"]}
    path_records = {record["path"]: record for record in target_records}
    decisions = make_decisions(target["duplicates"], dependency, seed_blob_ids, path_records, target["commit"])
    inventory = make_inventory(target, baseline)
    provenance = make_provenance(repo, baseline, target, args.seed, args.baseline_scan, args.target_scan)
    exact = {
        "schema_version": SCHEMA_VERSION,
        "baseline_commit": baseline["commit"],
        "target_commit": target["commit"],
        "baseline_exact_groups": baseline["duplicates"],
        "target_exact_groups": target["duplicates"],
        "seed_metrics": seed["metrics"],
        "seed_validation": seed_validation,
    }
    out.mkdir(parents=True)
    json_dump(out / "inventory.json", inventory)
    json_dump(out / "provenance.json", provenance)
    json_dump(out / "exact-duplicates.json", exact)
    json_dump(out / "near-duplicate-candidates.json", near)
    json_dump(out / "dependency-map.json", dependency)
    json_dump(out / "dedup-decisions.json", decisions)
    write_coverage(out / "coverage.md", baseline, target, seed_validation, near, dependency, decisions)
    make_sha_sums(out)
    verify_artifacts(out)
    print(json.dumps({
        "out": str(out),
        "target_commit": target["commit"],
        "exact_groups": len(target["duplicates"]),
        "mechanical_candidates": len(mechanical["candidates"]),
        "codeblock_candidates": len(codeblock["candidates"]),
        "dependency_targets": len(dependency["targets"]),
    }, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
