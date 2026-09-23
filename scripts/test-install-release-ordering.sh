#!/usr/bin/env bash
set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source_installer="${repository_root}/deploy/install-release.sh"
test_root="$(mktemp -d)"
archives=()
lock_holder_pid=""

cleanup() {
  if [[ -n "$lock_holder_pid" ]]; then
    touch "$test_root/holder-release"
    wait "$lock_holder_pid" 2>/dev/null || true
  fi
  rm -rf -- "$test_root"
  if ((${#archives[@]})); then
    rm -f -- "${archives[@]}"
  fi
}
trap cleanup EXIT

fail() {
  echo "install release ordering test: $*" >&2
  exit 1
}

sha_one=1111111111111111111111111111111111111111
sha_manual=2222222222222222222222222222222222222222
sha_stale=3333333333333333333333333333333333333333
sha_failed=4444444444444444444444444444444444444444
sha_first=5555555555555555555555555555555555555555
sha_second=6666666666666666666666666666666666666666
sha_recovered=7777777777777777777777777777777777777777
sha_invalid_lock=9999999999999999999999999999999999999999
sha_missing_commerce=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
sha_missing_archive=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
sha_missing_0066=cccccccccccccccccccccccccccccccccccccccc
sha_missing_0150=c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0
sha_missing_0151=c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1
sha_missing_0152=c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2
sha_missing_0153=c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3
sha_missing_0155=c5c5c5c5c5c5c5c5c5c5c5c5c5c5c5c5c5c5c5c5
sha_missing_0162=c6c6c6c6c6c6c6c6c6c6c6c6c6c6c6c6c6c6c6c6
sha_missing_0163=c7c7c7c7c7c7c7c7c7c7c7c7c7c7c7c7c7c7c7c7
sha_missing_0164=c8c8c8c8c8c8c8c8c8c8c8c8c8c8c8c8c8c8c8c8
sha_missing_0165=c8c8c8c8c8c8c8c8c8c8c8c8c8c8c8c8c8c8c8c8
sha_missing_0166=c9c9c9c9c9c9c9c9c9c9c9c9c9c9c9c9c9c9c9c9
sha_missing_0167=cacacacacacacacacacacacacacacacacacacaca
sha_missing_0170=1717171717171717171717171717171717171717
sha_missing_0171=1818181818181818181818181818181818181818
sha_missing_0172=1919191919191919191919191919191919191919
sha_missing_0173=1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a
sha_missing_0174=1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b
sha_missing_0175=1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c
sha_missing_0176=1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d
sha_missing_0177=1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e
sha_missing_0185=f3f3f3f3f3f3f3f3f3f3f3f3f3f3f3f3f3f3f3f3
sha_missing_0186=f4f4f4f4f4f4f4f4f4f4f4f4f4f4f4f4f4f4f4f4
sha_missing_0187=f5f5f5f5f5f5f5f5f5f5f5f5f5f5f5f5f5f5f5f5
sha_missing_0188=f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6
sha_missing_0189=f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7
sha_missing_0190=f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8f8
sha_missing_0191=f9f9f9f9f9f9f9f9f9f9f9f9f9f9f9f9f9f9f9f9
sha_missing_0192=fafafafafafafafafafafafafafafafafafafafa
sha_missing_0193=babababababababababababababababababababa
sha_missing_0067=dddddddddddddddddddddddddddddddddddddddd
sha_missing_0071=eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee
sha_missing_0072=ffffffffffffffffffffffffffffffffffffffff
sha_missing_0073=1010101010101010101010101010101010101010
sha_missing_0074=2020202020202020202020202020202020202020
sha_missing_0075=3030303030303030303030303030303030303030
sha_missing_0080=4040404040404040404040404040404040404040
sha_missing_0094=5050505050505050505050505050505050505050
sha_missing_runtime_config_history=6060606060606060606060606060606060606060
sha_missing_0092=7070707070707070707070707070707070707070
sha_missing_owner_handoff_history=8080808080808080808080808080808080808080
sha_missing_0095=9090909090909090909090909090909090909090
sha_missing_0146=1461461461461461461461461461461461461461
sha_missing_0147=1471471471471471471471471471471471471471
sha_missing_commerce_push_history=abababababababababababababababababababab
sha_missing_open_platform_history=bcbcbcbcbcbcbcbcbcbcbcbcbcbcbcbcbcbcbcbc
sha_missing_0096=cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd
sha_missing_0097=9797979797979797979797979797979797979797
sha_missing_0098=8282828282828282828282828282828282828282
sha_missing_0099=8383838383838383838383838383838383838383
sha_missing_0100=8484848484848484848484848484848484848484
sha_missing_0124=9494949494949494949494949494949494949494
sha_missing_operation_runner=8585858585858585858585858585858585858585
sha_missing_operation_result=8686868686868686868686868686868686868686
sha_tampered_operation_runner=8787878787878787878787878787878787878787

mkdir -p "$test_root/bin" "$test_root/aicrm" "$test_root/etc-aicrm" "$test_root/systemd"
printf 'AICRM_SURVEY_DATA_KEY=%043d\n' 0 > "$test_root/etc-aicrm/aicrm.env"

cat > "$test_root/bin/id" <<'EOF'
#!/usr/bin/env bash
[[ "${1:-}" == aicrm ]] && exit 0
exec /usr/bin/id "$@"
EOF
cat > "$test_root/bin/chown" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
cat > "$test_root/bin/curl" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
cat > "$test_root/bin/mv" <<'EOF'
#!/usr/bin/env bash
if [[ "${1:-}" == -T* ]]; then
  options="$1"
  shift
  target="${!#}"
  /bin/rm -f "$target"
  [[ "$options" == *f* ]] && exec /bin/mv -f "$@"
fi
exec /bin/mv "$@"
EOF
cat > "$test_root/bin/readlink" <<'EOF'
#!/usr/bin/env bash
if [[ "${1:-}" == -f && "${2:-}" == /proc/*/exe ]]; then
  calls=0
  [[ -f "$AICRM_TEST_EFFECTS_READLINK_STATE" ]] && calls="$(<"$AICRM_TEST_EFFECTS_READLINK_STATE")"
  calls=$((calls + 1))
  printf '%s\n' "$calls" > "$AICRM_TEST_EFFECTS_READLINK_STATE"
  if ((calls <= AICRM_TEST_EFFECTS_EXE_DELAY_CALLS)); then
    printf '/previous/aicrm\n'
    exit 0
  fi
  printf '%s\n' "$AICRM_TEST_EXPECTED_EXE"
  exit 0
fi
if [[ "${1:-}" == -f ]]; then
  exec /usr/bin/readlink "$2"
fi
exec /usr/bin/readlink "$@"
EOF
cat > "$test_root/bin/systemctl" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "${AICRM_TEST_LOG}.systemctl"
if [[ "${1:-}" == start && "${2:-}" == aicrm-migrate.service ]]; then
  printf 'migration:%s\n' "${AICRM_TEST_LABEL:-unknown}" >> "$AICRM_TEST_LOG"
  [[ "${AICRM_TEST_FAIL_MIGRATION:-}" == 1 ]] && exit 1
  [[ -n "${AICRM_TEST_MIGRATION_DELAY:-}" ]] && /bin/sleep "$AICRM_TEST_MIGRATION_DELAY"
fi
if [[ "${1:-}" == show && "${2:-}" == aicrm-effects-worker.service ]]; then
  printf '4242\n'
fi
exit 0
EOF
chmod 0755 "$test_root/bin"/*

# macOS lacks the flock CLI. Use the actual kernel flock on inherited fd 9
# rather than a mkdir substitute, so configure/installer exclusion is tested.
if ! command -v flock >/dev/null 2>&1; then
  cat > "$test_root/bin/flock" <<'EOF'
#!/usr/bin/env python3
import fcntl
import sys
import time
args = sys.argv[1:]
wait = float(args[1]) if args[0] == '-w' else None
fd = int(args[-1])
if wait is None:
    fcntl.flock(fd, fcntl.LOCK_EX)
else:
    deadline = time.monotonic() + wait
    while True:
        try:
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
            break
        except BlockingIOError:
            if time.monotonic() >= deadline:
                sys.exit(1)
            time.sleep(0.01)
EOF
  chmod 0755 "$test_root/bin/flock"
fi
cp "$source_installer" "$test_root/install-release.sh"
sed \
  -e "s|/opt/aicrm|$test_root/aicrm|g" \
  -e "s|/etc/aicrm|$test_root/etc-aicrm|g" \
  -e "s|/etc/systemd/system|$test_root/systemd|g" \
  -e 's|flock -w 15 9|flock -w 2 9|' \
  "$test_root/install-release.sh" > "$test_root/install-release.rewritten.sh"
mv "$test_root/install-release.rewritten.sh" "$test_root/install-release.sh"
chmod 0755 "$test_root/install-release.sh"

make_release() {
  local sha="$1"
  local missing_release_file="${2:-}"
  local tampered_release_file="${3:-}"
  local release="$test_root/package-${sha}"
  local archive="/tmp/aicrm-${sha}.tar.gz"
  mkdir -p "$release/bin" "$release/migrations" "$release/web/dist/admin" "$release/web/dist/sidebar" "$release/web/dist/aiassistant" "$release/deploy"
  for binary in aicrm aicrm-operation-cycle-runner aicrm-operation-cycle-result wecom-archive-sdk-runner migrate-platform migrate-access-role-convergence check-enterprise-directory migrate-river migrate-phone-identities migrate-identity-phone-vault migrate-survey-v2 migrate-commerce-history migrate-message-archive migrate-order-attribution migrate-order-distribution-qualification migrate-automation-operations migrate-v2-config-definitions migrate-v2-runtime-config-releases migrate-v2-commerce-external-push-history migrate-open-platform migrate-media-legacy-materials migrate-hxc-daily-lessons migrate-channel-history migrate-v2-customer-tag-history migrate-radar-v2 migrate-sidebar-history migrate-owner-handoff-history bootstrap-automation-operations; do
    printf '#!/usr/bin/env bash\nexit 0\n' > "$release/bin/$binary"
    chmod 0755 "$release/bin/$binary"
  done
  for migration in \
    0005_external_effects.sql 0006_wecom_callback_channel_acquisition.sql 0007_media.sql \
    0008_tag_catalog.sql 0009_customer_activation.sql 0010_product.sql 0011_coupon_rules.sql \
    0012_group_ops.sql 0013_automation_agents.sql 0014_operation_cycles.sql \
    0015_config_adminops.sql 0016_media_content_packages.sql 0017_group_ops_history.sql \
    0018_survey.sql 0019_tag_catalog_sync_projection.sql 0020_order.sql 0021_payment.sql \
    0022_customer_profile_sections.sql 0024_order_product_version.sql \
    0025_payment_reconciliation.sql 0026_identity_history_receipts.sql \
    0027_admin_access_login_compat.sql 0028_hxc_dashboard.sql \
    0029_channel_center.sql 0030_config_definition_import.sql \
    0031_channel_history_import.sql 0032_channel_acquisition_assets.sql \
    0033_wecom_welcome_grants.sql 0034_channel_entrant_actions.sql \
    0035_channel_acquisition_links.sql 0036_ai_assistant_review.sql \
    0037_outbound_private_messages.sql 0038_survey_oauth_phone_vault.sql \
    0047_automation_operations_migration.sql \
    0048_segment_audience_schedule_state.sql 0049_order_history_attribution.sql \
    0050_radar_core.sql 0051_radar_sessions_events.sql 0052_radar_legacy_import.sql \
    0053_segment_audience_member_event_fact_kinds.sql \
    0066_channel_welcome_intents.sql \
    0150_channel_welcome_message_snapshots.sql \
    0151_access_role_governance.sql \
    0152_access_login_grants.sql \
    0153_wecom_customer_detail_projection.sql \
    0155_group_ops_webhook_dynamic_executions.sql \
    0156_distribution_profit_sharing_payment.sql \
    0157_distribution_core.sql \
    0158_order_distribution_qualification_evidence.sql \
    0159_distribution_admin_receipts.sql \
    0160_external_effect_system_control_actor.sql \
    0161_payment_paid_confirmation_time.sql \
    0164_channel_welcome_provider_rejections.sql \
    0162_payment_h5_distribution_return_path.sql \
    0163_payment_profit_sharing_receiver_recovery.sql \
    0165_payment_profit_sharing_receiver_failure_class.sql \
    0166_payment_profit_sharing_instruction_failure_class.sql \
    0167_distribution_settlement_not_paid_exception.sql \
    0170_wecom_contact_description_effect.sql \
    0171_wecom_contact_description_source_coverage.sql \
    0172_order_checkout_post_purchase_action.sql \
    0173_product_paid_purchase_action_target_snapshot.sql \
    0174_product_external_push_test_delivery_id.sql \
    0175_customer_minimum_directory_projection.sql \
    0176_survey_single_submission_claims.sql \
    0177_survey_operation_legacy_parity.sql \
    0181_hxc_dashboard_views.sql \
    0185_referral_core.sql \
    0186_adminops_inspections.sql \
    0187_adminops_notification_effect.sql \
    0188_adminops_retention.sql \
    0189_owner_process_retention.sql \
    0190_adminops_cpu_profiles.sql \
    0191_payment_h5_referral_return_path.sql \
    0192_adminops_diagnostic_event_identity.sql \
    0193_adminops_governance_outcomes.sql \
    0067_survey_completion_snapshots.sql \
    0083_segment_audience_refresh_modes.sql \
    0085_segment_audience_refresh_kind.sql \
    0086_wecom_profile_primary_owner.sql \
    0087_automation_manual_ai_review.sql \
    0089_outbound_message_content_snapshots.sql \
    0061_product_public_purchase.sql \
    0063_identity_hxc_source_observations.sql \
    0064_hxc_dashboard_identity_v2.sql \
    0068_payment_session_beneficiary_selection.sql \
    0069_coupon_claim_redemption_lifecycle.sql \
    0070_service_period_entitlement_fulfillment.sql \
    0071_message_archive_core.sql \
    0072_message_archive_migration_receipts.sql \
    0073_survey_completion_test_push_snapshots.sql \
    0074_survey_external_operation_execution_facts.sql \
    0075_external_effects_survey_completion_kind.sql \
    0076_order_checkout_snapshots.sql \
    0077_coupon_public_slug.sql \
    0078_group_ops_provider_tasks.sql \
    0079_service_period_member_grid.sql \
    0080_media_legacy_material_mappings.sql \
    0081_group_ops_webhook_unconfigured_reference.sql \
    0082_group_ops_history_import.sql \
    0084_hxc_shared_facts.sql \
    0088_order_service_entitlement_alliance.sql \
    0090_survey_oauth_state_redirect.sql \
    0091_survey_assessment_business_keys.sql \
    0092_customer_owner_handoff.sql \
    0093_customer_tag_commands.sql \
    0094_runtime_config_releases.sql \
    0095_product_external_push.sql \
    0146_product_external_push_field_mapping.sql \
    0147_outbound_commerce_mapping_mode.sql \
    0096_open_platform.sql \
    0097_segment_audience_mutation_actor.sql \
    0098_message_archive_historical_projection.sql \
    0099_survey_historical_external_projection.sql \
    0100_ai_assistant_machine_actor.sql \
    0124_operation_excel_batch_lifecycle.sql; do
    : > "$release/migrations/$migration"
  done
  : > "$release/web/dist/asset-manifest.json"
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
    : > "$release/web/dist/admin/$new_shell_page.html"
  done
  : > "$release/web/dist/sidebar/index.html"
  for ai_assistant_asset in \
    list.html detail.html \
    send_content_readonly_detail.css send_content_readonly_detail.js \
    cloud_plan_review.js; do
    : > "$release/web/dist/aiassistant/$ai_assistant_asset"
  done
  mkdir -p "$release/web/dist/assets/standard-components"
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
    : > "$release/web/dist/assets/standard-components/$standard_component_asset"
  done
  for unit in \
    aicrm.service aicrm-migrate.service aicrm-wecom-worker.service aicrm-wecom-worker.timer \
    aicrm-effects-worker.service aicrm-customer-sync-daily.service aicrm-customer-sync-daily.timer \
    aicrm-hxc-dashboard-refresh.service aicrm-hxc-dashboard-refresh.timer \
    aicrm-hxc-daily-lessons-sync.service aicrm-hxc-daily-lessons-sync.timer aicrm-hxc-dashboard-rollout.service \
    aicrm-automation-bootstrap.service; do
    : > "$release/deploy/$unit"
  done
  : > "$release/deploy/rollout-hxc-identity-v2.sh"
  # Host/systemd behavior is tested separately; these deterministic fixtures
  # verify ordering and that failed/stale deployments never run cleanup.
  cat > "$release/deploy/install-host-maintenance.py" <<'PY'
import os
with open(os.environ["AICRM_TEST_LOG"], "a") as log:
    log.write("host-install:" + os.environ["AICRM_TEST_LABEL"] + "\n")
PY
  cat > "$release/deploy/publish-governance-releases.py" <<'PYBRIDGE'
import os
os.fstat(9)
PYBRIDGE
  cat > "$release/deploy/post-release-retention.py" <<'PY'
import os
with open(os.environ["AICRM_TEST_LOG"], "a") as log:
    log.write("release-cleanup:" + os.environ["AICRM_TEST_LABEL"] + "\n")
PY
  cat > "$release/deploy/record-release-success.py" <<'PY'
import os
import sys
with open(os.environ["AICRM_TEST_LOG"], "a") as log:
    log.write(("receipt-recovery:" if "--revoke-incomplete" in sys.argv else "success-receipt:") + os.environ["AICRM_TEST_LABEL"] + "\n")
if "--revoke-incomplete" in sys.argv:
    sys.exit(1 if os.environ.get("AICRM_TEST_FAIL_RECEIPT_RECOVERY") == "1" else 0)
if os.environ.get("AICRM_TEST_FAIL_SUCCESS_RECEIPT") == "1":
    sys.exit(1)
PY
  if [[ -n "$missing_release_file" ]]; then
    rm -f -- "$release/$missing_release_file"
  fi
  (
    cd "$release"
    LC_ALL=C find . -type f ! -name release-files.sha256 -print0 | sort -z | xargs -0 sha256sum > release-files.sha256
  )
  if [[ -n "$tampered_release_file" ]]; then
    printf 'tampered-after-manifest\n' >> "$release/$tampered_release_file"
  fi
  tar -C "$release" -czf "$archive" .
  archives+=("$archive")
}

run_release() {
  local sha="$1"
  local run_number="${2:-}"
  local label="$3"
  local effects_delay="${4:-0}"
  PATH="$test_root/bin:$PATH" \
    AICRM_TEST_LOCK_DIR="$test_root/install.lock" \
    AICRM_TEST_LOG="$test_root/install.log" \
    AICRM_TEST_LABEL="$label" \
    AICRM_TEST_EFFECTS_EXE_DELAY_CALLS="$effects_delay" \
    AICRM_TEST_EFFECTS_READLINK_STATE="$test_root/effects-readlink-${sha}" \
    AICRM_TEST_EXPECTED_EXE="$test_root/aicrm/releases/${sha}/bin/aicrm" \
    "$test_root/install-release.sh" "/tmp/aicrm-${sha}.tar.gz" "$sha" "$run_number" >> "$test_root/installer.log" 2>&1
}

for sha in "$sha_one" "$sha_manual" "$sha_stale" "$sha_failed" "$sha_first" "$sha_second" "$sha_recovered" "$sha_invalid_lock"; do make_release "$sha"; done
for missing_release in \
  "$sha_missing_operation_runner:bin/aicrm-operation-cycle-runner" \
  "$sha_missing_operation_result:bin/aicrm-operation-cycle-result" \
  "$sha_missing_commerce:bin/migrate-commerce-history" \
  "$sha_missing_archive:bin/migrate-message-archive" \
  "$sha_missing_0066:migrations/0066_channel_welcome_intents.sql" \
  "$sha_missing_0150:migrations/0150_channel_welcome_message_snapshots.sql" \
  "$sha_missing_0151:migrations/0151_access_role_governance.sql" \
  "$sha_missing_0152:migrations/0152_access_login_grants.sql" \
  "$sha_missing_0153:migrations/0153_wecom_customer_detail_projection.sql" \
  "$sha_missing_0155:migrations/0155_group_ops_webhook_dynamic_executions.sql" \
  "$sha_missing_0162:migrations/0162_payment_h5_distribution_return_path.sql" \
  "$sha_missing_0163:migrations/0163_payment_profit_sharing_receiver_recovery.sql" \
  "$sha_missing_0164:migrations/0164_channel_welcome_provider_rejections.sql" \
  "$sha_missing_0165:migrations/0165_payment_profit_sharing_receiver_failure_class.sql" \
  "$sha_missing_0166:migrations/0166_payment_profit_sharing_instruction_failure_class.sql" \
  "$sha_missing_0167:migrations/0167_distribution_settlement_not_paid_exception.sql" \
  "$sha_missing_0170:migrations/0170_wecom_contact_description_effect.sql" \
  "$sha_missing_0171:migrations/0171_wecom_contact_description_source_coverage.sql" \
  "$sha_missing_0172:migrations/0172_order_checkout_post_purchase_action.sql" \
  "$sha_missing_0173:migrations/0173_product_paid_purchase_action_target_snapshot.sql" \
  "$sha_missing_0174:migrations/0174_product_external_push_test_delivery_id.sql" \
  "$sha_missing_0175:migrations/0175_customer_minimum_directory_projection.sql" \
  "$sha_missing_0176:migrations/0176_survey_single_submission_claims.sql" \
  "$sha_missing_0177:migrations/0177_survey_operation_legacy_parity.sql" \
  "$sha_missing_0185:migrations/0185_referral_core.sql" \
  "$sha_missing_0186:migrations/0186_adminops_inspections.sql" \
  "$sha_missing_0187:migrations/0187_adminops_notification_effect.sql" \
  "$sha_missing_0188:migrations/0188_adminops_retention.sql" \
  "$sha_missing_0189:migrations/0189_owner_process_retention.sql" \
  "$sha_missing_0190:migrations/0190_adminops_cpu_profiles.sql" \
  "$sha_missing_0191:migrations/0191_payment_h5_referral_return_path.sql" \
  "$sha_missing_0192:migrations/0192_adminops_diagnostic_event_identity.sql" \
  "$sha_missing_0193:migrations/0193_adminops_governance_outcomes.sql" \
  "$sha_missing_0067:migrations/0067_survey_completion_snapshots.sql" \
  "$sha_missing_0071:migrations/0071_message_archive_core.sql" \
  "$sha_missing_0072:migrations/0072_message_archive_migration_receipts.sql" \
  "$sha_missing_0073:migrations/0073_survey_completion_test_push_snapshots.sql" \
  "$sha_missing_0074:migrations/0074_survey_external_operation_execution_facts.sql" \
  "$sha_missing_0092:migrations/0092_customer_owner_handoff.sql" \
  "$sha_missing_owner_handoff_history:bin/migrate-owner-handoff-history" \
  "$sha_missing_0075:migrations/0075_external_effects_survey_completion_kind.sql" \
  "$sha_missing_0080:migrations/0080_media_legacy_material_mappings.sql" \
  "$sha_missing_0094:migrations/0094_runtime_config_releases.sql" \
  "$sha_missing_runtime_config_history:bin/migrate-v2-runtime-config-releases" \
  "$sha_missing_0095:migrations/0095_product_external_push.sql" \
  "$sha_missing_0146:migrations/0146_product_external_push_field_mapping.sql" \
  "$sha_missing_0147:migrations/0147_outbound_commerce_mapping_mode.sql" \
  "$sha_missing_commerce_push_history:bin/migrate-v2-commerce-external-push-history" \
  "$sha_missing_0096:migrations/0096_open_platform.sql" \
  "$sha_missing_0097:migrations/0097_segment_audience_mutation_actor.sql" \
  "$sha_missing_0098:migrations/0098_message_archive_historical_projection.sql" \
  "$sha_missing_0099:migrations/0099_survey_historical_external_projection.sql" \
  "$sha_missing_0100:migrations/0100_ai_assistant_machine_actor.sql" \
  "$sha_missing_0124:migrations/0124_operation_excel_batch_lifecycle.sql" \
  "$sha_missing_open_platform_history:bin/migrate-open-platform"; do
  sha="${missing_release%%:*}"
  missing_path="${missing_release#*:}"
  label="missing-${missing_path##*/}"
  make_release "$sha" "$missing_path"
  if run_release "$sha" 98 "$label"; then
    fail "installer accepted a release missing ${missing_path}"
  fi
  [[ ! -L "$test_root/aicrm/current" ]] || fail "missing ${missing_path} activated a release"
  if [[ -f "$test_root/install.log" ]] && grep -Fqx "migration:${label}" "$test_root/install.log"; then
    fail "missing ${missing_path} ran migrations"
  fi
done

make_release "$sha_tampered_operation_runner" "" "bin/aicrm-operation-cycle-runner"
if run_release "$sha_tampered_operation_runner" 98 tampered-operation-runner; then
  fail "installer accepted a release whose OperationCycle runner differed from the manifest"
fi
[[ ! -L "$test_root/aicrm/current" ]] || fail "tampered OperationCycle runner activated a release"
if [[ -f "$test_root/install.log" ]] && grep -Fqx 'migration:tampered-operation-runner' "$test_root/install.log"; then
  fail "tampered OperationCycle runner ran migrations"
fi

# Model the same kernel lock held by configure-ops-runtime (or any other live
# process). A newer numbered release is not evidence that this owner is stale.
cp "$test_root/etc-aicrm/aicrm.env" "$test_root/env-before-busy"
python3 - "$test_root" <<'PY_HOLDER' &
import fcntl
from pathlib import Path
import sys
import time
root = Path(sys.argv[1])
with (root / 'aicrm/install-release.lock').open('a') as lock:
    fcntl.flock(lock, fcntl.LOCK_EX)
    (root / 'holder-ready').touch()
    while not (root / 'holder-release').exists():
        time.sleep(0.01)
PY_HOLDER
lock_holder_pid=$!
for _ in $(seq 1 200); do
  [[ -f "$test_root/holder-ready" ]] && break
  /bin/sleep 0.01
done
[[ -f "$test_root/holder-ready" ]] || fail "real lock holder did not start"
set +e
run_release "$sha_one" 100 busy
busy_status=$?
set -e
[[ "$busy_status" == 75 ]] || fail "busy release did not fail explicitly"
kill -0 "$lock_holder_pid" || fail "installer terminated a live lock holder"
cmp -s "$test_root/env-before-busy" "$test_root/etc-aicrm/aicrm.env" || fail "busy installer changed runtime configuration"
[[ ! -L "$test_root/aicrm/current" && ! -d "$test_root/aicrm/releases/$sha_one" ]] || fail "busy installer changed release selection/package"
[[ ! -s "$test_root/install.log.systemctl" ]] || fail "busy installer changed services"
[[ -f "/tmp/aicrm-${sha_one}.tar.gz" ]] || fail "busy installer consumed its retry archive"
touch "$test_root/holder-release"
wait "$lock_holder_pid"
lock_holder_pid=""

# A killed configurer leaves recovery evidence; changing SHA before recovery
# would make its same-release rollback impossible. Reject without mutations.
printf '{}\n' > "$test_root/etc-aicrm/.ops-runtime-recovery.json"
set +e
run_release "$sha_one" 100 recovery-required
recovery_status=$?
set -e
[[ "$recovery_status" == 75 ]] || fail "unrecovered runtime configuration did not block installation"
grep -qF 'recovery_required' "$test_root/installer.log" || fail "missing recovery-required explanation"
cmp -s "$test_root/env-before-busy" "$test_root/etc-aicrm/aicrm.env" || fail "recovery-required installer changed runtime configuration"
[[ ! -L "$test_root/aicrm/current" && ! -d "$test_root/aicrm/releases/$sha_one" ]] || fail "recovery-required installer staged or activated a release"
[[ ! -s "$test_root/install.log.systemctl" && -f "/tmp/aicrm-${sha_one}.tar.gz" ]] || fail "recovery-required installer changed services or consumed archive"
rm "$test_root/etc-aicrm/.ops-runtime-recovery.json"

if AICRM_TEST_FAIL_RECEIPT_RECOVERY=1 run_release "$sha_one" 100 recovery-failed; then
  fail "unrecoverable success receipt unexpectedly allowed release"
fi
cmp -s "$test_root/env-before-busy" "$test_root/etc-aicrm/aicrm.env" || fail "failed receipt recovery changed runtime configuration"
[[ ! -L "$test_root/aicrm/current" && ! -s "$test_root/install.log.systemctl" ]] || fail "failed receipt recovery changed current or services"
[[ ! -e "$test_root/aicrm/last-successful-run-number" ]] || fail "failed receipt recovery advanced success marker"
make_release "$sha_one"

run_release "$sha_one" 100 initial 1
[[ "$(grep -E '^(host-install|migration|success-receipt|release-cleanup):initial$' "$test_root/install.log" | tr '\n' ' ')" == 'host-install:initial migration:initial success-receipt:initial release-cleanup:initial ' ]] || fail "maintenance install/migration/success receipt/cleanup ordering changed"
[[ "$(<"$test_root/effects-readlink-${sha_one}")" == 2 ]] || fail "effects worker executable readiness was not retried"
[[ "$(<"$test_root/aicrm/last-successful-run-number")" == 100 ]] || fail "successful run did not persist its run number"
[[ "$(readlink "$test_root/aicrm/current")" == "$test_root/aicrm/releases/$sha_one" ]] || fail "initial release was not activated"
[[ ! -e "/tmp/aicrm-${sha_one}.tar.gz" ]] || fail "successful versioned install did not clean its archive"

run_release "$sha_manual" "" manual
[[ "$(<"$test_root/aicrm/last-successful-run-number")" == 100 ]] || fail "manual compatibility install advanced the CI run number"
manual_current="$(readlink "$test_root/aicrm/current")"
[[ "$manual_current" == "$test_root/aicrm/releases/$sha_manual" ]] || fail "manual compatibility install did not activate: ${manual_current}"

run_release "$sha_stale" 99 stale
[[ "$(<"$test_root/aicrm/last-successful-run-number")" == 100 ]] || fail "stale run advanced the deployment marker"
[[ "$(readlink "$test_root/aicrm/current")" == "$test_root/aicrm/releases/$sha_manual" ]] || fail "stale run replaced the active release"

if PATH="$test_root/bin:$PATH" \
  AICRM_TEST_LOCK_DIR="$test_root/install.lock" \
  AICRM_TEST_LOG="$test_root/install.log" \
  AICRM_TEST_LABEL=failed \
  AICRM_TEST_FAIL_MIGRATION=1 \
  AICRM_TEST_EXPECTED_EXE="$test_root/aicrm/releases/${sha_failed}/bin/aicrm" \
  "$test_root/install-release.sh" "/tmp/aicrm-${sha_failed}.tar.gz" "$sha_failed" 101 >> "$test_root/installer.log" 2>&1; then
  fail "migration failure unexpectedly succeeded"
fi
[[ "$(<"$test_root/aicrm/last-successful-run-number")" == 100 ]] || fail "failed run advanced the deployment marker"
[[ "$(readlink "$test_root/aicrm/current")" == "$test_root/aicrm/releases/$sha_manual" ]] || fail "failed run did not roll back"
if grep -Eq '^(success-receipt|release-cleanup):(failed|stale)$' "$test_root/install.log"; then
  fail "failed or stale release claimed success or ran cleanup"
fi
[[ -x "$test_root/aicrm/current/bin/aicrm-operation-cycle-runner" && -x "$test_root/aicrm/current/bin/aicrm-operation-cycle-result" ]] || fail "rollback did not retain the previous OperationCycle runner artifacts"

# A healthy HTTP status alone cannot advance the deployment marker: the real
# observer also checks the exact API/worker images and installed schema.
make_release "$sha_failed"
if AICRM_TEST_FAIL_SUCCESS_RECEIPT=1 run_release "$sha_failed" 101 receipt-failed; then
  fail "unverified successful-release receipt unexpectedly succeeded"
fi
[[ "$(<"$test_root/aicrm/last-successful-run-number")" == 100 ]] || fail "unverified receipt advanced deployment marker"
[[ "$(readlink "$test_root/aicrm/current")" == "$test_root/aicrm/releases/$sha_manual" ]] || fail "unverified receipt did not roll back"
if grep -q '^release-cleanup:receipt-failed$' "$test_root/install.log"; then
  fail "unverified release ran cleanup"
fi

PATH="$test_root/bin:$PATH" \
  AICRM_TEST_LOCK_DIR="$test_root/install.lock" \
  AICRM_TEST_LOG="$test_root/install.log" \
  AICRM_TEST_LABEL=first \
  AICRM_TEST_MIGRATION_DELAY=1 \
  AICRM_TEST_EXPECTED_EXE="$test_root/aicrm/releases/${sha_first}/bin/aicrm" \
  "$test_root/install-release.sh" "/tmp/aicrm-${sha_first}.tar.gz" "$sha_first" 102 > "$test_root/first.log" 2>&1 &
first_pid=$!
/bin/sleep 0.1
run_release "$sha_second" 103 second
wait "$first_pid"
[[ "$(<"$test_root/aicrm/last-successful-run-number")" == 103 ]] || fail "serialized deployments did not retain the newest run number"
[[ "$(readlink "$test_root/aicrm/current")" == "$test_root/aicrm/releases/$sha_second" ]] || fail "serialized deployments did not retain the newest release"
[[ "$(grep '^migration:' "$test_root/install.log" | tail -n 2 | tr '\n' ' ')" == 'migration:first migration:second ' ]] || fail "second deployment entered the critical section before the first completed"

# A valid wrapper shares its already-held open file description. Reopening
# that path here would deadlock against the wrapper itself.
(
  exec 9>"$test_root/aicrm/install-release.lock"
  PATH="$test_root/bin:$PATH" flock 9
  AICRM_RELEASE_LOCK_HELD=1 AICRM_RELEASE_LOCK_FD=9 run_release "$sha_recovered" 105 inherited
)
[[ "$(<"$test_root/aicrm/last-successful-run-number")" == 105 ]] || fail "inherited lock did not retain run ordering"
[[ "$(readlink "$test_root/aicrm/current")" == "$test_root/aicrm/releases/$sha_recovered" ]] || fail "inherited lock did not activate the release"

set +e
(
  exec 9>"$test_root/wrong.lock"
  AICRM_RELEASE_LOCK_HELD=1 AICRM_RELEASE_LOCK_FD=9 run_release "$sha_invalid_lock" 106 wrong-inherited-lock
)
wrong_lock_status=$?
set -e
[[ "$wrong_lock_status" == 15 ]] || fail "inherited unrelated fd was accepted"
[[ "$(readlink "$test_root/aicrm/current")" == "$test_root/aicrm/releases/$sha_recovered" ]] || fail "invalid inherited lock changed current"
[[ -f "/tmp/aicrm-${sha_invalid_lock}.tar.gz" ]] || fail "invalid inherited lock consumed its retry archive"

echo "install release ordering contract passed"
