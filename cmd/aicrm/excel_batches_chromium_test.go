package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	accessapp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/app"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accesshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/http"
	effect "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	config "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Real rendered Host, session/CSRF, database, edits and atomic approval. The
// preparation provider is a deterministic fixture; Python XLSX parsing and
// observation calculation have separate restart/format/window tests.
func TestPostgreSQLExcelBatchesChromiumJourney(t *testing.T) {
	if !config.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1")
	}
	runExcelCompositionJourney(t, true)
}
func TestPostgreSQLExcelHTTPCompositionJourney(t *testing.T) { runExcelCompositionJourney(t, false) }
func runExcelCompositionJourney(t *testing.T, browser bool) {
	t.Helper()
	_, source, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(source), "..", "..")
	t.Chdir(root)
	if browser {
		prepareProductExternalPushChromiumArtifacts(t, root)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	token := strings.Repeat("x", 32)
	cover, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAYAAABytg0kAAAAFElEQVR4nGL6z8DwnwEZAAIAAP//HxcCAa7PZcoAAAAASUVORK5CYII=")
	// Cover uploads now enter Media before the Excel batch freezes its source.
	// Assert the actual immutable PNG digest rather than the old component-only
	// fixture token.
	coverHash := effect.Hash(string(cover))
	card := map[string]any{"appid": "fixture-app", "path": "pages/article/article?lesson_id=1", "title": "标准案例", "cover_digest": ""}
	component := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(401)
			return
		}
		var value any = map[string]any{"ok": true}
		switch {
		case r.URL.Path == "/prepare":
			raw, _ := io.ReadAll(r.Body)
			if string(raw) == "fixture-replace" {
				value = map[string]any{"file_digest": effect.Hash("file-replace"), "rows": []any{map[string]any{"unionid": "browser-user-three", "text": "替换后的第一条", "sender_userid": "staff-three", "card": card, "segment": "C"}, map[string]any{"unionid": "browser-user-four", "text": "替换后的第二条", "sender_userid": "staff-four", "card": card, "segment": ""}}}
			} else if string(raw) == "fixture-drift" {
				value = map[string]any{"file_digest": effect.Hash("file-drift"), "rows": []any{map[string]any{"unionid": "browser-user-one", "text": "同键漂移", "sender_userid": "staff-one", "card": card, "segment": "A"}}}
			} else {
				value = map[string]any{"file_digest": effect.Hash("file"), "rows": []any{map[string]any{"unionid": "browser-user-one", "text": "第一条待审核话术", "sender_userid": "staff-one", "card": card, "segment": "A"}, map[string]any{"unionid": "browser-user-two", "text": "第二条待审核话术", "sender_userid": "staff-two", "card": card, "segment": "B"}}}
			}
		case r.URL.Path == "/covers":
			value = map[string]any{"cover_digest": coverHash}
		case strings.HasPrefix(r.URL.Path, "/covers/"):
			w.Header().Set("Content-Type", "image/png")
			w.Write(cover)
			return
		case strings.HasPrefix(r.URL.Path, "/reports/"):
			if strings.HasSuffix(r.URL.Path, ".csv") {
				w.Header().Set("Content-Type", "text/csv; charset=utf-8")
				_, _ = w.Write([]byte("id,delivery_state\n1,delivery_proven\n"))
				return
			}
			value = map[string]any{"pending": true, "updated_at": "2026-09-07T01:02:03.611265Z"}
		}
		_ = json.NewEncoder(w).Encode(value)
	}))
	defer component.Close()
	server := httptest.NewUnstartedServer(http.NotFoundHandler())
	defer server.Close()
	origin := "https://" + server.Listener.Addr().String()
	key := base64.RawStdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	application, err := compose(ctx, config.Runtime{Role: config.RoleAPI, DatabaseURL: databaseURL, PublicOrigin: origin, ReleaseSHA: "excel-browser", WorkerOwner: "excel-browser", WorkerLimit: 1,
		Effects: config.Effects{ProviderEnabled: true}, WeCom: config.WeCom{Enabled: true, CorpID: "fixture-corp", AgentID: "fixture-agent", Secret: "fixture-secret", ContactSecret: "fixture-contact", ContextSigningKey: strings.Repeat("z", 32), APIBase: component.URL, HTTPClient: component.Client()},
		GroupOps: config.GroupOps{WebhookSecret: "excel-fixture-webhook"}, Survey: config.Survey{DataKey: key, IdentityPhoneDataKey: key, OAuthOpenPlatformID: "fixture"},
		AIAssistant: config.AIAssistant{UIEnabled: true, DispatchEnabled: true, ProviderPermission: "private-message-authorized", ExcelBatchURL: component.URL, ExcelBatchToken: token},
		Bootstrap:   config.Bootstrap{Enabled: true, Username: "excel-browser", Password: "excel-browser-password", DisplayName: "Excel Browser"}})
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	if err = application.bootstrap(ctx, config.Bootstrap{Enabled: true, Username: "excel-browser", Password: "excel-browser-password", DisplayName: "Excel Browser"}); err != nil {
		t.Fatal(err)
	}
	session, csrf := adminAccessLogin(t, application.handler, "excel-browser", "excel-browser-password")
	strategy := httptest.NewRequest(http.MethodPost, "/api/admin/operation-cycles/strategies", bytes.NewReader([]byte(`{"strategy_key":"excel.fixture","title":"Excel 固定长期计划","definition":{"schedule":"每周一 09:00","indicator_color":"#2EA121","primary_action":"start_review","stages":[{"key":"retro","label":"复盘","color":"#2EA121","state":"current"}]}}`)))
	strategy.Header.Set("Content-Type", "application/json")
	strategy.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: session})
	strategy.AddCookie(&http.Cookie{Name: accesshttp.CSRFCookieName, Value: csrf})
	strategy.Header.Set("X-CSRF-Token", csrf)
	strategy.Header.Set("Idempotency-Key", "http-composition-strategy")
	strategyResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(strategyResponse, strategy)
	if strategyResponse.Code != http.StatusCreated {
		t.Fatalf("full HTTP strategy: %d %s", strategyResponse.Code, strategyResponse.Body.String())
	}
	request := httptest.NewRequest("POST", "/api/admin/operation-batches/strategies/excel.fixture/imports", bytes.NewReader([]byte("fixture")))
	request.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: session})
	request.AddCookie(&http.Cookie{Name: accesshttp.CSRFCookieName, Value: csrf})
	request.Header.Set("X-CSRF-Token", csrf)
	request.Header.Set("Idempotency-Key", "http-composition-import")
	response := httptest.NewRecorder()
	application.handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("full HTTP import: %d %s", response.Code, response.Body.String())
	}
	var imported struct {
		Batch struct {
			ID      int64 `json:"id"`
			Version int64 `json:"version"`
		} `json:"batch"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &imported); err != nil || imported.Batch.ID < 1 {
		t.Fatal("missing native plan", err)
	}
	detail := authenticatedAdminGet(t, application.handler, session, fmt.Sprintf("/api/admin/operation-batches/%d?limit=1", imported.Batch.ID))
	if detail.Code != 200 {
		t.Fatalf("full HTTP detail: %d %s", detail.Code, detail.Body.String())
	}
	legacy := authenticatedAdminGet(t, application.handler, session, "/api/admin/operation-batches/legacy")
	if legacy.Code != http.StatusOK || !strings.Contains(legacy.Body.String(), `"items":[]`) {
		t.Fatalf("unlinked legacy discovery: %d %s", legacy.Code, legacy.Body.String())
	}
	if !browser {
		anonymous := httptest.NewRecorder()
		application.handler.ServeHTTP(anonymous, httptest.NewRequest(http.MethodGet, "/api/admin/operation-batches/strategy-summaries", nil))
		if anonymous.Code != http.StatusForbidden {
			t.Fatalf("anonymous summary read=%d body=%s", anonymous.Code, anonymous.Body.String())
		}
		if _, err = application.management.AddUser(ctx, accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 1, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}}, accessapp.AddUserInput{
			Username: "excel-summary-viewer", Password: "excel-summary-viewer-password", DisplayName: "Excel Summary Viewer", Roles: []accessdomain.Role{accessdomain.RoleViewer},
		}); err != nil {
			t.Fatal(err)
		}
		viewerSession, _ := adminAccessLogin(t, application.handler, "excel-summary-viewer", "excel-summary-viewer-password")
		if viewer := authenticatedAdminGet(t, application.handler, viewerSession, "/api/admin/operation-batches/strategy-summaries?limit=20&offset=0"); viewer.Code != http.StatusOK {
			t.Fatalf("viewer summary read=%d body=%s", viewer.Code, viewer.Body.String())
		}

		// The operation page is deliberately bounded at 20 strategies. Create
		// enough local-only fixtures to prove that the page after the first 100
		// stays reachable through the composed summary read endpoint.
		for index := 1; index <= 120; index++ {
			key := fmt.Sprintf("excel.page.%03d", index)
			body := fmt.Sprintf(`{"strategy_key":%q,"title":%q,"definition":{"schedule":"每周一 09:00","indicator_color":"#2EA121","primary_action":"start_review","stages":[{"key":"retro","label":"复盘","color":"#2EA121","state":"current"}]}}`, key, fmt.Sprintf("分页长期计划 %03d", index))
			req := httptest.NewRequest(http.MethodPost, "/api/admin/operation-cycles/strategies", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-CSRF-Token", csrf)
			req.Header.Set("Idempotency-Key", fmt.Sprintf("excel-summary-page-%03d", index))
			req.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: session})
			req.AddCookie(&http.Cookie{Name: accesshttp.CSRFCookieName, Value: csrf})
			res := httptest.NewRecorder()
			application.handler.ServeHTTP(res, req)
			if res.Code != http.StatusCreated {
				t.Fatalf("summary page strategy %d: %d %s", index, res.Code, res.Body.String())
			}
		}
		type summaryItem struct {
			StrategyKey       string `json:"strategy_key"`
			LatestBatchStatus string `json:"latest_batch_status"`
			LatestBatch       *struct {
				ID      int64 `json:"id"`
				Summary struct {
					ExpectedTasks int `json:"expected_tasks"`
				} `json:"summary"`
			} `json:"latest_batch"`
		}
		type summaryPage struct {
			Items      []summaryItem `json:"items"`
			Total      int           `json:"total"`
			Limit      int           `json:"limit"`
			Offset     int           `json:"offset"`
			HasMore    bool          `json:"has_more"`
			NextOffset *int          `json:"next_offset"`
		}
		readSummary := func(offset int) summaryPage {
			res := authenticatedAdminGet(t, application.handler, session, fmt.Sprintf("/api/admin/operation-batches/strategy-summaries?limit=20&offset=%d", offset))
			if res.Code != http.StatusOK {
				t.Fatalf("summary page offset=%d: %d %s", offset, res.Code, res.Body.String())
			}
			var page summaryPage
			if err = json.Unmarshal(res.Body.Bytes(), &page); err != nil {
				t.Fatal(err)
			}
			return page
		}
		first := readSummary(0)
		if first.Total != 121 || first.Limit != 20 || first.Offset != 0 || len(first.Items) != 20 || !first.HasMore || first.NextOffset == nil || *first.NextOffset != 20 {
			t.Fatalf("first summary page=%+v", first)
		}
		if statuses := first.Items; len(statuses) == 0 || statuses[0].LatestBatchStatus != "ready" || statuses[0].LatestBatch != nil {
			t.Fatalf("unbatched strategy must remain a ready, known absence: %+v", statuses)
		}
		middle := readSummary(100)
		if middle.Total != 121 || len(middle.Items) != 20 || !middle.HasMore || middle.NextOffset == nil || *middle.NextOffset != 120 {
			t.Fatalf("page after first 100=%+v", middle)
		}
		last := readSummary(120)
		if last.Total != 121 || len(last.Items) != 1 || last.HasMore || last.NextOffset != nil || last.Items[0].StrategyKey != "excel.fixture" || last.Items[0].LatestBatchStatus != "ready" || last.Items[0].LatestBatch == nil || last.Items[0].LatestBatch.ID != imported.Batch.ID || last.Items[0].LatestBatch.Summary.ExpectedTasks != 2 {
			t.Fatalf("last summary page=%+v", last)
		}
		if invalid := authenticatedAdminGet(t, application.handler, session, "/api/admin/operation-batches/strategy-summaries?limit=101&offset=0"); invalid.Code != http.StatusBadRequest {
			t.Fatalf("unbounded summary limit: %d %s", invalid.Code, invalid.Body.String())
		}
		for _, path := range []string{
			"/api/admin/operation-batches/strategy-summaries?limit=&offset=0",
			"/api/admin/operation-batches/strategy-summaries?limit=20&offset=",
		} {
			if invalid := authenticatedAdminGet(t, application.handler, session, path); invalid.Code != http.StatusBadRequest {
				t.Fatalf("empty summary page argument path=%s status=%d body=%s", path, invalid.Code, invalid.Body.String())
			}
		}
		// The route must not expose a next offset which it later refuses. Offset
		// 10,020 used to be rejected by an artificial 10,000 cap, even though a
		// full page at 10,000 could return it. It is now a valid, replayable read.
		if beyondFormerOffsetCap := readSummary(10020); beyondFormerOffsetCap.Offset != 10020 || len(beyondFormerOffsetCap.Items) != 0 || beyondFormerOffsetCap.HasMore || beyondFormerOffsetCap.NextOffset != nil {
			t.Fatalf("summary page beyond former offset cap=%+v", beyondFormerOffsetCap)
		}

		write := func(method, path, idempotencyKey string, body []byte) *httptest.ResponseRecorder {
			req := httptest.NewRequest(method, path, bytes.NewReader(body))
			req.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: session})
			req.AddCookie(&http.Cookie{Name: accesshttp.CSRFCookieName, Value: csrf})
			req.Header.Set("X-CSRF-Token", csrf)
			req.Header.Set("Idempotency-Key", idempotencyKey)
			res := httptest.NewRecorder()
			application.handler.ServeHTTP(res, req)
			return res
		}
		if replay := write(http.MethodPost, "/api/admin/operation-batches/strategies/excel.fixture/imports", "http-composition-import", []byte("fixture")); replay.Code != http.StatusOK {
			t.Fatalf("same-key import replay: %d %s", replay.Code, replay.Body.String())
		}
		if drift := write(http.MethodPost, "/api/admin/operation-batches/strategies/excel.fixture/imports", "http-composition-import", []byte("fixture-drift")); drift.Code != http.StatusConflict || !strings.Contains(drift.Body.String(), "idempotency_conflict") {
			t.Fatalf("same-key import drift: %d %s", drift.Code, drift.Body.String())
		}
		if duplicate := write(http.MethodPost, "/api/admin/operation-batches/strategies/excel.fixture/imports", "http-composition-import-duplicate", []byte("fixture")); duplicate.Code != http.StatusConflict || !strings.Contains(duplicate.Body.String(), "duplicate_file") {
			t.Fatalf("duplicate import: %d %s", duplicate.Code, duplicate.Body.String())
		}
		if fresh := write(http.MethodPost, "/api/admin/operation-batches/strategies/excel.fixture/imports?new=1", "http-composition-import-new", []byte("fixture")); fresh.Code != http.StatusCreated {
			t.Fatalf("explicit new import: %d %s", fresh.Code, fresh.Body.String())
		}
		if invalidCover := write(http.MethodPost, fmt.Sprintf("/api/admin/operation-batches/%d/cover?expected_version=%d", imported.Batch.ID, imported.Batch.Version), "http-cover-invalid", []byte("not an image")); invalidCover.Code != http.StatusBadRequest {
			t.Fatalf("invalid cover: %d %s", invalidCover.Code, invalidCover.Body.String())
		}
		coverResponse := write(http.MethodPost, fmt.Sprintf("/api/admin/operation-batches/%d/cover?expected_version=%d", imported.Batch.ID, imported.Batch.Version), "http-cover-upload", cover)
		if coverResponse.Code != http.StatusOK {
			t.Fatalf("full HTTP cover: %d %s", coverResponse.Code, coverResponse.Body.String())
		}
		var covered struct {
			Batch struct {
				Version     int64  `json:"version"`
				CoverDigest string `json:"cover_digest"`
			} `json:"batch"`
		}
		if err = json.Unmarshal(coverResponse.Body.Bytes(), &covered); err != nil || covered.Batch.Version < 2 || covered.Batch.CoverDigest != string(coverHash) {
			t.Fatalf("cover result missing current cover/version: %v %s", err, coverResponse.Body.String())
		}
		var detailBody struct {
			Rows []struct {
				ID      int64 `json:"id"`
				Version int64 `json:"version"`
			} `json:"rows"`
			NextCursor string `json:"next_cursor"`
		}
		if err = json.Unmarshal(detail.Body.Bytes(), &detailBody); err != nil || len(detailBody.Rows) != 1 || detailBody.NextCursor == "" {
			t.Fatalf("first detail page: %v %s", err, detail.Body.String())
		}
		pageTwo := authenticatedAdminGet(t, application.handler, session, fmt.Sprintf("/api/admin/operation-batches/%d?limit=1&cursor=%s", imported.Batch.ID, detailBody.NextCursor))
		if pageTwo.Code != http.StatusOK {
			t.Fatalf("second detail page: %d %s", pageTwo.Code, pageTwo.Body.String())
		}
		// Reload after cover because the first detail was deliberately captured
		// before its version change; row version is a separate CAS value.
		detail = authenticatedAdminGet(t, application.handler, session, fmt.Sprintf("/api/admin/operation-batches/%d?limit=50", imported.Batch.ID))
		if err = json.Unmarshal(detail.Body.Bytes(), &detailBody); err != nil || len(detailBody.Rows) != 2 {
			t.Fatalf("detail after cover: %v %s", err, detail.Body.String())
		}
		genericReview := write(http.MethodPost, fmt.Sprintf("/api/admin/ai-assistant/plans/%d/recipients/%d/review", imported.Batch.ID, detailBody.Rows[0].ID), "http-generic-review-blocked", []byte(fmt.Sprintf(`{"expected_version":%d,"decision":"approved"}`, detailBody.Rows[0].Version)))
		if genericReview.Code != http.StatusConflict {
			t.Fatalf("generic review bypassed controlled Excel command: %d %s", genericReview.Code, genericReview.Body.String())
		}
		patchBody, _ := json.Marshal(map[string]any{"expected_version": detailBody.Rows[0].Version, "text": "人工修改后的话术", "path": "pages/article/article?lesson_id=1", "title": "标准案例", "segment": "D", "excluded": false})
		patched := write(http.MethodPatch, fmt.Sprintf("/api/admin/operation-batches/%d/rows/%d", imported.Batch.ID, detailBody.Rows[0].ID), "http-row-edit", patchBody)
		if patched.Code != http.StatusOK {
			t.Fatalf("controlled row edit: %d %s", patched.Code, patched.Body.String())
		}
		var patchedBody struct {
			Batch struct {
				Version int64 `json:"version"`
			} `json:"batch"`
		}
		if err = json.Unmarshal(patched.Body.Bytes(), &patchedBody); err != nil || patchedBody.Batch.Version < 3 {
			t.Fatalf("row edit version: %v %s", err, patched.Body.String())
		}
		versions := authenticatedAdminGet(t, application.handler, session, fmt.Sprintf("/api/admin/operation-batches/%d/versions", imported.Batch.ID))
		if versions.Code != http.StatusOK || !strings.Contains(versions.Body.String(), `"content_version":1`) {
			t.Fatalf("version list: %d %s", versions.Code, versions.Body.String())
		}
		history := authenticatedAdminGet(t, application.handler, session, fmt.Sprintf("/api/admin/operation-batches/%d/versions/1?limit=50", imported.Batch.ID))
		if history.Code != http.StatusOK || !strings.Contains(history.Body.String(), "人工修改后的话术") || !strings.Contains(history.Body.String(), string(coverHash)) {
			t.Fatalf("historical contents/cover: %d %s", history.Code, history.Body.String())
		}
		edited := authenticatedAdminGet(t, application.handler, session, fmt.Sprintf("/api/admin/operation-batches/%d?limit=50", imported.Batch.ID))
		if edited.Code != http.StatusOK || json.Unmarshal(edited.Body.Bytes(), &detailBody) != nil || len(detailBody.Rows) != 2 {
			t.Fatalf("read row before reverting historical content: %d %s", edited.Code, edited.Body.String())
		}
		revertBody, _ := json.Marshal(map[string]any{"expected_version": detailBody.Rows[0].Version, "text": "第一条待审核话术", "path": "pages/article/article?lesson_id=1", "title": "标准案例", "segment": "D", "excluded": false})
		reverted := write(http.MethodPatch, fmt.Sprintf("/api/admin/operation-batches/%d/rows/%d", imported.Batch.ID, detailBody.Rows[0].ID), "http-row-revert-historical-content", revertBody)
		if reverted.Code != http.StatusOK {
			t.Fatalf("restoring prior immutable content: %d %s", reverted.Code, reverted.Body.String())
		}
		var revertedBody struct {
			Batch struct {
				Version int64 `json:"version"`
			} `json:"batch"`
		}
		if err = json.Unmarshal(reverted.Body.Bytes(), &revertedBody); err != nil || revertedBody.Batch.Version <= patchedBody.Batch.Version {
			t.Fatalf("reverted content version: %v %s", err, reverted.Body.String())
		}
		reappliedCover := write(http.MethodPost, fmt.Sprintf("/api/admin/operation-batches/%d/cover?expected_version=%d", imported.Batch.ID, revertedBody.Batch.Version), "http-cover-reapply-historical-content", cover)
		if reappliedCover.Code != http.StatusOK {
			t.Fatalf("reapplying current cover: %d %s", reappliedCover.Code, reappliedCover.Body.String())
		}
		var reappliedBody struct {
			Batch struct {
				Version     int64  `json:"version"`
				CoverDigest string `json:"cover_digest"`
			} `json:"batch"`
		}
		if err = json.Unmarshal(reappliedCover.Body.Bytes(), &reappliedBody); err != nil || reappliedBody.Batch.Version <= revertedBody.Batch.Version || reappliedBody.Batch.CoverDigest != string(coverHash) {
			t.Fatalf("reapplied cover result: %v %s", err, reappliedCover.Body.String())
		}
		replaced := write(http.MethodPut, fmt.Sprintf("/api/admin/operation-batches/%d/import?expected_version=%d", imported.Batch.ID, reappliedBody.Batch.Version), "http-replace", []byte("fixture-replace"))
		if replaced.Code != http.StatusOK {
			t.Fatalf("replace: %d %s", replaced.Code, replaced.Body.String())
		}
		var replacedBody struct {
			Batch struct {
				ID                    int64  `json:"id"`
				Version               int64  `json:"version"`
				CurrentContentVersion int    `json:"current_content_version"`
				CoverDigest           string `json:"cover_digest"`
			} `json:"batch"`
		}
		if err = json.Unmarshal(replaced.Body.Bytes(), &replacedBody); err != nil || replacedBody.Batch.ID != imported.Batch.ID || replacedBody.Batch.CurrentContentVersion != 2 || replacedBody.Batch.CoverDigest != string(coverHash) {
			t.Fatalf("replacement did not retain batch/cover: %v %s", err, replaced.Body.String())
		}
		versions = authenticatedAdminGet(t, application.handler, session, fmt.Sprintf("/api/admin/operation-batches/%d/versions", imported.Batch.ID))
		if versions.Code != http.StatusOK || !strings.Contains(versions.Body.String(), `"content_version":1`) || !strings.Contains(versions.Body.String(), `"content_version":2`) {
			t.Fatalf("version list after replacement: %d %s", versions.Code, versions.Body.String())
		}
		preview := write(http.MethodPost, fmt.Sprintf("/api/admin/operation-batches/%d/preview-approval", imported.Batch.ID), "http-preview", []byte(fmt.Sprintf(`{"expected_version":%d}`, replacedBody.Batch.Version)))
		if preview.Code != http.StatusOK {
			t.Fatalf("preview: %d %s", preview.Code, preview.Body.String())
		}
		var previewBody struct {
			PreviewDigest effect.Digest `json:"preview_digest"`
		}
		if err = json.Unmarshal(preview.Body.Bytes(), &previewBody); err != nil || !effect.ValidDigest(previewBody.PreviewDigest) {
			t.Fatalf("preview digest: %v %s", err, preview.Body.String())
		}
		approved := write(http.MethodPost, fmt.Sprintf("/api/admin/operation-batches/%d/approve", imported.Batch.ID), "http-approve", []byte(fmt.Sprintf(`{"expected_version":%d,"preview_digest":%q}`, replacedBody.Batch.Version, previewBody.PreviewDigest)))
		if approved.Code != http.StatusOK {
			t.Fatalf("approve: %d %s", approved.Code, approved.Body.String())
		}
		if blocked := write(http.MethodPut, fmt.Sprintf("/api/admin/operation-batches/%d/import?expected_version=%d", imported.Batch.ID, replacedBody.Batch.Version), "http-replace-submitted", []byte("fixture")); blocked.Code != http.StatusConflict {
			t.Fatalf("submitted replacement: %d %s", blocked.Code, blocked.Body.String())
		}
		report := authenticatedAdminGet(t, application.handler, session, fmt.Sprintf("/api/admin/operation-batches/%d/report", imported.Batch.ID))
		csv := authenticatedAdminGet(t, application.handler, session, fmt.Sprintf("/api/admin/operation-batches/%d/report.csv", imported.Batch.ID))
		if report.Code != http.StatusOK || csv.Code != http.StatusOK || !strings.Contains(csv.Body.String(), "delivery_state") {
			t.Fatalf("report/csv: report=%d csv=%d csv_body=%s", report.Code, csv.Code, csv.Body.String())
		}

		// Strategy facts are authoritative for the page. If their own read fails,
		// the endpoint fails as a whole instead of representing unknown strategies.
		if _, err = application.pool.Native().Exec(ctx, `ALTER TABLE operation_cycle_strategies RENAME TO operation_cycle_strategies_summary_failure`); err != nil {
			t.Fatal(err)
		}
		strategyTableRenamed := true
		defer func() {
			if strategyTableRenamed {
				_, _ = application.pool.Native().Exec(context.Background(), `ALTER TABLE operation_cycle_strategies_summary_failure RENAME TO operation_cycle_strategies`)
			}
		}()
		if failedStrategyRead := authenticatedAdminGet(t, application.handler, session, "/api/admin/operation-batches/strategy-summaries?limit=20&offset=0"); failedStrategyRead.Code != http.StatusServiceUnavailable {
			t.Fatalf("strategy summary source failure=%d body=%s", failedStrategyRead.Code, failedStrategyRead.Body.String())
		}
		if _, err = application.pool.Native().Exec(ctx, `ALTER TABLE operation_cycle_strategies_summary_failure RENAME TO operation_cycle_strategies`); err != nil {
			t.Fatal(err)
		}
		strategyTableRenamed = false

		// The aggregate belongs to AI Assistant. A real PostgreSQL failure there
		// must leave the independently-read strategy page available and label its
		// batch facts unavailable instead of silently reporting no batch.
		if _, err = application.pool.Native().Exec(ctx, `ALTER TABLE ai_assistant_excel_imports RENAME TO ai_assistant_excel_imports_summary_failure`); err != nil {
			t.Fatal(err)
		}
		importsTableRenamed := true
		defer func() {
			if importsTableRenamed {
				_, _ = application.pool.Native().Exec(context.Background(), `ALTER TABLE ai_assistant_excel_imports_summary_failure RENAME TO ai_assistant_excel_imports`)
			}
		}()
		degraded := authenticatedAdminGet(t, application.handler, session, "/api/admin/operation-batches/strategy-summaries?limit=20&offset=120")
		if degraded.Code != http.StatusOK || !strings.Contains(degraded.Body.String(), `"strategy_key":"excel.fixture"`) || !strings.Contains(degraded.Body.String(), `"latest_batch_status":"unavailable"`) || strings.Contains(degraded.Body.String(), `"latest_batch_status":"ready"`) {
			t.Fatalf("batch aggregate degradation=%d body=%s", degraded.Code, degraded.Body.String())
		}
		return
	}
	var contentReadGate struct {
		sync.Mutex
		armed bool
	}
	contentReadRelease := make(chan struct{})
	var releaseContentRead sync.Once
	delayedContentPath := ""
	server.Config.Handler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/__fixture__/excel-arm-content-read":
			if request.Method != http.MethodPost {
				http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			batchID, err := strconv.ParseInt(request.URL.Query().Get("batch_id"), 10, 64)
			if err != nil || batchID < 1 {
				http.Error(writer, "invalid batch id", http.StatusBadRequest)
				return
			}
			var exists bool
			if err = application.pool.Native().QueryRow(request.Context(), `SELECT EXISTS(SELECT 1 FROM ai_assistant_excel_imports WHERE plan_id=$1)`, batchID).Scan(&exists); err != nil {
				http.Error(writer, "fixture batch lookup failed", http.StatusInternalServerError)
				return
			}
			if !exists {
				http.Error(writer, "fixture batch not found", http.StatusNotFound)
				return
			}
			contentReadGate.Lock()
			contentReadGate.armed = true
			delayedContentPath = fmt.Sprintf("/api/admin/operation-batches/%d", batchID)
			contentReadGate.Unlock()
			writer.WriteHeader(http.StatusNoContent)
			return
		case "/__fixture__/excel-release-content-read":
			if request.Method != http.MethodPost {
				http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			releaseContentRead.Do(func() { close(contentReadRelease) })
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		contentReadGate.Lock()
		delayContentRead := contentReadGate.armed && request.Method == http.MethodGet && request.URL.Path == delayedContentPath
		if delayContentRead {
			contentReadGate.armed = false
			delayedContentPath = ""
		}
		contentReadGate.Unlock()
		if delayContentRead {
			select {
			case <-contentReadRelease:
			case <-request.Context().Done():
				return
			}
		}
		application.handler.ServeHTTP(writer, request)
	})
	server.StartTLS()
	cmd := exec.CommandContext(ctx, "node", filepath.Join(root, "cmd/aicrm/excel_batches_chromium_journey.mjs"))
	cmd.Env = append(os.Environ(), "AICRM_EXCEL_TEST_URL="+server.URL, "AICRM_EXCEL_TEST_USERNAME=excel-browser", "AICRM_EXCEL_TEST_PASSWORD=excel-browser-password")
	output, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "excel_batches_chromium: PASS") {
		t.Fatalf("browser: %v %s", err, output)
	}
	var queued, excluded int
	err = application.pool.Native().QueryRow(ctx, `SELECT (SELECT count(*) FROM outbound_private_message_intents),(SELECT count(*) FROM ai_assistant_plan_recipients WHERE review_state='rejected')`).Scan(&queued, &excluded)
	if err != nil || queued != 1 || excluded != 1 {
		t.Fatalf("queued=%d excluded=%d err=%v", queued, excluded, err)
	}
}
