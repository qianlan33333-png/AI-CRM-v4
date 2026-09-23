import os
import unittest
from unittest.mock import patch

import local_first_gate


class StagingReceiptClassificationTests(unittest.TestCase):
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
            ):
                diff.return_value = path + "\n"
                self.assertFalse(local_first_gate.requires_staging_receipt("b" * 40), path)
            diff.return_value = "internal/payment/app/service.go\n"
            self.assertTrue(local_first_gate.requires_staging_receipt("b" * 40))


if __name__ == "__main__":
    unittest.main()
