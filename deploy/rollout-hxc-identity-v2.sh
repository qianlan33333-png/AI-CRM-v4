#!/usr/bin/env bash
set -euo pipefail

release_sha="${1:-}"
rollout_mode="${2:-full}"
[[ "$rollout_mode" == full || "$rollout_mode" == ensure ]] || { echo "invalid HXC rollout mode" >&2; exit 2; }
runtime_env=/etc/aicrm/aicrm.env
current_link=/opt/aicrm/current
rollout_lock=/opt/aicrm/hxc-identity-v2-rollout.lock
# The durable job keys remain hxc-dashboard-v2 for compatibility. The
# projection published by the installed HXC owner is currently v3.
hxc_projection_rule_version=hxc-current-v3

if [[ ${EUID} -ne 0 || ! "$release_sha" =~ ^[0-9a-f]{40}$ ]]; then
  echo "invalid HXC rollout invocation" >&2
  exit 2
fi
current_release="$(readlink -f "$current_link")"
if [[ "$current_release" != "/opt/aicrm/releases/${release_sha}" || ! -f "$runtime_env" ]]; then
  echo "requested HXC release is not active" >&2
  exit 3
fi
for required in \
  "$current_release/bin/aicrm" \
  "$current_release/migrations/0063_identity_hxc_source_observations.sql" \
  "$current_release/migrations/0064_hxc_dashboard_identity_v2.sql" \
  "$current_release/migrations/0084_hxc_shared_facts.sql"; do
  [[ -f "$required" ]] || { echo "HXC rollout artifact incomplete" >&2; exit 3; }
done
grep -qx 'AICRM_HXC_SYNC_ENABLED=true' "$runtime_env"
grep -qx 'AICRM_HXC_UNIONID_VERIFIED=true' "$runtime_env"
grep -Eq '^AICRM_HXC_UNIONID_SCOPE=wechat-open-platform:[^[:space:]]+$' "$runtime_env"
grep -Eq '^AICRM_IDENTITY_OBSERVATION_VAULT_KEY=[A-Za-z0-9+/]{43}=$' "$runtime_env"

database_url="$(sed -n 's/^AICRM_DATABASE_URL=//p' "$runtime_env")"
if [[ -z "$database_url" || "$database_url" == *$'\n'* ]]; then
  echo "HXC rollout database configuration is invalid" >&2
  exit 3
fi
psql_bin="$(command -v psql)"
[[ -x "$psql_bin" ]]

exec 9>"$rollout_lock"
flock 9

run_sql() {
  runuser -u aicrm -- "$psql_bin" -X -v ON_ERROR_STOP=1 -Atq --dbname="$database_url" -c "$1"
}

set_write_mode() {
  local enabled="$1" next_env
  next_env="$(mktemp /etc/aicrm/.aicrm.env.hxc-mode.XXXXXX)"
  while IFS= read -r line || [[ -n "$line" ]]; do
    case "$line" in
      AICRM_HXC_IDENTITY_WRITE_ENABLED=*) ;;
      *) printf '%s\n' "$line" >> "$next_env" ;;
    esac
  done < "$runtime_env"
  printf 'AICRM_HXC_IDENTITY_WRITE_ENABLED=%s\n' "$enabled" >> "$next_env"
  chmod 0600 "$next_env"
  chown --reference="$runtime_env" "$next_env"
  mv -f -- "$next_env" "$runtime_env"
}

restart_runtime() {
  systemctl restart aicrm.service
  systemctl restart aicrm-effects-worker.service
  local ready=false response
  for _ in $(seq 1 30); do
    response="$(curl --fail --silent --show-error http://127.0.0.1:8080/readyz 2>/dev/null || true)"
    if [[ "$response" == *"\"release_sha\":\"${release_sha}\""* && "$response" == *'"status":"ready"'* ]] && systemctl is-active --quiet aicrm-effects-worker.service; then
      ready=true
      break
    fi
    sleep 1
  done
  [[ "$ready" == true ]]
}

rollout_complete=false
disable_on_failure() {
  unset database_url
  if [[ "$rollout_complete" != true ]]; then
    set_write_mode false || true
    systemctl restart aicrm.service >/dev/null 2>&1 || true
    systemctl restart aicrm-effects-worker.service >/dev/null 2>&1 || true
    echo "HXC identity rollout stopped with writes disabled" >&2
  fi
}
trap disable_on_failure EXIT

