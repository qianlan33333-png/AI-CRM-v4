package adminops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	opsport "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

func applyGovernanceOutcomesMigration(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	sql, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../migrations/0193_adminops_governance_outcomes.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(context.Background(), string(sql)); err != nil {
		t.Fatal(err)
	}
}
func governanceObserve(t *testing.T, pool *pgxpool.Pool, at time.Time, status string, rollback bool) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var run int64
	err = tx.QueryRow(ctx, `INSERT INTO adminops_inspection_runs(request_key,slot,state,release_sha,started_at) VALUES($1,$2,'running',$3,$2) RETURNING id`, fmt.Sprint(at.UnixNano()), at, strings.Repeat("a", 40)).Scan(&run)
	if err != nil {
		t.Fatal(err)
	}
	result := opsport.CheckResult{CheckDefinition: opsport.CheckDefinition{ID: "identity.conflicts"}, CheckObservation: opsport.CheckObservation{Status: status, Code: "open_conflicts"}, RunID: run}
	if _, _, err = recordInspectionIssue(ctx, tx, result, at); err != nil {
		t.Fatal(err)
	}
	if !rollback {
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
}
func governanceDefaultAttribution(version int64) opsport.IncidentAttributionCommand {
	return opsport.IncidentAttributionCommand{ExpectedVersion: version, IncidentAttribution: opsport.IncidentAttribution{Classification: "confirmed_defect", EscapedDefect: "unclassified", ChangeFailure: "unclassified", RootCause: "unknown", Remediation: "unknown", FaultStartBasis: "unknown"}}
}

type governanceReleaseFixture struct {
	facts platformport.GovernanceReleaseEvidence
	err   error
}

func (f *governanceReleaseFixture) ReadGovernanceReleases(context.Context) (platformport.GovernanceReleaseEvidence, error) {
	return f.facts, f.err
}
func governanceReleases(at time.Time) *governanceReleaseFixture {
	return &governanceReleaseFixture{facts: platformport.GovernanceReleaseEvidence{Version: 1, GeneratedAt: at, CurrentRelease: strings.Repeat("a", 40), Facts: []platformport.GovernanceReleaseFact{{Sequence: 1, ReleaseSHA: strings.Repeat("a", 40), SucceededAt: at.Add(-3 * time.Hour), ReceiptDigest: strings.Repeat("b", 64)}}}}
}

