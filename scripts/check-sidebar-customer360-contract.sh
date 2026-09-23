#!/usr/bin/env bash
set -euo pipefail

donor_root="${AICRM_SIDEBAR_DONOR_DIR:-}"
if [[ -z "$donor_root" || ! -d "$donor_root/.git" ]]; then
  echo "AICRM_SIDEBAR_DONOR_DIR must point to the frozen AI-CRM checkout" >&2
  exit 1
fi

expected_commit="dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f"
[[ "$(git -C "$donor_root" rev-parse HEAD)" == "$expected_commit" ]]
cd "$donor_root"
shasum -a 256 --check <<'SUMS'
5fc78020b14805d4141d5f07a05af193811e1c282d36797ccd8efbf5289a3397  aicrm_next/app/admin_console/templates/sidebar_customer_workbench.html
a23120e4ce92a37b8001630615d6c42821a3e581e04edab4b52b488bce3d080b  aicrm_next/app/admin_console/static/sidebar_workbench/sidebar_workbench.css
f20515f3192f3a11048929c7c7b375e1ae274165ae173c0d8415735ffa25424d  aicrm_next/app/admin_console/static/sidebar_workbench/sidebar_workbench.js
48722c325a545c0f276aaa44a462adb64993a273b0fd7b1edbc27284eddd3914  aicrm_next/app/admin_console/static/sidebar_workbench/product-card-cover.png
SUMS

cd - >/dev/null
# The runnable document is generated from the frozen donor plus the V3 Host.
# Test the generated overlay itself; the old static workbench runtime is not a
# release dependency and must not become a false green contract target.
node scripts/sidebar-workbench-host-adapter-e2e.mjs
