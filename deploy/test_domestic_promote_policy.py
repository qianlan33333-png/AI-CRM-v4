import hashlib
import importlib.util
import json
from contextlib import ExitStack
from pathlib import Path
import stat
import tempfile
import unittest
from types import SimpleNamespace
from unittest import mock


ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location("domestic_promote_policy", ROOT / "deploy/domestic-promote.py")
installer = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(installer)


def systemd_unit_output(env_file: Path) -> str:
    return (
        "User=aicrm\nGroup=aicrm\nWorkingDirectory=/opt/aicrm/current\n"
        f"EnvironmentFiles={env_file} (ignore_errors=no) "
        "/opt/aicrm/current/release.env (ignore_errors=yes)\n"
    )


def metadata_package(incoming: Path, sha: str, *, migration: bool) -> tuple[bytes, str]:
    (incoming / "bin").mkdir(parents=True)
    (incoming / "web/dist").mkdir(parents=True)
    (incoming / "bin/aicrm").write_bytes(b"test-aicrm")
    (incoming / "web/dist/index.html").write_text("synthetic")
    if migration:
        (incoming / "migrations").mkdir()
        (incoming / "migrations/0208_contract.sql").write_text("SELECT 1;\n")
    entries = []
    for path in sorted(item for item in incoming.rglob("*") if item.is_file()):
        entries.append(f"{installer.digest(path)}  {path.relative_to(incoming).as_posix()}\n")
    manifest = incoming / "release-files.sha256"
    manifest.write_text("".join(entries))
    value = {
        "source_sha": sha,
        "source_tree": "d" * 40,
        "release_files_sha256": installer.digest(manifest),
        "migrations_changed": migration,
        "changed_paths": ["migrations/0208_contract.sql"] if migration else ["web/src/page.tsx"],
    }
    content = json.dumps(value, sort_keys=True).encode()
    return content, hashlib.sha256(content).hexdigest()


