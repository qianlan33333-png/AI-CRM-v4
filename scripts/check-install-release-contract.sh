#!/usr/bin/env bash
set -euo pipefail

installer="deploy/install-release.sh"
ci_workflow=".github/workflows/ci.yml"
quality_lanes="scripts/ci/quality_lanes.py"
release_builder="scripts/run-donor-view-consumers.sh"
grep -qxF 'export PYTHONDONTWRITEBYTECODE=1' "$installer" || { echo "release hooks must not add unregistered Python cache files" >&2; exit 1; }
grep -qF 'release host must be Linux x86_64' "$installer" || { echo "installer must reject non-Linux release hosts" >&2; exit 1; }
grep -qF 'release binary is not Linux amd64 ELF' "$installer" || { echo "installer must reject non-Linux release binaries" >&2; exit 1; }
canonical_backend_full_go_test() {
  grep -qF 'scripts/ci/quality_lanes.py backend' "$ci_workflow" &&
    grep -qF '"go", "test", "-p", "1", "-race", "-count=1", "-timeout=15m", "./..."' "$quality_lanes"
}
start_line="$(grep -nE '^if ! systemctl enable aicrm-effects-worker\.service \|\| ! systemctl restart aicrm-effects-worker\.service; then$' "$installer" | cut -d: -f1)"
test -n "$start_line" || { echo "effects worker enable and restart must be rollback guarded" >&2; exit 1; }
exit_line="$(grep -nE '^  exit 8$' "$installer" | cut -d: -f1)"
restart_line="$(grep -nE '^    systemctl restart aicrm-effects-worker\.service \|\| true$' "$installer" | cut -d: -f1)"
active_line="$(grep -nF 'if [[ "$effects_worker_ready" != true ]]; then' "$installer" | cut -d: -f1)"
active_exit_line="$(grep -nE '^  exit 9$' "$installer" | cut -d: -f1)"
test -n "$exit_line" && test -n "$restart_line" && test -n "$active_line" && test -n "$active_exit_line" || {
  echo "effects worker failure must restart the previous compatible worker and exit" >&2; exit 1;
}
test "$(sed -n "$((start_line + 1))p" "$installer")" = "  rollback" && test "$((start_line + 2))" -eq "$exit_line" || {
  echo "effects worker rollback order is invalid" >&2; exit 1;
}
test "$(sed -n "$((active_line + 1))p" "$installer")" = "  rollback" && test "$((active_line + 2))" -eq "$active_exit_line" || {
  echo "effects worker must be active on the activated release" >&2; exit 1;
}
grep -qF 'for _ in $(seq 1 30); do' "$installer" || { echo "effects worker activation must have a bounded readiness retry" >&2; exit 1; }
grep -qF '[[ "$(readlink -f "/proc/${effects_worker_pid}/exe")" == "$release_dir/bin/aicrm" ]]' "$installer" || {
  echo "effects worker executable must match the activated release" >&2; exit 1;
}
grep -qxF 'test -f "$release_dir/deploy/record-release-success.py"' "$installer" || { echo "release must include its success observer" >&2; exit 1; }
receipt_line="$(grep -nF 'if ! python3 "$release_dir/deploy/record-release-success.py" "${success_receipt_args[@]}"; then' "$installer" | cut -d: -f1)"
marker_line="$(grep -nF '  next_run_file="$(mktemp "${last_successful_run_file}.XXXXXX")"' "$installer" | cut -d: -f1)"
cleanup_line="$(grep -nF 'if ! python3 "$release_dir/deploy/post-release-retention.py" --sha "$release_sha"; then' "$installer" | cut -d: -f1)"
test -n "$receipt_line" && test "$receipt_line" -gt "$active_line" && test "$receipt_line" -lt "$marker_line" && test "$receipt_line" -lt "$cleanup_line" || { echo "verified success receipt must precede success marker and cleanup" >&2; exit 1; }
test "$(sed -n "$((receipt_line + 1))p" "$installer")" = '  rollback' && test "$(sed -n "$((receipt_line + 2))p" "$installer")" = '  exit 18' || { echo "unverified success receipt must fail and roll back release" >&2; exit 1; }
recovery_line="$(grep -nF 'if ! python3 "$release_dir/deploy/record-release-success.py" --sha "$release_sha" --revoke-incomplete; then' "$installer" | cut -d: -f1)"
env_line="$(grep -nF "if ! grep -Eq '^AICRM_SURVEY_DATA_KEY=.{43}$'" "$installer" | cut -d: -f1)"
test -n "$recovery_line" && test "$recovery_line" -lt "$env_line" || { echo "success publication recovery must precede environment and service changes" >&2; exit 1; }

for contract in \
  "deploy/aicrm.service:api" \
  "deploy/aicrm-wecom-worker.service:worker" \
  "deploy/aicrm-effects-worker.service:effects-worker"; do
  unit="${contract%%:*}"
  role="${contract#*:}"
  grep -qxF "ExecStart=/usr/bin/env AICRM_ROLE=${role} /opt/aicrm/current/bin/aicrm" "$unit" || {
    echo "$unit must override the shared environment role at exec time" >&2
    exit 1
  }
