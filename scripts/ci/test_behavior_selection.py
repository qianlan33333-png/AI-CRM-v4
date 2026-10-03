from __future__ import annotations

import subprocess
import tempfile
import unittest
from pathlib import Path

import behavior_selection


class BehaviorSelectionTest(unittest.TestCase):
    def setUp(self) -> None:
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        for command in (["git", "init", "-q"], ["git", "config", "user.name", "Fixture"],
                        ["git", "config", "user.email", "fixture@example.invalid"]):
            subprocess.run(command, cwd=self.root, check=True)
        self.write("go.mod", "module example.invalid/crm\n\ngo 1.26\n")
        self.write("internal/catalog/catalog.go", "package catalog\nfunc Name() string { return \"catalog\" }\n")
        self.write("internal/catalog/catalog_test.go", "package catalog\nfunc TestCatalogName(t *testing.T) {}\n")
        self.write("internal/order/order.go",
                   'package order\nimport "example.invalid/crm/internal/catalog"\nvar _ = catalog.Name()\n')
        self.write("internal/order/order_test.go", "package order\nfunc TestOrder(t *testing.T) {}\n")
        self.write("cmd/aicrm/composition.go", "package main\nfunc route() {}\n")
        self.write("cmd/aicrm/adapters_test.go", "package main\nfunc TestUnrelated(t *testing.T) {}\n")
        self.write("cmd/aicrm/referral_chromium_test.go",
                   "package main\nfunc TestReferralChromiumJourney(t *testing.T) {}\n")
        self.write("web/v3/referralCenter.ts", "export const view = 1;\n")
        self.write("web/v3/referralCenter.test.mjs", "// old check\n")
        self.commit("base")
        self.base = self.sha()

    def write(self, path: str, content: str) -> None:
        file = self.root / path
        file.parent.mkdir(parents=True, exist_ok=True)
        file.write_text(content)

    def commit(self, label: str) -> None:
        subprocess.run(["git", "add", "."], cwd=self.root, check=True)
        subprocess.run(["git", "commit", "-qm", label], cwd=self.root, check=True)

    def sha(self) -> str:
        return subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=self.root, text=True).strip()

    def select(self, paths):
        return behavior_selection.select_light(self.root, self.base, self.sha(), paths)

    def test_changed_package_selects_only_itself_and_one_hop_importers(self):
        self.write("internal/catalog/catalog.go", 'package catalog\nfunc Name() string { return "updated" }\n')
        self.commit("catalog update")
        choice = self.select(["internal/catalog/catalog.go"])
        self.assertEqual(choice["profile"], "ai-selected")
        self.assertEqual(choice["selection_source"], "adjacent-behavior-checks")
        self.assertEqual({check["package"] for check in choice["checks"]},
                         {"./internal/catalog", "./internal/order"})
        self.assertNotIn("./cmd/aicrm", {check["package"] for check in choice["checks"]})

    def test_cmd_host_source_requires_a_named_check_instead_of_a_package_suite(self):
        self.write("cmd/aicrm/composition.go", 'package main\nfunc route() { _ = "/referral" }\n')
        self.commit("host route update")
        with self.assertRaisesRegex(ValueError, "docs/release-checks.json is required"):
            self.select(["cmd/aicrm/composition.go"])

    def test_changed_named_host_test_does_not_expand_to_browser_or_full_lane(self):
        self.write("cmd/aicrm/composition.go", 'package main\nfunc route() { _ = "/referral" }\n')
        self.write("cmd/aicrm/adapters_test.go",
                   "package main\nfunc TestUnrelated(t *testing.T) {}\nfunc TestReferralCSP(t *testing.T) {}\n")
        self.commit("host route and named check")
        choice = self.select(["cmd/aicrm/composition.go", "cmd/aicrm/adapters_test.go"])
        self.assertEqual(choice["lanes"], ["backend"])
        self.assertIn({"lane": "backend", "path": "cmd/aicrm/adapters_test.go",
                       "package": "./cmd/aicrm", "test": "TestReferralCSP"}, choice["checks"])
        self.assertNotIn("./cmd/aicrm", [check.get("package") for check in choice["checks"]
                                         if check.get("test") is None])

    def test_changed_chromium_test_runs_only_its_named_browser_journey(self):
        self.write("cmd/aicrm/referral_chromium_test.go",
                   "package main\nfunc TestReferralChromiumJourney(t *testing.T) { _ = 1 }\n")
        self.commit("browser journey")
        choice = self.select(["cmd/aicrm/referral_chromium_test.go"])
        self.assertEqual(choice["lanes"], ["browser"])
        self.assertEqual(choice["checks"], [{"lane": "browser", "path": "cmd/aicrm/referral_chromium_test.go",
                                              "test": "TestReferralChromiumJourney"}])

    def test_adjacent_web_test_is_selected_without_shared_or_browser_expansion(self):
        self.write("web/v3/shared/ui/widget.ts", "export const widget = 2;\n")
        self.write("web/v3/shared/ui/widget.test.mjs", "// direct node test\n")
        self.commit("shared component")
        choice = self.select(["web/v3/shared/ui/widget.ts", "web/v3/shared/ui/widget.test.mjs"])
        self.assertEqual(choice["lanes"], ["frontend"])
        self.assertEqual(choice["checks"], [{"lane": "frontend", "path": "web/v3/shared/ui/widget.test.mjs"}])

    def test_go_fixture_change_selects_its_referencing_test_and_deleted_test_is_not_emitted(self):
        fixture = "internal/order/checkout_journey.mjs"
        self.write(fixture, "// journey fixture\n")
        self.write("internal/order/order_test.go",
                   'package order\nfunc TestCheckoutJourney(t *testing.T) { _ = "checkout_journey.mjs" }\n')
        self.commit("journey fixture")
        base = self.sha()
        self.write(fixture, "// revised journey fixture\n")
        self.commit("journey update")
        choice = behavior_selection.select_light(self.root, base, self.sha(), [fixture])
        self.assertEqual(choice["checks"], [{"lane": "backend", "path": "internal/order/order_test.go",
                                              "package": "./internal/order", "test": "TestCheckoutJourney"}])

        test_path = "internal/order/order_test.go"
        (self.root / test_path).unlink()
        self.commit("delete consumer test")
        with self.assertRaisesRegex(ValueError, "docs/release-checks.json is required"):
            behavior_selection.select_light(self.root, self.sha(), self.sha(), [test_path])

    def test_unknown_runtime_path_requires_a_list_instead_of_full_fallback(self):
        self.write("unknown-runtime.toml", "new=true\n")
        self.commit("unknown runtime")
        with self.assertRaisesRegex(ValueError, "docs/release-checks.json is required"):
            self.select(["unknown-runtime.toml"])

    def test_documentation_only_stays_light(self):
        self.write("docs/example.md", "doc\n")
        self.commit("documentation")
        choice = self.select(["docs/example.md"])
        self.assertEqual(choice["profile"], "documentation")
        self.assertEqual(choice["lanes"], ["preflight"])
        self.assertEqual(choice["checks"], [])


if __name__ == "__main__":
    unittest.main()
