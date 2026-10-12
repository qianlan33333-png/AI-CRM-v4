package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	ownerport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type OpsDiagnosticReader struct{ pool *pgxpool.Pool }

func NewOpsDiagnosticReader(pool *pgxpool.Pool) *OpsDiagnosticReader {
	return &OpsDiagnosticReader{pool: pool}
}
func (r *OpsDiagnosticReader) ReadOpsDiagnosticCounts(ctx context.Context, at time.Time) (map[string]int64, error) {
	if r == nil || r.pool == nil || at.IsZero() {
		return nil, errors.New("group catalog diagnostics unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	tx, err := platformpostgres.DiagnosticTransaction(ctx, r.pool)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var ready, failed, overdue int64
	err = tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM group_ops_directory_groups WHERE sync_state='ready'),(SELECT count(*) FROM group_ops_directory_groups WHERE sync_state='failed'),(SELECT count(*) FROM group_ops_catalog_runs WHERE state='running' AND started_at<$1::timestamptz-interval '1 hour')`, at.UTC()).Scan(&ready, &failed, &overdue)
	return map[string]int64{"groups_ready": ready, "groups_unreadable": failed, "scans_overdue": overdue}, err
}

var _ ownerport.OpsDiagnosticReader = (*OpsDiagnosticReader)(nil)
