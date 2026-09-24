import copy
import hashlib
import json
import os
from pathlib import Path
import tempfile
import time
import unittest
from unittest.mock import patch

import affected_shadow as shadow
import quality_lanes


def make_log(package, tests, package_action="pass"):
    events = []
    for test, action in tests:
        events.append({"Action": "run", "Package": package, "Test": test})
        events.append({"Action": action, "Package": package, "Test": test, "Elapsed": 0.1})
    events.append({"Action": package_action, "Package": package, "Elapsed": 0.2})
    return "\n".join(json.dumps(event, separators=(",", ":")) for event in events)


class ShadowCollectionTests(unittest.TestCase):
    def setUp(self):
        self.plan = {
            "baseline_sha": "a" * 40, "baseline_tree": "b" * 40,
            "head_sha": "c" * 40, "head_tree": "d" * 40,
            "policy_fingerprint": "e" * 64,
            "source_clean": True,
            "source": {"working_tree_clean": True, "head_matches": True},
            "evidence_eligible": True,
            "observed_mode": "shadow",
            "changed_paths": ["internal/payment/app/service.go"],
            "selected_lanes": ["preflight", "backend", "browser"],
            "enforced": {"selected_lanes": ["preflight", "backend", "browser"]},
            "selected_checks": [
                {"lane": "backend", "path": "internal/payment/app/service_test.go", "test": "TestPaymentState"},
                {"lane": "browser", "path": "cmd/aicrm/payment_chromium_test.go", "test": "TestPaymentChromiumJourney"},
            ],
            "candidate_go_packages": [
                "example.test/crm/cmd/aicrm",
                "example.test/crm/internal/payment/app",
            ],
            "parent_prd": {"id": "docs/prd/trial.md", "path": "docs/prd/trial.md", "sha256": "f" * 64},
        }
        self.graph = {
            "schema": 1,
            "baseline_sha": self.plan["baseline_sha"], "baseline_tree": self.plan["baseline_tree"],
            "head_sha": self.plan["head_sha"], "head_tree": self.plan["head_tree"],
            "changed_paths": self.plan["changed_paths"],
            "graph_valid": True, "unowned_go_paths": [],
            "module": "example.test/crm", "graph_fingerprint": "9" * 64,
            "selected_packages": [
                {"dir": "internal/payment/app", "import_path": "example.test/crm/internal/payment/app",
                 "test_files": ["internal/payment/app/service_test.go"]},
                {"dir": "cmd/aicrm", "import_path": "example.test/crm/cmd/aicrm",
                 "test_files": ["cmd/aicrm/payment_chromium_test.go"]},
            ],
        }
        backend_log = "\n".join([
            make_log("example.test/crm/internal/payment/app", [
                ("TestPaymentState", "pass"), ("TestPaymentNewRegression", "pass"),
            ]),
            make_log("example.test/crm/cmd/aicrm", [
                ("TestPaymentChromiumJourney", "skip"),
                ("TestAnotherChromiumJourney", "skip"),
            ]),
            make_log("github.com/qianlan33333-png/AI-CRM-v3/cmd/aicrm", [
                ("TestDomesticReleaseInstalledAlipayCheckout", "skip"),
            ]),
        ])
        browser_log = make_log("example.test/crm/cmd/aicrm", [
            ("TestPaymentChromiumJourney", "pass"), ("TestAnotherChromiumJourney", "pass")])
        full_backend_command = [
            "bash", "scripts/run-go-with-donor-views.sh", "go", "test", "-json",
            "-p", "1", "-race", "-count=1", "-timeout=15m", "./...",
        ]
        self.runs = [
            {"lane": "preflight", "tested_sha": self.plan["head_sha"], "tree": self.plan["head_tree"],
             "policy_fingerprint": self.plan["policy_fingerprint"], "result": "success", "exit_code": 0,
             "elapsed_seconds": 3.0, "run_id": 100, "run_attempt": 1,
             "commands": [["python3", "scripts/dev_preflight.py", "fast"]]},
            {"lane": "backend", "tested_sha": self.plan["head_sha"], "tree": self.plan["head_tree"],
             "policy_fingerprint": self.plan["policy_fingerprint"], "result": "success", "exit_code": 0,
             "elapsed_seconds": 30.0, "run_id": 100, "run_attempt": 1,
             "commands": [full_backend_command], "go_json_text": backend_log},
            {"lane": "browser", "tested_sha": self.plan["head_sha"], "tree": self.plan["head_tree"],
             "policy_fingerprint": self.plan["policy_fingerprint"], "result": "success", "exit_code": 0,
             "elapsed_seconds": 55.0, "run_id": 100, "run_attempt": 1,
             "commands": [["python3", "scripts/dev_preflight.py", "browser"]],
             "go_json_text": browser_log},
        ]

    def collect(self, runs=None, plan=None, graph=None, pr_number=None,
                original_gate_result="success"):
        observations = copy.deepcopy(self.runs if runs is None else runs)
        import tempfile
        from pathlib import Path
        with tempfile.TemporaryDirectory() as temp:
            for run in observations:
                log_text = run.pop("go_json_text", None)
                if log_text is None:
                    run["go_json_log"] = None
                else:
                    log_path = Path(temp) / (run["lane"] + ".jsonl")
                    log_path.write_text(log_text)
                    run["go_json_log"] = str(log_path)
            return shadow.collect_execution(plan or self.plan, graph or self.graph, observations,
                                            pr_number=pr_number,
                                            original_gate_result=original_gate_result)

    def test_full_backend_package_run_counts_new_tests_and_browser_lane_owns_journey(self):
        result = self.collect()
        self.assertTrue(result["required_results_passed"])
        self.assertTrue(result["measurement_eligible"])
        payment = next(item for item in result["package_measurements"]
                       if item["package"].endswith("internal/payment/app"))
        self.assertEqual(payment["executed_top_level_tests"], 2)
        self.assertEqual(result["unknown_skips"], [])
        self.assertEqual(result["candidate_result"], "success")
        self.assertTrue(result["stage_receipt_required"])
        self.assertEqual(result["stage_owned_skips"][0]["test"],
                         "TestDomesticReleaseInstalledAlipayCheckout")

    def test_green_package_terminal_with_no_test_events_is_not_a_pass(self):
        runs = copy.deepcopy(self.runs)
        runs[1]["go_json_text"] = "\n".join([
            '{"Action":"pass","Package":"example.test/crm/internal/payment/app","Elapsed":0.3}',
            make_log("example.test/crm/cmd/aicrm", [("TestPaymentChromiumJourney", "skip")]),
        ])
        result = self.collect(runs)
        self.assertFalse(result["required_results_passed"])
        self.assertFalse(result["measurement_eligible"])
        self.assertTrue(any("test-file inventory but no executed tests" in item for item in result["problems"]))

    def test_verified_no_test_package_is_compile_only_not_a_test_pass(self):
        plan = copy.deepcopy(self.plan)
        graph = copy.deepcopy(self.graph)
        package = "example.test/crm/internal/payment/domain"
        plan["candidate_go_packages"].append(package)
        graph["selected_packages"].append({"dir": "internal/payment/domain", "import_path": package,
                                           "test_files": [], "source_files": ["internal/payment/domain/domain.go"],
                                           "embed_files": []})
        runs = copy.deepcopy(self.runs)
        no_test_events = [
            {"Action": "output", "Package": package, "Output": "? example.test/crm/internal/payment/domain [no test files]\n"},
            {"Action": "skip", "Package": package, "Elapsed": 0},
        ]
        runs[1]["go_json_text"] += "\n" + "\n".join(json.dumps(event) for event in no_test_events)
        result = self.collect(runs, plan=plan, graph=graph)
        self.assertTrue(result["required_results_passed"])
        inventory = result["package_test_inventory"][package]
        self.assertEqual(inventory["execution_kind"], "compile_only_no_tests")
        self.assertEqual(inventory["executed_top_level_tests"], 0)
        measurement = next(item for item in result["package_measurements"] if item["package"] == package)
        self.assertEqual(measurement["execution_kind"], "compile_only_no_tests")
        self.assertEqual(measurement["terminal_action"], "skip")

    def test_empty_inventory_without_no_test_compile_event_fails_closed(self):
        plan = copy.deepcopy(self.plan)
        graph = copy.deepcopy(self.graph)
        package = "example.test/crm/internal/payment/domain"
        plan["candidate_go_packages"].append(package)
        graph["selected_packages"].append({"dir": "internal/payment/domain", "import_path": package,
                                           "test_files": [], "source_files": ["internal/payment/domain/domain.go"],
                                           "embed_files": []})
        runs = copy.deepcopy(self.runs)
        runs[1]["go_json_text"] += "\n" + json.dumps({"Action": "pass", "Package": package, "Elapsed": 0.1})
        result = self.collect(runs, plan=plan, graph=graph)
        self.assertFalse(result["required_results_passed"])
        self.assertTrue(any("lacks successful no-test compile evidence" in item for item in result["problems"]))

    def test_selected_frontend_check_is_not_ignored_when_only_backend_runs(self):
        plan = copy.deepcopy(self.plan)
        plan["selected_lanes"] = ["backend", "frontend"]
        plan["selected_checks"] = [{"lane": "frontend", "path": "scripts/openapi-contract.mjs"}]
        runs = [copy.deepcopy(self.runs[1])]
        result = self.collect(runs, plan=plan)
        self.assertFalse(result["required_results_passed"])
        self.assertFalse(result["measurement_eligible"])
        self.assertTrue(any("selected lane did not execute: frontend" in item for item in result["problems"]))
        self.assertTrue(any("selected check lane did not execute" in item for item in result["problems"]))

    def test_required_skip_or_missing_test_is_not_a_pass(self):
        runs = copy.deepcopy(self.runs)
        runs[1]["go_json_text"] = runs[1]["go_json_text"].replace(
            '"Action":"pass","Package":"example.test/crm/internal/payment/app","Test":"TestPaymentState"',
            '"Action":"skip","Package":"example.test/crm/internal/payment/app","Test":"TestPaymentState"', 1)
        result = self.collect(runs)
        self.assertFalse(result["required_results_passed"])
        self.assertFalse(result["measurement_eligible"])
        self.assertTrue(any("TestPaymentState" in item for item in result["problems"]))

        runs = copy.deepcopy(self.runs)
        runs[1]["go_json_text"] = runs[1]["go_json_text"].replace(
            '{"Action":"run","Package":"example.test/crm/internal/payment/app","Test":"TestPaymentState"}\n', "")
        runs[1]["go_json_text"] = runs[1]["go_json_text"].replace(
            '{"Action":"pass","Package":"example.test/crm/internal/payment/app","Test":"TestPaymentState","Elapsed":0.1}\n', "")
        result = self.collect(runs)
        self.assertFalse(result["required_results_passed"])
        self.assertTrue(any("unexecuted" in item for item in result["problems"]))

        runs = copy.deepcopy(self.runs)
        runs[2].pop("go_json_text")
        result = self.collect(runs)
        self.assertFalse(result["required_results_passed"])
        self.assertTrue(any("no Go JSON execution log" in item for item in result["problems"]))

    def test_unmapped_optional_skip_makes_measurement_ineligible_not_required_regression(self):
        runs = copy.deepcopy(self.runs)
        runs[1]["go_json_text"] = runs[1]["go_json_text"].replace(
            '{"Action":"pass","Package":"example.test/crm/internal/payment/app","Elapsed":0.2}',
            '{"Action":"skip","Package":"example.test/crm/internal/payment/app","Test":"TestOptionalProvider"}\n'
            '{"Action":"pass","Package":"example.test/crm/internal/payment/app","Elapsed":0.2}')
        result = self.collect(runs)
        self.assertFalse(result["required_results_passed"])
        self.assertEqual(result["candidate_result"], "unknown")
        self.assertFalse(result["measurement_eligible"])
        self.assertEqual(result["unknown_skips"], [{
            "lane": "backend", "package": "example.test/crm/internal/payment/app", "test": "TestOptionalProvider",
        }])

    def test_skip_outside_candidate_packages_is_observed_without_blocking_candidate(self):
        runs = copy.deepcopy(self.runs)
        runs[1]["go_json_text"] += "\n" + make_log(
            "example.test/crm/internal/optional", [("TestOptionalProvider", "skip")])
        result = self.collect(runs)
        self.assertTrue(result["required_results_passed"])
        self.assertEqual(result["candidate_result"], "success")
        self.assertTrue(result["measurement_eligible"])
        self.assertEqual(result["unknown_skips"], [])
        self.assertEqual(result["unselected_skips"], [{
            "lane": "backend", "package": "example.test/crm/internal/optional",
            "test": "TestOptionalProvider",
        }])

    def test_trial_completion_uses_exact_trusted_enforced_lanes_not_all_five(self):
        plan = copy.deepcopy(self.plan)
        plan["enforced"] = {"selected_lanes": ["preflight"]}
        plan["candidate_go_packages"] = []
        plan["selected_lanes"] = ["preflight"]
        plan["selected_checks"] = []
        graph = copy.deepcopy(self.graph)
        graph["selected_packages"] = []
        runs = [copy.deepcopy(self.runs[0])]
        result = self.collect(runs, plan=plan, graph=graph, pr_number=321)
        self.assertTrue(result["trial_observation"]["complete"])
        self.assertEqual(result["trial_observation"]["required_lanes"], ["preflight"])

    def test_original_gate_failure_with_successful_candidate_lanes_is_a_miss(self):
        result = self.collect(pr_number=321, original_gate_result="failure")
        self.assertEqual(result["original_ci_lane_failures"], [])
        self.assertEqual(result["candidate_result"], "success")
        self.assertEqual(result["trial_observation"]["original_ci_result"], "failure")
        self.assertEqual(result["trial_observation"]["candidate_result"], "success")

    def test_failed_selected_lane_with_green_package_log_is_unknown_not_a_miss(self):
        runs = copy.deepcopy(self.runs)
        runs[1]["result"] = "failed"
        runs[1]["exit_code"] = 1
        result = self.collect(runs, pr_number=321, original_gate_result="failure")
        self.assertEqual(result["original_ci_lane_failures"], ["backend"])
        self.assertEqual(result["candidate_result"], "unknown")
        self.assertEqual(result["trial_observation"]["candidate_result"], "unknown")

    def test_graph_path_inventory_policy_and_command_mismatch_fail_closed(self):
        bad_graph = copy.deepcopy(self.graph)
        bad_graph["changed_paths"] = ["other.go"]
        with self.assertRaisesRegex(shadow.ShadowEvidenceError, "changed paths"):
            self.collect(graph=bad_graph)

        bad_graph = copy.deepcopy(self.graph)
        bad_graph["selected_packages"][0].pop("test_files")
        with self.assertRaisesRegex(shadow.ShadowEvidenceError, "package inventory"):
            self.collect(graph=bad_graph)

        runs = copy.deepcopy(self.runs)
        runs[1]["tree"] = "e" * 40
        with self.assertRaisesRegex(shadow.ShadowEvidenceError, "tested SHA/tree"):
            self.collect(runs)

        runs = copy.deepcopy(self.runs)
        runs[1]["policy_fingerprint"] = "f" * 64
        with self.assertRaisesRegex(shadow.ShadowEvidenceError, "policy fingerprint"):
            self.collect(runs)

        runs = copy.deepcopy(self.runs)
        runs[1]["commands"][0].extend(["-run", "^TestPaymentState$"])
        result = self.collect(runs)
        self.assertFalse(result["measurement_eligible"])
        self.assertTrue(any("filtered the affected package suite" in item for item in result["problems"]))


