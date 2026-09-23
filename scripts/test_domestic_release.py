import importlib.util
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock


ROOT = Path(__file__).resolve().parents[1]


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


worker = load("domestic_release", ROOT / "scripts/domestic_release.py")
installer = load("domestic_promote", ROOT / "deploy/domestic-promote.py")


class DomesticReleaseTest(unittest.TestCase):
    def test_first_parent_two_successive_merges(self):
        with tempfile.TemporaryDirectory() as temp:
            repo = Path(temp)
            subprocess.run(["git", "init", "-q", str(repo)], check=True)
            subprocess.run(["git", "-C", str(repo), "config", "user.email", "ci@example.invalid"], check=True)
            subprocess.run(["git", "-C", str(repo), "config", "user.name", "CI"], check=True)
            shas = []
            for number in range(3):
                (repo / "file").write_text(str(number))
                subprocess.run(["git", "-C", str(repo), "add", "file"], check=True)
                subprocess.run(["git", "-C", str(repo), "commit", "-qm", str(number)], check=True)
                shas.append(subprocess.check_output(["git", "-C", str(repo), "rev-parse", "HEAD"], text=True).strip())
            self.assertEqual(worker.first_parent_queue(repo, shas[0], shas[2]), shas[1:])

    def test_manifest_rejects_changed_and_extra_files(self):
        with tempfile.TemporaryDirectory() as temp:
            payload = Path(temp)
            (payload / "bin").mkdir()
            (payload / "web/dist").mkdir(parents=True)
            (payload / "bin/aicrm").write_text("app")
            (payload / "web/dist/index.html").write_text("page")
            entries = []
            for item in (payload / "bin/aicrm", payload / "web/dist/index.html"):
                entries.append(f"{installer.digest(item)}  {item.relative_to(payload).as_posix()}\n")
            manifest = payload / "release-files.sha256"
            manifest.write_text("".join(entries))
            meta = {"release_files_sha256": installer.digest(manifest)}
            installer.verify_payload(payload, meta)
            (payload / "web/dist/index.html").write_text("tampered")
            with self.assertRaisesRegex(ValueError, "digest mismatch"):
                installer.verify_payload(payload, meta)
            (payload / "web/dist/index.html").write_text("page")
            (payload / "extra").write_text("unlisted")
            with self.assertRaisesRegex(ValueError, "payload digest mismatch"):
                installer.verify_payload(payload, meta)

    def test_newest_failed_check_blocks_old_success(self):
        sha = "a" * 40
        runs = {"check_runs": [
            {"name": "check", "head_sha": sha, "app": {"slug": "github-actions"}, "id": 1, "started_at": "2026-01-01T00:00:00Z", "status": "completed", "conclusion": "success"},
            {"name": "check", "head_sha": sha, "app": {"slug": "github-actions"}, "id": 2, "started_at": "2026-01-01T01:00:00Z", "status": "completed", "conclusion": "failure"},
        ]}
        response = mock.MagicMock()
        response.__enter__.return_value = response
        with mock.patch.object(worker.urllib.request, "urlopen", return_value=response), mock.patch.object(worker.json, "load", return_value=runs):
            self.assertFalse(worker.exact_check_success(sha))

    def test_prod_install_failure_stops_as_outcome_unknown(self):
        # Fault injected after the production install was sent. The queue
        # cannot assume the failed reply means the remote install failed.
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            sha0, sha1 = "a" * 40, "b" * 40
            state = root / "state.json"
            state.write_text(json.dumps({"status": "ready", "processed_sha": sha0, "deployed_source_sha": sha0, "prod_installed_sha": sha0}))
            (root / "builds" / sha0 / "release").mkdir(parents=True)
            cfg = {"repo": str(root), "work_root": str(root), "state": str(state), "production_enabled": True, "prod_key": "key", "prod_known_hosts": "hosts", "prod_user": "ubuntu", "prod_host": "127.0.0.1", "prod_helper": "/fixed/helper"}
            with mock.patch.object(worker, "git", return_value=sha1), mock.patch.object(worker, "require_official_origin"), mock.patch.object(worker, "first_parent_queue", return_value=[sha1]), mock.patch.object(worker, "exact_check_success", return_value=True), mock.patch.object(worker, "command", side_effect=[json.dumps({"runtime_changed": True}), RuntimeError("ssh interrupted")]), mock.patch.object(worker, "build_candidate", return_value=(root, {"source_sha": sha1, "release_files_sha256": "f" * 64})), mock.patch.object(worker, "stage_install", return_value={}), mock.patch.object(worker, "copy_payload", return_value=("/incoming", "/meta")):
                with self.assertRaises(RuntimeError):
                    worker.poll(cfg)
            self.assertEqual(json.loads(state.read_text())["status"], "outcome_unknown")

    def test_staging_failure_stops_before_production(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            sha0, sha1 = "a" * 40, "b" * 40
            state = root / "state.json"
            state.write_text(json.dumps({"status": "ready", "processed_sha": sha0, "deployed_source_sha": sha0, "prod_installed_sha": sha0}))
            (root / "builds" / sha0 / "release").mkdir(parents=True)
            cfg = {"repo": str(root), "work_root": str(root), "state": str(state), "production_enabled": True}
            with mock.patch.object(worker, "git", return_value=sha1), mock.patch.object(worker, "require_official_origin"), mock.patch.object(worker, "first_parent_queue", return_value=[sha1]), mock.patch.object(worker, "exact_check_success", return_value=True), mock.patch.object(worker, "command", return_value=json.dumps({"runtime_changed": True})), mock.patch.object(worker, "build_candidate", side_effect=RuntimeError("stage build failed")), mock.patch.object(worker, "copy_payload") as prod_copy:
                with self.assertRaisesRegex(RuntimeError, "stage build failed"):
                    worker.poll(cfg)
                prod_copy.assert_not_called()
            self.assertEqual(json.loads(state.read_text())["status"], "staging_failed")

    def test_two_checked_commits_install_in_order(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            sha0, sha1, sha2 = "a" * 40, "b" * 40, "c" * 40
            state = root / "state.json"
            state.write_text(json.dumps({"status": "ready", "processed_sha": sha0, "deployed_source_sha": sha0, "prod_installed_sha": sha0}))
            for sha in (sha0, sha1):
                (root / "builds" / sha / "release").mkdir(parents=True)
            cfg = {"repo": str(root), "work_root": str(root), "state": str(state), "production_enabled": True, "prod_key": "key", "prod_known_hosts": "hosts", "prod_user": "ubuntu", "prod_host": "127.0.0.1", "prod_helper": "/fixed/helper"}
            metadatas = [{"source_sha": sha, "release_files_sha256": "f" * 64} for sha in (sha1, sha2)]
            command_results = [json.dumps({"runtime_changed": True}), json.dumps({**metadatas[0], "technical_status": "installed_healthy", "manifest_sha256": "f" * 64}), json.dumps({"runtime_changed": True}), json.dumps({**metadatas[1], "technical_status": "installed_healthy", "manifest_sha256": "f" * 64})]
            readbacks = [{"release_env": f"AICRM_RELEASE_SHA={sha}\n", "readyz": {"release_sha": sha, "status": "ready"}} for sha in (sha1, sha2)]
            def fake_git(_repo, *args):
                if args[0] == "rev-parse":
                    return sha2
                if args[0] == "show":
                    return "1760000000"
                return ""
            with mock.patch.object(worker, "git", side_effect=fake_git), mock.patch.object(worker, "require_official_origin"), mock.patch.object(worker, "first_parent_queue", return_value=[sha1, sha2]), mock.patch.object(worker, "exact_check_success", return_value=True), mock.patch.object(worker, "command", side_effect=command_results), mock.patch.object(worker, "build_candidate", side_effect=[(root, metadatas[0]), (root, metadatas[1])]), mock.patch.object(worker, "stage_install", return_value={}) as stage, mock.patch.object(worker, "copy_payload", side_effect=[("/incoming/one", "/meta/one"), ("/incoming/two", "/meta/two")]) as transfer, mock.patch.object(worker, "prod_readback", side_effect=readbacks):
                worker.poll(cfg)
            self.assertEqual([call.args[3] for call in stage.call_args_list], [sha0, sha1])
            self.assertEqual([call.args[4] for call in transfer.call_args_list], [sha0, sha1])
            self.assertEqual(json.loads(state.read_text())["processed_sha"], sha2)

    def test_health_failure_switches_back(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            releases = root / "releases"
            releases.mkdir()
            old = releases / ("a" * 40)
            old.mkdir()
            (old / "release.env").write_text(f"AICRM_RELEASE_SHA={'a' * 40}\n")
            current = root / "current"
            current.symlink_to(old)
            incoming = root / "incoming"
            (incoming / "bin").mkdir(parents=True)
            (incoming / "web/dist").mkdir(parents=True)
            (incoming / "bin/aicrm").write_text("app")
            (incoming / "web/dist/index.html").write_text("page")
            lines = [f"{installer.digest(path)}  {path.relative_to(incoming).as_posix()}\n" for path in sorted(incoming.rglob("*")) if path.is_file()]
            manifest = incoming / "release-files.sha256"
            manifest.write_text("".join(lines))
            metadata = {"source_sha": "b" * 40, "source_tree": "c" * 40, "release_files_sha256": installer.digest(manifest), "migrations_changed": False}
            env = root / "runtime.env"
            env.write_text("AICRM_DATABASE_URL=synthetic\n")
            with mock.patch.object(installer, "ROOT", root), mock.patch.object(installer, "RELEASES", releases), mock.patch.object(installer, "CURRENT", current), mock.patch.object(installer, "LOCK", root / "install.lock"), mock.patch.object(installer, "ENV", env), mock.patch.object(installer.shutil, "which", return_value="/bin/systemctl"), mock.patch.object(installer, "run") as system_run, mock.patch.object(installer, "backup_database") as backup, mock.patch.object(installer, "restart_services"), mock.patch.object(installer, "readiness", side_effect=[RuntimeError("health failed"), None]):
                with self.assertRaisesRegex(RuntimeError, "health failed"):
                    installer.install(incoming, metadata, "a" * 40)
                backup.assert_not_called()
                self.assertFalse(any("aicrm-migrate.service" in call.args for call in system_run.call_args_list))
            self.assertEqual(current.resolve(), old.resolve())


if __name__ == "__main__":
    unittest.main()
