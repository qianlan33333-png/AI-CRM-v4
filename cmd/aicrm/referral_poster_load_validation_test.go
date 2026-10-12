package main

// Opt-in isolated backend workload. OAuth and phone encoding are deliberately
// outside this test; no production database or Provider is contacted.
import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	accessstore "github.com/qianlan33333-png/AI-CRM-v3/internal/access/store"
	orderstore "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	referralapp "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/app"
	referralstore "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/store"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	referraldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/domain"
	referralhttp "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/http"
	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
)

type posterLoadBoundary struct{ at time.Time }

func (b posterLoadBoundary) Resolve(_ context.Context, token string) (referralport.TrustedSessionActor, error) {
	id, e := strconv.ParseInt(strings.TrimPrefix(token, "load-user-"), 10, 64)
	if e != nil {
		return referralport.TrustedSessionActor{}, e
	}
	return posterLoadActor(id, b.at), nil
}
func (posterLoadBoundary) BridgePaymentSession(context.Context, string) (string, time.Time, error) {
	return "", time.Time{}, referralport.ErrUnauthorized
}
func (posterLoadBoundary) DisplayNames(context.Context, []customerdomain.CustomerID) (map[customerdomain.CustomerID]string, error) {
	return map[customerdomain.CustomerID]string{}, nil
}
func (posterLoadBoundary) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{}, referralport.ErrUnauthorized
}
func (posterLoadBoundary) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{}, referralport.ErrUnauthorized
}

type posterLoadReads struct {
	*referralapp.PosterService
	snapshots atomic.Int64
}

