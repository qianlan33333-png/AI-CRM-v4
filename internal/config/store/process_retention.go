package store

import (
	"context"
	"time"

	configport "github.com/qianlan33333-png/AI-CRM-v3/internal/config/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// CleanupProcessDetailWithin deletes only config_runtime_usage observations.
// Business facts, durable receipts and restart/idempotency state stay untouched.
// The caller owns commit/rollback. No Provider or independent job is created.
func (r *Repository) CleanupProcessDetailWithin(ctx context.Context, command configport.ProcessRetentionCommand) (configport.ProcessRetentionReport, error) {
	var report configport.ProcessRetentionReport
	if command.Before.IsZero() || command.Limit < 1 || command.Limit > 1000 {
		return report, configport.ErrProcessRetentionInvalid
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
		rows, queryErr := tx.Query(ctx, `SELECT pg_column_size(t)::bigint FROM config_runtime_usage t WHERE t.used_at<$1 ORDER BY t.used_at,t.id LIMIT $2`, report.Before, command.Limit+1)
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
        SELECT t.id FROM config_runtime_usage t
        WHERE t.used_at<$1
        ORDER BY t.used_at,t.id LIMIT $2 FOR UPDATE SKIP LOCKED
    ), deleted AS (
        DELETE FROM config_runtime_usage t USING candidates c
        WHERE t.id=c.id AND t.used_at<$1
            AND t.used_at<statement_timestamp()-interval '720 hours'
        RETURNING pg_column_size(t)::bigint AS bytes
    ) SELECT (SELECT count(*) FROM candidates),count(*),COALESCE(sum(bytes),0)::bigint FROM deleted`, report.Before, command.Limit).Scan(&report.Candidates, &report.Deleted, &report.Bytes)
	if err != nil {
		return report, err
	}
	// Do not skip locked rows here: their presence must remain visible even if
	// this batch could not claim any of them. There are no incoming run/usage
	// references; any future FK remains enforced by PostgreSQL during DELETE.
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM config_runtime_usage t WHERE t.used_at<$1)`, report.Before).Scan(&report.Remaining)
	return report, err
}

var _ configport.ProcessRetention = (*Repository)(nil)
