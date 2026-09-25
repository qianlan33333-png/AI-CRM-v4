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
            (repo / "scripts/domestic_release.py").write_text("# updated fixed controller\n", encoding="utf-8")
            subprocess.run(["git", "-C", str(repo), "add", "cmd/aicrm/main.go", "scripts/domestic_release.py"], check=True)
            subprocess.run(["git", "-C", str(repo), "commit", "-qm", "add Go source"], check=True)
            with mock.patch.object(builder, "_make_plan", side_effect=AssertionError("target source executed")):
                result = builder.classify(repo, base, "HEAD")
            self.assertTrue(result["runtime_changed"])
            self.assertEqual(result["controller_files"], ["scripts/domestic_release.py"])

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
        for sample_path in (
            "deploy/domestic-main-release-example.json",
            "deploy/domestic-release.example.json",
            "deploy/domestic-release-role.production.example",
            "deploy/domestic-release-role.staging.example",
        ):
            with self.subTest(sample_path=sample_path):
                sample = builder.classify_paths([sample_path])
                self.assertFalse(sample.runtime_changed)
                self.assertFalse(sample.full_build)

    def test_preflight_and_reviewed_validation_tools_do_not_install_application(self) -> None:
        result = builder.classify_paths([
            "AGENTS.md",
            "docs/prd/2026-09-24-small-step-impact-checks.md",
            "skills/aicrm-v3-development/SKILL.md",
            "scripts/dev_preflight.py",
            "scripts/check-architecture.py",
            ".gitleaksignore",
        ])
        self.assertFalse(result.runtime_changed)
        self.assertFalse(result.full_build)
        self.assertEqual(result.build_mode, "none")

        # The allowlist stays explicit: a new executable script is not silently
        # assumed to be validation-only.
        unknown = builder.classify_paths(["scripts/new-release-helper.py"])
        self.assertTrue(unknown.runtime_changed)
        self.assertTrue(unknown.full_build)

    def test_manual_github_sync_is_operator_only_not_an_application_build(self) -> None:
        result = builder.classify_paths(["scripts/manual_github_sync.py"])
        self.assertFalse(result.runtime_changed)
        self.assertFalse(result.full_build)
        self.assertEqual(result.build_mode, "none")
        self.assertEqual(result.controller_files, [])

    def test_aicrm_chromium_journeys_are_tests_but_application_mjs_stays_graph_checked(self) -> None:
        invitation_journey = "cmd/aicrm/invitation_chromium_journey.mjs"
        repository = Path(__file__).resolve().parents[1]
        self.assertTrue((repository / invitation_journey).is_file())
        invitation = builder.classify_paths([invitation_journey])
        self.assertFalse(invitation.runtime_changed)
        self.assertFalse(invitation.full_build)
        self.assertEqual(invitation.build_mode, "none")

        application_asset = "cmd/aicrm/application_runtime.mjs"
        application = builder.classify_paths([application_asset])
        self.assertTrue(application.runtime_changed)
        self.assertEqual(application.graph_paths, [application_asset])

        command = builder.ReleaseCommand("./cmd/aicrm", "aicrm")
        packages = {
            "example/cmd/aicrm": builder.GoPackage(
                "example/cmd/aicrm", "cmd/aicrm", [], {"cmd/aicrm/main.go"}, set(), set(),
            ),
        }
        graphs = {"./cmd/aicrm": {"example/cmd/aicrm"}}
        with self.assertRaises(builder.BuildError):
            builder.affected_go_commands([application_asset], [command], packages, graphs)

        embedded_asset = "cmd/aicrm/embedded_runtime.mjs"
        packages["example/cmd/aicrm"].embed_files.add(embedded_asset)
        self.assertEqual(
            builder.affected_go_commands([embedded_asset], [command], packages, graphs),
            ["./cmd/aicrm"],
        )

    def test_frontend_migration_and_infrastructure_impacts_choose_minimum_safe_build(self) -> None:
        frontend = builder.classify_paths(["web/v3/payment/page.ts"])
        self.assertTrue(frontend.runtime_changed)
        self.assertTrue(frontend.frontend_changed)
        self.assertFalse(frontend.full_build)
        self.assertEqual(frontend.build_mode, "frontend_incremental")
        self.assertEqual(frontend.frontend_build_paths, ["web/v3/payment/page.ts"])

        migration = builder.classify_paths(["migrations/0201_payment_status.sql"])
        self.assertTrue(migration.runtime_changed)
        self.assertTrue(migration.migrations_changed)
        self.assertFalse(migration.full_build)
        self.assertEqual(migration.build_mode, "manifest_only")
        self.assertEqual(migration.package_overlays, ["migrations"])

        for path in ("go.sum", "scripts/run-donor-view-consumers.sh", "deploy/install-release.sh", "components/excel-batches/batches.py"):
            with self.subTest(path=path):
                classified = builder.classify_paths([path])
                if path.startswith("components/excel-batches/"):
                    self.assertFalse(classified.full_build)
                    self.assertEqual(classified.build_mode, "manifest_only")
                else:
                    self.assertTrue(classified.full_build)
        for path in ("components/excel-batches/requirements.txt", "components/excel-batches/aicrm-excel-batches.service", "components/unknown/worker.py"):
            with self.subTest(path=path):
                self.assertTrue(builder.classify_paths([path]).full_build)
        self.assertTrue(builder.classify_paths(["web/donor-sources/library/static/page.js"]).full_build)

    def test_controller_and_ci_changes_are_not_app_builds_but_controller_files_are_explicit(self) -> None:
        controller = builder.classify_paths([
            "scripts/domestic_main_release.py",
            "scripts/domestic_release.py",
            "scripts/domestic_release_build.py",
            "deploy/domestic-promote.py",
            "deploy/aicrm-domestic-main-release.service",
            "deploy/aicrm-domestic-main-release.timer",
            "scripts/test_domestic_release.py",
        ])
        self.assertFalse(controller.runtime_changed)
        self.assertFalse(controller.full_build)
        self.assertEqual(controller.build_mode, "controller_only")
        self.assertEqual(controller.controller_files, [
            "deploy/aicrm-domestic-main-release.service",
            "deploy/aicrm-domestic-main-release.timer",
            "deploy/domestic-promote.py",
            "scripts/domestic_main_release.py",
            "scripts/domestic_release.py",
            "scripts/domestic_release_build.py",
        ])

        ci = builder.classify_paths([".github/workflows/ci.yml", "scripts/ci/impact_selection.py"])
        self.assertFalse(ci.runtime_changed)
        self.assertFalse(ci.full_build)
        self.assertEqual(ci.build_mode, "none")

    def test_shared_or_unclassified_paths_still_force_full_build(self) -> None:
        for path in ("go.mod", "scripts/run-donor-view-consumers.sh", "deploy/unknown.service", "internal/platform/release.go", "components/unknown/worker.py"):
            with self.subTest(path=path):
                self.assertTrue(builder.classify_paths([path]).full_build)

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
            self.assertEqual(manifest["build_mode"], "none")
            self.assertEqual(manifest["go_commands"], [])
            self.assertIn("build", manifest["phase_timings_seconds"])
            self.assertEqual(manifest["release_files_sha256"], hashlib.sha256((release / builder.CHECKSUM_NAME).read_bytes()).hexdigest())
            self.assertEqual(base_before, self._snapshot(base_release))
            self.assertEqual(json.loads((output / builder.MANIFEST_NAME).read_text(encoding="utf-8")), manifest)

    def test_migration_build_reuses_verified_binaries_and_replaces_payload_tree(self) -> None:
        with tempfile.TemporaryDirectory(prefix="domestic-release-migration-") as temporary:
            root = Path(temporary)
            repo = root / "repo"
            repo.mkdir()
            self._init_git_fixture(repo)
            (repo / "migrations").mkdir()
            (repo / "migrations/0001_base.sql").write_text("SELECT 1;\n", encoding="utf-8")
            subprocess.run(["git", "-C", str(repo), "add", "migrations/0001_base.sql"], check=True)
            subprocess.run(["git", "-C", str(repo), "commit", "-m", "base migration"], check=True, capture_output=True)
            base_sha = subprocess.check_output(["git", "-C", str(repo), "rev-parse", "HEAD"], text=True).strip()
            (repo / "migrations/0002_additive.sql").write_text("SELECT 2;\n", encoding="utf-8")
            subprocess.run(["git", "-C", str(repo), "add", "migrations/0002_additive.sql"], check=True)
            subprocess.run(["git", "-C", str(repo), "commit", "-m", "add migration"], check=True, capture_output=True)
            target_sha = subprocess.check_output(["git", "-C", str(repo), "rev-parse", "HEAD"], text=True).strip()

            base_release = root / "base-release"
            (base_release / "bin").mkdir(parents=True)
            (base_release / "bin/aicrm").write_bytes(b"previous-binary")
            (base_release / "migrations").mkdir()
            (base_release / "migrations/0001_base.sql").write_text("old packaged base\n", encoding="utf-8")
            (base_release / "migrations/stale.sql").write_text("must be removed\n", encoding="utf-8")
            builder.write_release_inventory(base_release)

            with mock.patch.object(builder, "_build_go_commands", side_effect=AssertionError("migration-only release must reuse binaries")), mock.patch.object(builder, "_build_frontend", side_effect=AssertionError("migration-only release must reuse frontend")):
                manifest = builder.build(repo, base_sha, target_sha, str(base_release), root / "output")

            release = root / "output/release"
            self.assertEqual((release / "bin/aicrm").read_bytes(), b"previous-binary")
            self.assertEqual((release / "migrations/0001_base.sql").read_text(), "SELECT 1;\n")
            self.assertEqual((release / "migrations/0002_additive.sql").read_text(), "SELECT 2;\n")
            self.assertFalse((release / "migrations/stale.sql").exists())
            self.assertEqual(manifest["build_mode"], "manifest_only")
            self.assertFalse(manifest["full_build"])
            self.assertEqual(manifest["go_commands"], [])
            builder.verify_release_inventory(release)

    def test_build_keeps_validation_scope_separate_from_deployed_package_base(self) -> None:
        with tempfile.TemporaryDirectory(prefix="domestic-release-dual-base-") as temporary:
            root = Path(temporary)
            repo = root / "repo"
            repo.mkdir()
            self._init_git_fixture(repo)
            (repo / "migrations").mkdir()
            (repo / "migrations/0001_base.sql").write_text("SELECT 1;\n", encoding="utf-8")
            subprocess.run(["git", "-C", str(repo), "add", "migrations/0001_base.sql"], check=True)
            subprocess.run(["git", "-C", str(repo), "commit", "-qm", "deployed base"], check=True)
            deployed_sha = subprocess.check_output(["git", "-C", str(repo), "rev-parse", "HEAD"], text=True).strip()

            (repo / "docs").mkdir()
            (repo / "docs/validation-notes.md").write_text("CI validation advanced here.\n", encoding="utf-8")
            subprocess.run(["git", "-C", str(repo), "add", "docs/validation-notes.md"], check=True)
            subprocess.run(["git", "-C", str(repo), "commit", "-qm", "advance validation baseline"], check=True)
            validation_sha = subprocess.check_output(["git", "-C", str(repo), "rev-parse", "HEAD"], text=True).strip()

            (repo / "migrations/0002_additive.sql").write_text("SELECT 2;\n", encoding="utf-8")
            subprocess.run(["git", "-C", str(repo), "add", "migrations/0002_additive.sql"], check=True)
            subprocess.run(["git", "-C", str(repo), "commit", "-qm", "add migration"], check=True)
            target_sha = subprocess.check_output(["git", "-C", str(repo), "rev-parse", "HEAD"], text=True).strip()

            base_release = root / "deployed-release"
            (base_release / "bin").mkdir(parents=True)
            (base_release / "bin/aicrm").write_bytes(b"deployed-binary")
            (base_release / "bin/migrate-customer").write_bytes(b"deployed-migration-binary")
            (base_release / "migrations").mkdir()
            (base_release / "migrations/0001_base.sql").write_text("SELECT 1;\n", encoding="utf-8")
            builder.write_release_inventory(base_release)

            output = root / "output"
            manifest = builder.build(
                repo, deployed_sha, target_sha, str(base_release), output,
                validation_scope_base_value=validation_sha,
            )
            release = output / "release"
            self.assertEqual(manifest["base_sha"], deployed_sha)
            self.assertEqual(manifest["validation_scope_base_sha"], validation_sha)
            self.assertEqual(manifest["validation_scope_base_tree"], builder._tree_sha(repo, validation_sha))
            self.assertEqual(manifest["validation_scope_changed_paths"], ["migrations/0002_additive.sql"])
            self.assertIs(manifest["actual_ci_baseline_verified"], False)
            self.assertEqual(manifest["go_commands"], [])
            self.assertEqual((release / "bin/aicrm").read_bytes(), b"deployed-binary")
            self.assertEqual((release / "bin/migrate-customer").read_bytes(), b"deployed-migration-binary")
            self.assertEqual((release / "migrations/0002_additive.sql").read_text(encoding="utf-8"), "SELECT 2;\n")

    def test_removing_migration_or_packaged_worker_source_fails_closed(self) -> None:
        for relative in ("migrations/0001_base.sql", "components/excel-batches/batches.py"):
            with self.subTest(relative=relative), tempfile.TemporaryDirectory(prefix="domestic-release-delete-") as temporary:
                root = Path(temporary)
                repo = root / "repo"
                repo.mkdir()
                self._init_git_fixture(repo)
                source = repo / relative
                source.parent.mkdir(parents=True, exist_ok=True)
                source.write_text("source\n", encoding="utf-8")
                subprocess.run(["git", "-C", str(repo), "add", relative], check=True)
                subprocess.run(["git", "-C", str(repo), "commit", "-m", "add packaged source"], check=True, capture_output=True)
                prior_sha = subprocess.check_output(["git", "-C", str(repo), "rev-parse", "HEAD"], text=True).strip()
                source.unlink()
                subprocess.run(["git", "-C", str(repo), "add", "-u", relative], check=True)
                subprocess.run(["git", "-C", str(repo), "commit", "-m", "remove packaged source"], check=True, capture_output=True)
                target_sha = subprocess.check_output(["git", "-C", str(repo), "rev-parse", "HEAD"], text=True).strip()

                with self.assertRaisesRegex(builder.BuildError, "deletion requires an explicit removal plan"):
                    builder.build(repo, prior_sha, target_sha, "none", root / "output")

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

    @unittest.skipUnless(shutil.which("go"), "Go is required for dependency graph integration")
    def test_plan_uses_aicrm_graph_and_base_graph_for_deleted_files(self) -> None:
        with tempfile.TemporaryDirectory(prefix="domestic-release-delete-graph-") as temporary:
            repo = Path(temporary) / "repo"
            repo.mkdir()
            self._init_go_fixture(repo)
            webshell = repo / "internal/webshell/webshell.go"
            webshell.write_text(
                'package webshell\nimport "embed"\n'
                '//go:embed static/*.html\nvar paymentPages embed.FS\n',
                encoding="utf-8",
            )
            (repo / "internal/webshell/static/keep.html").write_text("<h1>Keep</h1>\n", encoding="utf-8")
            (repo / "internal/webshell/legacy.go").write_text(
                "package webshell\nfunc legacyPage() string { return \"old\" }\n",
                encoding="utf-8",
            )
            subprocess.run(["git", "-C", str(repo), "add", "."], check=True)
            subprocess.run(["git", "-C", str(repo), "commit", "-qm", "establish removable graph inputs"], check=True)
            base_sha = subprocess.check_output(["git", "-C", str(repo), "rev-parse", "HEAD"], text=True).strip()

            # The aicrm composition package change is graph-resolvable, so it
            # rebuilds aicrm without forcing every migration binary.
            (repo / "cmd/aicrm/main.go").write_text(
                'package main\nimport _ "example.test/crm/internal/webshell"\nfunc main() { _ = "updated" }\n',
                encoding="utf-8",
            )
            # Delete one embed match and one Go file while retaining the
            # package. Only the base graph knows their prior package ownership.
            (repo / "internal/webshell/static/payment.html").unlink()
            (repo / "internal/webshell/legacy.go").unlink()
            subprocess.run(["git", "-C", str(repo), "add", "-A"], check=True)
            subprocess.run(["git", "-C", str(repo), "commit", "-qm", "remove old package inputs"], check=True)
            target_sha = subprocess.check_output(["git", "-C", str(repo), "rev-parse", "HEAD"], text=True).strip()

            result = builder.plan(repo, base_sha, target_sha)
            self.assertTrue(result["runtime_changed"])
            self.assertFalse(result["full_build"])
            self.assertEqual(result["go_commands"], ["./cmd/aicrm"])
            self.assertEqual(
                result["changed_paths"],
                [
                    "cmd/aicrm/main.go",
                    "internal/webshell/legacy.go",
                    "internal/webshell/static/payment.html",
                ],
            )

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
