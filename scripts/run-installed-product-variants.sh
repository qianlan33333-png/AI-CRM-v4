#!/usr/bin/env bash
# Same installed Product fixture; disposable loopback PG, no real Provider.
set -euo pipefail
if [[ $# != 8 || "$1" != --binary || "$3" != --root || "$5" != --sha || "$7" != --binary-sha256 ]]; then
  echo 'usage: run-installed-product-variants.sh --binary /absolute/bin/aicrm --root /absolute/package --sha SHA --binary-sha256 SHA256' >&2
  exit 2
fi
export AICRM_INSTALLED_PRODUCT_BINARY="$2" AICRM_INSTALLED_PRODUCT_ROOT="$4" AICRM_INSTALLED_PRODUCT_SHA="$6" AICRM_INSTALLED_PRODUCT_BINARY_SHA256="$8"
python3 - <<'PY'
import os,sys
from urllib.parse import urlparse,unquote
u=urlparse(os.environ.get('AICRM_DATABASE_URL',''))
if u.hostname not in ('localhost','127.0.0.1','::1') or not unquote(u.path[1:]).startswith('aicrm_test_') or not unquote(u.path[1:]).endswith('_acceptance_test'):
    sys.exit('requires explicit loopback isolated aicrm_test_*_acceptance_test PostgreSQL')
PY
export AICRM_REQUIRE_CHROMIUM_JOURNEY=1
exec bash scripts/run-go-with-donor-views.sh go test -json -p 1 -count=1 -timeout=4m -run '^TestPostgreSQLProductVariantsChromiumJourney$' ./cmd/aicrm
