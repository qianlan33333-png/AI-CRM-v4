package postgres

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	"time"
)

func DiagnosticCounts(ctx context.Context, pool *pgxpool.Pool, at time.Time) (map[string]int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	tx, e := DiagnosticTransaction(ctx, pool)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	var connections, waiting, longtx, size, maximum int64
	err := tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE wait_event_type='Lock'),count(*) FILTER(WHERE xact_start<$1::timestamptz-interval '5 minutes'),pg_database_size(current_database()),current_setting('max_connections')::bigint FROM pg_stat_activity WHERE datname=current_database()`, at.UTC()).Scan(&connections, &waiting, &longtx, &size, &maximum)
	return map[string]int64{"connections": connections, "max_connections": maximum, "lock_waiters": waiting, "long_transactions": longtx, "database_bytes": size}, err
}
