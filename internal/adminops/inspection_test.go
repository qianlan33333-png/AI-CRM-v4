package adminops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	opsport "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformdiagnostics "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/diagnostics"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/riverqueue/river"
)

func TestInspectionObservationNeverMakesMissingOrInvalidEvidenceGreen(t *testing.T) {
	now := time.Now().UTC()
	def := InspectionCatalog()[0]
	for _, input := range []opsport.CheckObservation{
		{Status: "ok", Code: "observed", ObservedAt: now.Add(-76 * time.Minute)},
		{Status: "ok", Code: "observed"},
		{Status: "ok", Code: "user@example.com", ObservedAt: now},
		{Status: "ok", Code: "observed", ObservedAt: now, Metrics: map[string]int64{"phone:123": 1}},
		{Status: "ok", Code: "observed", ObservedAt: now, Metrics: map[string]int64{"count": -1}},
	} {
		if got := normalizeObservation(input, def, now); got.Status == "ok" {
			t.Fatalf("false green: %+v", got)
		}
	}
	if len(InspectionCatalog()) < 20 {
		t.Fatal("coverage directory unexpectedly shrank")
	}
}

func TestInspectionPeriodicJobsUseOnlyHourlyPoll(t *testing.T) {
	jobs := InspectionPeriodicJobs()
	if len(jobs) != 1 {
		t.Fatalf("periodic jobs=%d, want one hourly poll", len(jobs))
	}
}

func TestInspectionHourlyScheduleUsesClockMinuteFive(t *testing.T) {
	at := time.Date(2026, 9, 18, 10, 4, 0, 0, time.UTC)
	if got := (opsHourlySchedule{}).Next(at); !got.Equal(at.Add(time.Minute)) {
		t.Fatalf("next=%s", got)
	}
	at = at.Add(time.Minute)
	if got := (opsHourlySchedule{}).Next(at); !got.Equal(at.Add(time.Hour)) {
		t.Fatalf("boundary next=%s", got)
	}
}

func TestPostgreSQLInspectionPayloadExpiryKeepsAcceptanceEvidence(t *testing.T) {
	pool, uow := inspectionTestPool(t)
	ctx := context.Background()
	now := time.Now().UTC().Add(-721 * time.Hour)
	a := &inspectionTestAccepter{}
	s, e := NewInspectionService(pool, uow, nil, a, InspectionOptions{ReleaseSHA: "test", NotificationEnabled: true, NotificationTargetRef: "ops-primary", Now: func() time.Time { return now }})
	if e != nil {
		t.Fatal(e)
	}
	r, e := s.PrepareReport(ctx, now)
	if e != nil {
		t.Fatal(e)
	}
	now = time.Now().UTC()
	if _, e = s.LoadOpsReportPayload(ctx, a.envelope); !errors.Is(e, opsport.ErrOpsReportPayloadExpired) {
		t.Fatalf("expired provider payload=%v", e)
	}
	list, e := s.Reports(ctx)
	if e != nil || len(list) != 0 {
		t.Fatalf("expired report read=%v %v", list, e)
	}
	if _, e = pool.Exec(ctx, `UPDATE adminops_inspection_reports SET content=NULL,content_bytes=NULL,payload_pruned_at=clock_timestamp() WHERE id=$1`, r.ID); e != nil {
		t.Fatal(e)
	}
	var id, digest string
	if e = pool.QueryRow(ctx, `SELECT effect_id,payload_digest FROM adminops_inspection_reports WHERE id=$1`, r.ID).Scan(&id, &digest); e != nil || id != r.EffectID || digest != string(a.envelope.PayloadDigest) {
		t.Fatalf("lost acceptance=%s %s %v", id, digest, e)
	}
	if _, e = pool.Exec(ctx, `UPDATE adminops_inspection_reports SET content='{}',content_bytes='restored',payload_pruned_at=NULL WHERE id=$1`, r.ID); e == nil {
		t.Fatal("expired payload restored")
	}
}

