"""Contracts for exact-source and trusted-policy affected shadow plans."""
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import affected_plan
import impact_selection


class AffectedPlanTest(unittest.TestCase):
    def repo(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        root = Path(directory.name)
        (root / "docs/prd").mkdir(parents=True)
        (root / "docs/prd/example.md").write_text("before\n")
        (root / "docs/operations").mkdir(parents=True)
        (root / "docs/operations/domestic-release.md").write_text("release guide before\n")
        (root / "docs/development-before-start.md").write_text("development guide before\n")
        (root / "skills/aicrm-v3-development-frontdoor").mkdir(parents=True)
        (root / "skills/aicrm-v3-development-frontdoor/SKILL.md").write_text("frontdoor skill before\n")
        (root / "skills/aicrm-v3-development").mkdir(parents=True)
        (root / "skills/aicrm-v3-development/SKILL.md").write_text("skill before\n")
        for command in (
            ["git", "init", "-q"],
            ["git", "config", "user.email", "test@example.invalid"],
            ["git", "config", "user.name", "test"],
            ["git", "add", "."],
            ["git", "commit", "-qm", "base"],
        ):
            subprocess.run(command, cwd=root, check=True)
        base = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip()
        (root / "docs/prd/example.md").write_text("after\n")
        subprocess.run(["git", "add", "."], cwd=root, check=True)
        subprocess.run(["git", "commit", "-qm", "head"], cwd=root, check=True)
        head = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip()
        return directory, root, base, head

    @staticmethod
    def report(paths):
        return {"risk": "low", "changed_paths": paths, "direct": [], "affected": [], "checks": []}

    @staticmethod
    def graph_for(root, base, head):
        base_sha = affected_plan.resolve_commit(root, base)
        head_sha = affected_plan.resolve_commit(root, head)
        return {
            "schema": 1,
            "baseline_sha": base_sha,
            "baseline_tree": affected_plan.tree_for(root, base_sha),
            "head_sha": head_sha,
            "head_tree": affected_plan.tree_for(root, head_sha),
            "changed_paths": affected_plan.changed_paths(root, base, head),
            "graph_valid": True,
            "unowned_go_paths": [],
            "selected_packages": [],
            "graph_fingerprint": "f" * 64,
        }

    def test_plan_schema_binds_commit_trees_policy_and_package_graph(self):
        directory, root, base, head = self.repo()
        with patch.object(affected_plan, "analyze_trusted_base",
                          return_value=self.report(["docs/prd/example.md"])):
            plan = affected_plan.build_plan(root, base, head)
        self.assertEqual(plan["schema"], 1)
        self.assertEqual(plan["mode"], "shadow")
        self.assertEqual(plan["observed_mode"], "shadow")
        self.assertEqual(plan["baseline_sha"], base)
        self.assertEqual(plan["head_sha"], head)
        self.assertEqual(len(plan["baseline_tree"]), 40)
        self.assertEqual(len(plan["head_tree"]), 40)
        self.assertEqual(len(plan["policy_fingerprint"]), 64)
        self.assertEqual(set(plan["policy_fingerprints"]), {"planner", "repository_head"})
        self.assertEqual(plan["candidate"]["selected_lanes"], ["preflight"])
        self.assertEqual(plan["candidate"]["selected_checks"], [])
        self.assertEqual(plan["candidate"]["selection_reasons"], ["documentation-only"])
        self.assertEqual(plan["enforced"]["selected_lanes"], ["preflight"])
        self.assertEqual(plan["graph_result"]["status"], "not_required")
        self.assertEqual(plan["graph_result"]["proof"], "registered_docs_or_tooling_only")
        self.assertTrue(plan["evidence_eligible"])

    def test_dirty_tree_cannot_produce_narrow_candidate_evidence(self):
        directory, root, base, head = self.repo()
        (root / "docs/prd/example.md").write_text("uncommitted\n")
        plan = affected_plan.build_plan(root, base, head)
        self.assertFalse(plan["source"]["working_tree_clean"])
        self.assertFalse(plan["evidence_eligible"])
        self.assertEqual(plan["candidate"]["selection_mode"], "full")
        self.assertEqual(plan["candidate"]["selection_reasons"], ["dirty-working-tree"])
        self.assertEqual(plan["enforced"]["selection_mode"], "full")

    def test_unbound_graph_result_fails_closed(self):
        directory, root, base, head = self.repo()
        with patch.object(affected_plan, "analyze_trusted_base",
                                     return_value=self.report(["docs/prd/example.md"])):
            plan = affected_plan.build_plan(root, base, head, graph_result={"status": "complete"})
        self.assertFalse(plan["evidence_eligible"])
        self.assertEqual(plan["candidate"]["selection_mode"], "full")
        self.assertEqual(plan["candidate"]["selection_reasons"], ["missing-or-unsupported-package-graph"])

    def test_graph_changed_paths_or_unowned_go_files_fail_closed(self):
        directory, root, base, head = self.repo()
        graph_result = self.graph_for(root, base, head)
        graph_result["changed_paths"] = ["wrong.go"]
        with patch.object(affected_plan, "analyze_trusted_base",
                                     return_value=self.report(["docs/prd/example.md"])):
            plan = affected_plan.build_plan(root, base, head, graph_result=graph_result)
        self.assertFalse(plan["evidence_eligible"])
        self.assertEqual(plan["candidate"]["selection_reasons"], ["package-graph-changed-paths-mismatch"])

        graph_result = self.graph_for(root, base, head)
        graph_result["graph_valid"] = False
        graph_result["unowned_go_paths"] = ["internal/new/file.go"]
        with directory, patch.object(affected_plan, "analyze_trusted_base",
                                     return_value=self.report(["docs/prd/example.md"])):
            plan = affected_plan.build_plan(root, base, head, graph_result=graph_result)
        self.assertFalse(plan["evidence_eligible"])
        self.assertEqual(plan["candidate"]["selection_reasons"], ["package-graph-has-unowned-go-paths"])

    def test_unmapped_path_selection_is_full_even_on_clean_source(self):
        directory, root, base, head = self.repo()
        report = {"risk": "low", "changed_paths": ["new-runtime.toml"], "direct": [], "checks": []}
        graph_result = self.graph_for(root, base, head)
        with patch.object(affected_plan, "analyze_trusted_base", return_value=report), \
                patch.object(affected_plan.go_affected_graph, "build_graph", return_value=graph_result):
            plan = affected_plan.build_plan(root, base, head)
        self.assertTrue(plan["evidence_eligible"])
        self.assertEqual(plan["candidate"]["selection_mode"], "full")
        self.assertEqual(plan["candidate"]["selected_lanes"], list(impact_selection.LANES))

    def test_payment_leaf_gets_bounded_package_and_browser_candidate(self):
        graph_result = {"selected_packages": [{"import_path": "m/internal/payment/app"},
                                               {"import_path": "m/cmd/aicrm"}]}
        candidate, packages = affected_plan.package_candidate(
            {"risk": "high", "direct": ["commerce"], "checks": [
                {"lane": "backend", "path": "internal/product/app/unrelated_test.go", "test": "TestUnrelated"},
                {"lane": "browser", "path": "cmd/aicrm/old_test.go", "test": "TestOldChromiumJourney"},
            ]}, graph_result, ["internal/payment/app/state.go"], policy_changed=False)
        self.assertEqual(candidate["mode"], "targeted")
        self.assertEqual(candidate["lanes"], ["preflight", "backend", "browser"])
        self.assertEqual(packages, ["m/cmd/aicrm", "m/internal/payment/app"])
        self.assertEqual(candidate["checks"], [affected_plan.PAYMENT_BROWSER_CHECK])

    def test_policy_change_uses_full_trusted_fallback(self):
        candidate, packages = affected_plan.package_candidate(
            self.report(["scripts/ci/impact_selection.py"]), {"selected_packages": []},
            ["scripts/ci/impact_selection.py"], policy_changed=True)
        self.assertEqual(candidate["mode"], "full")
        self.assertEqual(candidate["reason"], "policy-change-requires-full")
        self.assertEqual(packages, [])

    def test_skills_and_tooling_guide_stay_light_but_remain_fingerprinted(self):
        directory, root, base, previous_head = self.repo()
        (root / "skills/aicrm-v3-development/SKILL.md").write_text("skill after\n")
        (root / "skills/aicrm-v3-development-frontdoor/SKILL.md").write_text("frontdoor skill after\n")
        (root / "docs/development-before-start.md").write_text("development guide after\n")
        subprocess.run(["git", "add", "."], cwd=root, check=True)
        subprocess.run(["git", "commit", "-qm", "update skills and tooling guide"], cwd=root, check=True)
        head = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip()
        paths = affected_plan.changed_paths(root, base, head)
        self.assertIn("skills/aicrm-v3-development/SKILL.md", paths)
        self.assertIn("skills/aicrm-v3-development-frontdoor/SKILL.md", paths)
        self.assertIn("docs/development-before-start.md", paths)
        with patch.object(affected_plan, "analyze_trusted_base", return_value=self.report(paths)):
            plan = affected_plan.build_plan(root, base, head)
        self.assertTrue(plan["evidence_eligible"])
        self.assertEqual(plan["candidate"]["selection_mode"], "targeted")
        self.assertEqual(plan["candidate"]["selected_lanes"], ["preflight"])
        self.assertEqual(plan["candidate"]["profile"], "documentation")
        self.assertEqual(plan["graph_result"]["status"], "not_required")
        # The existing trusted selector still sees the mixed skill/documentation
        # commit as full; the candidate is observed without changing that gate.
        self.assertEqual(plan["enforced"]["selection_mode"], "full")
        self.assertNotEqual(affected_plan.repository_policy_fingerprint(root, base),
                            affected_plan.repository_policy_fingerprint(root, head))


if __name__ == "__main__":
    unittest.main()
