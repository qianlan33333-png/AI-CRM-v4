from __future__ import annotations

import json
from pathlib import Path
import subprocess
import tempfile
import unittest

import ai_check_plan


class AICheckPlanTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        for command in (["git", "init", "-q"], ["git", "config", "user.name", "Fixture"],
                        ["git", "config", "user.email", "fixture@example.invalid"]):
            subprocess.run(command, cwd=self.root, check=True)
        self.write("internal/product/app/product.go", "package app\nfunc Create() {}\n")
        self.write("internal/product/app/product_test.go",
                   "package app\nfunc TestCreateProduct(t *testing.T) {}\n")
        self.write("cmd/aicrm/referral_chromium_test.go",
                   "package main\nfunc TestReferralChromiumJourney(t *testing.T) {}\n")
        self.write("web/v3/product.test.mjs", "// selected node contract\n")
        self.write("scripts/ci/test_contract.py", "import unittest\nclass Contract(unittest.TestCase): pass\n")
        self.write("scripts/test-check.sh", "#!/bin/sh\nexit 0\n")
        self.commit("base")
        self.base = self.sha()

    def write(self, path, content):
        target = self.root / path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(content)

    def commit(self, label):
        subprocess.run(["git", "add", "-A"], cwd=self.root, check=True)
        subprocess.run(["git", "commit", "-qm", label], cwd=self.root, check=True)

    def sha(self):
        return subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=self.root, text=True).strip()

    def commit_manifest(self, manifest):
        self.write(ai_check_plan.MANIFEST_PATH, json.dumps(manifest))
        self.commit("manifest")
        return self.sha()

    def test_manifest_reads_candidate_tree_and_normalizes_checks_and_names(self):
        head = self.commit_manifest({
            "baseline_sha": self.base,
            "checks": [
                {"lane": "backend", "path": "internal/product/app/product_test.go",
                 "package": "./internal/product/app", "test": "TestCreateProduct", "postgres": True},
                {"lane": "frontend", "path": "web/v3/product.test.mjs"},
                {"lane": "preflight", "path": "scripts/ci/test_contract.py"},
                {"lane": "preflight", "path": "scripts/test-check.sh"},
                {"lane": "browser", "path": "cmd/aicrm/referral_chromium_test.go",
                 "test": "TestReferralChromiumJourney"},
            ],
            "installed_checks": ["periodic-contact", {"name": "questionnaire-recovery"}],
            "build_commands": ["./cmd/aicrm"],
        })
        (self.root / ai_check_plan.MANIFEST_PATH).write_text("invalid local edit is ignored")

        plan = ai_check_plan.load_manifest(self.root, self.base, head)

        backend = plan["checks"][0]
        self.assertEqual(backend["package"], "./internal/product/app")
        self.assertTrue(backend["postgres"])
        self.assertTrue(backend["fresh"])
        self.assertEqual(plan["installed_checks"], ["periodic-contact", "questionnaire-recovery"])
        self.assertEqual(plan["build_commands"], ["./cmd/aicrm"])
        self.assertEqual(ai_check_plan.selection(plan)["profile"], "ai-selected")
        self.assertEqual(ai_check_plan.selection(plan)["selection_source"], "local-ai-check-list")

    def test_stale_baseline_is_rejected(self):
        head = self.commit_manifest({"baseline_sha": "1" * 40,
                                     "checks": [{"lane": "preflight", "path": "scripts/ci/test_contract.py"}]})
        with self.assertRaisesRegex(ai_check_plan.CheckPlanError, "stale AI check list"):
            ai_check_plan.load_manifest(self.root, self.base, head)

    def test_invalid_json_and_unsupported_command_fields_are_rejected(self):
        self.write(ai_check_plan.MANIFEST_PATH, "{")
        self.commit("bad json")
        with self.assertRaisesRegex(ai_check_plan.CheckPlanError, "invalid .*release-checks.json"):
            ai_check_plan.load_manifest(self.root, self.base, self.sha())

        head = self.commit_manifest({"checks": [{"lane": "preflight", "path": "scripts/test-check.sh",
                                                  "command": "rm -rf /"}]})
        with self.assertRaisesRegex(ai_check_plan.CheckPlanError, "unsupported check fields"):
            ai_check_plan.load_manifest(self.root, self.base, head)

    def test_unsafe_or_missing_paths_and_invalid_go_tests_are_rejected(self):
        unsafe = {"checks": [{"lane": "preflight", "path": "../scripts/test-check.sh"}]}
        head = self.commit_manifest(unsafe)
        with self.assertRaisesRegex(ai_check_plan.CheckPlanError, "safe repository-relative"):
            ai_check_plan.load_manifest(self.root, self.base, head)

        missing = {"checks": [{"lane": "preflight", "path": "scripts/missing.py"}]}
        head = self.commit_manifest(missing)
        with self.assertRaisesRegex(ai_check_plan.CheckPlanError, "does not exist"):
            ai_check_plan.load_manifest(self.root, self.base, head)

        absent_test = {"checks": [{"lane": "backend", "path": "internal/product/app/product_test.go",
                                   "test": "TestNotDefined"}]}
        head = self.commit_manifest(absent_test)
        with self.assertRaisesRegex(ai_check_plan.CheckPlanError, "not defined"):
            ai_check_plan.load_manifest(self.root, self.base, head)

    def test_package_globs_and_wrong_directories_are_rejected(self):
        for package in ("./internal/...", "./internal/payment"):
            with self.subTest(package=package):
                head = self.commit_manifest({"checks": [{"lane": "backend",
                    "path": "internal/product/app/product.go", "package": package}]})
                with self.assertRaises(ai_check_plan.CheckPlanError):
                    ai_check_plan.load_manifest(self.root, self.base, head)

    def test_backend_package_only_checks_resolve_to_a_real_go_file(self):
        head = self.commit_manifest({"checks": [
            {"lane": "backend", "package": "./internal/product/app", "test": "TestCreateProduct"},
            {"lane": "backend", "package": "./internal/product/app"},
        ]})
        plan = ai_check_plan.load_manifest(self.root, self.base, head)
        self.assertEqual(plan["checks"][0]["path"], "internal/product/app/product_test.go")
        self.assertEqual(plan["checks"][0]["package"], "./internal/product/app")
        self.assertEqual(plan["checks"][0]["test"], "TestCreateProduct")
        self.assertEqual(plan["checks"][1]["path"], "internal/product/app/product.go")

    def test_installed_names_must_be_short_lowercase_slugs(self):
        head = self.commit_manifest({"checks": [{"lane": "preflight", "path": "scripts/test-check.sh"}],
                                     "installed_checks": ["Periodic Contact"]})
        with self.assertRaisesRegex(ai_check_plan.CheckPlanError, "lowercase short names"):
            ai_check_plan.load_manifest(self.root, self.base, head)


if __name__ == "__main__":
    unittest.main()
