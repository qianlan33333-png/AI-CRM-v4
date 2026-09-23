package store

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	customerapp "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/app"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type TagHistoryPostgreSQL struct{}

func (TagHistoryPostgreSQL) ApplyHistoricalTagRecords(ctx context.Context, batch customerport.HistoricalTagBatch, records []customerport.HistoricalTagRecord) (customerport.HistoricalTagImportResult, error) {
	if !validHistoricalTagBatch(batch) {
		return customerport.HistoricalTagImportResult{}, customerport.ErrTagCommandInvalid
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return customerport.HistoricalTagImportResult{}, err
	}
	var batchID int64
	if err = tx.QueryRow(ctx, `INSERT INTO customer_tag_history_import_batches(source_system,snapshot_digest,snapshot_at)
		VALUES($1,$2,$3) ON CONFLICT(source_system,snapshot_digest) DO UPDATE SET snapshot_at=EXCLUDED.snapshot_at
		RETURNING id`, batch.SourceSystem, batch.SnapshotDigest, batch.SnapshotAt.UTC()).Scan(&batchID); err != nil {
		return customerport.HistoricalTagImportResult{}, err
	}
	result := customerport.HistoricalTagImportResult{}
	for _, record := range records {
		if !validHistoricalTagRecord(record) {
			return customerport.HistoricalTagImportResult{}, customerport.ErrTagCommandInvalid
		}
		var customer, staff any
		if record.CustomerID > 0 {
			customer = record.CustomerID
		}
		if record.StaffID > 0 {
			staff = record.StaffID
		}
		var inserted bool
		err = tx.QueryRow(ctx, `INSERT INTO customer_tag_history_receipts(batch_id,source_system,source_job_id,source_digest,effect_type,operation,source_state,resolution,reason,customer_id,staff_id,add_tag_ids,remove_tag_ids,occurred_at,completed_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,''),$10,$11,$12,$13,$14,$15)
			ON CONFLICT(source_system,source_job_id) DO NOTHING RETURNING true`, batchID, batch.SourceSystem, record.SourceJobID, record.SourceDigest, record.EffectType, record.Operation, record.SourceState, record.Resolution, record.Reason, customer, staff, normalizedIDs(record.AddTagIDs), normalizedIDs(record.RemoveTagIDs), record.OccurredAt.UTC(), record.CompletedAt).Scan(&inserted)
		if errors.Is(err, pgx.ErrNoRows) {
			var previousDigest string
			if err = tx.QueryRow(ctx, `SELECT source_digest FROM customer_tag_history_receipts WHERE source_system=$1 AND source_job_id=$2`, batch.SourceSystem, record.SourceJobID).Scan(&previousDigest); err != nil {
				return customerport.HistoricalTagImportResult{}, err
			}
			if previousDigest != record.SourceDigest {
				return customerport.HistoricalTagImportResult{}, customerport.ErrTagCommandConflict
			}
			result.Replayed++
			continue
		}
		if err != nil || !inserted {
			return customerport.HistoricalTagImportResult{}, err
		}
		switch record.Resolution {
		case "imported":
			result.Imported++
		case "pending":
			result.Pending++
		case "conflict":
			result.Conflict++
		case "excluded":
			result.Excluded++
		default:
			result.Failed++
		}
	}
	return result, nil
}

