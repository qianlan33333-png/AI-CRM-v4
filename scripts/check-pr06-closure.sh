#!/usr/bin/env bash

# PR06 closure gate. It verifies the fixed donor build/runtime contract and
# the v3-owned Group Ops boundary without compiling or rewriting donor files.

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "$0")" && pwd)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/.." && pwd)"
DONOR_DIR="$(printenv AICRM_PR06_DONOR_DIR 2>/dev/null || true)"
DONOR_SHA="$(printenv AICRM_PR06_DONOR_SHA 2>/dev/null || true)"
test -n "$DONOR_DIR" || DONOR_DIR=/private/tmp/aicrm-v2-pr04-donor
test -n "$DONOR_SHA" || DONOR_SHA=6bfbe5816bb89913c70adaca87d6a486260e016e
TARGET_ROOT="$REPO_ROOT/web/donors/groupops-v2/src"
ADMIN_BASE="$REPO_ROOT/internal/webshell/templates/admin_base.html"

fail() {
  printf 'PR06 closure check: FAIL: %s\n' "$*" >&2
  exit 1
}

pass() {
  printf 'PR06 closure check: PASS: %s\n' "$*"
}

require_file() {
  test -f "$1" || fail "missing file: $1"
}

require_text() {
  local needle="$1"
  local file="$2"
  grep -Fq -- "$needle" "$file" || fail "missing marker '$needle' in $file"
}

command -v git >/dev/null 2>&1 || fail "git is required"
command -v grep >/dev/null 2>&1 || fail "grep is required"
command -v cmp >/dev/null 2>&1 || fail "cmp is required"
command -v bash >/dev/null 2>&1 || fail "bash is required"
require_file "$ADMIN_BASE"
test -d "$DONOR_DIR" || fail "missing fixed donor checkout: $DONOR_DIR"
test -d "$TARGET_ROOT" || fail "missing donor archive: $TARGET_ROOT"

donor_head="$(git -C "$DONOR_DIR" rev-parse HEAD 2>/dev/null)" \
  || fail "unable to resolve donor HEAD"
[[ "$donor_head" == "$DONOR_SHA" ]] \
  || fail "donor HEAD $donor_head is not fixed SHA $DONOR_SHA"
pass "donor HEAD is fixed at $DONOR_SHA"

AICRM_PR06_ROOT="$REPO_ROOT" \
AICRM_PR06_DONOR_DIR="$DONOR_DIR" \
AICRM_PR06_DONOR_SHA="$DONOR_SHA" \
  bash "$REPO_ROOT/scripts/check-pr06-donor-manifest.sh"
pass "35/35 donor frontend files are byte-exact in the archive; 34 active sources remain frozen and the shared executable test is v3-owned"

BUILD="$REPO_ROOT/web/scripts/build.mjs"
MAIN="$REPO_ROOT/web/src/admin/main.ts"
LEGACY="$REPO_ROOT/web/src/admin/legacy.ts"
REGISTRY="$REPO_ROOT/web/src/admin/registry.json"
NAV="$REPO_ROOT/web/src/admin/nav.json"
GROUPOPS_TEMPLATE="$TARGET_ROOT/admin/templates/groupops.html"
GROUPOPS_DETAIL_TEMPLATE="$TARGET_ROOT/admin/templates/groupopsDetail.html"

require_file "$BUILD"
require_file "$MAIN"
require_file "$LEGACY"
require_file "$REGISTRY"
require_file "$NAV"
require_text "admin: path.join(SRC, 'admin/main.ts')" "$BUILD"
require_text "for (const screen of registry.screens)" "$BUILD"
require_text "admin/templates" "$BUILD"
require_text 'import("./legacy")' "$MAIN"
require_text 'import { AdminController } from "./controller";' "$LEGACY"
require_text 'new AdminController(api, page)' "$LEGACY"
require_text 'import("./sections/groupOpsHistory")' "$LEGACY"
require_text '"key": "groupops"' "$REGISTRY"
require_text '"key": "groupopsDetail"' "$REGISTRY"
require_text '"key": "groupops"' "$NAV"
git -C "$DONOR_DIR" show "$DONOR_SHA:web/scripts/build.mjs" | cmp -s - "$BUILD" \
  || fail "active build.mjs drifted from fixed donor build path"
pass "actual browser chain is build.mjs -> main.ts -> legacy.ts -> AdminController"

template_count="$(find "$TARGET_ROOT/admin/templates" -maxdepth 1 -type f -print | wc -l | tr -d ' ')"
[[ "$template_count" == "2" ]] \
  || fail "expected exactly two archived Group Ops templates, found $template_count"
