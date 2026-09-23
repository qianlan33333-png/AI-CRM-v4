#!/usr/bin/env bash
set -euo pipefail
repository_root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$repository_root"

python3 scripts/check-architecture.py
node --check internal/webshell/static/admin_console/radar_oneid_bridge.js

if rg -n 'internal/(identity|media|customer)/(app|store|http|provider)' internal/radar --glob '*.go'; then
  echo 'Radar crossed a domain implementation boundary' >&2
  exit 1
fi
if rg -n 'Kind(OAOpenID|MPOpenID)|openid.*fallback|OpenID.*fallback' internal/radar/provider --glob '*.go' --glob '!**/*_test.go'; then
  echo 'Radar OAuth contains an OpenID fallback' >&2
  exit 1
fi
if rg -n '(unionid|openid|external_userid|phone|access_token|oauth_code)[[:space:]]+(TEXT|VARCHAR|BYTEA)' migrations/0050_radar_core.sql migrations/0051_radar_sessions_events.sql migrations/0052_radar_legacy_import.sql -i; then
  echo 'Radar schema contains a forbidden external identity value column' >&2
  exit 1
fi
echo 'Radar OneID and privacy boundaries verified'
