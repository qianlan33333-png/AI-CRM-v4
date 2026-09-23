#!/usr/bin/env bash
# Controlled release entry point for the one-time Access role convergence.
# It is intentionally host-operated (never an Actions deployment default).
set -euo pipefail

mode="${1:-dry-run}"
archive="${2:-}"
release_sha="${3:-}"
access_home="${AICRM_ACCESS_HOME:-/opt/aicrm}"
runtime_env="${AICRM_ACCESS_RUNTIME_ENV:-/etc/aicrm/aicrm.env}"
[[ $# -le 3 ]] || { echo "this one-time Access release does not accept a CI run number" >&2; exit 2; }
[[ "$mode" == dry-run || "$mode" == apply || "$mode" == replay-check ]] || { echo "usage: $0 [dry-run|apply|replay-check] /tmp/aicrm-<sha>.tar.gz <sha>" >&2; exit 2; }
[[ "$release_sha" =~ ^[0-9a-f]{40}$ && "$archive" == "/tmp/aicrm-${release_sha}.tar.gz" && -f "$archive" ]] || { echo "invalid release archive or sha" >&2; exit 2; }
[[ $EUID -eq 0 && -r "$runtime_env" ]] || { echo "root and the protected aicrm runtime environment are required" >&2; exit 3; }

stage="$(mktemp -d "${access_home}/.access-governance-${release_sha}.XXXXXX")"
cleanup_stage() { rm -rf -- "$stage"; }
trap cleanup_stage EXIT
tar -xzf "$archive" -C "$stage"
# Candidate artifacts remain root-owned and aicrm may only traverse/read them.
# This avoids a root-only staging directory while preventing the runtime user
# from swapping a checked artifact before execution.
chown -R root:root "$stage"
chmod -R go-w "$stage"
find "$stage" -type d -exec chmod 0755 {} +
test -f "$stage/release-files.sha256"
(cd "$stage" && sha256sum --strict --check release-files.sha256)
test -x "$stage/bin/migrate-access-role-convergence"
test -x "$stage/bin/check-enterprise-directory"
test -x "$stage/bin/migrate-platform"
test -f "$stage/migrations/0151_access_role_governance.sql"
test -f "$stage/migrations/0152_access_login_grants.sql"

# Let systemd parse its native EnvironmentFile format and drop to aicrm. This
# avoids shell evaluation of production settings and keeps all secret values
# out of argv. --collect removes each temporary unit after it exits.
run_with_runtime_env() {
  local unit="aicrm-access-governance-${release_sha:0:12}-$$-${RANDOM}"
  local -a environment_args=()
  while [[ "${1:-}" == --setenv=* ]]; do
    environment_args+=("$1")
    shift
  done
  systemd-run --quiet --wait --collect --pipe --service-type=exec --unit="$unit" \
    --property=User=aicrm --property="EnvironmentFile=$runtime_env" \
    "${environment_args[@]}" "$@"
}

run_with_runtime_env "$stage/bin/check-enterprise-directory"
if [[ "$mode" == dry-run ]]; then
  run_with_runtime_env "$stage/bin/migrate-access-role-convergence" --mode=dry-run
	echo "{\"mode\":\"${mode}\",\"release_ready\":true}"
  exit 0
fi
if [[ "$mode" == replay-check ]]; then
  run_with_runtime_env "$stage/bin/migrate-access-role-convergence" --mode=replay-check
  echo '{"mode":"replay-check","release_ready":true}'
  exit 0
fi

release_lock="$access_home/install-release.lock"
exec 9>"$release_lock"
flock -n 9 || { echo "another release holds the host lock" >&2; exit 15; }
[[ "$(readlink -f "/proc/$$/fd/9")" == "$(readlink -f "$release_lock")" ]] || { echo "release lock fd is not the expected inode" >&2; exit 15; }
if systemctl is-active --quiet aicrm-migrate.service; then echo "aicrm-migrate.service is running" >&2; exit 15; fi

units=(aicrm.service aicrm-effects-worker.service aicrm-wecom-worker.timer aicrm-wecom-worker.service aicrm-customer-sync-daily.timer aicrm-customer-sync-daily.service)
declare -A initially_active=()
for unit in "${units[@]}"; do
  if systemctl is-active --quiet "$unit"; then initially_active["$unit"]=1; else initially_active["$unit"]=0; fi
done
mutation_started=false
handle_failure() {
  # Once apply starts, its caller cannot distinguish a rolled-back error from
  # a committed transaction followed by process loss. Preserve the write
  # barrier until a human uses replay-check; never revive an old writer here.
  if [[ "$mutation_started" == true ]]; then
    systemctl stop aicrm-wecom-worker.timer aicrm-customer-sync-daily.timer || true
    systemctl stop aicrm-wecom-worker.service aicrm-customer-sync-daily.service || true
    systemctl stop aicrm.service aicrm-effects-worker.service || true
    echo "access governance release failed after convergence; Access writers remain stopped" >&2
    return
  fi
  for unit in "${units[@]}"; do
    [[ "${initially_active[$unit]}" == 1 ]] && systemctl start "$unit" || true
  done
}
trap handle_failure ERR
trap 'handle_failure; exit 1' INT TERM

# Stop timers before their oneshots, then stop the API and the River runtime.
# Effects worker is included because it runs staff-directory refresh jobs that
# call Access ProjectWeComStaffWithin and write admin_users.
systemctl stop aicrm-wecom-worker.timer aicrm-customer-sync-daily.timer
systemctl stop aicrm-wecom-worker.service aicrm-customer-sync-daily.service
systemctl stop aicrm.service aicrm-effects-worker.service

mutation_started=true
run_with_runtime_env --setenv=AICRM_ACCESS_CONVERGENCE_APPROVED=1 "$stage/bin/migrate-access-role-convergence" --mode=apply

# The installer inherits fd 9. Its Access-specific rollback branch deliberately
# leaves writers stopped after schema transition begins; do not revive the old
# binary against the new role constraints.
AICRM_RELEASE_LOCK_HELD=1 AICRM_RELEASE_LOCK_FD=9 AICRM_ACCESS_GOVERNANCE_RELEASE=1 \
  /usr/bin/env bash "$stage/deploy/install-release.sh" "$archive" "$release_sha"

# Verify the durable completion marker before restoring any writer. A failed
# replay-check takes the ERR path above and stops the current candidate too.
run_with_runtime_env "$stage/bin/migrate-access-role-convergence" --mode=replay-check

# Installer defaults are broader than this maintenance window. Restore the
# prior active set; units that were inactive stay stopped (enabled state is not
# changed here). Excel and HXC are not Access writers and were never stopped.
for unit in "${units[@]}"; do
  if [[ "${initially_active[$unit]}" == 1 ]]; then systemctl start "$unit"; else systemctl stop "$unit"; fi
done
echo '{"mode":"apply","status":"active"}'
