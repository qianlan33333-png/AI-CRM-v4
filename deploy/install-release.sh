#!/usr/bin/env bash
set -euo pipefail

# Release verification treats the extracted tree as immutable package content.
# Python hooks run from that tree during installation; suppress bytecode writes
# so __pycache__ files cannot appear and invalidate the sealed package before
# the success observer records the release.
export PYTHONDONTWRITEBYTECODE=1

archive="${1:-}"
release_sha="${2:-}"
release_run_number="${3:-}"
release_root=/opt/aicrm/releases
current_link=/opt/aicrm/current
release_lock=/opt/aicrm/install-release.lock
last_successful_run_file=/opt/aicrm/last-successful-run-number

if [[ ! "$release_sha" =~ ^[0-9a-f]{40}$ ]]; then
  echo "invalid release sha" >&2
  exit 2
fi
if [[ "$archive" != "/tmp/aicrm-${release_sha}.tar.gz" || ! -f "$archive" ]]; then
  echo "invalid release archive" >&2
  exit 2
fi
if [[ -n "$release_run_number" && ! "$release_run_number" =~ ^[1-9][0-9]*$ ]]; then
  echo "invalid release run number" >&2
  exit 2
fi
if ! id aicrm >/dev/null 2>&1 || [[ ! -f /etc/aicrm/aicrm.env ]]; then
  echo "aicrm runtime is not provisioned" >&2
  exit 3
fi
# The root-owned helper and current symlink must not have an app-writable
# ancestor. This changes only release control directories, never runtime data.
for control_dir in /opt/aicrm "$release_root"; do
  if [[ -L "$control_dir" || ( -e "$control_dir" && ! -d "$control_dir" ) ]]; then
    echo "unsafe release control directory" >&2
    exit 3
  fi
  install -d -m 0755 "$control_dir"
  chown root:root "$control_dir"
  chmod 0755 "$control_dir"
done

# Share the exact host lock with release wrappers and runtime configuration.
# Never kill another holder: an open lock fd does not prove an obsolete deploy.
# Busy exits retain the input archive so the caller can retry after resolution.
if [[ -L "$release_lock" || ( -e "$release_lock" && ! -f "$release_lock" ) ]]; then
  echo "unsafe release lock" >&2
  exit 15
fi
inherited_release_lock=false
if [[ "${AICRM_RELEASE_LOCK_HELD:-}" == 1 ]]; then
  if [[ "${AICRM_RELEASE_LOCK_FD:-}" != 9 ]]; then
    echo "invalid inherited release lock" >&2
    exit 15
  fi
  inherited_release_lock=true
else
exec 9>"$release_lock"
fi
if ! python3 - "$release_lock" <<'VERIFY_RELEASE_LOCK'
import os
import stat
import sys
try:
    expected = os.stat(sys.argv[1], follow_symlinks=False)
    inherited = os.fstat(9)
    valid = stat.S_ISREG(expected.st_mode) and (expected.st_dev, expected.st_ino) == (inherited.st_dev, inherited.st_ino)
except OSError:
    valid = False
raise SystemExit(0 if valid else 1)
VERIFY_RELEASE_LOCK
then
  echo "invalid release lock descriptor" >&2
  exit 15
fi
if ! flock -w 15 9; then
  echo "release lock busy; retry after the current operation completes" >&2
  exit 75
fi
if [[ -e /etc/aicrm/.ops-runtime-recovery.json || -L /etc/aicrm/.ops-runtime-recovery.json ]]; then
  echo "runtime configuration recovery_required before release" >&2
  exit 75
fi

release_dir="${release_root}/${release_sha}"

install -d -m 0755 "$release_root"
if [[ -e "$release_dir" ]]; then
  if [[ ! -d "$release_dir" || -L "$release_dir" ]]; then
    echo "existing release path is not a directory" >&2
    exit 4
  fi
  echo "resuming validated release ${release_sha}"
else
  staging_dir="$(mktemp -d "${release_root}/.${release_sha}.staging.XXXXXX")"
  cleanup_staging() {
    if [[ -n "${staging_dir:-}" && -d "$staging_dir" ]]; then
      rm -rf -- "$staging_dir"
    fi
  }
  trap cleanup_staging EXIT
  tar -xzf "$archive" -C "$staging_dir"
  test -f "$staging_dir/release-files.sha256"
  (cd "$staging_dir" && sha256sum --strict --check release-files.sha256)
  mv -T "$staging_dir" "$release_dir"
  staging_dir=""
  trap - EXIT
