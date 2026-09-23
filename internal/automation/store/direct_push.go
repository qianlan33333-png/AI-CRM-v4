package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	automationapp "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/app"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
)

func (r *Repository) DirectPushConfigByReference(ctx context.Context, reference string, lock bool) (automationapp.DirectPushConfig, error) {
	t, err := tx(ctx)
	if err != nil {
		return automationapp.DirectPushConfig{}, err
	}
	query := `SELECT package_id,webhook_reference,enabled,max_per_customer_24h,version FROM automation_audience_push_configs WHERE webhook_reference=$1`
	if lock {
		query += ` FOR UPDATE`
	}
	var out automationapp.DirectPushConfig
	err = t.QueryRow(ctx, query, reference).Scan(&out.PackageID, &out.WebhookReference, &out.Enabled, &out.MaxPerCustomer24h, &out.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, automationapp.ErrDirectPushNotFound
	}
	return out, err
}

func (r *Repository) DirectPushConfigByPackage(ctx context.Context, packageID int64, lock bool) (automationapp.DirectPushConfig, error) {
	t, err := tx(ctx)
	if err != nil {
		return automationapp.DirectPushConfig{}, err
	}
	query := `SELECT package_id,webhook_reference,enabled,max_per_customer_24h,version FROM automation_audience_push_configs WHERE package_id=$1`
	if lock {
		query += ` FOR UPDATE`
	}
	var out automationapp.DirectPushConfig
	err = t.QueryRow(ctx, query, packageID).Scan(&out.PackageID, &out.WebhookReference, &out.Enabled, &out.MaxPerCustomer24h, &out.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, automationapp.ErrDirectPushNotFound
	}
	return out, err
}

func (r *Repository) ExistingDirectPushConfigReceipt(ctx context.Context, key string) ([32]byte, automationport.DirectPushConfigView, bool, error) {
	t, err := tx(ctx)
	if err != nil {
		return [32]byte{}, automationport.DirectPushConfigView{}, false, err
	}
	var rawDigest, rawResult []byte
	err = t.QueryRow(ctx, `SELECT payload_digest,result_snapshot FROM automation_audience_push_config_receipts WHERE idempotency_key=$1`, key).Scan(&rawDigest, &rawResult)
	if errors.Is(err, pgx.ErrNoRows) {
		return [32]byte{}, automationport.DirectPushConfigView{}, false, nil
	}
	var digest [32]byte
	var result automationport.DirectPushConfigView
	if err != nil || len(rawDigest) != 32 || json.Unmarshal(rawResult, &result) != nil {
		return digest, result, false, automationapp.ErrDirectPushConflict
	}
	copy(digest[:], rawDigest)
	return digest, result, true, nil
}

func (r *Repository) PutDirectPushConfig(ctx context.Context, config automationapp.DirectPushConfig, expectedVersion, actor int64, key string, payloadDigest [32]byte, now time.Time) (automationapp.DirectPushConfig, error) {
	t, err := tx(ctx)
	if err != nil {
		return config, err
	}
	if expectedVersion == 0 {
		err = t.QueryRow(ctx, `INSERT INTO automation_audience_push_configs(package_id,webhook_reference,enabled,max_per_customer_24h,version,updated_by,created_at,updated_at) VALUES($1,$2,$3,$4,1,$5,$6,$6) ON CONFLICT(package_id) DO NOTHING RETURNING version`, config.PackageID, config.WebhookReference, config.Enabled, config.MaxPerCustomer24h, actor, now.UTC()).Scan(&config.Version)
	} else {
		err = t.QueryRow(ctx, `UPDATE automation_audience_push_configs SET enabled=$2,max_per_customer_24h=$3,version=version+1,updated_by=$4,updated_at=$5 WHERE package_id=$1 AND version=$6 RETURNING webhook_reference,version`, config.PackageID, config.Enabled, config.MaxPerCustomer24h, actor, now.UTC(), expectedVersion).Scan(&config.WebhookReference, &config.Version)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return config, automationapp.ErrDirectPushConflict
	}
	if err != nil {
		return config, err
	}
	view := automationport.DirectPushConfigView{PackageID: config.PackageID, WebhookReference: config.WebhookReference, WebhookPath: "/api/automation/audience/webhooks/" + config.WebhookReference, ClientID: "aicrm-audience-direct-push", Enabled: config.Enabled, MaxPerCustomer24h: config.MaxPerCustomer24h, Version: config.Version}
	result, err := json.Marshal(view)
	if err != nil {
		return config, err
	}
	if _, err = t.Exec(ctx, `INSERT INTO automation_audience_push_config_receipts(idempotency_key,package_id,payload_digest,result_snapshot,created_at) VALUES($1,$2,$3,$4::jsonb,$5)`, key, config.PackageID, payloadDigest[:], result, now.UTC()); err != nil {
		return config, err
	}
	auditDigest := sha256.Sum256(result)
	_, err = t.Exec(ctx, `INSERT INTO automation_audience_push_config_audit_events(package_id,operation,actor_id,payload_digest,occurred_at) VALUES($1,'configured',$2,$3,$4)`, config.PackageID, actor, auditDigest[:], now.UTC())
	return config, err
}

