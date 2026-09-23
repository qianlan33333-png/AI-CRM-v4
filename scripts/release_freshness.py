#!/usr/bin/env python3
"""Issue and verify short-lived signed public-main freshness attestations."""
from __future__ import annotations
import argparse, hashlib, json, os, re, secrets, shutil, subprocess, tempfile, urllib.request
from datetime import datetime, timedelta, timezone
from pathlib import Path

NAMESPACE='aicrm-release-main-freshness-v1'
SHA=re.compile(r'^[0-9a-f]{40}$'); DIGEST=re.compile(r'^[0-9a-f]{64}$')

def sha256(path:Path)->str:
 h=hashlib.sha256()
 with path.open('rb') as f:
  for block in iter(lambda:f.read(1024*1024),b''): h.update(block)
 return h.hexdigest()

def canonical(value:dict)->bytes:
 return (json.dumps(value,sort_keys=True,separators=(',',':'),ensure_ascii=False)+'\n').encode()

def utcnow()->datetime: return datetime.now(timezone.utc)
def stamp(value:datetime)->str: return value.astimezone(timezone.utc).isoformat().replace('+00:00','Z')
def parse_stamp(value:str)->datetime: return datetime.fromisoformat(value.replace('Z','+00:00'))

def github_json(url:str,token:str|None=None)->dict:
 headers={'Accept':'application/vnd.github+json','User-Agent':'AI-CRM-v4-release-command-center','X-GitHub-Api-Version':'2022-11-28'}
 if token: headers['Authorization']='Bearer '+token
 with urllib.request.urlopen(urllib.request.Request(url,headers=headers),timeout=30) as response:
  return json.load(response)

def read_public_main(repository:str,branch:str='main',*,token:str|None=None,api_base:str='https://api.github.com',fetch=github_json)->tuple[str,str,datetime]:
 if not re.fullmatch(r'[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+',repository): raise ValueError('invalid GitHub repository')
 if branch!='main': raise ValueError('release branch must be main')
 base=api_base.rstrip('/')+'/repos/'+repository
 repo=fetch(base,token)
 if repo.get('private') is not False: raise ValueError('repository is not public')
 branch_value=fetch(base+'/branches/'+branch,token); main_sha=branch_value.get('commit',{}).get('sha','')
 if not SHA.fullmatch(main_sha): raise ValueError('GitHub branch returned invalid SHA')
 commit=fetch(base+'/git/commits/'+main_sha,token); main_tree=commit.get('tree',{}).get('sha','')
 if not SHA.fullmatch(main_tree): raise ValueError('GitHub commit returned invalid tree')
 return main_sha,main_tree,utcnow()

def build_attestation(*,repository,branch,stage,main_sha,main_tree,pr_head_sha,pr_head_tree,merge_preview_sha,candidate_tree_sha,bundle,package,identity,queried_at,now=None,ttl_seconds=900):
 if stage not in {'source','promotion'}: raise ValueError('invalid attestation stage')
 if branch!='main': raise ValueError('release branch must be main')
 if not 60<=ttl_seconds<=1800: raise ValueError('ttl must be between 60 and 1800 seconds')
 for name,value in {'main_sha':main_sha,'main_tree':main_tree,'pr_head_sha':pr_head_sha,'pr_head_tree':pr_head_tree,'merge_preview_sha':merge_preview_sha,'candidate_tree_sha':candidate_tree_sha}.items():
  if not SHA.fullmatch(value): raise ValueError(name+' must be a full SHA')
 if not bundle.is_file(): raise ValueError('source bundle missing')
 if stage=='source' and package is not None: raise ValueError('source attestation cannot bind a package')
 if stage=='promotion' and (package is None or not package.is_file()): raise ValueError('promotion attestation requires package')
 issued=now or utcnow()
 return {'schema':1,'repository':repository,'visibility':'public','branch':branch,'stage':stage,'main_sha':main_sha,'main_tree':main_tree,'pr_head_sha':pr_head_sha,'pr_head_tree':pr_head_tree,'merge_preview_sha':merge_preview_sha,'candidate_tree_sha':candidate_tree_sha,'bundle_sha256':sha256(bundle),'package_sha256':sha256(package) if package else None,'queried_at':stamp(queried_at),'issued_at':stamp(issued),'expires_at':stamp(issued+timedelta(seconds=ttl_seconds)),'nonce':secrets.token_hex(16),'signing_identity':identity}

def sign(attestation:dict,out:Path,signature_out:Path,key:Path)->None:
 if out.exists() or signature_out.exists(): raise ValueError('attestation output already exists')
 out.parent.mkdir(parents=True,exist_ok=True); out.write_bytes(canonical(attestation))
 generated=Path(str(out)+'.sig')
 try:
  subprocess.run(['ssh-keygen','-Y','sign','-f',str(key),'-n',NAMESPACE,str(out)],check=True,capture_output=True,text=True)
  signature_out.parent.mkdir(parents=True,exist_ok=True); generated.replace(signature_out)
 except Exception:
  out.unlink(missing_ok=True); generated.unlink(missing_ok=True); raise

