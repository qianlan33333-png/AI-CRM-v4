package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	accesshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/http"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

// OneID: not involved: this journey uses only the authenticated local admin.
// Persistence/external effects: Media snapshots, EER acceptance, refresh-round
// and fake-provider receipts run through the composed PostgreSQL/River Host.
// The fake server observes only fixture bytes and never contacts WeCom.
func TestPostgreSQLMediaRefreshChromiumJourney(t *testing.T) {
	if !platformconfig.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1")
	}
	_, source, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("locate media refresh Chromium script")
	}
	repository := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	t.Chdir(repository)
	prepareMediaRefreshChromiumArtifacts(t, repository)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	databaseURL, cleanup := mediaRefreshBrowserDatabase(t, ctx)
	defer cleanup()

	wecom := newMediaRefreshFakeWeCom(t)
	defer wecom.Close()
	excel := newMediaRefreshExcelComponent(t, ctx)
	defer excel.Close(t)
	server := httptest.NewUnstartedServer(http.NotFoundHandler())
	defer server.Close()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	application, err := compose(ctx, platformconfig.Runtime{
		Role: platformconfig.RoleAPI, DatabaseURL: databaseURL, PublicOrigin: "https://" + server.Listener.Addr().String(),
		ReleaseSHA: "media-refresh-chromium", WorkerOwner: "media-refresh-chromium", WorkerLimit: 1,
		GroupOps:    platformconfig.GroupOps{WebhookSecret: "media-refresh-chromium-webhook"},
		Survey:      platformconfig.Survey{DataKey: base64.RawStdEncoding.EncodeToString(key), IdentityPhoneDataKey: base64.RawStdEncoding.EncodeToString(key), OAuthOpenPlatformID: "fixture"},
		Bootstrap:   platformconfig.Bootstrap{Enabled: true, Username: "media-browser", Password: "media-browser-password", DisplayName: "Media Browser"},
		Effects:     platformconfig.Effects{ProviderEnabled: true},
		WeCom:       platformconfig.WeCom{Enabled: true, CorpID: "fixture-corp", AgentID: "fixture-agent", Secret: "fixture-secret", ContactSecret: "fixture-contact", ContextSigningKey: strings.Repeat("m", 32), APIBase: wecom.URL(), HTTPClient: wecom.Client()},
		AIAssistant: platformconfig.AIAssistant{UIEnabled: true, DispatchEnabled: true, ProviderPermission: "private-message-authorized", ExcelBatchURL: excel.URL, ExcelBatchToken: strings.Repeat("e", 32)},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	bootstrap := platformconfig.Bootstrap{Enabled: true, Username: "media-browser", Password: "media-browser-password", DisplayName: "Media Browser"}
	if err = application.bootstrap(ctx, bootstrap); err != nil {
		t.Fatal(err)
	}
	if err = mediaRefreshSeedStrategy(t, application); err != nil {
		t.Fatal(err)
	}
	missingSourceRef := mediaRefreshSeedHistoricalMissingSource(t, application)

	workerCtx, stopWorker := context.WithCancel(ctx)
	workerDone := make(chan error, 1)
	go func() { workerDone <- application.effectsRuntime.Run(workerCtx) }()
	defer func() {
		stopWorker()
		select {
		case err := <-workerDone:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("effects runtime: %v", err)
			}
		case <-time.After(20 * time.Second):
			t.Error("effects runtime did not stop")
		}
	}()

	// Only the disposable browser fixture delays its own thumbnail bytes. This
	// makes the V3 card's loading layer observable before the browser settles;
	// production Media reads, authorization and writes remain unchanged.
	server.Config.Handler = delayedMediaRefreshThumbnailReads(application.handler, 900*time.Millisecond)
	server.StartTLS()
	artifactDirectory := filepath.Join(os.TempDir(), "aicrm-daily-media-refresh-browser-artifacts")
	if err = os.MkdirAll(artifactDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	screenshot := filepath.Join(artifactDirectory, "media-refresh-chromium.png")
	command := exec.CommandContext(ctx, "node", filepath.Join(filepath.Dir(source), "media_refresh_chromium_journey.mjs"))
	command.Env = append(os.Environ(),
		"AICRM_MEDIA_REFRESH_TEST_URL="+server.URL,
		"AICRM_MEDIA_REFRESH_TEST_USERNAME=media-browser",
		"AICRM_MEDIA_REFRESH_TEST_PASSWORD=media-browser-password",
		"AICRM_MEDIA_REFRESH_SCREENSHOT="+screenshot,
		"AICRM_MEDIA_REFRESH_MISSING_SOURCE_REF="+missingSourceRef,
		"AICRM_MEDIA_REFRESH_XLSX="+excel.Workbook,
	)
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "media_refresh_chromium: PASS") {
		t.Fatalf("Chromium journey err=%v output=%s", err, strings.TrimSpace(string(output)))
	}
	if info, statErr := os.Stat(screenshot); statErr != nil || info.Size() < 100 {
		t.Fatalf("Chromium screenshot missing path=%s size=%d err=%v", screenshot, info.Size(), statErr)
	}

	var intents, drafts, submitted, current, parsedRows int
	if err = application.pool.Native().QueryRow(ctx, `SELECT (SELECT count(*) FROM outbound_private_message_intents),(SELECT count(*) FROM ai_assistant_plans WHERE source_kind='excel_batch' AND state='pending_review'),(SELECT count(*) FROM ai_assistant_plans WHERE source_kind='excel_batch' AND state='submitted'),(SELECT count(*) FROM outbound_material_preparations WHERE is_current AND media_id='fixture-media-2'),(SELECT count(*) FROM ai_assistant_content_versions WHERE content_payload::text LIKE '%真实解析第一条草稿%')`).Scan(&intents, &drafts, &submitted, &current, &parsedRows); err != nil {
		t.Fatal(err)
	}
	if intents != 0 || drafts != 1 || submitted != 0 || current != 1 || parsedRows != 2 {
		t.Fatalf("unapproved real Excel draft messages=%d drafts=%d submitted=%d refreshed-current=%d parsed-rows=%d", intents, drafts, submitted, current, parsedRows)
	}
	if uploads := wecom.Uploads(); uploads < 2 {
		t.Fatalf("manual refresh did not replace prior credential: fake uploads=%d", uploads)
	}
	t.Logf("media refresh Chromium screenshot: %s", screenshot)
}

