#!/usr/bin/env bash
set -euo pipefail

repository_root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$repository_root"

require_text() {
  local pattern="$1" file="$2" message="$3"
  if command -v rg >/dev/null 2>&1; then
    rg -q -- "$pattern" "$file" || { echo "$message" >&2; exit 1; }
  else
    grep -Eq -- "$pattern" "$file" || { echo "$message" >&2; exit 1; }
  fi
}

scan_go() {
  local pattern="$1"
  shift
  if command -v rg >/dev/null 2>&1; then
    rg -n --glob '*.go' -- "$pattern" "$@"
  else
    grep -REn --include='*.go' -- "$pattern" "$@"
  fi
}

scan_go_non_test() {
  local pattern="$1"
  shift
  if command -v rg >/dev/null 2>&1; then
    rg -n --glob '*.go' --glob '!**/*_test.go' -- "$pattern" "$@"
  else
    grep -REn --include='*.go' --exclude='*_test.go' -- "$pattern" "$@"
  fi
}

python3 scripts/check-architecture.py
require_text 'ReadOnly:[[:space:]]*true' internal/hxcdashboard/provider/mysql.go 'HXC provider snapshot must be read-only'
require_text 'Isolation:[[:space:]]*sql.LevelRepeatableRead' internal/hxcdashboard/provider/mysql.go 'HXC provider snapshot must be repeatable-read'
require_text 'row.Phone = phone.String' internal/hxcdashboard/provider/mysql.go 'HXC provider must carry phone into the trusted source row'
require_text 'projection.Rows\[i\]\.UnionID = ""' internal/hxcdashboard/app/service.go 'HXC projection must erase raw UnionID'
require_text 'projection.Rows\[i\]\.Phone = ""' internal/hxcdashboard/app/service.go 'HXC projection must erase raw phone'
require_text 'AICRM_HXC_IDENTITY_WRITE_ENABLED' internal/platform/config/config.go 'HXC identity writes require an independent flag'
require_text 'AICRM_IDENTITY_OBSERVATION_VAULT_KEY' internal/platform/config/config.go 'HXC identity observations require a vault key'
require_text 'ciphertext BYTEA' migrations/0063_identity_hxc_source_observations.sql 'HXC identity observations must be encrypted'
require_text '连续两个完整成功快照' 'docs/17-PRD-HXC漏斗-OneID双键匹配与身份持久化.md' 'HXC retirement policy must remain documented'
require_text '--dbname="\$database_url"' deploy/rollout-hxc-identity-v2.sh 'HXC rollout must pass the database URL as a libpq connection string'
require_text 'DELETE FROM hxc_dashboard_rows WHERE projection_id IN' internal/hxcdashboard/store/postgres.go 'HXC retention must prune row payloads without deleting immutable projection lineage'

if grep -qF 'PGDATABASE="$database_url"' deploy/rollout-hxc-identity-v2.sh; then
  echo 'HXC rollout must not treat the database URL as a database name' >&2
  exit 1
fi
if grep -qF 'DELETE FROM hxc_dashboard_versions' internal/hxcdashboard/store/postgres.go; then
  echo 'HXC retention must not delete projection headers referenced by immutable refresh receipts' >&2
  exit 1
fi

if scan_go 'identity_source_(subjects|observations|conflicts|resolution_receipts)' internal/hxcdashboard; then
  echo 'HXC dashboard crossed the Identity table ownership boundary' >&2
  exit 1
fi
if scan_go 'ProvisionCustomer|\.Provision\(' internal/hxcdashboard internal/identity/app/hxc_source.go; then
  echo 'HXC identity flow must never provision a Customer' >&2
  exit 1
fi
if scan_go_non_test '(slog|log)\.[A-Za-z]+\([^\n]*(UnionID|unionid|Phone|phone)' internal/hxcdashboard internal/identity; then
  echo 'HXC identity values must not enter logs' >&2
  exit 1
fi
if grep -Ein '(unionid|phone)[[:space:]]+(TEXT|VARCHAR)' migrations/0064_hxc_dashboard_identity_v2.sql; then
  echo 'HXC projection schema contains a raw identity value column' >&2
  exit 1
fi

echo 'HXC dual-key OneID, persistence, and PII boundaries verified'

bash scripts/test-rollout-hxc-identity-v2-contract.sh
