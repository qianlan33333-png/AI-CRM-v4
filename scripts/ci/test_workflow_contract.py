from pathlib import Path
import unittest


WORKFLOW = Path(__file__).resolve().parents[2] / ".github/workflows/ci.yml"


class RequiredCheckWorkflowContractTests(unittest.TestCase):
    def test_required_workflow_is_not_skipped_by_path_filters(self):
        source = WORKFLOW.read_text()
        self.assertIn("  pull_request:\n    branches: [main]", source)
        self.assertIn("  push:\n    branches: [main]", source)
        self.assertNotRegex(source, r"(?m)^\s+paths(?:-ignore)?:")

    def test_stable_check_aggregates_selected_jobs_even_when_some_are_skipped(self):
        source = WORKFLOW.read_text()
        self.assertIn("  check:\n    if: always()", source)
        self.assertIn("needs: [plan, preflight, backend, frontend, browser, archive-sdk, governance]", source)
        self.assertIn("python3 scripts/ci/verification.py gate", source)

    def test_maintainer_can_request_full_check_on_exact_selected_ref(self):
        source = WORKFLOW.read_text()
        self.assertIn("  workflow_dispatch:", source)
        self.assertIn("      force_full:", source)
        self.assertIn('"${{ inputs.force_full }}" == "true"', source)


if __name__ == "__main__":
    unittest.main()
