#!/usr/bin/env python3
"""Contracts for automatic inclusion and truthful browser execution evidence."""
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
import contextlib
import io
import sys
from types import SimpleNamespace
from unittest.mock import patch

import dev_preflight
from dev_preflight import Preflight, REQUIRED_JOURNEYS, SHELL_JOURNEYS, select_journeys, verify_journey_results


class BrowserCoverage(unittest.TestCase):
    def listing(self, extra=()):
        return "\n".join(sorted(REQUIRED_JOURNEYS | set(extra))) + "\nok example/cmd/aicrm 0.05s\n"

    def test_new_journey_joins_without_workflow_edit(self):
        name = "TestPostgreSQLSurveyCompletionChromiumJourney"
        self.assertIn(name, select_journeys(self.listing([name]), "business"))

    def test_groups_are_disjoint_and_exhaustive(self):
        listing = self.listing(["TestPostgreSQLGroupOpsStandardHostChromiumJourney"])
        early = set(select_journeys(listing, "shell"))
        later = set(select_journeys(listing, "business"))
        self.assertEqual(early, SHELL_JOURNEYS)
        self.assertFalse(early & later)
        self.assertEqual(early | later, set(select_journeys(listing, "all")))

    def test_focused_selection_requires_only_the_named_discovered_journey(self):
        name = "TestPostgreSQLMediaRefreshChromiumJourney"
        self.assertEqual(select_journeys(name + "\nPASS\n", "all", [name]), [name])
        with self.assertRaisesRegex(ValueError, "selected Chromium tests missing"):
            select_journeys("PASS\n", "all", [name])

    def test_browser_execution_keeps_all_discovered_tests_with_suite_budget(self):
        with tempfile.TemporaryDirectory() as directory, patch.dict(dev_preflight.os.environ, {"AICRM_DATABASE_URL": "isolated-test"}):
            check = Preflight(Path(directory))
            listing = Path(directory) / "listing.log"
            extra = "TestPostgreSQLDataWorkspaceChromiumJourney"
            listing.write_text(self.listing([extra]))
            with patch.object(check, "run", return_value=listing) as run, patch.object(dev_preflight, "verify_journey_results", return_value={}):
                check.browser("all")
            command = run.call_args_list[-1].args[1]
            self.assertIn("-timeout=15m", command)
            pattern = command[command.index("-run") + 1]
            for name in REQUIRED_JOURNEYS | {extra}:
                self.assertIn(name, pattern)

    def test_browser_execution_accepts_a_focused_registered_journey(self):
        name = "TestPostgreSQLMediaRefreshChromiumJourney"
        with tempfile.TemporaryDirectory() as directory, patch.dict(dev_preflight.os.environ, {"AICRM_DATABASE_URL": "isolated-test"}):
            check = Preflight(Path(directory))
            listing = Path(directory) / "listing.log"
            listing.write_text(name + "\nPASS\n")
            with patch.object(check, "run", return_value=listing) as run, \
                    patch.object(dev_preflight, "verify_journey_results", return_value={}):
                check.browser("all", [name])
            command = run.call_args_list[-1].args[1]
            self.assertEqual(command[command.index("-run") + 1], "^(" + name + ")$")

    def test_missing_existing_test_fails(self):
        for name in REQUIRED_JOURNEYS:
            with self.subTest(name=name), self.assertRaises(ValueError):
                select_journeys(self.listing().replace(name, ""), "all")

    def test_no_tests_to_run_is_not_success(self):
        with self.assertRaises(ValueError):
            select_journeys("testing: warning: no tests to run\nPASS\n", "all")

    def verify(self, events):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "events.jsonl"
            path.write_text("\n".join(json.dumps(event) for event in events))
            return verify_journey_results(path, ["TestExampleChromiumJourney"])

    def test_actual_pass_is_accepted(self):
        name = "TestExampleChromiumJourney"
        self.assertEqual(self.verify([{"Test": name, "Action": "pass"}, {"Action": "pass"}]), {name: "pass"})

    def test_skipped_journey_fails_even_when_package_passes(self):
        with self.assertRaises(ValueError):
            self.verify([{"Test": "TestExampleChromiumJourney", "Action": "skip"}, {"Action": "pass"}])

    def test_skipped_subtest_fails_even_when_parent_passes(self):
        name = "TestExampleChromiumJourney"
        with self.assertRaises(ValueError):
            self.verify([{"Test": name + "/browser", "Action": "skip"}, {"Test": name, "Action": "pass"}, {"Action": "pass"}])

    def test_missing_failed_or_truncated_results_fail(self):
        name = "TestExampleChromiumJourney"
        for events in [[], [{"Action": "pass"}], [{"Test": name, "Action": "pass"}], [{"Test": name, "Action": "fail"}, {"Action": "fail"}]]:
            with self.subTest(events=events), self.assertRaises(ValueError):
                self.verify(events)

    def test_command_failure_retains_exit_code_and_diagnostic(self):
        with tempfile.TemporaryDirectory() as directory, contextlib.redirect_stdout(io.StringIO()):
            check = Preflight(Path(directory))
            with self.assertRaises(RuntimeError):
                check.run("failure", [sys.executable, "-c", "print('fixture failure'); raise SystemExit(7)"])
            summary = json.loads((Path(directory) / "summary.json").read_text())
            self.assertEqual(summary["steps"][0]["exit_code"], 7)
            self.assertIn("fixture failure", (Path(directory) / "failure.log").read_text())

    def test_command_success_retains_reproduction_command(self):
        with tempfile.TemporaryDirectory() as directory, contextlib.redirect_stdout(io.StringIO()):
            check = Preflight(Path(directory))
            command = [sys.executable, "-c", "print('fixture passed')"]
            check.run("success", command)
            self.assertEqual(check.report["steps"][0]["command"], command)
            self.assertEqual(check.report["steps"][0]["exit_code"], 0)

    def test_full_rejects_dirty_source_before_any_lane(self):
        dirty = {"head": "a" * 40, "tree": "b" * 40, "status": [" M user-source.go"]}
        with tempfile.TemporaryDirectory() as directory, patch.object(Preflight, "source_snapshot", return_value=dirty):
            check = Preflight(Path(directory))
            with self.assertRaisesRegex(ValueError, "clean committed"):
                check.full()

    def test_full_records_source_drift_per_lane_and_refuses_claim(self):
        clean = {"head": "a" * 40, "tree": "b" * 40, "status": []}
        drifted = {"head": "a" * 40, "tree": "b" * 40, "status": [" M source.go"]}
        with tempfile.TemporaryDirectory() as directory, patch.object(Preflight, "source_snapshot", side_effect=[clean, clean, drifted]), \
                patch("dev_preflight.subprocess.check_output", return_value="c" * 40 + "\n"), \
                patch.object(Preflight, "run", return_value=Path(directory) / "lane.log"):
            check = Preflight(Path(directory))
            with self.assertRaisesRegex(RuntimeError, "changed during"):
                check.full()
            self.assertEqual(check.report["lanes"][0]["result"], "source_changed")

    def test_full_evidence_directory_cannot_be_inside_source_tree(self):
        with patch.object(sys, "argv", ["dev_preflight.py", "full", "--report-dir", str(dev_preflight.ROOT / "evidence")]):
            with self.assertRaises(SystemExit):
                dev_preflight.main()

    def test_full_child_python_does_not_dirty_a_real_temporary_git_checkout(self):
        with tempfile.TemporaryDirectory() as directory, tempfile.TemporaryDirectory() as evidence:
            repo = Path(directory)
            script = repo / "scripts" / "ci" / "quality_lanes.py"
            script.parent.mkdir(parents=True)
            (script.parent / "helper.py").write_text("value = 1\n")
            script.write_text("import helper\n")
            for command in (["git", "init", "-q"], ["git", "config", "user.email", "test@example.invalid"],
                            ["git", "config", "user.name", "test"], ["git", "add", "."],
                            ["git", "commit", "-qm", "fixture"], ["git", "update-ref", "refs/remotes/origin/main", "HEAD"]):
                subprocess.run(command, cwd=repo, check=True)
            with patch.object(dev_preflight, "ROOT", repo), patch.object(dev_preflight, "FULL_LANES", ("probe",)), \
                    contextlib.redirect_stdout(io.StringIO()):
                check = Preflight(Path(evidence))
                check.full()
            self.assertEqual(subprocess.check_output(["git", "status", "--porcelain=v1", "--untracked-files=all"],
                                                     cwd=repo, text=True), "")
            self.assertEqual(check.report["lanes"][0]["result"], "success")