func (r *Repository) ExistingDirectPushBatch(ctx context.Context, packageID int64, event [32]byte) ([32]byte, automationport.DirectPushBatchResult, bool, error) {
	t, err := tx(ctx)
	if err != nil {
		return [32]byte{}, automationport.DirectPushBatchResult{}, false, err
	}
	var payload []byte
	var raw []byte
	var result automationport.DirectPushBatchResult
	err = t.QueryRow(ctx, `SELECT payload_digest,result_snapshot FROM automation_audience_push_batches WHERE package_id=$1 AND event_id_digest=$2`, packageID, event[:]).Scan(&payload, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return [32]byte{}, automationport.DirectPushBatchResult{}, false, nil
	}
	if err != nil {
		return [32]byte{}, automationport.DirectPushBatchResult{}, false, err
	}
	var digest [32]byte
	if len(payload) != 32 || json.Unmarshal(raw, &result) != nil {
		return digest, result, false, automationapp.ErrDirectPushConflict
	}
	copy(digest[:], payload)
	return digest, result, true, nil
}

func (r *Repository) CreateDirectPushBatch(ctx context.Context, config automationapp.DirectPushConfig, event, payload [32]byte, itemCount int, now time.Time) (int64, error) {
	t, err := tx(ctx)
	if err != nil {
		return 0, err
	}
	var id int64
	err = t.QueryRow(ctx, `INSERT INTO automation_audience_push_batches(package_id,event_id_digest,payload_digest,result_snapshot,accepted_count,rejected_count,created_at) VALUES($1,$2,$3,'{"pending":true}'::jsonb,0,$4,$5) RETURNING id`, config.PackageID, event[:], payload[:], itemCount, now.UTC()).Scan(&id)
	return id, err
}

func (r *Repository) LockDirectPushRate(ctx context.Context, packageID int64, customerID customerdomain.CustomerID) error {
	t, err := tx(ctx)
	if err != nil {
		return err
	}
	_, err = t.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, fmt.Sprintf("audience-direct-push:%d:%d", packageID, customerID))
	return err
}
func (r *Repository) CountRecentDirectPushes(ctx context.Context, packageID int64, customerID customerdomain.CustomerID, after time.Time) (int, error) {
	t, err := tx(ctx)
	if err != nil {
		return 0, err
	}
	var n int
	err = t.QueryRow(ctx, `SELECT count(*) FROM automation_audience_push_items WHERE package_id=$1 AND customer_id=$2 AND created_at>$3 AND send_state<>'cancelled'`, packageID, customerID, after.UTC()).Scan(&n)
	return n, err
}

func (r *Repository) CreateDirectPushItem(ctx context.Context, batchID int64, d automationapp.DirectPushItemDraft) (int64, error) {
	t, err := tx(ctx)
	if err != nil {
		return 0, err
	}
	var id int64
	err = t.QueryRow(ctx, `INSERT INTO automation_audience_push_items(batch_id,package_id,snapshot_id,identity_id,customer_id,sender_staff_id,sender_set_version,miniprogram_id,client_reference,content_snapshot,content_snapshot_digest,send_state,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::jsonb,$11,'accepted',$12,$12) RETURNING id`, batchID, d.PackageID, d.SnapshotID, d.IdentityID, d.CustomerID, d.StaffID, d.SenderSetVersion, d.MiniProgramID, d.ClientReference, d.ContentSnapshot, d.ContentDigest[:], d.CreatedAt.UTC()).Scan(&id)
	return id, err
}
func (r *Repository) BindDirectPushEffect(ctx context.Context, id int64, a outboundport.MessageAcceptance, now time.Time) error {
	t, err := tx(ctx)
	if err != nil {
		return err
	}
	tag, err := t.Exec(ctx, `UPDATE automation_audience_push_items SET outbound_intent_id=$2,effect_id=$3,send_state='queued',updated_at=$4 WHERE id=$1 AND outbound_intent_id IS NULL`, id, a.MessageIntentID, a.EffectID, now.UTC())
	if err == nil && tag.RowsAffected() != 1 {
		return automationapp.ErrDirectPushConflict
	}
	return err
}

