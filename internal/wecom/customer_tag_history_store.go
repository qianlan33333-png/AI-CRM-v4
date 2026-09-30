package wecom

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

// appendTagSetDiff runs before replacing current state, in the publication UoW.
// Missing fields never call this function. Renames update display state only.
func appendTagSetDiff(ctx context.Context, tx pgx.Tx, customerID customerdomain.CustomerID, scope, employee string, tags []wecomport.ExternalContactTag, runID int64, at time.Time) error {
	values := make([]map[string]any, 0, len(tags))
	for _, t := range tags {
		values = append(values, map[string]any{"id": t.ProviderTagID, "type": t.Type, "name": t.Name, "group_name": t.GroupName})
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `WITH incoming AS (SELECT DISTINCT ON (type,id) * FROM jsonb_to_recordset($4::jsonb) AS t(id text,type smallint,name text,group_name text)),
 meta AS (SELECT COALESCE(p.revision,0) revision,COALESCE(p.baseline_initialized,false) initialized,COALESCE(p.baseline_date,DATE '2026-09-30') baseline, r.trigger_type
 FROM wecom_customer_sync_runs r LEFT JOIN wecom_directory_publications p ON p.corp_scope=r.corp_scope WHERE r.id=$5),
 changes AS (
 SELECT t.id,t.type,t.name,t.group_name,o.observed_at previous_at,
 CASE WHEN NOT m.initialized AND m.trigger_type<>'new_contact' AND NOT EXISTS(SELECT 1 FROM wecom_customer_tag_history h WHERE h.customer_id=$1 AND h.corp_scope=$2 AND h.employee_id=$3 AND h.provider_tag_type=t.type AND h.provider_tag_id=t.id AND h.event_type='baseline') THEN 'baseline'
 WHEN EXISTS(SELECT 1 FROM wecom_customer_tag_history h WHERE h.customer_id=$1 AND h.corp_scope=$2 AND h.employee_id=$3 AND h.provider_tag_type=t.type AND h.provider_tag_id=t.id) THEN 'readded' ELSE 'added' END kind
 FROM incoming t CROSS JOIN meta m LEFT JOIN wecom_customer_tag_observations o ON o.customer_id=$1 AND o.corp_scope=$2 AND o.employee_id=$3 AND o.provider_tag_type=t.type AND o.provider_tag_id=t.id
 WHERE o.customer_id IS NULL OR (o.observation_status<>'active' AND o.absence_reason IS DISTINCT FROM 'follow_not_observed') OR (NOT m.initialized AND m.trigger_type<>'new_contact' AND NOT EXISTS(SELECT 1 FROM wecom_customer_tag_history h WHERE h.customer_id=$1 AND h.corp_scope=$2 AND h.employee_id=$3 AND h.provider_tag_type=t.type AND h.provider_tag_id=t.id AND h.event_type='baseline'))
 UNION ALL
 SELECT o.provider_tag_id,o.provider_tag_type,o.observed_name,o.group_name,o.observed_at,'removed'
 FROM wecom_customer_tag_observations o WHERE o.customer_id=$1 AND o.corp_scope=$2 AND o.employee_id=$3 AND o.observation_status='active'
 AND NOT EXISTS(SELECT 1 FROM incoming t WHERE t.id=o.provider_tag_id AND t.type=o.provider_tag_type))
 INSERT INTO wecom_customer_tag_history(customer_id,corp_scope,employee_id,provider_tag_type,provider_tag_id,observed_name,group_name,event_type,reason,discovered_at,discovered_date,registration_date,previous_observed_at,from_revision,to_revision,run_id)
 SELECT $1,$2,$3,c.type,c.id,c.name,c.group_name,c.kind,CASE WHEN c.kind='baseline' THEN 'initial_baseline' ELSE 'tag_set_changed' END,$6,($6::timestamptz AT TIME ZONE 'Asia/Shanghai')::date,
 CASE WHEN c.kind='baseline' THEN m.baseline ELSE ($6::timestamptz AT TIME ZONE 'Asia/Shanghai')::date END,c.previous_at,GREATEST(m.revision-1,0),m.revision,$5 FROM changes c CROSS JOIN meta m
 ON CONFLICT DO NOTHING`, customerID, scope, employee, raw, runID, at.UTC())
	return err
}
