#!/usr/bin/env bash
# Run existing recovery contracts against package bytes in an isolated process.
set -euo pipefail
if [[ $# != 8 || "$1" != --binary || "$3" != --root || "$5" != --sha || "$7" != --binary-sha256 ]]; then
  echo 'usage: run-installed-survey-recovery.sh --binary /absolute/bin/aicrm --root /absolute/package --sha SHA --binary-sha256 SHA256' >&2
  exit 2
fi
export AICRM_INSTALLED_SURVEY_BINARY="$2" AICRM_INSTALLED_SURVEY_ROOT="$4" AICRM_INSTALLED_SURVEY_SHA="$6" AICRM_INSTALLED_SURVEY_BINARY_SHA256="$8"
python3 - <<'PY'
import os,sys,re,subprocess,hashlib,json
from pathlib import Path
from urllib.parse import urlparse,unquote
if sys.platform!='linux':
    sys.exit('full installed OAuth recovery requires Linux process-scoped SSL_CERT_FILE trust; no system trust store is modified')
try:
    u=urlparse(os.environ.get('AICRM_DATABASE_URL',''))
    if u.hostname not in ('localhost','127.0.0.1','::1') or not unquote(u.path[1:]).endswith('_acceptance_test'): raise ValueError()
    head=os.environ['AICRM_INSTALLED_SURVEY_SHA'];digest=os.environ['AICRM_INSTALLED_SURVEY_BINARY_SHA256']
    binary=Path(os.environ['AICRM_INSTALLED_SURVEY_BINARY']);root=Path(os.environ['AICRM_INSTALLED_SURVEY_ROOT'])
    if not re.fullmatch('[0-9a-f]{40}',head) or not re.fullmatch('[0-9a-f]{64}',digest):raise ValueError()
    if not binary.is_absolute() or not root.is_absolute() or not binary.is_file() or not os.access(binary,os.X_OK):raise ValueError()
    if hashlib.sha256(binary.read_bytes()).hexdigest()!=digest:raise ValueError()
    if json.loads((root/'web/dist/asset-manifest.json').read_text())['source_sha']!=head:raise ValueError()
    if subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip()!=head or subprocess.check_output(['git','status','--porcelain'],text=True).strip():raise ValueError()
except Exception:
    sys.exit('requires a clean exact-SHA V4 source, matching binary/Host package and explicit local isolated acceptance_test PostgreSQL database')
PY
# Keep the existing editor lifecycle assertions against the exact package's
# document after final build/install. Source preparation does not build Host.
node -e 'if (process.version !== "v24.18.0") { console.error("installed Survey editor lifecycle requires Node 24.18.0"); process.exit(2); }'
node web/scripts/questionnaire-editor-publish-lifecycle.mjs
export AICRM_REQUIRE_CHROMIUM_JOURNEY=1
exec python3 scripts/dev_preflight.py browser \
  --journey TestPostgreSQLPublicSurveyReauthorizationChromiumJourney \
  --journey TestPostgreSQLPublicSurveyLostResponseChromiumJourney \
  --journey TestPostgreSQLPublicSurveyNativeInputRecoveryChromiumJourney \
  --journey TestPostgreSQLSurveyAttachmentChromiumJourney \
  --journey TestPostgreSQLSurveyEditorCreateChromiumJourney