done
grep -qx 'test -f "$release_dir/deploy/aicrm-hxc-dashboard-rollout.service"' "$installer" || { echo "release must include the HXC rollout unit" >&2; exit 1; }
grep -qx 'test -f "$release_dir/deploy/rollout-hxc-identity-v2.sh"' "$installer" || { echo "release must include the HXC rollout gate" >&2; exit 1; }
grep -qx 'install -m 0644 "$release_dir/deploy/aicrm-hxc-dashboard-rollout.service" /etc/systemd/system/aicrm-hxc-dashboard-rollout.service' "$installer" || { echo "installer must install the HXC rollout unit" >&2; exit 1; }
grep -qxF 'ExecStart=/usr/bin/env AICRM_ROLE=worker AICRM_CUSTOMER_SYNC_TRIGGER=daily /opt/aicrm/current/bin/aicrm' deploy/aicrm-customer-sync-daily.service || {
  echo "daily customer sync must pin its role and trigger at exec time" >&2
  exit 1
}
grep -qxF 'ExecStart=/usr/bin/env AICRM_ROLE=worker AICRM_HXC_SYNC_TRIGGER=scheduled /opt/aicrm/current/bin/aicrm' deploy/aicrm-hxc-dashboard-refresh.service || { echo "HXC refresh must use the durable worker trigger" >&2; exit 1; }
grep -qxF 'OnCalendar=*-*-* 03,09,15,21:15:00 Asia/Shanghai' deploy/aicrm-hxc-dashboard-refresh.timer || { echo "HXC refresh timer must run at the four approved Beijing times" >&2; exit 1; }
grep -qxF 'ExecStart=/opt/aicrm/current/bin/migrate-hxc-daily-lessons --mode sync --actor-admin-user-id 2' deploy/aicrm-hxc-daily-lessons-sync.service || { echo "HXC daily lesson sync must use the immutable UUID importer" >&2; exit 1; }
grep -qxF 'OnCalendar=*-*-* 10:00:00 Asia/Shanghai' deploy/aicrm-hxc-daily-lessons-sync.timer || { echo "HXC daily lesson sync must run at 10:00 Beijing time" >&2; exit 1; }
grep -qx 'install -m 0644 "$release_dir/deploy/aicrm-hxc-daily-lessons-sync.service" /etc/systemd/system/aicrm-hxc-daily-lessons-sync.service' "$installer" || { echo "installer must install the HXC daily lesson sync service" >&2; exit 1; }
grep -qx 'install -m 0644 "$release_dir/deploy/aicrm-hxc-daily-lessons-sync.timer" /etc/systemd/system/aicrm-hxc-daily-lessons-sync.timer' "$installer" || { echo "installer must install the HXC daily lesson sync timer" >&2; exit 1; }
if grep -qE '^AICRM_ROLE=' deploy/aicrm.env.example; then
  echo "the shared environment example must not assign a runtime role" >&2
  exit 1
