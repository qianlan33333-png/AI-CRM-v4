package adminops

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	opsport "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/diagnostics"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	"github.com/riverqueue/river/rivertype"
)

func TestPostgreSQLDiagnosticFanoutRetriesReplayAndHTTPRoute(t *testing.T) {
	pool, uow := inspectionTestPool(t)
	ctx := context.Background()
	s, err := NewInspectionService(pool, uow, nil, nil, InspectionOptions{ReleaseSHA: strings.Repeat("b", 40)})
	if err != nil {
		t.Fatal(err)
	}
	var recordErrors []error
	record := func(ctx context.Context, o diagnostics.Observation) error {
		err := s.RecordDiagnosticObservation(ctx, opsport.DiagnosticObservation{Component: "runtime", Code: o.Code, Correlation: o.Correlation, RouteTemplate: o.RouteTemplate, JobRef: o.JobRef, EffectRef: o.EffectRef, JobAttempt: o.JobAttempt})
		if err != nil {
			recordErrors = append(recordErrors, err)
		}
		return err
	}
	middleware := &jobqueue.CorrelationMiddleware{Release: strings.Repeat("b", 40), Record: record}
	businessFailure := errors.New("fixture business failure; must not be persisted")
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/fixture/{opaque}", func(w http.ResponseWriter, r *http.Request) {
		correlation := diagnostics.FromContext(r.Context())
		for _, event := range []struct{ job, attempt, effect int }{{101, 1, 201}, {102, 1, 202}, {101, 2, 201}, {101, 2, 201}, {101, 2, 203}} {
			correlation.EffectRef = "eer_" + strconv.Itoa(event.effect)
			insertCtx, err := diagnostics.WithCorrelation(r.Context(), correlation)
			if err != nil {
				t.Fatal(err)
			}
			params := &rivertype.JobInsertParams{}
			if _, err = middleware.InsertMany(insertCtx, []*rivertype.JobInsertParams{params}, func(context.Context) ([]*rivertype.JobInsertResult, error) { return nil, nil }); err != nil {
				t.Fatal(err)
			}
			err = middleware.Work(ctx, &rivertype.JobRow{ID: int64(event.job), Attempt: event.attempt, Metadata: params.Metadata}, func(workCtx context.Context) error {
				if diagnostics.FromContext(workCtx).RequestID != correlation.RequestID {
					t.Fatal("fanout broke the original HTTP request correlation")
				}
				return businessFailure
			})
			if err != businessFailure {
				t.Fatalf("diagnostics changed durable-job behavior: %v", err)
			}
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	w := httptest.NewRecorder()
	diagnostics.Middleware(mux, strings.Repeat("b", 40), record).ServeHTTP(w, httptest.NewRequest("POST", "/api/fixture/private-customer?token=fixture-secret", nil))
	if w.Code != http.StatusServiceUnavailable || len(recordErrors) != 0 {
		t.Fatalf("middleware->Owner contract failed: status=%d errors=%v", w.Code, recordErrors)
	}
	items, err := s.DiagnosticsByCorrelation(ctx, w.Header().Get("X-AICRM-Diagnostic-ID"))
	if err != nil || len(items) != 5 {
		t.Fatalf("want HTTP + four distinct job events, replay excluded: items=%v err=%v", items, err)
	}
	var jobs, attempts, effects, httpEvents int
	err = pool.QueryRow(ctx, `SELECT count(DISTINCT job_ref) FILTER(WHERE job_ref<>''),count(DISTINCT job_attempt) FILTER(WHERE job_ref<>''),count(DISTINCT effect_ref) FILTER(WHERE effect_ref<>''),count(*) FILTER(WHERE route_template='/api/fixture/{opaque}' AND code='http_server_error') FROM adminops_diagnostic_events`).Scan(&jobs, &attempts, &effects, &httpEvents)
	if err != nil || jobs != 2 || attempts != 2 || effects != 3 || httpEvents != 1 {
		t.Fatalf("lost fanout/attempt/static route: %d %d %d %d %v", jobs, attempts, effects, httpEvents, err)
	}
	for _, item := range items {
		if _, exists := item["job_attempt"]; exists {
			t.Fatal("this change must not introduce an undeclared API field")
		}
		for _, value := range item {
			if text, ok := value.(string); ok && (strings.Contains(text, "private-customer") || strings.Contains(text, "fixture-secret") || strings.Contains(text, "fixture business failure")) {
				t.Fatal("request or error text leaked into the diagnostic record")
			}
		}
	}
}

func TestPostgreSQLDiagnosticIdentityMigrationPreservesLegacyAndBoundsAttempt(t *testing.T) {
	pool, uow := inspectionTestPoolWithDiagnosticMigration(t, false)
	ctx := context.Background()
	id := strings.Repeat("c", 32)
	digest := string(effectport.Hash("ops-correlation-v1", id))
	if _, err := pool.Exec(ctx, `INSERT INTO adminops_diagnostic_events(component,code,correlation_digest,job_ref,effect_ref,occurred_at,release_sha) VALUES('runtime','durable_job_failed',$1,'river_301','eer_401',clock_timestamp(),'legacy')`, digest); err != nil {
		t.Fatal(err)
	}
	applyDiagnosticIdentityMigration(t, pool)
	var legacyAttempt int
	if err := pool.QueryRow(ctx, `SELECT job_attempt FROM adminops_diagnostic_events`).Scan(&legacyAttempt); err != nil || legacyAttempt != 0 {
		t.Fatalf("legacy event lost or attempt guessed: %d %v", legacyAttempt, err)
	}
	s, err := NewInspectionService(pool, uow, nil, nil, InspectionOptions{ReleaseSHA: "legacy"})
	if err != nil {
		t.Fatal(err)
	}
	old := opsport.DiagnosticObservation{Component: "runtime", Code: "durable_job_failed", Correlation: id, JobRef: "river_301", EffectRef: "eer_401"}
	if err = s.RecordDiagnosticObservation(ctx, old); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM adminops_diagnostic_events`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("legacy replay duplicated: %d %v", count, err)
	}
	for _, invalid := range []opsport.DiagnosticObservation{
		{Component: "runtime", Code: "durable_job_failed", Correlation: id, JobRef: "river_301", JobAttempt: -1},
		{Component: "runtime", Code: "durable_job_failed", Correlation: id, JobRef: "river_301", JobAttempt: 2147483648},
		{Component: "runtime", Code: "durable_job_failed", Correlation: id, JobAttempt: 1},
		{Component: "runtime", Code: "durable_job_failed", Correlation: id, JobRef: "river_" + strings.Repeat("1", 20)},
		{Component: "runtime", Code: "durable_job_failed", Correlation: id, EffectRef: "eer_" + strings.Repeat("1", 20)},
	} {
		if err = s.RecordDiagnosticObservation(ctx, invalid); !errors.Is(err, ErrInspectionInvalid) {
			t.Fatalf("invalid attempt accepted: %v", err)
		}
	}
	if _, err = pool.Exec(ctx, `UPDATE adminops_diagnostic_events SET job_attempt=-1`); err == nil {
		t.Fatal("database allowed negative attempt")
	}
	if _, err = pool.Exec(ctx, `UPDATE adminops_diagnostic_events SET job_ref='',job_attempt=1`); err == nil {
		t.Fatal("database allowed a positive attempt without a job")
	}
}

func TestPostgreSQLDiagnosticFanoutReadTTLAndCleanup(t *testing.T) {
	retention, pool := retentionTestService(t)
	ctx := context.Background()
	now := time.Now().UTC().Add(-time.Second).Truncate(time.Microsecond)
	retention.now = func() time.Time { return now }
	s := &InspectionService{pool: pool, now: func() time.Time { return now }, options: InspectionOptions{ReleaseSHA: "fixture"}}
	correlation := strings.Repeat("d", 32)
	for i, age := range []time.Duration{696 * time.Hour, 720 * time.Hour, 744 * time.Hour} {
		at := now.Add(-age)
		s.now = func() time.Time { return at }
		for _, job := range []string{"river_501", "river_502"} {
			if err := s.RecordDiagnosticObservation(ctx, opsport.DiagnosticObservation{Component: "runtime", Code: "durable_job_failed", Correlation: correlation, JobRef: job, JobAttempt: i + 1}); err != nil {
				t.Fatal(err)
			}
		}
	}
	s.now = func() time.Time { return now }
	visible, err := s.DiagnosticsByCorrelation(ctx, correlation)
	if err != nil || len(visible) != 4 {
		t.Fatalf("read TTL depends on cleaner or loses boundary events: %d %v", len(visible), err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO adminops_inspection_commands(request_digest,actor_id,job_id,accepted_at) VALUES($1,7,501,$2)`, string(effectport.Hash("permanent-command")), now.Add(-744*time.Hour)); err != nil {
		t.Fatal(err)
	}
	result, err := retention.PruneRetentionBatch(ctx, "ops_diagnostics")
	if err != nil || result.Deleted != 2 {
		t.Fatalf("wrong 31-day fanout cleanup: %+v %v", result, err)
	}
	var details, commands int
	if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM adminops_diagnostic_events),(SELECT count(*) FROM adminops_inspection_commands)`).Scan(&details, &commands); err != nil || details != 4 || commands != 1 {
		t.Fatalf("boundary details/permanent receipt damaged: %d %d %v", details, commands, err)
	}
}
