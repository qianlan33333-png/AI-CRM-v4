from __future__ import annotations

from contextlib import contextmanager, nullcontext, redirect_stdout
import io
import json
import os
from pathlib import Path
import stat
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parent))
import domestic_main_release as release


def git(repo: Path, *args: str) -> str:
    return subprocess.check_output(["git", f"--git-dir={repo}", *args], text=True).strip()


def make_repository(root: Path) -> tuple[Path, str, str, str]:
    source = root / "source"
    source.mkdir()
    subprocess.run(["git", "init", str(source)], check=True, stdout=subprocess.DEVNULL)
    subprocess.run(["git", "-C", str(source), "config", "user.name", "Test"], check=True)
    subprocess.run(["git", "-C", str(source), "config", "user.email", "test@example.invalid"], check=True)
    (source / "main.txt").write_text("main\n")
    subprocess.run(["git", "-C", str(source), "add", "main.txt"], check=True)
    subprocess.run(["git", "-C", str(source), "commit", "-m", "main"], check=True, stdout=subprocess.DEVNULL)
    base = subprocess.check_output(["git", "-C", str(source), "rev-parse", "HEAD"], text=True).strip()
    subprocess.run(["git", "-C", str(source), "checkout", "-b", "codex/one"], check=True, stdout=subprocess.DEVNULL)
    (source / "one.txt").write_text("one\n")
    subprocess.run(["git", "-C", str(source), "add", "one.txt"], check=True)
    subprocess.run(["git", "-C", str(source), "commit", "-m", "one"], check=True, stdout=subprocess.DEVNULL)
    candidate = subprocess.check_output(["git", "-C", str(source), "rev-parse", "HEAD"], text=True).strip()
    subprocess.run(["git", "-C", str(source), "checkout", "-b", "codex/two", base], check=True, stdout=subprocess.DEVNULL)
    (source / "two.txt").write_text("two\n")
    subprocess.run(["git", "-C", str(source), "add", "two.txt"], check=True)
    subprocess.run(["git", "-C", str(source), "commit", "-m", "two"], check=True, stdout=subprocess.DEVNULL)
    other = subprocess.check_output(["git", "-C", str(source), "rev-parse", "HEAD"], text=True).strip()
    bare = root / "domestic.git"
    subprocess.run(["git", "init", "--bare", str(bare)], check=True, stdout=subprocess.DEVNULL)
    subprocess.run(["git", f"--git-dir={bare}", "fetch", "--no-tags", str(source), f"{base}:refs/heads/main", f"{candidate}:refs/heads/codex/one", f"{other}:refs/heads/codex/two"], check=True, stdout=subprocess.DEVNULL)
    subprocess.run(["git", f"--git-dir={bare}", "symbolic-ref", "HEAD", "refs/heads/main"], check=True)
    return bare, base, candidate, other


def stdin_with_bytes(payload: bytes) -> mock.Mock:
    stream = mock.Mock()
    stream.buffer = io.BytesIO(payload)
    return stream


@contextmanager
def recovery_push_identity(*, group_gid: int = 12345, user_uid: int = 12346,
                           user_primary_gid: int = 12345,
                           root_group_members: tuple[str, ...] = ()):
    group = mock.Mock(gr_gid=group_gid)
    user = mock.Mock(pw_uid=user_uid, pw_gid=user_primary_gid)
    root_group = mock.Mock(gr_mem=list(root_group_members))
    with mock.patch.object(release.grp, "getgrnam", return_value=group), \
         mock.patch.object(release.pwd, "getpwnam", return_value=user), \
         mock.patch.object(release.grp, "getgrgid", return_value=root_group):
        yield group


