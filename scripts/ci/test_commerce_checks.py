"""The bounded commerce profile must retain package suites and journey discovery."""
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
import commerce_checks


class CommerceChecksTest(unittest.TestCase):
    def test_browser_dependency_inspection_keeps_complete_preparation_for_unknown_journeys(self):
        checks = [{"lane": "browser", "path": path, "test": name}
                  for path, name in commerce_checks.JOURNEY_FILES.items()]
        inputs = commerce_checks.browser_npm_inputs(checks)
        self.assertTrue(commerce_checks.JOURNEY_SCRIPTS.issubset(inputs))
        self.assertIn("scripts/prepare-donor-source-views.mjs", inputs)
        self.assertIsNone(commerce_checks.browser_npm_inputs(checks[:-1]))
        self.assertIsNone(commerce_checks.browser_npm_inputs(checks + [
            {"lane": "browser", "path": checks[0]["path"], "test": "TestNewChromiumJourney"}]))

    def graph(self):
        return {"graph_valid": True, "unowned_go_paths": [], "selected_packages": [
            {"dir": path, "import_path": "example.invalid/crm/" + path}
            for path in ("cmd/aicrm", "internal/product/http", "internal/payment/http")]}

    def source(self, root, revision, path):
        if path.endswith("service_period_identity_test.go"):
            return 'exec.Command("node", "service_period_identity_journey.mjs")'
        name = commerce_checks.JOURNEY_FILES[path]
        return "func " + name + "(t *testing.T) {}\n" + ("func TestNewAuthorizationChromiumJourney(t *testing.T) {}\n" if path.startswith("cmd/aicrm/public_commerce_") else "")

    def test_local_carrier_discovers_new_journey_and_keeps_four_minimum(self):
        path = next(iter(commerce_checks.JOURNEY_FILES))
        with patch.object(commerce_checks, "read_source", side_effect=self.source):
            selection = commerce_checks.selection(Path("/tmp"), "base", "head", [path], self.graph())
        self.assertEqual(selection["lanes"], ["preflight", "backend", "browser"])
        names = {check["test"] for check in selection["checks"]}
        self.assertEqual(names, set(commerce_checks.JOURNEY_FILES.values()) | {"TestNewAuthorizationChromiumJourney"})

    def test_embedded_contracts_are_covered_by_full_go_packages(self):
        with patch.object(commerce_checks, "read_source", side_effect=self.source):
            selection = commerce_checks.selection(Path("/tmp"), "base", "head", [
                "internal/product/http/service_period_identity.js",
                "internal/product/http/service_period_identity_test.go"], self.graph())
        self.assertNotIn("frontend", selection["lanes"])
        self.assertEqual(len(selection["checks"]), 4)

    def test_shared_fixture_migration_dependency_api_policy_unknown_are_full(self):
        for path in ("cmd/aicrm/admin_access_journey_integration_test.go", "cmd/aicrm/main.go",
                     "migrations/0205_next.sql", "web/v3/shared/ui/widget.mjs", "package-lock.json",
                     "api/openapi.yaml", "scripts/ci/commerce_checks.py", "internal/product/http/new.go"):
            with self.subTest(path=path):
                self.assertIsNone(commerce_checks.selection(Path("/tmp"), "base", "head", [path], self.graph()))

    def test_missing_composition_dependency_or_required_journey_cannot_narrow(self):
        graph = self.graph()
        graph["selected_packages"] = graph["selected_packages"][1:]
        path = "internal/product/http/service_period_public.go"
        self.assertIsNone(commerce_checks.selection(Path("/tmp"), "base", "head", [path], graph))
        with patch.object(commerce_checks, "read_source", return_value="func TestOtherChromiumJourney(t *testing.T) {}"):
            self.assertIsNone(commerce_checks.selection(Path("/tmp"), "base", "head", [path], self.graph()))

    def test_payment_callback_changes_do_not_inherit_oauth_profile(self):
        base = 'package http\nfunc (h *Handler) startH5OAuth() { if true { _ = "}" } }\nfunc callback() { verify() }\n'
        allowed = base.replace('if true', 'if false') + '// retry comment\nfunc retryPeriodDetailOAuth() bool { return true }\n'
        self.assertEqual(commerce_checks.without_oauth_functions(base), commerce_checks.without_oauth_functions(allowed))
        self.assertNotEqual(commerce_checks.without_oauth_functions(base), commerce_checks.without_oauth_functions(base.replace('verify()', 'ignoreSignature()')))
