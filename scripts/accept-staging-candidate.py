#!/usr/bin/env python3
import argparse,hashlib,json,time
from pathlib import Path
def main():
 p=argparse.ArgumentParser();p.add_argument('manifest',type=Path);p.add_argument('built',type=Path);p.add_argument('readback',type=Path);p.add_argument('out',type=Path);a=p.parse_args();m=json.loads(a.manifest.read_text());b=json.loads(a.built.read_text());r=json.loads(a.readback.read_text())
 for k in ('candidate_id','merge_preview_sha','tree_sha'):
  if m.get(k)!=b.get(k):raise SystemExit('candidate/built mismatch: '+k)
 if b.get('status')!='built':raise SystemExit('built receipt required')
 if r.get('business_verified') is not True:raise SystemExit('business readback is not verified')
 if r.get('effect_mode','virtual') not in ('virtual','live'):raise SystemExit('invalid effect mode')
 # The built receipt is authoritative for package provenance; the manifest
 # intentionally has no package hash before the build runs.
 v={**m,**b,'status':'accepted','accepted_at':int(time.time()),'business_readback':r,'receipt_sha256':hashlib.sha256(a.built.read_bytes()).hexdigest()};a.out.write_text(json.dumps(v,ensure_ascii=False,indent=2)+'\n')
if __name__=='__main__':main()