def verify(*,attestation_path:Path,signature_path:Path,allowed_signers:Path,identity:str,expected:dict,bundle:Path,package:Path|None,git_repository:Path,now=None,max_age_seconds=1800)->dict:
 raw=attestation_path.read_bytes(); value=json.loads(raw)
 if raw!=canonical(value): raise ValueError('attestation is not canonical JSON')
 proc=subprocess.run(['ssh-keygen','-Y','verify','-f',str(allowed_signers),'-I',identity,'-n',NAMESPACE,'-s',str(signature_path)],input=raw,capture_output=True)
 if proc.returncode: raise ValueError('attestation signature invalid')
 if value.get('schema')!=1 or value.get('visibility')!='public' or value.get('signing_identity')!=identity: raise ValueError('attestation identity contract mismatch')
 for field,expected_value in expected.items():
  if value.get(field)!=expected_value: raise ValueError(field+' mismatch')
 issued=parse_stamp(value['issued_at']); queried=parse_stamp(value['queried_at']); expires=parse_stamp(value['expires_at']); current=now or utcnow()
 if issued>current+timedelta(seconds=30) or queried>issued+timedelta(seconds=30) or current>=expires: raise ValueError('attestation is expired or from the future')
 if expires-issued>timedelta(seconds=max_age_seconds): raise ValueError('attestation validity window too long')
 if value.get('bundle_sha256')!=sha256(bundle): raise ValueError('bundle digest mismatch')
 stage=value.get('stage')
 if stage=='source':
  if value.get('package_sha256') is not None or package is not None: raise ValueError('source stage package contract mismatch')
 elif stage=='promotion':
  if package is None or value.get('package_sha256')!=sha256(package): raise ValueError('package digest mismatch')
 else: raise ValueError('invalid attestation stage')
 bundle_check=subprocess.run(['git','-C',str(git_repository),'bundle','verify',str(bundle.resolve())],capture_output=True,text=True)
 if bundle_check.returncode: raise ValueError('git bundle verification failed: '+bundle_check.stderr[-500:])
 heads=subprocess.check_output(['git','bundle','list-heads',str(bundle.resolve())],text=True)
 if value['merge_preview_sha'] not in heads: raise ValueError('bundle does not contain merge preview')
 return value

def main():
 p=argparse.ArgumentParser(); sub=p.add_subparsers(dest='command',required=True)
 issue=sub.add_parser('issue')
 issue.add_argument('--repository',required=True); issue.add_argument('--branch',default='main'); issue.add_argument('--stage',choices=['source','promotion'],required=True)
 for n in ('pr-head-sha','pr-head-tree','merge-preview-sha','candidate-tree-sha'): issue.add_argument('--'+n,required=True)
 issue.add_argument('--bundle',type=Path,required=True); issue.add_argument('--package',type=Path); issue.add_argument('--signing-key',type=Path,required=True); issue.add_argument('--identity',required=True); issue.add_argument('--ttl-seconds',type=int,default=900); issue.add_argument('--out',type=Path,required=True); issue.add_argument('--signature-out',type=Path,required=True); issue.add_argument('--api-base',default='https://api.github.com')
 check=sub.add_parser('verify')
 check.add_argument('--attestation',type=Path,required=True); check.add_argument('--signature',type=Path,required=True); check.add_argument('--allowed-signers',type=Path,required=True); check.add_argument('--identity',required=True); check.add_argument('--repository',required=True); check.add_argument('--branch',default='main'); check.add_argument('--stage',choices=['source','promotion'],required=True)
 for n in ('main-sha','main-tree','pr-head-sha','pr-head-tree','merge-preview-sha','candidate-tree-sha'): check.add_argument('--'+n,required=True)
 check.add_argument('--bundle',type=Path,required=True); check.add_argument('--package',type=Path); check.add_argument('--git-repository',type=Path,required=True); check.add_argument('--max-age-seconds',type=int,default=1800)
 a=p.parse_args()
 if a.command=='issue':
  token=os.environ.get('GITHUB_TOKEN'); main_sha,main_tree,queried=read_public_main(a.repository,a.branch,token=token,api_base=a.api_base)
  value=build_attestation(repository=a.repository,branch=a.branch,stage=a.stage,main_sha=main_sha,main_tree=main_tree,pr_head_sha=a.pr_head_sha,pr_head_tree=a.pr_head_tree,merge_preview_sha=a.merge_preview_sha,candidate_tree_sha=a.candidate_tree_sha,bundle=a.bundle,package=a.package,identity=a.identity,queried_at=queried,ttl_seconds=a.ttl_seconds)
  sign(value,a.out,a.signature_out,a.signing_key); result={'issued':str(a.out),'signature':str(a.signature_out),'main_sha':main_sha,'main_tree':main_tree,'expires_at':value['expires_at']}
 else:
  expected={k:getattr(a,k) for k in ('repository','branch','stage','main_sha','main_tree','pr_head_sha','pr_head_tree','merge_preview_sha','candidate_tree_sha')}
  value=verify(attestation_path=a.attestation,signature_path=a.signature,allowed_signers=a.allowed_signers,identity=a.identity,expected=expected,bundle=a.bundle,package=a.package,git_repository=a.git_repository,max_age_seconds=a.max_age_seconds); result={'verified':True,'main_sha':value['main_sha'],'candidate_tree_sha':value['candidate_tree_sha'],'stage':value['stage']}
 print(json.dumps(result,ensure_ascii=False,sort_keys=True))
if __name__=='__main__': main()