fi
grep -qx 'WantedBy=multi-user.target' deploy/aicrm-effects-worker.service || { echo "effects worker must be persistently enableable" >&2; exit 1; }
for migration_contract in \
  '0005_external_effects.sql:External Effects' \
  '0006_wecom_callback_channel_acquisition.sql:WeCom callback acquisition' \
  '0007_media.sql:Media' \
  '0008_tag_catalog.sql:Tag catalog' \
  '0009_customer_activation.sql:customer activation' \
  '0010_product.sql:Product' \
  '0011_coupon_rules.sql:Coupon rules' \
  '0012_group_ops.sql:Group Ops' \
  '0013_automation_agents.sql:Automation agents' \
  '0014_operation_cycles.sql:Operation Cycle' \
  '0015_config_adminops.sql:Config/AdminOps' \
  '0016_media_content_packages.sql:Media content packages' \
  '0017_group_ops_history.sql:Group Ops history' \
  '0019_tag_catalog_sync_projection.sql:Tag catalog sync projection' \
  '0020_order.sql:Order' \
  '0021_payment.sql:Payment' \
  '0024_order_product_version.sql:order product version' \
  '0025_payment_reconciliation.sql:payment reconciliation' \
  '0026_identity_history_receipts.sql:identity history receipts' \
  '0027_admin_access_login_compat.sql:Admin access login compatibility' \
  '0028_hxc_dashboard.sql:HXC dashboard' \
  '0029_channel_center.sql:Channel center' \
  '0030_config_definition_import.sql:configuration definition import' \
  '0031_channel_history_import.sql:Channel history import' \
  '0032_channel_acquisition_assets.sql:Channel acquisition assets' \
  '0033_wecom_welcome_grants.sql:WeCom welcome grants' \
  '0034_channel_entrant_actions.sql:Channel entrant actions' \
  '0035_channel_acquisition_links.sql:Channel acquisition links' \
  '0036_ai_assistant_review.sql:AI Assistant review' \
  '0037_outbound_private_messages.sql:Outbound private messages' \
  '0038_survey_oauth_phone_vault.sql:survey OAuth phone vault' \
  '0049_order_history_attribution.sql:order history attribution' \
  '0053_segment_audience_member_event_fact_kinds.sql:Segment audience member event fact kind repair' \
  '0083_segment_audience_refresh_modes.sql:Segment audience refresh modes' \
  '0085_segment_audience_refresh_kind.sql:Segment audience refresh kind' \
  '0086_wecom_profile_primary_owner.sql:WeCom profile primary owner' \
  '0087_automation_manual_ai_review.sql:Automation manual AI review' \
  '0089_outbound_message_content_snapshots.sql:Outbound message content snapshots' \
  '0061_product_public_purchase.sql:product public purchase' \
  '0063_identity_hxc_source_observations.sql:HXC identity source observations' \
  '0064_hxc_dashboard_identity_v2.sql:HXC dashboard identity v2' \
  '0066_channel_welcome_intents.sql:Channel welcome intents' \
  '0150_channel_welcome_message_snapshots.sql:Channel welcome message snapshots' \
  '0151_access_role_governance.sql:Access role governance' \
  '0152_access_login_grants.sql:Access login grants' \
  '0153_wecom_customer_detail_projection.sql:WeCom customer detail projection' \
  '0155_group_ops_webhook_dynamic_executions.sql:Group Ops dynamic webhook executions' \
  '0156_distribution_profit_sharing_payment.sql:Payment profit sharing' \
  '0157_distribution_core.sql:Distribution core' \
  '0158_order_distribution_qualification_evidence.sql:Order distribution qualification evidence' \
  '0159_distribution_admin_receipts.sql:Distribution administrative receipts' \
  '0160_external_effect_system_control_actor.sql:External Effects system control actor' \
  '0161_payment_paid_confirmation_time.sql:Payment paid confirmation time' \
  '0164_channel_welcome_provider_rejections.sql:Channel welcome Provider rejection disposition' \
  '0162_payment_h5_distribution_return_path.sql:Payment H5 Distribution return path' \
  '0163_payment_profit_sharing_receiver_recovery.sql:Payment profit-sharing receiver recovery' \
  '0165_payment_profit_sharing_receiver_failure_class.sql:Payment profit-sharing receiver failure class' \
  '0166_payment_profit_sharing_instruction_failure_class.sql:Payment profit-sharing instruction failure class' \
  '0167_distribution_settlement_not_paid_exception.sql:Distribution settlement not-paid exception' \
  '0170_wecom_contact_description_effect.sql:WeCom contact description external effect' \
  '0171_wecom_contact_description_source_coverage.sql:WeCom contact description source coverage' \
  '0172_order_checkout_post_purchase_action.sql:Order checkout post-purchase action snapshot' \
  '0173_product_paid_purchase_action_target_snapshot.sql:Product paid purchase action target snapshot' \
  '0174_product_external_push_test_delivery_id.sql:Product external push test delivery ID' \
  '0175_customer_minimum_directory_projection.sql:Customer minimum directory projection' \
  '0176_survey_single_submission_claims.sql:Survey single submission claims' \
  '0177_survey_operation_legacy_parity.sql:Survey operation legacy parity' \
  '0181_hxc_dashboard_views.sql:Basic dashboard views and scoped sharing' \
  '0185_referral_core.sql:Referral campaign core' \
  '0186_adminops_inspections.sql:AdminOps persisted inspections' \
  '0187_adminops_notification_effect.sql:AdminOps notification external effect' \
  '0188_adminops_retention.sql:AdminOps retention' \
  '0189_owner_process_retention.sql:Owner process retention' \
  '0190_adminops_cpu_profiles.sql:CPU profile audit receipts' \
  '0191_payment_h5_referral_return_path.sql:Payment H5 Referral return path' \
  '0192_adminops_diagnostic_event_identity.sql:Diagnostic event identity' \
  '0193_adminops_governance_outcomes.sql:Governance outcomes' \
  '0067_survey_completion_snapshots.sql:Survey completion snapshots' \
  '0068_payment_session_beneficiary_selection.sql:payment session beneficiary selection' \
  '0069_coupon_claim_redemption_lifecycle.sql:coupon claim redemption lifecycle' \
  '0070_service_period_entitlement_fulfillment.sql:service-period entitlement fulfillment' \
  '0071_message_archive_core.sql:message archive core' \
  '0072_message_archive_migration_receipts.sql:message archive migration receipts' \
  '0073_survey_completion_test_push_snapshots.sql:Survey completion test push snapshots' \
  '0074_survey_external_operation_execution_facts.sql:Survey external operation execution facts' \
  '0075_external_effects_survey_completion_kind.sql:Survey completion external effect kind' \
  '0076_order_checkout_snapshots.sql:Order checkout snapshots' \
  '0077_coupon_public_slug.sql:coupon public slug' \
  '0078_group_ops_provider_tasks.sql:Group Ops provider tasks' \
  '0079_service_period_member_grid.sql:service-period member grid' \
  '0080_media_legacy_material_mappings.sql:AI Assistant legacy media mappings' \
  '0081_group_ops_webhook_unconfigured_reference.sql:Group Ops unconfigured webhook repair' \
  '0082_group_ops_history_import.sql:Group Ops history import' \
  '0084_hxc_shared_facts.sql:HXC shared legacy facts' \
  '0088_order_service_entitlement_alliance.sql:service-period alliance' \
  '0090_survey_oauth_state_redirect.sql:Survey OAuth redirect repair' \
  '0091_survey_assessment_business_keys.sql:Survey assessment business key repair' \
  '0093_customer_tag_commands.sql:Customer tag command runtime' \
  '0092_customer_owner_handoff.sql:Customer owner handoff runtime' \
  '0094_runtime_config_releases.sql:runtime Config releases' \
  '0095_product_external_push.sql:product external push' \
  '0096_open_platform.sql:Open Platform machine credentials' \
  '0097_segment_audience_mutation_actor.sql:Segment audience mutation actors' \
  '0098_message_archive_historical_projection.sql:Archive historical external projection' \
  '0099_survey_historical_external_projection.sql:Survey historical external projection' \
  '0100_ai_assistant_machine_actor.sql:AI Assistant machine actor' \
  '0124_operation_excel_batch_lifecycle.sql:Excel operation batch lifecycle'; do
  migration="${migration_contract%%:*}"
  label="${migration_contract#*:}"
  test -f "migrations/${migration}" || {
    echo "${label} migration must exist in the source release" >&2
    exit 1
  }
  grep -qx "test -f \"\$release_dir/migrations/${migration}\"" "$installer" || {
    echo "release must require the current ${label} migration filename" >&2
    exit 1
  }
