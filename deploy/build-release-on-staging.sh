#!/usr/bin/env bash
set -euo pipefail
sha="${1:?merge preview sha}"; bundle="${AICRM_SOURCE_BUNDLE:?}"; manifest="${AICRM_CANDIDATE_MANIFEST:?}"
attestation="${AICRM_SOURCE_ATTESTATION:?}"; signature="${AICRM_SOURCE_SIGNATURE:?}"
allowed_signers="${AICRM_STAGING_ALLOWED_SIGNERS:?}"
host="${STAGING_HOST:?}"; user="${STAGING_USER:-ubuntu}"; key="${STAGING_KEY:?}"; known="${STAGING_KNOWN_HOSTS:?}"
[[ "$sha" =~ ^[0-9a-f]{40}$ ]] || exit 2
python3 scripts/release_candidate.py validate "$manifest"
[[ "$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["merge_preview_sha"])' "$manifest")" == "$sha" ]]
python3 scripts/verify-staging-source.py --manifest "$manifest" --bundle "$bundle" \
  --attestation "$attestation" --signature "$signature" --allowed-signers "$allowed_signers" \
  --git-repository . --require-objects
flags=(-i "$key" -o BatchMode=yes -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile="$known" -o ConnectTimeout=30)
ssh "${flags[@]}" "$user@$host" "sudo install -d -o '$user' -g '$user' /opt/aicrm/source-bundles /opt/aicrm/builds; sudo touch /opt/aicrm/staging-build.lock; sudo chown '$user' /opt/aicrm/staging-build.lock"
remote="/opt/aicrm/source-bundles/$sha"
ssh "${flags[@]}" "$user@$host" "mkdir '$remote'"
scp "${flags[@]}" "$bundle" "$user@$host:$remote/candidate.bundle"
# The signed digest binds this exact bundle. Missing remote prerequisites require
# a new full bundle and a new short-lived attestation, never a silent replacement.
scp "${flags[@]}" "$manifest" "$user@$host:$remote/candidate.json"
scp "${flags[@]}" "$attestation" "$user@$host:$remote/attestation.json"
scp "${flags[@]}" "$signature" "$user@$host:$remote/attestation.sig"
scp "${flags[@]}" scripts/release_freshness.py "$user@$host:$remote/release_freshness.py"
scp "${flags[@]}" scripts/verify-staging-source.py "$user@$host:$remote/verify-staging-source.py"
scp "${flags[@]}" deploy/build-release-on-staging-remote.sh "$user@$host:$remote/build.sh"
ssh "${flags[@]}" "$user@$host" "bash '$remote/build.sh' '$sha' '$remote/candidate.bundle' '/opt/aicrm/builds/$sha' '$remote/candidate.json' '$remote/attestation.json' '$remote/attestation.sig' '$remote/verify-staging-source.py'"
# Only small metadata comes back to the caller; archive stays on staging.
scp "${flags[@]}" "$user@$host:/opt/aicrm/builds/$sha/staging-receipt.json" "staging-receipt-$sha.json"
