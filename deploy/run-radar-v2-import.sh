#!/usr/bin/env bash
set -euo pipefail

snapshot="${1:-}"
snapshot_digest="${2:-}"
batch_key="${3:-}"
release_sha="${4:-}"
if [[ "$snapshot" != /tmp/aicrm-radar-v2-*.json || ! "$snapshot_digest" =~ ^[0-9a-f]{64}$ || ! "$batch_key" =~ ^radar-v2-[0-9a-f]{24}$ || ! "$release_sha" =~ ^[0-9a-f]{40}$ ]]; then
  echo "invalid Radar import identity" >&2
  exit 2
fi
if [[ ! -f "$snapshot" || ! -f /etc/aicrm/aicrm.env ]]; then
  echo "Radar import inputs unavailable" >&2
  exit 2
fi

current_release="$(readlink -f /opt/aicrm/current)"
if [[ "$current_release" != "/opt/aicrm/releases/${release_sha}" ]]; then
  echo "requested release is not active" >&2
  exit 3
fi
migrator="${current_release}/bin/migrate-radar-v2"
[[ -x "$migrator" ]]
database_url="$(sed -n 's/^AICRM_DATABASE_URL=//p' /etc/aicrm/aicrm.env | tail -n 1)"
[[ -n "$database_url" ]]

report_root="/var/lib/aicrm/radar-imports/${batch_key}"
backup_root=/var/backups/aicrm
install -d -m 0700 -o aicrm -g aicrm "$report_root" "$backup_root"
backup_file="${backup_root}/radar-pre-${batch_key}.dump"
cleanup() {
  unset database_url
  unlink "$snapshot" 2>/dev/null || true
  if [[ "$0" == /tmp/run-radar-v2-import-*.sh ]]; then unlink "$0" 2>/dev/null || true; fi
}
trap cleanup EXIT

run_db() { runuser -u aicrm -- env AICRM_DATABASE_URL="$database_url" "$@"; }
runuser -u aicrm -- env PGDATABASE="$database_url" /usr/bin/pg_dump --format=custom --no-owner --no-privileges --file="$backup_file"
chmod 0600 "$backup_file"
actor_id="$(runuser -u aicrm -- env PGDATABASE="$database_url" /usr/bin/psql -Atqc "SELECT id FROM admin_users WHERE is_active ORDER BY id LIMIT 1")"
[[ "$actor_id" =~ ^[1-9][0-9]*$ ]]

run_db "$migrator" --mode dry-run --snapshot "$snapshot" > "$report_root/dry-run.json"
run_db "$migrator" --mode import --snapshot "$snapshot" --snapshot-sha256 "$snapshot_digest" --batch-key "$batch_key" --actor-id "$actor_id" --confirm > "$report_root/import.json"
run_db "$migrator" --mode reconcile --snapshot "$snapshot" > "$report_root/reconcile.json"
run_db "$migrator" --mode import --snapshot "$snapshot" --snapshot-sha256 "$snapshot_digest" --batch-key "$batch_key" --actor-id "$actor_id" --confirm > "$report_root/replay.json"
chmod 0600 "$report_root"/*.json

python3 - "$report_root" "$backup_file" <<'PY'
import json, os, sys
root, backup = sys.argv[1:]
payload = {}
for name in ("dry-run", "import", "reconcile", "replay"):
    with open(os.path.join(root, name + ".json"), encoding="utf-8") as handle:
        payload[name] = json.load(handle)
payload["backup_created"] = os.path.isfile(backup) and os.path.getsize(backup) > 0
print(json.dumps(payload, ensure_ascii=False, separators=(",", ":")))
PY