func TestPostgreSQLInspectionsPersistUnknownReplayAndIssueCAS(t *testing.T) {
	pool, uow := inspectionTestPool(t)
	now := time.Now().UTC().Truncate(time.Second)
	status := "warning"
	c := opsport.CollectorFunc{ID: "identity.conflicts", Read: func(context.Context, time.Time) (opsport.CheckObservation, error) {
		return opsport.CheckObservation{Status: status, Code: "open_conflicts", ObservedAt: now, Metrics: map[string]int64{"open": 2}}, nil
	}}
	s, e := NewInspectionService(pool, uow, []opsport.InspectionCollector{c}, nil, InspectionOptions{ReleaseSHA: "test", Now: func() time.Time { return now }})
	if e != nil {
		t.Fatal(e)
	}
	first, e := s.Scan(context.Background(), now)
	if e != nil {
		t.Fatal(e)
	}
	if first.State != "partial_failed" || len(first.Results) != len(InspectionCatalog()) {
		t.Fatalf("run=%+v", first)
	}
	again, e := s.Scan(context.Background(), now)
	if e != nil || again.ID != first.ID {
		t.Fatalf("replay=%+v %v", again, e)
	}
	overview, e := s.Overview(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	var issue opsport.InspectionIssue
	for _, i := range overview.Issues {
		if i.CheckID == c.ID {
			issue = i
		}
	}
	if issue.ID == 0 || issue.Occurrences != 1 {
		t.Fatalf("issue=%+v", issue)
	}
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results <- s.UpdateIssue(context.Background(), issue.ID, issue.Version, 7, "acknowledged", fmt.Sprintf("acknowledge-%d", i))
		}(i)
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for e := range results {
		if e == nil {
			success++
		} else if errors.Is(e, ErrInspectionConflict) {
			conflict++
		} else {
			t.Fatal(e)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("CAS=%d/%d", success, conflict)
	}
	now = now.Add(5 * time.Minute)
	status = "unknown"
	if _, e = s.Scan(context.Background(), now); e != nil {
		t.Fatal(e)
	}
	var state string
	if e = pool.QueryRow(context.Background(), `SELECT status FROM adminops_inspection_issues WHERE id=$1`, issue.ID).Scan(&state); e != nil || state == "resolved" {
		t.Fatalf("unknown resolved issue=%s %v", state, e)
	}
	now = now.Add(5 * time.Minute)
	status = "ok"
	if _, e = s.Scan(context.Background(), now); e != nil {
		t.Fatal(e)
	}
	if e = pool.QueryRow(context.Background(), `SELECT status FROM adminops_inspection_issues WHERE id=$1`, issue.ID).Scan(&state); e != nil || state != "resolved" {
		t.Fatalf("fresh recovery=%s %v", state, e)
	}
	now = now.Add(76 * time.Minute)
	overview, e = s.Overview(context.Background())
	if e != nil || overview.Fresh {
		t.Fatalf("stale overview=%+v %v", overview, e)
	}
	for _, r := range overview.Checks {
		if r.ID == c.ID && r.Status != "stale" {
			t.Fatalf("stale check=%+v", r)
		}
	}
}

type inspectionTestAccepter struct {
	fail     bool
	envelope effectport.Envelope
}

func (a *inspectionTestAccepter) AcceptAndQueueWithin(ctx context.Context, c effectport.AcceptCommand) (effectport.Projection, effectport.Receipt, error) {
	tx, e := platformpostgres.RequireTransaction(ctx)
	if e != nil {
		return effectport.Projection{}, effectport.Receipt{}, e
	}
	a.envelope = c.Envelope
	if c.Lane != "ops_notification" {
		return effectport.Projection{}, effectport.Receipt{}, errors.New("wrong lane")
	}
	var effectNumber int64
	e = tx.QueryRow(ctx, `INSERT INTO inspection_test_acceptances(key) VALUES($1) RETURNING id`, string(c.ReceiptKey)).Scan(&effectNumber)
	if e != nil {
		return effectport.Projection{}, effectport.Receipt{}, e
	}
	if a.fail {
		return effectport.Projection{}, effectport.Receipt{}, errors.New("injected acceptance failure")
	}
	return effectport.Projection{ID: fmt.Sprintf("eer_%d", effectNumber), Owner: "adminops", Kind: "feishu_ops_notification_v1", State: effectport.StateQueued}, effectport.Receipt{}, nil
}
func TestPostgreSQLInspectionReportAtomicAcceptanceImmutableReplayAndObserver(t *testing.T) {
	pool, uow := inspectionTestPool(t)
	ctx := context.Background()
	now := time.Now().UTC()
	a := &inspectionTestAccepter{fail: true}
	s, e := NewInspectionService(pool, uow, nil, a, InspectionOptions{ReleaseSHA: "test", NotificationEnabled: true, NotificationTargetRef: "ops-primary", Now: func() time.Time { return now }})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.PrepareReport(ctx, now); e == nil {
		t.Fatal("expected rollback")
	}
	var reports, accepts int
	if e = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM adminops_inspection_reports),(SELECT count(*) FROM inspection_test_acceptances)`).Scan(&reports, &accepts); e != nil || reports != 0 || accepts != 0 {
		t.Fatalf("split transaction %d/%d %v", reports, accepts, e)
	}
	a.fail = false
	r, e := s.PrepareReport(ctx, now)
	if e != nil {
		t.Fatal(e)
	}
	replay, e := s.PrepareReport(ctx, now.Add(time.Minute))
	if e != nil || r.ID != replay.ID || string(r.Content) != string(replay.Content) {
		t.Fatalf("changed replay %v", e)
	}
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM inspection_test_acceptances`).Scan(&accepts); e != nil || accepts != 1 {
		t.Fatalf("duplicate effect %d %v", accepts, e)
	}
	var message map[string]any
	if e = json.Unmarshal(r.Content, &message); e != nil || message["msg_type"] != "text" || len(r.Content) > 24000 {
		t.Fatalf("invalid provider payload %s", r.Content)
	}
	loaded, e := s.LoadOpsReportPayload(ctx, a.envelope)
	if e != nil || string(loaded.Content) != string(r.Content) {
		t.Fatalf("payload read %v", e)
	}
	corrupt := a.envelope
	corrupt.PayloadDigest = effectport.Hash("corrupt")
	if _, e = s.LoadOpsReportPayload(ctx, corrupt); !errors.Is(e, ErrInspectionConflict) {
		t.Fatalf("envelope mismatch=%v", e)
	}
	if _, e = pool.Exec(ctx, `UPDATE adminops_inspection_reports SET content_bytes='changed' WHERE id=$1`, r.ID); e == nil {
		t.Fatal("report content mutable")
	}
	p := effectport.Projection{ID: r.EffectID, Owner: "adminops", Kind: "feishu_ops_notification_v1", State: effectport.StateUnknown}
	if e = uow.Within(ctx, func(txctx context.Context) error { return s.ObserveOpsReportWithin(txctx, p) }); e != nil {
		t.Fatal(e)
	}
	list, e := s.Reports(ctx)
	if e != nil || len(list) != 1 || list[0].EffectState != "outcome_unknown" {
		t.Fatalf("report=%+v %v", list, e)
	}
}

