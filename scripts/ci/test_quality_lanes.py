import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch
import contextlib
import io
import json
import sys

import quality_lanes


class QualityLaneTests(unittest.TestCase):
    def test_timing_fingerprint_requires_exact_hosted_runner_image(self):
        base = {"GITHUB_ACTIONS": "true", "RUNNER_OS": "Linux", "RUNNER_ARCH": "X64"}
        with patch.dict(os.environ, base, clear=True), patch.object(
                quality_lanes.subprocess, "check_output", return_value="tool version\n"):
            environment, cache = quality_lanes._measurement_fingerprints()
        self.assertTrue(environment)
        self.assertIsNone(cache)

        pinned = {**base, "ImageOS": "ubuntu22", "ImageVersion": "202609.1"}
        with patch.dict(os.environ, pinned, clear=True), patch.object(
                quality_lanes.subprocess, "check_output", return_value="tool version\n"):
            environment, cache = quality_lanes._measurement_fingerprints()
        self.assertTrue(environment)
        self.assertTrue(cache)

    def test_all_ci_lanes_have_one_canonical_command_definition(self):
        for lane in quality_lanes.LANES:
            commands = quality_lanes.commands(lane, Path("/tmp/evidence"))
            self.assertTrue(commands, lane)
            self.assertTrue(all(isinstance(command, list) and command for command in commands), lane)

    def test_tooling_profile_runs_release_contracts_without_application_builds(self):
        commands = quality_lanes.tooling_contract_commands()
        rendered = " ".join(" ".join(command) for command in commands)
        self.assertIn("scripts/ci", rendered)
        self.assertIn("test_domestic_release", rendered)
        self.assertIn("test_domestic_promote", rendered)
        self.assertNotIn("go test", rendered)
        self.assertNotIn("npm", rendered)
        self.assertNotIn("Chromium", rendered)

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

    def test_invalid_focus_configuration_writes_failed_lane_receipt(self):
        with tempfile.TemporaryDirectory() as temp:
            report_dir = Path(temp)
            argv = ["quality_lanes.py", "backend", "--report-dir", str(report_dir),
                    "--focus-packages-json", "{}"]
            with patch.object(sys, "argv", argv), patch.object(quality_lanes, "missing_prerequisites", return_value=[]), \
                    contextlib.redirect_stderr(io.StringIO()):
                self.assertEqual(quality_lanes.main(), 2)
            receipt = json.loads((report_dir / "run.json").read_text())
            self.assertEqual(receipt["result"], "failed")
            self.assertEqual(receipt["exit_code"], 2)
            self.assertGreater(receipt["elapsed_seconds"], 0)
            self.assertEqual(receipt["commands"], [])

    def test_reused_report_directory_does_not_mix_old_json_events_or_receipt(self):
        with tempfile.TemporaryDirectory() as temp, contextlib.redirect_stdout(io.StringIO()):
            report_dir = Path(temp)
            (report_dir / "backend-go-test.jsonl").write_text('{"Test":"OLD_COMMIT"}\n')
            (report_dir / "run.json").write_text('{"result":"success","tested_sha":"old"}\n')
            commands = [["go", "test", "-json", "first-run"], ["go", "test", "-json", "second-run"]]

            def record_command(command, env, lane, directory, execution):
                execution["commands"].append(command)
                log = directory / (lane + "-go-test.jsonl")
                with log.open("a", encoding="utf-8") as output:
                    output.write(command[-1] + "\n")
                execution["go_json_log"] = log.name

            with patch.object(quality_lanes, "missing_prerequisites", return_value=[]), \
                    patch.object(quality_lanes, "commands", side_effect=[[[*commands[0]]], [[*commands[1]]]]), \
                    patch.object(quality_lanes, "run_recorded", side_effect=record_command), \
                    patch.object(quality_lanes, "_run_policy_fingerprint", return_value="f" * 64):
                for _ in range(2):
                    with patch.object(sys, "argv", ["quality_lanes.py", "backend", "--report-dir", str(report_dir)]):
                        self.assertEqual(quality_lanes.main(), 0)

            self.assertEqual((report_dir / "backend-go-test.jsonl").read_text(), "second-run\n")
            receipt = json.loads((report_dir / "run.json").read_text())
            self.assertEqual(receipt["result"], "success")
            self.assertEqual(receipt["commands"], [commands[1]])


if __name__ == "__main__":
    unittest.main()
