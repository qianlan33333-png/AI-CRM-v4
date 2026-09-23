package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	mediaapp "github.com/qianlan33333-png/AI-CRM-v3/internal/media/app"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
)

type materialSecurityStub struct {
	principal        accessdomain.Principal
	authErr, csrfErr error
}

func (s materialSecurityStub) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return s.principal, s.authErr
}
func (s materialSecurityStub) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return s.principal, s.csrfErr
}

type materialSourceStub struct {
	get  func(context.Context, string) (outboundport.MaterialSourceSnapshot, error)
	list func(context.Context, outboundport.MaterialSnapshotPageRequest) (outboundport.MaterialSnapshotPage, error)
}

func (s materialSourceStub) GetSourceSnapshot(ctx context.Context, ref string) (outboundport.MaterialSourceSnapshot, error) {
	return s.get(ctx, ref)
}
func (s materialSourceStub) ListEnabledSourceSnapshots(ctx context.Context, request outboundport.MaterialSnapshotPageRequest) (outboundport.MaterialSnapshotPage, error) {
	return s.list(ctx, request)
}
func (s materialSourceStub) ReadSourceBytes(context.Context, outboundport.MaterialSourceSnapshot) (outboundport.MaterialSourceContent, error) {
	return outboundport.MaterialSourceContent{}, errors.New("not used")
}

type materialStatusStub struct {
	get func(context.Context, outboundport.MaterialSourceSnapshot, string) (outboundport.MaterialResult, error)
}

func (s materialStatusStub) GetMaterialStatus(ctx context.Context, source outboundport.MaterialSourceSnapshot, scope string) (outboundport.MaterialResult, error) {
	return s.get(ctx, source, scope)
}

type materialPreparerStub struct {
	prepare func(context.Context, outboundport.MaterialRequest) (outboundport.MaterialResult, error)
}

func (s materialPreparerStub) Prepare(ctx context.Context, req outboundport.MaterialRequest) (outboundport.MaterialResult, error) {
	return s.prepare(ctx, req)
}
func (s materialPreparerStub) ReadyForSend(context.Context, outboundport.MaterialRequest) (outboundport.MaterialResult, error) {
	return outboundport.MaterialResult{}, errors.New("not used")
}

type materialRefresherStub struct {
	refresh func(context.Context, outboundport.MaterialRefreshCommand) (outboundport.MaterialRefreshRound, error)
	get     func(context.Context, int64) (outboundport.MaterialRefreshRound, error)
	today   func(context.Context) (outboundport.MaterialRefreshRound, bool, error)
}

func (s materialRefresherStub) RefreshAll(ctx context.Context, req outboundport.MaterialRefreshCommand) (outboundport.MaterialRefreshRound, error) {
	return s.refresh(ctx, req)
}
func (s materialRefresherStub) GetRefreshRound(ctx context.Context, id int64) (outboundport.MaterialRefreshRound, error) {
	return s.get(ctx, id)
}
func (s materialRefresherStub) GetTodayRefreshRound(ctx context.Context) (outboundport.MaterialRefreshRound, bool, error) {
	return s.today(ctx)
}

type materialFacadeStub struct{ mediaapp.HTTPFacade }

func newMaterialHandler(t *testing.T, source materialSourceStub, status materialStatusStub, preparer materialPreparerStub, refresher materialRefresherStub, security materialSecurityStub) *Handler {
	t.Helper()
	h, err := NewHandler(materialFacadeStub{}, security)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.BindMaterialPreparation(source, status, preparer, refresher, "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); err != nil {
		t.Fatal(err)
	}
	return h
}
func readySource() outboundport.MaterialSourceSnapshot {
	return outboundport.MaterialSourceSnapshot{SourceRef: "image:1", SourceType: "image", ContentDigest: [32]byte{1}, FileName: "cover.png", MediaType: "image/png", SizeBytes: 1, SnapshotVersion: 1}
}
func adminPrincipal() accessdomain.Principal {
	return accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}
}
func noopSource() materialSourceStub {
	return materialSourceStub{get: func(context.Context, string) (outboundport.MaterialSourceSnapshot, error) { return readySource(), nil }, list: func(context.Context, outboundport.MaterialSnapshotPageRequest) (outboundport.MaterialSnapshotPage, error) {
		return outboundport.MaterialSnapshotPage{}, nil
	}}
}
func noopStatus() materialStatusStub {
	return materialStatusStub{get: func(context.Context, outboundport.MaterialSourceSnapshot, string) (outboundport.MaterialResult, error) {
		return outboundport.MaterialResult{State: "missing", CredentialState: "missing"}, nil
	}}
}
func noopPreparer() materialPreparerStub {
	return materialPreparerStub{prepare: func(context.Context, outboundport.MaterialRequest) (outboundport.MaterialResult, error) {
		return outboundport.MaterialResult{State: "queued"}, nil
	}}
}
func noopRefresher() materialRefresherStub {
	return materialRefresherStub{refresh: func(context.Context, outboundport.MaterialRefreshCommand) (outboundport.MaterialRefreshRound, error) {
		return outboundport.MaterialRefreshRound{ID: 1}, nil
	}, get: func(context.Context, int64) (outboundport.MaterialRefreshRound, error) {
		return outboundport.MaterialRefreshRound{ID: 1}, nil
	}, today: func(context.Context) (outboundport.MaterialRefreshRound, bool, error) {
		return outboundport.MaterialRefreshRound{}, false, nil
	}}
}
func responseCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body.Code
}

