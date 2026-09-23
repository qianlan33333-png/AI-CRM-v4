package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	tagapp "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/app"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/tag/domain"
	tagport "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/port"
)

type handlerUOW struct{}

func (handlerUOW) Within(ctx context.Context, callback func(context.Context) error) error {
	return callback(ctx)
}

type handlerStore struct {
	groups     []domain.Group
	tags       []domain.Tag
	syncStatus tagport.SyncStatus
	operations []tagport.ArchiveMutationOperation
}

func (store handlerStore) ListGroups(context.Context) ([]domain.Group, error) {
	return store.groups, nil
}
func (store handlerStore) ListTags(context.Context) ([]domain.Tag, error) { return store.tags, nil }
func (handlerStore) CreateGroup(context.Context, string) (domain.Group, error) {
	return domain.Group{}, errors.New("not called")
}
func (handlerStore) CreateTag(context.Context, int64, string) (domain.Tag, error) {
	return domain.Tag{}, errors.New("not called")
}
func (handlerStore) UpdateGroup(context.Context, int64, string) (domain.Group, error) {
	return domain.Group{}, errors.New("not called")
}
func (handlerStore) ArchiveGroup(context.Context, int64) (domain.Group, error) {
	return domain.Group{}, errors.New("not called")
}
func (handlerStore) UpdateTag(context.Context, int64, string) (domain.Tag, error) {
	return domain.Tag{}, errors.New("not called")
}
func (handlerStore) ArchiveTag(context.Context, int64) (domain.Tag, error) {
	return domain.Tag{}, errors.New("not called")
}
func (store handlerStore) GetGroup(_ context.Context, id int64) (domain.Group, error) {
	for _, item := range store.groups {
		if item.ID == id {
			return item, nil
		}
	}
	return domain.Group{}, tagapp.ErrNotFound
}
func (store handlerStore) GetTag(_ context.Context, id int64) (domain.Tag, error) {
	for _, item := range store.tags {
		if item.ID == id {
			return item, nil
		}
	}
	return domain.Tag{}, tagapp.ErrNotFound
}
func (handlerStore) ReorderGroups(context.Context, []int64) ([]domain.Group, error) {
	return nil, errors.New("not called")
}
func (handlerStore) ReorderTags(context.Context, []int64) ([]domain.Tag, error) {
	return nil, errors.New("not called")
}
func (store handlerStore) LatestSync(context.Context) (tagport.SyncStatus, error) {
	return store.syncStatus, nil
}
func (store handlerStore) ListArchiveMutationOperations(context.Context, int) ([]tagport.ArchiveMutationOperation, error) {
	return append([]tagport.ArchiveMutationOperation(nil), store.operations...), nil
}
func (handlerStore) ReserveSync(context.Context, tagport.SyncCommand) (tagport.SyncReceipt, error) {
	return tagport.SyncReceipt{}, errors.New("not called")
}
func (handlerStore) AcceptSync(context.Context, int64, int64, tagport.SyncEffectReceipt) (tagport.SyncReceipt, error) {
	return tagport.SyncReceipt{}, errors.New("not called")
}

type handlerSecurity struct{}

func (handlerSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{InternalID: 7, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}, nil
}
func (handlerSecurity) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{InternalID: 7, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}, nil
}

type handlerGate struct{}

func (handlerGate) Get(context.Context) (domain.ExecutionGate, error) {
	return domain.ExecutionGate{LocalCommandAcceptanceAvailable: true, LocalQueueAvailable: true, ObservedAt: time.Now().UTC()}, nil
}

type handlerSyncEvents struct{}

func (handlerSyncEvents) Append(context.Context, tagport.Event) (int64, error) { return 1, nil }

type handlerSyncEnqueuer struct{}

func (handlerSyncEnqueuer) EnqueueSync(context.Context, tagport.SyncJob) (tagport.SyncEffectReceipt, error) {
	return tagport.SyncEffectReceipt{QueueJobID: 1, EffectID: 1, EffectRef: "eer_1", EffectState: "queued", AcceptReceiptID: "accept", QueueReceiptID: "queue"}, nil
}

var _ platformport.UnitOfWork = handlerUOW{}
var _ tagport.CatalogStore = handlerStore{}
var _ tagport.ArchiveMutationOperationReader = handlerStore{}
var _ tagport.ExecutionGateReader = handlerGate{}

