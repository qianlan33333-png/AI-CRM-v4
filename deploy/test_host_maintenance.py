import contextlib
import copy
import fcntl
import importlib.util
import io
import json
import os
from pathlib import Path
import stat
import subprocess
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch


def load(name,file):
    spec=importlib.util.spec_from_file_location(name,Path(__file__).with_name(file))
    module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module)
    return module


maintenance=load("maintenance","runtime-maintenance.py")
post=load("post_retention","post-release-retention.py")


class HostMaintenanceTests(unittest.TestCase):
    def test_helper_executes_only_fixed_commands_and_publishes_safe_partial_counts(self):
        saved=[];calls=[]
        def command(argv,**kwargs):
            calls.append(argv)
            if argv==maintenance.FILE_COMMAND:
                return SimpleNamespace(returncode=0,stdout=json.dumps({"candidates":7,"deleted":7,"bytes":21,"protected":2,"remaining":True,"uninitialized_roots":0}))
            return SimpleNamespace(returncode=1,stdout=b"",stderr=b"RAW_PROVIDER_SECRET")
        with patch.object(maintenance,"write_result",side_effect=lambda path,value:saved.append(copy.deepcopy(value))),patch.object(maintenance.subprocess,"run",side_effect=command),contextlib.redirect_stdout(io.StringIO()) as out:
            status=maintenance.run()
        self.assertEqual(status,1)
        self.assertEqual(calls,[maintenance.FILE_COMMAND,maintenance.JOURNAL_COMMAND])
        self.assertEqual([v["state"] for v in saved],["running","partial_failed"])
        self.assertEqual(saved[-1]["runtime"]["deleted"],7)
        self.assertNotIn("RAW_PROVIDER_SECRET",out.getvalue())

    def test_schema_credentials_are_only_env_and_connection_is_read_only(self):
        with tempfile.TemporaryDirectory() as temporary:
            config=Path(temporary)/"app.env"
            config.write_text('AICRM_DATABASE_URL="postgres://dbuser:synthetic-password@127.0.0.1:5432/aicrm?sslmode=disable"\nUNRELATED_SECRET=not_read\n')
            environment=post.database_environment(config)
        with patch.object(post.subprocess,"run",return_value=SimpleNamespace(returncode=0,stdout='{"migrations":[]}')) as command:
            post.snapshot("a"*40,environment)
        argv=command.call_args.args[0]
        self.assertNotIn("synthetic-password",repr(argv))
        self.assertEqual(environment["PGDATABASE"],"aicrm")
        self.assertIn("default_transaction_read_only=on",environment["PGOPTIONS"])
        self.assertNotIn("UNRELATED_SECRET",environment)
        self.assertIn("encode(checksum,'hex')",argv[-1])

    def test_inherited_lock_is_same_inode_and_retained_after_check(self):
        with tempfile.TemporaryDirectory() as temporary:
            root=Path(temporary)
            with (root/"install-release.lock").open("w+") as holder:
                fcntl.flock(holder,fcntl.LOCK_EX|fcntl.LOCK_NB)
                post.require_lock(root,holder.fileno())
                with (root/"install-release.lock").open("r+") as competitor:
                    with self.assertRaises(BlockingIOError):fcntl.flock(competitor,fcntl.LOCK_EX|fcntl.LOCK_NB)
                with (root/"other").open("w+") as wrong:
                    with self.assertRaises(ValueError):post.require_lock(root,wrong.fileno())

    def test_post_release_missing_rollbacks_is_explicit_gap_and_never_applies(self):
        saved=[]
        owner=SimpleNamespace(inventory=lambda *args:{"blockers":["missing"],"rollback_releases":[]},apply_plan=lambda *args:self.fail("must not clean blocked inventory"))
        writer=SimpleNamespace(write_result=lambda path,value:saved.append(copy.deepcopy(value)), sample_space=maintenance.sample_space, space_observations=maintenance.space_observations)
        with tempfile.TemporaryDirectory() as temporary:
            root=Path(temporary).resolve();sha="a"*40
            (root/"releases"/sha).mkdir(parents=True);(root/"current").symlink_to(root/"releases"/sha)
            work=root/"work";work.mkdir(mode=0o700)
            real=Path.lstat
            def file_stat(path):
                value=real(path)
                if path==work:return SimpleNamespace(st_mode=value.st_mode,st_uid=0)
                return value
            with patch.object(post,"ROOT",root),patch.object(post,"WORK",work),patch.object(post.os,"geteuid",return_value=0),patch.object(post,"require_lock"),patch.object(post,"database_environment",return_value={}),patch.object(post,"snapshot",return_value={}),patch.object(post,"load_helper",side_effect=[writer,owner]),patch.object(Path,"lstat",file_stat),contextlib.redirect_stdout(io.StringIO()):
                status=post.run(sha)
        self.assertEqual(status,1)
        self.assertEqual(saved[-1]["reason"],"two_verified_schema_compatible_rollbacks_missing")
        self.assertEqual(saved[-1]["deleted_count"],0)
        self.assertEqual(saved[-1]["space_observations"][0]["state"],"sampled")

    def test_space_sampling_is_net_available_change_not_logical_deletion(self):
        with tempfile.TemporaryDirectory() as temporary:
            resources={"process_storage":Path(temporary),"journal_runtime_storage":Path(temporary)/"absent"}
            with patch.object(maintenance.os,"statvfs",return_value=SimpleNamespace(f_bavail=42,f_frsize=4096)):
                sampled=maintenance.sample_space(resources)
        self.assertEqual(sampled["process_storage"][1],42*4096)
        self.assertIsNone(sampled["journal_runtime_storage"])
        observations=maintenance.space_observations({"release_storage":(1,1000),"process_storage":None,"journal_runtime_storage":(2,100)}, {"release_storage":(1,600),"process_storage":(1,900),"journal_runtime_storage":(3,200)})
        self.assertEqual(observations[0]["available_bytes_net_change"],-400)
        self.assertEqual(observations[0]["state"],"sampled")
        self.assertEqual(observations[1]["state"],"unavailable")
        self.assertIsNone(observations[1]["available_bytes_before"])
        self.assertIsNone(observations[2]["available_bytes_net_change"])

    def test_polkit_grants_only_exact_unit_start_and_has_no_timer(self):
        path=Path(__file__).with_name("60-aicrm-runtime-retention.rules")
        script='''const fs=require("fs"),vm=require("vm");let rule;vm.runInNewContext(fs.readFileSync(process.argv[1],"utf8"),{polkit:{Result:{YES:"yes"},addRule:f=>rule=f}});for(const [id,user,unit,verb,yes] of [["org.freedesktop.systemd1.manage-units","aicrm","aicrm-runtime-retention.service","start",true],["org.freedesktop.systemd1.manage-units","aicrm","sshd.service","start",false],["org.freedesktop.systemd1.manage-units","aicrm","aicrm-runtime-retention.service","restart",false],["org.freedesktop.systemd1.manage-units","other","aicrm-runtime-retention.service","start",false],["org.freedesktop.systemd1.manage-unit-files","aicrm","aicrm-runtime-retention.service","start",false]]){const got=rule({id,lookup:k=>({unit,verb})[k]},{user});if((got==="yes")!==yes)process.exit(1);}'''
        subprocess.run(["node","-e",script,str(path)],check=True,capture_output=True)
        unit=path.with_name("aicrm-runtime-retention.service").read_text()
        self.assertIn("NoNewPrivileges=true",unit)
        self.assertIn("Type=oneshot",unit)
        self.assertNotIn("[Install]",unit)
        self.assertFalse(path.with_name("aicrm-runtime-retention.timer").exists())


if __name__=="__main__":unittest.main()
