from __future__ import annotations

import hashlib
import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))
import manual_github_sync as sync


def run_git(root: Path, *args: str) -> str:
    return subprocess.check_output(["git", "-C", str(root), *args], text=True).strip()


class ManualGitHubSyncTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
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
        }

    def tearDown(self) -> None:
        self.temp.cleanup()

    def test_dry_run_reports_docs_only_lag_without_changing_github(self) -> None:
        result = sync.synchronize(
            self.repo, "domestic", "origin", lambda: self.readback, execute=False
        )
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

        result = sync.synchronize(
            self.repo, "domestic", "origin", readback, execute=True
        )
        self.assertEqual(reads, 2)
        self.assertEqual(result["status"], "synchronized")
        self.assertEqual(result["github_synced_sha"], self.domestic_sha)
        self.assertEqual(result["github_pending_commit_count"], 0)
        self.assertEqual(run_git(self.repo, "ls-remote", str(self.github), "refs/heads/main").split()[0], self.domestic_sha)

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
            sync.synchronize(self.repo, "domestic", "origin", lambda: self.readback, execute=True)
        self.assertEqual(run_git(self.repo, "ls-remote", str(self.github), "refs/heads/main").split()[0], diverged_sha)

    def test_production_cursor_mismatch_blocks(self) -> None:
        stale = json.loads(json.dumps(self.readback))
        stale["cursor"]["main_sha"] = self.app_sha
        stale["cursor"]["main_tree"] = self.app_tree
        with self.assertRaisesRegex(sync.SyncError, "production_main_sha_differs_from_domestic_main"):
            sync.synchronize(self.repo, "domestic", "origin", lambda: stale, execute=True)
        self.assertEqual(run_git(self.repo, "ls-remote", str(self.github), "refs/heads/main").split()[0], self.github_base)

    def test_latest_install_receipt_mismatch_blocks(self) -> None:
        stale = json.loads(json.dumps(self.readback))
        stale["install_receipt"]["source_sha"] = self.domestic_sha
        with self.assertRaisesRegex(sync.SyncError, "production_install_receipt_source_mismatch"):
            sync.synchronize(self.repo, "domestic", "origin", lambda: stale, execute=True)
        self.assertEqual(run_git(self.repo, "ls-remote", str(self.github), "refs/heads/main").split()[0], self.github_base)


if __name__ == "__main__":
    unittest.main()