type inspectionTestSecurity struct{ deny, csrfDeny bool }

func (s inspectionTestSecurity) ReadPrincipal(context.Context, *http.Request) (accessdomain.Principal, error) {
	if s.deny {
		return accessdomain.Principal{}, errors.New("denied")
	}
	return accessdomain.Principal{InternalID: 7, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}}, nil
}
func (s inspectionTestSecurity) AuthorizeCSRF(ctx context.Context, r *http.Request) (accessdomain.Principal, error) {
	if s.csrfDeny {
		return accessdomain.Principal{}, errors.New("csrf")
	}
	return s.ReadPrincipal(ctx, r)
}
func TestPostgreSQLInspectionHTTPPermissionsManualReadbackAndSafeErrors(t *testing.T) {
	pool, uow := inspectionTestPool(t)
	s, e := NewInspectionService(pool, uow, nil, nil, InspectionOptions{ReleaseSHA: "test"})
	if e != nil {
		t.Fatal(e)
	}
	for _, security := range []inspectionTestSecurity{{deny: true}, {csrfDeny: true}} {
		h, _ := NewInspectionHandler(s, security)
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/api/admin/ops-inspections/runs", strings.NewReader(`{}`))
		r.Header.Set("Idempotency-Key", "manual-test-one")
		h.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("status=%d", w.Code)
		}
	}
	h, _ := NewInspectionHandler(s, inspectionTestSecurity{})
	if e = h.BindManualEnqueuer(inspectionCommandQueue(t, pool, uow, nil)); e != nil {
		t.Fatal(e)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/admin/ops-inspections/runs", strings.NewReader(`{}`))
	r.Header.Set("Idempotency-Key", "manual-test-one")
	h.ServeHTTP(w, r)
	if w.Code != 202 {
		t.Fatalf("manual=%d %s", w.Code, w.Body.String())
	}
	var accepted opsport.ManualInspectionAcceptance
	if e = json.Unmarshal(w.Body.Bytes(), &accepted); e != nil {
		t.Fatal(e)
	}
	worker := NewInspectionWorker()
	if e = worker.BindService(s); e != nil {
		t.Fatal(e)
	}
	if e = worker.Work(context.Background(), &river.Job[InspectionJobArgs]{Args: acceptedInspectionArgs(t, pool, accepted.JobID)}); e != nil {
		t.Fatal(e)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/admin/ops-inspections", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "uncovered") {
		t.Fatalf("read=%d %s", w.Code, w.Body.String())
	}
	if e = s.RecordDiagnostic(context.Background(), "http", "internal_error", "unsafe-token-value"); e != nil {
		t.Fatal(e)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/admin/ops-diagnostics", nil))
	if w.Code != 200 || strings.Contains(w.Body.String(), "unsafe-token-value") || !strings.Contains(w.Body.String(), "correlation_digest") {
		t.Fatalf("diagnostics=%s", w.Body.String())
	}
}

func inspectionTestPool(t *testing.T) (*pgxpool.Pool, *platformpostgres.UnitOfWork) {
	return inspectionTestPoolWithDiagnosticMigration(t, true)
}

func inspectionTestPoolWithDiagnosticMigration(t *testing.T, includeIdentity bool) (*pgxpool.Pool, *platformpostgres.UnitOfWork) {
	t.Helper()
	raw, e := platformconfig.DatabaseURL()
	if e != nil {
		t.Skip("AICRM_DATABASE_URL is required")
	}
	ctx := context.Background()
	admin, e := pgxpool.New(ctx, raw)
	if e != nil {
		t.Fatal(e)
	}
	schema := fmt.Sprintf("ops_inspection_test_%d", time.Now().UnixNano())
	if _, e = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); e != nil {
		t.Fatal(e)
	}
	parsed, e := url.Parse(raw)
	if e != nil {
		t.Fatal(e)
	}
	q := parsed.Query()
	q.Set("search_path", schema)
	q.Set("application_name", schema)
	parsed.RawQuery = q.Encode()
	pool, e := platformpostgres.Open(ctx, platformpostgres.Config{URL: parsed.String()})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
	})
	_, file, _, _ := runtime.Caller(0)
	sql, e := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "migrations", "0186_adminops_inspections.sql"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Native().Exec(ctx, string(sql)); e != nil {
		t.Fatal(e)
	}
	if includeIdentity {
		applyDiagnosticIdentityMigration(t, pool.Native())
	}
	applyGovernanceOutcomesMigration(t, pool.Native())
	if _, e = pool.Native().Exec(ctx, `CREATE TABLE inspection_test_acceptances(id BIGINT GENERATED ALWAYS AS IDENTITY, key TEXT PRIMARY KEY)`); e != nil {
		t.Fatal(e)
	}
	uow, e := platformpostgres.NewUnitOfWork(pool)
	if e != nil {
		t.Fatal(e)
	}
	return pool.Native(), uow
}

