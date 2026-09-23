#!/usr/bin/env bash
set -euo pipefail
mirror="${AICRM_SOURCE_MIRROR:-/opt/aicrm/source-mirror.git}"; status="${AICRM_SOURCE_MIRROR_STATUS:-/opt/aicrm/source-mirror-status.json}"
mkdir -p "$(dirname "$mirror")"; [[ -d "$mirror/objects" ]] || git init --bare "$mirror" >/dev/null
set +e; timeout "${AICRM_MIRROR_TIMEOUT:-120}" git -C "$mirror" fetch --prune origin main; rc=$?; set -e
python3 - "$status" "$rc" "$mirror" <<'PY'
import json,sys,time,pathlib,subprocess
p=pathlib.Path(sys.argv[1]);rc=int(sys.argv[2]);m=sys.argv[3];v={'schema':1,'status':'ready' if rc==0 else 'mirror_stale','updated_at':int(time.time()),'mirror':m,'error_code':rc}
if rc==0:
 try:v['main_sha']=subprocess.check_output(['git','-C',m,'rev-parse','refs/remotes/origin/main'],text=True).strip()
 except Exception:pass
p.parent.mkdir(parents=True,exist_ok=True);p.write_text(json.dumps(v,indent=2)+'\n')
PY
exit 0