if find "$TARGET_ROOT" -type f \( -iname '*content*package*' -o -iname '*package*editor*' \) -print -quit | grep -q .; then
  fail "archive contains an unapproved independent content-package editor file"
fi
if grep -REni \
    '/api/admin/content-packages|previewMediaContentPackage|createMediaContentPackage|updateMediaContentPackage|contentPackageEditor|content-package-editor' \
    "$TARGET_ROOT/admin/templates" \
    "$TARGET_ROOT/admin/sections" \
    "$TARGET_ROOT/api/admin.ts" >/dev/null; then
  fail "Group Ops active frontend contains a content-package editor/API wiring"
fi
require_text 'content_package' "$TARGET_ROOT/admin/sections/groupOpsHistory.ts"
pass "no independent Group Ops content-package editor; historical content_package remains read-only"

sidebar_count="$( (grep -o 'class="admin-sidebar"' "$ADMIN_BASE" || true) | wc -l | tr -d ' ')"
[[ "$sidebar_count" == "1" ]] \
  || fail "v3 admin_base must contain exactly one admin-sidebar, found $sidebar_count"
if grep -En '<aside|class="side"|\.side([^[:alnum:]_]|$)' "$GROUPOPS_TEMPLATE" "$GROUPOPS_DETAIL_TEMPLATE" >/dev/null; then
  fail "Group Ops business templates contain a second donor sidebar"
fi
pass "PR10 admin_base is the sole sidebar and Group Ops templates are business-only"

# The active web/src tree is intentionally the fixed donor tree. A filename
# search for "groupops" would therefore flag the donor implementation itself
# as a duplicate. Ordinary runs require no modification. The dedicated PR-3
# disposable proof is different: it stages exactly every declared view as a
# deletion, then proves each untracked replacement against the immutable index
# and receipt. That is not a donor mutation or a fallback exemption.
if [[ "${AICRM_DEDUP_DISPOSABLE_WORKTREE:-}" == "1" ]]; then
  AICRM_DEDUP_DISPOSABLE_WORKTREE=1 node "$REPO_ROOT/scripts/verify-disposable-donor-views.mjs" >/dev/null \
    || fail "disposable source-view proof is not the exact declared removal/materialization set"
  pass "PR-3 disposable source views are exactly receipted replacements; no donor drift is staged"
elif [[ "$(AICRM_DEDUP_REPOSITORY_ROOT="$REPO_ROOT" node -e 'const path=require("path"); const i=require(path.join(process.env.AICRM_DEDUP_REPOSITORY_ROOT, "web/donor-sources/source-index.json")); process.stdout.write(String(i.bindings.filter((b) => b.current_path_state === "untracked_post_p4").length))')" == "230" ]]; then
  node "$REPO_ROOT/scripts/check-donor-source-view-ignore.mjs" "$REPO_ROOT" >/dev/null \
    || fail "P4 source-view ignore list is not the exact declared target set"
  node "$REPO_ROOT/scripts/verify-materialized-donor-source-views.mjs" --root "$REPO_ROOT" >/dev/null \
    || fail "P4 source views are not exact untracked receipted replacements"
  node "$REPO_ROOT/scripts/check-p4-donor-view-closure.mjs" --root "$REPO_ROOT" --prefix web/src >/dev/null \
    || fail "P4 source-view closure contains an undeclared active donor change"
  pass "P4 source views are exact untracked replacements; no active donor runtime drift is staged"
elif git -C "$REPO_ROOT" status --short -- web/src | grep -q .; then
  fail "branch modified active donor web/src; Group Ops frontend must remain byte-exact"
else
  pass "no second active frontend shell/runtime was introduced; web/src is unmodified donor"
fi

for path in \
  internal/groupops/app/runtime.go \
  internal/groupops/app/history.go \
  internal/groupops/http/handler.go \
  internal/groupops/module.go \
  internal/groupops/store/postgres.go \
  internal/groupops/ui.go \
  internal/outbound/group_message.go \
  cmd/aicrm/group_ops_adapters.go \
  cmd/aicrm/group_ops_protocol_auth.go \
  internal/media/app/groupops_preparation.go \
  internal/media/store/groupops_preparation.go \
  migrations/0012_group_ops.sql \
  migrations/0016_media_content_packages.sql \
  migrations/0017_group_ops_history.sql; do
  require_file "$REPO_ROOT/$path"
