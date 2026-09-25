package store

import (
	"context"
	"errors"
	"strings"

	archiveport "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// MachineContactTouchTimes reads only private messages already ingested from
// the provider. It does not interpret a queued outbound intent as a send.
func (PostgreSQL) MachineContactTouchTimes(ctx context.Context, corpScope string, customerIDs []int64) (map[int64]archiveport.MachineContactTouch, error) {
	result := make(map[int64]archiveport.MachineContactTouch, len(customerIDs))
	if len(customerIDs) == 0 {
		return result, nil
	}
	if !strings.HasPrefix(corpScope, "wecom-corp:") || len(corpScope) <= len("wecom-corp:") {
		return nil, errors.New("invalid archive corp scope")
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT customer.customer_id_at_ingest,
		max(message.occurred_at) FILTER (WHERE sender.actor_type='staff'),
		max(message.occurred_at) FILTER (WHERE sender.actor_type='external_customer')
		FROM message_archive_messages message
		JOIN message_archive_participants customer ON customer.message_id=message.id AND customer.customer_id_at_ingest=ANY($2::bigint[])
		JOIN message_archive_participants sender ON sender.message_id=message.id AND sender.participant_role='sender'
		WHERE message.corp_scope=$1 AND message.conversation_type='private' AND message.action<>'revoke'
		GROUP BY customer.customer_id_at_ingest`, corpScope, customerIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var item archiveport.MachineContactTouch
		if err = rows.Scan(&id, &item.LastStaffMessageAt, &item.LastCustomerMessageAt); err != nil {
			return nil, err
		}
		result[id] = item
	}
	return result, rows.Err()
}

var _ archiveport.MachineContactTouchReader = PostgreSQL{}
