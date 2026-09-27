"""Exercise writable-copy isolation, invalidation, cleanup and resource locking."""
import json
import multiprocessing
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import time
import unittest
from unittest.mock import patch

import check_preparation as prep


def slot_worker(directory, queue):
    os.environ["AICRM_TEST_PREP_DIR"] = directory
    with prep.heavy_slot(["go", "build"]):
        queue.put(("enter", time.monotonic()))
        time.sleep(0.1)
        queue.put(("leave", time.monotonic()))


def cached_copy_worker(directory, repository, queue):
    os.environ["AICRM_TEST_PREP_DIR"] = directory
    os.environ.pop("AICRM_HEAVY_SLOT_HELD", None)
    with prep.heavy_slot(['bash','scripts/run-go-with-donor-views.sh','go','test','-race','-count=1']) as env:
        with patch.object(prep,'artifact_input',return_value='a'*64):
            prep.materialize_artifact(Path(repository))
        queue.put(env)


class CheckPreparationTest(unittest.TestCase):
    def fixture(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        root = Path(temp.name)
        cache = root / "cache"; cache.mkdir(mode=0o700)
        repo = root / "repo"; repo.mkdir()
        self.enterContext(patch.dict(os.environ, {"AICRM_TEST_PREP_DIR": str(cache), "AICRM_TEST_PREP_CACHE": ""}))
        return cache, repo

    def shared_fixture(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        base = Path(temp.name)
        snapshots = base / "shared-preparation"
        snapshots.mkdir(mode=0o700)
        self.enterContext(patch.dict(os.environ, {"AICRM_TEST_PREP_CACHE": str(snapshots)}))
        return base, snapshots

    def test_artifact_copies_are_writable_isolated_and_input_changes_rebuild(self):
        cache, root = self.fixture()
        fingerprints = ["a" * 64, "a" * 64, "b" * 64]
        def build(command, cwd):
            if command[:3] == ["npm", "run", "build"]:
                target = Path(cwd) / "web/dist"; target.mkdir(parents=True, exist_ok=True)
                (target / "asset-manifest.json").write_text('{"original":true}')
                (target / "page.js").write_text("original")
            elif command[1] == "scripts/stage-pr01-effects-ui.mjs":
                target = Path(command[-1]); target.mkdir(parents=True)
                (target / "asset-manifest.json").write_text('{"original":true}')
                (target / "page.js").write_text("original")
        with patch.object(prep, "artifact_input", side_effect=fingerprints), patch.object(prep, "run", side_effect=build) as run:
            prep.materialize_artifact(root)
            (root / "web/dist/page.js").write_text("fixture changed")
            prep.materialize_artifact(root)
            self.assertEqual((root / "web/dist/page.js").read_text(), "original")
            self.assertEqual(run.call_count, 5)
            prep.materialize_artifact(root)
            self.assertEqual(run.call_count, 10)
        (cache / ("artifact-" + "b" * 64) / "dist/page.js").write_text("corrupt")
        with patch.object(prep, "artifact_input", return_value="b" * 64), patch.object(prep, "run", side_effect=build) as run:
            prep.materialize_artifact(root)
            self.assertEqual((root / "web/dist/page.js").read_text(), "original")
            self.assertEqual(run.call_count, 5)

    def test_dependency_inputs_install_once_and_lock_change_invalidates(self):
        cache, root = self.fixture()
        (root / "package.json").write_text("{}")
        (root / "package-lock.json").write_text("first")
        def install(command, cwd):
            modules = cwd / "node_modules"; modules.mkdir(exist_ok=True)
            (modules / ".package-lock.json").write_text("installed")
        with patch.object(prep, "tool_input", return_value=["node", "npm"]), patch.object(prep, "run", side_effect=install) as run:
            prep.npm_dependencies(root, ".")
            prep.npm_dependencies(root, ".")
            self.assertEqual(run.call_count, 1)
            self.assertIn("--prefer-offline", run.call_args.args[0])
            self.assertNotIn("--offline", run.call_args.args[0])
            (root / "package-lock.json").write_text("second")
            prep.npm_dependencies(root, ".")
            self.assertEqual(run.call_count, 2)

    def test_shared_npm_snapshot_crosses_attempts_as_independent_writable_copies(self):
        base, snapshots = self.shared_fixture()
        roots = [base / "checkout-one", base / "checkout-two"]
        attempts = [base / "attempt-one", base / "attempt-two"]
        for root, attempt in zip(roots, attempts):
            root.mkdir(); attempt.mkdir(mode=0o700)
            (root / "package.json").write_text("{}")
            (root / "package-lock.json").write_text("same lock")
        def install(command, cwd):
            modules = cwd / "node_modules"; modules.mkdir(exist_ok=True)
            (modules / ".package-lock.json").write_text("installed")
            (modules / "dependency.js").write_text("from snapshot")
        with patch.object(prep, "tool_input", return_value=["node", "npm"]), \
                patch.object(prep, "run", side_effect=install) as run:
            with patch.dict(os.environ, {"AICRM_TEST_PREP_DIR": str(attempts[0])}):
                prep.npm_dependencies(roots[0], ".")
            (roots[0] / "node_modules/dependency.js").write_text("attempt one mutation")
            with patch.dict(os.environ, {"AICRM_TEST_PREP_DIR": str(attempts[1])}):
                prep.npm_dependencies(roots[1], ".")
            self.assertEqual((roots[1] / "node_modules/dependency.js").read_text(), "from snapshot")
            (roots[1] / "node_modules/dependency.js").write_text("attempt two mutation")
            with patch.dict(os.environ, {"AICRM_TEST_PREP_DIR": str(attempts[1])}):
                prep.npm_dependencies(roots[1], ".")
            self.assertEqual((roots[1] / "node_modules/dependency.js").read_text(), "from snapshot")
            self.assertEqual(run.call_count, 1)
            snapshot_modules = next(snapshots.glob("npm-*/modules"))
            (snapshot_modules / "dependency.js").write_text("corrupt shared snapshot")
            third_root = base / "checkout-three"; third_root.mkdir()
            third_attempt = base / "attempt-three"; third_attempt.mkdir(mode=0o700)
            (third_root / "package.json").write_text("{}")
            (third_root / "package-lock.json").write_text("same lock")
            with patch.dict(os.environ, {"AICRM_TEST_PREP_DIR": str(third_attempt)}):
                prep.npm_dependencies(third_root, ".")
            self.assertEqual((third_root / "node_modules/dependency.js").read_text(), "from snapshot")
            self.assertEqual(run.call_count, 2)

    def test_shared_artifact_snapshot_reused_across_attempts_with_small_receipt(self):
        base, snapshots = self.shared_fixture()
        roots = [base / "checkout-one", base / "checkout-two"]
        attempts = [base / "attempt-one", base / "attempt-two"]
        for root, attempt in zip(roots, attempts):
            root.mkdir(); attempt.mkdir(mode=0o700)
        fingerprint = "c" * 64
        def build(command, cwd):
            if command[:3] == ["npm", "run", "build"]:
                target = Path(cwd) / "web/dist"; target.mkdir(parents=True, exist_ok=True)
                (target / "asset-manifest.json").write_text('{"source":"same"}')
                (target / "base.js").write_text("verified base")
            elif command[1] == "scripts/stage-pr01-effects-ui.mjs":
                target = Path(command[-1]); target.mkdir(parents=True)
                (target / "asset-manifest.json").write_text('{"source":"same"}')
                (target / "release.js").write_text("verified release")
        with patch.object(prep, "artifact_input", return_value=fingerprint), \
                patch.object(prep, "run", side_effect=build) as run:
            with patch.dict(os.environ, {"AICRM_TEST_PREP_DIR": str(attempts[0])}):
                prep.materialize_artifact(roots[0])
            run_count = run.call_count
            cache = snapshots / ("artifact-" + fingerprint)
            self.assertTrue(cache.is_dir())
            receipt_dir = attempts[0] / ("artifact-" + fingerprint)
            self.assertTrue((receipt_dir / "receipt.json").is_file())
            self.assertEqual({item.name for item in receipt_dir.iterdir()}, {"receipt.json"})
            with patch.dict(os.environ, {"AICRM_TEST_PREP_DIR": str(attempts[1])}):
                prep.materialize_artifact(roots[1])
            self.assertEqual(run.call_count, run_count)
            self.assertEqual((roots[1] / "web/dist/release.js").read_text(), "verified release")
            (roots[1] / "web/dist/release.js").write_text("attempt mutation")
            self.assertEqual((cache / "dist/release.js").read_text(), "verified release")
            (cache / "dist/release.js").write_text("corrupt shared snapshot")
            third_root = base / "checkout-three"; third_root.mkdir()
            third_attempt = base / "attempt-three"; third_attempt.mkdir(mode=0o700)
            with patch.dict(os.environ, {"AICRM_TEST_PREP_DIR": str(third_attempt)}), \
                    patch.object(prep, "run", side_effect=build) as rebuild:
                prep.materialize_artifact(third_root)
            self.assertEqual(rebuild.call_count, 5)
            self.assertEqual((third_root / "web/dist/release.js").read_text(), "verified release")
            self.assertIn("artifact_corruption", (third_attempt / "preparation.jsonl").read_text())

    def test_artifact_input_binds_head_tree_tracked_bytes_and_source_sha(self):
        cache, root = self.fixture()
        del cache
        subprocess.run(["git", "init", "-q"], cwd=root, check=True)
        subprocess.run(["git", "config", "user.name", "Test"], cwd=root, check=True)
        subprocess.run(["git", "config", "user.email", "test@example.invalid"], cwd=root, check=True)
        tracked = root / "input.txt"; tracked.write_text("one")
        subprocess.run(["git", "add", "input.txt"], cwd=root, check=True)
        subprocess.run(["git", "commit", "-qm", "initial"], cwd=root, check=True)
        with patch.object(prep, "tool_input", return_value=["node 24", "npm 11"]):
            first = prep.artifact_input(root)
            subprocess.run(["git", "commit", "--allow-empty", "-qm", "new head"], cwd=root, check=True)
            second = prep.artifact_input(root)
            self.assertNotEqual(first, second)
            with patch.dict(os.environ, {"AICRM_SOURCE_SHA": "override-source"}):
                third = prep.artifact_input(root)
                self.assertNotEqual(second, third)
                tracked.write_text("two")
                working = prep.artifact_input(root)
                self.assertNotEqual(third, working)
                subprocess.run(["git", "add", "input.txt"], cwd=root, check=True)
                subprocess.run(["git", "commit", "-qm", "new tree"], cwd=root, check=True)
                fourth = prep.artifact_input(root)
                self.assertNotEqual(working, fourth)

    def test_snapshot_cache_rejects_public_modes_symlink_and_unknown_entries(self):
        prep_root, _ = self.fixture()
        snapshots = prep_root.parent / "snapshot-cache"
        snapshots.mkdir(mode=0o700)
        with patch.dict(os.environ, {"AICRM_TEST_PREP_CACHE": str(snapshots)}):
            self.assertEqual(prep.snapshot_root(prep_root), snapshots)
            os.chmod(snapshots, 0o755)
            with self.assertRaisesRegex(ValueError, "exclusively"):
                prep.snapshot_root(prep_root)
            os.chmod(snapshots, 0o700)
            (snapshots / "unexpected").write_text("not a snapshot")
            os.chmod(snapshots / "unexpected", 0o600)
            with self.assertRaisesRegex(ValueError, "unknown entry"):
                prep.snapshot_root(prep_root)
            (snapshots / "unexpected").unlink()
            linked = snapshots.parent / "linked-cache"
            linked.symlink_to(snapshots, target_is_directory=True)
            with patch.dict(os.environ, {"AICRM_TEST_PREP_CACHE": str(linked)}):
                with self.assertRaisesRegex(ValueError, "directory"):
                    prep.snapshot_root(prep_root)
            unit_link = snapshots / ("npm-" + "d" * 64)
            unit_link.symlink_to(prep_root, target_is_directory=True)
            with self.assertRaisesRegex(ValueError, "linked"):
                prep.snapshot_root(prep_root)

    def _stage_cache_fixture(self, base, snapshots, fingerprint, shell_text=None):
        base.mkdir(parents=True, exist_ok=True)
        root = base / "stage-checkout"; root.mkdir()
        attempt = base / "stage-attempt"; attempt.mkdir(mode=0o700)
        scripts = root / "scripts"; scripts.mkdir()
        if shell_text is None:
            canonical = Path(prep.__file__).resolve().parents[2] / "scripts/run-donor-view-consumers.sh"
            shell_text = canonical.read_text()
        (scripts / "run-donor-view-consumers.sh").write_text(shell_text)
        cache = snapshots / ("artifact-" + fingerprint); cache.mkdir(mode=0o700)
        (cache / "web-dist").mkdir(); (cache / "dist").mkdir()
        (cache / "web-dist/asset-manifest.json").write_text('{"kind":"web"}')
        (cache / "web-dist/base.js").write_text("cached base")
        (cache / "dist/asset-manifest.json").write_text('{"kind":"release"}')
        (cache / "dist/release.js").write_text("cached release")
        prep.atomic_json(cache / "receipt.json", {
            "input": fingerprint,
            "output": prep.directory_digest(cache / "dist"),
            "web_dist_output": prep.directory_digest(cache / "web-dist"),
        })
        return root, attempt, cache

    def test_stage_hit_restores_both_dist_trees_and_reruns_all_four_stage_tests(self):
        base, snapshots = self.shared_fixture()
        fingerprint = "e" * 64
        root, attempt, cache = self._stage_cache_fixture(base, snapshots, fingerprint)
        with patch.dict(os.environ, {"AICRM_TEST_PREP_DIR": str(attempt)}), \
                patch.object(prep, "artifact_input", return_value=fingerprint), \
                patch.object(prep, "npm_dependencies") as npm, \
                patch.object(prep, "run") as run, \
                patch.object(prep, "publish_artifact") as publish:
            prep.frontend_checks(root, "stage")
        npm.assert_any_call(root, ".")
        npm.assert_any_call(root, "web/v3")
        commands = [call.args[0] for call in run.call_args_list]
        tests = [command for command in commands if len(command) >= 2 and command[1].startswith("scripts/test-")]
        self.assertEqual(tests, [
            ["node", "scripts/test-stage-pr01-effects-ui.mjs"],
            ["node", "scripts/test-groupops-history-release.mjs", "web/dist", "release/web/dist"],
            ["node", "scripts/test-stage-survey-ui.mjs", "web/dist", "release/web/dist"],
            ["node", "scripts/test-stage-new-shell-ui.mjs", "web/dist", "release/web/dist"],
        ])
        self.assertNotIn(["npm", "run", "build"], commands)
        self.assertEqual((root / "web/dist/base.js").read_text(), "cached base")
        self.assertEqual((root / "release/web/dist/release.js").read_text(), "cached release")
        (root / "web/dist/base.js").write_text("local mutation")
        self.assertEqual((cache / "web-dist/base.js").read_text(), "cached base")
        publish.assert_called_once()

    def test_check_mode_keeps_full_canonical_contract_even_with_valid_stage_cache(self):
        base, snapshots = self.shared_fixture()
        fingerprint = "f" * 64
        root, attempt, _ = self._stage_cache_fixture(base, snapshots, fingerprint)
        with patch.dict(os.environ, {"AICRM_TEST_PREP_DIR": str(attempt)}), \
                patch.object(prep, "npm_dependencies"), \
                patch.object(prep, "run") as run, \
                patch.object(prep.subprocess, "run") as shell, \
                patch.object(prep, "publish_artifact"):
            prep.frontend_checks(root, "check")
        run.assert_called_once_with(["node", "scripts/prepare-donor-source-views.mjs"], root)
        shell.assert_called_once()
        script = shell.call_args.args[0][2]
        for contract in ("run_frontend_and_stage_checks", "npm run typecheck", "npx tsc",
                         "npm test", "npm run build", "test-groupops-history-release.mjs",
                         "test-stage-new-shell-ui.mjs"):
            self.assertIn(contract, script)

    def test_unrecognized_build_or_shell_contract_disables_stage_cache(self):
        base, snapshots = self.shared_fixture()
        canonical = (Path(prep.__file__).resolve().parents[2] / "scripts/run-donor-view-consumers.sh").read_text()
        variants = {
            "new build contract": canonical.replace(
                "build_frontend() {\n  node scripts/generate-ai-assistant-client.mjs\n  npm run build\n",
                "build_frontend() {\n  node scripts/generate-ai-assistant-client.mjs\n  npm run typecheck\n  npm run build\n", 1),
            "compound stage command": canonical.replace(
                "    node scripts/test-stage-pr01-effects-ui.mjs\n",
                "    node scripts/test-stage-pr01-effects-ui.mjs && node scripts/extra-contract.mjs\n", 1),
        }
        for index, (label, shell_text) in enumerate(variants.items()):
            with self.subTest(label=label):
                test_fingerprint = f"{index + 9:x}" * 64
                root, attempt, _ = self._stage_cache_fixture(base / str(index), snapshots,
                                                              test_fingerprint, shell_text)
                with patch.dict(os.environ, {"AICRM_TEST_PREP_DIR": str(attempt)}), \
                        patch.object(prep, "artifact_input", return_value=test_fingerprint), \
                        patch.object(prep, "npm_dependencies"), \
                        patch.object(prep, "run"), \
                        patch.object(prep.subprocess, "run") as shell, \
                        patch.object(prep, "publish_artifact"):
                    prep.frontend_checks(root, "stage")
                shell.assert_called_once()
                script = shell.call_args.args[0][2]
                self.assertIn("build_frontend", script)
                self.assertIn("stage_frontend", script)
                if label == "new build contract":
                    self.assertIn("npm run typecheck", script)
                else:
                    self.assertIn("&& node scripts/extra-contract.mjs", script)

    def test_stage_test_failure_propagates_without_publishing_test_success(self):
        base, snapshots = self.shared_fixture()
        fingerprint = "b" * 64
        root, attempt, _ = self._stage_cache_fixture(base, snapshots, fingerprint)
        def fail_stage_test(command, cwd):
            if len(command) >= 2 and command[1] == "scripts/test-stage-pr01-effects-ui.mjs":
                raise subprocess.CalledProcessError(1, command)
        with patch.dict(os.environ, {"AICRM_TEST_PREP_DIR": str(attempt)}), \
                patch.object(prep, "artifact_input", return_value=fingerprint), \
                patch.object(prep, "npm_dependencies"), \
                patch.object(prep, "run", side_effect=fail_stage_test), \
                patch.object(prep, "publish_artifact") as publish:
            with self.assertRaises(subprocess.CalledProcessError):
                prep.frontend_checks(root, "stage")
        publish.assert_not_called()
        self.assertTrue((attempt / ("artifact-" + fingerprint) / "receipt.json").is_file())

    def test_corrupt_dependency_copy_restores_and_corrupt_snapshot_reinstalls(self):
        cache,root=self.fixture()
        (root/"package.json").write_text("{}")
        (root/"package-lock.json").write_text("lock")
        def install(command,cwd):
            modules=cwd/"node_modules"; modules.mkdir(exist_ok=True)
            (modules/".package-lock.json").write_text("installed")
            (modules/"library.js").write_text("verified")
        with patch.object(prep,"tool_input",return_value=["node","npm"]),patch.object(prep,"run",side_effect=install) as run:
            prep.npm_dependencies(root,".")
            (root/"node_modules/library.js").write_text("corrupt writable copy")
            prep.npm_dependencies(root,".")
            self.assertEqual((root/"node_modules/library.js").read_text(),"verified")
            self.assertEqual(run.call_count,1)
            snapshot=next(cache.glob("npm-*/modules"))
            (snapshot/"library.js").write_text("corrupt snapshot")
            (root/"node_modules/library.js").write_text("corrupt copy again")
            prep.npm_dependencies(root,".")
            self.assertEqual(run.call_count,2)
            self.assertEqual((root/"node_modules/library.js").read_text(),"verified")
            self.assertIn("npm_corruption",(cache/"preparation.jsonl").read_text())

    def test_cleanup_rejects_non_synthetic_server_and_unowned_database(self):
        cache, _ = self.fixture()
        (cache / "database-bad.json").write_text(json.dumps({"database": "production"}))
        for raw in ("postgres://role@10.0.4.1/aicrm_test_x", "postgres://role@localhost/production", "postgres://role@localhost/aicrm_test_x"):
            with self.subTest(raw=raw), patch.object(prep.subprocess, "run") as run:
                with self.assertRaises(ValueError):
                    prep.cleanup_databases(cache, raw)
                run.assert_not_called()

    def test_heavy_commands_serialize_across_processes(self):
        cache, _ = self.fixture()
        context = multiprocessing.get_context("fork")
        queue = context.Queue()
        children = [context.Process(target=slot_worker, args=(str(cache), queue)) for _ in range(2)]
        for child in children: child.start()
        events = [queue.get(timeout=5) for _ in range(4)]
        for child in children:
            child.join(5)
            self.assertEqual(child.exitcode, 0)
        self.assertEqual([event[0] for event in events], ["enter", "leave", "enter", "leave"])

    def test_cached_test_and_artifact_copy_do_not_wait_for_the_heavy_slot(self):
        cache,root=self.fixture(); snapshot=cache/('artifact-'+'a'*64)
        snapshot.mkdir(mode=0o700)
        (snapshot/'dist').mkdir(parents=True); (snapshot/'dist/page.js').write_text('verified')
        (snapshot/'web-dist').mkdir(); (snapshot/'web-dist/page.js').write_text('base')
        prep.atomic_json(snapshot/'receipt.json',{'input':'a'*64,'output':prep.directory_digest(snapshot/'dist'),
                                                  'web_dist_output':prep.directory_digest(snapshot/'web-dist')})
        context=multiprocessing.get_context('fork'); queue=context.Queue()
        child=context.Process(target=cached_copy_worker,args=(str(cache),str(root),queue))
        self.addCleanup(lambda: child.kill() if child.is_alive() else None)
        with prep.heavy_slot(['npm','build']):
            # A second process must finish while the first retains the slot;
            # the bounded queue also catches regressions without hanging CI.
            child.start(); env=queue.get(timeout=5)
            self.assertIn('-toolexec=',env['GOFLAGS'])
            self.assertNotIn('AICRM_HEAVY_SLOT_HELD',env)
            child.join(5); self.assertEqual(child.exitcode,0)
        self.assertEqual((root/'web/dist/page.js').read_text(),'verified')

    def test_native_tool_hook_serializes_compilers_but_not_identity_or_vet(self):
        cache,root=self.fixture(); tool=root/'compile'
        tool.write_text('#!'+sys.executable+'\nimport os,time,sys\n'
                        'if "-V=full" in sys.argv: print("compile version fixture"); sys.exit(0)\n'
                        'print("enter",os.getpid(),time.monotonic(),flush=True)\n'
                        'time.sleep(0.1)\nprint("leave",os.getpid(),time.monotonic(),flush=True)\n')
        tool.chmod(0o700); argv=[sys.executable,prep.__file__,'go-tool',str(tool)]
        children=[subprocess.Popen(argv,stdout=subprocess.PIPE,text=True) for _ in range(2)]
        events=[]
        for child in children:
            output,_=child.communicate(timeout=5);self.assertEqual(child.returncode,0)
            events.extend((float(row.split()[2]),row.split()[0]) for row in output.splitlines())
        self.assertEqual([kind for _,kind in sorted(events)],['enter','leave','enter','leave'])
        with prep.heavy_slot(['npm','build']):
            version=subprocess.run(argv+['-V=full'],capture_output=True,text=True,timeout=5)
            self.assertEqual(version.returncode,0);self.assertIn('compile version fixture',version.stdout)
            vet=root/'vet';shutil.copyfile(tool,vet);vet.chmod(0o700)
            self.assertEqual(subprocess.run(argv[:-1]+[str(vet)],capture_output=True,timeout=5).returncode,0)
        with prep.heavy_slot(['npm','build']) as inherited:
            self.assertEqual(subprocess.run(argv,env=inherited,capture_output=True,timeout=5).returncode,0)

    def test_actual_go_hook_preserves_race_fresh_tests_vet_and_failure(self):
        cache,root=self.fixture(); marker=cache/'test-executions'
        (root/'go.mod').write_text('module fixture/go-slot\n\ngo 1.24\n')
        source='package slot\nimport ("os";"testing")\n'+\
               'func TestFresh(t *testing.T) { f,e:=os.OpenFile(os.Getenv("SLOT_MARKER"),os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if e!=nil {t.Fatal(e)};defer f.Close();f.WriteString("executed\\n") }\n'+\
               'func TestAssertion(t *testing.T) {}\n'
        testfile=root/'slot_test.go';testfile.write_text(source)
        with prep.heavy_slot(['go','vet']) as env:
            subprocess.run(['go','vet','-p','1','./...'],cwd=root,env=env,capture_output=True,check=True,timeout=120)
        for number in range(2):
            with prep.heavy_slot(['go','test']) as env:
                env['SLOT_MARKER']=str(marker)
                result=subprocess.run(['go','test','-json','-race','-p','1','-count=1','./...'],cwd=root,env=env,
                                      capture_output=True,text=True,timeout=120)
            self.assertEqual(result.returncode,0,result.stderr)
            events=[json.loads(line) for line in result.stdout.splitlines()]
            self.assertEqual(sum(e.get('Action')=='pass' and bool(e.get('Test')) for e in events),2)
            self.assertEqual(marker.read_text().splitlines(),['executed']*(number+1))
        testfile.write_text(source.replace('func TestAssertion(t *testing.T) {}','func TestAssertion(t *testing.T) {t.Fatal("expected assertion failure")}'))
        with prep.heavy_slot(['go','test']) as env:
            env['SLOT_MARKER']=str(marker)
            result=subprocess.run(['go','test','-json','-race','-p','1','-count=1','./...'],cwd=root,env=env,
                                  capture_output=True,text=True,timeout=120)
        self.assertNotEqual(result.returncode,0)
        self.assertIn('"Action":"fail"',result.stdout)
