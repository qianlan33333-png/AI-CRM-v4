#!/usr/bin/env python3
"""Offline root-only candidate writer. Never changes a service or active environment."""
import json, os, pathlib, stat, subprocess
SOURCE=pathlib.Path('/root/aicrm-cutover-config-candidate-20260911/candidate.json')
OUT=pathlib.Path('/root/aicrm-domain-cutover-candidate-20260911/wecom.env.candidate')
KEYS=['AICRM_WECOM_CORP_ID','AICRM_WECOM_AGENT_ID','AICRM_WECOM_SECRET','AICRM_WECOM_CONTACT_SECRET','AICRM_WECOM_CALLBACK_TOKEN','AICRM_WECOM_CALLBACK_AES_KEY']
if os.geteuid()!=0: raise SystemExit('root required')
if stat.S_IMODE(SOURCE.stat().st_mode)!=0o600 or SOURCE.stat().st_uid!=0: raise SystemExit('unsafe source permissions')
data=json.loads(SOURCE.read_text())
if data.get('activate') is not False: raise SystemExit('candidate must remain inactive')
values=data['values']
for key in KEYS:
    if not isinstance(values.get(key),str) or not values[key] or any(c in values[key] for c in '\r\n\0'): raise SystemExit('missing or unsafe grouped setting')
# Runtime config takes precedence over env. Never silently mix applications.
query="SELECT COALESCE(jsonb_object_agg(v.setting_key,v.value),'{}'::jsonb) FROM config_runtime_active_release a JOIN config_runtime_release_values v ON v.release_id=a.release_id WHERE a.singleton AND v.setting_key IN ('wecom.corp_id','wecom.agent_id')"
try: overrides=json.loads(subprocess.check_output(['sudo','-u','postgres','psql','-X','-d','aicrm','-Atc',query],stderr=subprocess.DEVNULL,text=True))
except Exception: raise SystemExit('runtime override verification failed')
for short,key in [('wecom.corp_id',KEYS[0]),('wecom.agent_id',KEYS[1])]:
    if short in overrides and str(overrides[short])!=values[key]: raise SystemExit('conflicting runtime override; use configuration owner before activation')
# These are systemd EnvironmentFile quoted values, NOT shell source code.
def quote(value): return '"'+value.replace('\\','\\\\').replace('"','\\"')+'"'
lines=[key+'='+quote(values[key]) for key in KEYS]
lines += ['AICRM_PUBLIC_ORIGIN="https://www.youcangogogo.com"','AICRM_H5_PUBLIC_ORIGIN="https://www.youcangogogo.com"']
OUT.parent.mkdir(mode=0o700,parents=True,exist_ok=True)
if stat.S_IMODE(OUT.parent.stat().st_mode)!=0o700 or OUT.parent.stat().st_uid!=0: raise SystemExit('unsafe destination')
fd=os.open(OUT,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600)
with os.fdopen(fd,'w') as f: f.write('\n'.join(lines)+'\n')
print('grouped_candidate_prepared; inactive; root0600')
