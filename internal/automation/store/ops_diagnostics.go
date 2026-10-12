package store

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	ownerport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	"time"
)

// OpsDiagnosticReader reads only automation-owned tables and never changes them.
type OpsDiagnosticReader struct{ pool *pgxpool.Pool }

func NewOpsDiagnosticReader(pool *pgxpool.Pool) *OpsDiagnosticReader {
	return &OpsDiagnosticReader{pool}
}
func (r *OpsDiagnosticReader) ReadOpsDiagnosticCounts(ctx context.Context, at time.Time) (map[string]int64, error) {
	if r == nil || r.pool == nil || at.IsZero() {
		return nil, errors.New("automation diagnostics unavailable")
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
	// Provider acceptance of a WeCom send task still awaits employee delivery.
	// It is neither proved delivery nor unfinished system execution. Keep the
	// existing business states intact and count that wait separately.
	const waiting = `EXISTS(SELECT 1 FROM automation_run_recipients rr WHERE rr.run_id=r.id AND rr.state='provider_accepted')
 AND NOT EXISTS(SELECT 1 FROM automation_run_recipients rr WHERE rr.run_id=r.id AND rr.state NOT IN ('provider_accepted','delivery_proven','reconciled','cancelled'))
 AND NOT EXISTS(SELECT 1 FROM automation_generation_items g WHERE g.run_id=r.id AND g.state IN ('accepted','queued'))`
	for _, q := range []struct{ key, sql string }{
		{"runs_unknown", `SELECT count(*) FROM automation_runs WHERE state='outcome_unknown' AND $1::timestamptz IS NOT NULL`},
		{"runs_partial_failed", `SELECT count(*) FROM automation_runs WHERE state='partial_failed' AND $1::timestamptz IS NOT NULL`},
		{"runs_awaiting_delivery", `SELECT count(*) FROM automation_runs r WHERE r.state='executing' AND (` + waiting + `) AND $1::timestamptz IS NOT NULL`},
		{"runs_overdue", `SELECT count(*) FROM automation_runs r WHERE r.state='executing' AND r.updated_at<$1::timestamptz-interval '1 hour' AND NOT (` + waiting + `)`},
	} {
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
