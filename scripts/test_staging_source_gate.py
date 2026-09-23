import importlib.util
import json
import shutil
import subprocess
import sys
import tempfile
import unittest
from datetime import datetime, timedelta, timezone
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "scripts"))
from test_release_freshness import FreshnessTests
from release_freshness import build_attestation, sign
spec = importlib.util.spec_from_file_location("staging_source", ROOT / "scripts" / "verify-staging-source.py")
source = importlib.util.module_from_spec(spec)
spec.loader.exec_module(source)


@unittest.skipUnless(shutil.which("ssh-keygen"), "ssh-keygen required")
class StagingSourceGateTests(unittest.TestCase):
    def fixture(self, root, *, issued=None, main=None, preview=None, tree=None):
        repo, bundle, key, allowed, _, base, base_tree, head, head_tree, merged = FreshnessTests().fixture(root)
        manifest = root / "candidate.json"
        manifest.write_text(json.dumps({"schema": 1, "base_main_sha": base,
                                        "pr_head_sha": head, "merge_preview_sha": merged,
                                        "tree_sha": tree or head_tree}))
        at = issued or datetime.now(timezone.utc)
        value = build_attestation(repository="qianlan33333-png/AI-CRM-v4", branch="main",
                                  stage="source", main_sha=main or base, main_tree=base_tree,
                                  pr_head_sha=head, pr_head_tree=head_tree,
                                  merge_preview_sha=preview or merged,
                                  candidate_tree_sha=tree or head_tree, bundle=bundle,
                                  package=None, identity=source.IDENTITY, queried_at=at, now=at,
                                  ttl_seconds=60 if issued else 900)
        attestation = root / "attestation.json"
        signature = root / "attestation.sig"
        sign(value, attestation, signature, key)
        return repo, bundle, manifest, attestation, signature, allowed, value

    def check(self, files, *, repo=None, objects=True):
        repository, bundle, manifest, attestation, signature, allowed, _ = files
        return source.check(manifest, bundle, attestation, signature, allowed,
                            repo or repository, objects)

    def test_valid_source_and_wrong_repository_fail(self):
        with tempfile.TemporaryDirectory() as t:
            files = self.fixture(Path(t))
            self.assertEqual(self.check(files)["repository"], source.REPOSITORY)
            value = json.loads(files[3].read_text())
            value["repository"] = "qianlan33333-png/AI-CRM-v3"
            files[3].write_text(json.dumps(value))
            with self.assertRaises(ValueError):
                self.check(files)

    def test_missing_signature_expiry_and_changed_bundle_fail(self):
        with tempfile.TemporaryDirectory() as t:
            files = self.fixture(Path(t), issued=datetime.now(timezone.utc) - timedelta(minutes=20))
            with self.assertRaisesRegex(ValueError, "expired"):
                self.check(files)
        with tempfile.TemporaryDirectory() as t:
            files = self.fixture(Path(t))
            files[4].unlink()
            with self.assertRaisesRegex(ValueError, "signature invalid"):
                self.check(files)
        with tempfile.TemporaryDirectory() as t:
            files = self.fixture(Path(t))
            files[1].write_bytes(files[1].read_bytes() + b"tampered")
            with self.assertRaisesRegex(ValueError, "bundle digest"):
                self.check(files)

    def test_signed_wrong_tree_and_parent_fail(self):
        with tempfile.TemporaryDirectory() as t:
            files = self.fixture(Path(t), tree="a" * 40)
            with self.assertRaisesRegex(ValueError, "tree mismatch"):
                self.check(files)
        with tempfile.TemporaryDirectory() as t:
            root = Path(t)
            files = self.fixture(root)
            repo, _, manifest, *_ = files
            candidate = json.loads(manifest.read_text())
            wrong = subprocess.check_output(["git", "-C", str(repo), "commit-tree",
                                             candidate["tree_sha"], "-p", candidate["pr_head_sha"],
                                             "-p", candidate["base_main_sha"]],
                                            input=b"wrong parents\n").decode().strip()
            candidate["merge_preview_sha"] = wrong
            manifest.write_text(json.dumps(candidate))
            subprocess.run(["git", "-C", str(repo), "update-ref", "refs/candidates/preview", wrong], check=True)
            files[1].unlink()
            subprocess.run(["git", "-C", str(repo), "bundle", "create", str(files[1]), "--all"], check=True)
            signed = json.loads(files[3].read_text())
            signed["merge_preview_sha"] = wrong
            signed["bundle_sha256"] = __import__("hashlib").sha256(files[1].read_bytes()).hexdigest()
            files[3].unlink()
            files[4].unlink()
            sign(signed, files[3], files[4], root / "signing")
            with self.assertRaisesRegex(ValueError, "parents mismatch"):
                self.check(files)

    def test_missing_bundle_prerequisite_fails_on_remote(self):
        with tempfile.TemporaryDirectory() as t:
            root = Path(t)
            files = self.fixture(root)
            repo, _, manifest, _, _, _, _ = files
            candidate = json.loads(manifest.read_text())
            incremental = root / "incremental.bundle"
            subprocess.run(["git", "-C", str(repo), "bundle", "create", str(incremental),
                            "refs/candidates/preview", "^" + candidate["base_main_sha"]], check=True)
            empty = root / "empty.git"
            subprocess.run(["git", "init", "--bare", "-q", str(empty)], check=True)
            result = subprocess.run(["git", "-C", str(empty), "bundle", "verify",
                                     str(incremental)], capture_output=True)
            self.assertNotEqual(result.returncode, 0)

    def test_entry_and_remote_verify_before_source_execution(self):
        local = (ROOT / "deploy/build-release-on-staging.sh").read_text()
        remote = (ROOT / "deploy/build-release-on-staging-remote.sh").read_text()
        self.assertLess(local.index("scripts/verify-staging-source.py --manifest"),
                        local.index("ssh \"${flags[@]}\""))
        self.assertLess(remote.index("python3 \"$verifier\""),
                        remote.index("git clone --no-checkout"))
        self.assertLess(remote.index("--require-objects"),
                        remote.index("git clone --no-checkout"))
        self.assertIn("'repository':'AI-CRM-v4'", remote)
        self.assertIn('"repository": "AI-CRM-v4"',
                      (ROOT / "scripts/write-release-provenance.py").read_text())


if __name__ == "__main__":
    unittest.main()
