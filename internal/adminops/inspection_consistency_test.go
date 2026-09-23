package adminops

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	opsport "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/port"
)

type inspectionReadInterleave struct {
	once    sync.Once
	between func()
}

func (t *inspectionReadInterleave) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.HasPrefix(data.SQL, "SELECT check_id,owner,status,code,observed_at,metrics FROM adminops_inspection_results") {
		t.once.Do(t.between)
	}
	return ctx
}
func (*inspectionReadInterleave) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestPostgreSQLInspectionOverviewKeepsSnapshotAcrossConcurrentRecovery(t *testing.T) {
	pool, uow := inspectionTestPool(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	status := "critical"
	collector := opsport.CollectorFunc{ID: "identity.conflicts", Read: func(context.Context, time.Time) (opsport.CheckObservation, error) {
		return opsport.CheckObservation{Status: status, Code: "fixture_conflict", ObservedAt: now}, nil
	}}
	service, err := NewInspectionService(pool, uow, []opsport.InspectionCollector{collector}, nil, InspectionOptions{ReleaseSHA: "snapshot", Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	before, err := service.ManualScan(ctx, "before-snapshot-read")
	if err != nil {
		t.Fatal(err)
	}
	var mutationErr error
	var after opsport.InspectionRun
	config := pool.Config()
	config.ConnConfig.Tracer = &inspectionReadInterleave{between: func() {
		status = "ok"
		now = now.Add(time.Second)
		after, mutationErr = service.ManualScan(ctx, "recovery-between-reads")
	}}
	traced, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer traced.Close()
	reader, err := NewInspectionService(traced, uow, nil, nil, InspectionOptions{ReleaseSHA: "snapshot", Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := reader.Overview(ctx)
	if err != nil || mutationErr != nil {
		t.Fatalf("read=%v recovery=%v", err, mutationErr)
	}
	if snapshot.Latest == nil || snapshot.Latest.ID != before.ID || after.ID == before.ID {
		t.Fatalf("snapshot run=%+v concurrent=%+v", snapshot.Latest, after)
	}
	var criticalCheck, openIssue bool
	for _, check := range snapshot.Checks {
		if check.ID == collector.ID {
			criticalCheck = check.Status == "critical"
		}
	}
	for _, issue := range snapshot.Issues {
		if issue.CheckID == collector.ID {
			openIssue = issue.Status == "open" && issue.Severity == "critical"
		}
	}
	if !criticalCheck || !openIssue {
		t.Fatalf("mixed pre-recovery checks/post-recovery issues: check=%v issue=%v", criticalCheck, openIssue)
	}
	fresh, err := service.Overview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Latest == nil || fresh.Latest.ID != after.ID {
		t.Fatal("next overview did not observe committed recovery")
	}
	for _, issue := range fresh.Issues {
		if issue.CheckID == collector.ID {
			t.Fatal("committed recovery remained open")
		}
	}
}

func TestPostgreSQLInspectionHourlyReportWaitsForCompletionBoundary(t *testing.T) {
	pool, uow := inspectionTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	now := time.Now().UTC()
	collector := opsport.CollectorFunc{ID: "identity.conflicts", Read: func(context.Context, time.Time) (opsport.CheckObservation, error) {
		return opsport.CheckObservation{Status: "critical", Code: "fixture_conflict", ObservedAt: now}, nil
	}}
	service, err := NewInspectionService(pool, uow, []opsport.InspectionCollector{collector}, nil, InspectionOptions{ReleaseSHA: "snapshot", Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.ManualScan(ctx, "hourly-boundary-before")
	if err != nil {
		t.Fatal(err)
	}
	writer, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback(context.Background())
	if _, err = writer.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('adminops-inspection-completion',0))`); err != nil {
		t.Fatal(err)
	}
	type response struct {
		report opsport.OpsReport
		err    error
	}
	done := make(chan response, 1)
	go func() { report, e := service.PrepareReport(ctx, now); done <- response{report, e} }()
	// Observe the real PostgreSQL waiter, not an arbitrary scheduling sleep.
	deadline := time.Now().Add(3 * time.Second)
	for {
		select {
		case early := <-done:
			t.Fatalf("hourly report crossed an unfinished completion boundary: %v", early.err)
		default:
		}
		var waiting bool
		err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event_type='Lock' AND query=$1)`, `SELECT pg_advisory_xact_lock(hashtextextended('adminops-inspection-completion',0))`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("report never joined completion lock")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err = writer.Exec(ctx, `UPDATE adminops_inspection_results SET status='ok',code='fixture_recovered' WHERE run_id=$1 AND check_id='identity.conflicts'`, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = writer.Exec(ctx, `UPDATE adminops_inspection_issues SET status='resolved',critical_active=false,resolved_at=$1 WHERE check_id='identity.conflicts'`, now); err != nil {
		t.Fatal(err)
	}
	if err = writer.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	result := <-done
	if result.err != nil {
		t.Fatal(result.err)
	}
	text := string(result.report.Content)
	if !strings.Contains(text, "fixture_recovered") || strings.Contains(text, "fixture_conflict") {
		t.Fatalf("frozen report mixed scan states: %s", text)
	}
	var recorded int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM adminops_inspection_reports WHERE notification_kind='hourly'`).Scan(&recorded); err != nil || recorded != 1 {
		t.Fatal(fmt.Sprint("report rows=", recorded, " error=", err))
	}
}
