"""Validate this preparation receipt batch without building or installing CRM."""
from __future__ import annotations

import json
import os
from pathlib import Path
import subprocess
import sys
import unittest

ROOT = Path(__file__).resolve().parents[3]
DIRECTORY = Path(__file__).resolve().parent
PREFIX = DIRECTORY.relative_to(ROOT).as_posix()
CHECK_PATH = PREFIX + "/test_preparation_contract.py"
BASELINE = "62d6ef5e0b153bea66ca9646429bfd992a7f42a7"
DOCUMENTS = (
    "ICP备案接入准备.md",
    "certificate-preparation-readback.json",
    "excel-preparation-test-readback.json",
    "icp-preparation-readback.json",
    "issued-certificate-readback.json",
    "provider-network-readback.json",
    "sdk-preparation-readback.json",
    "source-current-health-readback.json",
    "source-network-specification-readback.json",
    "tencent-readback.json",
    "volcengine-readback.json",
    "workbench-preparation-readback.json",
    "迁移前置准备清单.md",
    "迁移方案与PRD.md",
)
sys.path[:0] = [str(ROOT / "scripts"), str(ROOT / "scripts/ci")]
import ai_check_plan
import domestic_release_build as builder


class PreparationDocumentContract(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.head = subprocess.check_output(
            ["git", "rev-parse", "HEAD"], cwd=ROOT, text=True
        ).strip()

    def test_documents_are_regular_utf8_and_receipts_are_json_objects(self):
        for name in DOCUMENTS:
            with self.subTest(document=name):
                path = DIRECTORY / name
                self.assertTrue(path.is_file())
                self.assertFalse(path.is_symlink())
                content = path.read_text(encoding="utf-8")
                self.assertTrue(content.strip())
                if path.suffix == ".json":
                    self.assertIsInstance(json.loads(content), dict)

    def test_candidate_checklist_binds_only_this_document_contract(self):
        self.assertEqual(os.environ.get("AICRM_DEDUP_BASE_SHA", BASELINE), BASELINE)
        manifest = ai_check_plan.load_manifest(ROOT, BASELINE, self.head)
        self.assertIsNotNone(manifest)
        self.assertEqual(manifest["baseline_sha"], BASELINE)
        self.assertEqual(manifest["checks"], [{"lane": "preflight", "path": CHECK_PATH}])
        self.assertEqual(manifest["installed_checks"], [])
        self.assertEqual(manifest["build_commands"], [])

    def test_document_json_and_checklist_do_not_select_application_install(self):
        result = builder.classify(ROOT, BASELINE, self.head)
        expected = {PREFIX + "/" + name for name in DOCUMENTS}
        expected.update({CHECK_PATH, ai_check_plan.MANIFEST_PATH})
        self.assertEqual(set(result["changed_paths"]), expected)
        self.assertFalse(result["runtime_changed"])
        self.assertFalse(result["full_build"])
        self.assertEqual(result["build_mode"], "none")
        self.assertEqual(result["controller_files"], [])
        # A real application change must still select runtime work.
        mixed = builder.classify_paths([PREFIX + "/icp-preparation-readback.json",
                                        "cmd/aicrm/main.go"])
        self.assertTrue(mixed.runtime_changed)


if __name__ == "__main__":
    unittest.main()
