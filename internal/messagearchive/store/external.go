package store

import (
	"context"
	"strconv"
	"strings"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/app"
	archiveport "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// ExternalCustomerMessages owns the legacy-machine projection. Its customer
// predicate is applied in this Archive query, before it returns provider
// identifiers; the Open Platform host never reads Archive tables directly.
func (PostgreSQL) ExternalCustomerMessages(ctx context.Context, query archiveport.ExternalChatRecordQuery) (archiveport.ExternalChatRecordPage, error) {
	if len(query.CustomerIDs) == 0 || strings.TrimSpace(query.ExternalUserID) != query.ExternalUserID || query.ExternalUserID == "" || len(query.ExternalUserID) > 1024 ||
		(query.ChatScene != "private" && query.ChatScene != "group") || query.Limit < 1 || query.Limit > 20 || query.Offset < 0 ||
		(!query.StartAt.IsZero() && query.StartAt.Location().String() != "UTC") {
		return archiveport.ExternalChatRecordPage{}, app.ErrProviderPage
	}
	ids := make([]int64, len(query.CustomerIDs))
	for index, customerID := range query.CustomerIDs {
		if customerID < 1 {
			return archiveport.ExternalChatRecordPage{}, app.ErrProviderPage
		}
		ids[index] = int64(customerID)
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return archiveport.ExternalChatRecordPage{}, err
	}
	args := []any{ids, query.ExternalUserID, query.ChatScene, query.StartAt.IsZero(), query.StartAt, query.WithUserID}
	const visibleWhere = `EXISTS (
			SELECT 1 FROM message_archive_participants external_customer
			WHERE external_customer.message_id=message.id
			AND external_customer.actor_type='external_customer'
			AND external_customer.customer_id_at_ingest=ANY($1)
			AND external_customer.provider_value=$2
		)
		AND message.conversation_type=$3
		AND ($4 OR message.occurred_at >= $5)
		AND ($6='' OR EXISTS (
			SELECT 1 FROM message_archive_participants peer
			WHERE peer.message_id=message.id
			AND peer.provider_value=$6
		))`
	var total int64
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM message_archive_messages message WHERE `+visibleWhere, args...).Scan(&total); err != nil {
		return archiveport.ExternalChatRecordPage{}, err
	}
	rows, err := tx.Query(ctx, `
		SELECT message.id,message.msgid,message.conversation_type,
			COALESCE(legacy.historical_unionid,''),
			COALESCE((SELECT external_customer.provider_value
				FROM message_archive_participants external_customer
				WHERE external_customer.message_id=message.id
				AND external_customer.actor_type='external_customer'
				AND external_customer.customer_id_at_ingest=ANY($1)
				AND external_customer.provider_value=$2
				ORDER BY external_customer.id LIMIT 1),''),
			COALESCE((SELECT staff.provider_value
				FROM message_archive_participants staff
				WHERE staff.message_id=message.id AND staff.actor_type='staff'
				ORDER BY staff.id LIMIT 1),''),
			COALESCE((SELECT sender.provider_value
				FROM message_archive_participants sender
				WHERE sender.message_id=message.id AND sender.participant_role='sender'
				ORDER BY sender.id LIMIT 1),''),
			COALESCE((SELECT receiver.provider_value
				FROM message_archive_participants receiver
				WHERE receiver.message_id=message.id AND receiver.participant_role='recipient'
				ORDER BY receiver.id LIMIT 1),''),
			message.roomid,COALESCE(legacy.historical_group_name,''),message.msgtype,message.content_text,
			COALESCE((SELECT media.provider_file_ref FROM message_archive_media media WHERE media.message_id=message.id ORDER BY media.id LIMIT 1),''),
			message.occurred_at
		FROM message_archive_messages message
		LEFT JOIN message_archive_legacy_projections legacy ON legacy.message_id=message.id
		WHERE `+visibleWhere+`
		ORDER BY message.occurred_at ASC,message.id ASC
		LIMIT $7 OFFSET $8`, append(args, query.Limit, query.Offset)...)
	if err != nil {
		return archiveport.ExternalChatRecordPage{}, err
	}
	defer rows.Close()
	page := archiveport.ExternalChatRecordPage{Items: []archiveport.ExternalChatRecord{}, Total: total}
	for rows.Next() {
		var item archiveport.ExternalChatRecord
		var messageID int64
		if err = rows.Scan(&messageID, &item.MessageID, &item.ChatScene, &item.UnionID, &item.ExternalUserID, &item.WithUserID, &item.Sender, &item.Receiver, &item.RoomID, &item.GroupName, &item.MessageType, &item.Content, &item.MediaID, &item.OccurredAt); err != nil {
			return archiveport.ExternalChatRecordPage{}, err
		}
		item.ChatType = item.ChatScene
		item.ChatID = item.RoomID
		item.SourceID = strconv.FormatInt(messageID, 10)
		page.Items = append(page.Items, item)
	}
	if err = rows.Err(); err != nil {
		return archiveport.ExternalChatRecordPage{}, err
	}
	return page, nil
}