func TestMaterialPreparationPrepareProtectsAdminWritesAndMapsErrors(t *testing.T) {
	for _, tc := range []struct {
		name     string
		security materialSecurityStub
		source   materialSourceStub
		preparer materialPreparerStub
		want     int
		code     string
	}{
		{"auth", materialSecurityStub{authErr: errors.New("no")}, noopSource(), noopPreparer(), http.StatusUnauthorized, "UNAUTHORIZED"},
		{"csrf", materialSecurityStub{principal: adminPrincipal(), csrfErr: errors.New("no")}, noopSource(), noopPreparer(), http.StatusForbidden, "FORBIDDEN"},
		{"source_missing", materialSecurityStub{principal: adminPrincipal()}, materialSourceStub{get: func(context.Context, string) (outboundport.MaterialSourceSnapshot, error) {
			return outboundport.MaterialSourceSnapshot{}, mediaport.ErrSourceNotFound
		}, list: noopSource().list}, noopPreparer(), http.StatusNotFound, "NOT_FOUND"},
		{"source_transient", materialSecurityStub{principal: adminPrincipal()}, materialSourceStub{get: func(context.Context, string) (outboundport.MaterialSourceSnapshot, error) {
			return outboundport.MaterialSourceSnapshot{}, errors.New("db")
		}, list: noopSource().list}, noopPreparer(), http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE"},
		{"body_drift", materialSecurityStub{principal: adminPrincipal()}, noopSource(), materialPreparerStub{prepare: func(context.Context, outboundport.MaterialRequest) (outboundport.MaterialResult, error) {
			return outboundport.MaterialResult{}, outboundport.ErrMaterialOperationConflict
		}}, http.StatusConflict, "CONFLICT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newMaterialHandler(t, tc.source, noopStatus(), tc.preparer, noopRefresher(), tc.security)
			r := httptest.NewRequest(http.MethodPost, "/api/admin/media-preparations/image:1/prepare", strings.NewReader(`{"force":true}`))
			r.Header.Set("Idempotency-Key", "key-12345678")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)
			if rec.Code != tc.want {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if got := responseCode(t, rec); got != tc.code {
				t.Fatalf("code=%s", got)
			}
		})
	}
}
func TestMaterialPreparationsListUsesNullForUnknownTimes(t *testing.T) {
	source := noopSource()
	source.list = func(context.Context, outboundport.MaterialSnapshotPageRequest) (outboundport.MaterialSnapshotPage, error) {
		return outboundport.MaterialSnapshotPage{Items: []outboundport.MaterialSourceSnapshot{readySource()}, Done: true}, nil
	}
	h := newMaterialHandler(t, source, noopStatus(), noopPreparer(), noopRefresher(), materialSecurityStub{principal: adminPrincipal()})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/admin/media-preparations", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"provider_created_at", "last_succeeded_at", "expires_at", "next_refresh_at"} {
		if body.Items[0][field] != nil {
			t.Fatalf("%s=%v, want null", field, body.Items[0][field])
		}
	}
	if _, ok := body.Items[0]["provider_created_at"].(time.Time); ok {
		t.Fatal("time unexpectedly bypassed JSON")
	}
	if body.Items[0]["credential_state"] != "missing" || body.Items[0]["credential_usable"] != false {
		t.Fatalf("credential projection=%v", body.Items[0])
	}
}

