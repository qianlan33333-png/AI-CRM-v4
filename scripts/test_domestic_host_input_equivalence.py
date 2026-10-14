import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))
import domestic_main_release as controller
import domestic_release_build as builder


class HostInputEquivalence(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="host-input-fixture-")
        self.addCleanup(self.temporary.cleanup)
        self.repo = Path(self.temporary.name)
        self.gitdir = self.repo / ".git"
        self.git("init", "--quiet")
        self.write("web/v3/app.ts", "original frontend")
        self.write("web/package-lock.json", "original dependency lock")
        for path in builder.APPLICATION_SOURCE_REGISTRIES:
            self.write(path, "original registry")
        self.host = self.commit()

    def git(self, *args):
        return subprocess.check_output(["git", "-C", str(self.repo),
            "-c", "user.name=fixture", "-c", "user.email=fixture@invalid",
            "-c", "commit.gpgsign=false", "-c", "core.filemode=true",
            "-c", "core.hooksPath=/dev/null", *args], text=True).strip()

    def write(self, path, text):
        target = self.repo / path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(text)

    def commit(self):
        self.git("add", "-A")
        self.git("commit", "--quiet", "-m", "synthetic frontend inputs")
        return self.git("rev-parse", "HEAD")

    def test_registry_only_and_backend_change_allow_reused_host(self):
        for path in builder.APPLICATION_SOURCE_REGISTRIES:
            self.assertFalse(builder._requires_frontend_build(path))
            self.write(path, "new application registry")
        self.write("backend.go", "backend revision")
        controller._verify_host_frontend_inputs(self.gitdir, self.host, self.commit())

    def test_frontend_input_drift_is_rejected(self):
        for change in ("code", "lock", "unknown_registry", "delete", "rename", "mode", "chinese", "quote", "newline"):
            with self.subTest(change=change):
                self.git("reset", "--hard", "--quiet", self.host)
                target = self.repo / "web/v3/app.ts"
                if change == "code":
                    self.write("web/v3/app.ts", "changed frontend")
                elif change == "lock":
                    self.write("web/package-lock.json", "changed dependencies")
                elif change == "unknown_registry":
                    self.write("web/donor-sources/source-index.json.extra", "not excluded")
                elif change == "delete":
                    target.unlink()
                elif change == "rename":
                    target.rename(target.with_name("renamed.ts"))
                elif change == "mode":
                    target.chmod(0o755)
                else:
                    path = {"chinese": "web/中文.ts", "quote": 'web/quoted".ts', "newline": "web/newline\n.ts"}[change]
                    self.write(path, "unregistered changed input")
                with self.assertRaisesRegex(controller.ReleaseError, "frontend inputs differ"):
                    controller._verify_host_frontend_inputs(self.gitdir, self.host, self.commit())

    def test_missing_commit_or_web_object_is_rejected(self):
        with self.assertRaises(controller.ReleaseError):
            controller._verify_host_frontend_inputs(self.gitdir, self.host, "0" * 40)
        self.git("rm", "-r", "--quiet", "web")
        missing = self.commit()
        with self.assertRaises(controller.ReleaseError):
            controller._verify_host_frontend_inputs(self.gitdir, self.host, missing)

    def test_web_root_shape_change_is_rejected(self):
        self.git("rm", "-r", "--quiet", "web")
        self.write("web", "not a directory")
        with self.assertRaisesRegex(controller.ReleaseError, "not a Git tree"):
            controller._verify_host_frontend_inputs(self.gitdir, self.host, self.commit())

    def test_repair_is_source_only(self):
        result = builder.classify_paths([
            "scripts/domestic_main_release.py", "scripts/test_domestic_host_input_equivalence.py",
            "cmd/aicrm/installed_survey_provenance_test.go",
            "cmd/aicrm/public_survey_installed_fixture_test.go", "docs/release-checks.json"])
        self.assertFalse(result.runtime_changed)
        self.assertEqual(result.build_mode, "controller_only")
        self.assertEqual(result.controller_files, ["scripts/domestic_main_release.py"])


if __name__ == "__main__":
    unittest.main(verbosity=2)
