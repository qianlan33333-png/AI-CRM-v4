package adminops

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	opsport "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestPostgreSQLRetentionCountsOnlyChangedPayloadBytes(t *testing.T) {
	service, pool := retentionTestService(t)
	ctx := context.Background()
	now := time.Now().UTC().Add(-time.Second).Truncate(time.Microsecond)
	service.now = func() time.Time { return now }
	old := now.Add(-721 * time.Hour)
	var run int64
	if err := pool.QueryRow(ctx, `INSERT INTO adminops_inspection_runs(request_key,slot,state,release_sha,started_at,completed_at) VALUES('bytes-fixture',$1,'completed','test',$1,$1) RETURNING id`, old).Scan(&run); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO adminops_inspection_results(run_id,check_id,owner,status,code,observed_at,metrics) VALUES($1,'fixture','adminops','ok','fixture',$2,'{"count":123,"another_count":789}')`, run, old); err != nil {
		t.Fatal(err)
	}
	preview, err := service.PreviewRetention(ctx, "ops_results")
	if err != nil || preview.Candidates != 1 || preview.EstimatedPayloadBytes <= 0 {
		t.Fatalf("result preview=%+v %v", preview, err)
	}
	result, err := service.PruneRetentionBatch(ctx, "ops_results")
	if err != nil || result.Deleted != 1 || result.PayloadBytes != preview.EstimatedPayloadBytes {
		t.Fatalf("result bytes=%+v want=%+v err=%v", result, preview, err)
	}
	var recorded int64
	if err = pool.QueryRow(ctx, `SELECT payload_bytes FROM adminops_retention_runs WHERE policy='ops_results'`).Scan(&recorded); err != nil || recorded != result.PayloadBytes {
		t.Fatalf("atomic byte receipt=%d %v", recorded, err)
	}
	// Two eligible reports, one exactly on the cutoff, one fresh. A held row
	// lock excludes one candidate; only the row actually pruned is counted.
	for _, item := range []struct {
		key string
		at  time.Time
	}{{"free", old}, {"locked", old}, {"boundary", now.Add(-720 * time.Hour)}, {"fresh", now}} {
		if _, err = pool.Exec(ctx, `INSERT INTO adminops_inspection_reports(notification_kind,event_key,hour_key,target_ref,source_digest,target_digest,payload_digest,policy_digest,content,content_bytes,effect_state,created_at,updated_at) VALUES('hourly',$1,$2,'target',$1,$1,$1,$1,'{"msg_type":"text","content":{"text":"measured payload"}}',convert_to('measured payload','UTF8'),'disabled',$3,$3)`, item.key, item.at.Add(time.Duration(len(item.key))*time.Second), item.at); err != nil {
			t.Fatal(err)
		}
	}
	var expectedBytes int64
	if err = pool.QueryRow(ctx, `SELECT octet_length(content_bytes)::bigint+pg_column_size(content) FROM adminops_inspection_reports WHERE event_key='free'`).Scan(&expectedBytes); err != nil {
		t.Fatal(err)
	}
	lock, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback(context.Background())
	if _, err = lock.Exec(ctx, `SELECT id FROM adminops_inspection_reports WHERE event_key='locked' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	result, err = service.PruneRetentionBatch(ctx, "ops_report_payloads")
	if err != nil || result.Deleted != 1 || result.PayloadBytes != expectedBytes {
		t.Fatalf("actual affected report bytes=%+v expected=%d err=%v", result, expectedBytes, err)
	}
	if err = pool.QueryRow(ctx, `SELECT payload_bytes FROM adminops_retention_runs WHERE policy='ops_report_payloads'`).Scan(&recorded); err != nil || recorded != expectedBytes {
		t.Fatalf("report byte receipt=%d %v", recorded, err)
	}
	if err = lock.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var untouched int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM adminops_inspection_reports WHERE content IS NOT NULL`).Scan(&untouched); err != nil || untouched != 3 {
		t.Fatalf("locked/boundary/fresh damaged=%d %v", untouched, err)
	}
}

func TestPostgreSQLInspectionResumeProtectsActiveRunButNotAbandonedDetails(t *testing.T) {
	retention, pool := retentionTestService(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	retention.now = func() time.Time { return now }
	owned, err := platformpostgres.Open(ctx, platformpostgres.Config{URL: pool.Config().ConnString()})
	if err != nil {
		t.Fatal(err)
	}
	defer owned.Close()
	uow, err := platformpostgres.NewUnitOfWork(owned)
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := manualInspectionDigest("resume-running-command")
	old := now.Add(-721 * time.Hour)
	var resumedID int64
	if err = pool.QueryRow(ctx, `INSERT INTO adminops_inspection_runs(request_key,slot,state,release_sha,started_at) VALUES($1,$2,'running','old',$2) RETURNING id`, digest, old).Scan(&resumedID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO adminops_inspection_runs(request_key,slot,state,release_sha,started_at) VALUES('abandoned-running',$1,'running','old',$1),('boundary-running',$2,'running','old',$2)`, old, now.Add(-720*time.Hour)); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	collector := opsport.CollectorFunc{ID: "identity.conflicts", Read: func(ctx context.Context, _ time.Time) (opsport.CheckObservation, error) {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return opsport.CheckObservation{}, ctx.Err()
		}
		return opsport.CheckObservation{Status: "ok", Code: "resumed", ObservedAt: now}, nil
	}}
	service, err := NewInspectionService(pool, uow, []opsport.InspectionCollector{collector}, nil, InspectionOptions{ReleaseSHA: "new", Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	type response struct {
		run opsport.InspectionRun
		err error
	}
	done := make(chan response, 1)
	go func() { r, e := service.ManualScan(ctx, "resume-running-command"); done <- response{r, e} }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("resumed collector not entered")
	}
	// Release on every assertion path so the test never leaves a live scan.
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	var started time.Time
	var state, sha string
	if err = pool.QueryRow(ctx, `SELECT started_at,state,release_sha FROM adminops_inspection_runs WHERE id=$1`, resumedID).Scan(&started, &state, &sha); err != nil || !started.Equal(now) || state != "running" || sha != "new" {
		t.Fatalf("active retry not refreshed: %s %s %s %v", started, state, sha, err)
	}
	result, err := retention.PruneRetentionBatch(ctx, "ops_runs")
	if err != nil || result.Deleted != 1 || result.PayloadBytes <= 0 {
		t.Fatalf("abandoned cleanup=%+v %v", result, err)
	}
	var alive int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM adminops_inspection_runs WHERE id=$1 OR request_key='boundary-running'`, resumedID).Scan(&alive); err != nil || alive != 2 {
		t.Fatalf("active/boundary process removed=%d %v", alive, err)
	}
	close(release)
	finished := <-done
	if finished.err != nil || finished.run.ID != resumedID {
		t.Fatalf("resumed completion=%+v %v", finished.run, finished.err)
	}
	// Replaying a completed command must not renew its 30-day detail clock.
	now = now.Add(time.Hour)
	if _, err = service.ManualScan(ctx, "resume-running-command"); err != nil {
		t.Fatal(err)
	}
	var replayedStart time.Time
	if err = pool.QueryRow(ctx, `SELECT started_at FROM adminops_inspection_runs WHERE id=$1`, resumedID).Scan(&replayedStart); err != nil || !replayedStart.Equal(started) {
		t.Fatalf("completed replay extended retention=%s %v", replayedStart, err)
	}
}

func TestPostgreSQLRetentionSkipsConcurrentRunRefresh(t *testing.T) {
	retention, pool := retentionTestService(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	retention.now = func() time.Time { return now }
	var id int64
	if err := pool.QueryRow(ctx, `INSERT INTO adminops_inspection_runs(request_key,slot,state,release_sha,started_at) VALUES('locked-resume',$1,'running','old',$1) RETURNING id`, now.Add(-721*time.Hour)).Scan(&id); err != nil {
		t.Fatal(err)
	}
	refresh, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer refresh.Rollback(context.Background())
	if _, err = refresh.Exec(ctx, `UPDATE adminops_inspection_runs SET started_at=$2 WHERE id=$1`, id, now); err != nil {
		t.Fatal(err)
	}
	result, err := retention.PruneRetentionBatch(ctx, "ops_runs")
	if err != nil || result.Deleted != 0 || result.PayloadBytes != 0 {
		t.Fatalf("refresh race=%+v %v", result, err)
	}
	if err = refresh.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var actual time.Time
	if err = pool.QueryRow(ctx, `SELECT started_at FROM adminops_inspection_runs WHERE id=$1`, id).Scan(&actual); err != nil || !actual.Equal(now) {
		t.Fatalf("committed refresh lost=%s %v", actual, err)
	}
}

type releaseReaderFixture struct {
	report platformport.ReleaseMaintenanceReport
	err    error
}

func (f releaseReaderFixture) ReadReleaseLatest(context.Context) (platformport.ReleaseMaintenanceReport, error) {
	return f.report, f.err
}

type hostReaderFixture struct {
	report platformport.HostMaintenanceReport
	err    error
}

func (f hostReaderFixture) ReadLatest(context.Context) (platformport.HostMaintenanceReport, error) {
	return f.report, f.err
}
func TestPostgreSQLRetentionPartialReleaseEvidenceSurvivesReadAPI(t *testing.T) {
	service, _ := retentionTestService(t)
	ctx := context.Background()
	if got := service.ReleaseLatest(ctx); got["status"] != "unknown" {
		t.Fatalf("unconfigured=%+v", got)
	}
	now := time.Now().UTC()
	zero, one := int64(0), int64(1)
	report := platformport.ReleaseMaintenanceReport{Version: 1, ReleaseSHA: strings.Repeat("a", 40), ObservedAt: now, State: "gap", Reason: "two_verified_schema_compatible_rollbacks_missing", DeletedCount: &zero, DeletedBytes: &zero, RollbackCount: &one, SpaceObservations: []platformport.HostSpaceObservation{{Resource: "release_storage", State: "unknown"}}}
	if err := service.BindReleaseReader(releaseReaderFixture{report: report, err: errors.Join(errors.New("fixture"), platformport.ErrHostMaintenancePartial)}); err != nil {
		t.Fatal(err)
	}
	if err := service.BindHostReader(hostReaderFixture{report: platformport.HostMaintenanceReport{Version: 1, StartedAt: now, State: "partial_failed", FailureCodes: []string{"journal_cleanup_failed"}, SpaceObservations: []platformport.HostSpaceObservation{{Resource: "journal_runtime_storage", State: "unknown"}}}, err: platformport.ErrHostMaintenancePartial}); err != nil {
		t.Fatal(err)
	}
	h, err := NewRetentionHandler(service, inspectionTestSecurity{})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/admin/ops-retention/runs", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"release"`) || !strings.Contains(w.Body.String(), report.Reason) || !strings.Contains(w.Body.String(), "journal_cleanup_failed") || !strings.Contains(w.Body.String(), "space_observations") {
		t.Fatalf("partial evidence lost: %d %s", w.Code, w.Body.String())
	}
	service.release = releaseReaderFixture{report: report, err: errors.New("invalid proof")}
	if got := service.ReleaseLatest(ctx); got["status"] != "unknown" || got["deleted_bytes"] != nil {
		t.Fatalf("invalid proof exposed=%+v", got)
	}
}