func (r *Repository) CompleteDirectPushBatch(ctx context.Context, id int64, result automationport.DirectPushBatchResult, payload [32]byte, now time.Time) error {
	t, err := tx(ctx)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	tag, err := t.Exec(ctx, `UPDATE automation_audience_push_batches SET result_snapshot=$2::jsonb,accepted_count=$3,rejected_count=$4 WHERE id=$1`, id, raw, result.AcceptedCount, result.RejectedCount)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return automationapp.ErrDirectPushConflict
	}
	audit := sha256.Sum256(raw)
	if _, err = t.Exec(ctx, `INSERT INTO automation_audience_push_audit_events(batch_id,event_type,payload_digest,occurred_at) VALUES($1,'automation.audience_push.accepted.v1',$2,$3)`, id, audit[:], now.UTC()); err != nil {
		return err
	}
	outbox, _ := json.Marshal(map[string]any{"batch_id": formatBatch(id), "accepted_count": result.AcceptedCount, "rejected_count": result.RejectedCount})
	key := sha256.Sum256([]byte("accepted:" + strconv.FormatInt(id, 10)))
	_, err = t.Exec(ctx, `INSERT INTO automation_audience_push_outbox(event_type,batch_id,payload,idempotency_digest,occurred_at) VALUES('automation.audience_push.accepted.v1',$1,$2::jsonb,$3,$4)`, id, outbox, key[:], now.UTC())
	_ = payload
	return err
}

