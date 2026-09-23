#!/usr/bin/env bash
# Read-only capture of the frozen legacy owner_migration_results rows. The
# output is intentionally not a V3 runtime dependency: feed it once to
# migrate-owner-handoff-history --mode=inspect-stream to create an AEAD
# protected snapshot before any V3 database write.
set -euo pipefail

output="${1:-}"
source_host="${AICRM_OWNER_HANDOFF_SOURCE_SSH_HOST:-}"
source_user="${AICRM_OWNER_HANDOFF_SOURCE_SSH_USER:-ubuntu}"
source_key="${AICRM_OWNER_HANDOFF_SOURCE_SSH_KEY_FILE:-}"
known_hosts="${AICRM_OWNER_HANDOFF_SOURCE_KNOWN_HOSTS_FILE:-}"
corp_scope="${AICRM_OWNER_HANDOFF_HISTORY_CORP_SCOPE:-}"

if [[ -z "$output" || -z "$source_host" || -z "$source_key" || -z "$known_hosts" || -z "$corp_scope" ]]; then
  echo "usage: capture-owner-handoff-history-source.sh OUTPUT with pinned source SSH and AICRM_OWNER_HANDOFF_HISTORY_CORP_SCOPE" >&2
  exit 2
fi
case "$source_host" in *[!A-Za-z0-9.-]*|"") echo "invalid source host" >&2; exit 2 ;; esac
if [[ ! "$corp_scope" =~ ^wecom-corp:[A-Za-z0-9._-]+$ ]]; then echo "invalid owner handoff corp scope" >&2; exit 2; fi
if [[ ! -s "$source_key" || ! -s "$known_hosts" ]]; then
  echo "source SSH material is unavailable" >&2
  exit 2
fi

umask 077
sql_file="$(mktemp)"
cleanup() { [[ ! -f "$sql_file" ]] || unlink "$sql_file"; }
trap cleanup EXIT

# The result-row query is shared with the PostgreSQL fixture. It preserves both
# rows_json ordinality and explicit empty result batches as source line 0.
query_file="$(cd "$(dirname "$0")" && pwd)/owner-handoff-history-source-query.sql"
test -s "$query_file" || { echo "owner handoff source query is unavailable" >&2; exit 2; }
cat > "$sql_file" <<SQL
BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;
SET LOCAL statement_timeout='15min';
SELECT '__AICRM_OWNER_HANDOFF_HISTORY__|' || to_char(transaction_timestamp() AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"');
SQL
sed "s/__CORP_SCOPE__/${corp_scope}/g" "$query_file" >> "$sql_file"
printf 'COMMIT;\n' >> "$sql_file"

ssh_flags=(-i "$source_key" -o BatchMode=yes -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes -o "UserKnownHostsFile=$known_hosts" -o ConnectTimeout=15)
ssh "${ssh_flags[@]}" "${source_user}@${source_host}" psql-stdin < "$sql_file" > "$output"
chmod 0600 "$output"
[[ "$(grep -c '__AICRM_OWNER_HANDOFF_HISTORY__|' "$output" || true)" == 1 ]]
echo "captured one consistent read-only owner handoff history stream"
