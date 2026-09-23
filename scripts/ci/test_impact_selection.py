import unittest

import impact_selection


class SelectionTest(unittest.TestCase):
    def report(self, risk, changed=(), lanes=()):
        return {"risk": risk, "changed_paths": list(changed), "checks": [{"lane": lane} for lane in lanes]}

    def test_high_risk_is_full(self):
        result = impact_selection.select(self.report("high"))
        self.assertEqual(result["mode"], "full")
        self.assertEqual(result["lanes"], list(impact_selection.LANES))

    def test_protected_path_cannot_be_downgraded(self):
        result = impact_selection.select(self.report("low", [".github/workflows/ci.yml"]))
        self.assertEqual(result["mode"], "full")

    def test_medium_selects_mapped_lanes_and_preflight(self):
        result = impact_selection.select(self.report("medium", ["internal/referral/app/admin.go"], lanes=("browser", "backend")))
        self.assertEqual(result, {"mode": "targeted", "lanes": ["preflight", "backend", "frontend", "browser"], "reason": "affected-capabilities"})

    def test_low_only_runs_preflight(self):
        self.assertEqual(impact_selection.select(self.report("low", ["docs/guide.md"]))["lanes"], ["preflight"])

    def test_unknown_path_and_unknown_risk_use_full(self):
        for report in (self.report("low", ["new-runtime.toml"]), self.report("unexpected"), self.report("low")):
            self.assertEqual(impact_selection.select(report)["mode"], "full")
