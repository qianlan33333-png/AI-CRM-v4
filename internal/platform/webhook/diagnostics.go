package webhook

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"time"
)

func DiagnosticCounts(ctx context.Context, pool *pgxpool.Pool, at time.Time) (map[string]int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	tx, e := platformpostgres.DiagnosticTransaction(ctx, pool)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	var failed, overdue, expired int64
	err := tx.QueryRow(ctx, `SELECT count(*) FILTER(WHERE status='failed'),count(*) FILTER(WHERE status IN ('received','retryable') AND next_attempt_at<$1::timestamptz-interval '10 minutes'),count(*) FILTER(WHERE status='processing' AND lease_expires_at<$1) FROM webhook_inbox`, at.UTC()).Scan(&failed, &overdue, &expired)
	return map[string]int64{"failed": failed, "due_over_10m": overdue, "expired_processing_leases": expired}, err
}
