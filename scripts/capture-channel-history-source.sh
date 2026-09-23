#!/usr/bin/env bash
set -euo pipefail

output="${1:-}"
source_host="${AICRM_CHANNEL_SOURCE_SSH_HOST:-}"
source_user="${AICRM_CHANNEL_SOURCE_SSH_USER:-ubuntu}"
source_key="${AICRM_CHANNEL_SOURCE_SSH_KEY_FILE:-}"
known_hosts="${AICRM_CHANNEL_SOURCE_KNOWN_HOSTS_FILE:-}"

if [[ -z "$output" || -z "$source_host" || -z "$source_key" || -z "$known_hosts" ]]; then
  echo "usage: capture-channel-history-source.sh OUTPUT with source SSH environment" >&2
  exit 2
fi
case "$source_host" in
  *[!A-Za-z0-9.-]*|"") echo "invalid source host" >&2; exit 2 ;;
esac
if [[ ! -s "$source_key" || ! -s "$known_hosts" ]]; then
  echo "source SSH material is unavailable" >&2
  exit 2
fi

tables=(
  automation_channel
  automation_channel_assignee
  automation_channel_assignment_event
  automation_channel_contact
  automation_channel_entry_effect_log
  automation_channel_entry_runtime
  automation_channel_qrcode_asset
  automation_channel_scene_alias
  channel_welcome_effect_dependency
  channel_welcome_effect_graph
  wecom_customer_acquisition_links
  image_library
  miniprogram_library
  attachment_library
  group_invite_library
  wecom_corp_tag_groups
  wecom_corp_tags
)

umask 077
sql_file="$(mktemp)"
cleanup() {
  if [[ -f "$sql_file" ]]; then
    unlink "$sql_file"
  fi
}
trap cleanup EXIT

{
  echo "BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;"
  echo "SET LOCAL statement_timeout='10min';"
  echo "SELECT '__AICRM_SNAPSHOT__|' || to_char(transaction_timestamp() AT TIME ZONE 'UTC','YYYY-MM-DD\"T\"HH24:MI:SS.US\"Z\"');"
  for table in "${tables[@]}"; do
    printf "SELECT '__AICRM_TABLE__|%s|' || encode(convert_to((SELECT json_agg(attname ORDER BY attnum)::text FROM pg_attribute WHERE attrelid='public.%s'::regclass AND attnum>0 AND NOT attisdropped),'UTF8'),'hex');\n" "$table" "$table"
    case "$table" in
      image_library)
        printf "SELECT '__AICRM_ROW__|' || encode(convert_to(to_jsonb(source_row)::text,'UTF8'),'hex') FROM public.image_library AS source_row WHERE id IN (SELECT DISTINCT value::bigint FROM public.automation_channel c CROSS JOIN LATERAL jsonb_array_elements_text(COALESCE(c.welcome_image_library_ids,'[]'::jsonb)) value WHERE value ~ '^[0-9]+$');\n"
        ;;
      miniprogram_library)
        printf "SELECT '__AICRM_ROW__|' || encode(convert_to(to_jsonb(source_row)::text,'UTF8'),'hex') FROM public.miniprogram_library AS source_row WHERE id IN (SELECT DISTINCT value::bigint FROM public.automation_channel c CROSS JOIN LATERAL jsonb_array_elements_text(COALESCE(c.welcome_miniprogram_library_ids,'[]'::jsonb)) value WHERE value ~ '^[0-9]+$');\n"
        ;;
      attachment_library)
        printf "SELECT '__AICRM_ROW__|' || encode(convert_to(to_jsonb(source_row)::text,'UTF8'),'hex') FROM public.attachment_library AS source_row WHERE id IN (SELECT DISTINCT value::bigint FROM public.automation_channel c CROSS JOIN LATERAL jsonb_array_elements_text(COALESCE(c.welcome_attachment_library_ids,'[]'::jsonb)) value WHERE value ~ '^[0-9]+$');\n"
        ;;
      group_invite_library)
        printf "SELECT '__AICRM_ROW__|' || encode(convert_to(to_jsonb(source_row)::text,'UTF8'),'hex') FROM public.group_invite_library AS source_row WHERE id IN (SELECT DISTINCT value::bigint FROM public.automation_channel c CROSS JOIN LATERAL jsonb_array_elements_text(COALESCE(c.welcome_group_invite_library_ids,'[]'::jsonb)) value WHERE value ~ '^[0-9]+$');\n"
        ;;
      wecom_corp_tags)
        printf "SELECT '__AICRM_ROW__|' || encode(convert_to(to_jsonb(source_row)::text,'UTF8'),'hex') FROM public.wecom_corp_tags AS source_row WHERE tag_id IN (SELECT DISTINCT entry_tag_id FROM public.automation_channel WHERE entry_tag_id<>'');\n"
        ;;
      wecom_corp_tag_groups)
        printf "SELECT '__AICRM_ROW__|' || encode(convert_to(to_jsonb(source_row)::text,'UTF8'),'hex') FROM public.wecom_corp_tag_groups AS source_row WHERE group_id IN (SELECT DISTINCT group_id FROM public.wecom_corp_tags WHERE tag_id IN (SELECT DISTINCT entry_tag_id FROM public.automation_channel WHERE entry_tag_id<>''));\n"
        ;;
      *)
        printf "SELECT '__AICRM_ROW__|' || encode(convert_to(to_jsonb(source_row)::text,'UTF8'),'hex') FROM public.%s AS source_row;\n" "$table"
        ;;
    esac
  done
  echo "COMMIT;"
} > "$sql_file"

ssh_flags=(-i "$source_key" -o BatchMode=yes -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes -o "UserKnownHostsFile=$known_hosts" -o ConnectTimeout=15)
ssh "${ssh_flags[@]}" "${source_user}@${source_host}" psql-stdin < "$sql_file" > "$output"
chmod 0600 "$output"

snapshot_markers="$(grep -c '__AICRM_SNAPSHOT__|' "$output" || true)"
table_markers="$(grep -c '__AICRM_TABLE__|' "$output" || true)"
if [[ "$snapshot_markers" != 1 || "$table_markers" != "${#tables[@]}" ]]; then
  echo "source capture marker validation failed" >&2
  exit 3
fi
echo "captured one consistent read-only channel snapshot stream with ${#tables[@]} tables"