def _receipt(record, commands):
    return {
        "schema": 1, "kind": "canonical_ci_lane_execution",
        "workflow_path": ".github/workflows/ci.yml", "conclusion": "success",
        "pr_number": record["pr_number"], "run_id": record["run_id"],
        "run_attempt": record["run_attempt"], "tested_sha": record["source_sha"],
        "tree": record["tree"], "policy_fingerprint": record["policy_fingerprint"],
        "parent_prd_id": record["parent_prd_id"], "parent_prd_sha": record["parent_prd_sha"],
        "commands_sha256": shadow._command_digest(commands),
        "elapsed_seconds": record["elapsed_seconds"],
        "includes_setup": True, "includes_vet": True, "includes_test_suite": True,
        "test_json_sha256": "9" * 64,
    }


def refresh_capability_cost(record, deployment_seconds=20.0):
    phase_timings = {"build": deployment_seconds / 3,
                     "stage_install": deployment_seconds / 3,
                     "production_install": deployment_seconds / 6,
                     "production_readback": deployment_seconds / 6,
                     "total": deployment_seconds}
    installed_manifest = "a" * 64
    state_snapshot = {
        "schema_version": 1, "status": "ready",
        "processed_sha": record["source_sha"],
        "deployed_source_sha": record["source_sha"],
        "prod_installed_sha": record["source_sha"],
        "prod_installed_manifest_sha256": installed_manifest,
        "last_release_timings_seconds": phase_timings,
    }
    manifest_snapshot = {
        "schema_version": 1, "source_sha": record["source_sha"],
        "source_tree": record["tree"], "release_files_sha256": installed_manifest,
        "build_mode": "full", "phase_timings_seconds": {"build": phase_timings["build"]},
    }
    state_digest = shadow._json_digest(state_snapshot)
    manifest_digest = shadow._json_digest(manifest_snapshot)
    identity = {
        "capability_id": record["scope_id"],
        "parent_prd_id": record["parent_prd_id"], "parent_prd_sha": record["parent_prd_sha"],
        "pr_number": record["pr_number"], "source_sha": record["source_sha"],
        "tree": record["tree"], "policy_fingerprint": record["policy_fingerprint"],
    }
    deployment = {
        "schema": 1, "kind": "domestic_release_state_timing", "conclusion": "success",
        **identity, "elapsed_seconds": deployment_seconds,
        "parent_prd_source_sha256": record["parent_prd_sha"],
        "state_path": "/tmp/domestic-release-state.json", "state_file_sha256": state_digest,
        "state_snapshot": state_snapshot, "state_snapshot_sha256": state_digest,
        "manifest_path": "/tmp/domestic-release.json", "manifest_file_sha256": manifest_digest,
        "manifest_snapshot": manifest_snapshot, "manifest_snapshot_sha256": manifest_digest,
        "phase_timings_seconds": phase_timings,
        "installed_source_sha": record["source_sha"], "installed_manifest_sha256": installed_manifest,
        "deployment_id": shadow._json_digest({
            "state_file_sha256": state_digest, "manifest_file_sha256": manifest_digest, **identity}),
    }
    ci_seconds = record["ci_wall_seconds"]
    record["capability_cost_receipt"] = {
        "schema": 1, "metric": "ci_plus_deploy_total_seconds",
        "capability_id": record["scope_id"],
        "parent_prd_id": record["parent_prd_id"], "parent_prd_sha": record["parent_prd_sha"],
        "pr_number": record["pr_number"], "source_sha": record["source_sha"], "tree": record["tree"],
        "policy_fingerprint": record["policy_fingerprint"],
        "ci_run_id": record["run_id"], "ci_run_attempt": record["run_attempt"],
        "ci_seconds": ci_seconds, "backend_lane_seconds": record["elapsed_seconds"],
        "deployment_seconds": deployment_seconds,
        "total_seconds": ci_seconds + deployment_seconds,
        "ci_receipt_sha256": shadow._json_digest({
            "backend_execution_receipt": record["execution_receipt"],
            "ci_wall_receipt": record["ci_wall_receipt"],
        }),
        "deployment_receipt": deployment,
    }


