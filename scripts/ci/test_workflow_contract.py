from pathlib import Path
import re
import unittest


WORKFLOW = Path(__file__).resolve().parents[2] / ".github/workflows/ci.yml"


class RequiredCheckWorkflowContractTests(unittest.TestCase):
    def test_if_expressions_have_balanced_parentheses(self):
        source = WORKFLOW.read_text()
        conditions = []
        for line_number, line in enumerate(source.splitlines(), 1):
            match = re.search(r"(?:^|\s)if:\s*(.*?)\s*$", line)
            if not match:
                continue
            expression = match.group(1).strip()
            if expression.startswith("${{") and expression.endswith("}}"):
                expression = expression[3:-2]
            depth = 0
            quoted = False
            index = 0
            while index < len(expression):
                char = expression[index]
                if quoted:
                    if char == "'":
                        if index + 1 < len(expression) and expression[index + 1] == "'":
                            index += 2
                            continue
                        quoted = False
                elif char == "'":
                    quoted = True
                elif char == "(":
                    depth += 1
                elif char == ")":
                    depth -= 1
                    if depth < 0:
                        break
                index += 1
            self.assertGreater(len(expression), 0, f"workflow line {line_number} has an empty if expression")
            self.assertEqual((depth, quoted), (0, False), f"unbalanced if expression at workflow line {line_number}")
            conditions.append(line_number)
        self.assertGreater(len(conditions), 0, "workflow has no if expressions to validate")

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
        self.assertIn('verification.py plan --run-attempt "${{ github.run_attempt }}"', source)

    def test_shadow_plan_is_bounded_and_outside_the_required_gate(self):
        source = WORKFLOW.read_text()
        self.assertIn("17 19 * * *", source)  # 03:17 Asia/Shanghai
        self.assertIn("  affected-plan:\n", source)
        self.assertIn("timeout --signal=TERM 60s python3 scripts/dev_preflight.py affected", source)
        self.assertIn("GITHUB_BASE_SHA: ${{ github.event.pull_request.base.sha }}", source)
        plan = source.split("  affected-plan:\n", 1)[1].split("\n  preflight:", 1)[0]
        self.assertIn("actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e", plan)
        self.assertIn("hashFiles('go.sum')", plan)
        self.assertIn("go-v2-${{ runner.os }}-${{ runner.arch }}", plan)
        check = source.split("  check:\n", 1)[1].split("\n  affected-shadow:", 1)[0]
        self.assertNotIn("affected-shadow", check)
        self.assertNotIn("affected-plan", check)

    def test_shadow_collector_observes_original_gate_without_gating_it(self):
        source = WORKFLOW.read_text()
        shadow = source.split("  affected-shadow:\n", 1)[1].split("\n  quality-report:", 1)[0]
        self.assertIn("needs: [plan, affected-plan, preflight, backend, frontend, browser, archive-sdk, governance, check]", shadow)
        self.assertIn('--original-gate-result "${{ needs.check.result }}"', shadow)
        self.assertIn('--pr-number "${{ github.event.pull_request.number || \'0\' }}"', shadow)
        self.assertIn("exit 0", shadow)
        check = source.split("  check:\n", 1)[1].split("\n  affected-shadow:", 1)[0]
        self.assertNotIn("affected-shadow", check)

    def test_reporting_artifact_failures_do_not_change_the_required_gate(self):
        source = WORKFLOW.read_text()
        check = source.split("  check:\n", 1)[1].split("\n  affected-shadow:", 1)[0]
        self.assertEqual(check.count("continue-on-error: true"), 3)
        self.assertIn("Record exact main full regression result\n        if:", check)
        self.assertIn("Upload exact main full regression result\n        if:", check)
        self.assertIn("Record the exact tested PR tree and selection\n        if:", check)


if __name__ == "__main__":
    unittest.main()
