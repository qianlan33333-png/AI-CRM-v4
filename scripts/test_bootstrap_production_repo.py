import json
import hashlib
import subprocess
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).with_name("bootstrap_production_repo.py")


class BootstrapTests(unittest.TestCase):
    def make_source(self, root):
        source = root / "source"; source.mkdir()
        subprocess.run(["git", "-C", str(source), "init", "-q"], check=True)
        subprocess.run(["git", "-C", str(source), "config", "user.name", "test"], check=True)
        subprocess.run(["git", "-C", str(source), "config", "user.email", "test@example.invalid"], check=True)
        (source / "main.txt").write_text("production\n")
        subprocess.run(["git", "-C", str(source), "add", "main.txt"], check=True)
        subprocess.run(["git", "-C", str(source), "commit", "-qm", "production"], check=True)
        commit = subprocess.check_output(["git", "-C", str(source), "rev-parse", "HEAD"], text=True).strip()
        tree = subprocess.check_output(["git", "-C", str(source), "rev-parse", "HEAD^{tree}"], text=True).strip()
        return source, commit, tree

    def test_requires_exact_released_receipt_and_preserves_source_tree(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp); source = root / "source"; source.mkdir()
            subprocess.run(["git", "-C", str(source), "init", "-q"], check=True)
            subprocess.run(["git", "-C", str(source), "config", "user.name", "test"], check=True)
            subprocess.run(["git", "-C", str(source), "config", "user.email", "test@example.invalid"], check=True)
            (source / "main.txt").write_text("production\n")
            subprocess.run(["git", "-C", str(source), "add", "main.txt"], check=True)
            subprocess.run(["git", "-C", str(source), "commit", "-qm", "production"], check=True)
            commit = subprocess.check_output(["git", "-C", str(source), "rev-parse", "HEAD"], text=True).strip()
            tree = subprocess.check_output(["git", "-C", str(source), "rev-parse", "HEAD^{tree}"], text=True).strip()
            package = root / "package.tar"; package.write_bytes(b"accepted package")
            readback = root / "readback.json"; readback.write_text("{\"verified\":true}\n")
            observed = root / "observation.json"; observed.write_text("{\"passed\":true}\n")
            complete = {"status": "released", "environment": "production", "commit_sha": commit, "tree_sha": tree,
                        "package_path": str(package), "package_sha256": hashlib.sha256(package.read_bytes()).hexdigest(),
                        "business_receipts": [{"work_item": item, "status": "accepted", "readback_evidence_path": str(readback), "readback_evidence_sha256": hashlib.sha256(readback.read_bytes()).hexdigest()} for item in ("alipay-config", "welcome-group-invite", "shipping-address")],
                        "observation": {"status": "passed", "evidence_path": str(observed), "evidence_sha256": hashlib.sha256(observed.read_bytes()).hexdigest()}}
            receipt = root / "receipt.json"; receipt.write_text(json.dumps(complete))
            destination = root / "new"
            result = subprocess.run(["python3", str(SCRIPT), "--source", str(source), "--receipt", str(receipt), "--destination", str(destination)], capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual((destination / "main.txt").read_text(), "production\n")
            self.assertEqual(subprocess.check_output(["git", "-C", str(destination), "rev-parse", "HEAD^{tree}"], text=True).strip(), tree)
            self.assertEqual(json.loads((root / "new-production-provenance.json").read_text())["source_tree_sha"], tree)

    def test_allows_observing_baseline_only_with_exact_deferred_technical_readback(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp); source, commit, tree = self.make_source(root)
            package = root / "package.tar"; package.write_bytes(b"same production package")
            package_sha = hashlib.sha256(package.read_bytes()).hexdigest()
            evidence = root / "production-readback.json"; evidence.write_text(json.dumps({"active_sha": commit, "tree_sha": tree, "package_sha256": package_sha, "readyz": "ready"}))
            receipt_value = {
                "status": "observing", "environment": "production", "commit_sha": commit, "tree_sha": tree,
                "package_path": str(package), "package_sha256": package_sha,
                "business_acceptance": {"status": "business_acceptance_deferred_to_user", "exception_id": "current-three-project-post-production-user-acceptance-v1", "scope": ["alipay-config", "welcome-group-invite", "shipping-address"], "owner": "user"},
                "production_readback": {"release_sha": commit, "tree_sha": tree, "package_sha256": package_sha, "readyz_status": "ready", "evidence_path": str(evidence), "evidence_sha256": hashlib.sha256(evidence.read_bytes()).hexdigest()},
            }
            receipt = root / "receipt.json"; receipt.write_text(json.dumps(receipt_value)); destination = root / "new"
            result = subprocess.run(["python3", str(SCRIPT), "--source", str(source), "--receipt", str(receipt), "--destination", str(destination)], capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            provenance = json.loads((root / "new-production-provenance.json").read_text())
            self.assertEqual(provenance["business_acceptance_status"], "business_acceptance_deferred_to_user")
            self.assertEqual(provenance["release_status"], "observing")

    def test_rejects_deferred_baseline_with_mismatched_active_readback(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp); source, commit, tree = self.make_source(root)
            package = root / "package.tar"; package.write_bytes(b"same production package")
            evidence = root / "production-readback.json"; evidence.write_text("{}")
            receipt = root / "receipt.json"; receipt.write_text(json.dumps({
                "status": "observing", "environment": "production", "commit_sha": commit, "tree_sha": tree,
                "package_path": str(package), "package_sha256": hashlib.sha256(package.read_bytes()).hexdigest(),
                "business_acceptance": {"status": "business_acceptance_deferred_to_user", "exception_id": "current-three-project-post-production-user-acceptance-v1", "scope": ["alipay-config", "welcome-group-invite", "shipping-address"], "owner": "user"},
                "production_readback": {"release_sha": "f" * 40, "tree_sha": tree, "package_sha256": hashlib.sha256(package.read_bytes()).hexdigest(), "readyz_status": "ready", "evidence_path": str(evidence), "evidence_sha256": hashlib.sha256(evidence.read_bytes()).hexdigest()},
            }))
            result = subprocess.run(["python3", str(SCRIPT), "--source", str(source), "--receipt", str(receipt), "--destination", str(root / "new")], capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertFalse((root / "new").exists())

    def test_rejects_installed_or_partial_batch(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp); source = root / "source"; source.mkdir()
            subprocess.run(["git", "-C", str(source), "init", "-q"], check=True)
            subprocess.run(["git", "-C", str(source), "config", "user.name", "test"], check=True)
            subprocess.run(["git", "-C", str(source), "config", "user.email", "test@example.invalid"], check=True)
            (source / "file").write_text("source\n")
            subprocess.run(["git", "-C", str(source), "add", "file"], check=True)
            subprocess.run(["git", "-C", str(source), "commit", "-qm", "source"], check=True)
            commit = subprocess.check_output(["git", "-C", str(source), "rev-parse", "HEAD"], text=True).strip()
            tree = subprocess.check_output(["git", "-C", str(source), "rev-parse", "HEAD^{tree}"], text=True).strip()
            receipt = root / "receipt.json"
            for status, items in (("production", []), ("released", [{"work_item": "alipay-config", "status": "accepted", "readback_evidence_sha256": "b"*64}])):
                receipt.write_text(json.dumps({"status": status, "environment": "production", "commit_sha": commit, "tree_sha": tree, "package_sha256": "a"*64, "business_receipts": items, "observation": {"status": "passed", "evidence_sha256": "c"*64}}))
                result = subprocess.run(["python3", str(SCRIPT), "--source", str(source), "--receipt", str(receipt), "--destination", str(root / "new")], capture_output=True, text=True)
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse((root / "new").exists())


if __name__ == "__main__":
    unittest.main()
