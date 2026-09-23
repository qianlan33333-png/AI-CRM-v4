package channel

import (
	"context"
	"fmt"
	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

var _ channelport.CustomerActivityReader = (*PostgreSQLStore)(nil)

func (store *PostgreSQLStore) CustomerChannelActivities(ctx context.Context, customerID customerdomain.CustomerID, q customerport.PageQuery) (customerport.TimelinePage, error) {
	if customerID < 1 || q.Limit < 1 || q.Limit > 101 || q.Watermark.IsZero() || q.AfterID < 0 {
		return customerport.TimelinePage{}, ErrInvalidCatalogCommand
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return customerport.TimelinePage{}, err
	}
	var after any
	if !q.AfterAt.IsZero() {
		after = q.AfterAt.UTC()
	}
	rows, err := tx.Query(ctx, `WITH latest_history AS (
  SELECT DISTINCT ON (h.channel_id,h.source_contact_id) h.*
  FROM channel_history_contacts h JOIN channel_history_import_runs ir ON ir.id=h.import_run_id
  WHERE ir.state IN ('completed','reconciled') AND ir.snapshot_timestamp <= $2
  ORDER BY h.channel_id,h.source_contact_id,ir.snapshot_timestamp DESC,h.import_run_id DESC
 ), history AS (
  SELECT h.id,h.channel_id,h.last_entered_at
  FROM latest_history h
  LEFT JOIN LATERAL (SELECT customer_id FROM channel_history_contact_reconciliations WHERE history_contact_id=h.id AND reconciled_at <= $2 ORDER BY reconciled_at DESC,id DESC LIMIT 1) r ON true
  WHERE COALESCE(r.customer_id,h.customer_id)=$1
 ), runtime AS (
  SELECT e.id,COALESCE(r.binding_id,e.binding_id) binding_id,e.occurred_at
  FROM channel_acquisition_entrant_receipts e
  LEFT JOIN channel_acquisition_entrant_reconciliation_receipts r ON r.entrant_receipt_id=e.id AND r.reconciled_at <= $2
  WHERE COALESCE(r.customer_id,e.customer_id)=$1 AND (r.id IS NOT NULL OR e.status='channel_attributed')
 ), facts AS (
  SELECT h.id*2 id,h.channel_id,h.last_entered_at occurred_at,'channel.history_entered' event_type,'history' asset_kind FROM history h
  UNION ALL
  SELECT e.id*2+1,b.channel_id,e.occurred_at,'channel.entered',b.asset_kind
  FROM runtime e JOIN channel_acquisition_state_bindings b ON b.id=e.binding_id
  WHERE NOT EXISTS (SELECT 1 FROM history h WHERE h.channel_id=b.channel_id AND h.last_entered_at >= e.occurred_at)
 )
 SELECT f.id,f.channel_id,f.occurred_at,f.event_type,f.asset_kind,COALESCE(v.name,c.code,'')
 FROM facts f LEFT JOIN channels c ON c.id=f.channel_id
 LEFT JOIN channel_config_versions v ON v.channel_id=c.id AND v.config_version=c.current_config_version
 WHERE f.occurred_at <= $2 AND ($3::timestamptz IS NULL OR (f.occurred_at,f.id)<($3,$4))
 ORDER BY f.occurred_at DESC,f.id DESC LIMIT $5`, customerID, q.Watermark.UTC(), after, q.AfterID, q.Limit)
	if err != nil {
		return customerport.TimelinePage{}, err
	}
	defer rows.Close()
	page := customerport.TimelinePage{Items: []customerport.TimelineItem{}}
	for rows.Next() {
		var item customerport.TimelineItem
		var channelID int64
		var kind, name string
		if err = rows.Scan(&item.ID, &channelID, &item.OccurredAt, &item.EventType, &kind, &name); err != nil {
			return customerport.TimelinePage{}, err
		}
		if name == "" {
			name = fmt.Sprintf("渠道 #%d", channelID)
		}
		action := "通过渠道码添加："
		if kind == "customer_acquisition_link" {
			action = "通过获客链接进入："
		}
		if kind == "history" {
			action = "历史渠道进入："
		}
		item.Title = action + name
		item.SourceDomain = "channel"
		page.Items = append(page.Items, item)
	}
	return page, rows.Err()
}