func TestMaterialPreparationsListProjectsUnscannableSources(t *testing.T) {
	source := noopSource()
	source.list = func(context.Context, outboundport.MaterialSnapshotPageRequest) (outboundport.MaterialSnapshotPage, error) {
		return outboundport.MaterialSnapshotPage{Failures: []outboundport.MaterialSourceFailure{{SourceRef: "attachment:9", FailureCode: "source_bytes_missing"}}, Done: true}, nil
	}
	h := newMaterialHandler(t, source, noopStatus(), noopPreparer(), noopRefresher(), materialSecurityStub{principal: adminPrincipal()})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/admin/media-preparations", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items    []map[string]any `json:"items"`
		Failures []map[string]any `json:"failures"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 0 || len(body.Failures) != 1 {
		t.Fatalf("items=%v failures=%v", body.Items, body.Failures)
	}
	failure := body.Failures[0]
	if failure["source_ref"] != "attachment:9" || failure["failure_code"] != "source_bytes_missing" || failure["credential_state"] != "missing" || failure["credential_usable"] != false {
		t.Fatalf("failure projection=%v", failure)
	}
	for _, prohibited := range []string{"content_digest", "file_name", "media_type", "media_id"} {
		if _, exists := failure[prohibited]; exists {
			t.Fatalf("failure guessed %s: %v", prohibited, failure)
		}
	}
}

func TestMaterialProjectionSeparatesLatestFailureFromUsableCredential(t *testing.T) {
	source := readySource()
	ready := materialProjection(source, outboundport.MaterialResult{State: "final_failed", FailureCode: "not_supported", CredentialState: "ready", CredentialUsable: true})
	if ready["state"] != "final_failed" || ready["credential_state"] != "ready" || ready["credential_usable"] != true {
		t.Fatalf("usable credential projection=%v", ready)
	}
	expired := materialProjection(source, outboundport.MaterialResult{State: "final_failed", CredentialState: "expired", CredentialUsable: false})
	if expired["credential_state"] != "expired" || expired["credential_usable"] != false {
		t.Fatalf("expired credential projection=%v", expired)
	}
}

func TestMaterialPreparationMutationForwardsAdminAndIdempotencyKey(t *testing.T) {
	var prepareRequest outboundport.MaterialRequest
	var refreshRequest outboundport.MaterialRefreshCommand
	preparer := materialPreparerStub{prepare: func(_ context.Context, req outboundport.MaterialRequest) (outboundport.MaterialResult, error) {
		prepareRequest = req
		return outboundport.MaterialResult{State: "queued"}, nil
	}}
	refresher := noopRefresher()
	refresher.refresh = func(_ context.Context, req outboundport.MaterialRefreshCommand) (outboundport.MaterialRefreshRound, error) {
		refreshRequest = req
		return outboundport.MaterialRefreshRound{ID: 9, State: "queued"}, nil
	}
	h := newMaterialHandler(t, noopSource(), noopStatus(), preparer, refresher, materialSecurityStub{principal: adminPrincipal()})
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodPost, "/api/admin/media-preparations/image:1/prepare", strings.NewReader(`{"force":true}`)),
		httptest.NewRequest(http.MethodPost, "/api/admin/media-preparations/refresh-rounds", strings.NewReader(`{"force":true}`)),
	} {
		request.Header.Set("Idempotency-Key", "retry-key-12345678")
		recorder := httptest.NewRecorder()
		h.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusAccepted {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	}
	if prepareRequest.OperationKey != "retry-key-12345678" || prepareRequest.ActorAdminID != 7 || !prepareRequest.ForceRefresh {
		t.Fatalf("prepare request=%+v", prepareRequest)
	}
	if refreshRequest.OperationKey != "retry-key-12345678" || refreshRequest.ActorAdminID != 7 || !refreshRequest.Force {
		t.Fatalf("refresh request=%+v", refreshRequest)
	}
}

func TestMaterialPreparationRejectsNonForcedMutations(t *testing.T) {
	h := newMaterialHandler(t, noopSource(), noopStatus(), noopPreparer(), noopRefresher(), materialSecurityStub{principal: adminPrincipal()})
	for _, path := range []string{"/api/admin/media-preparations/image:1/prepare", "/api/admin/media-preparations/refresh-rounds"} {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"force":false}`))
		r.Header.Set("Idempotency-Key", "force-required-key-123")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusBadRequest || responseCode(t, w) != "MALFORMED_REQUEST" {
			t.Fatalf("path=%s status=%d body=%s", path, w.Code, w.Body.String())
		}
	}
}
