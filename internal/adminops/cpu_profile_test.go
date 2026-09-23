package adminops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

type cpuProfileTestSampler struct {
	count   atomic.Int64
	capture func(context.Context, string) error
	mu      sync.Mutex
	payload map[string][]byte
}

func (p *cpuProfileTestSampler) Capture(ctx context.Context, id string) (platformport.CPUProfileArtifact, error) {
	p.count.Add(1)
	if p.capture != nil {
		if err := p.capture(ctx, id); err != nil {
			return platformport.CPUProfileArtifact{}, err
		}
	}
	data := []byte("profile-test-safe")
	sum := sha256.Sum256(data)
	p.mu.Lock()
	p.payload[id] = data
	p.mu.Unlock()
	return platformport.CPUProfileArtifact{SHA256: hex.EncodeToString(sum[:]), Bytes: int64(len(data))}, nil
}
func (p *cpuProfileTestSampler) Read(_ context.Context, id string) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	data, ok := p.payload[id]
	if !ok {
		return nil, platformport.ErrCPUProfileUnavailable
	}
	return append([]byte(nil), data...), nil
}
func cpuProfileFixture(t *testing.T) (*CPUProfileService, *pgxpool.Pool, *cpuProfileTestSampler) {
	t.Helper()
	pool, _ := inspectionTestPool(t)
	_, file, _, _ := runtime.Caller(0)
	ddl, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../migrations/0190_adminops_cpu_profiles.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(context.Background(), string(ddl)); err != nil {
		t.Fatal(err)
	}
	profiler := &cpuProfileTestSampler{payload: map[string][]byte{}}
	s, err := NewCPUProfileService(pool, profiler, strings.Repeat("a", 40), true)
	if err != nil {
		t.Fatal(err)
	}
	return s, pool, profiler
}

func TestCPUProfileConstructorNormalizesUntrustedReleaseEvenWhenDisabled(t *testing.T) {
	for _, release := range []string{"", "test", "unknown", "release-token-secret", strings.Repeat("a", 64)} {
		for _, enabled := range []bool{false, true} {
			s, err := NewCPUProfileService(&pgxpool.Pool{}, &cpuProfileTestSampler{}, release, enabled)
			if err != nil || s.release != "unknown" || s.enabled != enabled {
				t.Fatalf("release normalization failed: %v", err)
			}
		}
	}
	valid := strings.Repeat("a", 40)
	s, err := NewCPUProfileService(&pgxpool.Pool{}, &cpuProfileTestSampler{}, valid, false)
	if err != nil || s.release != valid {
		t.Fatalf("valid release discarded=%v", err)
	}
}

type cpuProfileSecurity struct {
	principal accessdomain.Principal
	csrfDeny  bool
}

