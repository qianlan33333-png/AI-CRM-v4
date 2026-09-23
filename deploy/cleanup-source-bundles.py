#!/usr/bin/env python3
"""Inventory or delete proven-expired staging source-bundle groups.

A group is exactly <sha>.bundle/.json/.sh. Deletion additionally requires a
root-owned retirement receipt under builds/<sha>/source-bundle-retention.json
and a matching terminal release-queue item older than the retention boundary.
"""
from __future__ import annotations
import argparse,fcntl,hashlib,json,os,re,stat,sys,time,uuid
from pathlib import Path
SHA=re.compile(r'^[0-9a-f]{40}$'); SUFFIXES={'.bundle','.json','.sh'}; TERMINAL={'released','stale_candidate'}; RETENTION=30*86400
ROOT_UID=0
class Refuse(RuntimeError):pass

def digest(path):
 h=hashlib.sha256()
 with path.open('rb') as f:
  for block in iter(lambda:f.read(1024*1024),b''):h.update(block)
 return h.hexdigest()

def safe_root(root):
 info=root.lstat()
 if root.resolve()!=root or not stat.S_ISDIR(info.st_mode) or info.st_mode&0o022:raise Refuse('invalid_source_bundle_root')
 return info

def secure_lock(path):
 descriptor=os.open(path,os.O_RDWR|os.O_CREAT|os.O_NOFOLLOW,0o600)
 info=os.fstat(descriptor)
 if not stat.S_ISREG(info.st_mode) or info.st_nlink!=1 or info.st_mode&0o022:
  os.close(descriptor);raise Refuse('invalid_retention_lock')
 return descriptor

def trusted_active(current,success):
 try:resolved=current.resolve(strict=True)
 except OSError as exc:raise Refuse('active_release_unavailable') from exc
 if resolved.parent.name!='releases' or not SHA.fullmatch(resolved.name):raise Refuse('active_release_unmanaged')
 for path in (current.parent,current.parent/'releases',success):
  info=path.lstat()
  if info.st_uid!=ROOT_UID or info.st_mode&0o022 or not stat.S_ISDIR(info.st_mode):raise Refuse('active_release_control_untrusted')
 receipt=success/(resolved.name+'.json')
 try:info=receipt.lstat()
 except OSError as exc:raise Refuse('active_release_receipt_unavailable') from exc
 if info.st_uid!=ROOT_UID or info.st_nlink!=1 or info.st_mode&0o022 or not stat.S_ISREG(info.st_mode):raise Refuse('active_release_receipt_untrusted')
 try:value=json.loads(receipt.read_text())
 except (OSError,json.JSONDecodeError) as exc:raise Refuse('active_release_receipt_invalid') from exc
 if value.get('version')!=1 or value.get('release_sha')!=resolved.name or not isinstance(value.get('sequence'),int):raise Refuse('active_release_receipt_invalid')
 return resolved.name

def queue_terminal(item,before):
 if item.get('status') not in TERMINAL:return False
 events=item.get('events')
 if not isinstance(events,list) or not events:return False
 terminal=[e for e in events if e.get('to')==item['status'] and isinstance(e.get('time'),int)]
 return bool(terminal) and max(e['time'] for e in terminal)<before

def retirement(builds,sha,item,before,files):
 build=builds/sha
 if not build.is_dir() or build.is_symlink() or build.resolve().parent!=builds.resolve():return None
 path=build/'source-bundle-retention.json'
 if not path.is_file() or path.is_symlink():return None
 info=path.stat()
 if info.st_uid!=ROOT_UID or info.st_nlink!=1 or info.st_mode&0o022:return None
 try:value=json.loads(path.read_text())
 except (OSError,json.JSONDecodeError):return None
 if value.get('schema')!=1 or value.get('release_sha')!=sha or value.get('candidate_id')!=item.get('candidate_id') or value.get('terminal_status')!=item.get('status'):return None
 if not isinstance(value.get('expired_at'),int) or value['expired_at']>=before:return None
 expected=value.get('files')
 if not isinstance(expected,dict) or set(expected)!=set(files):return None
 if any(not re.fullmatch(r'[0-9a-f]{64}',expected[name]) or expected[name]!=files[name]['sha256'] for name in files):return None
 return {'path':str(path),'sha256':digest(path),'inode':info.st_ino,'device':info.st_dev}

