#!/usr/bin/env bash
set -euo pipefail
sha="${1:?merge preview sha}"; bundle="${AICRM_SOURCE_BUNDLE:?}"; manifest="${AICRM_CANDIDATE_MANIFEST:?}"
host="${STAGING_HOST:?}"; user="${STAGING_USER:-ubuntu}"; key="${STAGING_KEY:?}"; known="${STAGING_KNOWN_HOSTS:?}"
[[ "$sha" =~ ^[0-9a-f]{40}$ ]] || exit 2
python3 scripts/release_candidate.py validate "$manifest"
[[ "$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["merge_preview_sha"])' "$manifest")" == "$sha" ]]
git bundle verify "$bundle"
flags=(-i "$key" -o BatchMode=yes -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile="$known" -o ConnectTimeout=30)
ssh "${flags[@]}" "$user@$host" "sudo install -d -o '$user' -g '$user' /opt/aicrm/source-bundles /opt/aicrm/builds; sudo touch /opt/aicrm/staging-build.lock; sudo chown '$user' /opt/aicrm/staging-build.lock"
remote="/opt/aicrm/source-bundles/$sha"
scp "${flags[@]}" "$bundle" "$user@$host:$remote.bundle"
# Verify prerequisites against the staging cache. Network/SSH failures remain errors.
if ! ssh "${flags[@]}" "$user@$host" "git -C /opt/aicrm/source-mirror.git bundle verify '$remote.bundle'"; then
  full="$(mktemp)"; ref="refs/candidates/$sha"; trap 'rm -f "$full"; git update-ref -d "$ref"' EXIT
  git update-ref "$ref" "$sha"; git bundle create "$full" "$ref"
  scp "${flags[@]}" "$full" "$user@$host:$remote.bundle"
fi
scp "${flags[@]}" "$manifest" "$user@$host:$remote.json"
scp "${flags[@]}" deploy/build-release-on-staging-remote.sh "$user@$host:$remote.sh"
ssh "${flags[@]}" "$user@$host" "bash '$remote.sh' '$sha' '$remote.bundle' '/opt/aicrm/builds/$sha' '$remote.json'"
# Only small metadata comes back to the caller; archive stays on staging.
scp "${flags[@]}" "$user@$host:/opt/aicrm/builds/$sha/staging-receipt.json" "staging-receipt-$sha.json"
