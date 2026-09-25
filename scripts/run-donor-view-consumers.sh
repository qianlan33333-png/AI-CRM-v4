#!/usr/bin/env bash
set -euo pipefail

# Compatibility name retained for callers; every command uses only the current
# AI-CRM-v3 checkout. No external repository, donor checkout, or donor SHA is used.
mode="${1:-}"
if [[ "$mode" == release || "$mode" == release-fast ]]; then
  if [[ "${GOOS:-}" != linux || "${GOARCH:-}" != amd64 ]]; then
    echo "release builds require GOOS=linux GOARCH=amd64" >&2
    exit 2
  fi
fi
npm ci --no-audit --no-fund
npm ci --prefix web/v3 --no-audit --no-fund
# Materialize only the source views already committed inside this v3 checkout;
# no network checkout or external repository is consulted.
node scripts/prepare-donor-source-views.mjs >/dev/null

run_frontend_and_stage_checks() {
  bash scripts/check-radar-boundaries.sh
  node scripts/generate-ai-assistant-client.mjs
  npm run typecheck
  npx tsc -p web/v3/tsconfig.json --noEmit
  node scripts/sidebar-wecom-jssdk-contract.mjs
  if [[ "$mode" == check ]]; then npm test; fi
  # Some frontend journeys install Hosts into dist. Rebuild the inexpensive
  # raw frontend before staging so the final package cannot contain test edits.
  npm run build
  node --test internal/webshell/static/admin_console/automation_create_code_adapter.test.mjs
  node internal/webshell/static/admin_console/admin_core_operations.test.mjs
  node internal/webshell/static/admin_console/admin_access_committed_search.test.mjs
  node internal/webshell/static/admin_console/survey_operations_frozen_runtime.test.mjs
  node web/v3/committedTextSearch.test.mjs
  node --test internal/webshell/chromium_launch.test.mjs
  node --test internal/webshell/chromium_binary.test.mjs
  node internal/webshell/owner_handoff_host.test.mjs
  node scripts/build-v3-host-adapters.mjs
  node --test web/v3/publicCommerceHost.test.mjs
  node scripts/groupops-host-adapter-e2e.mjs
  node web/v3/pageHeaderActions.test.mjs
  node web/v3/distributionAdmin.test.mjs
  node web/v3/referralCenter.test.mjs
  node web/v3/referralCenter.captainJourney.test.mjs
  node web/v3/referralAdmin.test.mjs
  node web/v3/shared/ui/selectionSession.test.mjs
  node web/v3/shared/ui/selectionDialog.test.mjs
  node web/v3/shared/ui/confirmationDialog.test.mjs
  node web/v3/shared/ui/tableActionMenu.test.mjs
  node web/v3/shared/ui/materialPickerAdapter.test.mjs
  node --test web/v3/shared/ui/contentPresentation.test.mjs
  node --test web/v3/shared/ui/contentComposer.test.mjs
  node web/v3/automationContentHost.test.mjs
  node web/v3/automationLifecycle.test.mjs
  node web/v3/shared/ui/groupPickerAdapter.test.mjs
  node web/v3/shared/ui/tagPickerAdapter.test.mjs
  node web/v3/shared/ui/staffPickerAdapter.test.mjs
  node web/v3/componentStatesHost.test.mjs
  node scripts/operation-cycles-shell-e2e.mjs
  node scripts/excel-batches-dom-test.mjs
  node scripts/ai-assistant-shell-e2e.mjs
  TZ=Asia/Shanghai node scripts/open-platform-host-e2e.mjs
  node scripts/order-host-adapter-e2e.mjs
  node web/v3/channelCenterAdapter.test.mjs
  node web/v3/hxcPresentation.test.mjs
  node web/v3/customerAdapter.test.mjs
  node web/v3/overviewAdmin.test.mjs
  node web/v3/governanceAdmin.test.mjs
  node web/v3/governanceOutcomes.test.mjs
  node web/v3/diagnosticFeedbackHost.test.mjs
  node web/v3/distributionCenter.test.mjs
  node web/v3/distributionCenter.lifecycle.test.mjs
  node web/v3/navigationHost.test.mjs
  node web/v3/adminSessionHost.test.mjs
  node web/v3/h5AuthAdapter.test.mjs
  node web/v3/surveyCompletionAction.test.mjs
  node web/v3/surveyPublicHost.test.mjs
  node web/v3/h5ControllerTime.test.mjs
  node web/v3/fieldMappingEditor.test.mjs
  node web/v3/productAdapter.field_mapping.test.mjs
  node web/v3/productAdapter.save_recovery.test.mjs
  node web/v3/productAdapter.material_order.test.mjs
  node web/v3/productAdapter.sp_material.test.mjs
  node web/v3/productAdapter.archive.test.mjs
  node web/v3/orderAdapter.test.mjs
  node web/v3/couponAdapter.test.mjs
  node web/v3/channelAdmissionHost.test.mjs
  node web/v3/standardComponentsRefresh.test.mjs
  node web/v3/materialSaveAdapter.test.mjs
  node web/v3/imageLibraryFilterHost.test.mjs
  node web/v3/materialLibraryPresentation.test.mjs
  node web/v3/actionFeedback.test.mjs
  node web/v3/productAdapter.upload_feedback.test.mjs
  node web/v3/radarAdapter.upload_feedback.test.mjs
  node web/v3/surfaceFeedbackHost.test.mjs
  node web/v3/memberGridFeedbackHost.test.mjs
  node web/v3/radarAdapter.test.mjs
  node web/v3/radarAdapter.exact_id.test.mjs
  node web/v3/radarAdapter.list_pagination.test.mjs
  node web/v3/radarAdapter.presentation.test.mjs
  TZ=UTC node web/v3/radarAdapter.visitors.test.mjs
  TZ=America/Los_Angeles node web/v3/radarAdapter.visitors.test.mjs
  node web/v3/sidebar_send_recovery.test.mjs
  node internal/webshell/static/admin_console/tag_sync_bridge.test.mjs
  node internal/webshell/static/admin_console/survey_share_guard_real_host.test.mjs
}

