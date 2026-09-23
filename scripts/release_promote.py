#!/usr/bin/env python3
"""Serial same-package production promotion, reusing the reviewed installer path.

No candidate discovery, no package rebuild, no automatic retry after uncertainty.
"""
from __future__ import annotations
import argparse
import fcntl
import hashlib
import json
import os
import platform
import shutil
import subprocess
import tempfile
import time
import uuid
from pathlib import Path
from release_control import prepare_promote
from release_batch import validate_batch
from release_handoff import live_remote_main
from release_queue import change, reservation_file
from release_events import save

ACTIVE = {'preview_building','staging_acceptance','frozen','waiting_merge','merged','production','observing'}

def digest_file(path):
    digest=hashlib.sha256()
    with Path(path).open('rb') as handle:
        for block in iter(lambda:handle.read(1024*1024),b''): digest.update(block)
    return digest.hexdigest()

def require_staging_node(identity_path: Path) -> None:
    if platform.system() != 'Linux': raise ValueError('promotion executes only on Linux staging node')
    stat=identity_path.stat()
    if stat.st_uid != 0 or stat.st_mode & 0o022: raise ValueError('staging node identity must be root-owned and not writable by group/other')
    identity=json.loads(identity_path.read_text())
    if identity.get('role')!='staging-build' or identity.get('public_ip')!='49.232.57.128':
        raise ValueError('not the configured staging build node')

def ssh_readback(host,user,key,known_hosts):
    command=['ssh','-i',str(key),'-o','BatchMode=yes','-o','IdentitiesOnly=yes',
             '-o','StrictHostKeyChecking=yes','-o',f'UserKnownHostsFile={known_hosts}',
             '-o','ConnectTimeout=30',f'{user}@{host}',
             'curl --fail --silent --show-error http://127.0.0.1:8080/readyz']
    result=subprocess.run(command,text=True,capture_output=True,timeout=60,check=True)
    return json.loads(result.stdout)

def queue_update(queue_file, candidate_id, token, generation, operation):
    lock_path=queue_file.with_suffix('.lock'); lock_path.parent.mkdir(parents=True,exist_ok=True)
    with lock_path.open('a') as lock:
        fcntl.flock(lock,fcntl.LOCK_EX)
        reservation=reservation_file(queue_file)
        current=json.loads(reservation.read_text()) if reservation.exists() else None
        if current and (current.get('candidate_id'),current.get('token'),current.get('generation'))!=(candidate_id,token,generation):
            raise ValueError('another promotion owns queue')
        queue=json.loads(queue_file.read_text())
        result=operation(queue,reservation)
        save(queue_file,queue)
        return result

