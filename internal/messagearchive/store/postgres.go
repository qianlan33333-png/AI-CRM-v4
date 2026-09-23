// Package store implements MessageArchive's PostgreSQL-owned tables only.
package store

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/app"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/domain"
	archiveport "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type PostgreSQL struct{}

func NewPostgreSQL() PostgreSQL { return PostgreSQL{} }

var _ app.Store = PostgreSQL{}

func (PostgreSQL) CommittedCursor(ctx context.Context, scope string) (uint64, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO message_archive_sync_state(corp_scope) VALUES($1) ON CONFLICT(corp_scope) DO NOTHING`, scope); err != nil {
		return 0, err
	}
	var cursor int64
	err = tx.QueryRow(ctx, `SELECT last_seq FROM message_archive_sync_state WHERE corp_scope=$1`, scope).Scan(&cursor)
	if err != nil || cursor < 0 {
		return 0, err
	}
	return uint64(cursor), nil
}

func (PostgreSQL) StartRun(ctx context.Context, run app.SyncRun) (int64, error) {
	if run.StartSeq > math.MaxInt64 {
		return 0, app.ErrProviderPage
	}
	if _, err := (PostgreSQL{}).CommittedCursor(ctx, run.CorpScope); err != nil {
		return 0, err
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return 0, err
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO message_archive_sync_runs(corp_scope,trigger_type,webhook_delivery_id,start_seq,end_seq,status,started_at)
		VALUES($1,$2,NULLIF($3,0),$4,$4,'running',$5) RETURNING id`, run.CorpScope, run.Trigger, run.WebhookDeliveryID, int64(run.StartSeq), run.StartedAt).Scan(&id)
	return id, err
}

