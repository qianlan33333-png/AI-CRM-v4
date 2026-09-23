import os
from pathlib import Path
import subprocess
import unittest
from unittest.mock import patch

import quality_lanes


class QualityLaneTests(unittest.TestCase):
    def test_all_ci_lanes_have_one_canonical_command_definition(self):
        for lane in quality_lanes.LANES:
            commands = quality_lanes.commands(lane, Path("/tmp/evidence"))
            self.assertTrue(commands, lane)
            self.assertTrue(all(isinstance(command, list) and command for command in commands), lane)

    def test_focused_backend_runs_only_registered_package_tests(self):
        checks = [
            {"lane": "backend", "path": "internal/media/app/image_upload_test.go",
             "test": "TestUploadActorScopedReplayConflictAndRollback"},
            {"lane": "browser", "path": "cmd/aicrm/media_refresh_chromium_postgres_integration_test.go",
             "test": "TestPostgreSQLMediaRefreshChromiumJourney"},
        ]
        commands = quality_lanes.focused_commands("backend", Path("/tmp/evidence"), checks)
        self.assertEqual(len(commands), 1)
        self.assertEqual(commands[0][-1], "./internal/media/app")
        self.assertIn("-race", commands[0])
        self.assertIn("-count=1", commands[0])
        self.assertEqual(commands[0][commands[0].index("-run") + 1],
                         "^(TestUploadActorScopedReplayConflictAndRollback)$")
        self.assertNotIn("./...", commands[0])

    def test_focused_browser_runs_only_the_registered_journey(self):
        check = {"lane": "browser", "path": "cmd/aicrm/media_refresh_chromium_postgres_integration_test.go",
                 "test": "TestPostgreSQLMediaRefreshChromiumJourney"}
        command = quality_lanes.focused_commands("browser", Path("/tmp/evidence"), [check])[0]
        self.assertIn("--journey", command)
        self.assertIn(check["test"], command)
        self.assertNotIn("--group", command)

    def test_focused_frontend_runs_only_registered_scripts(self):
        checks = [
            {"lane": "frontend", "path": "web/scripts/ui-shell-contract.mjs"},
            {"lane": "browser", "path": "cmd/aicrm/component_states_chromium_postgres_integration_test.go",
             "test": "TestPostgreSQLComponentStatesChromiumJourney"},
        ]
        self.assertEqual(quality_lanes.focused_commands("frontend", Path("/tmp/evidence"), checks),
                         [["node", "web/scripts/ui-shell-contract.mjs"]])

    def test_unsupported_focused_mapping_falls_back_to_full_lane(self):
        commands = quality_lanes.focused_commands(
            "backend", Path("/tmp/evidence"),
            [{"lane": "backend", "path": "internal/media/app/service.go", "test": "TestUnsafe.*"}],
        )
        self.assertIn("./...", commands[-1])

    def test_backend_keeps_full_race_coverage_with_bounded_package_timeout(self):
        commands = quality_lanes.commands("backend", Path("/tmp/evidence"))
        test_command = next(command for command in commands if "go" in command and "test" in command)
        self.assertIn("-race", test_command)
        self.assertIn("-count=1", test_command)
        self.assertIn("-timeout=15m", test_command)
        self.assertEqual(test_command[-1], "./...")
        self.assertNotIn("-run", test_command)

    def test_database_lanes_require_reachable_postgresql_16(self):
        with patch.object(quality_lanes, "command_available", return_value=True), patch.object(
                quality_lanes, "exact_version", return_value=True), patch.object(
                quality_lanes, "postgres_16_ready", return_value=False), patch.object(
                quality_lanes, "chromium_font_ready", return_value=True):
            self.assertIn("PostgreSQL 16 reachable through AICRM_DATABASE_URL",
                          quality_lanes.missing_prerequisites("backend"))
            self.assertIn("PostgreSQL 16 reachable through AICRM_DATABASE_URL",
                          quality_lanes.missing_prerequisites("browser"))

    def test_browser_requires_chromium_but_frontend_does_not(self):
        def available(name):
            return name != "google-chrome"
        with patch.object(quality_lanes, "command_available", side_effect=available), patch.object(
                quality_lanes, "exact_version", return_value=True), patch.object(
                quality_lanes, "postgres_16_ready", return_value=True), patch.object(
                quality_lanes, "chromium_font_ready", return_value=True):
            self.assertIn("google-chrome", quality_lanes.missing_prerequisites("browser"))
            self.assertNotIn("google-chrome", quality_lanes.missing_prerequisites("frontend"))

    def test_postgres_check_does_not_print_database_url(self):
        with patch.dict(os.environ, {"AICRM_DATABASE_URL": "postgres://secret@127.0.0.1/aicrm_test_secret"}), patch.object(
                quality_lanes, "command_available", return_value=True), patch("subprocess.run") as run:
            run.return_value.returncode = 1
            self.assertFalse(quality_lanes.postgres_16_ready())
            self.assertNotIn("postgres://secret", run.call_args.args[0])

    def test_postgres_rejects_remote_or_shared_database_without_connecting(self):
        for url in ("postgres://user:pass@10.0.0.2:5432/aicrm_test_isolated", "postgres://user:pass@127.0.0.1:5432/aicrm_shared"):
            with self.subTest(url=url), patch.dict(os.environ, {"AICRM_DATABASE_URL": url}), patch.object(
                    quality_lanes, "command_available", return_value=True), patch("subprocess.run") as run:
                self.assertFalse(quality_lanes.postgres_16_ready())
                run.assert_not_called()

    def test_version_match_is_exact_token_not_substring(self):
        with patch.object(quality_lanes, "command_available", return_value=True), patch("subprocess.run") as run:
            run.return_value.returncode = 0
            run.return_value.stdout = "go version go1.26.60 linux/amd64\n"
            self.assertFalse(quality_lanes.exact_version(["go", "version"], "go1.26.6"))

    def test_dispatch_dedup_baseline_falls_back_to_checked_out_head_parent(self):
        head = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=quality_lanes.ROOT, text=True).strip()
        with patch.dict(os.environ, {"AICRM_DEDUP_HEAD_SHA": head, "AICRM_DEDUP_BASE_SHA": ""}, clear=False):
            self.assertTrue(quality_lanes.dedup_base_ready())


if __name__ == "__main__":
    unittest.main()