fi
test -x "$release_dir/bin/aicrm"
test -x "$release_dir/bin/aicrm-operation-cycle-runner"
test -x "$release_dir/bin/aicrm-operation-cycle-result"
test -x "$release_dir/bin/wecom-archive-sdk-runner"
test -x "$release_dir/bin/migrate-platform"
test -x "$release_dir/bin/migrate-access-role-convergence"
test -x "$release_dir/bin/check-enterprise-directory"
test -x "$release_dir/bin/migrate-river"
test -x "$release_dir/bin/migrate-phone-identities"
test -x "$release_dir/bin/migrate-identity-phone-vault"
test -x "$release_dir/bin/migrate-survey-v2"
test -x "$release_dir/bin/migrate-commerce-history"
test -x "$release_dir/bin/migrate-message-archive"
test -x "$release_dir/bin/migrate-order-attribution"
test -x "$release_dir/bin/migrate-order-distribution-qualification"
test -x "$release_dir/bin/migrate-automation-operations"
test -x "$release_dir/bin/migrate-v2-config-definitions"
test -x "$release_dir/bin/migrate-v2-runtime-config-releases"
test -x "$release_dir/bin/migrate-v2-commerce-external-push-history"
test -x "$release_dir/bin/migrate-open-platform"
test -x "$release_dir/bin/migrate-media-legacy-materials"
test -x "$release_dir/bin/migrate-hxc-daily-lessons"
test -x "$release_dir/bin/migrate-channel-history"
test -x "$release_dir/bin/migrate-v2-customer-tag-history"
test -x "$release_dir/bin/migrate-radar-v2"
test -x "$release_dir/bin/migrate-sidebar-history"
test -x "$release_dir/bin/migrate-owner-handoff-history"
test -x "$release_dir/bin/bootstrap-automation-operations"
if ! command -v file >/dev/null 2>&1; then
  echo "release architecture check requires file(1)" >&2
  exit 6
fi
if [[ "$(uname -s)" != Linux || "$(uname -m)" != x86_64 ]]; then
  echo "release host must be Linux x86_64" >&2
  exit 6
