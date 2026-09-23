package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	archiveport "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/port"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
)

type archiveUOW struct{}

func (archiveUOW) Within(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

type archiveAuditStub struct{ events []platformaudit.Event }

func (s *archiveAuditStub) Append(_ context.Context, event platformaudit.Event) (platformaudit.Event, error) {
	s.events = append(s.events, event)
	return event, nil
}

type archiveAuthStub struct {
	principal accessdomain.Principal
	err       error
}

func (s archiveAuthStub) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return s.principal, s.err
}

type archiveReaderStub struct {
	items      []archiveport.MessageItem
	queries    []archiveport.CustomerQuery
	media      archiveport.MediaContent
	mediaCalls int
}

func (s *archiveReaderStub) CustomerMessages(_ context.Context, q archiveport.CustomerQuery) (archiveport.CustomerPage, error) {
	s.queries = append(s.queries, q)
	out := s.items
	if len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return archiveport.CustomerPage{Items: out, AsOf: q.Watermark}, nil
}
func (s *archiveReaderStub) CustomerStaff(_ context.Context, _ customerdomain.CustomerID) ([]archiveport.StaffOption, error) {
	return []archiveport.StaffOption{{ID: 9, DisplayName: "Archive Staff"}}, nil
}
func (s *archiveReaderStub) ReadPrivateMedia(_ context.Context, _ customerdomain.CustomerID, _ int64) (archiveport.MediaContent, error) {
	s.mediaCalls++
	return s.media, nil
}
func TestPaginationCursorUsesLastVisibleItem(t *testing.T) {
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	audit := &archiveAuditStub{}
	reader := &archiveReaderStub{items: make([]archiveport.MessageItem, 101)}
	for i := range reader.items {
		reader.items[i] = archiveport.MessageItem{ID: int64(101 - i), OccurredAt: now.Add(-time.Duration(i) * time.Minute)}
	}
	h, err := NewHandler(archiveAuthStub{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 1, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}, reader, audit, archiveUOW{})
	if err != nil {
		t.Fatal(err)
	}
	h.now = func() time.Time { return now }
	response := httptest.NewRecorder()
	h.Routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/message-archive/customers/1?limit=50", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if len(reader.queries) != 1 || reader.queries[0].Limit != 51 {
		t.Fatalf("query=%+v", reader.queries)
	}
	first := reader.queries[0]
	page := archiveport.CustomerPage{Items: reader.items[:51]}
	nextValue := next(first, page).(map[string]any)
	if nextValue["after_id"] != int64(52) {
		t.Fatalf("after_id=%v want 52", nextValue["after_id"])
	}
	if got := visible(first, page); len(got) != 50 || got[49].ID != 52 {
		t.Fatalf("visible tail=%+v", got[len(got)-1])
	}
	// Simulate the strict tuple predicate with that cursor: the withheld 51st
	// item remains the first value of the following page, so 101 records have no gap.
	after := int64(nextValue["after_id"].(int64))
	remaining := []archiveport.MessageItem{}
	for _, item := range reader.items {
		if item.ID < after {
			remaining = append(remaining, item)
		}
	}
	if len(remaining) != 51 || remaining[0].ID != 51 {
		t.Fatalf("remaining=%d first=%d", len(remaining), remaining[0].ID)
	}
}
func TestPaginationHundredOneRecordsHasNoLoss(t *testing.T) {
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	all := make([]archiveport.MessageItem, 101)
	for i := range all {
		all[i] = archiveport.MessageItem{ID: int64(101 - i), OccurredAt: now.Add(-time.Duration(i) * time.Minute)}
	}
	query := archiveport.CustomerQuery{Limit: 51, Watermark: now}
	seen := map[int64]bool{}
	offset := 0
	for {
		end := offset + query.Limit
		if end > len(all) {
			end = len(all)
		}
		page := archiveport.CustomerPage{Items: all[offset:end]}
		for _, item := range visible(query, page) {
			if seen[item.ID] {
				t.Fatalf("duplicate %d", item.ID)
			}
			seen[item.ID] = true
		}
		n := next(query, page)
		if n == nil {
			break
		}
		offset += query.Limit - 1
		if offset >= len(all) {
			break
		}
	}
	if len(seen) != 101 {
		t.Fatalf("seen=%d", len(seen))
	}
}

