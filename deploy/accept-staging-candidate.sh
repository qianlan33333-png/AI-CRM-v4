#!/usr/bin/env bash
set -euo pipefail
manifest="${1:?candidate manifest}"; built="${2:?built receipt}"; out="${3:?accepted receipt}"; business_readback="${4:?authenticated business readback JSON}"
root="$(cd "$(dirname "$0")/.." && pwd)"; fixture="${AICRM_STAGING_FIXTURE_MANIFEST:-$root/scripts/staging-fixtures/customer-sync-and-research.json}"
readback="$(mktemp)"; trap 'rm -f "$readback" "${readback}.seed"' EXIT
AICRM_STAGING_DATABASE_URL="${AICRM_STAGING_DATABASE_URL:?AICRM_STAGING_DATABASE_URL is required}" \
  bash "$root/deploy/seed-staging-business-fixtures.sh" > "${readback}.seed"
python3 - "${readback}.seed" "$business_readback" "$readback" <<'PY'
import json,sys
seed=json.load(open(sys.argv[1])); actual=json.load(open(sys.argv[2]))
seed.update(actual)
json.dump(seed,open(sys.argv[3],'w'))
PY
python3 "$root/scripts/validate-staging-fixture-readback.py" "$readback" --fixture "$fixture" >/dev/null
python3 "$root/scripts/accept-staging-candidate.py" "$manifest" "$built" "$readback" "$out"