func newHandlerForContractTest(t *testing.T) *Handler {
	t.Helper()
	store := handlerStore{groups: []domain.Group{{ID: 11, Name: "Lifecycle", SortOrder: 0}}, tags: []domain.Tag{{ID: 21, GroupID: 11, GroupName: "Lifecycle", Name: "Warm", SortOrder: 0}}}
	handler, err := NewHandler(tagapp.NewService(handlerUOW{}, store, nil, nil, nil), &tagapp.SyncService{}, handlerGate{}, handlerSecurity{})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestIdempotencyKeyCompatibilityBoundary(t *testing.T) {
	valid := "catalog-key-0001"
	request := httptest.NewRequest("POST", "/api/admin/wecom/tags", nil)
	request.Header.Set("Idempotency-Key", valid)
	if got, err := idempotencyKey(request, valid); err != nil || got != valid {
		t.Fatalf("matching body/header = %q, %v", got, err)
	}
	for name, test := range map[string]struct {
		body    string
		headers []string
	}{
		"mismatch":          {body: valid, headers: []string{"catalog-key-0002"}},
		"body whitespace":   {body: " " + valid, headers: nil},
		"header whitespace": {body: "", headers: []string{" " + valid}},
		"duplicate header":  {body: "", headers: []string{valid, valid}},
	} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/api/admin/wecom/tags", nil)
			for _, header := range test.headers {
				r.Header.Add("Idempotency-Key", header)
			}
			if _, err := idempotencyKey(r, test.body); err == nil {
				t.Fatal("idempotencyKey() error = nil")
			}
		})
	}
	generated, err := idempotencyKey(httptest.NewRequest("POST", "/api/admin/wecom/tags", nil), "")
	if err != nil || !strings.HasPrefix(generated, "server_compat_") {
		t.Fatalf("generated compatibility key = %q, %v", generated, err)
	}
}

func TestOpaqueRequestIDIsSchemaSafe(t *testing.T) {
	if got := opaqueRequestID(); !strings.HasPrefix(got, "tagreq_") || len(got) < 8 {
		t.Fatalf("opaqueRequestID() = %q", got)
	}
}

func TestMutationEnvelopeReportsOnlyPersistedProviderState(t *testing.T) {
	local := mutationEnvelope("tag_reordered", false, "")
	if local["source_status"] != "local_catalog" {
		t.Fatalf("local envelope = %#v", local)
	}
	if _, exists := local["effect_state"]; exists {
		t.Fatalf("local envelope claims effect state: %#v", local)
	}
	unknown := mutationEnvelope("tag_archived", false, "outcome_unknown")
	if unknown["source_status"] != "provider_write_outcome_unknown" || unknown["effect_state"] != "outcome_unknown" {
		t.Fatalf("unknown envelope = %#v", unknown)
	}
	validated := mutationEnvelope("tag_created", true, "queued")
	if validated["source_status"] != "validation_only" {
		t.Fatalf("validated envelope = %#v", validated)
	}
	if got := matchingProviderMutationState("queued", "outcome_unknown"); got != "" {
		t.Fatalf("mismatched group-create state = %q", got)
	}
}

func TestTagsCatalogUsesFrozenAliasesAndNestedGroups(t *testing.T) {
	recorder := httptest.NewRecorder()
	newHandlerForContractTest(t).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/admin/wecom/tags", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"fallback_used", "sync_executed", "fixture_used"} {
		if value, ok := body[field].(bool); !ok || value {
			t.Fatalf("%s = %#v", field, body[field])
		}
	}
	tag := body["tags"].([]any)[0].(map[string]any)
	if tag["id"] != float64(21) || tag["name"] != "Warm" || tag["tag_name"] != "Warm" {
		t.Fatalf("tag aliases = %#v", tag)
	}
	group := body["groups"].([]any)[0].(map[string]any)
	if group["name"] != "Lifecycle" || len(group["tags"].([]any)) != 1 {
		t.Fatalf("group nested tags = %#v", group)
	}
}

func TestTagsCatalogExposesOnlyRecordedProviderWriteStateAndReadbackTime(t *testing.T) {
	readback := time.Date(2026, 9, 8, 9, 0, 0, 0, time.UTC)
	store := handlerStore{
		groups: []domain.Group{{ID: 11, Name: "Lifecycle", SortOrder: 0, ProviderMutationState: "executed", ProviderReadbackAt: &readback}},
		tags:   []domain.Tag{{ID: 21, GroupID: 11, GroupName: "Lifecycle", Name: "Warm", SortOrder: 0, ProviderMutationState: "outcome_unknown"}},
	}
	handler, err := NewHandler(tagapp.NewService(handlerUOW{}, store, nil, nil, nil), &tagapp.SyncService{}, handlerGate{}, handlerSecurity{})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/admin/wecom/tags", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var body map[string]any
	if err = json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	group := body["groups"].([]any)[0].(map[string]any)
	tag := body["tags"].([]any)[0].(map[string]any)
	if group["provider_write_state"] != "executed" || group["synced_at"] != readback.Format(time.RFC3339) || tag["provider_write_state"] != "outcome_unknown" || tag["synced_at"] != nil {
		t.Fatalf("group=%#v tag=%#v", group, tag)
	}
}