fi
for binary in "$release_dir"/bin/*; do
  [[ -f "$binary" && -x "$binary" ]] || continue
  file_description="$(file -b "$binary")"
  [[ "$file_description" == ELF\ 64-bit*\ x86-64* ]] || {
    echo "release binary is not Linux amd64 ELF: $binary ($file_description)" >&2
    exit 6
  }
done
test -f "$release_dir/migrations/0005_external_effects.sql"
test -f "$release_dir/migrations/0006_wecom_callback_channel_acquisition.sql"
test -f "$release_dir/migrations/0007_media.sql"
test -f "$release_dir/migrations/0008_tag_catalog.sql"
test -f "$release_dir/migrations/0009_customer_activation.sql"
test -f "$release_dir/migrations/0010_product.sql"
test -f "$release_dir/migrations/0011_coupon_rules.sql"
test -f "$release_dir/migrations/0012_group_ops.sql"
test -f "$release_dir/migrations/0013_automation_agents.sql"
test -f "$release_dir/migrations/0014_operation_cycles.sql"
test -f "$release_dir/migrations/0016_media_content_packages.sql"
test -f "$release_dir/migrations/0017_group_ops_history.sql"
test -f "$release_dir/migrations/0018_survey.sql"
test -f "$release_dir/migrations/0019_tag_catalog_sync_projection.sql"
test -f "$release_dir/migrations/0022_customer_profile_sections.sql"
test -f "$release_dir/migrations/0020_order.sql"
test -f "$release_dir/migrations/0021_payment.sql"
test -f "$release_dir/migrations/0024_order_product_version.sql"
test -f "$release_dir/migrations/0025_payment_reconciliation.sql"
test -f "$release_dir/migrations/0026_identity_history_receipts.sql"
test -f "$release_dir/migrations/0027_admin_access_login_compat.sql"
test -f "$release_dir/migrations/0028_hxc_dashboard.sql"
test -f "$release_dir/migrations/0029_channel_center.sql"
test -f "$release_dir/migrations/0030_config_definition_import.sql"
test -f "$release_dir/migrations/0031_channel_history_import.sql"
test -f "$release_dir/migrations/0032_channel_acquisition_assets.sql"
test -f "$release_dir/migrations/0033_wecom_welcome_grants.sql"
test -f "$release_dir/migrations/0034_channel_entrant_actions.sql"
test -f "$release_dir/migrations/0050_radar_core.sql"
test -f "$release_dir/migrations/0051_radar_sessions_events.sql"
test -f "$release_dir/migrations/0052_radar_legacy_import.sql"
test -f "$release_dir/web/dist/admin/radar.html"
test -f "$release_dir/web/dist/admin/radarDetail.html"
test -f "$release_dir/web/dist/admin/radarForm.html"
test -f "$release_dir/migrations/0035_channel_acquisition_links.sql"
test -f "$release_dir/migrations/0036_ai_assistant_review.sql"
test -f "$release_dir/migrations/0037_outbound_private_messages.sql"
test -f "$release_dir/migrations/0038_survey_oauth_phone_vault.sql"
test -f "$release_dir/migrations/0047_automation_operations_migration.sql"
test -f "$release_dir/migrations/0048_segment_audience_schedule_state.sql"
test -f "$release_dir/migrations/0049_order_history_attribution.sql"
test -f "$release_dir/migrations/0053_segment_audience_member_event_fact_kinds.sql"
test -f "$release_dir/migrations/0083_segment_audience_refresh_modes.sql"
test -f "$release_dir/migrations/0085_segment_audience_refresh_kind.sql"
test -f "$release_dir/migrations/0086_wecom_profile_primary_owner.sql"
test -f "$release_dir/migrations/0087_automation_manual_ai_review.sql"
test -f "$release_dir/migrations/0089_outbound_message_content_snapshots.sql"
test -f "$release_dir/migrations/0061_product_public_purchase.sql"
test -f "$release_dir/migrations/0063_identity_hxc_source_observations.sql"
test -f "$release_dir/migrations/0064_hxc_dashboard_identity_v2.sql"
test -f "$release_dir/migrations/0066_channel_welcome_intents.sql"
test -f "$release_dir/migrations/0150_channel_welcome_message_snapshots.sql"
test -f "$release_dir/migrations/0151_access_role_governance.sql"
test -f "$release_dir/migrations/0152_access_login_grants.sql"
test -f "$release_dir/migrations/0153_wecom_customer_detail_projection.sql"
test -f "$release_dir/migrations/0155_group_ops_webhook_dynamic_executions.sql"
test -f "$release_dir/migrations/0156_distribution_profit_sharing_payment.sql"
test -f "$release_dir/migrations/0157_distribution_core.sql"
test -f "$release_dir/migrations/0158_order_distribution_qualification_evidence.sql"
test -f "$release_dir/migrations/0159_distribution_admin_receipts.sql"
test -f "$release_dir/migrations/0160_external_effect_system_control_actor.sql"
test -f "$release_dir/migrations/0161_payment_paid_confirmation_time.sql"
test -f "$release_dir/migrations/0164_channel_welcome_provider_rejections.sql"
test -f "$release_dir/migrations/0162_payment_h5_distribution_return_path.sql"
test -f "$release_dir/migrations/0163_payment_profit_sharing_receiver_recovery.sql"
test -f "$release_dir/migrations/0165_payment_profit_sharing_receiver_failure_class.sql"
test -f "$release_dir/migrations/0166_payment_profit_sharing_instruction_failure_class.sql"
test -f "$release_dir/migrations/0167_distribution_settlement_not_paid_exception.sql"
test -f "$release_dir/migrations/0170_wecom_contact_description_effect.sql"
test -f "$release_dir/migrations/0171_wecom_contact_description_source_coverage.sql"
test -f "$release_dir/migrations/0172_order_checkout_post_purchase_action.sql"
test -f "$release_dir/migrations/0173_product_paid_purchase_action_target_snapshot.sql"
test -f "$release_dir/migrations/0174_product_external_push_test_delivery_id.sql"
test -f "$release_dir/migrations/0175_customer_minimum_directory_projection.sql"
test -f "$release_dir/migrations/0176_survey_single_submission_claims.sql"
test -f "$release_dir/migrations/0177_survey_operation_legacy_parity.sql"
test -f "$release_dir/migrations/0181_hxc_dashboard_views.sql"
test -f "$release_dir/migrations/0185_referral_core.sql"
test -f "$release_dir/migrations/0186_adminops_inspections.sql"
test -f "$release_dir/migrations/0187_adminops_notification_effect.sql"
test -f "$release_dir/migrations/0188_adminops_retention.sql"
test -f "$release_dir/migrations/0189_owner_process_retention.sql"
test -f "$release_dir/migrations/0190_adminops_cpu_profiles.sql"
test -f "$release_dir/migrations/0191_payment_h5_referral_return_path.sql"
test -f "$release_dir/migrations/0192_adminops_diagnostic_event_identity.sql"
test -f "$release_dir/migrations/0193_adminops_governance_outcomes.sql"
test -f "$release_dir/migrations/0067_survey_completion_snapshots.sql"
test -f "$release_dir/migrations/0084_hxc_shared_facts.sql"
test -f "$release_dir/migrations/0090_survey_oauth_state_redirect.sql"
test -f "$release_dir/migrations/0091_survey_assessment_business_keys.sql"
test -f "$release_dir/migrations/0092_customer_owner_handoff.sql"
test -f "$release_dir/migrations/0093_customer_tag_commands.sql"
test -f "$release_dir/migrations/0094_runtime_config_releases.sql"
test -f "$release_dir/migrations/0095_product_external_push.sql"
test -f "$release_dir/migrations/0146_product_external_push_field_mapping.sql"
test -f "$release_dir/migrations/0147_outbound_commerce_mapping_mode.sql"
test -f "$release_dir/migrations/0096_open_platform.sql"
test -f "$release_dir/migrations/0097_segment_audience_mutation_actor.sql"
test -f "$release_dir/migrations/0098_message_archive_historical_projection.sql"
test -f "$release_dir/migrations/0099_survey_historical_external_projection.sql"
test -f "$release_dir/migrations/0100_ai_assistant_machine_actor.sql"
test -f "$release_dir/migrations/0124_operation_excel_batch_lifecycle.sql"
test -f "$release_dir/migrations/0068_payment_session_beneficiary_selection.sql"
test -f "$release_dir/migrations/0069_coupon_claim_redemption_lifecycle.sql"
test -f "$release_dir/migrations/0070_service_period_entitlement_fulfillment.sql"
test -f "$release_dir/migrations/0071_message_archive_core.sql"
test -f "$release_dir/migrations/0072_message_archive_migration_receipts.sql"
test -f "$release_dir/migrations/0073_survey_completion_test_push_snapshots.sql"
test -f "$release_dir/migrations/0074_survey_external_operation_execution_facts.sql"
test -f "$release_dir/migrations/0075_external_effects_survey_completion_kind.sql"
test -f "$release_dir/migrations/0076_order_checkout_snapshots.sql"
test -f "$release_dir/migrations/0077_coupon_public_slug.sql"
test -f "$release_dir/migrations/0078_group_ops_provider_tasks.sql"
test -f "$release_dir/migrations/0079_service_period_member_grid.sql"
test -f "$release_dir/migrations/0080_media_legacy_material_mappings.sql"
test -f "$release_dir/migrations/0081_group_ops_webhook_unconfigured_reference.sql"
test -f "$release_dir/migrations/0082_group_ops_history_import.sql"
test -f "$release_dir/migrations/0088_order_service_entitlement_alliance.sql"
test -f "$release_dir/deploy/aicrm-hxc-dashboard-rollout.service"
test -f "$release_dir/deploy/rollout-hxc-identity-v2.sh"
test -f "$release_dir/migrations/0015_config_adminops.sql"
test -f "$release_dir/web/dist/asset-manifest.json"
# The new shell is only release-complete when every private admin document and
# its sidebar workbench are present alongside the manifest-verified hashed
# closure.  Each document is still served behind the existing admin session;
# this check does not create an additional public donor surface.
for new_shell_page in \
  agentEdit \
  agents \
  ai \
  aiDetail \
  apidocs \
  attach \
  audienceEdit \
  automation \
  campaigns \
  channelForm \
  channels \
  config \
  configDetail \
  couponData \
  couponForm \
  coupons \
  customerDetail \
  customers \
  cycles \
  cyclesDetail \
  funnel \
  groupops \
  groupopsDetail \
  images \
  index \
  mpLib \
  orderDetail \
  orders \
  ownerMig \
  productForm \
  products \
  questionnaireDetail \
  questionnaireOps \
  questionnaires \
  radar \
  radarDetail \
  radarForm \
  spProductData \
  spProductForm \
  spProducts \
  tags \
  wecom-tags; do
  test -f "$release_dir/web/dist/admin/${new_shell_page}.html"
done
test -f "$release_dir/web/dist/sidebar/index.html"
for ai_assistant_asset in \
  list.html detail.html \
  send_content_readonly_detail.css send_content_readonly_detail.js \
  cloud_plan_review.js; do
  test -f "$release_dir/web/dist/aiassistant/$ai_assistant_asset"
done
for standard_component_asset in \
  operation_member_picker.js \
  group_chat_picker.css \
  group_chat_picker.js \
  material_picker.css \
  material_picker.js \
  send_content_composer.css \
  send_content_composer.js \
  wecom_tag_picker.css \
  wecom_tag_picker.js \
  coupon_form.html \
  coupon_form_runtime.js \
  coupon_styles.html \
  channel_code_form.html \
  channel_admission_pages.js \
  standard_components_host.js; do
  test -f "$release_dir/web/dist/assets/standard-components/$standard_component_asset" || {
    echo "release is missing standard component: $standard_component_asset" >&2
    exit 4
  }
done
test -f "$release_dir/release-files.sha256"
test -f "$release_dir/deploy/record-release-success.py"
test -f "$release_dir/deploy/publish-governance-releases.py"
# Privileged deployment hooks must never execute application-writable code.
# Seal the package before its final checksum verification, including resumes.
chown -R root:root "$release_dir"
chmod -R go-w "$release_dir"
(cd "$release_dir" && sha256sum --strict --check release-files.sha256)
printf 'AICRM_RELEASE_SHA=%s\n' "$release_sha" > "$release_dir/release.env"
# The isolated Excel service account must traverse the immutable release root.
chmod 0755 "$release_dir"

cleanup_release_artifacts() {
  rm -f -- "$archive"
  if [[ "$0" == "/tmp/install-release-${release_sha}.sh" ]]; then
    rm -f -- "$0"
  fi
}
trap cleanup_release_artifacts EXIT

run_is_not_newer() {
  local candidate="$1"
  local deployed="$2"
  [[ ${#candidate} -lt ${#deployed} ]] || \
    ([[ ${#candidate} -eq ${#deployed} ]] && [[ "$candidate" < "$deployed" || "$candidate" == "$deployed" ]])
}

if [[ -n "$release_run_number" && -e "$last_successful_run_file" ]]; then
  last_successful_run_number="$(<"$last_successful_run_file")"
  if [[ ! "$last_successful_run_number" =~ ^[1-9][0-9]*$ ]]; then
    echo "invalid last successful release run number" >&2
    exit 11
  fi
  if run_is_not_newer "$release_run_number" "$last_successful_run_number"; then
    echo "skipping stale release ${release_sha}: run ${release_run_number} is not newer than deployed run ${last_successful_run_number}"
    exit 0
  fi
elif [[ -z "$release_run_number" ]]; then
  echo "installing release ${release_sha} without a CI run number; serialized but not stale-run guarded" >&2
fi

# Revoke an interrupted success claim before changing env/current/services.
# This only restores root publication metadata; it never starts the failed SHA.
if ! python3 "$release_dir/deploy/record-release-success.py" --sha "$release_sha" --revoke-incomplete; then
  echo "release success metadata recovery_required" >&2
  exit 18
fi

if ! grep -Eq '^AICRM_SURVEY_DATA_KEY=.{43}$' /etc/aicrm/aicrm.env; then
  survey_data_key="$(openssl rand -base64 32 | tr -d '\n=')"
  if grep -q '^AICRM_SURVEY_DATA_KEY=' /etc/aicrm/aicrm.env; then
    sed -i "s|^AICRM_SURVEY_DATA_KEY=.*$|AICRM_SURVEY_DATA_KEY=${survey_data_key}|" /etc/aicrm/aicrm.env
  else
    printf '\nAICRM_SURVEY_DATA_KEY=%s\n' "$survey_data_key" >> /etc/aicrm/aicrm.env
  fi
  unset survey_data_key
  chmod 0600 /etc/aicrm/aicrm.env
fi
if ! grep -Eq '^AICRM_IDENTITY_PHONE_DATA_KEY=.{43}$' /etc/aicrm/aicrm.env; then
  identity_phone_data_key="$(openssl rand -base64 32 | tr -d '\n=')"
  if grep -q '^AICRM_IDENTITY_PHONE_DATA_KEY=' /etc/aicrm/aicrm.env; then
    sed -i "s|^AICRM_IDENTITY_PHONE_DATA_KEY=.*$|AICRM_IDENTITY_PHONE_DATA_KEY=${identity_phone_data_key}|" /etc/aicrm/aicrm.env
  else
    printf '\nAICRM_IDENTITY_PHONE_DATA_KEY=%s\n' "$identity_phone_data_key" >> /etc/aicrm/aicrm.env
  fi
  unset identity_phone_data_key
  chmod 0600 /etc/aicrm/aicrm.env
fi
if ! grep -Eq '^AICRM_HXC_SUBJECT_HMAC_KEY=.{32,}$' /etc/aicrm/aicrm.env; then
  hxc_subject_hmac_key="$(openssl rand -base64 48 | tr -d '\n=')"
  if grep -q '^AICRM_HXC_SUBJECT_HMAC_KEY=' /etc/aicrm/aicrm.env; then
    sed -i "s|^AICRM_HXC_SUBJECT_HMAC_KEY=.*$|AICRM_HXC_SUBJECT_HMAC_KEY=${hxc_subject_hmac_key}|" /etc/aicrm/aicrm.env
  else
    printf '\nAICRM_HXC_SUBJECT_HMAC_KEY=%s\n' "$hxc_subject_hmac_key" >> /etc/aicrm/aicrm.env
  fi
  unset hxc_subject_hmac_key
  chmod 0600 /etc/aicrm/aicrm.env
fi
if ! grep -Eq '^AICRM_IDENTITY_OBSERVATION_VAULT_KEY=[A-Za-z0-9+/]{43}=$' /etc/aicrm/aicrm.env; then
  identity_observation_vault_key="$(openssl rand -base64 32 | tr -d '\n')"
  if grep -q '^AICRM_IDENTITY_OBSERVATION_VAULT_KEY=' /etc/aicrm/aicrm.env; then
    sed -i "s|^AICRM_IDENTITY_OBSERVATION_VAULT_KEY=.*$|AICRM_IDENTITY_OBSERVATION_VAULT_KEY=${identity_observation_vault_key}|" /etc/aicrm/aicrm.env
  else
    printf '\nAICRM_IDENTITY_OBSERVATION_VAULT_KEY=%s\n' "$identity_observation_vault_key" >> /etc/aicrm/aicrm.env
  fi
  unset identity_observation_vault_key
  chmod 0600 /etc/aicrm/aicrm.env
fi
if ! grep -Eq '^AICRM_HXC_IDENTITY_WRITE_ENABLED=(true|false)$' /etc/aicrm/aicrm.env; then
  if grep -q '^AICRM_HXC_IDENTITY_WRITE_ENABLED=' /etc/aicrm/aicrm.env; then
    sed -i 's|^AICRM_HXC_IDENTITY_WRITE_ENABLED=.*$|AICRM_HXC_IDENTITY_WRITE_ENABLED=false|' /etc/aicrm/aicrm.env
  else
    printf '\nAICRM_HXC_IDENTITY_WRITE_ENABLED=false\n' >> /etc/aicrm/aicrm.env
  fi
  chmod 0600 /etc/aicrm/aicrm.env
fi

# Bootstrap interruption is safe only while owning the shared release lock.
bootstrap_load_state="$(systemctl show aicrm-automation-bootstrap.service -p LoadState --value 2>/dev/null || true)"
if [[ "$bootstrap_load_state" == loaded ]]; then
  systemctl kill --kill-whom=all --signal=TERM aicrm-automation-bootstrap.service 2>/dev/null || true
  sleep 2
  systemctl kill --kill-whom=all --signal=KILL aicrm-automation-bootstrap.service 2>/dev/null || true
  if ! timeout 15s systemctl stop aicrm-automation-bootstrap.service; then
    systemctl status --no-pager --full aicrm-automation-bootstrap.service || true
    exit 14
  fi
fi

# The component is optional, but a partial provisioning must never activate a
# release against stale or missing credentials. Initial provisioning stays a
# separate, controlled operation: this installer only uses a complete existing
# configuration and never supplies source SQL or coverage evidence itself.
excel_config=/etc/aicrm-excel/config.json
excel_env=/etc/aicrm-excel/service.env
if [[ -e "$excel_config" || -e "$excel_env" ]]; then
  if [[ ! -f "$excel_config" || ! -f "$excel_env" ]]; then
    echo "Excel component configuration is incomplete" >&2
    exit 16
  fi
  api_excel_url_lines="$(grep -c '^EXCEL_BATCH_URL=' /etc/aicrm/aicrm.env || true)"
  api_excel_token_lines="$(grep -c '^EXCEL_BATCH_TOKEN=' /etc/aicrm/aicrm.env || true)"
  component_excel_token_lines="$(grep -c '^EXCEL_BATCH_TOKEN=' "$excel_env" || true)"
  if [[ "$api_excel_url_lines" != 1 || "$api_excel_token_lines" != 1 || "$component_excel_token_lines" != 1 ]] || ! grep -qxF 'EXCEL_BATCH_URL=http://127.0.0.1:8791' /etc/aicrm/aicrm.env; then
    echo "Excel component API configuration is incomplete" >&2
    exit 16
  fi
  api_excel_token="$(sed -n 's/^EXCEL_BATCH_TOKEN=//p' /etc/aicrm/aicrm.env)"
  component_excel_token="$(sed -n 's/^EXCEL_BATCH_TOKEN=//p' "$excel_env")"
  if [[ ${#api_excel_token} -lt 32 || "$api_excel_token" != "$component_excel_token" ]]; then
    unset api_excel_token component_excel_token
    echo "Excel component API token does not match" >&2
    exit 16
  fi
  unset api_excel_token component_excel_token
  if ! python3 - "$excel_config" <<'CHECK_EXCEL_CONFIG'
import json
import re
import sys

config = json.load(open(sys.argv[1], encoding="utf-8"))
source = config.get("source")
if not isinstance(source, dict):
    raise SystemExit("Excel component source configuration is invalid")
coverage = source.get("coverage_sql")
mysql = source.get("mysql")
if coverage is None and isinstance(mysql, dict):
    # The current adapter reads source.coverage_sql. Check the nested spelling
    # too so an ignored legacy-shaped value cannot slip through as a fake
    # coverage assertion if a future adapter accepts it.
    coverage = mysql.get("coverage_sql")
if coverage is not None and not isinstance(coverage, str):
    raise SystemExit("Excel component coverage configuration is invalid")
normalized = re.sub(r"\s+", " ", coverage.strip()).rstrip(";").strip() if coverage else ""
if re.fullmatch(r"SELECT (?:1|TRUE)(?: AS [A-Za-z_][A-Za-z0-9_]*)?", normalized, re.IGNORECASE):
    raise SystemExit("Excel component coverage query must not be unconditional")
CHECK_EXCEL_CONFIG
  then
    exit 16
  fi
  bash "$release_dir/deploy/prepare-excel-component.sh" "$release_dir"
fi

previous=""
if [[ -L "$current_link" ]]; then
	previous="$(readlink -f "$current_link")"
fi

ln -sfn "$release_dir" "${current_link}.new"
mv -Tf "${current_link}.new" "$current_link"
install -m 0644 "$release_dir/deploy/aicrm.service" /etc/systemd/system/aicrm.service
install -m 0644 "$release_dir/deploy/aicrm-migrate.service" /etc/systemd/system/aicrm-migrate.service
install -m 0644 "$release_dir/deploy/aicrm-wecom-worker.service" /etc/systemd/system/aicrm-wecom-worker.service
install -m 0644 "$release_dir/deploy/aicrm-wecom-worker.timer" /etc/systemd/system/aicrm-wecom-worker.timer
install -m 0644 "$release_dir/deploy/aicrm-effects-worker.service" /etc/systemd/system/aicrm-effects-worker.service
install -m 0644 "$release_dir/deploy/aicrm-customer-sync-daily.service" /etc/systemd/system/aicrm-customer-sync-daily.service
install -m 0644 "$release_dir/deploy/aicrm-customer-sync-daily.timer" /etc/systemd/system/aicrm-customer-sync-daily.timer
install -m 0644 "$release_dir/deploy/aicrm-hxc-dashboard-refresh.service" /etc/systemd/system/aicrm-hxc-dashboard-refresh.service
install -m 0644 "$release_dir/deploy/aicrm-hxc-dashboard-refresh.timer" /etc/systemd/system/aicrm-hxc-dashboard-refresh.timer
install -m 0644 "$release_dir/deploy/aicrm-hxc-daily-lessons-sync.service" /etc/systemd/system/aicrm-hxc-daily-lessons-sync.service
install -m 0644 "$release_dir/deploy/aicrm-hxc-daily-lessons-sync.timer" /etc/systemd/system/aicrm-hxc-daily-lessons-sync.timer
install -m 0644 "$release_dir/deploy/aicrm-hxc-dashboard-rollout.service" /etc/systemd/system/aicrm-hxc-dashboard-rollout.service
install -m 0644 "$release_dir/deploy/aicrm-automation-bootstrap.service" /etc/systemd/system/aicrm-automation-bootstrap.service
systemctl daemon-reload

rollback() {
	if [[ "${AICRM_ACCESS_GOVERNANCE_RELEASE:-}" == 1 ]]; then
		# After a pre-0151 convergence, older binaries are not a safe fallback:
		# they can write the historical multi-role representation against the
		# new schema. The wrapper has already stopped Access writers, so preserve
		# that stopped state for controlled recovery instead of restarting them.
		systemctl stop aicrm-wecom-worker.timer aicrm-customer-sync-daily.timer || true
		systemctl stop aicrm-wecom-worker.service aicrm-customer-sync-daily.service || true
		systemctl stop aicrm.service aicrm-effects-worker.service || true
		echo "access governance release failed; Access writers remain stopped" >&2
		return
	fi
  if [[ -n "$previous" && -d "$previous" ]]; then
    ln -sfn "$previous" "${current_link}.rollback"
    mv -Tf "${current_link}.rollback" "$current_link"
    if [[ -f "$excel_config" && -f "$excel_env" ]]; then
      if [[ -f "$previous/components/excel-batches/batches.py" ]]; then
        systemctl restart aicrm-excel-batches.service || true
      else
        systemctl stop aicrm-excel-batches.service || true
      fi
    fi
    systemctl restart aicrm.service || true
    systemctl restart aicrm-wecom-worker.timer || true
    systemctl restart aicrm-effects-worker.service || true
    systemctl restart aicrm-customer-sync-daily.timer || true
    systemctl restart aicrm-hxc-dashboard-refresh.timer || true
    systemctl restart aicrm-hxc-daily-lessons-sync.timer || true
  fi
}

if [[ -f "$excel_config" && -f "$excel_env" ]]; then
  if ! systemctl enable aicrm-excel-batches.service || ! systemctl restart aicrm-excel-batches.service; then
    rollback
    exit 16
  fi
  if ! python3 - <<'CHECK_EXCEL'
import json,time,urllib.request
from pathlib import Path
token = next(line.split('=',1)[1] for line in Path('/etc/aicrm-excel/service.env').read_text().splitlines() if line.startswith('EXCEL_BATCH_TOKEN='))
for attempt in range(30):
    try:
        request=urllib.request.Request('http://127.0.0.1:8791/health',headers={'Authorization':'Bearer '+token})
        with urllib.request.urlopen(request,timeout=2) as response:
            if json.load(response).get('ok'): break
    except Exception: pass
    time.sleep(1)
else: raise SystemExit('Excel component failed readiness')
CHECK_EXCEL
  then
    rollback
    exit 16
  fi
  printf '%s\n' 'Excel component configuration and readiness verified'
fi

# Install the fixed root helper before normal service restarts activate the
# CRM-only journal/TMPDIR drop-ins. No extra timer or worker privilege is added.
if ! python3 "$release_dir/deploy/install-host-maintenance.py"; then
  rollback
  exit 17
fi

if ! systemctl start aicrm-migrate.service; then
  rollback
  exit 5
fi
if ! systemctl restart aicrm.service; then
  rollback
  exit 6
fi

ready=false
for _ in $(seq 1 30); do
  if curl --fail --silent --show-error http://127.0.0.1:8080/readyz >/dev/null; then
    ready=true
    break
  fi
  sleep 1
done
if [[ "$ready" != true ]]; then
  rollback
  exit 7
fi

systemctl enable --now aicrm-wecom-worker.timer
if ! systemctl enable aicrm-effects-worker.service || ! systemctl restart aicrm-effects-worker.service; then
  rollback
  exit 8
fi
effects_worker_ready=false
for _ in $(seq 1 30); do
  effects_worker_pid="$(systemctl show aicrm-effects-worker.service -p MainPID --value)"
  if systemctl is-active --quiet aicrm-effects-worker.service && \
    [[ "$effects_worker_pid" =~ ^[1-9][0-9]*$ ]] && \
    [[ "$(readlink -f "/proc/${effects_worker_pid}/exe")" == "$release_dir/bin/aicrm" ]]; then
    effects_worker_ready=true
    break
  fi
  sleep 1
done
if [[ "$effects_worker_ready" != true ]]; then
  rollback
  exit 9
fi
if ! systemctl start aicrm-automation-bootstrap.service; then
  systemctl status --no-pager --full aicrm-automation-bootstrap.service || true
  journalctl --no-pager -o cat -n 50 -u aicrm-automation-bootstrap.service || true
  rollback
  exit 13
fi
journalctl --no-pager -o cat -n 20 -u aicrm-automation-bootstrap.service || true
if ! systemctl enable --now aicrm-customer-sync-daily.timer; then
  rollback
  exit 10
fi
if ! systemctl enable --now aicrm-hxc-dashboard-refresh.timer; then
  rollback
  exit 12
fi
if ! systemctl enable --now aicrm-hxc-daily-lessons-sync.timer; then
  rollback
  exit 14
fi
# A same-schema package is a rollback only after this observer verifies the
# real API readiness and both process images. It inherits the same fd 9 lock.
success_receipt_args=(--sha "$release_sha")
if [[ -n "$release_run_number" ]]; then
  success_receipt_args+=(--run-number "$release_run_number")
fi
if ! python3 "$release_dir/deploy/record-release-success.py" "${success_receipt_args[@]}"; then
  rollback
  exit 18
fi
if [[ -n "$release_run_number" ]]; then
  next_run_file="$(mktemp "${last_successful_run_file}.XXXXXX")"
  printf '%s\n' "$release_run_number" > "$next_run_file"
  chmod 0644 "$next_run_file"
  mv -f "$next_run_file" "$last_successful_run_file"
fi
echo "release ${release_sha} active"
# FD 9 still holds the installer's shared lock. The hook verifies this same
# inode and calls inventory/apply without acquiring a conflicting second fd.
if ! python3 "$release_dir/deploy/post-release-retention.py" --sha "$release_sha"; then
  echo 'release cleanup gap: inspect root-owned maintenance result; active release retained' >&2
fi

# Publish only minimal verified release facts; never expose original receipts.
if ! python3 "$release_dir/deploy/publish-governance-releases.py" --lock-fd 9; then
  echo 'governance release denominator unavailable: inspect trusted bridge; active release retained' >&2
fi