class HostRoleTests(unittest.TestCase):
    def role_fixture(self, parent: Path, role: str) -> tuple[Path, Path]:
        parent.mkdir(mode=0o755, parents=True, exist_ok=True)
        role_file = parent / "domestic-release-role"
        role_file.write_text(role + "\n")
        role_file.chmod(0o644)
        return parent, role_file

    def protected_lstat(self, parent: Path, role_file: Path, *, directory_uid=0, directory_gid=0, role_uid=0, role_gid=0, role_mode=None):
        original = Path.lstat

        def fake(path):
            info = original(path)
            if path == parent:
                return SimpleNamespace(st_mode=info.st_mode, st_uid=directory_uid, st_gid=directory_gid)
            if path == role_file:
                mode = info.st_mode if role_mode is None else stat.S_IFREG | role_mode
                return SimpleNamespace(st_mode=mode, st_uid=role_uid, st_gid=role_gid)
            return info

        return mock.patch.object(Path, "lstat", autospec=True, side_effect=fake)

    def test_host_role_requires_exact_root_protected_value_and_optional_match(self):
        with tempfile.TemporaryDirectory() as temporary:
            parent, role_file = self.role_fixture(Path(temporary), "staging")
            with mock.patch.object(installer, "HOST_ROLE_DIRECTORY", parent), mock.patch.object(installer, "HOST_ROLE_FILE", role_file), mock.patch.object(installer, "actual_host_role", return_value="staging"), self.protected_lstat(parent, role_file):
                self.assertEqual(installer.require_host_role(), "staging")
                self.assertEqual(installer.require_host_role("staging"), "staging")
                with self.assertRaisesRegex(RuntimeError, "role mismatch"):
                    installer.require_host_role("production")

    def test_staging_marker_on_production_host_fails_closed(self):
        with tempfile.TemporaryDirectory() as temporary:
            parent, role_file = self.role_fixture(Path(temporary), "staging")
            with mock.patch.object(installer, "HOST_ROLE_DIRECTORY", parent), mock.patch.object(installer, "HOST_ROLE_FILE", role_file), mock.patch.object(installer, "actual_host_role", return_value="production"), self.protected_lstat(parent, role_file):
                with self.assertRaisesRegex(RuntimeError, "does not match the machine identity"):
                    installer.require_host_role()

    def test_host_role_fails_closed_for_missing_malformed_linked_or_writable_config(self):
        with tempfile.TemporaryDirectory() as temporary:
            parent = Path(temporary)
            role_file = parent / "domestic-release-role"
            parent.chmod(0o755)
            with mock.patch.object(installer, "HOST_ROLE_DIRECTORY", parent), mock.patch.object(installer, "HOST_ROLE_FILE", role_file), mock.patch.object(installer, "actual_host_role", return_value="staging"), self.protected_lstat(parent, role_file):
                with self.assertRaisesRegex(RuntimeError, "missing or unsafe"):
                    installer.require_host_role()
                role_file.write_text("stage\n")
                with self.assertRaisesRegex(RuntimeError, "exactly staging or production"):
                    installer.require_host_role()
                role_file.write_text("staging\nextra\n")
                with self.assertRaisesRegex(RuntimeError, "exactly staging or production"):
                    installer.require_host_role()
                role_file.write_text("production\n")
                role_file.chmod(0o666)
                with self.assertRaisesRegex(RuntimeError, "missing or unsafe"):
                    installer.require_host_role()
                role_file.chmod(0o644)
                role_file.unlink()
                role_file.symlink_to(parent / "missing")
                with self.assertRaisesRegex(RuntimeError, "missing or unsafe"):
                    installer.require_host_role()

    def test_role_directory_accepts_root_owned_protected_non_root_group(self):
        with tempfile.TemporaryDirectory() as temporary:
            parent, role_file = self.role_fixture(Path(temporary), "production")
            parent.chmod(0o750)
            with mock.patch.object(installer, "HOST_ROLE_DIRECTORY", parent), mock.patch.object(installer, "HOST_ROLE_FILE", role_file), mock.patch.object(installer, "actual_host_role", return_value="production"), self.protected_lstat(parent, role_file, directory_gid=1001):
                self.assertEqual(installer.require_host_role("production"), "production")

    def test_role_directory_rejects_non_root_owner_or_group_and_other_write(self):
        for owner, mode in ((1001, 0o750), (0, 0o770), (0, 0o752)):
            with self.subTest(owner=owner, mode=oct(mode)), tempfile.TemporaryDirectory() as temporary:
                parent, role_file = self.role_fixture(Path(temporary), "production")
                parent.chmod(mode)
                with mock.patch.object(installer, "HOST_ROLE_DIRECTORY", parent), mock.patch.object(installer, "HOST_ROLE_FILE", role_file), mock.patch.object(installer, "actual_host_role", return_value="production"), self.protected_lstat(parent, role_file, directory_uid=owner, directory_gid=1001):
                    with self.assertRaisesRegex(RuntimeError, "missing or unsafe"):
                        installer.require_host_role()

    def test_role_marker_must_remain_root_group_owned(self):
        with tempfile.TemporaryDirectory() as temporary:
            parent, role_file = self.role_fixture(Path(temporary), "production")
            with mock.patch.object(installer, "HOST_ROLE_DIRECTORY", parent), mock.patch.object(installer, "HOST_ROLE_FILE", role_file), mock.patch.object(installer, "actual_host_role", return_value="production"), self.protected_lstat(parent, role_file, role_gid=1001):
                with self.assertRaisesRegex(RuntimeError, "missing or unsafe"):
                    installer.require_host_role()

    def test_staging_marker_keeps_0644_compatibility_but_rejects_group_or_other_write(self):
        with tempfile.TemporaryDirectory() as temporary:
            parent, role_file = self.role_fixture(Path(temporary), "staging")
            with mock.patch.object(installer, "HOST_ROLE_DIRECTORY", parent), mock.patch.object(installer, "HOST_ROLE_FILE", role_file), mock.patch.object(installer, "actual_host_role", return_value="staging"), self.protected_lstat(parent, role_file, role_mode=0o644):
                self.assertEqual(installer.require_host_role(), "staging")
            for mode in (0o664, 0o646):
                with self.subTest(mode=oct(mode)), mock.patch.object(installer, "HOST_ROLE_DIRECTORY", parent), mock.patch.object(installer, "HOST_ROLE_FILE", role_file), mock.patch.object(installer, "actual_host_role", return_value="staging"), self.protected_lstat(parent, role_file, role_mode=mode):
                    with self.assertRaisesRegex(RuntimeError, "missing or unsafe"):
                        installer.require_host_role()