func TestTagsCatalogRetainsArchiveOutcomeRecordAfterRowIsHidden(t *testing.T) {
	recordedAt := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	store := handlerStore{groups: []domain.Group{}, tags: []domain.Tag{}, operations: []tagport.ArchiveMutationOperation{{Operation: tagport.CatalogTagArchive, LocalID: 21, State: "outcome_unknown", RecordedAt: recordedAt}}}
	handler, err := NewHandler(tagapp.NewService(handlerUOW{}, store, nil, nil, nil), &tagapp.SyncService{}, handlerGate{}, handlerSecurity{})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/admin/wecom/tags", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var body map[string]any
	if err = json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	operations := body["archive_operations"].([]any)
	if body["archive_operations_status"] != "ready" || len(operations) != 1 {
		t.Fatalf("archive operations = %#v", body)
	}
	operation := operations[0].(map[string]any)
	if operation["operation"] != "tag_archive" || operation["local_id"] != float64(21) || operation["state"] != "outcome_unknown" || operation["recorded_at"] != recordedAt.Format(time.RFC3339) {
		t.Fatalf("archive operation = %#v", operation)
	}
}

func TestTagSyncStatusReturnsDurableProjection(t *testing.T) {
	store := handlerStore{syncStatus: tagport.SyncStatus{ReceiptID: 41, EffectID: "eer_91", State: tagport.SyncExecuted, GroupCount: 36, TagCount: 125}}
	handler, err := NewHandler(tagapp.NewService(handlerUOW{}, store, nil, nil, nil), tagapp.NewSyncService(handlerUOW{}, store, handlerSyncEvents{}, handlerSyncEnqueuer{}), handlerGate{}, handlerSecurity{})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/admin/wecom/tags/sync-status", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Sync tagport.SyncStatus `json:"sync"`
	}
	if err = json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Sync.ReceiptID != 41 || body.Sync.State != tagport.SyncExecuted || body.Sync.GroupCount != 36 || body.Sync.TagCount != 125 || body.Sync.Active {
		t.Fatalf("sync status = %#v", body.Sync)
	}
}

func TestTagSyncInProgressHasStableConflictCode(t *testing.T) {
	recorder := httptest.NewRecorder()
	resultError(recorder, tagport.ErrSyncInProgress)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), `"detail":"sync_in_progress"`) || !strings.Contains(recorder.Body.String(), `"error_code":"sync_in_progress"`) {
		t.Fatalf("sync conflict = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestTagProviderWriteGateHasStableUnavailableCode(t *testing.T) {
	recorder := httptest.NewRecorder()
	resultError(recorder, errors.Join(tagapp.ErrUnavailable, tagapp.ErrProviderMutationUnavailable))
	if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), `"detail":"tag_catalog_write_unavailable"`) || !strings.Contains(recorder.Body.String(), `"error":"tag_catalog_write_unavailable"`) {
		t.Fatalf("write gate = %d %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "本次未新增、修改或删除标签") {
		t.Fatal("write gate must explain the unchanged result")
	}
}

func TestTagMutationContractAllowsFrozenMetadataAndKeepsDryRunSideEffectFree(t *testing.T) {
	handler := newHandlerForContractTest(t)
	for _, method := range []string{http.MethodPost, http.MethodPatch} {
		path, payload := "/api/admin/wecom/tags", `{"group_id":11,"group_name":"Lifecycle","tag_name":"Warm","actor":{"spoofed":true},"dry_run":true}`
		if method == http.MethodPatch {
			path, payload = "/api/admin/wecom/tags/21", `{"tag_name":"Warm","actor":123,"dry_run":true}`
		}
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(method, path, strings.NewReader(payload))
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s status = %d body=%s", method, recorder.Code, recorder.Body.String())
		}
		var response map[string]any
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response["dry_run"] != true || !strings.HasSuffix(response["reason"].(string), "_validated") || response["fixture_used"] != false {
			t.Fatalf("%s response = %#v", method, response)
		}
	}

	put := httptest.NewRecorder()
	handler.ServeHTTP(put, httptest.NewRequest(http.MethodPut, "/api/admin/wecom/tags/21", strings.NewReader(`{"tag_name":"Warm","dry_run":true}`)))
	if put.Code != http.StatusOK || put.Body.Len() != 0 {
		t.Fatalf("PUT = %d %q, want 200 void", put.Code, put.Body.String())
	}
}

func TestTagDeleteRejectsMalformedChunkedAndUnknownJSON(t *testing.T) {
	handler := newHandlerForContractTest(t)
	for _, payload := range []string{`{"unknown":true}`, `{`} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodDelete, "/api/admin/wecom/tags/21", io.NopCloser(strings.NewReader(payload)))
		request.ContentLength = -1 // chunked/unknown-length must not bypass decode errors.
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("payload %q status = %d body=%s", payload, recorder.Code, recorder.Body.String())
		}
	}
}
