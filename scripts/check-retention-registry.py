#!/usr/bin/env python3
"""Fail closed when a PostgreSQL table or retention policy is unclassified.

The checked-in registry is an explicit allowlist. This checker never infers
deletability from a table name and never executes SQL or deletes data.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import re
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
CREATE = re.compile(r'\bCREATE\s+(?:UNLOGGED\s+)?TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?(?:public\.)?"?([a-z_][a-z_0-9]*)"?\s*\(', re.I)
RIVER_TABLES = {"river_job", "river_leader", "river_migration", "river_queue", "river_client", "river_client_queue"}
POLICIES = {"protected_unclassified", "protected_mixed_payload", "permanent", "security_ttl", "temporary_upload_parts_30d", "river_terminal_jobs_30d", "owner_projection", "runtime_coordination", "operational_detail_30d", "report_payload_30d"}
OPERATIONAL_DETAILS = {"adminops_diagnostic_snapshots", "adminops_inspection_runs", "adminops_inspection_results", "adminops_diagnostic_events", "adminops_retention_runs"}
SECURITY_TTL = {"admin_sessions", "distribution_browser_sessions", "payment_h5_oauth_states", "payment_sessions", "radar_oauth_states", "radar_view_sessions", "survey_identity_sessions", "survey_oauth_states", "wecom_oauth_states"}
COORDINATION = (RIVER_TABLES - {"river_job", "river_migration"}) | {"admin_login_rate_limits", "wecom_provider_cache_metadata"}
PROJECTIONS = {"hxc_dashboard_rows", "customer_directory_projection", "customer_timeline_projection", "adminops_release_projections"}
OPERATIONAL_DETAILS |= {'config_runtime_usage', 'message_archive_sync_runs'}
SECURITY_TTL |= {'survey_result_tokens', 'ai_assistant_integration_nonces'}
SECURITY_TTL |= {'openplatform_customer_windows', 'openplatform_customer_window_items'}
COORDINATION |= {'wecom_customer_tag_refresh_watermarks', 'operation_cycle_runners', 'wecom_external_contact_event_cursors', 'referral_relationship_locks', 'message_archive_sync_state', 'referral_participation_locks', 'segment_audience_schedule_states'}
PROJECTIONS |= {'wecom_customer_owner_observations', 'wecom_group_membership_facts', 'wecom_group_provider_facts', 'group_ops_directory_groups', 'wecom_customer_tag_observations', 'group_ops_operation_member_directory', 'hxc_registration_coverage', 'wecom_external_contact_profiles'}
RESOURCE_PATH = re.compile(r'/(?:opt|etc|usr/local/libexec|var/lib|var/log|run)(?:/[A-Za-z0-9_.@+-]+)+')
DIRECTIVE_ROOTS = {"StateDirectory": "/var/lib", "LogsDirectory": "/var/log", "CacheDirectory": "/var/cache", "RuntimeDirectory": "/run"}
RUNTIME_SNAPSHOT = "internal/adminops/retention_resources.generated.json"


def coverage_items(root: Path, registry: dict) -> list[dict]:
    """Read-only coverage, never an input to a deletion executor.

    Categories come from reviewed policies; executor bindings are exact resource
    identities in the canonical registry, never a cache/log/name heuristic.
    """
    bindings = registry.get("coverage_bindings", {})
    remaining = set(bindings)
    result = []
    for section, kind in (("tables", "table"), ("resources", "resource"), ("filesystem_prefixes", "filesystem_prefix")):
        for name, item in sorted(registry[section].items()):
            policy = item["policy"]
            status, gap = "protected", ""
            if policy == "protected_unclassified":
                status, gap = "gap", "classification_unverified"
            elif policy == "protected_mixed_payload":
                status, gap = "gap", "mixed_payload_owner_split_required"
            elif policy == "security_ttl":
                status, gap = "gap", "security_ttl_physical_cleanup_missing"
            elif policy in {"runtime_coordination", "owner_projection"}:
                status = "owner_managed"
            elif policy not in {"permanent", "never_delete_by_retention"}:
                status = "binding_required"
            entry = {"kind": kind, "name": name, "owner": item["owner"], "policy": policy,
                     "reason": item["reason"], "source": item.get("source", "docs/governance/retention-registry.json#" + section + "/" + name),
                     "cleanup_entrypoint": "", "policy_id": "", "coverage_status": status,
                     "gap_code": gap, "authorization_expiry": "owner_security_ttl" if policy == "security_ttl" else "not_assessed"}
            identity = kind + ":" + name
            if identity in bindings:
                binding = bindings[identity]
                if set(binding) != {"coverage_status", "gap_code", "policy_id", "cleanup_entrypoint"}:
                    raise ValueError("invalid coverage binding fields: " + identity)
                if status == "binding_required":
                    allowed = {"policy_available", "native_unobserved", "host_unobserved"}
                elif policy in {"runtime_coordination", "owner_projection"}:
                    allowed = {"gap"}
                else:
                    allowed = set()  # business, mixed, security and unknown stay protected
                if binding["coverage_status"] not in allowed:
                    raise ValueError("invalid coverage binding policy: " + identity)
                if binding["coverage_status"] == "gap":
                    if binding["gap_code"] != "owner_cleanup_contract_missing" or binding["policy_id"] or binding["cleanup_entrypoint"]:
                        raise ValueError("invalid protected coverage gap: " + identity)
                else:
                    path, sep, symbol = binding["cleanup_entrypoint"].partition("#")
                    if not sep or not symbol or Path(path).is_absolute() or ".." in Path(path).parts or not (root / path).is_file() or symbol not in (root / path).read_text():
                        raise ValueError("coverage entrypoint missing: " + identity)
                    if binding["gap_code"] or not binding["policy_id"]:
                        raise ValueError("invalid executable coverage binding: " + identity)
                entry.update(binding)
                remaining.remove(identity)
            if entry["coverage_status"] == "binding_required":
                raise ValueError("explicit coverage binding required: " + identity)
            result.append(entry)
    if remaining:
        raise ValueError("unknown coverage binding: " + ",".join(sorted(remaining)))
    return result


def runtime_snapshot(root: Path, registry: dict, raw: bytes) -> bytes:
    value = {"registry_version": registry["version"], "registry_sha256": hashlib.sha256(raw).hexdigest(),
             "inventory_scope": "committed_registry", "items": coverage_items(root, registry)}
    return (json.dumps(value, ensure_ascii=False, indent=2) + "\n").encode()


def validate_runtime_snapshot(root: Path, registry: dict, raw: bytes) -> list[str]:
    target = root / RUNTIME_SNAPSHOT
    if not target.is_file() or target.read_bytes() != runtime_snapshot(root, registry, raw):
        return ["runtime coverage catalog drift; run python3 scripts/check-retention-registry.py --write-runtime-snapshot"]
    return []


def source_resource_paths(root: Path) -> set[str]:
    paths = set()
    for directory in ("deploy", "internal", "cmd"):
        for source in (root / directory).rglob("*"):
            if source.suffix not in {".go", ".py", ".sh", ".service", ".conf", ".example"} or source.name.endswith("_test.go") or source.name.startswith("test"):
                continue
            content = source.read_text()
            paths.update(value.rstrip(".") for value in RESOURCE_PATH.findall(content))
            for directive, prefix in DIRECTIVE_ROOTS.items():
                for value in re.findall(r"^" + directive + r"=([^\n#]+)", content, re.M):
                    paths.update(prefix + "/" + item.split(":")[0] for item in value.split())
    return paths


def validate_resources(root: Path, registry: dict, live_roots: list[str] | None = None) -> list[str]:
    prefixes = registry.get("filesystem_prefixes", {})
    errors = []
    for path, item in prefixes.items():
        if not path.startswith("/") or item.get("scope") not in {"exact", "subtree"} or not item.get("owner") or not item.get("reason") or item.get("policy") not in {"protected_unclassified", "permanent", "never_delete_by_retention", "30_days", "verified_release_allowlist"}:
            errors.append(f"invalid filesystem classification: {path}")
        if item.get("policy") == "30_days" and path not in {"/var/lib/aicrm/process-tmp", "/var/lib/aicrm/process-diagnostics"}:
            errors.append(f"unapproved filesystem deletion prefix: {path}")
    for path in sorted(source_resource_paths(root) | set(live_roots or [])):
        if not any(path == prefix or (item.get("scope") == "subtree" and path.startswith(prefix + "/")) for prefix, item in prefixes.items()):
            errors.append(f"unclassified filesystem resource: {path}")
    return errors


def source_tables(root: Path) -> dict[str, str]:
    result = {}
    sources = sorted((root / "migrations").glob("*.sql")) + [root / "cmd/migrate-platform/main.go"]
    for source in sources:
        text = re.sub(r"/\*.*?\*/", "", source.read_text(), flags=re.S)
        text = re.sub(r"--[^\n]*", "", text)
        for name in CREATE.findall(text):
            if name in result:
                raise ValueError(f"duplicate table declaration: {name}")
            result[name] = str(source.relative_to(root))
    return result


def validate(root: Path, registry: dict, live_tables: list[str] | None = None, live_roots: list[str] | None = None) -> list[str]:
    errors = []
    if registry.get("version") != 1 or registry.get("unknown_policy") != "deny_delete":
        errors.append("registry must be version 1 with unknown_policy=deny_delete")
    actual = source_tables(root)
    registered = registry.get("tables", {})
    expected = set(actual) | RIVER_TABLES
    for name in sorted(expected - registered.keys()):
        errors.append(f"unclassified table: {name}")
    for name in sorted(registered.keys() - expected):
        errors.append(f"registry source missing: {name}")
    for name, item in registered.items():
        if not item.get("owner") or not (root / "internal" / item["owner"]).is_dir():
            errors.append(f"invalid owner: {name}")
        if item.get("policy") not in POLICIES:
            errors.append(f"invalid retention policy: {name}")
        if name in actual and item.get("source") != actual[name]:
            errors.append(f"wrong source: {name}")
        if item.get("policy") == "temporary_upload_parts_30d" and name != "media_attachment_upload_parts":
            errors.append(f"unapproved temporary part deletion: {name}")
        if item.get("policy") == "river_terminal_jobs_30d" and name != "river_job":
            errors.append(f"unapproved terminal job deletion: {name}")
        if item.get("policy") == "owner_projection" and name not in PROJECTIONS:
            errors.append(f"unapproved projection retention: {name}")
        if item.get("policy") == "runtime_coordination" and name not in COORDINATION:
            errors.append(f"unapproved coordination lifecycle: {name}")
        if item.get("policy") == "security_ttl" and name not in SECURITY_TTL:
            errors.append(f"unapproved security TTL classification: {name}")
        if item.get("policy") in {"protected_unclassified", "protected_mixed_payload"} and item.get("cleanup") != "disabled":
            errors.append(f"{'unclassified' if item['policy'] == 'protected_unclassified' else 'mixed'} resource must deny cleanup: {name}")
        if item.get("cleanup") == "enabled" and not item.get("executor"):
            errors.append(f"enabled cleanup requires an owner executor: {name}")
        if item.get("policy") == "operational_detail_30d" and name not in OPERATIONAL_DETAILS:
            errors.append(f"unapproved operational detail deletion: {name}")
        if item.get("policy") == "report_payload_30d" and name != "adminops_inspection_reports":
            errors.append(f"unapproved report payload deletion: {name}")
        if not item.get("reason"):
            errors.append(f"missing retention reason: {name}")
    if live_tables is not None:
        for name in sorted(set(live_tables) - registered.keys()):
            errors.append(f"unclassified live table: {name}")
    resources = registry.get("resources", {})
    if set(resources) != {"release_directories", "process_journals", "process_files", "database_backups", "secret_store", "business_files"}:
        errors.append("resource inventory incomplete")
    if resources.get("process_journals", {}).get("namespace") != "aicrm":
        errors.append("journal retention must be limited to aicrm namespace")
    if resources.get("process_files", {}).get("roots") != ["/var/lib/aicrm/process-tmp", "/var/lib/aicrm/process-diagnostics"]:
        errors.append("process file roots must be explicitly registered")
    for name in ("database_backups", "secret_store", "business_files"):
        if resources.get(name, {}).get("policy") != "never_delete_by_retention":
            errors.append(f"protected resource policy changed: {name}")
    errors.extend(validate_resources(root, registry, live_roots))
    try:
        coverage_items(root, registry)
    except (ValueError, KeyError) as error:
        errors.append(str(error))
    return errors


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=ROOT)
    parser.add_argument("--live-tables", type=Path, help="JSON list of public table names from a read-only inventory")
    parser.add_argument("--live-roots", type=Path, help="JSON list of actual application storage roots from read-only host inventory")
    parser.add_argument("--write-runtime-snapshot", action="store_true", help="regenerate only the embedded read-only coverage catalog after successful validation")
    args = parser.parse_args()
    raw = (args.root / "docs/governance/retention-registry.json").read_bytes()
    registry = json.loads(raw)
    live = json.loads(args.live_tables.read_text()) if args.live_tables else None
    live_roots = json.loads(args.live_roots.read_text()) if args.live_roots else None
    failures = validate(args.root, registry, live, live_roots)
    if not failures:
        expected = runtime_snapshot(args.root, registry, raw)
        target = args.root / RUNTIME_SNAPSHOT
        if args.write_runtime_snapshot:
            target.write_bytes(expected)
        else:
            failures.extend(validate_runtime_snapshot(args.root, registry, raw))
    counts = {policy: sum(item["policy"] == policy for item in registry["tables"].values()) for policy in sorted(POLICIES)}
    print(json.dumps({"ok": not failures, "registered_tables": len(registry["tables"]), "classification_counts": counts, "errors": failures}, ensure_ascii=False))
    return 1 if failures else 0


if __name__ == "__main__":
    raise SystemExit(main())
