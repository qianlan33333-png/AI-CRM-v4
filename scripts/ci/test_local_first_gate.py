from __future__ import annotations

import os
from unittest import TestCase, main
from unittest.mock import patch

import local_first_gate


class LocalFirstGateTests(TestCase):
    def requires_receipt_for(self, changed_paths: list[str]) -> bool:
        with patch.dict(os.environ, {"PR_BASE_SHA": "f" * 40}):
            with patch.object(
                local_first_gate.subprocess,
                "check_output",
                return_value="\n".join(changed_paths),
            ):
                return local_first_gate.requires_staging_receipt("e" * 40)

    def test_release_preflight_tools_use_operator_only_path_without_business_receipt(self):
        changed_paths = [
            "scripts/deploy-release-local.sh",
            "scripts/release_archive_preflight.py",
            "scripts/test_release_archive_preflight.py",
            "scripts/ci/quality_lanes.py",
            "scripts/ci/local_first_gate.py",
            "scripts/ci/test_local_first_gate.py",
            "docs/prd/2026-09-23-local-release-archive-layout-preflight.md",
        ]
        self.assertFalse(self.requires_receipt_for(changed_paths))

    def test_runtime_changes_still_require_business_receipt(self):
        self.assertTrue(self.requires_receipt_for([
            "scripts/release_archive_preflight.py",
            "internal/payment/app/service.go",
        ]))

    def test_invalid_base_still_fails_closed(self):
        with patch.dict(os.environ, {"PR_BASE_SHA": "invalid"}):
            with patch.object(local_first_gate.subprocess, "check_output") as check_output:
                self.assertTrue(local_first_gate.requires_staging_receipt("e" * 40))
                check_output.assert_not_called()


if __name__ == "__main__":
    main(verbosity=2)