func (s *posterLoadReads) ReadCampaignPosterSnapshot(ctx context.Context, id int64, slot int, digest [32]byte) (referralport.CampaignPosterImage, error) {
	s.snapshots.Add(1)
	return s.PosterService.ReadCampaignPosterSnapshot(ctx, id, slot, digest)
}
func TestPostgreSQLPoster1000Users10Seconds(t *testing.T) {
	if os.Getenv("AICRM_POSTER_LOAD") != "1" {
		t.Skip("explicit isolated workload only")
	}
	source, e := os.ReadFile(os.Getenv("AICRM_POSTER_SOURCE_FIXTURE"))
	if e != nil {
		t.Fatal(e)
	}
	h := newPosterLoadHarness(t)
	ctx := context.Background()
	audit, _ := platformaudit.NewService(platformaudit.NewPostgreSQLStore())
	s, e := referralapp.NewPosterService(h.uow, h.repository, &posterLoadImage{content: source}, audit, platformoutbox.NewPostgreSQL())
	if e != nil {
		t.Fatal(e)
	}

	c, e := h.admin.CreateCampaign(ctx, referralport.CreateCampaignCommand{ActorAdminID: 9001, Name: "load-isolated", StartsAt: h.clock.Add(-time.Hour), EndsAt: h.clock.Add(time.Hour), TeamMode: referraldomain.TeamModeIndividual, QualificationMode: referraldomain.QualificationFreeSignup, IdempotencyKey: "load-isolated-create"})
	if e != nil {
		t.Fatal(e)
	}
	published, e := s.SetCampaignPosters(ctx, referralport.SetCampaignPostersCommand{CampaignID: c.ID, ActorAdminID: 9001, ExpectedVersion: c.Version, IdempotencyKey: "load-isolated-posters", Posters: []referralport.CampaignPosterSource{{ImageID: 1}, {ImageID: 2}, {ImageID: 3}}})
	if e != nil {
		t.Fatal(e)
	}
	c, e = h.admin.SetCampaignState(ctx, referralport.SetCampaignStateCommand{CampaignID: c.ID, ActorAdminID: 9001, ExpectedVersion: c.Version + 1, Target: referraldomain.CampaignActive, IdempotencyKey: "load-isolated-active"})
	if e != nil {
		t.Fatal(e)
	}
	// Synthetic already-qualified participants; identity resolution is unchanged.
	if _, e = h.pool.Exec(ctx, "INSERT INTO referral_participations(campaign_id,customer_id,state,joined_at) SELECT $1,n,'active',$2 FROM generate_series(1,1000) n", c.ID, h.clock); e != nil {
		t.Fatal(e)
	}
	reads := &posterLoadReads{PosterService: s}
	boundary := posterLoadBoundary{h.clock}
	handler, e := referralhttp.NewHandler(referralhttp.Config{Public: h.service, Admin: h.admin, Posters: reads, Sessions: boundary, Bridge: boundary, Names: boundary, Security: boundary, AllowedOrigins: []string{"https://referral.test"}, SessionCookieName: "session", CSRFCookieName: "csrf", CSRFHeader: "X-CSRF"})
	if e != nil {
		t.Fatal(e)
	}
	server := httptest.NewServer(http.HandlerFunc(handler.ServePublicHTTP))
	defer server.Close()
	client := server.Client()
	client.Timeout = 5 * time.Second
	probe := newPosterLoadProbes(t, h)
	baselineProbe := probe.sample(100)
	type row struct {
		Millis float64
		Status int
		Bytes  int64
		OK     bool
		Error  string
	}
	outcomes := make([]row, 1000)
	start := time.Now()
	initial := h.pool.Stat()
	snapshotStart := reads.snapshots.Load()
	var peakHeap atomic.Uint64
	stop := make(chan struct{})
	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				var m runtime.MemStats
				runtime.ReadMemStats(&m)
				for old := peakHeap.Load(); m.HeapAlloc > old; old = peakHeap.Load() {
					if peakHeap.CompareAndSwap(old, m.HeapAlloc) {
						break
					}
				}
			}
		}
	}()
	probeDone := make(chan struct{})
	var duringProbe posterLoadProbeResult
	go func() {
		defer close(probeDone)
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				duringProbe.add(probe.sample(1))
			}
		}
	}()
	var wg sync.WaitGroup
	for i := range outcomes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			time.Sleep(time.Until(start.Add(time.Duration(i) * 10 * time.Millisecond)))
			began := time.Now()
			var parallel sync.WaitGroup
			var inviteOK, configOK bool
			parallel.Add(2)
			go func() {
				defer parallel.Done()
				r, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/api/v1/referral/campaigns/%d/invite", server.URL, c.ID), nil)
				r.AddCookie(&http.Cookie{Name: "session", Value: fmt.Sprintf("load-user-%d", i+1)})
				r.AddCookie(&http.Cookie{Name: "csrf", Value: "synthetic-csrf"})
				r.Header.Set("X-CSRF", "synthetic-csrf")
				r.Header.Set("Origin", "https://referral.test")
				r.Header.Set("Idempotency-Key", fmt.Sprintf("poster-load-user-%04d", i))
				v, e := client.Do(r)
				if e == nil {
					io.Copy(io.Discard, v.Body)
					v.Body.Close()
					inviteOK = v.StatusCode == 200
				}
			}()
			go func() {
				defer parallel.Done()
				v, e := client.Get(fmt.Sprintf("%s/api/v1/referral/campaigns/%d", server.URL, c.ID))
				if e == nil {
					io.Copy(io.Discard, v.Body)
					v.Body.Close()
					configOK = v.StatusCode == 200
				}
			}()
			parallel.Wait()
			r := row{}
			if !inviteOK || !configOK {
				r.Error = fmt.Sprintf("invite=%t config=%t", inviteOK, configOK)
			}
			v, e := client.Get(server.URL + published[i%3].ImageURL)
			if e != nil {
				r.Error = e.Error()
			} else {
				r.Status = v.StatusCode
				r.Bytes, _ = io.Copy(io.Discard, v.Body)
				v.Body.Close()
				r.OK = inviteOK && configOK && r.Status == 200
			}
			r.Millis = float64(time.Since(began).Microseconds()) / 1000
			outcomes[i] = r
		}(i)
	}
	wg.Wait()
	elapsed := time.Since(start)
	close(stop)
	<-monitorDone
	<-probeDone
	final := h.pool.Stat()
	latencies := make([]float64, 0, 1000)
	success, busy, errorsCount := 0, 0, 0
	var transferred int64
	failures := map[string]int{}
	for _, r := range outcomes {
		latencies = append(latencies, r.Millis)
		if r.OK {
			success++
		} else if r.Status == 503 && r.Error == "" {
			busy++
		} else {
			errorsCount++
			failures[fmt.Sprintf("%d:%s", r.Status, r.Error)]++
		}
		transferred += r.Bytes
	}
	sort.Float64s(latencies)
	var usage syscall.Rusage
	syscall.Getrusage(syscall.RUSAGE_SELF, &usage)
	rss := usage.Maxrss
	if runtime.GOOS != "darwin" {
		rss *= 1024
	}
	report := map[string]any{"scope": "isolated PostgreSQL + real Referral HTTP: 1000 trusted test actors, parallel invitation/config reads, 3 cold slots, simultaneous real Order Get and Access session/username reads; no OAuth, phone encoding or production network", "os": runtime.GOOS, "gomaxprocs": runtime.GOMAXPROCS(0), "arrival_window_seconds": 10, "elapsed_seconds": elapsed.Seconds(), "users": 1000, "success": success, "busy": busy, "other_failure": errorsCount, "failure_classes": failures, "p95_ms": latencies[949], "max_ms": latencies[999], "image_body_bytes": transferred, "average_image_mbps": float64(transferred) * 8 / elapsed.Seconds() / 1e6, "peak_go_heap_bytes": peakHeap.Load(), "process_peak_rss_bytes": rss, "snapshot_reads": reads.snapshots.Load() - snapshotStart, "database_acquire_count": final.AcquireCount() - initial.AcquireCount(), "database_acquire_wait_ms": float64((final.AcquireDuration() - initial.AcquireDuration()).Microseconds()) / 1000, "database_empty_acquire_count": final.EmptyAcquireCount() - initial.EmptyAcquireCount(), "database_canceled_acquires": final.CanceledAcquireCount() - initial.CanceledAcquireCount(), "database_max_connections": final.MaxConns(), "source_sha256": fmt.Sprintf("%x", sha256.Sum256(source)), "unverified": []string{"installed application order/login journeys (local store reads covered)", "Linux 2 CPU 2GB and actual outbound bandwidth", "WeChat iOS/Android save and scan"}}
	report["baseline_read_probes"] = baselineProbe.summary()
	report["during_read_probes"] = duringProbe.summary()
	if duringProbe.Errors != 0 {
		t.Errorf("order/login read errors: %d", duringProbe.Errors)
	}
	encoded, _ := json.MarshalIndent(report, "", "  ")
	t.Log(string(encoded))
	if path := os.Getenv("AICRM_POSTER_LOAD_REPORT"); path != "" {
		if e := os.WriteFile(path, encoded, 0600); e != nil {
			t.Fatal(e)
		}
	}
	var frozen []byte
	if e = h.pool.QueryRow(ctx, "SELECT image_bytes FROM referral_campaign_posters WHERE campaign_id=$1 AND slot=1", c.ID).Scan(&frozen); e != nil || sha256.Sum256(frozen) != sha256.Sum256(source) {
		t.Fatal("source changed")
	}
	if errorsCount != 0 || success+busy != 1000 || reads.snapshots.Load()-snapshotStart != 3 {
		t.Fatalf("unexpected results: success=%d busy=%d errors=%d reads=%d", success, busy, errorsCount, reads.snapshots.Load()-snapshotStart)
	}
}

