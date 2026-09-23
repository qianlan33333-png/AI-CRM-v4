package adminops

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	opsport "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
)

func selfHealthService(t *testing.T, enabled bool, now *time.Time) (*InspectionService, *pgxpool.Pool) {
	t.Helper()
	pool, uow := inspectionTestPool(t)
	s, err := NewInspectionService(pool, uow, nil, &inspectionTestAccepter{}, InspectionOptions{
		ReleaseSHA: "self-health", NotificationEnabled: enabled, NotificationTargetRef: "ops-primary",
		Now: func() time.Time { return *now },
	})
	if err != nil {
		t.Fatal(err)
	}
	return s, pool
}

func selfHealthCompletedRun(t *testing.T, pool *pgxpool.Pool, at time.Time, omit string) int64 {
	t.Helper()
	ctx := context.Background()
	var id int64
	if err := pool.QueryRow(ctx, `INSERT INTO adminops_inspection_runs(request_key,slot,state,release_sha,started_at,completed_at)
 VALUES($1,$2,'partial_failed','self-health',$2,$2) RETURNING id`, "self-fixture:"+at.Format(time.RFC3339Nano), at).Scan(&id); err != nil {
		t.Fatal(err)
	}
	for _, def := range InspectionCatalog() {
		if def.ID == omit {
			continue
		}
		if _, err := pool.Exec(ctx, `INSERT INTO adminops_inspection_results(run_id,check_id,owner,status,code,observed_at,metrics)
 VALUES($1,$2,$3,'ok','observed',$4,'{}')`, id, def.ID, def.Owner, at); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func selfHealthReport(t *testing.T, pool *pgxpool.Pool, kind, state string, hour, created time.Time) int64 {
	t.Helper()
	key := fmt.Sprintf("self-fixture:%s:%s:%s", kind, hour.Format(time.RFC3339Nano), created.Format(time.RFC3339Nano))
	var id int64
	if err := pool.QueryRow(context.Background(), `INSERT INTO adminops_inspection_reports
 (hour_key,notification_kind,event_key,target_ref,source_digest,target_digest,payload_digest,policy_digest,content,content_bytes,effect_id,effect_state,created_at,updated_at)
 VALUES($1,$2,$3,'ops-primary',$3,'target','payload','policy','{}','{}',$3,$4,$5,$5) RETURNING id`, hour, kind, key, state, created).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func selfHealthRead(t *testing.T, s *InspectionService, at time.Time) opsport.CheckObservation {
	t.Helper()
	collector := s.collectors["inspection.self"]
	if collector == nil {
		t.Fatal("default self collector missing")
	}
	o, err := collector.Collect(context.Background(), at)
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Metrics) > 32 {
		t.Fatal("self observation exceeds safe metric limit")
	}
	for _, def := range InspectionCatalog() {
		if def.ID == collector.CheckID() {
			if normalized := normalizeObservation(o, def, at); normalized.Code != o.Code {
				t.Fatalf("invalid self observation: %+v", normalized)
			}
		}
	}
	return o
}

func TestPostgreSQLInspectionSelfNewInstallAndMinuteFiveBoundary(t *testing.T) {
	now := time.Date(2026, 9, 18, 10, 30, 0, 0, time.UTC)
	s, pool := selfHealthService(t, true, &now)
	o := selfHealthRead(t, s, now)
	if o.Code != "first_scan_pending" || o.Metrics["hourly_expected"] != 0 {
		t.Fatalf("new installation invented history: %+v", o)
	}
	run, err := s.Scan(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range run.Results {
		if result.ID == "inspection.self" && (result.Code != "first_scan_pending" || result.Metrics["latest_complete_available"] != 0) {
			t.Fatalf("own running scan treated as completed/failed: %+v", result)
		}
	}
	o = selfHealthRead(t, s, now)
	if o.Status != "ok" || o.Metrics["latest_result_checks"] != int64(len(InspectionCatalog())) {
		t.Fatalf("completed first scan not visible: %+v", o)
	}
	// A future prepared report cannot satisfy an earlier due hour.
	selfHealthReport(t, pool, "hourly", "executed", now.Truncate(time.Hour).Add(time.Hour), now)
	now = now.Truncate(time.Hour).Add(time.Hour + 4*time.Minute)
	selfHealthCompletedRun(t, pool, now, "")
	if o = selfHealthRead(t, s, now); o.Metrics["hourly_expected"] != 0 {
		t.Fatalf("pre-:05 window already due: %+v", o)
	}
	now = now.Add(time.Minute)
	o = selfHealthRead(t, s, now)
	if o.Status != "ok" || o.Metrics["hourly_expected"] != 1 || o.Metrics["hourly_pending_grace"] != 1 || o.Metrics["hourly_missing"] != 0 || o.Metrics["hourly_prepared"] != 0 {
		t.Fatalf(":05 report worker grace/future window: %+v", o)
	}
	now = now.Add(2 * time.Minute)
	o = selfHealthRead(t, s, now)
	if o.Code != "hourly_windows_missing" || o.Metrics["hourly_missing"] != 1 || o.Metrics["hourly_pending_grace"] != 0 {
		t.Fatalf("missed window not detected after worker deadline: %+v", o)
	}
}

func TestPostgreSQLInspectionSelfRestartKeepsMissingWindowEvidence(t *testing.T) {
	now := time.Date(2026, 9, 18, 14, 10, 0, 0, time.UTC)
	s, pool := selfHealthService(t, true, &now)
	selfHealthCompletedRun(t, pool, now.Add(-3*time.Hour-40*time.Minute), "") // first scan 10:30
	selfHealthCompletedRun(t, pool, now.Add(-time.Minute), "")
	for _, hour := range []int{10, 12} {
		at := time.Date(2026, 9, 18, hour, 0, 0, 0, time.UTC)
		selfHealthReport(t, pool, "hourly", "executed", at, at.Add(65*time.Minute))
	}
	selfHealthReport(t, pool, "critical", "executed", now.Truncate(time.Hour).Add(-3*time.Hour), now)
	restarted, err := NewInspectionService(pool, s.uow, nil, s.effects, s.options)
	if err != nil {
		t.Fatal(err)
	}
	o := selfHealthRead(t, restarted, now)
	if o.Code != "hourly_windows_missing" || o.Metrics["hourly_expected"] != 4 || o.Metrics["hourly_prepared"] != 2 || o.Metrics["hourly_missing"] != 2 || o.Metrics["hourly_executed"] != 2 {
		t.Fatalf("restart or other notification kind hid gaps: %+v", o)
	}
	// The rolling audit never invents more than 24 due hourly windows.
	selfHealthCompletedRun(t, pool, now.Add(-72*time.Hour), "")
	o = selfHealthRead(t, restarted, now)
	if o.Metrics["hourly_expected"] != 24 || o.Metrics["hourly_missing"] != 22 {
		t.Fatalf("unbounded hourly audit: %+v", o)
	}
	// Expiring process details must not reset an established hourly reporter.
	// Its permanent receipts survive and still prove the missed later windows.
	if _, err = pool.Exec(context.Background(), `DELETE FROM adminops_inspection_results; DELETE FROM adminops_inspection_runs`); err != nil {
		t.Fatal(err)
	}
	selfHealthCompletedRun(t, pool, now, "")
	o = selfHealthRead(t, restarted, now)
	if o.Metrics["hourly_expected"] != 4 || o.Metrics["hourly_missing"] != 2 || o.Code != "hourly_windows_missing" {
		t.Fatalf("process retention reset durable reporting history: %+v", o)
	}
}

func TestPostgreSQLInspectionSelfDisabledAndNotificationStates(t *testing.T) {
	for _, tc := range []struct {
		name, state, status, code, metric string
		age                               time.Duration
		disabled                          bool
	}{
		{"queued_fresh", "queued", "ok", "self_observed", "", time.Minute, false},
		{"queued_overdue", "queued", "warning", "notification_overdue", "notification_queued_overdue", 10 * time.Minute, false},
		{"attempted_overdue", "attempted", "warning", "notification_overdue", "notification_attempted_overdue", 11 * time.Minute, false},
		{"unknown_old", "outcome_unknown", "critical", "notification_outcome_unknown", "notification_unknown", 25 * time.Hour, false},
		{"final_failed", "final_failed", "critical", "notification_final_failed", "notification_final_failed", time.Minute, false},
		{"cancelled", "cancelled", "critical", "notification_final_failed", "notification_cancelled", time.Minute, false},
		{"invalid", "unexpected", "unknown", "invalid_notification_state", "notification_invalid", time.Minute, false},
		{"terminal_history", "final_failed", "ok", "self_observed", "", 25 * time.Hour, false},
		{"disabled", "outcome_unknown", "uncovered", "notification_disabled", "notification_unknown", time.Minute, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Microsecond)
			s, pool := selfHealthService(t, !tc.disabled, &now)
			selfHealthCompletedRun(t, pool, now, "")
			selfHealthReport(t, pool, "critical", tc.state, now.Truncate(time.Hour), now.Add(-tc.age))
			o := selfHealthRead(t, s, now)
			if o.Status != tc.status || o.Code != tc.code || (tc.metric != "" && o.Metrics[tc.metric] != 1) {
				t.Fatalf("notification evidence lost or false green: %+v", o)
			}
		})
	}
}

func TestPostgreSQLInspectionSelfCountsCompletedChecksAndIgnoresCurrentRun(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Hour).Add(30 * time.Minute)
	s, pool := selfHealthService(t, true, &now)
	last := now.Add(-5 * time.Minute)
	selfHealthCompletedRun(t, pool, last, "payment.recovery")
	if _, err := pool.Exec(context.Background(), `INSERT INTO adminops_inspection_runs(request_key,slot,state,release_sha,started_at)
 VALUES('current-running',$1,'running','self-health',$1),('future-running',$2,'running','self-health',$2)`, now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	o := selfHealthRead(t, s, now)
	if o.Code != "inspection_check_count_mismatch" || o.Metrics["latest_missing_checks"] != 1 || o.Metrics["latest_result_checks"] != int64(len(InspectionCatalog())-1) || o.Metrics["latest_completion_age_seconds"] != 300 {
		t.Fatalf("running/future scan replaced completed evidence: %+v", o)
	}
	now = now.Add(71 * time.Minute)
	selfHealthReport(t, pool, "hourly", "executed", now.Truncate(time.Hour).Add(-time.Hour), now)
	o = selfHealthRead(t, s, now)
	if o.Status != "stale" || o.Code != "scan_completion_stale" {
		t.Fatalf("old completion was green: %+v", o)
	}
}

func TestPostgreSQLInspectionSelfReadIsBoundedWhenReportTableLocked(t *testing.T) {
	now := time.Now().UTC()
	s, pool := selfHealthService(t, true, &now)
	ctx := context.Background()
	locker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer locker.Rollback(ctx)
	if _, err = locker.Exec(ctx, `LOCK TABLE adminops_inspection_reports IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	o, err := s.collectSelfHealth(ctx, now)
	if err == nil || o.Status != "unknown" || time.Since(started) >= 3*time.Second {
		t.Fatalf("locked source false green or unbounded: %+v err=%v elapsed=%s", o, err, time.Since(started))
	}
}

func TestPostgreSQLInspectionSelfUnknownAlertDoesNotCreateNotificationLoop(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Hour).Add(30 * time.Minute)
	s, pool := selfHealthService(t, true, &now)
	ctx := context.Background()
	selfHealthReport(t, pool, "critical", "outcome_unknown", now.Truncate(time.Hour), now.Add(-time.Minute))
	if _, err := s.ManualScan(ctx, "self-unknown-first"); err != nil {
		t.Fatal(err)
	}
	reports, err := s.Reports(ctx)
	if err != nil || len(reports) != 2 {
		t.Fatalf("missing single self notification: count=%d err=%v", len(reports), err)
	}
	var selfEffect string
	for _, report := range reports {
		if strings.Contains(string(report.Content), "inspection.self") && report.EffectState == "queued" {
			selfEffect = report.EffectID
		}
	}
	if selfEffect == "" {
		t.Fatal("default self collector did not produce its independent notification")
	}
	if err := s.uow.Within(ctx, func(txctx context.Context) error {
		return s.ObserveOpsReportWithin(txctx, effectport.Projection{ID: selfEffect, Owner: "adminops", Kind: "feishu_ops_notification_v1", State: effectport.StateUnknown})
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		now = now.Add(time.Minute)
		if _, err = s.ManualScan(ctx, fmt.Sprintf("self-unknown-repeat-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	var total, accepts, issues int64
	var active bool
	if err = pool.QueryRow(ctx, `SELECT
 (SELECT count(*) FROM adminops_inspection_reports),
 (SELECT count(*) FROM inspection_test_acceptances),
 (SELECT count(*) FROM adminops_inspection_issues WHERE check_id='inspection.self' AND code='notification_outcome_unknown'),
 (SELECT critical_active FROM adminops_inspection_issues WHERE check_id='inspection.self' AND code='notification_outcome_unknown')`).Scan(&total, &accepts, &issues, &active); err != nil {
		t.Fatal(err)
	}
	if total != 2 || accepts != 1 || issues != 1 || !active {
		t.Fatalf("recursive or duplicated alert: reports=%d accepts=%d issues=%d active=%v", total, accepts, issues, active)
	}
	if o := selfHealthRead(t, s, now); o.Code != "notification_outcome_unknown" || o.Metrics["notification_unknown"] != 2 {
		t.Fatalf("unknown reports were hidden or retried: %+v", o)
	}
}
