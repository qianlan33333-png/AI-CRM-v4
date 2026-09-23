package http

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	mediaapp "github.com/qianlan33333-png/AI-CRM-v3/internal/media/app"
	mediastore "github.com/qianlan33333-png/AI-CRM-v3/internal/media/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type handlerTestSecurity struct{}

func TestAttachmentDownloadRoleMatrix(t *testing.T) {
	for _, test := range []struct {
		name  string
		value accessdomain.Principal
		want  bool
	}{
		{name: "viewer", value: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleViewer}}, want: false},
		{name: "admin", value: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}, want: true},
		{name: "super", value: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}}, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := writeRole(test.value); got != test.want {
				t.Fatalf("writeRole(%+v)=%v want %v", test.value, got, test.want)
			}
		})
	}
}

func (handlerTestSecurity) Authenticate(_ context.Context, request *http.Request) (accessdomain.Principal, error) {
	if request.Header.Get("X-Test-Auth") == "none" {
		return accessdomain.Principal{}, errors.New("unauthorized")
	}
	if request.Header.Get("X-Test-Role") == "viewer" {
		return accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 8, Roles: []accessdomain.Role{accessdomain.RoleViewer}}, nil
	}
	return accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}, nil
}
func (handlerTestSecurity) AuthorizeCSRF(ctx context.Context, request *http.Request) (accessdomain.Principal, error) {
	if request.Header.Get("X-CSRF-Token") != "test-csrf" {
		return accessdomain.Principal{}, accessdomain.ErrCSRFRequired
	}
	return handlerTestSecurity{}.Authenticate(ctx, request)
}

