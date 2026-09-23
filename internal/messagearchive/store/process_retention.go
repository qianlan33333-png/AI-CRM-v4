package store

import (
	"context"
	"time"

	archiveport "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// CleanupProcessDetailWithin deletes only message_archive_sync_runs observations.
// Business facts, durable receipts and restart/idempotency state stay untouched.
// The caller owns commit/rollback. No Provider or independent job is created.
func (r PostgreSQL) CleanupProcessDetailWithin(ctx context.Context, command archiveport.ProcessRetentionCommand) (archiveport.ProcessRetentionReport, error) {
	var report archiveport.ProcessRetentionReport
	if command.Before.IsZero() || command.Limit < 1 || command.Limit > 1000 {
		return report, archiveport.ErrProcessRetentionInvalid
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return report, err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err = tx.QueryRow(ctx, `SELECT LEAST($1::timestamptz, statement_timestamp()-interval '720 hours')`, command.Before.UTC()).Scan(&report.Before); err != nil {
		return report, err
	}
	if !command.Apply {
		// The time index and LIMIT+1 bound both the preview and remaining probe.
		rows, queryErr := tx.Query(ctx, `SELECT pg_column_size(t)::bigint FROM message_archive_sync_runs t WHERE t.finished_at<$1 AND t.status IN ('succeeded','failed') ORDER BY t.finished_at,t.id LIMIT $2`, report.Before, command.Limit+1)
		if queryErr != nil {
			return report, queryErr
		}
		defer rows.Close()
		for rows.Next() {
			var size int64
			if err = rows.Scan(&size); err != nil {
				return report, err
			}
			if report.Candidates < int64(command.Limit) {
				report.Candidates++
				report.Bytes += size
			} else {
				report.Remaining = true
			}
		}
		return report, rows.Err()
	}
	// The entire maintenance UoW retains a short lock wait; row contention is
	// skipped, while DDL/table-lock contention fails rather than waiting freely.
	if _, err = tx.Exec(ctx, `SET LOCAL lock_timeout='250ms'`); err != nil {
		return report, err
	}
	err = tx.QueryRow(ctx, `WITH candidates AS MATERIALIZED (
        SELECT t.id FROM message_archive_sync_runs t
        WHERE t.finished_at<$1 AND t.status IN ('succeeded','failed')
        ORDER BY t.finished_at,t.id LIMIT $2 FOR UPDATE SKIP LOCKED
    ), deleted AS (
        DELETE FROM message_archive_sync_runs t USING candidates c
        WHERE t.id=c.id AND t.finished_at<$1 AND t.status IN ('succeeded','failed')
            AND t.finished_at<statement_timestamp()-interval '720 hours'
        RETURNING pg_column_size(t)::bigint AS bytes
    ) SELECT (SELECT count(*) FROM candidates),count(*),COALESCE(sum(bytes),0)::bigint FROM deleted`, report.Before, command.Limit).Scan(&report.Candidates, &report.Deleted, &report.Bytes)
	if err != nil {
		return report, err
	}
	// Do not skip locked rows here: their presence must remain visible even if
	// this batch could not claim any of them. There are no incoming run/usage
	// references; any future FK remains enforced by PostgreSQL during DELETE.
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM message_archive_sync_runs t WHERE t.finished_at<$1 AND t.status IN ('succeeded','failed'))`, report.Before).Scan(&report.Remaining)
	return report, err
}

var _ archiveport.ProcessRetention = PostgreSQL{}