class AffectedPreflight(unittest.TestCase):
    def plan(self, lanes=("preflight", "backend", "browser"), mode="targeted",
             profile="affected-packages", evidence_eligible=True):
        changed_paths = ([("internal/payment/app/service.go")]
                         if profile == "affected-packages" else ["scripts/ci/impact_selection.py"])
        candidate_checks = ([
            {"lane": "backend", "path": "internal/payment/app/service_test.go", "test": "TestPayment"},
            {"lane": "browser", "path": "cmd/aicrm/payment_test.go", "test": "TestPaymentChromiumJourney"},
        ] if mode == "targeted" and profile != "tooling" else [])
        package_inventory = ([
            {"dir": "cmd/aicrm", "import_path": "example.test/crm/cmd/aicrm",
             "test_files": ["cmd/aicrm/unit_test.go", "cmd/aicrm/payment_test.go"]},
            {"dir": "internal/payment/app", "import_path": "example.test/crm/internal/payment/app",
             "test_files": ["internal/payment/app/service_test.go"]},
        ] if profile == "affected-packages" else [])
        packages = [item["import_path"] for item in package_inventory]
        plan = {
            "schema": 1, "observed_mode": "shadow", "evidence_eligible": evidence_eligible,
            "baseline_sha": "1" * 40, "baseline_tree": "2" * 40,
            "head_sha": "a" * 40, "head_tree": "b" * 40,
            "policy_fingerprint": "c" * 64,
            "changed_paths": changed_paths,
            "source_clean": True,
            "source": {"checked_out_head": "a" * 40, "status": [],
                       "working_tree_clean": True, "head_matches": True},
            "parent_prd": {"id": "docs/prd/trial.md", "sha256": "e" * 64},
            "candidate": {
                "selection_mode": mode,
                "selected_lanes": list(lanes),
                "selected_checks": candidate_checks,
                "profile": profile,
            },
            "candidate_go_packages": packages,
        }
        plan["selected_lanes"] = list(lanes)
        plan["selected_checks"] = plan["candidate"]["selected_checks"]
        plan["graph_result"] = {
            "schema": 1, "baseline_sha": plan["baseline_sha"], "baseline_tree": plan["baseline_tree"],
            "head_sha": plan["head_sha"], "head_tree": plan["head_tree"],
            "changed_paths": plan["changed_paths"], "graph_valid": True, "unowned_go_paths": [],
            "module": "example.test/crm", "graph_fingerprint": "d" * 64,
            "selected_packages": package_inventory,
        }
        return plan

    def prepare_check(self, directory, plan, run_side_effect=None):
        source = {"head": plan["head_sha"], "tree": plan["head_tree"], "status": []}
        with patch.object(Preflight, "source_snapshot", return_value=source):
            check = Preflight(Path(directory))

        def success(name, command, env=None):
            lane = command[2]
            lane_dir = Path(command[command.index("--report-dir") + 1])
            lane_dir.mkdir(parents=True, exist_ok=True)
            log_path = check.report_dir / (name + ".log")
            log_path.write_text("lane output\n")
            check.report["steps"].append({"name": name, "command": command, "exit_code": 0,
                                          "seconds": 1.0, "log": str(log_path)})
            commands = [["python", "scripts/dev_preflight.py", "fast"]]
            go_log = None
            if lane == "backend":
                package_args = ["./" + item["dir"] for item in plan["graph_result"]["selected_packages"]]
                package_args = package_args or ["./..."]
                commands = [["bash", "scripts/run-go-with-donor-views.sh", "go", "test", "-json",
                             "-p", "1", "-race", "-count=1", "-timeout=15m",
                             *package_args]]
                go_log = "backend-go-test.jsonl"
                events = [
                    {"Action": "run", "Package": "example.test/crm/cmd/aicrm", "Test": "TestUnit"},
                    {"Action": "pass", "Package": "example.test/crm/cmd/aicrm", "Test": "TestUnit", "Elapsed": 0.1},
                    {"Action": "run", "Package": "example.test/crm/cmd/aicrm", "Test": "TestPaymentChromiumJourney"},
                    {"Action": "skip", "Package": "example.test/crm/cmd/aicrm", "Test": "TestPaymentChromiumJourney", "Elapsed": 0},
                    {"Action": "pass", "Package": "example.test/crm/cmd/aicrm", "Elapsed": 0.2},
                    {"Action": "run", "Package": "example.test/crm/internal/payment/app", "Test": "TestPayment"},
                    {"Action": "pass", "Package": "example.test/crm/internal/payment/app", "Test": "TestPayment", "Elapsed": 0.1},
                    {"Action": "pass", "Package": "example.test/crm/internal/payment/app", "Elapsed": 0.2},
                ]
                (lane_dir / go_log).write_text("\n".join(json.dumps(event) for event in events) + "\n")
            elif lane == "browser":
                commands = [[sys.executable, "scripts/dev_preflight.py", "browser",
                             "--journey", "TestPaymentChromiumJourney"]]
                go_log = "browser-execution.log"
                events = [
                    {"Action": "run", "Package": "example.test/crm/cmd/aicrm", "Test": "TestPaymentChromiumJourney"},
                    {"Action": "pass", "Package": "example.test/crm/cmd/aicrm", "Test": "TestPaymentChromiumJourney", "Elapsed": 0.1},
                    {"Action": "pass", "Package": "example.test/crm/cmd/aicrm", "Elapsed": 0.2},
                ]
                (lane_dir / go_log).write_text("\n".join(json.dumps(event) for event in events) + "\n")
            receipt = {"lane": lane, "result": "success", "exit_code": 0,
                       "tested_sha": plan["head_sha"], "tree": plan["head_tree"],
                       "policy_fingerprint": plan["policy_fingerprint"],
                       "commands": commands, "elapsed_seconds": 1.0, "run_id": None,
                       "run_attempt": None, "go_json_log": go_log}
            (lane_dir / "run.json").write_text(json.dumps(receipt))
            return log_path

        if run_side_effect is not None:
            run = run_side_effect
        else:
            run = success
        return check, success, run, source

    def test_exact_source_binding_accepts_clean_plan_even_if_shadow_sample_is_ineligible(self):
        plan = self.plan(lanes=dev_preflight.FULL_LANES, mode="full", profile="full",
                         evidence_eligible=False)
        source = {"head": plan["head_sha"], "tree": plan["head_tree"], "status": []}
        binding = {key: plan[key] for key in
                   ("baseline_sha", "baseline_tree", "head_sha", "head_tree", "policy_fingerprint")}
        verification = SimpleNamespace(exact_source_binding=lambda *args, **kwargs: binding)
        with patch.dict(sys.modules, {"verification": verification}):
            self.assertTrue(dev_preflight.exact_affected_plan_source_binding(plan, source))
            plan["source"]["working_tree_clean"] = False
            self.assertFalse(dev_preflight.exact_affected_plan_source_binding(plan, source))

    def test_local_affected_runs_candidate_lanes_and_full_package_suites(self):
        plan = self.plan()
        with tempfile.TemporaryDirectory() as directory:
            check, _, run, source = self.prepare_check(directory, plan)
            commands = []

            def capture(name, command, env=None):
                commands.append(command)
                return run(name, command, env)

            with patch.object(check, "run", side_effect=capture), \
                    patch.object(check, "source_snapshot", return_value=source), \
                    patch.object(dev_preflight, "exact_affected_plan_source_binding", return_value=True):
                result = check.affected(plan)

            self.assertEqual(result, "passed")
            self.assertEqual(check.report["local_scope"], ["preflight", "backend", "browser"])
            self.assertEqual(check.report["affected_execution"]["completed_lanes"], ["preflight", "backend", "browser"])
            self.assertTrue(check.report["affected_collection"]["required_results_passed"])
            self.assertFalse(check.report["affected_collection"]["measurement_eligible"])
            self.assertEqual(check.report["claim"], "local_affected_plan")
            self.assertFalse(check.report["eligible_for_delivery"])
            backend_command = next(command for command in commands if command[2] == "backend")
            self.assertIn("--focus-packages-json", backend_command)
            self.assertNotIn("--focus-checks-json", backend_command)
            browser_command = next(command for command in commands if command[2] == "browser")
            self.assertIn("--focus-checks-json", browser_command)

    def test_local_affected_reports_missing_browser_environment_as_incomplete(self):
        plan = self.plan(lanes=("browser",))
        with tempfile.TemporaryDirectory() as directory:
            check, success, _, source = self.prepare_check(directory, plan)

            def unavailable(name, command, env=None):
                lane_dir = Path(command[command.index("--report-dir") + 1])
                lane_dir.mkdir(parents=True, exist_ok=True)
                log_path = check.report_dir / (name + ".log")
                log_path.write_text("missing required local environment: Linux amd64 browser environment, google-chrome\n")
                check.report["steps"].append({"name": name, "command": command, "exit_code": 2,
                                              "seconds": 0.1, "log": str(log_path)})
                (lane_dir / "run.json").write_text(json.dumps({"lane": "browser", "result": "failed",
                    "exit_code": 2, "tested_sha": plan["head_sha"], "tree": plan["head_tree"],
                    "policy_fingerprint": plan["policy_fingerprint"], "commands": [], "elapsed_seconds": 0.1}))
                raise RuntimeError("browser lane missing prerequisites")

            with patch.object(check, "run", side_effect=unavailable), patch.object(
                    check, "source_snapshot", return_value=source), patch.object(
                    dev_preflight, "exact_affected_plan_source_binding", return_value=True):
                result = check.affected(plan)

            self.assertEqual(result, "incomplete")
            self.assertEqual(check.report["affected_execution"]["incomplete_lanes"], ["browser"])
            self.assertEqual(check.report["lanes"][0]["missing_environment"],
                             ["Linux amd64 browser environment", "google-chrome"])
            self.assertTrue(check.report["existing_ci_gates_retained"])

    def test_local_affected_rejects_success_without_matching_run_receipt(self):
        plan = self.plan(lanes=("backend",))
        with tempfile.TemporaryDirectory() as directory:
            check, _, run, source = self.prepare_check(directory, plan)
            def no_receipt(name, command, env=None):
                log_path = check.report_dir / (name + ".log")
                log_path.write_text("success\n")
                check.report["steps"].append({"name": name, "command": command, "exit_code": 0,
                                              "seconds": 1.0, "log": str(log_path)})
                return log_path
            with patch.object(check, "run", side_effect=no_receipt), patch.object(
                    check, "source_snapshot", return_value=source), patch.object(
                    dev_preflight, "exact_affected_plan_source_binding", return_value=True):
                result = check.affected(plan)
            self.assertEqual(result, "incomplete")
            self.assertTrue(any("without a matching successful run receipt" in item["error"]
                                for item in check.report["lanes"]))

    def test_local_affected_rejects_dirty_plan(self):
        with tempfile.TemporaryDirectory() as directory:
            check = Preflight(Path(directory))
            dirty_plan = {"evidence_eligible": False}
            with self.assertRaisesRegex(ValueError, "does not bind the clean checkout"):
                check.affected(dirty_plan)

    def test_local_full_plan_runs_when_shadow_sampling_is_ineligible(self):
        plan = self.plan(lanes=dev_preflight.FULL_LANES, mode="full", profile="full",
                         evidence_eligible=False)
        with tempfile.TemporaryDirectory() as directory:
            check, _, run, source = self.prepare_check(directory, plan)
            commands = []

            def capture(name, command, env=None):
                commands.append(command)
                return run(name, command, env)

            with patch.object(check, "run", side_effect=capture), \
                    patch.object(check, "source_snapshot", return_value=source), \
                    patch.object(dev_preflight, "exact_affected_plan_source_binding", return_value=True):
                result = check.affected(plan)

            self.assertEqual(result, "passed")
            self.assertEqual(check.report["affected_execution"]["completed_lanes"],
                             list(dev_preflight.FULL_LANES))
            self.assertFalse(check.report["affected_collection"]["measurement_eligible"])
            self.assertEqual(check.report["affected_collection"]["evidence_kind"], "local_only")
            self.assertEqual(commands[0][commands[0].index("--profile") + 1], "full")

    def test_tooling_candidate_passes_tooling_profile_to_preflight_lane(self):
        plan = self.plan(lanes=("preflight",), mode="targeted", profile="tooling")
        with tempfile.TemporaryDirectory() as directory:
            check, _, run, source = self.prepare_check(directory, plan)
            commands = []

            def capture(name, command, env=None):
                commands.append(command)
                return run(name, command, env)

            with patch.object(check, "run", side_effect=capture), \
                    patch.object(check, "source_snapshot", return_value=source), \
                    patch.object(dev_preflight, "exact_affected_plan_source_binding", return_value=True):
                result = check.affected(plan)

            self.assertEqual(result, "passed")
            preflight_command = next(command for command in commands if command[2] == "preflight")
            self.assertEqual(preflight_command[preflight_command.index("--profile") + 1], "tooling")

    def test_affected_dry_run_prints_plan_without_running_checks(self):
        plan = {"mode": "shadow", "evidence_eligible": True, "candidate": {"selected_lanes": ["preflight"]}}
        output = io.StringIO()
        with patch.object(sys, "argv", ["dev_preflight.py", "affected", "--base", "a" * 40, "--dry-run"]), \
                patch.object(dev_preflight, "build_affected_plan", return_value=plan) as build, \
                contextlib.redirect_stdout(output):
            result = dev_preflight.main()
        build.assert_called_once_with("a" * 40, "HEAD")
        self.assertEqual(result, 0)
        self.assertEqual(json.loads(output.getvalue()), plan)

    def test_affected_dry_run_marks_dirty_source_ineligible(self):
        plan = {"mode": "shadow", "evidence_eligible": False, "candidate": {"selected_lanes": ["preflight"]}}
        output = io.StringIO()
        with patch.object(sys, "argv", ["dev_preflight.py", "affected", "--base", "a" * 40, "--dry-run"]), \
                patch.object(dev_preflight, "build_affected_plan", return_value=plan), \
                contextlib.redirect_stdout(output):
            result = dev_preflight.main()
        self.assertEqual(result, 2)
        self.assertFalse(json.loads(output.getvalue())["evidence_eligible"])


if __name__ == "__main__":
    unittest.main()
