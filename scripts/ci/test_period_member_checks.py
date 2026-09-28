import json
import contextlib
import subprocess
import tempfile
from pathlib import Path
import unittest
from unittest.mock import patch

import period_member_checks as policy
import quality_lanes
import cmd_test_groups

ROOT = Path(__file__).resolve().parents[2]
BASE = "d0566b40a0330b12abb9cba390dc6f73319cd924"
HEAD = "c510a95b6ca52801c9561a3463c3e68c4bbc1cf9"
MODULE = "github.com/qianlan33333-png/AI-CRM-v3"


class PeriodMemberImpactTests(unittest.TestCase):
    def graph(self):
        directories = {str(Path(path).parent) for path in policy.REVIEWED_PATHS if path.endswith('.go')}
        return {"graph_valid": True, "unowned_go_paths": [], "removed_packages": [],
                "selected_packages": [{"dir": directory, "import_path": MODULE + "/" + directory}
                                      for directory in sorted(directories)]}

    def test_reviewed_read_only_diff_can_select_current_graph_without_fixed_package_count(self):
        paths = [*policy.REVIEWED_PATHS, "docs/prd/2026-09-28-period-product-member-count.md"]
        result = policy.selection(ROOT, BASE, HEAD, paths, self.graph())
        self.assertEqual(result["profile"], policy.PROFILE)
        self.assertIs(result["verified_scope"], True)
        self.assertEqual(result["lanes"], ["preflight", "backend", "frontend", "browser"])
        self.assertEqual(len([check for check in result["checks"] if check["lane"] == "browser"]), 4)

    def test_new_function_only_followup_remains_unverified(self):
        path = "internal/order/store/entitlement.go"
        old = (ROOT / path).read_text()
        new = old.replace("return counts, rows.Err()", "return counts, rows.Err() // checked", 1)
        with patch.object(policy, "_source_at", side_effect=[old, new]):
            result = policy.selection(ROOT, BASE, HEAD, [path], self.graph())
        self.assertEqual(result["profile"], policy.PROFILE)
        self.assertIs(result["verified_scope"], False)

    def test_unknown_path_graph_and_patch_all_fail_closed(self):
        paths = list(policy.REVIEWED_PATHS)
        self.assertIsNone(policy.selection(ROOT, BASE, HEAD, [*paths, "internal/payment/app/authorization.go"], self.graph()))
        invalid = self.graph(); invalid["graph_valid"] = False
        self.assertIsNone(policy.selection(ROOT, BASE, HEAD, paths, invalid))
        with patch.object(policy, "REVIEWED_DIFF_SHA256", "0" * 64):
            self.assertIsNone(policy.selection(ROOT, BASE, HEAD, paths, self.graph()))

    def test_followup_requires_only_read_only_count_function_changes(self):
        path = "internal/order/store/entitlement.go"
        source = (ROOT / path).read_text()
        old = "return counts, rows.Err()"
        self.assertIn(old, source)
        safe = source.replace(old, "return counts, rows.Err() // preserve the read-only count", 1)
        self.assertTrue(policy._safe_count_function_change(path, source, safe))
        self.assertFalse(policy._safe_count_function_change(path, source, safe + "\n// unrelated source edit\n"))
        write = source.replace(old, "tx.Exec(ctx, `DELETE FROM order_service_entitlements`)\n\treturn counts, rows.Err()", 1)
        self.assertFalse(policy._safe_count_function_change(path, source, write))

    def test_affected_backend_vets_all_packages_and_groups_cmd_without_duplicate_suite(self):
        report = Path('/tmp/period-member-evidence')
        packages = [MODULE + "/cmd/aicrm", MODULE + "/internal/order/store", MODULE + "/internal/product/http"]
        commands = quality_lanes.period_member_backend_commands(quality_lanes.commands("backend", report), packages, report)
        vet = next(command for command in commands if 'vet' in command)
        tests = [command for command in commands if 'test' in command and 'go' in command]
        grouped = next(command for command in commands if any(item.endswith('cmd_test_groups.py') for item in command))
        self.assertEqual(len(tests), 1)
        self.assertIn('./cmd/aicrm', vet)
        self.assertNotIn('./cmd/aicrm', tests[0])
        self.assertEqual(tests[0][-2:], ['./internal/order/store', './internal/product/http'])
        self.assertEqual(grouped[-1], str(report / 'cmd-test-groups'))

    def test_frontend_and_browser_require_exact_reviewed_check_set(self):
        report = Path('/tmp/period-member-evidence')
        frontend = [{"lane": "frontend", "path": path} for path in policy.FRONTEND_CHECKS]
        commands = quality_lanes.period_member_frontend_commands(report, frontend)
        self.assertEqual(commands[0], ["npm", "run", "orval:check"])
        self.assertIn(["npm", "run", "build", "--silent"], commands)
        self.assertIn(["node", "web/scripts/admin-adapter-contract.mjs"], commands)
        with self.assertRaises(ValueError):
            quality_lanes.period_member_frontend_commands(report, frontend[:-1])
        browser = [{"lane": "browser", "path": path, "test": name} for path, name in policy.BROWSER_CHECKS]
        self.assertTrue(quality_lanes.period_member_browser_commands(report, browser))
        with self.assertRaises(ValueError):
            quality_lanes.period_member_browser_commands(report, browser[:-1])

    def test_group_receipt_counts_cmd_package_once(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / 'go.mod').write_text('module example.invalid/crm\n')
            report = root / 'report'
            group_dir = report / 'cmd-test-groups'
            group_dir.mkdir(parents=True)
            (group_dir / 'summary.json').write_text(json.dumps({"status":"passed","backend_tests":3}))
            execution = {"commands": [], "required_go_packages": ["example.invalid/crm/internal/order/store"],
                         "required_packages": 1, "completed_packages": 1, "completed_tests": 2}
            command = ["python3", "scripts/ci/cmd_test_groups.py", "--mode", "run", "--report-dir", str(group_dir)]
            with patch.object(quality_lanes, "ROOT", root), \
                 patch.object(quality_lanes.check_preparation, "heavy_slot", return_value=contextlib.nullcontext({})), \
                 patch.object(quality_lanes.subprocess, "run", return_value=subprocess.CompletedProcess(command, 0)):
                quality_lanes.run_recorded(command, {}, "backend", report, execution)
            self.assertEqual(execution["required_packages"], 2)
            self.assertEqual(execution["completed_packages"], 2)
            self.assertEqual(execution["completed_tests"], 5)

    def test_group_audit_rejects_skip_duplicate_or_missing(self):
        name = 'TestBusiness'
        good = [json.dumps({"Action": "run", "Test": name}),
                json.dumps({"Action": "pass", "Test": name}), json.dumps({"Action": "pass"})]
        self.assertEqual(cmd_test_groups.audit_events(good, [name])['passed'], 1)
        for bad in (good + [good[0]], good[:1] + [json.dumps({"Action": "skip", "Test": name}), good[-1]], good[:1] + good[-1:]):
            with self.assertRaises(ValueError):
                cmd_test_groups.audit_events(bad, [name])


if __name__ == '__main__':
    unittest.main()
