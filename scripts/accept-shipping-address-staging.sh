#!/usr/bin/env bash
set -euo pipefail

[[ "${AICRM_STAGING_ENVIRONMENT:-}" == staging ]] || { echo 'staging environment marker required' >&2; exit 2; }
[[ -n "${AICRM_STAGING_DATABASE_URL:-}" ]] || { echo 'AICRM_STAGING_DATABASE_URL required' >&2; exit 2; }
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
AICRM_DATABASE_URL="$AICRM_STAGING_DATABASE_URL" python3 - "$SCRIPT_DIR" <<'PY'
import os
import sys
sys.path.insert(0, os.path.join(sys.argv[1], "ci"))
import quality_lanes

if not quality_lanes.is_local_test_database_url():
    raise SystemExit('shipping acceptance requires loopback PostgreSQL database aicrm_ci or aicrm_test_*')
if not quality_lanes.postgres_16_ready():
    raise SystemExit('shipping acceptance requires a reachable PostgreSQL 16 test database')
PY
export AICRM_DATABASE_URL="$AICRM_STAGING_DATABASE_URL"
go test ./cmd/aicrm -run '^TestPostgreSQLShippingAddressSnapshotAndTransactionDetailAcceptance$' -count=1 -v