func delayedMediaRefreshThumbnailReads(next http.Handler, delay time.Duration) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/api/admin/image-library/") && strings.Contains(request.URL.Path, "/variants/") {
			time.Sleep(delay)
		}
		next.ServeHTTP(writer, request)
	})
}

// The release artifact is prepared by the browser build stage.  This fixture
// deliberately reads it in place: rebuilding or replacing web/dist would race
// the other Chromium journeys and would test a different release closure.
func prepareMediaRefreshChromiumArtifacts(t *testing.T, repository string) {
	t.Helper()
	manifest, err := os.ReadFile(filepath.Join(repository, "web", "dist", "asset-manifest.json"))
	if err != nil || !bytes.Contains(manifest, []byte(`"materialSaveHost"`)) || !bytes.Contains(manifest, []byte(`"operationCyclesHost"`)) {
		t.Fatalf("Media refresh Chromium requires the staged release Host artifact: %v", err)
	}
}

func mediaRefreshBrowserDatabase(t *testing.T, ctx context.Context) (string, func()) {
	t.Helper()
	raw, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Fatal("AICRM_DATABASE_URL is required for the browser PostgreSQL journey")
	}
	adminConfig, err := pgxpool.ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, adminConfig)
	if err != nil {
		t.Fatalf("browser PostgreSQL database unavailable: %v", err)
	}
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	schema := "media_refresh_chromium_" + hex.EncodeToString(random[:])
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	config := adminConfig.Copy()
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
		t.Fatalf("dedicated browser database unavailable: %v", err)
	}
	if err = adminAccessMigrateCompositionSchema(ctx, pool); err != nil {
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
		t.Fatal(err)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	return parsed.String(), func() {
		pool.Close()
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
	}
}

