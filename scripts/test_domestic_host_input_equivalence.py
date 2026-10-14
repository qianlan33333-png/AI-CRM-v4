import subprocess
import json
import os
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

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


class CleanupAnchor(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="cleanup-anchor-fixture-")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.prep = self.root / "prep"; self.prep.mkdir()
        self.binary = self.root / "bin"; self.binary.mkdir()
        self.log = self.root / "calls.jsonl"
        self.clone = "aicrm_test_clone_" + "a" * 16 + "_acceptance_test"
        self.config = {"check_database_url": "postgresql://fixture:synthetic@127.0.0.1:5432/aicrm_ci?sslmode=disable",
            "_check_attempt_database": self.clone,
            "build_path": str(self.binary) + ":/opt/aicrm/toolchain/npm/bin:/usr/bin:/bin"}
        self.registry = self.prep / ("database-" + self.clone + ".json")
        self.registry.write_text(json.dumps({"database": self.clone}))
        psql = self.binary / "psql"
        psql.write_text("#!" + sys.executable + "\n" + '''import os,sys,json
from pathlib import Path
sql=sys.argv[-1]
with Path(os.environ['FAKE_PSQL_LOG']).open('a') as f:
 f.write(json.dumps({'database':os.environ['PGDATABASE'],'role':os.environ['PGUSER'],'host':os.environ['PGHOST'],'sslmode':os.environ['PGSSLMODE'],'sql':sql})+'\\n')
if sql.startswith('SELECT'):print('f' if os.environ.get('FAKE_WRONG_OWNER') else 't')
elif os.environ.get('FAKE_DROP_FAILURE') or os.environ['PGDATABASE'] in sql:sys.exit(1)
''')
        psql.chmod(0o755)

    def run_cleanup(self, **flags):
        original = dict(self.config)
        def execute(config, command, **kwargs):
            environment = controller._check_env(config)
            self.assertEqual(environment['AICRM_DATABASE_URL'], original['check_database_url'])
            environment.update(FAKE_PSQL_LOG=str(self.log), **flags)
            return subprocess.run([sys.executable, *command[1:]], env=environment,
                capture_output=True, text=True, timeout=10)
        with patch.object(controller, '_require_cleanup_lock'), patch.object(controller, '_build_command', side_effect=execute):
            try: controller._cleanup_attempt_databases(self.config, self.prep)
            finally: self.assertEqual(self.config, original)

    def test_deleting_execution_clone_uses_anchor_and_preserves_connection_identity(self):
        self.run_cleanup()
        calls=[json.loads(x) for x in self.log.read_text().splitlines()]
        self.assertEqual(len(calls),2)
        self.assertTrue(all(c['database']=='aicrm_ci' and c['role']=='fixture' and c['host']=='127.0.0.1' and c['sslmode']=='disable' for c in calls))
        self.assertTrue(calls[-1]['sql'].startswith('DROP DATABASE'))
        self.assertIn(self.clone,calls[-1]['sql'])
        self.assertFalse(self.registry.exists())

    def test_wrong_owner_preserves_inventory_without_drop(self):
        with self.assertRaises(controller.CheckEnvironmentError):self.run_cleanup(FAKE_WRONG_OWNER='1')
        self.assertTrue(self.registry.exists())
        self.assertNotIn('DROP DATABASE',self.log.read_text())

    def test_drop_failure_preserves_inventory(self):
        with self.assertRaises(controller.CheckEnvironmentError):self.run_cleanup(FAKE_DROP_FAILURE='1')
        self.assertTrue(self.registry.exists())

    def test_unregistered_database_is_never_dropped(self):
        self.registry.unlink(); invalid=self.prep/'database-production.json'
        invalid.write_text(json.dumps({'database':'production'}))
        with self.assertRaises(controller.CheckEnvironmentError):self.run_cleanup()
        self.assertTrue(invalid.exists());self.assertFalse(self.log.exists())


if __name__ == "__main__":
    unittest.main(verbosity=2)