func TestArchiveRequiresExplicitAdminRoleAndAuditsMedia(t *testing.T) {
	reader := &archiveReaderStub{media: archiveport.MediaContent{Kind: "image", Data: []byte("\x89PNG\r\n\x1a\nprivate")}}
	audit := &archiveAuditStub{}
	denied, err := NewHandler(archiveAuthStub{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 1}}, reader, audit, archiveUOW{})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	denied.Routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/message-archive/customers/1/media/2", nil))
	if response.Code != http.StatusForbidden || reader.mediaCalls != 0 || len(audit.events) != 0 {
		t.Fatalf("denied status=%d reads=%d audits=%d", response.Code, reader.mediaCalls, len(audit.events))
	}
	allowed, err := NewHandler(archiveAuthStub{principal: accessdomain.Principal{Kind: accessdomain.KindStaff, InternalID: 9, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}}}, reader, audit, archiveUOW{})
	if err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	allowed.Routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/message-archive/customers/1/media/2", nil))
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "private, no-store" || response.Header().Get("X-Content-Type-Options") != "nosniff" || response.Header().Get("Content-Type") != "image/png" || reader.mediaCalls != 1 || len(audit.events) != 1 || audit.events[0].Action != "message_archive.read" {
		t.Fatalf("allowed status=%d headers=%v reads=%d audits=%+v", response.Code, response.Header(), reader.mediaCalls, audit.events)
	}
}

func TestArchiveStaffOptionsUseDisplayNamesAndSameReadAuthorization(t *testing.T) {
	reader, audit := &archiveReaderStub{}, &archiveAuditStub{}
	h, err := NewHandler(archiveAuthStub{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 1, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}, reader, audit, archiveUOW{})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	h.Routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/message-archive/customers/1/staff", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"display_name":"Archive Staff"`) || len(audit.events) != 1 {
		t.Fatalf("status=%d body=%s audits=%+v", response.Code, response.Body.String(), audit.events)
	}
}

func TestArchiveMediaRejectsNonRasterBytes(t *testing.T) {
	reader := &archiveReaderStub{media: archiveport.MediaContent{Kind: "image", Data: []byte("<svg onload=alert(1)>")}}
	audit := &archiveAuditStub{}
	h, err := NewHandler(archiveAuthStub{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 1, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}, reader, audit, archiveUOW{})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	h.Routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/message-archive/customers/1/media/2", nil))
	if response.Code != http.StatusBadRequest || len(audit.events) != 0 || response.Header().Get("Content-Type") == "image/svg+xml" {
		t.Fatalf("status=%d headers=%v audits=%d", response.Code, response.Header(), len(audit.events))
	}
}
func TestArchiveFiltersPreserveTimeEmployeeTypeAndDirection(t *testing.T) {
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	reader, audit := &archiveReaderStub{}, &archiveAuditStub{}
	h, err := NewHandler(archiveAuthStub{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 1, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}, reader, audit, archiveUOW{})
	if err != nil {
		t.Fatal(err)
	}
	h.now = func() time.Time { return now }
	request := httptest.NewRequest(http.MethodGet, "/api/admin/message-archive/customers/1?start_at=2026-09-01T00:00:00Z&end_at=2026-09-04T00:00:00Z&staff_user_id=9&message_type=image&direction=staff_to_customer&chat_type=private&q=needle", nil)
	response := httptest.NewRecorder()
	h.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || len(reader.queries) != 1 {
		t.Fatalf("status=%d queries=%+v", response.Code, reader.queries)
	}
	got := reader.queries[0]
	if got.StartAt.Format(time.RFC3339) != "2026-09-01T00:00:00Z" || got.Watermark.Format(time.RFC3339) != "2026-09-04T00:00:00Z" || got.StaffUserID != 9 || got.MessageType != "image" || got.Direction != "staff_to_customer" || got.ChatType != "private" || got.Search != "needle" {
		t.Fatalf("query=%+v", got)
	}
}
