#!/usr/bin/env python3
"""Adversarial checks for the gates, plus references to real behavior regressions."""
from copy import deepcopy
from datetime import date
import json
from pathlib import Path
import tempfile
import unittest

import governance_impact as impact
import governance_security as security


class ImpactTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.files = {
            "internal/shared/port/port.go": "package port\n",
            "internal/consumer/app.go": 'package consumer\nimport shared "github.com/qianlan33333-png/AI-CRM-v3/internal/shared/port"\n',
            "internal/unrelated/app.go": "package unrelated\n",
            "checks_test.go": "package checks\nfunc TestPreservation(t *testing.T) {}\n",
        }
        for path, text in self.files.items():
            target = self.root / path
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_text(text)
        check = {"path": "checks_test.go", "test": "TestPreservation", "lane": "backend"}
        self.registry = {"schema": 1, "required_roots": list(impact.REQUIRED_ROOTS), "components": [
            {"id": name, "owner": "Domain owner", "risk": "medium", "paths": [f"internal/{name}/*"], "checks": [check]}
            for name in ("shared", "consumer", "unrelated")
        ]}

    def analyze(self, changed):
        return impact.analyze(self.root, self.registry, list(self.files), changed)

    def test_real_import_discovers_consumers_without_redundant_manual_edge(self):
        report = self.analyze(["internal/shared/port/port.go"])
        self.assertEqual(report["direct"], ["shared"])
        self.assertEqual(report["affected"], ["consumer", "shared"])
        self.assertEqual(report["edges"][0]["source"], "internal/consumer/app.go")

    def test_consumer_change_does_not_reverse_dependency_direction(self):
        self.assertEqual(self.analyze(["internal/consumer/app.go"])["affected"], ["consumer"])

    def test_unregistered_new_production_domain_fails(self):
        with self.assertRaisesRegex(ValueError, "need capability registration"):
            self.analyze(["internal/newdomain/handler.go"])

    def test_registry_cannot_hide_future_domains_behind_catch_all(self):
        self.registry["components"][0]["paths"] = ["internal/*"]
        with self.assertRaisesRegex(ValueError, "explicit registration"):
            self.analyze([])

    def test_registry_cannot_disable_coverage_or_exclude_unregistered_paths(self):
        for update in ({"required_roots": []}, {"excluded_paths": ["internal/*"]}):
            original = deepcopy(self.registry)
            self.registry.update(update)
            with self.assertRaisesRegex(ValueError, "cannot be disabled"):
                self.analyze([])
            self.registry = original

    def test_rule_change_cannot_lower_its_own_risk_in_registry(self):
        path = "docs/governance/rules.json"
        self.registry["components"][0]["paths"].append(path)
        self.files[path] = "{}"
        self.assertEqual(self.analyze([path])["risk"], "high")

    def test_unregistered_existing_production_domain_fails_full_registry_audit(self):
        self.files["internal/forgotten/handler.go"] = ""
        with self.assertRaisesRegex(ValueError, "unregistered production path"):
            self.analyze([])

    def test_deleted_behavior_test_mapping_is_not_green(self):
        (self.root / "checks_test.go").write_text("package checks\n")
        with self.assertRaisesRegex(ValueError, "missing test function"):
            self.analyze([])

    def test_stale_paths_unknown_dependencies_and_invalid_lane_fail(self):
        for modification in ({"paths": ["missing/*"]}, {"depends_on": ["missing"]},
                             {"checks": [{"path": "checks_test.go", "lane": "skipped"}]}):
            with self.subTest(modification=modification):
                original = deepcopy(self.registry)
                self.registry["components"][0].update(modification)
                with self.assertRaises(ValueError):
                    self.analyze([])
                self.registry = original

    def test_cycles_terminate_and_keep_transitive_consumers(self):
        self.registry["components"][0]["depends_on"] = ["consumer"]
        self.registry["components"][2]["depends_on"] = ["consumer"]
        self.assertEqual(self.analyze(["internal/shared/port/port.go"])["affected"], ["consumer", "shared", "unrelated"])

    def test_browser_mapping_cannot_bypass_host_autodiscovery(self):
        self.registry["components"][0]["checks"][0]["lane"] = "browser"
        with self.assertRaisesRegex(ValueError, "auto-discovered"):
            self.analyze([])

    def test_current_head_author_declaration_is_not_independent_approval(self):
        head = "a" * 40
        body = f"Governance-Head: {head}\nGovernance-Preservation: Config keeps all unrelated values.\nGovernance-Validation: go test ./internal/config/app passed."
        result = impact.author_review(body, head, "high")
        self.assertEqual(result["status"], "recorded")
        self.assertEqual(result["independent_review"], "unverified")
        self.assertEqual(impact.author_review(body, "b" * 40, "high")["status"], "missing_or_stale")
        self.assertEqual(impact.author_review("Governance-Head: " + head, head, "high")["status"], "missing_or_stale")
        empty = body.replace("Config keeps all unrelated values.", "")
        self.assertEqual(impact.author_review(empty, head, "high")["status"], "missing_or_stale")
        self.assertEqual(impact.author_review(body + f"\nGovernance-Head: {head}", head, "high")["status"], "missing_or_stale")

    def test_critical_governance_lane_cannot_skip_or_fail_or_cancel(self):
        for result in ("skipped", "neutral", "cancelled", "failure", "", None):
            with self.subTest(result=result), self.assertRaises(ValueError):
                impact.gate({"governance": {"result": result}})
        impact.gate({"governance": {"result": "success"}})