func (PostgreSQL) CommitBatch(ctx context.Context, batch app.Batch) (app.BatchResult, error) {
	if batch.EndSeq > math.MaxInt64 || batch.ExpectedCursor > math.MaxInt64 || batch.EndSeq < batch.ExpectedCursor {
		return app.BatchResult{}, app.ErrProviderPage
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return app.BatchResult{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO message_archive_sync_state(corp_scope) VALUES($1) ON CONFLICT(corp_scope) DO NOTHING`, batch.CorpScope); err != nil {
		return app.BatchResult{}, err
	}
	var current int64
	if err = tx.QueryRow(ctx, `SELECT last_seq FROM message_archive_sync_state WHERE corp_scope=$1 FOR UPDATE`, batch.CorpScope).Scan(&current); err != nil {
		return app.BatchResult{}, err
	}
	if uint64(current) != batch.ExpectedCursor {
		return app.BatchResult{}, app.ErrCursorAdvanced
	}
	result := app.BatchResult{CommittedCursor: batch.EndSeq}
	for _, message := range batch.Messages {
		if !message.Valid() || message.CorpScope != batch.CorpScope || message.Seq > batch.EndSeq || message.Seq <= batch.ExpectedCursor {
			return app.BatchResult{}, app.ErrProviderPage
		}
		id, inserted, insertErr := insertMessage(ctx, tx, message)
		if insertErr != nil {
			return app.BatchResult{}, insertErr
		}
		if !inserted {
			result.Duplicates++
			continue
		}
		result.Inserted++
		for _, participant := range message.Participants {
			if err = insertParticipant(ctx, tx, id, participant); err != nil {
				return app.BatchResult{}, err
			}
			if participant.ResolutionStatus == domain.ResolutionNotFound || participant.ResolutionStatus == domain.ResolutionConflict {
				result.Unresolved++
			}
		}
		for _, media := range message.Media {
			if err = insertMedia(ctx, tx, id, media); err != nil {
				return app.BatchResult{}, err
			}
		}
	}
	for _, issue := range batch.Issues {
		if issue.Seq <= batch.ExpectedCursor || issue.Seq > batch.EndSeq {
			return app.BatchResult{}, app.ErrProviderPage
		}
		command, issueErr := tx.Exec(ctx, `INSERT INTO message_archive_ingest_issues(corp_scope,seq,msgid,stage,reason_code,payload_digest,protected_payload)
			VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(corp_scope,seq,stage,payload_digest) DO NOTHING`, batch.CorpScope, int64(issue.Seq), issue.MsgID, issue.Stage, issue.Reason, issue.Digest[:], nullableBytes(issue.Payload))
		if issueErr != nil {
			return app.BatchResult{}, issueErr
		}
		result.Issues += int(command.RowsAffected())
	}
	if _, err = tx.Exec(ctx, `UPDATE message_archive_sync_state SET last_seq=$2,last_notify_received_at=GREATEST(COALESCE(last_notify_received_at,$3),$3),last_pull_started_at=clock_timestamp(),last_error_code='',updated_at=clock_timestamp() WHERE corp_scope=$1`, batch.CorpScope, int64(batch.EndSeq), batch.NotifyReceivedAt); err != nil {
		return app.BatchResult{}, err
	}
	if batch.RunID > 0 {
		if _, err = tx.Exec(ctx, `UPDATE message_archive_sync_runs SET end_seq=$2,pages=pages+1,fetched_count=fetched_count+$3,inserted_count=inserted_count+$4,duplicate_count=duplicate_count+$5,unresolved_count=unresolved_count+$6,issue_count=issue_count+$7 WHERE id=$1 AND status='running'`, batch.RunID, int64(batch.EndSeq), len(batch.Messages)+len(batch.Issues), result.Inserted, result.Duplicates, result.Unresolved, result.Issues); err != nil {
			return app.BatchResult{}, err
		}
	}
	return result, nil
}

func (PostgreSQL) RecordBlockedIssue(ctx context.Context, scope string, issue app.IngestIssue, at time.Time) error {
	if scope == "" || issue.Seq == 0 || issue.Stage == "" || issue.Reason == "" || len(issue.Payload) == 0 {
		return app.ErrProviderPage
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO message_archive_sync_state(corp_scope) VALUES($1) ON CONFLICT(corp_scope) DO NOTHING`, scope); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO message_archive_ingest_issues(corp_scope,seq,msgid,stage,reason_code,payload_digest,protected_payload,created_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(corp_scope,seq,stage,payload_digest) DO NOTHING`, scope, int64(issue.Seq), issue.MsgID, issue.Stage, issue.Reason, issue.Digest[:], issue.Payload, at); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE message_archive_sync_state SET last_error_code='provider_page_invalid',updated_at=$2 WHERE corp_scope=$1`, scope, at)
	return err
}

func (PostgreSQL) FinishRun(ctx context.Context, runID int64, finish app.SyncRunFinish) error {
	if runID < 1 || finish.EndSeq > math.MaxInt64 || (finish.Status != "succeeded" && finish.Status != "failed") {
		return app.ErrProviderPage
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	command, err := tx.Exec(ctx, `UPDATE message_archive_sync_runs SET end_seq=$2,status=$3,error_code=$4,finished_at=$5 WHERE id=$1 AND status='running'`, runID, int64(finish.EndSeq), finish.Status, finish.ErrorCode, finish.FinishedAt)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return nil
	}
	if finish.Status == "succeeded" {
		_, err = tx.Exec(ctx, `UPDATE message_archive_sync_state SET last_notify_processed_at=$2,last_pull_succeeded_at=$2,last_error_code='',updated_at=$2 WHERE corp_scope=(SELECT corp_scope FROM message_archive_sync_runs WHERE id=$1)`, runID, finish.FinishedAt)
	}
	return err
}

func (PostgreSQL) CustomerMessages(ctx context.Context, query archiveport.CustomerQuery) (archiveport.CustomerPage, error) {
	if len(query.CustomerIDs) == 0 || query.Limit < 1 || query.Limit > 101 || query.Watermark.IsZero() ||
		(query.ChatType != "" && query.ChatType != "private" && query.ChatType != "group") ||
		(query.Direction != "" && query.Direction != "customer_to_staff" && query.Direction != "staff_to_customer") ||
		!validMessageTypeFilter(query.MessageType) || query.StaffUserID < 0 ||
		(!query.StartAt.IsZero() && query.StartAt.After(query.Watermark)) ||
		strings.TrimSpace(query.Search) != query.Search || len(query.Search) > 300 {
		return archiveport.CustomerPage{}, app.ErrProviderPage
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return archiveport.CustomerPage{}, err
	}
	ids := make([]int64, len(query.CustomerIDs))
	for index, id := range query.CustomerIDs {
		if id < 1 {
			return archiveport.CustomerPage{}, app.ErrProviderPage
		}
		ids[index] = int64(id)
	}
	afterID := query.AfterID
	if afterID == 0 {
		afterID = math.MaxInt64
	}
	rows, err := tx.Query(ctx, `SELECT message.id,message.conversation_type,message.msgtype,message.occurred_at,message.content_text,
		CASE WHEN message.provider_payload IS NULL THEN 'supported' ELSE 'unsupported' END,
		CASE
			WHEN BOOL_OR(sender.participant_role='sender' AND sender.actor_type='external_customer') THEN 'customer_to_staff'
			WHEN BOOL_OR(sender.participant_role='sender' AND sender.actor_type='staff') THEN 'staff_to_customer'
			ELSE 'unknown'
		END,
		COALESCE(array_remove(array_agg(DISTINCT staff.staff_user_id ORDER BY staff.staff_user_id),NULL),'{}'::bigint[]),
		COALESCE(array_remove(array_agg(DISTINCT media.id),NULL),'{}'::bigint[])
		FROM message_archive_messages message
		JOIN message_archive_participants customer_participant ON customer_participant.message_id=message.id
		LEFT JOIN message_archive_participants sender ON sender.message_id=message.id
		LEFT JOIN message_archive_participants staff ON staff.message_id=message.id AND staff.actor_type='staff'
		LEFT JOIN message_archive_media media ON media.message_id=message.id
		WHERE customer_participant.customer_id_at_ingest=ANY($1) AND ($2 OR message.occurred_at >= $3)
		AND message.occurred_at <= $4 AND ($5='' OR message.conversation_type=$5)
		AND (message.occurred_at,message.id) < ($6,$7) AND ($8='' OR message.content_text ILIKE '%' || $8 || '%')
		AND ($9='' OR message.msgtype=$9)
		AND ($10=0 OR EXISTS(SELECT 1 FROM message_archive_participants staff_filter WHERE staff_filter.message_id=message.id AND staff_filter.staff_user_id=$10))
		AND ($11='' OR ($11='customer_to_staff' AND EXISTS(SELECT 1 FROM message_archive_participants direction_sender WHERE direction_sender.message_id=message.id AND direction_sender.participant_role='sender' AND direction_sender.actor_type='external_customer')) OR ($11='staff_to_customer' AND EXISTS(SELECT 1 FROM message_archive_participants direction_sender WHERE direction_sender.message_id=message.id AND direction_sender.participant_role='sender' AND direction_sender.actor_type='staff')))
		GROUP BY message.id,message.conversation_type,message.msgtype,message.occurred_at,message.content_text,message.provider_payload
		ORDER BY message.occurred_at DESC,message.id DESC LIMIT $12`, ids, query.StartAt.IsZero(), query.StartAt, query.Watermark, query.ChatType, afterAt(query), afterID, query.Search, query.MessageType, query.StaffUserID, query.Direction, query.Limit)
	if err != nil {
		return archiveport.CustomerPage{}, err
	}
	defer rows.Close()
	page := archiveport.CustomerPage{Items: []archiveport.MessageItem{}, AsOf: query.Watermark}
	for rows.Next() {
		var item archiveport.MessageItem
		if err = rows.Scan(&item.ID, &item.ChatType, &item.MessageType, &item.OccurredAt, &item.ContentText, &item.RenderType, &item.Direction, &item.StaffIDs, &item.MediaIDs); err != nil {
			return archiveport.CustomerPage{}, err
		}
		page.Items = append(page.Items, item)
	}
	return page, rows.Err()
}

func (PostgreSQL) CustomerStaffIDs(ctx context.Context, customerIDs []customerdomain.CustomerID) ([]int64, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	if len(customerIDs) == 0 {
		return []int64{}, nil
	}
	ids := make([]int64, 0, len(customerIDs))
	for _, id := range customerIDs {
		if id < 1 {
			return nil, app.ErrProviderPage
		}
		ids = append(ids, int64(id))
	}
	rows, err := tx.Query(ctx, `SELECT DISTINCT staff_participant.staff_user_id
		FROM message_archive_participants customer_participant
		JOIN message_archive_participants staff_participant ON staff_participant.message_id=customer_participant.message_id
		WHERE customer_participant.customer_id_at_ingest = ANY($1::bigint[])
		AND staff_participant.staff_user_id IS NOT NULL
		ORDER BY staff_participant.staff_user_id`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []int64{}
	for rows.Next() {
		var item int64
		if err = rows.Scan(&item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// MediaAccess checks ownership through durable ingest participants only. Its
// caller supplies the current OneID lineage, so this does not query or write
// identity tables directly. The provider file reference stays inside this
// archive-owned store and never reaches the HTTP response.
func (PostgreSQL) MediaAccess(ctx context.Context, query app.MediaQuery) (app.MediaReference, error) {
	if query.MediaID < 1 || len(query.CustomerIDs) == 0 {
		return app.MediaReference{}, app.ErrProviderPage
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return app.MediaReference{}, err
	}
	ids := make([]int64, len(query.CustomerIDs))
	for index, id := range query.CustomerIDs {
		if id < 1 {
			return app.MediaReference{}, app.ErrProviderPage
		}
		ids[index] = int64(id)
	}
	var reference app.MediaReference
	var expectedSize *int64
	err = tx.QueryRow(ctx, `SELECT media.media_kind,media.provider_file_ref,media.expected_md5,media.expected_size
		FROM message_archive_media media
		JOIN message_archive_messages message ON message.id=media.message_id
		JOIN message_archive_participants participant ON participant.message_id=message.id
		WHERE media.id=$1 AND participant.customer_id_at_ingest=ANY($2)
		LIMIT 1`, query.MediaID, ids).Scan(&reference.Kind, &reference.ProviderFileRef, &reference.ExpectedMD5, &expectedSize)
	if errors.Is(err, pgx.ErrNoRows) {
		return app.MediaReference{}, app.ErrProviderPage
	}
	if err != nil {
		return app.MediaReference{}, err
	}
	if expectedSize != nil {
		reference.ExpectedSize, reference.HasExpectedSize = *expectedSize, true
	}
	return reference, nil
}

func insertMessage(ctx context.Context, tx pgx.Tx, message domain.Message) (int64, bool, error) {
	var id int64
	err := tx.QueryRow(ctx, `INSERT INTO message_archive_messages(corp_scope,seq,msgid,action,msgtype,conversation_type,roomid,msgtime_ms,occurred_at,content_text,normalized_payload,provider_payload,recalled_msgid)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) ON CONFLICT(corp_scope,msgid) DO NOTHING RETURNING id`, message.CorpScope, int64(message.Seq), message.MsgID, message.Action, message.MessageType, message.Conversation, message.RoomID, message.OccurredAt.UnixMilli(), message.OccurredAt, message.ContentText, []byte(message.Normalized), nullableJSON(message.ProviderPayload), message.RecalledMsgID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	return id, err == nil, err
}
func insertParticipant(ctx context.Context, tx pgx.Tx, messageID int64, participant domain.Participant) error {
	_, err := tx.Exec(ctx, `INSERT INTO message_archive_participants(message_id,participant_role,actor_type,provider_value,provider_value_digest,staff_user_id,customer_id_at_ingest,identity_id_at_ingest,resolution_status,resolution_reason,resolved_at)
		VALUES($1,$2,$3,$4,$5,NULLIF($6,0),NULLIF($7,0),NULLIF($8,0),$9,$10,$11)`, messageID, participant.Role, participant.ActorType, participant.ProviderValue, participant.ProviderDigest[:], participant.StaffUserID, int64(participant.CustomerID), participant.IdentityID, participant.ResolutionStatus, participant.ResolutionReason, participant.ResolvedAt)
	return err
}
func insertMedia(ctx context.Context, tx pgx.Tx, messageID int64, media domain.MediaReference) error {
	var size any
	if media.HasSize {
		size = media.Size
	}
	_, err := tx.Exec(ctx, `INSERT INTO message_archive_media(message_id,media_kind,provider_file_ref,provider_file_digest,expected_md5,expected_size,status)
		VALUES($1,$2,$3,$4,$5,$6,'pending') ON CONFLICT(message_id,provider_file_digest) DO NOTHING`, messageID, media.Kind, media.FileID, media.Digest[:], media.MD5, size)
	return err
}
func nullableJSON(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}
func nullableBytes(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}
func afterAt(query archiveport.CustomerQuery) time.Time {
	if query.AfterAt.IsZero() {
		return query.Watermark.Add(time.Nanosecond)
	}
	return query.AfterAt
}

func validMessageTypeFilter(value string) bool {
	if value == "" {
		return true
	}
	if len(value) > 120 {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_') {
			return false
		}
	}
	return true
}