done
grep -qx 'test -x "$release_dir/bin/migrate-automation-operations"' "$installer" || { echo "release must include Automation Operations migration tool" >&2; exit 1; }
grep -qx 'test -x "$release_dir/bin/migrate-access-role-convergence"' "$installer" || { echo "release must include the Access convergence tool" >&2; exit 1; }
grep -qF 'go build -trimpath -ldflags "-s -w" -o release/bin/migrate-access-role-convergence ./cmd/migrate-access-role-convergence' "$release_builder" || { echo "release must build the Access convergence tool" >&2; exit 1; }
grep -qx 'test -x "$release_dir/bin/check-enterprise-directory"' "$installer" || { echo "release must include the enterprise-directory preflight" >&2; exit 1; }
grep -qF 'go build -trimpath -ldflags "-s -w" -o release/bin/check-enterprise-directory ./cmd/check-enterprise-directory' "$release_builder" || { echo "release must build the enterprise-directory preflight" >&2; exit 1; }
for operation_runner_binary in aicrm-operation-cycle-runner aicrm-operation-cycle-result; do
  grep -qx "test -x \"\$release_dir/bin/${operation_runner_binary}\"" "$installer" || { echo "release must require ${operation_runner_binary}" >&2; exit 1; }
done
grep -qF 'go build -trimpath -ldflags "-s -w" -o release/bin/aicrm-operation-cycle-runner ./cmd/operation-cycle-runner' "$release_builder" || { echo "release workflow must build the OperationCycle runner" >&2; exit 1; }
grep -qF 'go build -trimpath -ldflags "-s -w" -o release/bin/aicrm-operation-cycle-result ./cmd/operation-cycle-result' "$release_builder" || { echo "release workflow must build the OperationCycle result client" >&2; exit 1; }
grep -qx 'test -x "$release_dir/bin/wecom-archive-sdk-runner"' "$installer" || { echo "release must include the WeCom archive SDK runner" >&2; exit 1; }
grep -qF 'scripts/build-wecom-archive-sdk-runner-linux.sh release/bin/wecom-archive-sdk-runner' "$release_builder" || { echo "release workflow must build the real Linux cgo archive runner" >&2; exit 1; }
grep -qF 'promote-staging-direct.sh' "$ci_workflow" || grep -qF 'deploy/promote-staging-release.sh' "$ci_workflow" || { echo "CI must promote the accepted staging package" >&2; exit 1; }
if grep -A 80 '^  deploy:$' "$ci_workflow" | grep -qF 'run-donor-view-consumers.sh release-fast'; then
  echo "production CI must not rebuild the release package" >&2
  exit 1
fi
grep -A 3 '^  deploy:$' "$ci_workflow" | grep -qF "vars.AICRM_ENABLE_ACTIONS_DEPLOY == 'true'" || { echo "Actions deployment must require the explicit repository opt-in" >&2; exit 1; }
grep -A 3 '^  deploy:$' "$ci_workflow" | grep -qF "vars.AICRM_CLOUD_DEPLOY_BREAKGLASS == 'true'" || { echo "Actions deployment must require the explicit break-glass opt-in" >&2; exit 1; }
grep -A 5 '^  deploy:$' "$ci_workflow" | grep -qF "needs.check.result == 'success'" || { echo "Actions deployment must remain gated by the complete CI check" >&2; exit 1; }
grep -qF 'CGO_ENABLED=1 GOOS=linux GOARCH=amd64 GOWORK=off' scripts/build-wecom-archive-sdk-runner-linux.sh || { echo "archive release runner must be a Linux amd64 cgo build" >&2; exit 1; }
grep -qF 'scripts/run-go-with-donor-views.sh scripts/build-wecom-archive-sdk-runner-linux.sh "$work/runner"' scripts/check-wecom-message-archive-sdk.sh || { echo "official SDK ABI check must exercise the release runner builder" >&2; exit 1; }
grep -qx 'test -x "$release_dir/bin/migrate-commerce-history"' "$installer" || { echo "release must reject a missing commerce history migration tool" >&2; exit 1; }
grep -qF 'go build -trimpath -ldflags "-s -w" -o release/bin/migrate-commerce-history ./cmd/migrate-commerce-history' "$release_builder" || { echo "CI must build the commerce history migration tool" >&2; exit 1; }
grep -qx 'test -x "$release_dir/bin/migrate-message-archive"' "$installer" || { echo "release must reject a missing message archive migration tool" >&2; exit 1; }
grep -qF 'go build -trimpath -ldflags "-s -w" -o release/bin/migrate-message-archive ./cmd/migrate-message-archive' "$release_builder" || { echo "CI must build the message archive migration tool" >&2; exit 1; }
canonical_backend_full_go_test || { echo "CI must test the message archive migration command through the canonical backend lane" >&2; exit 1; }
grep -qx 'test -x "$release_dir/bin/migrate-order-attribution"' "$installer" || { echo "release must include order history attribution tool" >&2; exit 1; }
grep -qF 'go build -trimpath -ldflags "-s -w" -o release/bin/migrate-order-attribution ./cmd/migrate-order-attribution' "$release_builder" || { echo "CI must build the order history attribution tool" >&2; exit 1; }
grep -qx 'test -x "$release_dir/bin/migrate-order-distribution-qualification"' "$installer" || { echo "release must include the reviewed historical qualification importer" >&2; exit 1; }
grep -qF 'go build -trimpath -ldflags "-s -w" -o release/bin/migrate-order-distribution-qualification ./cmd/migrate-order-distribution-qualification' "$release_builder" || { echo "CI must build the reviewed historical qualification importer" >&2; exit 1; }
grep -qx 'test -x "$release_dir/bin/migrate-referral-sales"' "$installer" || { echo "release must include the Referral sales backfill tool" >&2; exit 1; }
grep -qF 'go build -trimpath -ldflags "-s -w" -o release/bin/migrate-referral-sales ./cmd/migrate-referral-sales' "$release_builder" || { echo "release workflow must build the Referral sales backfill tool" >&2; exit 1; }
canonical_backend_full_go_test || { echo "CI must test the historical qualification importer through the canonical backend lane" >&2; exit 1; }
grep -qx 'test -f "$release_dir/migrations/0047_automation_operations_migration.sql"' "$installer" || { echo "release must require Automation Operations migration schema" >&2; exit 1; }
grep -qx 'test -f "$release_dir/migrations/0048_segment_audience_schedule_state.sql"' "$installer" || { echo "release must require Automation Operations schedule state" >&2; exit 1; }
config_migration="migrations/0015_config_adminops.sql"
for table in config_settings config_audits config_outbox adminops_release_projections adminops_diagnostic_snapshots; do
  grep -qE "^CREATE TABLE ${table}[[:space:]]*\\(" "$config_migration" || {
    echo "Config/AdminOps migration must define ${table}" >&2
    exit 1
  }
