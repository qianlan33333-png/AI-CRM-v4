#!/usr/bin/env bash
# Reuse the current installed-API fixture and command against isolated data.
set -euo pipefail
if [[ $# != 10 || "$1" != --binary || "$3" != --root || "$5" != --sha || "$7" != --binary-sha256 || "$9" != --tool ]]; then
  echo 'usage: run-installed-external-order-history.sh --binary /absolute/bin/aicrm --root /absolute/package --sha SHA --binary-sha256 SHA256 --tool /absolute/bin/migrate-external-orders' >&2
  exit 2
fi
export AICRM_INSTALLED_AUDIENCE_BINARY="$2" AICRM_INSTALLED_AUDIENCE_ROOT="$4" AICRM_INSTALLED_AUDIENCE_SHA="$6" AICRM_INSTALLED_AUDIENCE_BINARY_SHA256="$8" AICRM_INSTALLED_EXTERNAL_HISTORY_BINARY="${10}"
python3 - <<'PY'
import os,sys,hashlib
from pathlib import Path
from urllib.parse import urlparse,unquote
u=urlparse(os.environ.get('AICRM_DATABASE_URL',''))
binary=Path(os.environ['AICRM_INSTALLED_EXTERNAL_HISTORY_BINARY'])
root=Path(os.environ['AICRM_INSTALLED_AUDIENCE_ROOT'])
if u.hostname not in ('localhost','127.0.0.1','::1') or not unquote(u.path[1:]).startswith('aicrm_test_') or not unquote(u.path[1:]).endswith('_acceptance_test'):
    sys.exit('requires explicit loopback isolated aicrm_test_*_acceptance_test PostgreSQL')
if not binary.is_absolute() or not binary.is_file() or not os.access(binary,os.X_OK) or binary.resolve()!= (root/'bin/migrate-external-orders').resolve():
    sys.exit('requires the migration tool inside the exact installed package')
print('installed_external_history_tool_sha256='+hashlib.sha256(binary.read_bytes()).hexdigest())
PY
export AICRM_REQUIRE_CHROMIUM_JOURNEY=1
exec bash scripts/run-go-with-donor-views.sh go test -json -p 1 -count=1 -timeout=8m \
  -run '^TestPostgreSQLExternalOrderHistoryChromiumJourney$' ./cmd/aicrm
