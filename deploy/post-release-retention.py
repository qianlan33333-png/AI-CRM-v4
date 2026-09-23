#!/usr/bin/env python3
"""Run verified release cleanup at the end of the existing locked installer.

FD 9 is inherited from install-release.sh. Never acquire a second independent
lock or clean anything after a failed release. A gap is explicit and does not
roll back a successfully started release.
"""
import argparse
import datetime as dt
import fcntl
import importlib.util
import json
import os
from pathlib import Path
import re
import shlex
import stat
import subprocess
import sys
import tempfile
from urllib.parse import parse_qs, unquote, urlsplit

sys.dont_write_bytecode = True

ROOT = Path("/opt/aicrm")
RESULT = Path("/var/lib/aicrm-maintenance/release-cleanup.json")
WORK = Path("/run/aicrm-release-retention")


def load_helper(name, filename):
    spec=importlib.util.spec_from_file_location(name,Path(__file__).with_name(filename))
    module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module)
    return module


def require_lock(root, descriptor=9):
    target = (root / "install-release.lock").lstat()
    opened = os.fstat(descriptor)
    if not stat.S_ISREG(target.st_mode) or (target.st_dev,target.st_ino)!=(opened.st_dev,opened.st_ino):
        raise ValueError("installer_lock_not_inherited")
    fcntl.flock(descriptor,fcntl.LOCK_EX|fcntl.LOCK_NB)


def database_environment(path):
    # Read only these two config keys. No shell sourcing, command arguments,
    # logging, or third-party environment libraries expose the DSN.
    values = {}
    for line in path.read_text().splitlines():
        key, separator, value = line.partition("=")
        if separator and key in {"AICRM_DATABASE_URL","DATABASE_URL"}:
            parts = shlex.split(value, comments=False)
            if len(parts)!=1:
                raise ValueError("database_configuration_unsupported")
            values[key]=parts[0]
    value = values.get("AICRM_DATABASE_URL") or values.get("DATABASE_URL")
    parsed=urlsplit(value or "")
    query=parse_qs(parsed.query,strict_parsing=True)
    if parsed.scheme not in {"postgres","postgresql"} or not parsed.hostname or not parsed.path.strip("/") or set(query)-{"sslmode","connect_timeout","application_name"}:
        raise ValueError("database_configuration_unsupported")
    return {"PATH":"/usr/bin:/bin", "PGHOST":parsed.hostname, "PGPORT":str(parsed.port or 5432),
            "PGUSER":unquote(parsed.username or ""), "PGPASSWORD":unquote(parsed.password or ""),
            "PGDATABASE":unquote(parsed.path[1:]), "PGSSLMODE":query.get("sslmode",["prefer"])[0],
            "PGCONNECT_TIMEOUT":"5", "PGAPPNAME":"aicrm-release-retention-readonly",
            "PGOPTIONS":"-c default_transaction_read_only=on -c statement_timeout=10000"}


def snapshot(sha, environment):
    sql = "SELECT json_build_object('current_sha','"+sha+"','captured_at',clock_timestamp(),'migrations',(SELECT json_agg(json_build_object('version',version,'name',name,'checksum',encode(checksum,'hex')) ORDER BY version) FROM public.platform_schema_migrations));"
    result=subprocess.run(["/usr/bin/psql","-X","-A","-t","--set=ON_ERROR_STOP=1","-c",sql],env=environment,capture_output=True,text=True,timeout=20,check=False)
    if result.returncode:
        raise ValueError("installed_schema_read_failed")
    return json.loads(result.stdout)


def run(sha):
    report={"version":1,"release_sha":sha,"state":"gap","reason":"cleanup_not_attempted","deleted_count":None,"deleted_bytes":None,"observed_at":dt.datetime.now(dt.timezone.utc).isoformat(),"space_observations":[]}
    observer = load_helper("maintenance_result", "runtime-maintenance.py")
    resources = {"release_storage": ROOT / "releases"}
    space_before = observer.sample_space(resources)
    try:
        if os.geteuid()!=0 or not re.fullmatch(r"[0-9a-f]{40}",sha):
            raise ValueError("root_and_release_sha_required")
        require_lock(ROOT)
        if (ROOT/"current").resolve()!=ROOT/"releases"/sha:
            raise ValueError("release_changed_before_cleanup")
        cleanup=load_helper("release_cleanup","cleanup-releases.py")
        WORK.mkdir(mode=0o700,exist_ok=True)
        info=WORK.lstat()
        if not stat.S_ISDIR(info.st_mode) or WORK.resolve()!=WORK or info.st_uid!=0 or info.st_mode&0o077:
            raise ValueError("unsafe_schema_work_directory")
        with tempfile.TemporaryDirectory(dir=WORK) as temporary:
            schema=Path(temporary)/"schema.json"
            schema.write_text(json.dumps(snapshot(sha,database_environment(Path("/etc/aicrm/aicrm.env")))))
            plan=cleanup.inventory(ROOT,schema,[])
            if plan["blockers"]:
                report["reason"]="two_verified_schema_compatible_rollbacks_missing"
                report.update(deleted_count=0,deleted_bytes=0)
                report["rollback_count"]=len(plan["rollback_releases"])
            else:
                result=cleanup.apply_plan(ROOT,schema,plan,[])
                report.update(state="completed",reason="verified_cleanup_completed",deleted_count=len(result["deleted"]),deleted_bytes=result["deleted_bytes"],rollback_count=len(result["rollback_releases"]))
    except Exception as failure:
        # Detailed stable per-release evidence remains in cleanup's root-owned
        # audit; do not leak config, psql errors or paths through release logs.
        report["reason"]="release_cleanup_evidence_or_execution_gap"
        if getattr(failure,"deleted",None):
            report["confirmed_deleted_count"]=len(failure.deleted)
            report["confirmed_deleted_bytes"]=sum(item["bytes"] for item in failure.deleted)
    report["space_observations"] = observer.space_observations(space_before, observer.sample_space(resources))
    report["observed_at"] = dt.datetime.now(dt.timezone.utc).isoformat()
    try:
        observer.write_result(RESULT,report)
    except Exception:
        report.update(state="gap",reason="release_cleanup_result_persistence_failed")
    print(json.dumps(report,sort_keys=True))
    return 0 if report["state"]=="completed" else 1


if __name__=="__main__":
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--sha",required=True)
    raise SystemExit(run(parser.parse_args().sha))
