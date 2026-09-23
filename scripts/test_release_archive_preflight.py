#!/usr/bin/env python3
from __future__ import annotations

import io
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile
import unittest

import release_archive_preflight as preflight


ROOT = Path(__file__).resolve().parents[1]
ELF_AMD64_HEADER = bytearray(64)
ELF_AMD64_HEADER[:4] = b"\x7fELF"
ELF_AMD64_HEADER[4] = 2
ELF_AMD64_HEADER[5] = 1
ELF_AMD64_HEADER[18:20] = b"\x3e\x00"


def write_archive(path: Path, entries: list[tuple[str, bytes, int, bytes | None]]) -> None:
    with tarfile.open(path, "w:gz", format=tarfile.PAX_FORMAT) as bundle:
        for name, data, mode, member_type in entries:
            info = tarfile.TarInfo(name)
            info.mode = mode
            if member_type is not None:
                info.type = member_type
            if info.type in (tarfile.SYMTYPE, tarfile.LNKTYPE):
                info.linkname = data.decode("utf-8")
                info.size = 0
                bundle.addfile(info)
            elif info.type in (tarfile.FIFOTYPE, tarfile.CHRTYPE, tarfile.BLKTYPE):
                info.size = 0
                bundle.addfile(info)
            else:
                info.size = len(data)
                bundle.addfile(info, io.BytesIO(data))


def valid_entries() -> list[tuple[str, bytes, int, bytes | None]]:
    return [
        ("bin/aicrm", bytes(ELF_AMD64_HEADER), 0o755, None),
        ("migrations/0001_test.sql", b"-- migration fixture\n", 0o644, None),
        ("release-files.sha256", b"", 0o644, None),
    ]


