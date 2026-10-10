"""Private, read-only CRM sidebar projection of the current published archive.

This module does not authenticate callers: the existing HTTP handler must retain
its admin/operator gate and Agent allowlist. No DB, refresh, model or send calls.
"""
from __future__ import annotations

import hashlib
import importlib.util
import json
import re
from datetime import datetime, timedelta, timezone
from functools import lru_cache
from pathlib import Path

CONTRACT = "crm-sidebar-ai-operations.v1"
SCOPE_CONTRACT = "crm-private-message-scope.v1"
FACT_CONTRACT = "crm-production-read-facts.v1"
SUMMARY_FIELDS = ("记忆摘要", "身份阶段", "行业", "业务形态", "业务阶段", "业务焦点", "关注话题", "团队规模", "收入阶段", "核心痛苦场景")


class ReadUnavailable(ValueError):
    def __init__(self, code, status=503):
        super().__init__(code)
        self.code, self.status = code, status


def _instant(value):
    try:
        result = datetime.fromisoformat(str(value).replace("Z", "+00:00"))
        if result.tzinfo is None:
            raise ValueError()
        return result.astimezone(timezone.utc)
    except (ValueError, TypeError):
        raise ReadUnavailable("source_timestamp_invalid") from None


def _id(value):
    if isinstance(value, bool) or not re.fullmatch(r"[1-9][0-9]{0,18}", str(value)):
        raise ReadUnavailable("source_identity_invalid")
    result = int(value)
    if result > 2**63 - 1:
        raise ReadUnavailable("source_identity_invalid")
    return result


def _summary(row):
    parts = []
    for field in SUMMARY_FIELDS:
        value = row.get(field)
        if isinstance(value, list) and all(isinstance(item, str) for item in value):
            value = "、".join(value)
        if isinstance(value, str) and value.strip():
            parts.append(f"{field}：{value.strip()}")
    text = "\n".join(parts)
    if len(text.encode("utf-8")) > 32000:
        raise ReadUnavailable("source_summary_too_large")
    return text