def benchmark_record(pair_id, mode, pr_number, scope_type="class", scope_id="payment", **overrides):
    test_command = ["go", "test", "-json", "-race", "-count=1", "-timeout=15m",
                    "./..." if mode == "full" else "./internal/payment/app"]
    commands = [["go", "vet", "./..."] if mode == "full"
                else ["go", "vet", "./internal/payment/app"], test_command]
    value = {
        "evidence_kind": "real_command", "pair_id": pair_id, "mode": mode,
        "scope_type": scope_type, "scope_id": scope_id,
        "source_sha": f"{pr_number:040x}", "tree": f"{pr_number + 1:040x}",
        "environment_fingerprint": "c" * 64, "cache_fingerprint": "d" * 64,
        "policy_fingerprint": "e" * 64,
        "parent_prd_id": "docs/prd/trial.md", "parent_prd_sha": "f" * 64,
        "pr_number": pr_number, "run_id": 9000 + pr_number, "run_attempt": 1,
        "commands": commands, "elapsed_seconds": 100.0 if mode == "full" else 50.0,
        "exit_code": 0,
    }
    if mode == "targeted":
        value["selected_package_dirs"] = ["./internal/payment/app"]
    value.update(overrides)
    value["execution_receipt"] = _receipt(value, value["commands"])
    if scope_type == "capability" and mode == "targeted":
        value["ci_wall_seconds"] = 50.0
        value["ci_wall_receipt"] = {
            "schema": 1, "kind": "github_run_plan_to_required_check_wall_time",
            "run_id": value["run_id"], "run_attempt": value["run_attempt"],
            "plan_job": "plan", "plan_conclusion": "success",
            "plan_started_at": "2026-09-25T00:00:00Z",
            "required_check_job": "check", "required_check_conclusion": "success",
            "required_check_started_at": "2026-09-25T00:00:40Z",
            "required_check_completed_at": "2026-09-25T00:00:50Z",
            "elapsed_seconds": 50.0, "jobs_json_sha256": "a" * 64,
        }
        refresh_capability_cost(value)
    return value


