"""Contracts for exact-source check plans and short-list selection."""
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import affected_plan


class AffectedPlanTest(unittest.TestCase):
    def repo(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        root = Path(temporary.name)
        files = {
            "go.mod": "module example.invalid/crm\n\ngo 1.26\n",
            "docs/prd/example.md": "before\n",
            "scripts/ci/quality_lanes.py": "# baseline runner\n",
            "scripts/ci/test_contract.py": "import unittest\nclass Contract(unittest.TestCase): pass\n",
            "internal/catalog/catalog.go": "package catalog\nfunc Name() string { return \"catalog\" }\n",
            "internal/catalog/catalog_test.go": "package catalog\nfunc TestCatalogName(t *testing.T) {}\n",
            "internal/order/order.go":
                'package order\nimport "example.invalid/crm/internal/catalog"\nvar _ = catalog.Name()\n',
            "internal/order/order_test.go": "package order\nfunc TestOrder(t *testing.T) {}\n",
        }
        for relative, content in files.items():
            target = root / relative
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_text(content)
        for command in (["git", "init", "-q"], ["git", "config", "user.email", "test@example.invalid"],
                        ["git", "config", "user.name", "test"], ["git", "add", "."],
                        ["git", "commit", "-qm", "base"]):
            subprocess.run(command, cwd=root, check=True)
        base = self.sha(root)
        (root / "docs/prd/example.md").write_text("after\n")
        self.commit(root, "documentation")
        return root, base, self.sha(root)

    @staticmethod
    def sha(root):
        return subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip()

    @staticmethod
    def commit(root, message):
        subprocess.run(["git", "add", "-A"], cwd=root, check=True)
        subprocess.run(["git", "commit", "-qm", message], cwd=root, check=True)

    def test_documentation_plan_keeps_receipt_fields_without_building_graph(self):
        root, base, head = self.repo()
        with patch.object(affected_plan.go_affected_graph, "build_graph",
                          side_effect=AssertionError("default planning must not build the dependency graph")):
            plan = affected_plan.build_plan(root, base, head)
        self.assertTrue(plan["evidence_eligible"])
        self.assertEqual(plan["baseline_sha"], base)
        self.assertEqual(plan["head_sha"], head)
        self.assertEqual(plan["candidate"]["profile"], "documentation")
        self.assertEqual(plan["graph_result"]["proof"], "registered_docs_or_tooling_only")
        self.assertEqual(plan["candidate_go_packages"], [])

    def test_candidate_manifest_beats_policy_and_graph_fallbacks(self):
        root, _, baseline = self.repo()
        target = root / "scripts/ci/quality_lanes.py"
        target.write_text("# modified runner contract\n")
        manifest = {
            "baseline_sha": baseline,
            "checks": [{"lane": "preflight", "path": "scripts/ci/test_contract.py"}],
            "installed_checks": ["periodic-contact", {"name": "questionnaire-recovery"}],
            "build_commands": ["./cmd/aicrm"],
        }
        (root / "docs/release-checks.json").write_text(json.dumps(manifest))
        self.commit(root, "runner and selected checks")
        head = self.sha(root)
        with patch.object(affected_plan.go_affected_graph, "build_graph",
                          side_effect=AssertionError("AI list must bypass graph")), \
                patch.object(affected_plan, "analyze_trusted_base",
                             side_effect=AssertionError("AI list must bypass governance mapping")):
            plan = affected_plan.build_plan(root, baseline, head, graph_result={"invalid": True})
        self.assertTrue(plan["policy_changed"])
        self.assertEqual(plan["candidate"]["profile"], "ai-selected")
        self.assertEqual(plan["selection_source"], "local-ai-check-list")
        self.assertEqual(plan["selected_checks"], manifest["checks"])
        self.assertEqual(plan["candidate_go_packages"], [])
        self.assertEqual(plan["installed_checks"], ["periodic-contact", "questionnaire-recovery"])
        self.assertEqual(plan["build_commands"], ["./cmd/aicrm"])
        self.assertEqual(plan["graph_result"]["proof"], "local_ai_check_list")

    def test_dirty_or_wrong_head_source_fails_explicitly(self):
        root, base, head = self.repo()
        (root / "docs/prd/example.md").write_text("uncommitted\n")
        with self.assertRaisesRegex(ValueError, "clean working tree"):
            affected_plan.build_plan(root, base, head)
        (root / "docs/prd/example.md").write_text("after\n")
        with self.assertRaisesRegex(ValueError, "head to be checked out"):
            affected_plan.build_plan(root, base, base)

    def test_unknown_runtime_requires_a_list_instead_of_full_fallback(self):
        root, _, base = self.repo()
        (root / "new-runtime.toml").write_text("unknown = true\n")
        self.commit(root, "unknown runtime")
        with self.assertRaisesRegex(ValueError, "docs/release-checks.json is required"):
            affected_plan.build_plan(root, base, self.sha(root))

    def test_missing_manifest_selects_changed_and_one_hop_go_packages(self):
        root, _, base = self.repo()
        target = root / "internal/catalog/catalog.go"
        target.write_text('package catalog\nfunc Name() string { return "updated" }\n')
        self.commit(root, "catalog change")
        head = self.sha(root)
        with patch.object(affected_plan.go_affected_graph, "build_graph",
                          side_effect=AssertionError("light selector must not build the dependency graph")):
            plan = affected_plan.build_plan(root, base, head)
        self.assertEqual(plan["candidate"]["profile"], "ai-selected")
        self.assertEqual(plan["selection_source"], "adjacent-behavior-checks")
        packages = {check["package"] for check in plan["selected_checks"]}
        self.assertEqual(packages, {"./internal/catalog", "./internal/order"})
        self.assertEqual(plan["candidate_go_packages"], [])


if __name__ == "__main__":
    unittest.main()