func TestMediaHTTPCompatibilitySecurityAndFrozenWriteContract(t *testing.T) {
	url, urlErr := platformconfig.DatabaseURL()
	if urlErr != nil {
		t.Skip("database URL not configured")
	}
	repository, closeRepository, native := newHTTPIntegrationRepository(t, url)
	defer closeRepository()
	service, err := mediaapp.NewHTTPFacade(repository)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(service, handlerTestSecurity{})
	if err != nil {
		t.Fatal(err)
	}
	serve := func(request *http.Request) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}
	admin := func(request *http.Request) *http.Request {
		request.Header.Set("X-CSRF-Token", "test-csrf")
		return request
	}

	for _, endpoint := range []struct {
		path   string
		fields []string
	}{
		{"/api/admin/image-library", []string{"items", "images"}},
		{"/api/admin/attachment-library", []string{"items", "attachments"}},
		{"/api/admin/miniprogram-library", []string{"items", "miniprograms", "mini_programs"}},
		{"/api/admin/group-invite-library", []string{"items", "group_invites"}},
	} {
		empty := responseJSON(t, serve(httptest.NewRequest(http.MethodGet, endpoint.path, nil).WithContext(context.Background())), http.StatusOK)
		for _, field := range endpoint.fields {
			requireJSONArray(t, empty, field)
		}
	}
	for _, query := range []string{"?enabled_only=TRUE", "?limit=1&limit=2"} {
		got := serve(httptest.NewRequest(http.MethodGet, "/api/admin/image-library"+query, nil))
		payload := responseJSON(t, got, http.StatusUnprocessableEntity)
		if len(payload) != 3 || payload["code"] != "VALIDATION_FAILED" || payload["message"] != "Validation failed." {
			t.Fatalf("image query %q payload=%#v", query, payload)
		}
		if requestID, ok := payload["request_id"].(string); !ok || !strings.HasPrefix(requestID, "media_") {
			t.Fatalf("image query %q request id=%#v", query, payload["request_id"])
		}
	}
	for query, want := range map[string]float64{"?limit=0": 100, "?limit=-1": 1, "?limit=999": 500, "?offset=-1": 100} {
		payload := responseJSON(t, serve(httptest.NewRequest(http.MethodGet, "/api/admin/image-library"+query, nil)), http.StatusOK)
		if payload["limit"] != want {
			t.Fatalf("image query %q limit=%v want=%v", query, payload["limit"], want)
		}
		if query == "?offset=-1" && payload["offset"] != float64(0) {
			t.Fatalf("negative image offset=%v", payload["offset"])
		}
	}
	unauthenticated := httptest.NewRequest(http.MethodGet, "/api/admin/image-library", nil)
	unauthenticated.Header.Set("X-Test-Auth", "none")
	if got := serve(unauthenticated); got.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d", got.Code)
	}
	unauthenticatedWrite := httptest.NewRequest(http.MethodPost, "/api/admin/miniprogram-library", bytes.NewBufferString(`{"name":"unauth","appid":"wx","pagepath":"pages/a","title":"unauth"}`))
	unauthenticatedWrite.Header.Set("X-Test-Auth", "none")
	unauthenticatedWrite.Header.Set("X-CSRF-Token", "test-csrf")
	if got := serve(unauthenticatedWrite); got.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated write must precede csrf/role checks: status=%d", got.Code)
	}
	viewer := httptest.NewRequest(http.MethodPost, "/api/admin/miniprogram-library", bytes.NewBufferString(`{"name":"viewer","appid":"wx1","pagepath":"pages/a","title":"viewer"}`))
	viewer.Header.Set("X-Test-Role", "viewer")
	viewer.Header.Set("X-CSRF-Token", "test-csrf")
	if got := serve(viewer); got.Code != http.StatusForbidden {
		t.Fatalf("viewer write status=%d", got.Code)
	}
	if got := serve(httptest.NewRequest(http.MethodPost, "/api/admin/miniprogram-library", bytes.NewBufferString(`{}`))); got.Code != http.StatusForbidden {
		t.Fatalf("missing csrf status=%d", got.Code)
	}
	badCSRF := httptest.NewRequest(http.MethodPost, "/api/admin/miniprogram-library", bytes.NewBufferString(`{}`))
	badCSRF.Header.Set("X-CSRF-Token", "bad")
	if got := serve(badCSRF); got.Code != http.StatusForbidden {
		t.Fatalf("bad csrf status=%d", got.Code)
	}

	miniBody := `{"name":"compat","appid":"wx123","pagepath":"pages/a","title":"card"}`
	compat := serve(admin(httptest.NewRequest(http.MethodPost, "/api/admin/miniprogram-library", bytes.NewBufferString(miniBody))))
	compatJSON := responseJSON(t, compat, http.StatusOK)
	requireJSONFields(t, compatJSON, "ok", "item", "miniprogram", "item_id", "changed", "thumb_resolve", "local_only", "provider_call_executed", "real_external_call_executed")
	var serverCompatAudit int
	if err = native.QueryRow(context.Background(), `SELECT count(*) FROM media_audit_events WHERE payload->>'idempotency_source'='server_compat'`).Scan(&serverCompatAudit); err != nil || serverCompatAudit != 1 {
		t.Fatalf("server compatibility audit=%d err=%v", serverCompatAudit, err)
	}
	key := "explicit-mini-key-0001"
	firstRequest := admin(httptest.NewRequest(http.MethodPost, "/api/admin/miniprogram-library", bytes.NewBufferString(`{"name":"replay","appid":"wx123","pagepath":"pages/b","title":"card"}`)))
	firstRequest.Header.Set("Idempotency-Key", key)
	first := responseJSON(t, serve(firstRequest), http.StatusOK)
	replayRequest := admin(httptest.NewRequest(http.MethodPost, "/api/admin/miniprogram-library", bytes.NewBufferString(`{"name":"replay","appid":"wx123","pagepath":"pages/b","title":"card"}`)))
	replayRequest.Header.Set("Idempotency-Key", key)
	replay := responseJSON(t, serve(replayRequest), http.StatusOK)
	if first["item_id"] != replay["item_id"] {
		t.Fatalf("replay changed item: first=%v replay=%v", first, replay)
	}
	drift := admin(httptest.NewRequest(http.MethodPost, "/api/admin/miniprogram-library", bytes.NewBufferString(`{"name":"drift","appid":"wx123","pagepath":"pages/b","title":"card"}`)))
	drift.Header.Set("Idempotency-Key", key)
	if got := serve(drift); got.Code != http.StatusConflict {
		t.Fatalf("payload drift status=%d", got.Code)
	}

	imageResponse := responseJSON(t, serve(multipartImageRequest(t, "/api/admin/image-library/upload", "image", "cover.png", httpPNG(t))), http.StatusForbidden)
	_ = imageResponse
	imageRequest := multipartImageRequest(t, "/api/admin/image-library/upload", "image", "cover.png", httpPNG(t))
	imageRequest.Header.Set("X-CSRF-Token", "test-csrf")
	image := responseJSON(t, serve(imageRequest), http.StatusOK)
	requireJSONFields(t, image, "ok", "item", "image", "item_id", "source_status", "storage_adapter_mode")
	imageID := int64(image["item_id"].(float64))
	uploadItem := image["item"].(map[string]any)
	if _, ok := uploadItem["tags"].(string); !ok || image["source_status"] != "local_upload" || image["route_owner"] != "ai_crm_next" || image["storage_adapter_mode"] != "postgresql" {
		t.Fatalf("legacy image upload donor DTO=%#v", image)
	}
	imageDetail := responseJSON(t, serve(httptest.NewRequest(http.MethodGet, "/api/admin/image-library/"+jsonID(imageID)+"?variant=thumb_160", nil)), http.StatusOK)
	imageItem, ok := imageDetail["image"].(map[string]any)
	if !ok || imageItem["source"] != "upload" || imageItem["thumb_media_id_expires_at"] != "" || imageItem["variant_url"] != "/api/admin/image-library/"+jsonID(imageID)+"/variants/thumb_160" {
		t.Fatalf("image detail compatibility=%#v", imageDetail)
	}
	if got := serve(httptest.NewRequest(http.MethodGet, "/api/admin/image-library/"+jsonID(imageID)+"?variant=unknown", nil)); got.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown variant status=%d", got.Code)
	}
	variantResponse := serve(httptest.NewRequest(http.MethodGet, "/api/admin/image-library/"+jsonID(imageID)+"/variants/thumb_160", nil))
	if variantResponse.Code != http.StatusOK || variantResponse.Header().Get("ETag") == "" || variantResponse.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("app variant contract status=%d etag=%q type=%q", variantResponse.Code, variantResponse.Header().Get("ETag"), variantResponse.Header().Get("Content-Type"))
	}
	if got := serve(httptest.NewRequest(http.MethodGet, "/api/admin/image-library/"+jsonID(imageID)+"/variants/nope", nil)); got.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown variant path status=%d", got.Code)
	}
	missingFileName := admin(httptest.NewRequest(http.MethodPost, "/api/admin/image-library", strings.NewReader(`{"data_url":"data:image/png;base64,`+base64.StdEncoding.EncodeToString(httpPNG(t))+`"}`)))
	if got := serve(missingFileName); got.Code != http.StatusBadRequest {
		t.Fatalf("canonical image must require file_name: status=%d", got.Code)
	}
	upperCaseDataURL := admin(httptest.NewRequest(http.MethodPost, "/api/admin/image-library", strings.NewReader(`{"data_url":"data:IMAGE/PNG;base64,`+base64.StdEncoding.EncodeToString(httpPNG(t))+`","file_name":"cover.png"}`)))
	if got := serve(upperCaseDataURL); got.Code != http.StatusBadRequest {
		t.Fatalf("upper-case canonical data URL status=%d", got.Code)
	}
	overBody := admin(httptest.NewRequest(http.MethodPost, "/api/admin/image-library", strings.NewReader(`{"data_url":"data:image/png;base64,`+base64.StdEncoding.EncodeToString(httpPNG(t))+`","file_name":"cover.png","description":"`+strings.Repeat("x", (10<<20)*4/3+(1<<20)+1)+`"}`)))
	if got := serve(overBody); got.Code != http.StatusBadRequest {
		t.Fatalf("canonical image body cap status=%d", got.Code)
	}
	unknownMini := admin(httptest.NewRequest(http.MethodPost, "/api/admin/miniprogram-library", strings.NewReader(`{"name":"bad","appid":"wx","pagepath":"pages/a","title":"bad","thumb_media_id":"client"}`)))
	if got := serve(unknownMini); got.Code != http.StatusBadRequest {
		t.Fatalf("mini client thumb_media_id status=%d", got.Code)
	}

	attachmentRequest := multipartImageRequest(t, "/api/admin/attachment-library/upload", "attachment", "guide.pdf", []byte("%PDF-1.4\n1 0 obj\n<<>>\nendobj\ntrailer\n<<>>\n%%EOF\n"))
	attachmentRequest.Header.Set("X-CSRF-Token", "test-csrf")
	attachment := responseJSON(t, serve(attachmentRequest), http.StatusOK)
	requireJSONFields(t, attachment, "ok", "item", "attachment", "id", "version", "download_url")
	attachmentID := int64(attachment["id"].(float64))
	viewerDownload := httptest.NewRequest(http.MethodGet, "/api/admin/attachment-library/"+jsonID(attachmentID)+"/download", nil)
	viewerDownload.Header.Set("X-Test-Role", "viewer")
	if got := serve(viewerDownload); got.Code != http.StatusForbidden {
		t.Fatalf("viewer attachment download status=%d", got.Code)
	}
	adminDownload := httptest.NewRequest(http.MethodGet, "/api/admin/attachment-library/"+jsonID(attachmentID)+"/download", nil)
	if got := serve(adminDownload); got.Code != http.StatusOK || got.Header().Get("Content-Disposition") == "" {
		t.Fatalf("admin attachment download status=%d disposition=%q", got.Code, got.Header().Get("Content-Disposition"))
	}
	cas := admin(httptest.NewRequest(http.MethodPut, "/api/admin/attachment-library/"+jsonID(attachmentID), bytes.NewBufferString(`{"name":"guide2","expected_version":1}`)))
	cas.Header.Set("Idempotency-Key", "attachment-cas-key-0001")
	if got := serve(cas); got.Code != http.StatusOK {
		t.Fatalf("attachment cas status=%d", got.Code)
	}
	stale := admin(httptest.NewRequest(http.MethodPut, "/api/admin/attachment-library/"+jsonID(attachmentID), bytes.NewBufferString(`{"name":"guide3","expected_version":1}`)))
	stale.Header.Set("Idempotency-Key", "attachment-cas-key-0002")
	if got := serve(stale); got.Code != http.StatusConflict {
		t.Fatalf("attachment stale cas status=%d", got.Code)
	}
	decimalVersion := admin(httptest.NewRequest(http.MethodPut, "/api/admin/attachment-library/"+jsonID(attachmentID), bytes.NewBufferString(`{"name":"guide3","expected_version":1.5}`)))
	if got := serve(decimalVersion); got.Code != http.StatusBadRequest {
		t.Fatalf("fractional attachment version status=%d", got.Code)
	}
	if _, err = native.Exec(context.Background(), `INSERT INTO media_references(material_kind,material_id,owner,reference_digest) VALUES('attachment',$1,'automation.attachment','sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa')`, attachmentID); err != nil {
		t.Fatal(err)
	}
	// This is an opaque future-domain registry fact. No attachment owner reader
	// is installed in PR02, so deletion must fail closed instead of inventing a
	// consumer-specific conflict list.
	if got := serve(admin(httptest.NewRequest(http.MethodDelete, "/api/admin/attachment-library/"+jsonID(attachmentID), nil))); got.Code != http.StatusServiceUnavailable {
		t.Fatalf("opaque attachment reference must fail closed: status=%d", got.Code)
	}

	miniDetail := responseJSON(t, serve(httptest.NewRequest(http.MethodGet, "/api/admin/miniprogram-library/"+jsonID(int64(compatJSON["item_id"].(float64))), nil)), http.StatusOK)
	requireJSONFields(t, miniDetail, "ok", "item", "miniprogram", "local_only", "provider_call_executed", "real_external_call_executed")
	miniNoopID := int64(compatJSON["item_id"].(float64))
	miniNoop := admin(httptest.NewRequest(http.MethodPut, "/api/admin/miniprogram-library/"+jsonID(miniNoopID), strings.NewReader(`{"name":"compat","appid":"wx123","pagepath":"pages/a","title":"card"}`)))
	miniNoop.Header.Set("Idempotency-Key", "mini-noop-replay-key-0001")
	miniNoopResult := responseJSON(t, serve(miniNoop), http.StatusOK)
	if miniNoopResult["changed"] != false || miniNoopResult["thumb_resolve"] != nil {
		t.Fatalf("mini no-op response=%#v", miniNoopResult)
	}
	miniNoopReplay := admin(httptest.NewRequest(http.MethodPut, "/api/admin/miniprogram-library/"+jsonID(miniNoopID), strings.NewReader(`{"name":"compat","appid":"wx123","pagepath":"pages/a","title":"card"}`)))
	miniNoopReplay.Header.Set("Idempotency-Key", "mini-noop-replay-key-0001")
	miniNoopReplayResult := responseJSON(t, serve(miniNoopReplay), http.StatusOK)
	if miniNoopReplayResult["changed"] != false || miniNoopReplayResult["item_id"] != miniNoopResult["item_id"] {
		t.Fatalf("mini no-op replay=%#v", miniNoopReplayResult)
	}
	group := admin(httptest.NewRequest(http.MethodPost, "/api/admin/group-invite-library", bytes.NewBufferString(`{"name":"group","title":"group","join_url":"https://work.weixin.qq.com/gm/a","cover_image_id":`+jsonID(imageID)+`}`)))
	groupResponse := responseJSON(t, serve(group), http.StatusOK)
	requireJSONFields(t, groupResponse, "ok", "item", "group_invite", "item_id", "local_only", "provider_call_executed", "real_external_call_executed")
	groupID := int64(groupResponse["item_id"].(float64))
	decimalCover := admin(httptest.NewRequest(http.MethodPost, "/api/admin/group-invite-library", strings.NewReader(`{"name":"fraction","title":"fraction","join_url":"https://work.weixin.qq.com/gm/a","cover_image_id":`+jsonID(imageID)+`.5}`)))
	if got := serve(decimalCover); got.Code != http.StatusBadRequest {
		t.Fatalf("fractional group cover status=%d", got.Code)
	}
	imageConflict := responseJSON(t, serve(admin(httptest.NewRequest(http.MethodDelete, "/api/admin/image-library/"+jsonID(imageID), nil))), http.StatusConflict)
	if imageConflict["error"] != "image_has_references" {
		t.Fatalf("image conflict=%#v", imageConflict)
	}
	requireJSONFields(t, imageConflict, "references")
	refs := imageConflict["references"].(map[string]any)
	groupRefs, ok := refs["group_invites"].([]any)
	if !ok || len(groupRefs) == 0 || groupRefs[0].(map[string]any)["id"] == nil || refs["campaign_steps"] == nil || refs["import_preflights"] == nil {
		t.Fatalf("image references must be actual registry results: %#v", refs)
	}
	for _, invalidURL := range []string{"https://user@work.weixin.qq.com/gm/a", "https://work.weixin.qq.com/gm/a?q=1", "https://work.weixin.qq.com/gm/a/b"} {
		request := admin(httptest.NewRequest(http.MethodPost, "/api/admin/group-invite-library", strings.NewReader(`{"title":"bad","join_url":"`+invalidURL+`"}`)))
		if got := serve(request); got.Code != http.StatusBadRequest {
			t.Fatalf("invalid group URL %q status=%d", invalidURL, got.Code)
		}
	}
	groupDetail := responseJSON(t, serve(httptest.NewRequest(http.MethodGet, "/api/admin/group-invite-library/"+jsonID(groupID), nil)), http.StatusOK)
	requireJSONFields(t, groupDetail, "ok", "item", "group_invite", "provider_call_executed")
	archive := admin(httptest.NewRequest(http.MethodDelete, "/api/admin/group-invite-library/"+jsonID(groupID), nil))
	archive.Header.Set("Idempotency-Key", "group-archive-key-0001")
	if got := serve(archive); got.Code != http.StatusOK {
		t.Fatalf("group archive status=%d", got.Code)
	}
}

