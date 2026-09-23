#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
dsn="${AICRM_STAGING_DATABASE_URL:?AICRM_STAGING_DATABASE_URL is required; production data is never copied}"
psql "$dsn" -v ON_ERROR_STOP=1 -f "$root/scripts/staging-fixtures/customer-sync-and-research.sql" >/dev/null
psql "$dsn" -v ON_ERROR_STOP=1 -At <<'SQL'
SELECT json_build_object(
 'fixture_id','customer-sync-and-research-v1',
 'synthetic',true,
 'customer_sync_run_status',(SELECT status FROM wecom_customer_sync_runs WHERE run_key='staging-fixture:customer-sync:v1'),
 'customer_projection_count',(SELECT count(*) FROM customer_directory_projection WHERE source='staging_fixture'),
 'customer_profile_count',(SELECT count(*) FROM wecom_external_contact_profiles WHERE customer_id=990001),
 'research_material_count',(SELECT count(*) FROM media_miniprograms WHERE id=990011 AND title='预发布合成教研课卡'),
 'research_mapping_count',(SELECT count(*) FROM media_legacy_material_mappings WHERE source_system='staging-fixture' AND legacy_material_id='00000000-0000-0000-0000-000000000001'));
SQL
