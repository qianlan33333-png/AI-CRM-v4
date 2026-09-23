package channel

import (
	"context"
	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"time"
)

// AudienceEntries combines immutable native attributed entrants with
// reconciled historical facts. It chooses the latest entry per customer and
// channel, so repeated historical imports and native callbacks do not produce
// duplicate audience members.
func (store *PostgreSQLStore) AudienceEntries(ctx context.Context, reference time.Time) ([]channelport.AudienceEntry, error) {
	if reference.IsZero() {
		return nil, ErrInvalidCatalogCommand
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `WITH history_latest AS (
		SELECT DISTINCT ON (h.channel_id,h.source_contact_id) h.*
		FROM channel_history_contacts h JOIN channel_history_import_runs r ON r.id=h.import_run_id
		WHERE r.state IN ('completed','reconciled') AND h.last_entered_at <= $1
		ORDER BY h.channel_id,h.source_contact_id,r.snapshot_timestamp DESC,h.import_run_id DESC
	), facts AS (
		SELECT h.customer_id,h.channel_id,c.code AS channel_code,h.owner_reference,NULL::bigint AS owner_staff_id,h.first_entered_at,h.last_entered_at FROM history_latest h JOIN channels c ON c.id=h.channel_id WHERE h.customer_id IS NOT NULL
		UNION ALL
		SELECT e.customer_id,b.channel_id,c.code AS channel_code,''::text AS owner_reference,a.staff_id AS owner_staff_id,e.occurred_at,e.occurred_at
		FROM channel_acquisition_entrant_receipts e
		JOIN channel_acquisition_state_bindings b ON b.id=e.binding_id
		JOIN channels c ON c.id=b.channel_id
		LEFT JOIN channel_entrant_assignments a ON a.callback_id=e.callback_id
		WHERE e.status='channel_attributed' AND e.customer_id IS NOT NULL AND e.occurred_at <= $1
	), latest AS (
		SELECT DISTINCT ON (channel_id,customer_id) customer_id,channel_id,channel_code,owner_reference,owner_staff_id,first_entered_at,last_entered_at
		FROM facts ORDER BY channel_id,customer_id,last_entered_at DESC,first_entered_at DESC
	) SELECT customer_id,channel_id,channel_code,owner_reference,owner_staff_id,first_entered_at,last_entered_at FROM latest ORDER BY channel_id,customer_id`, reference.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []channelport.AudienceEntry{}
	for rows.Next() {
		var item channelport.AudienceEntry
		if err = rows.Scan(&item.CustomerID, &item.ChannelID, &item.ChannelCode, &item.OwnerReference, &item.OwnerStaffID, &item.FirstEnteredAt, &item.LastEnteredAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

var _ channelport.AudienceEntryReader = (*PostgreSQLStore)(nil)
