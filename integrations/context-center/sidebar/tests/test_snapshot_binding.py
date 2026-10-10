import json
import pathlib
import sys
import tempfile
import types
import unittest
from unittest.mock import patch

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parents[1] / "ops"))
import sidebar_ai_reader as reader
from test_sidebar_reader import fixtures, resolve


class PublishedBindingTests(unittest.TestCase):
    def setUp(self):
        reader._load.cache_clear()

    def test_current_release_is_pinned_and_pointer_switch_is_rejected(self):
        archive, facts = fixtures()
        with tempfile.TemporaryDirectory(prefix="aicrm-reader-published-") as directory:
            root = pathlib.Path(directory)
            (root / "public/data").mkdir(parents=True)
            (root / "var/private-data").mkdir(parents=True)
            (root / "release-a").mkdir()
            (root / "release-b").mkdir()
            archive_file = root / "release-a/archive.json"
            fact_file = root / "release-a/facts.json"
            alternate = root / "release-b/archive.json"
            archive_file.write_text(json.dumps(archive))
            fact_file.write_text(json.dumps(facts))
            alternate.write_text(json.dumps({**archive, "snapshot_id": "next-fixture"}))
            archive_pointer = root / "public/data/canonical_users_live.json"
            archive_pointer.symlink_to(archive_file)
            (root / "var/private-data/crm-production-source.json").symlink_to(fact_file)
            helpers = types.SimpleNamespace(fold_source_facts=lambda value: value)
            with patch.dict(sys.modules, {"user_reference": types.SimpleNamespace(resolve_reference=resolve)}), patch.object(reader, "_existing_facts_module", return_value=helpers):
                result = reader.read_from_published(root, 42, "corp", "staff")
                self.assertEqual(result["snapshot"]["last_customer_message"]["text"], "两个晚上")
            def switching_resolve(ref, data):
                archive_pointer.unlink()
                archive_pointer.symlink_to(alternate)
                return resolve(ref, data)
            with patch.dict(sys.modules, {"user_reference": types.SimpleNamespace(resolve_reference=switching_resolve)}), patch.object(reader, "_existing_facts_module", return_value=helpers):
                with self.assertRaises(reader.ReadUnavailable) as failure:
                    reader.read_from_published(root, 42, "corp", "staff")
                self.assertEqual(failure.exception.code, "source_snapshot_changed")


if __name__ == "__main__":
    unittest.main()
