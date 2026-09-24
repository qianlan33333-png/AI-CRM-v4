import importlib.util
import hashlib
import io
import json
import os
from contextlib import ExitStack, contextmanager, redirect_stderr, redirect_stdout
from pathlib import Path
import subprocess
import tempfile
import unittest
from types import SimpleNamespace
from unittest import mock


ROOT = Path(__file__).resolve().parents[1]

def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


worker = load("domestic_release", ROOT / "scripts/domestic_release.py")
installer = load("domestic_promote", ROOT / "deploy/domestic-promote.py")


def service_process(sha, pid=1, active=True):
    return {
        "active": active,
        "pid": pid,
        "executable": f"/opt/aicrm/releases/{sha}/bin/aicrm",
    }


def success_receipt(metadata, previous_sha):
    return {
        "schema_version": 1,
        "source_sha": metadata["source_sha"],
        "source_tree": metadata.get("source_tree"),
        "manifest_sha256": metadata["release_files_sha256"],
        "previous_sha": previous_sha,
        "database_backup": None,
        "technical_status": "installed_healthy",
        "installed_at_utc": "2026-09-24T00:00:00Z",
    }


def release_readback(sha, manifest_sha, *, receipt_target=None, receipt_exists=False, receipt=None):
    target = receipt_target or sha
    return {
        "current": f"/opt/aicrm/releases/{sha}",
        "release_env": f"AICRM_RELEASE_SHA={sha}\n",
        "manifest_sha256": manifest_sha,
        "readyz": {"release_sha": sha, "status": "ready"},
        "services": {unit: service_process(sha) for unit in ("aicrm.service", "aicrm-effects-worker.service")},
        "receipt_target_sha": target,
        "receipt_exists": receipt_exists,
        "receipt": receipt,
    }


def encode_metadata(metadata):
    content = json.dumps(metadata, sort_keys=True).encode()
    return content, hashlib.sha256(content).hexdigest()


