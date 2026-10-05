#!/usr/bin/env bash
# Existing Audience contracts served by the supplied binary/Host in isolation.
set -euo pipefail
if [[ $# != 8 || "$1" != --binary || "$3" != --root || "$5" != --sha || "$7" != --binary-sha256 ]]; then
  echo 'usage: run-installed-audience.sh --binary /absolute/bin/aicrm --root /absolute/package --sha SHA --binary-sha256 SHA256' >&2
  exit 2
fi
export AICRM_INSTALLED_AUDIENCE_BINARY="$2" AICRM_INSTALLED_AUDIENCE_ROOT="$4" AICRM_INSTALLED_AUDIENCE_SHA="$6" AICRM_INSTALLED_AUDIENCE_BINARY_SHA256="$8"
python3 - <<'PY'
import os,re,sys,json,hashlib,subprocess
from pathlib import Path
from urllib.parse import urlparse,unquote
try:
    u=urlparse(os.environ.get('AICRM_DATABASE_URL',''))
    name=unquote(u.path[1:])
    if u.hostname not in ('localhost','127.0.0.1','::1') or not name.startswith('aicrm_test_') or not name.endswith('_acceptance_test'):raise ValueError()
    binary=Path(os.environ['AICRM_INSTALLED_AUDIENCE_BINARY']);root=Path(os.environ['AICRM_INSTALLED_AUDIENCE_ROOT'])
    head=os.environ['AICRM_INSTALLED_AUDIENCE_SHA'];digest=os.environ['AICRM_INSTALLED_AUDIENCE_BINARY_SHA256']
    if not re.fullmatch('[0-9a-f]{40}',head) or not re.fullmatch('[0-9a-f]{64}',digest):raise ValueError()
    if not binary.is_absolute() or not root.is_absolute() or not binary.is_file() or not os.access(binary,os.X_OK):raise ValueError()
    if hashlib.sha256(binary.read_bytes()).hexdigest()!=digest:raise ValueError()
    host_sha=json.loads((root/'web/dist/asset-manifest.json').read_text())['source_sha']
    if not isinstance(host_sha,str) or not re.fullmatch('[0-9a-f]{40}',host_sha):raise ValueError()
    if subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip()!=head or subprocess.check_output(['git','status','--porcelain'],text=True).strip():raise ValueError()
except Exception:
    sys.exit('requires exact clean V4 SHA, matching supplied binary/Host, and explicit loopback aicrm_test_*_acceptance_test PostgreSQL')
PY
export AICRM_REQUIRE_CHROMIUM_JOURNEY=1
exec bash scripts/run-go-with-donor-views.sh go test -json -p 1 -count=1 -timeout=8m \
  -run '^(TestPostgreSQLAudienceSendHistoryChromiumJourney|TestPostgreSQLAudienceConfirmationChromiumJourney|TestSurveyAudienceOwnerRetirementChromiumJourney)$' ./cmd/aicrm
