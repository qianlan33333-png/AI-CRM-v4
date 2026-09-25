from __future__ import annotations

import json
import os
from pathlib import Path
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


class DomesticMainReleaseTests(unittest.TestCase):
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
        env = release._check_env({**base, "build_path": "/opt/aicrm/toolchain/go/bin:/opt/aicrm/toolchain/node/bin:/usr/bin:/bin"})
        self.assertEqual(env["PATH"], "/opt/aicrm/toolchain/go/bin:/opt/aicrm/toolchain/node/bin:/usr/bin:/bin")

    def test_build_toolchain_probe_runs_as_isolated_user_and_requires_all_tools(self) -> None:
        config = {"check_database_url": "postgresql://localhost/aicrm_ci",
                  "build_path": "/opt/go/bin:/opt/node/bin:/usr/bin:/bin"}
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
            repo, base, candidate, _other = make_repository(root)
            git(repo, "update-ref", release.MAIN_REF, candidate, base)
            release._pin_candidate(repo, candidate)
            base_tree = release._tree(repo, base)
            candidate_tree = release._tree(repo, candidate)
            app = {"sha": base, "tree": base_tree, "manifest_sha256": "a" * 64}
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
        with mock.patch.dict(os.environ, {"SSH_ORIGINAL_COMMAND": exact}), \
             mock.patch.object(release.os, "execv", side_effect=SystemExit(0)) as execv:
            with self.assertRaises(SystemExit):
                release.restricted_ssh()
            execv.assert_called_once_with(
                "/usr/bin/git", ["git", "-c", f"safe.directory={release.DEFAULT_REPO}",
                                 "upload-pack", release.DEFAULT_REPO],
            )

        for command in (
            f"git-upload-pack '{release.DEFAULT_REPO}'; touch /tmp/no",
            "git-upload-pack /etc/passwd",
            f"git-receive-pack '{release.DEFAULT_REPO}' --config core.hooksPath=/tmp",
        ):
            with mock.patch.dict(os.environ, {"SSH_ORIGINAL_COMMAND": command}), \
                 mock.patch.object(release.os, "execv") as execv, \
                 self.assertRaises(release.ReleaseError):
                release.restricted_ssh()
                execv.assert_not_called()

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
