package outbox

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"time"
)

// Expected event types come from composition's explicit consumer catalog.
// Unclassified audit events are counted as a coverage gap, never delivery failure.
func DiagnosticCounts(ctx context.Context, pool *pgxpool.Pool, at time.Time, expected []string) (map[string]int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	tx, e := platformpostgres.DiagnosticTransaction(ctx, pool)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	var due, unclassified int64
	err := tx.QueryRow(ctx, `SELECT count(*) FILTER(WHERE processed_at IS NULL AND event_type=ANY($2::text[]) AND occurred_at<$1::timestamptz-interval '10 minutes'),count(*) FILTER(WHERE processed_at IS NULL AND NOT(event_type=ANY($2::text[]))) FROM outbox_events`, at.UTC(), expected).Scan(&due, &unclassified)
	return map[string]int64{"expected_consumer_overdue": due, "without_consumer_obligation": unclassified}, err
}
