#!/usr/bin/env python3
"""Offline proof derivation; no network/database calls and no identity writes."""
import argparse
import base64
import hashlib
import json
import os
import stat
from cryptography.hazmat.primitives.ciphers.aead import AESGCM


def private_read(path):
    info = os.lstat(path)
    if not stat.S_ISREG(info.st_mode) or stat.S_IMODE(info.st_mode) != 0o600:
        raise ValueError("regular 0600 input required")
    with open(path, "rb") as stream:
        return stream.read()


def exclusive(path, value):
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "wb") as stream:
        stream.write(value)
        stream.flush()
        os.fsync(stream.fileno())


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--capture", required=True)
    parser.add_argument("--key", required=True)
    parser.add_argument("--expected-sha256", required=True)
    parser.add_argument("--corp-env", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--output-key", required=True)
    args = parser.parse_args()
    blob = private_read(args.capture)
    if hashlib.sha256(blob).hexdigest() != args.expected_sha256:
        raise ValueError("frozen encrypted source digest mismatch")
    raw_key = private_read(args.key).strip()
    key = base64.b64decode(raw_key + b"=" * (-len(raw_key) % 4), validate=True)
    magic = b"AICRM-COMMERCE-CUTOVER-RAW-1\n"
    if not blob.startswith(magic):
        raise ValueError("invalid source envelope")
    start = len(magic)
    capture = json.loads(AESGCM(key).decrypt(blob[start:start+12], blob[start+12:], magic))
    corp = os.environ.get(args.corp_env)
    if not corp:
        raise ValueError("confirmed target corp required")
    relations = {}
    for row in capture["tables"]["wecom_external_contact_identity_map"]:
        if row.get("corp_id") != corp:
            continue
        raw = row.get("raw_profile") or {}
        contact = raw.get("external_contact") or {}
        valid = type(raw.get("errcode")) in (int, float) and raw["errcode"] == 0 and isinstance(contact.get("unionid"), str) and isinstance(contact.get("external_userid"), str)
        evidence = {"map_id": row["id"], "provider_ok": valid, "corp_id": corp, "external_id": row.get("external_userid") or "", "unionid": row.get("unionid") or "", "status": row.get("status") or "", "raw_unionid": contact.get("unionid") if isinstance(contact.get("unionid"), str) else "", "raw_external_id": contact.get("external_userid") if isinstance(contact.get("external_userid"), str) else ""}
        relations.setdefault((row.get("unionid"), row.get("external_userid")), []).append(evidence)
    rows = []
    for row in sorted(capture["tables"]["crm_user_identity"], key=lambda row: row["unionid"]):
        opaque, external = row["unionid"], row.get("primary_external_userid") or ""
        rows.append({"unionid": opaque, "crm_status": row.get("identity_status") or "", "primary_external_id": external, "evidence": sorted(relations.get((opaque, external), []), key=lambda row: row["map_id"])})
    proof = {"version": 2, "resolution_mode": "existing_wecom_only", "scopes": {"corp_id": corp, "union_scope": ""}, "captured_at": capture["snapshot_at"], "rows": rows}
    raw = json.dumps(proof, separators=(",", ":"), ensure_ascii=False).encode()
    magic, nonce = b"AICRM-CUTOVER-IDENTITY-PROOF-V1\0", os.urandom(12)
    exclusive(args.output, magic + nonce + AESGCM(key).encrypt(nonce, raw, magic))
    exclusive(args.output_key, base64.b64encode(key).rstrip(b"="))
    print(json.dumps({"protected_rows": len(rows), "source_sha256": args.expected_sha256, "identity_writes": 0, "provider_calls": 0}))


if __name__ == "__main__":
    try:
        main()
    except Exception:
        raise SystemExit("frozen proof derivation failed; no identities were written")
