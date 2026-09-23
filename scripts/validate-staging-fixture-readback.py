#!/usr/bin/env python3
"""Validate synthetic staging business facts, not merely HTTP status."""
import argparse,json
from pathlib import Path

def main():
 p=argparse.ArgumentParser();p.add_argument('readback',type=Path);p.add_argument('--fixture',type=Path,required=True);a=p.parse_args()
 expected=json.loads(a.fixture.read_text());actual=json.loads(a.readback.read_text())
 if actual.get('fixture_id') != expected['fixture_id'] or actual.get('synthetic') is not True: raise SystemExit('missing synthetic fixture identity')
 checks={'customer_sync_run_status':'succeeded','customer_projection_count':1,'customer_profile_count':1,'research_material_count':1,'research_mapping_count':1}
 for k,want in checks.items():
  if actual.get(k)!=want: raise SystemExit(f'fixture readback failed: {k}={actual.get(k)!r}')
 if actual.get('business_verified') is not True: raise SystemExit('fixture readback must explicitly set business_verified=true')
 print(json.dumps({'fixture_id':actual['fixture_id'],'synthetic':True,'business_verified':True,'assertions':checks},ensure_ascii=False))
if __name__=='__main__':main()
