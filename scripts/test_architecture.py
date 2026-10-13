#!/usr/bin/env python3
"""Adversarial fixtures for the runtime/test distinction, not production exceptions."""
import contextlib
import importlib.util
import io
import tempfile
import unittest
from pathlib import Path

spec = importlib.util.spec_from_file_location("architecture", Path(__file__).with_name("check-architecture.py"))
gate = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gate)
PREFIX = gate.MODULE_PREFIX


class ArchitectureTests(unittest.TestCase):
    def test_current_runtime_repository_is_checked(self):
        # The release lane discovers unittest files; execute the actual gate
        # here as well as synthetic fixtures, rather than discovering the CLI.
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            gate.main()
        self.assertRegex(output.getvalue(), r"architecture: checked [1-9][0-9]* Go files")

    def check(self, path, source, rejected=None):
        with tempfile.TemporaryDirectory() as directory:
            original = gate.ROOT
            gate.ROOT = Path(directory)
            target = gate.ROOT / path
            target.parent.mkdir(parents=True)
            target.write_text(source)
            stderr = io.StringIO()
            try:
                with contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(stderr):
                    if rejected:
                        with self.assertRaises(SystemExit):
                            gate.main()
                        self.assertIn(rejected, stderr.getvalue())
                    else:
                        gate.main()
            finally:
                gate.ROOT = original

    def test_integration_fixture_may_compose_and_read_environment(self):
        self.check("internal/platform/config/fixture_test.go", f'package config_test\nimport ("os"; "{PREFIX}customer/store")\nfunc fixture() {{ os.Getenv("SYNTHETIC_DSN") }}')

    def test_runtime_cross_domain_concrete_rejected(self):
        self.check("internal/order/app/x.go", f'package app\nimport "{PREFIX}customer/store"\n', "cross-domain concrete")

    def test_platform_reverse_import_rejected(self):
        self.check("internal/platform/postgres/x.go", f'package postgres\nimport "{PREFIX}customer/port"\n', "platform imports business")

    def test_runtime_test_helper_import_rejected_even_from_cmd(self):
        self.check("cmd/aicrm/x.go", f'package main\nimport "{PREFIX}platform/config/tests"\n', "test-only helper")

    def test_runtime_env_alias_comments_and_spacing_rejected(self):
        self.check("internal/order/app/x.go", 'package app\nimport host "os"\nfunc f() { host /*noise*/ . LookupEnv /*noise*/ ("X") }', "reads environment")

    def test_runtime_dot_import_env_rejected(self):
        self.check("internal/order/app/x.go", 'package app\nimport . "os"\nfunc f() { Getenv ("X") }', "reads environment")

    def test_verified_constructor_rejected_in_http_and_composition(self):
        for path in ("internal/identity/http/x.go", "cmd/aicrm/x.go"):
            with self.subTest(path=path):
                self.check(path, 'package x\nfunc f() { identity.NewVerifiedFact() }', "constructs a verified identity")

    def test_contracts_and_provider_constructor_remain_allowed(self):
        self.check("internal/order/provider/x.go", f'package provider\nimport "{PREFIX}customer/port"\nfunc f() {{ identity.NewVerifiedFact() }}')

    def test_fake_imports_env_and_constructor_inside_comments_or_strings_ignored(self):
        self.check("internal/order/app/x.go", f'''package app
// import "{PREFIX}customer/store"
var fixture = `import "{PREFIX}customer/store"
os.Getenv("X"); identity.NewVerifiedFact()`
''')

    def test_old_source_marker_remains_forbidden_in_fixture(self):
        self.check("internal/order/app/x_test.go", 'package app\nvar source = "AI-CRM-production"', "donor marker")


if __name__ == "__main__":
    unittest.main()
