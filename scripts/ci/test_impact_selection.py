import unittest
from pathlib import Path
import subprocess
import json

import governance_impact
import impact_selection


class SelectionTest(unittest.TestCase):
    def report(self, risk, changed=(), lanes=()):
        def check(lane):
            examples = {
                "backend": {"path": "internal/fixture/test.go", "test": "TestFixture"},
                "browser": {"path": "cmd/aicrm/fixture_chromium_test.go", "test": "TestFixtureChromiumJourney"},
                "frontend": {"path": "web/scripts/fixture.mjs"},
                "archive-sdk": {"path": "scripts/check-fixture.sh"},
                "preflight": {"path": "scripts/ci/test_fixture.py"},
            }
            return {"lane": lane, **examples.get(lane, {"path": "internal/fixture/test.go"})}
        return {"risk": risk, "changed_paths": list(changed),
                "checks": [check(lane) for lane in lanes]}

    def test_high_risk_is_full(self):
        result = impact_selection.select(self.report("high"))
        self.assertEqual(result["mode"], "full")
        self.assertEqual(result["lanes"], list(impact_selection.LANES))

    def test_release_tooling_uses_contract_profile_without_application_lanes(self):
        paths = [".github/workflows/ci.yml", "AGENTS.md", "README.md", "deploy/README.md",
                 "docs/operations/domestic-release.md",
                 "docs/operations/archive/2026-09-23-5538-0206-staging-retry.md",
                 "docs/operations/archive/2026-09-24-staging-migration-backup-retry-plan.superseded.md",
                 "docs/plans/2026-09-24-staging-migration-backup-retry.md",
                 "docs/prd/2026-09-24-release-test-loop.md",
                 "deploy/domestic-release.example.json",
                 "deploy/domestic-release-role.production.example",
                 "deploy/domestic-release-role.staging.example", "scripts/ci/impact_selection.py",
                 "scripts/domestic_release.py", "scripts/domestic_release_build.py",
                 "deploy/domestic-promote.py", "deploy/test_domestic_promote_contract.py"]
        result = impact_selection.select(self.report("high", paths))
        self.assertEqual(result, {"mode": "targeted", "lanes": ["preflight"], "checks": [],
                                  "reason": "release-tooling-contracts", "profile": "tooling"})

    def test_current_release_simplification_changed_paths_select_tool_contracts(self):
        paths = [
            ".github/workflows/ci.yml", "AGENTS.md", "README.md", "deploy/README.md",
            "deploy/domestic-promote.py", "deploy/domestic-release-role.production.example",
            "deploy/domestic-release-role.staging.example", "deploy/test_domestic_promote_policy.py",
            "docs/operations/domestic-release.md",
            "docs/operations/archive/2026-09-23-5538-0206-staging-retry.md",
            "docs/operations/archive/2026-09-24-staging-migration-backup-retry-plan.superseded.md",
            "docs/plans/2026-09-24-staging-migration-backup-retry.md",
            "docs/prd/2026-09-24-release-test-loop.md",
            "scripts/ci/impact_selection.py", "scripts/ci/local_first_gate.py",
            "scripts/ci/quality_lanes.py", "scripts/ci/test_impact_selection.py",
            "scripts/ci/test_local_first_gate.py", "scripts/ci/test_quality_lanes.py",
            "scripts/ci/test_workflow_contract.py", "scripts/ci/verification.py",
            "scripts/domestic_release.py", "scripts/domestic_release_build.py",
            "scripts/test_domestic_release.py", "scripts/test_domestic_release_build.py",
        ]
        result = impact_selection.select(self.report("high", paths))
        self.assertEqual(result, {"mode": "targeted", "lanes": ["preflight"], "checks": [],
                                  "reason": "release-tooling-contracts", "profile": "tooling"})

    def test_unknown_or_shared_paths_cannot_enter_release_tooling_profile(self):
        for changed in (
            ["scripts/ci/quality_lanes.py", "scripts/run-go-with-donor-views.sh"],
            [".github/actions/ci-setup/action.yml"],
            ["deploy/aicrm-domestic-release.timer"],
            ["deploy/unknown.service"],
            ["deploy/domestic-release.example.json", "deploy/unknown.service"],
            ["migrations/0200_example.sql", "scripts/domestic_release.py"],
            ["scripts/domestic_release.py", "go.mod"],
            ["scripts/test_release_promote.py"],
        ):
            with self.subTest(changed=changed):
                result = impact_selection.select(self.report("low", changed))
                self.assertEqual(result["mode"], "full")

    def test_medium_selects_mapped_lanes_and_preflight(self):
        result = impact_selection.select(self.report("medium", ["internal/referral/app/admin.go"], lanes=("browser", "backend")))
        self.assertEqual(result, {"mode": "targeted", "lanes": ["preflight", "backend", "browser"],
                                  "checks": [{"lane": "browser", "path": "cmd/aicrm/fixture_chromium_test.go",
                                              "test": "TestFixtureChromiumJourney"},
                                             {"lane": "backend", "path": "internal/fixture/test.go",
                                              "test": "TestFixture"}],
                                  "reason": "affected-capabilities"})

    def test_shared_and_migration_paths_force_all_lanes_even_if_report_says_medium(self):
        for path in ("migrations/0200_example.sql", "cmd/migrate-example/main.go",
                     "internal/configmigration/runner.go", "internal/platform/config.go",
                     "internal/webshell/host.go", ".github/actions/ci-setup/action.yml",
                     "scripts/run-go-with-donor-views.sh", "package-lock.json"):
            with self.subTest(path=path):
                result = impact_selection.select(self.report("medium", [path], lanes=("backend",)))
                self.assertEqual(result["mode"], "full")
                self.assertEqual(result["lanes"], list(impact_selection.LANES))

    def test_unknown_or_malformed_mapping_falls_back_to_full(self):
        reports = (
            self.report("medium", ["internal/media/app/service.go"], lanes=("unknown",)),
            {"risk": "medium", "changed_paths": ["internal/media/app/service.go"],
             "checks": [{"lane": "backend"}]},
            {"risk": "medium", "changed_paths": ["internal/media/app/service.go"],
             "checks": [{"lane": "backend", "path": "../outside.go", "test": "TestUnsafe"}]},
        )
        for report in reports:
            with self.subTest(report=report):
                self.assertEqual(impact_selection.select(report)["mode"], "full")

    def test_low_only_runs_preflight(self):
        self.assertEqual(impact_selection.select(self.report("low", ["docs/guide.md"]))["lanes"], ["preflight"])

    def test_unknown_path_and_unknown_risk_use_full(self):
        for report in (self.report("low", ["new-runtime.toml"]), self.report("unexpected"), self.report("low")):
            self.assertEqual(impact_selection.select(report)["mode"], "full")

    def test_candidate_unions_tooling_prd_and_ordinary_skill_docs(self):
        report = self.report("high", [
            "scripts/ci/test_workflow_contract.py",
            "docs/prd/2026-09-24-small-step-impact-checks.md",
            "skills/ordinary-writing/SKILL.md",
        ], lanes=("preflight",))
        # The currently enforced selector remains conservative for this mixed
        # set; only the separate candidate plan combines the equivalent impacts.
        self.assertEqual(impact_selection.select(report)["mode"], "full")
        candidate = impact_selection.select_candidate(report)
        self.assertEqual(candidate["mode"], "targeted")
        self.assertEqual(candidate["lanes"], ["preflight"])
        self.assertEqual(candidate["profile"], "tooling")
        self.assertEqual(candidate["reasons"], ["documentation-only", "release-tooling-contracts"])
        self.assertEqual(candidate["checks"], [])

    def test_candidate_instruction_docs_use_light_consistency_profile(self):
        report = self.report("low", [
            "AGENTS.md", "README.md", "docs/development-before-start.md",
            "skills/aicrm-v3-development/SKILL.md", "docs/prd/ordinary.md",
        ])
        candidate = impact_selection.select_candidate(report)
        self.assertEqual(candidate["mode"], "targeted")
        self.assertEqual(candidate["profile"], "documentation")
        self.assertEqual(candidate["lanes"], ["preflight"])
        self.assertEqual(candidate["reasons"], ["documentation-only"])

    def test_candidate_instruction_docs_cannot_lower_executable_check_policy(self):
        report = self.report("low", [
            "scripts/ci/impact_selection.py", "AGENTS.md", "docs/prd/ordinary.md",
        ])
        candidate = impact_selection.select_candidate(report)
        self.assertEqual(candidate["mode"], "full")
        self.assertEqual(candidate["reason"], "policy-change-requires-full")
        self.assertEqual(candidate["lanes"], list(impact_selection.LANES))

    def test_candidate_unions_multiple_capability_checks_and_lanes(self):
        report = {
            "risk": "medium",
            "direct": ["referral", "customer"],
            "changed_paths": ["internal/referral/app/service.go", "internal/customer/app/service.go"],
            "checks": [
                {"lane": "backend", "path": "internal/referral/app/service_test.go", "test": "TestReferral"},
                {"lane": "backend", "path": "internal/referral/app/service_test.go", "test": "TestReferral"},
                {"lane": "browser", "path": "cmd/aicrm/fixture_test.go", "test": "TestFixtureChromiumJourney"},
            ],
        }
        candidate = impact_selection.select_candidate(report)
        self.assertEqual(candidate["mode"], "targeted")
        self.assertEqual(candidate["lanes"], ["preflight", "backend", "browser"])
        self.assertEqual(len(candidate["checks"]), 2)
        self.assertEqual(candidate["reasons"], ["affected-capabilities"])

    def test_candidate_unknown_mixed_path_and_malformed_mapping_fall_back_to_full(self):
        reports = (
            self.report("low", ["scripts/ci/test_fixture.py", "docs/prd/ordinary.md", "new-runtime.toml"],
                        lanes=("preflight",)),
            self.report("medium", ["internal/referral/app/service.go"], lanes=("unknown",)),
            {"risk": "medium", "direct": ["referral"],
             "changed_paths": ["internal/referral/app/service.go"],
             "checks": [{"lane": "backend", "path": "../outside.go", "test": "TestUnsafe"}]},
        )
        for report in reports:
            with self.subTest(report=report):
                candidate = impact_selection.select_candidate(report)
                self.assertEqual(candidate["mode"], "full")
                self.assertEqual(candidate["lanes"], list(impact_selection.LANES))

    def test_candidate_uses_current_registry_without_transitive_tooling_checks(self):
        root = Path(__file__).resolve().parents[2]
        tracked = subprocess.check_output(["git", "ls-files"], cwd=root, text=True).splitlines()
        registry = json.loads((root / "docs/governance/capability-impact.json").read_text())
        report = governance_impact.analyze(root, registry, tracked, [
            "deploy/domestic-promote.py", "docs/prd/ordinary.md", "skills/ordinary-writing/SKILL.md",
        ])
        # The registry can carry downstream application checks for delivery
        # governance. The candidate keeps tooling + prose in their unioned
        # preflight profile instead of copying those unrelated consumer checks.
        candidate = impact_selection.select_candidate(report)
        self.assertEqual(candidate["mode"], "targeted")
        self.assertEqual(candidate["profile"], "tooling")
        self.assertEqual(candidate["lanes"], ["preflight"])
        self.assertEqual(candidate["checks"], [])