func TestPostgreSQLGovernanceRecurrenceSeparatesHealthyTimeAndMissingStarts(t *testing.T) {
	pool, uow := inspectionTestPool(t)
	ctx := context.Background()
	start := time.Now().UTC().Add(-50 * time.Hour).Truncate(time.Second)
	now := start.Add(50 * time.Hour)
	s, _ := NewGovernanceOutcomesService(pool, uow, nil, GovernanceOutcomesOptions{Now: func() time.Time { return now }})
	governanceObserve(t, pool, start, "critical", false)
	governanceObserve(t, pool, start.Add(5*time.Minute), "unknown", false)
	governanceObserve(t, pool, start.Add(10*time.Minute), "ok", false)
	governanceObserve(t, pool, start.Add(48*time.Hour), "critical", false)
	governanceObserve(t, pool, start.Add(48*time.Hour+20*time.Minute), "ok", false)
	page, err := s.Episodes(ctx, start.Add(-time.Hour), now, 0, 50)
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("episodes=%+v err=%v", page, err)
	}
	// Each cycle is attributed separately. The first has actual fault-start evidence;
	// the second must remain missing for MTTD even though both have recovery times.
	for i, e := range page.Items {
		c := governanceDefaultAttribution(e.Version)
		c.EscapedDefect = "yes"
		if i == 1 {
			started := start.Add(-time.Minute)
			c.FaultStartedAt = &started
			c.FaultStartBasis = "operator_confirmed"
		}
		if _, err = s.Attribute(ctx, e.ID, 7, fmt.Sprintf("attribution-%d", i), c); err != nil {
			t.Fatal(err)
		}
	}
	out, err := s.Outcomes(ctx, start.Add(-time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	if out.EpisodeCount != 2 || out.OpenCount != 0 || out.ConfirmedDefects != 2 || out.RepeatedDefects.Numerator != 1 || out.DetectedToRecovered.SampleCount != 2 || out.DetectedToRecovered.MeanSeconds == nil || *out.DetectedToRecovered.MeanSeconds != 900 {
		t.Fatalf("healthy gap leaked: %+v", out)
	}
	if out.MTTD.SampleCount != 1 || out.MTTD.MissingCount != 1 || out.MTTD.MeanSeconds == nil || *out.MTTD.MeanSeconds != 60 || out.Deployments.EvidenceState != "unavailable" || out.Deployments.VerifiedSuccessCohortFailureRatio != nil {
		t.Fatalf("invented facts: %+v", out)
	}
}
func TestPostgreSQLGovernanceUnknownAndExistingIssuesDoNotFabricateDefects(t *testing.T) {
	pool, uow := inspectionTestPool(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	at := now.Add(-time.Hour)
	fingerprint := string(effectport.Hash("ops-issue-v1", "identity.conflicts", "open_conflicts"))
	if _, err := pool.Exec(ctx, `INSERT INTO adminops_inspection_issues(fingerprint,check_id,code,status,severity,first_seen,last_seen) VALUES($1,'identity.conflicts','open_conflicts','open','unknown',$2,$2)`, fingerprint, at.Add(-1000*time.Hour)); err != nil {
		t.Fatal(err)
	}
	governanceObserve(t, pool, at, "unknown", false)
	s, _ := NewGovernanceOutcomesService(pool, uow, nil, GovernanceOutcomesOptions{Now: func() time.Time { return now }})
	page, err := s.Episodes(ctx, at.Add(-time.Hour), now, 0, 50)
	if err != nil || len(page.Items) != 1 {
		t.Fatal(page, err)
	}
	if page.Items[0].DetectionOrigin != "first_observed_existing_issue" || !page.Items[0].DetectedAt.Equal(at) {
		t.Fatalf("fabricated history: %+v", page)
	}
	out, err := s.Outcomes(ctx, at.Add(-time.Hour), now)
	if err != nil || out.ConfirmedDefects != 0 || out.UnclassifiedEpisodes != 1 || out.MTTD.MeanSeconds != nil || out.DetectedToRecovered.MeanSeconds != nil || out.EscapedDefects.Ratio != nil {
		t.Fatal(out, err)
	}
}
func TestPostgreSQLGovernanceEpisodeAndIssueRollbackAndPermanentFacts(t *testing.T) {
	pool, uow := inspectionTestPool(t)
	ctx := context.Background()
	at := time.Now().UTC().Add(-800 * time.Hour).Truncate(time.Second)
	governanceObserve(t, pool, at, "critical", true)
	for _, table := range []string{"adminops_incident_episodes", "adminops_inspection_issues"} {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("rollback %s=%d %v", table, n, err)
		}
	}
	governanceObserve(t, pool, at, "critical", false)
	governanceObserve(t, pool, at.Add(time.Minute), "ok", true)
	s, _ := NewGovernanceOutcomesService(pool, uow, nil, GovernanceOutcomesOptions{})
	page, err := s.Episodes(ctx, at.Add(-time.Hour), time.Now(), 0, 1)
	if err != nil || len(page.Items) != 1 || page.Items[0].RecoveredAt != nil {
		t.Fatal(page, err)
	}
	for _, sql := range []string{`DELETE FROM adminops_incident_episodes`, `TRUNCATE adminops_incident_episodes`, `UPDATE adminops_incident_episodes SET detected_at=detected_at-interval '1 hour',version=version+1`} {
		if _, err = pool.Exec(ctx, sql); err == nil {
			t.Fatalf("permanent identity changed: %s", sql)
		}
	}
	if _, err = pool.Exec(ctx, `DELETE FROM adminops_inspection_runs WHERE started_at<clock_timestamp()-interval '720 hours'`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Episode(ctx, page.Items[0].ID); err != nil {
		t.Fatal("minimal episode lost with process rows", err)
	}
}
func TestPostgreSQLGovernanceAttributionCASIdempotenceAndAuditAtomicity(t *testing.T) {
	pool, uow := inspectionTestPool(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	governanceObserve(t, pool, now.Add(-time.Hour), "critical", false)
	s, _ := NewGovernanceOutcomesService(pool, uow, nil, GovernanceOutcomesOptions{})
	page, _ := s.Episodes(ctx, time.Time{}, time.Time{}, 0, 10)
	e := page.Items[0]
	command := governanceDefaultAttribution(e.Version)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.Attribute(ctx, e.ID, 7, fmt.Sprintf("concurrent-%d", i), command)
			results <- err
		}(i)
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrInspectionConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("CAS=%d/%d", success, conflict)
	}
	current, _ := s.Episode(ctx, e.ID)
	command.ExpectedVersion = current.Version
	receipts := make(chan opsport.IncidentAttributionReceipt, 2)
	results = make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := s.Attribute(ctx, e.ID, 7, "one-logical-command", command)
			receipts <- r
			results <- err
		}()
	}
	wg.Wait()
	close(receipts)
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	var receipt opsport.IncidentAttributionReceipt
	replays := 0
	for r := range receipts {
		if receipt.ActionID != 0 && r.ActionID != receipt.ActionID {
			t.Fatal("replay created another action")
		}
		receipt = r
		if r.Replay {
			replays++
		}
	}
	if replays != 1 {
		t.Fatal("missing replay receipt")
	}
	command.RootCause = "code"
	if _, err := s.Attribute(ctx, e.ID, 7, "one-logical-command", command); !errors.Is(err, ErrInspectionConflict) {
		t.Fatal("changed payload replay", err)
	}
	var actions int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM adminops_incident_attributions`).Scan(&actions); err != nil || actions != 2 {
		t.Fatal(actions, err)
	}
	if _, err := pool.Exec(ctx, `CREATE FUNCTION reject_governance_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test audit failure'; END $$; CREATE TRIGGER test_reject BEFORE INSERT ON adminops_incident_attributions FOR EACH ROW EXECUTE FUNCTION reject_governance_audit()`); err != nil {
		t.Fatal(err)
	}
	current, _ = s.Episode(ctx, e.ID)
	command.ExpectedVersion = current.Version
	if _, err := s.Attribute(ctx, e.ID, 7, "audit-must-rollback", command); err == nil {
		t.Fatal("audit failure accepted")
	}
	after, _ := s.Episode(ctx, e.ID)
	if after.Version != current.Version || after.RootCause != current.RootCause {
		t.Fatal("state committed without audit")
	}
	for _, sql := range []string{`DELETE FROM adminops_incident_attributions`, `UPDATE adminops_incident_attributions SET actor_id=8`, `TRUNCATE adminops_incident_attributions`} {
		if _, err := pool.Exec(ctx, sql); err == nil {
			t.Fatal("audit mutable")
		}
	}
}
func TestPostgreSQLGovernanceReleaseSubsetGapsRevocationAndDenominator(t *testing.T) {
	pool, uow := inspectionTestPool(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	source := governanceReleases(now)
	source.facts.Facts = append(source.facts.Facts, platformport.GovernanceReleaseFact{Sequence: 3, ReleaseSHA: strings.Repeat("c", 40), ReceiptDigest: strings.Repeat("d", 64), SucceededAt: now.Add(-2 * time.Hour)})
	s, _ := NewGovernanceOutcomesService(pool, uow, source, GovernanceOutcomesOptions{Now: func() time.Time { return now }})
	if _, err := s.RefreshReleaseEvidence(ctx); err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		at := now.Add(time.Duration(-90+i*20) * time.Minute)
		governanceObserve(t, pool, at, "critical", false)
		governanceObserve(t, pool, at.Add(10*time.Minute), "ok", false)
	}
	page, _ := s.Episodes(ctx, time.Time{}, time.Time{}, 0, 10)
	for i, e := range page.Items {
		c := governanceDefaultAttribution(e.Version)
		c.ChangeFailure = "yes"
		seq := int64(1)
		c.CausedByReleaseSequence = &seq
		if _, err := s.Attribute(ctx, e.ID, 7, fmt.Sprintf("release-attribution-%d", i), c); err != nil {
			t.Fatal(err)
		}
	}
	out, err := s.Outcomes(ctx, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Deployments.VerifiedSuccessfulDeployments != 2 || out.Deployments.ConfirmedFailedDeployments != 1 || out.Deployments.HistoricalSequenceGaps == nil || *out.Deployments.HistoricalSequenceGaps != 1 || out.Deployments.FullChangeFailureRateAvailable || out.Deployments.VerifiedSuccessCohortFailureRatio == nil || *out.Deployments.VerifiedSuccessCohortFailureRatio != .5 {
		t.Fatalf("false denominator: %+v", out.Deployments)
	}
	source.facts.Facts[0].Revoked = true
	source.facts.CurrentRelease = strings.Repeat("c", 40)
	out, err = s.Outcomes(ctx, time.Time{}, time.Time{})
	if err != nil || out.Deployments.VerifiedSuccessfulDeployments != 1 || out.Deployments.ConfirmedFailedDeployments != 0 || out.Deployments.RevokedReleaseAttributions != 2 {
		t.Fatal(out.Deployments, err)
	}
	source.facts.Facts[0].Revoked = false
	out, err = s.Outcomes(ctx, time.Time{}, time.Time{})
	if err != nil || out.Deployments.EvidenceState != "unavailable" || out.Deployments.VerifiedSuccessCohortFailureRatio != nil {
		t.Fatal("revoked fact resurrected", out, err)
	}
	source.facts.Facts[0].Revoked = true
	source.facts.Facts[1].ReceiptDigest = strings.Repeat("e", 64)
	if _, err = s.RefreshReleaseEvidence(ctx); !errors.Is(err, ErrInspectionConflict) {
		t.Fatal("same sequence changed", err)
	}
}
func TestPostgreSQLGovernanceAttributionRejectsTimeEvidenceAndUnconfirmedDefects(t *testing.T) {
	pool, uow := inspectionTestPool(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	at := now.Add(-time.Hour)
	governanceObserve(t, pool, at, "unknown", false)
	s, _ := NewGovernanceOutcomesService(pool, uow, nil, GovernanceOutcomesOptions{})
	page, _ := s.Episodes(ctx, time.Time{}, time.Time{}, 0, 10)
	episode := page.Items[0]
	cases := []func(*opsport.IncidentAttributionCommand){
		func(c *opsport.IncidentAttributionCommand) {
			start := at.Add(time.Second)
			c.FaultStartedAt = &start
			c.FaultStartBasis = "operator_confirmed"
		},
		func(c *opsport.IncidentAttributionCommand) {
			c.ChangeFailure = "yes"
			seq := int64(777)
			c.CausedByReleaseSequence = &seq
		},
		func(c *opsport.IncidentAttributionCommand) {
			c.Classification = "observation_gap"
			c.EscapedDefect = "yes"
		},
		func(c *opsport.IncidentAttributionCommand) { c.RootCause = "phone+86123456" },
		func(c *opsport.IncidentAttributionCommand) { minutes := -1; c.EffortMinutes = &minutes },
	}
	for i, mutate := range cases {
		command := governanceDefaultAttribution(episode.Version)
		mutate(&command)
		if _, err := s.Attribute(ctx, episode.ID, 7, fmt.Sprintf("invalid-command-%d", i), command); !errors.Is(err, ErrInspectionInvalid) {
			t.Fatal(i, err)
		}
	}
	source := governanceReleases(now)
	source.facts.Facts[0].SucceededAt = at.Add(time.Minute)
	s.releases = source
	if _, err := s.RefreshReleaseEvidence(ctx); err != nil {
		t.Fatal(err)
	}
	c := governanceDefaultAttribution(episode.Version)
	c.ChangeFailure = "yes"
	seq := int64(1)
	c.CausedByReleaseSequence = &seq
	if _, err := s.Attribute(ctx, episode.ID, 7, "release-after-fault-start", c); !errors.Is(err, ErrInspectionInvalid) {
		t.Fatal("causal order not enforced", err)
	}
	out, _ := s.Outcomes(ctx, time.Time{}, time.Time{})
	if out.ConfirmedDefects != 0 || out.MTTD.SampleCount != 0 {
		t.Fatal("invalid attribution leaked")
	}
}

type governancePlainAdmin struct{}

func (governancePlainAdmin) ReadPrincipal(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{InternalID: 8, Kind: accessdomain.KindAdmin}, nil
}
func (s governancePlainAdmin) AuthorizeCSRF(ctx context.Context, r *http.Request) (accessdomain.Principal, error) {
	return s.ReadPrincipal(ctx, r)
}
func TestPostgreSQLGovernanceHTTPPermissionCSRFClosedPayloadAndPagination(t *testing.T) {
	pool, uow := inspectionTestPool(t)
	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	governanceObserve(t, pool, at, "critical", false)
	governanceObserve(t, pool, at.Add(time.Minute), "ok", false)
	governanceObserve(t, pool, at.Add(2*time.Minute), "critical", false)
	s, _ := NewGovernanceOutcomesService(pool, uow, nil, GovernanceOutcomesOptions{})
	page, _ := s.Episodes(context.Background(), time.Time{}, time.Time{}, 0, 1)
	if !page.HasMore || page.NextBeforeID == 0 {
		t.Fatal(page)
	}
	c := governanceDefaultAttribution(page.Items[0].Version)
	body, _ := json.Marshal(c)
	path := fmt.Sprintf("/api/admin/ops-governance/episodes/%d/attribution", page.Items[0].ID)
	for _, security := range []InspectionSecurity{inspectionTestSecurity{deny: true}, inspectionTestSecurity{csrfDeny: true}, governancePlainAdmin{}} {
		h, _ := NewGovernanceOutcomesHandler(s, security)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("PUT", path, strings.NewReader(string(body))))
		if w.Code != 403 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	h, _ := NewGovernanceOutcomesHandler(s, inspectionTestSecurity{})
	for _, bad := range []string{"null", "[]", string(body) + strings.Repeat(" ", 4096) + `{}`, strings.TrimSuffix(string(body), "}") + `,"message":"SECRET"}`, strings.TrimSuffix(string(body), "}") + `,"expected_version":1}`} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("PUT", path, strings.NewReader(bad))
		r.Header.Set("Idempotency-Key", "http-command-one")
		h.ServeHTTP(w, r)
		if w.Code != 400 || strings.Contains(w.Body.String(), "SECRET") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("PUT", path, strings.NewReader(string(body)))
	r.Header.Set("Idempotency-Key", "http-command-one")
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, path := range []string{"/api/admin/ops-governance/outcomes", "/api/admin/ops-governance/episodes?limit=1", fmt.Sprintf("/api/admin/ops-governance/episodes/%d", page.Items[0].ID)} {
		w = httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || w.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

func TestPostgreSQLGovernanceEpisodePreservesInspectionCodeContract(t *testing.T) {
	pool, _ := inspectionTestPool(t)
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Hour)
	for _, code := range []string{"x", strings.Repeat("x", 96)} {
		if !safeInspectionCode.MatchString(code) {
			t.Fatal("fixture is not an accepted existing observation")
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		result := opsport.CheckResult{CheckDefinition: opsport.CheckDefinition{ID: "identity.conflicts"}, CheckObservation: opsport.CheckObservation{Status: "warning", Code: code}}
		_, _, err = recordInspectionIssue(ctx, tx, result, at)
		if err != nil {
			tx.Rollback(ctx)
			t.Fatal("episode broke existing collector contract", err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
}
