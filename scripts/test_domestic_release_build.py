#!/usr/bin/env python3
from __future__ import annotations

import hashlib
import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest import mock
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import domestic_release_build as builder


class DomesticReleaseBuildTests(unittest.TestCase):
    def test_classify_does_not_execute_target_source(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            repo = Path(temporary)
            self._init_git_fixture(repo)
            base = subprocess.check_output(["git", "-C", str(repo), "rev-parse", "HEAD"], text=True).strip()
            (repo / "cmd/aicrm").mkdir(parents=True)
            (repo / "cmd/aicrm/main.go").write_text("package main\nfunc main() {}\n")
            subprocess.run(["git", "-C", str(repo), "add", "cmd/aicrm/main.go"], check=True)
            subprocess.run(["git", "-C", str(repo), "commit", "-qm", "add Go source"], check=True)
            with mock.patch.object(builder, "_make_plan", side_effect=AssertionError("target source executed")):
                result = builder.classify(repo, base, "HEAD")
            self.assertTrue(result["runtime_changed"])

    def test_docs_and_tests_do_not_change_runtime(self) -> None:
        result = builder.classify_paths([
            "README.md",
            "docs/release.md",
            "internal/customer/identity_test.go",
            "scripts/test-release-shape.py",
        ])
        self.assertFalse(result.runtime_changed)
        self.assertFalse(result.frontend_changed)
        self.assertFalse(result.full_build)
        self.assertFalse(result.migrations_changed)

    def test_frontend_change_is_incremental_and_migration_is_full(self) -> None:
        frontend = builder.classify_paths(["web/v3/payment/page.ts"])
        self.assertTrue(frontend.runtime_changed)
        self.assertTrue(frontend.frontend_changed)
        self.assertFalse(frontend.full_build)
        self.assertEqual(frontend.frontend_build_paths, ["web/v3/payment/page.ts"])

        migration = builder.classify_paths(["migrations/0201_payment_status.sql"])
        self.assertTrue(migration.runtime_changed)
        self.assertTrue(migration.migrations_changed)
        self.assertTrue(migration.full_build)

        for path in ("go.sum", "scripts/run-donor-view-consumers.sh", "deploy/install-release.sh", "components/excel-batches/batches.py"):
            with self.subTest(path=path):
                self.assertTrue(builder.classify_paths([path]).full_build)
        self.assertTrue(builder.classify_paths(["web/donor-sources/library/static/page.js"]).full_build)

    def test_dependency_graph_limits_build_to_affected_command_and_embed_consumer(self) -> None:
        commands = [
            builder.ReleaseCommand("./cmd/aicrm", "aicrm"),
            builder.ReleaseCommand("./cmd/migrate-customer", "migrate-customer"),
        ]
        packages = {
            "example/cmd/aicrm": builder.GoPackage("example/cmd/aicrm", "cmd/aicrm", ["example/internal/customer"], set(), set()),
            "example/cmd/migrate-customer": builder.GoPackage("example/cmd/migrate-customer", "cmd/migrate-customer", [], set(), set()),
            "example/internal/customer": builder.GoPackage(
                "example/internal/customer", "internal/customer", [], {"internal/customer/customer.go"},
                {"internal/customer/static/payment.html"}, {"static"},
            ),
        }
        graphs = {
            "./cmd/aicrm": {"example/cmd/aicrm", "example/internal/customer"},
            "./cmd/migrate-customer": {"example/cmd/migrate-customer"},
        }
        source_change = builder.affected_go_commands(
            ["internal/customer/customer.go"], commands, packages, graphs,
        )
        embedded_asset_change = builder.affected_go_commands(
            ["internal/customer/static/payment.html"], commands, packages, graphs,
        )
        deleted_embedded_asset_change = builder.affected_go_commands(
            ["internal/customer/static/removed.html"], commands, packages, graphs,
        )
        self.assertEqual(source_change, ["./cmd/aicrm"])
        self.assertEqual(embedded_asset_change, ["./cmd/aicrm"])
        self.assertEqual(deleted_embedded_asset_change, ["./cmd/aicrm"])

    def test_build_emits_complete_inventory_and_does_not_modify_base_release(self) -> None:
        with tempfile.TemporaryDirectory(prefix="domestic-release-test-") as temporary:
            root = Path(temporary)
            repo = root / "repo"
            repo.mkdir()
            self._init_git_fixture(repo)
            base_sha = subprocess.check_output(["git", "-C", str(repo), "rev-parse", "HEAD"], text=True).strip()

            (repo / "docs").mkdir()
            (repo / "docs/release.md").write_text("documentation update\n", encoding="utf-8")
            subprocess.run(["git", "-C", str(repo), "add", "docs/release.md"], check=True)
            subprocess.run(["git", "-C", str(repo), "commit", "-m", "docs: update release notes"], check=True, capture_output=True)
            target_sha = subprocess.check_output(["git", "-C", str(repo), "rev-parse", "HEAD"], text=True).strip()

            base_release = root / "base-release"
            (base_release / "bin").mkdir(parents=True)
            (base_release / "bin/aicrm").write_bytes(b"previous-binary")
            (base_release / "web").mkdir()
            (base_release / "web/index.html").write_text("previous-ui\n", encoding="utf-8")
            (base_release / "release.env").write_text("PRIVATE=must-not-ship\n", encoding="utf-8")
            builder.write_release_inventory(base_release)
            builder.verify_release_inventory(base_release, allow_release_env=True)
            base_before = self._snapshot(base_release)

            output = root / "output"
            manifest = builder.build(repo, base_sha, target_sha, str(base_release), output)

            release = output / "release"
            self.assertEqual((release / "bin/aicrm").read_bytes(), b"previous-binary")
            self.assertEqual((release / "web/index.html").read_text(encoding="utf-8"), "previous-ui\n")
            self.assertFalse((release / "release.env").exists())
            self.assertFalse((release / "domestic-release.json").exists())
            inventory = (release / builder.CHECKSUM_NAME).read_text(encoding="utf-8")
            self.assertIn("bin/aicrm", inventory)
            self.assertIn("web/index.html", inventory)
            self.assertNotIn("release.env", inventory)
            self.assertNotIn(builder.CHECKSUM_NAME, inventory)
            self.assertEqual(manifest["source_sha"], target_sha)
            self.assertEqual(manifest["base_sha"], base_sha)
            self.assertFalse(manifest["runtime_changed"])
            self.assertFalse(manifest["full_build"])
            self.assertEqual(manifest["go_commands"], [])
            self.assertEqual(manifest["release_files_sha256"], hashlib.sha256((release / builder.CHECKSUM_NAME).read_bytes()).hexdigest())
            self.assertEqual(base_before, self._snapshot(base_release))
            self.assertEqual(json.loads((output / builder.MANIFEST_NAME).read_text(encoding="utf-8")), manifest)

    @unittest.skipUnless(shutil.which("go"), "Go is required for dependency graph integration")
    def test_plan_uses_go_list_graph_for_embedded_frontend_asset(self) -> None:
        with tempfile.TemporaryDirectory(prefix="domestic-release-graph-") as temporary:
            repo = Path(temporary) / "repo"
            repo.mkdir()
            self._init_go_fixture(repo)
            base_sha = subprocess.check_output(["git", "-C", str(repo), "rev-parse", "HEAD"], text=True).strip()

            (repo / "internal/webshell/static/payment.html").write_text("<h1>Updated</h1>\n", encoding="utf-8")
            subprocess.run(["git", "-C", str(repo), "add", "internal/webshell/static/payment.html"], check=True)
            subprocess.run(["git", "-C", str(repo), "commit", "-m", "update embedded payment page"], check=True, capture_output=True)
            target_sha = subprocess.check_output(["git", "-C", str(repo), "rev-parse", "HEAD"], text=True).strip()

            result = builder.plan(repo, base_sha, target_sha)
            self.assertTrue(result["runtime_changed"])
            self.assertTrue(result["frontend_changed"])
            self.assertFalse(result["full_build"])
            self.assertEqual(result["go_commands"], ["./cmd/aicrm"])
            self.assertEqual(result["changed_paths"], ["internal/webshell/static/payment.html"])

    @staticmethod
    def _snapshot(root: Path) -> dict[str, bytes]:
        return {
            path.relative_to(root).as_posix(): path.read_bytes()
            for path in root.rglob("*") if path.is_file()
        }

    @staticmethod
    def _init_git_fixture(repo: Path) -> None:
        (repo / "scripts").mkdir()
        (repo / "scripts/run-donor-view-consumers.sh").write_text(
            "#!/usr/bin/env bash\n"
            "go build -trimpath -ldflags \"-s -w\" -o release/bin/aicrm ./cmd/aicrm\n",
            encoding="utf-8",
        )
        (repo / "scripts/build-wecom-archive-sdk-runner-linux.sh").write_text("#!/usr/bin/env bash\n", encoding="utf-8")
        (repo / "README.md").write_text("fixture\n", encoding="utf-8")
        subprocess.run(["git", "-C", str(repo), "init", "-q"], check=True)
        subprocess.run(["git", "-C", str(repo), "config", "user.name", "Release Builder Test"], check=True)
        subprocess.run(["git", "-C", str(repo), "config", "user.email", "release-builder-test@example.invalid"], check=True)
        subprocess.run(["git", "-C", str(repo), "add", "."], check=True)
        subprocess.run(["git", "-C", str(repo), "commit", "-m", "baseline"], check=True, capture_output=True)

    @staticmethod
    def _init_go_fixture(repo: Path) -> None:
        (repo / "scripts").mkdir()
        (repo / "cmd/aicrm").mkdir(parents=True)
        (repo / "cmd/migrate-customer").mkdir(parents=True)
        (repo / "internal/webshell/static").mkdir(parents=True)
        (repo / "go.mod").write_text("module example.test/crm\n\ngo 1.20\n", encoding="utf-8")
        (repo / "cmd/aicrm/main.go").write_text(
            'package main\nimport _ "example.test/crm/internal/webshell"\nfunc main() {}\n', encoding="utf-8",
        )
        (repo / "cmd/migrate-customer/main.go").write_text("package main\nfunc main() {}\n", encoding="utf-8")
        (repo / "internal/webshell/webshell.go").write_text(
            'package webshell\nimport _ "embed"\n//go:embed static/payment.html\nvar paymentPage string\n', encoding="utf-8",
        )
        (repo / "internal/webshell/static/payment.html").write_text("<h1>Initial</h1>\n", encoding="utf-8")
        (repo / "scripts/run-donor-view-consumers.sh").write_text(
            "go build -trimpath -ldflags \"-s -w\" -o release/bin/aicrm ./cmd/aicrm\n"
            "go build -trimpath -ldflags \"-s -w\" -o release/bin/migrate-customer ./cmd/migrate-customer\n",
            encoding="utf-8",
        )
        (repo / "scripts/build-wecom-archive-sdk-runner-linux.sh").write_text("#!/usr/bin/env bash\n", encoding="utf-8")
        subprocess.run(["git", "-C", str(repo), "init", "-q"], check=True)
        subprocess.run(["git", "-C", str(repo), "config", "user.name", "Release Builder Test"], check=True)
        subprocess.run(["git", "-C", str(repo), "config", "user.email", "release-builder-test@example.invalid"], check=True)
        subprocess.run(["git", "-C", str(repo), "add", "."], check=True)
        subprocess.run(["git", "-C", str(repo), "commit", "-m", "baseline"], check=True, capture_output=True)


if __name__ == "__main__":
    unittest.main(verbosity=2)
