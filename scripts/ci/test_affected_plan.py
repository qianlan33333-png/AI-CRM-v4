"""Contracts for exact-source binding and fail-closed shadow impact plans."""
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
        root = Path(directory.name)
        (root / "docs/prd").mkdir(parents=True)
        (root / "docs/prd/example.md").write_text("before\n")
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

    def test_plan_schema_binds_commit_trees_and_both_policy_fingerprints(self):
        directory, root, base, head = self.repo()
        with directory, patch.object(affected_plan, "analyze_head", return_value=self.report(["docs/prd/example.md"])):
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
        self.assertIsNone(plan["graph_result"])
        self.assertTrue(plan["evidence_eligible"])

    def test_dirty_tree_cannot_produce_narrow_candidate_evidence(self):
        directory, root, base, head = self.repo()
        with directory:
            (root / "docs/prd/example.md").write_text("uncommitted\n")
            plan = affected_plan.build_plan(root, base, head)
        self.assertFalse(plan["source"]["working_tree_clean"])
        self.assertFalse(plan["evidence_eligible"])
        self.assertEqual(plan["candidate"]["selection_mode"], "full")
        self.assertEqual(plan["candidate"]["selection_reasons"], ["dirty-working-tree"])
        self.assertEqual(plan["enforced"]["selection_mode"], "full")

    def test_unchecked_graph_result_is_full_and_ineligible_until_validated(self):
        directory, root, base, head = self.repo()
        report = self.report(["docs/prd/example.md"])
        with directory, patch.object(affected_plan, "analyze_head", return_value=report):
            plan = affected_plan.build_plan(root, base, head, graph_result={"status": "complete"})
        self.assertFalse(plan["evidence_eligible"])
        self.assertEqual(plan["candidate"]["selection_mode"], "full")
        self.assertEqual(plan["candidate"]["selection_reasons"], ["unvalidated-graph-result"])

    def test_unmapped_path_selection_is_full_even_on_clean_source(self):
        directory, root, base, head = self.repo()
        report = {"risk": "low", "changed_paths": ["new-runtime.toml"], "direct": [], "checks": []}
        with directory, patch.object(affected_plan, "analyze_head", return_value=report):
            plan = affected_plan.build_plan(root, base, head)
        self.assertTrue(plan["evidence_eligible"])
        self.assertEqual(plan["candidate"]["selection_mode"], "full")
        self.assertEqual(plan["candidate"]["selected_lanes"], list(impact_selection.LANES))


if __name__ == "__main__":
    unittest.main()
