import copy
import json
import pathlib
import sys
import unittest
from datetime import datetime, timezone, timedelta

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parents[1] / "ops"))
from sidebar_ai_reader import project, ReadUnavailable, SCOPE_CONTRACT, FACT_CONTRACT
from refresh_diagnostics import checked_step, StepFailure, failure_reason
import subprocess


def fixtures():
    archive = {"generated_at": "2026-10-09T01:16:21+00:00", "snapshot_id": "workbench-fixture",
               "crm_source_snapshot_id": "crm-production-fixture", "identity_aliases": {"CID-42": "usr_000000000042"},
               "users": [{"safe_user_ref": "usr_000000000042", "snapshot_id": "workbench-fixture", "记忆摘要": "希望安排时间"}]}
    facts = {"contract": FACT_CONTRACT, "complete": True, "read_only": True, "message_scope_version": SCOPE_CONTRACT,
             "source_snapshot_id": "crm-production-fixture", "source_as_of": "2026-10-09T01:15:27+00:00", "messages": []}
    for mid, role, text in [(1, "recipient", "每周可投入多久？"), (2, "sender", "两个晚上")]:
        facts["messages"].append({"customer_id": 42, "message_id": mid, "occurred_at": f"2026-10-09T01:0{mid}:00+00:00",
                                  "content_text": text, "msgtype": "text", "participant_role": role, "conversation_type": "private",
                                  "corp_scope": "wecom-corp:corp", "staff_provider_user_ids": ["staff"], "participant_count": 2,
                                  "sender_actor_type": "staff" if role == "recipient" else "external_customer"})
    return archive, facts


def resolve(ref, archive):
    # Production calls the existing owned user_reference.resolve_reference.
    return archive["identity_aliases"].get(ref, ref)


def read(archive, facts):
    return project(archive, facts, 42, "corp", "staff", resolve_reference=resolve,
                   now=datetime(2026, 10, 10, tzinfo=timezone.utc))


class ReaderTests(unittest.TestCase):
    def test_bilateral_scope_skips_other_customer_employee_corp_and_group(self):
        archive, facts = fixtures()
        base = facts["messages"][1]
        for i, patch in enumerate([{"customer_id": 43}, {"staff_provider_user_ids": ["other-staff"]},
                                   {"corp_scope": "wecom-corp:other-corp"}, {"conversation_type": "group"}], 10):
            facts["messages"].append({**base, **patch, "message_id": i, "content_text": "must not leak"})
        result = read(archive, facts)["snapshot"]
        self.assertEqual([m["role"] for m in result["messages"]], ["sales", "customer"])
        self.assertEqual(result["last_customer_message"]["text"], "两个晚上")
        self.assertNotIn("must not leak", json.dumps(result))
        self.assertEqual(result["synced_at"], "2026-10-09T01:15:27+00:00")

    def test_rejects_missing_scope_ambiguous_private_identity_and_mixed_release(self):
        changes = [lambda a, f: f.pop("message_scope_version"),
                   lambda a, f: f["messages"][0].pop("corp_scope"),
                   lambda a, f: f["messages"][0].update(staff_provider_user_ids=["staff", "other"]),
                   lambda a, f: f["messages"][0].update(participant_count=3),
                   lambda a, f: f["messages"][0].update(sender_actor_type="robot"),
                   lambda a, f: f["messages"].append(copy.deepcopy(f["messages"][0])),
                   lambda a, f: a.update(crm_source_snapshot_id="different-source"),
                   lambda a, f: a["identity_aliases"].clear(),
                   lambda a, f: f.update(complete=False),
                   lambda a, f: f.update(source_as_of="2026-10-09T01:15:27"),
                   lambda a, f: f.update(source_as_of="2026-10-11T01:15:27+00:00")]
        for change in changes:
            with self.subTest(change=changes.index(change)):
                archive, facts = fixtures()
                change(archive, facts)
                with self.assertRaises(ReadUnavailable):
                    read(archive, facts)

    def test_nontext_is_not_transcribed_and_every_frozen_input_changes_version(self):
        archive, facts = fixtures()
        old = read(archive, facts)["snapshot"]["source_version"]
        facts["messages"][0]["content_text"] = "员工迟到的补充"
        self.assertNotEqual(old, read(archive, facts)["snapshot"]["source_version"])
        facts["messages"][1].update(msgtype="image", content_text="must not become a fabricated transcription")
        last = read(archive, facts)["snapshot"]["last_customer_message"]
        self.assertEqual(last["type"], "image")
        self.assertEqual(last["text"], "")

    def test_keeps_last_customer_after_many_sales_replies_and_matches_go_tie_break(self):
        archive, facts = fixtures()
        base = facts["messages"][0]
        for i in range(3, 48):
            facts["messages"].append({**base, "message_id": i, "occurred_at": "2026-10-09T01:10:00+00:00"})
        result = read(archive, facts)["snapshot"]
        self.assertEqual(len(result["messages"]), 40)
        self.assertEqual(result["last_customer_message"]["id"], "2")
        self.assertIn(result["last_customer_message"], result["messages"])
        archive, facts = fixtures()
        facts["messages"] = [{**facts["messages"][1], "message_id": x} for x in [9, 10]]
        self.assertEqual(read(archive, facts)["snapshot"]["last_customer_message"]["id"], "9")


class DiagnosticsTests(unittest.TestCase):
    def test_private_scope_and_bridge_failures_have_fixed_codes(self):
        for error, code in [("production_message_scope_unavailable", "production_private_scope_unavailable"),
                            ("production_read_bridge_not_configured", "crm_read_bridge_configuration_missing"),
                            ("production_source_stale", "production_source_contract_unavailable")]:
            self.assertEqual(failure_reason("synthetic private detail " + error), code)

    def test_records_safe_failure_category_and_never_retains_stderr(self):
        private = "private-pass-token-and-chat-must-not-leak"
        def runner(*args, **kwargs):
            return subprocess.CompletedProcess(args[0], 1, "", f"{private}\nPermission denied (publickey).")
        with self.assertRaises(StepFailure) as failure:
            checked_step(["python3", "/fixture/hxc-sync.py"], cwd="/fixture", runner=runner)
        self.assertEqual(failure.exception.receipt["reason_code"], "upstream_authentication_failed")
        self.assertNotIn(private, str(failure.exception))
        self.assertNotIn(private, json.dumps(failure.exception.receipt))

    def test_timeout_and_unknown_failure_do_not_claim_a_root_cause(self):
        def timeout(*args, **kwargs):
            raise subprocess.TimeoutExpired(args[0], 1, stderr="private secret")
        with self.assertRaises(StepFailure) as failure:
            checked_step(["python3", "/fixture/hxc-sync.py"], cwd="/fixture", runner=timeout)
        self.assertEqual(failure.exception.receipt["reason_code"], "step_timeout")
        def unknown(*args, **kwargs):
            return subprocess.CompletedProcess(args[0], 1, "private secret", "unknown private user record")
        with self.assertRaises(StepFailure) as failure:
            checked_step(["python3", "/fixture/hxc-sync.py"], cwd="/fixture", runner=unknown)
        self.assertEqual(failure.exception.receipt["reason_code"], "upstream_failure_unclassified")


if __name__ == "__main__":
    unittest.main()