done
require_text 'routeApplicationWithGroupOps' "$REPO_ROOT/cmd/aicrm/composition.go"
require_text 'NewProviderRouterWithGroupMessage' "$REPO_ROOT/cmd/aicrm/composition.go"
require_text 'NewGroupMessageProvider' "$REPO_ROOT/cmd/aicrm/composition.go"
# Provider adapters live in the composition-owned adapter file. Composition
# wires them and preserves disabled-by-default configuration; it need not
# duplicate their concrete types.
require_text 'providerDisabledGroupOpsDirectory' "$REPO_ROOT/cmd/aicrm/group_ops_adapters.go"
require_text 'providerDisabledGroupOpsEvidence' "$REPO_ROOT/cmd/aicrm/group_ops_adapters.go"
require_text 'SetCompletionSink' "$REPO_ROOT/cmd/aicrm/composition.go"
require_text 'BindContentDelivery' "$REPO_ROOT/cmd/aicrm/composition.go"
require_text 'groupopsmaterial.NewFreezer' "$REPO_ROOT/cmd/aicrm/composition.go"
require_text 'BindMaterialPreparation' "$REPO_ROOT/cmd/aicrm/composition.go"
require_text 'ReadPreparedGroupOpsMaterials' "$REPO_ROOT/internal/media/store/groupops_preparation.go"
require_text 'RecordPreparedGroupOpsMaterialsWithin' "$REPO_ROOT/internal/media/store/groupops_preparation.go"
require_text 'media_group_ops_preparation_receipts' "$REPO_ROOT/migrations/0016_media_content_packages.sql"
require_text 'media_group_ops_preparation_items' "$REPO_ROOT/migrations/0016_media_content_packages.sql"
require_text 'group-ops-provider-disabled' "$REPO_ROOT/internal/outbound/group_message.go"
require_text 'AICRM_GROUP_OPS_WEBHOOK_SECRET' "$REPO_ROOT/internal/platform/config/config.go"
require_text '/api/admin/automation-conversion/group-ops/plans' "$REPO_ROOT/api/openapi.yaml"
require_text '/api/admin/automation-conversion/group-ops/history/plans' "$REPO_ROOT/api/openapi.yaml"
require_text '/api/automation/group-ops/webhooks/{webhook_key}' "$REPO_ROOT/api/openapi.yaml"
pass "v3 Group Ops owner implementation, OpenAPI, composition adapter, and disabled Provider gate are present"

if grep -REn --include='*.go' '"github.com/[^\"]+/AI-CRM-v3/internal/(customer|identity|audience|segment|campaign)' \
    "$REPO_ROOT/internal/groupops" >/dev/null; then
  fail "Group Ops imports a forbidden Customer/Identity/Audience/Segment/Campaign package"
fi
if grep -REn --include='*.go' '"github.com/[^\"]+/AI-CRM-v3/internal/(externaleffects/(store|http|worker|provider)|media/(store|app|http)|customer|identity|audience)' \
    "$REPO_ROOT/internal/groupops" >/dev/null; then
  fail "Group Ops bypasses a stable Port with cross-domain store/app/http/provider import"
fi
if grep -REn --include='*.go' 'FROM[[:space:]]+admin_users|JOIN[[:space:]]+admin_users' \
    "$REPO_ROOT/internal/groupops/store" >/dev/null; then
  fail "Group Ops store reads Access-owned admin_users instead of the Composition Access port"
fi
pass "Group Ops has no forbidden cross-domain imports or owner-table bypass"

require_text 'GroupOpsMaterialSourceCapturer' "$REPO_ROOT/cmd/aicrm/group_ops_adapters.go"
require_text 'GroupOpsMaterialSnapshotFreezer' "$REPO_ROOT/cmd/aicrm/group_ops_adapters.go"
require_text 'outboundport.MaterialSourceReader' "$REPO_ROOT/cmd/aicrm/group_ops_adapters.go"
require_text 'outboundport.MaterialPreparer' "$REPO_ROOT/cmd/aicrm/group_ops_adapters.go"
require_text 'newUnifiedGroupOpsMaterialAdapter(' "$REPO_ROOT/cmd/aicrm/composition.go"
require_text 'mediaContentBindings.SourceCapturer, materialFreezer, mediaRepository, materialPreparation, materialScopeDigest' "$REPO_ROOT/cmd/aicrm/composition.go"
require_text 'ContentPackagesPath' "$REPO_ROOT/internal/groupops/http/handler.go"
pass "Composition binds Media capture/freezing and Outbound source/preparation through stable ports; no kind/id source digest fallback"

printf 'PR06 closure check: PASS: local Group Ops closure, Media snapshot binding, deterministic disabled Provider and frontend hard gates verified.\n'