class HostEnvironmentContractTests(unittest.TestCase):
    def test_protected_environment_accepts_staging_and_production_permissions(self):
        original = Path.lstat
        for role, group, mode in (("staging", 1001, 0o640), ("production", 0, 0o600)):
            with self.subTest(role=role), tempfile.TemporaryDirectory() as temporary:
                path = Path(temporary) / "aicrm.env"
                path.write_text("synthetic only\n")

                def fake(candidate):
                    info = original(candidate)
                    if candidate == path:
                        return SimpleNamespace(st_mode=stat.S_IFREG | mode, st_uid=0, st_gid=group)
                    return info

                with mock.patch.object(Path, "lstat", autospec=True, side_effect=fake), mock.patch.object(installer.grp, "getgrnam", return_value=SimpleNamespace(gr_gid=1001)):
                    installer._require_protected_runtime_environment(path)

    def test_protected_environment_rejects_unreadable_writable_linked_or_non_root_file(self):
        original = Path.lstat
        cases = ((1001, 1001, 0o600, stat.S_IFREG), (0, 1001, 0o660, stat.S_IFREG),
                 (0, 0, 0o602, stat.S_IFREG), (0, 0, 0o200, stat.S_IFREG),
                 (0, 0, 0o600, stat.S_IFLNK), (0, 0, 0o600, stat.S_IFDIR),
                 (0, 1002, 0o640, stat.S_IFREG), (0, 1001, 0o604, stat.S_IFREG),
                 (0, 1001, 0o744, stat.S_IFREG))
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "aicrm.env"
            path.write_text("synthetic only\n")
            for owner, group, mode, file_type in cases:
                with self.subTest(owner=owner, group=group, mode=oct(mode), file_type=file_type):
                    def fake(candidate):
                        info = original(candidate)
                        if candidate == path:
                            return SimpleNamespace(st_mode=file_type | mode, st_uid=owner, st_gid=group)
                        return info

                    with mock.patch.object(Path, "lstat", autospec=True, side_effect=fake), mock.patch.object(installer.grp, "getgrnam", return_value=SimpleNamespace(gr_gid=1001)):
                        with self.assertRaisesRegex(RuntimeError, "missing or unsafe"):
                            installer._require_protected_runtime_environment(path)

    def test_systemd_requires_non_optional_runtime_environment_file(self):
        required = installer.ENV
        self.assertTrue(installer._has_required_systemd_environment_file(f"{required} (ignore_errors=no) /opt/aicrm/current/release.env (ignore_errors=yes)", required))
        self.assertFalse(installer._has_required_systemd_environment_file(f"{required} (ignore_errors=yes)", required))
        self.assertFalse(installer._has_required_systemd_environment_file("/etc/aicrm/other.env (ignore_errors=no)", required))


