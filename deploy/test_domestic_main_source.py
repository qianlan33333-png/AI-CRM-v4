import hashlib
import importlib.util
import io
import json
from contextlib import nullcontext
from pathlib import Path
import shutil
import stat
import subprocess
import tempfile
import unittest
from contextlib import redirect_stdout
from types import SimpleNamespace
from unittest import mock


ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location("domestic_promote_main_source", ROOT / "deploy/domestic-promote.py")
installer = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(installer)
assert_root_file = installer._assert_root_file
assert_root_directory = installer._assert_root_directory


class DomesticMainSourceTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.incoming = self.root / "domestic-incoming"
        self.domestic_root = self.root / "domestic"
        self.domestic_root.mkdir(mode=0o755)
        self.backups = self.domestic_root / "source-backups"
        self.domestic_main = self.domestic_root / "source-cursor"
        self.releases = self.root / "releases"
        self.receipts = self.root / "domestic-receipts"
        self.repo = self.root / "source-repo"
        for path in (self.incoming, self.backups, self.releases, self.receipts):
            path.mkdir(mode=0o700)
        self.repo.mkdir()
        self.git("init", "--quiet")
        self.git("config", "user.name", "Domestic source fixture")
        self.git("config", "user.email", "source-fixture@example.invalid")
        (self.repo / "app.txt").write_text("installed app\n")
        self.git("add", "app.txt")
        self.git("commit", "--quiet", "-m", "installed app")
        self.app_sha = self.git("rev-parse", "HEAD")
        self.app_tree = self.git("rev-parse", "HEAD^{tree}")
        (self.repo / "docs.md").write_text("source-only baseline\n")
        self.git("add", "docs.md")
        self.git("commit", "--quiet", "-m", "source-only baseline")
        self.baseline_sha = self.git("rev-parse", "HEAD")
        self.baseline_tree = self.git("rev-parse", "HEAD^{tree}")
        (self.repo / "next.txt").write_text("next release\n")
        self.git("add", "next.txt")
        self.git("commit", "--quiet", "-m", "next release")
        self.next_sha = self.git("rev-parse", "HEAD")
        self.next_tree = self.git("rev-parse", "HEAD^{tree}")
        (self.repo / "stale.txt").write_text("stale base candidate\n")
        self.git("add", "stale.txt")
        self.git("commit", "--quiet", "-m", "stale base candidate")
        self.stale_sha = self.git("rev-parse", "HEAD")
        self.stale_tree = self.git("rev-parse", "HEAD^{tree}")
        self.git("branch", "-M", "main")
        self._make_bundle(self.baseline_sha, self.app_sha, baseline=True)
        self.baseline_bundle = self.incoming / f"{self.baseline_sha}.bundle"
        self.baseline_digest = installer.digest(self.baseline_bundle)
        self._make_bundle(self.next_sha, self.baseline_sha)
        self.next_bundle = self.incoming / f"{self.next_sha}.bundle"
        self.next_digest = installer.digest(self.next_bundle)
        self._make_bundle(self.stale_sha, self.app_sha)
        self.stale_bundle = self.incoming / f"{self.stale_sha}.bundle"
        self.stale_digest = installer.digest(self.stale_bundle)
        self.manifest_sha = self._make_production_app()
        self._patched = mock.patch.multiple(
            installer,
            ROOT=self.root,
            LOCK=self.root / "install-release.lock",
            DOMESTIC_INCOMING=self.incoming,
            SOURCE_BACKUPS=self.backups,
            DOMESTIC_MAIN=self.domestic_main,
            DOMESTIC_MAIN_STATE=self.domestic_main / "state.json",
            RELEASES=self.releases,
            CURRENT=self.root / "current",
            RECEIPTS=self.receipts,
            require_host_role=mock.Mock(return_value="production"),
            readiness=mock.Mock(return_value=None),
            verify_root_owned_release=mock.Mock(return_value=None),
            _production_release_lock=mock.Mock(return_value=nullcontext()),
            _assert_root_directory=self._assert_directory,
            _assert_root_file=self._assert_file,
        )
        self._patched.start()
        self.addCleanup(self._patched.stop)
        self.chown_patch = mock.patch.object(installer.os, "chown", return_value=None)
        self.fchown_patch = mock.patch.object(installer.os, "fchown", return_value=None)
        self.chown_patch.start()
        self.fchown_patch.start()
        self.addCleanup(self.chown_patch.stop)
        self.addCleanup(self.fchown_patch.stop)

    def git(self, *args):
        return subprocess.run(
            ["git", "-C", str(self.repo), *args], check=True, text=True,
            stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        ).stdout.strip()

    def _make_bundle(self, source_sha: str, old_sha: str, *, baseline: bool = False):
        main_sha = source_sha if baseline else old_sha
        self.git("update-ref", "refs/heads/main", main_sha)
        ref = f"refs/domestic/candidates/{source_sha}"
        self.git("update-ref", ref, source_sha)
        destination = self.incoming / f"{source_sha}.bundle"
        self.git("bundle", "create", str(destination), "refs/heads/main", ref)
        destination.chmod(0o600)

    def test_domestic_smoke_source_uses_only_fixed_bare_repo_and_exact_candidate_ref(self):
        bare = self.root / "domestic-source.git"
        subprocess.run(["git", "init", "--bare", str(bare)], check=True, stdout=subprocess.DEVNULL)
        candidate_ref = f"refs/domestic/candidates/{self.next_sha}"
        subprocess.run([
            "git", f"--git-dir={bare}", "fetch", "--no-tags", str(self.repo),
            f"{self.next_sha}:{candidate_ref}",
        ], check=True, stdout=subprocess.DEVNULL)
        with mock.patch.object(installer, "DOMESTIC_SOURCE_REPOSITORY", bare):
            self.assertEqual(
                installer._source_commit_tree(self.next_sha, source_ref=candidate_ref, source_repository=bare),
                self.next_tree,
            )
            with self.assertRaisesRegex(RuntimeError, "exact immutable candidate ref"):
                installer._source_commit_tree(self.next_sha, source_ref=candidate_ref, source_repository=self.repo)

    def test_domestic_parent_directories_reject_group_or_world_write(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            domestic = root / "domestic"
            domestic.mkdir(mode=0o755)
            root.chmod(0o755)
            domestic.chmod(0o755)

            original_lstat = Path.lstat
            root_owned_paths = {root, domestic}

            def root_owned_lstat(path):
                result = original_lstat(path)
                if path not in root_owned_paths:
                    return result
                fields = list(result)
                fields[4] = 0
                fields[5] = 0
                return installer.os.stat_result(fields)

            with mock.patch.object(installer, "ROOT", root), \
                 mock.patch.object(installer, "_assert_root_directory", assert_root_directory), \
                 mock.patch.object(Path, "lstat", root_owned_lstat):
                installer._assert_domestic_parent()

                for unsafe_parent in (root, domestic):
                    for unsafe_mode in (0o775, 0o757):
                        with self.subTest(parent=unsafe_parent.name, mode=oct(unsafe_mode)):
                            unsafe_parent.chmod(unsafe_mode)
                            with self.assertRaisesRegex(RuntimeError, "unsafe"):
                                installer._assert_domestic_parent()
                            unsafe_parent.chmod(0o755)

                domestic.rename(root / "domestic-directory")
                with self.assertRaisesRegex(RuntimeError, "missing"):
                    installer._assert_domestic_parent()
                (root / "domestic").symlink_to(root / "domestic-directory", target_is_directory=True)
                with self.assertRaisesRegex(RuntimeError, "unsafe"):
                    installer._assert_domestic_parent()

    def _assert_directory(self, path: Path, *, create=False, mode=0o700, private=False):
        if path.is_symlink():
            raise RuntimeError("unsafe fixture directory")
        if not path.exists():
            if not create:
                raise RuntimeError("missing fixture directory")
            path.mkdir(mode=mode, parents=True)
        if not path.is_dir():
            raise RuntimeError("unsafe fixture directory")
        if private and stat.S_IMODE(path.lstat().st_mode) != 0o700:
            raise RuntimeError("unsafe fixture directory mode")

    def _assert_file(self, path: Path, *, exact_mode=None):
        info = path.lstat()
        if path.is_symlink() or not stat.S_ISREG(info.st_mode):
            raise RuntimeError("unsafe fixture file")
        if exact_mode is not None and stat.S_IMODE(info.st_mode) != exact_mode:
            raise RuntimeError("unsafe fixture file mode")
        return info

    def _make_production_app(self):
        release = self.releases / self.app_sha
        (release / "bin").mkdir(parents=True)
        (release / "web/dist").mkdir(parents=True)
        (release / "bin/aicrm").write_bytes(b"synthetic app binary")
        (release / "web/dist/index.html").write_text("synthetic page")
        entries = []
        for path in sorted(item for item in release.rglob("*") if item.is_file()):
            entries.append(f"{installer.digest(path)}  {path.relative_to(release).as_posix()}\n")
        manifest = release / "release-files.sha256"
        manifest.write_text("".join(entries))
        manifest_sha = installer.digest(manifest)
        (release / "release.env").write_text(f"AICRM_RELEASE_SHA={self.app_sha}\n")
        self.current = self.root / "current"
        self.current.symlink_to(release)
        self.install_receipt = {
            "schema_version": 1,
            "source_sha": self.app_sha,
            "source_tree": self.app_tree,
            "manifest_sha256": manifest_sha,
            "previous_sha": None,
            "database_backup": None,
            "technical_status": "installed_healthy",
            "installed_at_utc": "2026-09-25T00:00:00Z",
        }
        receipt_path = self.receipts / f"{self.app_sha}.json"
        receipt_path.write_text(json.dumps(self.install_receipt, sort_keys=True) + "\n")
        receipt_path.chmod(0o600)
        return manifest_sha

    def _save_baseline(self):
        return installer.save_domestic_source_bundle(
            self.baseline_bundle,
            source_sha=self.baseline_sha,
            source_tree=self.baseline_tree,
            previous_main_sha=self.app_sha,
            expected_bundle_sha256=self.baseline_digest,
            allow_baseline_transition=True,
        )

    def test_save_verifies_exact_refs_tree_digest_and_self_contained_history(self):
        result = self._save_baseline()
        self.assertEqual(result["status"], "verified")
        self.assertTrue(result["self_contained"])
        self.assertEqual(result["source_sha"], self.baseline_sha)
        self.assertEqual(result["previous_main_sha"], self.app_sha)
        verified = installer.verify_domestic_source_backup(
            source_sha=self.baseline_sha,
            source_tree=self.baseline_tree,
            previous_main_sha=self.app_sha,
            expected_bundle_sha256=self.baseline_digest,
            allow_baseline_transition=True,
        )
        self.assertEqual(verified["_first_parent"][:2], [self.baseline_sha, self.app_sha])
        repeated = self._save_baseline()
        self.assertEqual(repeated["bundle_sha256"], self.baseline_digest)
        self.assertTrue((self.backups / f"{self.baseline_sha}.bundle").exists())

    def test_retry_verifies_source_bundle_left_by_interrupted_receipt_write(self):
        target = self.backups / f"{self.baseline_sha}.bundle"
        shutil.copyfile(self.baseline_bundle, target)
        target.chmod(0o400)
        result = self._save_baseline()
        self.assertEqual(result["bundle_sha256"], self.baseline_digest)
        self.assertTrue(target.exists())
        self.assertTrue((self.backups / f"{self.baseline_sha}.bundle.json").exists())

    def test_rejects_tree_digest_previous_main_and_unapproved_refs(self):
        cases = (
            {"source_tree": "f" * 40},
            {"expected_bundle_sha256": "a" * 64},
            {"previous_main_sha": "e" * 40},
        )
        for overrides in cases:
            values = {
                "source_tree": self.baseline_tree,
                "previous_main_sha": self.app_sha,
                "expected_bundle_sha256": self.baseline_digest,
            }
            values.update(overrides)
            with self.subTest(overrides=overrides), self.assertRaises((ValueError, RuntimeError)):
                installer.save_domestic_source_bundle(
                    self.baseline_bundle, source_sha=self.baseline_sha,
                    allow_baseline_transition=True, **values,
                )
        self.assertFalse((self.backups / f"{self.baseline_sha}.bundle").exists())

    def test_rejects_wrong_candidate_sha_and_low_free_disk(self):
        candidate = self.baseline_bundle
        candidate.chmod(0o400)
        with self.assertRaisesRegex(ValueError, "exactly the approved"):
            installer._verify_source_bundle_file(
                candidate, source_sha="f" * 40, source_tree=self.baseline_tree,
                previous_main_sha=self.app_sha, bundle_sha256=self.baseline_digest,
                allow_baseline_transition=True,
            )
        with mock.patch.object(installer.shutil, "disk_usage", return_value=SimpleNamespace(free=100)):
            with self.assertRaisesRegex(RuntimeError, "insufficient free disk"):
                installer.save_domestic_source_bundle(
                    self.baseline_bundle, source_sha=self.baseline_sha,
                    source_tree=self.baseline_tree, previous_main_sha=self.app_sha,
                    expected_bundle_sha256=self.baseline_digest,
                    allow_baseline_transition=True,
                )
        self.assertFalse((self.backups / f"{self.baseline_sha}.bundle").exists())

    def test_root_bundle_and_cursor_permissions_are_enforced(self):
        source_path = self.root / "root-owned-file"
        source_path.write_bytes(b"protected")

        def fake_lstat(_path, *, owner=0, group=0, mode=0o400):
            return SimpleNamespace(st_mode=stat.S_IFREG | mode, st_uid=owner, st_gid=group)

        with mock.patch.object(installer.Path, "lstat", autospec=True, side_effect=fake_lstat):
            self.assertEqual(assert_root_file(source_path, exact_mode=0o400).st_uid, 0)
        with mock.patch.object(installer.Path, "lstat", autospec=True, side_effect=lambda *_args: fake_lstat(source_path, owner=501)):
            with self.assertRaisesRegex(RuntimeError, "unsafe"):
                assert_root_file(source_path, exact_mode=0o400)
        with mock.patch.object(installer.Path, "lstat", autospec=True, side_effect=lambda *_args: SimpleNamespace(st_mode=stat.S_IFREG | 0o600, st_uid=0, st_gid=0)):
            with self.assertRaisesRegex(RuntimeError, "unsafe"):
                assert_root_file(source_path, exact_mode=0o400)

    def test_verify_cli_uses_source_backup_contract_flags(self):
        argv = [
            "domestic-promote.py", "--verify-domestic-source-backup",
            "--source-sha", self.baseline_sha,
            "--source-tree", self.baseline_tree,
            "--previous-main-sha", self.app_sha,
            "--source-bundle-sha256", self.baseline_digest,
            "--allow-baseline-transition",
        ]
        output = io.StringIO()
        expected = {"status": "verified", "_first_parent": [self.baseline_sha, self.app_sha]}
        with mock.patch.object(installer.sys, "argv", argv), mock.patch.object(installer.os, "geteuid", return_value=0), mock.patch.object(installer, "verify_domestic_source_backup", return_value=expected) as verify, redirect_stdout(output):
            installer.main()
        verify.assert_called_once_with(
            source_sha=self.baseline_sha, source_tree=self.baseline_tree,
            previous_main_sha=self.app_sha, expected_bundle_sha256=self.baseline_digest,
            allow_baseline_transition=True,
        )
        self.assertEqual(json.loads(output.getvalue()), {"status": "verified"})
    def test_baseline_transition_is_explicit_and_normal_candidate_uses_old_main_ref(self):
        self.baseline_bundle.chmod(0o400)
        with self.assertRaisesRegex(ValueError, "bundle refs do not match"):
            installer._verify_source_bundle_file(
                self.baseline_bundle, source_sha=self.baseline_sha, source_tree=self.baseline_tree,
                previous_main_sha=self.app_sha, bundle_sha256=self.baseline_digest,
            )
        normal = installer.save_domestic_source_bundle(
            self.next_bundle, source_sha=self.next_sha, source_tree=self.next_tree,
            previous_main_sha=self.baseline_sha, expected_bundle_sha256=self.next_digest,
        )
        self.assertEqual(normal["source_sha"], self.next_sha)

    def test_rejects_tampered_saved_bundle_and_receipt(self):
        self._save_baseline()
        backup = self.backups / f"{self.baseline_sha}.bundle"
        backup.chmod(0o600)
        backup.write_bytes(backup.read_bytes() + b"tamper")
        backup.chmod(0o400)
        with self.assertRaisesRegex(ValueError, "digest mismatch"):
            installer.verify_domestic_source_backup(
                source_sha=self.baseline_sha, source_tree=self.baseline_tree,
                previous_main_sha=self.app_sha, expected_bundle_sha256=self.baseline_digest,
                allow_baseline_transition=True,
            )
        backup.chmod(0o600)
        backup.write_bytes(self.baseline_bundle.read_bytes())
        backup.chmod(0o400)
        receipt_path = self.backups / f"{self.baseline_sha}.bundle.json"
        receipt = json.loads(receipt_path.read_text())
        receipt["source_tree"] = "a" * 40
        receipt_path.chmod(0o600)
        receipt_path.write_text(json.dumps(receipt))
        with self.assertRaisesRegex(ValueError, "receipt identity mismatch"):
            installer.verify_domestic_source_backup(
                source_sha=self.baseline_sha, source_tree=self.baseline_tree,
                previous_main_sha=self.app_sha, expected_bundle_sha256=self.baseline_digest,
                allow_baseline_transition=True,
            )

    def test_initialize_readback_and_same_identity_are_idempotent(self):
        self._save_baseline()
        state = installer.initialize_domestic_main(
            main_sha=self.baseline_sha,
            main_tree=self.baseline_tree,
            installed_app_sha=self.app_sha,
            installed_app_tree=self.app_tree,
            installed_manifest_sha256=self.manifest_sha,
            source_bundle_sha256=self.baseline_digest,
        )
        repeated = installer.initialize_domestic_main(
            main_sha=self.baseline_sha,
            main_tree=self.baseline_tree,
            installed_app_sha=self.app_sha,
            installed_app_tree=self.app_tree,
            installed_manifest_sha256=self.manifest_sha,
            source_bundle_sha256=self.baseline_digest,
        )
        self.assertEqual(repeated, state)
        output = installer.read_domestic_main()
        self.assertEqual(output["status"], "ready")
        self.assertEqual(output["cursor"]["main_sha"], self.baseline_sha)
        self.assertEqual(output["cursor"]["installed_app_sha"], self.app_sha)
        raw_receipt = (self.receipts / f"{self.app_sha}.json").read_bytes()
        self.assertEqual(output["install_receipt_sha256"], hashlib.sha256(raw_receipt).hexdigest())

    def test_unknown_production_install_receipt_does_not_create_main_cursor(self):
        self._save_baseline()
        receipt_path = self.receipts / f"{self.app_sha}.json"
        unknown = dict(self.install_receipt, technical_status="outcome_unknown")
        receipt_path.write_text(json.dumps(unknown, sort_keys=True) + "\n")
        with self.assertRaisesRegex(ValueError, "install receipt identity mismatch"):
            installer.initialize_domestic_main(
                main_sha=self.baseline_sha, main_tree=self.baseline_tree,
                installed_app_sha=self.app_sha, installed_app_tree=self.app_tree,
                installed_manifest_sha256=self.manifest_sha,
                source_bundle_sha256=self.baseline_digest,
            )
        self.assertFalse(installer.DOMESTIC_MAIN_STATE.exists())

    def test_install_receipt_mismatch_stale_cursor_cas_and_lost_ack_retry(self):
        self._save_baseline()
        installer.initialize_domestic_main(
            main_sha=self.baseline_sha, main_tree=self.baseline_tree,
            installed_app_sha=self.app_sha, installed_app_tree=self.app_tree,
            installed_manifest_sha256=self.manifest_sha, source_bundle_sha256=self.baseline_digest,
        )
        receipt_path = self.receipts / f"{self.app_sha}.json"
        original_receipt = receipt_path.read_bytes()
        receipt_path.write_bytes(original_receipt + b" ")
        with self.assertRaises((ValueError, RuntimeError)):
            installer.read_domestic_main()
        receipt_path.write_bytes(original_receipt)
        self._make_bundle(self.next_sha, self.baseline_sha)
        self.next_bundle = self.incoming / f"{self.next_sha}.bundle"
        self.next_digest = installer.digest(self.next_bundle)
        installer.save_domestic_source_bundle(
            self.next_bundle, source_sha=self.next_sha, source_tree=self.next_tree,
            previous_main_sha=self.baseline_sha, expected_bundle_sha256=self.next_digest,
        )
        recorded = installer.record_domestic_main(
            main_sha=self.next_sha, main_tree=self.next_tree,
            installed_app_sha=self.app_sha, installed_app_tree=self.app_tree,
            installed_manifest_sha256=self.manifest_sha,
            source_bundle_sha256=self.next_digest,
            expected_previous_main_sha=self.baseline_sha,
        )
        repeated = installer.record_domestic_main(
            main_sha=self.next_sha, main_tree=self.next_tree,
            installed_app_sha=self.app_sha, installed_app_tree=self.app_tree,
            installed_manifest_sha256=self.manifest_sha,
            source_bundle_sha256=self.next_digest,
            expected_previous_main_sha=self.baseline_sha,
        )
        self.assertEqual(repeated, recorded)
        self.assertEqual(installer.read_domestic_main()["cursor"]["main_sha"], self.next_sha)
        stale_bundle = self.incoming / f"{self.stale_sha}.bundle"
        installer.save_domestic_source_bundle(
            stale_bundle, source_sha=self.stale_sha, source_tree=self.stale_tree,
            previous_main_sha=self.app_sha, expected_bundle_sha256=self.stale_digest,
        )
        with self.assertRaisesRegex(RuntimeError, "compare-and-swap base mismatch"):
            installer.record_domestic_main(
                main_sha=self.stale_sha, main_tree=self.stale_tree,
                installed_app_sha=self.app_sha, installed_app_tree=self.app_tree,
                installed_manifest_sha256=self.manifest_sha,
                source_bundle_sha256=self.stale_digest,
                expected_previous_main_sha=self.app_sha,
            )

    def test_install_manifest_and_current_must_match_readback(self):
        self._save_baseline()
        installer.initialize_domestic_main(
            main_sha=self.baseline_sha, main_tree=self.baseline_tree,
            installed_app_sha=self.app_sha, installed_app_tree=self.app_tree,
            installed_manifest_sha256=self.manifest_sha, source_bundle_sha256=self.baseline_digest,
        )
        manifest = self.releases / self.app_sha / "release-files.sha256"
        manifest.write_text(manifest.read_text() + "\n")
        with self.assertRaises((ValueError, RuntimeError)):
            installer.read_domestic_main()


if __name__ == "__main__":
    unittest.main()