wait_for_run() {
  local mode="$1" run_key="$2" status="" record
  for _ in $(seq 1 360); do
    record="$(run_sql "SELECT status||'|'||source_count||'|'||processed_count||'|'||identity_replay_verified_count||'|'||COALESCE(projection_id,0)||'|'||COALESCE(error_code,'') FROM hxc_dashboard_refresh_runs WHERE run_key='${run_key}'")"
    if [[ -n "$record" ]]; then
      IFS='|' read -r status run_source_count run_processed_count run_replay_count run_projection_id run_error_code <<< "$record"
      if [[ "$status" == succeeded ]]; then
        run_key_for_projection="$run_key"
        return 0
      fi
      if [[ "$status" == failed ]]; then
        echo "HXC ${mode} failed with safe code ${run_error_code}" >&2
        return 1
      fi
    fi
    sleep 5
  done
  echo "HXC ${mode} timed out" >&2
  return 1
}

verify_run_projection() {
  local counts
  counts="$(run_sql "SELECT v.total_count||'|'||v.matched_count||'|'||v.unmatched_count||'|'||v.conflict_count||'|'||v.matched_by_unionid_count||'|'||v.matched_by_phone_count||'|'||v.matched_by_both_count||'|'||v.pending_observation_count||'|'||v.invalid_identity_count FROM hxc_dashboard_versions v JOIN hxc_dashboard_refresh_runs r ON r.projection_id=v.id WHERE r.run_key='${run_key_for_projection}' AND r.status='succeeded' AND r.source_count=${run_source_count} AND r.projection_id=${run_projection_id} AND v.rule_version='${hxc_projection_rule_version}' AND v.status IN ('published','superseded')")"
  [[ -n "$counts" ]] || return 1
  IFS='|' read -r total_count matched_count unmatched_count conflict_count matched_union matched_phone matched_both pending_count invalid_count <<< "$counts"
  ((total_count == matched_count + unmatched_count + conflict_count)) || return 1
  ((matched_count == matched_union + matched_phone + matched_both)) || return 1
  ((unmatched_count == pending_count + invalid_count)) || return 1
  ((total_count == run_source_count)) || return 1
}

verify_current_published_projection() {
  local counts
  counts="$(run_sql "SELECT total_count||'|'||matched_count||'|'||unmatched_count||'|'||conflict_count||'|'||matched_by_unionid_count||'|'||matched_by_phone_count||'|'||matched_by_both_count||'|'||pending_observation_count||'|'||invalid_identity_count FROM hxc_dashboard_versions WHERE rule_version='${hxc_projection_rule_version}' AND status='published'")"
  [[ -n "$counts" ]] || return 1
  IFS='|' read -r total_count matched_count unmatched_count conflict_count matched_union matched_phone matched_both pending_count invalid_count <<< "$counts"
  ((total_count == matched_count + unmatched_count + conflict_count)) || return 1
  ((matched_count == matched_union + matched_phone + matched_both)) || return 1
  ((unmatched_count == pending_count + invalid_count)) || return 1
}

# Routine code releases need effective-state readback, not another inspect/apply
# cycle. A missing activation or a new rule version still takes the full path.
verify_existing_rollout() {
  local response
  grep -qx 'AICRM_HXC_IDENTITY_WRITE_ENABLED=true' "$runtime_env" || return 1
  verify_current_published_projection || return 1
  systemctl is-active --quiet aicrm-effects-worker.service || return 1
  systemctl is-active --quiet aicrm-hxc-dashboard-refresh.timer || return 1
  systemctl is-enabled --quiet aicrm-hxc-dashboard-refresh.timer || return 1
  response="$(curl --fail --silent --show-error --max-time 10 http://127.0.0.1:8080/readyz 2>/dev/null)" || return 1
  [[ "$response" == *"\"release_sha\":\"${release_sha}\""* && "$response" == *'"status":"ready"'* ]]
}

trigger_run() {
  local mode="$1"
  systemctl reset-failed aicrm-hxc-dashboard-rollout.service >/dev/null 2>&1 || true
  systemctl start aicrm-hxc-dashboard-rollout.service
  wait_for_run "$mode" "initial:hxc-dashboard-v2:${mode}:${release_sha}"
}