func newHTTPIntegrationRepository(t *testing.T, url string, beforeManagement ...func(*pgxpool.Pool)) (*mediastore.Repository, func(), *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	raw := make([]byte, 6)
	if _, err = rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	schema := "media_http_" + hex.EncodeToString(raw)
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, file, _, _ := runtime.Caller(0)
	sql, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", "0007_media.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
	groupSQL, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", "0180_material_groups.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, string(groupSQL)); err != nil {
		t.Fatal(err)
	}
	for _, seed := range beforeManagement {
		seed(native)
	}
	managementSQL, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", "0182_media_group_management.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, string(managementSQL)); err != nil {
		t.Fatal(err)
	}
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := mediastore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	return repository, func() {
		pool.Close()
		native.Close()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	}, native
}

func multipartImageRequest(t *testing.T, path, field, fileName string, content []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": field, "filename": fileName}))
	if field == "image" {
		header.Set("Content-Type", "image/png")
	} else {
		header.Set("Content-Type", "application/pdf")
	}
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, path, &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}
func httpPNG(t *testing.T) []byte {
	t.Helper()
	value := image.NewRGBA(image.Rect(0, 0, 2, 2))
	value.Set(0, 0, color.RGBA{R: 255, A: 255})
	var out bytes.Buffer
	if err := png.Encode(&out, value); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
func responseJSON(t *testing.T, recorder *httptest.ResponseRecorder, wanted int) map[string]any {
	t.Helper()
	if recorder.Code != wanted {
		t.Fatalf("status=%d body=%s, wanted=%d", recorder.Code, recorder.Body.String(), wanted)
	}
	var value map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &value); err != nil {
		t.Fatalf("json body=%q err=%v", recorder.Body.String(), err)
	}
	return value
}
func requireJSONFields(t *testing.T, value map[string]any, fields ...string) {
	t.Helper()
	for _, field := range fields {
		if _, ok := value[field]; !ok {
			t.Fatalf("missing %q in %#v", field, value)
		}
	}
}