func (r *Repository) DirectPushStatuses(ctx context.Context, packageID int64, ids []int64) ([]automationport.DirectPushStatus, error) {
	t, err := tx(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := t.Query(ctx, `SELECT id,CASE WHEN send_state IN ('accepted','queued','provider_accepted','delivery_proven','final_failed','outcome_unknown') THEN send_state WHEN send_state IN ('attempted','retryable_failed') THEN 'queued' ELSE 'final_failed' END,observation_state,failure_code,observation_reason,sent_at,observation_due_at,observed_at FROM automation_audience_push_items WHERE package_id=$1 AND id=ANY($2)`, packageID, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byID := map[int64]automationport.DirectPushStatus{}
	for rows.Next() {
		var id int64
		var v automationport.DirectPushStatus
		if err = rows.Scan(&id, &v.SendState, &v.ObservationState, &v.FailureCode, &v.ObservationReason, &v.SentAt, &v.ObservationDueAt, &v.ObservedAt); err != nil {
			return nil, err
		}
		v.PushID = "ap_" + strconv.FormatInt(id, 10)
		byID[id] = v
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	out := make([]automationport.DirectPushStatus, 0, len(ids))
	for _, id := range ids {
		v, ok := byID[id]
		if !ok {
			return nil, automationapp.ErrDirectPushNotFound
		}
		out = append(out, v)
	}
	return out, nil
}

func (r *Repository) DirectPushReconciliationCandidates(ctx context.Context, limit int) ([]automationapp.DirectPushReconciliationCandidate, error) {
	t, err := tx(ctx)
	if err != nil || limit < 1 || limit > 1000 {
		return nil, err
	}
	rows, err := t.Query(ctx, `SELECT id FROM automation_audience_push_items WHERE send_state IN ('provider_accepted','outcome_unknown') ORDER BY updated_at,id LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]automationapp.DirectPushReconciliationCandidate, 0)
	for rows.Next() {
		var item automationapp.DirectPushReconciliationCandidate
		if err = rows.Scan(&item.ID); err != nil {
			return nil, err
		}
		item.ContentReference = "audience-direct-push_" + strconv.FormatInt(item.ID, 10)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) MarkDirectPushDelivery(ctx context.Context, id int64, evidence automationport.DirectPushDeliveryEvidence, now time.Time) (bool, error) {
	t, err := tx(ctx)
	if err != nil || id < 1 || !evidence.Ready || evidence.Delivered == evidence.Failed {
		return false, err
	}
	if evidence.Delivered {
		if evidence.SentAt == nil || evidence.SentAt.IsZero() {
			return false, automationapp.ErrDirectPushInvalid
		}
		// Cast both temporal parameters explicitly. PostgreSQL otherwise sees the
		// repeated $2 in a column assignment and an interval expression through
		// different inference paths when the statement is prepared by pgx,
		// yielding SQLSTATE 42P08 and preventing delivery reconciliation.
		tag, updateErr := t.Exec(ctx, `UPDATE automation_audience_push_items SET send_state='delivery_proven',observation_state='observing',sent_at=$2::timestamptz,observation_due_at=$2::timestamptz+INTERVAL '24 hours',failure_code='',updated_at=$3::timestamptz WHERE id=$1 AND send_state IN ('provider_accepted','outcome_unknown')`, id, evidence.SentAt.UTC(), now.UTC())
		return updateErr == nil && tag.RowsAffected() == 1, updateErr
	}
	code := strings.TrimSpace(evidence.FailureCode)
	if code == "" || len(code) > 100 {
		code = "provider_delivery_failed"
	}
	tag, updateErr := t.Exec(ctx, `UPDATE automation_audience_push_items SET send_state='final_failed',failure_code=$2,updated_at=$3::timestamptz WHERE id=$1 AND send_state IN ('provider_accepted','outcome_unknown')`, id, code, now.UTC())
	return updateErr == nil && tag.RowsAffected() == 1, updateErr
}

func (r *Repository) DirectPushObservation(ctx context.Context, id int64) (automationapp.DirectPushObservation, error) {
	t, err := tx(ctx)
	if err != nil {
		return automationapp.DirectPushObservation{}, err
	}
	var item automationapp.DirectPushObservation
	err = t.QueryRow(ctx, `SELECT id,customer_id,content_snapshot,sent_at,observation_due_at FROM automation_audience_push_items WHERE id=$1 AND send_state='delivery_proven' AND observation_state='observing'`, id).Scan(&item.ID, &item.CustomerID, &item.ContentSnapshot, &item.SentAt, &item.DueAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return item, automationapp.ErrDirectPushNotFound
	}
	return item, err
}

func (r *Repository) RecordDirectPushObservation(ctx context.Context, id int64, result automationport.DirectPushOpenResult, now time.Time) error {
	t, err := tx(ctx)
	if err != nil {
		return err
	}
	reason := strings.TrimSpace(result.Reason)
	if len(reason) > 100 {
		reason = "observation_unavailable"
	}
	var batchID int64
	err = t.QueryRow(ctx, `UPDATE automation_audience_push_items SET observation_state=$2,observation_reason=$3,observed_at=$4,updated_at=$4 WHERE id=$1 AND send_state='delivery_proven' AND observation_state='observing' RETURNING batch_id`, id, result.State, reason, now.UTC()).Scan(&batchID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf("%d:%s:%s", id, result.State, reason)))
	_, err = t.Exec(ctx, `INSERT INTO automation_audience_push_audit_events(batch_id,event_type,payload_digest,occurred_at) VALUES($1,'automation.audience_push.observed.v1',$2,$3)`, batchID, digest[:], now.UTC())
	return err
}

func (r *Repository) DirectPushAdminRecords(ctx context.Context, packageID int64, limit int) ([]automationport.DirectPushAdminRecord, error) {
	t, err := tx(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := t.Query(ctx, `SELECT i.id,i.batch_id,i.client_reference,i.sender_staff_id,i.miniprogram_id,COALESCE(i.content_snapshot->'sources'->0->>'name',''),COALESCE(NULLIF(i.content_snapshot->'sources'->0->>'version',''),'0')::bigint,CASE WHEN i.send_state IN ('accepted','queued','provider_accepted','delivery_proven','final_failed','outcome_unknown') THEN i.send_state WHEN i.send_state IN ('attempted','retryable_failed') THEN 'queued' ELSE 'final_failed' END,i.observation_state,i.failure_code,i.observation_reason,i.sent_at,i.observation_due_at,i.observed_at,i.created_at FROM automation_audience_push_items i WHERE i.package_id=$1 ORDER BY i.created_at DESC,i.id DESC LIMIT $2`, packageID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]automationport.DirectPushAdminRecord, 0)
	for rows.Next() {
		var item automationport.DirectPushAdminRecord
		var id, batchID int64
		if err = rows.Scan(&id, &batchID, &item.ClientReference, &item.SenderStaffID, &item.MiniProgramID, &item.MiniProgramName, &item.MiniProgramVersion, &item.SendState, &item.ObservationState, &item.FailureCode, &item.ObservationReason, &item.SentAt, &item.ObservationDueAt, &item.ObservedAt, &item.CreatedAt); err != nil {
			return nil, err
		}
		item.PushID = "ap_" + strconv.FormatInt(id, 10)
		item.BatchID = "apb_" + strconv.FormatInt(batchID, 10)
		items = append(items, item)
	}
	return items, rows.Err()
}

func formatBatch(id int64) string { return "apb_" + strconv.FormatInt(id, 10) }

var _ automationapp.DirectPushStore = (*Repository)(nil)
