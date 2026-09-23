#!/usr/bin/env bash
set -euo pipefail

# Local-first release path. The target must already have the same runtime
# contract as production; this script never copies secrets or a database.
archive="${1:-}"
sha="${2:-}"
target="${DEPLOY_TARGET:?DEPLOY_TARGET is required}"
user="${DEPLOY_USER:-ubuntu}"
key="${DEPLOY_KEY:?DEPLOY_KEY is required}"
known_hosts="${DEPLOY_KNOWN_HOSTS:?DEPLOY_KNOWN_HOSTS is required}"
receipt="${DEPLOY_RECEIPT:-staging-receipts/${sha}.json}"
environment="${DEPLOY_ENVIRONMENT:-staging}"
[[ -f "$archive" && "$archive" == *"${sha}.tar.gz" ]] || { echo "archive/sha mismatch" >&2; exit 2; }
[[ "$sha" =~ ^[0-9a-f]{40}$ ]] || { echo "invalid sha" >&2; exit 2; }
python3 scripts/check-migration-sequence.py --base origin/main
tmp_check="$(mktemp -d)"
trap 'rm -rf -- "$tmp_check"' EXIT
python3 scripts/release_archive_preflight.py "$archive" "$tmp_check"
python3 scripts/check-release-binaries.py "$tmp_check/bin"
chmod 600 "$key"
ssh_flags=(-i "$key" -o BatchMode=yes -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile="$known_hosts" -o ConnectTimeout=30)
remote_archive="/tmp/aicrm-${sha}.tar.gz"
remote_installer="/tmp/install-release-${sha}.sh"
remote_root_wrapper="/tmp/run-release-as-root-${sha}.sh"
ssh "${ssh_flags[@]}" "$user@$target" "sudo install -d -m 0755 /opt/aicrm/incoming && sudo rm -f '$remote_archive' '$remote_installer' '$remote_root_wrapper'"
scp "${ssh_flags[@]}" "$archive" "$user@$target:$remote_archive"
local_digest="$(sha256sum "$archive" | cut -d' ' -f1)"
remote_digest="$(ssh "${ssh_flags[@]}" "$user@$target" "sha256sum '$remote_archive'" | cut -d' ' -f1)"
[[ "$local_digest" == "$remote_digest" ]] || { echo "release archive digest mismatch" >&2; exit 3; }
scp "${ssh_flags[@]}" deploy/install-release.sh "$user@$target:$remote_installer"
scp "${ssh_flags[@]}" deploy/run-release-as-root.sh "$user@$target:$remote_root_wrapper"
ssh "${ssh_flags[@]}" "$user@$target" "sudo install -o root -g root -m 0755 $remote_root_wrapper /opt/aicrm/incoming/run-release-as-root.sh && sudo install -o root -g root -m 0755 $remote_installer /opt/aicrm/incoming/install-release.sh && sudo /opt/aicrm/incoming/run-release-as-root.sh '$remote_archive' '$sha'"
readback="$(ssh "${ssh_flags[@]}" "$user@$target" "readlink -f /opt/aicrm/current; systemctl is-active aicrm.service; systemctl is-active aicrm-effects-worker.service; curl --fail --silent --show-error http://127.0.0.1:8080/readyz")"
tree_sha="$(git rev-parse "${sha}^{tree}" 2>/dev/null || true)"
[[ "$tree_sha" =~ ^[0-9a-f]{40}$ ]] || { echo "release sha is not a local git commit" >&2; exit 4; }
mkdir -p "$(dirname "$receipt")"
python3 - "$receipt" "$sha" "$tree_sha" "$local_digest" "$target" "$readback" "$environment" <<'PY'
import json, sys
from datetime import datetime, timezone
path, sha, tree_sha, digest, target, readback, environment = sys.argv[1:]
ready = json.loads(readback.splitlines()[-1])
if ready.get("release_sha") != sha or ready.get("status") != "ready":
    raise SystemExit("staging readiness did not prove the requested release")
with open(path, "w") as stream:
    json.dump({"schema": 1, "environment": environment, "target": target,
               "release_sha": sha, "tree_sha": tree_sha,
               "package_sha256": digest,
               "observed_at": datetime.now(timezone.utc).isoformat(),
               "readback": readback.splitlines()}, stream, sort_keys=True)
    stream.write("\n")
PY
printf '%s\n' "$environment receipt: $receipt"