func (TagHistoryPostgreSQL) VerifyHistoricalTagRecords(ctx context.Context, batch customerport.HistoricalTagBatch, records []customerport.HistoricalTagRecord) (customerport.HistoricalTagImportResult, error) {
	if !validHistoricalTagBatch(batch) {
		return customerport.HistoricalTagImportResult{}, customerport.ErrTagCommandInvalid
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return customerport.HistoricalTagImportResult{}, err
	}
	var batchID int64
	if err = tx.QueryRow(ctx, `SELECT id FROM customer_tag_history_import_batches WHERE source_system=$1 AND snapshot_digest=$2`, batch.SourceSystem, batch.SnapshotDigest).Scan(&batchID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return customerport.HistoricalTagImportResult{}, customerport.ErrTagCommandUnavailable
		}
		return customerport.HistoricalTagImportResult{}, err
	}
	result := customerport.HistoricalTagImportResult{}
	for _, record := range records {
		if !validHistoricalTagRecord(record) {
			return customerport.HistoricalTagImportResult{}, customerport.ErrTagCommandInvalid
		}
		var digest, effectType, operation, sourceState, resolution, reason string
		var customerID, staffID *int64
		var add, remove []int64
		var occurredAt time.Time
		var completedAt *time.Time
		// Receipts are globally idempotent by (source_system, source_job_id). A
		// later protected snapshot may intentionally overlap a previous capture,
		// so verification compares that immutable receipt rather than demand a
		// duplicate row in the current snapshot batch.
		err = tx.QueryRow(ctx, `SELECT source_digest,effect_type,operation,source_state,resolution,COALESCE(reason,''),customer_id,staff_id,add_tag_ids,remove_tag_ids,occurred_at,completed_at
			FROM customer_tag_history_receipts WHERE source_system=$1 AND source_job_id=$2`, batch.SourceSystem, record.SourceJobID).Scan(&digest, &effectType, &operation, &sourceState, &resolution, &reason, &customerID, &staffID, &add, &remove, &occurredAt, &completedAt)
		if err != nil || digest != record.SourceDigest || effectType != record.EffectType || operation != record.Operation || sourceState != record.SourceState || resolution != record.Resolution || reason != record.Reason || int64OrZero(customerID) != int64(record.CustomerID) || int64OrZero(staffID) != record.StaffID || !sameIDs(add, record.AddTagIDs) || !sameIDs(remove, record.RemoveTagIDs) || !occurredAt.UTC().Equal(record.OccurredAt.UTC()) || !sameHistoricalTime(completedAt, record.CompletedAt) {
			return customerport.HistoricalTagImportResult{}, customerport.ErrTagCommandConflict
		}
		switch resolution {
		case "imported":
			result.Imported++
		case "pending":
			result.Pending++
		case "conflict":
			result.Conflict++
		case "excluded":
			result.Excluded++
		default:
			result.Failed++
		}
	}
	return result, nil
}

func validHistoricalTagBatch(batch customerport.HistoricalTagBatch) bool {
	return batch.SourceSystem == "v2_external_effect_job" && effectport.ValidDigest(effectport.Digest(batch.SnapshotDigest)) && !batch.SnapshotAt.IsZero()
}
func validHistoricalTagRecord(record customerport.HistoricalTagRecord) bool {
	if record.SourceJobID < 1 || !effectport.ValidDigest(effectport.Digest(record.SourceDigest)) || record.OccurredAt.IsZero() || (record.EffectType != "wecom.contact.tag.mark" && record.EffectType != "wecom.contact.tag.unmark") || (record.Operation != "tag_mark" && record.Operation != "tag_unmark") || strings.TrimSpace(record.SourceState) != record.SourceState || record.SourceState == "" || strings.TrimSpace(record.Reason) != record.Reason || len(record.Reason) > 64 {
		return false
	}
	if record.Resolution != "imported" && record.Resolution != "pending" && record.Resolution != "conflict" && record.Resolution != "excluded" && record.Resolution != "failed" {
		return false
	}
	add, remove := normalizedIDs(record.AddTagIDs), normalizedIDs(record.RemoveTagIDs)
	if len(add) != len(record.AddTagIDs) || len(remove) != len(record.RemoveTagIDs) || len(add)+len(remove) > 100 || (record.EffectType == "wecom.contact.tag.mark" && len(remove) != 0) || (record.EffectType == "wecom.contact.tag.unmark" && len(add) != 0) {
		return false
	}
	return record.Resolution != "imported" || (record.CustomerID > 0 && len(add)+len(remove) > 0)
}
func normalizedIDs(ids []int64) []int64 {
	if len(ids) == 0 {
		return []int64{}
	}
	out := append([]int64(nil), ids...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	for i, id := range out {
		if id < 1 || i > 0 && id == out[i-1] {
			return nil
		}
	}
	return out
}
func sameIDs(left, right []int64) bool {
	left, right = normalizedIDs(left), normalizedIDs(right)
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
func sameHistoricalTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.UTC().Equal(right.UTC())
}

func int64OrZero(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

var _ customerapp.HistoricalTagStore = TagHistoryPostgreSQL{}
