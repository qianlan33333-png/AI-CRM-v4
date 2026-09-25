from __future__ import annotations

import hashlib
import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).parent))
import manual_github_sync as sync


def run_git(root: Path, *args: str) -> str:
    return subprocess.check_output(["git", "-C", str(root), *args], text=True).strip()


class ManualGitHubSyncTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.known_hosts = self.root / "known_hosts"
        self.known_hosts.write_text("test pinned host key\n", encoding="utf-8")
        self.repo = self.root / "developer"
        self.repo.mkdir()
        subprocess.run(["git", "-C", str(self.repo), "init", "-q", "--initial-branch=main"], check=True)
        run_git(self.repo, "config", "user.name", "Sync Test")
        run_git(self.repo, "config", "user.email", "sync@example.invalid")
        (self.repo / "app.go").write_text("package app\n", encoding="utf-8")
        run_git(self.repo, "add", "app.go")
        run_git(self.repo, "commit", "-qm", "base application")
        self.app_sha = run_git(self.repo, "rev-parse", "HEAD")
        self.app_tree = run_git(self.repo, "rev-parse", "HEAD^{tree}")

        self.domestic = self.root / "domestic.git"
        self.github = self.root / "github.git"
        subprocess.run(["git", "init", "-q", "--bare", "--initial-branch=main", str(self.domestic)], check=True)
        subprocess.run(["git", "init", "-q", "--bare", "--initial-branch=main", str(self.github)], check=True)
        run_git(self.repo, "remote", "add", "domestic", str(self.domestic))
        run_git(self.repo, "remote", "add", "origin", str(self.github))
        run_git(self.repo, "push", "-q", "domestic", "main:main")
        run_git(self.repo, "push", "-q", "origin", "main:main")
        self.github_base = self.app_sha

        # A docs-only commit advances the source cursor without changing the
        # most recent application installation SHA.
        (self.repo / "release-notes.md").write_text("Documented release.\n", encoding="utf-8")
        run_git(self.repo, "add", "release-notes.md")
        run_git(self.repo, "commit", "-qm", "document release")
        self.domestic_sha = run_git(self.repo, "rev-parse", "HEAD")
        self.domestic_tree = run_git(self.repo, "rev-parse", "HEAD^{tree}")
        run_git(self.repo, "push", "-q", "domestic", "main:main")

        self.receipt = {
            "schema_version": 1,
            "source_sha": self.app_sha,
            "source_tree": self.app_tree,
            "manifest_sha256": "a" * 64,
            "technical_status": "installed_healthy",
            "installed_at_utc": "2026-09-25T00:00:00Z",
        }
        raw_receipt = json.dumps(self.receipt, sort_keys=True, separators=(",", ":")).encode()
        self.receipt_sha256 = hashlib.sha256(raw_receipt).hexdigest()
        self.readback = {
            "status": "ready",
            "cursor": {
                "schema_version": 1,
                "main_sha": self.domestic_sha,
                "main_tree": self.domestic_tree,
                "installed_app_sha": self.app_sha,
                "installed_app_tree": self.app_tree,
                "install_receipt_sha256": self.receipt_sha256,
                "installed_manifest_sha256": self.receipt["manifest_sha256"],
                "source_bundle_sha256": "b" * 64,
                "status": "ready",
                "updated_at_utc": "2026-09-25T00:01:00Z",
            },
            "install_receipt": self.receipt,
            "install_receipt_sha256": self.receipt_sha256,
        }

    def sync_local_remotes(self, *, execute: bool, production_reader=None, archive_ack_writer=None) -> dict:
        def logical_remote_url(_repo: Path, remote: str, *, push: bool = False) -> str:
            if remote == "domestic":
                return "ssh://aicrm-release-push@stage-alias/opt/aicrm/domestic/source.git"
            if remote == "origin":
                return "git@github.com:qianlan33333-png/AI-CRM-v4.git"
            raise AssertionError(f"unexpected remote {remote}")

        def logical_target(_host: str, role: str, user: str, _known_hosts=None, _ssh_binary="ssh") -> str:
            if role == "staging" and user == "aicrm-release-push":
                return "10.0.4.6"
            if role == "production" and user == "ubuntu":
                return "124.220.53.183"
            raise AssertionError(f"unexpected SSH target: {role}/{user}")

        with patch.object(sync, "_remote_url", side_effect=logical_remote_url), patch.object(sync, "_resolve_ssh_target", side_effect=logical_target):
            return sync.synchronize(
                self.repo,
                "domestic",
                "origin",
                production_reader or (lambda: self.readback),
                execute=execute,
                archive_ack_writer=(archive_ack_writer or self.fake_archive_ack) if execute else None,
                known_hosts=self.known_hosts,
            )

    def fake_archive_ack(self, sha: str) -> dict:
        return {
            "status": "recorded",
            "github_synced_sha": sha,
            "domestic_main_sha": self.domestic_sha,
            "pending_first_parent_count": 0,
            "confirmed_at_utc": "2026-09-25T00:02:00Z",
        }

    def tearDown(self) -> None:
        self.temp.cleanup()

    def test_dry_run_reports_docs_only_lag_without_changing_github(self) -> None:
        result = self.sync_local_remotes(execute=False)
        self.assertEqual(result["status"], "pending")
        self.assertEqual(result["github_synced_sha"], self.github_base)
        self.assertEqual(result["github_pending_commit_count"], 1)
        self.assertEqual(result["github_pending_commits"][0]["sha"], self.domestic_sha)
        self.assertEqual(run_git(self.repo, "ls-remote", str(self.github), "refs/heads/main").split()[0], self.github_base)

    def test_execute_fast_forwards_github_after_rechecking_receipt_and_refs(self) -> None:
        reads = 0

        def readback() -> dict:
            nonlocal reads
            reads += 1
            return self.readback

        result = self.sync_local_remotes(execute=True, production_reader=readback)
        self.assertEqual(reads, 2)
        self.assertEqual(result["status"], "synchronized")
        self.assertEqual(result["github_synced_sha"], self.domestic_sha)
        self.assertEqual(result["archive_ack_status"], "last_confirmed")
        self.assertEqual(result["last_manual_confirmed_github_sha"], self.domestic_sha)
        self.assertEqual(result["github_pending_commit_count"], 0)
        self.assertEqual(run_git(self.repo, "ls-remote", str(self.github), "refs/heads/main").split()[0], self.domestic_sha)

    def test_successful_push_with_failed_github_readback_reports_unknown_and_target(self) -> None:
        original_fetch = sync.fetch_main
        github_fetches = 0

        def fail_after_push(repo, remote, target_ref, *, ssh_command=None):
            nonlocal github_fetches
            if remote == "origin":
                github_fetches += 1
                if github_fetches == 3:
                    raise sync.SyncError("git_fetch_failed")
            return original_fetch(repo, remote, target_ref, ssh_command=ssh_command)

        ack_calls: list[str] = []
        with patch.object(sync, "fetch_main", side_effect=fail_after_push):
            result = self.sync_local_remotes(
                execute=True,
                archive_ack_writer=lambda sha: ack_calls.append(sha) or self.fake_archive_ack(sha),
            )
        self.assertEqual(result["status"], "github_push_outcome_unknown")
        self.assertEqual(result["github_target_sha"], self.domestic_sha)
        self.assertIsNone(result["github_synced_sha"])
        self.assertIsNone(result["github_readback_sha"])
        self.assertTrue(result["read_only_reconciliation_required"])
        self.assertIn("do not retry", result["reconciliation_action"])
        self.assertEqual(ack_calls, [])
        self.assertEqual(run_git(self.repo, "ls-remote", str(self.github), "refs/heads/main").split()[0], self.domestic_sha)

    def test_timed_out_push_with_failed_readback_is_unknown_and_not_retried(self) -> None:
        original_run = sync.subprocess.run
        push_calls = 0

        def push_then_timeout(args, *positional, **kwargs):
            nonlocal push_calls
            if isinstance(args, list) and len(args) >= 4 and args[:2] == ["git", "-C"] and Path(args[2]).resolve() == self.repo.resolve() and args[3] == "push":
                push_calls += 1
                original_run(args, *positional, **kwargs)
                raise subprocess.TimeoutExpired(args, timeout=120)
            return original_run(args, *positional, **kwargs)

        original_fetch = sync.fetch_main
        github_fetches = 0

        def fail_readback(repo, remote, target_ref, *, ssh_command=None):
            nonlocal github_fetches
            if remote == "origin":
                github_fetches += 1
                if github_fetches == 3:
                    raise sync.SyncError("git_fetch_failed")
            return original_fetch(repo, remote, target_ref, ssh_command=ssh_command)

        with patch.object(sync, "fetch_main", side_effect=fail_readback), patch.object(sync.subprocess, "run", side_effect=push_then_timeout):
            result = self.sync_local_remotes(execute=True)
        self.assertEqual(push_calls, 1)
        self.assertEqual(result["status"], "github_push_outcome_unknown")
        self.assertEqual(result["github_push_response"], "unknown")
        self.assertEqual(result["github_target_sha"], self.domestic_sha)
        self.assertIsNone(result["github_synced_sha"])
        self.assertEqual(run_git(self.repo, "ls-remote", str(self.github), "refs/heads/main").split()[0], self.domestic_sha)

    def test_push_success_with_stage_ack_failure_reports_readback_without_repush(self) -> None:
        def unavailable(_sha: str) -> dict:
            raise sync.SyncError("stage_archive_ack_rejected")

        result = self.sync_local_remotes(execute=True, archive_ack_writer=unavailable)
        self.assertEqual(result["status"], "github_push_succeeded_archive_ack_pending")
        self.assertEqual(result["github_readback_sha"], self.domestic_sha)
        self.assertEqual(result["last_manual_confirmed_github_sha"], None)
        self.assertEqual(run_git(self.repo, "ls-remote", str(self.github), "refs/heads/main").split()[0], self.domestic_sha)

    def test_stage_ack_ssh_uses_fixed_command_and_sha_payload(self) -> None:
        response = {
            "status": "recorded",
            "github_synced_sha": self.domestic_sha,
            "domestic_main_sha": self.domestic_sha,
            "pending_first_parent_count": 0,
            "confirmed_at_utc": "2026-09-25T00:02:00Z",
        }
        ssh_config = subprocess.CompletedProcess(
            [], 0,
            stdout="hostname 10.0.4.6\nport 22\nuser aicrm-release-push\nhostkeyalias staging-pinned\n",
            stderr="",
        )
        pin = subprocess.CompletedProcess([], 0, stdout="staging-pinned ssh-ed25519 AAAATEST\n", stderr="")
        completed = subprocess.CompletedProcess([], 0, stdout=json.dumps(response), stderr="")
        with patch.object(sync.subprocess, "run", side_effect=[ssh_config, pin, completed]) as run:
            value = sync._record_archive_ack_via_ssh(
                "stage",
                self.domestic_sha,
                user="aicrm-release-push",
                ssh_key=Path("/tmp/local-key"),
                known_hosts=self.known_hosts,
            )
        self.assertEqual(value, response)
        command = run.call_args_list[2].args[0]
        self.assertEqual(command[0:2], ["ssh", "-T"])
        self.assertIn("StrictHostKeyChecking=yes", command)
        self.assertIn("GlobalKnownHostsFile=/dev/null", command)
        self.assertIn(f"UserKnownHostsFile={self.known_hosts.resolve()}", command)
        self.assertIn("aicrm-release-push@stage", command)
        self.assertEqual(command[-1], f"domestic-archive-ack --sha {self.domestic_sha}")
        self.assertEqual(run.call_args_list[2].kwargs["input"], json.dumps({"sha": self.domestic_sha}, separators=(",", ":")) + "\n")

    def test_archive_ack_must_confirm_current_domestic_main(self) -> None:
        ack = self.fake_archive_ack(self.domestic_sha)
        ack["domestic_main_sha"] = self.app_sha
        with self.assertRaisesRegex(sync.SyncError, "stage_archive_ack_domestic_sha_mismatch"):
            sync.validate_archive_ack(ack, self.domestic_sha)

    def test_archive_ack_with_nonzero_pending_count_is_not_confirmed(self) -> None:
        ack = self.fake_archive_ack(self.domestic_sha)
        ack["pending_first_parent_count"] = 1
        with self.assertRaisesRegex(sync.SyncError, "stage_archive_ack_pending_first_parent_nonzero"):
            sync.validate_archive_ack(ack, self.domestic_sha)

    def test_diverged_github_main_blocks_without_modifying_remote(self) -> None:
        other = self.root / "other"
        subprocess.run(["git", "clone", "-q", str(self.github), str(other)], check=True)
        run_git(other, "config", "user.name", "Other Writer")
        run_git(other, "config", "user.email", "other@example.invalid")
        (other / "unrelated.txt").write_text("independent advance\n", encoding="utf-8")
        run_git(other, "add", "unrelated.txt")
        run_git(other, "commit", "-qm", "unexpected github update")
        run_git(other, "push", "-q", "origin", "main:main")
        diverged_sha = run_git(other, "rev-parse", "HEAD")

        with self.assertRaisesRegex(sync.SyncError, "github_main_is_not_an_ancestor_of_domestic_main"):
            self.sync_local_remotes(execute=True)
        self.assertEqual(run_git(self.repo, "ls-remote", str(self.github), "refs/heads/main").split()[0], diverged_sha)

    def test_production_cursor_mismatch_blocks(self) -> None:
        stale = json.loads(json.dumps(self.readback))
        stale["cursor"]["main_sha"] = self.app_sha
        stale["cursor"]["main_tree"] = self.app_tree
        with self.assertRaisesRegex(sync.SyncError, "production_main_sha_differs_from_domestic_main"):
            self.sync_local_remotes(execute=True, production_reader=lambda: stale)
        self.assertEqual(run_git(self.repo, "ls-remote", str(self.github), "refs/heads/main").split()[0], self.github_base)

    def test_latest_install_receipt_mismatch_blocks(self) -> None:
        stale = json.loads(json.dumps(self.readback))
        stale["install_receipt"]["source_sha"] = self.domestic_sha
        with self.assertRaisesRegex(sync.SyncError, "production_install_receipt_source_mismatch"):
            self.sync_local_remotes(execute=True, production_reader=lambda: stale)
        self.assertEqual(run_git(self.repo, "ls-remote", str(self.github), "refs/heads/main").split()[0], self.github_base)

    def test_helper_receipt_byte_digest_mismatch_blocks(self) -> None:
        stale = json.loads(json.dumps(self.readback))
        stale["install_receipt_sha256"] = "c" * 64
        with self.assertRaisesRegex(sync.SyncError, "production_install_receipt_digest_mismatch"):
            self.sync_local_remotes(execute=True, production_reader=lambda: stale)
        self.assertEqual(run_git(self.repo, "ls-remote", str(self.github), "refs/heads/main").split()[0], self.github_base)

    def test_github_remote_allowlist_accepts_only_expected_repository(self) -> None:
        accepted = (
            "https://github.com/qianlan33333-png/AI-CRM-v4.git",
            "ssh://git@github.com/qianlan33333-png/AI-CRM-v4.git",
            "git@github.com:qianlan33333-png/AI-CRM-v4.git",
        )
        rejected = (
            "https://github.com/attacker/AI-CRM-v4.git",
            "git@github.com:qianlan33333-png/other-repo.git",
            "https://token@github.com/qianlan33333-png/AI-CRM-v4.git",
            "http://github.com/qianlan33333-png/AI-CRM-v4.git",
            "ssh://user@github.com/qianlan33333-png/AI-CRM-v4.git",
        )
        for value in accepted:
            with self.subTest(value=value):
                self.assertTrue(sync.is_expected_github_url(value))
        for value in rejected:
            with self.subTest(value=value):
                self.assertFalse(sync.is_expected_github_url(value))

    def test_domestic_remote_allowlist_requires_fixed_bare_repository_path(self) -> None:
        accepted = (
            "aicrm-release-push@staging:/opt/aicrm/domestic/source.git",
            "ssh://aicrm-release-push@10.0.4.6/opt/aicrm/domestic/source.git",
        )
        rejected = (
            "/tmp/source.git",
            "file:///opt/aicrm/domestic/source.git",
            "ubuntu@staging:/opt/aicrm/domestic/source.git",
            "staging:/opt/aicrm/domestic/source.git",
            "aicrm-release-push@staging:/opt/aicrm/other/source.git",
            "https://example.invalid/opt/aicrm/domestic/source.git",
            "ssh://root@staging/opt/aicrm/domestic/source.git",
        )
        for value in accepted:
            with self.subTest(value=value):
                self.assertTrue(sync.is_expected_domestic_url(value))
        for value in rejected:
            with self.subTest(value=value):
                self.assertFalse(sync.is_expected_domestic_url(value))

    def test_ssh_alias_must_resolve_to_allowlisted_endpoint_and_pinned_hostkey(self) -> None:
        resolved = subprocess.CompletedProcess(
            [], 0,
            stdout="hostname 124.220.53.183\nport 22\nuser ubuntu\nhostkeyalias prod-pinned\n",
            stderr="",
        )
        pinned = subprocess.CompletedProcess([], 0, stdout="prod-pinned ssh-ed25519 AAAATEST\n", stderr="")
        with patch.object(sync.subprocess, "run", side_effect=[resolved, pinned]) as run:
            canonical = sync._resolve_ssh_target("prod-alias", "production", "ubuntu", self.known_hosts)
        self.assertEqual(canonical, "124.220.53.183")
        self.assertEqual(run.call_args_list[0].args[0][-1], "ubuntu@prod-alias")
        self.assertEqual(run.call_args_list[1].args[0][2], "prod-pinned")

    def test_ssh_alias_to_unexpected_server_is_rejected_before_hostkey_lookup(self) -> None:
        resolved = subprocess.CompletedProcess(
            [], 0,
            stdout="hostname 198.51.100.77\nport 22\nuser aicrm-release-push\nhostkeyalias none\n",
            stderr="",
        )
        with patch.object(sync.subprocess, "run", return_value=resolved) as run:
            with self.assertRaisesRegex(sync.SyncError, "staging_ssh_host_not_allowlisted"):
                sync._resolve_ssh_target("wrong-stage", "staging", "aicrm-release-push", self.known_hosts)
        self.assertEqual(run.call_count, 1)

    def test_wrong_domestic_remote_path_blocks_before_fetch(self) -> None:
        run_git(self.repo, "remote", "set-url", "domestic", "aicrm-release-push@staging:/opt/aicrm/other/source.git")
        with self.assertRaisesRegex(sync.SyncError, "domestic_remote_not_authoritative_repository"):
            sync.synchronize(self.repo, "domestic", "origin", lambda: self.readback, execute=False)

    def test_unexpected_github_push_url_blocks_before_fetch(self) -> None:
        expected_fetch = "https://github.com/qianlan33333-png/AI-CRM-v4.git"
        wrong_push = "git@github.com:attacker/AI-CRM-v4.git"
        run_git(self.repo, "remote", "set-url", "domestic", "aicrm-release-push@staging:/opt/aicrm/domestic/source.git")
        run_git(self.repo, "remote", "set-url", "origin", expected_fetch)
        run_git(self.repo, "remote", "set-url", "--push", "origin", wrong_push)
        with patch.object(sync, "_resolve_ssh_target", return_value="10.0.4.6"):
            with self.assertRaisesRegex(sync.SyncError, "github_remote_not_expected_repository"):
                sync.synchronize(
                    self.repo,
                    "domestic",
                    "origin",
                    lambda: self.readback,
                    execute=True,
                    archive_ack_writer=self.fake_archive_ack,
                    known_hosts=self.known_hosts,
                )


if __name__ == "__main__":
    unittest.main()