for migration in 0063 0064; do
  [[ "$(run_sql "SELECT count(*) FROM platform_schema_migrations WHERE version='${migration}'")" == 1 ]]
done
[[ "$(run_sql "SELECT count(*) FROM (SELECT kind,scope_key,normalized_value_digest,normalized_value FROM customer_identities WHERE status='active' GROUP BY kind,scope_key,normalized_value_digest,normalized_value HAVING count(DISTINCT customer_id)>1) duplicate_keys")" == 0 ]]

if [[ "$rollout_mode" == ensure ]] && verify_existing_rollout; then
  rollout_complete=true
  unset database_url
  trap - EXIT
  echo "HXC current rule already active; effective state verified without replaying rollout"
  exit 0
fi

set_write_mode false
restart_runtime
trigger_run inspect
[[ "$run_source_count" == "$run_processed_count" && "$run_replay_count" == 0 && "$run_projection_id" =~ ^[1-9][0-9]*$ ]]
verify_run_projection

customer_count_before="$(run_sql 'SELECT count(*) FROM customers')"
subjects_before="$(run_sql 'SELECT count(*) FROM identity_source_subjects')"
observations_before="$(run_sql 'SELECT count(*) FROM identity_source_observations')"
receipts_before="$(run_sql 'SELECT count(*) FROM identity_source_resolution_receipts')"
conflicts_before="$(run_sql 'SELECT count(*) FROM identity_source_conflicts')"

set_write_mode true
restart_runtime
trigger_run apply
[[ "$run_source_count" == "$run_processed_count" && "$run_replay_count" == "$run_source_count" && "$run_projection_id" =~ ^[1-9][0-9]*$ ]]
apply_replay_count="$run_replay_count"
[[ "$(run_sql 'SELECT count(*) FROM customers')" == "$customer_count_before" ]]
verify_run_projection
[[ "$(run_sql "SELECT count(*) FROM identity_source_subjects WHERE source_system='hxc' AND status<>'retired'")" == "$run_source_count" ]]

systemctl enable --now aicrm-hxc-dashboard-refresh.timer
systemctl is-active --quiet aicrm-effects-worker.service
systemctl is-active --quiet aicrm-hxc-dashboard-refresh.timer
systemctl is-enabled --quiet aicrm-hxc-dashboard-refresh.timer

# Exercise the exact timer service path after persistence is enabled. The
# enabled timer remains responsible for subsequent natural refreshes.
scheduled_key="scheduled:$(TZ=Asia/Shanghai date +%Y-%m-%dT%H):hxc-dashboard-v2:apply"
systemctl start aicrm-hxc-dashboard-refresh.service
wait_for_run apply "$scheduled_key"
[[ "$run_source_count" == "$run_processed_count" && "$run_replay_count" == "$run_source_count" && "$run_projection_id" =~ ^[1-9][0-9]*$ ]]
[[ "$(run_sql 'SELECT count(*) FROM customers')" == "$customer_count_before" ]]
verify_run_projection
[[ "$(run_sql "SELECT count(*) FROM identity_source_subjects WHERE source_system='hxc' AND status<>'retired'")" == "$run_source_count" ]]
verify_current_published_projection

subjects_after="$(run_sql 'SELECT count(*) FROM identity_source_subjects')"
observations_after="$(run_sql 'SELECT count(*) FROM identity_source_observations')"
receipts_after="$(run_sql 'SELECT count(*) FROM identity_source_resolution_receipts')"
conflicts_after="$(run_sql 'SELECT count(*) FROM identity_source_conflicts')"
printf 'HXC identity v2 active: total=%s matched=%s unmatched=%s conflict=%s unionid=%s phone=%s both=%s pending=%s invalid=%s replay_verified=%s new_customers=0 subjects_delta=%s observations_delta=%s receipts_delta=%s conflicts_delta=%s\n' \
  "$total_count" "$matched_count" "$unmatched_count" "$conflict_count" "$matched_union" "$matched_phone" "$matched_both" "$pending_count" "$invalid_count" "$apply_replay_count" \
  "$((subjects_after-subjects_before))" "$((observations_after-observations_before))" "$((receipts_after-receipts_before))" "$((conflicts_after-conflicts_before))"

rollout_complete=true
unset database_url
trap - EXIT
