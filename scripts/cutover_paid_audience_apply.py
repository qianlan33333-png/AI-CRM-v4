"""Import via normal authenticated API callback; no database or credentials here.

apply(request, journal_path, target_label, reference_time) expects
request(method, path, body, headers) -> (HTTP status, decoded JSON dict).
Caller supplies ordinary admin session + CSRF, and explicitly selected target.
Journal must be retained for replay; it binds the target label and frozen defs.
"""
import hashlib
import json
import os
from pathlib import Path


def apply(request, journal_path, target_label, reference_time, rules=None):
    if not target_label:
        raise ValueError("explicit target required")
    fixtures = Path(__file__).resolve().parents[1] / "docs/migrations/fixtures"
    defaults = rules is None
    if defaults:
        names = {30: "首月体验已报名-HuangYouCan企微", 37: "报名商品编号202608121337的用户"}
        rules = {i: {"name": names[i], "definition": json.loads((fixtures / f"cutover-audience-{i}-definition.json").read_text()), "refresh_mode": "every_3m", "refresh_cron_utc": ""} for i in (30, 37)}
    if not rules or any(not isinstance(i, int) or i < 1 for i in rules):
        raise ValueError("positive source rule keys required")
    for rule in rules.values():
        if set(rule) != {"name", "definition", "refresh_mode", "refresh_cron_utc"} or not rule["name"] or not rule["definition"].get("template_key"):
            raise ValueError("explicit native name/definition/schedule required")
    definitions = {str(i): rules[i]["definition"] for i in rules}
    # Preserve all existing default journals/keys; optional rule bundles bind
    # names and schedules too, so a changed plan cannot reuse old receipts.
    fingerprint = hashlib.sha256(json.dumps(definitions if defaults else rules, sort_keys=True).encode()).hexdigest()
    path = Path(journal_path)
    journal = json.loads(path.read_text()) if path.exists() else {"target": target_label, "definitions_sha256": fingerprint, "reference_time": reference_time, "steps": {}}
    if journal["target"] != target_label or journal["definitions_sha256"] != fingerprint or journal["reference_time"] != reference_time:
        raise ValueError("journal target/definition/reference conflict")

    def save():
        tmp = path.with_suffix(path.suffix + ".tmp")
        fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        with os.fdopen(fd, "w") as out:
            json.dump(journal, out)
            out.flush()
            os.fsync(out.fileno())
        os.replace(tmp, path)

    def read(endpoint):
        status, result = request("GET", endpoint, None, {})
        if status != 200:
            raise RuntimeError(f"read failed HTTP {status}")
        return result

    def step(source, action, method, endpoint, make_body, accepted):
        name = str(source) + ":" + action
        if name not in journal["steps"]:
            key = hashlib.sha256(json.dumps([target_label, fingerprint, source, action, reference_time]).encode()).hexdigest()
            journal["steps"][name] = {"key": "cutover-paid-rule:" + key, "body": make_body(), "path": endpoint, "method": method}
            save()
        item = journal["steps"][name]
        if item["path"] != endpoint or item["method"] != method:
            raise ValueError("journal request conflict")
        if "result" not in item:
            status, result = request(method, endpoint, item["body"], {"Idempotency-Key": item["key"]})
            if status not in accepted:
                raise RuntimeError(f"{name} failed HTTP {status}; retain journal for original-key retry")
            item["result"] = result
            save()
        return item["result"]

    result = []
    for source in sorted(rules):
        created = step(source, "create", "POST", "/api/admin/ai-audience/packages", lambda: {"name": rules[source]["name"], "template_key": definitions[str(source)]["template_key"]}, (200, 201))
        target = created["package"]["id"]
        endpoint = f"/api/admin/ai-audience/packages/{target}"
        current = read(endpoint)["package"]
        if any(current.get(key) for key in ("automation_binding_id", "sender_set_id", "current_automation_binding_id", "current_sender_set_id")):
            raise ValueError("unexpected automation binding or sender set")
        step(source, "configure", "PUT", endpoint + "/configuration", lambda: {"expected_package_version": read(endpoint)["package"]["version"], "refresh_cron_utc": rules[source]["refresh_cron_utc"], "refresh_mode": rules[source]["refresh_mode"], "definition": definitions[str(source)]}, (200, 201))
        config = read(endpoint + "/configuration")["configuration"]
        if config["definition"] != definitions[str(source)] or config["refresh_mode"] != rules[source]["refresh_mode"] or config.get("refresh_cron_utc", "") != rules[source]["refresh_cron_utc"]:
            raise ValueError("configuration readback drift")
        step(source, "activate", "POST", endpoint + "/activate", lambda: {"expected_version": read(endpoint)["package"]["version"]}, (200,))
        refreshed = step(source, "refresh", "POST", endpoint + "/refresh", lambda: {"reference_time": reference_time}, (202,))
        final = read(endpoint)["package"]
        if final["lifecycle"] != "active" or any(final.get(key) for key in ("automation_binding_id", "sender_set_id", "current_automation_binding_id", "current_sender_set_id")):
            raise ValueError("activation readback mismatch")
        result.append({"source_package": source, "target_package": target, "lifecycle": "active", "refresh_run": refreshed["refresh_run"], "population_verified": False})
    return result
