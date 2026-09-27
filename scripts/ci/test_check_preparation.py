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
        self.enterContext(patch.dict(os.environ, {"AICRM_TEST_PREP_DIR": str(cache)}))
        return cache, repo

    def test_artifact_copies_are_writable_isolated_and_input_changes_rebuild(self):
        cache, root = self.fixture()
        fingerprints = ["a" * 64, "a" * 64, "b" * 64]
        def build(command, cwd):
            if "stage-pr01-effects-ui.mjs" in command[1]:
                target = Path(command[-1]); target.mkdir()
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
        with patch.object(prep, "artifact_input", return_value="b" * 64):
            with self.assertRaisesRegex(ValueError, "artifact bytes"):
                prep.materialize_artifact(root)

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
            (root / "package-lock.json").write_text("second")
            prep.npm_dependencies(root, ".")
            self.assertEqual(run.call_count, 2)

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
        (snapshot/'dist').mkdir(parents=True); (snapshot/'dist/page.js').write_text('verified')
        prep.atomic_json(snapshot/'receipt.json',{'input':'a'*64,'output':prep.directory_digest(snapshot/'dist')})
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