def promote(*,handoff_path,queue_file,merged_main,production_sha,host,user,key_file,known_hosts_file,
            attempt_file,deploy_script,staging_node_id=Path('/opt/aicrm/staging-node-id.json'),readback=ssh_readback,deploy=None,
            bootstrap=None):
    reviewed = Path(__file__).resolve().with_name('deploy-release-local.sh')
    if deploy is None and Path(deploy_script).resolve() != reviewed:
        raise ValueError('only reviewed scripts/deploy-release-local.sh may install production package')
    if deploy is None and (host != '124.220.53.183' or user != 'ubuntu'):
        raise ValueError('production host/user differ from reviewed release target')
    if deploy is None: require_staging_node(Path(staging_node_id))
    handoff=json.loads(handoff_path.read_text())
    if 'members' in handoff:
        if handoff.get('business_acceptance',{}).get('status')=='business_acceptance_deferred_to_user':
            from release_deferred_acceptance import validate_deferred_batch
            handoff=validate_deferred_batch(handoff_path,check_live_main=False);receipt_path=Path(handoff['_technical_receipt_path'])
        else:
            handoff=validate_batch(handoff_path,check_live_main=False)
            first=handoff['members'][0]; receipt_path=Path(first['receipt_ref'])
            if not receipt_path.is_absolute(): receipt_path=handoff_path.parent/receipt_path
        handoff['staging_acceptance']={'candidate_id':handoff['candidate_id'],'receipt':str(receipt_path)}
    cid=handoff['staging_acceptance']['candidate_id']
    lock_file=queue_file.with_name(queue_file.name+'.promotion.lock')
    with lock_file.open('a') as long_lock:
        fcntl.flock(long_lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
        if attempt_file.exists():
            previous=json.loads(attempt_file.read_text())
            if previous.get('candidate_id')!=cid or previous.get('merged_main_sha')!=merged_main:
                raise ValueError('attempt file belongs to another candidate')
            # Any previous deploy attempt, even if it crashed, is readback only.
            if previous['status'] in {'attempting','outcome_unknown','observing'}:
                with tempfile.TemporaryDirectory(prefix='aicrm-promotion-credentials-') as temp:
                    local_key,local_hosts=copy_credentials(key_file,known_hosts_file,Path(temp))
                    active=readback(host,user,local_key,local_hosts)
                previous['latest_readback']=active
                save(attempt_file,previous)
                return previous
            raise ValueError('existing attempt status cannot be retried')
        prepared=prepare_promote(handoff_path,queue_file,merged_main,production_sha,bootstrap)
        receipt_path=Path(handoff['staging_acceptance']['receipt'])
        if not receipt_path.is_absolute(): receipt_path=handoff_path.parent/receipt_path
        accepted=json.loads(receipt_path.read_text())
        archive=Path(accepted['package_path'])
        if not archive.is_absolute(): archive=receipt_path.parent/archive
        expected_dir=Path('/opt/aicrm/builds')/handoff['merge_preview_sha']
        if deploy is None and (archive.resolve().parent!=expected_dir or not archive.is_file()):
            raise ValueError('package must be the accepted artifact under staging builds directory')
        if archive.name!=f'aicrm-{handoff["merge_preview_sha"]}.tar.gz':
            raise ValueError('archive name does not match exact preview SHA')
        if digest_file(archive)!=handoff['package_sha256']: raise ValueError('archive digest changed')
        for path in (key_file,known_hosts_file):
            if not Path(path).is_file(): raise ValueError('promotion credentials missing')
        with tempfile.TemporaryDirectory(prefix='aicrm-promotion-credentials-') as temp:
            local_key,local_hosts=copy_credentials(key_file,known_hosts_file,Path(temp))
            active=readback(host,user,local_key,local_hosts)
            if active.get('release_sha')!=production_sha or active.get('status')!='ready':
                raise ValueError('active production release differs from expected ancestor')
            token=uuid.uuid4().hex
            generation=uuid.uuid4().hex
            def reserve(queue,reservation):
                if reservation.exists(): raise ValueError('promotion reservation already exists')
                owned=[i for i in queue['items'] if i.get('candidate_id')==cid]
                bridge=prepared.get('bootstrap_bridge')
                other=[i for i in queue['items'] if i.get('status') in ACTIVE and i.get('candidate_id')!=cid
                       and not (bridge and i.get('candidate_id')==bridge['legacy_candidate_id']
                                and i.get('status')=='observing' and i.get('repair_candidate_id')==cid)]
                if len(owned)!=1 or owned[0]['status']!='waiting_merge' or other: raise ValueError('queue owner changed')
                save(reservation,{'candidate_id':cid,'token':token,'generation':generation,'created_at':int(time.time())})
                change(queue,'transition',candidate_id=cid,status='merged')
                change(queue,'transition',candidate_id=cid,status='production')
            queue_update(queue_file,cid,token,generation,reserve)
            attempt={'candidate_id':cid,'merged_main_sha':merged_main,'merge_preview_sha':handoff['merge_preview_sha'],
                     'candidate_tree_sha':prepared['candidate_tree_sha'],'package_sha256':prepared['package_sha256'],
                     'previous_active_sha':production_sha,'status':'attempting','attempt_id':str(uuid.uuid4()),
                     'bootstrap_bridge':prepared.get('bootstrap_bridge'),
                     'business_acceptance_status':handoff.get('business_acceptance',{}).get('status','accepted'),
                     'unverified_business_journeys':handoff.get('business_acceptance',{}).get('unverified_business_journeys',[]),
                     'started_at':int(time.time())}
            save(attempt_file,attempt)
            if live_remote_main(Path(handoff['worktree'])) != merged_main or digest_file(archive) != handoff['package_sha256']:
                attempt.update(status='outcome_unknown',error='remote main or archive changed after reservation')
                save(attempt_file,attempt)
                raise ValueError(attempt['error'])
            env=os.environ.copy();env.update(DEPLOY_TARGET=host,DEPLOY_USER=user,DEPLOY_KEY=str(local_key),
                                              DEPLOY_KNOWN_HOSTS=str(local_hosts),DEPLOY_ENVIRONMENT='production',
                                              DEPLOY_RECEIPT=str(attempt_file.with_name(attempt_file.stem+'-install-receipt.json')))
            command=['bash',str(deploy_script),str(archive),handoff['merge_preview_sha']]
            try:
                if deploy is None: subprocess.run(command,cwd=Path(handoff['worktree']),env=env,check=True,timeout=1800)
                else: deploy(command,env)
                install=json.loads(Path(env['DEPLOY_RECEIPT']).read_text())
                if install.get('release_sha')!=handoff['merge_preview_sha'] or install.get('tree_sha')!=handoff['candidate_tree_sha'] or install.get('package_sha256')!=handoff['package_sha256']:
                    raise ValueError('production install receipt differs from accepted package')
                observed=readback(host,user,local_key,local_hosts)
                if observed.get('release_sha')!=handoff['merge_preview_sha'] or observed.get('status')!='ready':
                    raise ValueError('post-install active release mismatch')
                attempt.update(status='observing',install_receipt=env['DEPLOY_RECEIPT'],latest_readback=observed)
                save(attempt_file,attempt)
                def finish_queue(q,reservation):
                    change(q,'transition',candidate_id=cid,status='observing')
                    reservation.unlink()
                queue_update(queue_file,cid,token,generation,finish_queue)
                return attempt
            except BaseException as exc:
                attempt.update(status='outcome_unknown',error=f'{type(exc).__name__}: {exc}'[:1000])
                save(attempt_file,attempt)
                raise

def copy_credentials(key,hosts,temp):
    os.chmod(temp,0o700)
    copies=[]
    for source,name in ((key,'key'),(hosts,'known_hosts')):
        dest=temp/name
        shutil.copyfile(source,dest);os.chmod(dest,0o600);copies.append(dest)
    return copies

def main():
    p=argparse.ArgumentParser()
    for name in ('handoff','queue','key','known-hosts','attempt-file','deploy-script'):
        p.add_argument('--'+name,required=True,type=Path)
    for name in ('merged-main','production-sha','host','user'):
        p.add_argument('--'+name,required=True)
    p.add_argument('--bootstrap',type=Path)
    a=p.parse_args()
    result=promote(handoff_path=a.handoff,queue_file=a.queue,merged_main=a.merged_main,
        production_sha=a.production_sha,host=a.host,user=a.user,key_file=a.key,bootstrap=a.bootstrap,
        known_hosts_file=a.known_hosts,attempt_file=a.attempt_file,deploy_script=a.deploy_script)
    print(json.dumps(result,ensure_ascii=False,indent=2))
if __name__=='__main__':main()
