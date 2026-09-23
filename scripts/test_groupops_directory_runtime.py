import os
from pathlib import Path
import subprocess
import tempfile
import unittest


SCRIPT = Path(__file__).resolve().parents[1] / "deploy/configure-groupops-directory-runtime.py"


class DirectoryRuntimeTest(unittest.TestCase):
    def run_config(self, source):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        path = Path(directory.name) / "runtime.env"
        path.write_text(source)
        result = subprocess.run(
            ["python3", str(SCRIPT), "a" * 40],
            env={**os.environ, "AICRM_RUNTIME_ENV_FILE": str(path)},
            text=True, capture_output=True,
        )
        return path, result

    def test_read_activation_preserves_dispatch_and_secrets(self):
        source = "AICRM_WECOM_ENABLED=true\nAICRM_WECOM_CONTACT_SECRET=test-private-value\nAICRM_GROUP_OPS_PROVIDER_ENABLED=false\nAICRM_GROUP_OPS_PROVIDER_READ_ENABLED=false\n"
        path, result = self.run_config(source)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("AICRM_GROUP_OPS_PROVIDER_READ_ENABLED=true", path.read_text())
        self.assertIn("AICRM_GROUP_OPS_PROVIDER_ENABLED=false", path.read_text())
        self.assertNotIn("test-private-value", result.stdout + result.stderr)
        self.assertEqual(path.stat().st_mode & 0o777, 0o600)

    def test_missing_prerequisite_and_duplicate_are_not_applied(self):
        for source in ("AICRM_WECOM_ENABLED=false\n", "AICRM_WECOM_ENABLED=true\nAICRM_WECOM_ENABLED=false\n"):
            path, result = self.run_config(source)
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual(path.read_text(), source)


if __name__ == "__main__":
    unittest.main()
