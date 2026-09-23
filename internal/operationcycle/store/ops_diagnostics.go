package store

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	ownerport "github.com/qianlan33333-png/AI-CRM-v3/internal/operationcycle/port"
	"time"
)

// OpsDiagnosticReader reads only operationcycle-owned tables and never changes them.
type OpsDiagnosticReader struct{ pool *pgxpool.Pool }

func NewOpsDiagnosticReader(pool *pgxpool.Pool) *OpsDiagnosticReader {
	return &OpsDiagnosticReader{pool}
}
func (r *OpsDiagnosticReader) ReadOpsDiagnosticCounts(ctx context.Context, at time.Time) (map[string]int64, error) {
	if r == nil || r.pool == nil || at.IsZero() {
		return nil, errors.New("operationcycle diagnostics unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	tx, e := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SET LOCAL statement_timeout='2500ms'; SET LOCAL lock_timeout='250ms'`); e != nil {
		return nil, e
	}
	out := map[string]int64{}
	for _, q := range []struct{ key, sql string }{{"actions_failed", `SELECT count(*) FROM operation_cycle_action_requests WHERE status='failed' AND $1::timestamptz IS NOT NULL`}, {"actions_overdue", `SELECT count(*) FROM operation_cycle_action_requests WHERE status IN ('queued','claimed','thread_bound','turn_started') AND updated_at<$1::timestamptz-interval '1 hour'`}} {
		var n int64
		if e = tx.QueryRow(ctx, q.sql, at.UTC()).Scan(&n); e != nil {
			return nil, e
		}
		out[q.key] = n
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	return out, nil
}

var _ ownerport.OpsDiagnosticReader = (*OpsDiagnosticReader)(nil)
