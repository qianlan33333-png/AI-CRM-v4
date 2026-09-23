import unittest

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

    def test_protected_path_cannot_be_downgraded(self):
        result = impact_selection.select(self.report("low", [".github/workflows/ci.yml"]))
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
                     "internal/webshell/host.go", "scripts/ci/impact_selection.py", "package-lock.json"):
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