func applyDiagnosticIdentityMigration(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	sql, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../migrations/0192_adminops_diagnostic_event_identity.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(context.Background(), string(sql)); err != nil {
		t.Fatal(err)
	}
}

func TestPostgreSQLInspectionLateCompletionCannotReopenNewerRecovery(t *testing.T) {
	pool, uow := inspectionTestPool(t)
	ctx := context.Background()
	old := time.Now().UTC().Add(-time.Minute)
	entered, release := make(chan struct{}), make(chan struct{})
	collector := opsport.CollectorFunc{ID: "identity.conflicts", Read: func(context.Context, time.Time) (opsport.CheckObservation, error) {
		close(entered)
		<-release
		return opsport.CheckObservation{Status: "warning", Code: "open_conflicts", ObservedAt: old}, nil
	}}
	a, e := NewInspectionService(pool, uow, []opsport.InspectionCollector{collector}, nil, InspectionOptions{ReleaseSHA: "test", Now: func() time.Time { return old }})
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { _, err := a.ManualScan(ctx, "old-inspection"); done <- err }()
	<-entered
	now := old.Add(time.Minute)
	collector.Read = func(context.Context, time.Time) (opsport.CheckObservation, error) {
		return opsport.CheckObservation{Status: "ok", Code: "no_conflicts", ObservedAt: now}, nil
	}
	b, e := NewInspectionService(pool, uow, []opsport.InspectionCollector{collector}, nil, InspectionOptions{ReleaseSHA: "test", Now: func() time.Time { return now }})
	if e != nil {
		t.Fatal(e)
	}
	newer, e := b.ManualScan(ctx, "new-inspection")
	if e != nil {
		t.Fatal(e)
	}
	close(release)
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	result, e := b.Overview(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if result.Latest.ID != newer.ID {
		t.Fatal("late completion replaced newer run")
	}
	for _, issue := range result.Issues {
		if issue.CheckID == collector.ID {
			t.Fatal("late failure reopened recovered check")
		}
	}
}

func TestPostgreSQLInspectionCriticalAndRecoveryAreMergedAndDeduplicated(t *testing.T) {
	pool, uow := inspectionTestPool(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	status := "critical"
	stale := false
	// Isolate the two business signals under test. The default self collector's
	// separate alert for an unknown notification is covered by its PG journey.
	collectors := []opsport.InspectionCollector{opsport.CollectorFunc{ID: "inspection.self", Read: func(_ context.Context, at time.Time) (opsport.CheckObservation, error) {
		return opsport.CheckObservation{Status: "ok", Code: "observed", ObservedAt: at}, nil
	}}}
	for _, id := range []string{"identity.conflicts", "payment.recovery"} {
		collectors = append(collectors, opsport.CollectorFunc{ID: id, Read: func(context.Context, time.Time) (opsport.CheckObservation, error) {
			observed := now
			if stale {
				observed = now.Add(-76 * time.Minute)
			}
			return opsport.CheckObservation{Status: status, Code: "invariant_violation", ObservedAt: observed}, nil
		}})
	}
	accepter := &inspectionTestAccepter{}
	service, e := NewInspectionService(pool, uow, collectors, accepter, InspectionOptions{ReleaseSHA: "test", NotificationEnabled: true, NotificationTargetRef: "ops-primary", Now: func() time.Time { return now }})
	if e != nil {
		t.Fatal(e)
	}
	run, e := service.ManualScan(ctx, "critical-scan-first")
	if e != nil {
		t.Fatal(e)
	}
	reports, e := service.Reports(ctx)
	if e != nil || len(reports) != 1 || reports[0].NotificationKind != "critical" || !strings.Contains(string(reports[0].Content), "identity.conflicts") || !strings.Contains(string(reports[0].Content), "payment.recovery") {
		t.Fatalf("unmerged alerts=%+v %v", reports, e)
	}
	criticalEnvelope := accepter.envelope
	loaded, e := service.LoadOpsReportPayload(ctx, criticalEnvelope)
	if e != nil || loaded.SourceDigest != criticalEnvelope.SourceRefDigest || loaded.PolicyDigest != criticalEnvelope.PolicyVersionHash || loaded.EventKey != "critical:run:"+fmt.Sprint(run.ID) || loaded.NotificationKind != "critical" {
		t.Fatalf("event envelope=%+v %v", loaded, e)
	}
	if loaded.SourceDigest != effectport.Hash("ops-event-source-v1", "critical", loaded.EventKey) || loaded.PolicyDigest != effectport.Hash("ops-report-policy-v1", "critical-v1") {
		t.Fatal("event binding changed")
	}
	if e = uow.Within(ctx, func(txctx context.Context) error {
		return service.ObserveOpsReportWithin(txctx, effectport.Projection{ID: reports[0].EffectID, Owner: "adminops", Kind: "feishu_ops_notification_v1", State: effectport.StateUnknown})
	}); e != nil {
		t.Fatal(e)
	}
	if _, e = service.ManualScan(ctx, "critical-scan-first"); e != nil {
		t.Fatal(e)
	}
	for i, observation := range []string{"critical", "unknown", "warning", "critical", "ok"} {
		now = now.Add(time.Minute)
		status = observation
		stale = observation == "ok"
		if _, e = service.ManualScan(ctx, fmt.Sprintf("repeated-scan-%d", i)); e != nil {
			t.Fatal(e)
		}
	}
	reports, e = service.Reports(ctx)
	if e != nil || len(reports) != 1 || reports[0].EffectState != "outcome_unknown" {
		t.Fatalf("duplicate/false recovery=%+v %v", reports, e)
	}
	now = now.Add(time.Minute)
	stale = false
	status = "ok"
	if _, e = service.ManualScan(ctx, "fresh-recovery-first"); e != nil {
		t.Fatal(e)
	}
	reports, e = service.Reports(ctx)
	if e != nil || len(reports) != 2 || reports[0].NotificationKind != "recovery" {
		t.Fatalf("missing merged recovery=%+v %v", reports, e)
	}
	if _, e = service.ManualScan(ctx, "fresh-recovery-first"); e != nil {
		t.Fatal(e)
	}
	now = now.Add(time.Minute)
	if _, e = service.ManualScan(ctx, "fresh-recovery-again"); e != nil {
		t.Fatal(e)
	}
	status = "critical"
	now = now.Add(time.Minute)
	if _, e = service.ManualScan(ctx, "new-critical-incident"); e != nil {
		t.Fatal(e)
	}
	if _, e = service.PrepareReport(ctx, now); e != nil {
		t.Fatal(e)
	}
	reports, e = service.Reports(ctx)
	if e != nil || len(reports) != 4 {
		t.Fatalf("new lifecycle/hourly=%+v %v", reports, e)
	}
	var accepts int
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM inspection_test_acceptances`).Scan(&accepts); e != nil || accepts != 4 {
		t.Fatalf("effect accepts=%d %v", accepts, e)
	}
	if _, e = pool.Exec(ctx, `UPDATE adminops_inspection_reports SET event_key='changed' WHERE id=$1`, reports[0].ID); e == nil {
		t.Fatal("notification identity mutable")
	}
}

func TestPostgreSQLInspectionCriticalAcceptanceFailureRollsBackDecision(t *testing.T) {
	pool, uow := inspectionTestPool(t)
	ctx := context.Background()
	now := time.Now().UTC()
	a := &inspectionTestAccepter{fail: true}
	c := opsport.CollectorFunc{ID: "identity.conflicts", Read: func(context.Context, time.Time) (opsport.CheckObservation, error) {
		return opsport.CheckObservation{Status: "critical", Code: "critical_conflict", ObservedAt: now}, nil
	}}
	s, e := NewInspectionService(pool, uow, []opsport.InspectionCollector{c}, a, InspectionOptions{ReleaseSHA: "test", NotificationEnabled: true, NotificationTargetRef: "ops-primary", Now: func() time.Time { return now }})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ManualScan(ctx, "atomic-critical-scan"); e == nil {
		t.Fatal("expected effect acceptance failure")
	}
	var results, issues, reports, accepts int
	e = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM adminops_inspection_results),(SELECT count(*) FROM adminops_inspection_issues),(SELECT count(*) FROM adminops_inspection_reports),(SELECT count(*) FROM inspection_test_acceptances)`).Scan(&results, &issues, &reports, &accepts)
	if e != nil || results+issues+reports+accepts != 0 {
		t.Fatalf("split evidence/results=%d issues=%d reports=%d accepts=%d err=%v", results, issues, reports, accepts, e)
	}
	a.fail = false
	if _, e = s.ManualScan(ctx, "atomic-critical-scan"); e != nil {
		t.Fatal(e)
	}
	list, e := s.Reports(ctx)
	if e != nil || len(list) != 1 {
		t.Fatalf("retry=%+v %v", list, e)
	}
}

func TestPostgreSQLInspectionRunReadWindowDoesNotDependOnCleaner(t *testing.T) {
	pool, uow := inspectionTestPool(t)
	now := time.Now().UTC().Add(-721 * time.Hour)
	s, e := NewInspectionService(pool, uow, nil, nil, InspectionOptions{ReleaseSHA: "test", Now: func() time.Time { return now }})
	if e != nil {
		t.Fatal(e)
	}
	run, e := s.ManualScan(context.Background(), "expired-run-readback")
	if e != nil {
		t.Fatal(e)
	}
	now = now.Add(721 * time.Hour)
	if _, e = s.Run(context.Background(), run.ID); !errors.Is(e, ErrInspectionNotFound) {
		t.Fatalf("expired run still readable: %v", e)
	}
	overview, e := s.Overview(context.Background())
	if e != nil || overview.Latest != nil || overview.Fresh {
		t.Fatalf("expired evidence=%+v %v", overview, e)
	}
	if NewReportWorker().Timeout(nil) <= time.Duration(len(InspectionCatalog()))*3*time.Second {
		t.Fatal("report worker cannot finish bounded unhealthy collectors")
	}
}

func TestPostgreSQLInspectionClientEventsRequirePermissionCSRFAndClosedPayload(t *testing.T) {
	pool, uow := inspectionTestPool(t)
	s, e := NewInspectionService(pool, uow, nil, nil, InspectionOptions{ReleaseSHA: "test"})
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		body     string
		security inspectionTestSecurity
		want     int
	}{
		{`{"code":"frontend_error"}`, inspectionTestSecurity{}, 200},
		{`{"code":"frontend_error","message":"secret"}`, inspectionTestSecurity{}, 400},
		{`{"code":"secret"}`, inspectionTestSecurity{}, 400},
		{`{"code":"frontend_error"}`, inspectionTestSecurity{deny: true}, 403},
		{`{"code":"frontend_error"}`, inspectionTestSecurity{csrfDeny: true}, 403},
	} {
		h, _ := NewInspectionHandler(s, tc.security)
		r := httptest.NewRequest("POST", "/api/admin/ops-diagnostics/client-events", strings.NewReader(tc.body))
		ctx, e := platformdiagnostics.WithCorrelation(r.Context(), platformdiagnostics.Correlation{RequestID: strings.Repeat("a", 32), ReleaseSHA: strings.Repeat("b", 40)})
		if e != nil {
			t.Fatal(e)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r.WithContext(ctx))
		if w.Code != tc.want {
			t.Fatalf("status=%d want=%d body=%s", w.Code, tc.want, w.Body.String())
		}
	}
	events, e := s.DiagnosticsByCorrelation(context.Background(), strings.Repeat("a", 32))
	if e != nil || len(events) != 1 || events[0]["code"] != "frontend_error" {
		t.Fatalf("events=%v %v", events, e)
	}
}
