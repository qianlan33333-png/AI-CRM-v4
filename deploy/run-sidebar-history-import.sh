#!/usr/bin/env bash
set -euo pipefail

snapshot="${1:-}"
snapshot_digest="${2:-}"
release_sha="${3:-}"
if [[ "$snapshot" != /tmp/aicrm-sidebar-history-*.json || ! "$snapshot_digest" =~ ^[0-9a-f]{64}$ || ! "$release_sha" =~ ^[0-9a-f]{40}$ ]]; then
  echo "invalid sidebar history import identity" >&2
  exit 2
fi
if [[ ! -f "$snapshot" || ! -f /etc/aicrm/aicrm.env ]]; then
  echo "sidebar history import inputs unavailable" >&2
  exit 2
fi

current_release="$(readlink -f /opt/aicrm/current)"
if [[ "$current_release" != "/opt/aicrm/releases/${release_sha}" ]]; then
  echo "requested release is not active" >&2
  exit 3
fi
migrator="${current_release}/bin/migrate-sidebar-history"
[[ -x "$migrator" ]]
database_url="$(sed -n 's/^AICRM_DATABASE_URL=//p' /etc/aicrm/aicrm.env | tail -n 1)"
[[ -n "$database_url" ]]

report_key="sidebar-${snapshot_digest:0:24}"
report_root="/var/lib/aicrm/sidebar-history-imports/${report_key}"
backup_root=/var/backups/aicrm
install -d -m 0700 -o aicrm -g aicrm "$report_root" "$backup_root"
backup_file="${backup_root}/sidebar-history-pre-${report_key}.dump"
cleanup() {
  unset database_url
  unlink "$snapshot" 2>/dev/null || true
  if [[ "$0" == /tmp/run-sidebar-history-import-*.sh ]]; then unlink "$0" 2>/dev/null || true; fi
}
trap cleanup EXIT

run_db() { runuser -u aicrm -- env AICRM_DATABASE_URL="$database_url" "$@"; }
run_db "$migrator" --mode dry-run --snapshot "$snapshot" > "$report_root/dry-run.json"
run_db "$migrator" --mode preflight --snapshot "$snapshot" --manifest-sha256 "$snapshot_digest" > "$report_root/preflight.json"
python3 - "$report_root/preflight.json" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as handle:
    payload = json.load(handle)
if payload.get("blocked") != 0 or payload.get("ready") != payload.get("input"):
    print(json.dumps({"sidebar_history_preflight_blocked": payload}, ensure_ascii=False, separators=(",", ":")), file=sys.stderr)
    raise SystemExit(4)
PY

runuser -u aicrm -- env PGDATABASE="$database_url" /usr/bin/pg_dump --format=custom --no-owner --no-privileges --file="$backup_file"
chmod 0600 "$backup_file"

run_db "$migrator" --mode apply --snapshot "$snapshot" --manifest-sha256 "$snapshot_digest" --confirm-apply > "$report_root/apply.json"
run_db "$migrator" --mode reconcile --snapshot "$snapshot" --manifest-sha256 "$snapshot_digest" > "$report_root/reconcile.json"
run_db "$migrator" --mode apply --snapshot "$snapshot" --manifest-sha256 "$snapshot_digest" --confirm-apply > "$report_root/replay.json"
run_db "$migrator" --mode reconcile --snapshot "$snapshot" --manifest-sha256 "$snapshot_digest" > "$report_root/final-reconcile.json"
chmod 0600 "$report_root"/*.json

python3 - "$report_root" "$backup_file" <<'PY'
import json, os, sys
root, backup = sys.argv[1:]
payload = {}
for name in ("dry-run", "preflight", "apply", "reconcile", "replay", "final-reconcile"):
    with open(os.path.join(root, name + ".json"), encoding="utf-8") as handle:
        payload[name] = json.load(handle)
payload["backup_created"] = os.path.isfile(backup) and os.path.getsize(backup) > 0
if payload["apply"]["result"]["quarantined"] != 0 or payload["replay"]["result"]["quarantined"] != 0:
    raise SystemExit("sidebar history import produced unexpected quarantine rows")
print(json.dumps(payload, ensure_ascii=False, separators=(",", ":")))
PY
