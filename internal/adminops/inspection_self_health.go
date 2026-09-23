package adminops

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	opsport "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/port"
)

// collectSelfHealth reads only AdminOps-owned facts. The persisted first scan,
// not process start, determines eligible reporting windows after a restart.
// Permanent hourly receipts retain that evidence after process-detail cleanup.
// The current running scan is deliberately excluded from completed evidence.
func (s *InspectionService) collectSelfHealth(ctx context.Context, now time.Time) (opsport.CheckObservation, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	o := opsport.CheckObservation{Status: "unknown", Code: "source_unavailable", ObservedAt: now, Metrics: map[string]int64{}}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return o, err
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		_ = tx.Rollback(cleanup)
	}()
	if _, err = tx.Exec(ctx, `SET LOCAL statement_timeout='2500ms'; SET LOCAL lock_timeout='250ms'`); err != nil {
		return o, err
	}
	ids := make([]string, 0, len(InspectionCatalog()))
	for _, check := range InspectionCatalog() {
		ids = append(ids, check.ID)
	}
	m := o.Metrics
	m["catalog_checks"] = int64(len(ids))
	m["configured_checks"] = int64(len(s.collectors))
	if s.options.NotificationEnabled {
		m["notification_enabled"] = 1
	} else {
		m["notification_enabled"] = 0
	}
	var first, baseline, completed *time.Time
	var resultCount, catalogCount int64
	err = tx.QueryRow(ctx, `WITH first_scan AS (
 SELECT started_at FROM adminops_inspection_runs WHERE started_at<=$1 ORDER BY started_at,id LIMIT 1
), first_report AS (
 SELECT hour_key+interval '5 minutes' AS baseline FROM adminops_inspection_reports
 WHERE notification_kind='hourly' AND created_at<=$1 AND hour_key+interval '65 minutes'<=$1
 ORDER BY hour_key LIMIT 1
), latest AS (
 SELECT id,completed_at FROM adminops_inspection_runs
 WHERE state IN ('completed','partial_failed') AND completed_at IS NOT NULL AND completed_at<=$1 AND started_at<=$1
 ORDER BY started_at DESC,id DESC LIMIT 1
)
SELECT (SELECT started_at FROM first_scan),least((SELECT started_at FROM first_scan),(SELECT baseline FROM first_report)),(SELECT completed_at FROM latest),
 (SELECT count(*) FROM adminops_inspection_results WHERE run_id=(SELECT id FROM latest)),
 (SELECT count(*) FROM adminops_inspection_results WHERE run_id=(SELECT id FROM latest) AND check_id=ANY($2::text[]))`, now, ids).Scan(&first, &baseline, &completed, &resultCount, &catalogCount)
	if err != nil {
		return o, err
	}
	m["first_scan_available"], m["hourly_baseline_available"], m["latest_complete_available"], m["latest_completion_age_seconds"] = 0, 0, 0, 0
	m["latest_result_checks"], m["latest_catalog_checks"] = resultCount, catalogCount
	m["latest_missing_checks"], m["latest_unexpected_checks"] = int64(len(ids))-catalogCount, resultCount-catalogCount
	if first != nil {
		m["first_scan_available"] = 1
	}
	if baseline != nil {
		m["hourly_baseline_available"] = 1
	}
	if completed != nil {
		m["latest_complete_available"] = 1
		m["latest_completion_age_seconds"] = int64(now.Sub(*completed) / time.Second)
	}

	// A window's hour_key names its previous-hour flow; its due time is the
	// following hour at :05. Keep the due count exact while allowing the
	// existing two-minute report worker to scan and freeze its own payload.
	latestHour := now.UTC().Add(-5 * time.Minute).Truncate(time.Hour).Add(-time.Hour)
	var expected, prepared, missing, grace, executed, reconciled, unaccepted int64
	err = tx.QueryRow(ctx, `WITH due AS (
 SELECT h AS hour_key,h+interval '65 minutes' AS due_at
 FROM generate_series($1::timestamptz-interval '23 hours',$1::timestamptz,interval '1 hour') h
 WHERE $2::timestamptz IS NOT NULL AND h+interval '65 minutes'>$2
), windows AS (
 SELECT d.hour_key,d.due_at,r.id,r.effect_id,r.effect_state
 FROM due d LEFT JOIN adminops_inspection_reports r ON r.hour_key=d.hour_key
 AND r.notification_kind='hourly' AND r.created_at<=$3
)
SELECT count(DISTINCT hour_key),count(DISTINCT hour_key) FILTER(WHERE id IS NOT NULL),
 count(*) FILTER(WHERE id IS NULL AND due_at+interval '2 minutes'<=$3),
 count(*) FILTER(WHERE id IS NULL AND due_at+interval '2 minutes'>$3),
 count(*) FILTER(WHERE effect_state='executed'),count(*) FILTER(WHERE effect_state='reconciled'),
 count(*) FILTER(WHERE id IS NOT NULL AND effect_id IS NULL)
FROM windows`, latestHour, baseline, now).Scan(&expected, &prepared, &missing, &grace, &executed, &reconciled, &unaccepted)
	if err != nil {
		return o, err
	}
	m["hourly_expected"], m["hourly_prepared"], m["hourly_missing"], m["hourly_pending_grace"] = expected, prepared, missing, grace
	m["hourly_executed"], m["hourly_reconciled"], m["hourly_unaccepted"] = executed, reconciled, unaccepted

	// Unknown and other unfinished sends remain visible even when older than
	// 24h. Completed terminal failures are a 24h incident count, so a failed
	// immutable historical report cannot leave the service red forever.
	var queued, attempted, unknown, failed, cancelled, invalid, disabled int64
	err = tx.QueryRow(ctx, `SELECT
 count(*) FILTER(WHERE effect_state IN ('accepted','queued','retryable_failed') AND created_at<=$1::timestamptz-interval '10 minutes'),
 count(*) FILTER(WHERE effect_state='attempted' AND created_at<=$1::timestamptz-interval '10 minutes'),
 count(*) FILTER(WHERE effect_state='outcome_unknown'),
 count(*) FILTER(WHERE effect_state='final_failed' AND created_at>=$1::timestamptz-interval '24 hours'),
 count(*) FILTER(WHERE effect_state='cancelled' AND created_at>=$1::timestamptz-interval '24 hours'),
 count(*) FILTER(WHERE effect_state NOT IN ('disabled','accepted','queued','retryable_failed','attempted','outcome_unknown','executed','reconciled','final_failed','cancelled')),
 count(*) FILTER(WHERE effect_state='disabled' AND created_at>=$1::timestamptz-interval '24 hours')
 FROM adminops_inspection_reports WHERE created_at<=$1
 AND (created_at>=$1::timestamptz-interval '24 hours' OR effect_state NOT IN ('disabled','executed','reconciled','final_failed','cancelled'))`, now).Scan(&queued, &attempted, &unknown, &failed, &cancelled, &invalid, &disabled)
	if err != nil {
		return o, err
	}
	m["notification_queued_overdue"], m["notification_attempted_overdue"], m["notification_unknown"] = queued, attempted, unknown
	m["notification_final_failed"], m["notification_cancelled"], m["notification_invalid"], m["notification_disabled_recent"] = failed, cancelled, invalid, disabled
	if err = tx.Commit(ctx); err != nil {
		return o, err
	}
	o.Status, o.Code = "ok", "self_observed"
	switch {
	case !s.options.NotificationEnabled:
		o.Status, o.Code = "uncovered", "notification_disabled"
	case unknown > 0:
		o.Status, o.Code = "critical", "notification_outcome_unknown"
	case failed+cancelled > 0:
		o.Status, o.Code = "critical", "notification_final_failed"
	case missing > 0:
		o.Status, o.Code = "critical", "hourly_windows_missing"
	case invalid > 0:
		o.Status, o.Code = "unknown", "invalid_notification_state"
	case queued+attempted > 0:
		o.Status, o.Code = "warning", "notification_overdue"
	case unaccepted > 0:
		o.Status, o.Code = "uncovered", "hourly_notification_unaccepted"
	case completed == nil:
		o.Status, o.Code = "unknown", "first_scan_pending"
	case now.Sub(*completed) > 75*time.Minute:
		o.Status, o.Code = "stale", "scan_completion_stale"
	case catalogCount != int64(len(ids)) || resultCount != catalogCount:
		o.Status, o.Code = "warning", "inspection_check_count_mismatch"
	}
	return o, nil
}