class DomesticMainReleaseTests(unittest.TestCase):
    def test_config_state_path_matches_systemd_condition(self) -> None:
        config = {
            "repo": release.DEFAULT_REPO,
            "state": release.DEFAULT_STATE,
            "lock": "/opt/aicrm/domestic/control/controller.lock",
            "work_root": "/opt/aicrm/domestic/control/work",
            "source_worktree": "/opt/aicrm/domestic/control/work/candidate",
            "stage_incoming": "/opt/aicrm/domestic-incoming",
            "stage_helper": "/usr/local/libexec/aicrm/domestic-promote.py",
            "prod_host": "10.0.4.13",
            "prod_user": "ubuntu",
            "prod_key": "/home/ubuntu/.ssh/ai-crm-v4-prod-deploy",
            "prod_known_hosts": "/home/ubuntu/.ssh/known_hosts_aicrm_prod",
            "prod_incoming": "/opt/aicrm/domestic-incoming",
            "prod_helper": "/usr/local/libexec/aicrm/domestic-promote.py",
            "push_user": "aicrm-release-push",
            "push_group": "aicrm-release-push",
            "controller_path": release.DEFAULT_CONTROLLER,
            "config_path": release.DEFAULT_CONFIG,
            "production_enabled": False,
        }
        self.assertIs(release._check_config(config), config)

        config["state"] = "/opt/aicrm/domestic/control/alternate-state.json"
        with self.assertRaisesRegex(ValueError, "state path must match the systemd unit contract"):
            release._check_config(config)

    def test_bare_objects_are_readable_but_not_writable_without_push_group(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            repo, _base, _candidate, _other = make_repository(Path(temporary))
            with mock.patch.object(release.os, "geteuid", return_value=0), \
                 mock.patch.object(release.os, "chown"):
                release._secure_bare_repository_permissions(repo, 12345)
            for path in (repo, repo / "objects", repo / "refs/heads/codex"):
                mode = path.stat().st_mode
                self.assertTrue(mode & 0o005, path)
                self.assertFalse(mode & 0o002, path)
            self.assertTrue((repo / "objects").stat().st_mode & 0o020)
            self.assertTrue((repo / "refs/heads/codex").stat().st_mode & 0o020)

    def test_fixed_bare_repository_requires_root_owned_non_writable_parent(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            parent = Path(temporary)
            repo = parent / "source.git"
            parent.chmod(0o775)
            with self.assertRaisesRegex(release.ReleaseError, "not be group/world writable"):
                release._verify_bare_repository_parent(repo, require_root_owner=False)
            parent.chmod(0o755)
            if os.geteuid() != 0:
                with self.assertRaisesRegex(release.ReleaseError, "must be root-owned"):
                    release._verify_bare_repository_parent(repo, require_root_owner=True)

    def test_bootstrap_tightens_shared_git_hooks_before_safe_directory_check(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            seed_repo, base, _candidate, _other = make_repository(root)
            destination = root / "bootstrapped.git"
            original_run = release._run
            original_safe_directory = release._safe_directory
            init_modes: list[int] = []
            checked_modes: list[int] = []

            def run_and_capture_shared_hooks(args, **kwargs):
                result = original_run(args, **kwargs)
                if args[:3] == ["git", "init", "--bare"]:
                    init_modes.append(stat.S_IMODE((destination / "hooks").lstat().st_mode))
                return result

            def safe_after_tightening(path, **kwargs):
                if path == destination / "hooks":
                    checked_modes.append(stat.S_IMODE(path.lstat().st_mode))
                return original_safe_directory(path, **kwargs)

            with mock.patch.object(release, "_run", side_effect=run_and_capture_shared_hooks), \
                 mock.patch.object(release, "_safe_directory", side_effect=safe_after_tightening), \
                 mock.patch.object(release, "_secure_bare_repository_permissions"):
                result = release.bootstrap_bare_repository(
                    destination, seed_repo, base,
                    controller_path="/usr/local/libexec/aicrm/domestic_main_release.py",
                    push_group=release.grp.getgrgid(os.getegid()).gr_name,
                )

            self.assertEqual(len(init_modes), 1)
            self.assertTrue(init_modes[0] & 0o020, oct(init_modes[0]))
            self.assertEqual(len(checked_modes), 1)
            self.assertFalse(checked_modes[0] & 0o022, oct(checked_modes[0]))
            self.assertEqual(result["main_sha"], base)
            self.assertTrue((destination / "hooks/pre-receive").is_file())

    def test_new_hooks_directory_refuses_symlink_or_wrong_owner_before_chmod(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            target = root / "target"
            target.mkdir(mode=0o2775)
            linked = root / "hooks-link"
            linked.symlink_to(target, target_is_directory=True)
            target_mode = stat.S_IMODE(target.lstat().st_mode)
            with self.assertRaisesRegex(release.ReleaseError, "unsafe type or owner"):
                release._prepare_new_bare_hooks_directory(linked)
            self.assertEqual(stat.S_IMODE(target.lstat().st_mode), target_mode)

            hooks = root / "hooks"
            hooks.mkdir(mode=0o2775)
            hooks_mode = stat.S_IMODE(hooks.lstat().st_mode)
            owner = hooks.lstat().st_uid
            with mock.patch.object(release.os, "geteuid", return_value=owner + 1):
                with self.assertRaisesRegex(release.ReleaseError, "unsafe type or owner"):
                    release._prepare_new_bare_hooks_directory(hooks)
            self.assertEqual(stat.S_IMODE(hooks.lstat().st_mode), hooks_mode)

    def test_recover_partial_bootstrap_repairs_exact_shared_init_without_advancing_main(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            seed_repo, base, _candidate, _other = make_repository(root)
            repo = root / "partial.git"
            subprocess.run(["git", "init", "--bare", "--shared=group", str(repo)], check=True,
                           stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            subprocess.run(["git", f"--git-dir={repo}", "config", "receive.denyDeletes", "true"], check=True)
            subprocess.run(["git", f"--git-dir={repo}", "config", "receive.denyNonFastForwards", "true"], check=True)
            subprocess.run(["git", f"--git-dir={repo}", "config", "core.logAllRefUpdates", "true"], check=True)
            subprocess.run(["git", f"--git-dir={repo}", "fetch", "--no-tags", str(seed_repo),
                            f"{base}:refs/heads/main"], check=True, stdout=subprocess.DEVNULL)
            subprocess.run(["git", f"--git-dir={repo}", "symbolic-ref", "HEAD", "refs/heads/main"], check=True)
            control = root / "control"
            control.mkdir(mode=0o700)
            state_path = control / "state.json"
            lock_path = control / "controller.lock"
            config = {"repo": str(repo), "state": str(state_path), "lock": str(lock_path),
                      "controller_path": "/usr/local/libexec/aicrm/domestic_main_release.py",
                      "push_user": "release-push-test", "push_group": "release-push-group-test"}
            tree = release._tree(repo, base)
            verified_calls: list[dict[str, str]] = []

            def verify_exact(candidate_repo, *, controller_path, push_group):
                hook = candidate_repo / "hooks/pre-receive"
                self.assertTrue(hook.is_file())
                self.assertTrue(os.access(hook, os.X_OK))
                self.assertIn(controller_path, hook.read_text(encoding="utf-8"))
                main_sha = release._resolve_ref(candidate_repo, release.MAIN_REF)
                result = {"main_sha": main_sha, "main_tree": release._tree(candidate_repo, main_sha)}
                verified_calls.append(result)
                return result

            lock_path.write_text("", encoding="utf-8")
            lock_path.chmod(0o600)
            with recovery_push_identity() as push_group, \
                 mock.patch.object(release, "_check_config", return_value=config), \
                 mock.patch.object(release, "DEFAULT_LOCK", str(lock_path)), \
                 mock.patch.object(release, "_secure_bare_repository_permissions") as secure, \
                 mock.patch.object(release, "verify_bare_repository", side_effect=verify_exact):
                with mock.patch.object(release, "_install_new_pre_receive_hook",
                                       side_effect=release.ReleaseError("simulated interruption")):
                    with self.assertRaisesRegex(release.ReleaseError, "simulated interruption"):
                        release.recover_partial_bootstrap(config, base, tree, base)
                self.assertTrue(lock_path.is_file())
                self.assertEqual(stat.S_IMODE(lock_path.stat().st_mode), 0o600)
                result = release.recover_partial_bootstrap(config, base, tree, base)

            self.assertEqual(result["status"], "partial_bootstrap_recovered")
            self.assertEqual(result["main_sha"], base)
            self.assertEqual(result["main_tree"], tree)
            self.assertFalse(result["ledger_created"])
            self.assertEqual(git(repo, "for-each-ref", "--format=%(refname)"), release.MAIN_REF)
            self.assertFalse(state_path.exists())
            self.assertEqual(len(verified_calls), 1)
            secure.assert_called_once_with(repo, push_group.gr_gid)
            with self.assertRaisesRegex(release.ReleaseError, "receive hook to be absent"):
                with recovery_push_identity(), \
                     mock.patch.object(release, "_check_config", return_value=config), \
                     mock.patch.object(release, "DEFAULT_LOCK", str(lock_path)):
                    release.recover_partial_bootstrap(config, base, tree, base)

    def test_recover_partial_bootstrap_rejects_unreviewed_state_before_writing_hook(self) -> None:
        for mutation in ("wrong_sha", "wrong_tree", "wrong_app", "extra_ref", "existing_state",
                         "world_writable", "unsafe_lock", "busy_lock", "root_push_group", "root_push_user",
                         "primary_root_group", "supplemental_root_group"):
            with self.subTest(mutation=mutation), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                seed_repo, base, candidate, _other = make_repository(root)
                repo = root / "partial.git"
                subprocess.run(["git", "init", "--bare", "--shared=group", str(repo)], check=True,
                               stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
                for key, value in (("receive.denyDeletes", "true"),
                                   ("receive.denyNonFastForwards", "true"),
                                   ("core.logAllRefUpdates", "true")):
                    subprocess.run(["git", f"--git-dir={repo}", "config", key, value], check=True)
                subprocess.run(["git", f"--git-dir={repo}", "fetch", "--no-tags", str(seed_repo),
                                f"{base}:refs/heads/main"], check=True, stdout=subprocess.DEVNULL)
                subprocess.run(["git", f"--git-dir={repo}", "symbolic-ref", "HEAD", "refs/heads/main"], check=True)
                control = root / "control"
                control.mkdir(mode=0o700)
                state_path, lock_path = control / "state.json", control / "controller.lock"
                config = {"repo": str(repo), "state": str(state_path), "lock": str(lock_path),
                          "controller_path": "/usr/local/libexec/aicrm/domestic_main_release.py",
                          "push_user": "release-push-test", "push_group": "release-push-group-test"}
                expected_tree = release._tree(repo, base)
                if mutation == "extra_ref":
                    subprocess.run(["git", f"--git-dir={repo}", "fetch", "--no-tags", str(seed_repo),
                                    f"{candidate}:refs/heads/codex/unexpected"], check=True,
                                   stdout=subprocess.DEVNULL)
                if mutation == "existing_state":
                    state_path.write_text("{}\n", encoding="utf-8")
                if mutation == "world_writable":
                    (repo / "config").chmod(0o666)
                if mutation == "unsafe_lock":
                    lock_path.write_text("", encoding="utf-8")
                    lock_path.chmod(0o644)
                identity = {
                    "root_push_group": {"group_gid": 0},
                    "root_push_user": {"user_uid": 0},
                    "primary_root_group": {"user_primary_gid": 0},
                    "supplemental_root_group": {"root_group_members": ("release-push-test",)},
                }.get(mutation, {})
                with recovery_push_identity(**identity), \
                     mock.patch.object(release, "_check_config", return_value=config), \
                     mock.patch.object(release, "DEFAULT_LOCK", str(lock_path)), \
                     mock.patch.object(release, "_secure_bare_repository_permissions") as secure:
                    sha = "f" * 40 if mutation == "wrong_sha" else base
                    tree = "e" * 40 if mutation == "wrong_tree" else expected_tree
                    app_sha = "d" * 40 if mutation == "wrong_app" else base
                    if mutation == "busy_lock":
                        with release._locked(lock_path):
                            with self.assertRaisesRegex(release.ReleaseError, "serial lock"):
                                release.recover_partial_bootstrap(config, sha, tree, app_sha)
                    else:
                        with self.assertRaises(release.ReleaseError):
                            release.recover_partial_bootstrap(config, sha, tree, app_sha)
                    secure.assert_not_called()
                self.assertFalse((repo / "hooks/pre-receive").exists())
                if mutation not in {"unsafe_lock", "busy_lock"}:
                    self.assertFalse(lock_path.exists())

    def test_new_baseline_allows_merges_only_when_installed_app_is_on_first_parent(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source = root / "source"
            source.mkdir()
            subprocess.run(["git", "init", str(source)], check=True, stdout=subprocess.DEVNULL)
            subprocess.run(["git", "-C", str(source), "config", "user.name", "Test"], check=True)
            subprocess.run(["git", "-C", str(source), "config", "user.email", "test@example.invalid"], check=True)
            (source / "README.md").write_text("base\n", encoding="utf-8")
            subprocess.run(["git", "-C", str(source), "add", "README.md"], check=True)
            subprocess.run(["git", "-C", str(source), "commit", "-m", "root"], check=True,
                           stdout=subprocess.DEVNULL)
            common = subprocess.check_output(["git", "-C", str(source), "rev-parse", "HEAD"], text=True).strip()
            subprocess.run(["git", "-C", str(source), "checkout", "-b", "installed-app"], check=True,
                           stdout=subprocess.DEVNULL)
            (source / "app.go").write_text("package app\n", encoding="utf-8")
            subprocess.run(["git", "-C", str(source), "add", "app.go"], check=True)
            subprocess.run(["git", "-C", str(source), "commit", "-m", "installed app"], check=True,
                           stdout=subprocess.DEVNULL)
            app_sha = subprocess.check_output(["git", "-C", str(source), "rev-parse", "HEAD"], text=True).strip()
            subprocess.run(["git", "-C", str(source), "checkout", "-b", "domestic-main", common], check=True,
                           stdout=subprocess.DEVNULL)
            (source / "CUTOVER.md").write_text("source-only merge\n", encoding="utf-8")
            subprocess.run(["git", "-C", str(source), "add", "CUTOVER.md"], check=True)
            subprocess.run(["git", "-C", str(source), "commit", "-m", "source-only"], check=True,
                           stdout=subprocess.DEVNULL)
            subprocess.run(["git", "-C", str(source), "merge", "--no-ff", "installed-app", "-m", "merge app"],
                           check=True, stdout=subprocess.DEVNULL)
            main_sha = subprocess.check_output(["git", "-C", str(source), "rev-parse", "HEAD"], text=True).strip()
            repo = root / "bare.git"
            subprocess.run(["git", "init", "--bare", str(repo)], check=True, stdout=subprocess.DEVNULL)
            subprocess.run(["git", f"--git-dir={repo}", "fetch", "--no-tags", str(source),
                            f"{main_sha}:refs/heads/main"], check=True, stdout=subprocess.DEVNULL)
            app = {"sha": app_sha, "tree": release._tree(repo, app_sha), "manifest_sha256": "a" * 64}
            self.assertNotIn(app_sha, git(repo, "rev-list", "--first-parent", main_sha).splitlines())
            with mock.patch.object(release.builder, "classify", return_value={"runtime_changed": False}):
                with self.assertRaisesRegex(release.ReleaseError, "first-parent chain"):
                    release._validate_baseline_identity(repo, main_sha, release._tree(repo, main_sha), app)

            subprocess.run(["git", "-C", str(source), "checkout", "-b", "domestic-main-first-parent", app_sha],
                           check=True, stdout=subprocess.DEVNULL)
            (source / "CUTOVER.md").write_text("source-only on first parent\n", encoding="utf-8")
            subprocess.run(["git", "-C", str(source), "add", "CUTOVER.md"], check=True)
            subprocess.run(["git", "-C", str(source), "commit", "-m", "first-parent source-only"], check=True,
                           stdout=subprocess.DEVNULL)
            first_parent_main = subprocess.check_output(["git", "-C", str(source), "rev-parse", "HEAD"], text=True).strip()
            first_parent_repo = root / "first-parent.git"
            subprocess.run(["git", "init", "--bare", str(first_parent_repo)], check=True,
                           stdout=subprocess.DEVNULL)
            subprocess.run(["git", f"--git-dir={first_parent_repo}", "fetch", "--no-tags", str(source),
                            f"{first_parent_main}:refs/heads/main"], check=True, stdout=subprocess.DEVNULL)
            with mock.patch.object(release.builder, "classify", return_value={"runtime_changed": False}):
                release._validate_baseline_identity(first_parent_repo, first_parent_main,
                                                    release._tree(first_parent_repo, first_parent_main), app)

    def test_controller_maintenance_marker_binds_base_candidate_and_all_fixed_bytes(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            repo, base, candidate, _other = make_repository(root)
            source_blobs = {path: f"fixed:{path}".encode() for path in release.builder.FIXED_CONTROLLER_FILES}
            fixed_hashes = {path: release.hashlib.sha256(blob).hexdigest()
                            for path, blob in source_blobs.items()}
            marker = {"candidate_sha": candidate, "candidate_tree": release._tree(repo, candidate),
                      "base_sha": base, "controller_files": ["scripts/domestic_main_release.py"],
                      "fixed_file_sha256": fixed_hashes, "check_receipt_sha256": "a" * 64,
                      "checked_at_utc": release._utc_now()}
            state = release._new_state(base, release._tree(repo, base),
                                       {"sha": base, "tree": release._tree(repo, base),
                                        "manifest_sha256": "b" * 64})
            state["queue"] = [{"candidate_id": candidate, "ref": "refs/heads/codex/one",
                               "head_sha": candidate, "base_sha": base, "status": "pending"}]
            state["controller_maintenance"] = marker
            config = {"controller_path": release.DEFAULT_CONTROLLER}
            classification = {"runtime_changed": False,
                              "controller_files": ["scripts/domestic_main_release.py"]}
            with mock.patch.object(release.builder, "classify", return_value=classification), \
                 mock.patch.object(release, "_source_blob", side_effect=lambda _repo, _sha, path: source_blobs[path]), \
                 mock.patch.object(release, "_verify_controller_files") as verify_files:
                release._validate_state(state)
                self.assertEqual(release._verify_controller_maintenance_candidate(config, repo, state), marker)
                verify_files.assert_called_once_with(config, repo, candidate,
                                                     sorted(release.builder.FIXED_CONTROLLER_FILES))

                missing = dict(state, controller_maintenance=None)
                with self.assertRaises(release.ControllerMaintenanceRequired):
                    release._verify_controller_maintenance_candidate(config, repo, missing)

                old_base = json.loads(json.dumps(state))
                old_base["controller_maintenance"]["base_sha"] = "f" * 40
                with self.assertRaisesRegex(release.ReleaseError, "marker base"):
                    release._validate_state(old_base)

                other_candidate = json.loads(json.dumps(state))
                other_candidate["controller_maintenance"]["candidate_sha"] = "e" * 40
                with self.assertRaisesRegex(release.ReleaseError, "queue front"):
                    release._validate_state(other_candidate)

                changed_hashes = json.loads(json.dumps(state))
                changed_hashes["controller_maintenance"]["fixed_file_sha256"]["scripts/domestic_main_release.py"] = "c" * 64
                with self.assertRaisesRegex(release.ControllerMaintenanceRequired, "hashes differ"):
                    release._verify_controller_maintenance_candidate(config, repo, changed_hashes)

    def test_poll_consumes_maintenance_marker_before_normal_candidate_cas(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            repo, base, candidate, _other = make_repository(root)
            state_path = root / "state.json"
            lock_path = root / "controller.lock"
            tree = release._tree(repo, base)
            state = release._new_state(base, tree,
                                       {"sha": base, "tree": tree, "manifest_sha256": "b" * 64})
            state["queue"] = [{"candidate_id": candidate, "ref": "refs/heads/codex/one",
                               "head_sha": candidate, "base_sha": base, "status": "pending"}]
            state["controller_maintenance"] = {
                "candidate_sha": candidate, "candidate_tree": release._tree(repo, candidate),
                "base_sha": base, "controller_files": ["scripts/domestic_main_release.py"],
                "fixed_file_sha256": {path: "a" * 64 for path in release.builder.FIXED_CONTROLLER_FILES},
                "check_receipt_sha256": "c" * 64, "checked_at_utc": release._utc_now(),
            }
            config = {"repo": str(repo), "state": str(state_path), "lock": str(lock_path),
                      "production_enabled": True, "controller_path": release.DEFAULT_CONTROLLER,
                      "push_group": "push-group"}
            processed: list[str] = []
            durable_claims: list[dict[str, object]] = []

            def process(_config, _state_path, current_state, item):
                self.assertIsNotNone(current_state["controller_maintenance"])
                current_state["controller_maintenance"] = None
                current_state["in_flight"] = {"candidate_id": item["candidate_id"],
                                               "head_sha": item["head_sha"],
                                               "production_install_started": False}
                item["status"] = "checking"
                release._update_state(state_path, current_state, status="blocked")
                durable_claims.append({"maintenance": current_state["controller_maintenance"],
                                       "in_flight": dict(current_state["in_flight"])})
                release._advance_main_cas(repo, base, candidate)
                current_state["main"] = {"sha": candidate, "tree": release._tree(repo, candidate)}
                current_state["in_flight"] = None
                current_state["status"] = "ready"
                item["status"] = "completed"
                processed.append(item["head_sha"])
                return {"status": "completed", "candidate_sha": candidate}

            def update_state(_path, current, **changes):
                current.update(changes)

            with mock.patch.object(release.os, "geteuid", return_value=0), \
                 mock.patch.object(release, "_check_config", return_value=config), \
                 mock.patch.object(release, "_locked", return_value=__import__("contextlib").nullcontext()), \
                 mock.patch.object(release, "_assert_legacy_release_path_stopped"), \
                 mock.patch.object(release, "verify_bare_repository", return_value={"bare": True}), \
                 mock.patch.object(release, "_load_state", return_value=state), \
                 mock.patch.object(release, "_update_state", side_effect=update_state), \
                 mock.patch.object(release, "_verify_controller_maintenance_candidate", return_value=state["controller_maintenance"]), \
                 mock.patch.object(release, "process_candidate", side_effect=process):
                result = release.poll(config)

            self.assertEqual(processed, [candidate])
            self.assertEqual(durable_claims, [{"maintenance": None,
                                               "in_flight": {"candidate_id": candidate,
                                                              "head_sha": candidate,
                                                              "production_install_started": False}}])
            self.assertEqual(result["status"], "completed")
            self.assertEqual(release._resolve_ref(repo, release.MAIN_REF), candidate)
            self.assertIsNone(state["controller_maintenance"])
            self.assertFalse(state_path.exists())

    def test_maintenance_marker_survives_preclaim_error(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            repo, base, candidate, _other = make_repository(root)
            tree = release._tree(repo, base)
            state = release._new_state(base, tree,
                                       {"sha": base, "tree": tree, "manifest_sha256": "b" * 64})
            item = {"candidate_id": candidate, "ref": "refs/heads/codex/one",
                    "head_sha": candidate, "base_sha": base, "status": "pending"}
            state["queue"] = [item]
            marker = {"candidate_sha": candidate, "candidate_tree": release._tree(repo, candidate),
                      "base_sha": base, "controller_files": ["scripts/domestic_main_release.py"],
                      "fixed_file_sha256": {path: "a" * 64 for path in release.builder.FIXED_CONTROLLER_FILES},
                      "check_receipt_sha256": "c" * 64, "checked_at_utc": release._utc_now()}
            state["controller_maintenance"] = marker
            config = {"repo": str(repo), "work_root": str(root),
                      "source_worktree": str(root / "worktree")}
            with mock.patch.object(release, "_resolve_ref", side_effect=release.ReleaseError("preclaim failure")), \
                 mock.patch.object(release, "_update_state") as persist:
                with self.assertRaisesRegex(release.ReleaseError, "preclaim failure"):
                    release.process_candidate(config, root / "state.json", state, item)
            self.assertEqual(state["controller_maintenance"], marker)
            self.assertIsNone(state["in_flight"])
            persist.assert_not_called()

    def test_poll_recovers_claimed_candidate_before_helper_check_and_never_retries_unknown(self) -> None:
        for production_started in (False, True):
            with self.subTest(production_started=production_started), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                repo, base, candidate, _other = make_repository(root)
                state_path = root / "state.json"
                tree = release._tree(repo, base)
                state = release._new_state(base, tree,
                                           {"sha": base, "tree": tree, "manifest_sha256": "b" * 64})
                item = {"candidate_id": candidate, "ref": "refs/heads/codex/one",
                        "head_sha": candidate, "base_sha": base, "status": "checking"}
                state["queue"] = [item]
                state["status"] = "blocked"
                state["in_flight"] = {"candidate_id": candidate, "ref": item["ref"],
                                       "head_sha": candidate, "production_install_started": production_started,
                                       "commit_started": False, "phase": "checks"}
                config = {"repo": str(repo), "state": str(state_path),
                          "lock": str(root / "controller.lock"), "production_enabled": True,
                          "controller_path": release.DEFAULT_CONTROLLER,
                          "push_group": "release-push-group-test"}
                with mock.patch.object(release.os, "geteuid", return_value=0), \
                     mock.patch.object(release, "_check_config", return_value=config), \
                     mock.patch.object(release, "_locked", return_value=__import__("contextlib").nullcontext()), \
                     mock.patch.object(release, "_assert_legacy_release_path_stopped"), \
                     mock.patch.object(release, "verify_bare_repository", return_value={"bare": True}), \
                     mock.patch.object(release, "_load_state", return_value=state), \
                     mock.patch.object(release, "atomic_json") as persist, \
                     mock.patch.object(release, "_verify_controller_files") as verify_files, \
                     mock.patch.object(release, "process_candidate") as process:
                    expected = "outcome is unknown" if production_started else "queue is blocked"
                    with self.assertRaisesRegex(release.ReleaseError, expected):
                        release.poll(config)
                persist.assert_called_once()
                verify_files.assert_not_called()
                process.assert_not_called()
                if production_started:
                    self.assertEqual(state["status"], "outcome_unknown")
                    self.assertEqual(item["status"], "outcome_unknown")
                else:
                    self.assertEqual(state["status"], "blocked")
                    self.assertEqual(item["status"], "failed")
                    self.assertIsNone(state["in_flight"])

    def test_failed_candidate_cannot_reuse_maintenance_marker(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            repo, base, candidate, _other = make_repository(root)
            tree = release._tree(repo, base)
            state = release._new_state(base, tree,
                                       {"sha": base, "tree": tree, "manifest_sha256": "b" * 64})
            item = {"candidate_id": candidate, "ref": "refs/heads/codex/one",
                    "head_sha": candidate, "base_sha": base, "status": "pending"}
            state["queue"] = [item]
            state["controller_maintenance"] = {
                "candidate_sha": candidate, "candidate_tree": release._tree(repo, candidate),
                "base_sha": base, "controller_files": ["scripts/domestic_main_release.py"],
                "fixed_file_sha256": {path: "a" * 64 for path in release.builder.FIXED_CONTROLLER_FILES},
                "check_receipt_sha256": "c" * 64, "checked_at_utc": release._utc_now(),
            }
            state_path = root / "state.json"
            state["in_flight"] = {}
            with mock.patch.object(release, "atomic_json") as save:
                release._mark_failed(state_path, state, item, RuntimeError("failed"), "checks")
            self.assertIsNone(state["controller_maintenance"])
            self.assertEqual(item["status"], "failed")
            self.assertEqual(state["status"], "blocked")
            save.assert_called_once()

    def test_policy_changes_cannot_request_targeted_lanes(self) -> None:
        targeted = {"enforced": {"selection_mode": "targeted", "selected_lanes": ["preflight"],
                                 "selected_checks": [], "profile": "tooling"},
                    "candidate_go_packages": []}
        with self.assertRaises(release.ReleaseError):
            release._enforced_lanes(targeted, ["scripts/ci/quality_lanes.py"])
        full = {"enforced": {"selection_mode": "full", "selected_lanes": list(release.builder_ci_lanes()),
                              "selected_checks": [], "profile": "full"},
                "candidate_go_packages": []}
        _enforced, lanes, checks, packages, profile = release._enforced_lanes(
            full, ["scripts/ci/quality_lanes.py"])
        self.assertEqual(lanes, list(release.builder_ci_lanes()))
        self.assertEqual(checks, [])
        self.assertEqual(packages, [])
        self.assertEqual(profile, "full")

    def test_interrupted_release_is_classified_before_any_retry(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            repo, base, candidate, _other = make_repository(root)
            main_tree = release._tree(repo, base)
            app = {"sha": base, "tree": main_tree, "manifest_sha256": "a" * 64}
            for phase, expected in (({"production_install_started": True}, "outcome_unknown"),
                                    ({"commit_started": True}, "outcome_unknown"),
                                    ({"stage_install_started": True}, "staging_reset_required"),
                                    ({}, "retry_required")):
                with self.subTest(phase=phase), tempfile.TemporaryDirectory() as state_tmp:
                    state_path = Path(state_tmp) / "state.json"
                    state = release._new_state(base, main_tree, app)
                    state["status"] = "blocked"
                    state["queue"] = [{"candidate_id": candidate, "ref": "refs/heads/codex/one",
                                        "head_sha": candidate, "base_sha": base, "status": "checking"}]
                    state["in_flight"] = {"candidate_id": candidate, "head_sha": candidate,
                                           **phase}
                    release.atomic_json(state_path, state)
                    result = release._recover_orphaned_inflight(state_path, state)
                    self.assertEqual(result, expected)
                    recovered = json.loads(state_path.read_text())
                    self.assertEqual(recovered["status"], "outcome_unknown" if expected == "outcome_unknown" else "blocked")
                    self.assertEqual(recovered.get("staging_out_of_sync", False), expected == "staging_reset_required")
                    if expected == "outcome_unknown":
                        self.assertIsNotNone(recovered["in_flight"])
                        self.assertEqual(recovered["queue"][0]["status"], "outcome_unknown")
                    elif expected == "staging_reset_required":
                        self.assertIsNotNone(recovered["in_flight"])
                        self.assertEqual(recovered["queue"][0]["status"], "failed")
                    else:
                        self.assertIsNone(recovered["in_flight"])
                        self.assertEqual(recovered["queue"][0]["status"], "failed")

    def test_old_release_timer_and_oneshot_must_be_stopped(self) -> None:
        def fake_run(args, **_kwargs):
            verb, unit = args[-2], args[-1]
            state = "disabled" if verb == "is-enabled" else "inactive"
            return subprocess.CompletedProcess(args, 0, state + "\n", "")
        with mock.patch.object(release, "_run", side_effect=fake_run):
            release._assert_release_timers_stopped()
            release._assert_legacy_release_path_stopped()
        def active_old_service(args, **_kwargs):
            verb, unit = args[-2], args[-1]
            state = "disabled" if verb == "is-enabled" else ("active" if unit == "aicrm-domestic-release.service" else "inactive")
            return subprocess.CompletedProcess(args, 0, state + "\n", "")
        with mock.patch.object(release, "_run", side_effect=active_old_service), \
             self.assertRaises(release.ReleaseError):
            release._assert_release_timers_stopped()
        with mock.patch.object(release, "_run", side_effect=active_old_service), \
             self.assertRaises(release.ReleaseError):
            release._assert_legacy_release_path_stopped()

    def test_stage_reset_ack_clears_only_interrupted_stage_record_then_allows_retry(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            repo, base, candidate, _other = make_repository(root)
            app = {"sha": base, "tree": release._tree(repo, base), "manifest_sha256": "a" * 64}
            state = release._new_state(base, app["tree"], app)
            state.update({"status": "blocked", "staging_out_of_sync": True,
                          "queue": [{"candidate_id": candidate, "ref": "refs/heads/codex/one",
                                     "head_sha": candidate, "base_sha": base, "status": "failed",
                                     "failure": {"phase": "interrupted-staging-install"}}],
                          "in_flight": {"candidate_id": candidate, "head_sha": candidate,
                                        "stage_install_started": True, "phase": "interrupted-staging-install"}})
            state_path, lock_path = root / "state.json", root / "controller.lock"
            config = {"state": str(state_path), "lock": str(lock_path)}
            with mock.patch.object(release.os, "geteuid", return_value=0), \
                 mock.patch.object(release, "_check_config", return_value=config), \
                 mock.patch.object(release, "_locked", return_value=__import__("contextlib").nullcontext()), \
                 mock.patch.object(release, "_load_state", return_value=state), \
                 mock.patch.object(release, "_installed_app_identity", return_value=(dict(app), {})), \
                 mock.patch.object(release, "_update_state", side_effect=lambda _path, value, **kw: value.update(kw)):
                result = release.acknowledge_stage_reset(config)
            self.assertEqual(result["status"], "stage_reset_verified")
            self.assertIsNone(state["in_flight"])
            self.assertFalse(state["staging_out_of_sync"])
            with mock.patch.object(release, "_load_state", return_value=state):
                retried = release.submit_candidate(repo, state_path, "refs/heads/codex/one", candidate,
                                                   base, root / "controller.lock")
            self.assertEqual(retried["status"], "pending")

    def test_same_sha_retry_keeps_attempt_counter_for_unique_evidence(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            repo, base, candidate, _other = make_repository(root)
            state = release._new_state(base, release._tree(repo, base),
                                       {"sha": base, "tree": release._tree(repo, base), "manifest_sha256": "a" * 64})
            state.update({"status": "blocked", "queue": [{"candidate_id": candidate,
                          "ref": "refs/heads/codex/one", "head_sha": candidate,
                          "base_sha": base, "status": "failed", "attempt": 4}]})
            with mock.patch.object(release, "_load_state", return_value=state):
                result = release.submit_candidate(repo, root / "state.json", "refs/heads/codex/one",
                                                  candidate, base, root / "lock")
            self.assertEqual(result["status"], "pending")
            self.assertEqual(state["queue"][0]["attempt"], 4)

    def test_smoke_helper_contract_pins_domestic_candidate_ref(self) -> None:
        sha, tree = "1" * 40, "2" * 40
        manifest, helper = "a" * 64, "b" * 64
        receipt = {"status": "passed", "source_sha": sha, "source_tree": tree,
                   "source_ref": release._candidate_ref(sha), "installed_sha": sha,
                   "manifest_sha256": manifest, "helper_sha256": helper, "stage_role": "staging"}
        with mock.patch.object(release, "_worktree_git", return_value="internal/payment/checkout.go"), \
             mock.patch.object(release, "_needs_installed_alipay_smoke", return_value=True), \
             mock.patch.object(release.legacy, "command", return_value=json.dumps(receipt)) as command:
            result = release._run_installed_smoke({"stage_helper": "/usr/local/libexec/aicrm/domestic-promote.py",
                                                  "repo": release.DEFAULT_REPO},
                                                  Path("/candidate"), sha, manifest, helper, tree)
        self.assertEqual(result, receipt)
        argv = command.call_args.args
        self.assertIn("--source-ref", argv)
        self.assertIn(release._candidate_ref(sha), argv)
        self.assertIn("--source-repository", argv)
        self.assertIn(release.DEFAULT_REPO, argv)

    def test_candidate_stage_helper_host_check_binds_exact_git_blob(self) -> None:
        sha = "1" * 40
        expected = __import__("hashlib").sha256(b"candidate helper bytes").hexdigest()
        receipt = {"host_role": "staging", "postgres_major": 16,
                   "database_connection": "verified", "helper_sha256": expected}
        completed = subprocess.CompletedProcess([], 0, json.dumps(receipt) + "\n", "")
        config = {"stage_helper": "/usr/local/libexec/aicrm/domestic-promote.py"}
        with mock.patch.object(release, "_source_blob", return_value=b"candidate helper bytes"), \
             mock.patch.object(release.legacy, "_helper_fixed_path", return_value=config["stage_helper"]), \
             mock.patch.object(release, "_protected_exact_helper", return_value=True), \
             mock.patch.object(release, "_run", return_value=completed) as run:
            result = release._run_stage_helper_host_contract(config, Path("/repo.git"), sha)
        self.assertEqual(result, receipt)
        argv = run.call_args.args[0]
        self.assertEqual(argv[-3:], ["--check-host-contract", "--expected-helper-sha256", expected])
        with mock.patch.object(release, "_source_blob", return_value=b"candidate helper bytes"), \
             mock.patch.object(release.legacy, "_helper_fixed_path", return_value=config["stage_helper"]), \
             mock.patch.object(release, "_protected_exact_helper", return_value=False), \
             mock.patch.object(release, "_run") as run:
            self.assertIsNone(release._run_stage_helper_host_contract(config, Path("/repo.git"), sha))
            run.assert_not_called()

    def test_check_env_requires_absolute_explicit_build_path(self) -> None:
        base = {"check_database_url": "postgresql://localhost/aicrm_ci"}
        with self.assertRaises(release.ReleaseError):
            release._check_env(base)
        with self.assertRaises(release.ReleaseError):
            release._check_env({**base, "build_path": "/opt/go/bin:relative"})
        with self.assertRaisesRegex(release.ReleaseError, "fixed npm toolchain directory"):
            release._check_env({**base, "build_path": "/opt/aicrm/toolchain/go-1.26.6/bin:/opt/aicrm/toolchain/node-v24.18.0-linux-x64/bin:/usr/bin:/bin"})
        expected_path = "/opt/aicrm/toolchain/go-1.26.6/bin:/opt/aicrm/toolchain/npm/bin:/opt/aicrm/toolchain/node-v24.18.0-linux-x64/bin:/usr/bin:/bin"
        env = release._check_env({**base, "build_path": expected_path})
        self.assertEqual(env["PATH"], expected_path)

    def test_build_toolchain_probe_runs_as_isolated_user_and_requires_all_tools(self) -> None:
        config = {"check_database_url": "postgresql://localhost/aicrm_ci",
                  "build_path": "/opt/aicrm/toolchain/go-1.26.6/bin:/opt/aicrm/toolchain/npm/bin:/opt/aicrm/toolchain/node-v24.18.0-linux-x64/bin:/usr/bin:/bin"}
        expected = {name: {"path": f"/opt/{name}/bin/{name}", "version": name + " version"}
                    for name in release.BUILD_TOOLCHAIN}
        completed = subprocess.CompletedProcess([], 0, json.dumps(expected) + "\n", "")
        with mock.patch.object(release, "_build_command", return_value=completed) as run:
            self.assertEqual(release._verify_build_toolchain(config), expected)
        self.assertEqual(run.call_args.args[1][:2], ["/usr/bin/python3", "-c"])
        missing = subprocess.CompletedProcess([], 21, "", "")
        with mock.patch.object(release, "_build_command", return_value=missing), \
             self.assertRaises(release.ReleaseError):
            release._verify_build_toolchain(config)
        incomplete = subprocess.CompletedProcess([], 0, json.dumps({"go": expected["go"]}) + "\n", "")
        with mock.patch.object(release, "_build_command", return_value=incomplete), \
             self.assertRaises(release.ReleaseError):
            release._verify_build_toolchain(config)

    def test_source_bundle_contains_only_main_and_exact_candidate_and_restores(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            repo, base, candidate, _other = make_repository(root)
            release._pin_candidate(repo, candidate)
            output, metadata = release._create_full_bundle(repo, root / "work", candidate, release._tree(repo, candidate))
            listed = subprocess.check_output(["git", "bundle", "list-heads", str(output)], text=True).splitlines()
            refs = {line.split(maxsplit=1)[1]: line.split(maxsplit=1)[0] for line in listed}
            self.assertEqual(refs, {release.MAIN_REF: base, release._candidate_ref(candidate): candidate})
            self.assertTrue(metadata["self_contained"])
            self.assertEqual(metadata["previous_main_sha"], base)

    def test_source_only_baseline_bundle_binds_new_main_to_prior_installed_app(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            repo, base, runtime_candidate, _other = make_repository(root)
            app = {"sha": base, "tree": release._tree(repo, base), "manifest_sha256": "a" * 64}
            with self.assertRaisesRegex(release.ReleaseError, "application changes newer"):
                release._validate_baseline_identity(repo, runtime_candidate,
                                                    release._tree(repo, runtime_candidate), app)
            source = root / "source-only"
            subprocess.run(["git", "clone", str(repo), str(source)], check=True, stdout=subprocess.DEVNULL)
            subprocess.run(["git", "-C", str(source), "config", "user.name", "Test"], check=True)
            subprocess.run(["git", "-C", str(source), "config", "user.email", "test@example.invalid"], check=True)
            (source / "README.md").write_text("source-only documentation\n")
            subprocess.run(["git", "-C", str(source), "add", "README.md"], check=True)
            subprocess.run(["git", "-C", str(source), "commit", "-m", "docs"], check=True, stdout=subprocess.DEVNULL)
            candidate = subprocess.check_output(["git", "-C", str(source), "rev-parse", "HEAD"], text=True).strip()
            subprocess.run(["git", f"--git-dir={repo}", "fetch", "--no-tags", str(source),
                            f"{candidate}:{release.MAIN_REF}"], check=True, stdout=subprocess.DEVNULL)
            release._pin_candidate(repo, candidate)
            base_tree = release._tree(repo, base)
            candidate_tree = release._tree(repo, candidate)
            release._validate_baseline_identity(repo, candidate, candidate_tree, app)
            bundle, metadata = release._create_full_bundle(
                repo, root / "work", candidate, candidate_tree,
                previous_main_sha=base, baseline_transition=True,
            )
            listed = subprocess.check_output(["git", "bundle", "list-heads", str(bundle)], text=True).splitlines()
            refs = {line.split(maxsplit=1)[1]: line.split(maxsplit=1)[0] for line in listed}
            self.assertEqual(refs, {release.MAIN_REF: candidate, release._candidate_ref(candidate): candidate})
            self.assertEqual(metadata["previous_main_sha"], base)
            self.assertTrue(metadata["baseline_transition"])
            with self.assertRaises(release.ReleaseError):
                release._validate_baseline_identity(repo, base, base_tree, {
                    "sha": candidate, "tree": candidate_tree, "manifest_sha256": "b" * 64,
                })

    def test_baseline_upload_accepts_installed_291_response_then_verifies_durable_receipt(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            bundle = root / "baseline.bundle"
            bundle.write_bytes(b"verified baseline bundle")
            sha, tree, previous = "1" * 40, "2" * 40, "3" * 40
            digest = release._file_sha256(bundle)
            metadata = {"source_sha": sha, "source_tree": tree, "previous_main_sha": previous,
                        "bundle_sha256": digest, "bundle_bytes": bundle.stat().st_size,
                        "baseline_transition": True}
            saved = {"status": "verified", "source_sha": sha, "source_tree": tree,
                     "previous_main_sha": previous, "bundle_sha256": digest,
                     "self_contained": True, "bundle_bytes": bundle.stat().st_size}
            calls: list[tuple[str, ...]] = []

            def production_ssh(_config, *args, **_kwargs):
                calls.append(tuple(args))
                if "--save-domestic-source-bundle" in args:
                    # Installed 291 helper persists baseline_transition in its
                    # receipt but omits it from this success response.
                    return json.dumps(saved) + "\n"
                if "--verify-domestic-source-backup" in args:
                    self.assertIn("--allow-baseline-transition", args)
                    return json.dumps(saved) + "\n"
                return ""

            config = {"prod_source_bundle_incoming": release.SOURCE_BUNDLE_INCOMING_ROOT,
                      "prod_user": "ubuntu", "prod_host": "production.test", "prod_helper": "/fixed/helper",
                      "prod_key": "/fixed/key", "prod_known_hosts": "/fixed/known-hosts"}
            with mock.patch.object(release, "_production_ssh", side_effect=production_ssh), \
                 mock.patch.object(release.legacy, "command", return_value="") as transfer:
                receipt = release._upload_and_store_bundle(config, bundle, metadata, baseline_transition=True)

            self.assertTrue(receipt["baseline_transition"])
            transfer.assert_called_once()
            self.assertEqual(sum("--save-domestic-source-bundle" in call for call in calls), 1)
            self.assertEqual(sum("--verify-domestic-source-backup" in call for call in calls), 1)

    def test_saved_baseline_verification_rejects_digest_mismatch(self) -> None:
        metadata = {"source_sha": "1" * 40, "source_tree": "2" * 40,
                    "previous_main_sha": "3" * 40, "bundle_sha256": "a" * 64,
                    "bundle_bytes": 24, "baseline_transition": True}
        response = {"status": "verified", "source_sha": metadata["source_sha"],
                    "source_tree": metadata["source_tree"],
                    "previous_main_sha": metadata["previous_main_sha"],
                    "bundle_sha256": "b" * 64, "self_contained": True, "bundle_bytes": 24}
        with mock.patch.object(release, "_production_ssh", return_value=json.dumps(response)) as ssh, \
             self.assertRaisesRegex(release.ReleaseError, "readback does not match"):
            release._verify_saved_production_bundle({"prod_helper": "/fixed/helper"}, metadata,
                                                    baseline_transition=True)
        self.assertIn("--allow-baseline-transition", ssh.call_args.args)

    def _baseline_resume_fixture(self, root: Path) -> tuple[dict, str, str, dict]:
        repo, app_sha, baseline_sha, _other = make_repository(root)
        subprocess.run(["git", f"--git-dir={repo}", "update-ref", "refs/heads/main", baseline_sha, app_sha],
                       check=True)
        release._pin_candidate(repo, baseline_sha)
        baseline_tree = release._tree(repo, baseline_sha)
        app = {"sha": app_sha, "tree": release._tree(repo, app_sha), "manifest_sha256": "a" * 64}
        work_root = root / "work"
        _bundle, bundle_meta = release._create_full_bundle(
            repo, work_root, baseline_sha, baseline_tree,
            previous_main_sha=app_sha, baseline_transition=True,
        )
        private_dir = root / "private"
        private_dir.mkdir(mode=0o700)
        config = {"repo": str(repo), "state": str(root / "state.json"),
                  "lock": str(private_dir / "controller.lock"), "work_root": str(work_root),
                  "controller_path": "/fixed/controller", "push_group": "release-push-test",
                  "prod_helper": "/fixed/helper", "production_enabled": True}
        return config, app_sha, baseline_sha, {"app": app, "tree": baseline_tree, "bundle_meta": bundle_meta}

    def test_baseline_overlay_does_not_require_github_ci(self) -> None:
        self.assertFalse(hasattr(release, "_github_json"))
        self.assertFalse(hasattr(release, "_github_pr46_ci_evidence"))

    def test_baseline_overlay_seed_bundle_requires_one_complete_main_ref(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            repo, base, candidate, _other = make_repository(root)
            subprocess.run(["git", f"--git-dir={repo}", "update-ref", "refs/heads/main", candidate], check=True)
            bundle = root / f"domestic-main-seed-{candidate}.bundle"
            subprocess.run(["git", f"--git-dir={repo}", "bundle", "create", str(bundle), "refs/heads/main"], check=True,
                           stdout=subprocess.DEVNULL)
            work_root = root / "work"
            work_root.mkdir(mode=0o700)
            with mock.patch.object(release, "BASELINE_OVERLAY_SEED_ROOT", root):
                with release._verified_overlay_seed_bundle(repo, work_root, bundle, candidate, base) as (seed_repo, digest, tree):
                    self.assertEqual(stat.S_IMODE(seed_repo.parent.stat().st_mode), 0o711)
                    self.assertEqual(tree, release._tree(seed_repo, candidate))
                    self.assertEqual(digest, release._file_sha256(bundle))
                extra_ref = f"refs/domestic-main-seed/{candidate}"
                subprocess.run(["git", f"--git-dir={repo}", "update-ref", extra_ref, candidate], check=True)
                malformed = root / f"domestic-main-seed-{candidate}.bundle"
                malformed.unlink()
                subprocess.run(["git", f"--git-dir={repo}", "bundle", "create", str(malformed),
                                "refs/heads/main", extra_ref], check=True, stdout=subprocess.DEVNULL)
                with self.assertRaisesRegex(release.ReleaseError, "unexpected refs"):
                    with release._verified_overlay_seed_bundle(repo, work_root, malformed, candidate, base):
                        self.fail("multi-ref seed bundle must not be accepted")

    def test_baseline_overlay_runs_local_base291_tool_and_host_preflight(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            repo, base, candidate, _other = make_repository(root)
            work_root = root / "work"
            work_root.mkdir(mode=0o700)
            receipt = {"status": "passed", "baseline_sha": base, "head_sha": candidate,
                       "execution_receipt_sha256": "a" * 64, "selected_lanes": ["preflight"]}
            host = {"host_role": "staging", "postgres_major": 16,
                    "database_connection": "verified", "helper_sha256": "b" * 64}
            config = {"work_root": str(work_root), "stage_helper": "/stage/helper"}

            def check_traversable_parent(_config, _repo, worktree, _report_dir, _base, _head):
                self.assertEqual(stat.S_IMODE(worktree.parent.stat().st_mode), 0o755)
                return receipt

            with mock.patch.object(release, "_safe_directory"), \
                 mock.patch.object(release, "_run"), \
                 mock.patch.object(release, "_make_worktree_metadata_readable"), \
                 mock.patch.object(release, "_check_report", side_effect=check_traversable_parent) as check, \
                 mock.patch.object(release, "_build_command") as build, \
                 mock.patch.object(release, "_tree", return_value="2" * 40), \
                 mock.patch.object(release, "_run_stage_helper_host_contract", return_value=host) as contract:
                result = release._run_baseline_overlay_preflight(
                    config, repo, base_sha=base, candidate_sha=candidate)
            self.assertEqual(result["baseline_sha"], base)
            self.assertEqual(result["head_sha"], candidate)
            self.assertEqual(result["stage_host_contract"], host)
            self.assertEqual(check.call_args.args[4:], (base, candidate))
            self.assertEqual(build.call_count, 3)
            self.assertEqual(build.call_args_list[-1].args[1][1:3],
                             ["-m", "unittest"])
            self.assertEqual(build.call_args_list[-1].args[1][-1],
                             "scripts.test_domestic_release_controller")
            contract.assert_called_once_with(config, repo, candidate)
            with mock.patch.object(release, "_safe_directory"), \
                 mock.patch.object(release, "_run"), \
                 mock.patch.object(release, "_make_worktree_metadata_readable"), \
                 mock.patch.object(release, "_check_report", return_value=receipt), \
                 mock.patch.object(release, "_build_command"), \
                 mock.patch.object(release, "_run_stage_helper_host_contract", return_value=None):
                with self.assertRaisesRegex(release.ControllerMaintenanceRequired, "host contract"):
                    release._run_baseline_overlay_preflight(
                        config, repo, base_sha=base, candidate_sha=candidate)

    def test_baseline_overlay_evidence_is_create_only_and_same_identity_resumable(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            work_root = Path(temporary) / "work"
            work_root.mkdir(mode=0o700)
            config = {"work_root": str(work_root)}
            evidence = {"schema_version": 1, "candidate": {"sha": "a" * 40},
                        "created_at_utc": "2026-09-26T00:00:00Z",
                        "local_preflight": {"check_receipt_sha256": "d" * 64,
                            "stage_host_contract": {"host_role": "staging", "postgres_major": 16,
                                "database_connection": "verified", "systemd_services": 3,
                                "helper_sha256": "b" * 64}}}
            fake_stat = mock.Mock(st_mode=stat.S_IFREG | 0o600, st_uid=0, st_gid=0, st_size=0)
            with mock.patch.object(release.os, "geteuid", return_value=0), \
                 mock.patch.object(release, "_safe_directory"), \
                 mock.patch.object(release.os, "fstat", return_value=fake_stat):
                first = release._store_baseline_overlay_evidence(config, evidence)
                retry = release._store_baseline_overlay_evidence(config, {
                    **evidence, "created_at_utc": "2026-09-26T00:01:00Z",
                    "local_preflight": {**evidence["local_preflight"],
                        "check_receipt_sha256": "e" * 64},
                })
                self.assertEqual(first, retry)
                different_head = {**evidence, "candidate": {"sha": "b" * 40}}
                second_candidate = release._store_baseline_overlay_evidence(config, different_head)
                self.assertNotEqual(first, second_candidate)
                with self.assertRaisesRegex(release.ReleaseError, "different identity"):
                    release._store_baseline_overlay_evidence(config, {
                        **evidence, "helper_blob_sha256": "c" * 64,
                    })
                changed_host_contract = {**evidence, "local_preflight": {
                    **evidence["local_preflight"], "stage_host_contract": {
                        **evidence["local_preflight"]["stage_host_contract"], "postgres_major": 15}}}
                with self.assertRaisesRegex(release.ReleaseError, "different identity"):
                    release._store_baseline_overlay_evidence(config, changed_host_contract)
            for candidate_sha in ("a" * 40, "b" * 40):
                overlay = release._overlay_evidence_path(config, candidate_sha)
                self.assertEqual(stat.S_IMODE(overlay.stat().st_mode), 0o600)
                self.assertTrue(overlay.is_file())

    def test_active_overlay_candidate_allows_pre_cursor_supersede_and_locks_after_cursor(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            work_root = Path(temporary) / "work"
            work_root.mkdir(mode=0o700)
            config = {"work_root": str(work_root)}
            fake_stat = mock.Mock(st_mode=stat.S_IFREG | 0o600, st_uid=0, st_gid=0, st_size=0)
            with mock.patch.object(release.os, "geteuid", return_value=0), \
                 mock.patch.object(release, "_safe_directory"), \
                 mock.patch.object(release.os, "fstat", return_value=fake_stat):
                first = release._select_active_overlay_candidate(
                    config, "a" * 40, cursor_exists=False, ledger_exists=False, allow_supersede=True)
                self.assertEqual(first["candidate_sha"], "a" * 40)
                # Simulates a crash after marker A but before initialize cursor.
                resumed = release._select_active_overlay_candidate(
                    config, "a" * 40, cursor_exists=False, ledger_exists=False, allow_supersede=True)
                self.assertEqual(resumed["candidate_sha"], "a" * 40)
                with self.assertRaisesRegex(release.ReleaseError, "fixed by an existing cursor"):
                    release._select_active_overlay_candidate(
                        config, "b" * 40, cursor_exists=True, ledger_exists=False, allow_supersede=True)
                second = release._select_active_overlay_candidate(
                    config, "b" * 40, cursor_exists=False, ledger_exists=False, allow_supersede=True)
                self.assertEqual(second["candidate_sha"], "b" * 40)
                self.assertEqual(second["superseded_candidate_shas"], ["a" * 40])
                self.assertEqual(release._read_active_overlay_candidate(config), second)
                with self.assertRaisesRegex(release.ReleaseError, "fixed by an existing cursor"):
                    release._select_active_overlay_candidate(
                        config, "c" * 40, cursor_exists=True, ledger_exists=False, allow_supersede=True)
                with self.assertRaisesRegex(release.ReleaseError, "fixed by an existing cursor"):
                    release._select_active_overlay_candidate(
                        config, "c" * 40, cursor_exists=False, ledger_exists=True, allow_supersede=True)

    def test_baseline_overlay_readback_binds_live_helper_app_cursor_and_receipt(self) -> None:
        candidate_sha, helper_sha, controller_sha = "a" * 40, "b" * 64, "c" * 64
        app = {"sha": release.BASELINE_OVERLAY_APP_SHA, "tree": "d" * 40,
               "manifest_sha256": "e" * 64}
        bundle = {"source_sha": release.BASELINE_OVERLAY_BASE_SHA,
                  "source_tree": release.BASELINE_OVERLAY_BASE_TREE,
                  "previous_main_sha": app["sha"], "bundle_sha256": "f" * 64,
                  "baseline_transition": True}
        evidence = {
            "schema_version": 1, "operation": "resume_baseline_helper_overlay",
            "baseline": {"sha": release.BASELINE_OVERLAY_BASE_SHA,
                         "tree": release.BASELINE_OVERLAY_BASE_TREE},
            "installed_app": app,
            "production_source_backup": {"source_sha": bundle["source_sha"],
                "source_tree": bundle["source_tree"], "previous_main_sha": app["sha"],
                "bundle_sha256": bundle["bundle_sha256"], "source_receipt_sha256": "1" * 64},
            "candidate": {"pr_number": 46, "sha": candidate_sha, "tree": "2" * 40,
                "helper_blob_sha256": helper_sha, "controller_blob_sha256": controller_sha},
            "local_preflight": {"baseline_sha": release.BASELINE_OVERLAY_BASE_SHA,
                "head_sha": candidate_sha, "candidate_tree": "2" * 40,
                "check_receipt_sha256": "5" * 64, "selected_lanes": ["preflight"],
                "stage_host_contract": {"host_role": "staging", "postgres_major": 16,
                    "database_connection": "verified", "helper_sha256": helper_sha}},
            "seed_bundle_sha256": "3" * 64,
        }
        cursor = {"main_sha": release.BASELINE_OVERLAY_BASE_SHA,
                  "main_tree": release.BASELINE_OVERLAY_BASE_TREE,
                  "installed_app_sha": app["sha"], "installed_app_tree": app["tree"],
                  "installed_manifest_sha256": app["manifest_sha256"],
                  "source_bundle_sha256": bundle["bundle_sha256"]}
        stored = {"evidence": evidence, "evidence_sha256": "4" * 64}
        config = {"work_root": "/work", "stage_helper": "/stage/helper", "prod_helper": "/prod/helper"}
        with mock.patch.object(release, "_read_baseline_overlay_evidence", return_value=stored), \
             mock.patch.object(release, "_read_active_overlay_candidate", return_value={"candidate_sha": candidate_sha}), \
             mock.patch.object(release, "_load_existing_baseline_bundle", return_value=(Path("/bundle"), bundle)), \
             mock.patch.object(release, "_verify_saved_production_bundle", return_value={
                 "source_receipt_sha256": "1" * 64}), \
             mock.patch.object(release, "_protected_exact_helper", return_value=True), \
             mock.patch.object(release.legacy, "_remote_file_sha256", return_value=helper_sha), \
             mock.patch.object(release, "_file_sha256", return_value=controller_sha):
            result = release._verify_existing_baseline_helper_overlay(
                config, Path("/repo"), candidate_sha=candidate_sha,
                base_sha=release.BASELINE_OVERLAY_BASE_SHA,
                base_tree=release.BASELINE_OVERLAY_BASE_TREE,
                installed_app=app, production_cursor=cursor)
            self.assertEqual(result["helper_blob_sha256"], helper_sha)
            with self.assertRaisesRegex(release.ReleaseError, "cursor"):
                release._verify_existing_baseline_helper_overlay(
                    config, Path("/repo"), candidate_sha=candidate_sha,
                    base_sha=release.BASELINE_OVERLAY_BASE_SHA,
                    base_tree=release.BASELINE_OVERLAY_BASE_TREE,
                    installed_app=app, production_cursor={**cursor, "main_sha": "9" * 40})

        with mock.patch.object(release, "_read_baseline_overlay_evidence", return_value=stored), \
             mock.patch.object(release, "_read_active_overlay_candidate", return_value={"candidate_sha": candidate_sha}), \
             mock.patch.object(release, "_load_existing_baseline_bundle", return_value=(Path("/bundle"), bundle)), \
             mock.patch.object(release, "_verify_saved_production_bundle") as saved, \
             mock.patch.object(release, "_protected_exact_helper", return_value=True), \
             mock.patch.object(release.legacy, "_remote_file_sha256", return_value="9" * 64), \
             mock.patch.object(release, "_file_sha256", return_value=controller_sha):
            with self.assertRaisesRegex(release.ControllerMaintenanceRequired, "production and running"):
                release._verify_existing_baseline_helper_overlay(
                    config, Path("/repo"), candidate_sha=candidate_sha,
                    base_sha=release.BASELINE_OVERLAY_BASE_SHA,
                    base_tree=release.BASELINE_OVERLAY_BASE_TREE, installed_app=app)
            saved.assert_not_called()

    def test_activate_and_verify_select_overlay_only_for_explicit_candidate_sha(self) -> None:
        candidate_sha = "a" * 40
        app = {"sha": release.BASELINE_OVERLAY_APP_SHA, "tree": "d" * 40,
               "manifest_sha256": "e" * 64}
        cursor = {"main_sha": release.BASELINE_OVERLAY_BASE_SHA,
                  "main_tree": release.BASELINE_OVERLAY_BASE_TREE,
                  "installed_app_sha": app["sha"], "installed_app_tree": app["tree"],
                  "installed_manifest_sha256": app["manifest_sha256"],
                  "source_bundle_sha256": "f" * 64}
        repository = {"main_sha": release.BASELINE_OVERLAY_BASE_SHA,
                      "main_tree": release.BASELINE_OVERLAY_BASE_TREE}
        with tempfile.TemporaryDirectory() as temporary:
            state_path = Path(temporary) / "state.json"
            config = {"repo": "/repo", "state": str(state_path), "lock": "/lock",
                      "controller_path": "/controller", "push_group": "push"}
            activation_order = []
            with mock.patch.object(release, "verify_bare_repository", return_value=repository), \
                 mock.patch.object(release, "_installed_app_identity", return_value=(app, {})), \
                 mock.patch.object(release, "_verify_production_cursor",
                                   side_effect=lambda *_a, **_kw: activation_order.append("production_cursor") or {"cursor": cursor}), \
                 mock.patch.object(release, "_verify_existing_baseline_helper_overlay",
                                   side_effect=lambda *a, **kw: activation_order.append("overlay") or {"source_bundle_sha256": "f" * 64}) as overlay, \
                 mock.patch.object(release, "_verify_controller_files") as strict, \
                 mock.patch.object(release, "atomic_json"):
                activated = release._activate_locked_no_lock(config, candidate_sha=candidate_sha)
            self.assertEqual(activated["status"], "activated")
            self.assertEqual(overlay.call_args.kwargs["candidate_sha"], candidate_sha)
            self.assertEqual(activation_order, ["overlay", "production_cursor"])
            strict.assert_not_called()

            state = release._new_state(release.BASELINE_OVERLAY_BASE_SHA,
                release.BASELINE_OVERLAY_BASE_TREE, app)
            verify_order = []
            with mock.patch.object(release, "_check_config", return_value=config), \
                 mock.patch.object(release, "_locked", return_value=nullcontext()), \
                 mock.patch.object(release, "verify_bare_repository", return_value=repository), \
                 mock.patch.object(release, "_load_state", return_value=state), \
                 mock.patch.object(release, "_installed_app_identity", return_value=(app, {})), \
                 mock.patch.object(release, "_verify_production_cursor",
                                   side_effect=lambda *_a, **_kw: verify_order.append("production_cursor") or {"cursor": cursor}), \
                 mock.patch.object(release, "_verify_existing_baseline_helper_overlay",
                                   side_effect=lambda *a, **kw: verify_order.append("overlay") or {"source_bundle_sha256": "f" * 64}) as overlay, \
                 mock.patch.object(release, "_verify_controller_files") as strict, \
                 mock.patch.object(release.legacy, "stage_readback", return_value={}), \
                 mock.patch.object(release.legacy, "verify_readback"):
                verified = release.verify(config, candidate_sha=candidate_sha)
            self.assertEqual(verified["status"], "verified")
            self.assertEqual(overlay.call_args.kwargs["candidate_sha"], candidate_sha)
            self.assertEqual(verify_order, ["overlay", "production_cursor"])
            strict.assert_not_called()

    def test_poll_without_transition_marker_still_requires_fixed_helpers(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            repo, base, _candidate, _other = make_repository(root)
            tree = release._tree(repo, base)
            state = release._new_state(base, tree,
                {"sha": base, "tree": tree, "manifest_sha256": "a" * 64})
            config = {"repo": str(repo), "state": str(root / "state.json"),
                      "lock": str(root / "lock"), "production_enabled": True,
                      "controller_path": release.DEFAULT_CONTROLLER, "push_group": "release-push-test"}
            with mock.patch.object(release.os, "geteuid", return_value=0), \
                 mock.patch.object(release, "_check_config", return_value=config), \
                 mock.patch.object(release, "_locked", return_value=nullcontext()), \
                 mock.patch.object(release, "_assert_legacy_release_path_stopped"), \
                 mock.patch.object(release, "verify_bare_repository", return_value={"bare": True}), \
                 mock.patch.object(release, "_load_state", return_value=state), \
                 mock.patch.object(release, "_verify_controller_files",
                                   side_effect=release.ControllerMaintenanceRequired("strict helper check")) as strict:
                with self.assertRaisesRegex(release.ControllerMaintenanceRequired, "strict helper"):
                    release.poll(config)
            strict.assert_called_once()

    def test_resume_baseline_reuses_verified_local_and_production_pair_on_reentry(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            config, _app_sha, baseline_sha, evidence = self._baseline_resume_fixture(root)
            app = evidence["app"]
            cursor = {"main_sha": baseline_sha, "main_tree": evidence["tree"],
                      "installed_app_sha": app["sha"], "installed_app_tree": app["tree"],
                      "installed_manifest_sha256": app["manifest_sha256"],
                      "source_bundle_sha256": evidence["bundle_meta"]["bundle_sha256"]}
            production_payload = {"cursor": cursor}
            record_calls = []
            with mock.patch.object(release.os, "geteuid", return_value=0), \
                 mock.patch.object(release, "_check_config", side_effect=lambda value: value), \
                 mock.patch.object(release, "_locked", return_value=nullcontext()), \
                 mock.patch.object(release, "_safe_directory"), \
                 mock.patch.object(release, "_assert_release_timers_stopped"), \
                 mock.patch.object(release, "verify_bare_repository", return_value={"main_sha": baseline_sha, "main_tree": evidence["tree"]}), \
                 mock.patch.object(release, "_installed_app_identity", side_effect=lambda *_a, **_kw: (app, {})), \
                 mock.patch.object(release, "_validate_baseline_identity"), \
                 mock.patch.object(release, "_verify_controller_files"), \
                 mock.patch.object(release, "_verify_saved_production_bundle", return_value={"status": "verified", "bundle_sha256": evidence["bundle_meta"]["bundle_sha256"]}) as verify_saved, \
                 mock.patch.object(release, "_record_production_cursor", side_effect=lambda *a, **kw: record_calls.append(kw) or {"status": "ready"}), \
                 mock.patch.object(release, "_verify_production_cursor", return_value=production_payload), \
                 mock.patch.object(release, "_upload_and_store_bundle") as upload, \
                 mock.patch.object(release.legacy, "command") as transfer:
                first = release.resume_baseline(config)
                # The production helper's initialize operation is create-only
                # for a new cursor and idempotent for this exact existing one.
                second = release.resume_baseline(config)

            self.assertEqual(first["status"], "baseline_resumed")
            self.assertFalse(first["bundle_uploaded"])
            self.assertFalse(first["application_deployed"])
            self.assertEqual(second["source_bundle_sha256"], first["source_bundle_sha256"])
            self.assertEqual(len(record_calls), 2)
            self.assertTrue(all(call["initialize"] is True for call in record_calls))
            self.assertEqual(verify_saved.call_count, 2)
            upload.assert_not_called()
            transfer.assert_not_called()

    def test_resume_baseline_stops_before_cursor_on_local_bundle_digest_mismatch(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            config, _app_sha, baseline_sha, evidence = self._baseline_resume_fixture(root)
            bundle = Path(config["work_root"]) / "source-bundles" / f"{baseline_sha}.bundle"
            bundle.write_bytes(bundle.read_bytes() + b"tamper")
            app = evidence["app"]
            with mock.patch.object(release.os, "geteuid", return_value=0), \
                 mock.patch.object(release, "_check_config", side_effect=lambda value: value), \
                 mock.patch.object(release, "_locked", return_value=nullcontext()), \
                 mock.patch.object(release, "_safe_directory"), \
                 mock.patch.object(release, "_assert_release_timers_stopped"), \
                 mock.patch.object(release, "verify_bare_repository", return_value={"main_sha": baseline_sha, "main_tree": evidence["tree"]}), \
                 mock.patch.object(release, "_installed_app_identity", side_effect=lambda *_a, **_kw: (app, {})), \
                 mock.patch.object(release, "_validate_baseline_identity"), \
                 mock.patch.object(release, "_verify_controller_files"), \
                 mock.patch.object(release, "_verify_saved_production_bundle") as verify_saved, \
                 mock.patch.object(release, "_record_production_cursor") as record:
                with self.assertRaisesRegex(release.ReleaseError, "identity or digest"):
                    release.resume_baseline(config)
            verify_saved.assert_not_called()
            record.assert_not_called()

    def test_resume_baseline_stops_when_existing_cursor_readback_has_another_identity(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            config, _app_sha, baseline_sha, evidence = self._baseline_resume_fixture(root)
            app = evidence["app"]
            wrong_cursor = {"main_sha": "f" * 40, "main_tree": evidence["tree"],
                            "installed_app_sha": app["sha"], "installed_app_tree": app["tree"],
                            "installed_manifest_sha256": app["manifest_sha256"],
                            "source_bundle_sha256": evidence["bundle_meta"]["bundle_sha256"]}
            with mock.patch.object(release.os, "geteuid", return_value=0), \
                 mock.patch.object(release, "_check_config", side_effect=lambda value: value), \
                 mock.patch.object(release, "_locked", return_value=nullcontext()), \
                 mock.patch.object(release, "_safe_directory"), \
                 mock.patch.object(release, "_assert_release_timers_stopped"), \
                 mock.patch.object(release, "verify_bare_repository", return_value={"main_sha": baseline_sha, "main_tree": evidence["tree"]}), \
                 mock.patch.object(release, "_installed_app_identity", side_effect=lambda *_a, **_kw: (app, {})), \
                 mock.patch.object(release, "_validate_baseline_identity"), \
                 mock.patch.object(release, "_verify_controller_files"), \
                 mock.patch.object(release, "_verify_saved_production_bundle", return_value={"status": "verified"}), \
                 mock.patch.object(release, "_record_production_cursor", return_value={"status": "ready"}), \
                 mock.patch.object(release, "_verify_production_cursor", return_value={"cursor": wrong_cursor}):
                with self.assertRaisesRegex(release.ReleaseError, "cursor does not match"):
                    release.resume_baseline(config)

    def test_receive_hook_rejects_main_deletion_non_fast_forward_and_shell_payload(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            repo, base, candidate, _other = make_repository(Path(temporary))
            release.validate_receive_updates(repo, f"{release.ZERO_SHA} {candidate} refs/heads/codex/new\n")
            for update in (
                f"{base} {candidate} refs/heads/main\n",
                f"{candidate} {release.ZERO_SHA} refs/heads/codex/one\n",
                f"{candidate} {base} refs/heads/codex/one\n",
            ):
                with self.subTest(update=update), self.assertRaises(release.ReleaseError):
                    release.validate_receive_updates(repo, update)
            with self.assertRaises(release.ReleaseError):
                release.validate_receive_updates(repo, f"{release.ZERO_SHA} {candidate} refs/heads/codex/new;touch\n")

    def test_real_git_receive_hook_accepts_codex_branch_and_rejects_main(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            repo, base, candidate, _other = make_repository(root)
            hook = repo / "hooks/pre-receive"
            module_dir = Path(release.__file__).resolve().parent
            hook.write_text(
                "#!/usr/bin/env python3\n"
                "import sys\n"
                "from pathlib import Path\n"
                f"sys.path.insert(0, {str(module_dir)!r})\n"
                "import domestic_main_release as release\n"
                f"release.validate_receive_updates(Path({str(repo)!r}), sys.stdin.read())\n",
                encoding="utf-8",
            )
            hook.chmod(0o755)
            subprocess.run(["git", f"--git-dir={repo}", "config", "receive.denyDeletes", "true"], check=True)
            subprocess.run(["git", f"--git-dir={repo}", "config", "receive.denyNonFastForwards", "true"], check=True)
            source = root / "source"
            pushed = subprocess.run(["git", "-C", str(source), "push", str(repo),
                                     f"{candidate}:refs/heads/codex/from-laptop"],
                                    text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            self.assertEqual(pushed.returncode, 0, pushed.stderr)
            self.assertEqual(git(repo, "rev-parse", "refs/heads/codex/from-laptop"), candidate)
            denied = subprocess.run(["git", "-C", str(source), "push", str(repo),
                                     f"{candidate}:refs/heads/main"],
                                    text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            self.assertNotEqual(denied.returncode, 0)
            self.assertEqual(git(repo, "rev-parse", release.MAIN_REF), base)

    def test_nested_git_object_storage_is_push_writable_but_main_and_hooks_are_not(self) -> None:
        for relative in (Path("objects"), Path("objects/pack"), Path("objects/ab"),
                         Path("refs/heads/codex"), Path("refs/heads/codex/task")):
            with self.subTest(path=str(relative)):
                self.assertTrue(release._push_writable_bare_path(relative))
        for relative in (Path("refs/heads/main"), Path("refs/domestic/candidates/abc"),
                         Path("hooks/pre-receive"), Path("config"), Path("HEAD")):
            with self.subTest(path=str(relative)):
                self.assertFalse(release._push_writable_bare_path(relative))

    def test_stale_second_branch_can_be_replaced_on_current_main_without_controller_rebase(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            repo, base, first, stale_second = make_repository(root)
            source = root / "source"
            subprocess.run(["git", "-C", str(source), "checkout", "-b", "codex/two-v2", first],
                           check=True, stdout=subprocess.DEVNULL)
            (source / "two-v2.txt").write_text("second based on current main\n")
            subprocess.run(["git", "-C", str(source), "add", "two-v2.txt"], check=True)
            subprocess.run(["git", "-C", str(source), "commit", "-m", "updated second candidate"],
                           check=True, stdout=subprocess.DEVNULL)
            updated_second = subprocess.check_output(["git", "-C", str(source), "rev-parse", "HEAD"], text=True).strip()
            subprocess.run(["git", f"--git-dir={repo}", "fetch", "--no-tags", str(source),
                            f"refs/heads/codex/two-v2:refs/heads/codex/two-v2"], check=True)
            state = release._new_state(base, release._tree(repo, base), {
                "sha": base, "tree": release._tree(repo, base), "manifest_sha256": "a" * 64,
            })
            state["status"] = "ready"
            state["queue"] = [
                {"candidate_id": first, "ref": "refs/heads/codex/one", "head_sha": first,
                 "base_sha": base, "status": "completed"},
                {"candidate_id": stale_second, "ref": "refs/heads/codex/two", "head_sha": stale_second,
                 "base_sha": base, "status": "pending"},
            ]
            release._advance_main_cas(repo, base, first)  # Candidate A has completed its serial release.
            state["main"] = {"sha": first, "tree": release._tree(repo, first)}
            state_file, lock_file = root / "state.json", root / "release.lock"
            stale_result = release.process_candidate({"repo": str(repo)}, state_file, state, state["queue"][1])
            self.assertEqual(stale_result["status"], "stale_base")
            self.assertEqual(state["queue"][1]["status"], "stale_base")
            with mock.patch.object(release, "_load_state", return_value=state):
                with self.assertRaises(release.ReleaseError):
                    release.submit_candidate(repo, state_file, "refs/heads/codex/two-v2",
                                             updated_second, first, lock_file)
                result = release.submit_candidate(repo, state_file, "refs/heads/codex/two-v2",
                                                  updated_second, first, lock_file,
                                                  supersedes_candidate_id=stale_second)
            self.assertEqual(result["status"], "pending")
            self.assertEqual(state["queue"][1]["head_sha"], updated_second)
            self.assertEqual(state["queue"][1]["base_sha"], first)
            self.assertEqual(state["queue"][1]["supersedes_ref"], "refs/heads/codex/two")
            self.assertEqual(state["queue"][1]["status"], "pending")

    def test_stage_reset_ack_requires_both_hosts_on_last_verified_app(self) -> None:
        installed = {"sha": "a" * 40, "tree": "b" * 40, "manifest_sha256": "c" * 64}
        state = release._new_state("d" * 40, "e" * 40, installed)
        state.update({"status": "blocked", "staging_out_of_sync": True})
        release._validate_stage_reset(state, dict(installed), dict(installed))
        with self.assertRaises(release.ReleaseError):
            release._validate_stage_reset(state, {**installed, "sha": "f" * 40}, dict(installed))
        state["staging_out_of_sync"] = False
        with self.assertRaises(release.ReleaseError):
            release._validate_stage_reset(state, dict(installed), dict(installed))

    def test_stage_install_failure_records_manual_synthetic_reset_requirement(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            state = release._new_state("a" * 40, "b" * 40, {
                "sha": "c" * 40, "tree": "d" * 40, "manifest_sha256": "e" * 64,
            })
            state["in_flight"] = {"stage_install_started": True}
            item = {"status": "checking"}
            release._mark_failed(Path(temporary) / "state.json", state, item, RuntimeError(), "stage-smoke")
            self.assertTrue(state["staging_out_of_sync"])
            self.assertIsNone(state["in_flight"])
            self.assertIn("rebuild the synthetic staging database", item["failure"]["required_recovery"])

    def test_reconcile_preserves_installed_app_for_docs_only_candidate(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            repo, base, candidate, _other = make_repository(Path(temporary))
            app = {"sha": base, "tree": release._tree(repo, base), "manifest_sha256": "a" * 64}
            inflight = {
                "candidate_sha": candidate, "candidate_tree": release._tree(repo, candidate),
                "previous_main_sha": base, "runtime_changed": False,
                "installed_app": app, "previous_installed_app": app,
                "bundle_meta": {"source_sha": candidate, "source_tree": release._tree(repo, candidate),
                                "previous_main_sha": base, "bundle_sha256": "b" * 64,
                                "self_contained": True, "verified_from_empty_repository": True},
            }
            identity = release._reconcile_identity(repo, inflight)
            self.assertFalse(identity[-1])
            self.assertEqual(identity[4], app)

    def test_restricted_ssh_only_executes_exact_upload_pack_command(self) -> None:
        exact = f"git-upload-pack '{release.DEFAULT_REPO}'"
        with mock.patch.dict(os.environ, {"SSH_ORIGINAL_COMMAND": exact,
                                          "GIT_CONFIG_COUNT": "1", "GIT_CONFIG_KEY_0": "core.hooksPath",
                                          "GIT_CONFIG_VALUE_0": "/tmp/unsafe"}), \
             mock.patch.object(release.os, "execve", side_effect=SystemExit(0)) as execve:
            with self.assertRaises(SystemExit):
                release.restricted_ssh()
            args = execve.call_args.args
            self.assertEqual(args[:2], (
                "/usr/bin/git", ["git", "-c", f"safe.directory={release.DEFAULT_REPO}",
                                 "upload-pack", release.DEFAULT_REPO]))
            self.assertNotIn("GIT_CONFIG_COUNT", args[2])

        for command in (
            f"git-upload-pack '{release.DEFAULT_REPO}'; touch /tmp/no",
            "git-upload-pack /etc/passwd",
            f"git-receive-pack '{release.DEFAULT_REPO}' --config core.hooksPath=/tmp",
        ):
            with mock.patch.dict(os.environ, {"SSH_ORIGINAL_COMMAND": command}), \
                 mock.patch.object(release.os, "execve") as execve, \
                 self.assertRaises(release.ReleaseError):
                release.restricted_ssh()
                execve.assert_not_called()

    def test_restricted_ssh_archive_ack_passes_sha_only_over_stdin(self) -> None:
        sha = "a" * 40
        result = subprocess.CompletedProcess([], 0, '{"status":"recorded"}', "")
        with mock.patch.dict(os.environ, {"SSH_ORIGINAL_COMMAND": f"domestic-archive-ack --sha {sha}"}), \
             mock.patch.object(release, "_run", return_value=result) as run, \
             redirect_stdout(io.StringIO()):
            release.restricted_ssh()

        args, kwargs = run.call_args
        self.assertEqual(args[0], ["/usr/bin/sudo", "-n", "/usr/bin/python3", release.DEFAULT_CONTROLLER,
                                   "archive-ack-stdin", "--config", release.DEFAULT_CONFIG])
        self.assertEqual(kwargs, {"input_text": json.dumps({"sha": sha}) + "\n", "timeout": 60})
        self.assertNotIn(sha, args[0])

    def test_restricted_ssh_archive_ack_rejects_extra_or_malformed_arguments(self) -> None:
        sha = "a" * 40
        commands = (
            f"domestic-archive-ack --sha {sha} --config /etc/other.json",
            f"domestic-archive-ack --sha {sha} extra",
            f"domestic-archive-ack --sha {sha}; touch /tmp/no",
            f"domestic-archive-ack --sha {sha.upper()}",
            "domestic-archive-ack --sha " + "a" * 39,
            f"domestic-archive-ack --sha {sha} --sha {sha}",
        )
        for command in commands:
            with self.subTest(command=command), \
                 mock.patch.dict(os.environ, {"SSH_ORIGINAL_COMMAND": command}), \
                 mock.patch.object(release, "_run") as run, \
                 self.assertRaises(release.ReleaseError):
                release.restricted_ssh()
                run.assert_not_called()

    def test_archive_ack_stdin_accepts_only_one_exact_sha_field(self) -> None:
        config = {"repo": release.DEFAULT_REPO}
        sha = "b" * 40
        with mock.patch.object(release.os, "geteuid", return_value=0), \
             mock.patch.object(release, "_check_config", return_value=config) as check_config, \
             mock.patch.object(release, "archive_ack", return_value={"status": "recorded"}) as ack, \
             mock.patch.object(release.sys, "stdin", stdin_with_bytes((json.dumps({"sha": sha}) + "\n").encode())):
            result = release._archive_ack_stdin(config)

        self.assertEqual(result, {"status": "recorded"})
        check_config.assert_called_once_with(config)
        ack.assert_called_once_with(config, sha)

    def test_archive_ack_stdin_rejects_malicious_or_ambiguous_json(self) -> None:
        valid_sha = "c" * 40
        invalid_payloads = (
            b"",
            b"[]",
            b"null",
            json.dumps({"sha": valid_sha, "extra": "ignored"}).encode(),
            ('{"sha":"' + valid_sha + '","sha":"' + "d" * 40 + '"}').encode(),
            (json.dumps({"sha": valid_sha}) + "\n{}").encode(),
            json.dumps({"sha": "C" * 40}).encode(),
            json.dumps({"sha": "x" * 40}).encode(),
            json.dumps({"sha": 123}).encode(),
            b" " * 513,
            ("é" * 257).encode("utf-8"),
            b"\xff",
        )
        for payload in invalid_payloads:
            with self.subTest(payload=repr(payload[:80])), \
                 mock.patch.object(release.os, "geteuid", return_value=0), \
                 mock.patch.object(release, "_check_config") as check_config, \
                 mock.patch.object(release, "archive_ack") as ack, \
                 mock.patch.object(release.sys, "stdin", stdin_with_bytes(payload)):
                with self.assertRaises(release.ReleaseError):
                    release._archive_ack_stdin({})
                check_config.assert_not_called()
                ack.assert_not_called()

    def test_archive_ack_stdin_rejects_non_root_invocation(self) -> None:
        with mock.patch.object(release.os, "geteuid", return_value=1000), \
             mock.patch.object(release, "archive_ack") as ack, \
             mock.patch.object(release.sys, "stdin", stdin_with_bytes((('{"sha":"' + "e" * 40 + '"}').encode()))):
            with self.assertRaisesRegex(release.ReleaseError, "fixed root-mediated endpoint"):
                release._archive_ack_stdin({})
        ack.assert_not_called()

    def test_archive_ack_stdin_rejects_non_binary_input_stream(self) -> None:
        with mock.patch.object(release.os, "geteuid", return_value=0), \
             mock.patch.object(release, "archive_ack") as ack, \
             mock.patch.object(release.sys, "stdin", io.StringIO('{"sha":"' + "f" * 40 + '"}')):
            with self.assertRaises(release.ReleaseError):
                release._archive_ack_stdin({})
        ack.assert_not_called()

    def test_generated_trusted_runner_is_valid_python(self) -> None:
        compile(release._trusted_runner_code(), "<trusted-runner>", "exec")

    def test_controller_and_selection_policy_changes_are_not_self_certified(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            repo, base, candidate, _other = make_repository(root)
            worktree = root / "worktree"
            subprocess.run(["git", "--git-dir", str(repo), "worktree", "add", "--detach", str(worktree), candidate], check=True, stdout=subprocess.DEVNULL)
            self.assertEqual(release._check_policy_changes(repo, base, candidate), [])
            (worktree / "scripts").mkdir()
            (worktree / "scripts/ci").mkdir()
            (worktree / "scripts/ci/quality_lanes.py").write_text("# changed\n")
            subprocess.run(["git", "-C", str(worktree), "add", "scripts/ci/quality_lanes.py"], check=True)
            subprocess.run(["git", "-C", str(worktree), "commit", "-m", "change checker"], check=True, stdout=subprocess.DEVNULL)
            head = subprocess.check_output(["git", "-C", str(worktree), "rev-parse", "HEAD"], text=True).strip()
            self.assertEqual(release._check_policy_changes(repo, base, head), ["scripts/ci/quality_lanes.py"])

    def test_unknown_state_requires_prospective_app_and_bundle_proof(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            repo, base, candidate, _other = make_repository(Path(temporary))
            state = release._new_state(base, release._tree(repo, base), {
                "sha": base, "tree": release._tree(repo, base), "manifest_sha256": "a" * 64,
            })
            state["status"] = "outcome_unknown"
            state["in_flight"] = {
                "candidate_id": candidate, "ref": "refs/heads/codex/one",
                "head_sha": candidate, "head_tree": release._tree(repo, candidate),
                "base_sha": base, "base_tree": release._tree(repo, base),
                "candidate_sha": candidate, "candidate_tree": release._tree(repo, candidate),
                "previous_main_sha": base, "previous_installed_app": state["installed_app"],
                "installed_app": {"sha": candidate, "tree": release._tree(repo, candidate), "manifest_sha256": "b" * 64},
                "previous_installed_app": state["installed_app"],
                "runtime_changed": True,
                "release_metadata": {"source_sha": candidate, "source_tree": release._tree(repo, candidate), "release_files_sha256": "b" * 64},
                "bundle_meta": {"source_sha": candidate, "source_tree": release._tree(repo, candidate),
                                "previous_main_sha": base, "bundle_sha256": "c" * 64,
                                "self_contained": True, "verified_from_empty_repository": True},
                "production_install_started": True,
            }
            release._validate_state(state)
            identity = release._reconcile_identity(repo, state["in_flight"])
            self.assertEqual(identity[0], candidate)
            state["in_flight"].pop("previous_installed_app")
            with self.assertRaises(release.ReleaseError):
                release._reconcile_identity(repo, state["in_flight"])


if __name__ == "__main__":
    unittest.main()
