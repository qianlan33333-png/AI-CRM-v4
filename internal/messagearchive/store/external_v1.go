package store

import (
	"context"
	"strconv"
	"strings"

	archiveapp "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/app"
	archiveport "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// V1ChatRecords is the Archive-owned local read model for Open Platform. It
// never calls the SDK, reads raw participant identifiers, or leaks protected
// provider payload/media references. The selected canonical lineage predicate
// is inside this query before its keyset/limit is applied.
func (PostgreSQL) V1ChatRecords(ctx context.Context, query archiveport.V1ChatRecordQuery) (archiveport.V1ChatRecordPage, error) {
	if len(query.CustomerIDs) == 0 || (query.ChatType != "private" && query.ChatType != "group") || query.StaffUserID < 0 || (query.ChatType == "private" && query.StaffUserID < 1) ||
		(query.SourceSystem != "" && query.SourceSystem != "message_archive") || strings.TrimSpace(query.SourceRecordID) != query.SourceRecordID || len(query.SourceRecordID) > 128 ||
		strings.TrimSpace(query.MessageID) != query.MessageID || len(query.MessageID) > 512 ||
		query.Limit < 1 || query.Limit > 20 || query.EndAt.IsZero() || query.EndAt.Location().String() != "UTC" ||
		(!query.StartAt.IsZero() && query.StartAt.Location().String() != "UTC") || (!query.StartAt.IsZero() && !query.StartAt.Before(query.EndAt)) ||
		(query.BeforeOccurredAt.IsZero() != (query.BeforeMessageID == 0)) || query.BeforeMessageID < 0 {
		return archiveport.V1ChatRecordPage{}, archiveapp.ErrProviderPage
	}
	ids := make([]int64, 0, len(query.CustomerIDs))
	seen := make(map[int64]struct{}, len(query.CustomerIDs))
	for _, customerID := range query.CustomerIDs {
		id := int64(customerID)
		if id < 1 {
			return archiveport.V1ChatRecordPage{}, archiveapp.ErrProviderPage
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return archiveport.V1ChatRecordPage{Items: []archiveport.V1ChatRecord{}}, nil
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return archiveport.V1ChatRecordPage{}, err
	}
	beforeID := query.BeforeMessageID
	if beforeID == 0 {
		beforeID = int64(^uint64(0) >> 1)
	}
	beforeAt := query.BeforeOccurredAt
	if beforeAt.IsZero() {
		beforeAt = query.EndAt
	}
	args := []any{ids, query.ChatType, query.StartAt.IsZero(), query.StartAt, query.EndAt, query.SourceRecordID, query.MessageID, beforeAt, beforeID, query.StaffUserID, query.Limit + 1}
	rows, err := tx.Query(ctx, `
		SELECT message.id,message.msgid,message.conversation_type,message.msgtype,message.content_text,
			COALESCE(NULLIF(message.normalized_payload->>'render_type',''),'supported'),
			CASE
				WHEN EXISTS(SELECT 1 FROM message_archive_participants sender WHERE sender.message_id=message.id AND sender.participant_role='sender' AND sender.actor_type='external_customer') THEN 'customer_to_staff'
				WHEN EXISTS(SELECT 1 FROM message_archive_participants sender WHERE sender.message_id=message.id AND sender.participant_role='sender' AND sender.actor_type='staff') THEN 'staff_to_customer'
				ELSE 'unknown'
			END,
			message.occurred_at,message.roomid,COALESCE(legacy.historical_group_name,''),
			COALESCE(ARRAY(SELECT DISTINCT staff.staff_user_id FROM message_archive_participants staff WHERE staff.message_id=message.id AND staff.actor_type='staff' AND staff.staff_user_id IS NOT NULL ORDER BY staff.staff_user_id),'{}'::bigint[]),
			CASE
				WHEN message.msgtype IN ('text','revoke') THEN 'not_applicable'
				WHEN EXISTS(SELECT 1 FROM message_archive_media media WHERE media.message_id=message.id AND media.status='ready') THEN 'available'
				ELSE 'unavailable'
			END,
			CASE
				WHEN message.msgtype IN ('text','revoke') THEN 'not_applicable'
				WHEN EXISTS(SELECT 1 FROM message_archive_media media WHERE media.message_id=message.id AND media.status='ready') THEN 'api_unavailable'
				ELSE 'unavailable'
			END
		FROM message_archive_messages message
		LEFT JOIN message_archive_legacy_projections legacy ON legacy.message_id=message.id
		WHERE EXISTS(
			SELECT 1 FROM message_archive_participants customer_participant
			WHERE customer_participant.message_id=message.id
			AND customer_participant.customer_id_at_ingest=ANY($1::bigint[])
		)
		AND ($2='' OR message.conversation_type=$2)
		AND ($3 OR message.occurred_at >= $4)
		AND message.occurred_at < $5
		AND ($6='' OR message.id::text=$6)
		AND ($7='' OR message.msgid=$7)
		AND (message.occurred_at,message.id) < ($8,$9)
		AND ($10=0 OR EXISTS(
			SELECT 1 FROM message_archive_participants staff_filter
			WHERE staff_filter.message_id=message.id AND staff_filter.staff_user_id=$10
		))
		ORDER BY message.occurred_at DESC,message.id DESC
		LIMIT $11`, args...)
	if err != nil {
		return archiveport.V1ChatRecordPage{}, err
	}
	defer rows.Close()
	items := make([]archiveport.V1ChatRecord, 0, query.Limit+1)
	for rows.Next() {
		var item archiveport.V1ChatRecord
		var id int64
		if err = rows.Scan(&id, &item.MessageID, &item.ChatType, &item.MessageType, &item.Content, &item.RenderType, &item.Direction, &item.OccurredAt, &item.ConversationID, &item.GroupName, &item.StaffIDs, &item.MediaArchiveStatus, &item.MediaAvailability); err != nil {
			return archiveport.V1ChatRecordPage{}, err
		}
		item.SourceSystem = "message_archive"
		item.SourceRecordID = strconv.FormatInt(id, 10)
		item.Staff = []archiveport.StaffOption{}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return archiveport.V1ChatRecordPage{}, err
	}
	hasMore := len(items) > query.Limit
	if hasMore {
		items = items[:query.Limit]
	}
	return archiveport.V1ChatRecordPage{Items: items, HasMore: hasMore}, nil
}