done
grep -qx 'test -x "$release_dir/bin/migrate-phone-identities"' "$installer" || { echo "release must include phone migration tool" >&2; exit 1; }
grep -qx 'test -x "$release_dir/bin/migrate-v2-config-definitions"' "$installer" || { echo "release must include configuration definition migration tool" >&2; exit 1; }
grep -qF 'go build -trimpath -ldflags "-s -w" -o release/bin/migrate-v2-config-definitions ./cmd/migrate-v2-config-definitions' "$release_builder" || { echo "CI must build the configuration definition migration tool" >&2; exit 1; }
grep -qx 'test -x "$release_dir/bin/migrate-v2-runtime-config-releases"' "$installer" || { echo "release must include runtime configuration history tool" >&2; exit 1; }
grep -qF 'go build -trimpath -ldflags "-s -w" -o release/bin/migrate-v2-runtime-config-releases ./cmd/migrate-v2-runtime-config-releases' "$release_builder" || { echo "CI must build the runtime configuration history tool" >&2; exit 1; }
grep -qx 'test -x "$release_dir/bin/migrate-v2-commerce-external-push-history"' "$installer" || { echo "release must include commerce external-push history tool" >&2; exit 1; }
grep -qF 'go build -trimpath -ldflags "-s -w" -o release/bin/migrate-v2-commerce-external-push-history ./cmd/migrate-v2-commerce-external-push-history' "$release_builder" || { echo "CI must build commerce external-push history tool" >&2; exit 1; }
grep -qx 'test -x "$release_dir/bin/migrate-open-platform"' "$installer" || { echo "release must include Open Platform history tool" >&2; exit 1; }
grep -qF 'go build -trimpath -ldflags "-s -w" -o release/bin/migrate-open-platform ./cmd/migrate-open-platform' "$release_builder" || { echo "CI must build the Open Platform history tool" >&2; exit 1; }
canonical_backend_full_go_test || { echo "CI must test the Open Platform history tool through the canonical backend lane" >&2; exit 1; }
grep -qx 'test -x "$release_dir/bin/migrate-media-legacy-materials"' "$installer" || { echo "release must include legacy Media mapping migration tool" >&2; exit 1; }
grep -qF 'go build -trimpath -ldflags "-s -w" -o release/bin/migrate-media-legacy-materials ./cmd/migrate-media-legacy-materials' "$release_builder" || { echo "CI must build the legacy Media mapping migration tool" >&2; exit 1; }
grep -qx 'test -x "$release_dir/bin/migrate-hxc-daily-lessons"' "$installer" || { echo "release must include the HXC daily lesson migration tool" >&2; exit 1; }
grep -qF 'go build -trimpath -ldflags "-s -w" -o release/bin/migrate-hxc-daily-lessons ./cmd/migrate-hxc-daily-lessons' "$release_builder" || { echo "CI must build the HXC daily lesson migration tool" >&2; exit 1; }
grep -qx 'test -x "$release_dir/bin/migrate-channel-history"' "$installer" || { echo "release must include channel history migration tool" >&2; exit 1; }
grep -qx 'test -x "$release_dir/bin/migrate-v2-customer-tag-history"' "$installer" || { echo "release must include customer tag history migration tool" >&2; exit 1; }
grep -qF 'go build -trimpath -ldflags "-s -w" -o release/bin/migrate-v2-customer-tag-history ./cmd/migrate-v2-customer-tag-history' "$release_builder" || { echo "release workflow must build customer tag history migration tool" >&2; exit 1; }
grep -qx 'test -x "$release_dir/bin/migrate-radar-v2"' "$installer" || { echo "release must include Radar migration tool" >&2; exit 1; }
grep -qF 'go build -trimpath -ldflags "-s -w" -o release/bin/migrate-radar-v2 ./cmd/migrate-radar-v2' "$release_builder" || { echo "release workflow must build Radar migration tool" >&2; exit 1; }
for page in radar radarDetail radarForm; do
  grep -qx "test -f \"\$release_dir/web/dist/admin/$page.html\"" "$installer" || { echo "release must include Radar UI page $page" >&2; exit 1; }
