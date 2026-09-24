import os
import io
import json
import unittest
from contextlib import redirect_stdout
from unittest.mock import patch

import local_first_gate


class StagingReceiptClassificationTests(unittest.TestCase):
    def test_preflight_prd_skill_and_release_tool_checks_are_operator_only(self):
        base = "a" * 40
        with patch.dict(os.environ, {"PR_BASE_SHA": base}), patch(
            "local_first_gate.subprocess.check_output"
        ) as diff:
            diff.return_value = (
                "AGENTS.md\n"
                "docs/prd/2026-09-24-small-step-impact-checks.md\n"
                "skills/aicrm-v3-development/SKILL.md\n"
                "scripts/dev_preflight.py\n"
                "scripts/check-architecture.py\n"
            )
            self.assertFalse(local_first_gate.requires_staging_receipt("b" * 40))
            diff.return_value += "scripts/new-release-helper.py\n"
            self.assertTrue(local_first_gate.requires_staging_receipt("b" * 40))

    def test_release_metadata_is_operator_only_but_runtime_still_needs_receipt(self):
        base = "a" * 40
        with patch.dict(os.environ, {"PR_BASE_SHA": base}), patch(
            "local_first_gate.subprocess.check_output"
        ) as diff:
            for path in (
                "scripts/release_queue.py",
                "scripts/release_control.py",
                "scripts/release_coordinator.py",
                "scripts/release_events.py",
                "scripts/release_handoff.py",
                "scripts/test_release_coordinator.py",
                "scripts/domestic_release.py",
                "scripts/domestic_release_build.py",
                "scripts/test_domestic_release.py",
                "scripts/test_domestic_release_build.py",
                "deploy/domestic-promote.py",
                "deploy/domestic-release-role.production.example",
                "deploy/domestic-release-role.staging.example",
            ):
                diff.return_value = path + "\n"
                self.assertFalse(local_first_gate.requires_staging_receipt("b" * 40), path)
            diff.return_value = "internal/payment/app/service.go\n"
            self.assertTrue(local_first_gate.requires_staging_receipt("b" * 40))

    def test_targeted_runtime_pr_is_valid_code_ci_and_keeps_staging_separate(self):
        env = {"GITHUB_EVENT_NAME": "pull_request", "PR_HEAD_SHA": "b" * 40,
               "CI_NEEDS": json.dumps({"plan": {"outputs": {"mode": "targeted"}}})}
        with patch.dict(os.environ, env), patch.object(local_first_gate, "requires_staging_receipt", return_value=True), \
                patch("local_first_gate.subprocess.check_output", return_value="c" * 40), redirect_stdout(io.StringIO()) as output:
            local_first_gate.main()
        self.assertIn('"staging":"pending_release_handoff_validation"', output.getvalue())


if __name__ == "__main__":
    unittest.main()
