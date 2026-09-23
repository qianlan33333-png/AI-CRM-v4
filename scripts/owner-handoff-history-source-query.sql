-- Read-only legacy owner_migration_results export. __CORP_SCOPE__ is substituted
-- only after the capture script validates the complete scope grammar.
SELECT '__AICRM_OWNER_HANDOFF_HISTORY_ROW__|' || encode(convert_to(jsonb_build_object(
  'source_batch_id', r.result_id,
  'source_line_id', item.ordinality::text,
  'mode', CASE WHEN r.include_wecom_transfer THEN 'wecom_then_crm' ELSE 'local_only' END,
  'source_state', CASE
    WHEN COALESCE(NULLIF(item.row->>'external_userid',''), '')='' OR COALESCE(NULLIF(r.source_owner_userid,''), '')='' OR COALESCE(NULLIF(r.target_owner_userid,''), '')='' THEN 'invalid_source'
    ELSE COALESCE(NULLIF(item.row->>'status',''), NULLIF(item.row->>'crm_status',''), NULLIF(item.row->>'wecom_status',''), 'legacy_recorded')
  END,
  'occurred_at', COALESCE(r.executed_at,r.created_at),
  'corp_scope', '__CORP_SCOPE__',
  'external_userid', COALESCE(item.row->>'external_userid',''),
  'source_owner_userid', r.source_owner_userid,
  'target_owner_userid', r.target_owner_userid,
  'wecom_status', COALESCE(item.row->>'wecom_status',''),
  'crm_status', COALESCE(item.row->>'crm_status','')
)::text,'UTF8'),'hex')
FROM public.owner_migration_results r
CROSS JOIN LATERAL jsonb_array_elements(COALESCE(r.rows_json,'[]'::jsonb)) WITH ORDINALITY AS item(row, ordinality)
UNION ALL
SELECT '__AICRM_OWNER_HANDOFF_HISTORY_ROW__|' || encode(convert_to(jsonb_build_object(
  'source_batch_id', r.result_id,
  'source_line_id', '0',
  'mode', CASE WHEN r.include_wecom_transfer THEN 'wecom_then_crm' ELSE 'local_only' END,
  'source_state', 'empty_batch',
  'occurred_at', COALESCE(r.executed_at,r.created_at),
  'corp_scope', '__CORP_SCOPE__',
  'external_userid', '',
  'source_owner_userid', COALESCE(r.source_owner_userid,''),
  'target_owner_userid', COALESCE(r.target_owner_userid,''),
  'wecom_status', '',
  'crm_status', ''
)::text,'UTF8'),'hex')
FROM public.owner_migration_results r
WHERE jsonb_array_length(COALESCE(r.rows_json,'[]'::jsonb))=0
ORDER BY 1;