def inventory(root,builds,queue,before,active_sha):
 root_info=safe_root(root)
 names=list(root.iterdir()); groups={}; unknown=[]
 for path in names:
  match=re.fullmatch(r'([0-9a-f]{40})(\.bundle|\.json|\.sh)',path.name)
  if not match:unknown.append(path.name);continue
  info=path.lstat()
  if not stat.S_ISREG(info.st_mode) or info.st_nlink!=1 or info.st_dev!=root_info.st_dev or info.st_uid!=root_info.st_uid or info.st_mode&0o022:raise Refuse('unsafe_source_bundle_member')
  groups.setdefault(match.group(1),{})[match.group(2)]={'path':path,'inode':info.st_ino,'device':info.st_dev,'size':info.st_size,'mtime_ns':info.st_mtime_ns,'sha256':digest(path)}
 if unknown:raise Refuse('unknown_source_bundle_member')
 items={}
 for item in queue.get('items',[]):
  if isinstance(item,dict):items.setdefault(item.get('merge_preview_sha') or item.get('commit_sha'),[]).append(item)
 eligible=[];protected=[]
 for sha,files in sorted(groups.items()):
  if set(files)!=SUFFIXES:protected.append({'sha':sha,'reason':'incomplete_group'});continue
  records=items.get(sha,[])
  if sha==active_sha:protected.append({'sha':sha,'reason':'active_release'});continue
  if not records or any(not queue_terminal(item,before) for item in records):protected.append({'sha':sha,'reason':'active_unknown_or_recent'});continue
  identities={(item.get('candidate_id'),item.get('status')) for item in records}
  if len(identities)!=1:protected.append({'sha':sha,'reason':'ambiguous_terminal_records'});continue
  item=records[0]
  proof=retirement(builds,sha,item,before,files)
  if not proof:protected.append({'sha':sha,'reason':'retirement_proof_missing'});continue
  eligible.append({'sha':sha,'candidate_id':item['candidate_id'],'files':{k:{x:v[x] for x in ('inode','device','size','mtime_ns','sha256')} for k,v in files.items()},'receipt':proof})
 return {'schema':1,'mode':'inventory','root':str(root),'before':before,'eligible':eligible,'protected':protected,'created_at':int(time.time())}

def apply(root,builds,queue,before,active_sha,plan):
 fresh=inventory(root,builds,queue,before,active_sha)
 if plan.get('schema')!=1 or plan.get('mode')!='inventory' or plan.get('root')!=str(root) or plan.get('before')!=before:raise Refuse('invalid_cleanup_plan')
 if plan.get('eligible')!=fresh['eligible']:raise Refuse('cleanup_plan_stale')
 deleted=[]
 descriptor=os.open(root,os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW)
 try:
  for group in fresh['eligible']:
   for suffix in sorted(SUFFIXES):
    name=group['sha']+suffix; expected=group['files'][suffix]; current=os.stat(name,dir_fd=descriptor,follow_symlinks=False)
    if (current.st_ino,current.st_dev,current.st_size,current.st_mtime_ns)!=(expected['inode'],expected['device'],expected['size'],expected['mtime_ns']):raise Refuse('source_bundle_changed_before_delete')
    os.unlink(name,dir_fd=descriptor)
   deleted.append(group['sha'])
 finally:os.close(descriptor)
 return {'schema':1,'mode':'apply','deleted':deleted,'completed_at':int(time.time())}

def run(args):
 before=args.before if args.before is not None else int(time.time())-RETENTION
 if before>int(time.time())-RETENTION:raise Refuse('retention_boundary_too_recent')
 for p in (args.root,args.builds,args.queue.parent):
  if not p.is_absolute():raise Refuse('paths_must_be_absolute')
 build_descriptor=secure_lock(args.build_lock);queue_descriptor=secure_lock(args.queue.with_suffix('.lock'))
 try:
  fcntl.flock(build_descriptor,fcntl.LOCK_EX);fcntl.flock(queue_descriptor,fcntl.LOCK_EX)
  queue=json.loads(args.queue.read_text()) if args.queue.exists() else {'items':[]}
  active_sha=trusted_active(args.current,args.success)
  if args.mode=='inventory':return inventory(args.root,args.builds,queue,before,active_sha)
  if args.plan is None:raise Refuse('apply_requires_plan')
  return apply(args.root,args.builds,queue,before,active_sha,json.loads(args.plan.read_text()))
 finally:
  os.close(queue_descriptor);os.close(build_descriptor)

def main():
 p=argparse.ArgumentParser();p.add_argument('mode',choices=('inventory','apply'),nargs='?',default='inventory');p.add_argument('--root',type=Path,default=Path('/opt/aicrm/source-bundles'));p.add_argument('--builds',type=Path,default=Path('/opt/aicrm/builds'));p.add_argument('--queue',type=Path,default=Path('/opt/aicrm/release-queue.json'));p.add_argument('--build-lock',type=Path,default=Path('/opt/aicrm/staging-build.lock'));p.add_argument('--current',type=Path,default=Path('/opt/aicrm/current'));p.add_argument('--success',type=Path,default=Path('/opt/aicrm/release-success'));p.add_argument('--before',type=int);p.add_argument('--plan',type=Path)
 a=p.parse_args()
 try:print(json.dumps(run(a),sort_keys=True));return 0
 except (OSError,ValueError,Refuse) as e:print(json.dumps({'ok':False,'reason':str(e) if isinstance(e,Refuse) else 'source_bundle_cleanup_io_or_format_failed'}),file=sys.stderr);return 1
if __name__=='__main__':raise SystemExit(main())