def defect_replay():
    def side(sha, tree, run_id, outcome, command_id):
        return {
            "source_sha": sha, "tree": tree, "run_id": run_id, "run_attempt": 1,
            "test_action": outcome,
            "execution_receipt": {"conclusion": outcome, "tested_sha": sha, "tree": tree,
                                  "run_id": run_id, "run_attempt": 1, "command_id": command_id},
        }
    return {
        "defect_id": "payment-regression-1",
        "parent_prd_id": "docs/prd/trial.md", "parent_prd_sha": "f" * 64,
        "package": "example.test/crm/internal/payment/app",
        "test": "TestPaymentState",
        "selected_packages": ["example.test/crm/internal/payment/app"],
        "vulnerable": side("1" * 40, "2" * 40, 123, "fail", "bad-run"),
        "fixed": side("3" * 40, "4" * 40, 124, "pass", "fixed-run"),
    }


def trial_observation(pr_number, source_sha, tree, policy_fingerprint,
                     original="success", candidate="success", run_id=None):
    run_id = run_id if run_id is not None else 12000 + pr_number
    commands_sha = shadow._command_digest([["python3", "scripts/dev_preflight.py", "fast"]])
    observation = {
        "schema": 1, "kind": "pr_shadow_observation", "pr_number": pr_number,
        "run_id": run_id, "run_attempt": 1,
        "baseline_sha": "1" * 40, "baseline_tree": "2" * 40,
        "source_sha": source_sha, "tree": tree,
        "policy_fingerprint": policy_fingerprint,
        "parent_prd_id": "docs/prd/trial.md", "parent_prd_sha": "f" * 64,
        "original_ci_result": original, "candidate_result": candidate,
        "required_lanes": ["preflight"], "complete": True,
        "lane_receipts": [{
            "lane": "preflight", "result": "success", "exit_code": 0,
            "elapsed_seconds": 10.0, "run_id": run_id, "run_attempt": 1,
            "tested_sha": source_sha, "tree": tree,
            "policy_fingerprint": policy_fingerprint, "commands_sha256": commands_sha,
        }],
    }
    observation["receipt_id"] = shadow._json_digest(observation)
    return observation


