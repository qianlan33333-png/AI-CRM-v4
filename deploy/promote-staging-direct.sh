#!/usr/bin/env bash
set -euo pipefail
# Execute on staging. Explicit candidate receipt, never scan for a matching old tree.
receipt="${1:-}"; merge_sha="${2:?merged commit}"; tree="${3:?merged tree}"
key="${PRODUCTION_KEY_FILE:?temporary key file}"; kh="${PRODUCTION_KNOWN_HOSTS_FILE:?pinned known hosts}"
trap 'rm -f -- "$key" "$kh"' EXIT
chmod 600 "$key" "$kh"
[[ "$merge_sha" =~ ^[0-9a-f]{40}$ && "$tree" =~ ^[0-9a-f]{40}$ ]] || exit 2
if [[ -z "$receipt" ]]; then
  receipt="$(python3 - "$tree" <<'PY'
import pathlib,json,sys
for p in sorted(pathlib.Path('/opt/aicrm/builds').glob('*/staging-receipt.json')):
 try:
  v=json.loads(p.read_text())
  if v.get('status')=='accepted' and v.get('tree_sha')==sys.argv[1]: print(p); break
 except Exception: pass
else: raise SystemExit('no accepted staging receipt for merged tree')
PY
)"
fi
root="$(cd "$(dirname "$receipt")" && pwd)"; receipt="$root/$(basename "$receipt")"
cd "$root"
read -r release_sha cid digest < <(python3 - "$receipt" "$tree" <<'PY'
import json,re,sys
v=json.load(open(sys.argv[1]))
if v.get('status')!='accepted' or v.get('tree_sha')!=sys.argv[2]:raise SystemExit('accepted candidate tree mismatch')
for k,n in [('commit_sha',40),('candidate_id',24),('package_sha256',64)]:
 if not re.fullmatch('[0-9a-f]{'+str(n)+'}',v.get(k,'')):raise SystemExit('invalid '+k)
if v['commit_sha']!=v.get('merge_preview_sha'):raise SystemExit('not a merge-preview receipt')
print(v['commit_sha'],v['candidate_id'],v['package_sha256'])
PY
)
archive="$root/aicrm-$release_sha.tar.gz"
python3 scripts/validate-staging-receipt.py "$receipt" --head "$release_sha" --tree "$tree" --package "$archive" >/dev/null
# Older accepted receipts may predate queue registration. Adopt them explicitly
# as waiting_merge so the direct path never silently bypasses the queue.
if ! python3 - "$cid" <<'PY'
import json, pathlib, sys
p=pathlib.Path('/opt/aicrm/release-queue.json')
v=json.loads(p.read_text()) if p.exists() else {'items': []}
raise SystemExit(0 if any(i.get('candidate_id')==sys.argv[1] for i in v.get('items',[])) else 1)
PY
then python3 scripts/release_queue.py adopt-accepted "$receipt"; fi
python3 scripts/release_queue.py transition "$cid" merged
python3 scripts/release_queue.py transition "$cid" production
# Reuse the reviewed installer/observer/readback path, running on staging.
# Its single scp sends the archive directly from staging to production.
DEPLOY_TARGET="${PRODUCTION_HOST:?}" DEPLOY_USER="${PRODUCTION_USER:-ubuntu}" \
DEPLOY_KEY="$key" DEPLOY_KNOWN_HOSTS="$kh" DEPLOY_ENVIRONMENT=production \
DEPLOY_RECEIPT="$root/production-install-receipt.json" \
bash scripts/deploy-release-local.sh "$archive" "$release_sha"
python3 scripts/release_queue.py transition "$cid" observing
# Installed/readiness is deliberately not marked released: business readback and observation still required.
python3 - "$root/production-install-receipt.json" "$merge_sha" "$cid" "$digest" "$tree" <<'PY'
import json,sys
p,merge_sha,cid,digest,tree=sys.argv[1:]
v=json.load(open(p));v.update(merge_sha=merge_sha,candidate_id=cid,tree_sha=tree,package_sha256=digest,status='observing',promotion_mode='staging_direct')
with open(p,'w') as out:json.dump(v,out,indent=2);out.write('\n')
PY
