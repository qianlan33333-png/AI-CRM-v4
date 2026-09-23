import importlib.util
import io
import json
import shutil
import subprocess
import sys
import tempfile
import unittest
from datetime import datetime, timedelta, timezone
from pathlib import Path
from unittest.mock import patch

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

    def test_release_tool_changes_do_not_require_app_staging_receipt(self):
        gate_spec = importlib.util.spec_from_file_location(
            "local_first_gate", ROOT / "scripts/ci/local_first_gate.py")
        gate = importlib.util.module_from_spec(gate_spec)
        gate_spec.loader.exec_module(gate)
        changed = ("deploy/build-release-on-staging.sh\n"
                   "scripts/verify-staging-source.py\n"
                   "scripts/test_staging_source_gate.py\n"
                   "scripts/release_freshness.py\n"
                   "scripts/write-release-provenance.py\n")
        with patch.dict("os.environ", {"PR_BASE_SHA": "a" * 40}):
            with patch.object(gate.subprocess, "check_output", return_value=changed):
                self.assertFalse(gate.requires_staging_receipt("b" * 40))
            with patch.object(gate.subprocess, "check_output",
                              return_value=changed + "internal/payment/app/service.go\n"):
                self.assertTrue(gate.requires_staging_receipt("b" * 40))

    def test_test_fixtures_require_ci_but_no_app_receipt(self):
        gate_spec = importlib.util.spec_from_file_location(
            "local_first_gate", ROOT / "scripts/ci/local_first_gate.py")
        gate = importlib.util.module_from_spec(gate_spec)
        gate_spec.loader.exec_module(gate)
        changed = ("scripts/test-install-release-ordering.sh\n"
                   "cmd/aicrm/core_operations_chromium_journey.mjs\n"
                   "internal/payment/app/service_test.go\n"
                   "scripts/release_control.py\n"
                   "scripts/release_coordinator.py\n"
                   "scripts/release_events.py\n"
                   "scripts/test_release_control.py\n")
        with patch.dict("os.environ", {"PR_BASE_SHA": "a" * 40}):
            with patch.object(gate.subprocess, "check_output", return_value=changed):
                self.assertFalse(gate.requires_staging_receipt("b" * 40))
            with patch.object(gate.subprocess, "check_output",
                              return_value=changed + "internal/automation/app/policies.go\n"):
                self.assertTrue(gate.requires_staging_receipt("b" * 40))

    def test_pr_code_gate_does_not_claim_staging_acceptance(self):
        gate_spec = importlib.util.spec_from_file_location(
            "local_first_gate", ROOT / "scripts/ci/local_first_gate.py")
        gate = importlib.util.module_from_spec(gate_spec)
        gate_spec.loader.exec_module(gate)
        head, tree = "b" * 40, "c" * 40
        environment = {"GITHUB_EVENT_NAME": "pull_request", "PR_HEAD_SHA": head,
                       "CI_NEEDS": json.dumps({"plan": {"outputs": {"mode": "full"}}})}
        for label in ("real_receipt", "fabricated_link", "no_receipt"):
            with self.subTest(label=label), patch.dict("os.environ", environment):
                with patch.object(gate, "requires_staging_receipt", return_value=True), \
                        patch.object(gate.subprocess, "check_output", return_value=tree), \
                        patch("sys.stdout", new_callable=io.StringIO) as output:
                    self.assertEqual(gate.main(), 0)
                    self.assertEqual(json.loads(output.getvalue())["staging"], "pending_release_handoff_validation")
        with patch.dict("os.environ", {**environment, "CI_NEEDS": json.dumps({"plan": {"outputs": {"mode": "light"}}})}):
            with patch.object(gate, "requires_staging_receipt", return_value=True):
                with self.assertRaisesRegex(SystemExit, "requires full code CI"):
                    gate.main()


if __name__ == "__main__":
    unittest.main()
