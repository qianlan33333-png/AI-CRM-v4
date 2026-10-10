import copy
import hashlib
import io
import json
import os
from pathlib import Path
import sys
import tempfile
import threading
import unittest
from concurrent.futures import ThreadPoolExecutor

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "ops"))
from sidebar_ai_reader import ReadUnavailable
from sidebar_manual_generation import CONTRACT, NativeEngine, generate, protected_api_once


def fixture():
    messages = [{"id": "1", "role": "customer", "type": "text", "text": "我现在准备整理门店流程", "occurred_at": "2026-10-09T02:00:00+00:00"},
                {"id": "2", "role": "sales", "type": "text", "text": "想先整理哪个具体环节？", "occurred_at": "2026-10-09T02:01:00+00:00"},
                {"id": "3", "role": "customer", "type": "text", "text": "先看每天盘点的分工", "occurred_at": "2026-10-09T02:02:00+00:00"}]
    return {"customer_id": 42, "corp_id": "synthetic-corp", "employee_id": "synthetic-employee", "source_version": "synthetic-archive:synthetic-source:content",
            "summary": "业务形态：门店；业务焦点：流程整理", "summary_updated_at": "2026-10-09T02:03:00Z", "synced_at": "2026-10-09T02:02:30Z",
            "messages": messages, "last_customer_message": messages[-1]}


def request(snapshot=None):
    return {"contract": CONTRACT, "idempotency_key": "synthetic-content-scope-key", "snapshot": copy.deepcopy(snapshot or fixture())}


class Engine:
    def __init__(self, action=None):
        self.calls = 0
        self.action = action
    def generate(self, snapshot):
        self.calls += 1
        if self.action:
            return self.action(snapshot)
        return "先把每天盘点分成核数量和记差异两个动作。现在这两步分别由谁来做？"


