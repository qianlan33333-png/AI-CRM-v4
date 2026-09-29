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
    def test_commerce_preparation_retains_source_guards_whole_packages_and_new_journeys(self):
        import commerce_checks
        report = Path("/tmp/evidence")
        preflight = quality_lanes.commerce_commands("preflight", report, [])
        self.assertEqual(preflight[0], quality_lanes.commands("preflight", report)[0])
        self.assertEqual(preflight[-1], ["bash", "scripts/audit/check-dedup-base-diff.sh", "."])
        backend = quality_lanes.commerce_commands("backend", report, [])
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp); (root / "go.mod").write_text("module example/crm\n")
            packages = ["example/crm/cmd/aicrm", "example/crm/internal/product/http"]
            with patch.object(quality_lanes, "ROOT", root):
                focused = quality_lanes.replace_full_backend_test_with_packages(backend, packages)
        self.assertEqual(focused[0][-1], "stage")
        self.assertEqual(focused[1][-2:], ["./cmd/aicrm", "./internal/product/http"])
        self.assertEqual(focused[2][-2:], ["./cmd/aicrm", "./internal/product/http"])
        for flag in ("-race", "-count=1", "-p"):
            self.assertIn(flag, focused[2])
        self.assertNotIn("-run", focused[2])
        checks = [{"lane": "browser", "path": path, "test": name}
                  for path, name in commerce_checks.JOURNEY_FILES.items()]
        browser = quality_lanes.commerce_commands("browser", report, checks)
        self.assertEqual(len(browser), 2)
        for name in commerce_checks.JOURNEY_FILES.values():
            self.assertIn(name, browser[-1])
        added = {"lane": "browser", "path": checks[0]["path"], "test": "TestNewAuthorizationChromiumJourney"}
        browser = quality_lanes.commerce_commands("browser", report, [*checks, added])
        self.assertEqual(len(browser), 5)
        self.assertIn("TestNewAuthorizationChromiumJourney", browser[-1])
        self.assertIn("openpyxl", browser[2][-1])
        self.assertEqual(len(quality_lanes.commands("preflight", report)), 10)
        self.assertEqual(len(quality_lanes.commands("backend", report)), 6)
        with self.assertRaises(ValueError):
            quality_lanes.commerce_commands("frontend", report, [])

    def test_first_actual_go_test_run_publishes_runtime_before_test_completion(self):
        with tempfile.TemporaryDirectory() as temp, contextlib.redirect_stdout(io.StringIO()):
            root = Path(temp); report = root / "report"
            execution = {"commands": []}
            def lines():
                yield json.dumps({"Action": "start", "Package": "example/a"}) + "\n"
                self.assertEqual(json.loads((report / "progress.json").read_text())["started_tests"], 0)
                yield json.dumps({"Action": "run", "Package": "example/a", "Test": "TestBusiness"}) + "\n"
                progress = json.loads((report / "progress.json").read_text())
                self.assertEqual(progress["started_tests"], 1)
                self.assertEqual(progress["completed_tests"], 0)
                yield json.dumps({"Action": "pass", "Package": "example/a", "Test": "TestBusiness"}) + "\n"
                yield json.dumps({"Action": "pass", "Package": "example/a"}) + "\n"
            stream = unittest.mock.MagicMock()
            stream.__iter__.side_effect = lines
            process = unittest.mock.Mock(stdout=stream)
            process.wait.return_value = 0
            with patch.object(quality_lanes, "ROOT", root), \
                 patch.object(quality_lanes.subprocess, "check_output", return_value="example/a\n"), \
                 patch.object(quality_lanes.subprocess, "Popen", return_value=process):
                quality_lanes.run_recorded(["go", "test", "-json", "-race", "-count=1", "./a"], None,
                                           "backend", report, execution)
            self.assertEqual(execution["started_tests"], 1)
            self.assertEqual(execution["completed_tests"], 1)

    def test_go_continuation_runs_complete_unfinished_packages_and_retains_suite_flags(self):
        with tempfile.TemporaryDirectory() as temp, contextlib.redirect_stdout(io.StringIO()):
            root = Path(temp)
            module = "example.invalid/crm"
            (root / "go.mod").write_text("module " + module + "\n")
            report = root / "report"
            required = [module + "/a", module + "/b", module + "/c"]
            process = unittest.mock.Mock()
            process.stdout = io.StringIO(json.dumps({"Action":"pass", "Package":required[1]}) + "\n")
            process.wait.return_value = 0
            execution = {"commands":[], "resume_go_packages":[required[0], required[2]]}
            command = ["go", "test", "-json", "-p", "1", "-race", "-count=1", "-timeout=30m", "./..."]
            with patch.object(quality_lanes, "ROOT", root), \
                 patch.object(quality_lanes.subprocess, "check_output", return_value="\n".join(required)), \
                 patch.object(quality_lanes.subprocess, "Popen", return_value=process) as run:
                quality_lanes.run_recorded(command, None, "backend", report, execution)
            actual = run.call_args.args[0]
            self.assertEqual(actual, command[:-1] + ["./b"])
            self.assertNotIn("-run", actual)
            self.assertEqual(execution["required_go_packages"], required)
            self.assertEqual(execution["commands"], [command])
            self.assertEqual(execution["command_results"][0]["executed_command"], actual)

    def test_database_precheck_uses_owned_local_objects_and_never_special_parameter_privileges(self):
        url = "postgres://synthetic@localhost/aicrm_test_probe_acceptance_test?sslmode=disable"
        with tempfile.TemporaryDirectory() as prep, \
             patch.dict(os.environ, {"AICRM_DATABASE_URL":url,"AICRM_TEST_PREP_DIR":prep}), \
             patch.object(quality_lanes, "postgres_16_ready", return_value=True), \
             patch.object(quality_lanes.subprocess, "run", return_value=subprocess.CompletedProcess([],0)) as run:
            self.assertTrue(quality_lanes.postgres_operations_ready())
        commands = [call.args[0] for call in run.call_args_list]
        rendered = " ".join(" ".join(command) for command in commands)
        self.assertIn("TEMPLATE template0", rendered)
        self.assertIn("ALLOW_CONNECTIONS false", rendered)
        self.assertEqual(sum("DROP DATABASE" in command[-1] for command in commands), 2)
        self.assertNotIn("session_replication_role", rendered)
        self.assertNotIn("postgres://", rendered)

    def test_database_probe_registers_before_create_and_retains_failed_drop(self):
        with tempfile.TemporaryDirectory() as raw:
            prep=Path(raw); seen=[]
            def sql(args,**kwargs):
                statement=args[-1];seen.append(statement)
                if statement.startswith('CREATE DATABASE'):
                    name=statement.split('"')[1]
                    self.assertTrue((prep/('database-'+name+'.json')).is_file())
                if statement.startswith('DROP DATABASE'):
                    raise subprocess.TimeoutExpired(args,15)
                return subprocess.CompletedProcess(args,0)
            with patch.dict(os.environ,{'AICRM_DATABASE_URL':'postgres://role@localhost/aicrm_test_probe_acceptance_test','AICRM_TEST_PREP_DIR':raw}), \
                 patch.object(quality_lanes,'postgres_16_ready',return_value=True), \
                 patch.object(quality_lanes.subprocess,'run',side_effect=sql):
                self.assertFalse(quality_lanes.postgres_operations_ready())
            self.assertEqual(len(list(prep.glob('database-*.json'))),2)
            self.assertEqual(sum(s.startswith('DROP DATABASE') for s in seen),2)

    def test_database_probe_never_creates_without_attempt_inventory(self):
        with patch.dict(os.environ,{'AICRM_DATABASE_URL':'postgres://role@localhost/aicrm_test_probe_acceptance_test'},clear=True), \
             patch.object(quality_lanes,'postgres_16_ready',return_value=True), \
             patch.object(quality_lanes.subprocess,'run') as sql:
            self.assertFalse(quality_lanes.postgres_operations_ready());sql.assert_not_called()

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
        commands = quality_lanes.focused_commands("browser", Path("/tmp/evidence"), [check])
        self.assertEqual(commands[:-1], quality_lanes.commands("browser", Path("/tmp/evidence"))[:-1])
        command = commands[-1]
        self.assertIn("--journey", command)
        self.assertIn(check["test"], command)
        self.assertNotIn("--group", command)

    def test_continuation_reuses_checks_but_recreates_setup(self):
        with tempfile.TemporaryDirectory() as temp, contextlib.redirect_stdout(io.StringIO()):
            report = Path(temp)
            validation = ["node", "scripts/validate-openapi.mjs"]
            setup = [sys.executable, "-m", "venv", str(report / ".venv")]
            execution = {"commands": [], "resume_commands": [quality_lanes.normalized_command(c, report)
                          for c in [validation, setup]]}
            with patch.object(quality_lanes.subprocess, "run", return_value=subprocess.CompletedProcess([], 0)) as run:
                quality_lanes.run_recorded(validation, None, "frontend", report, execution)
                quality_lanes.run_recorded(setup, None, "frontend", report, execution)
                self.assertEqual(run.call_count, 1)
                self.assertEqual(run.call_args.args[0], setup)
            self.assertTrue(execution["command_results"][0]["reused"])
            self.assertNotIn("reused", execution["command_results"][1])

    def test_failed_browser_keeps_test_log_for_environment_diagnosis(self):
        with tempfile.TemporaryDirectory() as temp, contextlib.redirect_stdout(io.StringIO()):
            report = Path(temp)
            (report / "browser-execution.log").write_text('{"Action":"fail","Test":"TestFailedChromiumJourney"}\n')
            execution = {"commands": []}
            with patch.object(quality_lanes.subprocess, "run", return_value=subprocess.CompletedProcess([], 1)):
                with self.assertRaises(subprocess.CalledProcessError):
                    quality_lanes.run_recorded([sys.executable, "scripts/dev_preflight.py", "browser"], None,
                                               "browser", report, execution)
            self.assertEqual(execution["go_json_log"], "browser-execution.log")

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

    def test_local_database_policy_matches_canonical_isolation_boundary(self):
        accepted = (
            "postgres://user:pass@localhost/aicrm_test_one",
            "postgresql://user:pass@127.0.0.1/aicrm_test_payment_9bbbf28?sslmode=disable",
            "postgres://user:pass@[::1]/aicrm_ci",
        )
        rejected = (
            "postgres://user:pass@db.example/aicrm_test_one",
            "postgres://user:pass@localhost/aicrm_production",
            "https://localhost/aicrm_test_one",
            "postgres://user:pass@[broken/aicrm_test_one",
        )
        for value in accepted:
            with self.subTest(value=value):
                self.assertTrue(quality_lanes.is_local_test_database_url(value))
        for value in rejected:
            with self.subTest(value=value):
                self.assertFalse(quality_lanes.is_local_test_database_url(value))

    def test_browser_requires_chromium_but_frontend_does_not(self):
        def available(name):
            return name != "google-chrome"
        with patch.object(quality_lanes, "command_available", side_effect=available), patch.object(
                quality_lanes, "exact_version", return_value=True), patch.object(
                quality_lanes, "postgres_16_ready", return_value=True), patch.object(
                quality_lanes, "chromium_font_ready", return_value=True), patch.object(
                quality_lanes, "chromium_ready", return_value=False):
            self.assertIn("Chromium executable (existing resolver)", quality_lanes.missing_prerequisites("browser"))
            self.assertNotIn("Chromium executable (existing resolver)", quality_lanes.missing_prerequisites("frontend"))

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

    def test_postgres_rejects_malformed_database_urls_without_connecting(self):
        urls = (
            "postgres://user:pass@[broken/aicrm_test_isolated",
            "postgres://user:pass@localhost:not-a-port/aicrm_test_isolated",
        )
        for url in urls:
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
