#!/usr/bin/env bash
set -euo pipefail

output="${1:-}"
source_host="${AICRM_SIDEBAR_SOURCE_SSH_HOST:-}"
source_user="${AICRM_SIDEBAR_SOURCE_SSH_USER:-ubuntu}"
source_key="${AICRM_SIDEBAR_SOURCE_SSH_KEY_FILE:-}"
known_hosts="${AICRM_SIDEBAR_SOURCE_KNOWN_HOSTS_FILE:-}"

if [[ -z "$output" || -z "$source_host" || -z "$source_key" || -z "$known_hosts" ]]; then
  echo "usage: capture-sidebar-history-source.sh OUTPUT with pinned source SSH environment" >&2
  exit 2
fi
case "$source_host" in *[!A-Za-z0-9.-]*|"") echo "invalid source host" >&2; exit 2 ;; esac
if [[ ! -s "$source_key" || ! -s "$known_hosts" ]]; then
  echo "source SSH material is unavailable" >&2
  exit 2
fi

umask 077
sql_file="$(mktemp)"
cleanup() { [[ ! -f "$sql_file" ]] || unlink "$sql_file"; }
trap cleanup EXIT

cat > "$sql_file" <<'SQL'
BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;
SET LOCAL statement_timeout='15min';
SELECT '__AICRM_SIDEBAR_SNAPSHOT__|' || to_char(transaction_timestamp() AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"');
SELECT '__AICRM_SIDEBAR_ENTITLEMENT__|' || encode(convert_to((jsonb_build_object(
  'source_id',e.id,
  'unionid',e.unionid,
  'service_product_id',e.service_product_id,
  'product_name',COALESCE(NULLIF(p.name,''),p.product_code),
  'status',e.status,
  'start_at',e.start_at,
  'end_at',e.end_at,
  'remark',COALESCE(NULLIF(e.metadata_json->>'admin_remark',''),NULLIF(e.metadata_json->>'remark',''),''),
  'created_at',e.created_at,
  'updated_at',e.updated_at
) || CASE
  -- Keep source presence and JSON type. The v2 Go capture normalizes strings
  -- with the frozen Python str(...).strip() rule; PostgreSQL BTRIM is ASCII
  -- only and therefore must not be presented as an equivalent conversion.
  -- A missing key remains absent, JSON null remains unknown, and a non-string
  -- reaches the protected parser and stops capture rather than being guessed.
  WHEN e.metadata_json ? 'admin_alliance'
    THEN jsonb_build_object('alliance', e.metadata_json->'admin_alliance')
  ELSE '{}'::jsonb
END)::text,'UTF8'),'hex')
FROM public.service_period_entitlements e
JOIN public.wechat_pay_products p ON p.id=e.trade_product_id
WHERE e.tenant_id='aicrm' AND e.status IN ('active','expired','refunded')
ORDER BY e.id;
SELECT '__AICRM_SIDEBAR_COUPON__|' || encode(convert_to(jsonb_build_object(
  'source_id',c.id,
  'unionid',c.unionid,
  'coupon_id',c.coupon_id,
  'status',c.status,
  'claimed_at',c.claimed_at,
  'valid_from',c.valid_from,
  'valid_until',c.valid_until,
  'redeemed_at',c.consumed_at,
  'created_at',c.created_at,
  'updated_at',c.updated_at
)::text,'UTF8'),'hex')
FROM public.commerce_coupon_claims c
WHERE c.tenant_id='aicrm'
ORDER BY c.id;
COMMIT;
SQL

ssh_flags=(-i "$source_key" -o BatchMode=yes -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes -o "UserKnownHostsFile=$known_hosts" -o ConnectTimeout=15)
ssh "${ssh_flags[@]}" "${source_user}@${source_host}" psql-stdin < "$sql_file" > "$output"
chmod 0600 "$output"
[[ "$(grep -c '__AICRM_SIDEBAR_SNAPSHOT__|' "$output" || true)" == 1 ]]
echo "captured one consistent read-only sidebar history snapshot stream"
