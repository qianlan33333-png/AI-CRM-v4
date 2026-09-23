import importlib.util
from pathlib import Path
import json
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]


def load():
    spec = importlib.util.spec_from_file_location("receipt", ROOT / "scripts" / "validate-staging-receipt.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class StagingReceiptTests(unittest.TestCase):
    def test_receipt_binds_head_tree_and_package(self):
        checker = load()
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            package = root / "package.tar.gz"
            package.write_bytes(b"package")
            receipt = root / "receipt.json"
            receipt.write_text(json.dumps({
                "schema": 1, "repository": "AI-CRM-v4", "environment": "staging",
                "status": "accepted", "commit_sha": "a" * 40, "tree_sha": "b" * 40,
                "package_sha256": __import__("hashlib").sha256(b"package").hexdigest(),
            }))
            value = checker.load(receipt)
            self.assertEqual(value["commit_sha"], "a" * 40)
            self.assertEqual(value["tree_sha"], "b" * 40)

    def test_rejects_unaccepted_receipt(self):
        checker = load()
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp) / "receipt.json"
            path.write_text(json.dumps({"schema": 1, "environment": "staging", "status": "queued"}))
            with self.assertRaisesRegex(ValueError, "missing repository"):
                checker.load(path)

    def test_rejects_legacy_repository(self):
        checker = load()
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp) / "receipt.json"
            path.write_text(json.dumps({
                "schema": 1, "repository": "AI-CRM-v3", "environment": "staging",
                "status": "accepted", "commit_sha": "a" * 40,
                "tree_sha": "b" * 40, "package_sha256": "c" * 64,
            }))
            with self.assertRaisesRegex(ValueError, "AI-CRM-v4"):
                checker.load(path)


if __name__ == "__main__":
    unittest.main()