build_release_binaries() {
  mkdir -p release/bin
  go build -trimpath -ldflags "-s -w" -o release/bin/aicrm ./cmd/aicrm
  go build -trimpath -ldflags "-s -w" -o release/bin/aicrm-operation-cycle-runner ./cmd/operation-cycle-runner
  go build -trimpath -ldflags "-s -w" -o release/bin/aicrm-operation-cycle-result ./cmd/operation-cycle-result
  scripts/build-wecom-archive-sdk-runner-linux.sh release/bin/wecom-archive-sdk-runner
  go build -trimpath -ldflags "-s -w" -o release/bin/migrate-platform ./cmd/migrate-platform

  # This audited, one-time command is intentionally shipped with the release
  # that introduces 0151/0152. It is invoked only by the controlled host
  # release wrapper before the platform migrator runs.
  go build -trimpath -ldflags "-s -w" -o release/bin/migrate-access-role-convergence ./cmd/migrate-access-role-convergence
  go build -trimpath -ldflags "-s -w" -o release/bin/check-enterprise-directory ./cmd/check-enterprise-directory
  go build -trimpath -ldflags "-s -w" -o release/bin/migrate-river ./cmd/migrate-river
  go build -trimpath -ldflags "-s -w" -o release/bin/migrate-phone-identities ./cmd/migrate-phone-identities
  go build -trimpath -ldflags "-s -w" -o release/bin/migrate-identity-phone-vault ./cmd/migrate-identity-phone-vault
  go build -trimpath -ldflags "-s -w" -o release/bin/migrate-survey-v2 ./cmd/migrate-survey-v2
  go build -trimpath -ldflags "-s -w" -o release/bin/migrate-commerce-history ./cmd/migrate-commerce-history
  go build -trimpath -ldflags "-s -w" -o release/bin/migrate-message-archive ./cmd/migrate-message-archive
  go build -trimpath -ldflags "-s -w" -o release/bin/migrate-order-attribution ./cmd/migrate-order-attribution
  go build -trimpath -ldflags "-s -w" -o release/bin/migrate-order-distribution-qualification ./cmd/migrate-order-distribution-qualification
  go build -trimpath -ldflags "-s -w" -o release/bin/migrate-referral-sales ./cmd/migrate-referral-sales
  go build -trimpath -ldflags "-s -w" -o release/bin/migrate-automation-operations ./cmd/migrate-automation-operations
  go build -trimpath -ldflags "-s -w" -o release/bin/migrate-v2-config-definitions ./cmd/migrate-v2-config-definitions
  go build -trimpath -ldflags "-s -w" -o release/bin/migrate-v2-runtime-config-releases ./cmd/migrate-v2-runtime-config-releases
  go build -trimpath -ldflags "-s -w" -o release/bin/migrate-v2-commerce-external-push-history ./cmd/migrate-v2-commerce-external-push-history
  go build -trimpath -ldflags "-s -w" -o release/bin/migrate-open-platform ./cmd/migrate-open-platform
  go build -trimpath -ldflags "-s -w" -o release/bin/migrate-media-legacy-materials ./cmd/migrate-media-legacy-materials
  go build -trimpath -ldflags "-s -w" -o release/bin/migrate-hxc-daily-lessons ./cmd/migrate-hxc-daily-lessons
  go build -trimpath -ldflags "-s -w" -o release/bin/migrate-channel-history ./cmd/migrate-channel-history
  go build -trimpath -ldflags "-s -w" -o release/bin/migrate-v2-customer-tag-history ./cmd/migrate-v2-customer-tag-history
  go build -trimpath -ldflags "-s -w" -o release/bin/migrate-owner-handoff-history ./cmd/migrate-owner-handoff-history
  go build -trimpath -ldflags "-s -w" -o release/bin/migrate-radar-v2 ./cmd/migrate-radar-v2
  go build -trimpath -ldflags "-s -w" -o release/bin/migrate-sidebar-history ./cmd/migrate-sidebar-history
  go build -trimpath -ldflags "-s -w" -o release/bin/bootstrap-automation-operations ./cmd/bootstrap-automation-operations
}