class SecurityTest(unittest.TestCase):
    def test_unpinned_tool_binary_cannot_claim_pinned_scan(self):
        security.verify_build_info("\tmod\texample.org/tool\tv1.2.3\th1:fixture", "example.org/tool", "v1.2.3")
        with self.assertRaises(ValueError):
            security.verify_build_info("\tmod\texample.org/tool\tv1.2.2", "example.org/tool", "v1.2.3")
    def records(self, function="Danger"):
        return [{"config": {"scanner_name": "govulncheck"}}, {"progress": {"message": "scan"}},
                {"finding": {"osv": "GO-TEST-1", "fixed_version": "v2.0.0", "trace": [
                    {"module": "example.org/mod", "package": "example.org/mod/pkg", "function": function}]}}]

    def debt(self, **updates):
        entry = {"id": "GO-TEST-1", "module": "example.org/mod", "owner": "Test domain owner",
                 "first_recorded": "2026-09-18", "expires": "2026-10-02", "tracking": "test-fixture",
                 "reason": "Test-only explicit exception with a bounded remediation period.",
                 "status": "approved_timeboxed_exception", "approved_by": "fixture-reviewer", "approval_evidence": "fixture-review-1"}
        entry.update(updates)
        return {"entries": [entry]}

    def evaluate(self, ledger, baseline=True, today=date(2026, 9, 18)):
        findings = security.reachable_findings(self.records())
        return security.evaluate_debt(findings, findings if baseline else {}, ledger, today)

    def test_json_zero_exit_does_not_hide_reachable_vulnerability(self):
        stream = "\n".join(json.dumps(record) for record in self.records())
        findings = security.reachable_findings(security.decode_stream(stream))
        self.assertEqual(len(findings), 1)
        self.assertTrue(security.evaluate_debt(findings, {}, self.debt(), date(2026, 9, 18))[1])

    def test_empty_and_truncated_streams_cannot_pass(self):
        for stream in ("", "{}", '{"config":{}}', '{"config":{}}\n{"finding":'):
            with self.subTest(stream=stream), self.assertRaises(ValueError):
                security.decode_stream(stream)

    def test_module_only_findings_are_not_claimed_reachable(self):
        self.assertFalse(security.reachable_findings(self.records(function="")))

    def test_new_reachable_symbol_cannot_use_even_approved_baseline_exception(self):
        self.assertTrue(self.evaluate(self.debt(), baseline=False)[1])

    def test_recording_debt_without_approval_never_grants_waiver(self):
        self.assertTrue(self.evaluate(self.debt(status="remediation_required"))[1])
        self.assertTrue(self.evaluate(self.debt(approval_evidence=""))[1])
        self.assertTrue(self.evaluate({"entries": []})[1])

    def test_expired_and_indefinite_debt_fail(self):
        self.assertTrue(self.evaluate(self.debt(), today=date(2026, 10, 3))[1])
        self.assertTrue(self.evaluate(self.debt(expires="2030-01-01"))[1])
        self.assertTrue(self.evaluate(self.debt(owner=""))[1])

    def test_only_explicit_unexpired_existing_exception_passes(self):
        result, failures = self.evaluate(self.debt())
        self.assertFalse(failures)
        self.assertEqual(result[0]["status"], "approved_timeboxed_existing_debt")


class WorkflowTest(unittest.TestCase):
    def test_complete_gate_still_runs_original_lanes_and_new_governance(self):
        workflow = (impact.ROOT / ".github/workflows/ci.yml").read_text()
        check = workflow.split("\n  check:\n", 1)[1].split("\n  quality-report:\n", 1)[0]
        self.assertIn("needs: [plan, preflight, backend, frontend, browser, archive-sdk, governance]", check)
        self.assertIn("python3 scripts/ci/governance_impact.py --gate", check)
        self.assertIn("python3 scripts/ci/verification.py gate", check)
        governance = workflow.split("\n  governance:\n", 1)[1].split("\n  check:\n", 1)[0]
        self.assertNotIn("continue-on-error", governance)
        self.assertNotIn("needs.plan.outputs.full", governance)

    def test_required_check_stays_stable_and_github_ci_has_no_production_deploy_job(self):
        workflow = (impact.ROOT / ".github/workflows/ci.yml").read_text()
        self.assertIn("\n  check:\n", workflow)
        self.assertIn("AICRM_CI_FOCUS_CHECKS: ${{ needs.plan.outputs.checks }}", workflow)
        self.assertIn("impact-analysis-failed", workflow)
        self.assertNotIn("\n  deploy:\n", workflow)
        self.assertNotIn("promote-staging-direct.sh", workflow)

    def test_weekly_scanner_is_reporting_without_write_permissions(self):
        workflow = (impact.ROOT / ".github/workflows/governance-weekly.yml").read_text()
        self.assertIn("schedule:", workflow)
        self.assertIn("--weekly", workflow)
        self.assertNotIn(": write", workflow)
        self.assertNotIn("pull_request_target", workflow)

    def test_pr_quality_evidence_is_structured_and_lightweight_manifest_is_retained(self):
        workflow = (impact.ROOT / ".github/workflows/ci.yml").read_text()
        template = (impact.ROOT / ".github/pull_request_template.md").read_text()
        self.assertIn("ci-quality-summary-${{ github.run_id }}-${{ github.run_attempt }}", workflow)
        self.assertIn("retention-days: 90", workflow)
        for field in ("首轮 CI 证据", "当前 head 最终 required check", "失败原因分类", "修复提交与复跑阶段", "观察窗口"):
            self.assertIn(field, template)


if __name__ == "__main__":
    unittest.main()
