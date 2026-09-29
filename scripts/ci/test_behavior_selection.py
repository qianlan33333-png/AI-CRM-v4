from __future__ import annotations

import subprocess
import tempfile
import unittest
from pathlib import Path

import behavior_selection
import quality_lanes


class BehaviorSelectionTest(unittest.TestCase):
    def setUp(self) -> None:
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        for command in (["git", "init", "-q"], ["git", "config", "user.name", "Fixture"],
                        ["git", "config", "user.email", "fixture@example.invalid"]):
            subprocess.run(command, cwd=self.root, check=True)
        self.write("cmd/aicrm/composition.go", "package main\nfunc route() {}\n")
        self.write("cmd/aicrm/adapters_test.go", "package main\nfunc TestUnrelated(t *testing.T) {}\n")
        self.write("cmd/aicrm/referral_chromium_postgres_integration_test.go",
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

    def graph(self) -> dict:
        return {"selected_packages": [{"dir": "cmd/aicrm", "import_path": "m/cmd/aicrm",
                                        "source_files": ["cmd/aicrm/composition.go"]}]}

    def test_page_specific_csp_uses_direct_tests_and_real_journey(self) -> None:
        self.write("cmd/aicrm/composition.go", 'package main\nfunc route() { _ = "/referral" }\n')
        self.write("cmd/aicrm/adapters_test.go", "package main\nfunc TestUnrelated(t *testing.T) {}\nfunc TestReferralCSP(t *testing.T) {}\n")
        self.write("web/v3/referralCenter.ts", "export const view = 2;\n")
        self.write("web/v3/referralCenter.test.mjs", "// new check\n")
        self.commit("referral")
        paths = subprocess.check_output(["git", "diff", "--name-only", self.base, "HEAD"],
                                        cwd=self.root, text=True).splitlines()
        choice, packages = behavior_selection.select(self.root, self.base, self.sha(), paths, self.graph())
        self.assertEqual(choice["profile"], "behavior")
        self.assertEqual(choice["lanes"], ["preflight", "backend", "frontend", "browser"])
        self.assertEqual(packages, [])
        self.assertEqual(choice["checks"], [
            {"lane": "backend", "path": "cmd/aicrm/adapters_test.go", "test": "TestReferralCSP"},
            {"lane": "browser", "path": "cmd/aicrm/referral_chromium_postgres_integration_test.go",
             "test": "TestReferralChromiumJourney"},
            {"lane": "frontend", "path": "web/v3/referralCenter.test.mjs"},
        ])
        backend = quality_lanes.behavior_commands("backend", self.root, choice["checks"])
        self.assertEqual(len(backend), 1)
        self.assertNotIn("-race", backend[0])
        self.assertIn("^(TestReferralCSP)$", backend[0])
        browser = quality_lanes.behavior_commands("browser", self.root, choice["checks"])
        self.assertEqual(len(browser), 1)
        self.assertIn("TestReferralChromiumJourney", browser[0])
        self.assertNotIn("pip", " ".join(browser[0]))

    def test_unmapped_composition_falls_back_to_package_suite(self) -> None:
        self.write("cmd/aicrm/composition.go", "package main\nfunc route() { _ = 2 }\n")
        self.commit("unmapped composition")
        choice, _ = behavior_selection.select(self.root, self.base, self.sha(),
                                              ["cmd/aicrm/composition.go"], self.graph())
        self.assertEqual(choice["checks"], [{"lane": "backend", "path": "cmd/aicrm/composition.go"}])

    def test_unrelated_new_test_does_not_hide_composition_change(self) -> None:
        self.write("cmd/aicrm/composition.go", 'package main\nfunc route() { _ = "/referral" }\n')
        self.write("cmd/aicrm/adapters_test.go", "package main\nfunc TestUnrelated(t *testing.T) {}\nfunc TestPayments(t *testing.T) {}\n")
        self.commit("mixed")
        choice, _ = behavior_selection.select(self.root, self.base, self.sha(),
                                              ["cmd/aicrm/composition.go", "cmd/aicrm/adapters_test.go"], self.graph())
        self.assertIn({"lane": "backend", "path": "cmd/aicrm/composition.go"}, choice["checks"])
        self.assertNotIn({"lane": "backend", "path": "cmd/aicrm/adapters_test.go", "test": "TestPayments"},
                         choice["checks"])

    def test_unknown_runtime_and_policy_are_handled_separately(self) -> None:
        self.write("unknown-runtime.toml", "new=true\n")
        self.commit("unknown")
        unknown, _ = behavior_selection.select(self.root, self.base, self.sha(),
                                               ["unknown-runtime.toml"], {"selected_packages": []})
        self.assertEqual(unknown["mode"], "full")
        tooling, _ = behavior_selection.select(self.root, self.base, self.sha(),
                                               ["scripts/ci/behavior_selection.py"],
                                               {"selected_packages": []}, policy_changed=True)
        self.assertEqual(tooling["profile"], "tooling")

    def test_policy_and_runtime_change_cannot_select_only_tool_contracts(self) -> None:
        self.write("cmd/aicrm/composition.go", 'package main\nfunc route() { _ = "/referral" }\n')
        self.commit("runtime with selector")
        choice, _ = behavior_selection.select(self.root, self.base, self.sha(),
                                              ["scripts/ci/behavior_selection.py", "cmd/aicrm/composition.go"],
                                              self.graph(), policy_changed=True)
        self.assertEqual(choice["mode"], "full")
        self.assertIn("backend", choice["lanes"])

    def test_shared_web_with_adjacent_test_widens_to_consumers(self) -> None:
        self.write("web/v3/shared/ui/widget.ts", "export const widget = 1;\n")
        self.write("web/v3/shared/ui/widget.test.mjs", "// direct check\n")
        self.commit("shared")
        choice, _ = behavior_selection.select(self.root, self.base, self.sha(),
                                              ["web/v3/shared/ui/widget.ts", "web/v3/shared/ui/widget.test.mjs"],
                                              {"selected_packages": []})
        self.assertEqual(choice["reason"], "shared-web-consumers-unknown")
        self.assertIn("browser", choice["lanes"])

    def test_node_journey_fixture_runs_its_go_host_test(self) -> None:
        self.write("internal/product/http/service_period_public_journey.mjs", "// changed journey\n")
        self.write("internal/product/http/public_test.go",
                   'package http\nfunc TestServicePeriodPublicBrowserJourney(t *testing.T) { _ = "service_period_public_journey.mjs" }\n')
        self.commit("fixture baseline")
        base = self.sha()
        self.write("internal/product/http/service_period_public_journey.mjs", "// revised journey\n")
        self.commit("fixture update")
        choice, _ = behavior_selection.select(self.root, base, self.sha(),
                                              ["internal/product/http/service_period_public_journey.mjs"],
                                              {"selected_packages": []})
        self.assertIn({"lane": "backend", "path": "internal/product/http/public_test.go",
                       "test": "TestServicePeriodPublicBrowserJourney"}, choice["checks"])


if __name__ == "__main__":
    unittest.main()