def project(archive, facts, customer_id, corp_id, employee_id, *, resolve_reference, now=None):
    """Inputs are one published release; lineage folding is owned by the existing exporter."""
    customer_id = _id(customer_id)
    if any(not isinstance(value, str) or not value or value != value.strip() or len(value) > 256 for value in (corp_id, employee_id)):
        raise ReadUnavailable("request_scope_invalid", 400)
    if (facts.get("contract") != FACT_CONTRACT or facts.get("complete") is not True or facts.get("read_only") is not True
            or facts.get("message_scope_version") != SCOPE_CONTRACT):
        raise ReadUnavailable("source_scope_unavailable")
    source_id = facts.get("source_snapshot_id")
    if (not isinstance(source_id, str) or not source_id.startswith("crm-production-")
            or archive.get("crm_source_snapshot_id") != source_id or not archive.get("snapshot_id")):
        raise ReadUnavailable("source_snapshot_mismatch")
    synced_at = _instant(facts.get("source_as_of"))
    summary_at = _instant(archive.get("generated_at"))
    now = now or datetime.now(timezone.utc)
    if max(synced_at, summary_at) > now + timedelta(minutes=2):
        raise ReadUnavailable("source_timestamp_invalid")
    # CID alias resolution is the already published CRM identity bridge. No
    # matching by display name, phone, raw UnionID or a new customer hash.
    try:
        reference = resolve_reference(f"CID-{customer_id}", archive)
    except ValueError:
        raise ReadUnavailable("source_identity_unavailable") from None
    if not isinstance(reference, str) or not re.fullmatch(r"usr_[0-9a-f]{12}", reference):
        raise ReadUnavailable("source_identity_unavailable", 404)
    matches = [row for row in archive.get("users", []) if isinstance(row, dict) and row.get("safe_user_ref") == reference]
    if len(matches) != 1 or matches[0].get("snapshot_id") != archive["snapshot_id"]:
        raise ReadUnavailable("source_identity_unavailable", 404)
    messages, seen = [], set()
    source_rows = facts.get("messages")
    if not isinstance(source_rows, list):
        raise ReadUnavailable("source_scope_unavailable")
    for row in source_rows:
        if not isinstance(row, dict):
            raise ReadUnavailable("source_scope_unavailable")
        if _id(row.get("customer_id")) != customer_id or row.get("conversation_type") != "private":
            continue
        required = {"corp_scope", "staff_provider_user_ids", "participant_count", "sender_actor_type"}
        if not required.issubset(row):
            raise ReadUnavailable("source_scope_unavailable")
        staff = row["staff_provider_user_ids"]
        if not isinstance(staff, list) or not staff or any(not isinstance(item, str) or not item for item in staff):
            raise ReadUnavailable("source_scope_unavailable")
        if not isinstance(row["corp_scope"], str) or not row["corp_scope"].startswith("wecom-corp:"):
            raise ReadUnavailable("source_scope_unavailable")
        if row["corp_scope"] != f"wecom-corp:{corp_id}" or employee_id not in staff:
            continue
        if staff != [employee_id] or row["participant_count"] != 2:
            raise ReadUnavailable("source_scope_ambiguous")
        role = row.get("participant_role")
        expected_sender = "external_customer" if role == "sender" else "staff" if role == "recipient" else None
        if expected_sender is None or row["sender_actor_type"] != expected_sender:
            raise ReadUnavailable("source_scope_ambiguous")
        message_id = str(_id(row.get("message_id")))
        if message_id in seen:
            raise ReadUnavailable("source_dialogue_duplicate")
        seen.add(message_id)
        occurred_at = _instant(row.get("occurred_at"))
        message_type = row.get("msgtype")
        if not isinstance(message_type, str) or not message_type:
            raise ReadUnavailable("source_dialogue_invalid")
        text = row.get("content_text") if message_type == "text" else ""
        if text is None:
            text = ""
        if not isinstance(text, str) or len(text.encode("utf-8")) > 16000:
            raise ReadUnavailable("source_dialogue_invalid")
        messages.append({"id": message_id, "role": "customer" if role == "sender" else "sales", "type": message_type,
                         "text": text, "occurred_at": occurred_at.isoformat()})
    # Match the existing Go consumer's deterministic string-ID tie break.
    messages.sort(key=lambda row: (_instant(row["occurred_at"]), row["id"]))
    last = next((row for row in reversed(messages) if row["role"] == "customer"), None)
    messages = messages[-40:]
    if last is not None and last not in messages:
        messages = [last, *messages[-39:]]
    snapshot = {"customer_id": customer_id, "corp_id": corp_id, "employee_id": employee_id, "summary": _summary(matches[0]),
                "summary_updated_at": summary_at.isoformat(), "synced_at": synced_at.isoformat(),
                "messages": messages, "last_customer_message": last}
    digest = hashlib.sha256(json.dumps(snapshot, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode()).hexdigest()
    snapshot["source_version"] = f"{archive['snapshot_id']}:{source_id}:{digest}"
    return {"ok": True, "contract": CONTRACT, "message_scope_version": SCOPE_CONTRACT, "snapshot": snapshot}


def _stamp(path):
    resolved = path.resolve(strict=True)
    st = resolved.stat()
    if st.st_size > 150000000:
        raise ReadUnavailable("source_file_too_large")
    return str(resolved), st.st_ino, st.st_mtime_ns, st.st_size


def _existing_facts_module():
    # This already owns canonical merge lineage in the published source facts.
    spec = importlib.util.spec_from_file_location("sidebar_existing_source_facts", Path(__file__).with_name("warehouse-source-facts.py"))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


@lru_cache(maxsize=2)
def _load(kind, stamp):
    with Path(stamp[0]).open(encoding="utf-8") as stream:
        value = json.load(stream)
    if not isinstance(value, dict):
        raise ReadUnavailable("source_snapshot_invalid")
    if kind == "archive":
        # Retain only identities, source metadata and the existing cleaned
        # fields. Full customer chat bodies do not live in this summary cache.
        value = {**{key: value.get(key) for key in ("snapshot_id", "crm_source_snapshot_id", "generated_at", "identity_aliases")},
                 "users": [{key: row.get(key) for key in ("safe_user_ref", "snapshot_id", *SUMMARY_FIELDS)}
                           for row in value.get("users", []) if isinstance(row, dict)]}
    elif kind == "facts":
        if value.get("message_scope_version") != SCOPE_CONTRACT:
            raise ReadUnavailable("source_scope_unavailable")
        folded = _existing_facts_module().fold_source_facts(value)
        value = {**{key: value.get(key) for key in ("contract", "complete", "read_only", "message_scope_version", "source_snapshot_id", "source_as_of")},
                 "messages": folded["messages"]}
    return value


def read_from_published(root, customer_id, corp_id, employee_id):
    from user_reference import resolve_reference
    root = Path(root)
    paths = (root / "public/data/canonical_users_live.json", root / "var/private-data/crm-production-source.json")
    try:
        before = tuple(_stamp(path) for path in paths)
        archive, facts = _load("archive", before[0]), _load("facts", before[1])
        result = project(archive, facts, customer_id, corp_id, employee_id, resolve_reference=resolve_reference)
        if tuple(_stamp(path) for path in paths) != before:
            raise ReadUnavailable("source_snapshot_changed", 409)
        return result
    except ReadUnavailable:
        raise
    except (OSError, ValueError, TypeError, KeyError):
        # No private source row, path, provider payload or exception text leaves
        # the existing privileged HTTP boundary.
        raise ReadUnavailable("source_snapshot_invalid") from None
