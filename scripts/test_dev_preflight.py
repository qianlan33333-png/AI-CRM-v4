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


if __name__ == "__main__":
    unittest.main()