done
grep -qx 'test -x "$release_dir/bin/migrate-sidebar-history"' "$installer" || { echo "release must include sidebar history migration tool" >&2; exit 1; }
grep -qF 'go build -trimpath -ldflags "-s -w" -o release/bin/migrate-sidebar-history ./cmd/migrate-sidebar-history' "$release_builder" || { echo "release workflow must build sidebar history migration tool" >&2; exit 1; }
grep -qx 'test -x "$release_dir/bin/migrate-owner-handoff-history"' "$installer" || { echo "release must include owner handoff history migration tool" >&2; exit 1; }
grep -qF 'go build -trimpath -ldflags "-s -w" -o release/bin/migrate-owner-handoff-history ./cmd/migrate-owner-handoff-history' "$release_builder" || { echo "release workflow must build owner handoff history migration tool" >&2; exit 1; }
canonical_backend_full_go_test || { echo "CI must test owner handoff history migration command through the canonical backend lane" >&2; exit 1; }
grep -qx 'test -x "$release_dir/bin/bootstrap-automation-operations"' "$installer" || { echo "release must include Automation Operations semantic bootstrap" >&2; exit 1; }
grep -qF 'go build -trimpath -ldflags "-s -w" -o release/bin/bootstrap-automation-operations ./cmd/bootstrap-automation-operations' "$release_builder" || { echo "CI must build Automation Operations semantic bootstrap" >&2; exit 1; }
grep -qxF 'ExecStart=/opt/aicrm/current/bin/bootstrap-automation-operations' deploy/aicrm-automation-bootstrap.service || { echo "Automation Operations bootstrap unit must execute the release binary" >&2; exit 1; }
grep -qxF '  systemctl kill --kill-whom=all --signal=KILL aicrm-automation-bootstrap.service 2>/dev/null || true' "$installer" || { echo "deployment must terminate a stale Automation Operations bootstrap within the host lock" >&2; exit 1; }
grep -qxF '  if ! timeout 15s systemctl stop aicrm-automation-bootstrap.service; then' "$installer" || { echo "deployment must bound stale bootstrap stop confirmation" >&2; exit 1; }
# Busy releases must not terminate a runtime configuration or an unknown lock
# holder. The same acquired fd protects packages, configuration, and services.
grep -qxF 'if ! flock -w 15 9; then' "$installer" || { echo "release lock acquisition must be bounded" >&2; exit 1; }
grep -qxF '    inherited = os.fstat(9)' "$installer" && grep -qF '(expected.st_dev, expected.st_ino) == (inherited.st_dev, inherited.st_ino)' "$installer" || { echo "inherited release fd must reference the exact lock inode" >&2; exit 1; }
grep -qF 'runtime configuration recovery_required before release' "$installer" || { echo "unrecovered runtime configuration must block release" >&2; exit 1; }
if grep -qE 'terminate_stale_release_lock_holders|kill .*stale_(pid|child_pid)|/proc/\[0-9\]\*/fd' "$installer"; then
  echo "release may not kill lock holders to acquire the shared lock" >&2; exit 1
