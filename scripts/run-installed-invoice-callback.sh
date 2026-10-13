#!/usr/bin/env bash
# Callback requests execute installed bytes, never a source handler or host.
set -euo pipefail
if [[ ( $# != 8 && $# != 14 ) || "$1" != --binary || "$3" != --root || "$5" != --sha || "$7" != --binary-sha256 ]]; then
  echo 'usage: run-installed-invoice-callback.sh --binary /absolute/bin/aicrm --root /absolute/package --sha SHA --binary-sha256 SHA256 [--host-source-sha SHA --host-manifest-sha256 SHA256 --source-repository /absolute/current-v4-repository]' >&2
  exit 2
fi
export AICRM_INSTALLED_PRODUCT_BINARY="$2" AICRM_INSTALLED_PRODUCT_ROOT="$4" AICRM_INSTALLED_PRODUCT_SHA="$6" AICRM_INSTALLED_PRODUCT_BINARY_SHA256="$8"
export AICRM_REQUIRE_INSTALLED_PRODUCT_CONTRACTS=1
if [[ $# == 14 ]]; then
  if [[ "$9" != --host-source-sha || "${11}" != --host-manifest-sha256 || "${13}" != --source-repository ]]; then
    echo 'invalid installed Host provenance arguments' >&2
    exit 2
  fi
  export AICRM_INSTALLED_PRODUCT_HOST_SOURCE_SHA="${10}" AICRM_INSTALLED_PRODUCT_HOST_MANIFEST_SHA256="${12}" AICRM_INSTALLED_PRODUCT_SOURCE_REPOSITORY="${14}"
fi
python3 - <<'PY'
import os, sys
from urllib.parse import urlparse, unquote
u = urlparse(os.environ.get('AICRM_DATABASE_URL', ''))
name = unquote(u.path[1:])
if u.hostname not in ('localhost', '127.0.0.1', '::1') or not name.startswith('aicrm_test_') or not name.endswith('_acceptance_test'):
    sys.exit('requires explicit loopback isolated aicrm_test_*_acceptance_test PostgreSQL')
PY
exec bash scripts/run-go-with-donor-views.sh go test -json -p 1 -count=1 -timeout=5m \
  -run '^TestInvoicePostgreSQL(CallbackBootstrapInstalled|BillEntryInstalled|SettingsInstalled)$' ./cmd/aicrm