# Pure compilation/staging is reusable by CI and deployment. Regression belongs
# to PR jobs, not the main-to-server path. Structural package checks stay here.
build_frontend() {
  node scripts/generate-ai-assistant-client.mjs
  npm run build
  node scripts/build-v3-host-adapters.mjs
}

stage_frontend() {
    mkdir -p release
    node scripts/stage-pr01-effects-ui.mjs web/dist release/web/dist
    node scripts/test-stage-pr01-effects-ui.mjs
    node scripts/test-groupops-history-release.mjs web/dist release/web/dist
    node scripts/stage-survey-ui.mjs web/dist release/web/dist
    node scripts/test-stage-survey-ui.mjs web/dist release/web/dist
    node scripts/stage-new-shell-ui.mjs web/dist release/web/dist
    node scripts/test-stage-new-shell-ui.mjs web/dist release/web/dist
}

case "$mode" in
  stage)
    build_frontend
    stage_frontend
    ;;
  release-fast)
    build_release_binaries
    build_frontend
    stage_frontend
    cp -R migrations deploy release/
    mkdir -p release/components/excel-batches
    cp components/excel-batches/batches.py components/excel-batches/requirements.txt components/excel-batches/aicrm-excel-batches.service release/components/excel-batches/
    (
      cd release
      LC_ALL=C find . -type f ! -name release-files.sha256 -print0 \
        | sort -z \
        | xargs -0 sha256sum > release-files.sha256
      sha256sum --strict --check release-files.sha256
    )
    archive="aicrm-${GITHUB_SHA:?GITHUB_SHA is required}.tar.gz"
    python3 scripts/create-release-archive.py release "$archive"
    python3 scripts/write-release-provenance.py "$archive" "${archive%.tar.gz}.provenance.json"
    ;;
  check)
    run_frontend_and_stage_checks
    stage_frontend
    ;;
  release)
    build_release_binaries
    run_frontend_and_stage_checks
    cp -R migrations deploy release/
    mkdir -p release/components/excel-batches
    cp components/excel-batches/batches.py components/excel-batches/requirements.txt components/excel-batches/aicrm-excel-batches.service release/components/excel-batches/
    node scripts/stage-pr01-effects-ui.mjs web/dist release/web/dist
    node scripts/test-stage-pr01-effects-ui.mjs
    node scripts/test-groupops-history-release.mjs web/dist release/web/dist
    node scripts/stage-survey-ui.mjs web/dist release/web/dist
    node scripts/test-stage-survey-ui.mjs web/dist release/web/dist
    node scripts/stage-new-shell-ui.mjs web/dist release/web/dist
    node scripts/test-stage-new-shell-ui.mjs web/dist release/web/dist
    (
      cd release
      LC_ALL=C find . -type f ! -name release-files.sha256 -print0 \
        | sort -z \
        | xargs -0 sha256sum > release-files.sha256
      sha256sum --strict --check release-files.sha256
    )
    archive="aicrm-${GITHUB_SHA:?GITHUB_SHA is required}.tar.gz"
    python3 scripts/create-release-archive.py release "$archive"
    python3 scripts/write-release-provenance.py "$archive" "${archive%.tar.gz}.provenance.json"
    ;;
  *)
    echo "usage: scripts/run-donor-view-consumers.sh check|stage|release|release-fast" >&2
    exit 2
    ;;
esac
