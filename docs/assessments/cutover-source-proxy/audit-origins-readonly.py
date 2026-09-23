import pathlib,subprocess,json,stat
pid=subprocess.check_output(['systemctl','show','aicrm','-p','MainPID','--value'],text=True).strip()
e=dict(x.decode().split('=',1) for x in pathlib.Path('/proc/'+pid+'/environ').read_bytes().split(b'\0') if b'=' in x)
c=json.loads(pathlib.Path('/root/aicrm-cutover-config-candidate-20260911/candidate.json').read_text())['values']
override={}
for line in pathlib.Path('/root/aicrm-domain-cutover-candidate-20260911/wecom.env.candidate').read_text().splitlines():
 if '=' in line:
  k,v=line.split('=',1)
  if k.endswith('PUBLIC_ORIGIN'):override[k]=v.strip('"')
old=['id-dev.youcangogogo.com','www.qianlan333.cloud']
formal='https://www.youcangogogo.com'
for k in sorted(set(e)|set(c)|set(override)):
 if k.endswith('PUBLIC_ORIGIN') or any(h in str(e.get(k,'')) for h in old) or any(h in str(c.get(k,'')) for h in old):
  effective=override.get(k,c.get(k,e.get(k,'')))
  print(json.dumps({'key':k,'active_needs_change':any(h in str(e.get(k,'')) for h in old) or (k.endswith('PUBLIC_ORIGIN') and e.get(k)!=formal),'candidate_needs_change':any(h in str(effective) for h in old) or (k.endswith('PUBLIC_ORIGIN') and effective!=formal),'candidate_override_present':k in override}))
for k in ['AICRM_WECHAT_PAY_PRIVATE_KEY_PATH','AICRM_WECHAT_PAY_PLATFORM_CERT_PATH']:
 p=pathlib.Path('/root/aicrm-cutover-config-candidate-20260911/'+k+'.pem')
 print(json.dumps({'key':k,'candidate_file_present':p.is_file(),'root_only':p.stat().st_uid==0 and stat.S_IMODE(p.stat().st_mode)==0o600,'active_file_present':pathlib.Path(e.get(k,'/nonexistent')).is_file(),'active_service_readable':subprocess.run(['sudo','-u','aicrm','test','-r',e.get(k,'/nonexistent')],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL).returncode==0}))
print('service_user='+subprocess.check_output(['systemctl','show','aicrm','-p','User','--value'],text=True).strip())