func mediaRefreshSeedStrategy(t *testing.T, application *composedApplication) error {
	body := []byte(`{"strategy_key":"media.refresh.fixture","title":"素材刷新 Excel 长期计划","definition":{"schedule":"每周一 09:00","indicator_color":"#2EA121","primary_action":"start_review","stages":[{"key":"review","label":"审核","color":"#2EA121","state":"current"}]}}`)
	session, csrf := adminAccessLogin(t, application.handler, "media-browser", "media-browser-password")
	request := httptest.NewRequest(http.MethodPost, "/api/admin/operation-cycles/strategies", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", csrf)
	request.Header.Set("Idempotency-Key", "media-refresh-browser-strategy")
	request.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: session})
	request.AddCookie(&http.Cookie{Name: accesshttp.CSRFCookieName, Value: csrf})
	response := httptest.NewRecorder()
	application.handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		return fmt.Errorf("create strategy status=%d body=%s", response.Code, response.Body.String())
	}
	return nil
}

// mediaRefreshSeedHistoricalMissingSource creates a legitimate Media record,
// then simulates a legacy blob-loss record in this dedicated throwaway
// database. Current Media writes cannot create this shape because their
// foreign key protects blobs; the fixture proves that the scanner keeps an
// already-corrupt historical source visible for re-upload instead of silently
// dropping it from the administrator's page.
func mediaRefreshSeedHistoricalMissingSource(t *testing.T, application *composedApplication) string {
	t.Helper()
	content, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAYAAABytg0kAAAAFElEQVR4nGL6z8DwnwEZAAIAAP//HxcCAa7PZcoAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	session, csrf := adminAccessLogin(t, application.handler, "media-browser", "media-browser-password")
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="image"; filename="historical-missing.png"`)
	header.Set("Content-Type", "image/png")
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err = writer.WriteField("name", "历史缺失原文件"); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/admin/image-library/upload", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("X-CSRF-Token", csrf)
	request.Header.Set("Idempotency-Key", "media-refresh-browser-historical-missing")
	request.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: session})
	request.AddCookie(&http.Cookie{Name: accesshttp.CSRFCookieName, Value: csrf})
	response := httptest.NewRecorder()
	application.handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("seed historical Media source status=%d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Item struct {
			ID int64 `json:"id"`
		} `json:"item"`
	}
	if err = json.NewDecoder(response.Body).Decode(&payload); err != nil || payload.Item.ID < 1 {
		t.Fatalf("decode seed historical Media source id=%d err=%v", payload.Item.ID, err)
	}
	conn, err := application.pool.Native().Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err = conn.Exec(context.Background(), `SET session_replication_role = replica`); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(context.Background(), `UPDATE media_images SET blob_digest=$1 WHERE id=$2`, "sha256:"+strings.Repeat("0", 64), payload.Item.ID); err != nil {
		_, _ = conn.Exec(context.Background(), `SET session_replication_role = origin`)
		t.Fatal(err)
	}
	if _, err = conn.Exec(context.Background(), `SET session_replication_role = origin`); err != nil {
		t.Fatal(err)
	}
	return "image:" + strconv.FormatInt(payload.Item.ID, 10)
}

type mediaRefreshFakeWeCom struct {
	server  *httptest.Server
	mu      sync.Mutex
	uploads int
}

func newMediaRefreshFakeWeCom(t *testing.T) *mediaRefreshFakeWeCom {
	t.Helper()
	fixture := &mediaRefreshFakeWeCom{}
	fixture.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/gettoken":
			_, _ = w.Write([]byte(`{"errcode":0,"access_token":"fixture-token","expires_in":7200}`))
		case "/cgi-bin/media/upload":
			if r.Method != http.MethodPost || r.URL.Query().Get("access_token") != "fixture-token" {
				t.Errorf("unexpected upload request %s?%s", r.URL.Path, r.URL.RawQuery)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			fixture.mu.Lock()
			fixture.uploads++
			number := fixture.uploads
			fixture.mu.Unlock()
			_, _ = w.Write([]byte(fmt.Sprintf(`{"media_id":"fixture-media-%d","created_at":%d}`, number, time.Now().Unix())))
		default:
			t.Errorf("unexpected fake WeCom path=%s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	return fixture
}
func (f *mediaRefreshFakeWeCom) URL() string          { return f.server.URL }
func (f *mediaRefreshFakeWeCom) Client() *http.Client { return f.server.Client() }
func (f *mediaRefreshFakeWeCom) Close()               { f.server.Close() }
func (f *mediaRefreshFakeWeCom) Uploads() int         { f.mu.Lock(); defer f.mu.Unlock(); return f.uploads }

type mediaRefreshExcelComponent struct {
	URL      string
	Workbook string
	command  *exec.Cmd
	logs     bytes.Buffer
}

// newMediaRefreshExcelComponent runs the released Python parser against an
// isolated SQLite file and feeds Chromium a real six-column XLSX. Only WeCom
// remains a local fake in this journey.
func newMediaRefreshExcelComponent(t *testing.T, ctx context.Context) *mediaRefreshExcelComponent {
	t.Helper()
	directory := t.TempDir()
	workbook := filepath.Join(directory, "media-refresh-real.xlsx")
	generator := exec.CommandContext(ctx, "python3", "-c", `
from openpyxl import Workbook
import sys
book = Workbook(); sheet = book.active
sheet.append(["unionid", "话术", "小程序 path", "发送人 userid", "标题", "分层"])
sheet.append(["media-browser-one", "真实解析第一条草稿", "pages/article/article?lesson_id=101", "staff-one", "真实 Excel 标题一", "A"])
sheet.append(["media-browser-two", "真实解析第二条草稿", "pages/article/article?lesson_id=102", "staff-two", "真实 Excel 标题二", "B"])
book.save(sys.argv[1])
`, workbook)
	if output, err := generator.CombinedOutput(); err != nil {
		t.Fatalf("create real XLSX fixture: %v output=%s", err, strings.TrimSpace(string(output)))
	}
	configPath := filepath.Join(directory, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"appid":"fixture-app","source":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err = listener.Close(); err != nil {
		t.Fatal(err)
	}
	fixture := &mediaRefreshExcelComponent{URL: fmt.Sprintf("http://127.0.0.1:%d", port), Workbook: workbook}
	fixture.command = exec.CommandContext(ctx, "python3", "components/excel-batches/batches.py", "--config", configPath, "--database", filepath.Join(directory, "excel.sqlite"), "--port", strconv.Itoa(port))
	fixture.command.Env = append(os.Environ(), "EXCEL_BATCH_TOKEN="+strings.Repeat("e", 32))
	fixture.command.Stdout, fixture.command.Stderr = &fixture.logs, &fixture.logs
	if err = fixture.command.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		request, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, fixture.URL+"/health", nil)
		if requestErr == nil {
			request.Header.Set("Authorization", "Bearer "+strings.Repeat("e", 32))
			response, responseErr := http.DefaultClient.Do(request)
			if responseErr == nil {
				response.Body.Close()
				if response.StatusCode == http.StatusOK {
					return fixture
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	fixture.Close(t)
	t.Fatalf("real Excel component did not become healthy: %s", strings.TrimSpace(fixture.logs.String()))
	return nil
}

func (f *mediaRefreshExcelComponent) Close(t *testing.T) {
	t.Helper()
	if f == nil || f.command == nil || f.command.Process == nil {
		return
	}
	if f.command.ProcessState == nil || !f.command.ProcessState.Exited() {
		_ = f.command.Process.Kill()
	}
	_ = f.command.Wait()
}