func (s cpuProfileSecurity) ReadPrincipal(context.Context, *http.Request) (accessdomain.Principal, error) {
	return s.principal, nil
}
func (s cpuProfileSecurity) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	if s.csrfDeny {
		return accessdomain.Principal{}, errors.New("secret csrf error")
	}
	return s.principal, nil
}
func cpuProfileRequest(t *testing.T, h http.Handler, method, path, body, key string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Idempotency-Key", key)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestPostgreSQLCPUProfileHTTPPermissionCSRFContractsAndReplay(t *testing.T) {
	s, _, p := cpuProfileFixture(t)
	base := "/api/admin/ops-diagnostics/cpu-profiles"
	super := accessdomain.Principal{InternalID: 7, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}}
	for _, sec := range []cpuProfileSecurity{{}, {principal: accessdomain.Principal{InternalID: 7, Kind: accessdomain.KindAdmin}}, {principal: super, csrfDeny: true}} {
		h, _ := NewCPUProfileHandler(s, sec)
		w := cpuProfileRequest(t, h, "POST", base, `{}`, "permission-key")
		if w.Code != 403 {
			t.Fatalf("permission=%d", w.Code)
		}
	}
	h, _ := NewCPUProfileHandler(s, cpuProfileSecurity{principal: super})
	for _, body := range []string{`null`, `[]`, `{"seconds":30}`, `{"type":"heap"}`, `{"path":"/etc/passwd"}`, `{} {}`, `{}` + strings.Repeat(" ", 4095) + `{"seconds":30}`} {
		if w := cpuProfileRequest(t, h, "POST", base, body, "invalid-body"); w.Code != 400 {
			t.Fatalf("body accepted: %s %d", body, w.Code)
		}
	}
	if w := cpuProfileRequest(t, h, "POST", base+"?seconds=1", `{}`, "invalid-query"); w.Code != 400 {
		t.Fatalf("query=%d", w.Code)
	}
	if p.count.Load() != 0 {
		t.Fatal("denied request started profiler")
	}
	w := cpuProfileRequest(t, h, "POST", base, `{}`, "valid-profile-key")
	if w.Code != 200 {
		t.Fatalf("capture=%d %s", w.Code, w.Body.String())
	}
	var result CPUProfileRecord
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.State != "completed" || result.Target != "api" || result.DurationSeconds != 5 || result.ReleaseSHA != strings.Repeat("a", 40) {
		t.Fatalf("result=%+v", result)
	}
	w = cpuProfileRequest(t, h, "POST", base, `{}`, "valid-profile-key")
	var replay CPUProfileRecord
	_ = json.Unmarshal(w.Body.Bytes(), &replay)
	if w.Code != 200 || !replay.Replay || replay.ID != result.ID || p.count.Load() != 1 {
		t.Fatalf("replayed=%d %+v", w.Code, replay)
	}
	w = cpuProfileRequest(t, h, "GET", base+"/"+result.ID+"/download", "", "")
	if w.Code != 200 || w.Body.String() != "profile-test-safe" || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("download=%d", w.Code)
	}
	denied, _ := NewCPUProfileHandler(s, cpuProfileSecurity{})
	if w = cpuProfileRequest(t, denied, "GET", base+"/"+result.ID+"/download", "", ""); w.Code != 403 {
		t.Fatal("download leaked")
	}
	if _, err := s.Capture(context.Background(), 8, "valid-profile-key"); !errors.Is(err, ErrInspectionConflict) {
		t.Fatalf("cross-actor replay=%v", err)
	}
	p.mu.Lock()
	p.payload[result.ID] = []byte("secret-corrupt-file")
	p.mu.Unlock()
	w = cpuProfileRequest(t, h, "GET", base+"/"+result.ID+"/download", "", "")
	if w.Code != 503 || strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("corrupt file leaked: %s", w.Body.String())
	}
}