func requireJSONArray(t *testing.T, value map[string]any, field string) {
	t.Helper()
	items, exists := value[field]
	if !exists || items == nil {
		t.Fatalf("%s is null or absent in %#v", field, value)
	}
	if _, ok := items.([]any); !ok {
		t.Fatalf("%s is not a JSON array: %#v", field, items)
	}
}
func jsonID(value int64) string { return strconv.FormatInt(value, 10) }

func TestMaterialGroupsPersistFilterAndRejectStaleWrites(t *testing.T) {
	url, e := platformconfig.DatabaseURL()
	if e != nil {
		t.Skip("database URL not configured")
	}
	repo, cleanup, native := newHTTPIntegrationRepository(t, url)
	defer cleanup()
	service, e := mediaapp.NewHTTPFacade(repo)
	if e != nil {
		t.Fatal(e)
	}
	handler, e := NewHandler(service, handlerTestSecurity{})
	if e != nil {
		t.Fatal(e)
	}
	call := func(method, path, body, key string, csrf bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if csrf {
			r.Header.Set("X-CSRF-Token", "test-csrf")
		}
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	mini := responseJSON(t, call("POST", "/api/admin/miniprogram-library", `{"name":"Group card","appid":"wx-test","pagepath":"pages/a","title":"Group card"}`, "mini-group-create-0001", true), 200)
	miniID := int64(mini["item_id"].(float64))
	attachment, e := repo.CreateAttachment(context.Background(), 1, "group-attachment-create-0001", mediaapp.AttachmentInput{FileName: "test.pdf", Name: "Group PDF", Content: []byte("%PDF-test")})
	if e != nil {
		t.Fatal(e)
	}
	for _, item := range []struct {
		path string
		id   int64
	}{{"miniprogram-library", miniID}, {"attachment-library", attachment["id"].(int64)}} {
		base := "/api/admin/" + item.path
		endpoint := base + "/" + jsonID(item.id) + "/group"
		body := `{"category":"课程","expected_version":1}`
		responseJSON(t, call("PUT", endpoint, body, "group-set-key-"+item.path, false), 403)
		first := responseJSON(t, call("PUT", endpoint, body, "group-set-key-"+item.path, true), 200)
		replay := responseJSON(t, call("PUT", endpoint, body, "group-set-key-"+item.path, true), 200)
		if first["version"] != float64(2) || replay["version"] != first["version"] {
			t.Fatal("group write replay changed version")
		}
		responseJSON(t, call("PUT", endpoint, `{"category":"另组","expected_version":1}`, "group-stale-key-"+item.path, true), 409)
		grouped := responseJSON(t, call("GET", base+"?category=%E8%AF%BE%E7%A8%8B", "", "", false), 200)
		if grouped["total"] != float64(1) || grouped["items"].([]any)[0].(map[string]any)["category"] != "课程" {
			t.Fatal("group filter lost assignment")
		}
		ungrouped := responseJSON(t, call("GET", base+"?category=", "", "", false), 200)
		if ungrouped["total"] != float64(0) {
			t.Fatal("grouped material appeared in ungrouped")
		}
		facets := responseJSON(t, call("GET", base+"/groups", "", "", false), 200)
		if len(facets["items"].([]any)) != 2 {
			t.Fatal("group facets missing")
		}
	}
	var audits int
	if e = native.QueryRow(context.Background(), `SELECT count(*) FROM media_audit_events WHERE event_type='media.group_updated'`).Scan(&audits); e != nil || audits != 2 {
		t.Fatalf("group audit count=%d err=%v", audits, e)
	}
}
