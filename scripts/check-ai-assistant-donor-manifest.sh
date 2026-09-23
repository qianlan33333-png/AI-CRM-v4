#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo_root"

shasum -a 256 -c docs/migration/ai-assistant/donor-sha256.txt

# The audit tools, immutable source index and its synthetic test record frozen
# source paths as development/build evidence. None is an application runtime
# consumer. Keep those exact files out of this runtime-import guard; every
# other script, canonical payload and application path remains scanned.
if rg -n --glob '!web/donors/ai-assistant-production/**' --glob '!web/donor-sources/source-index.json' --glob '!docs/**' --glob '!scripts/audit/**' --glob '!scripts/donor-source-views.test.mjs' --glob '!scripts/check-ai-assistant-donor-manifest.sh' --glob '!scripts/build-v3-host-adapters.mjs' --glob '!web/v3/radarAdapter.test.mjs' --glob '!web/v3/productAdapter.save_recovery.test.mjs' --glob '!web/v3/productAdapter.material_order.test.mjs' --glob '!web/v3/productAdapter.sp_material.test.mjs' 'web/donors/ai-assistant-production' .; then
  echo 'frozen AI Assistant donor imported directly by runtime code' >&2
  exit 1
fi

echo 'AI Assistant donor manifest: OK'
