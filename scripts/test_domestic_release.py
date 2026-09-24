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
main_full_regression_blocker = worker._main_full_regression_blocker


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
    def setUp(self):
        self.release_gate = mock.patch.object(worker, "_main_full_regression_blocker", return_value=None)
        self.release_gate.start()

    def tearDown(self):
        self.release_gate.stop()

    @staticmethod
    def _ci_run(sha, *, run_id=1, event="push", attempt=1, started="2026-09-24T10:00:00Z", marker=True):
        return {
            "id": run_id,
            "run_attempt": attempt,
            "head_sha": sha,
            "head_branch": "main",
            "path": worker.CI_WORKFLOW_PATH,
            "event": event,
            "display_title": "[force_full] manual recovery" if event == "workflow_dispatch" else "main CI",
            "run_started_at": started,
            "created_at": started,
            "updated_at": started,
            "marker": marker,
        }

    @staticmethod
    def _ci_jobs(sha, *, attempt=1, lane_conclusion="success", marker=True):
        jobs = {}
        for name in worker.CI_REQUIRED_JOBS:
            conclusion = lane_conclusion if name in worker.CI_FULL_LANES else "success"
            job = {
                "name": name,
                "head_sha": sha,
                "run_attempt": attempt,
                "status": "completed",
                "conclusion": conclusion,
                "steps": [],
            }
            if name == "check" and marker:
                # This step is best-effort reporting; its own conclusion must
                # not override success of the exact required jobs.
                job["steps"] = [{"name": worker.CI_FULL_RESULT_STEP, "conclusion": "failure"}]
            jobs[name] = job
        return jobs

    def test_main_full_ci_requires_exact_attempt_jobs_and_ignores_marker_step_result(self):
        sha = "a" * 40
        run = self._ci_run(sha)
        jobs = self._ci_jobs(sha)
        self.assertEqual(worker._main_ci_run_classification(run, jobs)[0], "success")
        jobs["browser"]["head_sha"] = "b" * 40
        self.assertEqual(worker._main_ci_run_classification(run, jobs)[0], "unknown")

    def test_main_verified_schedule_cannot_clear_and_rerun_with_skipped_lanes_is_unknown(self):
        sha = "a" * 40
        run = self._ci_run(sha, event="schedule")
        jobs = self._ci_jobs(sha, lane_conclusion="skipped")
        self.assertEqual(worker._main_ci_run_classification(run, jobs)[0], "verified")
        run["run_attempt"] = 2
        for job in jobs.values():
            job["run_attempt"] = 2
        self.assertEqual(worker._main_ci_run_classification(run, jobs)[0], "unknown")

    def test_legacy_main_push_without_regression_marker_does_not_bootstrap_pause(self):
        sha = "a" * 40
        run = self._ci_run(sha, marker=False)
        jobs = self._ci_jobs(sha, lane_conclusion="failure", marker=False)
        jobs["governance"]["conclusion"] = "failure"
        self.assertEqual(worker._main_ci_run_classification(run, jobs)[0], "not_full")

    def test_schedule_or_force_full_missing_regression_marker_is_unknown(self):
        sha = "a" * 40
        for event in ("schedule", "workflow_dispatch"):
            with self.subTest(event=event):
                run = self._ci_run(sha, event=event, marker=False)
                jobs = self._ci_jobs(sha, marker=False)
                self.assertEqual(worker._main_ci_run_classification(run, jobs)[0], "unknown")

    def _scan_main_ci_runs(self, runs, jobs_by_id, candidate_sha, head_sha):
        with (
            mock.patch.object(worker, "_main_first_parent_positions", return_value={head_sha: 2, "b" * 40: 1, "a" * 40: 0}),
            mock.patch.object(worker, "_main_ci_run_pages", return_value=iter([runs])),
            mock.patch.object(worker, "_main_ci_run_jobs", side_effect=lambda run: jobs_by_id[run["id"]]),
        ):
            return main_full_regression_blocker(Path("/unused"), candidate_sha, head_sha)

    def test_later_full_success_on_descendant_clears_earlier_failure(self):
        old, descendant, head = "a" * 40, "b" * 40, "c" * 40
        failed = self._ci_run(old, run_id=1, started="2026-09-24T09:00:00Z")
        succeeded = self._ci_run(descendant, run_id=2, started="2026-09-24T10:00:00Z")
        jobs = {
            1: self._ci_jobs(old, lane_conclusion="failure"),
            2: self._ci_jobs(descendant),
        }
        self.assertIsNone(self._scan_main_ci_runs([failed, succeeded], jobs, old, head))

    def test_newer_full_failure_is_not_cleared_by_older_success(self):
        old, descendant, head = "a" * 40, "b" * 40, "c" * 40
        succeeded = self._ci_run(old, run_id=1, started="2026-09-24T09:00:00Z")
        failed = self._ci_run(descendant, run_id=2, started="2026-09-24T10:00:00Z")
        jobs = {
            1: self._ci_jobs(old),
            2: self._ci_jobs(descendant, lane_conclusion="failure"),
        }
        blocker = self._scan_main_ci_runs([succeeded, failed], jobs, descendant, head)
        self.assertEqual(blocker["sha"], descendant)

    def test_future_failure_does_not_block_earlier_queue_candidate(self):
        candidate, head = "b" * 40, "c" * 40
        failed = self._ci_run(head, run_id=3, started="2026-09-24T10:00:00Z")
        blocker = self._scan_main_ci_runs([failed], {3: self._ci_jobs(head, lane_conclusion="failure")}, candidate, head)
        self.assertIsNone(blocker)

    def test_verified_full_skip_does_not_clear_failure(self):
        old, descendant, head = "a" * 40, "b" * 40, "c" * 40
        failed = self._ci_run(old, run_id=1, started="2026-09-24T09:00:00Z")
        verified = self._ci_run(descendant, run_id=2, event="schedule", started="2026-09-24T10:00:00Z")
        jobs = {
            1: self._ci_jobs(old, lane_conclusion="failure"),
            2: self._ci_jobs(descendant, lane_conclusion="skipped"),
        }
        blocker = self._scan_main_ci_runs([failed, verified], jobs, old, head)
        self.assertEqual(blocker["sha"], old)

    def test_public_history_read_failure_is_marked_transient_for_bounded_retry(self):
        sha = "a" * 40
        with (
            mock.patch.object(worker, "_main_first_parent_positions", return_value={sha: 0}),
            mock.patch.object(worker, "_main_ci_run_pages", side_effect=worker.CIHistoryReadError("temporary")),
        ):
            blocker = main_full_regression_blocker(Path("/unused"), sha, sha)
        self.assertTrue(blocker["transient_read_error"])

    def test_transient_history_pause_retries_after_deadline_without_new_check_run(self):
        sha = "a" * 40
        signature = {"head_sha": sha, "run_id": 7, "started_at": "2026-09-24T10:00:00Z", "completed_at": "2026-09-24T10:01:00Z", "status": "completed", "conclusion": "success"}
        state = {
            "status": "ci_regression_blocked",
            "ci_regression_pause": {
                "candidate_sha": sha,
                "main_head_sha": sha,
                "blocker": {"classification": "unknown", "transient_read_error": True},
                "watched_check": signature,
                "retry_after_utc": "2026-09-24T09:00:00Z",
            },
        }
        with tempfile.TemporaryDirectory() as temporary:
            state_path = Path(temporary) / "state.json"
            with (
                mock.patch.object(worker, "_ci_retry_is_due", return_value=True),
                mock.patch.object(worker, "exact_check_success", side_effect=lambda _sha, observation=None, **_kw: (observation.update(signature) or True)),
                mock.patch.object(worker, "_main_full_regression_blocker", return_value=None) as history,
            ):
                self.assertTrue(worker._retry_ci_regression_pause(state_path, state, Path(temporary), sha))
            history.assert_called_once()
            self.assertEqual(state["status"], "ready")
            self.assertNotIn("ci_regression_pause", state)

    def test_unresolved_regression_does_not_rescan_until_check_changes(self):
        sha = "a" * 40
        signature = {"head_sha": sha, "run_id": 7, "started_at": "2026-09-24T10:00:00Z", "completed_at": "2026-09-24T10:01:00Z", "status": "completed", "conclusion": "success"}
        state = {"status": "ci_regression_blocked", "ci_regression_pause": {"candidate_sha": sha, "main_head_sha": sha, "blocker": {"classification": "unknown", "reason": "full lane failed"}, "watched_check": signature}}
        with tempfile.TemporaryDirectory() as temporary:
            with (
                mock.patch.object(worker, "exact_check_success", side_effect=lambda _sha, observation=None, **_kw: (observation.update(signature) or True)),
                mock.patch.object(worker, "_main_full_regression_blocker") as history,
            ):
                self.assertFalse(worker._retry_ci_regression_pause(Path(temporary) / "state.json", state, Path(temporary), sha))
            history.assert_not_called()

    def test_exact_check_api_error_sets_one_bounded_retry_without_erasing_blocker(self):
        sha = "a" * 40
        state = {
            "status": "ci_regression_blocked",
            "ci_regression_pause": {
                "candidate_sha": sha,
                "main_head_sha": sha,
                "blocker": {"classification": "unknown", "reason": "observed full lane failure"},
                "watched_check": {"head_sha": sha, "run_id": 7, "status": "completed", "conclusion": "success"},
            },
        }
        with tempfile.TemporaryDirectory() as temporary:
            state_path = Path(temporary) / "state.json"
            with mock.patch.object(worker, "exact_check_success", side_effect=OSError("rate limited")) as check:
                self.assertFalse(worker._retry_ci_regression_pause(state_path, state, Path(temporary), sha))
                retry_at = state["ci_regression_pause"]["retry_after_utc"]
                self.assertFalse(worker._retry_ci_regression_pause(state_path, state, Path(temporary), sha))
            self.assertEqual(check.call_count, 1)
            self.assertEqual(state["ci_regression_pause"]["blocker"]["reason"], "observed full lane failure")
            self.assertTrue(worker._parse_utc(retry_at))

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

    def test_installed_smoke_environment_is_a_complete_synthetic_allowlist(self):
        runtime_tmp = Path("/tmp/smoke-runtime")
        with mock.patch.object(installer.pwd, "getpwnam", return_value=SimpleNamespace(pw_dir="/home/ubuntu")):
            environment = installer.staging_smoke_test_environment(runtime_tmp)
        self.assertEqual(environment["GOMAXPROCS"], "2")
        self.assertEqual(environment["GOPROXY"], "off")
        self.assertEqual(environment["GOSUMDB"], "off")
        self.assertEqual(environment["HOME"], "/home/ubuntu")
        self.assertEqual(environment["TMPDIR"], str(runtime_tmp))
        self.assertEqual(
            environment["PATH"],
            f"{installer.SMOKE_GO.parent}:{installer.SMOKE_NODE.parent}:/usr/bin:/bin",
        )
        self.assertEqual(
            set(environment),
            {
                "HOME", "PATH", "GOCACHE", "GOMODCACHE", "GOTOOLCHAIN", "GOMAXPROCS", "GOPROXY",
                "GOSUMDB", "GOWORK", "TMPDIR",
            },
        )
        self.assertFalse(any(name.startswith("AICRM_") for name in environment))
        self.assertNotIn("GITHUB_TOKEN", environment)
        self.assertNotIn("AICRM_ALIPAY_PRIVATE_KEY", environment)

    def test_installed_smoke_inputs_are_private_file_not_environment_or_argv_values(self):
        connection = "postgres://smoke:synthetic-secret@127.0.0.1/aicrm_test?sslmode=disable"
        with tempfile.TemporaryDirectory(prefix="domestic-smoke-inputs-") as temporary:
            path = Path(temporary) / "inputs.json"
            installer._write_staging_smoke_inputs(
                path,
                database_url=connection,
                source_sha="a" * 40,
                installed_sha="b" * 40,
                installed_binary=Path("/opt/aicrm/releases") / ("b" * 40) / "bin/aicrm",
                installed_binary_sha256="c" * 64,
                uid=os.getuid(),
                gid=os.getgid(),
            )
            self.assertEqual(path.stat().st_mode & 0o777, 0o400)
            self.assertEqual(path.stat().st_uid, os.getuid())
            inputs = installer.json.loads(path.read_text())
            self.assertEqual(inputs["database_url"], connection)
            self.assertEqual(inputs["stage_role"], "staging")

    def test_installed_smoke_runner_passes_only_private_input_path_to_go_test(self):
        calls = []

        def run_command(command, source_root, environment, **kwargs):
            calls.append((command, environment, kwargs))
            if kwargs["label"] == "installed staging smoke":
                return installer.SMOKE_TEST_MARKER
            return "prepared"

        with tempfile.TemporaryDirectory(prefix="domestic-smoke-runner-contract-") as temporary:
            root = Path(temporary)
            smoke_input = root / "installed-smoke-input.json"
            source_root = root / "source"
            source_root.mkdir()
            git_dir = root / "repository-git"
            git_dir.mkdir()
            (git_dir / "index").write_bytes(b"live repository index\n")
            source_index = root / "source.index"
            source_index.write_bytes(b"checked source index\n")
            source_index.chmod(0o444)
            installer._attach_smoke_source_git_metadata(source_root, source_index, str(git_dir))
            with mock.patch.object(installer, "_run_unprivileged_smoke_command", side_effect=run_command), mock.patch.object(installer, "_verify_smoke_source_snapshot") as verify:
                installer._run_installed_smoke_test(
                    source_root,
                    {"HOME": "/home/ubuntu", "PATH": "/fixed/go:/fixed/node:/usr/bin:/bin", "TMPDIR": str(root)},
                    str(git_dir), source_index, "/fixed/node/node", smoke_input,
                )

        self.assertEqual(len(calls), 2)
        source_command, source_environment, _source_options = calls[0]
        test_command, test_environment, options = calls[1]
        self.assertIn("-args", test_command)
        self.assertIn(f"-domestic-release-smoke-input={smoke_input}", test_command)
        self.assertNotIn("AICRM_DATABASE_URL", test_environment)
        self.assertNotIn("synthetic-secret", repr(test_command))
        self.assertEqual(test_environment["HOME"], "/home/ubuntu")
        self.assertEqual(test_environment["PATH"], "/fixed/go:/fixed/node:/usr/bin:/bin")
        self.assertEqual(source_environment["GIT_INDEX_FILE"], str(source_index.resolve()))
        self.assertEqual(options["required_marker"], installer.SMOKE_TEST_MARKER)
        self.assertEqual(verify.call_count, 2)

    def test_installed_smoke_requires_the_contract_pass_marker(self):
        process = mock.Mock(pid=123456789, wait=mock.Mock(return_value=0), poll=mock.Mock(return_value=0))

        def start_process(_args, **kwargs):
            kwargs["stdout"].write(b"ok   cmd/aicrm\n")
            return process

        with mock.patch.object(installer.subprocess, "Popen", side_effect=start_process), mock.patch.object(installer, "_stop_smoke_process_group", return_value=False):
            with self.assertRaisesRegex(RuntimeError, "required pass marker"):
                installer._run_unprivileged_smoke_command(
                    ("/bin/true",), Path("/tmp/verified-source"), {},
                    timeout_seconds=10,
                    required_marker=installer.SMOKE_TEST_MARKER,
                    label="installed staging smoke",
                )

    def test_installed_smoke_failures_write_private_redacted_diagnostics(self):
        with tempfile.TemporaryDirectory(prefix="domestic-smoke-diagnostics-test-") as temporary:
            root = Path(temporary)
            diagnostic_root = root / "private-logs"
            connection = "postgres://smoke:synthetic-secret@127.0.0.1/aicrm_test?sslmode=disable"
            labels = ("staging smoke source preparation", "installed staging smoke")

            for index, label in enumerate(labels):
                process = mock.Mock(pid=120000 + index, wait=mock.Mock(return_value=2), poll=mock.Mock(return_value=2))

                def start_process(_args, **kwargs):
                    kwargs["stdout"].write(f"fixture failed for {connection}".encode())
                    kwargs["stderr"].write(b"synthetic provider fixture rejected request")
                    return process

                real_lstat = Path.lstat

                def trusted_root_lstat(path):
                    if path == diagnostic_root:
                        return SimpleNamespace(
                            st_mode=installer.stat.S_IFDIR | 0o700,
                            st_uid=0,
                            st_gid=0,
                        )
                    return real_lstat(path)

                with (
                    mock.patch.object(installer, "SMOKE_DIAGNOSTIC_ROOT", diagnostic_root),
                    mock.patch.object(installer.os, "geteuid", return_value=0),
                    mock.patch.object(installer.Path, "lstat", trusted_root_lstat),
                    mock.patch.object(installer.subprocess, "Popen", side_effect=start_process),
                    mock.patch.object(installer, "_stop_smoke_process_group", return_value=False),
                ):
                    with self.assertRaisesRegex(RuntimeError, "exit status 2; diagnostic=") as raised:
                        installer._run_unprivileged_smoke_command(
                            ("/fixed/tool",), root / "source",
                            {"AICRM_DATABASE_URL": connection},
                            timeout_seconds=10,
                            label=label,
                        )
                self.assertNotIn("synthetic-secret", str(raised.exception))
                diagnostic_path = Path(str(raised.exception).split("diagnostic=", 1)[1])
                self.assertEqual(diagnostic_path.parent, diagnostic_root)
                self.assertEqual(diagnostic_path.stat().st_mode & 0o777, 0o600)
                diagnostic = diagnostic_path.read_text()
                self.assertIn(label, diagnostic)
                self.assertIn("[REDACTED]", diagnostic)
                self.assertNotIn("synthetic-secret", diagnostic)
                self.assertNotIn(connection, diagnostic)

    def test_smoke_source_snapshot_is_bound_to_git_tree_and_detects_mutation(self):
        with tempfile.TemporaryDirectory(prefix="domestic-smoke-source-identity-") as temporary:
            root = Path(temporary)
            repository = root / "repository"
            repository.mkdir()
            subprocess.run(["git", "-C", str(repository), "init", "-q"], check=True)
            subprocess.run(["git", "-C", str(repository), "config", "user.name", "Smoke Fixture"], check=True)
            subprocess.run(["git", "-C", str(repository), "config", "user.email", "smoke@example.invalid"], check=True)
            (repository / "contract.txt").write_text("committed source\n")
            subprocess.run(["git", "-C", str(repository), "add", "contract.txt"], check=True)
            subprocess.run(["git", "-C", str(repository), "commit", "-qm", "smoke source"], check=True)
            sha = subprocess.check_output(["git", "-C", str(repository), "rev-parse", "HEAD"], text=True).strip()
            git_dir = subprocess.check_output(["git", "-C", str(repository), "rev-parse", "--absolute-git-dir"], text=True).strip()
            with tempfile.TemporaryDirectory(dir=root) as scratch_name:
                scratch = Path(scratch_name)
                archive = root / "source.tar"
                archive.write_bytes(subprocess.check_output(["git", "-C", str(repository), "archive", "--format=tar", sha]))
                source_root = scratch / "source"
                installer._extract_source_archive(archive, source_root)
                with mock.patch.object(installer, "SMOKE_SOURCE_REPOSITORY", repository):
                    index = installer._smoke_source_index(sha, source_root, scratch, git_dir)
                    installer._verify_smoke_source_snapshot(source_root, git_dir, index)
                    (source_root / "contract.txt").chmod(0o644)
                    (source_root / "contract.txt").write_text("mutated source\n")
                    with self.assertRaisesRegex(RuntimeError, "no longer matches"):
                        installer._verify_smoke_source_snapshot(source_root, git_dir, index)

    def test_archive_smoke_source_prepares_views_with_exact_read_only_index_in_real_node(self):
        node = installer.shutil.which("node")
        self.assertIsNotNone(node, "the real Node integration test requires Node.js")
        with tempfile.TemporaryDirectory(prefix="domestic-smoke-source-views-") as temporary:
            root = Path(temporary)
            repository = root / "repository"
            repository.mkdir()
            source = b"frozen canonical donor payload\n"
            source_blob = hashlib.sha1(b"blob " + str(len(source)).encode() + b"\0" + source).hexdigest()
            source_sha256 = hashlib.sha256(source).hexdigest()
            library = {
                "id": "fixture-library",
                "source_repository": "https://example.invalid/frozen.git",
                "source_commit": "89abcdef0123456789abcdef0123456789abcdef",
                "root": "sources",
                "immutable": True,
                "authority_kind": "frozen_donor",
            }
            content = {
                "id": "health",
                "library_id": library["id"],
                "canonical_path": "sources/health.schemas.ts",
                "source_path": "web/src/api/generated/health.schemas.ts",
                "source_git_blob_sha": source_blob,
                "content_sha256": source_sha256,
                "bytes": len(source),
                "mode": "100644",
            }
            index_document = {
                "schema_version": 1,
                "lock_path": "source-lock.json",
                "libraries": [library],
                "contents": [content],
                "bindings": [{
                    "module": "fixture",
                    "logical_path": "tracked/health.schemas.ts",
                    "content_id": "health",
                    "source_repository": library["source_repository"],
                    "source_commit": library["source_commit"],
                    "source_path": content["source_path"],
                    "source_git_blob_sha": source_blob,
                    "mode": "100644",
                    "usage": "frozen_donor_compatibility_view",
                    "freeze_gate": "scripts/check-fixture.sh",
                    "freeze_ledger": "docs/fixture-ledger.txt",
                    "current_path_state": "tracked_pre_p4_removal",
                }],
                "views": [{
                    "target_path": "views/health.schemas.ts",
                    "content_id": "health",
                    "enabled": True,
                }],
            }
            lock_entry = {
                "id": content["id"],
                "library_id": library["id"],
                "source_repository": library["source_repository"],
                "source_commit": library["source_commit"],
                "canonical_path": content["canonical_path"],
                "source_path": content["source_path"],
                "source_git_blob_sha": source_blob,
                "content_sha256": source_sha256,
                "bytes": len(source),
                "mode": "100644",
            }
            for relative in ("sources/health.schemas.ts", "tracked/health.schemas.ts"):
                target = repository / relative
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_bytes(source)
            (repository / "source-index.json").write_text(json.dumps(index_document, indent=2) + "\n")
            (repository / "source-lock.json").write_text(json.dumps({"schema_version": 1, "entries": [lock_entry]}, indent=2) + "\n")
            subprocess.run(["git", "-C", str(repository), "init", "-q"], check=True)
            subprocess.run(["git", "-C", str(repository), "config", "user.name", "Smoke Fixture"], check=True)
            subprocess.run(["git", "-C", str(repository), "config", "user.email", "smoke@example.invalid"], check=True)
            subprocess.run(["git", "-C", str(repository), "remote", "add", "origin", next(iter(installer.SMOKE_SOURCE_REMOTES))], check=True)
            subprocess.run(["git", "-C", str(repository), "add", "."], check=True)
            subprocess.run(["git", "-C", str(repository), "commit", "-qm", "smoke source"], check=True)
            source_sha = subprocess.check_output(["git", "-C", str(repository), "rev-parse", "HEAD"], text=True).strip()
            subprocess.run(["git", "-C", str(repository), "update-ref", "refs/remotes/origin/main", source_sha], check=True)
            git_dir = subprocess.check_output(["git", "-C", str(repository), "rev-parse", "--absolute-git-dir"], text=True).strip()

            live_view = repository / "views/health.schemas.ts"
            live_view.parent.mkdir()
            live_view.write_bytes(b"staged live-only view\n")
            subprocess.run(["git", "-C", str(repository), "add", "views/health.schemas.ts"], check=True)
            live_index_before = (Path(git_dir) / "index").read_bytes()

            with tempfile.TemporaryDirectory(dir=root) as scratch_name, mock.patch.object(installer, "SMOKE_SOURCE_REPOSITORY", repository):
                scratch = Path(scratch_name)
                tree = installer._archive_smoke_source(source_sha, scratch)
                source_root = scratch / "source"
                self.assertEqual(tree, subprocess.check_output(["git", "-C", str(repository), "rev-parse", f"{source_sha}^{{tree}}"], text=True).strip())
                self.assertFalse((source_root / ".git").exists(), "git archive must not supply Git metadata")
                self.assertFalse((source_root / "views/health.schemas.ts").exists(), "staged live-only path must not enter the commit archive")
                source_index = installer._smoke_source_index(source_sha, source_root, scratch, git_dir)
                installer._chown_smoke_source_snapshot(source_root, os.getuid(), os.getgid())
                metadata_index = installer._attach_smoke_source_git_metadata(source_root, source_index, git_dir)
                metadata_index_before = metadata_index.read_bytes()
                self.assertFalse(os.path.samefile(metadata_index, source_index))
                self.assertEqual(metadata_index_before, source_index.read_bytes())
                self.assertEqual(metadata_index.lstat().st_uid, os.geteuid())
                self.assertEqual(metadata_index.stat().st_mode & 0o777, 0o444)
                self.assertEqual((source_root / ".git").stat().st_mode & 0o777, 0o555)

                environment = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
                environment.update({
                    "PATH": f"{Path(node).parent}:/usr/bin:/bin",
                    "GIT_DIR": str(Path(git_dir).resolve()),
                    "GIT_WORK_TREE": str(source_root.resolve()),
                    "GIT_INDEX_FILE": str(source_index.resolve()),
                    "GIT_OPTIONAL_LOCKS": "0",
                    "GIT_CONFIG_COUNT": "1",
                    "GIT_CONFIG_KEY_0": "safe.directory",
                    "GIT_CONFIG_VALUE_0": str(repository.resolve()),
                })
                installer._verify_smoke_source_snapshot(source_root, git_dir, source_index)
                scratch.chmod(0o555)
                try:
                    prepared = subprocess.run(
                        [
                            str(node), str(ROOT / "scripts/prepare-donor-source-views.mjs"),
                            "--root", str(source_root), "--index", "source-index.json",
                        ],
                        cwd=source_root,
                        env=environment,
                        check=False,
                        capture_output=True,
                        text=True,
                    )
                finally:
                    scratch.chmod(0o755)
                self.assertEqual(prepared.returncode, 0, prepared.stderr)
                summary = json.loads(prepared.stdout)
                self.assertEqual(summary["created"], 1)
                self.assertEqual((source_root / "views/health.schemas.ts").read_bytes(), source)
                installer._verify_smoke_source_snapshot(source_root, git_dir, source_index)
                installer._verify_smoke_source_git_metadata(source_root, source_index, git_dir)
                self.assertEqual(metadata_index.read_bytes(), metadata_index_before)
                self.assertEqual((Path(git_dir) / "index").read_bytes(), live_index_before)
                self.assertIn("A  views/health.schemas.ts", subprocess.check_output(["git", "-C", str(repository), "status", "--short"], text=True))

                tracked_source = source_root / "tracked/health.schemas.ts"
                tracked_source.chmod(0o644)
                tracked_source.write_text("snapshot mutation\n")
                with self.assertRaisesRegex(RuntimeError, "no longer matches"):
                    installer._verify_smoke_source_snapshot(source_root, git_dir, source_index)

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
            "validation_scope_base_sha": sha0,
            "validation_scope_base_tree": "f" * 40,
            "validation_scope_changed_paths": [],
            "actual_ci_baseline_verified": False,
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
            "validation_scope_base_sha": sha0,
            "validation_scope_base_tree": "f" * 40,
            "validation_scope_changed_paths": [],
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
            if args[0] == "rev-parse" and args[1] == f"{sha0}^{{tree}}":
                return "f" * 40
            if args[0] == "rev-parse" and args[1] == f"{sha1}^1":
                return sha0
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

    def test_recover_installed_smoke_failure_keeps_orphan_blocked_before_production_read(self):
        with tempfile.TemporaryDirectory() as temp:
            fixture = self.recovery_fixture(Path(temp))
            payment_paths = ["internal/payment/app/service.go"]
            fixture["metadata"].update(validation_scope_changed_paths=payment_paths)
            fixture["metadata_path"].write_text(json.dumps(fixture["metadata"]))
            state = json.loads(fixture["state_path"].read_text())
            state["validation_scope_changed_paths"] = payment_paths
            fixture["state_path"].write_text(json.dumps(state))
            with (
                self.recovery_patches(fixture, [fixture["old"]]) as calls,
                mock.patch.object(worker, "_trusted_changed_paths", return_value=payment_paths),
                mock.patch.object(worker, "run_stage_smoke", side_effect=RuntimeError("required fixture unavailable")) as smoke,
            ):
                with self.assertRaisesRegex(RuntimeError, "required fixture unavailable"):
                    worker.recover(fixture["config"], retry_blocked=True, expected_sha=fixture["sha1"])

            smoke.assert_called_once_with(
                fixture["config"], fixture["repo"], fixture["sha1"], fixture["sha1"],
                installer.digest(fixture["build"] / "release" / "release-files.sha256"),
            )
            calls["production"].assert_not_called()
            calls["command"].assert_not_called()
            state = json.loads(fixture["state_path"].read_text())
            self.assertEqual(state["status"], "outcome_unknown")
            self.assertEqual(state["blocked_sha"], fixture["sha1"])
            self.assertEqual(state["last_stage_smoke"]["status"], "failed")
            self.assertNotIn("staging_verified_smoke", state)

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
            plan = {"base_sha": sha0, "target_sha": sha1, "changed_paths": ["scripts/domestic_release.py"], "runtime_changed": False, "controller_files": ["scripts/domestic_release.py"]}
            with mock.patch.object(worker, "git", return_value=sha1), mock.patch.object(worker, "require_official_origin"), mock.patch.object(worker, "first_parent_queue", return_value=[sha1]), mock.patch.object(worker, "exact_check_success", return_value=True), mock.patch.object(worker, "command", return_value=json.dumps(plan)), mock.patch.object(worker, "_trusted_changed_paths", return_value=plan["changed_paths"]), mock.patch.object(worker, "verify_controller_installation", side_effect=RuntimeError("stale fixed copy")), mock.patch.object(worker, "build_candidate") as build, mock.patch.object(worker, "stage_install") as stage, mock.patch.object(worker, "copy_payload") as transfer:
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
            plan = {"base_sha": sha0, "target_sha": sha1, "changed_paths": ["scripts/domestic_release.py"], "runtime_changed": False, "controller_files": ["scripts/domestic_release.py"]}
            verification = {"source_sha": sha1, "source_tree": "c" * 40, "files": {}, "duration_seconds": 0.4, "status": "matched"}
            with mock.patch.object(worker, "git", return_value=sha1), mock.patch.object(worker, "require_official_origin"), mock.patch.object(worker, "first_parent_queue", return_value=[sha1]), mock.patch.object(worker, "exact_check_success", return_value=True), mock.patch.object(worker, "command", return_value=json.dumps(plan)), mock.patch.object(worker, "_trusted_changed_paths", return_value=plan["changed_paths"]), mock.patch.object(worker, "verify_controller_installation", return_value=verification), mock.patch.object(worker, "build_candidate") as build, mock.patch.object(worker, "stage_install") as stage:
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

    def test_installed_alipay_smoke_policy_uses_fixed_paths(self):
        self.assertTrue(worker._alipay_smoke_required({"changed_paths": ["internal/payment/app/service.go"]}))
        self.assertTrue(worker._alipay_smoke_required({"changed_paths": [worker.ALIPAY_SMOKE_FIXTURE]}))
        self.assertTrue(worker._alipay_smoke_required({"changed_paths": ["migrations/0208_payment.sql"]}))
        self.assertTrue(worker._alipay_smoke_required({"changed_paths": ["cmd/aicrm/payment_callback.go"]}))
        self.assertTrue(worker._alipay_smoke_required({"changed_paths": ["internal/webshell/static/payment.html"]}))
        self.assertFalse(worker._alipay_smoke_required({"changed_paths": ["cmd/aicrm/invitation_chromium_journey.mjs"]}))
        self.assertFalse(worker._alipay_smoke_required({"changed_paths": ["internal/payment/app/service_test.go"]}))
        self.assertFalse(worker._alipay_smoke_required({"changed_paths": ["docs/release.md", "internal/payment/README.md", "scripts/ci/impact_selection.py"]}))
        self.assertTrue(worker._alipay_smoke_required({"changed_paths": ["cmd/aicrm/other_browser_journey.mjs"]}))
        with self.assertRaisesRegex(RuntimeError, "invalid release impact paths"):
            worker._alipay_smoke_required({"changed_paths": "internal/payment/app/service.go"})

    def test_queue_smokes_new_fixture_but_not_browser_test_or_followup_docs(self):
        with tempfile.TemporaryDirectory(prefix="domestic-release-smoke-queue-sequence-") as temporary:
            root = Path(temporary)
            deployed, browser_test, fixture_change, docs_change = (value * 40 for value in "1234")
            source_tree = "5" * 40
            old_helper_sha, new_helper_sha = "6" * 64, "7" * 64
            manifest_sha, binary_sha = "8" * 64, "9" * 64
            invitation_journey = "cmd/aicrm/invitation_chromium_journey.mjs"
            fixture = worker.ALIPAY_SMOKE_FIXTURE
            helper = "deploy/domestic-promote.py"
            documentation = "docs/release-follow-up.md"
            state_path = root / "state.json"
            state_path.write_text(json.dumps({
                "status": "ready", "processed_sha": deployed,
                "deployed_source_sha": deployed, "prod_installed_sha": deployed,
            }))
            config = {
                "repo": str(root), "state": str(state_path), "production_enabled": True,
                "stage_helper": "/fixed/domestic-promote.py",
            }
            first_paths = [invitation_journey]
            second_paths = [invitation_journey, fixture, helper]
            third_paths = [invitation_journey, fixture, helper, documentation]
            plans = iter((
                {"base_sha": deployed, "target_sha": browser_test, "changed_paths": first_paths, "runtime_changed": False, "controller_files": []},
                {"base_sha": deployed, "target_sha": fixture_change, "changed_paths": second_paths, "runtime_changed": False, "controller_files": [helper]},
                {"base_sha": deployed, "target_sha": docs_change, "changed_paths": third_paths, "runtime_changed": False, "controller_files": [helper]},
            ))
            path_ranges = {
                (deployed, browser_test): first_paths,
                (deployed, fixture_change): second_paths,
                (browser_test, fixture_change): [fixture, helper],
                (deployed, docs_change): third_paths,
                (fixture_change, docs_change): [documentation],
            }
            receipt = {
                "status": "passed", "contract": "alipay_checkout",
                "test_name": "TestDomesticReleaseInstalledAlipayCheckout",
                "test_marker": "domestic_release_installed_alipay_checkout: PASS",
                "stage_role": "staging", "source_sha": fixture_change,
                "source_tree": source_tree, "installed_sha": deployed,
                "manifest_sha256": manifest_sha, "helper_sha256": new_helper_sha,
                "installed_binary_sha256": binary_sha,
                "verified_at_utc": "2026-09-24T00:00:00Z",
            }
            smoke_commands = []

            def fake_git(_repo, *args):
                if args == ("rev-parse", "refs/remotes/origin/main"):
                    return docs_change
                if args == ("rev-parse", f"{fixture_change}^{{tree}}"):
                    return source_tree
                return ""

            def fake_command(*args, **_kwargs):
                if args[0] == "python3":
                    return json.dumps(next(plans))
                if args[0] == "sudo":
                    smoke_commands.append(args)
                    return json.dumps(receipt)
                self.fail(f"unexpected controller command: {args[0]}")

            def candidate_helper_digest(_repo, source_sha, path):
                self.assertEqual(path, helper)
                return {browser_test: old_helper_sha, fixture_change: new_helper_sha, docs_change: new_helper_sha}[source_sha]

            with (
                mock.patch.object(worker, "git", side_effect=fake_git),
                mock.patch.object(worker, "require_official_origin"),
                mock.patch.object(worker, "first_parent_queue", return_value=[browser_test, fixture_change, docs_change]),
                mock.patch.object(worker, "exact_check_success", return_value=True),
                mock.patch.object(worker, "command", side_effect=fake_command),
                mock.patch.object(worker, "_trusted_changed_paths", side_effect=lambda _repo, base, target: path_ranges[(base, target)]),
                mock.patch.object(worker, "verify_controller_installation", return_value={"duration_seconds": 0.1}) as verify_controller,
                mock.patch.object(worker, "_installed_stage_manifest_sha", return_value=manifest_sha) as read_manifest,
                mock.patch.object(worker, "_git_file_sha256", side_effect=candidate_helper_digest) as source_helper_digest,
                mock.patch.object(worker, "_local_file_sha256", return_value=new_helper_sha) as installed_helper_digest,
                mock.patch.object(worker, "build_candidate") as build,
                mock.patch.object(worker, "stage_install") as stage,
                mock.patch.object(worker, "copy_payload") as promote,
            ):
                result = worker.poll(config)

            self.assertEqual(result["status"], "ready")
            self.assertEqual(result["processed_sha"], docs_change)
            self.assertEqual(json.loads(state_path.read_text())["processed_sha"], docs_change)
            self.assertEqual(len(smoke_commands), 1)
            self.assertEqual(
                smoke_commands[0],
                (
                    "sudo", config["stage_helper"], "--run-staging-smoke",
                    "--source-sha", fixture_change, "--expected-sha", deployed,
                    "--expected-manifest-sha256", manifest_sha,
                    "--expected-helper-sha256", new_helper_sha,
                ),
            )
            source_helper_digest.assert_called_once_with(root, fixture_change, helper)
            installed_helper_digest.assert_called_once_with(Path(config["stage_helper"]))
            read_manifest.assert_called_once_with(config, deployed)
            self.assertEqual([call.args[2] for call in verify_controller.call_args_list], [fixture_change, docs_change])
            build.assert_not_called()
            stage.assert_not_called()
            promote.assert_not_called()

    def test_promotion_requires_a_bound_smoke_receipt_for_payment_changes(self):
        with tempfile.TemporaryDirectory(prefix="domestic-release-smoke-gate-") as temporary:
            root = Path(temporary)
            sha0, sha = "a" * 40, "b" * 40
            state_path = root / "state.json"
            original_state = {"status": "ready", "processed_sha": sha0}
            state_path.write_text(json.dumps(original_state))
            state = dict(original_state)
            metadata = {
                "base_sha": sha0,
                "validation_scope_base_sha": sha0,
                "source_tree": "c" * 40,
                "validation_scope_base_tree": "e" * 40,
                "validation_scope_changed_paths": ["internal/payment/app/service.go"],
                "actual_ci_baseline_verified": False,
                "release_files_sha256": "d" * 64,
            }
            with mock.patch.object(worker, "git", side_effect=lambda _repo, *args: sha0 if args == ("rev-parse", f"{sha}^1") else "e" * 40), mock.patch.object(worker, "_trusted_changed_paths", return_value=["internal/payment/app/service.go"]), mock.patch.object(worker, "copy_payload") as copy_payload:
                with self.assertRaisesRegex(RuntimeError, "required installed staging smoke receipt is missing"):
                    worker.promote_checked_candidate(
                        {"repo": str(root)}, state_path, state, sha, root, metadata, "a" * 40, 1.0,
                        stage_receipt={}, changed_paths=["internal/payment/app/service.go"],
                        build_base_sha=sha0, validation_scope_base_sha=sha0,
                    )
            copy_payload.assert_not_called()
            self.assertEqual(state_path.read_text(), json.dumps(original_state))

    def test_processed_validation_scope_prevents_repeating_old_checkout_smoke(self):
        with tempfile.TemporaryDirectory(prefix="domestic-release-validation-scope-") as temporary:
            root = Path(temporary)
            deployed, processed, target = "a" * 40, "b" * 40, "c" * 40
            state_path = root / "state.json"
            state_path.write_text(json.dumps({
                "status": "ready", "processed_sha": processed,
                "deployed_source_sha": deployed, "prod_installed_sha": deployed,
            }))
            config = {"repo": str(root), "state": str(state_path), "production_enabled": True}
            fixture_path = worker.ALIPAY_SMOKE_FIXTURE
            build_paths = [fixture_path, "docs/release-follow-up.md"]
            validation_paths = ["docs/release-follow-up.md"]
            plan = {"changed_paths": build_paths, "runtime_changed": False, "controller_files": []}
            with (
                mock.patch.object(worker, "git", return_value=target),
                mock.patch.object(worker, "require_official_origin"),
                mock.patch.object(worker, "first_parent_queue", return_value=[target]),
                mock.patch.object(worker, "exact_check_success", return_value=True),
                mock.patch.object(worker, "command", return_value=json.dumps(plan)),
                mock.patch.object(worker, "_trusted_changed_paths", side_effect=[build_paths, validation_paths]),
                mock.patch.object(worker, "run_stage_smoke") as smoke,
            ):
                result = worker.poll(config)

            self.assertEqual(result["status"], "ready")
            self.assertEqual(json.loads(state_path.read_text())["processed_sha"], target)
            smoke.assert_not_called()

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
            changed = ["internal/customer/app/service.go"]
            metadata = {
                "source_sha": sha1, "base_sha": sha0, "source_tree": "d" * 40,
                "validation_scope_base_sha": sha0, "validation_scope_base_tree": "e" * 40,
                "validation_scope_changed_paths": changed,
                "actual_ci_baseline_verified": False,
                "release_files_sha256": "f" * 64,
            }
            def fake_build_candidate(*_args):
                (root / "domestic-release.json").write_text(json.dumps(metadata))
                return root, metadata
            plan = {"base_sha": sha0, "target_sha": sha1, "changed_paths": changed, "runtime_changed": True}
            def fake_git(_repo, *args):
                if args[0] == "rev-parse" and args[1] == f"{sha1}^1":
                    return sha0
                if args[0] == "rev-parse" and args[1] == f"{sha0}^{{tree}}":
                    return "e" * 40
                return sha1

            with mock.patch.object(worker, "git", side_effect=fake_git), mock.patch.object(worker, "require_official_origin"), mock.patch.object(worker, "first_parent_queue", return_value=[sha1]), mock.patch.object(worker, "exact_check_success", return_value=True), mock.patch.object(worker, "command", side_effect=[json.dumps(plan), RuntimeError("ssh interrupted")]), mock.patch.object(worker, "_trusted_changed_paths", return_value=changed), mock.patch.object(worker, "build_candidate", side_effect=fake_build_candidate), mock.patch.object(worker, "stage_install", return_value={}), mock.patch.object(worker, "copy_payload", return_value=("/incoming", "/meta")):
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
            changed = ["internal/customer/app/service.go"]
            plan = {"base_sha": sha0, "target_sha": sha1, "changed_paths": changed, "runtime_changed": True}
            with mock.patch.object(worker, "git", return_value=sha1), mock.patch.object(worker, "require_official_origin"), mock.patch.object(worker, "first_parent_queue", return_value=[sha1]), mock.patch.object(worker, "exact_check_success", return_value=True), mock.patch.object(worker, "command", return_value=json.dumps(plan)), mock.patch.object(worker, "_trusted_changed_paths", return_value=changed), mock.patch.object(worker, "build_candidate", side_effect=RuntimeError("stage build failed")), mock.patch.object(worker, "copy_payload") as prod_copy:
                with self.assertRaisesRegex(RuntimeError, "stage build failed"):
                    worker.poll(cfg)
                prod_copy.assert_not_called()
            self.assertEqual(json.loads(state.read_text())["status"], "staging_failed")

    def test_installed_smoke_failure_stops_before_production_copy(self):
        with tempfile.TemporaryDirectory(prefix="domestic-release-poll-smoke-") as temporary:
            root = Path(temporary)
            sha0, sha1 = "a" * 40, "b" * 40
            state_path = root / "state.json"
            state_path.write_text(json.dumps({
                "status": "ready", "processed_sha": sha0,
                "deployed_source_sha": sha0, "prod_installed_sha": sha0,
            }))
            (root / "builds" / sha0 / "release").mkdir(parents=True)
            config = {
                "repo": str(root), "work_root": str(root), "state": str(state_path),
                "production_enabled": True,
            }
            metadata = {"source_sha": sha1, "source_tree": "c" * 40, "release_files_sha256": "d" * 64}
            plan = {"base_sha": sha0, "target_sha": sha1, "changed_paths": ["internal/payment/app/service.go"], "runtime_changed": True}
            with (
                mock.patch.object(worker, "git", return_value=sha1),
                mock.patch.object(worker, "require_official_origin"),
                mock.patch.object(worker, "first_parent_queue", return_value=[sha1]),
                mock.patch.object(worker, "exact_check_success", return_value=True),
                mock.patch.object(worker, "command", return_value=json.dumps(plan)),
                mock.patch.object(worker, "_trusted_changed_paths", return_value=plan["changed_paths"]),
                mock.patch.object(worker, "build_candidate", return_value=(root, metadata)),
                mock.patch.object(worker, "stage_install", return_value={}) as stage,
                mock.patch.object(worker, "run_stage_smoke", side_effect=RuntimeError("installed fixture failed")) as smoke,
                mock.patch.object(worker, "copy_payload") as production_copy,
            ):
                with self.assertRaisesRegex(RuntimeError, "installed fixture failed"):
                    worker.poll(config)

            stage.assert_called_once()
            smoke.assert_called_once()
            production_copy.assert_not_called()
            state = json.loads(state_path.read_text())
            self.assertEqual(state["status"], "staging_failed")
            self.assertEqual(state["blocked_sha"], sha1)
            self.assertEqual(state["last_stage_smoke"]["status"], "failed")

    def test_github_fetch_timeout_keeps_ready_cursor_and_never_builds_or_installs(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            sha0 = "a" * 40
            state = root / "state.json"
            initial = {
                "status": "ready",
                "processed_sha": sha0,
                "deployed_source_sha": sha0,
                "prod_installed_sha": sha0,
                "blocked_sha": None,
                "failure": None,
            }
            state.write_text(json.dumps(initial, sort_keys=True) + "\n")
            original_state = state.read_bytes()
            config = {"repo": str(root), "state": str(state), "production_enabled": True}

            def timeout_fetch(*args, **kwargs):
                self.assertEqual(args, ("git", "-C", str(root), "fetch", "--no-tags", "origin", "main"))
                self.assertEqual(kwargs["timeout"], worker.GITHUB_FETCH_TIMEOUT_SECONDS)
                raise subprocess.TimeoutExpired(args, kwargs["timeout"])

            with (
                mock.patch.object(worker, "require_official_origin"),
                mock.patch.object(worker, "command", side_effect=timeout_fetch) as command,
                mock.patch.object(worker, "exact_check_success") as check,
                mock.patch.object(worker, "build_candidate") as build,
                mock.patch.object(worker, "stage_install") as stage,
                mock.patch.object(worker, "copy_payload") as transfer,
                mock.patch.object(worker, "promote_checked_candidate") as promote,
            ):
                with self.assertRaises(subprocess.TimeoutExpired):
                    worker.poll(config)

            command.assert_called_once()
            check.assert_not_called()
            build.assert_not_called()
            stage.assert_not_called()
            transfer.assert_not_called()
            promote.assert_not_called()
            self.assertEqual(state.read_bytes(), original_state)
            self.assertEqual(json.loads(state.read_text()), initial)

    def test_two_checked_commits_install_in_order(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            sha0, sha1, sha2 = "a" * 40, "b" * 40, "c" * 40
            state = root / "state.json"
            state.write_text(json.dumps({"status": "ready", "processed_sha": sha0, "deployed_source_sha": sha0, "prod_installed_sha": sha0}))
            for sha in (sha0, sha1):
                (root / "builds" / sha / "release").mkdir(parents=True)
            cfg = {"repo": str(root), "work_root": str(root), "state": str(state), "production_enabled": True, "prod_key": "key", "prod_known_hosts": "hosts", "prod_user": "ubuntu", "prod_host": "127.0.0.1", "prod_helper": "/fixed/helper"}
            metadatas = [
                {
                    "source_sha": sha1, "base_sha": sha0, "source_tree": "d" * 40,
                    "validation_scope_base_sha": sha0, "validation_scope_base_tree": "e" * 40,
                    "validation_scope_changed_paths": ["internal/customer/app/service.go"],
                    "actual_ci_baseline_verified": False,
                    "release_files_sha256": "f" * 64, "migrations_changed": False,
                },
                {
                    "source_sha": sha2, "base_sha": sha1, "source_tree": "d" * 40,
                    "validation_scope_base_sha": sha1, "validation_scope_base_tree": "e" * 40,
                    "validation_scope_changed_paths": ["internal/customer/app/service.go"],
                    "actual_ci_baseline_verified": False,
                    "release_files_sha256": "f" * 64, "migrations_changed": False,
                },
            ]
            receipts = [success_receipt(metadatas[0], sha0), success_receipt(metadatas[1], sha1)]
            changes = [["internal/customer/app/service.go"], ["internal/customer/app/service.go"]]
            command_results = [
                json.dumps({"base_sha": sha0, "target_sha": sha1, "changed_paths": changes[0], "runtime_changed": True}),
                json.dumps(receipts[0]),
                json.dumps({"base_sha": sha1, "target_sha": sha2, "changed_paths": changes[1], "runtime_changed": True}),
                json.dumps(receipts[1]),
            ]
            readbacks = [{"current": f"/opt/aicrm/releases/{sha}", "release_env": f"AICRM_RELEASE_SHA={sha}\n", "manifest_sha256": "f" * 64, "readyz": {"release_sha": sha, "status": "ready"}, "services": {unit: service_process(sha) for unit in ("aicrm.service", "aicrm-effects-worker.service")}, "receipt": receipts[index]} for index, sha in enumerate((sha1, sha2))]
            metadata_hashes = []
            def fake_git(_repo, *args):
                if args[0] == "rev-parse":
                    if args[1] == f"{sha1}^1":
                        return sha0
                    if args[1] == f"{sha2}^1":
                        return sha1
                    if args[1] in (f"{sha0}^{{tree}}", f"{sha1}^{{tree}}"):
                        return "e" * 40
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
            path_reads = [changes[0], changes[0], changes[0], changes[0], changes[1], changes[1], changes[1], changes[1]]
            with mock.patch.object(worker, "git", side_effect=fake_git), mock.patch.object(worker, "require_official_origin"), mock.patch.object(worker, "first_parent_queue", return_value=[sha1, sha2]), mock.patch.object(worker, "exact_check_success", return_value=True), mock.patch.object(worker, "_trusted_changed_paths", side_effect=path_reads), mock.patch.object(worker, "command", side_effect=command_results) as commands, mock.patch.object(worker, "build_candidate", side_effect=fake_build_candidate), mock.patch.object(worker, "stage_install", return_value={}) as stage, mock.patch.object(worker, "copy_payload", side_effect=[("/incoming/one", "/meta/one"), ("/incoming/two", "/meta/two")]) as transfer, mock.patch.object(worker, "prod_readback", side_effect=readbacks):
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
