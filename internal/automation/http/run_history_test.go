package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	automationapp "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/app"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
)

type historyUOW struct{}

func (historyUOW) Within(ctx context.Context, f func(context.Context) error) error { return f(ctx) }

type historyAudience struct {
	segmentport.ExecutionConfigurationReader
}
type historySnapshots struct{ segmentport.SnapshotReader }
type historyStore struct {
	automationapp.RuntimeStore
	calls             int
	packageID, cursor int64
	limit             int
}

func (s *historyStore) ListRunPage(_ context.Context, packageID, cursor int64, limit int) (automationapp.RunPage, error) {
	s.calls++
	s.packageID = packageID
	s.cursor = cursor
	s.limit = limit
	return automationapp.RunPage{Total: 205, NextCursor: "17"}, nil
}

func TestRunHistoryQueryScopeValidationAndAuthorization(t *testing.T) {
	store := &historyStore{}
	service, err := automationapp.NewRuntimeService(historyUOW{}, store, historyAudience{}, historySnapshots{}, 100)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewRuntimeHandler(service, testSecurity{p: principal()})
	if err != nil {
		t.Fatal(err)
	}
	read := func(h *RuntimeHandler, query string) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/api/admin/automation-runs"+query, nil))
		return r
	}
	response := read(handler, "?package_id=33&cursor=101&limit=10")
	var body struct {
		Total int64  `json:"total"`
		Next  string `json:"next_cursor"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &body); err != nil || response.Code != 200 || body.Total != 205 || body.Next != "17" || store.packageID != 33 || store.cursor != 101 || store.limit != 10 {
		t.Fatalf("scope or total lost: status=%d body=%s store=%+v", response.Code, response.Body.String(), store)
	}
	read(handler, "?cursor=0")
	if store.packageID != 0 || store.cursor != 0 {
		t.Fatal("global list lost compatibility")
	}
	before := store.calls
	for _, query := range []string{"?package_id=0", "?package_id=-1", "?package_id=abc", "?package_id=", "?package_id=9223372036854775808", "?package_id=33&package_id=34", "?cursor=-1", "?cursor=abc", "?cursor=1&cursor=2"} {
		if r := read(handler, query); r.Code != 400 {
			t.Fatalf("invalid query %s status=%d", query, r.Code)
		}
	}
	unauthorized, _ := NewRuntimeHandler(service, testSecurity{err: errors.New("no session")})
	if r := read(unauthorized, "?package_id=33"); r.Code != 401 {
		t.Fatalf("no-session status=%d", r.Code)
	}
	forbidden, _ := NewRuntimeHandler(service, testSecurity{})
	if r := read(forbidden, "?package_id=33"); r.Code != 403 {
		t.Fatalf("no-role status=%d", r.Code)
	}
	if store.calls != before {
		t.Fatal("invalid or unauthorized query reached store")
	}
}
