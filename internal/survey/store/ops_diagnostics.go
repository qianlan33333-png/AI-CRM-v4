package store

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	ownerport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	"time"
)

// OpsDiagnosticReader reads only survey-owned tables and never changes them.
type OpsDiagnosticReader struct{ pool *pgxpool.Pool }

func NewOpsDiagnosticReader(pool *pgxpool.Pool) *OpsDiagnosticReader {
	return &OpsDiagnosticReader{pool}
}
func (r *OpsDiagnosticReader) ReadOpsDiagnosticCounts(ctx context.Context, at time.Time) (map[string]int64, error) {
	if r == nil || r.pool == nil || at.IsZero() {
		return nil, errors.New("survey diagnostics unavailable")
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
	for _, q := range []struct{ key, sql string }{{"missing_claim_submission", `SELECT count(*) FROM survey_submission_claims WHERE submission_id IS NULL AND claimed_at<$1::timestamptz-interval '5 minutes'`}, {"claim_owner_mismatch", `SELECT count(*) FROM survey_submission_claims c JOIN survey_submissions s ON s.id=c.submission_id WHERE (s.questionnaire_id<>c.questionnaire_id OR s.customer_id IS DISTINCT FROM c.customer_id) AND $1::timestamptz IS NOT NULL`}, {"definition_mismatch", `SELECT count(*) FROM survey_submissions s JOIN survey_definition_versions d ON d.id=s.definition_version_id WHERE s.questionnaire_id<>d.questionnaire_id AND $1::timestamptz IS NOT NULL`}, {"identity_conflicts", `SELECT count(*) FROM survey_submissions WHERE identity_state='conflict' AND submitted_at>=$1::timestamptz-interval '24 hours'`}} {
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