fi
lock_line="$(grep -nF 'if ! flock -w 15 9; then' "$installer" | cut -d: -f1)"
recovery_line="$(grep -nF 'runtime configuration recovery_required before release' "$installer" | cut -d: -f1)"
staging_line="$(grep -nF '  staging_dir="$(mktemp' "$installer" | cut -d: -f1)"
env_write_line="$(grep -nF '  survey_data_key=' "$installer" | cut -d: -f1)"
bootstrap_stop_line="$(grep -nF 'bootstrap_load_state=' "$installer" | cut -d: -f1)"
test "$recovery_line" -gt "$lock_line" && test "$staging_line" -gt "$recovery_line" && test "$env_write_line" -gt "$lock_line" && test "$bootstrap_stop_line" -gt "$lock_line" || { echo "package/configuration/service changes must follow lock and recovery checks" >&2; exit 1; }
grep -qxF 'if ! systemctl start aicrm-automation-bootstrap.service; then' "$installer" || { echo "deployment must run Automation Operations semantic bootstrap" >&2; exit 1; }
bootstrap_start_line="$(grep -nF 'if ! systemctl start aicrm-automation-bootstrap.service; then' "$installer" | cut -d: -f1)"
bootstrap_status_line="$(grep -nF '  systemctl status --no-pager --full aicrm-automation-bootstrap.service || true' "$installer" | tail -n 1 | cut -d: -f1)"
bootstrap_rollback_line="$(sed -n "$((bootstrap_start_line + 3))p" "$installer")"
test -n "$bootstrap_status_line" && test "$bootstrap_status_line" -gt "$bootstrap_start_line" && test "$bootstrap_rollback_line" = "  rollback" || { echo "bootstrap failure must emit service evidence before rollback" >&2; exit 1; }
grep -qx 'test -x "$release_dir/bin/migrate-identity-phone-vault"' "$installer" || { echo "release must include phone vault migration tool" >&2; exit 1; }
grep -qx 'test -f "$release_dir/release-files.sha256"' "$installer" || { echo "release must require its immutable file manifest" >&2; exit 1; }
for ai_assistant_asset in \
  list.html detail.html \
  send_content_readonly_detail.css send_content_readonly_detail.js \
  cloud_plan_review.js; do
  grep -qF 'test -f "$release_dir/web/dist/aiassistant/$ai_assistant_asset"' "$installer" || {
    echo "release installer must require AI Assistant UI assets" >&2
    exit 1
  }
done
# Unified components are installed at the same paths used by the shared Host.
grep -qF 'test -f "$release_dir/web/dist/assets/standard-components/$standard_component_asset"' "$installer" || { echo "installer must verify unified component assets" >&2; exit 1; }
grep -qF '  operation_member_picker.js' "$installer" || { echo "installer omits unified asset operation_member_picker.js" >&2; exit 1; }
grep -qF '  group_chat_picker.css' "$installer" || { echo "installer omits unified asset group_chat_picker.css" >&2; exit 1; }
grep -qF '  group_chat_picker.js' "$installer" || { echo "installer omits unified asset group_chat_picker.js" >&2; exit 1; }
grep -qF '  material_picker.css' "$installer" || { echo "installer omits unified asset material_picker.css" >&2; exit 1; }
grep -qF '  material_picker.js' "$installer" || { echo "installer omits unified asset material_picker.js" >&2; exit 1; }
grep -qF '  send_content_composer.css' "$installer" || { echo "installer omits unified asset send_content_composer.css" >&2; exit 1; }
grep -qF '  send_content_composer.js' "$installer" || { echo "installer omits unified asset send_content_composer.js" >&2; exit 1; }
grep -qF '  wecom_tag_picker.css' "$installer" || { echo "installer omits unified asset wecom_tag_picker.css" >&2; exit 1; }
grep -qF '  wecom_tag_picker.js' "$installer" || { echo "installer omits unified asset wecom_tag_picker.js" >&2; exit 1; }
grep -qF '  coupon_form.html' "$installer" || { echo "installer omits unified asset coupon_form.html" >&2; exit 1; }
grep -qF '  coupon_form_runtime.js' "$installer" || { echo "installer omits unified asset coupon_form_runtime.js" >&2; exit 1; }
grep -qF '  coupon_styles.html' "$installer" || { echo "installer omits unified asset coupon_styles.html" >&2; exit 1; }
grep -qF '  channel_code_form.html' "$installer" || { echo "installer omits unified asset channel_code_form.html" >&2; exit 1; }
grep -qF '  channel_admission_pages.js' "$installer" || { echo "installer omits unified asset channel_admission_pages.js" >&2; exit 1; }
grep -qF '  standard_components_host.js' "$installer" || { echo "installer omits unified asset standard_components_host.js" >&2; exit 1; }

grep -qx '(cd "$release_dir" && sha256sum --strict --check release-files.sha256)' "$installer" || { echo "existing releases must pass their complete file manifest before resume" >&2; exit 1; }
grep -qx 'chown -R root:root "$release_dir"' "$installer" || { echo "privileged release hooks must use root-owned packages" >&2; exit 1; }
grep -qx 'for control_dir in /opt/aicrm "$release_root"; do' "$installer" || { echo "root release hooks require sealed parent directories" >&2; exit 1; }
grep -q 'chown root:root "$control_dir"' "$installer" || { echo "release control directories must be root-owned" >&2; exit 1; }
grep -qx 'chmod -R go-w "$release_dir"' "$installer" || { echo "application users must not rewrite privileged hook code" >&2; exit 1; }
if grep -q 'chown -R aicrm:aicrm "$release_dir"' "$installer"; then
  echo "release must not return root hook code to the application owner" >&2; exit 1
fi
grep -qF 'mv -T "$staging_dir" "$release_dir"' "$installer" || { echo "new releases must become visible only after staged verification" >&2; exit 1; }
grep -qF 'sha256sum --strict --check release-files.sha256' "$release_builder" || { echo "CI must generate and verify the immutable release manifest" >&2; exit 1; }
grep -qx 'exec 9>"$release_lock"' "$installer" || { echo "installer must hold a host-side release lock" >&2; exit 1; }
grep -qx 'if ! flock -w 15 9; then' "$installer" || { echo "installer must serialize the release critical section with flock" >&2; exit 1; }
grep -qx 'release_run_number="${3:-}"' "$installer" || { echo "installer must accept the CI run number" >&2; exit 1; }
grep -qF 'last_successful_run_file=/opt/aicrm/last-successful-run-number' "$installer" || { echo "installer must retain the successful CI run marker" >&2; exit 1; }
grep -qF 'promote-staging-direct.sh' .github/workflows/ci.yml || grep -qF 'deploy/promote-staging-release.sh' .github/workflows/ci.yml || { echo "CI must promote through the reviewed staging gate" >&2; exit 1; }
grep -qF 'AICRM_SOURCE_BUNDLE' deploy/build-release-on-staging.sh || { echo "staging builds must require a local v4 source bundle" >&2; exit 1; }
grep -qF 'scripts/verify-staging-source.py --manifest' deploy/build-release-on-staging.sh || { echo "staging builds must verify signed source freshness" >&2; exit 1; }
grep -qF '/opt/aicrm/release-allowed-signers' deploy/build-release-on-staging-remote.sh || { echo "staging must use pinned allowed signers" >&2; exit 1; }
grep -qF 'staging-build.lock' deploy/build-release-on-staging-remote.sh && grep -qF 'flock -x' deploy/build-release-on-staging-remote.sh || { echo "staging builds must be single-flight" >&2; exit 1; }
grep -qF 'deploy/build-release-on-staging-remote.sh' deploy/build-release-on-staging.sh || { echo "staging build must use the reviewed remote builder" >&2; exit 1; }
if grep -qF 'git clone --filter=blob:none' deploy/build-release-on-staging.sh; then
  echo "staging build must not clone GitHub" >&2
  exit 1
fi
grep -qF 'tree_sha' deploy/promote-staging-direct.sh deploy/promote-staging-release.sh || { echo "promotion must compare the merged tree with the accepted staging tree" >&2; exit 1; }
grep -qF 'split -b 1m -a 4' deploy/upload-release-chunks.sh || { echo "release upload chunks must fit the slow production link attempt budget" >&2; exit 1; }
grep -qF 'timeout 300s scp' deploy/upload-release-chunks.sh || { echo "each release chunk upload must be time bounded" >&2; exit 1; }
grep -qF 'sha256sum --check --status' deploy/upload-release-chunks.sh || { echo "the reconstructed remote release must pass a SHA-256 check" >&2; exit 1; }
grep -qF 'run-release-as-root.sh' deploy/promote-staging-direct.sh deploy/promote-staging-release.sh scripts/deploy-release-local.sh || { echo "promotion must execute the root lock wrapper" >&2; exit 1; }
grep -qF 'if [[ "$0" == "/tmp/install-release-${release_sha}.sh" ]]; then' "$installer" || { echo "installer cleanup must be limited to its SHA-versioned path" >&2; exit 1; }
grep -qF 'AICRM_HXC_SOURCE_DSN: ${{ secrets.AICRM_HXC_SOURCE_DSN }}' .github/workflows/ci.yml || { echo "CI must read the HXC DSN from Actions secrets" >&2; exit 1; }
grep -qF 'AICRM_HXC_UNIONID_SCOPE: ${{ secrets.AICRM_HXC_UNIONID_SCOPE }}' .github/workflows/ci.yml || { echo "CI must read the HXC scope from Actions secrets" >&2; exit 1; }
grep -qF 'sudo /usr/bin/bash ${remote_configurer} ${remote_config} ${GITHUB_SHA}' .github/workflows/ci.yml || { echo "CI must apply HXC configuration through the audited runtime configurer" >&2; exit 1; }
grep -qF 'AICRM_WECHAT_PAY_H5_APP_ID: ${{ secrets.AICRM_WECHAT_PAY_H5_APP_ID }}' .github/workflows/ci.yml || { echo "CI must read the H5 OAuth AppID from Actions secrets" >&2; exit 1; }
grep -qF 'AICRM_WECHAT_PAY_H5_APP_SECRET: ${{ secrets.AICRM_WECHAT_PAY_H5_APP_SECRET }}' .github/workflows/ci.yml || { echo "CI must read the H5 OAuth AppSecret from Actions secrets" >&2; exit 1; }
grep -qF 'scp "${ssh_flags[@]}" deploy/configure-payment-h5-oauth-runtime.sh "${DEPLOY_USER}@${DEPLOY_HOST}:${remote_configurer}"' .github/workflows/ci.yml || { echo "CI must upload the audited H5 OAuth runtime configurer" >&2; exit 1; }
grep -qF 'sudo /usr/bin/bash ${remote_configurer} ${remote_config} ${GITHUB_SHA} skip-if-provider-disabled' .github/workflows/ci.yml || { echo "CI must explicitly skip H5 OAuth configuration only when payment is disabled" >&2; exit 1; }
scripts/test-configure-hxc-runtime.sh
scripts/test-configure-payment-h5-oauth-runtime.sh
scripts/test-install-release-ordering.sh

# PR164 ships the private new-shell document set as a release artifact. The
# installer must reject a partial stage before it can select current.
grep -qx 'test -f "$release_dir/web/dist/sidebar/index.html"' "$installer" || { echo "release must include the new shell sidebar document" >&2; exit 1; }
grep -qx '  test -f "$release_dir/web/dist/admin/${new_shell_page}.html"' "$installer" || { echo "release must verify each new shell admin document" >&2; exit 1; }
for new_shell_page in \
  agentEdit agents ai aiDetail apidocs attach audienceEdit automation campaigns \
  channelForm channels config configDetail couponData couponForm coupons customerDetail \
  customers cycles cyclesDetail funnel groupops groupopsDetail images index mpLib \
  orderDetail orders ownerMig productForm products questionnaireDetail questionnaireOps \
  questionnaires radar radarDetail radarForm spProductData spProductForm spProducts tags \
  wecom-tags; do
  grep -qE "^[[:space:]]*${new_shell_page}([[:space:]]+\\\\|; do)$" "$installer" || { echo "release must enumerate new shell document ${new_shell_page}" >&2; exit 1; }
done

grep -qxF 'test -f "$release_dir/deploy/publish-governance-releases.py"' "$installer" || { echo 'release requires governance fact publisher' >&2; exit 1; }
bridge_line="$(grep -nF 'if ! python3 "$release_dir/deploy/publish-governance-releases.py" --lock-fd 9; then' "$installer" | cut -d: -f1)"
[[ "$bridge_line" -gt "$receipt_line" ]] || { echo 'governance facts require a committed success receipt' >&2; exit 1; }