type posterLoadImage struct {
	content []byte
}

func (s *posterLoadImage) ReadEnabledPosterImage(context.Context, int64) (referralport.CampaignPosterImage, error) {
	return referralport.CampaignPosterImage{Content: s.content, MediaType: "image/png"}, nil
}

type posterLoadJobs struct{}

func (posterLoadJobs) EnqueueCampaignCloseWithin(context.Context, int64, time.Time) error { return nil }

type posterLoadCustomer struct{}

func (posterLoadCustomer) VerifyCanonicalCustomer(context.Context, int64) (bool, error) {
	return true, nil
}
func posterLoadActor(id int64, at time.Time) referralport.TrustedSessionActor {
	return referralport.TrustedSessionActor{CustomerID: id, IdentityID: id + 10000, AppID: "wx-referral-test", AppScope: "wechat-app:wx-referral-test", Channel: "h5_official_account", OccurredAt: at}
}

type posterLoadHarness struct {
	pool       *pgxpool.Pool
	uow        platformport.UnitOfWork
	repository *referralstore.Repository
	service    *referralapp.Service
	admin      *referralapp.AdminService
	clock      time.Time
}

func newPosterLoadHarness(t *testing.T) *posterLoadHarness {
	t.Helper()
	raw := os.Getenv("AICRM_DATABASE_URL")
	cfg, e := pgxpool.ParseConfig(raw)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.HasPrefix(cfg.ConnConfig.Database, "aicrm_poster_perf_") || (cfg.ConnConfig.Host != "/tmp" && cfg.ConnConfig.Host != "127.0.0.1" && cfg.ConnConfig.Host != "localhost") {
		t.Fatal("load test requires an isolated local aicrm_poster_perf_ database")
	}
	ctx := context.Background()
	admin, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	schema := "poster_load_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	name := pgx.Identifier{schema}.Sanitize()
	if _, e = admin.Exec(ctx, "CREATE SCHEMA "+name); e != nil {
		t.Fatal(e)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.MaxConns = 10
	pool, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	cleanup := func() {
		pool.Close()
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		admin.Exec(c, "DROP SCHEMA "+name+" CASCADE")
		admin.Close()
	}
	t.Cleanup(cleanup)
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..")
	for _, migration := range []string{"0001_platform.sql", "0185_referral_core.sql", "0198_referral_activity_config.sql", "0199_referral_individual_invitation.sql", "0210_referral_campaign_posters.sql", "0222_referral_product_activity_context_receipt.sql", "0229_referral_poster_share_text.sql", "0231_referral_poster_layout.sql", "0232_referral_invitation_verified_issuer.sql", "0003_access.sql", "0027_admin_access_login_compat.sql", "0151_access_role_governance.sql", "0152_access_login_grants.sql", "0020_order.sql", "0024_order_product_version.sql"} {
		body, e := os.ReadFile(filepath.Join(root, "migrations", migration))
		if e != nil {
			t.Fatal(e)
		}
		if _, e = pool.Exec(ctx, string(body)); e != nil {
			t.Fatalf("%s: %v", migration, e)
		}
	}
	if _, e = pool.Exec(ctx, "CREATE TABLE outbox_events(id bigint generated always as identity primary key,aggregate_type text,aggregate_id text,event_type text,event_version smallint,idempotency_key text unique,payload_json jsonb,occurred_at timestamptz,processed_at timestamptz)"); e != nil {
		t.Fatal(e)
	}
	wrapped, e := platformpostgres.Wrap(pool, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	uow, e := platformpostgres.NewUnitOfWork(wrapped)
	if e != nil {
		t.Fatal(e)
	}
	repo, e := referralstore.NewPostgreSQL(pool, uow)
	if e != nil {
		t.Fatal(e)
	}
	audit, _ := platformaudit.NewService(platformaudit.NewPostgreSQLStore())
	service, e := referralapp.NewService(uow, repo, "https://referral.test", base64.RawStdEncoding.EncodeToString(make([]byte, 32)), posterLoadJobs{}, audit, platformoutbox.NewPostgreSQL())
	if e != nil {
		t.Fatal(e)
	}
	administration, e := referralapp.NewAdminService(uow, repo, posterLoadCustomer{}, posterLoadJobs{}, audit, platformoutbox.NewPostgreSQL())
	if e != nil {
		t.Fatal(e)
	}
	// Cleanup is registered once by t.Cleanup.
	return &posterLoadHarness{pool: pool, uow: uow, repository: repo, service: service, admin: administration, clock: time.Now().UTC()}
}

type posterLoadProbes struct {
	h      *posterLoadHarness
	order  *orderstore.Repository
	access *accessstore.PostgreSQL
	digest [32]byte
}
type posterLoadProbeResult struct {
	Times  []float64
	Errors int
}

func (r *posterLoadProbeResult) add(v posterLoadProbeResult) {
	r.Times = append(r.Times, v.Times...)
	r.Errors += v.Errors
}
func (r posterLoadProbeResult) summary() map[string]any {
	sort.Float64s(r.Times)
	p95 := 0.0
	if len(r.Times) > 0 {
		p95 = r.Times[(len(r.Times)-1)*95/100]
	}
	return map[string]any{"samples": len(r.Times), "errors": r.Errors, "p95_ms": p95, "scope": "one transaction with real Order.Get, Access.SessionByTokenDigest, Access.UserByUsername on synthetic fixtures in same pool"}
}
func newPosterLoadProbes(t *testing.T, h *posterLoadHarness) *posterLoadProbes {
	t.Helper()
	ctx := context.Background()
	p := &posterLoadProbes{h: h, access: accessstore.NewPostgreSQL(), digest: sha256.Sum256([]byte("synthetic-load-session"))}
	p.order, _ = orderstore.NewPostgreSQL(h.pool, h.uow)
	_, e := h.pool.Exec(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,record_origin,effect_eligible,created_at,updated_at) VALUES('wechat_pay','aicrm-v4-test','load','M-load',1,1,100,'CNY','paid','native',true,NOW(),NOW());INSERT INTO order_items(order_id,line_no,product_code,product_name,unit_amount_minor,quantity,line_amount_minor) VALUES(1,1,'probe','Probe',100,1,100);INSERT INTO admin_users(username,password_hash,display_name,login_enabled,access_granted_at) VALUES('loadprobe','$argon2id$synthetic','Probe',true,NOW());INSERT INTO admin_user_roles(admin_user_id,role_code) VALUES(1,'super_admin');INSERT INTO access_super_admin_control(singleton,admin_user_id) VALUES(true,1);`)
	if e != nil {
		t.Fatal(e)
	}
	_, e = h.pool.Exec(ctx, "INSERT INTO admin_sessions(token_digest,csrf_token_digest,admin_user_id,session_version,expires_at) VALUES($1,$1,1,1,NOW()+interval '1 hour')", p.digest[:])
	if e != nil {
		t.Fatal(e)
	}
	first := p.sample(1)
	if first.Errors != 0 {
		t.Fatal("order/login probe fixture unreadable")
	}
	return p
}
func (p *posterLoadProbes) sample(count int) posterLoadProbeResult {
	r := posterLoadProbeResult{}
	for range count {
		start := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		e := p.h.uow.Within(ctx, func(tx context.Context) error {
			if _, e := p.order.Get(tx, 1, false); e != nil {
				return e
			}
			if _, e := p.access.SessionByTokenDigest(tx, p.digest, false); e != nil {
				return e
			}
			_, e := p.access.UserByUsername(tx, "loadprobe", false)
			return e
		})
		cancel()
		if e != nil {
			r.Errors++
		}
		r.Times = append(r.Times, float64(time.Since(start).Microseconds())/1000)
	}
	return r
}
