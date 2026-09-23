package store

import (
	"context"
	"time"

	adminopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

var _ adminopsport.SnapshotRetention = (*ProjectionStore)(nil)

// CleanupDiagnosticSnapshotsWithin only deletes the diagnostic process table;
// release projections remain durable evidence and are never touched here.
func (store *ProjectionStore) CleanupDiagnosticSnapshotsWithin(ctx context.Context, command adminopsport.SnapshotRetentionCommand) (adminopsport.SnapshotRetentionReport, error) {
	report := adminopsport.SnapshotRetentionReport{Before: command.Before.UTC()}
	if store == nil || ctx == nil || command.Before.IsZero() || command.Limit < 1 || command.Limit > 1000 {
		return report, adminopsport.ErrSnapshotRetentionRequest
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return report, err
	}
	var now time.Time
	if err = tx.QueryRow(ctx, `SELECT transaction_timestamp()`).Scan(&now); err != nil {
		return report, err
	}
	if command.Before.After(now.Add(-30 * 24 * time.Hour)) {
		return report, adminopsport.ErrSnapshotRetentionRequest
	}
	if !command.Apply {
		err = tx.QueryRow(ctx, `SELECT LEAST(count(*),$2),COALESCE(sum(size) FILTER(WHERE position<=$2),0),count(*)>$2 FROM (
 SELECT pg_column_size(details) AS size,row_number() OVER(ORDER BY observed_at,id) AS position
 FROM adminops_diagnostic_snapshots WHERE observed_at<$1 ORDER BY observed_at,id LIMIT $2+1) candidates`, command.Before, command.Limit).Scan(&report.Candidates, &report.Bytes, &report.Remaining)
		return report, err
	}
	var locked bool
	if err = tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtext('adminops.diagnostic_snapshot_retention'))`).Scan(&locked); err != nil {
		return report, err
	}
	if !locked {
		return report, adminopsport.ErrSnapshotRetentionBusy
	}
	err = tx.QueryRow(ctx, `WITH candidates AS (
 SELECT id FROM adminops_diagnostic_snapshots WHERE observed_at<$1
 ORDER BY observed_at,id LIMIT $2 FOR UPDATE SKIP LOCKED
), deleted AS (
 DELETE FROM adminops_diagnostic_snapshots snapshot USING candidates WHERE snapshot.id=candidates.id
 RETURNING pg_column_size(snapshot.details) AS size
) SELECT count(*),COALESCE(sum(size),0) FROM deleted`, command.Before, command.Limit).Scan(&report.Deleted, &report.Bytes)
	if err != nil {
		return report, err
	}
	report.Candidates = report.Deleted
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM adminops_diagnostic_snapshots WHERE observed_at<$1)`, command.Before).Scan(&report.Remaining)
	return report, err
}
