#!/usr/bin/env bash
set -euo pipefail

[[ "${AICRM_STAGING_ENVIRONMENT:-}" == staging ]] || { echo 'staging environment marker required' >&2; exit 2; }
[[ -n "${AICRM_STAGING_DATABASE_URL:-}" ]] || { echo 'AICRM_STAGING_DATABASE_URL required' >&2; exit 2; }
python3 - "$AICRM_STAGING_DATABASE_URL" <<'PY'
import sys
from urllib.parse import urlparse
value = urlparse(sys.argv[1])
if value.scheme not in ('postgres', 'postgresql') or value.hostname not in ('localhost', '127.0.0.1', '::1'):
    raise SystemExit('shipping acceptance requires a loopback staging database endpoint')
if not value.path.lstrip('/').endswith('_acceptance_test'):
    raise SystemExit('shipping acceptance requires a dedicated *_acceptance_test database')
PY
export AICRM_DATABASE_URL="$AICRM_STAGING_DATABASE_URL"
go test ./cmd/aicrm -run '^TestPostgreSQLShippingAddressSnapshotAndTransactionDetailAcceptance$' -count=1 -v
