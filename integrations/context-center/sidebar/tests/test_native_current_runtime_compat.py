"""Run against explicitly supplied current warehouse code and synthetic assets.

No warehouse environment, published data, service module or Provider is loaded.
Missing current source is a failure, never a skip or a synthetic replacement.
"""
import copy
import importlib
import json
import os
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "ops"))
from sidebar_manual_generation import NativeEngine, generate
from test_manual_generation import fixture, request


class CurrentNativeRuntimeCompatibilityTest(unittest.TestCase):
    def setUp(self):
        code = Path(os.environ["SIDEBAR_NATIVE_RUNTIME_OPS"]).resolve()
        for name in ("prompt_runtime.py", "copy_engine_v2.py", "hxc_writing_inputs.py", "prompt_asset_versions.py"):
            self.assertTrue((code / name).is_file(), name)
        sys.path.insert(0, str(code))
        self.addCleanup(lambda: sys.path.remove(str(code)))
        self.pr = importlib.import_module("prompt_runtime")
        self.assertEqual(Path(self.pr.__file__).resolve(), code / "prompt_runtime.py")
        self.temp = tempfile.TemporaryDirectory(prefix="sidebar-current-native-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.root.chmod(0o700)
        assets = self.root / "synthetic-resources"
        assets.mkdir()
        resources = {
            "persona.json": {"id": "persona.hxc", "instructions": ["Synthetic persona"]},
            "policy.json": {"id": "policy.private-domain", "instructions": ["Synthetic facts and privacy"]},
            "context.json": {"id": "context.user", "instructions": ["Synthetic sourced context"]},
            "style.json": {"id": "style.laohuang", "instructions": ["Synthetic voice"]},
            "sales.json": {"id": "skill.synthetic-sales", "type": "scene_skill", "title": "Synthetic sales", "instructions": ["Human reviewed reply only"]},
            "examples.json": {"id": "examples.synthetic", "examples": [{"id": "synthetic-example", "text": "先确认一个具体环节。", "service_kinds": ["sales"]}]},
            "output.json": {"id": "output.synthetic", "instructions": ["Return JSON candidates"]},
            "product.json": {"id": "product.synthetic", "instructions": ["No unconfirmed price or rights"]},
            "capability-hxc-card-v1.json": {"id": "capability.synthetic", "capabilities": {}},
            "manifest.json": {"runtime_id": "synthetic-current-native", "version": "synthetic.v1", "engine": "eight_modules_v2",
                              "common": ["persona.json", "policy.json", "context.json", "style.json"],
                              "primary_skills": {"sales_assist": "sales.json"}, "compatible_skills": {"sales_assist": ["sales_assist"]},
                              "scene_routes": {"sales_assist": ["sales.json"]}, "examples": "examples.json",
                              "output_contract": "output.json", "product_support": "product.json"},
        }
        for name, value in resources.items():
            (assets / name).write_text(json.dumps(value, ensure_ascii=False))
        self.runtime = self.pr.PromptRuntime(assets)
        self.assertNotEqual(assets.resolve(), self.pr.RUNTIME_DIR.resolve())
        self.products = self.root / "synthetic-products.json"
        self.products.write_text("[]")
        self.addCleanup(patch.stopall)
        patch.dict(os.environ, {"DARU_PRODUCT_FACTS_PATH": str(self.products)}).start()

    def test_current_eight_modules_frozen_dialogue_and_restart_receipt(self):
        bundles, calls = [], []
        def bundle(data):
            result = self.runtime.build(data)
            bundles.append(copy.deepcopy(result))
            return result
        text = "先把每天盘点分成核数量和记差异两个动作。现在这两步分别由谁来做？"
        def provider(prompt):
            calls.append(prompt)
            return json.dumps({"candidates": [{"variant": "safe", "push_copy": text, "blocked": False}]}, ensure_ascii=False)
        engine = NativeEngine(self.root, bundle, provider)
        reader = lambda *scope: {"snapshot": copy.deepcopy(fixture())}
        first = generate(request(), reader=reader, engine=engine, cache_dir=self.root, enabled=True)
        restarted = NativeEngine(self.root, bundle, provider)
        repeated = generate(request(), reader=reader, engine=restarted, cache_dir=self.root, enabled=True)
        self.assertEqual(first["text"], text)
        self.assertTrue(repeated["reused"])
        self.assertFalse(first["automatic_send_allowed"])
        self.assertEqual(len(calls), 1)
        self.assertEqual(len(bundles), 1)
        self.assertEqual(bundles[0]["trace"]["engine"], "eight_modules_v2")
        self.assertEqual(len(bundles[0]["modules"]), 8)
        self.assertEqual(bundles[0]["trace"]["recent_sent_text_count"], 1)
        self.assertEqual(bundles[0]["trace"]["recent_message_coverage"], "this_employee_private_conversation_max_40")
        dynamic = json.loads(bundles[0]["modules"][3]["body"])
        summary = dynamic["unified_archive"]["archive_fields"]["historical_cleaned_summary"]
        self.assertEqual(summary["status"], "archive_summary")
        self.assertEqual(summary["state"], "historical_reference")
        stated = dynamic["unified_archive"]["conversation"]["fields"]["last_customer_message"]
        self.assertEqual(stated["status"], "user_stated")
        self.assertEqual(stated["source_refs"], ["CRM.message_archive:3"])
        for row in fixture()["messages"]:
            self.assertIn(row["text"], calls[0])
        for value in ("synthetic-corp", "synthetic-employee", request()["idempotency_key"]):
            self.assertNotIn(value, calls[0])
        self.assertEqual(bundles[0]["trace"]["approved_sales_product_keys"], [])
        receipt = next(self.root.glob("sidebar-request-*.json"))
        self.assertEqual(receipt.stat().st_mode & 0o777, 0o600)
        self.assertNotIn(fixture()["summary"], receipt.read_text())
        self.assertNotIn(fixture()["messages"][0]["text"], receipt.read_text())

    def test_current_router_missing_main_skill_stops_before_provider(self):
        self.runtime.manifest["scene_routes"]["sales_assist"] = ["policy.json"]
        calls = []
        engine = NativeEngine(self.root, self.runtime.build, lambda prompt: calls.append(prompt))
        with self.assertRaisesRegex(ValueError, "exactly_one_main_skill_required"):
            engine.generate(fixture())
        self.assertEqual(calls, [])

    def test_current_product_catalog_filters_unapproved_and_frontend_facts(self):
        self.products.write_text(json.dumps([
            {"product_key": "founder_ai_upgrade", "product_name": "Synthetic approved", "status": "confirmed_synthetic", "price_confirmed": "synthetic approved price"},
            {"product_key": "opc_fde_camp", "product_name": "Synthetic unconfirmed", "status": "draft", "price_confirmed": "unapproved price"},
            {"product_key": "unrelated", "status": "confirmed_synthetic", "price_confirmed": "unrelated price"},
        ]))
        result = self.runtime.build({"scene": "sales_assist", "product_key": "none", "product_fact": {"price_confirmed": "frontend injected price"}})
        facts = json.loads(result["modules"][5]["body"].split("\n", 1)[1])["approved_sales_products"]
        self.assertEqual([fact["product_key"] for fact in facts], ["founder_ai_upgrade"])
        self.assertEqual(result["trace"]["approved_sales_product_keys"], ["founder_ai_upgrade"])
        self.assertNotIn("frontend injected price", json.dumps(result, ensure_ascii=False))
        self.assertNotIn("unapproved price", json.dumps(result, ensure_ascii=False))


if __name__ == "__main__":
    unittest.main()
