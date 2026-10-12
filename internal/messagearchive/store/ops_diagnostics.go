package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	archiveport "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type OpsDiagnosticReader struct{ pool *pgxpool.Pool }

func NewOpsDiagnosticReader(pool *pgxpool.Pool) *OpsDiagnosticReader {
	return &OpsDiagnosticReader{pool: pool}
}

func (r *OpsDiagnosticReader) ReadOpsDiagnosticCounts(ctx context.Context, at time.Time) (map[string]int64, error) {
	if r == nil || r.pool == nil || at.IsZero() {
		return nil, errors.New("archive diagnostics unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	tx, err := platformpostgres.DiagnosticTransaction(ctx, r.pool)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var sources, blocked, failures, imported, age int64
	err = tx.QueryRow(ctx, `SELECT
 (SELECT count(*) FROM message_archive_sync_state),
 (SELECT count(*) FROM (SELECT status,error_code FROM message_archive_sync_runs ORDER BY id DESC LIMIT 1) latest WHERE status='failed' AND error_code NOT IN ('work_budget_exhausted','previous_attempt_unfinished')),
 (SELECT count(*) FROM message_archive_sync_runs WHERE status='failed' AND error_code NOT IN ('work_budget_exhausted','previous_attempt_unfinished') AND started_at >= $1::timestamptz-interval '1 hour'),
 (SELECT count(*) FROM message_archive_messages WHERE created_at >= $1::timestamptz-interval '1 hour'),
 (SELECT greatest(0,coalesce(extract(epoch FROM $1::timestamptz-max(last_pull_succeeded_at)),0))::bigint FROM message_archive_sync_state)`, at.UTC()).Scan(&sources, &blocked, &failures, &imported, &age)
	return map[string]int64{"observed_sources": sources, "ingestion_blocked": blocked, "failures_last_hour": failures, "imported_last_hour": imported, "last_success_age_seconds": age}, err
}

var _ archiveport.OpsDiagnosticReader = (*OpsDiagnosticReader)(nil)
