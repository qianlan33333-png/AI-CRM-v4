package adminops

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	opsport "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/port"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

type retainedMediaFixture struct{}

func (retainedMediaFixture) CleanupExpiredUploadParts(context.Context, mediaport.UploadPartRetentionCommand) (mediaport.UploadPartRetentionReport, error) {
	return mediaport.UploadPartRetentionReport{}, nil
}
func (retainedMediaFixture) CleanupExpiredUploadPartsWithin(ctx context.Context, c mediaport.UploadPartRetentionCommand) (mediaport.UploadPartRetentionReport, error) {
	_, e := platformpostgres.RequireTransaction(ctx)
	return mediaport.UploadPartRetentionReport{}, e
}
func retentionTestService(t *testing.T) (*RetentionService, *pgxpool.Pool) {
	t.Helper()
	pool, _ := inspectionTestPool(t)
	_, file, _, _ := runtime.Caller(0)
	raw, e := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "migrations", "0188_adminops_retention.sql"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(context.Background(), string(raw)); e != nil {
		t.Fatal(e)
	}
	s, e := NewRetentionService(pool, retainedMediaFixture{}, retainedMediaFixture{}, true)
	if e != nil {
		t.Fatal(e)
	}
	return s, pool
}
func TestPostgreSQLRetentionBoundaryAndDurableReportEvidence(t *testing.T) {
	s, p := retentionTestService(t)
	ctx := context.Background()
	now := time.Now().UTC().Add(-time.Second).Truncate(time.Microsecond)
	s.now = func() time.Time { return now }
	for _, hours := range []int{696, 720, 744} {
		if _, e := p.Exec(ctx, `INSERT INTO adminops_diagnostic_events(component,code,correlation_digest,occurred_at,release_sha)VALUES('api','http_server_error',$1,$2,'release')`, string(rune(hours)), now.Add(-time.Duration(hours)*time.Hour)); e != nil {
			t.Fatal(e)
		}
	}
	old := now.Add(-744 * time.Hour)
	if _, e := p.Exec(ctx, `INSERT INTO adminops_inspection_reports(notification_kind,event_key,hour_key,target_ref,source_digest,target_digest,payload_digest,policy_digest,content,content_bytes,effect_state,created_at,updated_at)VALUES('hourly','test-hour',$1,'group','source','target','payload','policy','{"text":"old detail"}',convert_to('old detail','UTF8'),'outcome_unknown',$1,$1)`, old); e != nil {
		t.Fatal(e)
	}
	result, e := s.PruneRetentionBatch(ctx, "ops_diagnostics")
	if e != nil || result.Deleted != 1 {
		t.Fatalf("boundary result=%+v error=%v", result, e)
	}
	var count int64
	if e = p.QueryRow(ctx, `SELECT count(*) FROM adminops_diagnostic_events`).Scan(&count); e != nil || count != 2 {
		t.Fatal("29/30 day details lost", count, e)
	}
	result, e = s.PruneRetentionBatch(ctx, "ops_report_payloads")
	if e != nil || result.Deleted != 1 {
		t.Fatal(result, e)
	}
	var protected bool
	if e = p.QueryRow(ctx, `SELECT content IS NULL AND content_bytes IS NULL AND payload_pruned_at IS NOT NULL AND source_digest='source' AND target_digest='target' AND payload_digest='payload' AND policy_digest='policy' AND effect_state='outcome_unknown' FROM adminops_inspection_reports`).Scan(&protected); e != nil || !protected {
		t.Fatal("permanent notification evidence damaged", e)
	}
	if _, e = p.Exec(ctx, `DELETE FROM adminops_inspection_reports`); e == nil {
		t.Fatal("notification acceptance deletion permitted")
	}
	if _, e = p.Exec(ctx, `UPDATE adminops_inspection_reports SET payload_digest='changed'`); e == nil {
		t.Fatal("digest mutation permitted")
	}
	var measured int64
	if e = p.QueryRow(ctx, `SELECT deleted_rows FROM adminops_retention_runs WHERE policy='ops_diagnostics'`).Scan(&measured); e != nil || measured != 1 {
		t.Fatal("cleanup and count did not commit together", e)
	}
}
func TestPostgreSQLRetentionRejectsWrongPolicyAndReportsStoppedCleaner(t *testing.T) {
	s, p := retentionTestService(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if _, e := s.PruneRetentionBatch(ctx, "orders"); e == nil {
		t.Fatal("business table accepted as cleanup policy")
	}
	health, e := s.RetentionHealth(ctx, now)
	if e != nil || health.Status != "unknown" {
		t.Fatal("never-run cleaner shown healthy", health, e)
	}
	for _, policy := range s.Policies() {
		if !validRetentionPolicy(policy.ID) {
			continue
		}
		if _, e = p.Exec(ctx, `INSERT INTO adminops_retention_runs(hour_key,policy,policy_version,cutoff,state,started_at,completed_at) VALUES($1,$2,'fixture',$1::timestamptz-interval '720 hours','completed',$1,$1)`, now.Add(-24*time.Hour), policy.ID); e != nil {
			t.Fatal(e)
		}
	}
	health, e = s.RetentionHealth(ctx, now)
	if e != nil || health.Status != "stale" {
		t.Fatal("stopped cleaner shown healthy", health, e)
	}
	if _, e = p.Exec(ctx, `UPDATE adminops_retention_runs SET completed_at=$1`, now); e != nil {
		t.Fatal(e)
	}
	health, e = s.RetentionHealth(ctx, now)
	if e != nil || health.Status != "ok" {
		t.Fatal(health, e)
	}
	if _, e = p.Exec(ctx, `UPDATE adminops_retention_runs SET state='failed',completed_at=$1 WHERE policy='media_upload_parts'`, now.Add(-2*time.Hour)); e != nil {
		t.Fatal(e)
	}
	health, e = s.RetentionHealth(ctx, now)
	if e != nil || health.Status != "warning" {
		t.Fatal("old failure hidden by unrelated success", health, e)
	}
}

func (retainedMediaFixture) CleanupDiagnosticSnapshotsWithin(ctx context.Context, c opsport.SnapshotRetentionCommand) (opsport.SnapshotRetentionReport, error) {
	_, err := platformpostgres.RequireTransaction(ctx)
	return opsport.SnapshotRetentionReport{Before: c.Before}, err
}