class InstallBackupPolicyTests(unittest.TestCase):
    def install_fixture(self, root: Path, *, role: str, migration: bool):
        releases = root / "releases"
        releases.mkdir()
        old_sha = "a" * 40
        new_sha = "b" * 40
        old = releases / old_sha
        old.mkdir()
        (old / "release.env").write_text(f"AICRM_RELEASE_SHA={old_sha}\n")
        current = root / "current"
        current.symlink_to(old)
        incoming = root / "domestic-incoming" / new_sha
        incoming.mkdir(parents=True)
        content, content_hash = metadata_package(incoming, new_sha, migration=migration)
        env = root / "aicrm.env"
        env.write_text("AICRM_DATABASE_URL=postgres://test:test@127.0.0.1/test\n")
        paths = {
            "ROOT": root,
            "RELEASES": releases,
            "CURRENT": current,
            "LOCK": root / "install-release.lock",
            "RECEIPTS": root / "receipts",
            "ENV": env,
        }
        return paths, incoming, old_sha, new_sha, content, content_hash

    def install(self, root: Path, role: str, migration: bool, *, run_side_effect=None, backup_side_effect=None):
        paths, incoming, old_sha, new_sha, content, content_hash = self.install_fixture(root, role=role, migration=migration)
        run_calls = []

        def fake_run(*args, **kwargs):
            run_calls.append(args)
            if run_side_effect is not None:
                return run_side_effect(args, kwargs)
            return SimpleNamespace(returncode=0, stdout="", stderr="")

        patches = [mock.patch.object(installer, key, value) for key, value in paths.items()]
        with ExitStack() as stack:
            for patcher in patches:
                stack.enter_context(patcher)
            stack.enter_context(mock.patch.object(installer, "require_host_role", return_value=role))
            stack.enter_context(mock.patch.object(installer.shutil, "which", return_value="/usr/bin/systemctl"))
            stack.enter_context(mock.patch.object(installer, "verify_root_owned_release"))
            stack.enter_context(mock.patch.object(installer, "switch_to"))
            stack.enter_context(mock.patch.object(installer, "restart_services"))
            stack.enter_context(mock.patch.object(installer, "readiness"))
            stack.enter_context(mock.patch.object(installer, "run", side_effect=fake_run))
            backup = stack.enter_context(mock.patch.object(installer, "backup_database", side_effect=backup_side_effect))
            outcome = installer.install(
                incoming,
                content,
                old_sha,
                expected_sha=new_sha,
                metadata_sha256=content_hash,
            )
            return outcome, backup, run_calls, paths, new_sha

    def test_staging_migration_never_dumps_database(self):
        with tempfile.TemporaryDirectory() as temporary:
            outcome, backup, run_calls, paths, new_sha = self.install(Path(temporary), "staging", True)
            backup.assert_not_called()
            self.assertIsNone(outcome["database_backup"])
            self.assertIn(("systemctl", "start", "aicrm-migrate.service"), run_calls)

    def test_production_migration_requires_backup_before_switch(self):
        with tempfile.TemporaryDirectory() as temporary:
            backup_path = Path(temporary) / "backup.dump"
            outcome, backup, _run_calls, _paths, new_sha = self.install(
                Path(temporary), "production", True, backup_side_effect=lambda _sha: backup_path
            )
            backup.assert_called_once_with(new_sha)
            self.assertEqual(outcome["database_backup"], str(backup_path))

    def test_page_or_program_change_never_dumps_or_runs_migrations_on_either_role(self):
        for role in ("staging", "production"):
            with self.subTest(role=role), tempfile.TemporaryDirectory() as temporary:
                outcome, backup, run_calls, _paths, _new_sha = self.install(Path(temporary), role, False)
                backup.assert_not_called()
                self.assertIsNone(outcome["database_backup"])
                self.assertNotIn(("systemctl", "start", "aicrm-migrate.service"), run_calls)

    def test_unclassified_changed_migration_sql_is_rejected_before_install(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            paths, incoming, old_sha, new_sha, content, content_hash = self.install_fixture(root, role="production", migration=True)
            metadata = json.loads(content)
            metadata["migrations_changed"] = False
            content = json.dumps(metadata, sort_keys=True).encode()
            content_hash = hashlib.sha256(content).hexdigest()
            with ExitStack() as stack:
                for key, value in paths.items():
                    stack.enter_context(mock.patch.object(installer, key, value))
                stack.enter_context(mock.patch.object(installer, "require_host_role", return_value="production"))
                stack.enter_context(mock.patch.object(installer.shutil, "which", return_value="/usr/bin/systemctl"))
                switch = stack.enter_context(mock.patch.object(installer, "switch_to"))
                with self.assertRaisesRegex(ValueError, "marks migrations_changed=false"):
                    installer.install(incoming, content, old_sha, expected_sha=new_sha, metadata_sha256=content_hash)
                switch.assert_not_called()

    def test_staging_migration_failure_stops_candidate_and_names_safe_synthetic_rebuild(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            paths, incoming, old_sha, new_sha, content, content_hash = self.install_fixture(root, role="staging", migration=True)

            def failed_migration(*args, **_kwargs):
                if args == ("systemctl", "start", "aicrm-migrate.service"):
                    raise RuntimeError("migration command failed")
                return SimpleNamespace(returncode=0, stdout="", stderr="")

            with ExitStack() as stack:
                for key, value in paths.items():
                    stack.enter_context(mock.patch.object(installer, key, value))
                stack.enter_context(mock.patch.object(installer, "require_host_role", return_value="staging"))
                stack.enter_context(mock.patch.object(installer.shutil, "which", return_value="/usr/bin/systemctl"))
                stack.enter_context(mock.patch.object(installer, "verify_root_owned_release"))
                switch = stack.enter_context(mock.patch.object(installer, "switch_to"))
                stack.enter_context(mock.patch.object(installer, "restart_services"))
                stack.enter_context(mock.patch.object(installer, "readiness"))
                stack.enter_context(mock.patch.object(installer, "run", side_effect=failed_migration))
                backup = stack.enter_context(mock.patch.object(installer, "backup_database"))
                with self.assertRaisesRegex(RuntimeError, "disposable synthetic database.*do not restore a dump"):
                    installer.install(incoming, content, old_sha, expected_sha=new_sha, metadata_sha256=content_hash)
                backup.assert_not_called()
                self.assertFalse((paths["RECEIPTS"] / f"{new_sha}.json").exists())
                self.assertEqual(switch.call_count, 2)  # attempted candidate switch, then prior runtime rollback

    def test_production_backup_failure_prevents_runtime_switch(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            paths, incoming, old_sha, new_sha, content, content_hash = self.install_fixture(root, role="production", migration=True)
            with ExitStack() as stack:
                for key, value in paths.items():
                    stack.enter_context(mock.patch.object(installer, key, value))
                stack.enter_context(mock.patch.object(installer, "require_host_role", return_value="production"))
                stack.enter_context(mock.patch.object(installer.shutil, "which", return_value="/usr/bin/systemctl"))
                stack.enter_context(mock.patch.object(installer, "verify_root_owned_release"))
                switch = stack.enter_context(mock.patch.object(installer, "switch_to"))
                stack.enter_context(mock.patch.object(installer, "run", return_value=SimpleNamespace(returncode=0, stdout="", stderr="")))
                stack.enter_context(mock.patch.object(installer, "backup_database", side_effect=RuntimeError("pg_dump failed")))
                with self.assertRaisesRegex(RuntimeError, "pg_dump failed"):
                    installer.install(incoming, content, old_sha, expected_sha=new_sha, metadata_sha256=content_hash)
                switch.assert_not_called()


class HostContractTests(unittest.TestCase):
    def test_host_role_examples_are_exact_values(self):
        self.assertEqual((ROOT / "deploy/domestic-release-role.staging.example").read_text(), "staging\n")
        self.assertEqual((ROOT / "deploy/domestic-release-role.production.example").read_text(), "production\n")

    def test_host_tool_lookup_uses_only_contract_path(self):
        with mock.patch.object(installer.shutil, "which", return_value="/usr/bin/psql") as which:
            self.assertEqual(installer._host_tool("psql"), "/usr/bin/psql")
        which.assert_called_once_with("psql", path="/usr/bin:/bin")

    def test_fixed_helper_rehearsal_requires_exact_package_digest(self):
        helper = Path(installer.__file__)
        expected = installer.digest(helper)
        real_lstat = Path.lstat

        def safe_lstat(path):
            if path == helper:
                return SimpleNamespace(st_mode=stat.S_IFREG | 0o755, st_uid=0, st_gid=0)
            if path == helper.parent:
                return SimpleNamespace(st_mode=stat.S_IFDIR | 0o700, st_uid=0, st_gid=0)
            return real_lstat(path)

        with mock.patch.object(Path, "lstat", autospec=True, side_effect=safe_lstat):
            self.assertEqual(installer.verify_helper_digest(expected), expected)
            with self.assertRaisesRegex(RuntimeError, "does not match the checked Git source file"):
                installer.verify_helper_digest("0" * 64)

        def untrusted_file_lstat(path):
            if path == helper:
                return SimpleNamespace(st_mode=stat.S_IFREG | 0o755, st_uid=501, st_gid=20)
            if path == helper.parent:
                return SimpleNamespace(st_mode=stat.S_IFDIR | 0o700, st_uid=0, st_gid=0)
            return real_lstat(path)

        with mock.patch.object(Path, "lstat", autospec=True, side_effect=untrusted_file_lstat):
            with self.assertRaisesRegex(RuntimeError, "root-owned file"):
                installer.verify_helper_digest(expected)

    def test_actual_host_role_binds_known_hostname_to_private_ip(self):
        def path_exists(path):
            return path == Path("/usr/sbin/ip")

        with mock.patch.object(installer.socket, "gethostname", return_value="VM-4-6-ubuntu"), mock.patch.object(Path, "exists", autospec=True, side_effect=path_exists), mock.patch.object(installer, "_require_host_tool") as tool_check, mock.patch.object(installer.subprocess, "run", return_value=SimpleNamespace(returncode=0, stdout="2: ens3 inet 10.0.4.6/24 scope global ens3\n", stderr="")) as probe:
            self.assertEqual(installer.actual_host_role(), "staging")
        tool_check.assert_called_once_with("/usr/sbin/ip", "ip")
        self.assertEqual(probe.call_args.args[0], ["/usr/sbin/ip", "-o", "-4", "addr", "show", "scope", "global"])

    def test_actual_host_role_rejects_unknown_hostname_or_wrong_private_ip(self):
        def path_exists(path):
            return path == Path("/usr/sbin/ip")

        with mock.patch.object(installer.socket, "gethostname", return_value="unknown-release-vm"), self.assertRaisesRegex(RuntimeError, "not approved"):
            installer.actual_host_role()
        with mock.patch.object(installer.socket, "gethostname", return_value="VM-4-6-ubuntu"), mock.patch.object(Path, "exists", autospec=True, side_effect=path_exists), mock.patch.object(installer, "_require_host_tool"), mock.patch.object(installer.subprocess, "run", return_value=SimpleNamespace(returncode=0, stdout="2: ens3 inet 10.0.4.13/24 scope global ens3\n", stderr="")):
            with self.assertRaisesRegex(RuntimeError, "does not match its approved role"):
                installer.actual_host_role()
        with mock.patch.object(installer.socket, "gethostname", return_value="VM-4-13-ubuntu"), mock.patch.object(Path, "exists", autospec=True, side_effect=path_exists), mock.patch.object(installer, "_require_host_tool"), mock.patch.object(installer.subprocess, "run", return_value=SimpleNamespace(returncode=0, stdout="2: ens3 inet 10.0.4.13/24 scope global ens3\n", stderr="")):
            self.assertEqual(installer.actual_host_role(), "production")

    def test_service_user_permission_probe_uses_fixed_runuser_path_and_exact_user(self):
        target = Path("/opt/aicrm/current/bin/aicrm")
        with mock.patch.object(installer.pwd, "getpwnam", return_value=SimpleNamespace(pw_dir="/var/lib/aicrm")), mock.patch.object(installer.subprocess, "run", return_value=SimpleNamespace(returncode=0, stdout="", stderr="")) as probe:
            installer._service_user_can("-x", target)
        args, kwargs = probe.call_args
        self.assertEqual(args[0], ["/usr/sbin/runuser", "--preserve-environment", "-u", "aicrm", "--", "/usr/bin/test", "-x", str(target)])
        self.assertEqual(kwargs["env"], {"HOME": "/var/lib/aicrm", "PATH": "/usr/bin:/bin"})

    def test_host_contract_uses_runuser_restricted_path_postgres16_and_real_systemd_user(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            env_file = root / "aicrm.env"
            env_file.write_text("AICRM_DATABASE_URL=postgres://aicrm_test:synthetic@127.0.0.1/aicrm_test?sslmode=disable\n")
            with ExitStack() as stack:
                stack.enter_context(mock.patch.object(installer, "require_host_role", return_value="staging"))
                stack.enter_context(mock.patch.object(installer, "ENV", env_file))
                stack.enter_context(mock.patch.object(installer, "CURRENT", Path("/opt/aicrm/current")))
                stack.enter_context(mock.patch.object(installer, "RUNUSER", "/usr/sbin/runuser"))
                check_env = stack.enter_context(mock.patch.object(installer, "_require_protected_runtime_environment"))
                check_runuser = stack.enter_context(mock.patch.object(installer, "_require_root_executable"))
                check_tool = stack.enter_context(mock.patch.object(installer, "_require_host_tool"))
                stack.enter_context(mock.patch.object(installer, "_host_tool", side_effect=lambda name: f"/usr/bin/{name}"))
                service_user = stack.enter_context(mock.patch.object(installer, "_service_user_can"))
                stack.enter_context(mock.patch.object(installer.pwd, "getpwnam", return_value=SimpleNamespace(pw_dir="/var/lib/aicrm")))
                systemctl = stack.enter_context(mock.patch.object(installer, "run", return_value=SimpleNamespace(stdout=systemd_unit_output(env_file))))
                psql = stack.enter_context(mock.patch.object(installer.subprocess, "run", return_value=SimpleNamespace(returncode=0, stdout='[160013,"aicrm_test","aicrm_test","127.0.0.1/32"]\n', stderr="")))
                result = installer.check_host_contract()
            check_runuser.assert_called_once_with("/usr/sbin/runuser", "runuser")
            check_env.assert_called_once_with(env_file)
            self.assertEqual([call.args for call in check_tool.call_args_list], [("/usr/bin/psql", "psql"), ("/usr/bin/systemctl", "systemctl")])
            self.assertEqual(service_user.call_count, 2)
            self.assertEqual([call.args for call in service_user.call_args_list], [
                ("-x", Path("/opt/aicrm/current")),
                ("-x", Path("/opt/aicrm/current/bin/aicrm")),
            ])
            self.assertEqual(systemctl.call_count, 3)
            self.assertTrue(all("EnvironmentFiles" in call.args for call in systemctl.call_args_list))
            args, kwargs = psql.call_args
            self.assertEqual(args[0][0], "/usr/sbin/runuser")
            self.assertIn("-u", args[0])
            self.assertIn("aicrm", args[0])
            self.assertEqual(kwargs["env"]["PATH"], "/usr/bin:/bin")
            self.assertEqual(kwargs["env"]["PGHOST"], "127.0.0.1")
            self.assertIn("default_transaction_read_only=on", kwargs["env"]["PGOPTIONS"])
            self.assertNotIn("synthetic", repr(args[0]))
            self.assertEqual(result, {"host_role": "staging", "postgres_major": 16, "database_connection": "verified", "systemd_services": 3})

    def test_staging_host_contract_rejects_non_loopback_database_address(self):
        with tempfile.TemporaryDirectory() as temporary:
            env_file = Path(temporary) / "aicrm.env"
            env_file.write_text("AICRM_DATABASE_URL=postgres://aicrm_test:synthetic@127.0.0.1/aicrm_test\n")
            with ExitStack() as stack:
                stack.enter_context(mock.patch.object(installer, "require_host_role", return_value="staging"))
                stack.enter_context(mock.patch.object(installer, "ENV", env_file))
                stack.enter_context(mock.patch.object(installer, "CURRENT", Path("/opt/aicrm/current")))
                stack.enter_context(mock.patch.object(installer, "RUNUSER", "/usr/sbin/runuser"))
                stack.enter_context(mock.patch.object(installer, "_require_protected_runtime_environment"))
                stack.enter_context(mock.patch.object(installer, "_require_root_executable"))
                stack.enter_context(mock.patch.object(installer, "_require_host_tool"))
                stack.enter_context(mock.patch.object(installer, "_host_tool", side_effect=lambda name: f"/usr/bin/{name}"))
                stack.enter_context(mock.patch.object(installer, "_service_user_can"))
                stack.enter_context(mock.patch.object(installer.pwd, "getpwnam", return_value=SimpleNamespace(pw_dir="/var/lib/aicrm")))
                stack.enter_context(mock.patch.object(installer, "run", return_value=SimpleNamespace(stdout=systemd_unit_output(env_file))))
                stack.enter_context(mock.patch.object(installer.subprocess, "run", return_value=SimpleNamespace(returncode=0, stdout='[160013,"aicrm_test","aicrm_test","10.0.4.6/32"]\n', stderr="")))
                with self.assertRaisesRegex(RuntimeError, "identity or version mismatch"):
                    installer.check_host_contract()

    def test_staging_host_contract_accepts_postgres_loopback_cidr_ipv6(self):
        with tempfile.TemporaryDirectory() as temporary:
            env_file = Path(temporary) / "aicrm.env"
            env_file.write_text("AICRM_DATABASE_URL=postgres://aicrm_test:synthetic@127.0.0.1/aicrm_test\n")
            with ExitStack() as stack:
                stack.enter_context(mock.patch.object(installer, "require_host_role", return_value="staging"))
                stack.enter_context(mock.patch.object(installer, "ENV", env_file))
                stack.enter_context(mock.patch.object(installer, "CURRENT", Path("/opt/aicrm/current")))
                stack.enter_context(mock.patch.object(installer, "RUNUSER", "/usr/sbin/runuser"))
                stack.enter_context(mock.patch.object(installer, "_require_protected_runtime_environment"))
                stack.enter_context(mock.patch.object(installer, "_require_root_executable"))
                stack.enter_context(mock.patch.object(installer, "_require_host_tool"))
                stack.enter_context(mock.patch.object(installer, "_host_tool", side_effect=lambda name: f"/usr/bin/{name}"))
                stack.enter_context(mock.patch.object(installer, "_service_user_can"))
                stack.enter_context(mock.patch.object(installer.pwd, "getpwnam", return_value=SimpleNamespace(pw_dir="/var/lib/aicrm")))
                stack.enter_context(mock.patch.object(installer, "run", return_value=SimpleNamespace(stdout=systemd_unit_output(env_file))))
                stack.enter_context(mock.patch.object(installer.subprocess, "run", return_value=SimpleNamespace(returncode=0, stdout='[160013,"aicrm_test","aicrm_test","::1/128"]\n', stderr="")))
                result = installer.check_host_contract()
            self.assertEqual(result["postgres_major"], 16)

    def test_staging_host_contract_rejects_non_postgres16_server(self):
        with tempfile.TemporaryDirectory() as temporary:
            env_file = Path(temporary) / "aicrm.env"
            env_file.write_text("AICRM_DATABASE_URL=postgres://aicrm_test:synthetic@127.0.0.1/aicrm_test\n")
            with ExitStack() as stack:
                stack.enter_context(mock.patch.object(installer, "require_host_role", return_value="staging"))
                stack.enter_context(mock.patch.object(installer, "ENV", env_file))
                stack.enter_context(mock.patch.object(installer, "CURRENT", Path("/opt/aicrm/current")))
                stack.enter_context(mock.patch.object(installer, "RUNUSER", "/usr/sbin/runuser"))
                stack.enter_context(mock.patch.object(installer, "_require_protected_runtime_environment"))
                stack.enter_context(mock.patch.object(installer, "_require_root_executable"))
                stack.enter_context(mock.patch.object(installer, "_require_host_tool"))
                stack.enter_context(mock.patch.object(installer, "_host_tool", side_effect=lambda name: f"/usr/bin/{name}"))
                stack.enter_context(mock.patch.object(installer, "_service_user_can"))
                stack.enter_context(mock.patch.object(installer.pwd, "getpwnam", return_value=SimpleNamespace(pw_dir="/var/lib/aicrm")))
                stack.enter_context(mock.patch.object(installer, "run", return_value=SimpleNamespace(stdout=systemd_unit_output(env_file))))
                stack.enter_context(mock.patch.object(installer.subprocess, "run", return_value=SimpleNamespace(returncode=0, stdout='[150008,"aicrm_test","aicrm_test","127.0.0.1/32"]\n', stderr="")))
                with self.assertRaisesRegex(RuntimeError, "identity or version mismatch"):
                    installer.check_host_contract()

    def test_production_host_contract_requires_backup_tools_on_restricted_path(self):
        with tempfile.TemporaryDirectory() as temporary:
            env_file = Path(temporary) / "aicrm.env"
            env_file.write_text("AICRM_DATABASE_URL=postgres://aicrm:synthetic@127.0.0.1/aicrm\n")
            names = []
            with ExitStack() as stack:
                stack.enter_context(mock.patch.object(installer, "require_host_role", return_value="production"))
                stack.enter_context(mock.patch.object(installer, "ENV", env_file))
                stack.enter_context(mock.patch.object(installer, "CURRENT", Path("/opt/aicrm/current")))
                stack.enter_context(mock.patch.object(installer, "RUNUSER", "/usr/sbin/runuser"))
                stack.enter_context(mock.patch.object(installer, "_require_protected_runtime_environment"))
                stack.enter_context(mock.patch.object(installer, "_require_root_executable"))
                check_tool = stack.enter_context(mock.patch.object(installer, "_require_host_tool"))
                stack.enter_context(mock.patch.object(installer, "_host_tool", side_effect=lambda name: names.append(name) or f"/usr/bin/{name}"))
                stack.enter_context(mock.patch.object(installer, "_service_user_can"))
                stack.enter_context(mock.patch.object(installer.pwd, "getpwnam", return_value=SimpleNamespace(pw_dir="/var/lib/aicrm")))
                stack.enter_context(mock.patch.object(installer, "run", return_value=SimpleNamespace(stdout=systemd_unit_output(env_file))))
                stack.enter_context(mock.patch.object(installer.subprocess, "run", return_value=SimpleNamespace(returncode=0, stdout='[160013,"aicrm","aicrm","10.0.4.13/32"]\n', stderr="")))
                installer.check_host_contract()
            self.assertEqual(names, ["psql", "systemctl", "pg_dump", "pg_restore"])
            self.assertEqual([call.args[1] for call in check_tool.call_args_list], ["psql", "systemctl", "pg_dump", "pg_restore"])


if __name__ == "__main__":
    unittest.main()
