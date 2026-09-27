"""Release gate contracts for bounded concurrency and exact required evidence."""
import json
from pathlib import Path
import subprocess
import tempfile
import threading
import time
import unittest
from unittest.mock import patch

from scripts import domestic_main_release as release


class AccelerationGateTest(unittest.TestCase):
    def test_maintenance_continuation_only_uses_protected_digest_and_exact_identity(self):
        with tempfile.TemporaryDirectory() as raw:
            root=Path(raw); config={"work_root":str(root)}
            marker={"candidate_sha":"b"*40,"candidate_tree":"c"*40,"base_sha":"a"*40,
                    "check_receipt_sha256":"d"*64}
            self.assertIsNone(release._maintenance_check_checkpoint(config,marker))
            directory=root/"diagnostics"; directory.mkdir(mode=0o700)
            path=directory/("b"*40+"-completed-receipt.json")
            checkpoint=directory/("b"*40+"-completed-checkpoint.json")
            receipt={"status":"passed","head_sha":"b"*40,"head_tree":"c"*40,
                     "baseline_sha":"a"*40,"check_checkpoint":str(checkpoint)}
            def write(value):
                release.atomic_json(path,value)
                marker["check_receipt_sha256"]=release.hashlib.sha256(json.dumps(
                    value,ensure_ascii=False,sort_keys=True,separators=(",",":")).encode()).hexdigest()
            write(receipt)
            self.assertEqual(release._maintenance_check_checkpoint(config,marker),str(checkpoint))
            release.atomic_json(path,{**receipt,"head_tree":"changed"})
            self.assertIsNone(release._maintenance_check_checkpoint(config,marker))
            for change in ({"head_sha":"e"*40},{"head_tree":"e"*40},{"baseline_sha":"e"*40},
                           {"status":"failed"},{"check_checkpoint":str(root/"outside-checkpoint.json")}):
                write({**receipt,**change})
                with self.assertRaises(release.ReleaseError):
                    release._maintenance_check_checkpoint(config,marker)
            write({**receipt,"check_checkpoint":None})
            self.assertIsNone(release._maintenance_check_checkpoint(config,marker))
            write(receipt); path.chmod(0o644)
            with self.assertRaises(release.ReleaseError):
                release._maintenance_check_checkpoint(config,marker)

    def test_package_discovery_materializes_embed_inputs_in_private_checkout_first(self):
        with tempfile.TemporaryDirectory() as raw:
            root=Path(raw); checkout=root/"private"; policy=root/"trusted"
            (checkout/"web/donor-sources").mkdir(parents=True)
            (checkout/"web/donor-sources/source-index.json").write_text("{}")
            calls=[]
            def build(config,args,**kwargs):
                calls.append((args,kwargs))
                return subprocess.CompletedProcess(args,0,"module/a\nmodule/b\n" if args[0]=="go" else "prepared","")
            with patch.object(release,"_build_command",side_effect=build):
                inventory=release._discover_check_packages({},policy,checkout,{}, {"selection_mode":"full"})
            self.assertEqual(inventory,["module/a","module/b"])
            self.assertEqual(calls[0][0],["node",str(policy/"scripts/prepare-donor-source-views.mjs"),"--root",str(checkout)])
            self.assertEqual(calls[1][0],["go","list","-f","{{.ImportPath}}","./..."])
            self.assertTrue(all(call[1]["cwd"]==checkout and call[1]["safe_repository"]==checkout for call in calls))
            with patch.object(release,"_build_command",return_value=subprocess.CompletedProcess([],1,"","embed missing")):
                with self.assertRaises(release.CheckIncompleteError):
                    release._discover_check_packages({},policy,checkout,{}, {"selection_mode":"full"})

    def test_package_continuation_retains_only_whole_passed_packages(self):
        required = ["example/a", "example/b", "example/c", "example/no_tests"]
        events = [
            {"Action":"pass", "Package":"example/a", "Test":"TestA"},
            {"Action":"pass", "Package":"example/a"},
            {"Action":"pass", "Package":"example/b", "Test":"TestAlreadyPassed"},
            {"Action":"fail", "Package":"example/b", "Test":"TestFailed"},
            {"Action":"fail", "Package":"example/b"},
            {"Action":"pass", "Package":"example/c", "Test":"TestIncompletePackage"},
            {"Action":"skip", "Package":"example/no_tests"},
            {"Action":"output", "Package":"example/no_tests", "Output":"? example/no_tests [no test files]"}]
        passed = release._passed_go_packages(events, required)
        self.assertEqual(passed, ["example/a", "example/no_tests"])
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp)
            fresh = [{"Action":"pass", "Package":package} for package in ["example/b", "example/c"]]
            receipt = self.receipt(directory, "backend", fresh)
            receipt.update(required_go_packages=required, reused_go_packages=passed)
            snapshot = {"events":release._sanitized_test_events(events), "required_go_packages":required,
                        "result":{"log_path":"original-protected.log"}}
            release._merge_backend_continuation(directory, snapshot, receipt, required)
            release._verify_lane_evidence(directory, "backend", "b"*40, "c"*40, [], required)
            merged = release._lane_test_events(directory, receipt)
            self.assertNotIn("TestAlreadyPassed", [event.get("Test") for event in merged])
            self.assertNotIn("TestIncompletePackage", [event.get("Test") for event in merged])
            with self.assertRaises(release.CheckIncompleteError):
                release._merge_backend_continuation(directory, snapshot, receipt, required + ["example/changed"])

    def test_unknown_interruption_has_no_candidate_verdict_or_reuse_authority(self):
        timeout = [{"Action":"fail", "Test":"TestBusiness"},
                   {"Action":"output", "Output":"panic: test timed out after 30m"}]
        self.assertFalse(release._candidate_lane_failure({}, timeout))
        self.assertTrue(release._candidate_lane_failure({}, [{"Action":"fail", "Test":"TestAssertion"}]))
        self.assertFalse(release._candidate_lane_failure({}, []))
        with tempfile.TemporaryDirectory() as temp:
            item = {"attempt":1}; state = {"in_flight":{"phase":"checks"}}
            release._mark_failed(Path(temp)/"state.json", state, item,
                                 release.CheckIncompleteError("interrupted"), "checks")
            self.assertEqual(item["failure"]["kind"], "unknown")
            self.assertEqual(item["failure"]["candidate_verdict"], "not_evaluated")
            self.assertNotIn("check_checkpoint", item["failure"])
            self.assertEqual(state["status"], "blocked")

    def test_only_known_environment_errors_are_resumable(self):
        media = "TestPostgreSQLMediaRefreshChromiumJourney"
        ops = "TestPostgreSQLOpsGovernanceChromiumJourney"
        events = [{"Action":"fail", "Test":media}, {"Action":"fail", "Test":ops},
                  {"Action":"output", "Test":media, "Output":'ERROR: permission denied to set parameter "session_replication_role" (SQLSTATE 42501)'},
                  {"Action":"output", "Test":ops, "Output":"Error: spawn chromium ENOENT"}]
        self.assertTrue(release._environment_lane_failure({}, events))
        for output in ("HTTP 403 permission denied", "business assertion failed", "context deadline exceeded"):
            mixed = [*events, {"Action":"fail", "Test":"TestBusinessChromiumJourney"},
                     {"Action":"output", "Test":"TestBusinessChromiumJourney", "Output":output}]
            self.assertFalse(release._environment_lane_failure({}, mixed))
        self.assertFalse(release._environment_lane_failure({}, []))

    def test_environment_continuation_reuses_passed_lanes_and_runs_only_two_failed_journeys(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root / "scripts/ci").mkdir(parents=True)
            (root / "scripts/ci/check_preparation.py").write_text("trusted policy")
            diagnostics = root / "diagnostics"; diagnostics.mkdir(mode=0o700)
            names = [f"TestPassed{i}ChromiumJourney" for i in range(32)] + [
                "TestPostgreSQLMediaRefreshChromiumJourney", "TestPostgreSQLOpsGovernanceChromiumJourney"]
            calls = []
            attempt = {"value":1}
            def checkout(config, repo, path, sha):
                path.mkdir(); return path
            def build(config, args, **kwargs):
                if args[0] == "/usr/bin/mkdir": Path(args[-1]).mkdir()
                return subprocess.CompletedProcess(args, 0, "", "")
            def run(args, **kwargs):
                lane_dir = Path(args[args.index("--report-dir")+1]); lane = lane_dir.name
                calls.append((attempt["value"], lane))
                selected = names
                if "--focus-checks-json" in args:
                    selected = [item["test"] for item in json.loads(args[args.index("--focus-checks-json")+1])]
                    self.assertEqual(selected, names[-2:])
                events = []
                failed = attempt["value"] == 1 and lane == "browser"
                for name in selected if lane == "browser" else []:
                    action = "fail" if failed and name in names[-2:] else "pass"
                    events.append({"Action":action, "Test":name})
                    if action == "fail":
                        output = ('permission denied to set parameter "session_replication_role" (SQLSTATE 42501)'
                                  if name == names[-2] else "spawn chromium ENOENT")
                        events.append({"Action":"output", "Test":name, "Output":output})
                if lane == "backend": events = [{"Action":"pass", "Package":"example/required"}]
                value = self.receipt(lane_dir, lane, events)
                if lane == "browser":
                    (lane_dir / "summary.json").write_text(json.dumps({"required_browser_tests":selected}))
                if failed:
                    value.update(result="failed", exit_code=1)
                    (lane_dir / "run.json").write_text(json.dumps(value))
                return subprocess.CompletedProcess(args, int(failed))
            args = [root, root]
            lanes = ["preflight", "backend", "frontend", "browser"]
            with patch.object(release, "_private_check_checkout", side_effect=checkout), \
                 patch.object(release, "_build_command", side_effect=build), \
                 patch.object(release, "_prepare_check_dependencies"), \
                 patch.object(release, "_check_env", return_value={}), \
                 patch.object(release, "_verify_check_checkout_tree", return_value=1), \
                 patch.object(release, "_tree", return_value="c"*40), \
                 patch.object(release, "_worktree_git", side_effect=AssertionError("root must not read private clone Git metadata")), \
                 patch.object(release, "_run_check_process", side_effect=run):
                execution = root / "attempt1/candidate"; execution.mkdir(parents=True)
                reports = root / "reports1"; reports.mkdir()
                config = {"_check_snapshots":{}}
                with self.assertRaises(release.CheckEnvironmentError):
                    release._run_check_lanes(config, *args, execution, reports, "a"*40, "b"*40,
                        {"selection_mode":"full"}, lanes, [], [], "full", diagnostics)
                self.assertEqual(len(config["_check_snapshots"]), 4)
                identity = {"head_sha":"b"*40, "head_tree":"c"*40, "policy":"same"}
                checkpoint = diagnostics / "attempt1-checkpoint.json"
                release.atomic_json(checkpoint, {"schema":1, "failure_kind":"environment", "identity":identity,
                                                "lanes":config["_check_snapshots"]})
                prior = release._load_check_checkpoint(checkpoint, diagnostics, identity)
                completed = diagnostics / "maintenance-checkpoint.json"
                release.atomic_json(completed, {"schema":1, "result":"passed", "failure_kind":None,
                    "identity":identity, "lanes":config["_check_snapshots"]})
                self.assertEqual(release._load_check_checkpoint(completed,diagnostics,identity)["result"],"passed")
                self.assertEqual(release._load_check_checkpoint(completed,diagnostics,{**identity,"policy":"changed"}),{})
                attempt["value"] = 2
                execution = root / "attempt2/candidate"; execution.mkdir(parents=True)
                reports = root / "reports2"; reports.mkdir()
                results = release._run_check_lanes({"_check_checkpoint":prior}, *args, execution, reports,
                    "a"*40, "b"*40, {"selection_mode":"full"}, lanes, [], [], "full", diagnostics)
                self.assertEqual([lane for number,lane in calls if number == 2], ["browser"])
                browser = next(result for result in results if result["lane"] == "browser")
                self.assertEqual(len(browser["reused_browser_tests"]), 32)
                release._verify_lane_evidence(reports/"browser", "browser", "b"*40, "c"*40, [], [])
                # A code, scope or policy change starts every required lane again.
                for field in ("head_sha", "head_tree", "policy"):
                    self.assertEqual(release._load_check_checkpoint(checkpoint, diagnostics,
                                     {**identity, field:"changed"}), {})
                changed = dict(identity, head_sha="different")
                attempt["value"] = 3
                execution = root / "attempt3/candidate"; execution.mkdir(parents=True)
                reports = root / "reports3"; reports.mkdir()
                release._run_check_lanes({"_check_checkpoint":release._load_check_checkpoint(checkpoint, diagnostics, changed)},
                    *args, execution, reports, "a"*40, "b"*40, {"selection_mode":"full"}, lanes, [], [], "full", diagnostics)
                self.assertEqual({lane for number,lane in calls if number == 3}, set(lanes))
                log = Path(prior["lanes"]["backend"]["result"]["log_path"])
                log.write_text("altered")
                with self.assertRaisesRegex(release.ReleaseError, "hash or ownership"):
                    release._load_check_checkpoint(checkpoint, diagnostics, identity)
    def receipt(self, directory, lane, events):
        directory.mkdir(exist_ok=True)
        (directory / "tests.jsonl").write_text("".join(json.dumps(event) + "\n" for event in events))
        value = {"lane": lane, "result": "success", "exit_code": 0, "tested_sha": "b" * 40,
                 "tree": "c" * 40, "commands": [["go", "test"]], "go_json_log": "tests.jsonl"}
        (directory / "run.json").write_text(json.dumps(value))
        if lane == "browser":
            (directory / "summary.json").write_text(json.dumps({"required_browser_tests":["TestRequiredChromiumJourney"]}))
        return value

    def test_missing_skipped_or_wrong_identity_evidence_cannot_pass(self):
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp)
            check = {"lane": "browser", "test": "TestRequiredChromiumJourney"}
            for action in ("skip", "fail", None):
                events = [] if action is None else [{"Test": check["test"], "Action": action}]
                self.receipt(directory, "browser", events)
                with self.assertRaises(release.ReleaseError):
                    release._verify_lane_evidence(directory, "browser", "b" * 40, "c" * 40, [check], [])
            value = self.receipt(directory, "backend", [{"Package": "example/required", "Action": "pass"}])
            release._verify_lane_evidence(directory, "backend", "b" * 40, "c" * 40, [], ["example/required"])
            with self.assertRaisesRegex(release.ReleaseError, "missing or skipped"):
                release._verify_lane_evidence(directory, "backend", "b" * 40, "c" * 40, [], ["example/missing"])
            value["tree"] = "d" * 40
            (directory / "run.json").write_text(json.dumps(value))
            with self.assertRaisesRegex(release.ReleaseError, "identity"):
                release._verify_lane_evidence(directory, "backend", "b" * 40, "c" * 40, [], [])
            value["tree"] = "c" * 40
            value["required_commands"] = 1
            value["command_results"] = []
            (directory / "run.json").write_text(json.dumps(value))
            with self.assertRaisesRegex(release.ReleaseError, "command evidence"):
                release._verify_lane_evidence(directory, "backend", "b" * 40, "c" * 40, [], [])

    def test_legacy_browser_receipt_uses_complete_fixed_log_without_rewriting_receipt(self):
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp)
            check = {"lane": "browser", "test": "TestRequiredChromiumJourney"}
            for missing_field in (False, True):
                value = self.receipt(directory, "browser", [{"Test": check["test"], "Action": "pass"}])
                (directory / "tests.jsonl").replace(directory / "browser-execution.log")
                value["go_json_log"] = None
                if missing_field:
                    value.pop("go_json_log")
                original = json.dumps(value)
                (directory / "run.json").write_text(original)
                verified = release._verify_lane_evidence(directory, "browser", "b"*40, "c"*40, [check], [])
                self.assertEqual(verified, value)
                self.assertEqual((directory / "run.json").read_text(), original)
                self.assertEqual(release._lane_test_events(directory, value)[0]["Action"], "pass")

    def test_legacy_browser_receipt_still_requires_identity_discovery_and_every_terminal(self):
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp)
            for problem in ("missing_log", "empty_log", "failed", "skipped", "unfinished",
                            "missing_discovery", "wrong_identity", "invalid_explicit_log", "missing_command"):
                with self.subTest(problem=problem):
                    value = self.receipt(directory, "browser", [{"Test":"TestRequiredChromiumJourney", "Action":"pass"}])
                    log = directory / "browser-execution.log"
                    (directory / "tests.jsonl").replace(log)
                    value["go_json_log"] = None
                    if problem == "missing_log": log.unlink()
                    elif problem == "empty_log": log.write_text("")
                    elif problem in {"failed", "skipped", "unfinished"}:
                        action = {"failed":"fail", "skipped":"skip", "unfinished":"run"}[problem]
                        log.write_text(json.dumps({"Test":"TestRequiredChromiumJourney", "Action":action}) + "\n")
                    elif problem == "missing_discovery": (directory / "summary.json").unlink()
                    elif problem == "wrong_identity": value["tree"] = "d"*40
                    elif problem == "invalid_explicit_log": value["go_json_log"] = "../browser-execution.log"
                    elif problem == "missing_command": value["commands"] = []
                    (directory / "run.json").write_text(json.dumps(value))
                    with self.assertRaises(release.CheckIncompleteError):
                        release._verify_lane_evidence(directory, "browser", "b"*40, "c"*40, [], [])
            value = self.receipt(directory, "backend", [{"Package":"example/required", "Action":"pass"}])
            (directory / "tests.jsonl").replace(directory / "browser-execution.log")
            value["go_json_log"] = None
            (directory / "run.json").write_text(json.dumps(value))
            with self.assertRaises(release.CheckIncompleteError):
                release._verify_lane_evidence(directory, "backend", "b"*40, "c"*40, [], ["example/required"])

    def test_environment_block_keeps_candidate_pending_and_does_not_claim_regression(self):
        with tempfile.TemporaryDirectory() as temp:
            item = {"status": "checking", "attempt": 1}
            state = {"queue": [item], "in_flight": {"phase": "checks"}}
            release._mark_failed(Path(temp) / "state.json", state, item,
                                 release.CheckEnvironmentError("missing browser"), "checks")
            self.assertEqual(item["status"], "pending")
            self.assertEqual(item["failure"]["kind"], "environment")
            self.assertEqual(item["failure"]["candidate_verdict"], "not_evaluated")
            self.assertEqual(state["status"], "blocked")
            release._mark_failed(Path(temp) / "state.json", state, item, release.ReleaseError("assertion failed"), "checks")
            self.assertEqual(item["status"], "failed")
            self.assertEqual(len(item["attempt_history"]), 2)

    def test_independent_lanes_use_separate_checkouts_at_most_two_tasks(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root / "scripts/ci").mkdir(parents=True)
            (root / "scripts/ci/check_preparation.py").write_text("trusted fixture")
            execution = root / "candidate"; execution.mkdir()
            reports = root / "reports"; reports.mkdir()
            diagnostics = root / "diagnostics"; diagnostics.mkdir()
            counter = {"active": 0, "maximum": 0}
            guard = threading.Lock()
            checkouts = []
            def checkout(config, repo, path, sha):
                path.mkdir(); checkouts.append(path); return path
            def build(config, args, **kwargs):
                if args[0] == "/usr/bin/mkdir": Path(args[-1]).mkdir()
                return subprocess.CompletedProcess(args, 0, "", "")
            def run(args, **kwargs):
                index = args.index("--report-dir")
                directory = Path(args[index+1]); lane = directory.name
                with guard:
                    counter["active"] += 1
                    counter["maximum"] = max(counter["maximum"], counter["active"])
                time.sleep(0.05)
                self.receipt(directory, lane, [{"Action":"pass", **({"Test":"TestRequiredChromiumJourney"} if lane == "browser" else {})}])
                with guard: counter["active"] -= 1
                return subprocess.CompletedProcess(args, 0)
            with patch.object(release, "_private_check_checkout", side_effect=checkout), \
                 patch.object(release, "_build_command", side_effect=build), \
                 patch.object(release, "_prepare_check_dependencies"), \
                 patch.object(release, "_check_env", return_value={}), \
                 patch.object(release, "_verify_check_checkout_tree", return_value=1), \
                 patch.object(release, "_tree", return_value="c"*40), \
                 patch.object(release, "_worktree_git", side_effect=AssertionError("root must not read private clone Git metadata")), \
                 patch.object(release, "_run_check_process", side_effect=run):
                results = release._run_check_lanes({}, root, root, execution, reports, "a"*40, "b"*40,
                    {"selection_mode":"full"}, ["preflight","backend","frontend","browser"], [], [], "full", diagnostics)
            self.assertEqual(counter["maximum"], 2)
            self.assertEqual(len(set(checkouts)), 3)
            self.assertEqual([result["lane"] for result in results], ["preflight","backend","frontend","browser"])