class DomesticReleaseTest(unittest.TestCase):
    def test_database_uri_maps_stage_and_production_values_only_to_pg_environment(self):
        stage = installer.database_environment(
            "postgres://stage_user:stage%40secret@127.0.0.1/aicrm_stage?sslmode=disable"
        )
        production = installer.database_environment(
            "postgresql://prod%2Buser:prod%2Fsecret@db.internal:6432/aicrm_prod?sslmode=verify-full"
        )
        self.assertEqual(stage["PGUSER"], "stage_user")
        self.assertEqual(stage["PGPASSWORD"], "stage@secret")
        self.assertEqual(stage["PGDATABASE"], "aicrm_stage")
        self.assertEqual(stage["PGPORT"], "5432")
        self.assertEqual(stage["PGSSLMODE"], "disable")
        self.assertEqual(production["PGUSER"], "prod+user")
        self.assertEqual(production["PGPASSWORD"], "prod/secret")
        self.assertEqual(production["PGDATABASE"], "aicrm_prod")
        self.assertEqual(production["PGPORT"], "6432")
        self.assertEqual(production["PGSSLMODE"], "verify-full")

    def test_database_uri_rejects_unsupported_or_malformed_options_without_echoing_secret(self):
        for url in (
            "postgres://user:synthetic-secret@localhost/aicrm?sslmode=disable&connect_timeout=5",
            "postgres://user:synthetic-secret@localhost/aicrm?sslmode=disable&sslmode=require",
            "postgres://user:synthetic-secret@localhost/aicrm?sslmode=unknown",
            "postgres://user:synthetic-secret@localhost/aicrm%ZZ?sslmode=disable",
        ):
            with self.subTest(url=url), self.assertRaises(ValueError) as raised:
                installer.database_environment(url)
            self.assertNotIn("synthetic-secret", str(raised.exception))

    def test_backup_passes_uri_credentials_in_environment_not_argv_or_errors(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            env_file = root / "runtime.env"
            env_file.write_text(
                "AICRM_DATABASE_URL=postgres://stage_user:synthetic-secret@127.0.0.1/aicrm_stage?sslmode=disable\n"
            )
            calls = []

            def fake_dump(args, **kwargs):
                calls.append((args, kwargs))
                kwargs["stdout"].write(b"x" * 256)
                return SimpleNamespace(returncode=0)

            with mock.patch.object(installer, "ROOT", root), mock.patch.object(installer, "ENV", env_file), mock.patch.object(installer, "require_host_role", return_value="production"), mock.patch.object(installer.pwd, "getpwnam", return_value=SimpleNamespace(pw_dir="/var/lib/aicrm")), mock.patch.object(installer.subprocess, "run", side_effect=fake_dump), mock.patch.object(installer, "run") as restore:
                backup = installer.backup_database("b" * 40)

            self.assertTrue(backup.is_file())
            args, kwargs = calls[0]
            self.assertEqual(args[0], "/usr/sbin/runuser")
            self.assertEqual(args[-2], "pg_dump")
            self.assertEqual(kwargs["env"]["PATH"], "/usr/bin:/bin")
            self.assertNotIn("synthetic-secret", repr(args))
            self.assertNotIn("PGDATABASE=postgres://", repr(args))
            self.assertEqual(kwargs["env"]["PGPASSWORD"], "synthetic-secret")
            self.assertEqual(kwargs["env"]["PGDATABASE"], "aicrm_stage")
            self.assertEqual(kwargs["env"]["HOME"], "/var/lib/aicrm")
            self.assertNotIn("synthetic-secret", repr(restore.call_args))

    def test_backup_failure_does_not_copy_client_stderr_into_error(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            env_file = root / "runtime.env"
            env_file.write_text(
                "AICRM_DATABASE_URL=postgres://stage_user:synthetic-secret@127.0.0.1/aicrm_stage?sslmode=disable\n"
            )

            def failed_dump(_args, **kwargs):
                self.assertEqual(kwargs["stderr"], subprocess.PIPE)
                return SimpleNamespace(returncode=1, stderr=b"synthetic-secret")

            with mock.patch.object(installer, "ROOT", root), mock.patch.object(installer, "ENV", env_file), mock.patch.object(installer, "require_host_role", return_value="production"), mock.patch.object(installer.pwd, "getpwnam", return_value=SimpleNamespace(pw_dir="/var/lib/aicrm")), mock.patch.object(installer.subprocess, "run", side_effect=failed_dump):
                with self.assertRaisesRegex(RuntimeError, "pg_dump failed") as raised:
                    installer.backup_database("b" * 40)
            self.assertNotIn("synthetic-secret", str(raised.exception))

    def test_cached_artifact_verification_streams_release_files(self):
        with tempfile.TemporaryDirectory() as temporary:
            release = Path(temporary) / "release"
            (release / "bin").mkdir(parents=True)
            (release / "web/dist").mkdir(parents=True)
            (release / "bin/aicrm").write_bytes(b"x" * (4 * 1024 * 1024))
            (release / "web/dist/index.html").write_text("<html></html>")
            entries = []
            for path in sorted(item for item in release.rglob("*") if item.is_file()):
                entries.append(f"{hashlib.sha256(path.read_bytes()).hexdigest()}  {path.relative_to(release).as_posix()}\n")
            manifest = release / "release-files.sha256"
            manifest.write_text("".join(entries))
            metadata = {"release_files_sha256": hashlib.sha256(manifest.read_bytes()).hexdigest()}
            original_read_bytes = Path.read_bytes

            def reject_artifact_read_bytes(path):
                if path.is_relative_to(release):
                    raise AssertionError("cached release files must be hashed in chunks")
                return original_read_bytes(path)

            with mock.patch.object(Path, "read_bytes", reject_artifact_read_bytes):
                worker.verify_release_artifact(release, metadata)

    def recovery_fixture(self, root, *, migrations_changed=False, already_attempted=False):
        sha0, sha1 = "a" * 40, "b" * 40
        tree = "d" * 40
        previous_manifest = "e" * 64
        repo = root / "repo"
        repo.mkdir()
        work_root = root / "work"
        build = work_root / "builds" / sha1
        release = build / "release"
        (release / "bin").mkdir(parents=True)
        (release / "bin/aicrm").write_text("release bytes")
        manifest = release / "release-files.sha256"
        manifest.write_text(f"{installer.digest(release / 'bin/aicrm')}  bin/aicrm\n")
        metadata = {
            "source_sha": sha1,
            "base_sha": sha0,
            "source_tree": tree,
            "release_files_sha256": installer.digest(manifest),
            "migrations_changed": migrations_changed,
        }
        metadata_path = build / "domestic-release.json"
        metadata_path.write_text(json.dumps(metadata))
        state = {
            "status": "outcome_unknown",
            "processed_sha": sha0,
            "deployed_source_sha": sha0,
            "prod_installed_sha": sha0,
            "prod_installed_manifest_sha256": previous_manifest,
            "baseline_prod_manifest_sha256": previous_manifest,
            "blocked_sha": sha1,
            "failure": "health failed",
        }
        if already_attempted:
            state["recovery_attempted_sha"] = sha1
        state_path = root / "state.json"
        state_path.write_text(json.dumps(state))
        config = {
            "repo": str(repo),
            "work_root": str(work_root),
            "state": str(state_path),
            "production_enabled": True,
            "prod_key": "key",
            "prod_known_hosts": "known-hosts",
            "prod_user": "ubuntu",
            "prod_host": "10.0.4.13",
            "prod_incoming": "/opt/aicrm/domestic-incoming",
            "prod_helper": "/fixed/domestic-promote.py",
        }
        stage_receipt = success_receipt(metadata, sha0)
        stage = release_readback(sha1, metadata["release_files_sha256"], receipt_exists=True, receipt=stage_receipt)
        old = release_readback(sha0, previous_manifest, receipt_target=sha1)
        installed = release_readback(sha1, metadata["release_files_sha256"], receipt_exists=True, receipt=success_receipt(metadata, sha0))

        def fake_git(_repo, *args):
            if args[0] == "rev-parse" and args[1] == "refs/remotes/origin/main":
                return sha1
            if args[0] == "rev-parse" and args[1] == f"{sha1}^{{tree}}":
                return tree
            if args[0] == "show":
                return "1760000000"
            return ""

        return {
            "sha0": sha0,
            "sha1": sha1,
            "tree": tree,
            "previous_manifest": previous_manifest,
            "repo": repo,
            "work_root": work_root,
            "build": build,
            "metadata": metadata,
            "metadata_path": metadata_path,
            "state_path": state_path,
            "config": config,
            "stage": stage,
            "old": old,
            "installed": installed,
            "fake_git": fake_git,
        }

    @contextmanager
    def recovery_patches(self, fixture, production_readbacks, command_side_effect=None):
        def unexpected_command(*_args, **_kwargs):
            self.fail("unexpected external command during recovery test")

        with ExitStack() as stack:
            patches = {
                "origin": stack.enter_context(mock.patch.object(worker, "require_official_origin")),
                "git": stack.enter_context(mock.patch.object(worker, "git", side_effect=fixture["fake_git"])),
                "queue": stack.enter_context(mock.patch.object(worker, "first_parent_queue", return_value=[fixture["sha1"]])),
                "checks": stack.enter_context(mock.patch.object(worker, "exact_check_success", return_value=True)),
                "stage": stack.enter_context(mock.patch.object(worker, "stage_readback", return_value=fixture["stage"])),
                "production": stack.enter_context(mock.patch.object(worker, "prod_readback", side_effect=production_readbacks)),
                "command": stack.enter_context(mock.patch.object(worker, "command", side_effect=command_side_effect or unexpected_command)),
            }
            yield patches

    @contextmanager
    def installer_patches(self, root, current, env):
        with ExitStack() as stack:
            patches = {
                "systemctl": stack.enter_context(mock.patch.object(installer.shutil, "which", return_value="/bin/systemctl")),
                "run": stack.enter_context(mock.patch.object(installer, "run")),
                "switch": stack.enter_context(mock.patch.object(installer, "switch_to")),
                "restart": stack.enter_context(mock.patch.object(installer, "restart_services")),
                "readiness": stack.enter_context(mock.patch.object(installer, "readiness")),
                "backup": stack.enter_context(mock.patch.object(installer, "backup_database")),
                "units": stack.enter_context(mock.patch.object(installer, "install_units")),
            }
            stack.enter_context(mock.patch.object(installer, "ROOT", root))
            stack.enter_context(mock.patch.object(installer, "RELEASES", root / "releases"))
            stack.enter_context(mock.patch.object(installer, "CURRENT", current))
            stack.enter_context(mock.patch.object(installer, "LOCK", root / "install.lock"))
            stack.enter_context(mock.patch.object(installer, "RECEIPTS", root / "domestic-receipts"))
            stack.enter_context(mock.patch.object(installer, "ENV", env))
            yield patches

    def test_recover_retries_verified_orphan_once_and_advances_cursor(self):
        with tempfile.TemporaryDirectory() as temp:
            fixture = self.recovery_fixture(Path(temp))
            expected_digest = installer.digest(fixture["metadata_path"])
            production_receipt = fixture["installed"]["receipt"]

            def fake_command(*args, **_kwargs):
                if "sha256sum" in args:
                    return f"{expected_digest}  {fixture['metadata_path']}"
                if "--retry-existing" in args:
                    recorded = json.loads(fixture["state_path"].read_text())
                    self.assertEqual(recorded.get("recovery_attempted_sha"), fixture["sha1"])
                    self.assertTrue(recorded.get("recovery_attempted_at_utc"))
                    return json.dumps(production_receipt)
                self.fail(f"unexpected recovery command: {args}")

            with self.recovery_patches(fixture, [fixture["old"], fixture["installed"]], fake_command) as calls:
                result = worker.recover(fixture["config"], retry_blocked=True, expected_sha=fixture["sha1"])

            state = json.loads(fixture["state_path"].read_text())
            self.assertEqual(result, {"status": "ready", "processed_sha": fixture["sha1"], "recovery": "orphan_reused_healthy"})
            self.assertEqual(state["status"], "ready")
            self.assertEqual(state["processed_sha"], fixture["sha1"])
            self.assertEqual(state["deployed_source_sha"], fixture["sha1"])
            self.assertEqual(state["prod_installed_sha"], fixture["sha1"])
            self.assertIsNone(state["blocked_sha"])
            self.assertEqual(state["last_recovery_status"], "orphan_reused_healthy")
            self.assertEqual(calls["production"].call_count, 2)
            self.assertEqual(calls["command"].call_count, 2)
            retry_args = calls["command"].call_args_list[1].args
            self.assertIn("--retry-existing", retry_args)
            self.assertEqual(retry_args[retry_args.index("--expected-sha") + 1], fixture["sha1"])
            self.assertEqual(retry_args[retry_args.index("--metadata-sha256") + 1], expected_digest)
            self.assertEqual(retry_args[-1], fixture["sha0"])

    def test_recover_with_matching_receipt_only_repairs_cursor(self):
        with tempfile.TemporaryDirectory() as temp:
            fixture = self.recovery_fixture(Path(temp))
            with self.recovery_patches(fixture, [fixture["installed"]]) as calls:
                result = worker.recover(fixture["config"], retry_blocked=True, expected_sha=fixture["sha1"])

            state = json.loads(fixture["state_path"].read_text())
            self.assertEqual(result["status"], "ready")
            self.assertEqual(state["status"], "ready")
            self.assertEqual(state["processed_sha"], fixture["sha1"])
            self.assertEqual(state["prod_installed_sha"], fixture["sha1"])
            self.assertEqual(state["last_recovery_status"], "readback_confirmed")
            self.assertIsNone(state["blocked_sha"])
            self.assertNotIn("recovery_attempted_sha", state)
            calls["production"].assert_called_once_with(fixture["config"], fixture["sha1"])
            calls["command"].assert_not_called()

    def test_recover_rejects_conflicting_production_current(self):
        with tempfile.TemporaryDirectory() as temp:
            fixture = self.recovery_fixture(Path(temp))
            conflict = dict(fixture["old"])
            conflict["current"] = f"/opt/aicrm/releases/{'c' * 40}"
            with self.recovery_patches(fixture, [conflict]) as calls:
                with self.assertRaisesRegex(RuntimeError, "conflicts with the prior release"):
                    worker.recover(fixture["config"], retry_blocked=True, expected_sha=fixture["sha1"])

            state = json.loads(fixture["state_path"].read_text())
            self.assertEqual(state["status"], "outcome_unknown")
            self.assertEqual(state["blocked_sha"], fixture["sha1"])
            calls["command"].assert_not_called()

    def test_recover_rejects_migration_release(self):
        with tempfile.TemporaryDirectory() as temp:
            fixture = self.recovery_fixture(Path(temp), migrations_changed=True)
            with self.recovery_patches(fixture, []) as calls:
                with self.assertRaisesRegex(RuntimeError, "disabled for migration releases"):
                    worker.recover(fixture["config"], retry_blocked=True, expected_sha=fixture["sha1"])

            calls["stage"].assert_not_called()
            calls["production"].assert_not_called()
            calls["command"].assert_not_called()

    def test_recover_rejects_second_orphan_retry_attempt(self):
        with tempfile.TemporaryDirectory() as temp:
            fixture = self.recovery_fixture(Path(temp), already_attempted=True)
            with self.recovery_patches(fixture, [fixture["old"]]) as calls:
                with self.assertRaisesRegex(RuntimeError, "already attempted"):
                    worker.recover(fixture["config"], retry_blocked=True, expected_sha=fixture["sha1"])

            state = json.loads(fixture["state_path"].read_text())
            self.assertEqual(state["status"], "outcome_unknown")
            self.assertEqual(state["blocked_sha"], fixture["sha1"])
            calls["command"].assert_not_called()

    def test_recover_retry_failure_keeps_release_blocked(self):
        with tempfile.TemporaryDirectory() as temp:
            fixture = self.recovery_fixture(Path(temp))
            expected_digest = installer.digest(fixture["metadata_path"])

            def fail_retry(*args, **_kwargs):
                if "sha256sum" in args:
                    return expected_digest
                if "--retry-existing" in args:
                    raise RuntimeError("remote helper timed out")
                self.fail(f"unexpected recovery command: {args}")

            with self.recovery_patches(fixture, [fixture["old"]], fail_retry) as calls:
                with self.assertRaisesRegex(RuntimeError, "remote helper timed out"):
                    worker.recover(fixture["config"], retry_blocked=True, expected_sha=fixture["sha1"])

            state = json.loads(fixture["state_path"].read_text())
            self.assertEqual(state["status"], "outcome_unknown")
            self.assertEqual(state["blocked_sha"], fixture["sha1"])
            self.assertEqual(state["prod_installed_sha"], fixture["sha0"])
            self.assertEqual(state["recovery_attempted_sha"], fixture["sha1"])
            self.assertEqual(state["failure"], "remote helper timed out")
            self.assertEqual(calls["command"].call_count, 2)

    def test_recover_requires_an_explicit_expected_sha_before_external_reads(self):
        with tempfile.TemporaryDirectory() as temp:
            fixture = self.recovery_fixture(Path(temp))
            with self.recovery_patches(fixture, []) as calls:
                with self.assertRaises(TypeError) as raised:
                    worker.recover(fixture["config"], retry_blocked=True)

            self.assertIn("expected_sha", str(raised.exception))
            calls["stage"].assert_not_called()
            calls["production"].assert_not_called()
            calls["command"].assert_not_called()

    def test_recover_rejects_an_expected_sha_that_differs_from_blocked_ledger(self):
        with tempfile.TemporaryDirectory() as temp:
            fixture = self.recovery_fixture(Path(temp))
            with self.recovery_patches(fixture, []) as calls:
                with self.assertRaisesRegex(RuntimeError, "explicit --sha does not match blocked"):
                    worker.recover(fixture["config"], retry_blocked=True, expected_sha="c" * 40)

            calls["stage"].assert_not_called()
            calls["production"].assert_not_called()
            calls["command"].assert_not_called()

    def test_recover_rejects_none_as_expected_sha_before_external_reads(self):
        with tempfile.TemporaryDirectory() as temp:
            fixture = self.recovery_fixture(Path(temp))
            with self.recovery_patches(fixture, []) as calls:
                with self.assertRaisesRegex(ValueError, "exact --sha"):
                    worker.recover(fixture["config"], retry_blocked=True, expected_sha=None)

            calls["stage"].assert_not_called()
            calls["production"].assert_not_called()
            calls["command"].assert_not_called()

    def test_recover_cli_requires_both_retry_flag_and_sha(self):
        sha = "b" * 40
        invalid_args = (
            ["domestic_release.py", "--config", "unused", "recover", "--sha", sha],
            ["domestic_release.py", "--config", "unused", "recover", "--retry-blocked"],
        )
        for argv in invalid_args:
            with self.subTest(argv=argv):
                with mock.patch.object(worker, "load_config", return_value={}), mock.patch.object(worker, "recover") as recover_call, mock.patch.object(worker.sys, "argv", argv):
                    with redirect_stderr(io.StringIO()), self.assertRaises(SystemExit) as raised:
                        worker.main()
                self.assertEqual(raised.exception.code, 2)
                recover_call.assert_not_called()

    def test_recover_cli_rejects_sha_for_other_actions(self):
        sha = "b" * 40
        invalid_args = (
            ["domestic_release.py", "--config", "unused", "poll", "--sha", sha],
            ["domestic_release.py", "--config", "unused", "readback", "--sha", sha],
            ["domestic_release.py", "--config", "unused", "bind-baseline", "--sha", sha, "--prod-preview-sha", sha],
        )
        for argv in invalid_args:
            with self.subTest(argv=argv):
                with mock.patch.object(worker, "load_config", return_value={}), mock.patch.object(worker, "poll") as poll_call, mock.patch.object(worker, "prod_readback") as readback_call, mock.patch.object(worker, "bind_baseline") as baseline_call, mock.patch.object(worker.sys, "argv", argv):
                    with redirect_stderr(io.StringIO()), self.assertRaises(SystemExit) as raised:
                        worker.main()
                self.assertEqual(raised.exception.code, 2)
                poll_call.assert_not_called()
                readback_call.assert_not_called()
                baseline_call.assert_not_called()

    def test_one_off_staging_retry_command_is_retired(self):
        argv = ["domestic_release.py", "--config", "unused", "retry-staging", "--sha", "5" * 40]
        with mock.patch.object(worker.sys, "argv", argv), redirect_stderr(io.StringIO()), self.assertRaises(SystemExit) as raised:
            worker.main()
        self.assertEqual(raised.exception.code, 2)

    def test_installer_cli_requires_both_identity_flags_in_both_modes(self):
        source_modes = (
            ["--incoming", "/tmp/domestic-incoming/" + "b" * 40],
            ["--retry-existing"],
        )
        identity_flags = (
            ("--expected-sha", "b" * 40),
            ("--metadata-sha256", "d" * 64),
        )
        for source_mode in source_modes:
            for omitted in ("--expected-sha", "--metadata-sha256"):
                with self.subTest(source_mode=source_mode, omitted=omitted):
                    argv = ["domestic-promote.py", *source_mode, "--metadata", "/tmp/domestic-incoming/metadata.json", "--expected-base", "a" * 40]
                    for flag, value in identity_flags:
                        if flag != omitted:
                            argv.extend((flag, value))
                    with mock.patch.object(installer.os, "geteuid", return_value=0), mock.patch.object(installer.sys, "argv", argv), redirect_stderr(io.StringIO()):
                        with self.assertRaises(SystemExit) as raised:
                            installer.main()
                    self.assertEqual(raised.exception.code, 2)

    def test_installer_cli_reads_metadata_once_and_passes_bound_bytes(self):
        old_sha, new_sha = "a" * 40, "b" * 40
        for retry_existing in (False, True):
            with self.subTest(retry_existing=retry_existing), tempfile.TemporaryDirectory() as temp:
                root = (Path(temp) / "opt/aicrm").resolve()
                metadata_root = root / "domestic-incoming"
                metadata_root.mkdir(parents=True)
                metadata = {"source_sha": new_sha, "migrations_changed": False}
                content, metadata_sha256 = encode_metadata(metadata)
                metadata_path = metadata_root / f"{new_sha}.json"
                metadata_path.write_bytes(content)
                incoming = metadata_root / new_sha
                incoming.mkdir()
                source_mode = ["--retry-existing"] if retry_existing else ["--incoming", str(incoming)]
                argv = [
                    "domestic-promote.py",
                    *source_mode,
                    "--metadata",
                    str(metadata_path),
                    "--expected-base",
                    old_sha,
                    "--expected-sha",
                    new_sha,
                    "--metadata-sha256",
                    metadata_sha256,
                ]
                original_read_bytes = Path.read_bytes
                read_paths = []

                def tracked_read_bytes(path):
                    read_paths.append(path)
                    return original_read_bytes(path)

                with mock.patch.object(installer, "ROOT", root), mock.patch.object(installer.os, "geteuid", return_value=0), mock.patch.object(installer, "install", return_value={"status": "ok"}) as install_call, mock.patch.object(installer.sys, "argv", argv), mock.patch.object(Path, "read_bytes", new=tracked_read_bytes), redirect_stdout(io.StringIO()):
                    installer.main()

                self.assertEqual(read_paths, [metadata_path])
                call_args, call_kwargs = install_call.call_args
                self.assertEqual(call_args[0], None if retry_existing else incoming)
                self.assertEqual(call_args[1], content)
                self.assertEqual(call_args[2], old_sha)
                self.assertEqual(call_kwargs["expected_sha"], new_sha)
                self.assertEqual(call_kwargs["metadata_sha256"], metadata_sha256)
                self.assertEqual(call_kwargs["retry_existing"], retry_existing)

    def test_excel_component_update_requires_active_service_restart(self):
        self.assertEqual(
            installer.changed_units({"changed_paths": ["components/excel-batches/batches.py"]}),
            ["aicrm-excel-batches.service"],
        )

    def test_first_parent_two_successive_merges(self):
        with tempfile.TemporaryDirectory() as temp:
            repo = Path(temp)
            subprocess.run(["git", "init", "-q", str(repo)], check=True)
            subprocess.run(["git", "-C", str(repo), "config", "user.email", "ci@example.invalid"], check=True)
            subprocess.run(["git", "-C", str(repo), "config", "user.name", "CI"], check=True)
            shas = []
            for number in range(3):
                (repo / "file").write_text(str(number))
                subprocess.run(["git", "-C", str(repo), "add", "file"], check=True)
                subprocess.run(["git", "-C", str(repo), "commit", "-qm", str(number)], check=True)
                shas.append(subprocess.check_output(["git", "-C", str(repo), "rev-parse", "HEAD"], text=True).strip())
            self.assertEqual(worker.first_parent_queue(repo, shas[0], shas[2]), shas[1:])

    def controller_fixture(self, root):
        repo = root / "repo"
        (repo / "scripts").mkdir(parents=True)
        (repo / "deploy").mkdir()
        sources = {
            "scripts/domestic_release.py": b"controller-v1\n",
            "scripts/domestic_release_build.py": b"builder-v1\n",
            "deploy/domestic-promote.py": b"installer-v1\n",
        }
        for relative, content in sources.items():
            (repo / relative).write_bytes(content)
        subprocess.run(["git", "-C", str(repo), "init", "-q"], check=True)
        subprocess.run(["git", "-C", str(repo), "config", "user.name", "Controller Test"], check=True)
        subprocess.run(["git", "-C", str(repo), "config", "user.email", "controller-test@example.invalid"], check=True)
        subprocess.run(["git", "-C", str(repo), "add", "."], check=True)
        subprocess.run(["git", "-C", str(repo), "commit", "-qm", "controller source"], check=True)
        sha = subprocess.check_output(["git", "-C", str(repo), "rev-parse", "HEAD"], text=True).strip()

        fixed = root / "fixed"
        fixed.mkdir()
        (fixed / "domestic_release.py").write_bytes(sources["scripts/domestic_release.py"])
        (fixed / "domestic_release_build.py").write_bytes(sources["scripts/domestic_release_build.py"])
        stage_helper = root / "domestic-promote.py"
        stage_helper.write_bytes(sources["deploy/domestic-promote.py"])
        config = {
            "repo": str(repo),
            "stage_helper": str(stage_helper),
            "prod_helper": "/usr/local/libexec/aicrm/domestic-promote.py",
        }
        hashes = {path: hashlib.sha256(content).hexdigest() for path, content in sources.items()}
        return repo, sha, fixed, config, hashes

    def test_controller_verification_requires_exact_checked_fixed_files(self):
        with tempfile.TemporaryDirectory(prefix="domestic-controller-verify-") as temporary:
            root = Path(temporary)
            repo, sha, fixed, config, hashes = self.controller_fixture(root)
            paths = ["scripts/domestic_release.py", "scripts/domestic_release_build.py", "deploy/domestic-promote.py"]
            with mock.patch.object(worker, "__file__", str(fixed / "domestic_release.py")), mock.patch.object(worker, "_remote_file_sha256", return_value=hashes["deploy/domestic-promote.py"]):
                result = worker.verify_controller_installation(config, repo, sha, paths)
                self.assertEqual(result["status"], "matched")
                self.assertEqual(result["source_sha"], sha)
                self.assertEqual(set(result["files"]), set(paths))
                self.assertEqual(result["files"]["deploy/domestic-promote.py"]["production_sha256"], hashes["deploy/domestic-promote.py"])

                (fixed / "domestic_release_build.py").write_bytes(b"stale builder\n")
                with self.assertRaisesRegex(RuntimeError, "fixed controller digest mismatch"):
                    worker.verify_controller_installation(config, repo, sha, paths)

    def test_controller_update_mismatch_halts_queue_without_advancing_or_installing_app(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            sha0, sha1 = "a" * 40, "b" * 40
            state_path = root / "state.json"
            state_path.write_text(json.dumps({"status": "ready", "processed_sha": sha0, "deployed_source_sha": sha0, "prod_installed_sha": sha0}))
            config = {"repo": str(root), "state": str(state_path), "production_enabled": True}
            plan = {"runtime_changed": False, "controller_files": ["scripts/domestic_release.py"]}
            with mock.patch.object(worker, "git", return_value=sha1), mock.patch.object(worker, "require_official_origin"), mock.patch.object(worker, "first_parent_queue", return_value=[sha1]), mock.patch.object(worker, "exact_check_success", return_value=True), mock.patch.object(worker, "command", return_value=json.dumps(plan)), mock.patch.object(worker, "verify_controller_installation", side_effect=RuntimeError("stale fixed copy")), mock.patch.object(worker, "build_candidate") as build, mock.patch.object(worker, "stage_install") as stage, mock.patch.object(worker, "copy_payload") as transfer:
                with self.assertRaisesRegex(RuntimeError, "install them under the maintenance lock"):
                    worker.poll(config)
            state = json.loads(state_path.read_text())
            self.assertEqual(state["status"], "controller_update_required")
            self.assertEqual(state["blocked_sha"], sha1)
            self.assertEqual(state["processed_sha"], sha0)
            self.assertEqual(state["deployed_source_sha"], sha0)
            build.assert_not_called()
            stage.assert_not_called()
            transfer.assert_not_called()

    def test_controller_update_match_advances_only_source_cursor(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            sha0, sha1 = "a" * 40, "b" * 40
            state_path = root / "state.json"
            state_path.write_text(json.dumps({
                "status": "controller_update_required", "processed_sha": sha0,
                "deployed_source_sha": sha0, "prod_installed_sha": sha0,
                "blocked_sha": sha1,
            }))
            config = {"repo": str(root), "state": str(state_path), "production_enabled": True}
            plan = {"runtime_changed": False, "controller_files": ["scripts/domestic_release.py"]}
            verification = {"source_sha": sha1, "source_tree": "c" * 40, "files": {}, "duration_seconds": 0.4, "status": "matched"}
            with mock.patch.object(worker, "git", return_value=sha1), mock.patch.object(worker, "require_official_origin"), mock.patch.object(worker, "first_parent_queue", return_value=[sha1]), mock.patch.object(worker, "exact_check_success", return_value=True), mock.patch.object(worker, "command", return_value=json.dumps(plan)), mock.patch.object(worker, "verify_controller_installation", return_value=verification), mock.patch.object(worker, "build_candidate") as build, mock.patch.object(worker, "stage_install") as stage:
                result = worker.poll(config)
            state = json.loads(state_path.read_text())
            self.assertEqual(result["status"], "ready")
            self.assertEqual(state["processed_sha"], sha1)
            self.assertEqual(state["deployed_source_sha"], sha0)
            self.assertEqual(state["prod_installed_sha"], sha0)
            self.assertEqual(state["last_controller_verification"], verification)
            self.assertEqual(state["last_release_timings_seconds"]["controller_readback"], 0.4)
            build.assert_not_called()
            stage.assert_not_called()

    def test_manifest_rejects_changed_and_extra_files(self):
        with tempfile.TemporaryDirectory() as temp:
            payload = Path(temp)
            (payload / "bin").mkdir()
            (payload / "web/dist").mkdir(parents=True)
            (payload / "bin/aicrm").write_text("app")
            (payload / "web/dist/index.html").write_text("page")
            entries = []
            for item in (payload / "bin/aicrm", payload / "web/dist/index.html"):
                entries.append(f"{installer.digest(item)}  {item.relative_to(payload).as_posix()}\n")
            manifest = payload / "release-files.sha256"
            manifest.write_text("".join(entries))
            meta = {"release_files_sha256": installer.digest(manifest)}
            installer.verify_payload(payload, meta)
            (payload / "web/dist/index.html").write_text("tampered")
            with self.assertRaisesRegex(ValueError, "digest mismatch"):
                installer.verify_payload(payload, meta)
            (payload / "web/dist/index.html").write_text("page")
            (payload / "extra").write_text("unlisted")
            with self.assertRaisesRegex(ValueError, "payload digest mismatch"):
                installer.verify_payload(payload, meta)

    def test_newest_failed_check_blocks_old_success(self):
        sha = "a" * 40
        runs = {"check_runs": [
            {"name": "check", "head_sha": sha, "app": {"slug": "github-actions"}, "id": 1, "started_at": "2026-01-01T00:00:00Z", "status": "completed", "conclusion": "success"},
            {"name": "check", "head_sha": sha, "app": {"slug": "github-actions"}, "id": 2, "started_at": "2026-01-01T01:00:00Z", "status": "completed", "conclusion": "failure"},
        ]}
        response = mock.MagicMock()
        response.__enter__.return_value = response
        with mock.patch.object(worker.urllib.request, "urlopen", return_value=response), mock.patch.object(worker.json, "load", return_value=runs):
            self.assertFalse(worker.exact_check_success(sha))

    def test_exact_check_reports_run_duration_for_release_timing(self):
        sha = "a" * 40
        runs = {"check_runs": [{
            "name": "check", "head_sha": sha, "app": {"slug": "github-actions"}, "id": 9,
            "started_at": "2026-09-24T01:00:00Z", "completed_at": "2026-09-24T01:00:23Z",
            "status": "completed", "conclusion": "success",
        }]}
        response = mock.MagicMock()
        response.__enter__.return_value = response
        observation = {}
        with mock.patch.object(worker.urllib.request, "urlopen", return_value=response), mock.patch.object(worker.json, "load", return_value=runs):
            self.assertTrue(worker.exact_check_success(sha, observation=observation))
        self.assertEqual(observation["duration_seconds"], 23)
        self.assertEqual(observation["completed_at"], "2026-09-24T01:00:23Z")

    def test_prod_install_failure_stops_as_outcome_unknown(self):
        # Fault injected after the production install was sent. The queue
        # cannot assume the failed reply means the remote install failed.
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            sha0, sha1 = "a" * 40, "b" * 40
            state = root / "state.json"
            state.write_text(json.dumps({"status": "ready", "processed_sha": sha0, "deployed_source_sha": sha0, "prod_installed_sha": sha0}))
            (root / "builds" / sha0 / "release").mkdir(parents=True)
            cfg = {"repo": str(root), "work_root": str(root), "state": str(state), "production_enabled": True, "prod_key": "key", "prod_known_hosts": "hosts", "prod_user": "ubuntu", "prod_host": "127.0.0.1", "prod_helper": "/fixed/helper"}
            metadata = {"source_sha": sha1, "release_files_sha256": "f" * 64}
            def fake_build_candidate(*_args):
                (root / "domestic-release.json").write_text(json.dumps(metadata))
                return root, metadata
            with mock.patch.object(worker, "git", return_value=sha1), mock.patch.object(worker, "require_official_origin"), mock.patch.object(worker, "first_parent_queue", return_value=[sha1]), mock.patch.object(worker, "exact_check_success", return_value=True), mock.patch.object(worker, "command", side_effect=[json.dumps({"runtime_changed": True}), RuntimeError("ssh interrupted")]), mock.patch.object(worker, "build_candidate", side_effect=fake_build_candidate), mock.patch.object(worker, "stage_install", return_value={}), mock.patch.object(worker, "copy_payload", return_value=("/incoming", "/meta")):
                with self.assertRaises(RuntimeError):
                    worker.poll(cfg)
            self.assertEqual(json.loads(state.read_text())["status"], "outcome_unknown")
            self.assertEqual(json.loads(state.read_text())["processed_sha"], sha0)
            self.assertEqual(json.loads(state.read_text())["staging_verified_sha"], sha1)

    def test_staging_failure_stops_before_production(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            sha0, sha1 = "a" * 40, "b" * 40
            state = root / "state.json"
            state.write_text(json.dumps({"status": "ready", "processed_sha": sha0, "deployed_source_sha": sha0, "prod_installed_sha": sha0}))
            (root / "builds" / sha0 / "release").mkdir(parents=True)
            cfg = {"repo": str(root), "work_root": str(root), "state": str(state), "production_enabled": True}
            with mock.patch.object(worker, "git", return_value=sha1), mock.patch.object(worker, "require_official_origin"), mock.patch.object(worker, "first_parent_queue", return_value=[sha1]), mock.patch.object(worker, "exact_check_success", return_value=True), mock.patch.object(worker, "command", return_value=json.dumps({"runtime_changed": True})), mock.patch.object(worker, "build_candidate", side_effect=RuntimeError("stage build failed")), mock.patch.object(worker, "copy_payload") as prod_copy:
                with self.assertRaisesRegex(RuntimeError, "stage build failed"):
                    worker.poll(cfg)
                prod_copy.assert_not_called()
            self.assertEqual(json.loads(state.read_text())["status"], "staging_failed")

    def test_two_checked_commits_install_in_order(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            sha0, sha1, sha2 = "a" * 40, "b" * 40, "c" * 40
            state = root / "state.json"
            state.write_text(json.dumps({"status": "ready", "processed_sha": sha0, "deployed_source_sha": sha0, "prod_installed_sha": sha0}))
            for sha in (sha0, sha1):
                (root / "builds" / sha / "release").mkdir(parents=True)
            cfg = {"repo": str(root), "work_root": str(root), "state": str(state), "production_enabled": True, "prod_key": "key", "prod_known_hosts": "hosts", "prod_user": "ubuntu", "prod_host": "127.0.0.1", "prod_helper": "/fixed/helper"}
            metadatas = [{"source_sha": sha, "source_tree": "d" * 40, "release_files_sha256": "f" * 64, "migrations_changed": False} for sha in (sha1, sha2)]
            receipts = [success_receipt(metadatas[0], sha0), success_receipt(metadatas[1], sha1)]
            command_results = [json.dumps({"runtime_changed": True}), json.dumps(receipts[0]), json.dumps({"runtime_changed": True}), json.dumps(receipts[1])]
            readbacks = [{"current": f"/opt/aicrm/releases/{sha}", "release_env": f"AICRM_RELEASE_SHA={sha}\n", "manifest_sha256": "f" * 64, "readyz": {"release_sha": sha, "status": "ready"}, "services": {unit: service_process(sha) for unit in ("aicrm.service", "aicrm-effects-worker.service")}, "receipt": receipts[index]} for index, sha in enumerate((sha1, sha2))]
            metadata_hashes = []
            def fake_git(_repo, *args):
                if args[0] == "rev-parse":
                    return sha2
                if args[0] == "show":
                    return "1760000000"
                return ""
            candidates = iter(metadatas)
            def fake_build_candidate(*_args):
                metadata = next(candidates)
                metadata_bytes = json.dumps(metadata).encode()
                (root / "domestic-release.json").write_bytes(metadata_bytes)
                metadata_hashes.append(hashlib.sha256(metadata_bytes).hexdigest())
                return root, metadata
            with mock.patch.object(worker, "git", side_effect=fake_git), mock.patch.object(worker, "require_official_origin"), mock.patch.object(worker, "first_parent_queue", return_value=[sha1, sha2]), mock.patch.object(worker, "exact_check_success", return_value=True), mock.patch.object(worker, "command", side_effect=command_results) as commands, mock.patch.object(worker, "build_candidate", side_effect=fake_build_candidate), mock.patch.object(worker, "stage_install", return_value={}) as stage, mock.patch.object(worker, "copy_payload", side_effect=[("/incoming/one", "/meta/one"), ("/incoming/two", "/meta/two")]) as transfer, mock.patch.object(worker, "prod_readback", side_effect=readbacks):
                worker.poll(cfg)
            self.assertEqual([call.args[3] for call in stage.call_args_list], [sha0, sha1])
            self.assertEqual([call.args[4] for call in transfer.call_args_list], [sha0, sha1])
            helper_calls = [call for call in commands.call_args_list if "--expected-sha" in call.args]
            self.assertEqual(len(helper_calls), 2)
            for call, expected_sha, expected_metadata_sha in zip(helper_calls, (sha1, sha2), metadata_hashes):
                self.assertEqual(call.args[call.args.index("--expected-sha") + 1], expected_sha)
                self.assertEqual(call.args[call.args.index("--metadata-sha256") + 1], expected_metadata_sha)
            self.assertEqual(json.loads(state.read_text())["processed_sha"], sha2)
            final_state = json.loads(state.read_text())
            self.assertEqual(final_state["status"], "ready")
            self.assertEqual(final_state["staging_verified_sha"], sha2)
            self.assertEqual(final_state["staging_verified_receipt"], {})
            timings = final_state["last_release_timings_seconds"]
            self.assertIn("transfer", timings)
            self.assertIn("production_install", timings)
            self.assertIn("production_readback", timings)
            self.assertIn("total", timings)

    def test_readback_rejects_wrong_manifest_and_stopped_service(self):
        sha = "a" * 40
        readback = {"current": f"/opt/aicrm/releases/{sha}", "release_env": f"AICRM_RELEASE_SHA={sha}\n", "manifest_sha256": "b" * 64, "readyz": {"release_sha": sha, "status": "ready"}, "services": {unit: service_process(sha, pid=12) for unit in ("aicrm.service", "aicrm-effects-worker.service")}}
        with self.assertRaisesRegex(RuntimeError, "manifest digest mismatch"):
            worker.verify_readback(readback, sha, "c" * 64)
        readback["services"]["aicrm-effects-worker.service"]["active"] = False
        with self.assertRaisesRegex(RuntimeError, "service not active"):
            worker.verify_readback(readback, sha, "b" * 64)

    def test_readback_rejects_a_process_running_another_release(self):
        sha = "a" * 40
        readback = {"current": f"/opt/aicrm/releases/{sha}", "release_env": f"AICRM_RELEASE_SHA={sha}\n", "manifest_sha256": "b" * 64, "readyz": {"release_sha": sha, "status": "ready"}, "services": {unit: service_process(sha, pid=12) for unit in ("aicrm.service", "aicrm-effects-worker.service")}}
        readback["services"]["aicrm-effects-worker.service"]["executable"] = f"/opt/aicrm/releases/{'c' * 40}/bin/aicrm"
        with self.assertRaisesRegex(RuntimeError, "executable mismatch"):
            worker.verify_readback(readback, sha, "b" * 64)

    def test_0700_incoming_root_is_opened_without_chmodding_hardlinked_files(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            releases = root / "releases"
            old_sha, new_sha = "a" * 40, "b" * 40
            old = releases / old_sha
            (old / "bin").mkdir(parents=True)
            shared_file = old / "bin/private.dat"
            shared_file.write_text("shared immutable bytes")
            shared_file.chmod(0o600)
            (old / "release.env").write_text(f"AICRM_RELEASE_SHA={old_sha}\n")
            current = root / "current"
            current.symlink_to(old)

            incoming = root / "incoming" / new_sha
            (incoming / "bin").mkdir(parents=True)
            (incoming / "web/dist").mkdir(parents=True)
            (incoming / "bin/aicrm").write_text("new app")
            (incoming / "web/dist/index.html").write_text("new page")
            os.link(shared_file, incoming / "bin/private.dat")
            incoming.chmod(0o700)
            entries = [
                f"{installer.digest(path)}  {path.relative_to(incoming).as_posix()}\n"
                for path in sorted(incoming.rglob("*"))
                if path.is_file()
            ]
            manifest = incoming / "release-files.sha256"
            manifest.write_text("".join(entries))
            metadata = {"source_sha": new_sha, "source_tree": "c" * 40, "release_files_sha256": installer.digest(manifest), "migrations_changed": False}
            metadata_bytes, metadata_sha256 = encode_metadata(metadata)
            env = root / "runtime.env"
            env.write_text("AICRM_DATABASE_URL=synthetic\n")
            with mock.patch.object(installer, "ROOT", root), mock.patch.object(installer, "RELEASES", releases), mock.patch.object(installer, "CURRENT", current), mock.patch.object(installer, "LOCK", root / "install.lock"), mock.patch.object(installer, "RECEIPTS", root / "domestic-receipts"), mock.patch.object(installer, "ENV", env), mock.patch.object(installer, "require_host_role", return_value="production"), mock.patch.object(installer.shutil, "which", return_value="/bin/systemctl"), mock.patch.object(installer, "run"), mock.patch.object(installer, "verify_root_owned_release"), mock.patch.object(installer, "backup_database") as backup, mock.patch.object(installer, "restart_services"), mock.patch.object(installer, "readiness"):
                receipt = installer.install(incoming, metadata_bytes, old_sha, expected_sha=new_sha, metadata_sha256=metadata_sha256)
            installed = releases / new_sha
            installed_shared = installed / "bin/private.dat"
            self.assertEqual(receipt["source_sha"], new_sha)
            self.assertEqual(current.resolve(), installed.resolve())
            self.assertEqual(installed.stat().st_mode & 0o777, 0o755)
            self.assertEqual((installed / "bin").stat().st_mode & 0o777, 0o755)
            self.assertEqual(installed_shared.stat().st_ino, shared_file.stat().st_ino)
            self.assertEqual(installed_shared.stat().st_mode & 0o777, 0o600)
            self.assertEqual(shared_file.stat().st_mode & 0o777, 0o600)
            backup.assert_not_called()

    def test_retry_existing_orphan_is_locked_verified_and_one_time_mode_only(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            releases = root / "releases"
            old_sha, new_sha = "a" * 40, "b" * 40
            old = releases / old_sha
            (old / "bin").mkdir(parents=True)
            (old / "release.env").write_text(f"AICRM_RELEASE_SHA={old_sha}\n")
            current = root / "current"
            current.symlink_to(old)
            orphan = releases / new_sha
            (orphan / "bin").mkdir(parents=True)
            (orphan / "web/dist").mkdir(parents=True)
            (orphan / "bin/aicrm").write_text("orphan app")
            (orphan / "web/dist/index.html").write_text("orphan page")
            orphan.chmod(0o700)
            entries = [
                f"{installer.digest(path)}  {path.relative_to(orphan).as_posix()}\n"
                for path in sorted(orphan.rglob("*"))
                if path.is_file()
            ]
            manifest = orphan / "release-files.sha256"
            manifest.write_text("".join(entries))
            (orphan / "release.env").write_text(f"AICRM_RELEASE_SHA={new_sha}\n")
            metadata = {"source_sha": new_sha, "source_tree": "c" * 40, "release_files_sha256": installer.digest(manifest), "migrations_changed": False}
            metadata_bytes, metadata_sha256 = encode_metadata(metadata)
            env = root / "runtime.env"
            env.write_text("AICRM_DATABASE_URL=synthetic\n")
            with mock.patch.object(installer, "ROOT", root), mock.patch.object(installer, "RELEASES", releases), mock.patch.object(installer, "CURRENT", current), mock.patch.object(installer, "LOCK", root / "install.lock"), mock.patch.object(installer, "RECEIPTS", root / "domestic-receipts"), mock.patch.object(installer, "ENV", env), mock.patch.object(installer, "require_host_role", return_value="production"), mock.patch.object(installer.shutil, "which", return_value="/bin/systemctl"), mock.patch.object(installer, "verify_root_owned_release"), mock.patch.object(installer, "run"), mock.patch.object(installer, "restart_services"), mock.patch.object(installer, "readiness") as readiness:
                receipt = installer.install(None, metadata_bytes, old_sha, expected_sha=new_sha, metadata_sha256=metadata_sha256, retry_existing=True)
            self.assertEqual(current.resolve(), orphan.resolve())
            self.assertEqual(orphan.stat().st_mode & 0o777, 0o755)
            self.assertEqual(readiness.call_args_list[0].args[0], old_sha)
            self.assertEqual(readiness.call_args_list[-1].args[0], new_sha)
            self.assertEqual(receipt["technical_status"], "installed_healthy")
            self.assertTrue((root / "domestic-receipts" / f"{new_sha}.json").is_file())

    def test_install_rejects_metadata_identity_mismatch_before_live_writes(self):
        old_sha, new_sha, wrong_sha = "a" * 40, "b" * 40, "c" * 40
        for retry_existing in (False, True):
            for mismatch in ("metadata hash", "source SHA"):
                with self.subTest(retry_existing=retry_existing, mismatch=mismatch), tempfile.TemporaryDirectory() as temp:
                    root = Path(temp)
                    releases = root / "releases"
                    releases.mkdir()
                    old = releases / old_sha
                    old.mkdir()
                    (old / "release.env").write_text(f"AICRM_RELEASE_SHA={old_sha}\n")
                    current = root / "current"
                    current.symlink_to(old)
                    incoming = root / "domestic-incoming" / new_sha
                    incoming.mkdir(parents=True)
                    orphan = releases / new_sha
                    if retry_existing:
                        orphan.mkdir()
                    metadata = {"source_sha": new_sha, "migrations_changed": False}
                    content, correct_digest = encode_metadata(metadata)
                    expected_sha = new_sha
                    supplied_digest = correct_digest
                    if mismatch == "metadata hash":
                        supplied_digest = "0" * 64
                    else:
                        expected_sha = wrong_sha
                    expected_error = "metadata SHA256 mismatch" if mismatch == "metadata hash" else "metadata source SHA mismatch"
                    env = root / "runtime.env"
                    env.write_text("AICRM_DATABASE_URL=synthetic\n")

                    with self.installer_patches(root, current, env) as calls:
                        with self.assertRaisesRegex(ValueError, expected_error):
                            installer.install(
                                None if retry_existing else incoming,
                                content,
                                old_sha,
                                expected_sha=expected_sha,
                                metadata_sha256=supplied_digest,
                                retry_existing=retry_existing,
                            )

                    self.assertEqual(current.readlink(), old)
                    self.assertTrue(incoming.is_dir())
                    self.assertEqual(orphan.exists(), retry_existing)
                    for name in ("systemctl", "run", "switch", "restart", "readiness", "backup", "units"):
                        calls[name].assert_not_called()

    def test_orphan_manifest_uses_canonical_path_and_content_checks(self):
        with tempfile.TemporaryDirectory() as temp:
            release = Path(temp)
            (release / "bin").mkdir()
            (release / "web/dist").mkdir(parents=True)
            (release / "bin/aicrm").write_text("app")
            (release / "web/dist/index.html").write_text("page")
            (release / "release.env").write_text(f"AICRM_RELEASE_SHA={'b' * 40}\n")
            manifest = release / "release-files.sha256"
            lines = [f"{installer.digest(path)}  {path.relative_to(release).as_posix()}\n" for path in sorted(release.rglob("*")) if path.is_file() and path.name != "release.env"]
            manifest.write_text("".join(lines))
            metadata = {"source_sha": "b" * 40, "release_files_sha256": installer.digest(manifest)}
            installer.verify_payload_without_release_env(release, metadata)

            first = lines[0]
            manifest.write_text(first + first + "".join(lines[1:]))
            metadata["release_files_sha256"] = installer.digest(manifest)
            with self.assertRaisesRegex(ValueError, "unsafe manifest path"):
                installer.verify_payload_without_release_env(release, metadata)

            manifest.write_text("0" * 64 + "  ../escape\n")
            metadata["release_files_sha256"] = installer.digest(manifest)
            with self.assertRaisesRegex(ValueError, "unsafe manifest path"):
                installer.verify_payload_without_release_env(release, metadata)

    def test_root_owned_release_guard_rejects_non_root_and_symlinks(self):
        with tempfile.TemporaryDirectory() as temp:
            release = Path(temp) / "release"
            (release / "bin").mkdir(parents=True)
            (release / "bin/app").write_text("app")
            if release.stat().st_uid != 0 or release.stat().st_gid != 0:
                with self.assertRaisesRegex(ValueError, "not root-owned"):
                    installer.verify_root_owned_release(release)
            (release / "linked").symlink_to(release / "bin/app")
            with self.assertRaisesRegex(ValueError, "linked or special"):
                installer.verify_root_owned_release(release)

    def test_writable_nested_directory_is_rejected_before_chmodding_release_root(self):
        with tempfile.TemporaryDirectory() as temp:
            release = Path(temp) / "release"
            nested = release / "nested"
            nested.mkdir(parents=True)
            release.chmod(0o700)
            nested.chmod(0o777)

            with self.assertRaisesRegex(ValueError, "group or other writable"):
                installer.verify_root_owned_release(release)
            self.assertEqual(release.stat().st_mode & 0o777, 0o700)

            with self.assertRaisesRegex(ValueError, "group or other writable"):
                installer.make_release_directories_traversable(release)
            self.assertEqual(release.stat().st_mode & 0o777, 0o700)

    def test_health_failure_switches_back(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            releases = root / "releases"
            releases.mkdir()
            old = releases / ("a" * 40)
            old.mkdir()
            (old / "release.env").write_text(f"AICRM_RELEASE_SHA={'a' * 40}\n")
            current = root / "current"
            current.symlink_to(old)
            incoming = root / "incoming"
            (incoming / "bin").mkdir(parents=True)
            (incoming / "web/dist").mkdir(parents=True)
            (incoming / "bin/aicrm").write_text("app")
            (incoming / "web/dist/index.html").write_text("page")
            lines = [f"{installer.digest(path)}  {path.relative_to(incoming).as_posix()}\n" for path in sorted(incoming.rglob("*")) if path.is_file()]
            manifest = incoming / "release-files.sha256"
            manifest.write_text("".join(lines))
            metadata = {"source_sha": "b" * 40, "source_tree": "c" * 40, "release_files_sha256": installer.digest(manifest), "migrations_changed": False}
            metadata_bytes, metadata_sha256 = encode_metadata(metadata)
            env = root / "runtime.env"
            env.write_text("AICRM_DATABASE_URL=synthetic\n")
            with mock.patch.object(installer, "ROOT", root), mock.patch.object(installer, "RELEASES", releases), mock.patch.object(installer, "CURRENT", current), mock.patch.object(installer, "LOCK", root / "install.lock"), mock.patch.object(installer, "RECEIPTS", root / "domestic-receipts"), mock.patch.object(installer, "ENV", env), mock.patch.object(installer, "require_host_role", return_value="production"), mock.patch.object(installer.shutil, "which", return_value="/bin/systemctl"), mock.patch.object(installer, "run") as system_run, mock.patch.object(installer, "backup_database") as backup, mock.patch.object(installer, "verify_root_owned_release"), mock.patch.object(installer, "restart_services"), mock.patch.object(installer, "readiness", side_effect=[RuntimeError("health failed"), None]):
                with self.assertRaisesRegex(RuntimeError, "health failed"):
                    installer.install(incoming, metadata_bytes, "a" * 40, expected_sha="b" * 40, metadata_sha256=metadata_sha256)
                backup.assert_not_called()
                self.assertFalse(any("aicrm-migrate.service" in call.args for call in system_run.call_args_list))
            self.assertEqual(current.resolve(), old.resolve())


if __name__ == "__main__":
    unittest.main()
