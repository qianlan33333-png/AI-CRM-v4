package jobqueue

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

const (
	// River v0.24.0 reports each queue at startup and every ten minutes.
	// Its public Config does not expose this cadence. The runtime contract test
	// checks the actual producer config so a dependency upgrade cannot silently
	// invalidate this observation window.
	riverQueueReportInterval = 10 * time.Minute
	// Allow startup jitter (up to one second), the ten-second report SQL timeout,
	// and ordinary database/scheduler delays without hiding a missed report.
	riverQueueReportGrace = 2 * time.Minute
)

// DiagnosticCounts uses scheduled_at, not creation age: future appointments
// are not backlog. Historical terminal failures are reported separately.
func DiagnosticCounts(ctx context.Context, pool *pgxpool.Pool, at time.Time) (map[string]int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	tx, e := platformpostgres.DiagnosticTransaction(ctx, pool)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	var overdue, age, running, discarded, future, completed int64
	err := tx.QueryRow(ctx, `SELECT
 count(*) FILTER(WHERE state IN ('available','scheduled','retryable') AND scheduled_at<$1::timestamptz-interval '10 minutes'),
 coalesce(extract(epoch FROM $1::timestamptz-min(scheduled_at) FILTER(WHERE state IN ('available','scheduled','retryable') AND scheduled_at<$1)),0)::bigint,
 count(*) FILTER(WHERE state='running' AND attempted_at<$1::timestamptz-interval '10 minutes'),
 count(*) FILTER(WHERE state='discarded'),
 count(*) FILTER(WHERE state IN ('scheduled','retryable') AND scheduled_at>$1),
 count(*) FILTER(WHERE state='completed' AND finalized_at >= $1::timestamptz-interval '1 hour')
 FROM river_job`, at.UTC()).Scan(&overdue, &age, &running, &discarded, &future, &completed)
	return map[string]int64{"due_over_10m": overdue, "oldest_due_seconds": age, "running_over_10m": running, "discarded_retained": discarded, "future_scheduled": future, "completed_last_hour": completed}, err
}

// Queue observations are advisory live worker evidence maintained by River.
// A queue may be idle or paused and still report. Job completions are not a
// heartbeat, and one queue's report cannot prove another expected queue is live.
// These observations do not prove an external host can reach this machine.
func WorkerDiagnosticCounts(ctx context.Context, pool *pgxpool.Pool, at time.Time, queues ...string) (map[string]int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	tx, e := platformpostgres.DiagnosticTransaction(ctx, pool)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	var fresh, stale, paused, missing int64
	expected := queues
	if len(expected) == 0 {
		expected = []string{OutboundQueue, OutboundWelcomeQueue, OutboundExcelQueue, OutboundMediaQueue, OpsInspectionQueue, OpsNotificationQueue, OpsRetentionQueue}
	}
	cutoff := at.UTC().Add(-riverQueueReportInterval - riverQueueReportGrace)
	err := tx.QueryRow(ctx, `SELECT count(*) FILTER(WHERE q.name IS NOT NULL AND q.updated_at >= $1::timestamptz),count(*) FILTER(WHERE q.name IS NOT NULL AND q.updated_at < $1::timestamptz),count(*) FILTER(WHERE q.paused_at IS NOT NULL),count(*) FILTER(WHERE q.name IS NULL) FROM unnest($2::text[]) expected(name) LEFT JOIN river_queue q USING(name)`, cutoff, expected).Scan(&fresh, &stale, &paused, &missing)
	return map[string]int64{"expected_queues": int64(len(expected)), "fresh_queue_observations": fresh, "stale_queue_observations": stale, "missing_queue_observations": missing, "paused_queues": paused}, err
}
