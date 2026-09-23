#!/usr/bin/env bash
set -euo pipefail
umask 022
sha="${1:?sha}"; bundle="${2:?bundle}"; root="${3:?root}"; manifest="${4:?manifest}"
[[ "$sha" =~ ^[0-9a-f]{40}$ && "$root" == "/opt/aicrm/builds/$sha" ]] || exit 2
exec 9>/opt/aicrm/staging-build.lock
flock -x 9
# Immutable builds are never deleted by a concurrent or repeated request.
[[ ! -e "$root" ]] || { echo 'build already exists; inspect or use a new candidate attempt' >&2; exit 3; }
mirror=/opt/aicrm/source-mirror.git
[[ -d "$mirror/objects" ]] || git init --bare "$mirror"
git -C "$mirror" bundle verify "$bundle"
git -C "$mirror" fetch "$bundle" "$sha"
git clone --no-checkout "$mirror" "$root"
git -C "$root" fetch "$mirror" "$sha"
git -C "$root" checkout --detach "$sha"
cd "$root"
python3 scripts/release_candidate.py validate "$manifest"
[[ "$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["merge_preview_sha"])' "$manifest")" == "$sha" ]]
cp "$manifest" candidate-manifest.json
# Keep the exact candidate base available to migration validation even when the
# daily GitHub mirror is stale; this is an object reference, never a new source.
base="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["base_main_sha"])' "$manifest")"
git cat-file -e "$base^{commit}"
git update-ref refs/remotes/origin/main "$base"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GITHUB_SHA="$sha" bash scripts/run-donor-view-consumers.sh release-fast
python3 scripts/check-release-binaries.py release/bin
python3 - "$root" "$sha" <<'PY'
import hashlib,json,pathlib,subprocess,sys
root=pathlib.Path(sys.argv[1]);sha=sys.argv[2];archive=root/('aicrm-'+sha+'.tar.gz');m=json.loads((root/'candidate-manifest.json').read_text())
v={**m,'repository':'AI-CRM-v3','environment':'staging','status':'built','commit_sha':sha,'package_name':archive.name,'package_sha256':hashlib.sha256(archive.read_bytes()).hexdigest(),'business_readback':'not run; build provenance only'}
(root/'staging-receipt.json').write_text(json.dumps(v,indent=2)+'\n')
PY