class ArchivePreflightTests(unittest.TestCase):
    def test_accepts_flat_archive_root_and_extracts_actual_bin(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive = root / "release.tar.gz"
            output = root / "check"
            output.mkdir()
            write_archive(archive, valid_entries())

            self.assertEqual(preflight.prepare_release_archive(archive, output), 3)
            self.assertEqual((output / "bin/aicrm").read_bytes()[:20], bytes(ELF_AMD64_HEADER[:20]))
            self.assertTrue(os.access(output / "bin/aicrm", os.X_OK))
            self.assertTrue((output / "migrations/0001_test.sql").is_file())

    def test_rejects_release_wrapper_layout_before_extracting(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive = root / "wrapped.tar.gz"
            output = root / "check"
            output.mkdir()
            write_archive(archive, [
                ("release/bin/aicrm", bytes(ELF_AMD64_HEADER), 0o755, None),
                ("release/migrations/0001_test.sql", b"-- fixture\n", 0o644, None),
                ("release/release-files.sha256", b"", 0o644, None),
            ])

            with self.assertRaisesRegex(preflight.ArchiveLayoutError, "root bin/"):
                preflight.prepare_release_archive(archive, output)
            self.assertEqual(list(output.iterdir()), [])

    def test_rejects_missing_bin_migrations_or_manifest(self):
        cases = [
            ([entry for entry in valid_entries() if not entry[0].startswith("bin/")], "root bin/"),
            ([
                ("bin/", b"", 0o755, tarfile.DIRTYPE),
                *[entry for entry in valid_entries() if not entry[0].startswith("bin/")],
            ], "root bin/"),
            ([entry for entry in valid_entries() if not entry[0].startswith("migrations/")], "root migrations/"),
            ([entry for entry in valid_entries() if entry[0] != "release-files.sha256"], "release-files.sha256"),
        ]
        for entries, message in cases:
            with self.subTest(message=message), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                archive = root / "invalid.tar.gz"
                output = root / "check"
                output.mkdir()
                write_archive(archive, entries)
                with self.assertRaisesRegex(preflight.ArchiveLayoutError, message):
                    preflight.prepare_release_archive(archive, output)
                self.assertEqual(list(output.iterdir()), [])

    def test_rejects_unsafe_duplicate_and_non_regular_members(self):
        cases = [
            (valid_entries() + [("../outside", b"escape", 0o644, None)], "unsafe member path"),
            (valid_entries() + [("/absolute", b"escape", 0o644, None)], "absolute member path"),
            (valid_entries() + [("bin/aicrm", b"duplicate", 0o755, None)], "duplicate member path"),
            (valid_entries() + [("bin/link", b"aicrm", 0o777, tarfile.SYMTYPE)], "non-regular member"),
            (valid_entries() + [("bin/hardlink", b"bin/aicrm", 0o777, tarfile.LNKTYPE)], "non-regular member"),
            (valid_entries() + [("bin/pipe", b"", 0o644, tarfile.FIFOTYPE)], "non-regular member"),
            (valid_entries() + [("bin/._metadata", b"appledouble", 0o644, None)], "AppleDouble"),
        ]
        for entries, message in cases:
            with self.subTest(message=message), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                archive = root / "invalid.tar.gz"
                output = root / "check"
                output.mkdir()
                write_archive(archive, entries)
                with self.assertRaisesRegex(preflight.ArchiveLayoutError, message):
                    preflight.prepare_release_archive(archive, output)
                self.assertEqual(list(output.iterdir()), [])

    def test_deploy_script_checks_input_archive_and_stops_before_ssh_on_bad_layout(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            checkout = root / "checkout"
            scripts = checkout / "scripts"
            scripts.mkdir(parents=True)
            for name in (
                "deploy-release-local.sh",
                "release_archive_preflight.py",
                "check-release-binaries.py",
                "check-migration-sequence.py",
            ):
                shutil.copy2(ROOT / "scripts" / name, scripts / name)
            migrations = checkout / "migrations"
            migrations.mkdir()
            (migrations / "0001_preflight_fixture.sql").write_text("-- fixture\n")
            subprocess.run(["git", "init", "-q", "-b", "main", str(checkout)], check=True)
            subprocess.run(["git", "-C", str(checkout), "config", "user.name", "archive fixture"], check=True)
            subprocess.run(["git", "-C", str(checkout), "config", "user.email", "fixture@example.invalid"], check=True)
            subprocess.run(["git", "-C", str(checkout), "add", "scripts", "migrations"], check=True)
            subprocess.run(["git", "-C", str(checkout), "commit", "-qm", "fixture baseline"], check=True)
            sha = subprocess.check_output(["git", "-C", str(checkout), "rev-parse", "HEAD"], text=True).strip()
            subprocess.run(["git", "-C", str(checkout), "update-ref", "refs/remotes/origin/main", sha], check=True)

            ssh_bin = root / "fake-bin"
            ssh_bin.mkdir()
            ssh_log = root / "ssh.log"
            fake_ssh = ssh_bin / "ssh"
            fake_ssh.write_text("#!/bin/sh\nprintf '%s\\n' called >> \"$SSH_LOG\"\nexit 91\n")
            fake_ssh.chmod(0o755)
            key = root / "dummy-key"
            key.write_text("test key\n")
            known_hosts = root / "known_hosts"
            known_hosts.write_text("test-host ssh-ed25519 AAAA\n")

            def run_deploy(entries: list[tuple[str, bytes, int, bytes | None]], stale_release: bool) -> subprocess.CompletedProcess[str]:
                archive = root / f"aicrm-{sha}.tar.gz"
                if archive.exists():
                    archive.unlink()
                write_archive(archive, entries)
                release_root = checkout / "release"
                shutil.rmtree(release_root, ignore_errors=True)
                if stale_release:
                    stale_bin = release_root / "bin"
                    stale_bin.mkdir(parents=True)
                    stale_file = stale_bin / "stale"
                    stale_file.write_bytes(b"not an ELF from the input package")
                    stale_file.chmod(0o755)
                env = dict(os.environ)
                env.update({
                    "DEPLOY_TARGET": "unused.invalid",
                    "DEPLOY_KEY": str(key),
                    "DEPLOY_KNOWN_HOSTS": str(known_hosts),
                    "PATH": str(ssh_bin) + os.pathsep + env.get("PATH", ""),
                    "SSH_LOG": str(ssh_log),
                })
                return subprocess.run(
                    ["bash", "scripts/deploy-release-local.sh", str(archive), sha],
                    cwd=checkout, env=env, text=True, capture_output=True, check=False,
                )

            valid_without_local_release = run_deploy(valid_entries(), stale_release=False)
            self.assertEqual(valid_without_local_release.returncode, 91, valid_without_local_release.stderr)
            self.assertTrue(ssh_log.exists(), "valid flat-root archive must reach the mocked SSH boundary")
            ssh_log.unlink()

            valid_with_stale_release = run_deploy(valid_entries(), stale_release=True)
            self.assertEqual(valid_with_stale_release.returncode, 91, valid_with_stale_release.stderr)
            self.assertTrue(ssh_log.exists(), "stale release/bin must not replace the input archive check")
            ssh_log.unlink()

            wrapped = run_deploy([
                ("release/bin/aicrm", bytes(ELF_AMD64_HEADER), 0o755, None),
                ("release/migrations/0001_test.sql", b"-- fixture\n", 0o644, None),
                ("release/release-files.sha256", b"", 0o644, None),
            ], stale_release=True)
            self.assertNotEqual(wrapped.returncode, 0)
            self.assertIn("release archive rejected", wrapped.stderr)
            self.assertFalse(ssh_log.exists(), "invalid archive must fail before any SSH call")

            traversal = run_deploy(
                valid_entries() + [("../../outside", b"escape", 0o644, None)],
                stale_release=False,
            )
            self.assertNotEqual(traversal.returncode, 0)
            self.assertIn("unsafe member path", traversal.stderr)
            self.assertFalse(ssh_log.exists(), "unsafe archive must fail before any SSH call")


if __name__ == "__main__":
    unittest.main(verbosity=2)
