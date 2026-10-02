#!/usr/bin/env bash
# Candidate-owned fixture driver; repair assertions execute installed bytes.
set -euo pipefail
if [[ $# != 2 || "$1" != --binary || "$2" != /* || ! -x "$2" ]]; then
  echo 'usage: run-installed-phone-canonical-recovery.sh --binary /absolute/installed/bin/migrate-collected-phones' >&2
  exit 2
fi
python3 - <<'PY'
import os,sys
from urllib.parse import urlparse,unquote
u=urlparse(os.environ.get('AICRM_DATABASE_URL',''))
if u.hostname not in ('localhost','127.0.0.1','::1') or not unquote(u.path[1:]).endswith('_acceptance_test'):
    sys.exit('requires an explicit local isolated PostgreSQL acceptance_test database')
PY
export AICRM_INSTALLED_PHONE_RECOVERY_BINARY="$2"
exec bash scripts/run-go-with-donor-views.sh go test -json -p 1 -count=1 -timeout=5m \
  -run '^TestPostgreSQLPhoneCanonicalRecoveryMaintenanceJourney$' ./cmd/aicrm