class BenchmarkEvidenceTests(unittest.TestCase):
    def inputs(self):
        records = []
        for index in range(10):
            pr = 100 + index
            for scope_type, scope_id in (("class", "payment"), ("capability", "commerce")):
                pair = f"pr{pr}-{scope_type}"
                records.extend([
                    benchmark_record(pair, "full", pr, scope_type, scope_id),
                    benchmark_record(pair, "targeted", pr, scope_type, scope_id),
                ])
        baseline = {"commerce": {
            "metric": "ci_plus_deploy_total_seconds",
            "parent_prd_id": "docs/prd/trial.md", "parent_prd_sha": "f" * 64,
            "baseline_id": "commerce-baseline-1", "receipt_ids": ["base-1", "base-2"],
            "sample_count": 1, "total_seconds": 800.0,
        }}
        return records, baseline

    def evaluate(self, records=None, baselines=None, replays=None, defects=None,
                 observations=None, **kwargs):
        if records is None or baselines is None:
            default_records, default_baselines = self.inputs()
            records = default_records if records is None else records
            baselines = default_baselines if baselines is None else baselines
        if observations is None:
            first_by_pr = {}
            for record in records:
                if isinstance(record, dict):
                    first_by_pr.setdefault(record.get("pr_number"), record)
            observations = [trial_observation(
                pr, record["source_sha"], record["tree"], record["policy_fingerprint"])
                for pr, record in first_by_pr.items() if isinstance(pr, int) and pr > 0]
        return shadow.evaluate_benchmarks(
            records, ["payment"], ["commerce"], baselines,
            replays if replays is not None else [defect_replay()],
            defects if defects is not None else ["payment-regression-1"],
            "docs/prd/trial.md", "f" * 64,
            observations=observations, **kwargs)

    def test_ten_distinct_pr_trial_needs_three_class_pairs_baseline_and_defect_replay(self):
        result = self.evaluate()
        self.assertTrue(result["activation_ready"], result["unmet_reasons"])
        self.assertEqual(result["distinct_trial_pr_count"], 10)
        self.assertEqual(result["by_class"]["payment"]["distinct_pr_count"], 10)
        self.assertEqual(result["by_class"]["payment"]["p50_savings"], 0.5)
        self.assertEqual(result["by_capability"]["commerce"]["baseline_ci_deploy_seconds"], 800.0)
        self.assertEqual(result["eligible_classes"], ["payment"])

    def test_repeated_pairs_from_one_pr_do_not_meet_ten_pr_trial(self):
        records, baselines = self.inputs()
        repeated = []
        for index in range(10):
            pr = 100
            for scope_type, scope_id in (("class", "payment"), ("capability", "commerce")):
                pair = f"duplicate{index}-{scope_type}"
                repeated.extend([
                    benchmark_record(pair, "full", pr, scope_type, scope_id),
                    benchmark_record(pair, "targeted", pr, scope_type, scope_id),
                ])
        source = repeated[0]
        duplicate_observation = trial_observation(
            100, source["source_sha"], source["tree"], source["policy_fingerprint"])
        result = self.evaluate(repeated, baselines, observations=[duplicate_observation] * 10)
        self.assertFalse(result["activation_ready"])
        self.assertEqual(result["distinct_trial_pr_count"], 1)
        self.assertEqual(result["distinct_observation_receipt_count"], 1)
        self.assertTrue(any("duplicate benchmark pair" in item for item in result["rejected_records"]))

    def test_missing_or_synthetic_execution_receipt_never_enables_fast_paths(self):
        records, baselines = self.inputs()
        records[0].pop("execution_receipt")
        result = self.evaluate(records, baselines)
        self.assertFalse(result["activation_ready"])
        self.assertTrue(result["rejected_records"])

        records, baselines = self.inputs()
        records[0]["evidence_kind"] = "summed_package_estimate"
        result = self.evaluate(records, baselines)
        self.assertFalse(result["activation_ready"])

    def test_only_same_commit_environment_cache_policy_and_parent_prd_pairs_count(self):
        records, baselines = self.inputs()
        for index in range(0, len(records), 2):
            # Pair is full,targeted; break one identity field only on the target side.
            if records[index]["scope_type"] == "class":
                records[index + 1]["cache_fingerprint"] = "a" * 64
                records[index + 1]["execution_receipt"] = _receipt(records[index + 1],
                                                                    records[index + 1]["commands"])
                break
        result = self.evaluate(records, baselines)
        self.assertFalse(result["activation_ready"])
        self.assertTrue(any("identity mismatch" in item for item in result["rejected_records"]))

    def test_capability_cost_requires_measured_baseline_and_known_defect_replay(self):
        records, baselines = self.inputs()
        result = self.evaluate(records, {})
        self.assertFalse(result["activation_ready"])
        self.assertTrue(any("capability total" in item for item in result["unmet_reasons"]))

        result = self.evaluate(records, baselines, replays=[], defects=["payment-regression-1"])
        self.assertFalse(result["activation_ready"])
        self.assertEqual(result["missing_known_defect_replays"], ["payment-regression-1"])

    def test_class_p50_must_meet_savings_and_cover_three_distinct_prs(self):
        records, baselines = self.inputs()
        for record in records:
            if record["scope_type"] == "class" and record["mode"] == "targeted":
                record["elapsed_seconds"] = 75.0
                record["execution_receipt"] = _receipt(record, record["commands"])
        result = self.evaluate(records, baselines)
        self.assertFalse(result["activation_ready"])
        self.assertEqual(result["by_class"]["payment"]["p50_savings"], 0.25)

        records, baselines = self.inputs()
        for record in records:
            if record["scope_type"] == "class":
                record["pr_number"] = 100
                record["run_id"] = 9100
                record["execution_receipt"] = _receipt(record, record["commands"])
        result = self.evaluate(records, baselines)
        self.assertFalse(result["activation_ready"])
        self.assertEqual(result["by_class"]["payment"]["distinct_pr_count"], 1)

    def test_capability_target_cost_must_not_exceed_measured_baseline(self):
        records, baselines = self.inputs()
        for record in records:
            if record["scope_type"] == "capability" and record["mode"] == "targeted":
                refresh_capability_cost(record, deployment_seconds=100.0)
        result = self.evaluate(records, baselines)
        self.assertFalse(result["activation_ready"])
        self.assertFalse(result["by_capability"]["commerce"]["passes"])

    def test_capability_cost_requires_candidate_deployment_receipt(self):
        records, baselines = self.inputs()
        target = next(record for record in records
                      if record["scope_type"] == "capability" and record["mode"] == "targeted")
        target.pop("capability_cost_receipt")
        result = self.evaluate(records, baselines)
        self.assertFalse(result["activation_ready"])
        self.assertTrue(result["rejected_records"])

    def test_backend_lane_elapsed_alone_cannot_satisfy_capability_ci_cost(self):
        records, baselines = self.inputs()
        target = next(record for record in records
                      if record["scope_type"] == "capability" and record["mode"] == "targeted")
        target.pop("ci_wall_receipt")
        target.pop("ci_wall_seconds")
        target["capability_cost_receipt"]["ci_seconds"] = target["elapsed_seconds"]
        target["capability_cost_receipt"]["total_seconds"] = (
            target["elapsed_seconds"] + target["capability_cost_receipt"]["deployment_seconds"])
        result = self.evaluate(records, baselines)
        self.assertFalse(result["activation_ready"])
        self.assertFalse(result["by_capability"]["commerce"]["passes"])
        self.assertTrue(any("malformed" in item or "incomplete paired" in item
                            for item in result["rejected_records"]))

    def test_capability_total_sums_all_pr_deployments_without_pr_average(self):
        records, baselines = self.inputs()
        for record in records:
            if record["scope_type"] == "capability" and record["mode"] == "targeted":
                refresh_capability_cost(record, deployment_seconds=50.0)
        result = self.evaluate(records, baselines)
        self.assertFalse(result["activation_ready"])
        self.assertEqual(result["by_capability"]["commerce"]["total_targeted_ci_deploy_seconds"], 1000.0)
        self.assertEqual(result["by_capability"]["commerce"]["cost_change"], 0.25)

    def test_empty_known_defect_inventory_cannot_enable_any_class(self):
        result = self.evaluate(defects=[])
        self.assertFalse(result["activation_ready"])
        self.assertEqual(result["eligible_classes"], [])
        self.assertTrue(any("inventory is empty" in item for item in result["unmet_reasons"]))

    def test_class_trial_without_any_registered_capability_cost_cannot_enable(self):
        records, _ = self.inputs()
        class_records = [record for record in records if record["scope_type"] == "class"]
        result = shadow.evaluate_benchmarks(
            class_records, ["payment"], [], {}, [defect_replay()], ["payment-regression-1"],
            "docs/prd/trial.md", "f" * 64)
        self.assertFalse(result["activation_ready"])
        self.assertEqual(result["eligible_classes"], [])
        self.assertEqual(result["by_capability"], {})
        self.assertTrue(any("capability cost evidence inventory is empty" in item
                            for item in result["unmet_reasons"]))

    def test_ten_observations_do_not_require_ten_timing_pairs(self):
        records, baselines = self.inputs()
        records = [item for item in records
                   if item["scope_type"] == "capability"
                   or (item["scope_type"] == "class" and item["pr_number"] in {100, 101, 102})]
        result = self.evaluate(records, baselines)
        self.assertTrue(result["activation_ready"], result["unmet_reasons"])
        self.assertEqual(result["distinct_trial_pr_count"], 10)
        self.assertEqual(result["by_class"]["payment"]["distinct_pr_count"], 3)

    def test_original_failure_and_candidate_failure_is_countable_but_candidate_unknown_blocks(self):
        records, baselines = self.inputs()
        observations = [trial_observation(
            pr, next(item["source_sha"] for item in records if item["pr_number"] == pr),
            next(item["tree"] for item in records if item["pr_number"] == pr),
            "e" * 64,
            original="failure" if pr == 100 else "success",
            candidate="failure" if pr == 100 else "success")
            for pr in range(100, 110)]
        result = self.evaluate(records, baselines, observations=observations)
        self.assertEqual(result["distinct_trial_pr_count"], 10)

        observations[0] = trial_observation(
            100, observations[0]["source_sha"], observations[0]["tree"], "e" * 64,
            original="failure", candidate="unknown")
        result = self.evaluate(records, baselines, observations=observations)
        self.assertFalse(result["activation_ready"])
        self.assertEqual(result["distinct_trial_pr_count"], 9)
        self.assertEqual(result["unknown_observation_count"], 1)

    def test_candidate_pass_after_original_gate_failure_is_a_confirmed_miss(self):
        records, baselines = self.inputs()
        observations = [trial_observation(
            pr, next(item["source_sha"] for item in records if item["pr_number"] == pr),
            next(item["tree"] for item in records if item["pr_number"] == pr), "e" * 64,
            original="failure" if pr == 100 else "success")
            for pr in range(100, 110)]
        result = self.evaluate(records, baselines, observations=observations)
        self.assertFalse(result["activation_ready"])
        self.assertEqual(len(result["confirmed_misses"]), 1)

    def test_only_classes_meeting_their_own_threshold_are_eligible(self):
        records, baselines = self.inputs()
        for pr in range(100, 110):
            pair = f"pr{pr}-tooling"
            records.extend([
                benchmark_record(pair, "full", pr, "class", "tooling"),
                benchmark_record(pair, "targeted", pr, "class", "tooling",
                                 elapsed_seconds=75.0),
            ])
            target = records[-1]
            target["execution_receipt"] = _receipt(target, target["commands"])
        first_by_pr = {}
        for record in records:
            first_by_pr.setdefault(record["pr_number"], record)
        observations = [trial_observation(pr, record["source_sha"], record["tree"],
                                          record["policy_fingerprint"])
                        for pr, record in first_by_pr.items()]
        result = shadow.evaluate_benchmarks(
            records, ["payment", "tooling"], ["commerce"], baselines, [defect_replay()],
            ["payment-regression-1"], "docs/prd/trial.md", "f" * 64,
            observations=observations)
        self.assertTrue(result["activation_ready"])
        self.assertEqual(result["eligible_classes"], ["payment"])
        self.assertFalse(result["by_class"]["tooling"]["passes"])

    def test_pair_adapter_consumes_real_quality_lane_files_and_release_phase_timings(self):
        head = __import__("subprocess").check_output(
            ["git", "rev-parse", "HEAD"], cwd=quality_lanes.ROOT, text=True).strip()
        tree = __import__("subprocess").check_output(
            ["git", "rev-parse", "HEAD^{tree}"], cwd=quality_lanes.ROOT, text=True).strip()
        policy = "e" * 64
        full_commands = [
            ["bash", "scripts/run-go-with-donor-views.sh", "go", "vet", "./..."],
            ["bash", "scripts/run-go-with-donor-views.sh", "go", "test", "-json", "-p", "1",
             "-race", "-count=1", "-timeout=15m", "./..."],
        ]
        target_commands = [
            ["bash", "scripts/run-go-with-donor-views.sh", "go", "vet", "./..."],
            ["bash", "scripts/run-go-with-donor-views.sh", "go", "test", "-json", "-p", "1",
             "-race", "-count=1", "-timeout=15m", "./internal/payment/app"],
        ]
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            env = {
                "GITHUB_ACTIONS": "true", "GITHUB_PR_NUMBER": "321",
                "GITHUB_RUN_ID": "654321", "GITHUB_RUN_ATTEMPT": "1",
                "RUNNER_OS": "Linux", "RUNNER_ARCH": "X64",
                "ImageOS": "ubuntu22", "ImageVersion": "202609.1",
            }

            def make_run(name, commands):
                report = root / name
                report.mkdir()
                (report / "backend-go-test.jsonl").write_text(
                    '{"Action":"pass","Package":"example.test/crm/internal/payment/app","Elapsed":0.3}\n')
                execution = {"commands": commands, "go_json_log": "backend-go-test.jsonl"}
                with patch.dict(os.environ, env), patch.object(
                        quality_lanes, "_run_policy_fingerprint", return_value=policy):
                    quality_lanes._write_lane_receipt(
                        report, "backend", execution, time.monotonic() - (100 if name == "full" else 40),
                        "success", 0)
                return report / "run.json"

            full_run = make_run("full", full_commands)
            targeted_run = make_run("targeted", target_commands)
            release_state = {
                "schema_version": 1, "status": "ready", "processed_sha": head,
                "deployed_source_sha": head, "prod_installed_sha": head,
                "prod_installed_manifest_sha256": "a" * 64,
                "last_release_timings_seconds": {
                    "check": 6.0, "build": 112.3, "stage_install": 4.4,
                    "production_install": 5.1, "production_readback": 0.4,
                    "total": 129.8, "transfer": 2.4,
                },
            }
            release_manifest = {
                "schema_version": 1, "source_sha": head, "source_tree": tree,
                "release_files_sha256": "a" * 64, "build_mode": "full",
                "phase_timings_seconds": {"build": 112.3},
            }
            jobs = {
                "databaseId": 654321, "attempt": 1,
                "jobs": [
                    {"name": "plan", "conclusion": "success",
                     "startedAt": "2026-09-25T10:00:00Z",
                     "completedAt": "2026-09-25T10:01:00Z"},
                    {"name": "backend", "conclusion": "success",
                     "startedAt": "2026-09-25T10:02:00Z",
                     "completedAt": "2026-09-25T10:45:00Z"},
                    {"name": "check", "conclusion": "success",
                     "startedAt": "2026-09-25T10:50:00Z",
                     "completedAt": "2026-09-25T11:00:00Z"},
                ],
            }
            state_path, manifest_path, jobs_path = (
                root / "state.json", root / "domestic-release.json", root / "ci-jobs.json")
            state_path.write_text(json.dumps(release_state))
            manifest_path.write_text(json.dumps(release_manifest))
            jobs_path.write_text(json.dumps(jobs))
            with self.assertRaisesRegex(shadow.ShadowEvidenceError, "requires --ci-jobs"):
                shadow.benchmark_records_from_lane_runs(
                    full_run, targeted_run, "capability", "payment", state_path,
                    manifest_path, quality_lanes.ROOT)
            records = shadow.benchmark_records_from_lane_runs(
                full_run, targeted_run, "capability", "payment", state_path, manifest_path,
                quality_lanes.ROOT, jobs_path)
            release_state["processed_sha"] = "f" * 40
            state_path.write_text(json.dumps(release_state))
            with self.assertRaisesRegex(shadow.ShadowEvidenceError, "cursor/manifest"):
                shadow.benchmark_records_from_lane_runs(
                    full_run, targeted_run, "capability", "payment", state_path,
                    manifest_path, quality_lanes.ROOT, jobs_path)

        self.assertTrue(shadow._valid_execution_record(records[0]), json.dumps(records[0], indent=2))
        self.assertTrue(shadow._valid_capability_cost_receipt(
            records[1], records[1]["elapsed_seconds"], records[1]["execution_receipt"]),
            json.dumps(records[1]["capability_cost_receipt"], indent=2))
        self.assertTrue(shadow._valid_execution_record(records[1]), json.dumps(records[1], indent=2))
        receipt = records[1]["capability_cost_receipt"]["deployment_receipt"]
        self.assertEqual(receipt["kind"], "domestic_release_state_timing")
        self.assertEqual(receipt["elapsed_seconds"], 129.8)
        self.assertEqual(receipt["phase_timings_seconds"]["build"], 112.3)
        cost = records[1]["capability_cost_receipt"]
        self.assertEqual(cost["ci_seconds"], 3600.0)
        self.assertNotEqual(cost["ci_seconds"], records[1]["elapsed_seconds"])
        self.assertTrue(receipt["state_file_sha256"])
        self.assertTrue(receipt["manifest_file_sha256"])

    def test_pair_adapter_rejects_changed_go_log_or_different_run(self):
        head = __import__("subprocess").check_output(
            ["git", "rev-parse", "HEAD"], cwd=quality_lanes.ROOT, text=True).strip()
        commands = [["go", "vet", "./..."], ["go", "test", "-json", "-race", "-count=1", "./..."]]
        with tempfile.TemporaryDirectory() as temporary:
            report = Path(temporary)
            (report / "backend-go-test.jsonl").write_text("first\n")
            fake = benchmark_record("actual", "full", 321)
            receipt = fake["execution_receipt"]
            receipt.update({"kind": "canonical_ci_lane_execution", "workflow_path": ".github/workflows/ci.yml",
                            "tested_sha": head, "tree": "2" * 40, "run_id": 654321,
                            "run_attempt": 1, "commands_sha256": shadow._command_digest(commands),
                            "elapsed_seconds": 10.0, "test_json_sha256": hashlib.sha256(b"first\n").hexdigest()})
            run = {"lane": "backend", "result": "success", "exit_code": 0, "tested_sha": head,
                   "tree": "2" * 40, "policy_fingerprint": "e" * 64, "run_id": 654321,
                   "run_attempt": 1, "elapsed_seconds": 10.0, "commands": commands,
                   "go_json_log": "backend-go-test.jsonl", "execution_receipt": receipt}
            run_path = report / "run.json"
            run_path.write_text(json.dumps(run))
            (report / "backend-go-test.jsonl").write_text("modified\n")
            with self.assertRaisesRegex(shadow.ShadowEvidenceError, "Go JSON log"):
                shadow._verified_quality_lane_receipt(run_path)


if __name__ == "__main__":
    unittest.main()