func TestPostgreSQLCPUProfileGlobalConcurrencyNoSamplingTransactionAndCancellation(t *testing.T) {
	s, pool, p := cpuProfileFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	p.capture = func(ctx context.Context, _ string) error {
		close(entered)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := s.Capture(ctx, 7, "concurrent-first"); done <- err }()
	<-entered
	var state string
	var xact *time.Time
	if err := pool.QueryRow(context.Background(), `SELECT a.state,a.xact_start FROM pg_stat_activity a JOIN pg_locks l ON a.pid=l.pid WHERE l.locktype='advisory' AND l.objid=$1 AND l.granted AND a.datname=current_database() AND a.application_name=current_setting('application_name')`, cpuProfileLock).Scan(&state, &xact); err != nil || state != "idle" || xact != nil {
		t.Fatalf("sampler holds transaction: %s %v %v", state, xact, err)
	}
	second, _ := NewCPUProfileService(pool, p, strings.Repeat("b", 40), true)
	if _, err := second.Capture(context.Background(), 8, "concurrent-other"); !errors.Is(err, ErrCPUProfileBusy) {
		t.Fatalf("cross-instance concurrent=%v", err)
	}
	if r, err := second.Capture(context.Background(), 7, "concurrent-first"); err != nil || !r.Replay || r.State != "sampling" || p.count.Load() != 1 {
		t.Fatalf("inflight replay=%+v %v", r, err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("cancel did not record safe failure: %v", err)
	}
	r, err := s.Capture(context.Background(), 7, "concurrent-first")
	if err != nil || r.State != "failed" || p.count.Load() != 1 {
		t.Fatalf("failure replay=%+v %v", r, err)
	}
	p.capture = nil
	if _, err = s.Capture(context.Background(), 8, "after-cancel-key"); err != nil {
		t.Fatalf("lock not released after cancellation: %v", err)
	}
	var held int
	if err = pool.QueryRow(context.Background(), `SELECT count(*) FROM pg_locks l JOIN pg_stat_activity a ON a.pid=l.pid WHERE l.locktype='advisory' AND l.objid=$1 AND l.granted AND a.datname=current_database() AND a.application_name=current_setting('application_name')`, cpuProfileLock).Scan(&held); err != nil || held != 0 {
		t.Fatalf("pool leaked session lock=%d %v", held, err)
	}
}

func TestPostgreSQLCPUProfileCompletionFailureNeverResamplesAndDiscardsLostSession(t *testing.T) {
	s, pool, p := cpuProfileFixture(t)
	p.capture = func(ctx context.Context, _ string) error {
		_, err := pool.Exec(ctx, `SELECT pg_terminate_backend(l.pid) FROM pg_locks l JOIN pg_stat_activity a ON a.pid=l.pid WHERE l.locktype='advisory' AND l.objid=$1 AND l.granted AND a.datname=current_database() AND a.application_name=current_setting('application_name')`, cpuProfileLock)
		return err
	}
	r, err := s.Capture(context.Background(), 7, "lost-session-key")
	if !errors.Is(err, ErrCPUProfileUnknown) || r.State != "outcome_unknown" {
		t.Fatalf("lost commit=%+v %v", r, err)
	}
	p.capture = nil
	replay, err := s.Capture(context.Background(), 7, "lost-session-key")
	if err != nil || !replay.Replay || p.count.Load() != 1 || replay.State == "completed" {
		t.Fatalf("unknown replay resampled=%+v %v", replay, err)
	}
	if _, err = s.Capture(context.Background(), 8, "session-pool-usable"); !errors.Is(err, ErrCPUProfileBusy) {
		t.Fatalf("connection loss bypassed recent acceptance protection: %v", err)
	}
	var one int
	if err = pool.QueryRow(context.Background(), `SELECT 1`).Scan(&one); err != nil || one != 1 {
		t.Fatalf("pool broken after lost session: %v", err)
	}
}

func insertCPUProfileReceipt(t *testing.T, pool *pgxpool.Pool, number, actor int, age time.Duration, state string, size int64) string {
	t.Helper()
	id := fmt.Sprintf("%032x", number)
	digest := fmt.Sprintf("%064x", number)
	accepted := time.Now().UTC().Add(-age)
	var completed *time.Time
	sha, code := "", ""
	if state != "sampling" {
		completed = &accepted
	}
	if state == "completed" {
		sha = strings.Repeat("f", 64)
	}
	if state == "failed" {
		code = "capture_failed"
	}
	_, err := pool.Exec(context.Background(), `INSERT INTO adminops_cpu_profile_receipts(id,request_digest,actor_id,release_sha,state,failure_code,artifact_sha256,artifact_bytes,accepted_at,expires_at,completed_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$9::timestamptz+interval '720 hours',$10)`, id, digest, actor, strings.Repeat("a", 40), state, code, sha, size, accepted, completed)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestPostgreSQLCPUProfileRollingHourlyLimitAndDisabled(t *testing.T) {
	s, pool, p := cpuProfileFixture(t)
	s.enabled = false
	if _, err := s.Capture(context.Background(), 7, "disabled-key"); !errors.Is(err, ErrCPUProfileDisabled) {
		t.Fatalf("disabled=%v", err)
	}
	s.enabled = true
	for i := 1; i <= 3; i++ {
		insertCPUProfileReceipt(t, pool, i, 7, 10*time.Minute, "failed", 0)
	}
	if _, err := s.Capture(context.Background(), 7, "personal-hourly"); !errors.Is(err, ErrInspectionRateLimited) {
		t.Fatalf("actor limit=%v", err)
	}
	for i := 4; i <= 6; i++ {
		insertCPUProfileReceipt(t, pool, i, 8, 10*time.Minute, "failed", 0)
	}
	if _, err := s.Capture(context.Background(), 9, "global-hourly"); !errors.Is(err, ErrInspectionRateLimited) {
		t.Fatalf("global limit=%v", err)
	}
	if p.count.Load() != 0 {
		t.Fatal("rate/disabled guard started profiler")
	}
}

func TestPostgreSQLCPUProfileQuotaUnknownReservationsAndReadTTL(t *testing.T) {
	s, pool, p := cpuProfileFixture(t)
	for i := 1; i <= 32; i++ {
		insertCPUProfileReceipt(t, pool, i, 7, 2*time.Hour, "sampling", 0)
	}
	if _, err := s.Capture(context.Background(), 7, "full-capacity-key"); !errors.Is(err, ErrCPUProfileCapacity) {
		t.Fatalf("reserved cap=%v", err)
	}
	if p.count.Load() != 0 {
		t.Fatal("quota guard started profiler")
	}
	r, err := s.Get(context.Background(), fmt.Sprintf("%032x", 1))
	if err != nil || r.State != "outcome_unknown" {
		t.Fatalf("abandoned state=%+v %v", r, err)
	}
	// Known failures are terminal, release the reserved bytes, and cannot be reset.
	_, err = pool.Exec(context.Background(), `UPDATE adminops_cpu_profile_receipts SET state='failed',failure_code='capture_failed',completed_at=clock_timestamp() WHERE id=$1`, fmt.Sprintf("%032x", 1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Capture(context.Background(), 8, "freed-capacity-key"); err != nil {
		t.Fatalf("known failure reservation not released=%v", err)
	}
	if _, err = pool.Exec(context.Background(), `UPDATE adminops_cpu_profile_receipts SET state='sampling',failure_code='',completed_at=NULL WHERE id=$1`, fmt.Sprintf("%032x", 1)); err == nil {
		t.Fatal("terminal receipt reopened")
	}
	for _, hours := range []int{696, 720, 744} {
		id := insertCPUProfileReceipt(t, pool, 100+hours, 7, time.Duration(hours)*time.Hour, "completed", 1)
		_, err = s.Download(context.Background(), id)
		if hours >= 720 && !errors.Is(err, ErrCPUProfileExpired) {
			t.Fatalf("%dh download=%v", hours, err)
		}
		if hours < 720 && !errors.Is(err, ErrCPUProfileUnknown) {
			t.Fatalf("recent missing artifact=%v", err)
		}
	}
	rows, err := s.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.ID == fmt.Sprintf("%032x", 820) || r.ID == fmt.Sprintf("%032x", 844) {
			t.Fatal("expired detail remained visible")
		}
	}
	if _, err = pool.Exec(context.Background(), `DELETE FROM adminops_cpu_profile_receipts`); err == nil {
		t.Fatal("replay evidence deleted")
	}
}

func TestPostgreSQLCPUProfileExpiredReservationsDoNotBlockAndFailedAcceptDoesNotCapture(t *testing.T) {
	s, pool, p := cpuProfileFixture(t)
	for i := 1; i <= 33; i++ {
		insertCPUProfileReceipt(t, pool, i, 7, 721*time.Hour, "sampling", 0)
	}
	if _, err := s.Capture(context.Background(), 8, "expired-reservation"); err != nil {
		t.Fatalf("expired quota retained=%v", err)
	}
	_, err := pool.Exec(context.Background(), `CREATE FUNCTION fail_profile_accept() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture failure'; END $$; CREATE TRIGGER fail_profile_accept BEFORE INSERT ON adminops_cpu_profile_receipts FOR EACH ROW EXECUTE FUNCTION fail_profile_accept();`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Capture(context.Background(), 8, "failed-accept-key"); err == nil {
		t.Fatal("injected acceptance failure ignored")
	}
	if p.count.Load() != 1 {
		t.Fatal("profiler ran before successful acceptance commit")
	}
}

func TestPostgreSQLCPUProfileUncertainArtifactKeepsReservationAndCannotReplayCapture(t *testing.T) {
	s, pool, p := cpuProfileFixture(t)
	p.capture = func(context.Context, string) error { return platformport.ErrCPUProfileUncertain }
	r, err := s.Capture(context.Background(), 7, "uncertain-artifact")
	if !errors.Is(err, ErrCPUProfileUnknown) || r.State != "outcome_unknown" {
		t.Fatalf("uncertain cleanup=%+v %v", r, err)
	}
	var state string
	if err = pool.QueryRow(context.Background(), `SELECT state FROM adminops_cpu_profile_receipts WHERE id=$1`, r.ID).Scan(&state); err != nil || state != "sampling" {
		t.Fatalf("uncertain artifact reservation released=%s %v", state, err)
	}
	if _, err = s.Capture(context.Background(), 7, "uncertain-artifact"); err != nil || p.count.Load() != 1 {
		t.Fatalf("uncertain replay=%v", err)
	}
}