class ManualGenerationTest(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.cache = Path(self.directory.name)
        os.chmod(self.cache, 0o700)
        self.current = fixture()
        self.reader = lambda customer, corp, employee: {"snapshot": copy.deepcopy(self.current)}
    def tearDown(self):
        self.directory.cleanup()
    def run_generation(self, engine, data=None, **kwargs):
        return generate(data or request(), reader=self.reader, engine=engine, cache_dir=self.cache, enabled=True, **kwargs)

    def test_native_primitive_provenance_and_complete_bilateral_prompt(self):
        receipt = json.loads((Path(__file__).resolve().parents[1] / "native-migration-manifest.json").read_text())
        source = (Path(__file__).resolve().parents[1] / "ops/sidebar_native_copy.py").read_text()
        self.assertEqual(hashlib.sha256(source.encode()).hexdigest(), receipt["module_sha256"])
        import ast
        nodes = ast.parse(source).body
        for name, digest in receipt["primitives"].items():
            node = next(node for node in nodes if getattr(node, "name", None) == name or isinstance(node, ast.Assign) and any(isinstance(x, ast.Name) and x.id == name for x in node.targets))
            self.assertEqual(hashlib.sha256(ast.get_source_segment(source, node).encode()).hexdigest(), digest, name)
        captured = []
        bundle_calls = []
        def bundle(data):
            bundle_calls.append(data)
            return {"trace": {}, "relationship_context": {"recent_context": data["recent_context"], "memory_excerpt": "", "personalization_allowed": True},
                    "instructions": [], "selected_examples": [], "information_capture_plan": {}, "service_match_card": {}, "card_capability": {}, "content_fact": {}, "angle_plan": {}}
        reply = json.dumps({"candidates": [{"variant": "safe", "push_copy": "先把每天盘点分成核数量和记差异两个动作。现在这两步分别由谁来做？", "blocked": False}]}, ensure_ascii=False)
        engine = NativeEngine(self.cache, bundle, lambda prompt: captured.append(prompt) or reply)
        result = self.run_generation(engine)
        self.assertIn("盘点", result["text"])
        self.assertEqual(len(captured), 1)
        self.assertEqual(len(bundle_calls), 1)
        profile = bundle_calls[0]["profile_context"]
        self.assertEqual(profile["archive_fields"]["historical_cleaned_summary"]["status"], "archive_summary")
        self.assertEqual(profile["conversation"]["fields"]["last_customer_message"]["status"], "user_stated")
        self.assertEqual([row["text"] for row in profile["recent_correspondence"]["sent_messages"]], [fixture()["messages"][1]["text"]])
        self.assertEqual(len(profile["recent_correspondence"]["user_messages"]), 2)
        for row in fixture()["messages"]:
            self.assertIn(row["text"], captured[0])
        self.assertIn('"role":"sales"', captured[0])
        self.assertIn('"last_customer_message"', captured[0])
        self.assertNotIn("synthetic-employee", captured[0])
        self.assertNotIn("synthetic-corp", captured[0])
        self.assertNotIn("synthetic-content-scope-key", captured[0])
        self.assertFalse(result["automatic_send_allowed"])

    def test_completed_receipt_survives_restart_without_prompt_storage(self):
        first = Engine()
        result = self.run_generation(first)
        second = Engine()
        repeated = self.run_generation(second)
        self.assertEqual(first.calls, 1)
        self.assertEqual(second.calls, 0)
        self.assertEqual(repeated["text"], result["text"])
        self.assertTrue(repeated["reused"])
        receipt = next(self.cache.glob("sidebar-request-*.json"))
        self.assertEqual(receipt.stat().st_mode & 0o777, 0o600)
        text = receipt.read_text()
        for forbidden in ["synthetic-employee", "synthetic-corp", "synthetic-content-scope-key", fixture()["summary"], fixture()["messages"][0]["text"]]:
            self.assertNotIn(forbidden, text)
        self.assertEqual(set(json.loads(text)), {"contract", "binding", "status", "text"})

    def test_interruption_timeout_or_bad_result_never_retries(self):
        for failure in [SystemExit("synthetic termination"), TimeoutError("private synthetic provider detail"), ReadUnavailable("generation_invalid_result", 502)]:
            with self.subTest(type=type(failure).__name__):
                for path in self.cache.glob("*.json"):
                    path.unlink()
                def action(snapshot):
                    raise failure
                engine = Engine(action)
                with self.assertRaises((SystemExit, ReadUnavailable)):
                    self.run_generation(engine)
                restarted = Engine()
                with self.assertRaises(ReadUnavailable) as error:
                    self.run_generation(restarted)
                self.assertEqual(error.exception.code, "generation_outcome_unknown")
                self.assertEqual(restarted.calls, 0)
                self.assertNotIn("private synthetic", next(self.cache.glob("*.json")).read_text())

    def test_concurrent_duplicate_issues_one_call(self):
        entered, release = threading.Event(), threading.Event()
        def action(snapshot):
            entered.set()
            if not release.wait(3):
                raise TimeoutError()
            return "合成参考草稿"
        engine = Engine(action)
        with ThreadPoolExecutor(max_workers=2) as pool:
            original = pool.submit(self.run_generation, engine)
            self.assertTrue(entered.wait(3))
            with self.assertRaises(ReadUnavailable):
                self.run_generation(Engine())
            release.set()
            self.assertEqual(original.result(timeout=3)["text"], "合成参考草稿")
        self.assertEqual(engine.calls, 1)

    def test_changed_snapshot_scope_or_input_is_rejected_before_model(self):
        for field, value in [("customer_id", 43), ("corp_id", "other"), ("employee_id", "other"), ("summary", "changed"), ("source_version", "changed"), ("messages", [])]:
            with self.subTest(field=field):
                data = request();data["snapshot"][field] = value;engine = Engine()
                with self.assertRaises(ReadUnavailable):
                    self.run_generation(engine, data)
                self.assertEqual(engine.calls, 0)
        data = request();data["send"] = True
        with self.assertRaises(ReadUnavailable):
            self.run_generation(Engine(), data)
        with self.assertRaises(ReadUnavailable):
            generate(request(), reader=self.reader, engine=Engine(), cache_dir=self.cache)

    def test_snapshot_change_after_provider_keeps_receipt_unknown(self):
        def action(snapshot):
            self.current["source_version"] = "newly-published"
            return "stale draft must never leave service"
        with self.assertRaises(ReadUnavailable) as error:
            self.run_generation(Engine(action))
        self.assertEqual(error.exception.code, "source_snapshot_changed")
        self.assertNotIn("stale draft", next(self.cache.glob("*.json")).read_text())
        self.current = fixture()
        restarted = Engine()
        with self.assertRaises(ReadUnavailable):
            self.run_generation(restarted)
        self.assertEqual(restarted.calls, 0)

    def test_same_key_other_binding_and_equivalent_timezone(self):
        data = request();data["snapshot"]["synced_at"] = "2026-10-09T10:02:30+08:00"
        self.run_generation(Engine(), data)
        self.current["employee_id"] = "another-employee"
        data = request(self.current);engine = Engine()
        with self.assertRaises(ReadUnavailable) as error:
            self.run_generation(engine, data)
        self.assertEqual(error.exception.code, "generation_idempotency_conflict")
        self.assertEqual(engine.calls, 0)

    def test_provider_is_one_bounded_call_with_generic_failure(self):
        calls = []
        class Response(io.BytesIO):
            def __enter__(self):return self
            def __exit__(self, *args):self.close()
        class Opener:
            def open(self, req, timeout):
                calls.append((req, timeout))
                return Response(json.dumps({"choices": [{"message": {"content": "synthetic output"}}]}).encode())
        env = {"COPY_MODEL_PROVIDER": "ark_api", "ARK_API_KEY": "synthetic-test-only", "ARK_MODEL": "synthetic-model", "ARK_API_TEMPERATURE": "0.3"}
        self.assertEqual(protected_api_once("synthetic prompt", env=env, opener=Opener()), "synthetic output")
        self.assertEqual(len(calls), 1)
        req, timeout = calls[0]
        self.assertEqual(timeout, 25)
        self.assertEqual(json.loads(req.data)["temperature"], 0.3)
        self.assertNotIn("tools", json.loads(req.data))
        self.assertNotIn("synthetic-test-only", req.data.decode())
        class Failing:
            def open(self, *args, **kwargs):
                raise TimeoutError("synthetic-test-only and a private provider response")
        with self.assertRaises(ReadUnavailable) as error:
            protected_api_once("synthetic prompt", env=env, opener=Failing())
        self.assertEqual(str(error.exception), "generation_outcome_unknown")
        for configured in [{}, {**env, "COPY_MODEL_PROVIDER": "kimi_cli"}, {**env, "ARK_API_BASE_URL": "http://example.invalid"}]:
            with self.assertRaises(ReadUnavailable):
                protected_api_once("synthetic prompt", env=configured, opener=Opener())


if __name__ == "__main__":
    unittest.main()
